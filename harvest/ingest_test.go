package harvest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
)

// seen is a sink that records what it was given, and can be told to fail on a
// particular file or to stop reading part way through one.
type seen struct {
	files   []string
	bytes   int64
	failOn  string
	stopOn  string
	perFile int64
}

func (s *seen) Consume(_ context.Context, _ Pinned, f File, r io.Reader) (int64, error) {
	s.files = append(s.files, f.Path)
	if f.Path == s.failOn {
		return 0, errors.New("the decoder does not know this layout")
	}
	if f.Path == s.stopOn {
		n, err := io.CopyN(io.Discard, r, 10)
		s.bytes += n
		return 0, err
	}
	n, err := io.Copy(io.Discard, r)
	s.bytes += n
	if err != nil {
		return 0, err
	}
	return s.perFile, nil
}

func ingestFixture(t *testing.T, h *host) (*Ingest, []Work, *seen) {
	t.Helper()
	s, p, f := serveFile(t, h)
	l, _ := openLedger(t)
	sink := &seen{perFile: 3}

	in := &Ingest{
		Fetcher: &Fetcher{Client: s.Client(), Retries: -1},
		Ledger:  l,
		Sink:    sink,
		Box:     "server1",
	}
	second := File{Path: "shard-2.jsonl.zst", Bytes: f.Bytes, Digest: f.Digest}
	return in, []Work{{Pin: p, File: f}, {Pin: p, File: second}}, sink
}

func TestAnIngestFeedsEveryFileToTheSinkAndRecordsIt(t *testing.T) {
	in, todo, sink := ingestFixture(t, &host{})

	var reports []Report
	in.Progress = func(r Report) { reports = append(reports, r) }

	done, err := in.Run(t.Context(), todo)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if done != 2 {
		t.Errorf("Run finished %d files, want 2", done)
	}
	if len(sink.files) != 2 {
		t.Errorf("the sink saw %v", sink.files)
	}
	if sink.bytes != int64(2*len(body)) {
		t.Errorf("the sink read %d bytes, want %d", sink.bytes, 2*len(body))
	}
	if got := in.Ledger.Documents(); got != 6 {
		t.Errorf("the ledger records %d documents, want 6", got)
	}
	if len(reports) != 2 {
		t.Fatalf("progress was reported %d times", len(reports))
	}
	for _, r := range reports {
		if r.Err != nil {
			t.Errorf("%s reported %v", r.File.Path, r.Err)
		}
		if r.Digest != sha(body) {
			t.Errorf("%s reported digest %s", r.File.Path, r.Digest)
		}
		if r.Documents != 3 {
			t.Errorf("%s reported %d documents", r.File.Path, r.Documents)
		}
		// Not that the elapsed time is positive. Ten kilobytes over a loopback
		// connection finishes inside one tick of the Windows clock, and gamingpc
		// is a box this has to pass on.
		if r.Elapsed < 0 {
			t.Errorf("%s reported %s elapsed", r.File.Path, r.Elapsed)
		}
	}

	// The ledger is what makes the second run cheap, so it has to name the box
	// as well as the file.
	for _, e := range in.Ledger.Entries() {
		if e.Box != "server1" {
			t.Errorf("%s was recorded against box %q", e.Path, e.Box)
		}
	}
}

// A restart is the normal case over 154 files and days of transfer, not the
// exceptional one.
func TestARestartFetchesOnlyWhatIsLeft(t *testing.T) {
	h := &host{}
	in, todo, sink := ingestFixture(t, h)

	if _, err := in.Run(t.Context(), todo[:1]); err != nil {
		t.Fatalf("the first run: %v", err)
	}
	requests, _, _ := h.seen()
	if requests != 1 {
		t.Fatalf("the first run made %d requests", requests)
	}

	// Plan is what a restart calls, and it is what has to leave the finished
	// file out. The pin gets the file list here, because that is what a real one
	// carries and what the plan reads.
	pin := todo[0].Pin
	pin.Files = []File{todo[0].File, todo[1].File}

	rest, doneFiles, doneBytes := in.Ledger.Plan([]Pinned{pin})
	if doneFiles != 1 || doneBytes != todo[0].File.Bytes {
		t.Errorf("the plan reports %d files and %d bytes done", doneFiles, doneBytes)
	}
	if len(rest) != 1 || rest[0].File.Path != todo[1].File.Path {
		t.Fatalf("the plan after one file is %v", rest)
	}

	sink.files = nil
	if _, err := in.Run(t.Context(), rest); err != nil {
		t.Fatalf("the second run: %v", err)
	}
	if len(sink.files) != 1 || sink.files[0] != todo[1].File.Path {
		t.Errorf("the second run consumed %v", sink.files)
	}
	if requests, _, _ := h.seen(); requests != 2 {
		t.Errorf("the two runs made %d requests between them, want 2", requests)
	}
}

// A sink that fails on one file is going to fail on the next, and grinding
// through sixty more to report them together helps nobody.
func TestAnIngestStopsAtTheFirstFailureAndKeepsWhatItHad(t *testing.T) {
	in, todo, sink := ingestFixture(t, &host{})
	sink.failOn = todo[1].File.Path

	var reports []Report
	in.Progress = func(r Report) { reports = append(reports, r) }

	done, err := in.Run(t.Context(), todo)
	if err == nil {
		t.Fatal("Run reported success on a sink that failed")
	}
	if done != 1 {
		t.Errorf("Run finished %d files, want 1", done)
	}
	if !strings.Contains(err.Error(), todo[1].File.Path) {
		t.Errorf("the error does not say which file: %v", err)
	}
	if !in.Ledger.Done(todo[0].Pin, todo[0].File) {
		t.Error("the file that succeeded before the failure was not recorded")
	}
	if in.Ledger.Done(todo[1].Pin, todo[1].File) {
		t.Error("the file that failed was recorded as done")
	}
	if len(reports) != 2 || reports[1].Err == nil {
		t.Errorf("the failure was not reported: %+v", reports)
	}
}

