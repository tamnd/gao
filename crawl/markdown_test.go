package crawl

import (
	"strings"
	"testing"
)

// The markdown of a news page is the article with its shape left in, and the
// text of the same page is the same words without it.
func TestThePageComesBackAsMarkdownAndAsText(t *testing.T) {
	p := read(t, "https://baodongthap.vn/nong-nghiep/nong-dan-trung-vu-lua-he-thu-123.html", newsPage)

	if !strings.HasPrefix(p.Markdown, "# Nong dan Dong Thap trung vu lua he thu") {
		t.Errorf("the markdown does not start with the headline as a heading:\n%s", first(p.Markdown, 3))
	}
	if strings.Contains(p.Markdown, "#") && !strings.Contains(p.Text, "Nong dan Dong Thap") {
		t.Error("the text lost the headline the markdown kept")
	}
	if strings.Contains(p.Text, "# ") {
		t.Errorf("the text is carrying markdown:\n%s", first(p.Text, 3))
	}

	// Both renderings come from the container the extractor picked, so neither
	// of them has the navigation or the related headlines in it.
	for _, unwanted := range []string{"The thao", "Gia lua tang tro lai", "Giay phep so"} {
		if strings.Contains(p.Markdown, unwanted) {
			t.Errorf("the markdown picked up %q from outside the article", unwanted)
		}
	}
	for _, wanted := range []string{"bay tan", "Phong Nong nghiep huyen", "vu thu dong"} {
		if !strings.Contains(p.Markdown, wanted) {
			t.Errorf("the markdown lost %q, which is in the article", wanted)
		}
	}
}

// The body is the whole page, which is the column that does not depend on the
// extractor having been right.
func TestTheBodyIsTheWholePageAndNotTheExtractorsOpinionOfIt(t *testing.T) {
	p := read(t, "https://baodongthap.vn/nong-nghiep/nong-dan-trung-vu-lua-he-thu-123.html", newsPage)

	for _, wanted := range []string{"The thao", "Gia lua tang tro lai", "Giay phep so", "bay tan"} {
		if !strings.Contains(p.Body, wanted) {
			t.Errorf("the body lost %q", wanted)
		}
	}
	// Except the parts of a document that are not writing in any rendering.
	if strings.Contains(p.Body, "var ads") {
		t.Error("the body kept a script")
	}
	if len(p.Body) <= len(p.Markdown) {
		t.Errorf("the body is %d bytes and the article alone is %d, so it is not the whole page", len(p.Body), len(p.Markdown))
	}
}

// A page whose article the extractor throws away still has a body.
//
// This is not a hypothetical. vnexpress.net wraps its articles in a div called
// sidebar-1 inside one called header-content, and for as long as this package
// existed both of those words were an outright ban, so three articles fetched
// off the front page all came back as the multiplication sign from a close
// button. That is issue 176, and it is fixed, and fixing it did not bring back
// the pages already crawled.
//
// The extractor will be wrong again. What this covers is that a corpus with a
// body column survives it being wrong, which is the reason to have the column.
// The page here puts its writing inside an aside, which this package refuses
// unconditionally and correctly, so the test does not depend on any threshold.
func TestAPageTheExtractorGivesUpOnStillHasABody(t *testing.T) {
	const page = `<!doctype html><html lang="vi"><head><title>Bao</title></head><body>
	<aside>
	<h1>Thu tuong phe duyet de an giao duc thu do</h1>
	<p>Thu tuong ngay hai muoi thang tam phe duyet de an ve noi dung tren, voi
	muc tieu den nam hai nghin khong tram ba muoi.</p>
	<p>It nhat muoi hai truong se theo dinh huong xep hang hoac giu vai tro
	trung tam ve dao tao tai nang va nghien cuu.</p>
	</aside></body></html>`

	p := read(t, "https://vnexpress.net/de-an-giao-duc-5111470.html", page)
	if strings.Contains(p.Text, "Thu tuong phe duyet") {
		t.Errorf("the extractor read an aside as content, so this test no longer covers what it says:\n%s", p.Text)
	}
	for _, wanted := range []string{"Thu tuong phe duyet", "It nhat muoi hai truong"} {
		if !strings.Contains(p.Body, wanted) {
			t.Errorf("the body lost %q, which is the whole point of the column", wanted)
		}
	}
}

// Headings, lists, quotes, rules and code come out as markdown rather than as
// the words they contained.
func TestTheShapeOfADocumentSurvives(t *testing.T) {
	const page = `<!doctype html><html lang="vi"><body><div class="content">
	<h2>Dieu kien du tuyen</h2>
	<p>Thi sinh can dap ung cac dieu kien sau day truoc khi nop ho so du tuyen
	vao truong trong nam hoc nay.</p>
	<ul><li>Tot nghiep trung hoc pho thong</li>
	<li>Diem trung binh tu bay phay khong<ul><li>Tinh theo hoc ba</li></ul></li></ul>
	<ol><li>Nop ho so truc tuyen</li><li>Nop le phi</li></ol>
	<blockquote>Ho so nop sau han khong duoc xem xet trong bat ky truong hop nao.</blockquote>
	<hr>
	<pre>curl -s https://tuyensinh.example/api</pre>
	</div></body></html>`

	md := read(t, "https://tuyensinh.example/huong-dan", page).Markdown
	for _, want := range []string{
		"## Dieu kien du tuyen",
		"- Tot nghiep trung hoc pho thong",
		"  - Tinh theo hoc ba",
		"1. Nop ho so truc tuyen",
		"2. Nop le phi",
		"> Ho so nop sau han",
		"---",
		"```\ncurl -s https://tuyensinh.example/api\n```",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the markdown is missing %q:\n%s", want, md)
		}
	}
}

