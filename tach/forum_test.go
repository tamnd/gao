package tach

import (
	"strings"
	"testing"
)

// The posts of the test thread. They are real sentences of the kind a
// Vietnamese forum actually carries, because the thing being measured is
// whether real text survives the menus around it.
var bodies = []string{
	"Mình đang tìm bộ gõ tiếng Việt chạy được trên máy Linux mà vẫn gõ kiểu Telex quen tay. Bạn nào đã dùng qua thì cho xin ý kiến với.",
	"Mình dùng ibus-bamboo hơn hai năm nay rồi, gõ Telex ổn định và không bị mất dấu khi chuyển cửa sổ. Cài từ kho của Ubuntu là chạy được ngay.",
	"Mình thì chuyển sang fcitx5 vì dùng Wayland, ibus hay bị treo bảng gợi ý. Cấu hình lâu hơn một chút nhưng sau đó thì quên luôn là mình đang gõ bằng gì.",
	"Cảm ơn hai bạn, mình sẽ thử ibus-bamboo trước vì đang dùng Ubuntu bản thường, nếu vướng Wayland thì tính tiếp.",
}

// menu is the part of the page that is not the thread, and it is deliberately
// long, because on a real forum it is longer than any single post.
const menu = `
<nav class="mainmenu">
  <ul>
    <li><a href="/">Trang chủ</a></li>
    <li><a href="/forum/phan-cung">Phần cứng</a></li>
    <li><a href="/forum/phan-mem">Phần mềm</a></li>
    <li><a href="/forum/mang">Mạng và bảo mật</a></li>
    <li><a href="/forum/lap-trinh">Lập trình</a></li>
    <li><a href="/dang-nhap">Đăng nhập</a></li>
    <li><a href="/dang-ky">Đăng ký</a></li>
  </ul>
</nav>
<aside class="sidebar">
  <h3>Chủ đề mới nhất</h3>
  <ul>
    <li><a href="/t/1">Máy tính không lên nguồn sau khi thay nguồn mới</a></li>
    <li><a href="/t/2">Chia sẻ cấu hình build máy tầm mười lăm triệu</a></li>
    <li><a href="/t/3">Hỏi về tốc độ đọc ghi của ổ cứng gắn ngoài</a></li>
    <li><a href="/t/4">Cách đặt lại mật khẩu modem của nhà mạng</a></li>
    <li><a href="/t/5">Máy in không nhận lệnh in qua mạng nội bộ</a></li>
    <li><a href="/t/6">Tư vấn màn hình hai bảy inch cho dân văn phòng</a></li>
    <li><a href="/t/7">Ổ SSD báo lỗi SMART thì còn cứu được dữ liệu không</a></li>
    <li><a href="/t/8">Cài lại Windows mà không mất phân vùng dữ liệu</a></li>
  </ul>
  <h3>Thống kê diễn đàn</h3>
  <p>Tổng số bài viết: 1.284.930. Tổng số chủ đề: 96.117. Thành viên mới nhất: hoangminh92.</p>
  <p>Đang truy cập: 412 khách và 37 thành viên. Kỷ lục trực tuyến là 8.902 người vào ngày 14 tháng 3.</p>
</aside>`

// post renders one message the way forum software does, with the author, the
// body, and whatever else was passed in.
func post(author, body, extra string) string {
	return `<div class="post">
  <div class="postprofile"><a class="username" href="/member/` + author + `">` + author + `</a></div>
  <div class="postbody"><div class="content">` + body + extra + `</div></div>
</div>`
}

