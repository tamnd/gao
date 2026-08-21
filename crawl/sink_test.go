package crawl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/harvest"
	"github.com/tamnd/gao/reject"
	"github.com/tamnd/gao/store"
)

// visit is one fetched page, made here rather than fetched, since what the sink
// does with it is the same either way.
func visit(url, body string) *harvest.Visit {
	return &harvest.Visit{
		URL:    url,
		Host:   "baodongthap.example",
		Status: 200,
		Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:   []byte(body),
		Robots: harvest.Decision{Allowed: true, Why: "allow", Rule: "Allow: /"},
	}
}

func openSink(t *testing.T, o SinkOptions) *Sink {
	t.Helper()
	if o.Dir == "" {
		o.Dir = t.TempDir()
	}
	if o.Snapshot == "" {
		o.Snapshot = "gaocrawl-20260819"
	}
	s, err := OpenSink(o)
	if err != nil {
		t.Fatalf("OpenSink: %v", err)
	}
	return s
}

// The locator is the whole of what makes a document traceable, so it has to
// name bytes somebody can actually read back.
func TestTheLocatorNamesTheRecordThePageCameFrom(t *testing.T) {
	dir := t.TempDir()
	s := openSink(t, SinkOptions{Dir: dir, Record: true})

	v := visit("https://baodongthap.example/tin-1.html", "<html><body><p>Xin chao</p></body></html>")
	locator, err := s.Archive(v, time.Date(2026, 8, 19, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	name, offset, length, ok := parseLocator(locator)
	if !ok {
		t.Fatalf("the locator %q does not name a file, an offset and a length", locator)
	}
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("opening the volume: %v", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	r, err := harvest.NewWARCReader(io.LimitReader(f, length))
	if err != nil {
		t.Fatalf("reading at the offset the locator gave: %v", err)
	}
	rec, err := r.Next()
	if err != nil {
		t.Fatalf("the record at that offset would not read: %v", err)
	}
	if rec.Type() != "response" {
		t.Errorf("the locator points at a %s record, want the response", rec.Type())
	}
	if rec.URI() != v.URL {
		t.Errorf("the record at that offset is %s, want %s", rec.URI(), v.URL)
	}
	res, err := rec.Response()
	if err != nil {
		t.Fatalf("the response would not parse: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "Xin chao") {
		t.Errorf("the body at that offset is %q", body)
	}
}

// parseLocator splits path@offset+length, which is what [Sink.Archive] returns.
func parseLocator(s string) (name string, offset, length int64, ok bool) {
	name, rest, ok := strings.Cut(s, "@")
	if !ok {
		return "", 0, 0, false
	}
	a, b, ok := strings.Cut(rest, "+")
	if !ok {
		return "", 0, 0, false
	}
	var err error
	if offset, err = strconv.ParseInt(a, 10, 64); err != nil {
		return "", 0, 0, false
	}
	if length, err = strconv.ParseInt(b, 10, 64); err != nil {
		return "", 0, 0, false
	}
	return name, offset, length, true
}

// sampleDoc is one document of the kind a crawl produces: restricted, because
// everything a crawl of the open web keeps is, and with the columns the store
// checks filled in.
func sampleDoc(i int) *doc.Document {
	text := fmt.Sprintf("Bai viet so %d tren mot trang tin dia phuong.", i)
	d := &doc.Document{SchemaVersion: doc.SchemaVersion}
	d.RawID = doc.SumString("raw:" + text)
	d.DocID = doc.SumString(text)
	d.Text = text
	d.Source = doc.SourceCrawl
	d.SourceLocator = fmt.Sprintf("warc/gaocrawl-20260819-00000-00000.warc.gz@%d+2048", i*2048)
	d.URL = fmt.Sprintf("https://baodongthap.example/tin-%d.html", i)
	d.Host = "baodongthap.example"
	d.FetchedAt = time.Date(2026, 8, 19, 7, 0, 0, 0, time.UTC)
	d.MediaType = "text/html"
	d.Extractor = Extractor
	d.PipelineVersion = PipelineVersion
	d.LicenseClass, d.LicenseEvidence = licenseFor(harvest.Reservation{})
	return d
}

// A part goes to the store as it closes and the local copy goes with it, which
// is the claim that a crawl of any size runs on a disk sized for one part.
func TestAPartIsPushedAndTheDiskIsGivenBack(t *testing.T) {
	dir := t.TempDir()
	var pushed []string
	s := openSink(t, SinkOptions{
		Dir:          dir,
		BytesPerPart: 1, // every row closes a part
		Push: func(d store.Dataset, local, path string) error {
			pushed = append(pushed, d.Name+" "+path)
			if _, err := os.Stat(local); err != nil {
				return err
			}
			return nil
		},
	})

	for i := range 3 {
		d := sampleDoc(i)
		if err := s.Write(Verdict{Doc: d, Kept: true}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := s.Write(Verdict{
		Doc: sampleDoc(9), Stage: StageSift, Reason: reject.ReasonShort, Detail: "12 syllables against a floor of 60",
	}); err != nil {
		t.Fatalf("Write of a rejection: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(pushed) != 4 {
		t.Fatalf("%d parts were pushed: %v", len(pushed), pushed)
	}
	for _, want := range []string{KeptRepo, RejectRepo} {
		found := false
		for _, p := range pushed {
			found = found || strings.HasPrefix(p, want+" ")
		}
		if !found {
			t.Errorf("nothing was pushed to %s: %v", want, pushed)
		}
	}
	left, err := filepath.Glob(filepath.Join(dir, "*", "data", "*", "*.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("%d parts are still on the disk after being pushed: %v", len(left), left)
	}
	st := s.Stats()
	if st.Kept != 3 || st.Dropped != 1 {
		t.Errorf("the sink counted %d kept and %d dropped, want 3 and 1", st.Kept, st.Dropped)
	}
	if st.Pushed != 4 || st.Freed <= 0 {
		t.Errorf("the sink says %d parts pushed and %d bytes freed", st.Pushed, st.Freed)
	}
}

// A crawl that runs for a week and publishes at the end of it is a crawl whose
// dataset is a week behind and whose disk is holding the week. The clock is
// what stops both, so a part that has been open too long is closed by the next
// row that arrives rather than by the size it never reaches.
func TestAPartIsCutOnTheClock(t *testing.T) {
	var pushed []string
	s := openSink(t, SinkOptions{
		PartEvery: 20 * time.Millisecond,
		Push: func(d store.Dataset, local, path string) error {
			pushed = append(pushed, path)
			return nil
		},
	})

	// Three rows well inside the interval, which is one part and nothing pushed.
	for i := range 3 {
		if err := s.Write(Verdict{Doc: sampleDoc(i), Kept: true}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if len(pushed) != 0 {
		t.Fatalf("a part was pushed before the interval was up: %v", pushed)
	}

	time.Sleep(30 * time.Millisecond)
	if err := s.Write(Verdict{Doc: sampleDoc(3), Kept: true}); err != nil {
		t.Fatalf("Write after the interval: %v", err)
	}
	if len(pushed) != 1 {
		t.Fatalf("%d parts pushed after the interval was up, want the one that was open: %v", len(pushed), pushed)
	}
	// The row that closed the part is in it, and the part after it is empty
	// until something else arrives, so a run that stops here pushes one more.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(pushed) != 1 {
		t.Errorf("closing pushed a part with no rows in it: %v", pushed)
	}
	if st := s.Stats(); st.Kept != 4 {
		t.Errorf("the sink counted %d kept, want 4", st.Kept)
	}
}

// The clock is per repo and it starts when a row arrives, so a crawl that keeps
// nothing for an hour does not push an empty part every interval.
func TestAnIdleRepoDoesNotPushEmptyParts(t *testing.T) {
	var pushed []string
	s := openSink(t, SinkOptions{
		PartEvery: time.Millisecond,
		Push: func(d store.Dataset, local, path string) error {
			pushed = append(pushed, d.Name)
			return nil
		},
	})
	for i := range 4 {
		v := Verdict{Doc: sampleDoc(i), Stage: StageSift, Reason: reject.ReasonShort, Detail: "too short"}
		if err := s.Write(v); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, name := range pushed {
		if name == KeptRepo {
			t.Fatalf("the kept repo, which took no rows, pushed a part: %v", pushed)
		}
	}
	if len(pushed) == 0 {
		t.Errorf("the rejects repo pushed nothing over four intervals")
	}
}

// A crawl is stopped and started again all the time, and the second run is
// carrying on rather than starting over, so it must not write over what the
// first one published.
func TestASinkCarriesOnFromWhereItStopped(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	push := func(d store.Dataset, local, path string) error {
		paths = append(paths, path)
		return nil
	}

	first := openSink(t, SinkOptions{Dir: dir, Record: true, BytesPerPart: 1, Push: push})
	if err := first.Write(Verdict{Doc: sampleDoc(1), Kept: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := first.Archive(visit("https://baodongthap.example/a", "<html></html>"), time.Now()); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := openSink(t, SinkOptions{Dir: dir, Record: true, BytesPerPart: 1, Push: push})
	if err := second.Write(Verdict{Doc: sampleDoc(2), Kept: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := second.Archive(visit("https://baodongthap.example/b", "<html></html>"), time.Now()); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(paths) != 2 {
		t.Fatalf("%d parts were pushed: %v", len(paths), paths)
	}
	if paths[0] == paths[1] {
		t.Fatalf("the second run wrote over the first run's part at %s", paths[0])
	}
	volumes, err := filepath.Glob(filepath.Join(dir, "warc", "*.warc.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 2 {
		t.Errorf("%d volumes on the disk, want one per run: %v", len(volumes), volumes)
	}
}

// The disk under a crawler is cache. A run told to keep two volumes keeps two.
func TestOldVolumesAreAgedOut(t *testing.T) {
	dir := t.TempDir()
	s := openSink(t, SinkOptions{Dir: dir, Record: true, Volume: 1, Keep: 2})
	for i := range 5 {
		if _, err := s.Archive(visit("https://baodongthap.example/tin.html", "<html></html>"), time.Now()); err != nil {
			t.Fatalf("Archive %d: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	volumes, err := filepath.Glob(filepath.Join(dir, "warc", "*.warc.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 2 {
		t.Errorf("%d volumes are on the disk, want the two the run was told to keep: %v", len(volumes), volumes)
	}
	if s.Stats().Aged != 3 {
		t.Errorf("the sink says it aged out %d volumes, want 3", s.Stats().Aged)
	}
}

// Keep counts the volumes on the disk and not the ones this process wrote. A
// crawler is restarted to take a new binary and restarted by its supervisor
// after the network goes, so a run that only ages out its own volumes gives the
// box back nothing at all across a night of restarts.
func TestARestartAgesOutTheVolumesItFound(t *testing.T) {
	dir := t.TempDir()
	for i := range 4 {
		s := openSink(t, SinkOptions{Dir: dir, Record: true, Volume: 1, Keep: 2})
		if _, err := s.Archive(visit("https://baodongthap.example/tin.html", "<html></html>"), time.Now()); err != nil {
			t.Fatalf("Archive on run %d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close on run %d: %v", i, err)
		}
	}
	volumes, err := filepath.Glob(filepath.Join(dir, "warc", "*.warc.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 2 {
		t.Errorf("%d volumes on the disk after four runs, want the two the box was told to keep: %v", len(volumes), volumes)
	}

	// Another shard's volumes are another box's archive even when they are in
	// the same directory, and a run that swept them up would delete them.
	other := filepath.Join(dir, "warc", "gaocrawl-20260819-00007-00000.warc.gz")
	if err := os.WriteFile(other, []byte("not this box's"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := openSink(t, SinkOptions{Dir: dir, Record: true, Volume: 1, Keep: 1})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("shard 0 deleted shard 7's volume: %v", err)
	}
}

func TestASinkSaysWhichRepoItCannotFind(t *testing.T) {
	if _, err := OpenSink(SinkOptions{Snapshot: "gaocrawl-20260819"}); err == nil {
		t.Error("a sink opened with no directory to write under")
	}
	if _, err := OpenSink(SinkOptions{Dir: t.TempDir()}); err == nil {
		t.Error("a sink opened with no snapshot, which is the partition its parts sit under")
	}
}

// The state file is what a restart reads, so it has to say what a person
// reading it expects.
func TestTheStateFileSaysWhereTheRunGotTo(t *testing.T) {
	dir := t.TempDir()
	s := openSink(t, SinkOptions{Dir: dir, BytesPerPart: 1, Push: func(store.Dataset, string, string) error { return nil }})
	if err := s.Write(Verdict{Doc: sampleDoc(1), Kept: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		t.Fatalf("reading the state file: %v", err)
	}
	var got sinkState
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("the state file is not JSON: %v", err)
	}
	if got.Kept != 1 {
		t.Errorf("the state says the next kept part is %d, want 1", got.Kept)
	}
}

func TestASinkThatIsClosedSaysSo(t *testing.T) {
	s := openSink(t, SinkOptions{})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Write(Verdict{Doc: sampleDoc(1), Kept: true}); err == nil {
		t.Error("a closed sink took a document")
	}
	if _, err := s.Archive(visit("https://baodongthap.example/a", ""), time.Now()); err == nil {
		t.Error("a closed sink archived a visit")
	}
	if err := s.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
}

// Every worker on the box writes to one sink at once, so what it does under that
// has to be right rather than only fast. This is the test the lock split is for:
// it runs the three streams together the way a crawl does and then reads the
// archive back record by record, which is where an interleaved WARC shows up.
func TestManyWorkersWritingAtOnceLeaveAReadableArchive(t *testing.T) {
	dir := t.TempDir()
	s := openSink(t, SinkOptions{Dir: dir, Record: true})

	const workers, each = 24, 20
	var wg sync.WaitGroup
	errs := make(chan error, workers*each*2)
	for w := range workers {
		wg.Go(func() {
			for i := range each {
				n := w*each + i
				body := fmt.Sprintf("<html><body><p>Bai viet so %d</p></body></html>", n)
				if _, err := s.Archive(visit(fmt.Sprintf("https://baodongthap.example/tin-%d.html", n), body), time.Now()); err != nil {
					errs <- err
					return
				}
				// Both repos, because the two used to share a lock with the
				// archive and now hold one each.
				if err := s.Write(Verdict{Doc: sampleDoc(n), Kept: n%2 == 0, Stage: StageSift, Reason: reject.ReasonShort}); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("writing from many workers: %v", err)
	}

	stats := s.Stats()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if want := int64(workers * each); stats.Archived != want {
		t.Errorf("archived %d visits, want %d", stats.Archived, want)
	}
	if got := stats.Kept + stats.Dropped; got != int64(workers*each) {
		t.Errorf("wrote %d rows, want %d", got, workers*each)
	}

	volumes, err := filepath.Glob(filepath.Join(dir, "warc", "*.warc.gz"))
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, v := range volumes {
		f, err := os.Open(v)
		if err != nil {
			t.Fatal(err)
		}
		r, err := harvest.NewWARCReader(f)
		if err != nil {
			t.Fatalf("%s would not open: %v", filepath.Base(v), err)
		}
		for {
			rec, err := r.Next()
			if errors.Is(err, harvest.ErrDone) {
				break
			}
			if err != nil {
				t.Fatalf("%s stopped reading after %d records: %v", filepath.Base(v), seen, err)
			}
			if rec.Type() == "response" {
				seen++
			}
		}
		_ = f.Close()
	}
	// One response record per visit, all of them readable in sequence. A record
	// whose bytes were interleaved with another worker's would have ended the
	// read above rather than arriving short.
	if seen != workers*each {
		t.Errorf("read %d response records back, want %d", seen, workers*each)
	}
}

// A verdict with no document is a bug in the caller and says so rather than
// writing a row of nothing.
func TestAVerdictWithNoDocumentIsRefused(t *testing.T) {
	s := openSink(t, SinkOptions{})
	defer func() { _ = s.Close() }()
	if err := s.Write(Verdict{Kept: true}); err == nil {
		t.Error("a verdict with no document was written")
	}
}

// Keep is how many volumes may sit on the disk, and a snapshot is not part of
// that question. A run started under a new snapshot to measure a frontier change
// left every box in the fleet over its limit inside a day, because each snapshot
// was aged separately and kept its own two. server1 was holding eight volumes
// against a keep of four.
func TestANewSnapshotDoesNotStartTheVolumeCountOver(t *testing.T) {
	dir := t.TempDir()
	for _, snapshot := range []string{"web-20260819", "web-20260820", "web-20260820b"} {
		for range 2 {
			s := openSink(t, SinkOptions{Dir: dir, Record: true, Snapshot: snapshot, Volume: 1, Keep: 2})
			if _, err := s.Archive(visit("https://baodongthap.example/tin.html", "<html></html>"), time.Now()); err != nil {
				t.Fatalf("Archive under %s: %v", snapshot, err)
			}
			if err := s.Close(); err != nil {
				t.Fatalf("Close under %s: %v", snapshot, err)
			}
		}
	}
	volumes, err := filepath.Glob(filepath.Join(dir, "warc", "*.warc.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 2 {
		t.Errorf("%d volumes across three snapshots, want the two the box was told to keep: %v", len(volumes), volumes)
	}
	// And the two left are the newest two, which is the whole point of aging
	// rather than deleting. The volume counter carries on across snapshots, so
	// the survivors are the highest numbered wherever their snapshot sorts.
	for _, v := range volumes {
		if volumeOf(v) < 4 {
			t.Errorf("aging kept %s and deleted something newer", filepath.Base(v))
		}
	}
}

// The part number is on the disk before the part is on the hub.
//
// It used to be written after. A run stopped in the window between the push and
// the save came back holding the number it had before, wrote the same path
// again, and on the hub a path written again replaces what is at it. That is not
// a gap in a dataset, it is rows that were published and are not any more.
// Thirteen parts across open-index/vitweb and open-index/vitweb-rejects have two
// Add commits at the same path, minutes apart, at the times the fleet was
// restarted to measure a frontier change.
func TestThePartNumberIsWrittenDownBeforeThePartIsPushed(t *testing.T) {
	dir := t.TempDir()

	var pushed []string
	var atPush []sinkState
	push := func(_ store.Dataset, _, path string) error {
		pushed = append(pushed, path)
		// What a run started at this instant would come back holding.
		b, err := os.ReadFile(filepath.Join(dir, stateFile))
		if err != nil {
			t.Errorf("reading %s during the push of %s: %v", stateFile, path, err)
			return nil
		}
		var st sinkState
		if err := json.Unmarshal(b, &st); err != nil {
			t.Errorf("reading %s during the push of %s: %v", stateFile, path, err)
			return nil
		}
		atPush = append(atPush, st)
		return nil
	}

	s := openSink(t, SinkOptions{Dir: dir, BytesPerPart: 1, Push: push})
	for i := range 3 {
		if err := s.Write(Verdict{Doc: sampleDoc(i), Kept: true}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(pushed) < 2 {
		t.Fatalf("%d parts were pushed and the test needs at least two: %v", len(pushed), pushed)
	}
	for i, path := range pushed {
		// The path carries the number the part was written under, and the state
		// at the moment of the push has to be past it, since that number is
		// spent.
		want := partNumber(t, path)
		if got := atPush[i].Kept; got <= want {
			t.Fatalf("%s was being pushed while %s still said the next kept part is %d, so a run killed here would write over it",
				path, stateFile, got)
		}
	}
}

// partNumber pulls the part number off a path like
// data/web/snapshot-00001-00034.parquet.
func partNumber(t *testing.T, path string) int {
	t.Helper()
	base := strings.TrimSuffix(filepath.Base(path), ".parquet")
	i := strings.LastIndex(base, "-")
	if i < 0 {
		t.Fatalf("%q is not a part path", path)
	}
	n, err := strconv.Atoi(base[i+1:])
	if err != nil {
		t.Fatalf("%q is not a part path: %v", path, err)
	}
	return n
}
