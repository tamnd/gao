package spike_test

// Every reading in this file is taken off a real training log in testdata. The
// mutations below cut, thin and corrupt those logs, which is what a real log
// arrives having had done to it, but nothing here draws a curve with a formula.

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tamnd/gao/spike"
)

// The runs in testdata are four thousand steps of a hundred thousand step plan,
// checkpointing every two hundred.
const (
	total = 100_000
	every = 200
)

func load(t *testing.T, name string) []spike.Step {
	t.Helper()
	steps, err := spike.ReadSteps("testdata/" + name + ".jsonl")
	if err != nil {
		t.Fatalf("reading the %s log: %v", name, err)
	}
	if len(steps) == 0 {
		t.Fatalf("the %s log is empty", name)
	}
	return steps
}

func read(t *testing.T, name string) spike.Curve {
	t.Helper()
	c := spike.ReadCurve(name, total, every, load(t, name))
	if why := c.Blocking(); len(why) > 0 {
		t.Fatalf("the %s log was refused: %s", name, strings.Join(why, "; "))
	}
	return c
}

func says(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, want) {
			return
		}
	}
	t.Errorf("nothing said %q:\n%s", want, strings.Join(lines, "\n"))
}

func silent(t *testing.T, lines []string, about string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, about) {
			t.Errorf("this was said about %s, and it should not have been: %s", about, l)
		}
	}
}

func TestTheRunWhereNothingHappenedIsReadAsOne(t *testing.T) {
	c := read(t, "on-dinh")

	if len(c.Spikes) != 0 {
		t.Errorf("the clean run reported %d spikes, the first at step %d", len(c.Spikes), c.Spikes[0].Step)
	}
	if !c.Holds() {
		t.Errorf("the clean run did not hold: %s", strings.Join(c.Faults(), "; "))
	}
	if c.Rows != 400 || c.First != 0 || c.Last != 3990 || c.Every != 10 {
		t.Errorf("the log is 400 rows from step 0 to 3990 every 10, read as %d rows from %d to %d every %d",
			c.Rows, c.First, c.Last, c.Every)
	}
	if !c.Grad || !c.LR {
		t.Error("the log carries a gradient norm and a learning rate on every row and the reading says it does not")
	}
}

// The learning rate here was set twenty five times too high for thirty steps,
// which is a resume that came back without its scheduler state. This is the
// finding the protocol exists for.
func TestARealBlowupIsFoundAndPlacedOnTheRun(t *testing.T) {
	c := read(t, "vot-len")

	if len(c.Spikes) != 1 {
		t.Fatalf("the blowup came back as %d spikes: %+v", len(c.Spikes), c.Spikes)
	}
	s := c.Spikes[0]
	if s.Step != 2530 {
		t.Errorf("the rate was raised at step 2500 and the spike is reported at step %d", s.Step)
	}
	if s.Over < 0.4 {
		t.Errorf("the loss went to %.4f against a trailing %.4f and that is reported as %.1f%% over", s.Loss, s.Base, s.Over*100)
	}
	if s.Grad <= 1 {
		t.Errorf("the gradient norm beside the spike is %.3f, and it is the whole of telling one kind of spike from another", s.Grad)
	}
	if s.Band < s.Base*(1+spike.Rise) {
		t.Errorf("the band came back at %.4f, under the floor of %.4f", s.Band, s.Base*(1+spike.Rise))
	}
	if !s.Recovered() || s.Back != 2580 {
		t.Errorf("the loss came back at step %d, reported as %d", 2580, s.Back)
	}
}

// The same mistake, four hundred times too high for sixty steps. It is a
// different finding and not a larger one.
func TestARunThatNeverCameBackIsNotTheSameFindingAsOneThatDid(t *testing.T) {
	blip := read(t, "vot-len")
	gone := read(t, "phan-ky")

	if blip.Diverged != 0 {
		t.Errorf("a run that came back is counted as %d diverged", blip.Diverged)
	}
	silent(t, blip.Faults(), "never came back")
	if !blip.Holds() {
		t.Errorf("a run that spiked once and came back does not hold: %s", strings.Join(blip.Faults(), "; "))
	}

	if gone.Diverged != 1 || gone.Spikes[0].Recovered() {
		t.Fatalf("the run that never came back reported %d diverged: %+v", gone.Diverged, gone.Spikes)
	}
	says(t, gone.Faults(), "never came back inside the band")
	if gone.Holds() {
		t.Error("a run that diverged holds")
	}
}

func TestTheRewindIsMeasuredToTheCheckpointBeforeTheSpike(t *testing.T) {
	c := read(t, "vot-len")

	s := c.Spikes[0]
	if s.From != 2400 || s.Rewind != 130 {
		t.Errorf("a spike at step %d with a checkpoint every %d rewinds to step %d and costs %d steps",
			s.Step, every, s.From, s.Rewind)
	}
	if c.Rewind != s.Rewind {
		t.Errorf("one spike costing %d steps came back as %d over the run", s.Rewind, c.Rewind)
	}
	if math.Abs(c.Cost-0.0013) > 1e-9 {
		t.Errorf("%d steps of a %d step run came back as %.4f of it", c.Rewind, total, c.Cost)
	}
}

