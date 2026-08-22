package crawl

import (
	"net/url"
	"strings"
	"testing"
)

func read(t *testing.T, base, doc string) *Page {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parsing the base URL: %v", err)
	}
	p, err := Read(u, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return p
}

// render is the two markdown renderings of a page. They are a method rather
// than a pair of fields so that a crawl which throws the page away never pays
// for them, and a test that wants either one asks for both.
func render(t *testing.T, base, doc string) (markdown, body string) {
	t.Helper()
	return read(t, base, doc).Render()
}

// The shape of nearly every Vietnamese news page: a masthead, a navigation bar
// of every section on the site, the article, a column of related headlines, and
// a footer with the license number in it.
const newsPage = `<!doctype html>
<html lang="vi">
<head>
  <title>Nong dan Dong Thap trung vu lua he thu | Bao Dong Thap</title>
  <link rel="canonical" href="/nong-nghiep/nong-dan-trung-vu-lua-he-thu-123.html">
</head>
<body>
  <header id="site-header"><a href="/">Bao Dong Thap</a></header>
  <nav class="main-menu">
    <a href="/thoi-su">Thoi su</a> <a href="/kinh-te">Kinh te</a>
    <a href="/the-thao">The thao</a> <a href="/giai-tri">Giai tri</a>
    <a href="/phap-luat">Phap luat</a> <a href="/giao-duc">Giao duc</a>
  </nav>
  <div class="article-body">
    <h1>Nong dan Dong Thap trung vu lua he thu</h1>
    <p>Vu lua he thu nam nay o huyen Thap Muoi cho nang suat binh quan bay tan
    mot hecta, cao hon cung ky nam ngoai gan mot tan. Gia lua tuoi tai ruong
    dang duoc thuong lai mua vao khoang tam nghin dong mot kilogam.</p>
    <p>Theo Phong Nong nghiep huyen, toan huyen xuong giong hon ba muoi sau
    nghin hecta, trong do giong lua chat luong cao chiem tren tam muoi phan
    tram dien tich. Nong dan thu lai tu hai muoi den hai muoi lam trieu dong
    moi hecta sau khi tru chi phi.</p>
    <p>Nganh nong nghiep tinh khuyen cao ba con thu hoach dut diem truoc mua
    mua de tranh thiet hai, dong thoi chuan bi dat cho vu thu dong.</p>
  </div>
  <div class="sidebar related">
    <a href="/tin-1.html">Gia lua tang tro lai</a>
    <a href="/tin-2.html">Xuat khau gao vuot muc tieu</a>
    <a href="/tin-3.html">Mua lu ve som o dau nguon</a>
  </div>
  <div id="footer">Giay phep so 123/GP-TTDT. Ban quyen thuoc Bao Dong Thap.</div>
  <script>var ads = {slot: "top"};</script>
</body>
</html>`

func TestAnArticleIsFoundInThePageAroundIt(t *testing.T) {
	p := read(t, "https://baodongthap.example/nong-nghiep/nong-dan-trung-vu-lua-he-thu-123.html", newsPage)

	if !strings.Contains(p.Text, "nang suat binh quan bay tan") {
		t.Errorf("the article is not in the text:\n%s", p.Text)
	}
	if !strings.Contains(p.Text, "chuan bi dat cho vu thu dong") {
		t.Errorf("the last paragraph of the article is missing:\n%s", p.Text)
	}
	for _, unwanted := range []string{"The thao", "Xuat khau gao vuot muc tieu", "Giay phep so", "var ads"} {
		if strings.Contains(p.Text, unwanted) {
			t.Errorf("the text carries %q, which is not the article:\n%s", unwanted, p.Text)
		}
	}
	// Paragraphs survive as paragraphs. A document that is one long line is a
	// document nothing downstream can split into sentences.
	if n := strings.Count(p.Text, "\n\n"); n < 3 {
		t.Errorf("the text has %d paragraph breaks:\n%s", n, p.Text)
	}
	if p.Lang != "vi" {
		t.Errorf("the page reports lang %q", p.Lang)
	}
	if p.Title != "Nong dan Dong Thap trung vu lua he thu" {
		t.Errorf("the title is %q, with the masthead still on it", p.Title)
	}
	if want := "https://baodongthap.example/nong-nghiep/nong-dan-trung-vu-lua-he-thu-123.html"; p.Canonical != want {
		t.Errorf("the canonical address is %q, want %q", p.Canonical, want)
	}
}

