package tieng_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tamnd/gao/tieng"
)

// pool is marked Vietnamese, most of it function words, and it exists so that a
// test can write a lot of text without writing a collocation into it. The
// carrier below walks the pool at a stride, so the pairs it forms are spread
// evenly over the whole of it and none of them turns up often enough to take a
// slot. A carrier that was one sentence repeated would put itself at the top of
// every table in this file.
var pool = strings.Fields(`của là có và với một này đã để các
	thì cũng rằng đến từ sẽ vì tại được những
	không người phải nếu hoặc nhưng bảo cáo điều đó
	trước sau giữa bằng hơn nữa rất khá mới cũ
	trên dưới ngoài cùng riêng chung nhiều ít lớn nhỏ
	đầu cuối giờ ngày tháng năm tuần buổi sáng chiều`)

// line is six syllables off the pool, taken at a stride so that consecutive
// calls do not share a pair.
func line(i int) string {
	out := make([]string, 0, 6)
	for k := range 6 {
		out = append(out, pool[(i*7+k)%len(pool)])
	}
	return strings.Join(out, " ")
}

// docs builds a sample out of one body of text repeated into n named documents,
// which is what most of these tests want: a table whose counts are arithmetic
// rather than a property of a corpus somebody has to be handed.
func docs(n int, body string) []tieng.Doc {
	out := make([]tieng.Doc, 0, n)
	for i := range n {
		out = append(out, tieng.Doc{Name: fmt.Sprintf("bai-%03d.txt", i), Text: body})
	}
	return out
}

// repeat writes a phrase into a paragraph n times, each time behind a carrier
// the identifier will accept, so that the count of the phrase is exactly n and
// the count of everything else is well under the floor.
func repeat(n int, phrase string) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%s. %s.\n", line(i), phrase)
	}
	return b.String()
}

func read(t *testing.T, d []tieng.Doc) tieng.Reading {
	t.Helper()
	r := tieng.Read("a sample", tieng.Slots, d)
	if why := r.Blocking(); len(why) > 0 {
		t.Fatalf("the reading was blocked: %s", strings.Join(why, "; "))
	}
	return r
}

func says(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, want) {
			return
		}
	}
	t.Errorf("nothing said %q, and what was said is:\n  %s", want, strings.Join(lines, "\n  "))
}

func silent(t *testing.T, lines []string, about string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, about) {
			t.Errorf("something said %q, which this reading has no reason to: %s", about, l)
		}
	}
}

// The classification is the whole of the governed share, so it is worth one test
// that names every kind and one document that holds all of them.
func TestTheRuleGovernsSyllablesAndHandsEverythingElseToTheEscapeHatch(t *testing.T) {
	body := line(0) + " " + line(3) + " trong 2026 the quarterly report v2 cost 1.500.000 đồng.\n"

	r := read(t, docs(1, body))

	if r.Marked == 0 {
		t.Fatal("a marked Vietnamese sentence produced no marked syllables")
	}
	if r.Number != 2 {
		t.Errorf("%d units were counted as numbers, want 2026 and 1.500.000, which is 2", r.Number)
	}
	if r.Mixed != 1 {
		t.Errorf("%d units were counted as letters and digits, want v2, which is 1", r.Mixed)
	}
	if r.Foreign == 0 {
		t.Error("quarterly and report were counted as something the rule governs")
	}
	if r.Syllables != r.Marked+r.Bare {
		t.Errorf("the governed count %d is not the two registers added up, %d and %d", r.Syllables, r.Marked, r.Bare)
	}
	if r.Governed <= 0 || r.Governed >= 1 {
		t.Errorf("the governed share came back at %.3f over a document that holds both kinds", r.Governed)
	}
}

func TestARunThatRepeatsOftenEnoughTakesASlot(t *testing.T) {
	r := read(t, docs(1, repeat(tieng.MinCount+10, "việt nam")))

	if len(r.Runs) == 0 {
		t.Fatal("a run that turns up sixty times took no slot")
	}
	if got := r.Runs[0].Run; got != "việt nam" {
		t.Errorf("the slot that buys most went to %q", got)
	}
	if got := r.Runs[0].Count; got != tieng.MinCount+10 {
		t.Errorf("the run was counted %d times, want %d", got, tieng.MinCount+10)
	}
	if r.Cost <= 0 {
		t.Errorf("the rule was priced at %.4f over text that holds a merge it forbids", r.Cost)
	}
}

func TestARunThatDoesNotRepeatOftenEnoughTakesNothing(t *testing.T) {
	r := read(t, docs(1, repeat(tieng.MinCount-1, "việt nam")))

	for _, run := range r.Runs {
		if run.Run == "việt nam" {
			t.Fatalf("a run seen %d times took a slot, and the floor is %d", run.Count, tieng.MinCount)
		}
	}
	says(t, r.Faults(), "not the same reading as it giving up nothing")
}

