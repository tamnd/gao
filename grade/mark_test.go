package grade

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/normalize"
)

// The pages the diacritic verifier is tested against. They are real sentences
// rather than syllables in a row, because the thing being checked is whether an
// answer is the page with marks added, and a page nobody would write is a page
// no answer would be written against.
var pages = []string{
	"Một âm tiết viết không dấu trong tiếng Việt có thể ứng với nhiều từ khác hẳn nhau về nghĩa, và người đọc quen với ngôn ngữ này khôi phục dấu một cách tự nhiên nhờ ngữ cảnh.",
	"Chữ quốc ngữ ghi thanh điệu bằng dấu đặt trên hoặc dưới nguyên âm, cho nên một trang gõ thiếu dấu vẫn đọc được nhưng phải đoán, và người đoán sai thì hiểu sai câu.",
	"Bộ gõ tiếng Việt trên máy tính thường dùng kiểu Telex hoặc VNI, và phần mềm nào cũng phải xử lý được cả hai nếu muốn nhận văn bản người dùng thật sự gõ ra.",
}

func learned(t *testing.T) *Mark {
	t.Helper()
	d := NewMark()
	for _, p := range pages {
		if !d.Learn(p) {
			t.Fatalf("a page of real Vietnamese was refused: %q", p)
		}
	}
	if d.Items() != len(pages) {
		t.Fatalf("the key holds %d pages, want %d", d.Items(), len(pages))
	}
	return d
}

func TestThePageItselfScoresOne(t *testing.T) {
	d := learned(t)
	for _, page := range pages {
		v := d.Verify(normalize.Bare(page), page)
		if !v.Checked {
			t.Fatalf("the answer key itself came back ungraded: %s", v.Why)
		}
		if v.Reward != 1 {
			t.Errorf("the page scored %v against itself: %s", v.Reward, v.Why)
		}
	}
}

func TestTheThreeCheapAnswersAllScoreZero(t *testing.T) {
	d := learned(t)
	page := pages[0]
	prompt := normalize.Bare(page)

	// The empty answer. It contains nothing wrong, which is exactly why a check
	// written in terms of what an answer must not contain would pass it.
	if v := d.Verify(prompt, ""); !v.Checked || v.Reward != 0 {
		t.Errorf("the empty answer scored %v, checked %v", v.Reward, v.Checked)
	}

	// The prompt handed back. This is the answer a model produces when it learns
	// that copying is safe, and it is the one that has to be graded rather than
	// refused: it is a real attempt at the task that restored no marks at all.
	v := d.Verify(prompt, prompt)
	if !v.Checked {
		t.Fatalf("the bare page came back ungraded, which would hide the laziest possible answer: %s", v.Why)
	}
	if v.Reward != 0 {
		t.Errorf("handing the question back scored %v: %s", v.Reward, v.Why)
	}

	// The shape and none of the substance: a page of Vietnamese with marks all
	// over it that is not this page.
	if v := d.Verify(prompt, pages[1]); !v.Checked || v.Reward != 0 {
		t.Errorf("a different page scored %v, checked %v: %s", v.Reward, v.Checked, v.Why)
	}
}

func TestHalfTheMarksScoreAboutHalf(t *testing.T) {
	d := learned(t)
	page := pages[0]
	prompt := normalize.Bare(page)

	// Strip the marks off every second syllable and leave the rest alone.
	words := strings.Fields(page)
	for i := range words {
		if i%2 == 1 {
			words[i] = normalize.Bare(words[i])
		}
	}
	v := d.Verify(prompt, strings.Join(words, " "))
	if !v.Checked {
		t.Fatalf("a half restored page came back ungraded: %s", v.Why)
	}
	if v.Reward <= 0.2 || v.Reward >= 0.8 {
		t.Errorf("a half restored page scored %v, which is not between the page and the question: %s", v.Reward, v.Why)
	}
}

func TestAnAnswerCutOffPartWayIsNotAWrongAnswer(t *testing.T) {
	d := learned(t)
	page := pages[0]
	prompt := normalize.Bare(page)

	cut := []rune(page)[:40]
	v := d.Verify(prompt, string(cut))
	if v.Checked {
		t.Fatalf("a rollout that stopped early was graded as a wrong answer, which teaches the model to answer briefly: %s", v.Why)
	}
	if !strings.Contains(v.Why, "cut off") {
		t.Errorf("the verdict does not say what happened: %q", v.Why)
	}
}

