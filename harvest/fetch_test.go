package harvest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fleet"
)

// body is the content the test hosts serve. It is large enough that a cut can
// land in the middle of it and small enough to hash in a test.
var body = []byte(strings.Repeat("con co bay la bay la\n", 500))

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// host is an HTTP server that serves one file, honors Range, and can be told
// to drop the connection part way through the next few responses.
type host struct {
	mu       sync.Mutex
	content  []byte
	drops    int   // connections still to be cut short
	cut      int   // bytes to send before cutting
	cuts     []int // when set, the bytes to send on each cut in turn, then cut
	ignore   bool  // answer 200 and the whole file even to a Range request
	status   int   // when non-zero, the status to answer with instead
	ranges   []string
	auths    []string
	requests int
}

func (h *host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests++
	h.ranges = append(h.ranges, r.Header.Get("Range"))
	h.auths = append(h.auths, r.Header.Get("Authorization"))
	drop := h.drops > 0
	if drop {
		h.drops--
	}
	content, cut, ignore, status := h.content, h.cut, h.ignore, h.status
	if drop && len(h.cuts) > 0 {
		cut, h.cuts = h.cuts[0], h.cuts[1:]
	}
	h.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		return
	}

	start := 0
	if rng := r.Header.Get("Range"); rng != "" && !ignore {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"))
		if err != nil || n < 0 || n > len(content) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		start = n
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(content)-1, len(content)))
	}

	rest := content[start:]
	w.Header().Set("Content-Length", strconv.Itoa(len(rest)))
	if start > 0 {
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	if drop && cut < len(rest) {
		_, _ = w.Write(rest[:cut])
		// Flushed, so the client has the headers and a partial body before the
		// connection dies. Without this the whole response disappears and the
		// test would be exercising a failed request rather than a failed
		// transfer, which is a different thing with a different fix.
		w.(http.Flusher).Flush()
		// The abrupt close a real host gives when a link goes down, rather than
		// a clean end of body the client would read as a complete file.
		panic(http.ErrAbortHandler)
	}
	_, _ = w.Write(rest)
}

// toHost sends every request to the test server whatever address it names, so a
// genuine Hub pin can be fetched without going to the Hub.
type toHost struct {
	base string
	next http.RoundTripper
}

func (t toHost) RoundTrip(r *http.Request) (*http.Response, error) {
	u, err := url.Parse(t.base)
	if err != nil {
		return nil, err
	}
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
	return t.next.RoundTrip(r)
}

// quietLog swallows the server's own complaint about the aborted handler, which
// is the thing the test is deliberately causing.
func quietLog() *log.Logger { return log.New(io.Discard, "", 0) }

func (h *host) seen() (requests int, ranges, auths []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requests, append([]string(nil), h.ranges...), append([]string(nil), h.auths...)
}

// serveFile starts a host and returns it with a pin and file that address it.
func serveFile(t *testing.T, h *host) (*httptest.Server, Pinned, File) {
	t.Helper()
	if h.content == nil {
		h.content = body
	}
	// The abort is deliberate, so the server's own log of it is noise.
	s := httptest.NewUnstartedServer(h)
	s.Config.ErrorLog = quietLog()
	s.Start()
	t.Cleanup(s.Close)

	p := Pinned{
		Source:   doc.SourceHPLT3,
		Origin:   Direct,
		Repo:     s.URL,
		Revision: "sha256:" + strings.Repeat("a", 64),
	}
	return s, p, File{Path: "shard.jsonl.zst", Bytes: int64(len(h.content)), Digest: sha(h.content)}
}

func readAll(t *testing.T, b *Body) []byte {
	t.Helper()
	got, err := io.ReadAll(b)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return got
}

func TestAPinnedFileDownloadsAndVerifies(t *testing.T) {
	h := &host{}
	s, p, f := serveFile(t, h)

	fetch := &Fetcher{Client: s.Client(), Retries: -1}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = b.Close() }()

	if got := readAll(t, b); string(got) != string(body) {
		t.Errorf("the fetcher delivered %d bytes, want %d", len(got), len(body))
	}
	if !b.Done() {
		t.Error("a file that read to the end and verified does not report done")
	}
	if b.Digest() != f.Digest {
		t.Errorf("the fetcher hashed the file to %s, want %s", b.Digest(), f.Digest)
	}
	if b.Offset() != f.Bytes {
		t.Errorf("the fetcher read %d bytes, want %d", b.Offset(), f.Bytes)
	}
	if b.Reconnects() != 0 {
		t.Errorf("a transfer nothing interrupted reconnected %d times", b.Reconnects())
	}
}

