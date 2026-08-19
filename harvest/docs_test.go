package harvest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/reject"
)

// consume runs the sink over a file the way [Ingest] would, so that the test
// exercises the path the command actually takes.
func consume(t *testing.T, d *Docs, p Pinned, f File, r io.Reader) (int64, error) {
	t.Helper()
	return d.Consume(t.Context(), p, f, r)
}

func TestTheSinkAdmitsWhatTheContractAdmitsAndCountsIt(t *testing.T) {
	p, f := hpltPin(t)

	var got []*doc.Document
	d := &Docs{Emit: func(rec *doc.Document) error { got = append(got, rec); return nil }}

	n, err := consume(t, d, p, f, zstdOf(t, hpltLine+"\n"+hpltLine+"\n"))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if n != 2 || len(got) != 2 {
		t.Fatalf("Consume returned %d with %d emitted, want 2 and 2", n, len(got))
	}
	if d.Admitted() != 2 || d.Rejected() != 0 {
		t.Errorf("%d admitted and %d rejected", d.Admitted(), d.Rejected())
	}
	if len(d.Reasons()) != 0 {
		t.Errorf("a run with no rejections reported %v", d.Reasons())
	}
}

// The reject store is what turns a rejection rate from a thing somebody suspects
// into a number somebody can look up, so the row has to arrive with the stage,
// the reason, and the detail that names what was actually wrong.
func TestARejectedDocumentArrivesInTheRejectStoreWithItsReason(t *testing.T) {
	p, f := madladPin(t)

	var buf bytes.Buffer
	// Sample 1 so the text survives, which is what a test wants and what a full
	// pass cannot afford.
	rejects, err := reject.NewWriter(&buf, 1)
	if err != nil {
		t.Fatalf("reject.NewWriter: %v", err)
	}
	d := &Docs{Rejects: rejects}

	if _, err := consume(t, d, p, f, gzipOf(t, madladLine+"\n")); !errors.Is(err, ErrNothingAdmitted) {
		t.Fatalf("Consume returned %v, want ErrNothingAdmitted", err)
	}
	if err := rejects.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := reject.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("reject.NewReader: %v", err)
	}
	rec, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if rec.Stage != Stage {
		t.Errorf("reject_stage is %q, want %q", rec.Stage, Stage)
	}
	if rec.Reason != reject.ReasonContract {
		t.Errorf("reject_reason is %q, want %q", rec.Reason, reject.ReasonContract)
	}
	if !strings.Contains(rec.Detail, "url is unset") {
		t.Errorf("reject_detail does not say what failed: %q", rec.Detail)
	}
	if rec.SourceLocator != f.Path+":1" {
		t.Errorf("the rejected row cannot be found again: %q", rec.SourceLocator)
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Error("more than one rejection was written for one record")
	}
}

// A nil reject store is a decision and not a default. The counts still come out,
// so what is given up is the ability to look at what was thrown away and not the
// ability to count it.
func TestTheCountsComeOutWithNoRejectStoreAtAll(t *testing.T) {
	p, f := madladPin(t)
	d := &Docs{}

	if _, err := consume(t, d, p, f, gzipOf(t, madladLine+"\n"+madladLine+"\n")); !errors.Is(err, ErrNothingAdmitted) {
		t.Fatalf("Consume returned %v, want ErrNothingAdmitted", err)
	}
	if d.Rejected() != 2 {
		t.Errorf("%d rejected, want 2", d.Rejected())
	}
	if got := d.Reasons()[reject.ReasonContract]; got != 2 {
		t.Errorf("%d contract rejections, want 2", got)
	}
}

