package clean

// The pass: the line run over a repo full of parts rather than over a document.
//
// The shape is the ingest's, for the same reason the ingest has it. A part is
// read out of the store over HTTP, cleaned in memory, written to one local file,
// pushed, and deleted before the next part opens. Peak disk is one part per
// worker no matter how large the corpus is, which is what lets a box with a
// hundred gigabytes spare clean a quarter of a terabyte.
//
// The unit of work and the unit of resume are both one part. A pass that dies
// after two hundred parts is resumed by running it again: the clean repo is
// listed at the start, and a part already up there is not read, not cleaned and
// not pushed. That listing is one API call and it is the only piece of state the
// pass keeps, because the run's own log is the one record that cannot be right
// about a part that was pushed by a process that then died.
//
// Reading is the cost. The line is around eight milliseconds of one core per
// document, which is fast enough that a box with real cores finishes a part
// before it has finished downloading the next one, so the workers are sized for
// the network rather than for the CPU on every box in this fleet.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tamnd/gao/count"
	"github.com/tamnd/gao/cover"
	"github.com/tamnd/gao/normalize"
	"github.com/tamnd/gao/reject"
	"github.com/tamnd/gao/sift"
	"github.com/tamnd/gao/store"
)

// Pass is one run of the line over a store.
type Pass struct {
	// From is the repo the raw parts are read out of, and To is the repo the
	// clean parts are written to. They are two repos rather than two prefixes
	// of one, because the raw corpus is what a rerun of this stage reads and a
	// stage that overwrote its own input could only be run once.
	From *count.Store
	To   *store.Pusher

	// Clean is the dataset To describes, which is what the writer takes its
	// schema and its license check from.
	Clean store.Dataset

	// Dir is where a part is built before it is pushed.
	Dir string

	// Box is the machine label that goes in every part's footer.
	Box string

	// Workers is how many parts are in flight at once.
	Workers int

	// Keys is how many documents the deduplication set is sized for.
	Keys int

	// Push says whether a finished part goes to the store and the local copy is
	// deleted. Without it the parts stay in Dir, which is what a first run on a
	// new box does to see what comes out before anything is published.
	Push bool
}

// Cleaned is one part, after the pass has finished with it.
type Cleaned struct {
	// From is the raw part, and Path is where the clean part went, which is the
	// same path in the other repo.
	From store.Stored
	Path string

	// Skipped reports that the clean repo already had this part, in which case
	// every count below is zero and nothing was read.
	Skipped bool

	// Documents is what the part held and Kept is what came out of the line.
	Documents int64
	Kept      int64

	// Text is the bytes of text the clean part holds, and Bytes is the size of
	// the file that text went into.
	Text  int64
	Bytes int64

	// Took is the wall clock for reading, cleaning, writing and pushing this
	// part, which is the reading a throughput number is taken from.
	Took time.Duration
}

// Progress is called once per part, in whatever order the parts finish.
type Progress func(c Cleaned, done, of int)

// Report is what a pass adds up to.
//
// Every count is over the documents this run read, not over the corpus. A pass
// that skipped four hundred parts because they were already clean reports the
// parts it did, and the corpus wide numbers come from the parts index and from
// queries over the clean repo, which are the two things that are right about a
// repo several boxes have been writing to.
type Report struct {
	Box      string    `json:"box"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`

	Parts   int `json:"parts"`
	Skipped int `json:"skipped"`

	Documents int64 `json:"documents"`
	Kept      int64 `json:"kept"`

	// TextIn is the text the pass read and TextOut is the text it published. The
	// difference is what the line removed, and it is not the same shape as the
	// document counts: normalization takes bytes out of documents it keeps and
	// redaction puts a tag in where an identifier was.
	TextIn  int64 `json:"text_in"`
	TextOut int64 `json:"text_out"`
	Bytes   int64 `json:"bytes"`

	// Removed is what each stage took out, by reason.
	Removed map[Stage]map[reject.Reason]int64 `json:"removed,omitempty"`

	// The stage tallies, which are what the report commands over the raw corpus
	// print and are here so that a cleaning run answers the same questions
	// without a second pass over the same bytes.
	Normalize normalize.Tally `json:"normalize"`
	Sift      sift.Tally      `json:"sift"`
	Cover     cover.Tally     `json:"cover"`

	// Clusters is how many distinct documents the deduplication set held when
	// the run finished, and Unchecked is how many documents it had no room for.
	// A run with Unchecked above zero published duplicates it did not look at.
	Clusters  int   `json:"clusters"`
	Unchecked int64 `json:"unchecked"`

	// Line is the version of the line these parts were written by, so a report
	// read a year from now says which rules produced it.
	Line string `json:"line"`
}

