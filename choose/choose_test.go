package choose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reading(base string, quality, fertility float64) Reading {
	return Reading{Base: base, Quality: quality, Suite: "mmlu-pro multilingual", Fertility: fertility, Exposure: 0.02, Box: "gamingpc"}
}

// whole is every base on the roster measured on everything, which is the only
// state in which the table is entitled to name a choice.
func whole() []Reading {
	return []Reading{
		reading("gemma-3-27b-it", 62.0, 1.32),
		reading("qwen3-30b-a3b", 61.0, 1.51),
		reading("llama-3.3-70b-instruct", 58.0, 1.75),
		reading("mistral-small-3", 55.0, 1.60),
		reading("sailor2-8b", 44.0, 1.55),
	}
}

// The order is the content. A table that scores six things and adds them up is a
// table where the criterion that cannot be traded gets traded.
func TestTheCriteriaAreRankedAndTwoOfThemAreNotScores(t *testing.T) {
	got := Criteria()
	if len(got) != 6 {
		t.Fatalf("%d criteria, want the six that were written down", len(got))
	}
	for i, c := range got {
		if c.Rank != i+1 {
			t.Errorf("criterion %q is ranked %d in position %d", c.Name, c.Rank, i+1)
		}
	}
	if !got[0].Gate || got[0].Name != "license" {
		t.Errorf("criterion 1 is %q and gate=%v, want the license and a gate", got[0].Name, got[0].Gate)
	}
	if !got[2].Tie {
		t.Error("fertility is enough to break a tie and not enough to override criterion 2, and it is not marked as a tiebreak")
	}
	for _, c := range got[1:5] {
		if !c.Measured {
			t.Errorf("criterion %d, %s, is something somebody has to measure", c.Rank, c.Name)
		}
	}
}

// Criterion 1 removes rather than scores, so a base it fires on has to be
// unrankable no matter how well it does on everything below.
func TestALicenseThatForbidsDerivativesEndsTheCandidacy(t *testing.T) {
	best := Row{
		Base:    Base{Name: "closed-weights-70b"},
		Reading: reading("closed-weights-70b", 99.0, 1.10),
		Out:     "the license does not permit derivative weights",
	}
	if best.Scored() {
		t.Fatal("a base removed by criterion 1 was still scorable")
	}
	table := Table{Rows: append(Score(whole()).Rows, best)}
	for _, r := range table.Ranked() {
		if r.Base.Name == "closed-weights-70b" {
			t.Fatal("a base with the best quality and the best fertility on the table was ranked despite its license")
		}
	}

	// And nothing on the roster as it stands is out, which is worth asserting
	// because the day one of them is, this is where it shows up.
	for _, r := range Score(whole()).Rows {
		if r.Out != "" {
			t.Errorf("%s is on the roster and disqualified by it: %s", r.Base.Name, r.Out)
		}
	}
	if _, ok := FindBase("nothing"); ok {
		t.Error("a base nobody has heard of was found on the roster")
	}
}

// This is the sentence in the spec that is easiest to write down and hardest to
// implement: fertility breaks a tie and does not overturn the criterion above
// it.
func TestFertilityBreaksATieAndDoesNotOverturnQuality(t *testing.T) {
	// One point apart on quality, which is inside the band, so the better
	// fertility takes it.
	tied := Score([]Reading{
		reading("gemma-3-27b-it", 61.0, 1.60),
		reading("qwen3-30b-a3b", 62.0, 1.30),
	})
	r := tied.Ranked()
	if len(r) != 2 {
		t.Fatalf("%d rows came back scorable", len(r))
	}
	if r[0].Base.Name != "qwen3-30b-a3b" {
		t.Errorf("inside the band the better fertility did not win: %s leads", r[0].Base.Name)
	}
	if !tied.Tied() {
		t.Error("a one point gap did not read as a tie on criterion 2")
	}

	// Six points apart is not a tie, and a third better fertility does not move
	// it.
	clear := Score([]Reading{
		reading("gemma-3-27b-it", 62.0, 1.99),
		reading("qwen3-30b-a3b", 56.0, 1.50),
	})
	if got := clear.Ranked()[0].Base.Name; got != "gemma-3-27b-it" {
		t.Errorf("fertility overturned a six point gap on criterion 2: %s leads", got)
	}
	if clear.Tied() {
		t.Error("a six point gap read as a tie")
	}
	if !strings.Contains(tied.Verdict(), "criterion 3 decides") {
		t.Errorf("the verdict does not say which criterion decided: %s", tied.Verdict())
	}
	if strings.Contains(clear.Verdict(), "criterion 3 decides") {
		t.Errorf("a decision on criterion 2 was reported as one on criterion 3: %s", clear.Verdict())
	}
}

// A base nobody has measured is not a base that scored zero, and the difference
// is whether the table is a decision or a progress report.
func TestAnUnmeasuredCriterionIsAHoleRatherThanAZero(t *testing.T) {
	partial := Score([]Reading{reading("gemma-3-27b-it", 62.0, 1.32)})
	if partial.Decided() {
		t.Fatal("one base out of five read as a decision")
	}
	if len(partial.Missing()) != len(Bases())-1 {
		t.Errorf("%d bases reported as unmeasured: %v", len(partial.Missing()), partial.Missing())
	}
	if _, ok := partial.Choose(); ok {
		t.Error("a table with four holes in it chose a base anyway")
	}
	if !strings.Contains(partial.Verdict(), "a leader rather than a choice") {
		t.Errorf("the verdict reads as settled: %s", partial.Verdict())
	}

	// And the reason is named per base rather than counted.
	if !strings.Contains(strings.Join(partial.Missing(), " "), "criterion 4, Vietnamese exposure, has not been probed on it") {
		t.Errorf("the holes do not say which criterion is missing: %v", partial.Missing())
	}
}

