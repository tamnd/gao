package count

// The identities of one source, sorted, on disk.
//
// The questions S1 has to answer about the corpus are set questions. How much of
// FineWeb2 is already in GlotCC, how many distinct documents the five sources
// come to together, how much of a source is a copy of itself. All of them are
// answered from the identities alone, and none of them can be answered from a
// hash set in memory: HPLT v3 is a couple of hundred million documents on a box
// with 5 GB of RAM, and four sources at once is not something any box in the
// fleet can hold.
//
// Sorted on disk is what makes them answerable anyway. Two sorted files are
// intersected by walking them together, five are unioned by walking all five
// together, and neither needs more memory than the number of files being walked.
// The sort itself is done in runs that fit in memory and merged afterwards,
// which is the usual answer and the right one here.
//
// A key is the first eight bytes of the document's blake3 rather than all
// thirty two. That is a four times smaller file and a four times cheaper merge,
// and it costs a collision rate that rounds to nothing: at four hundred million
// documents the expected number of distinct documents that collide is under
// one in two hundred, so the measurement is a count and not an estimate at any
// precision anybody reports it to.

import (
	"bufio"
	"container/heap"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/tamnd/gao/doc"
)

// KeysExt is the extension a key file carries.
const KeysExt = ".keys"

// keysMagic is the first eight bytes of a key file. A file this shape is
// otherwise indistinguishable from any other run of little endian integers, and
// a merge fed the wrong file would answer rather than fail.
const keysMagic = "gaokeys1"

// keysHeader is the length of the header: the magic, the documents read, and
// the distinct keys kept.
const keysHeader = 8 + 8 + 8

// DefaultRunKeys is how many keys are held in memory before a run is spilled.
//
// Eight million keys is 64 MB, which is a tenth of what the smallest box in the
// fleet has spare while it is also holding a Parquet reader. A source of two
// hundred million documents spills twenty five runs at that size, and twenty
// five open files in a merge is nothing.
const DefaultRunKeys = 8 << 20

// ErrNotKeys is returned when a file being read as keys is not one.
var ErrNotKeys = errors.New("count: not a gao key file")

// Keys is what a key file says about the source it was built from.
type Keys struct {
	// Documents is how many documents were read.
	Documents int64

	// Distinct is how many of them were different from each other. The gap
	// between the two is the source repeating itself, which is worth reporting
	// on its own: a source that is a tenth duplicates is a fact about that
	// source rather than about the overlap with anything else.
	Distinct int64
}

// Duplication is the share of a source that is a repeat of something already in
// it, between 0 and 1.
func (k Keys) Duplication() float64 {
	if k.Documents == 0 {
		return 0
	}
	return 1 - float64(k.Distinct)/float64(k.Documents)
}

// Key is a document identity shortened to what a set needs.
type Key = uint64

// KeyOf shortens a document identity to a key.
//
// Big endian so that the order of keys is the order of the hashes they came
// from, which is not needed for correctness and makes a key file and a listing
// of doc_ids sort the same way when somebody is checking one against the other
// by hand.
func KeyOf(h doc.Hash) Key { return binary.BigEndian.Uint64(h[:8]) }

// Builder collects document identities and writes them out sorted.
//
// It is not safe for concurrent use. A part is read by one goroutine and the
// parts of a source are read one after another, because the cost here is the
// network and not the CPU.
type Builder struct {
	dir  string
	max  int
	buf  []Key
	runs []string
	docs int64
	err  error
}

// NewBuilder returns a builder that spills its runs into dir.
//
// The directory is the caller's and is not cleaned up here, because the runs are
// the expensive part: a build that dies after twenty runs has twenty runs worth
// of reading it does not have to do again, and deleting them on the way out
// would make that impossible.
func NewBuilder(dir string) *Builder {
	return &Builder{dir: dir, max: DefaultRunKeys}
}

// Add takes one document identity.
func (b *Builder) Add(h doc.Hash) error {
	if b.err != nil {
		return b.err
	}
	b.docs++
	b.buf = append(b.buf, KeyOf(h))
	if len(b.buf) >= b.max {
		if err := b.spill(); err != nil {
			b.err = err
			return err
		}
	}
	return nil
}

// Documents is how many identities have been added.
func (b *Builder) Documents() int64 { return b.docs }

// Runs is how many runs have been spilled, which is what a long build reports so
// that somebody watching it can see it moving.
func (b *Builder) Runs() int { return len(b.runs) }

