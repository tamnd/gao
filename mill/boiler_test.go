package mill

import (
	"strings"
	"testing"
)

// The furniture of a made up news site, written the way an extractor hands it
// over: the nav column first, the article, then the share prompt and the notice
// at the foot.
const (
	nav1   = "Trang chủ"
	nav2   = "Thời sự"
	nav3   = "Kinh doanh"
	share  = "Chia sẻ bài viết này lên mạng xã hội"
	notice = "Bản quyền thuộc về báo điện tử. Nghiêm cấm sao chép dưới mọi hình thức."
)

// page is one page of that site: the same furniture around a different body.
func page(body string) string {
	return strings.Join([]string{nav1, nav2, nav3, body, share, notice}, "\n") + "\n"
}

// bodies are the only lines that differ from page to page.
var bodies = []string{
	"Hà Nội bước vào đợt nắng nóng đầu tiên của mùa hè.",
	"Sông Hồng bắt nguồn từ Vân Nam và chảy qua các tỉnh phía bắc.",
	"Năm 1986, đại hội đảng quyết định chuyển sang nền kinh tế nhiều thành phần.",
	"Giá vé tàu tết được công bố sớm hơn mọi năm.",
	"Đội tuyển thắng hai trận liên tiếp trên sân nhà.",
	"Kỳ thi tốt nghiệp năm nay giữ nguyên bốn môn.",
}

// boiler counts one host's pages and hands back the counter, which is the first
// pass every test here needs before it can ask about the second.
func boiler(t *testing.T, host string, texts ...string) *Boiler {
	t.Helper()
	b := NewBoiler(DefaultFurniture())
	for _, s := range texts {
		b.Count(host, s)
	}
	return b
}

func pages() []string {
	out := make([]string, 0, len(bodies))
	for _, body := range bodies {
		out = append(out, page(body))
	}
	return out
}

// The case the pass exists for. None of these pages is a copy of another one, so
// deduplication by document keeps all six, and the copyright notice arrives six
// times.
func TestTheFurnitureOfAHostComesOutAndTheArticleStays(t *testing.T) {
	all := pages()
	b := boiler(t, "vnbao.vn", all...)

	got := b.Strip("vnbao.vn", all[0])
	if got.Lines != 6 {
		t.Errorf("the page had %d lines, want 6", got.Lines)
	}
	if got.Removed != 5 {
		t.Errorf("removed %d lines, want the three nav items, the share prompt and the notice", got.Removed)
	}
	if strings.TrimSpace(got.Text) != bodies[0] {
		t.Errorf("what is left is %q, want the article on its own", got.Text)
	}
	if got.Emptied {
		t.Error("a page with an article in it was reported as emptied")
	}
}

// Host aware, and this is the reason. "Đọc thêm" repeated across one site is
// that site's furniture. The same words repeated across the corpus are
// Vietnamese, and a pass that counted globally would take the language out a
// phrase at a time.
func TestALineIsFurnitureOnTheHostThatRepeatsItAndNowhereElse(t *testing.T) {
	b := NewBoiler(DefaultFurniture())
	for _, p := range pages() {
		b.Count("vnbao.vn", p)
	}
	// One post on a forum happens to end with the same sentence.
	post := "Mình vừa đọc xong bài này.\n" + share + "\n"
	b.Count("diendan.vn", post)

	if !b.Removes("vnbao.vn", share) {
		t.Error("the share prompt is on every page of the site and was not called furniture")
	}
	if b.Removes("diendan.vn", share) {
		t.Error("one forum post using the same sentence had it called furniture")
	}
	if got := b.Strip("diendan.vn", post); got.Removed != 0 {
		t.Errorf("the forum post lost %d lines, want 0", got.Removed)
	}
}

// A host has to be big enough for a share of it to mean anything. Three pages
// that agree on a sentence are not evidence that the sentence is furniture, and
// a corpus that trimmed on that evidence would be trimming noise.
func TestASmallHostKeepsEverything(t *testing.T) {
	all := pages()[:3]
	b := boiler(t, "quan.vn", all...)

	got := b.Strip("quan.vn", all[0])
	if got.Removed != 0 {
		t.Errorf("removed %d lines from a host with three documents, want 0", got.Removed)
	}
	if got.Text != all[0] {
		t.Error("a host with three documents came back changed")
	}
}

