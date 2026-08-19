package store

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"

	"github.com/tamnd/gao/doc"
)

// SegmentExt is the extension every segment file carries. It says what the file
// is to anybody holding it without gao installed: JSON lines, zstd compressed,
// and both of those are true even though there is an index frame at the end.
const SegmentExt = ".jsonl.zst"

// partExt marks a shard that is still being written. A shard is built under this
// extension and renamed into place only after it closes cleanly, so a build that
// dies halfway leaves files nobody will mistake for finished ones.
const partExt = ".part"

// ErrNotThisWorker is returned when a document hashes into a shard the set does
// not own.
var ErrNotThisWorker = errors.New("kho: that document belongs to another shard range")

// ErrSealed is returned when a shard set is pointed at a directory that already
// holds a sealed snapshot.
var ErrSealed = errors.New("kho: the snapshot is sealed")

// ShardName returns the file name of shard i of n.
//
// The count is in the name so that a directory listing is enough to tell whether
// a snapshot is complete, without reading a manifest and without trusting that
// the highest number present is the highest number there should be.
func ShardName(i, n int) string {
	return fmt.Sprintf("shard-%05d-of-%05d%s", i, n, SegmentExt)
}

var shardNamePattern = regexp.MustCompile(`^shard-(\d{5,})-of-(\d{5,})\.jsonl\.zst$`)

// ParseShardName returns the shard index and the shard count from a file name.
func ParseShardName(name string) (i, n int, ok bool) {
	m := shardNamePattern.FindStringSubmatch(name)
	if m == nil {
		return 0, 0, false
	}
	i, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false
	}
	n, err = strconv.Atoi(m[2])
	if err != nil || i >= n {
		return 0, 0, false
	}
	return i, n, true
}

// shardOptions is the settled configuration for a [ShardSet].
type shardOptions struct {
	from, to int
	writer   []WriterOption
	buffer   int
}

// ShardOption configures a [ShardSet].
type ShardOption func(*shardOptions)

// ShardRange restricts the set to shards [from, to), so that several workers can
// build one snapshot between them without any two of them holding the same file
// open.
//
// It also bounds memory, which is the reason it exists rather than a convenience
// on top of the reason. Every open shard holds a zstd encoder and a frame
// buffer, so a worker that owns all 750 shards of a full snapshot is holding
// several gigabytes of buffers on a box that has 6 GB. Own a range, make a pass,
// move to the next range.
func ShardRange(from, to int) ShardOption {
	return func(o *shardOptions) { o.from, o.to = from, to }
}

// ShardWriterOptions passes options through to each shard's segment writer.
func ShardWriterOptions(opts ...WriterOption) ShardOption {
	return func(o *shardOptions) { o.writer = append(o.writer, opts...) }
}

// ShardBuffer sets the size of the write buffer in front of each shard file.
func ShardBuffer(n int) ShardOption {
	return func(o *shardOptions) { o.buffer = n }
}

// DefaultShardBuffer is the buffer in front of each open shard file.
const DefaultShardBuffer = 256 << 10

// ShardSet fans records into n shards by the low bits of their identity hash and
// writes each shard as its own segment.
//
// Sharding is by hash rather than by source, date, or crawl batch. Those are all
// more useful for browsing and all wrong for the job: a hash shard is a uniform
// random sample of the corpus, so a stage that processes shard 7 sees the same
// distribution as a stage that processes the whole thing, and a bug that only
// shows up on one source shows up in every shard rather than in one file nobody
// opened. The same property is what makes deduplication tractable, since two
// copies of a document land in the same shard by construction.
type ShardSet[P Record[T], T any] struct {
	dir  string
	n    int
	from int
	to   int
	key  func(P) doc.Hash
	cfg  shardOptions

	open   map[int]*openShard[P, T]
	sealed []Shard
	closed bool
}

// openShard is one shard being written.
type openShard[P Record[T], T any] struct {
	index int
	name  string
	part  string
	f     *os.File
	buf   *bufio.Writer
	seg   *Writer[P, T]
}

// NewShardSet returns a shard set that writes n shards into dir. The key
// function gives the hash a record is sharded by, which for a document is its
// content identity and for a reject is whichever of its two identities it has.
func NewShardSet[P Record[T], T any](dir string, n int, key func(P) doc.Hash, opts ...ShardOption) (*ShardSet[P, T], error) {
	if n <= 0 {
		return nil, fmt.Errorf("kho: a snapshot needs at least one shard, got %d", n)
	}
	if key == nil {
		return nil, errors.New("kho: a shard set needs a key function")
	}

	cfg := shardOptions{from: 0, to: n, buffer: DefaultShardBuffer}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.from < 0 || cfg.to > n || cfg.from >= cfg.to {
		return nil, fmt.Errorf("kho: shard range [%d, %d) is not inside [0, %d)", cfg.from, cfg.to, n)
	}
	if Sealed(dir) {
		return nil, fmt.Errorf("%w: %s already has a %s", ErrSealed, dir, ManifestName)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("kho: creating the snapshot directory: %w", err)
	}

	return &ShardSet[P, T]{
		dir:  dir,
		n:    n,
		from: cfg.from,
		to:   cfg.to,
		key:  key,
		cfg:  cfg,
		open: make(map[int]*openShard[P, T], cfg.to-cfg.from),
	}, nil
}