// The failure this exists to prevent. Ranking overlapping runs independently
// sells the same three thousand appearances to four different slots and prints
// one phrase four times in a table of ten.
func TestALongerRunPaysForItselfOutOfTheShorterOnesInsideIt(t *testing.T) {
	r := read(t, docs(1, repeat(tieng.MinCount*4, "thành phố hồ chí")))

	if len(r.Runs) == 0 {
		t.Fatal("a run that turns up two hundred times took no slot")
	}
	if got := r.Runs[0].Run; got != "thành phố hồ chí" {
		t.Fatalf("the slot that buys most went to %q rather than to the whole run", got)
	}
	for _, run := range r.Runs[1:] {
		if strings.HasPrefix("thành phố hồ chí", run.Run) || strings.HasSuffix("thành phố hồ chí", run.Run) {
			t.Errorf("%q took a slot as well, and every one of its %d appearances is already inside the run above it", run.Run, run.Count)
		}
	}
}

// A tokenizer that pre-splits on punctuation cannot merge across a comma, and
// neither can this, or the table fills up with the join between one sentence and
// the next.
func TestAMergeDoesNotCrossPunctuation(t *testing.T) {
	r := read(t, docs(1, repeat(tieng.MinCount*2, "việt nam, việt nam")))

	for _, run := range r.Runs {
		if strings.HasPrefix(run.Run, "nam việt") {
			t.Errorf("%q took a slot, and the two syllables have a comma between them", run.Run)
		}
	}
	says(t, []string{r.Runs[0].Run}, "việt nam")
}

// The saving cannot be added up out of the table, because the same syllable
// cannot be swallowed by two merges. It is counted by walking the text with the
// table in hand, which is what a tokenizer does.
func TestTheSavingIsWalkedRatherThanAddedUp(t *testing.T) {
	r := read(t, docs(1, repeat(tieng.MinCount*3, "thành phố hồ chí")))

	var saves int
	for _, run := range r.Runs {
		saves += run.Saves
	}
	walked := float64(r.Syllables) * (1 - r.Crossing)
	if float64(saves) < walked-0.5 {
		t.Errorf("the table adds up to %d tokens saved and the walk found %.0f, so the walk is counting a merge the table does not hold", saves, walked)
	}
	if r.Inside == 0 {
		t.Error("no syllable was swallowed by a merge, over text built to hold two hundred of them")
	}
	if r.InsideShare <= 0 || r.InsideShare >= 1 {
		t.Errorf("the covered share came back at %.3f", r.InsideShare)
	}
}

func TestADocumentTheIdentifierDoesNotCallVietnameseIsLeftOutAndNamed(t *testing.T) {
	sample := docs(3, repeat(tieng.MinCount, "việt nam"))
	sample = append(sample, tieng.Doc{
		Name: "english.txt",
		Text: "The quarterly report of the working group is attached to this message and should be read with the appendix.",
	})

	r := read(t, sample)

	if r.Refused != 1 {
		t.Fatalf("%d documents were refused, want the English one", r.Refused)
	}
	if r.Docs != 3 {
		t.Errorf("%d documents were read, want 3", r.Docs)
	}
	says(t, r.Faults(), "english.txt")
	says(t, r.Faults(), "not Vietnamese to the identifier")
}

func TestOneDocumentCarryingTheTableIsNamed(t *testing.T) {
	sample := docs(2, repeat(2, "việt nam"))
	sample = append(sample, tieng.Doc{Name: "dai.txt", Text: repeat(200, "việt nam")})

	r := read(t, sample)

	if r.Widest != "dai.txt" {
		t.Errorf("the widest document came back as %q", r.Widest)
	}
	says(t, r.Faults(), "dai.txt supplies")
	says(t, r.Faults(), "the runs that pay best are the ones that page repeats")
}

func TestASampleTooSmallToCountRunsOverSaysSo(t *testing.T) {
	r := read(t, docs(4, line(3)+".\n"))

	says(t, r.Faults(), "under the 100,000 syllables")
	says(t, r.Faults(), "under the 200 it takes")
}

// The rule is stated about Vietnamese syllables. A page that is a third numbers
// is a page the rule mostly does not govern, and the comparison it produces is
// two tokenizers agreeing about the part that was never in question.
func TestTextTheRuleMostlyDoesNotGovernIsNamedAsThat(t *testing.T) {
	var b strings.Builder
	for i := range 200 {
		fmt.Fprintf(&b, "%s %d %d %d %d %d %d.\n", line(i), i, i+1, i+2, i+3, i+4, i+5)
	}

	r := read(t, docs(1, b.String()))

	if r.Governed >= tieng.MinGoverned {
		t.Fatalf("the governed share came back at %.3f over text that is a quarter numbers", r.Governed)
	}
	says(t, r.Faults(), "falls through to whatever the tokenizer would have done anyway")
}

