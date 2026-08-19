package harvest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fleet"
)

// ranger is a host that answers closed range requests, which is the only kind a
// [RangeAt] sends. It is separate from the host in fetch_test.go because that one
// answers the open ended ranges a resumed stream sends, and a test host that
// guesses which it was given would be testing the guess.
type ranger struct {
	mu sync.Mutex

	content []byte
	ignore  bool // answer 200 with the whole file, as a host with no range support does
	status  int  // when non-zero, answer with this instead
	short   int  // responses still to be cut short
	cut     int  // bytes to send before cutting one short

	got  []string
	nreq int
}

func (h *ranger) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.nreq++
	h.got = append(h.got, r.Header.Get("Range"))
	content, ignore, status := h.content, h.ignore, h.status
	short := h.short > 0
	if short {
		h.short--
	}
	cut := h.cut
	h.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		return
	}
	if ignore {
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
		return
	}

	first, last, ok := parseRange(r.Header.Get("Range"), len(content))
	if !ok {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	part := content[first : last+1]

	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, last, len(content)))
	w.Header().Set("Content-Length", strconv.Itoa(len(part)))
	w.WriteHeader(http.StatusPartialContent)
	if short && cut < len(part) {
		_, _ = w.Write(part[:cut])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	_, _ = w.Write(part)
}

func parseRange(s string, size int) (first, last int, ok bool) {
	spec, found := strings.CutPrefix(s, "bytes=")
	if !found {
		return 0, 0, false
	}
	lo, hi, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false
	}
	first, err := strconv.Atoi(lo)
	if err != nil {
		return 0, 0, false
	}
	last, err = strconv.Atoi(hi)
	if err != nil || first < 0 || first >= size || last < first {
		return 0, 0, false
	}
	if last >= size {
		last = size - 1
	}
	return first, last, true
}

func (h *ranger) seen() (int, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nreq, append([]string(nil), h.got...)
}

// bigBody is content long enough to span several windows at the size the tests
// use, with a pattern that makes a byte out of place obvious.
func bigBody(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

// serveRanges starts a range answering host and returns a reader over it, with
// windows small enough that a test can cross one in a few kilobytes.
func serveRanges(t *testing.T, h *ranger, window, keep int) (*ranger, *RangeAt) {
	t.Helper()
	if h.content == nil {
		h.content = bigBody(64 << 10)
	}
	s := httptest.NewUnstartedServer(h)
	s.Config.ErrorLog = quietLog()
	s.Start()
	t.Cleanup(s.Close)

	p := Pinned{
		Source:   doc.SourceFineWeb2,
		Origin:   Direct,
		Repo:     s.URL,
		Revision: "sha256:" + strings.Repeat("a", 64),
	}
	f := File{Path: "data/vie_Latn/train/000_00000.parquet", Bytes: int64(len(h.content))}

	r, err := (&Fetcher{RetryWait: time.Nanosecond}).OpenAt(t.Context(), p, f)
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	if window > 0 {
		r.window, r.keep = window, keep
	}
	return h, r
}

func TestAReadOutOfOrderReturnsWhatIsAtThatOffset(t *testing.T) {
	h, r := serveRanges(t, &ranger{}, 4<<10, 4)

	for _, off := range []int64{0, 17, 4095, 4096, 9000, int64(len(h.content)) - 10} {
		want := h.content[off:min(off+64, int64(len(h.content)))]
		got := make([]byte, len(want))
		n, err := r.ReadAt(got, off)
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("ReadAt at %d: %v", off, err)
		}
		if n != len(want) || string(got[:n]) != string(want) {
			t.Errorf("ReadAt at %d returned %d bytes, %q", off, n, got[:n])
		}
	}
	if r.Size() != int64(len(h.content)) {
		t.Errorf("Size is %d, want %d", r.Size(), len(h.content))
	}
}

// The whole reason for reading in windows. A Parquet reader asks for a page
// header, then a page, then the next header, and one request per ask would be
// tens of thousands of round trips for one file.
func TestManyReadsInsideOneWindowCostOneRequest(t *testing.T) {
	h, r := serveRanges(t, &ranger{}, 4<<10, 4)

	for off := int64(0); off < 4<<10; off += 64 {
		if _, err := r.ReadAt(make([]byte, 64), off); err != nil {
			t.Fatalf("ReadAt at %d: %v", off, err)
		}
	}
	n, ranges := h.seen()
	if n != 1 {
		t.Errorf("64 reads inside one window cost %d requests: %v", n, ranges)
	}
	if want := "bytes=0-4095"; ranges[0] != want {
		t.Errorf("the request was %q, want %q", ranges[0], want)
	}
	if r.Requests() != 1 || r.Bytes() != 4<<10 {
		t.Errorf("the reader reports %d requests and %d bytes", r.Requests(), r.Bytes())
	}
}