// The case the retry logic exists for. A 26.6 GB shard over a link that fails
// every few hours has to finish, and it only finishes if a drop costs the
// remainder rather than the whole file.
func TestADroppedConnectionResumesWhereItStopped(t *testing.T) {
	h := &host{drops: 3, cut: 1000}
	s, p, f := serveFile(t, h)

	fetch := &Fetcher{Client: s.Client(), RetryWait: time.Millisecond}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = b.Close() }()

	got := readAll(t, b)
	if string(got) != string(body) {
		t.Fatalf("the resumed file is %d bytes, want %d", len(got), len(body))
	}
	// The digest is the real assertion. A resume that re-read the first bytes
	// would deliver the right length and the wrong hash.
	if b.Digest() != f.Digest {
		t.Errorf("the resumed file hashes to %s, want %s", b.Digest(), f.Digest)
	}
	if b.Reconnects() != 3 {
		t.Errorf("the fetcher reconnected %d times, want 3", b.Reconnects())
	}

	requests, ranges, _ := h.seen()
	if requests != 4 {
		t.Errorf("the fetcher made %d requests, want 4", requests)
	}
	if ranges[0] != "" {
		t.Errorf("the first request asked for a range: %q", ranges[0])
	}
	for i, want := range []string{"bytes=1000-", "bytes=2000-", "bytes=3000-"} {
		if ranges[i+1] != want {
			t.Errorf("request %d asked for %q, want %q", i+2, ranges[i+1], want)
		}
	}
}

// A host that answers a Range request with the whole file again is not a slow
// path to be tolerated. Reading it would double-hash the first bytes and report
// a file larger than the one that exists.
func TestAHostThatIgnoresRangeIsAFailureRatherThanCorruption(t *testing.T) {
	h := &host{drops: 1, cut: 1000, ignore: true}
	s, p, f := serveFile(t, h)

	fetch := &Fetcher{Client: s.Client(), RetryWait: time.Millisecond}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = b.Close() }()

	_, err = io.ReadAll(b)
	if err == nil {
		t.Fatal("the fetcher accepted a host that restarted the file")
	}
	if !strings.Contains(err.Error(), "restarted the file at byte 0") {
		t.Errorf("the error does not say what the host did: %v", err)
	}
	if b.Done() {
		t.Error("a file the fetcher refused reads as done")
	}
}

func TestATruncatedFileDoesNotReadAsAWholeOne(t *testing.T) {
	h := &host{}
	s, p, f := serveFile(t, h)
	// The host is fine and the manifest expects more than exists, which is what
	// a file that shrank upstream looks like from here.
	f.Bytes += 1000

	fetch := &Fetcher{Client: s.Client(), Retries: -1}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = b.Close() }()

	_, err = io.ReadAll(b)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("reading a short file returned %v, want ErrTruncated", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(len(body))) {
		t.Errorf("the error does not say how much arrived: %v", err)
	}
}

func TestAFileThatHashesWrongIsRefused(t *testing.T) {
	h := &host{}
	s, p, f := serveFile(t, h)
	f.Digest = sha([]byte("something else entirely"))

	fetch := &Fetcher{Client: s.Client(), Retries: -1}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = b.Close() }()

	_, err = io.ReadAll(b)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("reading a file with the wrong hash returned %v, want ErrDigestMismatch", err)
	}
	// Both hashes have to be in the message, because the person reading it is
	// deciding whether upstream changed or the transfer broke.
	for _, want := range []string{f.Digest, sha(body)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %s: %v", want, err)
		}
	}
}

// HPLT publishes no digests and the Hub withholds them for gated repos, so a
// third of the manifest arrives with nothing to check against. The fetcher still
// produces a digest, which is what makes the second fetch checkable.
func TestAFileWithNoPinnedDigestStillProducesOne(t *testing.T) {
	h := &host{}
	s, p, f := serveFile(t, h)
	f.Digest = ""

	fetch := &Fetcher{Client: s.Client(), Retries: -1}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = b.Close() }()

	readAll(t, b)
	if !b.Done() {
		t.Fatal("a file with no pinned digest did not verify")
	}
	if b.Digest() != sha(body) {
		t.Errorf("the computed digest is %s, want %s", b.Digest(), sha(body))
	}
}

