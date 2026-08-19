package tighten

import (
	"strings"
	"testing"
)

// steps builds a log of n steps that behaves the way a healthy run behaves:
// the reward climbs, the entropy settles rather than collapses, the upper bound
// clips something, and most groups still teach at the end.
func steps(n int) []Step {
	out := make([]Step, 0, n)
	for i := range n {
		f := float64(i) / float64(n)
		groups := 1536
		kept := int(float64(groups) * (0.85 - 0.30*f))
		out = append(out, Step{
			Step:      i + 1,
			Box:       "8xH200 booked",
			Groups:    groups,
			Kept:      kept,
			Rollouts:  groups * Group,
			Truncated: groups * Group / 100,
			ClipLow:   0.031,
			ClipHigh:  0.004,
			Entropy:   0.92 - 0.22*f,
			Reward:    0.41 + 0.33*f,
		})
	}
	return out
}

func run(n int) Run {
	return Run{Specialist: "dau", Recipe: Plan(), Steps: steps(n)}
}

func refuses(t *testing.T, why []string, want string) {
	t.Helper()
	if len(why) == 0 {
		t.Fatalf("nothing was refused, and %q should have been", want)
	}
	for _, w := range why {
		if strings.Contains(w, want) {
			return
		}
	}
	t.Errorf("no refusal mentions %q, and what came back was:\n  %s", want, strings.Join(why, "\n  "))
}

func TestThePlannedRecipeHolds(t *testing.T) {
	r := Plan()
	if why := r.Blocking(); len(why) > 0 {
		t.Fatalf("the recipe the plan fixes does not hold: %v", why)
	}
	if !r.Holds() {
		t.Fatal("Holds disagrees with Blocking")
	}
	if r.EpsHigh <= r.EpsLow {
		t.Error("the planned bounds are not decoupled, which is the whole of clip-higher")
	}
	if r.Prompt+r.MaxResponse > Context {
		t.Errorf("the planned lengths need %d tokens against a context of %d", r.Prompt+r.MaxResponse, Context)
	}

	rows := r.Rows()
	if len(rows) < 8 {
		t.Fatalf("the recipe prints %d rows", len(rows))
	}
	for _, row := range rows {
		if row.Setting == "" || row.Why == "" {
			t.Errorf("%s is printed with no setting or no reason, and a number with no reason is a number somebody tunes", row.Element)
		}
	}
}

func TestASymmetricRecipeIsNotClipHigher(t *testing.T) {
	same := Plan()
	same.EpsHigh = same.EpsLow
	refuses(t, same.Blocking(), "which is symmetric clipping with clip-higher written next to it")

	backwards := Plan()
	backwards.EpsHigh = 0.1
	refuses(t, backwards.Blocking(), "the run tightens what should be loose")

	wide := Plan()
	wide.EpsHigh = 0.9
	refuses(t, wide.Blocking(), "there is no trust region left to speak of")

	none := Plan()
	none.EpsLow = 0
	refuses(t, none.Blocking(), "nothing bounds a token whose probability is falling")
}

func TestTheSettingsThatDecideWhatIsTrainedAreRefusedRatherThanWarnedAbout(t *testing.T) {
	small := Plan()
	small.Group = 4
	refuses(t, small.Blocking(), "the prompts the model understood least")

	seq := Plan()
	seq.Aggregation = Sequence
	refuses(t, seq.Blocking(), "divides a long correct answer by its own length")

	penalized := Plan()
	penalized.Penalized = true
	refuses(t, penalized.Blocking(), "it trains stopping early")

	flat := Plan()
	flat.Oversample = 1
	refuses(t, flat.Blocking(), "a batch size nothing ran at")

	kl := Plan()
	kl.KL, kl.Ablated = 0.001, false
	refuses(t, kl.Blocking(), "a coefficient about another model's reference policy")
}

func TestLengthsThatDoNotFitTheContextAreRefused(t *testing.T) {
	long := Plan()
	long.Prompt = 130000
	refuses(t, long.Blocking(), "cannot be answered at all")

	unset := Plan()
	unset.MaxResponse = 0
	refuses(t, unset.Blocking(), "the limit that is not written down is the one that ends up doing the grading")

	batch := Plan()
	batch.Batch = 0
	refuses(t, batch.Blocking(), "which is what the oversampling is sized against")
}

func TestAHealthyRunReadsAsOne(t *testing.T) {
	r := run(120)
	if why := r.Blocking(); len(why) > 0 {
		t.Fatalf("a well formed log was refused: %v", why)
	}
	if faults := r.Faults(); len(faults) > 0 {
		t.Fatalf("a healthy run came back with faults: %v", faults)
	}
	if !r.Holds() {
		t.Fatal("Holds disagrees with Faults")
	}
	if r.Box() != "8xH200 booked" {
		t.Errorf("the run names %q as its hardware", r.Box())
	}
	if !r.Binds() {
		t.Error("a run that clipped at the upper bound reads as one that never did")
	}
	if !r.Fills() {
		t.Errorf("3.0x sampling does not fill the batch at a late yield of %.3f", r.Late())
	}
	start, now := r.Entropy()
	if start <= now {
		t.Errorf("the entropy went from %.3f to %.3f, and this fixture falls", start, now)
	}
	if got := r.Yield(); got < 0.6 || got > 0.75 {
		t.Errorf("the yield came out at %.3f", got)
	}
	if v := r.Verdict(); !strings.Contains(v, "what the reward did is what the training did") {
		t.Errorf("the verdict of a clean run does not say so:\n  %s", v)
	}
}