// A shard that yields nothing means either the mapping is wrong or the source
// cannot satisfy the contract, and either way the next sixty files will do the
// same thing.
func TestAFileThatAdmitsNothingStopsTheRunAndSaysWhichFile(t *testing.T) {
	p, f := madladPin(t)
	d := &Docs{}

	n, err := consume(t, d, p, f, gzipOf(t, madladLine+"\n"))
	if !errors.Is(err, ErrNothingAdmitted) {
		t.Fatalf("Consume returned %v, want ErrNothingAdmitted", err)
	}
	if n != 0 {
		t.Errorf("Consume returned %d documents alongside the error", n)
	}
	for _, want := range []string{f.Path, string(p.Source), "1 rejected"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}

// An empty file is not the same thing as a file that admitted nothing. It has
// nothing to say about the mapping, so it is not an error.
func TestAFileWithNoRecordsIsNotAFailure(t *testing.T) {
	p, f := madladPin(t)
	d := &Docs{}

	n, err := consume(t, d, p, f, gzipOf(t, "\n\n"))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if n != 0 {
		t.Errorf("Consume returned %d documents from an empty file", n)
	}
}

// Language means the producer looked at the document and said it was not
// Vietnamese enough. A source that publishes no score at all has said nothing
// about any of its documents, and counting those as a language finding would
// report MADLAD-400 as ninety five gigabytes of failed language identification.
func TestAMissingLanguageScoreIsNotALanguageFinding(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  doc.Document
		want reject.Reason
	}{
		{"no score published", doc.Document{}, reject.ReasonContract},
		{"a score the producer was unsure of", scored("vie", 0.2), reject.ReasonLanguage},
		{"a score the producer stood behind", scored("vie", 0.9), reject.ReasonContract},
		{"a language that is not the one being ingested", scored("eng", 0.99), reject.ReasonLanguage},
		{"a language with no score", scored("tha", 0), reject.ReasonLanguage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.rec
			if got := reasonFor(&rec); got != tc.want {
				t.Errorf("reasonFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func scored(lang string, score float32) doc.Document {
	var d doc.Document
	d.Lang = lang
	d.LangScore = score
	return d
}

// A 26.6 GB shard is several million records and an hour of work, and a ctrl-C
// that waits for the end of the file is a ctrl-C that does not work.
func TestCancellationStopsInTheMiddleOfAFile(t *testing.T) {
	p, f := hpltPin(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var seen int
	d := &Docs{Emit: func(*doc.Document) error {
		seen++
		cancel()
		return nil
	}}

	var body strings.Builder
	for range 500 {
		body.WriteString(hpltLine + "\n")
	}
	if _, err := d.Consume(ctx, p, f, zstdOf(t, body.String())); !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume returned %v, want context.Canceled", err)
	}
	if seen != 1 {
		t.Errorf("%d documents were emitted after the cancel", seen)
	}
}

// The command has to be able to refuse a source before it starts a download that
// ends in this error two hundred gigabytes later.
func TestTheSinkRefusesASourceWithNoDecoder(t *testing.T) {
	p := mustPin(t, doc.SourceFineWeb2)
	d := &Docs{}

	_, err := consume(t, d, p, File{Path: "data/vie_Latn/train-00000.parquet"}, strings.NewReader(""))
	if !errors.Is(err, ErrNoDecoder) {
		t.Fatalf("Consume returned %v, want ErrNoDecoder", err)
	}
	if !strings.Contains(err.Error(), string(p.Source)) {
		t.Errorf("the error does not name the source: %v", err)
	}
}

// The map is handed out as a copy, so a caller can hold it across the rest of a
// run without watching it change underneath them.
func TestTheReasonsAreHandedOutAsACopy(t *testing.T) {
	p, f := madladPin(t)
	d := &Docs{}

	if _, err := consume(t, d, p, f, gzipOf(t, madladLine+"\n")); !errors.Is(err, ErrNothingAdmitted) {
		t.Fatalf("Consume returned %v", err)
	}
	held := d.Reasons()
	if _, err := consume(t, d, p, f, gzipOf(t, madladLine+"\n")); !errors.Is(err, ErrNothingAdmitted) {
		t.Fatalf("Consume returned %v", err)
	}
	if held[reject.ReasonContract] != 1 {
		t.Errorf("the map moved under the caller: %v", held)
	}
	if d.Reasons()[reject.ReasonContract] != 2 {
		t.Errorf("the second file was not counted: %v", d.Reasons())
	}
}

// Docs is a Sink, and the whole point of the interface is that the command can
// swap Count for Docs without Ingest knowing which it has.
func TestDocsIsASink(t *testing.T) {
	var _ Sink = &Docs{}
}
