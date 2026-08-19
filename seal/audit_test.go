package seal

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// results is what a complete, honest run of the small harness looks like.
func results() []Result {
	d := small().Digest()
	return []Result{
		{Harness: d, Arm: "a", Scores: map[string]float64{"vmlu": 0.52, "vi-diacritic": 0.031}},
		{Harness: d, Arm: "b", Scores: map[string]float64{"vmlu": 0.49, "vi-diacritic": 0.044}},
	}
}

func faultAbout(a Audit, s string) bool {
	for _, f := range a.Faults {
		if strings.Contains(f, s) {
			return true
		}
	}
	return false
}

func TestACompleteRunPasses(t *testing.T) {
	a := small().Audit(results())
	if !a.OK() {
		t.Fatalf("an honest run was faulted: %v", a.Faults)
	}
	if a.Reported != a.Promised {
		t.Errorf("%d numbers reported of %d promised", a.Reported, a.Promised)
	}
	if a.Promised != 4 {
		t.Errorf("two arms over two tasks promised %d numbers, want 4", a.Promised)
	}
	if a.Harness != small().Digest() {
		t.Error("the audit does not carry the harness digest")
	}
}

func TestABenchmarkThatArrivesWithTheNumbersArrivedAfterThem(t *testing.T) {
	r := results()
	r[0].Scores["vmlu-hard"] = 0.91

	a := small().Audit(r)
	if a.OK() {
		t.Fatal("a benchmark nobody closed the harness on was accepted")
	}
	if !faultAbout(a, "vmlu-hard") {
		t.Errorf("the added benchmark was not named: %v", a.Faults)
	}
}

func TestABenchmarkThatDisappearsIsCaughtJustAsLoudly(t *testing.T) {
	// This is the one that gets committed by accident and explained away as a
	// run that did not finish, so it has to fail exactly the way the other does.
	r := results()
	delete(r[0].Scores, "vi-diacritic")

	a := small().Audit(r)
	if a.OK() {
		t.Fatal("an arm that quietly dropped a benchmark passed")
	}
	if !faultAbout(a, "vi-diacritic") {
		t.Errorf("the missing benchmark was not named: %v", a.Faults)
	}
	if a.Reported != 3 {
		t.Errorf("%d numbers reported, want 3", a.Reported)
	}
}

func TestAnArmThatReportedNothingIsStillInTheTable(t *testing.T) {
	a := small().Audit(results()[:1])
	if a.OK() {
		t.Fatal("a comparison missing one of its arms passed")
	}
	if !faultAbout(a, "b") {
		t.Errorf("the missing arm was not named: %v", a.Faults)
	}
}

func TestNumbersScoredUnderAnotherHarnessAreNotComparable(t *testing.T) {
	other := small()
	other.Tasks[0].Shots = 0
	other.Tasks[0].Prompt = "{{item}}"

	r := results()
	r[1].Harness = other.Digest()

	a := small().Audit(r)
	if a.OK() {
		t.Fatal("numbers from a different harness were accepted into the comparison")
	}
	if !faultAbout(a, "not comparable") {
		t.Errorf("the fault does not say what is wrong: %v", a.Faults)
	}
}

func TestAResultWithNoDigestIsRejectedRatherThanAssumedFine(t *testing.T) {
	r := results()
	r[0].Harness = doc.Hash{}

	a := small().Audit(r)
	if a.OK() {
		t.Fatal("a result with nothing tying it to a measurement was accepted")
	}
	if !faultAbout(a, "no harness digest") {
		t.Errorf("the fault does not say what is missing: %v", a.Faults)
	}
}

func TestAnArmTheHarnessDoesNotNameIsNotPartOfTheComparison(t *testing.T) {
	r := append(results(), Result{
		Harness: small().Digest(),
		Arm:     "com-8B-cpt-gao-v2",
		Scores:  map[string]float64{"vmlu": 0.61, "vi-diacritic": 0.02},
	})

	a := small().Audit(r)
	if a.OK() {
		t.Fatal("an arm added after the harness was closed was accepted")
	}
	if !faultAbout(a, "com-8B-cpt-gao-v2") {
		t.Errorf("the extra arm was not named: %v", a.Faults)
	}
}

