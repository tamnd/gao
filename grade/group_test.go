package grade

import (
	"math"
	"strings"
	"testing"
)

// group builds a group whose rollouts scored the given rewards, all checked.
func group(rewards ...float64) *Group {
	g := NewGroup("dau", "một trang")
	for i, r := range rewards {
		g.Add("answer", checked("dau", r, "rollout %d", i))
	}
	return g
}

func TestTheGroupIsItsOwnBaseline(t *testing.T) {
	g := group(0.2, 0.4, 0.6, 0.8)
	if got := g.Mean(); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("mean is %v, want 0.5", got)
	}
	// Population deviation of 0.2 0.4 0.6 0.8 around 0.5 is sqrt(0.05).
	if got := g.Deviation(); math.Abs(got-math.Sqrt(0.05)) > 1e-9 {
		t.Fatalf("deviation is %v, want %v", got, math.Sqrt(0.05))
	}

	rollouts := g.Rollouts()
	sum := 0.0
	for _, r := range rollouts {
		sum += r.Advantage
	}
	if math.Abs(sum) > 1e-9 {
		t.Errorf("the advantages sum to %v, and a baseline the group is not centered on is not a baseline", sum)
	}
	if rollouts[0].Advantage >= 0 || rollouts[3].Advantage <= 0 {
		t.Errorf("the worst rollout scored %+.2f and the best %+.2f", rollouts[0].Advantage, rollouts[3].Advantage)
	}
}

func TestAGroupWhereEveryRolloutScoredTheSameTeachesNothing(t *testing.T) {
	g := group(0.5, 0.5, 0.5, 0.5)
	ok, why := g.Teaches()
	if ok {
		t.Fatal("a group with no spread in it was kept")
	}
	if !strings.Contains(why, "0.500") {
		t.Errorf("the reason does not say what everything scored: %q", why)
	}
	for _, r := range g.Rollouts() {
		if r.Advantage != 0 {
			t.Errorf("a group that teaches nothing produced advantage %+.2f", r.Advantage)
		}
	}
}

func TestAGroupWithAlmostNoSpreadIsDroppedRatherThanAmplified(t *testing.T) {
	g := group(0.500, 0.500, 0.500, 0.505)
	if ok, _ := g.Teaches(); ok {
		t.Fatalf("a group spanning %v was kept, and dividing by that turns rounding into a gradient", g.Deviation())
	}
	// The same group with a real spread in it is kept.
	if ok, why := group(0.1, 0.3, 0.7, 0.9).Teaches(); !ok {
		t.Fatalf("a group with a real spread was dropped: %s", why)
	}
}

func TestTwoRolloutsAreNotABaseline(t *testing.T) {
	g := group(0.1, 0.9)
	if ok, why := g.Teaches(); ok {
		t.Fatal("a group of two was used as its own baseline")
	} else if !strings.Contains(why, "4") {
		t.Errorf("the reason does not name the floor: %q", why)
	}
}

func TestAnAnswerNobodyCouldGradeIsDroppedRatherThanScoredZero(t *testing.T) {
	g := NewGroup("dau", "một trang")
	for _, r := range []float64{0.6, 0.7, 0.8, 0.9} {
		g.Add("answer", checked("dau", r, "graded"))
	}
	g.Add("cut off", Overlong("dau"))

	if g.Sampled() != 5 || g.Checked() != 4 || g.Dropped() != 1 {
		t.Fatalf("sampled %d, checked %d, dropped %d", g.Sampled(), g.Checked(), g.Dropped())
	}
	if got := g.Mean(); math.Abs(got-0.75) > 1e-9 {
		t.Errorf("the mean is %v, so the ungraded rollout was averaged in as a zero and lowered the bar for every other answer", got)
	}
	if a := g.Rollouts()[4].Advantage; a != 0 {
		t.Errorf("the ungraded rollout carries advantage %+.2f", a)
	}
}

func TestAGroupWithNothingCheckedInItSaysSoRatherThanDividingByZero(t *testing.T) {
	g := NewGroup("dau", "một trang")
	for i := 0; i < 4; i++ {
		g.Add("cut off", Overlong("dau"))
	}
	if g.Mean() != 0 || g.Deviation() != 0 {
		t.Fatalf("mean %v, deviation %v", g.Mean(), g.Deviation())
	}
	if ok, _ := g.Teaches(); ok {
		t.Fatal("a group nobody could grade was kept")
	}
	for _, r := range g.Rollouts() {
		if math.IsNaN(r.Advantage) {
			t.Fatal("an advantage came back not a number")
		}
	}
}

func TestAddingARolloutAfterTheAdvantagesWereReadRecomputesThem(t *testing.T) {
	g := group(0.2, 0.4, 0.6, 0.8)
	before := g.Rollouts()[0].Advantage
	g.Add("answer", checked("dau", 0.0, "a bad one"))
	after := g.Rollouts()[0].Advantage
	if before == after {
		t.Error("the advantages did not move when the group did")
	}
	if got := g.Mean(); math.Abs(got-0.4) > 1e-9 {
		t.Errorf("the mean is %v, want 0.4", got)
	}
}

func TestTheBatchSaysWhatTheStepBoughtAgainstWhatItCost(t *testing.T) {
	var b Batch
	b.Add(group(0.1, 0.3, 0.7, 0.9)) // teaches
	b.Add(group(0.5, 0.5, 0.5, 0.5)) // no spread
	b.Add(group(0.1, 0.9))           // too few
	b.Add(group(0.2, 0.4, 0.6, 0.8)) // teaches

	if b.Groups != 4 || b.Kept != 2 {
		t.Fatalf("%d groups, %d kept", b.Groups, b.Kept)
	}
	if math.Abs(b.Yield()-0.5) > 1e-9 {
		t.Errorf("yield is %v, want 0.5", b.Yield())
	}
	if b.Rollouts != 14 || b.Checked != 14 {
		t.Errorf("%d rollouts, %d checked", b.Rollouts, b.Checked)
	}
	if b.Unchecked() != 0 {
		t.Errorf("nothing was cut off and the unchecked share is %v", b.Unchecked())
	}
}

func TestABatchThatCouldNotGradeHalfOfWhatItSampledSaysSo(t *testing.T) {
	var b Batch
	for i := 0; i < 2; i++ {
		g := NewGroup("dau", "một trang")
		for _, r := range []float64{0.2, 0.8} {
			g.Add("answer", checked("dau", r, "graded"))
		}
		g.Add("cut off", Overlong("dau"))
		g.Add("cut off", Overlong("dau"))
		b.Add(g)
	}
	if math.Abs(b.Unchecked()-0.5) > 1e-9 {
		t.Fatalf("the unchecked share is %v, want 0.5", b.Unchecked())
	}
	if !strings.Contains(b.String(), "50% of them unchecked") {
		t.Errorf("the log line hides it: %q", b.String())
	}
}

func TestAnEmptyBatchReportsZeroRatherThanDividingByZero(t *testing.T) {
	var b Batch
	if b.Yield() != 0 || b.Unchecked() != 0 {
		t.Fatalf("yield %v, unchecked %v", b.Yield(), b.Unchecked())
	}
}

func TestTheGroupLogSaysWhyItWasDropped(t *testing.T) {
	got := group(0.5, 0.5, 0.5, 0.5).String()
	if !strings.Contains(got, "dropped:") {
		t.Errorf("a dropped group logs as if it were kept: %q", got)
	}
	if strings.Contains(group(0.1, 0.3, 0.7, 0.9).String(), "dropped:") {
		t.Error("a kept group logs as dropped")
	}
}
