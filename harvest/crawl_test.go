package harvest_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/harvest"
	"github.com/tamnd/gao/reject"
)

// Every test here is written from the server's side. The thing worth asserting
// about a crawler is not what it returned but what arrived at the site, so the
// test server writes down every request and the checks are mostly on that list.
// A test that only inspected the error would pass just as well for a fetcher
// that made the request and then discarded it.

type site struct {
	*httptest.Server

	routes map[string]http.HandlerFunc

	mu     sync.Mutex
	hits   []string
	agents []string
}

func newSite(t *testing.T, routes map[string]http.HandlerFunc) *site {
	t.Helper()
	s := &site{routes: routes}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *site) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.hits = append(s.hits, r.URL.RequestURI())
	s.agents = append(s.agents, r.Header.Get("User-Agent"))
	s.mu.Unlock()

	if h, ok := s.routes[r.URL.Path]; ok {
		h(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *site) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *site) askedFor(path string) int {
	n := 0
	for _, hit := range s.asked() {
		if hit == path {
			n++
		}
	}
	return n
}

func (s *site) agent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.agents) == 0 {
		return ""
	}
	return s.agents[0]
}

func (s *site) host(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// text is the ordinary route: a page that exists.
func text(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

// crawler builds one against the fake clock, so that a test of a thirty second
// crawl delay finishes in the time it takes to do the arithmetic.
func crawler(t *testing.T, o harvest.CrawlOptions) (*harvest.Crawler, *clock) {
	t.Helper()
	c := newClock()
	if o.Polite == nil {
		o.Polite = polite(c, harvest.PoliteOptions{})
	}
	if o.Client == nil {
		o.Client = &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return harvest.NewCrawler(o), c
}

func get(t *testing.T, c *harvest.Crawler, target string) (*harvest.Visit, error) {
	t.Helper()
	return c.Get(context.Background(), target)
}

// The commitment in the README, as a test. A site that blocks us is not asked
// again, and the way to check that is that nothing arrives, not that an error
// came back.
func TestABlockIsAStop(t *testing.T) {
	blocked := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt":  text("User-agent: *\nAllow: /\n"),
		"/tin-tuc/mot": blocked,
		"/tin-tuc/hai": text("<p>xin chào</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	if _, err := get(t, c, s.URL+"/tin-tuc/mot"); !errors.Is(err, harvest.ErrBlocked) {
		t.Fatalf("a 403 gave %v, and the crawl carries on", err)
	}

	// The second URL is on a path the site allowed and would have served. It
	// must not be asked for, because the host already said no to us.
	if _, err := get(t, c, s.URL+"/tin-tuc/hai"); !errors.Is(err, harvest.ErrBlocked) {
		t.Fatalf("the second URL on a blocked host gave %v", err)
	}
	if n := s.askedFor("/tin-tuc/hai"); n != 0 {
		t.Errorf("the host blocked us and we asked it for another page %d times", n)
	}
	if why, ok := c.Blocked(s.host(t)); !ok || !strings.Contains(why, "403") {
		t.Errorf("the block was recorded as %q, %v", why, ok)
	}
	if _, blockedHosts := c.Hosts(); blockedHosts != 1 {
		t.Errorf("%d hosts recorded as blocking us", blockedHosts)
	}
}

// A disallowed path is not fetched, which means the check happens before the
// request and not after it. The only thing the site should see is the robots.txt
// it wrote to stop us.
func TestADisallowedPathIsNeverRequested(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt":       text("User-agent: gaobot\nDisallow: /thanh-vien/\n"),
		"/thanh-vien/nguoi": text("<p>ho so</p>"),
		"/tin-tuc/bai":      text("<p>bai viet</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	_, err := get(t, c, s.URL+"/thanh-vien/nguoi")
	if !errors.Is(err, harvest.ErrDeclined) {
		t.Fatalf("a disallowed path gave %v", err)
	}
	if !strings.Contains(err.Error(), "Disallow: /thanh-vien/") {
		t.Errorf("the refusal does not name the rule that caused it: %v", err)
	}
	if n := s.askedFor("/thanh-vien/nguoi"); n != 0 {
		t.Errorf("robots.txt disallowed the path and it was requested %d times", n)
	}

	// The rest of the site is still ours, and a fetcher that read the file and
	// then stopped entirely would be as wrong as one that ignored it.
	v, err := get(t, c, s.URL+"/tin-tuc/bai")
	if err != nil {
		t.Fatalf("an allowed path gave %v", err)
	}
	if v.Robots.Why != harvest.RobotsAllowDefault && v.Robots.Why != harvest.RobotsAllow {
		t.Errorf("the visit records the decision as %q", v.Robots.Why)
	}
}

// The query string is part of what the file is matched against, because a rule
// that names one is a rule about the pages behind it.
func TestARuleNamingAQueryStringStopsTheRequest(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nDisallow: /tim-kiem?\n"),
		"/tim-kiem":   text("<p>ket qua</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	if _, err := get(t, c, s.URL+"/tim-kiem?q=lua+gao"); !errors.Is(err, harvest.ErrDeclined) {
		t.Fatalf("a search URL the site disallowed gave %v", err)
	}
	if n := s.askedFor("/tim-kiem?q=lua+gao"); n != 0 {
		t.Errorf("the search URL was requested %d times", n)
	}
}

// robots.txt is read once. A crawler that read it per URL would spend half its
// budget on one file and would still be reading a file that had not changed.
func TestRobotsIsReadOncePerHost(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		"/a":          text("a"),
		"/b":          text("b"),
		"/c":          text("c"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	for _, path := range []string{"/a", "/b", "/c"} {
		if _, err := get(t, c, s.URL+path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if n := s.askedFor("/robots.txt"); n != 1 {
		t.Errorf("robots.txt was fetched %d times for three pages", n)
	}
	if seen, _ := c.Hosts(); seen != 1 {
		t.Errorf("%d hosts tracked after crawling one", seen)
	}
}

// The header the contact page tells people to look for is the header the site
// actually receives, on the robots.txt request as much as on the page.
func TestTheSiteSeesTheAgentWePublished(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		"/bai-viet":   text("<p>noi dung</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{Version: "0.4.1"})

	if _, err := get(t, c, s.URL+"/bai-viet"); err != nil {
		t.Fatal(err)
	}
	// The first request is the one for robots.txt, and it carries the same
	// header as the page. A crawler that identified itself only on the requests
	// somebody was likely to look at would be doing something else.
	if got := s.agent(); got != harvest.Agent("0.4.1") {
		t.Errorf("the request for robots.txt arrived as %q and the contact page promises %q", got, harvest.Agent("0.4.1"))
	}
	if !strings.Contains(s.agent(), harvest.Contact) {
		t.Errorf("the request arrived as %q, with nowhere to complain to", s.agent())
	}
}

// A redirect is a URL, and a URL goes back to the frontier where something
// decides whether it may be asked for. Following it here would make a request
// that no robots.txt had been consulted about, which is the whole failure this
// is written to prevent.
func TestARedirectIsHandedBackRatherThanFollowed(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: gaobot\nDisallow: /moi/\n"),
		"/cu": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/moi/bai-viet", http.StatusMovedPermanently)
		},
		"/moi/bai-viet": text("<p>noi dung</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	v, err := get(t, c, s.URL+"/cu")
	if err != nil {
		t.Fatalf("a redirect came back as an error: %v", err)
	}
	if v.Status != http.StatusMovedPermanently {
		t.Errorf("the redirect arrived as status %d", v.Status)
	}
	if want := s.URL + "/moi/bai-viet"; v.Redirect != want {
		t.Errorf("the visit carries %q as the next URL and the header said %q", v.Redirect, want)
	}
	if n := s.askedFor("/moi/bai-viet"); n != 0 {
		t.Errorf("the redirect was followed into a disallowed path %d times", n)
	}

	// And the URL it handed back is refused when it does come round again,
	// which is the point of handing it back rather than following it.
	if _, err := get(t, c, v.Redirect); !errors.Is(err, harvest.ErrDeclined) {
		t.Errorf("the redirect target was fetched on a second pass: %v", err)
	}
}

// A site's own Crawl-delay is read at the same time as its rules, and it is the
// schedule that has to change, not a number written down somewhere.
func TestTheSitesCrawlDelayReachesTheSchedule(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nCrawl-delay: 30\n"),
		"/a":          text("a"),
		"/b":          text("b"),
	})
	c, clk := crawler(t, harvest.CrawlOptions{})

	for _, path := range []string{"/a", "/b"} {
		if _, err := get(t, c, s.URL+path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}

	// Four requests went out: robots.txt, the well known file, then two pages.
	// The first waited for nothing and every one after it waited the thirty
	// seconds the site asked for, including ours. The delay is read out of the
	// first file, so it applies to everything from the second request on.
	waits := clk.waits()
	if len(waits) != 4 {
		t.Fatalf("the schedule was consulted %d times for four requests: %v", len(waits), waits)
	}
	for i, w := range waits[1:] {
		if w != 30*time.Second {
			t.Errorf("request %d waited %v and the site asked for 30s", i+1, w)
		}
	}
}

// Above a few minutes a Crawl-delay stops being a schedule. The host is reported
// rather than queued, and it is reported without another request going out.
func TestAHostAskingForTooLongIsNotCrawled(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: gaobot\nCrawl-delay: 3600\n"),
		"/bai":        text("<p>noi dung</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	_, err := get(t, c, s.URL+"/bai")
	if !errors.Is(err, harvest.ErrBusy) {
		t.Fatalf("an hour between requests gave %v", err)
	}
	if n := s.askedFor("/bai"); n != 0 {
		t.Errorf("the page was fetched %d times from a host we cannot crawl politely", n)
	}

	// The second URL is answered from the file we already have, without asking
	// the site for it again.
	if _, err := get(t, c, s.URL+"/khac"); !errors.Is(err, harvest.ErrBusy) {
		t.Errorf("the second URL gave %v", err)
	}
	if n := s.askedFor("/robots.txt"); n != 1 {
		t.Errorf("robots.txt was fetched %d times for a host that is not being crawled", n)
	}
}

// A 429 is the site telling us something about itself. The answer is time, and
// the time it named.
func TestASiteAskingForTimeGetsTheTimeItNamed(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		"/nang": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
		},
	})
	clk := newClock()
	p := polite(clk, harvest.PoliteOptions{})
	c, _ := crawler(t, harvest.CrawlOptions{Polite: p})

	_, err := get(t, c, s.URL+"/nang")
	if !errors.Is(err, harvest.ErrBusy) {
		t.Fatalf("a 429 gave %v", err)
	}
	if !strings.Contains(err.Error(), "2m0s") {
		t.Errorf("the error does not carry the time the site asked for: %v", err)
	}

	// The next request to that host waits it out rather than coming back sooner
	// because the queue is full. Where it lands does not matter here, only what
	// it waited on the way.
	before := len(clk.waits())
	if _, err := get(t, c, s.URL+"/khac"); err != nil {
		t.Fatalf("the request after a 429 gave %v", err)
	}
	waits := clk.waits()
	if len(waits) <= before {
		t.Fatal("the request after a 429 did not go through the schedule")
	}
	if got := waits[before]; got < 2*time.Minute {
		t.Errorf("the request after a 429 waited %v and the site asked for two minutes", got)
	}
}

// A Retry-After nobody meant anything by still has to produce a number, because
// the alternative is coming straight back to a server that just said it was
// struggling.
func TestASiteThatSaysItIsBusyAndNothingElseStillGetsLeftAlone(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		"/nang": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		},
	})
	clk := newClock()
	c, _ := crawler(t, harvest.CrawlOptions{Polite: polite(clk, harvest.PoliteOptions{})})

	if _, err := get(t, c, s.URL+"/nang"); !errors.Is(err, harvest.ErrBusy) {
		t.Fatalf("a 503 with no Retry-After gave %v", err)
	}
	before := len(clk.waits())
	_, _ = get(t, c, s.URL+"/nang")
	waits := clk.waits()
	if len(waits) <= before || waits[before] < harvest.DefaultBackoff {
		t.Errorf("a 503 with no Retry-After left the host alone for %v", waits[before:])
	}
}

// The size cap is a refusal and not a truncation. Half a page is a page nobody
// can tell is half, and it would go into the store looking like a short article.
func TestABodyLargerThanAPageIsRefused(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		"/lon": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
		},
		"/vua": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", 1000)))
		},
	})
	c, _ := crawler(t, harvest.CrawlOptions{MaxBody: 4096})

	if _, err := get(t, c, s.URL+"/lon"); !errors.Is(err, harvest.ErrTooLarge) {
		t.Fatalf("a body over the cap gave %v", err)
	}
	v, err := get(t, c, s.URL+"/vua")
	if err != nil {
		t.Fatalf("a body under the cap gave %v", err)
	}
	if len(v.Body) != 1000 {
		t.Errorf("a 1000 byte page came back as %d bytes", len(v.Body))
	}
}

