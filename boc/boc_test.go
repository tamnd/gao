package boc

import (
	"fmt"
	"strings"
	"testing"
)

// thread builds a XenForo shaped page, which is what most of the large
// Vietnamese forums are running. Each post carries a per post class so the
// digit folding in shape has something to fold, a signature under it, and a
// reply button, because a page without those is a page this handler cannot be
// wrong about.
func thread(posts ...string) string {
	var b strings.Builder
	b.WriteString(`<html><head><title>Hỏi về thuế thu nhập cá nhân 2026 | Diễn đàn VOZ</title></head><body>
<nav class="p-nav"><a href="/">Trang chủ</a><a href="/f/">Diễn đàn</a></nav>
<aside class="block"><div class="block-row">Chủ đề mới nhất</div>
<div class="block-row">Giá vàng hôm nay</div><div class="block-row">Tuyển dụng IT</div></aside>
<h1 class="p-title-value">Hỏi về thuế thu nhập cá nhân 2026</h1>
<div class="p-body-main">`)
	for i, text := range posts {
		fmt.Fprintf(&b, `<article class="message message--post js-post-%d">
<span class="username" itemprop="name">thanhnv%d</span>
<time datetime="2026-03-%02dT09:%02d:00+07:00">%d giờ trước</time>
<div class="bbWrapper">%s</div>
<div class="message-signature">Đọc kỹ nội quy trước khi đăng bài, cảm ơn các bác.</div>
<a class="actionBar-action" href="/reply">Trả lời</a>
</article>`, 118432+i, i, i+1, i*7, i+2, text)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

// Four posts of ordinary forum Vietnamese, long enough to clear MinRunes and
// short enough to be realistic, since real posts are two sentences.
var posts = []string{
	"Em mới đi làm được sáu tháng, lương gross hai mươi triệu thì phải quyết toán thuế thế nào ạ.",
	"Bác lên trang thuế điện tử đăng ký tài khoản trước đã, xong rồi mới nộp tờ khai được nhé.",
	"Có ai làm thử trên app chưa, em thấy nó báo lỗi mã số thuế suốt mà không hiểu tại sao.",
	"Mình làm tuần trước thì bình thường, chắc do bác nhập sai số chứng minh thư thôi.",
}

func peel(t *testing.T, page string) Thread {
	t.Helper()
	got, err := Peel(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// This is the whole reason the package exists. Generic extraction keeps the
// densest single block on the page, and on a forum that is the sidebar.
func TestThePostsComeBackAndTheNavigationDoesNot(t *testing.T) {
	got := peel(t, thread(posts...))
	if !got.Ok() {
		t.Fatalf("no thread found: %s", got.Why)
	}
	if len(got.Posts) != len(posts) {
		t.Fatalf("%d posts, want %d: %s", len(got.Posts), len(posts), got.Describe())
	}
	for i, want := range posts {
		if got.Posts[i].Text != want {
			t.Errorf("post %d came back as %q", i, got.Posts[i].Text)
		}
	}
	for _, gone := range []string{"Trang chủ", "Giá vàng hôm nay", "Tuyển dụng IT", "Chủ đề mới nhất"} {
		for _, p := range got.Posts {
			if strings.Contains(p.Text, gone) {
				t.Errorf("the navigation came back inside a post: %q", gone)
			}
		}
	}
}

// A signature under every post and a reply button under every post are text
// that occurs as many times as there are posts, and nothing else on the page
// does that.
func TestWhatRepeatsUnderEveryPostIsFurniture(t *testing.T) {
	got := peel(t, thread(posts...))
	for _, want := range []string{"Đọc kỹ nội quy", "Trả lời"} {
		var found bool
		for _, f := range got.Furniture {
			if strings.Contains(f, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q was not recorded as furniture: %q", want, got.Furniture)
		}
	}
	if strings.Contains(strings.Join(postTexts(got), "\n"), "Đọc kỹ nội quy") {
		t.Error("the signature is still in the posts")
	}
}

// The furniture is kept verbatim rather than counted, because the way this
// handler fails is by dropping something that was not furniture and a count
// makes that invisible.
func TestTheFurnitureIsKeptRatherThanCounted(t *testing.T) {
	got := peel(t, thread(posts...))
	if len(got.Furniture) == 0 {
		t.Fatal("nothing was recorded as furniture")
	}
	for _, f := range got.Furniture {
		if f == "" {
			t.Error("an empty line was recorded as furniture")
		}
	}
}

// A quote is another post, already in the corpus once. Taking it again inflates
// the count and the deduplication does not catch it, because each copy sits in
// a different document with different text around it.
func TestQuotedTextIsTakenOutAndCounted(t *testing.T) {
	quoted := "Bác lên trang thuế điện tử đăng ký tài khoản trước đã, xong rồi mới nộp tờ khai được nhé."
	with := make([]string, len(posts))
	copy(with, posts)
	with[2] = `<blockquote class="bbCodeBlock">` + quoted + `</blockquote>` + with[2]

	got := peel(t, thread(with...))
	if !got.Ok() {
		t.Fatalf("no thread found: %s", got.Why)
	}
	if strings.Contains(got.Posts[2].Text, "đăng ký tài khoản trước") {
		t.Errorf("the quoted post came back inside the reply: %q", got.Posts[2].Text)
	}
	if got.Posts[2].Quoted != len([]rune(quoted)) {
		t.Errorf("counted %d runes of quotation, want %d", got.Posts[2].Quoted, len([]rune(quoted)))
	}
	if got.Quoted() <= 0 {
		t.Error("the thread does not report that any of it was quotation")
	}
}

// A thread that is mostly people quoting each other is a thread about to put
// the same sentences into the corpus three times, and the share is how that
// gets noticed rather than discovered later in a deduplication report.
func TestAThreadThatIsMostlyQuotationSaysSo(t *testing.T) {
	long := strings.Repeat("Em mới đi làm được sáu tháng nên chưa rõ thủ tục. ", 6)
	with := make([]string, len(posts))
	for i := range posts {
		with[i] = `<blockquote>` + long + `</blockquote>` + posts[i]
	}
	got := peel(t, thread(with...))
	if got.Quoted() < 0.5 {
		t.Errorf("a thread that is mostly quotation reported %.0f%%", 100*got.Quoted())
	}
}

// The author comes off markup that means it, and the timestamp off the
// attribute rather than off the "2 giờ trước" next to it, which stops being
// true the moment the page is stored.
func TestTheAuthorAndTheTimeComeOffTheMarkupRatherThanTheProse(t *testing.T) {
	got := peel(t, thread(posts...))
	if got.Posts[0].Author != "thanhnv0" {
		t.Errorf("author %q", got.Posts[0].Author)
	}
	if !strings.HasPrefix(got.Posts[1].At, "2026-03-02T") {
		t.Errorf("time %q, which is not the machine readable one", got.Posts[1].At)
	}
	for _, p := range got.Posts {
		if strings.Contains(p.At, "giờ trước") {
			t.Error("the relative time was taken as the timestamp")
		}
	}
}

// A name is left empty rather than guessed at, because a wrong name attached to
// an opinion is worse than no name.
func TestAPageWithNoNamesInItReturnsNoNames(t *testing.T) {
	page := strings.ReplaceAll(thread(posts...), `class="username" itemprop="name"`, `class="avatar"`)
	got := peel(t, page)
	if !got.Ok() {
		t.Fatalf("no thread found: %s", got.Why)
	}
	for i, p := range got.Posts {
		if p.Author != "" {
			t.Errorf("post %d invented the author %q", i, p.Author)
		}
	}
}

// An article page has no repeated element on it, and the handler has to say so
// rather than return the paragraphs as if they were posts, because the caller's
// next move is to hand the page to the generic extractor.
func TestAnArticleIsNotAThreadAndSaysSoRatherThanFailing(t *testing.T) {
	page := `<html><head><title>Giá gạo xuất khẩu tăng - Báo Nông nghiệp</title></head><body>
<h1>Giá gạo xuất khẩu tăng</h1>
<div class="article-body">
<p>Giá gạo xuất khẩu của Việt Nam trong tháng qua đã tăng lên mức cao nhất kể từ đầu năm, theo số liệu của hiệp hội.</p>
</div></body></html>`
	got := peel(t, page)
	if got.Ok() {
		t.Fatalf("an article came back as a thread of %d posts", len(got.Posts))
	}
	if len(got.Posts) != 0 {
		t.Error("a page with no thread in it still returned posts")
	}
	if !strings.Contains(got.Describe(), "no thread") {
		t.Errorf("the description does not say there is no thread: %s", got.Describe())
	}
}

// The sidebar and the category list repeat too, and they repeat more times than
// the posts do. Picking the group with the most members finds the sidebar, so
// the group with the most text wins instead.
func TestTheSidebarRepeatsMoreThanTheThreadAndStillLoses(t *testing.T) {
	var sidebar strings.Builder
	for i := range 40 {
		fmt.Fprintf(&sidebar, `<div class="block-row">Chủ đề số %d về giá cả</div>`, i)
	}
	page := strings.Replace(thread(posts...),
		`<div class="block-row">Chủ đề mới nhất</div>`, sidebar.String(), 1)

	got := peel(t, page)
	if !got.Ok() {
		t.Fatalf("no thread found: %s", got.Why)
	}
	if !strings.Contains(got.Shape, "message") {
		t.Errorf("the posts were found in %q, which is not the post list", got.Shape)
	}
	if len(got.Posts) != len(posts) {
		t.Errorf("%d posts, want %d", len(got.Posts), len(posts))
	}
}

// A row of category links repeats and holds text, and it is not a conversation.
func TestARepeatedElementFullOfButtonsIsNotAPostList(t *testing.T) {
	page := `<html><body><div class="node-list">
<div class="node">Chuyện trò linh tinh</div>
<div class="node">Máy tính</div>
<div class="node">Điện thoại</div>
</div></body></html>`
	got := peel(t, page)
	if got.Ok() {
		t.Fatalf("a category list came back as a thread: %s", got.Describe())
	}
}

// The title is the thread rather than the thread and the site, and a Vietnamese
// title full of dashes must not be truncated at the first one.
func TestTheTitleLosesTheSiteNameAndKeepsTheDashes(t *testing.T) {
	for _, tt := range []struct{ page, want string }{
		{thread(posts...), "Hỏi về thuế thu nhập cá nhân 2026"},
		{noH1(thread(posts...)), "Hỏi về thuế thu nhập cá nhân 2026"},
		{titled("Mua bán - trao đổi - thanh lý đồ cũ | VOZ"), "Mua bán - trao đổi - thanh lý đồ cũ"},
		{titled("Chuyện trò linh tinh"), "Chuyện trò linh tinh"},
	} {
		got := peel(t, tt.page)
		if got.Title != tt.want {
			t.Errorf("title %q, want %q", got.Title, tt.want)
		}
	}
}

// A page too short to hold a conversation is not a conversation, and the skip
// count is how the caller sees that something of the right shape was found and
// rejected.
func TestContainersTooShortToBePostsAreSkippedAndCounted(t *testing.T) {
	short := []string{posts[0], posts[1], "ok", "vâng"}
	got := peel(t, thread(short...))
	if !got.Ok() {
		t.Fatalf("no thread found: %s", got.Why)
	}
	if got.Skipped != 2 {
		t.Errorf("skipped %d containers, want 2", got.Skipped)
	}
	if len(got.Posts) != 2 {
		t.Errorf("%d posts, want 2", len(got.Posts))
	}
}

// Below half surviving, what was found was not a post list, and returning the
// two containers that did survive would be worse than returning nothing.
func TestAGroupThatIsMostlyFurnitureIsRefused(t *testing.T) {
	short := []string{posts[0], "ok", "vâng", "đúng", "chuẩn", "hay"}
	got := peel(t, thread(short...))
	if got.Ok() {
		t.Fatalf("a group that was mostly buttons came back as a thread: %s", got.Describe())
	}
	if !strings.Contains(got.Why, "furniture") {
		t.Errorf("the reason does not name the problem: %s", got.Why)
	}
}

// Two is a question and an answer, which is a thread. One is a page.
func TestTwoPostsAreAThreadAndOneIsNot(t *testing.T) {
	if got := peel(t, thread(posts[0], posts[1])); !got.Ok() {
		t.Errorf("two posts were not a thread: %s", got.Why)
	}
	if got := peel(t, thread(posts[0])); got.Ok() {
		t.Errorf("one post was a thread: %s", got.Describe())
	}
}

// The shape travels with the result, because a bad extraction has to be
// understandable rather than merely disbelieved.
func TestTheShapeThePostsWereFoundInIsReported(t *testing.T) {
	got := peel(t, thread(posts...))
	if !strings.HasPrefix(got.Shape, "article.") {
		t.Errorf("shape %q", got.Shape)
	}
	if strings.ContainsAny(got.Shape, "0123456789") {
		t.Errorf("the per post class survived into the shape: %q", got.Shape)
	}
	if !strings.Contains(got.Describe(), got.Shape) {
		t.Errorf("the description does not carry the shape: %s", got.Describe())
	}
}

// A description is what a crawl log prints, so it has to say what the fetch
// produced without printing the fetch.
func TestTheDescriptionSaysWhatTheFetchProduced(t *testing.T) {
	got := peel(t, thread(posts...))
	for _, want := range []string{"Hỏi về thuế", "4 posts", "quotation"} {
		if !strings.Contains(got.Describe(), want) {
			t.Errorf("the description does not mention %q: %s", want, got.Describe())
		}
	}
	if got.Runes() == 0 {
		t.Error("the thread reports no text in it")
	}
}

// An empty page is a page rather than an error.
func TestAnEmptyPageIsNotAThread(t *testing.T) {
	got := peel(t, "")
	if got.Ok() {
		t.Error("an empty page came back as a thread")
	}
	if got.Quoted() != 0 || got.Runes() != 0 {
		t.Error("an empty page has numbers on it")
	}
	if !strings.Contains(got.Describe(), "no thread") {
		t.Errorf("%s", got.Describe())
	}
}

func postTexts(t Thread) []string {
	out := make([]string, 0, len(t.Posts))
	for _, p := range t.Posts {
		out = append(out, p.Text)
	}
	return out
}

func noH1(page string) string {
	return strings.ReplaceAll(strings.ReplaceAll(page, "<h1 class=\"p-title-value\">", "<div>"), "</h1>", "</div>")
}

func titled(title string) string {
	return noH1(strings.Replace(thread(posts...),
		"Hỏi về thuế thu nhập cá nhân 2026 | Diễn đàn VOZ", title, 1))
}