// A sink that reads ten bytes of a shard and returns leaves a body that verified
// nothing, and the difference between that and a complete file is invisible from
// the byte count, so it is checked.
func TestASinkThatStopsEarlyDoesNotProduceAFinishedFile(t *testing.T) {
	in, todo, sink := ingestFixture(t, &host{})
	sink.stopOn = todo[0].File.Path

	_, err := in.Run(t.Context(), todo)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("Run returned %v, want ErrTruncated", err)
	}
	if !strings.Contains(err.Error(), "stopped at byte 10") {
		t.Errorf("the error does not say where it stopped: %v", err)
	}
	if in.Ledger.Done(todo[0].Pin, todo[0].File) {
		t.Error("a file the sink abandoned was recorded as done")
	}
}

func TestAFetchThatNeverStartsIsReported(t *testing.T) {
	in, todo, _ := ingestFixture(t, &host{status: 404})

	var reports []Report
	in.Progress = func(r Report) { reports = append(reports, r) }

	if _, err := in.Run(t.Context(), todo); err == nil {
		t.Fatal("Run reported success against a host answering 404")
	}
	if len(reports) != 1 || reports[0].Err == nil {
		t.Fatalf("the failed open was not reported: %+v", reports)
	}
	if reports[0].Reconnects != 0 || reports[0].Digest != "" {
		t.Errorf("a fetch that never opened reported %+v", reports[0])
	}
}

func TestAnIngestStopsWhenItsCallerDoes(t *testing.T) {
	in, todo, sink := ingestFixture(t, &host{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done, err := in.Run(ctx, todo)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v, want context.Canceled", err)
	}
	if done != 0 || len(sink.files) != 0 {
		t.Errorf("a canceled run finished %d files and consumed %v", done, sink.files)
	}
}

// The ledger is not optional. An ingest with nowhere to record progress starts
// from zero every time it is interrupted, and 608.9 GB will be interrupted.
func TestAnIngestWithoutALedgerRefusesToStart(t *testing.T) {
	in := &Ingest{}
	_, err := in.Run(t.Context(), nil)
	if err == nil {
		t.Fatal("an ingest with no ledger ran")
	}
	if !strings.Contains(err.Error(), "ledger") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

func TestAnIngestFillsInItsOwnDefaults(t *testing.T) {
	h := &host{}
	s, p, f := serveFile(t, h)
	l, _ := openLedger(t)

	// No sink and no fetcher, which is the configuration a caller reaches for
	// when it only wants to know whether the manifest is right.
	in := &Ingest{Ledger: l, Fetcher: &Fetcher{Client: s.Client(), Retries: -1}}
	done, err := in.Run(t.Context(), []Work{{Pin: p, File: f}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if done != 1 {
		t.Errorf("Run finished %d files, want 1", done)
	}
	// Count reports no documents rather than a byte count dressed up as one.
	if got := l.Documents(); got != 0 {
		t.Errorf("the counting sink reported %d documents", got)
	}
	if got := l.Bytes(); got != int64(len(body)) {
		t.Errorf("the ledger recorded %d bytes, want %d", got, len(body))
	}
}

func TestASinkFuncIsASink(t *testing.T) {
	var got doc.Source
	fn := SinkFunc(func(_ context.Context, p Pinned, _ File, r io.Reader) (int64, error) {
		got = p.Source
		_, err := io.Copy(io.Discard, r)
		return 7, err
	})
	n, err := fn.Consume(t.Context(), Pinned{Source: doc.SourceFinePDFs}, File{}, strings.NewReader("x"))
	if err != nil || n != 7 {
		t.Errorf("Consume = %d, %v", n, err)
	}
	if got != doc.SourceFinePDFs {
		t.Errorf("the sink was given source %q", got)
	}
}

// A reconnect is recorded because a source that needs forty of them a file is a
// source worth mirroring, and that is invisible unless it is counted.
func TestTheLedgerRecordsHowMuchTroubleAFileWas(t *testing.T) {
	h := &host{drops: 2, cut: 1000}
	s, p, f := serveFile(t, h)
	l, _ := openLedger(t)

	var report Report
	in := &Ingest{
		Fetcher:  &Fetcher{Client: s.Client(), RetryWait: 20 * time.Millisecond},
		Ledger:   l,
		Progress: func(r Report) { report = r },
	}
	if _, err := in.Run(t.Context(), []Work{{Pin: p, File: f}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Two reconnects at twenty milliseconds and forty, so the elapsed time is
	// long enough to be measurable on any clock the fleet runs.
	if report.Elapsed < 60*time.Millisecond {
		t.Errorf("a transfer that backed off twice reports %s elapsed", report.Elapsed)
	}
	entries := l.Entries()
	if len(entries) != 1 {
		t.Fatalf("the ledger holds %d entries", len(entries))
	}
	if entries[0].Reconnects != 2 {
		t.Errorf("the entry records %d reconnects, want 2", entries[0].Reconnects)
	}
	if entries[0].Digest != sha(body) {
		t.Errorf("the entry records digest %s", entries[0].Digest)
	}
}