// A row group is read one column at a time with as many live positions as the
// schema is wide, and a cache of one would be evicted by every column in turn.
func TestEveryColumnBeingReadAtOnceKeepsItsOwnWindow(t *testing.T) {
	const window, columns = 4 << 10, 4
	h, r := serveRanges(t, &ranger{}, window, columns)

	// One position per column, each in a window of its own, read round robin the
	// way a reader assembling rows moves through them.
	for range 8 {
		for col := range columns {
			off := int64(col) * window
			if _, err := r.ReadAt(make([]byte, 16), off); err != nil {
				t.Fatalf("ReadAt at %d: %v", off, err)
			}
		}
	}
	if n, ranges := h.seen(); n != columns {
		t.Errorf("%d columns read round robin cost %d requests: %v", columns, n, ranges)
	}

	// One more position than there are windows, and the least recently used one
	// goes, which is the behavior that makes the count worth setting.
	if _, err := r.ReadAt(make([]byte, 16), columns*window); err != nil {
		t.Fatalf("ReadAt past the kept windows: %v", err)
	}
	if _, err := r.ReadAt(make([]byte, 16), 0); err != nil {
		t.Fatalf("ReadAt at 0: %v", err)
	}
	if n, _ := h.seen(); n != columns+2 {
		t.Errorf("the evicted window was not refetched, %d requests", n)
	}
}

func TestAReadPastTheEndOfTheFileIsTheEndOfTheFile(t *testing.T) {
	h, r := serveRanges(t, &ranger{}, 4<<10, 4)
	size := int64(len(h.content))

	if n, err := r.ReadAt(make([]byte, 16), size); n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("a read at the end returned %d, %v", n, err)
	}
	if n, err := r.ReadAt(make([]byte, 16), size+1000); n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("a read past the end returned %d, %v", n, err)
	}
	if _, err := r.ReadAt(make([]byte, 16), -1); err == nil {
		t.Error("a negative offset was accepted")
	}
	if n, err := r.ReadAt(nil, 0); n != 0 || err != nil {
		t.Errorf("an empty read returned %d, %v", n, err)
	}

	// A read that runs off the end is served short with io.EOF, which is what
	// [io.ReaderAt] asks for and what a Parquet reader probing the tail does.
	p := make([]byte, 64)
	n, err := r.ReadAt(p, size-10)
	if n != 10 || !errors.Is(err, io.EOF) {
		t.Fatalf("a read over the end returned %d, %v", n, err)
	}
	if string(p[:n]) != string(h.content[size-10:]) {
		t.Errorf("the last bytes came back as %q", p[:n])
	}
}

// A read larger than a window would evict everything held to cache something
// nothing will ask for again, which is what a Parquet reader does when it pulls
// a whole column chunk.
func TestAReadLargerThanAWindowGoesStraightToTheHost(t *testing.T) {
	h, r := serveRanges(t, &ranger{}, 4<<10, 4)

	p := make([]byte, 16<<10)
	if _, err := r.ReadAt(p, 1000); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(p) != string(h.content[1000:1000+len(p)]) {
		t.Error("the large read returned the wrong bytes")
	}
	n, ranges := h.seen()
	if n != 1 {
		t.Errorf("a read of four windows cost %d requests: %v", n, ranges)
	}
	if want := "bytes=1000-17383"; ranges[0] != want {
		t.Errorf("the request was %q, want %q", ranges[0], want)
	}
	if len(r.windows) != 0 {
		t.Errorf("the large read cached %d windows", len(r.windows))
	}
}

// Taking the whole file would mean several gigabytes moved to satisfy a read of
// four megabytes, once per window, so it is a failure rather than a slow path.
func TestAHostThatWillNotAnswerARangeRequestIsAFailure(t *testing.T) {
	h, r := serveRanges(t, &ranger{ignore: true}, 4<<10, 4)

	_, err := r.ReadAt(make([]byte, 16), 0)
	if !errors.Is(err, ErrNoRange) {
		t.Fatalf("ReadAt returned %v, want ErrNoRange", err)
	}
	// Answered the same way on every retry, so retrying is just noise on
	// somebody else's server.
	if n, _ := h.seen(); n != 1 {
		t.Errorf("a host with no range support was asked %d times", n)
	}
}