// A site with no robots.txt has not asked for anything, and the crawl carries
// on. This is the common case and the one a conservative fetcher gets wrong.
func TestASiteWithNoRobotsFileIsStillCrawled(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/bai-viet": text("<p>noi dung</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	v, err := get(t, c, s.URL+"/bai-viet")
	if err != nil {
		t.Fatalf("a site with no robots.txt gave %v", err)
	}
	if string(v.Body) != "<p>noi dung</p>" {
		t.Errorf("the page came back as %q", v.Body)
	}
}

// A server that cannot answer for its own robots.txt is a server we cannot tell
// about, and a crawler that reads "I am broken" as "I did not object" is a
// crawler that hits hardest when a site is least able to take it.
func TestAHostWhoseRobotsFileIsBrokenIsLeftAlone(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
		"/bai-viet": text("<p>noi dung</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	if _, err := get(t, c, s.URL+"/bai-viet"); !errors.Is(err, harvest.ErrDeclined) {
		t.Fatalf("a 500 on robots.txt gave %v", err)
	}
	if n := s.askedFor("/bai-viet"); n != 0 {
		t.Errorf("the page was fetched %d times from a host whose robots.txt we could not read", n)
	}
}

// What the response said about text and data mining is read off the headers and
// kept with the visit, so that the decision is made once from everything the
// site said rather than twice from parts of it.
func TestWhatTheResponseSaysAboutMiningIsKept(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		"/bai-viet": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Robots-Tag", "noai")
			_, _ = w.Write([]byte("<p>noi dung</p>"))
		},
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	v, err := get(t, c, s.URL+"/bai-viet")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Reserve.Reserved() {
		t.Error("the response carried X-Robots-Tag: noai and the visit does not record a reservation")
	}
	if reason, _, ok := v.Reserve.Reject(); !ok || reason != reject.ReasonRobots {
		t.Errorf("the reservation rejects as %q, %v", reason, ok)
	}
}

