package crawl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamnd/gao/harvest"
	"github.com/tamnd/gao/reject"
	"github.com/tamnd/gao/store"
)

// body is the paragraphs of the article. They say different things because a
// page that says one thing four times is a page the repetition filter throws
// out, and rightly, so a fixture built that way would be testing the filter
// instead of the crawl.
var body = []string{
	`Vụ lúa hè thu năm nay ở huyện Tháp Mười cho năng suất bình quân bảy tấn một héc ta,
	cao hơn cùng kỳ năm ngoái gần một tấn. Giá lúa tươi tại ruộng đang được thương lái mua
	vào khoảng tám nghìn đồng một ki lô gam.`,
	`Ngành nông nghiệp tỉnh khuyến cáo bà con thu hoạch dứt điểm trước khi mùa mưa về, vì
	nước lên sớm mấy hôm là công sức cả vụ đổ xuống sông. Các trạm bơm ở xã Mỹ Quý đã chạy
	suốt tuần qua để rút nước trên đồng.`,
	`Ông Nguyễn Văn Bảy, chủ ba héc ta ruộng ở ấp Bốn, nói rằng giống lúa mới chịu phèn tốt
	hơn hẳn giống cũ ông trồng mười năm trước, và chi phí phân bón một công ruộng giảm
	chừng hai trăm nghìn đồng so với vụ đông xuân.`,
	`Toàn huyện xuống giống hơn ba mươi sáu nghìn héc ta trong vụ này. Phòng nông nghiệp cho
	biết đã có hợp đồng bao tiêu cho gần một nửa diện tích, phần còn lại bà con bán cho
	thương lái theo giá thị trường từng ngày.`,
}