func TestATruncatedRangeIsRetriedRatherThanTakenAsTheAnswer(t *testing.T) {
	h, r := serveRanges(t, &ranger{short: 2, cut: 100}, 4<<10, 4)

	p := make([]byte, 512)
	if _, err := r.ReadAt(p, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(p) != string(h.content[:512]) {
		t.Error("the retried read returned the wrong bytes")
	}
	if n, _ := h.seen(); n != 3 {
		t.Errorf("two short answers took %d requests to get past", n)
	}
}

func TestAHostThatKeepsTruncatingIsGivenUpOn(t *testing.T) {
	h, r := serveRanges(t, &ranger{short: 100, cut: 10}, 4<<10, 4)

	_, err := r.ReadAt(make([]byte, 512), 0)
	if !errors.Is(err, ErrShortRange) {
		t.Fatalf("ReadAt returned %v, want ErrShortRange", err)
	}
	if !strings.Contains(err.Error(), "gave up") {
		t.Errorf("the error does not say it gave up: %v", err)
	}
	if n, _ := h.seen(); n != DefaultRetries+1 {
		t.Errorf("a host that always truncates was asked %d times", n)
	}
}

// The message has to say what to do about it, because a 403 on a gated source is
// a missing agreement and not a bug.
func TestAGatedSourceReadOutOfOrderSaysWhatToDoAboutIt(t *testing.T) {
	_, r := serveRanges(t, &ranger{status: http.StatusForbidden}, 4<<10, 4)
	r.remote.Gated = true
	r.remote.From = string(doc.SourceCulturaX)
	r.remote.Page = "https://huggingface.co/datasets/uonlp/CulturaX"

	_, err := r.ReadAt(make([]byte, 16), 0)
	if !errors.Is(err, ErrGated) {
		t.Fatalf("ReadAt returned %v, want ErrGated", err)
	}
	if !strings.Contains(err.Error(), fleet.TokenEnv) {
		t.Errorf("the error does not name the token variable: %v", err)
	}
}

func TestASourceThatIsNotGatedAndRefusesIsNotReportedAsGated(t *testing.T) {
	_, r := serveRanges(t, &ranger{status: http.StatusForbidden}, 4<<10, 4)

	_, err := r.ReadAt(make([]byte, 16), 0)
	if err == nil || errors.Is(err, ErrGated) {
		t.Fatalf("ReadAt returned %v", err)
	}
}

func TestAFileWithNoPinnedSizeCannotBeReadOutOfOrder(t *testing.T) {
	f := &Fetcher{}
	p := Pinned{Source: doc.SourceFineWeb2, Origin: Direct, Repo: "https://example.invalid"}

	_, err := f.OpenAt(t.Context(), p, File{Path: "shard.parquet"})
	if err == nil || !strings.Contains(err.Error(), "no pinned size") {
		t.Fatalf("OpenAt returned %v", err)
	}
	// The name is in the message because a manifest with a missing size has one
	// bad line and the rest are fine.
	if err != nil && !strings.Contains(err.Error(), "shard.parquet") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

// A Parquet reader reads its columns from several goroutines at once, so this is
// the ordinary case rather than an unusual one.
func TestTheSameWindowFetchedFromTwoGoroutinesIsFetchedOnce(t *testing.T) {
	h, r := serveRanges(t, &ranger{}, 4<<10, 8)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := make([]byte, 64)
			if _, err := r.ReadAt(p, int64(i)*8); err != nil {
				t.Errorf("ReadAt: %v", err)
				return
			}
			if string(p) != string(h.content[i*8:i*8+64]) {
				t.Errorf("goroutine %d read the wrong bytes", i)
			}
		}()
	}
	wg.Wait()

	// All eight positions are inside the first window, so at most one of the
	// races that lost gets to keep its copy, and none of them gets a wrong one.
	if len(r.windows) != 1 {
		t.Errorf("eight concurrent reads of one window left %d windows", len(r.windows))
	}
}

func TestACanceledReadStops(t *testing.T) {
	h, r := serveRanges(t, &ranger{short: 100, cut: 10}, 4<<10, 4)

	ctx, cancel := context.WithCancel(t.Context())
	r.ctx = ctx
	cancel()

	if _, err := r.ReadAt(make([]byte, 512), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadAt returned %v, want context.Canceled", err)
	}
	if n, _ := h.seen(); n != 0 {
		t.Errorf("a canceled read made %d requests", n)
	}
}

var _ io.ReaderAt = (*RangeAt)(nil)