// Retention is the share of the documents read that were published.
func (r Report) Retention() float64 {
	if r.Documents == 0 {
		return 0
	}
	return float64(r.Kept) / float64(r.Documents)
}

// Rate is documents per second of wall clock, which is a fleet number rather
// than a per core one and is what a plan for the rest of the corpus is built on.
func (r Report) Rate() float64 {
	took := r.Finished.Sub(r.Started).Seconds()
	if took <= 0 {
		return 0
	}
	return float64(r.Documents) / took
}

// Run cleans every part in parts and returns what it did.
//
// The parts are given rather than listed here, because which parts a box takes
// is a scheduling question with its own command and answering it inside the
// pass would make a run that fetched the wrong half of the corpus look exactly
// like a run that fetched the right half.
func (p *Pass) Run(ctx context.Context, parts []store.Stored, note Progress) (Report, error) {
	if p.Workers < 1 {
		p.Workers = 1
	}
	rep := Report{Box: p.Box, Started: time.Now(), Line: PipelineVersion}

	have, err := p.held(ctx)
	if err != nil {
		return rep, err
	}

	seen := NewSeen(p.Keys)
	var (
		mu   sync.Mutex
		done int
		bad  error
	)
	work := make(chan store.Stored)

	var wg sync.WaitGroup
	for range p.Workers {
		wg.Go(func() {
			line := &Line{Limits: sift.Default(), Level: cover.L1, Seen: seen}
			for part := range work {
				c, tally, err := p.part(ctx, line, part, have[part.Path])
				mu.Lock()
				done++
				if err != nil && bad == nil {
					bad = err
				}
				if err == nil {
					rep.fold(c, tally)
					if note != nil {
						note(c, done, len(parts))
					}
				}
				mu.Unlock()
				if err != nil {
					return
				}
			}
		})
	}

	for _, part := range parts {
		select {
		case work <- part:
		case <-ctx.Done():
			bad = ctx.Err()
		}
		if bad != nil {
			break
		}
	}
	close(work)
	wg.Wait()

	rep.Finished = time.Now()
	rep.Clusters = seen.Len()
	rep.Unchecked = seen.Over()
	return rep, bad
}

// held is the set of paths the clean repo already has, which is what makes the
// pass resumable at the part.
func (p *Pass) held(ctx context.Context) (map[string]bool, error) {
	files, err := p.To.List(ctx, store.DataDir)
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(files))
	for _, f := range files {
		if f.Parquet() {
			have[f.Path] = true
		}
	}
	return have, nil
}

// tally is the per part accumulation of what the stages did, kept apart from
// [Report] so that a part that fails is not half folded into the run.
type tally struct {
	removed   map[Stage]map[reject.Reason]int64
	normalize normalize.Tally
	sift      sift.Tally
	cover     cover.Tally
	textIn    int64
}

