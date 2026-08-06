package dau

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/phoi"
	"github.com/tamnd/gao/soi"
)

// pages is real Vietnamese prose, long enough to clear the length floor. The
// numbers in this file are measured off it rather than asserted, the way soi
// measures the shape of the language, because a task set whose selection rules
// are tuned to one paragraph is a task set that works on one paragraph.
var pages = []string{
	`Hà Nội là thủ đô của nước Cộng hòa xã hội chủ nghĩa Việt Nam, nằm ở phía bắc, bên bờ sông Hồng. Thành phố có lịch sử hơn một nghìn năm kể từ khi vua Lý Thái Tổ dời đô về đây và đặt tên là Thăng Long, nghĩa là rồng bay lên. Khu phố cổ với ba mươi sáu phố phường vẫn giữ được cách bố trí từ thời xưa, mỗi phố bán một mặt hàng riêng, và tên phố ngày nay vẫn nhắc lại nghề cũ dù nghề ấy đã mất từ lâu.`,

	`Cây lúa nước được trồng ở đồng bằng sông Cửu Long từ hàng trăm năm nay và là nguồn sống của phần lớn nông dân trong vùng. Mỗi năm người ta cấy hai đến ba vụ, tùy theo con nước và tùy theo giống lúa. Sau khi gặt, hạt thóc được phơi khô trên sân, rồi đem xay để tách vỏ trấu, sàng để loại bỏ tấm và cám, và cuối cùng là nhặt bỏ những hạt sạn còn sót lại trước khi nấu thành cơm.`,

	`Tiếng Việt có sáu thanh điệu, trong đó năm thanh được ghi bằng dấu và một thanh không có dấu nào. Điều đó khiến cho một âm tiết viết không dấu có thể ứng với nhiều từ khác hẳn nhau về nghĩa. Người đọc quen với ngôn ngữ này khôi phục dấu một cách tự nhiên nhờ ngữ cảnh, nhưng máy thì phải học điều đó, và đây chính là lý do bài toán khôi phục dấu vừa dễ tạo dữ liệu vừa khó giải cho đúng.`,

	`Vào mùa mưa, nước từ thượng nguồn đổ về làm ngập các cánh đồng ở miền Tây, mang theo phù sa bồi đắp cho đất. Người dân địa phương gọi đó là mùa nước nổi và họ chờ đợi nó chứ không sợ nó, vì cùng với nước là cá linh, là bông điên điển, là những thứ chỉ có trong vài tháng ngắn ngủi mỗi năm. Khi nước rút, ruộng lại được cày và một vụ mới bắt đầu.`,
}

func hash(i int) doc.Hash { return doc.SumString(pages[i%len(pages)]) }

func TestTheQuestionIsThePageWithItsMarksTakenOff(t *testing.T) {
	it := NewItem(hash(0), pages[0])

	if it.Answer != pages[0] {
		t.Error("the answer is not the page it was made from")
	}
	if it.Prompt != phoi.Bare(pages[0]) {
		t.Error("the prompt is not the page with its marks off")
	}
	if _, marked := marks(it.Prompt); marked != 0 {
		t.Errorf("the prompt still carries %d marked characters", marked)
	}
	if it.Marked == 0 {
		t.Fatal("the answer carries no marks, so there is nothing to restore")
	}
	// The item's own counts are what a report divides by, so they have to be the
	// answer's rather than the prompt's.
	if it.Chars != len([]rune(pages[0])) {
		t.Errorf("chars = %d, want %d", it.Chars, len([]rune(pages[0])))
	}
}

// Every page of real Vietnamese carries marks at roughly the rate the language
// does, which is what the selection floor is set against.
func TestTheMarkedShareOfARealPage(t *testing.T) {
	for i, p := range pages {
		got := markedShare(p)
		if got < 0.20 || got > 0.32 {
			t.Errorf("page %d is %.3f marked, which is outside the range real Vietnamese sits in", i, got)
		}
		if got < Default().MinMarked {
			t.Errorf("page %d is below the selection floor at %.3f", i, got)
		}
	}
}

func TestAPerfectAnswerScoresPerfectly(t *testing.T) {
	it := NewItem(hash(0), pages[0])
	r := Grade(it, it.Answer)

	if !r.Faithful {
		t.Error("the page is not a faithful restoration of itself")
	}
	if r.Score.DER() != 0 {
		t.Errorf("DER = %.4f, want 0", r.Score.DER())
	}
	if r.Right != r.Syllables {
		t.Errorf("%d of %d syllables right, want all", r.Right, r.Syllables)
	}
	if r.Syllables == 0 {
		t.Fatal("the page has no syllables")
	}
}