func TestAnEntropyCollapseIsReportedAgainstTheRewardRatherThanAlone(t *testing.T) {
	r := run(60)
	for i := range r.Steps {
		if i >= len(r.Steps)-Window {
			r.Steps[i].Entropy = 0.21
			r.Steps[i].Reward = 0.44
		}
	}
	faults := r.Faults()
	refuses(t, faults, "this is the policy closing rather than the policy learning")
	if !strings.Contains(faults[0], "0.44") {
		t.Errorf("the collapse is reported without the reward beside it:\n  %s", faults[0])
	}
	if r.Holds() {
		t.Error("a collapsed run holds")
	}
}

func TestAnUpperBoundThatNeverBoundIsNotEvidence(t *testing.T) {
	r := run(40)
	for i := range r.Steps {
		r.Steps[i].ClipHigh = 0
	}
	refuses(t, r.Faults(), "this run is not evidence about clip-higher either way")
}

func TestALengthLimitThatIsDoingTheGradingIsNamed(t *testing.T) {
	r := run(40)
	for i := range r.Steps {
		r.Steps[i].Truncated = r.Steps[i].Rollouts / 4
	}
	refuses(t, r.Faults(), "the length limit is grading answers the verifier never saw")
}

func TestABatchThatStoppedFillingSaysWhatWouldFillIt(t *testing.T) {
	r := run(40)
	for i := range r.Steps[len(r.Steps)-Window:] {
		s := &r.Steps[len(r.Steps)-Window+i]
		s.Kept = s.Groups / 5
	}
	faults := r.Faults()
	refuses(t, faults, "wants 5.0x to fill")
	if got := r.Needed(); got < 4.9 || got > 5.1 {
		t.Errorf("the factor the late yield asks for came out at %.2f", got)
	}
	if r.Fills() {
		t.Error("a run at a fifth yield fills a batch drawn at 3.0x")
	}
}

func TestALogThatIsNotOneRunIsRefusedRatherThanRead(t *testing.T) {
	var none Run
	refuses(t, none.Blocking(), "there is nothing to read")
	if none.Yield() != 0 || none.Truncation() != 0 || none.Needed() != 0 {
		t.Error("an empty log has a yield, a truncation share, or an oversampling factor")
	}
	if len(none.Faults()) != 0 {
		t.Error("an empty log has faults, which are readings about a run that did not happen")
	}

	boxes := run(20)
	boxes.Steps[3].Box = "somewhere else"
	refuses(t, boxes.Blocking(), "a training curve with no hardware under it cannot be compared with another one")

	twice := run(20)
	twice.Steps[5].Step = twice.Steps[4].Step
	refuses(t, twice.Blocking(), "a step counted twice is a step whose rollouts are counted twice")

	kept := run(20)
	kept.Steps[2].Kept = kept.Steps[2].Groups + 1
	refuses(t, kept.Blocking(), "which is more than it sampled")

	cut := run(20)
	cut.Steps[2].Truncated = cut.Steps[2].Rollouts + 1
	refuses(t, cut.Blocking(), "which is more than it generated")

	rollouts := run(20)
	rollouts.Steps[7].Rollouts, rollouts.Steps[7].Truncated = 100, 1
	refuses(t, rollouts.Blocking(), "and the log records 100")

	clipped := run(20)
	clipped.Steps[1].ClipHigh = 1.4
	refuses(t, clipped.Blocking(), "a share of the tokens is in [0,1]")

	reward := run(20)
	reward.Steps[1].Reward = 4
	refuses(t, reward.Blocking(), "a verifier here returns a share of something countable")

	entropy := run(20)
	entropy.Steps[1].Entropy = -0.5
	refuses(t, entropy.Blocking(), "reports an entropy of -0.500")
}

func TestARunOffARecipeThatDoesNotHoldIsNotRead(t *testing.T) {
	r := run(20)
	r.Recipe.Aggregation = Sequence
	why := r.Blocking()
	refuses(t, why, "the configuration these steps came off does not hold")
	if len(why) != 1 {
		t.Errorf("a run off a bad recipe reports %d refusals, and the recipe is the only one worth reading", len(why))
	}
	if !strings.Contains(r.Verdict(), "This log is not a training run that can be read") {
		t.Errorf("the verdict reads a run it refused:\n  %s", r.Verdict())
	}
}

func TestTheStepsAreSortedIntoTheOrderTheyWereTaken(t *testing.T) {
	r := run(30)
	r.Steps[0], r.Steps[29] = r.Steps[29], r.Steps[0]
	r.Sort()
	for i := 1; i < len(r.Steps); i++ {
		if r.Steps[i-1].Step > r.Steps[i].Step {
			t.Fatalf("step %d comes before step %d", r.Steps[i-1].Step, r.Steps[i].Step)
		}
	}
	// The window at each end is what the entropy and the yield are read over, so
	// an unsorted log reads the wrong ten steps as the present.
	start, now := r.Entropy()
	if start <= now {
		t.Errorf("after sorting, the entropy reads %.3f to %.3f", start, now)
	}
}

func TestAShortRunReadsItsWholeSelfAsBothWindows(t *testing.T) {
	r := run(4)
	start, now := r.Entropy()
	if start != now {
		t.Errorf("a run shorter than the window reads two different entropies, %.3f and %.3f", start, now)
	}
	if len(r.Blocking()) > 0 {
		t.Errorf("a short run was refused: %v", r.Blocking())
	}
}
