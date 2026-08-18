package dem

// Checking a published count without downloading the corpus it came from.
//
// The size in a release note is produced by the ingest that wrote the corpus,
// which makes it a number the pipeline says about itself. A third party has to be
// able to arrive at the same number, and the obvious way to do that is to count
// the text again. At 900 GB that is a week of somebody's bandwidth, so nobody
// does it, and a number nobody checks is a number nobody has to be right about.
//
// The protocol is two levels and neither of them is a recount of the text.
//
// Level one is here. Every document carries n_chars, n_syllables and n_tokens as
// fixed width columns, so summing those columns over every part in the store
// gives the corpus in three of its four published units without reading a
// character of it. That is twelve bytes per document before the encoding takes
// most of even that away, against the few kilobytes the document is. What it
// proves is that the published total is the sum of what is actually stored, and
// what it catches is a report written from a run that did not finish, a source
// counted twice, a part that never made it to the store, and arithmetic.
//
// What it cannot catch is a column that lies. A stage that rewrote text and
// forgot to recount leaves columns that add up perfectly to a total describing
// text nobody has. That is level two, in spot.go, and level two does read text.
//
// The two together are what the milestone asks for and neither on its own is.
// Level one has full coverage and shallow reach, level two has deep reach over a
// sample, and a claim that rests on only one of them has a hole in it that is
// easy to name.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/parquet-go/parquet-go"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

// ShapeOf reads the shape columns of one part and returns what they add up to.
//
// Bytes comes back zero. The byte length of the text is not a column, so this
// cannot know it, and a zero the caller has to interpret is better than a number
// derived from the character count: that would be wrong by exactly the
// diacritics, and it would look like a measurement.
func ShapeOf(r io.ReaderAt, size int64) (Counts, error) {
	f, err := parquet.OpenFile(r, size)
	if err != nil {
		return Counts{}, fmt.Errorf("dem: opening a part of %d bytes: %w", size, err)
	}
	return shapeOf(f)
}

// shapeOf is ShapeOf over a part that is already open.
func shapeOf(f *parquet.File) (Counts, error) {
	c := Counts{Documents: f.NumRows()}
	for _, col := range []struct {
		name string
		into *int64
	}{
		{kho.CharsColumn, &c.Chars},
		{kho.SyllablesColumn, &c.Syllables},
		{kho.TokensColumn, &c.Tokens},
	} {
		// Counted as well as summed. A column chunk that stops early sums to a
		// smaller number and nothing else about it looks wrong, and a recount
		// whose whole job is to catch a total that is short would report the
		// short total as the answer.
		var rows int64
		if err := EachUint32(f, col.name, func(v uint32) error {
			rows++
			*col.into += int64(v)
			return nil
		}); err != nil {
			return Counts{}, err
		}
		if rows != c.Documents {
			return Counts{}, fmt.Errorf("dem: this part says it holds %d rows and its %s column holds %d values", c.Documents, col.name, rows)
		}
	}
	return c, nil
}

// Counting is called after each part with what the recount has read so far, and
// with whether that part was read or came off the resume log.
//
// The last argument is there because a caller that only has the byte count
// cannot tell a pass that read nothing from a pass that had nothing left to
// read. A fully resumed recount reports every column correct and zero bytes
// moved, which reads as a check that ran and found nothing wrong, and is a check
// that did not run.
type Counting func(part kho.Stored, i, of int, c Counts, moved int64, resumed bool)

// RecountOf sums the shape columns of every part of a snapshot.
//
// It is resumable at the part, because a pass over a snapshot of a thousand
// parts will be interrupted. The unit of lost work is one part.
func RecountOf(ctx context.Context, s *Store, snapshot, work string, note Counting) (Counts, error) {
	parts, err := s.Parts(ctx, snapshot)
	if err != nil {
		return Counts{}, err
	}
	if len(parts) == 0 {
		return Counts{}, fmt.Errorf("dem: %s holds no parts of %s, so there is nothing to recount", s.Repo, snapshot)
	}
	if err := os.MkdirAll(work, 0o750); err != nil {
		return Counts{}, err
	}
	log, err := openShapes(filepath.Join(work, snapshot+ShapesExt))
	if err != nil {
		return Counts{}, err
	}
	defer func() { _ = log.Close() }()

	var total Counts
	var moved int64
	for i, part := range parts {
		c, done := log.done(part.Path)
		if !done {
			if c, moved, err = recountPart(ctx, s, part, moved); err != nil {
				return Counts{}, err
			}
			if err := log.add(part.Path, c); err != nil {
				return Counts{}, err
			}
		}
		total.Merge(c)
		if note != nil {
			note(part, i+1, len(parts), total, moved, done)
		}
	}
	return total, nil
}

// recountPart reads one part's shape columns and returns the running total of
// bytes moved.
func recountPart(ctx context.Context, s *Store, part kho.Stored, moved int64) (Counts, int64, error) {
	r, err := s.Open(ctx, part)
	if err != nil {
		return Counts{}, moved, err
	}
	c, err := ShapeOf(r, r.Size())
	if err != nil {
		return Counts{}, moved, fmt.Errorf("dem: reading %s: %w", part.Path, err)
	}
	return c, moved + r.Bytes(), nil
}