// This is the number that has to be published with every result. A model that
// hands the question back has restored nothing, and character accuracy still
// calls it three quarters right.
func TestAnsweringWithTheQuestionRestoresNothing(t *testing.T) {
	var report Report
	for i, p := range pages {
		it := NewItem(hash(i), p)
		report.Add(Grade(it, it.Prompt))
	}

	if report.Restored() != 0 {
		t.Errorf("leaving the text bare restored %.4f of the marks, want none", report.Restored())
	}
	if report.Score.DER() != 1 {
		t.Errorf("DER = %.4f, want 1", report.Score.DER())
	}
	// Syllable accuracy is not zero, and that is the second half of the point.
	// Plenty of Vietnamese syllables carry no marks, so doing nothing gets every
	// one of those right for free. The floor is exactly the share of the page
	// that was never marked to begin with.
	var free int
	for _, p := range pages {
		for _, s := range syllables(p) {
			if phoi.Bare(s) == s {
				free++
			}
		}
	}
	if report.Right != free {
		t.Errorf("a bare answer got %d syllables right and %d of them carry no marks", report.Right, free)
	}
	if acc := report.SyllableAccuracy(); acc < 0.05 || acc > 0.25 {
		t.Errorf("syllable accuracy on a bare answer is %.3f, and a floor outside that range means the pages are not ordinary Vietnamese", acc)
	}
	// Faithful, because the bare text is the question exactly. That is the whole
	// trap: the answer passes every structural check and is worth nothing.
	if report.Unfaithful != 0 {
		t.Errorf("%d bare answers were called unfaithful", report.Unfaithful)
	}
	if acc := report.Score.Accuracy(); acc < 0.68 || acc > 0.82 {
		t.Errorf("character accuracy on a bare answer is %.3f, and the point of this test is that it is high", acc)
	}
}

func TestAnAnswerThatRewritesTheTextIsNotARestoration(t *testing.T) {
	it := NewItem(hash(0), pages[0])
	r := Grade(it, "Hà Nội là thủ đô. Thành phố này rất đẹp và có nhiều di tích lịch sử.")

	if r.Faithful {
		t.Error("an answer with different words was called a faithful restoration")
	}
	// Syllables are not scored on an unfaithful answer, because the two
	// sequences do not line up and a count that guessed at the alignment would
	// be a judgment dressed as a number.
	if r.Right != 0 {
		t.Errorf("%d syllables were counted right against an answer that was rewritten", r.Right)
	}
	if r.Syllables == 0 {
		t.Error("the page's syllables were not counted")
	}
}

func TestAReportIsTheSumOfItsResults(t *testing.T) {
	var report Report
	var syllables int
	for i, p := range pages {
		it := NewItem(hash(i), p)
		r := Grade(it, it.Answer)
		syllables += r.Syllables
		report.Add(r)
	}

	if report.Items != len(pages) {
		t.Errorf("items = %d, want %d", report.Items, len(pages))
	}
	if report.Syllables != syllables {
		t.Errorf("syllables = %d, want %d", report.Syllables, syllables)
	}
	if report.SyllableAccuracy() != 1 {
		t.Errorf("syllable accuracy on perfect answers is %.4f, want 1", report.SyllableAccuracy())
	}
	if report.Restored() != 1 {
		t.Errorf("restored = %.4f, want 1", report.Restored())
	}
}

func TestAnEmptyReportDoesNotDivideByZero(t *testing.T) {
	var report Report
	if got := report.SyllableAccuracy(); got != 0 {
		t.Errorf("syllable accuracy of nothing = %v, want 0", got)
	}
	if got := report.Restored(); got != 1 {
		t.Errorf("restored of nothing = %v, want 1, since no mark was lost", got)
	}
}

func TestSyllablesAreMaximalRunsOfLetters(t *testing.T) {
	got := syllables("Việt Nam, năm 2026: tốt!")
	want := []string{"Việt", "Nam", "năm", "tốt"}
	if len(got) != len(want) {
		t.Fatalf("syllables = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("syllable %d = %q, want %q", i, got[i], want[i])
		}
	}
	// The same rule doc uses to count the column, so the two agree by
	// construction rather than by coincidence.
	if n := doc.Syllables("Việt Nam, năm 2026: tốt!"); int(n) != len(got) {
		t.Errorf("doc counts %d syllables and this splits into %d", n, len(got))
	}
}

func TestTheSixWordsThatShareOneSpelling(t *testing.T) {
	// Every one of these is a real word and they all bare down to ma. This is
	// the part of the task that context has to do and a table cannot.
	for _, w := range []string{"ma", "má", "mà", "mả", "mã", "mạ"} {
		if got := phoi.Bare(w); got != "ma" {
			t.Errorf("Bare(%q) = %q, want ma", w, got)
		}
	}
	// And they are six different answers, not one, which soi's confusion matrix
	// is what makes visible.
	seen := map[soi.Tone]bool{}
	for _, w := range []string{"ma", "má", "mà", "mả", "mã", "mạ"} {
		seen[soi.Letters(w)[1].Tone] = true
	}
	if len(seen) != 6 {
		t.Errorf("the six spellings carry %d distinct tones, want 6", len(seen))
	}
}

func TestTheTaskSetHasAPublishedName(t *testing.T) {
	if Name != "vi-diacritic" {
		t.Errorf("Name = %q, and it is the string nhat checks the corpus against", Name)
	}
	if strings.TrimSpace(Name) != Name {
		t.Error("the name has whitespace on it")
	}
}
