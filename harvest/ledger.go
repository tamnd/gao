package harvest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tamnd/gao/doc"
)

// What an ingest has already done.
//
// A full pull is 122 files and 513.6 GB, which is days of transfer, and it will
// be interrupted. The question a restart has to answer is which files are
// already through, and the ledger is that answer: one line per finished file,
// appended and fsynced the moment the file finishes, so a kill loses at most the
// file that was in flight.
//
// The unit of resume is the file rather than the byte. Within a file, a dropped
// connection is resumed by [Body] without anybody hearing about it, and a
// process that dies takes its in flight file with it because gao never wrote
// those bytes to disk in the first place. Restarting one shard is at worst
// 26.6 GB of the 513.6, and the alternative is a partial file on disk that has
// to be trusted, which is the thing streaming exists to avoid.
//
// An entry names the revision it was fetched at, so re-pinning a source
// invalidates its entries rather than letting a restart skip files that are no
// longer the files in the manifest. That is the whole reason the revision is in
// the key: the failure it prevents is a corpus built half from one revision and
// half from the next, which nothing downstream could detect.
//
// The format is JSON lines and not a database. It is append only, it is read
// once at startup, and a person debugging a stalled ingest at two in the morning
// can read it with tail.

// LedgerName is the file an ingest keeps its progress in, inside the work
// directory.
const LedgerName = "ingest.jsonl"

// Entry is one finished file.
type Entry struct {
	// Source and Path say which file, and Revision says which version of it.
	Source   doc.Source `json:"source"`
	Revision string     `json:"revision"`
	Path     string     `json:"path"`

	// Bytes is the length of the file this entry accounts for. A streamed file
	// counts it on the way past, so it is what was received rather than what was
	// expected, and for a host that publishes no digest it is the only check
	// there is. A file read out of order is never received as a whole, so it
	// carries the pinned length and Moved says what actually crossed the wire.
	Bytes int64 `json:"bytes"`

	// Digest is the sha256 of the file as it arrived, and it is empty for a file
	// read out of order. Such a file never has all of its bytes in one place, so
	// there is nothing to hash, and the empty field is the record of what
	// reading Parquet costs rather than an oversight.
	Digest string `json:"digest"`

	// Access is how the file was read, "stream" or "random", and it is empty for
	// a stream. Moved and Requests are what a random read cost, and they are the
	// numbers that answer whether reading a 4.8 GB file in pieces was cheaper
	// than fetching it, which is the whole premise of doing it that way.
	Access   string `json:"access,omitempty"`
	Moved    int64  `json:"moved,omitempty"`
	Requests int64  `json:"requests,omitempty"`

	// Documents is how many documents the file yielded, which is the number the
	// measurement section is eventually built out of.
	Documents int64 `json:"documents"`

	// Reconnects is how many times the transfer dropped and resumed. It is
	// recorded because a source that needs forty reconnects a file is a source
	// worth mirroring, and that is invisible unless it is counted.
	Reconnects int `json:"reconnects"`

	// Finished is when the file completed, in RFC 3339. It is the only clock
	// reading in the package and it exists so a stalled ingest can be told from
	// a slow one.
	Finished string `json:"finished"`

	// Box is the machine that fetched it, so a count in a report can be traced
	// back to the box that produced it.
	Box string `json:"box,omitempty"`
}

// Key is the identity of a fetched file: which file, at which revision.
func (e Entry) Key() string { return string(e.Source) + "@" + e.Revision + "/" + e.Path }

// Ledger is the append only record of finished files.
type Ledger struct {
	path string
	f    *os.File
	done map[string]Entry
}

// OpenLedger opens or creates the ledger in dir, reading whatever is already in
// it.
//
// A line it cannot parse is an error rather than a line to skip. A ledger with a
// corrupt tail is a ledger that might be missing the entry for a file that did
// finish, and re-downloading 26.6 GB is cheaper than being wrong about what the
// corpus contains.
func OpenLedger(dir string) (*Ledger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("harvest: creating the ingest directory: %w", err)
	}
	path := filepath.Join(dir, LedgerName)

	done, err := readLedger(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("harvest: opening %s: %w", path, err)
	}
	return &Ledger{path: path, f: f, done: done}, nil
}

