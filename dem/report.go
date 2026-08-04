package dem

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/tamnd/gao/doc"
)

// File is the name a counting run writes its report under, inside the ingest
// directory beside the ledger.
const File = "counts.json"

// A Report is one run's counts, written so that the numbers outlive the terminal
// they were printed to.
//
// Box is on it because the fleet is four machines and a count that does not say
// where it was produced cannot be checked against a rerun. Tokenizer is on it
// because a token count without a named tokenizer is not a measurement. Both are
// required by the milestone and both are the kind of thing that is obvious to
// whoever ran the command and lost within a week.
type Report struct {
	Box       string    `json:"box"`
	Tokenizer string    `json:"tokenizer,omitempty"`
	Finished  time.Time `json:"finished"`

	// Sources is in source order rather than a map, so that two runs over the
	// same material produce the same bytes and a diff of two reports is
	// readable.
	Sources []SourceCounts `json:"sources"`

	// Total is every source. Natural is the corpus, which is the number that
	// gets quoted, and they are equal until the first synthetic source lands.
	Total   Counts `json:"total"`
	Natural Counts `json:"natural"`
}

// SourceCounts is one source's line in a report.
type SourceCounts struct {
	Source doc.Source `json:"source"`
	Counts
}

// Report renders the tally.
func (t *Tally) Report(box string, at time.Time) Report {
	r := Report{
		Box:       box,
		Tokenizer: t.Tokenizer,
		Finished:  at.UTC(),
		Total:     t.Total(),
		Natural:   t.Natural(),
	}
	for _, s := range t.Sources() {
		r.Sources = append(r.Sources, SourceCounts{Source: s, Counts: t.Source(s)})
	}
	return r
}

// Write saves the report into an ingest directory.
//
// It writes to a temporary file and renames, because the alternative is a report
// truncated by a machine that went down mid-write, and a half written count is
// worse than none: it parses.
func (r Report) Write(dir string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp := filepath.Join(dir, File+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, File))
}

// ErrNoReport reports an ingest directory that has no counts in it.
var ErrNoReport = errors.New("dem: no counts in this directory")

// ReadReport loads the report from an ingest directory.
func ReadReport(dir string) (Report, error) {
	f, err := os.Open(filepath.Join(dir, File))
	if errors.Is(err, os.ErrNotExist) {
		return Report{}, fmt.Errorf("%w: %s", ErrNoReport, dir)
	}
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = f.Close() }()
	return DecodeReport(f)
}

// DecodeReport reads a report from r.
func DecodeReport(r io.Reader) (Report, error) {
	var out Report
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return Report{}, fmt.Errorf("dem: reading counts: %w", err)
	}
	return out, nil
}

// ErrMixedTokenizers reports an attempt to add up counts produced by two
// different tokenizers.
//
// It is an error rather than a warning. Two tokenizers disagree on Vietnamese by
// something like 30%, so a sum across them is not a token count that is slightly
// off, it is a number that does not correspond to any tokenizer at all, and it
// would be quoted as a corpus size.
var ErrMixedTokenizers = errors.New("dem: these counts were produced by different tokenizers")

// Merge adds reports together, which is how the four boxes' counts become one
// corpus count.
//
// The result has no box, because it is from more than one. Every source line
// keeps its own totals and duplicate sources across reports are summed, so
// merging two boxes that each did half the shards gives the whole.
func Merge(reports ...Report) (Report, error) {
	var out Report
	by := make(map[doc.Source]*Counts)
	var order []doc.Source

	for _, r := range reports {
		switch {
		case out.Tokenizer == "":
			out.Tokenizer = r.Tokenizer
		case r.Tokenizer != "" && r.Tokenizer != out.Tokenizer:
			return Report{}, fmt.Errorf("%w: %s and %s", ErrMixedTokenizers, out.Tokenizer, r.Tokenizer)
		}
		if r.Finished.After(out.Finished) {
			out.Finished = r.Finished
		}
		for _, sc := range r.Sources {
			c, ok := by[sc.Source]
			if !ok {
				c = &Counts{}
				by[sc.Source] = c
				order = append(order, sc.Source)
			}
			c.Merge(sc.Counts)
		}
	}

	slices.Sort(order)
	for _, s := range order {
		c := *by[s]
		out.Sources = append(out.Sources, SourceCounts{Source: s, Counts: c})
		out.Total.Merge(c)
		if s.Natural() {
			out.Natural.Merge(c)
		}
	}
	return out, nil
}