// thread renders a whole page: the menu, the heading, the posts, and a footer.
func thread(extras ...string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><title>Bộ gõ tiếng Việt trên Linux - Diễn đàn Tin học - Trang 1</title></head><body>`)
	b.WriteString(menu)
	b.WriteString(`<div class="thread"><h1>Bộ gõ tiếng Việt trên Linux</h1>`)
	for i, body := range bodies {
		extra := ""
		if i < len(extras) {
			extra = extras[i]
		}
		b.WriteString(post(string(rune('a'+i))+"nguyen", body, extra))
	}
	b.WriteString(`</div><footer><p>Bản quyền thuộc về diễn đàn. Liên hệ quảng cáo.</p></footer></body></html>`)
	return b.String()
}

func read(t *testing.T, page string, o Options) *Thread {
	t.Helper()
	got, ok := Forum([]byte(page), o)
	if !ok {
		t.Fatal("the page did not read as a thread")
	}
	return got
}

func TestAThreadReadsAsItsPosts(t *testing.T) {
	got := read(t, thread(), Options{})
	if len(got.Posts) != len(bodies) {
		t.Fatalf("read %d posts, want %d:\n%s", len(got.Posts), len(bodies), got.Text())
	}
	for i, p := range got.Posts {
		if p.Text != bodies[i] {
			t.Errorf("post %d came back as %q", i+1, p.Text)
		}
		if p.Index != i+1 {
			t.Errorf("post %d is numbered %d", i+1, p.Index)
		}
	}
}

func TestTheNavigationIsNotInTheOutput(t *testing.T) {
	got := read(t, thread(), Options{})
	text := got.Text()
	for _, s := range []string{"Đăng nhập", "Chủ đề mới nhất", "Bản quyền", "Phần cứng"} {
		if strings.Contains(text, s) {
			t.Errorf("the menu item %q survived into the text", s)
		}
	}
	if got.Dropped <= got.Chars() {
		t.Errorf("the page kept %d characters and dropped %d, and on a thread the menu is the larger half",
			got.Chars(), got.Dropped)
	}
	if got.Yield() >= 0.5 {
		t.Errorf("the yield is %.2f, which is more page than thread", got.Yield())
	}
}

func TestQuotedTextIsRemovedAndCounted(t *testing.T) {
	// The third poster quotes the second, which is what a thread looks like and
	// what puts the same sentences in a document three times.
	quote := `<blockquote class="quote"><p>` + bodies[1] + `</p></blockquote>`
	got := read(t, thread("", "", quote), Options{})

	if len(got.Posts) != len(bodies) {
		t.Fatalf("read %d posts", len(got.Posts))
	}
	if got.Posts[2].Text != bodies[2] {
		t.Errorf("the quoting post came back as %q", got.Posts[2].Text)
	}
	if strings.Count(got.Text(), bodies[1]) != 1 {
		t.Error("the quoted sentences are in the document twice, which is a duplicate deduplication cannot see")
	}
	if got.Posts[2].Quoted == 0 {
		t.Error("the quotation was removed and not counted")
	}
	if got.Quoted() != got.Posts[2].Quoted {
		t.Errorf("the thread counted %d characters of quotation and the post %d", got.Quoted(), got.Posts[2].Quoted)
	}
}

func TestASignatureUnderEveryPostIsDropped(t *testing.T) {
	sig := `<div class="signature"><p>Gửi từ điện thoại của tôi, mong bỏ qua lỗi chính tả.</p></div>`
	got := read(t, thread(sig, sig, sig, sig), Options{})

	if strings.Contains(got.Text(), "Gửi từ điện thoại") {
		t.Errorf("the signature survived into the text:\n%s", got.Text())
	}
	if got.Repeated != len(bodies) {
		t.Errorf("%d repeated lines were dropped, want %d", got.Repeated, len(bodies))
	}
	for i, p := range got.Posts {
		if p.Text != bodies[i] {
			t.Errorf("post %d came back as %q", i+1, p.Text)
		}
	}
}

func TestALineRepeatedInsideOnePostIsNotASignature(t *testing.T) {
	twice := `<p>` + bodies[0] + `</p>`
	got := read(t, thread(twice), Options{})
	if got.Repeated != 0 {
		t.Errorf("%d lines were dropped as repeated, and both of them were in the same post", got.Repeated)
	}
	if strings.Count(got.Posts[0].Text, bodies[0]) != 2 {
		t.Errorf("the first post came back as %q", got.Posts[0].Text)
	}
}

func TestAThreadWhereEverybodySaysTheSameThingIsNotAThread(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><body><div class="thread">`)
	for i := range 4 {
		b.WriteString(post(string(rune('a'+i))+"nguyen", bodies[0], ""))
	}
	b.WriteString(`</div></body></html>`)

	if got, ok := Forum([]byte(b.String()), Options{}); ok {
		t.Errorf("a page whose every post is the same line read as a thread of %d posts", len(got.Posts))
	}
}

func TestAnArticlePageIsNotAThread(t *testing.T) {
	page := `<!doctype html><html><head><title>Một bài viết</title></head><body>` + menu + `
<article><h1>Chữ quốc ngữ và những gì nó ghi lại</h1>
<p>` + bodies[0] + `</p>
<p>` + bodies[1] + `</p>
<p>` + bodies[2] + `</p>
</article></body></html>`

	if got, ok := Forum([]byte(page), Options{}); ok {
		t.Errorf("an article read as a thread of %d posts:\n%s", len(got.Posts), got.Text())
	}
}