// article is a page with enough Vietnamese prose on it to clear the sift
// thresholds, which is what a page has to be to survive the whole run.
func article(n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><html lang="vi"><head><title>Bài %d | Báo Đồng Tháp</title></head><body>`, n)
	b.WriteString(`<nav><a href="/">Trang chủ</a> <a href="/thoi-su">Thời sự</a></nav><div class="content">`)
	fmt.Fprintf(&b, "<h1>Nông dân huyện Tháp Mười trúng vụ lúa hè thu thứ %d</h1>", n)
	for _, p := range body {
		fmt.Fprintf(&b, "<p>%s</p>", p)
	}
	b.WriteString(`</div><footer>Giấy phép số 123/GP-TTĐT</footer></body></html>`)
	return b.String()
}

// site is a small news site: an index that links to the articles, three
// articles, a page robots.txt keeps us off, and a page that is not there.
func site(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nDisallow: /rieng-tu/\nCrawl-delay: 0\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch {
		case r.URL.Path == "/":
			fmt.Fprint(w, `<html lang="vi"><body><ul>
			<li><a href="/tin/1.html">Bài một</a></li>
			<li><a href="/tin/2.html">Bài hai</a></li>
			<li><a href="/tin/3.html">Bài ba</a></li>
			<li><a href="/rieng-tu/noi-bo.html">Nội bộ</a></li>
			<li><a href="/khong-co.html">Đã gỡ</a></li>
			<li><a href="/chuyen-huong">Chuyển hướng</a></li>
			</ul></body></html>`)
		case strings.HasPrefix(r.URL.Path, "/tin/"):
			n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/tin/"), ".html"))
			fmt.Fprint(w, article(n))
		case r.URL.Path == "/chuyen-huong":
			http.Redirect(w, r, "/tin/1.html", http.StatusMovedPermanently)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "<html><body>Không tìm thấy</body></html>")
		}
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// The whole crawl over a real HTTP server: the articles are kept, the listing
// and the missing page are written down as rejections with a reason, robots.txt
// is honored, and both repos have parts on the disk at the end.
func TestACrawlKeepsTheArticlesAndSaysWhyItDroppedTheRest(t *testing.T) {
	srv := site(t)
	dir := t.TempDir()

	f, err := OpenFrontier(FrontierOptions{Dir: filepath.Join(dir, "frontier")})
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	defer func() { _ = f.Close() }()
	if ok, why, err := f.Offer(srv.URL + "/"); err != nil || !ok {
		t.Fatalf("offering the seed: ok=%v why=%q err=%v", ok, why, err)
	}

	s := openSink(t, SinkOptions{Dir: filepath.Join(dir, "out"), Snapshot: "gaocrawl-20260819"})
	c := harvest.NewCrawler(harvest.CrawlOptions{
		Polite:  harvest.NewPolite(harvest.PoliteOptions{Delay: time.Millisecond}),
		Version: "test",
	})

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	p, err := Run(ctx, RunOptions{Frontier: f, Sink: s, Crawler: c, Workers: 4, Batch: 8})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the sink: %v", err)
	}

	if p.Kept != 3 {
		t.Errorf("the run kept %d pages, want the three articles", p.Kept)
	}
	if p.Redirects != 1 {
		t.Errorf("the run saw %d redirects, want 1", p.Redirects)
	}
	// The listing, the 404, the redirect, and the page robots.txt declined.
	if p.Dropped < 3 {
		t.Errorf("the run dropped %d pages, want the listing, the missing page and the redirect", p.Dropped)
	}
	if p.Fetched < 5 {
		t.Errorf("the run fetched %d pages", p.Fetched)
	}

	kept := rows(t, dir, KeptRepo)
	if len(kept) != 3 {
		t.Fatalf("%d rows in %s, want 3", len(kept), KeptRepo)
	}
	for _, r := range kept {
		if r.Text != "" {
			t.Errorf("a crawled page shipped its text: %q", r.Text)
		}
		if r.LicenseClass != "restricted" {
			t.Errorf("a crawled page is class %q", r.LicenseClass)
		}
		if r.SourceLocator == "" || r.HTTPStatus != 200 {
			t.Errorf("the row does not point back at the fetch: %+v", r)
		}
		if r.RobotsDecision == "" {
			t.Error("the row does not say which robots rule allowed the fetch")
		}
	}

	drops := rejectRows(t, dir, RejectRepo)
	if len(drops) < 3 {
		t.Fatalf("%d rows in %s, want one per dropped page", len(drops), RejectRepo)
	}
	seen := map[string]bool{}
	for _, r := range drops {
		seen[r.RejectReason] = true
		if r.RejectStage == "" {
			t.Errorf("a rejection does not say which stage dropped it: %+v", r)
		}
	}
	if !seen["robots"] {
		t.Errorf("the page robots.txt declined is not in the rejections: %v", seen)
	}
	if !seen["fetch"] {
		t.Errorf("the missing page is not in the rejections: %v", seen)
	}
}

// A crawl is stopped by its context and that is not a failure, because stopping
// a crawl is how a crawl ends.
func TestAStoppedCrawlIsNotAFailure(t *testing.T) {
	srv := site(t)
	dir := t.TempDir()

	f, err := OpenFrontier(FrontierOptions{Dir: filepath.Join(dir, "frontier")})
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, _, err := f.Offer(srv.URL + "/"); err != nil {
		t.Fatalf("offering the seed: %v", err)
	}

	s := openSink(t, SinkOptions{Dir: filepath.Join(dir, "out")})
	defer func() { _ = s.Close() }()
	c := harvest.NewCrawler(harvest.CrawlOptions{
		Polite:  harvest.NewPolite(harvest.PoliteOptions{Delay: time.Millisecond}),
		Version: "test",
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Run(ctx, RunOptions{Frontier: f, Sink: s, Crawler: c, Workers: 2}); err != nil {
		t.Errorf("a canceled crawl came back as an error: %v", err)
	}
}

// The page limit stops a run, which is how a first run against real sites is
// kept to a size somebody can read.
func TestARunStopsAtThePageLimit(t *testing.T) {
	srv := site(t)
	dir := t.TempDir()

	f, err := OpenFrontier(FrontierOptions{Dir: filepath.Join(dir, "frontier")})
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, _, err := f.Offer(srv.URL + "/"); err != nil {
		t.Fatalf("offering the seed: %v", err)
	}

	s := openSink(t, SinkOptions{Dir: filepath.Join(dir, "out")})
	defer func() { _ = s.Close() }()
	c := harvest.NewCrawler(harvest.CrawlOptions{
		Polite:  harvest.NewPolite(harvest.PoliteOptions{Delay: time.Millisecond}),
		Version: "test",
	})

	p, err := Run(t.Context(), RunOptions{Frontier: f, Sink: s, Crawler: c, Workers: 1, Batch: 1, Pages: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p.Fetched > 3 {
		t.Errorf("a run limited to two pages fetched %d", p.Fetched)
	}
	if q := f.Stats().Queued(); q == 0 {
		t.Error("a run that stopped at its limit left nothing in the queue")
	}
}

func TestARunNeedsSomewhereToPutWhatItFetches(t *testing.T) {
	if _, err := Run(t.Context(), RunOptions{}); err == nil {
		t.Error("a run started with no frontier, sink or crawler")
	}
}

// rows reads back every part one repo got, which is what a query over the
// published dataset would see.
func rows(t *testing.T, dir, repo string) []store.Row {
	t.Helper()
	var out []store.Row
	for _, path := range parts(t, dir, repo) {
		got, err := store.ReadPart(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		out = append(out, got...)
	}
	return out
}

func rejectRows(t *testing.T, dir, repo string) []store.RejectRow {
	t.Helper()
	var out []store.RejectRow
	for _, path := range parts(t, dir, repo) {
		got, err := store.ReadRejectPart(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		out = append(out, got...)
	}
	return out
}

func parts(t *testing.T, dir, repo string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, "out", repo, "data", "*", "*.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// english is a page long enough to clear every threshold except the one that
// matters, which is the point of it.
func english(path string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><title>Rice harvest</title></head><body><div class="content">`)
	b.WriteString(`<h1>The rice harvest in the Mekong delta</h1>`)
	for range 6 {
		b.WriteString(`<p>The summer autumn rice crop in the delta province came in at seven tonnes
		a hectare this year, which is close to a tonne more than the same season last year, and
		traders were paying around eight thousand a kilogram for wet paddy at the field.</p>`)
		b.WriteString(`<p>The provincial agriculture department has asked growers to finish cutting
		before the rains arrive, because water coming up a few days early costs a whole season of
		work, and the pumping stations have been running all week to clear the fields.</p>`)
	}
	fmt.Fprintf(&b, `</div><a href="%s">Read more</a></body></html>`, path)
	return b.String()
}