// Links and images carry their addresses, resolved against the page they were
// on.
func TestLinksAndImagesKeepTheirAddresses(t *testing.T) {
	const page = `<!doctype html><html lang="vi"><body><div class="content">
	<p>Bo Giao duc da <a href="/van-ban/thong-tu-29.html">ban hanh thong tu</a>
	huong dan viec day them va hoc them tren pham vi ca nuoc trong nam nay.</p>
	<p><img src="../anh/lop-hoc.jpg" alt="Mot lop hoc buoi chieu"></p>
	<p>Xem them tai <a href="https://moet.gov.vn/">cong thong tin</a> cua bo.</p>
	</div></body></html>`

	md := read(t, "https://baogiaoduc.example/tin/2026/thong-tu.html", page).Markdown
	for _, want := range []string{
		"[ban hanh thong tu](https://baogiaoduc.example/van-ban/thong-tu-29.html)",
		"![Mot lop hoc buoi chieu](https://baogiaoduc.example/tin/anh/lop-hoc.jpg)",
		"[cong thong tin](https://moet.gov.vn/)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the markdown is missing %q:\n%s", want, md)
		}
	}
}

// A table comes out as a table, which is the case the text column cannot carry
// at all.
func TestATableComesBackAsATable(t *testing.T) {
	const page = `<!doctype html><html lang="vi"><body><div class="content">
	<p>Diem chuan cac nganh nam nay duoc hoi dong tuyen sinh cong bo chieu nay
	sau khi loc ao xong toan bo nguyen vong tren he thong.</p>
	<table>
	<tr><th>Nganh</th><th>Diem</th></tr>
	<tr><td>Cong nghe thong tin</td><td>26,5</td></tr>
	<tr><td>Kinh te</td><td>25,0</td></tr>
	</table></div></body></html>`

	md := read(t, "https://dhbk.example/diem-chuan", page).Markdown
	for _, want := range []string{
		"| Nganh | Diem |",
		"| --- | --- |",
		"| Cong nghe thong tin | 26,5 |",
		"| Kinh te | 25,0 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the markdown is missing %q:\n%s", want, md)
		}
	}
}

// A layout table renders as nothing, because a corpus full of two by one tables
// holding a logo and a menu is worse than one that leaves them out.
func TestATableHoldingNothingRendersAsNothing(t *testing.T) {
	const page = `<!doctype html><html lang="vi"><body><div class="content">
	<table><tr><td><img src="/logo.gif"></td><td></td></tr></table>
	<p>Cong ty chung toi chuyen cung cap vat tu nong nghiep cho ba con nong dan
	trong khu vuc dong bang song Cuu Long tu nam hai nghin le sau den nay.</p>
	</div></body></html>`

	md := read(t, "https://vattu.example/", page).Markdown
	if strings.Contains(md, "---") {
		t.Errorf("a table with nothing in it was rendered:\n%s", md)
	}
	if !strings.Contains(md, "Cong ty chung toi") {
		t.Errorf("the paragraph after the empty table was lost:\n%s", md)
	}
}

// Text that looks like markup comes back as text.
func TestTextThatLooksLikeMarkupIsNotMarkup(t *testing.T) {
	const page = `<!doctype html><html lang="vi"><body><div class="content">
	<p>Cong thuc tinh la a*b*c va bien duoc dat ten la _tong_ trong tai lieu
	huong dan su dung phan mem ke toan cua don vi.</p>
	<p># Khong phai tieu de</p>
	</div></body></html>`

	md := read(t, "https://ketoan.example/huong-dan", page).Markdown
	if strings.Contains(md, "a*b*c") {
		t.Errorf("a star in the middle of a word was left as emphasis:\n%s", md)
	}
	if strings.Contains(md, "\n# Khong phai tieu de") || strings.HasPrefix(md, "# Khong phai") {
		t.Errorf("a hash at the start of a paragraph was left as a heading:\n%s", md)
	}
	if !strings.Contains(md, "Khong phai tieu de") {
		t.Errorf("the words were lost along with the markup:\n%s", md)
	}
}

// Emphasis with nothing in it emits nothing, rather than a pair of markers a
// reader has to strip.
func TestEmptyEmphasisIsNotRendered(t *testing.T) {
	const page = `<!doctype html><html lang="vi"><body><div class="content">
	<p>Gia xang dau trong nuoc duoc lien bo dieu chinh vao chieu thu nam hang
	tuan theo chu ky dieu hanh moi da duoc ban hanh.<strong></strong><em> </em></p>
	<p>Muc giam lan nay la bay tram dong mot lit doi voi xang RON chin muoi lam
	va nam tram dong doi voi dau diesel.</p>
	</div></body></html>`

	md := read(t, "https://gia.example/xang-dau", page).Markdown
	for _, bad := range []string{"****", "**  **", "* *", "__"} {
		if strings.Contains(md, bad) {
			t.Errorf("the markdown holds %q:\n%s", bad, md)
		}
	}
}

// first is the opening lines of a string, for an error message that says enough
// without printing a whole page.
func first(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