// spill sorts what is in memory and writes it out as a run.
func (b *Builder) spill() error {
	if len(b.buf) == 0 {
		return nil
	}
	slices.Sort(b.buf)
	b.buf = slices.Compact(b.buf)

	path := filepath.Join(b.dir, fmt.Sprintf("run-%05d%s", len(b.runs), KeysExt))
	if err := writeKeys(path, b.docs, b.buf); err != nil {
		return err
	}
	b.runs = append(b.runs, path)
	b.buf = b.buf[:0]
	return nil
}

// Write merges everything added so far into one sorted key file at path.
//
// A build that never had to spill writes straight out of memory, which is the
// case for a small source and for every test.
func (b *Builder) Write(path string) (Keys, error) {
	if b.err != nil {
		return Keys{}, b.err
	}
	if len(b.runs) == 0 {
		slices.Sort(b.buf)
		b.buf = slices.Compact(b.buf)
		if err := writeKeys(path, b.docs, b.buf); err != nil {
			return Keys{}, err
		}
		return Keys{Documents: b.docs, Distinct: int64(len(b.buf))}, nil
	}
	if err := b.spill(); err != nil {
		return Keys{}, err
	}
	return MergeKeys(path, b.docs, b.runs...)
}

// writeKeys writes a sorted deduplicated slice of keys as a key file.
func writeKeys(path string, docs int64, keys []Key) error {
	f, err := os.Create(path) //nolint:gosec // the path is the caller's working directory
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	if err := writeKeysHeader(w, docs, int64(len(keys))); err != nil {
		return closeAfter(f, err)
	}
	var b [8]byte
	for _, k := range keys {
		binary.LittleEndian.PutUint64(b[:], k)
		if _, err := w.Write(b[:]); err != nil {
			return closeAfter(f, err)
		}
	}
	if err := w.Flush(); err != nil {
		return closeAfter(f, err)
	}
	return f.Close()
}

func writeKeysHeader(w io.Writer, docs, distinct int64) error {
	var h [keysHeader]byte
	copy(h[:8], keysMagic)
	binary.LittleEndian.PutUint64(h[8:16], uint64(docs))
	binary.LittleEndian.PutUint64(h[16:24], uint64(distinct))
	_, err := w.Write(h[:])
	return err
}

func closeAfter(f *os.File, err error) error {
	_ = f.Close()
	return err
}

// KeyReader walks a key file in order.
type KeyReader struct {
	f    *os.File
	r    *bufio.Reader
	keys Keys
	name string
}

// OpenKeys opens a key file for reading.
func OpenKeys(path string) (*KeyReader, error) {
	f, err := os.Open(path) //nolint:gosec // the path is the caller's working directory
	if err != nil {
		return nil, err
	}
	r := bufio.NewReaderSize(f, 1<<20)

	var h [keysHeader]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s is %d bytes, which is shorter than a header", ErrNotKeys, path, headerShort(path))
	}
	if string(h[:8]) != keysMagic {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s does not start with %q", ErrNotKeys, path, keysMagic)
	}
	return &KeyReader{
		f:    f,
		r:    r,
		name: path,
		keys: Keys{
			Documents: int64(binary.LittleEndian.Uint64(h[8:16])),
			Distinct:  int64(binary.LittleEndian.Uint64(h[16:24])),
		},
	}, nil
}

