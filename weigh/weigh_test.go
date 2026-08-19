package weigh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/cook"
)

// run is one arm that came back the way one should: the locked recipe, one
// checkpoint, one harness, one seed, and a curve that goes down.
func run(arm, data string, vmlu float64) Run {
	r := Run{
		Arm:        arm,
		Data:       data,
		Base:       "b3e0f1a9c2d4",
		Tokenizer:  "gemma-3",
		Tokens:     cook.Matched().Tokens,
		Batch:      cook.Matched().Batch,
		Sequence:   8192,
		PeakLR:     3e-5,
		Warmup:     0.01,
		Decay:      0.2,
		Precision:  "bf16",
		Seed:       17,
		Instance:   "8xH100-80GB",
		EvalBox:    "gamingpc",
		Harness:    "9d41c0b7ae52f6",
		Scores:     map[string]float64{Metric: vmlu, "vi-adherence": 0.86},
		BaseScores: map[string]float64{Metric: 44.1, "vi-adherence": 0.91},
	}
	loss := 2.31
	for step := range 40 {
		loss -= 0.004
		r.Curve = append(r.Curve, Point{Step: step * 500, Tokens: int64(step) * 5_000_000_000, Loss: loss})
	}
	return r
}

// comparison builds the three locked arms with the scores given, in the order
// nau locks them.
func comparison(gao, culturax, filtered float64) Comparison {
	arms := cook.Arms()
	return Comparison{
		Name: "com-8B-cpt",
		Runs: []Run{
			run(arms[0].ID, arms[0].Data, gao),
			run(arms[1].ID, arms[1].Data, culturax),
			run(arms[2].ID, arms[2].Data, filtered),
		},
	}
}

func refuses(t *testing.T, c Comparison, want string) {
	t.Helper()
	why := c.Blocking()
	if len(why) == 0 {
		t.Fatalf("the comparison was read and it should have been refused for %q", want)
	}
	for _, w := range why {
		if strings.Contains(w, want) {
			return
		}
	}
	t.Errorf("no refusal mentions %q, and what came back was:\n  %s", want, strings.Join(why, "\n  "))
}

func TestThreeArmsThatDifferOnlyInTheirDataAreAComparison(t *testing.T) {
	c := comparison(52.4, 46.9, 48.2)
	if why := c.Blocking(); len(why) > 0 {
		t.Fatalf("a matched comparison was refused: %v", why)
	}
	if !c.Controlled() {
		t.Fatal("three matched arms do not read as controlled")
	}
	if !c.Holds() {
		t.Fatalf("a comparison at a gap of %.1f and a lift of %.1f does not hold", c.Gap(), c.Lift())
	}
	if got := c.Gap(); got < 5.4 || got > 5.6 {
		t.Errorf("the gap came out at %.2f", got)
	}
	if v := c.Verdict(); !strings.Contains(v, "E6 and E7 both pass") {
		t.Errorf("the verdict does not say the gate passed:\n  %s", v)
	}
}

func TestAnArmGivenADifferentBatchIsNotTheSameRun(t *testing.T) {
	c := comparison(52.4, 46.9, 48.2)
	c.Runs[1].Batch = cook.Matched().Batch / 2
	refuses(t, c, "the recipe locks")
	if c.Controlled() {
		t.Error("arms on two batch sizes read as controlled")
	}
	if c.Holds() {
		t.Error("an uncontrolled comparison holds")
	}
	if v := c.Verdict(); !strings.Contains(v, "the gate is not reported against it") {
		t.Errorf("the verdict quotes a gate it should refuse to quote:\n  %s", v)
	}
	if strings.Contains(c.Verdict(), "5.5") {
		t.Error("the verdict prints the gap of an uncontrolled comparison, which is the number everybody would quote afterwards")
	}
}

