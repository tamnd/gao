package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// huskPage writes a XenForo shaped thread, which is what the large Vietnamese
// forums are running, with a sidebar that holds more text than the conversation
// does so the file is a test of the handler and not just of the plumbing.
func huskPage(t *testing.T, posts ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<html><head><title>Hỏi về thuế thu nhập cá nhân 2026 | Diễn đàn VOZ</title></head><body>
<h1 class="p-title-value">Hỏi về thuế thu nhập cá nhân 2026</h1><aside class="block">`)
	for i := range 30 {
		fmt.Fprintf(&b, `<div class="block-row">Chủ đề mới số %d</div>`, i)
	}
	b.WriteString(`</aside><div class="p-body-main">`)
	for i, text := range posts {
		fmt.Fprintf(&b, `<article class="message js-post-%d">
<span class="username">thanhnv%d</span><time datetime="2026-03-0%dT09:00:00+07:00">%d giờ trước</time>
<div class="bbWrapper">%s</div>
<div class="message-signature">Đọc kỹ nội quy trước khi đăng bài nhé các bác.</div>
<a class="actionBar-action" href="/reply">Trả lời</a></article>`, 118432+i, i, i+1, i+2, text)
	}
	b.WriteString(`</div></body></html>`)

	path := filepath.Join(t.TempDir(), "thread.html")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

var huskPosts = []string{
	"Em mới đi làm được sáu tháng, lương gross hai mươi triệu thì quyết toán thuế thế nào ạ.",
	"Bác lên trang thuế điện tử đăng ký tài khoản trước đã, xong rồi mới nộp tờ khai được nhé.",
	`<blockquote>Bác lên trang thuế điện tử đăng ký tài khoản trước đã.</blockquote>Em đăng ký rồi mà nó cứ báo sai mã số thuế, không hiểu vướng ở đâu nữa.`,
}

func TestTheThreadComesOutAndThePageStaysBehind(t *testing.T) {
	out, errOut, code := exec(t, "husk", huskPage(t, huskPosts...))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "message") {
		t.Errorf("the shape the posts were found in is not reported:\n%s", out)
	}
	if strings.Contains(out, "Chủ đề mới số") {
		t.Errorf("the sidebar came back as the conversation:\n%s", out)
	}
}

func TestThePostsCanBePrinted(t *testing.T) {
	out, _, code := exec(t, "husk", "-text", huskPage(t, huskPosts...))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{
		"Hỏi về thuế thu nhập cá nhân 2026",
		"lương gross hai mươi triệu",
		"thanhnv0, 2026-03-01T09:00:00+07:00",
		"runes of quotation taken out",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the posts do not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "giờ trước") {
		t.Errorf("the relative time was printed as the timestamp:\n%s", out)
	}
}

// The way this handler fails is by dropping something that was not a signature,
// so what it dropped has to be printable rather than only counted.
func TestWhatWasDroppedCanBeRead(t *testing.T) {
	out, _, code := exec(t, "husk", "-furniture", huskPage(t, huskPosts...))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"dropped as furniture", "Đọc kỹ nội quy", "Trả lời"} {
		if !strings.Contains(out, want) {
			t.Errorf("the furniture does not carry %q:\n%s", want, out)
		}
	}
}

// A page that is not a thread is an answer rather than a failure, because the
// caller's next move is the generic extractor and an error would have it
// dropping the page instead.
func TestAnArticleSaysThereIsNoThreadAndExitsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "article.html")
	page := `<html><head><title>Giá gạo xuất khẩu tăng</title></head><body><h1>Giá gạo xuất khẩu tăng</h1>
<div class="article-body"><p>Giá gạo xuất khẩu của Việt Nam tháng qua đã tăng lên mức cao nhất kể từ đầu năm nay.</p></div></body></html>`
	if err := os.WriteFile(path, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := exec(t, "husk", path)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "no thread") {
		t.Errorf("an article did not say it was not a thread:\n%s", out)
	}
	if !strings.Contains(out, "which is what an article looks like") {
		t.Errorf("the reason is not given:\n%s", out)
	}
}

func TestTheWholeThreadIsAvailableAsJSON(t *testing.T) {
	out, errOut, code := exec(t, "husk", "-json", huskPage(t, huskPosts...))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got huskReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Threads != 1 || len(got.Pages) != 1 {
		t.Fatalf("%d threads out of %d pages", got.Threads, len(got.Pages))
	}
	page := got.Pages[0]
	if len(page.Posts) != len(huskPosts) {
		t.Errorf("%d posts, want %d", len(page.Posts), len(huskPosts))
	}
	if page.Posts[2].Quoted == 0 {
		t.Error("the quotation was not counted")
	}
	if page.Title != "Hỏi về thuế thu nhập cá nhân 2026" {
		t.Errorf("title %q", page.Title)
	}
}

func TestSeveralPagesAreOneTable(t *testing.T) {
	out, _, code := exec(t, "husk", huskPage(t, huskPosts...), huskPage(t, huskPosts...))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if n := strings.Count(out, "thread.html"); n != 2 {
		t.Errorf("%d pages in the table, want 2:\n%s", n, out)
	}
}

func TestHuskWithoutAPageIsAUsageError(t *testing.T) {
	out, errOut, code := exec(t, "husk")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "usage: gao husk") {
		t.Errorf("no usage printed: %s", errOut)
	}
}

func TestAMissingPageIsAFailure(t *testing.T) {
	_, errOut, code := exec(t, "husk", filepath.Join(t.TempDir(), "nothing.html"))
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errOut)
	}
	if !strings.Contains(errOut, "gao husk:") {
		t.Errorf("the failure was not attributed: %s", errOut)
	}
}