// ShapesExt is the extension a recount's resume file carries.
const ShapesExt = ".shapes"

// A shapes file is a recount's resume record: one line of JSON per part,
// appended as that part finishes.
//
// It is one file rather than one file per part, which is the opposite of what
// the key pass does and for the opposite reason. A key file is megabytes and
// belongs on its own. A part's shape is four numbers, and a thousand forty byte
// files is a directory that costs more to list than the work it saves.
//
// A line goes out in a single append of a few dozen bytes. A run killed
// mid-write therefore leaves at most a last line with no newline after it, and
// that line is cut off when the file is next opened, so the part it came from is
// read again. The alternative is trusting a half written number, and a half
// written number parses.
type shapes struct {
	f  *os.File
	by map[string]Counts
}

// shapeLine is one part's line in a shapes file.
type shapeLine struct {
	Part string `json:"part"`
	Counts
}

func openShapes(path string) (*shapes, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // the path is the caller's working directory
	if err != nil {
		return nil, err
	}
	s := &shapes{f: f, by: map[string]Counts{}}
	end, err := s.read()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Truncate(end); err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.Seek(end, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	return s, nil
}

// read loads the finished lines and returns the offset just past the last one,
// which is where the next append goes and where anything unfinished is cut off.
//
// The whole file is read at once because it is around sixty bytes per part, and
// a snapshot of a thousand parts is smaller than one page of the corpus.
func (s *shapes) read() (int64, error) {
	b, err := io.ReadAll(s.f)
	if err != nil {
		return 0, err
	}
	// Everything past the last newline is a line that was never finished.
	end := bytes.LastIndexByte(b, '\n') + 1
	for _, line := range bytes.Split(b[:end], []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var l shapeLine
		if err := json.Unmarshal(line, &l); err != nil {
			// A line that was terminated and still does not parse is not a torn
			// tail, it is a file something else wrote, and appending gao's
			// records to the end of it would make two problems out of one.
			return 0, fmt.Errorf("dem: %s is not a recount resume file: %w", s.f.Name(), err)
		}
		s.by[l.Part] = l.Counts
	}
	return int64(end), nil
}

// done returns what a part came to last time, if it has been read already.
func (s *shapes) done(part string) (Counts, bool) {
	c, ok := s.by[part]
	return c, ok
}

// add records a part.
func (s *shapes) add(part string, c Counts) error {
	b, err := json.Marshal(shapeLine{Part: part, Counts: c})
	if err != nil {
		return err
	}
	if _, err := s.f.Write(append(b, '\n')); err != nil {
		return err
	}
	s.by[part] = c
	return nil
}

func (s *shapes) Close() error { return s.f.Close() }

// A Difference is one source's published counts against what the store holds.
type Difference struct {
	Source doc.Source `json:"source"`

	// Claimed is the report's line for this source, and Stored is what its parts
	// in the store add up to. A source in one and not the other has zeroes on the
	// side it is missing from, which is a difference and reads as one.
	Claimed Counts `json:"claimed"`
	Stored  Counts `json:"stored"`
}

// Agrees reports whether the published counts are the counts in the store.
//
// Bytes is not compared, because the store has no column for it. Saying so here
// rather than quietly treating a number nobody read as a number that matched is
// the difference between a check and the appearance of one.
func (d Difference) Agrees() bool {
	return d.Claimed.Documents == d.Stored.Documents &&
		d.Claimed.Chars == d.Stored.Chars &&
		d.Claimed.Syllables == d.Stored.Syllables &&
		d.Claimed.Tokens == d.Stored.Tokens
}

// Off returns the columns that do not agree, named, for a report that has to say
// what was wrong rather than that something was.
func (d Difference) Off() []string {
	var out []string
	for _, c := range []struct {
		name            string
		claimed, stored int64
	}{
		{"documents", d.Claimed.Documents, d.Stored.Documents},
		{"chars", d.Claimed.Chars, d.Stored.Chars},
		{"syllables", d.Claimed.Syllables, d.Stored.Syllables},
		{"tokens", d.Claimed.Tokens, d.Stored.Tokens},
	} {
		if c.claimed != c.stored {
			out = append(out, c.name)
		}
	}
	return out
}

// Compare puts a report against what a recount of the store found, per source.
//
// The result is in source order and holds every source either side names, so a
// source the report forgot and a source the store does not have both show up as
// a line rather than as an absence.
func Compare(claimed Report, stored map[doc.Source]Counts) []Difference {
	by := make(map[doc.Source]*Difference, len(stored))
	get := func(s doc.Source) *Difference {
		d, ok := by[s]
		if !ok {
			d = &Difference{Source: s}
			by[s] = d
		}
		return d
	}
	for _, sc := range claimed.Sources {
		get(sc.Source).Claimed = sc.Counts
	}
	for s, c := range stored {
		get(s).Stored = c
	}

	out := make([]Difference, 0, len(by))
	for _, d := range by {
		out = append(out, *d)
	}
	slices.SortFunc(out, func(a, b Difference) int { return strings.Compare(string(a.Source), string(b.Source)) })
	return out
}

// Agree reports whether every source's published counts are the counts in the
// store.
func Agree(diffs []Difference) bool {
	for _, d := range diffs {
		if !d.Agrees() {
			return false
		}
	}
	return len(diffs) > 0
}
