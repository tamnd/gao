package dem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reading is one measurement of one tokenizer on one box, on the text every
// other reading in the test was taken on.
func reading(name, box string, tokens int64) Fertility {
	c, _ := FindCandidate(name)
	return Fertility{
		Tokenizer: name, Vocab: c.Model.Vocab, Corpus: "1f4a9c02", Box: box,
		Chars: 4_000_000, Syllables: 1_000_000, Tokens: tokens,
	}
}

// on is the same reading taken on several boxes, which is what the fleet item
// asks for and is the only thing that makes a fertility number believable.
func on(name string, tokens int64, boxes ...string) []Fertility {
	out := make([]Fertility, 0, len(boxes))
	for _, box := range boxes {
		out = append(out, reading(name, box, tokens))
	}
	return out
}

func TestTheRosterSaysWhichCandidatesCanBeMeasuredAtAll(t *testing.T) {
	got := Candidates()
	if len(got) < 4 {
		t.Fatalf("%d candidates on the roster", len(got))
	}
	var pinned int
	for _, c := range got {
		if c.Model.Name == "" || c.Why == "" {
			t.Errorf("a candidate with no name or no reason to be there: %+v", c)
		}
		if c.Path != Continued && c.Path != Scratch {
			t.Errorf("%s belongs to neither training path", c.Model.Name)
		}
		if c.Pinned() {
			pinned++
		}
	}
	if pinned == 0 {
		t.Error("nothing on the roster is pinned, so nothing on it can be measured")
	}
	if pinned == len(got) {
		t.Error("every candidate reads as pinned, which is not true yet and the roster should say so")
	}
	if !mustFind(t, "gemma-3").Pinned() {
		t.Error("the incumbent is not pinned")
	}
	if mustFind(t, "gao-192k").Pinned() {
		t.Error("a vocabulary nobody has trained yet has a digest on it")
	}
}

func mustFind(t *testing.T, name string) Candidate {
	t.Helper()
	c, ok := FindCandidate(name)
	if !ok {
		t.Fatalf("%s is not on the roster", name)
	}
	return c
}

// The whole reason this is a gate rather than a statistic. The milestone's own
// example: 1.99 tokens per syllable against 1.50 is a third more of everything,
// forever.
func TestFertilityIsAMultiplierOnEverythingDownstream(t *testing.T) {
	cheap := Fertility{Syllables: 100, Tokens: 150}
	dear := Fertility{Syllables: 100, Tokens: 199}
	if got := dear.Cost(cheap); got < 1.32 || got > 1.34 {
		t.Errorf("1.99 against 1.50 tokens per syllable came out as %.3f", got)
	}
	if got := cheap.Cost(dear); got > 1 {
		t.Errorf("the cheaper tokenizer cost more: %.3f", got)
	}
	if got := cheap.Cost(Fertility{}); got != 0 {
		t.Errorf("a comparison against nothing came back as %.3f", got)
	}
}

func TestBothFertilityFiguresAreReported(t *testing.T) {
	f := Fertility{Chars: 3_000_000, Syllables: 1_000_000, Tokens: 1_000_000}
	if got := f.PerToken(); got != 3 {
		t.Errorf("%.2f characters per token", got)
	}
	if got := f.PerSyllable(); got != 1 {
		t.Errorf("%.2f tokens per syllable", got)
	}
	empty := Fertility{}
	if empty.PerToken() != 0 || empty.PerSyllable() != 0 {
		t.Error("a ratio came back over no text")
	}
}

// A fertility figure that has lost the text it was taken on is not comparable
// to anything, which is most of what makes fertility numbers in the literature
// hard to use.
func TestAReadingWithoutItsTextOrItsBoxIsRefused(t *testing.T) {
	f := reading("gemma-3", "server1", 1_300_000)
	f.Corpus = ""
	if f.Ok() {
		t.Fatal("a reading with no text behind it was accepted")
	}
	if !strings.Contains(f.Blocking()[0], "not a comparison") {
		t.Errorf("the reason is not given: %v", f.Blocking())
	}

	f = reading("gemma-3", "", 1_300_000)
	if f.Ok() {
		t.Fatal("a reading with no box on it was accepted")
	}
	if !strings.Contains(f.Blocking()[0], "reproducibility check") {
		t.Errorf("the reason is not given: %v", f.Blocking())
	}
}

// The item says measure every candidate, so a slate that quietly leaves out the
// ones nobody got to would be reporting the work as done.
func TestACandidateNobodyMeasuredIsNamed(t *testing.T) {
	s := Fold(on("gemma-3", 1_300_000, "server1", "server3"))
	if s.Complete() {
		t.Fatal("one candidate out of five read as the whole roster")
	}
	if len(s.Missing) != len(Candidates())-1 {
		t.Errorf("%d candidates reported as unmeasured: %v", len(s.Missing), s.Missing)
	}
	if !strings.Contains(s.Verdict(), "not been measured") {
		t.Errorf("the verdict does not say the comparison is incomplete: %s", s.Verdict())
	}

	// And one reading has nothing to be cheaper than, so there is no spread to
	// quote and quoting one anyway would be a number with no meaning in it.
	if strings.Contains(s.Verdict(), "-100%") {
		t.Errorf("a single measurement was priced against a spread that does not exist: %s", s.Verdict())
	}
}