// readLedger reads the entries already in the file, which may not exist yet.
func readLedger(path string) (map[string]Entry, error) {
	done := make(map[string]Entry)

	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return done, nil
	}
	if err != nil {
		return nil, fmt.Errorf("harvest: reading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for line := 1; sc.Scan(); line++ {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("harvest: %s line %d is not an ingest entry: %w", path, line, err)
		}
		if e.Source == "" || e.Path == "" || e.Revision == "" {
			return nil, fmt.Errorf("harvest: %s line %d does not say which file at which revision", path, line)
		}
		done[e.Key()] = e
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("harvest: reading %s: %w", path, err)
	}
	return done, nil
}

// Done reports whether a file has already been fetched at its pinned revision.
func (l *Ledger) Done(p Pinned, f File) bool {
	_, ok := l.done[Entry{Source: p.Source, Revision: p.Revision, Path: f.Path}.Key()]
	return ok
}

// Entries returns every finished file, in no particular order.
func (l *Ledger) Entries() []Entry {
	out := make([]Entry, 0, len(l.done))
	for _, e := range l.done {
		out = append(out, e)
	}
	return out
}

// Bytes returns how many bytes the ledger accounts for, which is the progress
// number a person actually wants.
func (l *Ledger) Bytes() int64 {
	var n int64
	for _, e := range l.done {
		n += e.Bytes
	}
	return n
}

// Documents returns how many documents the finished files yielded.
func (l *Ledger) Documents() int64 {
	var n int64
	for _, e := range l.done {
		n += e.Documents
	}
	return n
}

// Record appends one finished file and syncs it.
//
// The sync is the point. Without it, a box that loses power has a ledger the
// filesystem was going to write eventually, and a restart that skips a file
// whose bytes never reached anything is worse than a restart that redoes one.
func (l *Ledger) Record(e Entry) error {
	if e.Finished == "" {
		e.Finished = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("harvest: recording %s: %w", e.Path, err)
	}
	b = append(b, '\n')
	if _, err := l.f.Write(b); err != nil {
		return fmt.Errorf("harvest: writing %s: %w", l.path, err)
	}
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("harvest: syncing %s: %w", l.path, err)
	}
	l.done[e.Key()] = e
	return nil
}

// Close closes the ledger file.
func (l *Ledger) Close() error {
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	if err != nil {
		return fmt.Errorf("harvest: closing %s: %w", l.path, err)
	}
	return nil
}

// Work is one file still to fetch.
type Work struct {
	Pin  Pinned
	File File
}

// Plan returns the files still to fetch, in ingest order, along with what is
// already done.
//
// Ingest order is the order in the manifest and it is not arbitrary. HPLT v3 is
// first because it is the spine of the corpus and the source the headline number
// is a claim about, and an ingest that runs out of time or disk should have run
// out of it after the spine rather than before.
func (l *Ledger) Plan(sources []Pinned) (todo []Work, doneFiles int, doneBytes int64) {
	for _, p := range sources {
		for _, f := range p.Files {
			if l.Done(p, f) {
				doneFiles++
				doneBytes += f.Bytes
				continue
			}
			todo = append(todo, Work{Pin: p, File: f})
		}
	}
	return todo, doneFiles, doneBytes
}

// Remaining returns how many bytes a plan still has to move.
func Remaining(todo []Work) int64 {
	var n int64
	for _, w := range todo {
		n += w.File.Bytes
	}
	return n
}

// ReadLedger reads a ledger without opening it for writing, which is what a
// report wants and what a second process on the same directory must do.
func ReadLedger(dir string) ([]Entry, error) {
	done, err := readLedger(filepath.Join(dir, LedgerName))
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(done))
	for _, e := range done {
		out = append(out, e)
	}
	return out, nil
}