// Repetition inside one document is a different problem with its own measure
// in sift. Counting it here would let one page argue that its own refrain is
// the whole site's furniture.
func TestALineRepeatedInsideOneDocumentIsNotTheHostsFurniture(t *testing.T) {
	refrain := "Xem tiếp bên dưới"
	chorus := strings.Repeat(refrain+"\n", 20) + "Bài viết về mùa hè năm nay.\n"

	texts := append([]string{chorus}, pages()...)
	b := boiler(t, "vnbao.vn", texts...)

	if b.Removes("vnbao.vn", refrain) {
		t.Error("a line one page repeated twenty times was called the site's furniture")
	}
	if got := b.Strip("vnbao.vn", chorus); got.Removed != 0 {
		t.Errorf("the page lost %d of its own repeated lines, want 0", got.Removed)
	}
}

// A page that is nothing but furniture is a real thing on the web. Losing it
// silently is not the same as recording that it went.
func TestAPageThatIsOnlyFurnitureIsReportedAsEmptied(t *testing.T) {
	all := append(pages(), page(""))
	b := boiler(t, "vnbao.vn", all...)

	got := b.Strip("vnbao.vn", page(""))
	if !got.Emptied {
		t.Errorf("a page of nothing but furniture came back as %q and was not reported as emptied", got.Text)
	}
	if strings.TrimSpace(got.Text) != "" {
		t.Errorf("what is left of it is %q, want nothing", got.Text)
	}
}

// The same footer under two content management systems is one footer. It is the
// deduplication key that decides, so the curly quotes and the capitals of one
// template do not make a second line out of it.
func TestAFooterIsTheSameFooterUnderAnotherTemplate(t *testing.T) {
	texts := make([]string, 0, len(bodies))
	for i, body := range bodies {
		foot := notice
		if i%2 == 0 {
			foot = strings.ToUpper(notice) + "!"
		}
		texts = append(texts, body+"\n"+foot+"\n")
	}
	b := boiler(t, "vnbao.vn", texts...)

	for i, text := range texts {
		if got := b.Strip("vnbao.vn", text); got.Removed != 1 {
			t.Errorf("page %d lost %d lines, want the notice however it was rendered", i, got.Removed)
		}
	}
}

// A host the first pass never saw is not an error. It happens when a part is
// stripped against counts taken over a different part, and the honest answer is
// that nothing is known to repeat.
func TestAnUnknownHostComesBackWhole(t *testing.T) {
	b := boiler(t, "vnbao.vn", pages()...)

	text := page(bodies[0])
	got := b.Strip("khongbiet.vn", text)
	if got.Text != text {
		t.Error("a document from a host the pass never counted came back changed")
	}
	if got.Lines != 6 {
		t.Errorf("counted %d lines of it, want 6", got.Lines)
	}
	if got.Removed != 0 {
		t.Errorf("removed %d lines from it, want 0", got.Removed)
	}
}

// The report is read by a person deciding whether the thresholds are right, so
// it leads with the site the pass is doing the most to and shows what it took.
func TestTheReportLeadsWithTheHostThePassDidTheMostTo(t *testing.T) {
	b := NewBoiler(DefaultFurniture())
	quiet := make([]string, 0, len(bodies))
	for _, body := range bodies {
		quiet = append(quiet, body+"\n"+notice+"\n")
	}
	for _, p := range pages() {
		b.Count("vnbao.vn", p)
	}
	for _, q := range quiet {
		b.Count("yen.vn", q)
	}
	for _, p := range pages() {
		b.Strip("vnbao.vn", p)
	}
	for _, q := range quiet {
		b.Strip("yen.vn", q)
	}

	if b.Hosts() != 2 {
		t.Fatalf("counted %d hosts, want 2", b.Hosts())
	}
	got := b.Reports()
	if got[0].Host != "vnbao.vn" {
		t.Errorf("the report leads with %s, want the site five lines came off every page of", got[0].Host)
	}
	if got[0].Removed != 30 {
		t.Errorf("it removed %d lines, want 5 from each of 6 pages", got[0].Removed)
	}
	if got[0].Furniture != 5 {
		t.Errorf("it found %d furniture lines, want 5", got[0].Furniture)
	}
	if got[0].Documents != 6 {
		t.Errorf("it counted %d documents, want 6", got[0].Documents)
	}
	if got[1].Removed != 6 {
		t.Errorf("the quiet site lost %d lines, want its notice once per page", got[1].Removed)
	}
	if len(got[1].Samples) != 1 || got[1].Samples[0] != notice {
		t.Errorf("its sample is %q, want the notice", got[1].Samples)
	}
}