// The cadence is the price of the protocol, so a cadence that makes a rewind
// cost more than the run can afford is a fault about the cadence rather than
// about the spike.
func TestACadenceThatMakesARewindTooExpensiveIsAFaultAboutTheCadence(t *testing.T) {
	c := spike.ReadCurve("vot-len", total, 5000, load(t, "vot-len"))

	if len(c.Spikes) != 1 {
		t.Fatalf("the blowup came back as %d spikes", len(c.Spikes))
	}
	says(t, c.Faults(), "the checkpoint cadence of 5000 steps is the thing to change")
	says(t, c.Faults(), "2.5% of the run")
	if c.Holds() {
		t.Error("a run whose rewind costs more than the run can afford holds")
	}
}

// The band is two tests and a step has to clear both. This is why: the clean run
// puts rows a tenth over its own trailing median all the way through, so the
// rise on its own would report a run where nothing happened as a run with a
// dozen spikes in it.
func TestTheRiseOnItsOwnWouldReportTheCleanRun(t *testing.T) {
	c := read(t, "on-dinh")
	steps := load(t, "on-dinh")

	var over int
	for i := spike.Window; i < len(steps); i++ {
		window := make([]float64, 0, spike.Window)
		for _, s := range steps[i-spike.Window : i] {
			window = append(window, s.Loss)
		}
		slices.Sort(window)
		if steps[i].Loss > window[len(window)/2]*(1+spike.Rise) {
			over++
		}
	}

	if over < 5 {
		t.Fatalf("only %d rows of the clean run are over the rise, so this test is not testing what it says", over)
	}
	if len(c.Spikes) != 0 {
		t.Errorf("%d rows of the clean run clear the rise and %d of them were reported as spikes", over, len(c.Spikes))
	}
}

// This run is ten times longer and logged a hundredth as often, which is what a
// log looks like when somebody turned the logging down to keep a dashboard
// readable.
func TestLoggingTooCoarseForTheProtocolIsSaidSoRatherThanReadAsClean(t *testing.T) {
	c := read(t, "ghi-thua")

	if c.Every != 100 {
		t.Errorf("the log is written every 100 steps and the reading says every %d", c.Every)
	}
	says(t, c.Faults(), "the loss is logged every 100 steps")
	says(t, c.Faults(), "cannot tell a clean run from an unlogged one")
	if c.Holds() {
		t.Error("a run logged too coarsely to have held a spike holds")
	}
}

// A resume that comes back without its logging leaves a hole, and a hole is
// exactly where a spike would not have been recorded.
func TestAHoleInTheLogIsNamedAtTheStepItStarts(t *testing.T) {
	in := load(t, "on-dinh")
	gapped := append(append([]spike.Step(nil), in[:200]...), in[240:]...)

	c := spike.ReadCurve("on-dinh", total, every, gapped)

	says(t, c.Faults(), "the log jumps at 1 place, starting at step 2400")
	if c.Holds() {
		t.Error("a log with forty rows missing out of the middle holds")
	}
}

func TestALogWithNoGradientNormOrNoLearningRateSaysWhatThatCosts(t *testing.T) {
	in := load(t, "vot-len")
	for i := range in {
		in[i].Grad, in[i].LR = 0, 0
	}

	c := spike.ReadCurve("vot-len", total, every, in)

	if c.Grad || c.LR {
		t.Errorf("a log carrying neither reported grad %v and lr %v", c.Grad, c.LR)
	}
	says(t, c.Faults(), "no gradient norm")
	says(t, c.Faults(), "no learning rate")
	if len(c.Spikes) != 1 {
		t.Errorf("stripping the diagnostics changed the detection to %d spikes, and it is not part of the detection", len(c.Spikes))
	}
}

// Forty thousand steps on seven kilobytes of text, with five blowups in it. The
// model memorizes the corpus somewhere around step ten thousand and the loss
// collapses to a twentieth of a nat, which is a regime no pretraining run
// reaches and a regime where a band that is a fraction of the median is a
// fraction of nearly nothing. The protocol reports a hundred spikes off it.
//
// That is the right answer rather than a broken one, and it is the reason the
// count is a fault: past a few, the curve is the finding and the table under it
// is not a work list.
func TestACurveWithMoreSpikesThanTheProtocolHandlesSaysTheCurveIsTheFinding(t *testing.T) {
	c := read(t, "vot-nhieu")

	if len(c.Spikes) <= spike.MaxSpikes {
		t.Fatalf("this run came back as %d spikes: %+v", len(c.Spikes), c.Spikes)
	}
	says(t, c.Faults(), "the curve is the finding")
	if c.Holds() {
		t.Error("a run with a hundred spikes in it holds")
	}
}