// part cleans one part and pushes it.
func (p *Pass) part(ctx context.Context, line *Line, from store.Stored, done bool) (Cleaned, tally, error) {
	c := Cleaned{From: from, Path: from.Path, Skipped: done}
	var t tally
	if done {
		return c, t, nil
	}
	started := time.Now()

	src, err := p.From.OpenRows(ctx, from)
	if err != nil {
		return c, t, err
	}

	out, err := store.CreatePart(p.Dir, from.Path, p.Clean, p.stamp(from))
	if err != nil {
		return c, t, err
	}

	t.removed = map[Stage]map[reject.Reason]int64{}
	err = store.ScanRows(from.Path, src, func(row store.Row) error {
		d := store.DocumentOf(row)
		t.textIn += int64(len(d.Text))
		v := line.Run(d)

		t.normalize.Add(v.Normalized)
		if v.Stage != StageNormalize {
			t.sift.Add(line.Limits, v.Measured)
		}
		if v.Kept {
			t.cover.Add(line.Level, v.Found)
			if err := out.Append(d); err != nil {
				return fmt.Errorf("clean: %s: %w", from.Path, err)
			}
			return nil
		}
		if t.removed[v.Stage] == nil {
			t.removed[v.Stage] = map[reject.Reason]int64{}
		}
		t.removed[v.Stage][v.Reason]++
		return nil
	})
	if err != nil {
		out.Abandon()
		return c, t, err
	}

	c.Documents = t.normalize.Documents
	c.Kept = int64(out.Documents())
	c.Text = out.Text()

	// A part where the line kept nothing is a part with no file. Writing an
	// empty Parquet would put a row group of zero rows in the repo and a row in
	// the index that reads as a part somebody has to explain.
	if c.Kept == 0 {
		out.Abandon()
		c.Path = ""
		c.Took = time.Since(started)
		return c, t, nil
	}

	file, err := out.Close()
	if err != nil {
		return c, t, err
	}
	c.Bytes = file.Bytes

	if p.Push {
		if _, err := p.To.Push(ctx, filepath.Join(p.Dir, from.Path), from.Path); err != nil {
			return c, t, err
		}
		if err := os.Remove(filepath.Join(p.Dir, from.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return c, t, err
		}
	}
	c.Took = time.Since(started)
	return c, t, nil
}

// stamp is what the clean part says about itself. The snapshot is the raw
// part's, because a cleaned FineWeb2 part is still that revision of FineWeb2
// and a snapshot name that hid which revision it was cleaned from would make
// the clean repo unreproducible from the raw one.
//
// The sach in the stage is the old name of this package and stays for the same
// reason harvest.Extractor keeps its own: parts already pushed carry it, and a
// provenance value that means one thing should not be written two ways.
func (p *Pass) stamp(from store.Stored) store.Stamp {
	snapshot, _, _, _ := store.ParseStagePath(from.Path)
	return store.Stamp{Snapshot: snapshot, Stage: "gao-sach@" + PipelineVersion, Box: p.Box}
}

// fold adds one part into the report. The caller holds the lock.
func (r *Report) fold(c Cleaned, t tally) {
	if c.Skipped {
		r.Skipped++
		return
	}
	r.Parts++
	r.Documents += c.Documents
	r.Kept += c.Kept
	r.TextIn += t.textIn
	r.TextOut += c.Text
	r.Bytes += c.Bytes

	if r.Removed == nil {
		r.Removed = map[Stage]map[reject.Reason]int64{}
	}
	for stage, reasons := range t.removed {
		if r.Removed[stage] == nil {
			r.Removed[stage] = map[reject.Reason]int64{}
		}
		for reason, n := range reasons {
			r.Removed[stage][reason] += n
		}
	}
	addNormalize(&r.Normalize, t.normalize)
	addSift(&r.Sift, t.sift)
	addCover(&r.Cover, t.cover)
}

func addNormalize(into *normalize.Tally, from normalize.Tally) {
	into.Documents += from.Documents
	into.Changed += from.Changed
	into.Repaired += from.Repaired
	into.Homoglyphs += from.Homoglyphs
	into.Invisible += from.Invisible
	into.Controls += from.Controls
	into.Composed += from.Composed
	into.Tones += from.Tones
	into.Residue += from.Residue
	into.Syllables += from.Syllables
	into.Rejected += from.Rejected
	for k, n := range from.Legacy {
		if into.Legacy == nil {
			into.Legacy = map[string]int64{}
		}
		into.Legacy[k] += n
	}
}

func addSift(into *sift.Tally, from sift.Tally) {
	into.Documents += from.Documents
	into.Kept += from.Kept
	into.Syllables += from.Syllables
	for k, n := range from.Rejected {
		if into.Rejected == nil {
			into.Rejected = map[reject.Reason]int64{}
		}
		into.Rejected[k] += n
	}
	for k, n := range from.Diacritics {
		if into.Diacritics == nil {
			into.Diacritics = map[string]int64{}
		}
		into.Diacritics[k] += n
	}
}

func addCover(into *cover.Tally, from cover.Tally) {
	into.Documents += from.Documents
	into.Carrying += from.Carrying
	into.Spans += from.Spans
	into.Covered += from.Covered
	into.Cued += from.Cued
	for k, n := range from.ByKind {
		if into.ByKind == nil {
			into.ByKind = map[cover.Kind]int64{}
		}
		into.ByKind[k] += n
	}
}