// A category page is a page of links to articles. There is nothing on it to
// keep, and an extractor that keeps the headlines fills the corpus with the
// navigation of every site it visits.
func TestAListingHasNoTextOfItsOwn(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><body><div class="list">`)
	for i := range 40 {
		b.WriteString(`<div class="item"><a href="/bai-viet-`)
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(`.html">Mot tieu de bai bao khá dài để trông giống thật</a></div>`)
	}
	b.WriteString(`</div></body></html>`)

	p := read(t, "https://tin.example/thoi-su", b.String())
	if p.Text != "" {
		t.Errorf("a listing produced text:\n%s", p.Text)
	}
	if len(p.Links) < 20 {
		t.Errorf("the listing gave up %d links, which is where the crawl goes next", len(p.Links))
	}
}

func TestEveryLinkComesBackAbsolute(t *testing.T) {
	doc := `<html><head><base href="https://cdn.example/site/"></head><body>
	<a href="one.html">one</a>
	<a href="/two.html">two</a>
	<a href="//other.example/three.html">three</a>
	<a href="https://far.example/four.html">four</a>
	<a href="one.html">one again</a>
	<a href="#top">top</a>
	<a href="mailto:toa.soan@example.com">mail</a>
	<a href="javascript:void(0)">script</a>
	<a href="/paid.html" rel="nofollow sponsored">paid</a>
	<a href="/five.html#comments">five</a>
	</body></html>`

	p := read(t, "https://tin.example/bai-viet", doc)
	want := []string{
		"https://cdn.example/site/one.html",
		"https://cdn.example/two.html",
		"https://other.example/three.html",
		"https://far.example/four.html",
		"https://cdn.example/five.html",
	}
	if len(p.Links) != len(want) {
		t.Fatalf("the page gave %d links: %v", len(p.Links), p.Links)
	}
	for i, w := range want {
		if p.Links[i] != w {
			t.Errorf("link %d is %q, want %q", i, p.Links[i], w)
		}
	}
}

// A site can reserve its rights in a header or in the page, and meaning it in
// one place is meaning it.
func TestThePageIsReadForWhatItReserved(t *testing.T) {
	doc := `<html><head>
	<meta name="robots" content="noindex, nofollow">
	<meta name="tdm-reservation" content="1">
	</head><body><p>Noi dung.</p></body></html>`

	p := read(t, "https://kin.example/trang", doc)
	if !p.Reserve.NoIndex {
		t.Error("a page saying noindex was read as not reserving anything")
	}
	if !p.Reserve.NoTrain {
		t.Error("a page with a TDM reservation was read as not reserving anything")
	}
	if !p.NoFollow {
		t.Error("a page saying nofollow was read as following")
	}
	if len(p.Reserve.Said) == 0 {
		t.Error("the reservation carries no evidence of what the page said")
	}
}

func TestAPageWithNothingToResolveAgainstIsRefused(t *testing.T) {
	if _, err := Read(nil, strings.NewReader("<html></html>")); err == nil {
		t.Fatal("a page was read with no URL to resolve its links against")
	}
}