func TestAGatedSourceSaysWhatToDoAboutIt(t *testing.T) {
	h := &host{status: http.StatusForbidden}
	s, p, f := serveFile(t, h)
	p.Source, p.Gated = doc.SourceCulturaX, true

	fetch := &Fetcher{Client: s.Client(), Retries: -1}
	_, err := fetch.Open(t.Context(), p, f)
	if !errors.Is(err, ErrGated) {
		t.Fatalf("a gated source that answered 403 returned %v, want ErrGated", err)
	}
	// The message has to carry the two things somebody needs to act: the page
	// where the terms are accepted and the variable the token goes in.
	if !strings.Contains(err.Error(), fleet.TokenEnv) {
		t.Errorf("the error does not name %s: %v", fleet.TokenEnv, err)
	}
	if !strings.Contains(err.Error(), p.Page()) {
		t.Errorf("the error does not link the page with the terms on it: %v", err)
	}
}

// A 403 from a source that is not gated is a broken host, and calling it a
// permissions problem would send whoever reads it looking for a token they do
// not need.
func TestANotGatedSourceThatRefusesIsNotReportedAsGated(t *testing.T) {
	h := &host{status: http.StatusForbidden}
	s, p, f := serveFile(t, h)

	fetch := &Fetcher{Client: s.Client(), Retries: -1}
	_, err := fetch.Open(t.Context(), p, f)
	if err == nil {
		t.Fatal("a 403 was accepted")
	}
	if errors.Is(err, ErrGated) {
		t.Errorf("a source that is not gated reported as gated: %v", err)
	}
}

func TestTheTokenGoesToTheHubAndNowhereElse(t *testing.T) {
	h := &host{}
	s, p, f := serveFile(t, h)

	fetch := &Fetcher{Client: s.Client(), Token: "hf_secret", Retries: -1}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	readAll(t, b)
	_ = b.Close()

	_, _, auths := h.seen()
	if auths[0] != "" {
		// HPLT is somebody else's host and our Hub token is not their business.
		t.Errorf("a direct source was sent %q", auths[0])
	}

	hub := Pinned{
		Source:   doc.SourceFineWeb2,
		Origin:   Hub,
		Repo:     "HuggingFaceFW/fineweb-2",
		Revision: strings.Repeat("b", 40),
	}
	// A real Hub address, pointed at the test server by the transport, because
	// the thing under test is what the fetcher does with a hub pin.
	fetch.Client = &http.Client{Transport: toHost{base: s.URL, next: s.Client().Transport}}
	b, err = fetch.Open(t.Context(), hub, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	readAll(t, b)
	_ = b.Close()

	_, _, auths = h.seen()
	if auths[1] != "Bearer hf_secret" {
		t.Errorf("a hub source was sent %q", auths[1])
	}
}

// A host that answers the range request and then sends nothing is not a link
// that keeps going down. Nothing about the file is moving, so the reconnects
// buy nothing and the budget runs out.
func TestAHostThatStopsSendingIsGivenUpOn(t *testing.T) {
	// Three hundred bytes on the first connection and nothing on any of the
	// ones after it, which is a transfer that started and then stopped rather
	// than one that never started.
	h := &host{drops: 100, cuts: []int{300}}
	s, p, f := serveFile(t, h)

	fetch := &Fetcher{Client: s.Client(), Retries: 2, RetryWait: time.Millisecond}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = b.Close() }()

	_, err = io.ReadAll(b)
	if err == nil {
		t.Fatal("a host that never finished the file reported success")
	}
	// The byte it stopped at is the useful part of the message: it says whether
	// the transfer was making progress or failing at the start every time.
	for _, want := range []string{"gave up at byte 300", "2 reconnects", "moved nothing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// The case the whole retry budget exists for, and the one it got wrong until a
// real run found it. A link that drops every few gigabytes spends more than
// five reconnects on a 26 GB file and every one of them is followed by hours of
// working transfer, so a budget counted over the life of the file abandons a
// download that is nearly finished. Counted consecutively, the file lands.
func TestATransferThatKeepsMovingKeepsItsBudget(t *testing.T) {
	// Eight drops against a budget of two. Every reconnect is followed by a
	// thousand bytes, so none of them is a stall.
	h := &host{drops: 8, cut: 1000}
	s, p, f := serveFile(t, h)

	fetch := &Fetcher{Client: s.Client(), Retries: 2, RetryWait: time.Millisecond}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = b.Close() }()

	got := readAll(t, b)
	if string(got) != string(body) {
		t.Fatalf("the file came out %d bytes, want %d", len(got), len(body))
	}
	if b.Digest() != f.Digest {
		t.Errorf("the file hashes to %s, want %s", b.Digest(), f.Digest)
	}
	if b.Reconnects() != 8 {
		t.Errorf("the fetcher reconnected %d times, want 8", b.Reconnects())
	}
	if !b.Done() {
		t.Error("a file that arrived whole and verified does not read as done")
	}
}

// The other half of the consecutive rule. A host that hands out a byte per
// connection makes progress by that definition every time and would reset the
// budget forever, so the total is capped as well.
func TestATransferThatBarelyMovesStillStops(t *testing.T) {
	h := &host{drops: 1000, cut: 1}
	s, p, f := serveFile(t, h)

	fetch := &Fetcher{Client: s.Client(), Retries: 1, RetryWait: time.Microsecond}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = b.Close() }()

	_, err = io.ReadAll(b)
	if err == nil {
		t.Fatal("a host sending a byte at a time finished the file")
	}
	if !strings.Contains(err.Error(), "ceiling") {
		t.Errorf("the error does not say the ceiling was reached: %v", err)
	}
	if b.Reconnects() != RetryCeiling {
		t.Errorf("the fetcher reconnected %d times, want the ceiling of %d", b.Reconnects(), RetryCeiling)
	}
}