func TestAnAnswerThatRewroteTheTextIsNotAlignedAgainstThePage(t *testing.T) {
	d := learned(t)
	page := pages[0]
	prompt := normalize.Bare(page)

	// The page with two syllables added in the middle. Aligned against the page
	// this would collect most of its marks, and partial credit for a paraphrase
	// is how a restoration arm learns to paraphrase.
	rewritten := strings.Replace(page, "tiếng Việt", "tiếng Việt hiện nay", 1)
	v := d.Verify(prompt, rewritten)
	if !v.Checked {
		t.Fatalf("a rewrite came back ungraded rather than wrong: %s", v.Why)
	}
	if v.Reward != 0 {
		t.Errorf("a rewrite scored %v: %s", v.Reward, v.Why)
	}
}

func TestAPageReadOffDiskEndsInANewlineAndTheKeyStillMatches(t *testing.T) {
	d := NewMark()
	if !d.Learn(pages[0] + "\n") {
		t.Fatal("a page with a trailing newline was refused")
	}
	v := d.Verify(normalize.Bare(pages[0]), pages[0])
	if !v.Checked {
		t.Fatalf("a document read off disk and a prompt read out of a rollout file missed each other: %s", v.Why)
	}
	if v.Reward != 1 {
		t.Errorf("the page scored %v against itself: %s", v.Reward, v.Why)
	}
	// The same page with and without the newline is one key, not two.
	if !d.Learn(pages[0]) {
		t.Error("the same page was read as a second answer to the same question")
	}
	if d.Items() != 1 {
		t.Errorf("the key holds %d pages", d.Items())
	}
}

func TestAPromptTheKeyDoesNotHoldIsNotGraded(t *testing.T) {
	d := learned(t)
	v := d.Verify("mot trang khong co trong khoa", "một trang không có trong khóa")
	if v.Checked {
		t.Fatal("an answer to a question with no key came back graded")
	}
}

func TestAPageTypedWithoutMarksIsNotAnAnswerKey(t *testing.T) {
	d := NewMark()
	if d.Learn(normalize.Bare(pages[0])) {
		t.Fatal("a page with the marks already off it was taken as a key, and it is a second copy of the question")
	}
	if d.Learn("   ") {
		t.Fatal("an empty page was taken as a key")
	}
	if d.Items() != 0 {
		t.Errorf("the key holds %d pages after refusing both", d.Items())
	}
}

func TestTwoPagesDifferingOnlyInTheirMarksAreBothDropped(t *testing.T) {
	page := pages[0]
	other := strings.Replace(page, "khác hẳn", "khạc hẫn", 1)
	if other == page {
		t.Fatal("the two pages are the same, so this test checks nothing")
	}

	d := NewMark()
	if !d.Learn(page) {
		t.Fatal("the first page was refused")
	}
	if d.Learn(other) {
		t.Fatal("a second answer to the same question was taken")
	}
	if d.Items() != 0 {
		t.Errorf("the key still holds %d pages, and a prompt with two answers scores a correct restoration wrong about half the time", d.Items())
	}
	if v := d.Verify(normalize.Bare(page), page); v.Checked {
		t.Error("the ambiguous prompt is still being graded")
	}
}

func TestTheMarkedShareIsCountedOverLettersRatherThanBytes(t *testing.T) {
	if got := markedShare(""); got != 0 {
		t.Errorf("an empty page is %v marked", got)
	}
	if got := markedShare("1234567890 !?"); got != 0 {
		t.Errorf("a line with no letters in it is %v marked", got)
	}
	if got := markedShare(pages[0]); got < MinMarked {
		t.Errorf("a page of real Vietnamese is %.3f marked, under the %.2f floor", got, MinMarked)
	}
	if got := markedShare("ăâêôơưđ"); got != 1 {
		t.Errorf("a run of marked letters is %v marked", got)
	}
}

func TestTheVerdictSaysWhatItCounted(t *testing.T) {
	d := learned(t)
	v := d.Verify(normalize.Bare(pages[0]), pages[0])
	if !strings.Contains(v.Why, "marks came back") || !strings.Contains(v.Why, "syllables") {
		t.Errorf("the verdict does not say what it counted: %q", v.Why)
	}
	if v.Specialist != "dau" {
		t.Errorf("the verdict is attributed to %q", v.Specialist)
	}
}