// This is the fleet item, and it is the cheapest reproducibility check in the
// project: same tokenizer, same text, different box, different answer.
func TestTheSameTextCountedDifferentlyOnTwoBoxesIsAFault(t *testing.T) {
	got := on("gemma-3", 1_300_000, "server1", "server3")
	got[1].Tokens = 1_300_412

	s := Fold(got)
	if s.Reproduced() {
		t.Fatal("two boxes that disagreed were reported as reproducing")
	}
	if !strings.Contains(s.Faults[0], "not the pinned one") {
		t.Errorf("the fault does not say what it could be: %s", s.Faults[0])
	}
}

// Two boxes measuring different text is a different mistake and a quieter one,
// because the numbers will differ by a plausible amount rather than by an
// obvious one.
func TestTwoBoxesOnDifferentTextIsNotAComparison(t *testing.T) {
	got := on("gemma-3", 1_300_000, "server1", "gamingpc")
	got[1].Corpus = "9c021f4a"
	got[1].Tokens = 1_290_000

	s := Fold(got)
	if s.Reproduced() {
		t.Fatal("readings over different text were reported as reproducing")
	}
	if !strings.Contains(s.Faults[0], "not a comparison") {
		t.Errorf("the fault does not say what happened: %s", s.Faults[0])
	}
}

// Measuring twice on one box is a repeat and reads like a reproduction, which is
// exactly why it is called out.
func TestMeasuringTwiceOnOneBoxIsNotAReproduction(t *testing.T) {
	s := Fold(on("gemma-3", 1_300_000, "server1", "server1"))
	if s.Reproduced() {
		t.Fatal("the same box twice was reported as reproducing")
	}
	if !strings.Contains(s.Faults[0], "repeat rather than a reproduction") {
		t.Errorf("the fault does not name it: %s", s.Faults[0])
	}
}

// A file with a different vocabulary is not the file that was pinned, and it is
// the one difference that shows up in the reading itself.
func TestAVocabularyThatIsNotThePinnedOneIsAFault(t *testing.T) {
	got := on("gemma-3", 1_300_000, "server1", "server3")
	for i := range got {
		got[i].Vocab = 256000
	}
	s := Fold(got)
	if s.Reproduced() {
		t.Fatal("a tokenizer that is not the pinned one was accepted")
	}
	if !strings.Contains(strings.Join(s.Faults, " "), "not the file that was pinned") {
		t.Errorf("the fault does not say what is wrong: %v", s.Faults)
	}
}

func TestSomethingNobodyIsConsideringIsNotSilentlyRanked(t *testing.T) {
	got := on("mbart-50", 1_100_000, "server1", "server3")
	for i := range got {
		got[i].Vocab = 0
	}
	s := Fold(got)
	if len(s.Measured) != 0 {
		t.Error("a tokenizer nobody is considering was ranked against the roster")
	}
	if !strings.Contains(strings.Join(s.Faults, " "), "not on the roster") {
		t.Errorf("the fault does not say what happened: %v", s.Faults)
	}
}

func TestTheSlateRanksByTheFigureTheBudgetIsAFunctionOf(t *testing.T) {
	var got []Fertility
	got = append(got, on("gemma-3", 1_300_000, "server1", "server3")...)
	got = append(got, on("llama-3.3", 1_750_000, "server1", "server3")...)
	got = append(got, on("qwen3", 1_500_000, "server1", "server3")...)
	got = append(got, on("gao-192k", 1_350_000, "server1", "server3")...)
	got = append(got, on("gemma-3-plus-32k", 1_250_000, "server1", "server3")...)

	s := Fold(got)
	if !s.Complete() {
		t.Fatalf("the whole roster was measured and did not read as complete: %v %v", s.Missing, s.Faults)
	}
	if !s.Reproduced() {
		t.Fatalf("every candidate was measured on two boxes and did not read as reproduced: %v", s.Faults)
	}

	r := s.Ranked()
	if len(r) != 5 {
		t.Fatalf("%d candidates ranked", len(r))
	}
	if r[0].Candidate.Model.Name != "gemma-3-plus-32k" {
		t.Errorf("the cheapest candidate came back as %s", r[0].Candidate.Model.Name)
	}
	if r[len(r)-1].Candidate.Model.Name != "llama-3.3" {
		t.Errorf("the most expensive candidate came back as %s", r[len(r)-1].Candidate.Model.Name)
	}
	if got := s.Spread(); got < 1.39 || got > 1.41 {
		t.Errorf("the spread across the roster is %.3f, want the 1.75 against 1.25 ratio", got)
	}
	if !strings.Contains(s.Verdict(), "40% more for the same Vietnamese") {
		t.Errorf("the verdict does not price the choice: %s", s.Verdict())
	}
}