func TestAnArmThatFinishedShortIsNotAMatchedRun(t *testing.T) {
	c := comparison(52.4, 46.9, 48.2)
	c.Runs[2].Tokens = cook.Matched().Tokens - 20_000_000_000
	refuses(t, c, "which is 10.0% apart against a 2.0% line")

	// A percent under the line is a scheduler hiccup rather than a shortened arm.
	near := comparison(52.4, 46.9, 48.2)
	near.Runs[2].Tokens = cook.Matched().Tokens - 1_000_000_000
	if why := near.Blocking(); len(why) > 0 {
		t.Errorf("a half percent of drift was refused: %v", why)
	}
}

func TestThreeArmsOnThreeSeedsCannotSeparateDataFromNoise(t *testing.T) {
	c := comparison(52.4, 46.9, 48.2)
	c.Runs[1].Seed = 23
	refuses(t, c, "the arms differ in something other than their data")

	harness := comparison(52.4, 46.9, 48.2)
	harness.Runs[2].Harness = "0000deadbeef11"
	refuses(t, harness, "harness")

	base := comparison(52.4, 46.9, 48.2)
	base.Runs[0].BaseScores = map[string]float64{Metric: 41.0}
	refuses(t, base, "a base model scoring")
}

func TestAnArmThatTrainedThroughASpikeIsADifferentRun(t *testing.T) {
	c := comparison(52.4, 46.9, 48.2)
	c.Runs[0].Curve[25].Loss += 0.4
	spikes := c.Runs[0].Spikes()
	if len(spikes) != 1 || spikes[0].Step != 25*500 {
		t.Fatalf("the spike was not found: %v", spikes)
	}
	refuses(t, c, "carries no restart")

	// A spike that was rolled back is how a run is supposed to handle one.
	rolled := comparison(52.4, 46.9, 48.2)
	rolled.Runs[0].Curve[25].Loss += 0.4
	rolled.Runs[0].Restarts = 1
	if why := rolled.Blocking(); len(why) > 0 {
		t.Errorf("a spike that was rolled back was refused: %v", why)
	}
}

func TestTwoArmsAreNotAControlledComparison(t *testing.T) {
	c := comparison(52.4, 46.9, 48.2)
	c.Runs = c.Runs[:2]
	refuses(t, c, "three arms that become two are not a controlled comparison")
	if missing := c.Missing(); len(missing) != 1 || missing[0] != "com-8B-cpt-culturax-filtered" {
		t.Errorf("the missing arm is not named: %v", missing)
	}

	var none Comparison
	refuses(t, none, "there is nothing to weigh")

	twice := comparison(52.4, 46.9, 48.2)
	twice.Runs = append(twice.Runs, twice.Runs[0])
	refuses(t, twice, "came back twice")

	loose := comparison(52.4, 46.9, 48.2)
	loose.Runs[0].Arm = "com-8B-cpt-gao-v2"
	refuses(t, loose, "is not one of the three arms the comparison was locked with")

	swapped := comparison(52.4, 46.9, 48.2)
	swapped.Runs[1].Data = "gao"
	refuses(t, swapped, "and the arm is defined as")
}

func TestAGapUnderFourPointsStopsTheFromScratchRun(t *testing.T) {
	c := comparison(48.9, 46.9, 48.4)
	if c.Holds() {
		t.Fatalf("a gap of %.1f points holds", c.Gap())
	}
	v := c.Verdict()
	if !strings.Contains(v, "the from scratch run does not start") {
		t.Errorf("the verdict does not say what a failed E6 costs:\n  %s", v)
	}
	if !strings.Contains(v, "cleaning matters and scale did not") {
		t.Errorf("the verdict does not name the diagnosis:\n  %s", v)
	}
}

func TestAnArmThatBeatCulturaxWithoutBeatingItsOwnBaseFoundAWeakBaseline(t *testing.T) {
	c := comparison(49.0, 44.5, 46.0)
	for i := range c.Runs {
		c.Runs[i].BaseScores = map[string]float64{Metric: 44.1, "vi-adherence": 0.91}
	}
	if c.Gap() < E6 {
		t.Fatalf("the gap is %.1f and this case needs it over %.0f", c.Gap(), E6)
	}
	if c.Holds() {
		t.Fatalf("a lift of %.1f points holds against E7 at %.0f", c.Lift(), E7)
	}
	if v := c.Verdict(); !strings.Contains(v, "looks like a pass and is not") {
		t.Errorf("the verdict does not say what the combination means:\n  %s", v)
	}
}