// Shard returns the shard index a record belongs to.
func (s *ShardSet[P, T]) Shard(d P) int {
	return doc.Shard(s.key(d), s.n)
}

// Owns reports whether this set is responsible for shard i.
func (s *ShardSet[P, T]) Owns(i int) bool {
	return i >= s.from && i < s.to
}

// Append writes one record to whichever shard it belongs to, opening that shard
// if this is the first record to land in it.
//
// A record outside the set's range is an error rather than a silent drop. A
// worker that quietly ignored the documents it did not own would produce a
// snapshot that verifies, has the right shard count, and is missing a third of
// the corpus.
func (s *ShardSet[P, T]) Append(d P) error {
	if s.closed {
		return errors.New("kho: append to a closed shard set")
	}
	i := s.Shard(d)
	if !s.Owns(i) {
		return fmt.Errorf("%w: shard %d is not in [%d, %d)", ErrNotThisWorker, i, s.from, s.to)
	}
	sh, err := s.shardFor(i)
	if err != nil {
		return err
	}
	return sh.seg.Append(d)
}

// shardFor returns the open shard i, opening it if necessary.
func (s *ShardSet[P, T]) shardFor(i int) (*openShard[P, T], error) {
	if sh, ok := s.open[i]; ok {
		return sh, nil
	}
	name := ShardName(i, s.n)
	part := filepath.Join(s.dir, name+partExt)

	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, fmt.Errorf("kho: opening %s: %w", name, err)
	}
	buf := bufio.NewWriterSize(f, s.cfg.buffer)
	seg, err := NewWriter[P](buf, s.cfg.writer...)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(part)
		return nil, err
	}
	sh := &openShard[P, T]{index: i, name: name, part: part, f: f, buf: buf, seg: seg}
	s.open[i] = sh
	return sh, nil
}

// Count returns the number of records written so far.
func (s *ShardSet[P, T]) Count() int {
	var n int
	for _, sh := range s.open {
		n += sh.seg.Count()
	}
	return n
}

// Open returns the number of shards currently held open, which is the file
// descriptor and buffer cost the set is running at.
func (s *ShardSet[P, T]) Open() int {
	return len(s.open)
}

// Close finishes every open shard, moves it into place, and returns the shard
// records for the manifest, ordered by index.
//
// Shards that no record landed in are not created. At 750 shards and half a
// billion documents every shard is populated, and on a small test run most are
// not, so the manifest lists what exists rather than a run of empty files.
func (s *ShardSet[P, T]) Close() ([]Shard, error) {
	if s.closed {
		return slices.Clone(s.sealed), nil
	}
	s.closed = true

	indexes := slices.Sorted(maps.Keys(s.open))

	var errs []error
	for _, i := range indexes {
		sh := s.open[i]
		rec, err := s.finish(sh)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.sealed = append(s.sealed, rec)
	}
	if len(errs) > 0 {
		return s.sealed, errors.Join(errs...)
	}
	return slices.Clone(s.sealed), nil
}

// finish closes one shard and renames it into place.
func (s *ShardSet[P, T]) finish(sh *openShard[P, T]) (Shard, error) {
	if err := sh.seg.Close(); err != nil {
		_ = sh.f.Close()
		return Shard{}, err
	}
	if err := sh.buf.Flush(); err != nil {
		_ = sh.f.Close()
		return Shard{}, fmt.Errorf("kho: flushing %s: %w", sh.name, err)
	}

	// The file is fsynced before it is renamed. Without that, a crash can leave
	// the rename durable and the contents not, which is a shard that is present,
	// named as complete, and empty.
	if err := sh.f.Sync(); err != nil {
		_ = sh.f.Close()
		return Shard{}, fmt.Errorf("kho: syncing %s: %w", sh.name, err)
	}
	size, err := sh.f.Seek(0, io.SeekCurrent)
	if err != nil {
		_ = sh.f.Close()
		return Shard{}, fmt.Errorf("kho: sizing %s: %w", sh.name, err)
	}
	if err := sh.f.Close(); err != nil {
		return Shard{}, fmt.Errorf("kho: closing %s: %w", sh.name, err)
	}
	if err := os.Rename(sh.part, filepath.Join(s.dir, sh.name)); err != nil {
		return Shard{}, fmt.Errorf("kho: moving %s into place: %w", sh.name, err)
	}
	return Shard{
		Name:      sh.name,
		Index:     sh.index,
		Documents: sh.seg.Count(),
		Bytes:     size,
		Hash:      sh.seg.Hash(),
	}, nil
}

// Abandon closes every open shard and deletes the partial files. It is what a
// failed build should call, and calling it after [ShardSet.Close] does nothing,
// so it is safe to defer.
func (s *ShardSet[P, T]) Abandon() {
	if s.closed {
		return
	}
	s.closed = true
	for _, sh := range s.open {
		_ = sh.f.Close()
		_ = os.Remove(sh.part)
	}
}
