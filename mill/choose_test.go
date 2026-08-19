package mill

import (
	"errors"
	"strings"
	"testing"
)

// A run at one threshold, with everything the rule needs except the score, so a
// test says what it is varying and nothing else.
func run(threshold, retention, score float64) Ablation {
	return Ablation{
		Threshold: threshold,
		Retention: retention,
		Score:     score,
		Noise:     0.3,
		Tokens:    8_000_000_000,
		Eval:      "vi-cloze",
		Box:       "gamingpc",
	}
}

func TestNoAblationMeansTheDefaultIsStillADefault(t *testing.T) {
	c, err := Choose(nil)
	if !errors.Is(err, ErrNotMeasured) {
		t.Errorf("choosing from no runs returned %v, want ErrNotMeasured", err)
	}
	if c.Threshold != DefaultThreshold {
		t.Errorf("the fallback is %.2f, want the default %.2f", c.Threshold, DefaultThreshold)
	}
	if c.Measured {
		t.Error("a choice made from nothing came back marked as measured")
	}
}

func TestAWinnerAheadOfTheDefaultByMoreThanTheNoiseMovesTheThreshold(t *testing.T) {
	c, err := Choose([]Ablation{
		run(0.60, 0.71, 41.0),
		run(0.70, 0.79, 42.0),
		run(0.80, 0.84, 46.0),
		run(0.90, 0.91, 41.5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Measured {
		t.Errorf("a four point run with a clear winner was not called measured: %s", c.Why)
	}
	if c.Threshold != 0.80 {
		t.Errorf("chose %.2f, want 0.80, which beat the default's nearest run by more than %.0f standard errors", c.Threshold, Sigma)
	}
}

// The refusal that matters most. Four runs whose scores sit on top of each other
// have measured the eval's noise floor rather than the threshold, and saying so
// is the result.
func TestAFlatCurveKeepsTheDefaultAndSaysWhy(t *testing.T) {
	c, err := Choose([]Ablation{
		run(0.60, 0.71, 42.0),
		run(0.70, 0.79, 42.2),
		run(0.80, 0.84, 41.9),
		run(0.90, 0.91, 42.1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Measured {
		t.Error("a flat curve was reported as having chosen something")
	}
	if c.Threshold != DefaultThreshold {
		t.Errorf("a flat curve moved the threshold to %.2f", c.Threshold)
	}
	if len(c.Tied) != 4 {
		t.Errorf("%d of 4 runs tied, and on a flat curve all of them do", len(c.Tied))
	}
	if !strings.Contains(c.Why, "did not separate") {
		t.Errorf("the reason does not say the runs failed to separate: %q", c.Why)
	}
}

// A winner at the edge of the range says the range was drawn in the wrong place.
// The answer to that is another training run, not the edge.
func TestAWinnerAtTheEdgeOfTheRangeIsNotAWinner(t *testing.T) {
	c, err := Choose([]Ablation{
		run(0.60, 0.71, 39.0),
		run(0.70, 0.79, 41.0),
		run(0.80, 0.84, 43.0),
		run(0.90, 0.91, 48.0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Measured || c.Threshold != DefaultThreshold {
		t.Errorf("chose %.2f off a curve still rising at its own edge", c.Threshold)
	}
	if !strings.Contains(c.Why, "Widen the range") {
		t.Errorf("the reason does not ask for a wider range: %q", c.Why)
	}
}

// Between two answers the corpus cannot tell apart, the one that removes less
// wins, because everything the other one removes was removed for nothing.
func TestATieGoesToTheThresholdThatKeepsMoreDocuments(t *testing.T) {
	c, err := Choose([]Ablation{
		run(0.60, 0.60, 39.0),
		run(0.70, 0.72, 40.0),
		run(0.80, 0.88, 45.1),
		run(0.85, 0.90, 45.0),
		run(0.95, 0.97, 38.0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Measured {
		t.Fatalf("a curve with a clear peak was not called measured: %s", c.Why)
	}
	if c.Best.Threshold != 0.80 {
		t.Errorf("the best run is %.2f, want 0.80", c.Best.Threshold)
	}
	if c.Threshold != 0.85 {
		t.Errorf("chose %.2f, want 0.85, which is tied with 0.80 and keeps 90%% against 88%%", c.Threshold)
	}
	if !strings.Contains(c.Why, "removes less") {
		t.Errorf("the reason does not give the tie break: %q", c.Why)
	}
}

// A winner that cannot separate itself from the number already in use has not
// earned the change, whatever it does against the rest of the field.
func TestAWinnerTiedWithTheDefaultLeavesTheDefaultAlone(t *testing.T) {
	c, err := Choose([]Ablation{
		run(0.60, 0.60, 39.0),
		run(0.70, 0.72, 44.0),
		run(0.80, 0.88, 44.1),
		run(0.95, 0.97, 38.0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Measured || c.Threshold != DefaultThreshold {
		t.Errorf("chose %.2f over a default it beat by a tenth of a point", c.Threshold)
	}
	if !strings.Contains(c.Why, "the default stands") {
		t.Errorf("the reason does not say the default stands: %q", c.Why)
	}
}

func TestARunWithNoStandardErrorCannotBeComparedAgainstAnything(t *testing.T) {
	a := []Ablation{run(0.60, 0.71, 41), run(0.70, 0.79, 42), run(0.80, 0.84, 46)}
	a[1].Noise = 0
	if _, err := Choose(a); err == nil {
		t.Error("a score quoted without a standard error was accepted")
	}
	problems := CheckAblations(a)
	if len(problems) == 0 || !strings.Contains(problems[0], "standard error") {
		t.Errorf("the complaints do not name the missing standard error: %v", problems)
	}
}

// Two variables in one comparison is no comparison. The token count and the
// threshold cannot be pulled apart afterwards by any rule.
func TestARunThatChangedTwoThingsAtOnceIsRefused(t *testing.T) {
	a := []Ablation{run(0.60, 0.71, 41), run(0.70, 0.79, 42), run(0.80, 0.84, 46)}
	a[2].Tokens = 16_000_000_000
	if _, err := Choose(a); err == nil {
		t.Error("a set where one run trained on twice the tokens was accepted")
	}
	if !strings.Contains(strings.Join(CheckAblations(a), " "), "as much as the threshold") {
		t.Errorf("the complaints do not say the two got measured against each other: %v", CheckAblations(a))
	}
}

func TestRunsOnDifferentBoxesAreAHardwareComparison(t *testing.T) {
	a := []Ablation{run(0.60, 0.71, 41), run(0.70, 0.79, 42), run(0.80, 0.84, 46)}
	a[1].Box = "server2"
	if !strings.Contains(strings.Join(CheckAblations(a), " "), "hardware") {
		t.Errorf("a set spread across two boxes was not flagged: %v", CheckAblations(a))
	}
}

func TestARunWithNoBoxIsNotReproducible(t *testing.T) {
	a := []Ablation{run(0.60, 0.71, 41), run(0.70, 0.79, 42), run(0.80, 0.84, 46)}
	a[0].Box = ""
	if !strings.Contains(strings.Join(CheckAblations(a), " "), "reproduce") {
		t.Errorf("a run with no box behind it was accepted: %v", CheckAblations(a))
	}
}

func TestTwoPointsCannotChooseAThreshold(t *testing.T) {
	_, err := Choose([]Ablation{run(0.60, 0.71, 41), run(0.80, 0.84, 46)})
	if err == nil {
		t.Errorf("two runs chose a threshold, and %d is the fewest that can", MinAblations)
	}
}

// A set that sits entirely on one side of the number in use cannot say whether
// the number in use is wrong.
func TestASetThatDoesNotBracketTheDefaultCannotMoveIt(t *testing.T) {
	a := []Ablation{run(0.80, 0.84, 41), run(0.85, 0.88, 44), run(0.90, 0.91, 46)}
	if !strings.Contains(strings.Join(CheckAblations(a), " "), "each side") {
		t.Errorf("a set entirely above the default was accepted: %v", CheckAblations(a))
	}
}

func TestAThresholdRunTwiceIsOneMeasurementRatherThanTwo(t *testing.T) {
	a := []Ablation{run(0.60, 0.71, 41), run(0.70, 0.79, 42), run(0.70, 0.79, 43), run(0.80, 0.84, 46)}
	if !strings.Contains(strings.Join(CheckAblations(a), " "), "run twice") {
		t.Errorf("the same threshold scored twice was accepted as two points: %v", CheckAblations(a))
	}
}

func TestAThresholdOutsideTheRangeOfASimilarityIsRefused(t *testing.T) {
	a := []Ablation{run(0.60, 0.71, 41), run(0.70, 0.79, 42), run(1.4, 0.99, 46)}
	if !strings.Contains(strings.Join(CheckAblations(a), " "), "not a similarity") {
		t.Errorf("1.4 was accepted as a similarity: %v", CheckAblations(a))
	}
}

// The choice publishes the runs it came from, because a threshold quoted on its
// own is a default with a story attached.
func TestTheChoiceCarriesTheMeasurementUnderIt(t *testing.T) {
	c, err := Choose([]Ablation{
		run(0.60, 0.71, 41.0),
		run(0.70, 0.79, 42.0),
		run(0.80, 0.84, 46.0),
		run(0.90, 0.91, 41.5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Ablations) != 4 {
		t.Errorf("the choice carries %d runs, want all 4", len(c.Ablations))
	}
	out := c.String()
	for _, want := range []string{"0.80", "vi-cloze", "gamingpc", "retention"} {
		if !strings.Contains(out, want) {
			t.Errorf("the published choice does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "—") {
		t.Error("the published choice has an em dash in it")
	}
}
