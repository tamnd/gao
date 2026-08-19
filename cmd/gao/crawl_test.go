package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	out, errb, code := crawlRun(t, "-dir", dir, "-workers", "2", "-json", srv.URL+"/")
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
	got, err := crawlSeeds(file, []string{"dantri.com.vn"})
	if err != nil {
		t.Fatalf("crawlSeeds: %v", err)
	}
	want := []string{"https://dantri.com.vn/", "https://vnexpress.net/", "https://tuoitre.vn/kinh-doanh"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("seed %d is %q, want %q", i, got[i], want[i])
		}
	}
}
