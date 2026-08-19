package grade

import (
	"math"
	"strings"
	"testing"
)

// register holds four instruments that exist, with the article counts that make
// a citation past the end of one catchable.
func register(t *testing.T) *Register {
	t.Helper()
	r := NewRegister()
	for _, e := range []struct {
		kind     string
		id       string
		articles int
	}{
		{"luật", "24/2018/QH14", 43},
		{"bộ luật", "91/2015/QH13", 689},
		{"nghị định", "13/2023/NĐ-CP", 44},
		{"quyết định", "749/QĐ-TTg", 0},
	} {
		if !r.Add(e.kind, e.id, e.articles) {
			t.Fatalf("the register refused %s số %s", e.kind, e.id)
		}
	}
	if r.Size() != 4 {
		t.Fatalf("the register holds %d instruments, want 4", r.Size())
	}
	return r
}

func TestACitationIsReadOffTheSentenceTheWayItWasWritten(t *testing.T) {
	got := Citations("Việc này được quy định tại Nghị định số 13/2023/NĐ-CP về bảo vệ dữ liệu cá nhân.")
	if len(got) != 1 {
		t.Fatalf("read %d citations out of one sentence", len(got))
	}
	c := got[0]
	if c.Kind != "nghị định" || c.Number != 13 || c.Year != 2023 || c.Body != "NĐ-CP" {
		t.Fatalf("read %+v", c)
	}
	if c.ID() != "13/2023/NĐ-CP" {
		t.Errorf("the identifier came back as %q", c.ID())
	}
}

func TestAnInstrumentWithNoYearInItsNumberIsStillRead(t *testing.T) {
	got := Citations("Chương trình được phê duyệt tại Quyết định 749/QĐ-TTg.")
	if len(got) != 1 {
		t.Fatalf("read %d citations", len(got))
	}
	if got[0].Year != 0 || got[0].ID() != "749/QĐ-TTg" {
		t.Errorf("read %+v", got[0])
	}
}

func TestTheArticleInFrontOfAnInstrumentBelongsToIt(t *testing.T) {
	got := Citations("Theo Điều 17 của Luật số 24/2018/QH14 thì trách nhiệm thuộc về bên thu thập.")
	if len(got) != 1 {
		t.Fatalf("read %d citations", len(got))
	}
	if got[0].Article != 17 {
		t.Errorf("the article came back as %d", got[0].Article)
	}
	if want := "Điều 17 luật số 24/2018/QH14"; got[0].String() != want {
		t.Errorf("the citation renders as %q, want %q", got[0].String(), want)
	}
}

func TestBoLuatIsNotReadAsLuat(t *testing.T) {
	got := Citations("Bộ luật số 91/2015/QH13 quy định khác.")
	if len(got) != 1 {
		t.Fatalf("read %d citations", len(got))
	}
	if got[0].Kind != "bộ luật" {
		t.Errorf("the instrument came back as %q, and read as a luật it would be checked against the wrong entry", got[0].Kind)
	}
}

func TestOneInstrumentCitedFiveTimesIsOneCitation(t *testing.T) {
	text := strings.Repeat("Theo Nghị định số 13/2023/NĐ-CP. ", 5)
	if got := Citations(text); len(got) != 1 {
		t.Fatalf("read %d citations, and counting the repeats lets an answer raise its precision by restating what it was already sure of", len(got))
	}
	// Two different articles of the same instrument are two citations, since
	// they point at different text and either one can be wrong on its own.
	got := Citations("Điều 3 Nghị định số 13/2023/NĐ-CP và Điều 9 Nghị định số 13/2023/NĐ-CP.")
	if len(got) != 2 {
		t.Fatalf("two articles of one instrument read as %d citations", len(got))
	}
}

func TestAnIssuingCodeInLowerCaseIsNotACitation(t *testing.T) {
	if got := Citations("theo nghị định số 13/2023/nđ-cp"); len(got) != 0 {
		t.Errorf("read %d citations out of a line with no issuing code in it", len(got))
	}
}

func TestAnInstrumentCannotCarryAnotherBodysCode(t *testing.T) {
	got := Citations("Nghị định số 13/2023/QĐ-TTg quy định như sau.")
	if len(got) != 1 {
		t.Fatalf("read %d citations", len(got))
	}
	ok, why := got[0].Wellformed()
	if ok {
		t.Fatal("a nghị định issued by the Prime Minister passed, and only the Government issues one")
	}
	if !strings.Contains(why, "NĐ-CP") && !strings.Contains(why, "QĐ-TTg") {
		t.Errorf("the reason names neither code: %q", why)
	}
}

