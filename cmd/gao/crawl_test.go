package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/crawl"
)

// The command is tested against a server rather than against a mock, because
// what it is for is running a crawl and the parts of it worth checking are the
// ones that only appear when a real request goes out: robots.txt is read, the
// WARC lands on the disk, and both repos get a part.

func crawlRun(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb strings.Builder
	code := run(&out, &errb, append([]string{"crawl"}, args...))
	return out.String(), errb.String(), code
}

// site is a two page Vietnamese site with a robots.txt that keeps crawlers out
// of one directory.
func crawlSite(t *testing.T) *httptest.Server {
	t.Helper()
	page := `<!doctype html><html lang="vi"><head><title>Tin Đồng Tháp</title></head><body><div class="content">
	<h1>Nông dân huyện Tháp Mười thu hoạch sớm để tránh mưa</h1>
	<p>Vụ lúa hè thu năm nay ở huyện Tháp Mười cho năng suất bình quân bảy tấn một héc ta, cao hơn
	cùng kỳ năm ngoái gần một tấn, và giá lúa tươi tại ruộng đang ở mức tám nghìn đồng một ki lô gam.</p>
	<p>Ngành nông nghiệp tỉnh khuyến cáo bà con thu hoạch dứt điểm trước khi mùa mưa về, vì nước lên
	sớm mấy hôm là công sức cả vụ đổ xuống sông. Các trạm bơm ở xã Mỹ Quý đã chạy suốt tuần qua.</p>
	<p>Ông Nguyễn Văn Bảy, chủ ba héc ta ruộng ở ấp Bốn, nói giống lúa mới chịu phèn tốt hơn hẳn
	giống ông trồng mười năm trước, và chi phí phân bón một công ruộng giảm hai trăm nghìn đồng.</p>
	<p>Toàn huyện xuống giống hơn ba mươi sáu nghìn héc ta trong vụ này, và phòng nông nghiệp cho
	biết đã có hợp đồng bao tiêu cho gần một nửa diện tích, phần còn lại bán theo giá thị trường.</p>
	</div></body></html>`
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nDisallow: /rieng-tu/\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<html lang="vi"><body><a href="/tin/1.html">Bài</a>
			<a href="/rieng-tu/noi-bo.html">Nội bộ</a></body></html>`)
			return
		}
		fmt.Fprint(w, page)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestCrawlWritesBothRepos(t *testing.T) {
	srv := crawlSite(t)
	dir := t.TempDir()

	out, errb, code := crawlRun(t, "-dir", dir, "-workers", "2", "-warc", "-json", srv.URL+"/")
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errb)
	}

	// The JSON report is the last object printed, after the part lines.
	i := strings.Index(out, "{")
	if i < 0 {
		t.Fatalf("no report in the output:\n%s", out)
	}
	var p struct {
		Fetched int64 `json:"fetched"`
		Kept    int64 `json:"kept"`
		Dropped int64 `json:"dropped"`
		Sink    struct {
			Archived int64 `json:"archived"`
			Parts    int   `json:"parts"`
		} `json:"sink"`
	}
	if err := json.Unmarshal([]byte(out[i:]), &p); err != nil {
		t.Fatalf("reading the report: %v\n%s", err, out[i:])
	}
	if p.Kept != 1 {
		t.Errorf("the run kept %d pages, want the one article", p.Kept)
	}
	if p.Dropped < 1 {
		t.Errorf("the run dropped %d pages, want the listing at least", p.Dropped)
	}
	if p.Sink.Archived != p.Fetched {
		t.Errorf("%d pages fetched and %d archived, want every fetch in the WARC", p.Fetched, p.Sink.Archived)
	}
	if p.Sink.Parts != 2 {
		t.Errorf("%d parts written, want one per repo", p.Sink.Parts)
	}

	// Nothing was pushed, so both parts and the WARC are still here, and the
	// two repos are in directories of their own.
	for _, repo := range []string{"vitweb", "vitweb-rejects"} {
		found, err := filepath.Glob(filepath.Join(dir, repo, "data", "*", "*.parquet"))
		if err != nil || len(found) == 0 {
			t.Errorf("%s has no part on the disk: %v", repo, err)
		}
	}
	warc, err := filepath.Glob(filepath.Join(dir, "warc", "*.warc.gz"))
	if err != nil || len(warc) == 0 {
		t.Errorf("no WARC volume was written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "frontier", "frontier.json")); err != nil {
		t.Errorf("the frontier was not left where the next run reads it: %v", err)
	}
}

// A crawl stopped by its page limit leaves the queue where it was, and a second
// run picks it up rather than starting from the seed.
func TestCrawlResumesFromTheFrontier(t *testing.T) {
	srv := crawlSite(t)
	dir := t.TempDir()

	if _, errb, code := crawlRun(t, "-dir", dir, "-workers", "1", "-pages", "1", srv.URL+"/"); code != 0 {
		t.Fatalf("first run: %s", errb)
	}
	out, errb, code := crawlRun(t, "-dir", dir, "-workers", "1")
	if code != 0 {
		t.Fatalf("second run: %s%s", out, errb)
	}
	if !strings.Contains(out, "0 seeds queued") {
		t.Errorf("the second run was given seeds:\n%s", out)
	}
	if strings.Contains(out, "0 fetched") {
		t.Errorf("the second run fetched nothing, so the queue did not survive:\n%s", out)
	}
}

func TestCrawlRefusesAShardOutsideTheFleet(t *testing.T) {
	_, errb, code := crawlRun(t, "-dir", t.TempDir(), "-shard", "3", "-fleet", "3")
	if code == 0 {
		t.Error("box 4 of 3 was accepted")
	}
	if !strings.Contains(errb, "not a box in the fleet") {
		t.Errorf("the error does not say what is wrong: %s", errb)
	}
}

func TestCrawlNeedsADirectory(t *testing.T) {
	_, errb, code := crawlRun(t)
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "-dir is required") {
		t.Errorf("the error does not say what is missing: %s", errb)
	}
}

// A bare host is a seed, because a list from Certificate Transparency is hosts.
func TestCrawlSeedsTakeBareHosts(t *testing.T) {
	file := filepath.Join(t.TempDir(), "seeds.txt")
	body := "# a comment\nvnexpress.net\n\nhttps://tuoitre.vn/kinh-doanh\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := crawl.OpenFrontier(crawl.FrontierOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	defer func() { _ = f.Close() }()

	queued, refused, err := crawlSeeds(file, []string{"dantri.com.vn"}, f)
	if err != nil {
		t.Fatalf("crawlSeeds: %v", err)
	}
	if queued != 3 || refused != 0 {
		t.Fatalf("%d seeds queued and %d refused, want 3 and 0", queued, refused)
	}
	got, err := f.Next(10)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"https://dantri.com.vn/":        true,
		"https://vnexpress.net/":        true,
		"https://tuoitre.vn/kinh-doanh": true,
	}
	if len(got) != len(want) {
		t.Fatalf("the frontier holds %v, want %v", got, want)
	}
	for _, u := range got {
		if !want[u] {
			t.Errorf("the frontier holds %q, which was not a seed", u)
		}
	}
}

// A seed list is now an extract from a Common Crawl index and those are kept
// compressed, so a run should not have to gunzip six million URLs to a file
// first on a box whose disk is meant to be cache.
func TestCrawlSeedsReadAGzippedList(t *testing.T) {
	file := filepath.Join(t.TempDir(), "seeds.txt.gz")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte("https://baodongthap.example/tin-1.html\nbaocantho.example\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := crawl.OpenFrontier(crawl.FrontierOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	defer func() { _ = f.Close() }()

	queued, _, err := crawlSeeds(file, nil, f)
	if err != nil {
		t.Fatalf("crawlSeeds: %v", err)
	}
	if queued != 2 {
		t.Fatalf("%d seeds queued from a gzipped list, want 2", queued)
	}
}

// The seed goes to the frontier in batches, so a list that does not divide by
// the batch size is where a lost remainder would show up. The real list is six
// and a half million URLs and nobody counts those by hand.
func TestCrawlSeedsLongerThanOneBatchArriveWhole(t *testing.T) {
	const n = seedBatch*2 + 7
	var buf bytes.Buffer
	for i := range n {
		fmt.Fprintf(&buf, "https://host-%d.example/\n", i)
	}
	file := filepath.Join(t.TempDir(), "seeds.txt")
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := crawl.OpenFrontier(crawl.FrontierOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	defer func() { _ = f.Close() }()

	queued, refused, err := crawlSeeds(file, nil, f)
	if err != nil {
		t.Fatalf("crawlSeeds: %v", err)
	}
	if queued != n || refused != 0 {
		t.Fatalf("%d seeds queued and %d refused, want %d and 0", queued, refused, n)
	}
	if got := f.Stats().Admitted; got != n {
		t.Fatalf("the frontier admitted %d, want %d", got, n)
	}
}

// The WARC is off unless it is asked for, because writing it was a sixth of the
// crawler's CPU on a crawler whose ceiling is CPU. This is the test that the
// default really is off: the datasets still land, every fetch is still counted,
// and there is no volume on the disk.
func TestTheWARCIsOffUnlessItIsAskedFor(t *testing.T) {
	srv := crawlSite(t)
	dir := t.TempDir()

	out, errb, code := crawlRun(t, "-dir", dir, "-workers", "2", "-json", srv.URL+"/")
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errb)
	}
	i := strings.Index(out, "{")
	if i < 0 {
		t.Fatalf("no report in the output:\n%s", out)
	}
	var p struct {
		Fetched int64 `json:"fetched"`
		Kept    int64 `json:"kept"`
		Sink    struct {
			Archived  int64 `json:"archived"`
			Volumes   int   `json:"volumes"`
			WARCBytes int64 `json:"warc_bytes"`
			Parts     int   `json:"parts"`
		} `json:"sink"`
	}
	if err := json.Unmarshal([]byte(out[i:]), &p); err != nil {
		t.Fatalf("reading the report: %v\n%s", err, out[i:])
	}
	if p.Kept != 1 || p.Sink.Parts != 2 {
		t.Errorf("kept %d pages into %d parts, want the article and one part per repo", p.Kept, p.Sink.Parts)
	}
	if p.Sink.Volumes != 0 || p.Sink.WARCBytes != 0 {
		t.Errorf("%d volumes and %d bytes of WARC were written with -warc off", p.Sink.Volumes, p.Sink.WARCBytes)
	}
	// Archived still counts the fetches, because the count is what was seen and
	// not what was written down.
	if p.Sink.Archived != p.Fetched {
		t.Errorf("%d fetched and %d archived, want the count to hold with nothing recorded", p.Fetched, p.Sink.Archived)
	}
	if _, err := os.Stat(filepath.Join(dir, "warc")); !os.IsNotExist(err) {
		t.Errorf("a volume directory was made with -warc off: %v", err)
	}

	// The rows still say where they came from, which is the thing the WARC
	// locator was doing and the reason turning it off is safe.
	for _, repo := range []string{"vitweb", "vitweb-rejects"} {
		found, err := filepath.Glob(filepath.Join(dir, repo, "data", "*", "*.parquet"))
		if err != nil || len(found) == 0 {
			t.Errorf("%s has no part on the disk: %v", repo, err)
		}
	}
}

// The index describes the published repo, so asking for one on a run that
// publishes nothing is a flag that was meant for another box, and it is worth
// saying so before the crawl rather than after it.
func TestCrawlRefusesToIndexARunThatIsNotPushing(t *testing.T) {
	_, errb, code := crawlRun(t, "-dir", t.TempDir(), "-index", "1h")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "needs -push") {
		t.Errorf("the error does not say what is missing: %s", errb)
	}
}

// The ceiling the collector works against is worth saying out loud. A run that
// was given one and did not take it looks exactly like a run that was not given
// one, until the box runs out of memory and only one of the two explains it.
func TestCrawlSaysWhatCeilingTheCollectorGot(t *testing.T) {
	out, _, code := crawlRun(t, "-dir", t.TempDir(), "-pages", "0", "-memlimit", "1073741824")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out, "collector is working against 1.1 GB") {
		t.Errorf("the run does not say what ceiling it took: %s", out)
	}
}