// headerShort is the length of a file that turned out to be too short, for the
// error message. A file that cannot be stat'ed reports as empty, which is the
// right thing to say about a file nothing can be read from.
func headerShort(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// Keys returns what the header says the file holds.
func (k *KeyReader) Keys() Keys { return k.keys }

// Name returns the path the reader was opened on.
func (k *KeyReader) Name() string { return k.name }

// Next returns the next key, or false at the end of the file.
func (k *KeyReader) Next() (Key, bool, error) {
	var b [8]byte
	if _, err := io.ReadFull(k.r, b[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("count: reading %s: %w", k.name, err)
	}
	return binary.LittleEndian.Uint64(b[:]), true, nil
}

// Close closes the file.
func (k *KeyReader) Close() error { return k.f.Close() }

// MergeKeys merges sorted key files into one, dropping repeats, and reports what
// came out.
//
// The document count is the caller's rather than the sum of the headers, because
// the runs of one build all carry the running total of the build and adding them
// up would count the same documents several times over.
func MergeKeys(path string, docs int64, from ...string) (Keys, error) {
	m, err := openMerge(from)
	if err != nil {
		return Keys{}, err
	}
	defer m.close()

	f, err := os.Create(path) //nolint:gosec // the path is the caller's working directory
	if err != nil {
		return Keys{}, err
	}
	w := bufio.NewWriterSize(f, 1<<20)

	// The header carries a count that is not known until the merge has run, so
	// space is left for it and it is written over the top at the end. The
	// alternative is buffering the whole output, which is the thing this is
	// arranged not to do.
	if err := writeKeysHeader(w, 0, 0); err != nil {
		return Keys{}, closeAfter(f, err)
	}

	var distinct int64
	var b [8]byte
	for {
		key, ok, err := m.next()
		if err != nil {
			return Keys{}, closeAfter(f, err)
		}
		if !ok {
			break
		}
		distinct++
		binary.LittleEndian.PutUint64(b[:], key)
		if _, err := w.Write(b[:]); err != nil {
			return Keys{}, closeAfter(f, err)
		}
	}
	if err := w.Flush(); err != nil {
		return Keys{}, closeAfter(f, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Keys{}, closeAfter(f, err)
	}
	if err := writeKeysHeader(f, docs, distinct); err != nil {
		return Keys{}, closeAfter(f, err)
	}
	if err := f.Close(); err != nil {
		return Keys{}, err
	}
	return Keys{Documents: docs, Distinct: distinct}, nil
}

// merge walks several sorted key files at once, in order, without repeats.
type merge struct {
	readers []*KeyReader
	q       cursors

	started bool
}

// cursor is one file's current position in a merge.
type cursor struct {
	key Key
	at  int
}

// cursors orders the open files by the key each is sitting on, which is what
// makes the merge a heap rather than a scan of every file per key. Five files is
// small enough that either would do; a build spilling a hundred runs is not.
type cursors []cursor

func (c cursors) Len() int           { return len(c) }
func (c cursors) Less(i, j int) bool { return c[i].key < c[j].key }
func (c cursors) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }
func (c *cursors) Push(x any)        { *c = append(*c, x.(cursor)) }
func (c *cursors) Pop() (x any)      { old := *c; x = old[len(old)-1]; *c = old[:len(old)-1]; return x }
func (c cursors) peek() cursor       { return c[0] }
func (c cursors) empty() bool        { return len(c) == 0 }
func (c *cursors) replace(x cursor)  { (*c)[0] = x; heap.Fix(c, 0) }
func (c *cursors) drop()             { heap.Pop(c) }
func (c *cursors) start(x cursor)    { heap.Push(c, x) }

func openMerge(paths []string) (*merge, error) {
	m := &merge{}
	for _, p := range paths {
		r, err := OpenKeys(p)
		if err != nil {
			m.close()
			return nil, err
		}
		m.readers = append(m.readers, r)
	}
	return m, nil
}

func (m *merge) close() {
	for _, r := range m.readers {
		_ = r.Close()
	}
}

// next returns the next distinct key across every file, or false at the end.
func (m *merge) next() (Key, bool, error) {
	key, _, ok, err := m.nextGroup()
	return key, ok, err
}

// nextGroup returns the next distinct key and which files hold it, as a bit per
// file in the order they were opened.
//
// The mask is what makes every pairwise intersection one pass rather than one
// pass per pair. Five sources is ten pairs, and reading five files once to
// answer all ten is the difference between a measurement that takes an evening
// and one that takes a night.
func (m *merge) nextGroup() (key Key, in uint64, ok bool, err error) {
	if !m.started {
		m.started = true
		for i, r := range m.readers {
			first, have, err := r.Next()
			if err != nil {
				return 0, 0, false, err
			}
			if have {
				m.q.start(cursor{key: first, at: i})
			}
		}
	}
	if m.q.empty() {
		return 0, 0, false, nil
	}

	// Every cursor sitting on the smallest key is part of this group, and there
	// is one cursor per file, so the loop ends. Repeats inside a file are
	// already gone, so a file contributes to a group at most once.
	key = m.q.peek().key
	for !m.q.empty() && m.q.peek().key == key {
		top := m.q.peek()
		in |= 1 << top.at
		next, have, err := m.readers[top.at].Next()
		if err != nil {
			return 0, 0, false, err
		}
		if have {
			m.q.replace(cursor{key: next, at: top.at})
		} else {
			m.q.drop()
		}
	}
	return key, in, true, nil
}