// A measurement against a prediction somebody wrote down first is a different
// thing from a measurement, and the register is the reason to keep the two
// apart.
func TestAPredictionIsCheckedAgainstTheMeasurement(t *testing.T) {
	gemma := mustFind(t, "gemma-3")
	if gemma.Predicts.ID != "P07-5" {
		t.Fatalf("the prediction standing against the incumbent is %q", gemma.Predicts.ID)
	}
	held, applies := gemma.Predicts.Holds(Fertility{Chars: 3_020_000, Syllables: 1e6, Tokens: 1e6})
	if !applies || !held {
		t.Errorf("3.02 characters per token did not fall inside 2.85 to 3.15")
	}
	missed, _ := gemma.Predicts.Holds(Fertility{Chars: 2_400_000, Syllables: 1e6, Tokens: 1e6})
	if missed {
		t.Error("2.40 characters per token fell inside a bracket that stops at 2.85")
	}

	// P07-1 is about the other figure entirely, which is why the unit is on the
	// prediction rather than assumed.
	gao := mustFind(t, "gao-192k")
	if gao.Predicts.Unit != PerSyllable {
		t.Errorf("P07-1 is quoted in %q", gao.Predicts.Unit)
	}
	if _, applies := (Predicted{}).Holds(Fertility{Chars: 1, Syllables: 1, Tokens: 1}); applies {
		t.Error("a candidate with no prediction against it was judged anyway")
	}
	if _, applies := gao.Predicts.Holds(Fertility{}); applies {
		t.Error("a prediction was checked against a reading with nothing in it")
	}
}

// Most of these predictions are floors or ceilings rather than brackets, and a
// missing end has to read as no bound rather than as a bound at zero. P07-2 is
// the one that would go wrong quietly: it predicts fertility improves, so a
// reading better than the floor has to hold, and getting the direction backwards
// would report the incumbent as having satisfied it.
func TestAOneSidedPredictionIsNotABracket(t *testing.T) {
	expanded := mustFind(t, "gemma-3-plus-32k")
	if expanded.Predicts.ID != "P07-2" || expanded.Predicts.Unit != PerToken {
		t.Fatalf("P07-2 stands against the expanded tokenizer, quoted in characters per token, got %q in %q", expanded.Predicts.ID, expanded.Predicts.Unit)
	}
	if held, applies := expanded.Predicts.Holds(reading("gemma-3-plus-32k", "server1", 1e6)); !applies || !held {
		t.Error("4.00 characters per token is a 32% improvement on the incumbent and did not satisfy a prediction of at least 15%")
	}

	// The incumbent's own figure, which is the number the 15% is measured from
	// and so cannot be an improvement on itself.
	if held, _ := expanded.Predicts.Holds(Fertility{Chars: 3_020_000, Syllables: 1e6, Tokens: 1e6}); held {
		t.Error("the unexpanded fertility satisfied a prediction that the expansion improves on it")
	}

	// And the other direction, on the figure where lower is better.
	gao := mustFind(t, "gao-192k")
	if held, _ := gao.Predicts.Holds(Fertility{Chars: 4e6, Syllables: 1e6, Tokens: 1_300_000}); !held {
		t.Error("1.30 tokens per syllable did not satisfy a ceiling of 1.35")
	}
	if held, _ := gao.Predicts.Holds(Fertility{Chars: 4e6, Syllables: 1e6, Tokens: 1_500_000}); held {
		t.Error("1.50 tokens per syllable satisfied a ceiling of 1.35")
	}
}

// The anchor language half of P07-1, which is the constraint that stops the
// answer being a vocabulary that is excellent at Vietnamese and unusable.
func TestStayingWithinToleranceIsItsOwnQuestion(t *testing.T) {
	if !Within(2.80, 3.02, 0.08) {
		t.Error("2.80 against 3.02 is within 8% and did not read as it")
	}
	if Within(2.60, 3.02, 0.08) {
		t.Error("2.60 against 3.02 is 14% off and read as within 8%")
	}
	if Within(0, 3.02, 0.08) {
		t.Error("a missing measurement read as within tolerance")
	}
}

func TestAnEmptySlateIsNotAResult(t *testing.T) {
	s := Fold(nil)
	if s.Complete() || s.Reproduced() {
		t.Error("an empty slate reported itself as done")
	}
	if s.Spread() != 0 || len(s.Ranked()) != 0 {
		t.Error("an empty slate ranked something")
	}
	if !strings.Contains(s.Verdict(), "nothing on the roster has been measured") {
		t.Errorf("the verdict is not honest about it: %s", s.Verdict())
	}
}

func TestFertilityIsReadFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fertility.jsonl")
	var b strings.Builder
	for _, f := range on("gemma-3", 1_300_000, "server1", "server3") {
		line, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadFertility(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d readings, want 2", len(got))
	}
	if len(Fold(got).Faults) != 0 {
		t.Error("a clean pair did not survive the round trip")
	}

	if err := os.WriteFile(path, []byte(`{"tokenizer":"gemma-3","locale":"vi_VN"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFertility(path); err == nil {
		t.Error("a field this reader does not know about was read anyway")
	}
	if _, err := ReadFertility(filepath.Join(t.TempDir(), "nothing.jsonl")); err == nil {
		t.Error("a log that is not there was read")
	}
}