func TestAScoreOffTheScaleIsCaught(t *testing.T) {
	r := results()
	r[0].Scores["vmlu"] = 52 // percent where a rate was asked for

	a := small().Audit(r)
	if a.OK() {
		t.Fatal("a score of 52 on a scale of zero to one was accepted")
	}
	if !faultAbout(a, "rate between zero and one") {
		t.Errorf("the fault does not explain the scale: %v", a.Faults)
	}
}

func TestTheSameArmTwiceIsAFault(t *testing.T) {
	r := results()
	r = append(r, r[0])

	a := small().Audit(r)
	if !faultAbout(a, "reported twice") {
		t.Errorf("the same arm reported twice was accepted: %v", a.Faults)
	}
}

func TestAResultWithNoArmIsAFault(t *testing.T) {
	r := results()
	r[0].Arm = ""
	if a := small().Audit(r); !faultAbout(a, "no arm") {
		t.Errorf("a result belonging to nobody was accepted: %v", a.Faults)
	}
}

func TestTheFaultsComeOutInTheSameOrderEveryTime(t *testing.T) {
	// Maps are involved, so this is worth pinning: a report that reorders
	// itself between runs cannot be diffed, and diffing it is what somebody
	// checking this will do.
	r := results()
	r[0].Scores["them-mot"] = 0.5
	r[0].Scores["them-hai"] = 0.5
	delete(r[1].Scores, "vmlu")

	first := small().Audit(r).Faults
	for range 8 {
		if got := small().Audit(r).Faults; !equal(got, first) {
			t.Fatalf("the faults came out in a different order:\n%v\n%v", first, got)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTheTableKeepsTheHarnessOrderAndLeavesAGapAGap(t *testing.T) {
	r := results()
	delete(r[1].Scores, "vmlu")

	rows := small().Table(r)
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2", len(rows))
	}
	if rows[0][0] == nil || *rows[0][0] != 0.52 {
		t.Errorf("the first row does not hold the first arm's vmlu score: %v", rows[0][0])
	}
	if rows[0][1] != nil {
		// A missing number rendered as zero is a number, and on accuracy it is
		// the worst possible one, so an arm that did not report would look like
		// an arm that failed.
		t.Errorf("a missing number came back as %v rather than as missing", *rows[0][1])
	}
	if rows[1][0] == nil || *rows[1][0] != 0.031 {
		t.Errorf("the second row does not hold the diacritic score: %v", rows[1][0])
	}
}

func TestTheWinnerOnDiacriticErrorRateIsTheSmallestNumber(t *testing.T) {
	h := small()
	r := results()

	vmlu, _ := h.Task("vmlu")
	if got, ok := h.Winner(vmlu, r); !ok || got != "a" {
		t.Errorf("vmlu was won by %q, and a scored 0.52 against b's 0.49", got)
	}

	der, _ := h.Task("vi-diacritic")
	if got, ok := h.Winner(der, r); !ok || got != "a" {
		t.Errorf("the diacritic task was won by %q, and a scored 0.031 against b's 0.044, where smaller is better", got)
	}
}

func TestATieHasNoWinner(t *testing.T) {
	h := small()
	r := results()
	r[1].Scores["vmlu"] = r[0].Scores["vmlu"]

	vmlu, _ := h.Task("vmlu")
	if got, ok := h.Winner(vmlu, r); ok {
		t.Errorf("a tie was won by %q", got)
	}
}

func TestATaskNobodyReportedHasNoWinner(t *testing.T) {
	h := small()
	r := results()
	delete(r[0].Scores, "vmlu")
	delete(r[1].Scores, "vmlu")

	vmlu, _ := h.Task("vmlu")
	if _, ok := h.Winner(vmlu, r); ok {
		t.Error("a task nobody ran came back with a winner")
	}
}