// A canceled caller is not a flaky host. Reconnecting for one would turn a
// ctrl-C into a retry storm against somebody else's server.
func TestACanceledFetchDoesNotReconnect(t *testing.T) {
	h := &host{drops: 100, cut: 100}
	s, p, f := serveFile(t, h)

	ctx, cancel := context.WithCancel(t.Context())
	fetch := &Fetcher{Client: s.Client(), RetryWait: time.Hour}
	b, err := fetch.Open(ctx, p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = b.Close() }()

	cancel()
	if _, err := io.ReadAll(b); !errors.Is(err, context.Canceled) {
		t.Errorf("reading a canceled fetch returned %v, want context.Canceled", err)
	}

	before, _, _ := h.seen()
	if before > 1 {
		t.Errorf("a canceled fetch made %d requests", before)
	}
}

func TestAClosedBodyStopsBeingReadable(t *testing.T) {
	h := &host{}
	s, p, f := serveFile(t, h)

	fetch := &Fetcher{Client: s.Client(), Retries: -1}
	b, err := fetch.Open(t.Context(), p, f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := b.Read(make([]byte, 8)); err == nil {
		t.Error("a closed body kept reading")
	}
	if b.Done() {
		t.Error("a body closed before the end reads as done")
	}
	// Close is what a deferred cleanup calls, so calling it twice has to be
	// harmless rather than a nil dereference on a failed run.
	if err := b.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
}

func TestOpenReportsWhatWentWrongRatherThanGuessing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{"the file is gone", http.StatusNotFound, "404"},
		{"the host is down", http.StatusBadGateway, "502"},
		{"the host is rate limiting", http.StatusTooManyRequests, "429"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &host{status: tc.status}
			s, p, f := serveFile(t, h)
			fetch := &Fetcher{Client: s.Client(), Retries: -1}
			_, err := fetch.Open(t.Context(), p, f)
			if err == nil {
				t.Fatal("Open reported no error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not mention %s: %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), f.Path) {
				t.Errorf("the error does not say which file: %v", err)
			}
		})
	}

	fetch := &Fetcher{Retries: -1}
	if _, err := fetch.Open(t.Context(), Pinned{Origin: Direct, Repo: "://not a url"}, File{Path: "x"}); err == nil {
		t.Error("Open accepted an address that is not one")
	}
}

func TestTheFetcherDefaultsAreUsableOnTheirOwn(t *testing.T) {
	var f Fetcher
	if f.client() != http.DefaultClient {
		t.Error("a fetcher with no client does not use the default one")
	}
	if f.retries() != DefaultRetries {
		t.Errorf("a fetcher with no retry count uses %d", f.retries())
	}
	if f.retryWait() != DefaultRetryWait {
		t.Errorf("a fetcher with no backoff uses %s", f.retryWait())
	}
	if (&Fetcher{Retries: -1}).retries() != 0 {
		t.Error("a negative retry count is not no retries")
	}
	if (&Fetcher{Retries: 3}).retries() != 3 {
		t.Error("an explicit retry count is not honored")
	}
	if f.ceiling() != DefaultRetries*RetryCeiling {
		t.Errorf("the default ceiling is %d reconnects", f.ceiling())
	}
	// A fetcher that does not retry has nothing to cap. Were the ceiling
	// computed some other way it would hand a no-retry fetcher a hundred of
	// them, which is the opposite of what it was set to.
	if (&Fetcher{Retries: -1}).ceiling() != 0 {
		t.Error("a fetcher with no retries has a ceiling above zero")
	}
}