func TestAWholeRosterMeasuredIsADecision(t *testing.T) {
	table := Score(whole())
	if !table.Decided() {
		t.Fatalf("the whole roster measured did not read as decided: %v %v", table.Faults, table.Missing())
	}
	best, ok := table.Choose()
	if !ok {
		t.Fatal("a decided table declined to choose")
	}
	if best.Name != "gemma-3-27b-it" {
		t.Errorf("the table chose %s", best.Name)
	}
	if !strings.Contains(table.Verdict(), "so this is the choice") {
		t.Errorf("the verdict hedges on a complete table: %s", table.Verdict())
	}
}

// Two scores from two suites are not a comparison, and criterion 2 is the
// criterion that decides, so this is the fault that would quietly pick the
// wrong base.
func TestQualityFromTwoSuitesIsNotARanking(t *testing.T) {
	mixed := whole()
	mixed[1].Suite = "vmlu"
	table := Score(mixed)
	if table.Decided() {
		t.Fatal("a ranking across two suites read as a decision")
	}
	if len(table.Suites) != 2 {
		t.Fatalf("the table does not carry both suites: %v", table.Suites)
	}
	if !strings.Contains(strings.Join(table.Faults, " "), "a ranking across two suites is a ranking of the suites") {
		t.Errorf("the fault is not named: %v", table.Faults)
	}
}

func TestOneBaseWithTwoQualityFiguresIsAFault(t *testing.T) {
	twice := append(whole(), Reading{Base: "gemma-3-27b-it", Quality: 51.0, Suite: "mmlu-pro multilingual", Fertility: 1.32, Exposure: 0.02, Box: "server3"})
	table := Score(twice)
	if table.Decided() {
		t.Fatal("a base with two quality figures read as decided")
	}
	if !strings.Contains(strings.Join(table.Faults, " "), "a criterion that decides cannot have two values") {
		t.Errorf("the fault is not named: %v", table.Faults)
	}
}

func TestAReadingOfSomethingNobodyIsConsideringIsNotScored(t *testing.T) {
	table := Score(append(whole(), reading("gpt-oss-120b", 71.0, 1.20)))
	for _, r := range table.Ranked() {
		if r.Base.Name == "gpt-oss-120b" {
			t.Fatal("a model that is not on the roster was ranked")
		}
	}
	if !strings.Contains(strings.Join(table.Faults, " "), "is not on the roster") {
		t.Errorf("the extra reading was dropped without a word: %v", table.Faults)
	}
}

func TestAReadingWithoutItsEvidenceIsRefused(t *testing.T) {
	var empty Reading
	if empty.Ok() {
		t.Fatal("a reading with nothing in it read as usable")
	}
	if len(empty.Blocking()) < 4 {
		t.Errorf("only %d reasons came back: %v", len(empty.Blocking()), empty.Blocking())
	}
	quoted := reading("gemma-3-27b-it", 62.0, 1.32)
	quoted.Suite = ""
	if quoted.Ok() {
		t.Error("a quality figure with no suite behind it read as usable")
	}
}

// Every base has to say which vocabulary it comes with, because criterion 3 is a
// fact about the tokenizer rather than about the weights, and a base whose
// tokenizer nobody has measured is a base criterion 3 cannot be applied to.
func TestEveryBaseNamesTheTokenizerCriterionThreeIsAbout(t *testing.T) {
	for _, b := range Bases() {
		if b.Tokenizer == "" || b.Vocab == 0 {
			t.Errorf("%s does not name the vocabulary it comes with", b.Name)
		}
		if b.Context == 0 {
			t.Errorf("%s does not say what context length it was trained to, which is criterion 5", b.Name)
		}
		if b.License == "" || b.Why == "" {
			t.Errorf("%s is on the roster without a license or a reason", b.Name)
		}
	}

	// The families the spec names, all present.
	var families []string
	for _, b := range Bases() {
		if !contains(families, b.Family) {
			families = append(families, b.Family)
		}
	}
	if len(families) != 5 {
		t.Errorf("%d families on the roster: %v", len(families), families)
	}
}

func TestReadingsAreReadFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bases.jsonl")
	body := `{"base":"gemma-3-27b-it","quality":62,"suite":"mmlu-pro multilingual","fertility":1.32,"exposure":0.02,"box":"gamingpc"}
{"base":"qwen3-30b-a3b","quality":61,"suite":"mmlu-pro multilingual","fertility":1.51,"exposure":0.03,"box":"gamingpc"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadReadings(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Base != "qwen3-30b-a3b" {
		t.Fatalf("read back %+v", got)
	}

	bad := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"base":"gemma-3-27b-it","vmlu":51}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReadings(bad); err == nil {
		t.Error("a log with a field this reader does not know about was read anyway")
	}
}

func TestAnEmptyTableIsNotAChoice(t *testing.T) {
	table := Score(nil)
	if table.Decided() {
		t.Error("an empty table read as decided")
	}
	if _, ok := table.Choose(); ok {
		t.Error("an empty table chose a base")
	}
	if table.Tied() {
		t.Error("an empty table reported a tie")
	}
	if !strings.Contains(table.Verdict(), "nothing on the roster has been measured") {
		t.Errorf("the verdict of an empty table is %q", table.Verdict())
	}
}