func TestTheTitleLosesTheMastheadAndNothingElse(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Bai viet hay | VnExpress", "Bai viet hay"},
		{"Bai viet hay - Tuoi Tre Online", "Bai viet hay"},
		{"Tin nong nghiep", "Tin nong nghiep"},
		{"Mot tieu de co dau gach - noi giua cau - va con tiep tuc noi nua nen khong phai ten bao", "Mot tieu de co dau gach - noi giua cau - va con tiep tuc noi nua nen khong phai ten bao"},
	}
	for _, c := range cases {
		if got := trimTitle(c.in); got != c.want {
			t.Errorf("trimTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A page whose article is in a table is a page from 2009 and there are a lot of
// them on the Vietnamese web.
func TestAnArticleInATableIsStillAnArticle(t *testing.T) {
	doc := `<html><body><table><tr>
	<td class="menu"><a href="/a">Trang chu</a><a href="/b">Gioi thieu</a><a href="/c">Lien he</a></td>
	<td class="content"><p>Hoi nghi tong ket cong tac nam duoc to chuc sang nay tai hoi truong
	uy ban nhan dan tinh, voi su tham du cua dai dien cac so nganh va lanh dao cac huyen thi
	trong toan tinh.</p><p>Bao cao tai hoi nghi cho biet toc do tang truong kinh te dat muc
	de ra, thu ngan sach vuot du toan, va cac chi tieu ve van hoa xa hoi deu hoan thanh.</p></td>
	</tr></table></body></html>`

	p := read(t, "https://tinh.example/tin/1", doc)
	if !strings.Contains(p.Text, "Hoi nghi tong ket") {
		t.Errorf("the article in the table was not found:\n%s", p.Text)
	}
	if strings.Contains(p.Text, "Trang chu") {
		t.Errorf("the menu column came with it:\n%s", p.Text)
	}
}

// vnexpress wraps its articles in a div called sidebar-1 inside one called
// header-content. Both of those words used to be an outright ban, so this
// package returned two bytes from a vnexpress article for as long as it had
// existed: the multiplication sign off a close button. The three articles this
// was found on are in issue 176 with their byte counts.
const articleInASidebar = `<!doctype html>
<html lang="vi">
<head><title>Ap thap nhiet doi gay mua lon cho mien Bac - Bao VnExpress</title></head>
<body>
  <div class="header-content width_common">
    <div class="sidebar-1">
      <h1 class="title-detail">Ap thap nhiet doi gay mua lon cho mien Bac</h1>
      <p class="description">Ap thap nhiet doi tren vinh Bac Bo co the gay mua
      tren ba tram milimet tu nay den dem hai muoi hai thang tam o Dong Bac Bo
      va Thanh Hoa, nguy co gay ngap ung, lu quet va sat lo dat.</p>
      <p>Trung tam Du bao Khi tuong Thuy van quoc gia cho biet luc bay gio, ap
      thap nhiet doi tren vinh Bac Bo, cach dac khu Bach Long Vi khoang mot tram
      kilomet ve phia dong, suc gio manh nhat sau muoi mot kilomet mot gio, cap
      bay, giat tang hai cap va dang theo huong tay bac.</p>
      <p>Du bao den bay gio ngay mai, ap thap nhiet doi cach Hai Phong khoang
      mot tram chin muoi kilomet ve phia dong, cach Bach Long Vi mot tram hai
      muoi kilomet ve phia dong bac va co kha nang manh len thanh bao cap tam,
      giat cap muoi.</p>
      <p>Nganh phong chong thien tai khuyen cao cac dia phuong ven bien theo doi
      sat dien bien, chuan bi phuong an so tan dan o nhung khu vuc co nguy co
      sat lo va cam bien tu chieu nay.</p>
    </div>
  </div>
  <div class="footer">Bao dien tu VnExpress. Giay phep so 548/GP-BTTTT.</div>
</body>
</html>`

func TestAnArticleInAContainerCalledSidebarIsStillAnArticle(t *testing.T) {
	p := read(t, "https://vnexpress.example/thoi-su/ap-thap-nhiet-doi-123.html", articleInASidebar)

	for _, want := range []string{"tren ba tram milimet", "suc gio manh nhat", "cam bien tu chieu nay"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("the article is missing %q:\n%s", want, p.Text)
		}
	}
	if strings.Contains(p.Text, "Giay phep so") {
		t.Errorf("the footer is in the text:\n%s", p.Text)
	}
	md, _ := p.Render()
	if !strings.Contains(md, "# Ap thap nhiet doi") {
		t.Errorf("the markdown lost the headline:\n%s", md)
	}
	// The two renderings come off the same block, so they agree about what the
	// page is. A shape measured twice is a shape that can disagree with itself.
	if len(md) < len(p.Text) {
		t.Errorf("the markdown is %d bytes and the text is %d", len(md), len(p.Text))
	}
}

// The case the word list was written for, and the reason the words are still
// read at all. Forty teasers are forty headlines and a line of summary each,
// which is real writing by any measure that counts characters. What tells it
// from an article is that most of those characters are inside links.
func TestASidebarOfTeasersIsStillASidebar(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html lang="vi"><body><div class="sidebar-news">`)
	for i := range 40 {
		b.WriteString(`<div class="item"><a href="/bai-`)
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(`.html">Nong dan mien Tay trung vu lua he thu nam nay</a>`)
		b.WriteString(`<span> Nang suat cao hon cung ky.</span></div>`)
	}
	b.WriteString(`</div></body></html>`)

	p := read(t, "https://tin.example/", b.String())
	if p.Text != "" {
		t.Errorf("a sidebar of teasers produced text:\n%s", p.Text)
	}
	if len(p.Links) < 20 {
		t.Errorf("the sidebar gave up %d links, which is where the crawl goes next", len(p.Links))
	}
}

// A word that names a thing rather than a position is still a verdict. An
// advertisement is an advertisement at any length, and the ones that run long
// are the ones written to read like an article.
func TestAdvertisingIsNotContentHoweverMuchOfItThereIs(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html lang="vi"><body><div class="advert-block"><h2>Uu dai thang tam</h2>`)
	for range 12 {
		b.WriteString(`<p>Can ho cao cap ngay trung tam thanh pho, ban giao noi that
		day du, chiet khau len den mot tram trieu dong cho khach hang dat coc trong
		thang nay, ho tro vay ngan hang den bay muoi phan tram gia tri.</p>`)
	}
	b.WriteString(`</div></body></html>`)

	p := read(t, "https://tin.example/quang-cao", b.String())
	if p.Text != "" {
		t.Errorf("an advertisement produced text:\n%s", p.Text)
	}
}

// A masthead is a line of text and a phone number. It has few enough links in
// it to pass the ratio, so the length is what keeps it out.
func TestAMastheadIsTooShortToBeContent(t *testing.T) {
	doc := `<html lang="vi"><body>
	<div class="header-content">Bao Dong Thap. Duong so 1, phuong 1. Dien thoai 0277 3851 246.</div>
	<div class="content">
	  <p>Vu lua he thu nam nay o huyen Thap Muoi cho nang suat binh quan bay tan
	  mot hecta, cao hon cung ky nam ngoai gan mot tan. Gia lua tuoi tai ruong
	  dang duoc thuong lai mua vao khoang tam nghin dong mot kilogam.</p>
	  <p>Theo Phong Nong nghiep huyen, toan huyen xuong giong hon ba muoi sau
	  nghin hecta, trong do giong lua chat luong cao chiem tren tam muoi phan
	  tram dien tich.</p>
	</div></body></html>`

	p := read(t, "https://baodongthap.example/tin-123.html", doc)
	if strings.Contains(p.Text, "Dien thoai") {
		t.Errorf("the masthead is in the text:\n%s", p.Text)
	}
	if !strings.Contains(p.Text, "nang suat binh quan bay tan") {
		t.Errorf("the article is not in the text:\n%s", p.Text)
	}
}