func TestTheRegisterRefusesAnIdentifierItCouldNotCatchLater(t *testing.T) {
	r := NewRegister()
	if r.Add("nghị định", "13/2023/QĐ-TTg", 0) {
		t.Error("the register took a nghị định with the wrong body code, and a register holding one cannot catch one")
	}
	if r.Add("nghị định", "không phải số", 0) {
		t.Error("the register took a string that is not an identifier")
	}
	if r.Add("công văn", "1234/2023/CV-BTP", 0) {
		t.Error("the register took an instrument type it cannot check")
	}
	if r.Size() != 0 {
		t.Errorf("the register holds %d entries after refusing all three", r.Size())
	}
}

func TestACitationShapedStringIsNotACitation(t *testing.T) {
	r := register(t)
	got := Citations("Nghị định số 99/2024/NĐ-CP quy định điều đó.")
	ok, why := r.Check(got[0])
	if ok {
		t.Fatal("an instrument nobody issued passed the register")
	}
	if !strings.Contains(why, "99/2024/NĐ-CP") {
		t.Errorf("the reason does not say which one: %q", why)
	}
}

func TestAnArticlePastTheEndOfARealInstrumentIsCaught(t *testing.T) {
	r := register(t)
	got := Citations("Điều 200 Nghị định số 13/2023/NĐ-CP.")
	ok, why := r.Check(got[0])
	if ok {
		t.Fatal("article 200 of a 44 article instrument passed")
	}
	if !strings.Contains(why, "44") {
		t.Errorf("the reason does not say how many articles it has: %q", why)
	}
	// The register records no article count for the quyết định, so the article
	// is not checked rather than guessed at.
	if ok, why := r.Check(Citations("Điều 40 Quyết định số 749/QĐ-TTg.")[0]); !ok {
		t.Errorf("an article was checked against a count the register does not hold: %s", why)
	}
}

func TestAnInstrumentCalledTheWrongThingIsCaught(t *testing.T) {
	r := register(t)
	got := Citations("Nghị quyết số 24/2018/QH14 quy định điều đó.")
	if len(got) != 1 {
		t.Fatalf("read %d citations", len(got))
	}
	if ok, _ := got[0].Wellformed(); !ok {
		t.Fatal("a nghị quyết numbered QH14 is well formed, so this test has to reach the register to fail")
	}
	ok, why := r.Check(got[0])
	if ok {
		t.Fatal("a luật cited as a nghị quyết passed")
	}
	if !strings.Contains(why, "luật") {
		t.Errorf("the reason does not say what it actually is: %q", why)
	}
}

// asked returns a verifier holding one question and what an answer to it has to
// rest on.
func asked(t *testing.T, must ...string) (*Quote, string) {
	t.Helper()
	prompt := "Doanh nghiệp phải làm gì khi dữ liệu cá nhân của khách hàng bị lộ?"
	if len(Citations(prompt)) != 0 {
		t.Fatal("the question already contains a citation, so handing it back would score above zero for the wrong reason")
	}
	v := NewQuote(register(t))
	if !v.Ask(prompt, must...) {
		t.Fatalf("the key refused %v", must)
	}
	return v, prompt
}

func TestAnAnswerThatCitesWhatItHadToCitesScoresOne(t *testing.T) {
	v, prompt := asked(t, "nghị định số 13/2023/NĐ-CP")
	got := v.Verify(prompt, "Doanh nghiệp phải thông báo cho cơ quan có thẩm quyền trong 72 giờ theo Nghị định số 13/2023/NĐ-CP.")
	if !got.Checked {
		t.Fatalf("the answer came back ungraded: %s", got.Why)
	}
	if got.Reward != 1 {
		t.Errorf("the right answer scored %v: %s", got.Reward, got.Why)
	}
}

func TestTheThreeCheapAnswersAllScoreZeroHere(t *testing.T) {
	v, prompt := asked(t, "nghị định số 13/2023/NĐ-CP")

	for _, tc := range []struct {
		name   string
		answer string
	}{
		{"the empty answer", ""},
		{"the question handed back", prompt},
		{"an answer that says the right thing and rests on nothing", "Doanh nghiệp phải thông báo cho cơ quan có thẩm quyền trong vòng 72 giờ."},
		{"a citation with the shape and none of the substance", "Doanh nghiệp phải thông báo theo Nghị định số 99/2024/NĐ-CP."},
		{"a real instrument that has nothing to do with the question", "Doanh nghiệp phải thông báo theo Quyết định số 749/QĐ-TTg."},
	} {
		got := v.Verify(prompt, tc.answer)
		if !got.Checked {
			t.Errorf("%s came back ungraded: %s", tc.name, got.Why)
			continue
		}
		if got.Reward != 0 {
			t.Errorf("%s scored %v: %s", tc.name, got.Reward, got.Why)
		}
	}
}