// Every URL the crawler declined is written down in the same vocabulary as every
// other document the pipeline dropped, so that a run can be counted from one
// place.
func TestASkippedURLIsWrittenDownLikeEverythingElse(t *testing.T) {
	cases := []struct {
		err  error
		want reject.Reason
	}{
		{harvest.ErrDeclined, reject.ReasonRobots},
		// A wall is not a rule. Only the first of these is a publisher saying
		// no in a place meant for saying it.
		{harvest.ErrBlocked, reject.ReasonFetch},
		{harvest.ErrBusy, reject.ReasonFetch},
		{harvest.ErrTooLarge, reject.ReasonFetch},
		{errors.New("dial tcp: i/o timeout"), reject.ReasonFetch},
	}
	for _, c := range cases {
		reason, why, ok := harvest.Reject(c.err)
		if !ok {
			t.Errorf("%v is not a rejection", c.err)
			continue
		}
		if reason != c.want {
			t.Errorf("%v is written down as %q and belongs under %q", c.err, reason, c.want)
		}
		if why == "" {
			t.Errorf("%v is written down with no reason", c.err)
		}
	}
	if _, _, ok := harvest.Reject(nil); ok {
		t.Error("a fetch that worked is being recorded as a rejection")
	}
}

// This is the only thing in the project that opens a connection to somebody
// else's machine, so what it will and will not dial is worth stating.
func TestItOnlyFetchesTheWebAndSaysSoAboutTheRest(t *testing.T) {
	c, _ := crawler(t, harvest.CrawlOptions{})

	for _, target := range []string{
		"file:///etc/passwd",
		"ftp://ftp.example.vn/du-lieu.tar",
		"/tin-tuc/khong-co-host",
		"://",
	} {
		if _, err := c.Get(context.Background(), target); err == nil {
			t.Errorf("%q was fetched", target)
		}
	}
}

