package dau

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/phoi"
)

// counted is the text the baseline table is built from. It is not the text the
// items are made from, for the reason in the lexicon comment: a table is
// trivially perfect on the pages it counted, and a baseline measured on its own
// training data is the same mistake as a benchmark measured on the model's.
var counted = []string{
	`Thủ đô của Việt Nam nằm bên bờ một con sông lớn ở phía bắc. Thành phố này có lịch sử rất lâu đời và giữ được nhiều khu phố cũ, nơi mỗi con phố ngày xưa bán một mặt hàng riêng và tên phố còn nhắc lại nghề đó đến tận bây giờ.`,
	`Người nông dân ở đồng bằng phía nam cấy lúa hai đến ba vụ mỗi năm, tùy theo con nước lên xuống. Sau khi gặt, họ phơi thóc cho khô rồi đem xay, sàng và nhặt sạch trước khi nấu. Đó là công việc lặp lại hàng trăm năm nay và ai lớn lên ở đây cũng biết làm.`,
	`Ngôn ngữ này có sáu thanh điệu và chỉ năm trong số đó được ghi bằng dấu, cho nên một âm tiết viết không dấu có thể là nhiều từ khác nhau. Người đọc khôi phục lại nhờ ngữ cảnh mà không phải nghĩ, còn máy thì phải học, và đó là lý do bài toán này khó hơn nó trông có vẻ.`,
	`Mùa mưa mang nước từ thượng nguồn về làm ngập những cánh đồng ở miền tây và để lại phù sa cho đất. Người dân gọi đó là mùa nước nổi, họ chờ nó chứ không sợ nó, vì cùng với nước là cá và là những thứ chỉ có trong vài tháng mỗi năm.`,
}

func lexicon(t *testing.T) *Lexicon {
	t.Helper()
	l := NewLexicon()
	for i, p := range counted {
		if !l.Add(p) {
			t.Fatalf("counting text %d was refused", i)
		}
	}
	if l.Size() == 0 {
		t.Fatal("the lexicon counted nothing")
	}
	return l
}

// The number this exists to produce. A model that does not beat the table has
// learned the dictionary and not the language, and without publishing the
// table's score nobody reading the model's can tell.
func TestTheTableBeatsDoingNothingAndIsNotPerfect(t *testing.T) {
	l := lexicon(t)

	var table, nothing Report
	for i, p := range pages {
		it := NewItem(hash(i), p)
		table.Add(Grade(it, l.Restore(it.Prompt)))
		nothing.Add(Grade(it, it.Prompt))
	}

	if table.Restored() <= nothing.Restored() {
		t.Errorf("the table restored %.3f of the marks and doing nothing restored %.3f", table.Restored(), nothing.Restored())
	}
	if table.SyllableAccuracy() <= nothing.SyllableAccuracy() {
		t.Errorf("the table got %.3f of syllables right and doing nothing got %.3f",
			table.SyllableAccuracy(), nothing.SyllableAccuracy())
	}
	// And it is a floor rather than a solution. A table with no context cannot
	// choose between the six words that share one bare spelling, so there is
	// room above it for a model to be worth training.
	if table.SyllableAccuracy() >= 1 {
		t.Error("a table with no context answered every syllable correctly, which means the test text is not a real test")
	}
}

// The table has to be faithful by construction: it puts marks on and it changes
// nothing else. An answer that rewrites the text is not a restoration, and a
// baseline that did it would be scoring itself against a different task.
func TestTheTableOnlyEverAddsMarks(t *testing.T) {
	l := lexicon(t)
	for i, p := range pages {
		it := NewItem(hash(i), p)
		got := l.Restore(it.Prompt)
		if phoi.Bare(got) != it.Prompt {
			t.Errorf("page %d: the table changed the letters, not just the marks", i)
		}
	}
}

func TestTheTableKeepsTheCaseItWasAskedIn(t *testing.T) {
	l := NewLexicon()
	if !l.Add(strings.Repeat("Người nông dân ở đồng bằng phía nam trồng lúa nước. ", 8)) {
		t.Fatal("the counting text was refused")
	}

	got := l.Restore("Nguoi nong dan o dong bang phia nam trong lua nuoc.")
	if !strings.HasPrefix(got, "Người") {
		t.Errorf("the capital was lost: %q", got)
	}
	if strings.Contains(got, "NGƯỜI") {
		t.Errorf("the case was invented rather than kept: %q", got)
	}
}

// Ma, má, mà, mả, mã and mạ share one bare spelling and only context separates
// them. The table answers with whichever it saw most, every time, which is the
// exact shape of what a context free baseline cannot do.
func TestTheTableCannotChooseBetweenTheSixWords(t *testing.T) {
	l := NewLexicon()
	// A page of ordinary Vietnamese so the marked floor is cleared, with the
	// awkward syllable in it more than once.
	l.Add(pages[0] + " Người ta nói rằng mà thôi, mà rồi mà nữa, còn má thì khác hẳn.")

	first := l.Restore("ma")
	for range 5 {
		if got := l.Restore("ma"); got != first {
			t.Fatalf("the table answered ma as %q and then as %q", first, got)
		}
	}
	if first == "ma" {
		t.Skip("the counting text did not carry a marked form of ma")
	}
	if phoi.Bare(first) != "ma" {
		t.Errorf("the answer %q is not a spelling of ma", first)
	}
}

// Two runs over the same text have to produce the same table, or the baseline
// is not a number anybody can publish against.
func TestTheTableIsTheSameOnTwoRuns(t *testing.T) {
	first, second := lexicon(t), lexicon(t)
	prompt := phoi.Bare(strings.Join(pages, " "))

	if first.Restore(prompt) != second.Restore(prompt) {
		t.Fatal("two tables counted off the same text answer differently")
	}
	if first.Size() != second.Size() {
		t.Errorf("the tables hold %d and %d bare spellings", first.Size(), second.Size())
	}
}

// Feeding bare text in would teach the table that the right spelling of co is
// co, which is the failure that makes a baseline read as a good result.
func TestTheTableRefusesTextTypedWithoutMarks(t *testing.T) {
	l := NewLexicon()
	bare := phoi.Bare(strings.Join(pages, " "))

	if l.Add(bare) {
		t.Fatal("the table counted a page typed without marks")
	}
	if l.Size() != 0 {
		t.Errorf("the table holds %d spellings after counting nothing", l.Size())
	}
	// And having refused it, it still answers, by leaving what it does not know
	// alone.
	if got := l.Restore("khong dau"); got != "khong dau" {
		t.Errorf("an empty table answered %q rather than leaving the question alone", got)
	}
}

// A syllable the table has never seen comes back unchanged, which is honest and
// is also right more often than not, since plenty of Vietnamese carries no
// marks.
func TestASyllableTheTableHasNeverSeenComesBackUnchanged(t *testing.T) {
	l := lexicon(t)
	if got := l.Restore("zzzqqq"); got != "zzzqqq" {
		t.Errorf("Restore(%q) = %q, want it left alone", "zzzqqq", got)
	}
}

func TestTheTableKeepsEverythingThatIsNotALetter(t *testing.T) {
	l := lexicon(t)
	const prompt = "Ha Noi, nam 2026: tot!\nDong thu hai."
	got := l.Restore(prompt)

	if len(syllables(got)) != len(syllables(prompt)) {
		t.Errorf("the table changed how many syllables there are: %q", got)
	}
	for _, want := range []string{",", ":", "!", "\n", "2026"} {
		if !strings.Contains(got, want) {
			t.Errorf("the table dropped %q from the answer: %q", want, got)
		}
	}
}