func TestAnArmGivenEverySlotItAskedForIsNamedAsACeiling(t *testing.T) {
	body := repeat(tieng.MinCount+5, "việt nam") + repeat(tieng.MinCount+5, "hà nội")

	r := tieng.Read("a sample", 1, docs(1, body))

	if len(r.Runs) != 1 {
		t.Fatalf("%d slots were spent, and the arm was given 1", len(r.Runs))
	}
	says(t, r.Faults(), "all 1 slot went to a run that still pays for it")

	wide := tieng.Read("a sample", tieng.Slots, docs(1, body))
	silent(t, wide.Faults(), "went to a run that still pays")
	if wide.Cost <= r.Cost {
		t.Errorf("the arm with more slots priced the rule at %.4f, no higher than the arm with one at %.4f", wide.Cost, r.Cost)
	}
}

func TestTheSameTextReadTwiceGivesTheSameTable(t *testing.T) {
	body := repeat(tieng.MinCount+3, "việt nam") + repeat(tieng.MinCount+7, "hà nội") + repeat(tieng.MinCount+1, "thành phố")

	first := read(t, docs(1, body))
	second := read(t, docs(1, body))

	if len(first.Runs) != len(second.Runs) {
		t.Fatalf("one text gave %d slots and then %d", len(first.Runs), len(second.Runs))
	}
	for i := range first.Runs {
		if first.Runs[i] != second.Runs[i] {
			t.Fatalf("row %d came back as %q and then %q", i, first.Runs[i].Run, second.Runs[i].Run)
		}
	}
	if first.Crossing != second.Crossing {
		t.Errorf("one text priced the rule at %.6f and then %.6f", first.Crossing, second.Crossing)
	}
}

func TestARefusalIsAboutTheReadingRatherThanAboutTheText(t *testing.T) {
	good := docs(1, repeat(tieng.MinCount, "việt nam"))

	for _, c := range []struct {
		name   string
		source string
		slots  int
		docs   []tieng.Doc
		want   string
	}{
		{"no source", "", tieng.Slots, good, "does not say what text it was taken over"},
		{"no slots", "a sample", 0, good, "allowed no vocabulary slots"},
		{"no documents", "a sample", tieng.Slots, nil, "no text was read"},
		{"a document with no name", "a sample", tieng.Slots,
			[]tieng.Doc{{Text: line(0)}}, "arrived with no name"},
		{"two documents with no name", "a sample", tieng.Slots,
			[]tieng.Doc{{Text: line(0)}, {Text: line(1)}}, "2 documents arrived with no name"},
		{"the same document twice", "a sample", tieng.Slots,
			append(append([]tieng.Doc{}, good...), good...), "was read twice"},
		{"a document with no text", "a sample", tieng.Slots,
			[]tieng.Doc{{Name: "trong.txt"}}, "trong.txt holds no text"},
		{"nothing the identifier calls Vietnamese", "a sample", tieng.Slots,
			[]tieng.Doc{{Name: "en.txt", Text: "The quarterly report is attached to this message."}},
			"called none of these documents Vietnamese"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := tieng.Read(c.source, c.slots, c.docs)
			says(t, r.Blocking(), c.want)
			if r.Holds() {
				t.Error("a reading nobody can use reported that it holds")
			}
			if len(r.Faults()) > 0 {
				t.Errorf("a blocked reading also printed faults: %s", strings.Join(r.Faults(), "; "))
			}
			if !strings.Contains(r.Verdict(), c.want) {
				t.Errorf("the verdict does not lead with the refusal: %s", r.Verdict())
			}
		})
	}
}

func TestTheVerdictSaysWhatTheRuleCostsAndWhatItDidNotMeasure(t *testing.T) {
	r := read(t, docs(1, repeat(tieng.MinCount*2, "việt nam")))

	for _, want := range []string{
		"a syllable-atomic rule governs",
		"from 1.00 tokens per syllable to",
		"before a step is trained",
		`The slot that buys most is "việt nam"`,
		"What the rule buys is not in this reading",
		"P07-3",
	} {
		if !strings.Contains(r.Verdict(), want) {
			t.Errorf("the verdict does not say %q:\n%s", want, r.Verdict())
		}
	}
}

// The floor is reachable rather than theoretical, and the reason is that the
// inventory is small enough for every candidate vocabulary to hold all of it.
func TestTheInventoryIsOnTheReadingBecauseItIsWhatMakesTheFloorReachable(t *testing.T) {
	r := read(t, docs(1, repeat(tieng.MinCount, "việt nam")))

	if r.Inventory < 1000 {
		t.Fatalf("the syllable inventory came back at %d, which is not the inventory sang builds", r.Inventory)
	}
	if r.Inventory*6 > 192_000/2 {
		t.Errorf("the inventory is %d spellings, and six tones over that is not comfortably inside the 192k vocabulary this project is aiming at", r.Inventory)
	}
}
