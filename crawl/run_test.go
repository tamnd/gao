package crawl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/harvest"
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