func TestTheFiltersOnlyArmTakingHalfTheGapChangesTheFinding(t *testing.T) {
	c := comparison(52.4, 46.9, 50.0)
	if got := c.Cleaning(); got < 0.55 || got > 0.57 {
		t.Fatalf("the filters only arm took %.2f of the gap", got)
	}
	if !c.Holds() {
		t.Fatal("a comparison that passes both gates does not hold because of the diagnosis")
	}
	if v := c.Verdict(); !strings.Contains(v, "most of what gao bought was the cleaning rather than the scale") {
		t.Errorf("the verdict does not report the diagnosis:\n  %s", v)
	}

	// Cleaning CulturaX and making it worse is a real outcome and prints as one.
	worse := comparison(52.4, 46.9, 45.1)
	if got := worse.Cleaning(); got > 0 {
		t.Errorf("an arm that lost to its own unfiltered data took %.2f of the gap", got)
	}
}

func TestAnArmThatCannotBeReadIsRefusedRatherThanScored(t *testing.T) {
	c := comparison(52.4, 46.9, 48.2)
	c.Runs[0].Scores = map[string]float64{"vi-adherence": 0.86}
	refuses(t, c, "carries no VMLU score")

	noBase := comparison(52.4, 46.9, 48.2)
	for i := range noBase.Runs {
		noBase.Runs[i].BaseScores = nil
	}
	refuses(t, noBase, "nothing to read E7 against")

	short := comparison(52.4, 46.9, 48.2)
	short.Runs[1].Curve = short.Runs[1].Curve[:4]
	refuses(t, short, "not enough to see a spike in")

	bare := comparison(52.4, 46.9, 48.2)
	bare.Runs[2].Instance = ""
	refuses(t, bare, "a curve with no hardware under it")

	unfixed := comparison(52.4, 46.9, 48.2)
	for i := range unfixed.Runs {
		unfixed.Runs[i].Harness = ""
	}
	refuses(t, unfixed, "nothing says the benchmarks were fixed before the run")

	loose := comparison(52.4, 46.9, 48.2)
	for i := range loose.Runs {
		loose.Runs[i].Base = ""
	}
	refuses(t, loose, "does not name the checkpoint it continued from")
}

func TestReadingTheArmsOffDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "arms.jsonl")
	lines := make([]string, 0, 3)
	for _, r := range comparison(52.4, 46.9, 48.2).Runs {
		lines = append(lines, fmt.Sprintf(
			`{"arm":%q,"data":%q,"base":%q,"tokenizer":"gemma-3","tokens":%d,"batch":%d,"sequence":8192,"peak_lr":3e-05,"warmup":0.01,"decay":0.2,"precision":"bf16","seed":17,"instance":"8xH100-80GB","eval_box":"gamingpc","harness":%q,"restarts":0,"curve":[{"step":0,"tokens":0,"loss":2.31}],"scores":{"vmlu":%.1f},"base_scores":{"vmlu":44.1}}`,
			r.Arm, r.Data, r.Base, r.Tokens, r.Batch, r.Harness, r.Scores[Metric]))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ReadComparison("com-8B-cpt", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Runs) != 3 {
		t.Fatalf("%d arms came off three lines", len(c.Runs))
	}
	if got := c.Gap(); got < 5.4 || got > 5.6 {
		t.Errorf("the gap read off disk came out at %.2f", got)
	}
	// One point of curve is not a run, and the reader does not decide that.
	refuses(t, c, "not enough to see a spike in")

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"arm":"com-8B-cpt-gao","peak_learning_rate":3e-05}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadComparison("x", bad); err == nil {
		t.Error("an arm carrying an undeclared field was read")
	}
	if _, err := ReadComparison("x", filepath.Join(dir, "nothing.jsonl")); err == nil {
		t.Error("a file that is not there read as a comparison")
	}
}