// A canceled crawl stops, including in the middle of waiting for a host's turn,
// and it does not leave a request in flight behind it.
func TestACanceledCrawlDoesNotMakeTheRequest(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		"/bai-viet":   text("<p>noi dung</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(ctx, s.URL+"/bai-viet"); !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled crawl gave %v", err)
	}
	if n := len(s.asked()); n != 0 {
		t.Errorf("a canceled crawl made %d requests", n)
	}
}

// Many workers on one host is the shape a real crawl has, and the thing that
// must not happen is two of them arriving at once. Run under -race, this is also
// the check that the robots file and the blocked list are shared safely.
// The time on a visit is the time the site saw. It is the only column a reader
// can measure the crawl's manners with, and a time taken before the wait would
// say two requests went out together when one of them stood in line for the
// other. This runs on the real clock because that is the thing under test.
func TestTheVisitIsStampedWhenTheRequestGoesOut(t *testing.T) {
	var mu sync.Mutex
	var arrived []time.Time
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		"/bai-viet": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			arrived = append(arrived, time.Now())
			mu.Unlock()
			_, _ = w.Write([]byte("<p>noi dung</p>"))
		},
	})
	const delay = 80 * time.Millisecond
	c := harvest.NewCrawler(harvest.CrawlOptions{
		Polite: harvest.NewPolite(harvest.PoliteOptions{Delay: delay}),
	})

	first, err := get(t, c, s.URL+"/bai-viet")
	if err != nil {
		t.Fatalf("the first fetch: %v", err)
	}
	// Picked up now, sent one delay from now, and the visit has to say the
	// second of those.
	pickedUp := time.Now()
	second, err := get(t, c, s.URL+"/bai-viet")
	if err != nil {
		t.Fatalf("the second fetch: %v", err)
	}

	if gap := second.At.Sub(first.At); gap < delay/2 {
		t.Errorf("the two visits are stamped %v apart, and the schedule left %v between the requests", gap, delay)
	}
	if second.At.Sub(pickedUp) < delay/2 {
		t.Errorf("the second visit is stamped %v after it was picked up, so it was stamped before it waited", second.At.Sub(pickedUp))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(arrived) != 2 {
		t.Fatalf("the site saw %d requests", len(arrived))
	}
	for i, v := range []*harvest.Visit{first, second} {
		if d := v.At.Sub(arrived[i]); d > 200*time.Millisecond || d < -200*time.Millisecond {
			t.Errorf("visit %d is stamped %v from when the site saw it", i, d)
		}
	}
}