// And this is what the gradient norm is on the report for. Three of the
// excursions in that run are blowups the rate caused and the other ninety nine
// are noise at a collapsed scale. Sorted by loss the three do not come out on
// top. Sorted by gradient norm they are the top three, above every other spike
// in the run, which is the whole argument for the column and for the fault that
// fires when a log does not carry it.
func TestTheGradientNormSeparatesARealBlowupFromNoiseTheLossCannot(t *testing.T) {
	c := read(t, "vot-nhieu")

	byGrad := slices.Clone(c.Spikes)
	slices.SortFunc(byGrad, func(a, b spike.Spike) int {
		switch {
		case a.Grad > b.Grad:
			return -1
		case a.Grad < b.Grad:
			return 1
		}
		return 0
	})

	want := []int{8010, 12010, 16010}
	got := []int{byGrad[0].Step, byGrad[1].Step, byGrad[2].Step}
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the three blowups are at %v and the three highest gradient norms are at %v", want, got)
	}
	if byGrad[2].Grad <= byGrad[3].Grad {
		t.Errorf("the third blowup is at a gradient norm of %.3f and the next spike is at %.3f, which is not a separation",
			byGrad[2].Grad, byGrad[3].Grad)
	}
}

func TestALogTheProtocolCannotBeRunAgainstIsRefused(t *testing.T) {
	edit := func(f func([]spike.Step) []spike.Step) []string {
		in := f(load(t, "on-dinh"))
		return spike.ReadCurve("on-dinh", total, every, in).Blocking()
	}

	for _, c := range []struct {
		name string
		why  []string
		want string
	}{
		{"no run", spike.ReadCurve("", total, every, load(t, "on-dinh")).Blocking(), "does not say what run it came off"},
		{"no cadence", spike.ReadCurve("on-dinh", total, 0, load(t, "on-dinh")).Blocking(), "there is a detector here and no protocol"},
		{"no length", spike.ReadCurve("on-dinh", 0, every, load(t, "on-dinh")).Blocking(), "how long it is"},
		{"no steps", spike.ReadCurve("on-dinh", total, every, nil).Blocking(), "holds no steps"},
		{"a loss that is not a number", edit(func(in []spike.Step) []spike.Step {
			in[300].Loss = math.NaN()
			return in
		}), "step 3000 carries a loss that is not a number"},
		{"a step logged twice", edit(func(in []spike.Step) []spike.Step {
			in[300].Step = in[299].Step
			return in
		}), "step 2990 is logged twice"},
		{"a log that goes backwards", edit(func(in []spike.Step) []spike.Step {
			in[300].Step = 5
			return in
		}), "two runs concatenated"},
		{"a log past the end of the run", edit(func(in []spike.Step) []spike.Step {
			in[len(in)-1].Step = total + 1
			return in
		}), "one of the two numbers is from a different run"},
		{"too short to take a band off", edit(func(in []spike.Step) []spike.Step {
			return in[:100]
		}), "the first 100 rows have nothing to be judged against"},
	} {
		t.Run(c.name, func(t *testing.T) {
			says(t, c.why, c.want)
		})
	}
}

// A log that cannot be read is not a log with faults in it, and printing both
// would be printing a reading that was never taken.
func TestALogThatIsRefusedReportsNoFaults(t *testing.T) {
	c := spike.ReadCurve("", 0, 0, nil)

	if got := c.Faults(); got != nil {
		t.Errorf("a refused log reported faults: %s", strings.Join(got, "; "))
	}
	if c.Holds() {
		t.Error("a refused log holds")
	}
	if c.Verdict() != c.Blocking()[0] {
		t.Errorf("the verdict of a refused log is %q", c.Verdict())
	}
}

func TestTheVerdictSaysWhatWasReadAndWhatItWouldHaveCost(t *testing.T) {
	held := read(t, "on-dinh").Verdict()
	spiked := read(t, "vot-len").Verdict()

	for _, want := range []string{
		"on-dinh logged 400 rows from step 0 to step 3990, every 10 steps",
		"median loss",
		"the protocol had nothing to do",
		"the checkpoint cadence of 200 steps was never tested by this run",
	} {
		if !strings.Contains(held, want) {
			t.Errorf("the verdict of the clean run does not say %q: %s", want, held)
		}
	}
	for _, want := range []string{"1 spike cleared the band", "1 of them came back on their own", "130 steps", "0.1% of the run"} {
		if !strings.Contains(spiked, want) {
			t.Errorf("the verdict does not say %q: %s", want, spiked)
		}
	}
}

func TestTheSameLogReadTwiceGivesTheSameAnswer(t *testing.T) {
	in := load(t, "phan-ky")

	first := spike.ReadCurve("phan-ky", total, every, in)
	second := spike.ReadCurve("phan-ky", total, every, in)

	if !reflect.DeepEqual(first.Spikes, second.Spikes) {
		t.Errorf("two readings of one log:\n%+v\n%+v", first.Spikes, second.Spikes)
	}
	if first.Verdict() != second.Verdict() {
		t.Errorf("two verdicts off one log:\n%s\n%s", first.Verdict(), second.Verdict())
	}
}
