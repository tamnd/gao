package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A thread page, small but shaped like the real thing: a menu, a heading, three
// messages in identical boxes, and a footer.
const tachThread = `<!doctype html>
<html><head><title>Bộ gõ tiếng Việt trên Linux - Diễn đàn Tin học</title></head>
<body>
<nav><ul>
  <li><a href="/">Trang chủ</a></li>
  <li><a href="/forum/phan-mem">Phần mềm</a></li>
  <li><a href="/dang-nhap">Đăng nhập</a></li>
</ul></nav>
<div class="thread">
  <h1>Bộ gõ tiếng Việt trên Linux</h1>
  <div class="post">
    <a class="username" href="/member/an">an</a>
    <div class="content">Mình đang tìm bộ gõ tiếng Việt chạy được trên Linux mà vẫn gõ kiểu Telex quen tay.</div>
  </div>
  <div class="post">
    <a class="username" href="/member/binh">binh</a>
    <div class="content">Mình dùng ibus-bamboo hai năm nay, gõ Telex ổn định và không mất dấu khi chuyển cửa sổ.</div>
  </div>
  <div class="post">
    <a class="username" href="/member/cuong">cuong</a>
    <div class="content">
      <blockquote>Mình dùng ibus-bamboo hai năm nay, gõ Telex ổn định và không mất dấu khi chuyển cửa sổ.</blockquote>
      Mình thì chuyển sang fcitx5 vì dùng Wayland, cấu hình lâu hơn một chút nhưng sau đó thì quên luôn.
    </div>
  </div>
</div>
<footer><p>Bản quyền thuộc về diễn đàn.</p></footer>
</body></html>`

// An article page, which is what routing here wrongly looks like.
const tachArticle = `<!doctype html>
<html><head><title>Một bài viết</title></head>
<body><article><h1>Chữ quốc ngữ và những gì nó ghi lại</h1>
<p>Chữ quốc ngữ được các giáo sĩ soạn ra để ghi âm tiếng Việt bằng chữ cái Latinh.</p>
<p>Hệ thống dấu thanh đi kèm là thứ khiến tiếng Việt viết ra đọc lên gần đúng như nói.</p>
</article></body></html>`

func writePage(t *testing.T, name, page string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTachReadsAThreadAndSaysWhatItDropped(t *testing.T) {
	out, _, code := exec(t, "tach", writePage(t, "thread.html", tachThread))
	if code != 0 {
		t.Fatalf("gao tach: exit %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"3", "1 of 1 pages read as threads", "posts"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Đăng nhập") {
		t.Errorf("the menu is in the output:\n%s", out)
	}
}

func TestTachSaysSoWhenThePageIsNotAThread(t *testing.T) {
	out, _, code := exec(t, "tach", writePage(t, "article.html", tachArticle))
	if code != 1 {
		t.Fatalf("gao tach on an article: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "not a thread") {
		t.Errorf("the output does not say the page was not a thread:\n%s", out)
	}
	if !strings.Contains(out, "0 of 1 pages read as threads") {
		t.Errorf("the run summary is missing:\n%s", out)
	}
}

func TestTachCountsThePagesThatWereNotThreads(t *testing.T) {
	dir := t.TempDir()
	thread := filepath.Join(dir, "thread.html")
	article := filepath.Join(dir, "article.html")
	for path, page := range map[string]string{thread: tachThread, article: tachArticle} {
		if err := os.WriteFile(path, []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out, _, code := exec(t, "tach", thread, article)
	if code != 0 {
		t.Fatalf("gao tach: exit %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "1 of 2 pages read as threads") {
		t.Errorf("the run summary did not count the page that was not a thread:\n%s", out)
	}
}

func TestTachTextPrintsTheThreadItself(t *testing.T) {
	out, _, code := exec(t, "tach", "-text", writePage(t, "thread.html", tachThread))
	if code != 0 {
		t.Fatalf("gao tach -text: exit %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "# Bộ gõ tiếng Việt trên Linux") {
		t.Errorf("the title is missing:\n%s", out)
	}
	for _, want := range []string{"an:", "binh:", "cuong:", "chuyển sang fcitx5"} {
		if !strings.Contains(out, want) {
			t.Errorf("the text is missing %q:\n%s", want, out)
		}
	}
	// The third poster quoted the second, and the quote is not printed twice.
	if n := strings.Count(out, "ibus-bamboo hai năm nay"); n != 1 {
		t.Errorf("the quoted line appears %d times, want 1:\n%s", n, out)
	}
	if strings.Contains(out, "Bản quyền") {
		t.Errorf("the footer is in the text:\n%s", out)
	}
}

func TestTachJSONCarriesThePostsAndTheRun(t *testing.T) {
	out, _, code := exec(t, "tach", "-json",
		writePage(t, "thread.html", tachThread), writePage(t, "article.html", tachArticle))
	if code != 0 {
		t.Fatalf("gao tach -json: exit %d, want 0\n%s", code, out)
	}

	var report tachReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, out)
	}
	if len(report.Pages) != 2 {
		t.Fatalf("the report holds %d pages, want 2", len(report.Pages))
	}
	if report.Pages[0].Thread == nil {
		t.Fatal("the thread page came back with no thread")
	}
	if got := len(report.Pages[0].Thread.Posts); got != 3 {
		t.Errorf("the thread holds %d posts, want 3", got)
	}
	if report.Pages[0].Thread.Posts[0].Author != "an" {
		t.Errorf("the first post is attributed to %q", report.Pages[0].Thread.Posts[0].Author)
	}
	if report.Pages[1].Thread != nil {
		t.Error("the article came back as a thread")
	}
	if report.Run.Pages != 2 || report.Run.Threads != 1 || report.Run.Posts != 3 {
		t.Errorf("the run came back as %+v", report.Run)
	}
	if report.Run.QuotedCh == 0 {
		t.Error("the quotation was not counted in the run")
	}
}

func TestTachFloorsAreReachableFromTheCommandLine(t *testing.T) {
	page := writePage(t, "thread.html", tachThread)
	if _, _, code := exec(t, "tach", "-min-posts", "9", page); code != 1 {
		t.Errorf("a thread of three posts cleared a floor of nine: exit %d", code)
	}
	if _, _, code := exec(t, "tach", "-min-chars", "4000", page); code != 1 {
		t.Errorf("ordinary posts cleared a four thousand character floor: exit %d", code)
	}
}

func TestTachWithoutPagesExplainsItself(t *testing.T) {
	_, stderr, code := exec(t, "tach")
	if code != 2 {
		t.Fatalf("gao tach: exit %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage: gao tach") {
		t.Errorf("stderr did not print the usage:\n%s", stderr)
	}
}

func TestTachOnAMissingFileFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "khong-co.html")
	_, stderr, code := exec(t, "tach", missing)
	if code != 1 {
		t.Fatalf("gao tach on a missing file: exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "gao tach:") {
		t.Errorf("stderr did not name the command:\n%s", stderr)
	}
}

func TestTachRejectsAnUnknownFlag(t *testing.T) {
	if _, _, code := exec(t, "tach", "-nosuchflag"); code != 2 {
		t.Errorf("gao tach -nosuchflag: exit %d, want 2", code)
	}
}