func TestCitingEverythingInTheRegisterScoresLessThanCitingTheRightOne(t *testing.T) {
	v, prompt := asked(t, "nghị định số 13/2023/NĐ-CP")
	shotgun := v.Verify(prompt, "Xem Luật số 24/2018/QH14, Bộ luật số 91/2015/QH13, "+
		"Nghị định số 13/2023/NĐ-CP và Quyết định số 749/QĐ-TTg.")
	if !shotgun.Checked {
		t.Fatalf("the shotgun answer came back ungraded: %s", shotgun.Why)
	}
	if shotgun.Reward >= 1 {
		t.Fatalf("listing the whole register scored %v, so recall alone is collectable: %s", shotgun.Reward, shotgun.Why)
	}
	// One of four citations landed, so precision is 0.25 and recall is 1.
	if want := 2 * 0.25 * 1 / 1.25; math.Abs(shotgun.Reward-want) > 1e-9 {
		t.Errorf("the shotgun answer scored %v, want %v", shotgun.Reward, want)
	}
}

func TestCitingOneOfTheTwoInstrumentsScoresBetweenBoth(t *testing.T) {
	v, prompt := asked(t, "nghị định số 13/2023/NĐ-CP", "luật số 24/2018/QH14")

	half := v.Verify(prompt, "Theo Nghị định số 13/2023/NĐ-CP thì phải thông báo.")
	// One of one citations is right and one of two required is present, so
	// precision is 1 and recall is 0.5.
	if want := 2 * 1 * 0.5 / 1.5; math.Abs(half.Reward-want) > 1e-9 {
		t.Errorf("citing one of two scored %v, want %v: %s", half.Reward, want, half.Why)
	}

	both := v.Verify(prompt, "Theo Nghị định số 13/2023/NĐ-CP và Luật số 24/2018/QH14 thì phải thông báo.")
	if both.Reward != 1 {
		t.Errorf("citing both scored %v: %s", both.Reward, both.Why)
	}
	if half.Reward >= both.Reward {
		t.Error("citing half of what the answer had to rest on scored as well as citing all of it")
	}
}

func TestOneRightCitationAndOneInventedOneCostsPrecision(t *testing.T) {
	v, prompt := asked(t, "nghị định số 13/2023/NĐ-CP")
	got := v.Verify(prompt, "Theo Nghị định số 13/2023/NĐ-CP và Nghị định số 99/2024/NĐ-CP.")
	// One of two citations landed and the one required is present, so precision
	// is 0.5 and recall is 1.
	if want := 2 * 0.5 * 1 / 1.5; math.Abs(got.Reward-want) > 1e-9 {
		t.Errorf("scored %v, want %v: %s", got.Reward, want, got.Why)
	}
	if !strings.Contains(got.Why, "99/2024/NĐ-CP") {
		t.Errorf("the verdict does not say which citation was invented: %q", got.Why)
	}
}

func TestAKeyCannotAskForAnInstrumentTheRegisterDoesNotHold(t *testing.T) {
	v := NewQuote(register(t))
	if v.Ask("Câu hỏi", "nghị định số 99/2024/NĐ-CP") {
		t.Error("the key took a requirement nothing can satisfy, and an unwinnable item is a group that teaches nothing")
	}
	if v.Ask("Câu hỏi", "không phải trích dẫn") {
		t.Error("the key took a requirement that is not a citation")
	}
	if v.Ask("Câu hỏi") {
		t.Error("the key took a question with no requirement on it")
	}
	if v.Ask("   ", "nghị định số 13/2023/NĐ-CP") {
		t.Error("the key took an empty question")
	}
	if v.Items() != 0 {
		t.Errorf("the key holds %d prompts after refusing all four", v.Items())
	}
}

func TestAPromptTheKeyDoesNotHoldIsNotGradedHere(t *testing.T) {
	v, _ := asked(t, "nghị định số 13/2023/NĐ-CP")
	got := v.Verify("một câu hỏi khác", "Theo Nghị định số 13/2023/NĐ-CP.")
	if got.Checked {
		t.Fatal("an answer to a question with no key came back graded")
	}
	if got.Specialist != "trich" {
		t.Errorf("the verdict is attributed to %q", got.Specialist)
	}
}
