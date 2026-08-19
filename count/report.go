package count

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

	// Complete says the run these counts came from reached its end. A run over
	// a large source takes days, and the counts are rewritten after every file
	// so that a run in progress can be measured instead of being invisible
	// until it stops. That is only safe if a partial report says so, because
	// the alternative is a number that looks like a source total and is a
	// prefix of one.
	Complete bool `json:"complete"`

	// From says where the numbers came from, and is empty on a report the
	// ingest wrote, which is the overwhelming majority of them.
	//
	// It is set to "store" by a rebuild, and that is a different kind of report
	// rather than a fresher one. An ingest counts each document as it goes past
	// and knows how many bytes of text it held. A rebuild adds up columns in the
	// published Parquet, which is authoritative about what is in the corpus and
	// silent about byte lengths, because the byte length of the text is not a
	// column. So a rebuilt report carries a zero where the ingest carried a
	// number, and a reader has to be able to tell that zero from a corpus with
	// no text in it.
	From string `json:"from,omitempty"`

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

// FromStore is what From says on a report rebuilt out of the published corpus.
const FromStore = "store"

// Rebuilt makes a report out of what the store actually holds, which is the
// repair for a report that counted something twice.
//
// The counting a run does is a running tally, and a tally is a number that has
// to be right every time it is touched. The published corpus is not: it is a set
// of files, each written once, each carrying its own documents' shape columns,
// and adding them up cannot double count because a document that is in the store
// twice is in the store twice and that is the truth about the corpus. So this is
// not a second opinion about the same measurement, it is a measurement of a
// different and better defined thing.
//
// Bytes comes back zero for the reason [ShapeOf] gives, and the caller is
// expected to say so out loud rather than let a zero pass for a count. Nothing
// here scales the old byte figure by the ratio of the new document count to the
// old one. That would produce a number to five significant figures out of an
// assumption that the documents that were counted twice were the average size,
// which nobody measured and which is the whole failure this repairs.
func Rebuilt(box, tokenizer string, at time.Time, by map[doc.Source]Counts) Report {
	r := Report{
		Box:       box,
		Tokenizer: tokenizer,
		Finished:  at.UTC(),
		Complete:  true,
		From:      FromStore,
	}
	sources := make([]doc.Source, 0, len(by))
	for s := range by {
		sources = append(sources, s)
	}
	slices.Sort(sources)
	for _, s := range sources {
		c := by[s]
		r.Sources = append(r.Sources, SourceCounts{Source: s, Counts: c})
		r.Total.Merge(c)
		if s.Natural() {
			r.Natural.Merge(c)
		}
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
var ErrNoReport = errors.New("count: no counts in this directory")

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
		return Report{}, fmt.Errorf("count: reading counts: %w", err)
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
var ErrMixedTokenizers = errors.New("count: these counts were produced by different tokenizers")

// ErrMixedOrigins reports an attempt to add a report the ingest wrote to one
// rebuilt from the store.
//
// It is the same failure as mixing tokenizers, one column over. A rebuilt report
// carries no byte count because the store cannot answer for one, so a sum across
// the two is a byte total that silently omits whichever boxes were repaired, and
// it looks exactly like a corpus that is smaller than it is.
var ErrMixedOrigins = errors.New("count: these counts came from an ingest and from a store, and their byte columns do not mean the same thing")

// origin names where a report's numbers came from, for an error a person reads.
func origin(from string) string {
	if from == "" {
		return "an ingest"
	}
	return "the " + from
}

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

	// Complete only if every box is, since one box still running makes the sum
	// a prefix of the corpus rather than the corpus.
	out.Complete = len(reports) > 0

	for i, r := range reports {
		out.Complete = out.Complete && r.Complete
		switch {
		case out.Tokenizer == "":
			out.Tokenizer = r.Tokenizer
		case r.Tokenizer != "" && r.Tokenizer != out.Tokenizer:
			return Report{}, fmt.Errorf("%w: %s and %s", ErrMixedTokenizers, out.Tokenizer, r.Tokenizer)
		}
		if i == 0 {
			out.From = r.From
		} else if r.From != out.From {
			return Report{}, fmt.Errorf("%w: %s and %s", ErrMixedOrigins, origin(out.From), origin(r.From))
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