// A report of a host nothing was stripped from still says how many documents and
// distinct lines it holds, because a pass that did nothing to a site is a fact
// about the thresholds.
func TestAHostNothingWasStrippedFromStillReports(t *testing.T) {
	b := boiler(t, "quan.vn", pages()[:2]...)

	got := b.Reports()
	if len(got) != 1 {
		t.Fatalf("reported %d hosts, want 1", len(got))
	}
	if got[0].Documents != 2 {
		t.Errorf("counted %d documents, want 2", got[0].Documents)
	}
	if got[0].Lines != 7 {
		t.Errorf("counted %d distinct lines, want 7, the five of the furniture and the two bodies", got[0].Lines)
	}
	if got[0].Furniture != 0 || got[0].Removed != 0 {
		t.Errorf("a host too small to trim reports %d furniture lines and %d removed", got[0].Furniture, got[0].Removed)
	}
}

// The share rule is what makes the copies rule survive a host being large. A
// sentence three of a thousand pages carry is not that site's furniture.
func TestOnALargeHostThreeCopiesAreNotEnough(t *testing.T) {
	b := NewBoiler(DefaultFurniture())
	rare := "Bài này thuộc chuyên đề mùa hè."
	for i := range 100 {
		text := bodies[i%len(bodies)] + " " + string(rune('a'+i%26)) + "\n" + notice + "\n"
		if i < 3 {
			text += rare + "\n"
		}
		b.Count("lon.vn", text)
	}
	if b.Removes("lon.vn", rare) {
		t.Error("a line on three pages of a hundred was called furniture")
	}
	if !b.Removes("lon.vn", notice) {
		t.Error("a line on all hundred pages was not called furniture")
	}
}

// Blank lines are layout, and layout is settled in normalize. This pass leaves
// them where they are rather than counting them as the most repeated line on
// every site in the corpus.
func TestBlankLinesAreLeftAlone(t *testing.T) {
	texts := make([]string, 0, len(bodies))
	for _, body := range bodies {
		texts = append(texts, body+"\n\n"+notice+"\n")
	}
	b := boiler(t, "vnbao.vn", texts...)

	got := b.Strip("vnbao.vn", texts[0])
	if got.Removed != 1 {
		t.Errorf("removed %d lines, want the notice only", got.Removed)
	}
	if got.Text != bodies[0]+"\n\n" {
		t.Errorf("what is left is %q, want the body and the blank line after it", got.Text)
	}
}

// The samples a host keeps are bounded, because the report is read by a person
// and the alternative grows with the corpus.
func TestAHostKeepsAFewSamplesAndNotAllOfThem(t *testing.T) {
	b := NewBoiler(DefaultFurniture())
	furniture := make([]string, 0, SampleLines+5)
	for i := range SampleLines + 5 {
		furniture = append(furniture, "Mục "+string(rune('A'+i)))
	}
	texts := make([]string, 0, len(bodies))
	for _, body := range bodies {
		texts = append(texts, strings.Join(furniture, "\n")+"\n"+body+"\n")
	}
	for _, text := range texts {
		b.Count("vnbao.vn", text)
	}
	for _, text := range texts {
		b.Strip("vnbao.vn", text)
	}

	got := b.Reports()[0]
	if got.Furniture != len(furniture) {
		t.Errorf("found %d furniture lines, want %d", got.Furniture, len(furniture))
	}
	if len(got.Samples) != SampleLines {
		t.Errorf("kept %d samples, want %d", len(got.Samples), SampleLines)
	}
}