func TestAForumIndexIsNotAThread(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><body><div class="topiclist">`)
	for _, title := range []string{
		"Máy tính không lên nguồn sau khi thay nguồn mới",
		"Chia sẻ cấu hình build máy tầm mười lăm triệu",
		"Hỏi về tốc độ đọc ghi của ổ cứng gắn ngoài",
		"Cách đặt lại mật khẩu modem của nhà mạng",
	} {
		b.WriteString(`<div class="row"><a href="/t/1">` + title + `</a><span>12 trả lời</span></div>`)
	}
	b.WriteString(`</div></body></html>`)

	if got, ok := Forum([]byte(b.String()), Options{}); ok {
		t.Errorf("a list of thread titles read as a thread of %d posts:\n%s", len(got.Posts), got.Text())
	}
}

func TestTheProfileBoxGoesWithTheByline(t *testing.T) {
	// The post count differs per member, so the repeated line rule never sees
	// it, and every member who has posted once carries theirs into the corpus.
	profile := `<div class="postprofile">
  <a class="username" href="/thanh-vien/anhtuan">anhtuan</a>
  <p>Bài viết: 318</p>
  <p>Tham gia: 14 tháng 3, 2019</p>
</div>`
	page := `<html><body><div class="thread">`
	for _, body := range bodies {
		page += `<div class="post">` + profile + `<div class="content">` + body + `</div></div>`
	}
	page += `</div></body></html>`

	got := read(t, page, Options{})
	for i, p := range got.Posts {
		if p.Text != bodies[i] {
			t.Errorf("post %d came back as %q", i+1, p.Text)
		}
		if p.Author != "anhtuan" {
			t.Errorf("post %d is attributed to %q", i+1, p.Author)
		}
	}
}

func TestAPostThatOpensWithANameKeepsItsFirstParagraph(t *testing.T) {
	// The rule that drops the profile box is a rule about a box with nothing
	// long in it, and a post is not that even when a name sits at the top of it.
	page := `<html><body><div class="thread">`
	for _, body := range bodies {
		page += `<div class="post"><a class="username" href="/thanh-vien/anhtuan">anhtuan</a> ` + body + `</div>`
	}
	page += `</div></body></html>`

	got := read(t, page, Options{})
	for i, p := range got.Posts {
		if p.Text != bodies[i] {
			t.Errorf("post %d came back as %q", i+1, p.Text)
		}
	}
}

func TestAPostWithALinkInItIsStillAPost(t *testing.T) {
	// The rule that keeps forum indexes out is a rule about how much of a post
	// is anchor, and a poster who links to the download page is not an index.
	link := `<p>Tải bản mới nhất ở <a href="https://example.vn/tai-ve">trang chủ</a> nhé.</p>`
	got := read(t, thread(link), Options{})

	if len(got.Posts) != len(bodies) {
		t.Fatalf("read %d posts, want %d", len(got.Posts), len(bodies))
	}
	if !strings.Contains(got.Posts[0].Text, "Tải bản mới nhất ở trang chủ nhé.") {
		t.Errorf("the first post came back as %q", got.Posts[0].Text)
	}
}

func TestTheAuthorIsTakenWhenThePageSaysSoPlainly(t *testing.T) {
	got := read(t, thread(), Options{})
	for i, p := range got.Posts {
		if want := string(rune('a'+i)) + "nguyen"; p.Author != want {
			t.Errorf("post %d is attributed to %q, want %q", i+1, p.Author, want)
		}
	}

	// The same thread with nothing naming the poster. An author the page did
	// not give is left empty rather than guessed at.
	var b strings.Builder
	b.WriteString(`<html><body><div class="thread">`)
	for _, body := range bodies {
		b.WriteString(`<div class="post"><div class="content">` + body + `</div></div>`)
	}
	b.WriteString(`</div></body></html>`)

	got = read(t, b.String(), Options{})
	for i, p := range got.Posts {
		if p.Author != "" {
			t.Errorf("post %d is attributed to %q on a page that named nobody", i+1, p.Author)
		}
	}
}

func TestTheTitleComesFromTheHeadingRatherThanTheTitleTag(t *testing.T) {
	got := read(t, thread(), Options{})
	if got.Title != "Bộ gõ tiếng Việt trên Linux" {
		t.Errorf("the title came back as %q, and the title tag carries the board name and the page number", got.Title)
	}
}

func TestTheSameThreadReadsTheSameWayHoweverTheMarkupIsIndented(t *testing.T) {
	plain := thread()
	spaced := strings.ReplaceAll(plain, "><", ">\n\n  \t<")

	a, b := read(t, plain, Options{}), read(t, spaced, Options{})
	if a.Text() != b.Text() {
		t.Errorf("reindenting the markup changed the text:\n%q\n%q", a.Text(), b.Text())
	}
	if a.Chars() != b.Chars() {
		t.Errorf("reindenting changed the character count from %d to %d", a.Chars(), b.Chars())
	}
}

func TestRaisingTheFloorTurnsAThreadIntoSomethingElse(t *testing.T) {
	page := thread()
	if _, ok := Forum([]byte(page), Options{MinChars: 4000}); ok {
		t.Error("a thread of ordinary posts cleared a four thousand character floor")
	}
	if _, ok := Forum([]byte(page), Options{MinPosts: 9}); ok {
		t.Error("a thread of four posts cleared a floor of nine")
	}
	if _, ok := Forum([]byte(page), Options{MaxDepth: 1}); ok {
		t.Error("the posts were found at a depth the search was told not to reach")
	}
}

func TestNothingAtAllIsNotAThread(t *testing.T) {
	for _, page := range []string{"", "   ", "<html></html>", "khong phai HTML", "<div><p>Một câu.</p>"} {
		if _, ok := Forum([]byte(page), Options{}); ok {
			t.Errorf("%q read as a thread", page)
		}
	}
}