func TestManyWorkersOnOneHostDoNotOverlap(t *testing.T) {
	var live, most int
	var mu sync.Mutex
	counted := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		live++
		if live > most {
			most = live
		}
		mu.Unlock()
		time.Sleep(time.Millisecond)
		mu.Lock()
		live--
		mu.Unlock()
		_, _ = w.Write([]byte("<p>noi dung</p>"))
	}
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		"/bai-viet":   counted,
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), s.URL+"/bai-viet"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if most > 1 {
		t.Errorf("%d requests were in flight to one host at once", most)
	}
	if n := s.askedFor("/robots.txt"); n != 1 {
		t.Errorf("twenty workers fetched robots.txt %d times", n)
	}
}

// The well known file is the only mechanism that states a reservation for a
// whole site rather than on every response, so a site that publishes one has
// said something deliberate and a crawler that only read response headers would
// record every page on it as open.
func TestASiteWideReservationIsReadOnceAndAppliedToEveryPage(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		harvest.TDMRepPath: text(`[
			{"location": "/*", "tdm-reservation": 1, "tdm-policy": "https://example.vn/dieu-khoan"}
		]`),
		"/a": text("a"),
		"/b": text("b"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	for _, path := range []string{"/a", "/b"} {
		v, err := get(t, c, s.URL+path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !v.Reserve.NoTrain {
			t.Errorf("%s came back with no reservation and the site reserved everything", path)
		}
		if v.Reserve.Consent() != doc.ConsentNoTrain {
			t.Errorf("%s came back as consent %q", path, v.Reserve.Consent())
		}
		if v.Reserve.Policy != "https://example.vn/dieu-khoan" {
			t.Errorf("%s lost the policy the site pointed at: %q", path, v.Reserve.Policy)
		}
	}
	if n := s.askedFor(harvest.TDMRepPath); n != 1 {
		t.Errorf("the well known file was fetched %d times for two pages", n)
	}
}

// A site can reserve everything and then release one directory, which is how
// these files are actually written, and the release has to survive the trip
// through the crawler rather than only through the parser.
func TestTheLongestLocationInTheWellKnownFileWins(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		harvest.TDMRepPath: text(`[
			{"location": "/*", "tdm-reservation": 1},
			{"location": "/mo/*", "tdm-reservation": 0}
		]`),
		"/kin/bai": text("kin"),
		"/mo/bai":  text("mo"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	closed, err := get(t, c, s.URL+"/kin/bai")
	if err != nil {
		t.Fatal(err)
	}
	if !closed.Reserve.NoTrain {
		t.Error("a path under the site wide reservation came back open")
	}

	open, err := get(t, c, s.URL+"/mo/bai")
	if err != nil {
		t.Fatal(err)
	}
	if open.Reserve.NoTrain {
		t.Error("the directory the site released is still reserved")
	}
	// Released is not the same as unasked. The record has to show that the file
	// was read and said this path was free, because that is a site answering
	// rather than a site nobody asked.
	if len(open.Reserve.Signals()) == 0 {
		t.Error("a path the site explicitly released carries no record of it having been asked")
	}
}

// Almost every site has no such file, and the ordinary case has to be quiet.
// A 404 is a site that reserved nothing, and writing a note about it on every
// page would fill the record with the absence of a file.
func TestMostSitesHaveNoWellKnownFileAndSayNothingAboutIt(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt": text("User-agent: *\nAllow: /\n"),
		"/bai-viet":   text("<p>noi dung</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	v, err := get(t, c, s.URL+"/bai-viet")
	if err != nil {
		t.Fatal(err)
	}
	if v.Reserve.Reserved() {
		t.Error("a site with no well known file came back reserved")
	}
	if got := v.Reserve.Signals()["tdmrep"]; got != "" {
		t.Errorf("a site with no well known file left %q in the record", got)
	}
	if v.Reserve.Consent() != doc.ConsentOpen {
		t.Errorf("a site that was asked and said nothing came back as %q", v.Reserve.Consent())
	}
}

// The asymmetry with robots.txt, stated as a test. robots.txt decides whether a
// page may be fetched, so a file we could not read stops the fetch. This one
// decides what may be done with a page already fetched, and there is a second
// gate on that at the write into the store, so a file we could not read is
// written into the record and the crawl carries on. Stopping instead would hand
// any site a way to end its own crawl by misconfiguring a file most sites do not
// have.
func TestAWellKnownFileThatCannotBeReadIsRecordedRatherThanGuessed(t *testing.T) {
	broken := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt":      text("User-agent: *\nAllow: /\n"),
		harvest.TDMRepPath: broken,
		"/bai-viet":        text("<p>noi dung</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	v, err := get(t, c, s.URL+"/bai-viet")
	if err != nil {
		t.Fatalf("a broken well known file stopped the crawl: %v", err)
	}
	said := v.Reserve.Signals()["tdmrep"]
	if !strings.Contains(said, "not read") || !strings.Contains(said, "500") {
		t.Errorf("the record says %q about a file that answered 500", said)
	}
	if v.Reserve.NoTrain {
		t.Error("a file we could not read was read as a reservation, which is a guess")
	}
}

// Published and unparseable is a different finding from missing, and the record
// says which. A site with a typo in its JSON has tried to say something.
func TestAWellKnownFileThatIsNotJSONSaysSoInTheRecord(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt":      text("User-agent: *\nAllow: /\n"),
		harvest.TDMRepPath: text("khong phai json"),
		"/bai-viet":        text("<p>noi dung</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	v, err := get(t, c, s.URL+"/bai-viet")
	if err != nil {
		t.Fatal(err)
	}
	if said := v.Reserve.Signals()["tdmrep"]; !strings.Contains(said, "unreadable") {
		t.Errorf("the record says %q about a file that is not JSON", said)
	}
}

// The well known file is a path like any other and robots.txt applies to it. A
// site that disallowed it has not made an exception for us, and the record says
// the file was not read rather than that it was not there.
func TestAWellKnownFileTheSiteDisallowedIsNotFetched(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt":      text("User-agent: *\nDisallow: /.well-known/\n"),
		harvest.TDMRepPath: text(`[{"location": "/*", "tdm-reservation": 1}]`),
		"/bai-viet":        text("<p>noi dung</p>"),
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	v, err := get(t, c, s.URL+"/bai-viet")
	if err != nil {
		t.Fatal(err)
	}
	if n := s.askedFor(harvest.TDMRepPath); n != 0 {
		t.Errorf("a path robots.txt disallowed was fetched %d times", n)
	}
	if said := v.Reserve.Signals()["tdmrep"]; !strings.Contains(said, "robots.txt disallows it") {
		t.Errorf("the record says %q about a file we were not allowed to read", said)
	}
}

// Two statements about one page combine the restrictive way, and the pair worth
// testing is the one where they disagree: a site wide file that released this
// path and a response header that reserved it. Honoring the permissive one would
// turn a site saying no into a site saying yes.
func TestTheHeaderAndTheWellKnownFileCombineTheRestrictiveWay(t *testing.T) {
	s := newSite(t, map[string]http.HandlerFunc{
		"/robots.txt":      text("User-agent: *\nAllow: /\n"),
		harvest.TDMRepPath: text(`[{"location": "/*", "tdm-reservation": 0}]`),
		"/bai-viet": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Robots-Tag", "noai")
			_, _ = w.Write([]byte("<p>noi dung</p>"))
		},
	})
	c, _ := crawler(t, harvest.CrawlOptions{})

	v, err := get(t, c, s.URL+"/bai-viet")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Reserve.NoTrain {
		t.Error("a header reserving the page lost to a file releasing it")
	}
	// Both statements are in the record, because the conclusion is one word and
	// the evidence is two mechanisms.
	signals := v.Reserve.Signals()
	if signals["tdmrep"] == "" || signals["robots"] == "" {
		t.Errorf("the record kept one of the two statements and not the other: %v", signals)
	}
}