// The rule that keeps a crawl of the Vietnamese web from becoming a crawl of the
// web. A page in another language is fetched once, because there is no way to
// know what language a page is in without reading it, and then it is a dead end.
// Following it queues hosts whose entire subgraph is somebody else's web, and on
// the first fleet run that is where most of the requests went.
func TestALinkOnAPageInAnotherLanguageIsNotFollowed(t *testing.T) {
	var deeper atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nCrawl-delay: 0\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html lang="vi"><body><ul>
			<li><a href="/tin/1.html">Bài một</a></li>
			<li><a href="/en/">In English</a></li>
			</ul></body></html>`)
		case "/en/":
			fmt.Fprint(w, english("/en/deeper.html"))
		case "/en/deeper.html":
			deeper.Add(1)
			fmt.Fprint(w, english("/en/"))
		default:
			fmt.Fprint(w, article(1))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	f, err := OpenFrontier(FrontierOptions{Dir: filepath.Join(dir, "frontier")})
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	defer func() { _ = f.Close() }()
	if ok, why, err := f.Offer(srv.URL + "/"); err != nil || !ok {
		t.Fatalf("offering the seed: ok=%v why=%q err=%v", ok, why, err)
	}

	s := openSink(t, SinkOptions{Dir: filepath.Join(dir, "out"), Snapshot: "gaocrawl-20260819"})
	c := harvest.NewCrawler(harvest.CrawlOptions{
		Polite:  harvest.NewPolite(harvest.PoliteOptions{Delay: time.Millisecond}),
		Version: "test",
	})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := Run(ctx, RunOptions{Frontier: f, Sink: s, Crawler: c, Workers: 2, Batch: 8}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the sink: %v", err)
	}

	// The English page is fetched, because there is no way to know. It is
	// fetched once, and what it points at is never asked for.
	if n := deeper.Load(); n != 0 {
		t.Errorf("the page behind the English one was fetched %d times", n)
	}

	var sawEnglish bool
	for _, r := range rejectRows(t, dir, RejectRepo) {
		if strings.HasSuffix(r.URL, "/en/") {
			sawEnglish = true
			if r.RejectReason != string(reject.ReasonLanguage) {
				t.Errorf("the English page was turned away as %q, want language: %s", r.RejectReason, r.RejectDetail)
			}
		}
	}
	if !sawEnglish {
		t.Error("the English page was never fetched, so the test proved nothing")
	}
}

// A queue of URLs that all belong to one host is the shape a crawl slows down
// in, and until this counter existed there was no way to see it: the progress
// line showed a low rate and said nothing about why. Every worker that reaches
// for the same host gets one turn and the rest are handed their URL back, which
// is correct and cheap and completely invisible.
//
// The delay is long enough here that most turns land on a host that is not due.
// The run still has to finish and still has to keep the articles, because a URL
// given back is a URL that comes round again.
func TestWorkersReachingForOneHostAreCounted(t *testing.T) {
	srv := site(t)
	dir := t.TempDir()

	f, err := OpenFrontier(FrontierOptions{Dir: filepath.Join(dir, "frontier")})
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	defer func() { _ = f.Close() }()
	if ok, _, err := f.Offer(srv.URL + "/"); err != nil || !ok {
		t.Fatalf("offering the seed: %v", err)
	}

	s := openSink(t, SinkOptions{Dir: filepath.Join(dir, "out"), Snapshot: "gaocrawl-20260820"})
	c := harvest.NewCrawler(harvest.CrawlOptions{
		Polite:  harvest.NewPolite(harvest.PoliteOptions{Delay: 40 * time.Millisecond}),
		Version: "test",
	})

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	p, err := Run(ctx, RunOptions{Frontier: f, Sink: s, Crawler: c, Workers: 8, Batch: 8})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the sink: %v", err)
	}

	if p.Waited == 0 {
		t.Error("eight workers shared one host and none of them was told to wait")
	}
	if p.Kept != 3 {
		t.Errorf("the run kept %d pages, want the three articles, so waiting cost pages", p.Kept)
	}
}

// Several feeders taking from the same frontier fetch each page once.
//
// The feeder used to be one goroutine and is [DefaultFeeders] of them now,
// because two thousand workers all take from the channel it fills. What that
// change had to keep is that a URL still reaches exactly one worker. Run it
// under -race.
func TestSeveralFeedersFetchEachPageOnce(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "User-agent: *\nCrawl-delay: 0\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/tin/"), ".html"))
		fmt.Fprint(w, article(n))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	f, err := OpenFrontier(FrontierOptions{Dir: filepath.Join(dir, "frontier"), PerHost: 1 << 20})
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	defer func() { _ = f.Close() }()

	want := 400
	for i := range want {
		if _, _, err := f.Offer(fmt.Sprintf("%s/tin/%d.html", srv.URL, i)); err != nil {
			t.Fatalf("Offer: %v", err)
		}
	}

	s := openSink(t, SinkOptions{Dir: filepath.Join(dir, "out")})
	defer func() { _ = s.Close() }()
	c := harvest.NewCrawler(harvest.CrawlOptions{
		Polite:  harvest.NewPolite(harvest.PoliteOptions{Delay: time.Millisecond}),
		Version: "test",
	})

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	p, err := Run(ctx, RunOptions{Frontier: f, Sink: s, Crawler: c, Workers: 32, Batch: 16, Feeders: 8})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	// robots.txt and the TDM reservation are fetched once for the host and are
	// not pages. The articles carry a nav bar, so the run also finds the two
	// pages that links to, which is the crawl working rather than a miscount.
	delete(asked, "/robots.txt")
	delete(asked, "/.well-known/tdmrep.json")
	for path, n := range asked {
		if n != 1 {
			t.Fatalf("%s was fetched %d times, so a URL reached two workers", path, n)
		}
	}
	if p.Fetched != int64(len(asked)) {
		t.Fatalf("the run counted %d fetches and the site was asked for %d pages", p.Fetched, len(asked))
	}
	for i := range want {
		if asked[fmt.Sprintf("/tin/%d.html", i)] != 1 {
			t.Fatalf("/tin/%d.html was offered and never fetched", i)
		}
	}
}
