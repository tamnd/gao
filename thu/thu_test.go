package thu

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// honest builds a full set of results for the fixed slate: three baselines a
// little apart, most runs inside the noise, a handful outside it.
func honest(t *testing.T) (Slate, []Result) {
	t.Helper()
	s := Fixed()
	digest := s.Digest()

	// The baselines land within 0.004 of each other, which is the noise floor
	// every effect below is read against.
	scores := map[string]float64{"B01": 0.612, "B02": 0.609, "B03": 0.613}

	// A few runs that moved the number, and the rest that did not.
	moved := map[string]float64{
		"D05": -0.021, "Q01": -0.038, "V04": 0.017, "S01": -0.024,
		"Y01": -0.011, "E01": -0.014, "R04": -0.019, "N01": -0.031,
		"P01": -0.026, "Y04": -0.013,
	}
	for i, r := range s.Runs {
		if _, ok := scores[r.ID]; ok {
			continue
		}
		// Everything else sits inside the noise, in both directions, which is
		// what most of a real slate looks like.
		drift := float64(i%5)*0.001 - 0.002
		scores[r.ID] = scores["B01"] + drift + moved[r.ID]
	}

	results := make([]Result, 0, len(s.Runs))
	for _, r := range s.Runs {
		results = append(results, Result{
			Slate: digest, Run: r.ID, Score: scores[r.ID],
			Box: "8x H100 SXM", GPUHours: 233,
		})
	}
	return s, results
}

func faultAbout(t *testing.T, faults []string, want string) {
	t.Helper()
	for _, f := range faults {
		if strings.Contains(f, want) {
			return
		}
	}
	t.Errorf("no fault mentions %q, got:\n  %s", want, strings.Join(faults, "\n  "))
}

func noFaultAbout(t *testing.T, faults []string, want string) {
	t.Helper()
	for _, f := range faults {
		if strings.Contains(f, want) {
			t.Errorf("faulted for %q: %s", want, f)
		}
	}
}

func TestTheFixedSlateIsASlate(t *testing.T) {
	if faults := Fixed().Faults(); len(faults) > 0 {
		t.Errorf("the slate we are going to run is not one:\n  %s", strings.Join(faults, "\n  "))
	}
}

func TestTheSlateHoldsTheRunsItPromises(t *testing.T) {
	if got := len(Fixed().Runs); got != Runs {
		t.Errorf("the slate holds %d runs, want %d", got, Runs)
	}
}

func TestASlateThatLostARunIsRefused(t *testing.T) {
	// The failure this catches is not somebody deleting a run on purpose. It is
	// a slate that was forty when it was written and is thirty seven by the time
	// anybody adds it up.
	s := Fixed()
	s.Runs = s.Runs[:len(s.Runs)-3]
	faultAbout(t, s.Faults(), "either it lost runs or it grew them")
}

func TestARunThatVariesTwoThingsAnswersNeitherQuestion(t *testing.T) {
	s := Fixed()
	// Measure the vocabulary sweep against a run that changed the deduplication
	// threshold, which is the most tempting mistake on a slate this size because
	// it is the one that fits forty questions into twenty runs.
	for i, r := range s.Runs {
		if r.ID == "V02" {
			s.Runs[i].Against = "D01"
		}
	}
	faultAbout(t, s.Faults(), "two things at once and answers neither question")
}

func TestASweepMayBeReadAgainstItsOwnKnob(t *testing.T) {
	s := Fixed()
	for i, r := range s.Runs {
		if r.ID == "D02" {
			s.Runs[i].Against = "D01"
		}
	}
	noFaultAbout(t, s.Faults(), "two things at once")
}

func TestARunMeasuredAgainstNothingIsRefused(t *testing.T) {
	s := Fixed()
	for i, r := range s.Runs {
		if r.ID == "Q03" {
			s.Runs[i].Against = "Q99"
		}
	}
	faultAbout(t, s.Faults(), "which is not on the slate")
}

func TestTwoRunsOfTheSameRecipeAreOneRun(t *testing.T) {
	s := Fixed()
	for i, r := range s.Runs {
		if r.ID == "D03" {
			s.Runs[i].Value = "0.70" // the same as D01
		}
	}
	faultAbout(t, s.Faults(), "one run counted twice")
}

func TestASlateWithoutBaselineRepeatsHasNoNoiseFloor(t *testing.T) {
	s := Fixed()
	kept := s.Runs[:0]
	dropped := 0
	for _, r := range s.Runs {
		if r.Baseline() && dropped < 2 {
			dropped++
			continue
		}
		kept = append(kept, r)
	}
	s.Runs = kept
	faultAbout(t, s.Faults(), "an effect smaller than the gap between two runs of the same recipe is not an effect")
}

func TestTwoBaselinesAtTheSameSeedMeasureTheMachine(t *testing.T) {
	s := Fixed()
	for i, r := range s.Runs {
		if r.ID == "B02" {
			s.Runs[i].Seed = Seed
		}
	}
	faultAbout(t, s.Faults(), "measures the machine rather than the seed")
}

func TestARunWithNoQuestionIsRefused(t *testing.T) {
	s := Fixed()
	for i, r := range s.Runs {
		if r.ID == "E03" {
			s.Runs[i].Asks = ""
		}
	}
	faultAbout(t, s.Faults(), "the question gets written after the number comes back")
}

func TestAnUnpricedSlateIsNotOneAnybodyHasAgreedToRun(t *testing.T) {
	s := Fixed()
	s.Compute.USD = 0
	faultAbout(t, s.Faults(), "a slate nobody has costed is a slate nobody has agreed to run")
}

func TestThePriceHasToHaveADateOnIt(t *testing.T) {
	s := Fixed()
	s.Compute.Quoted = ""
	faultAbout(t, s.Faults(), "a price somebody remembers")
}

func TestTheFleetIsRefusedAsThePlaceThisRuns(t *testing.T) {
	// The fleet item on the milestone says this in words. Saying it in the
	// checker is what stops somebody writing gamingpc on the slate because every
	// other stage in the project runs there.
	s := Fixed()
	s.Compute.Instance = "gamingpc"
	faultAbout(t, s.Faults(), "does not fit on the one 24 GB card in the fleet")
}

func TestTheSlateDoesNotRunOnTheFleet(t *testing.T) {
	if faults := Fixed().Faults(); len(faults) > 0 {
		t.Fatalf("unexpected faults: %v", faults)
	}
	if Fixed().Compute.Instance == "" {
		t.Error("the slate does not say where it runs")
	}
}

func TestANoteDoesNotMoveTheSlateDigest(t *testing.T) {
	s := Fixed()
	before := s.Digest()
	s.Note = "rewritten after somebody read it out loud"
	for i := range s.Runs {
		s.Runs[i].Note = "a clearer sentence"
	}
	if s.Digest() != before {
		t.Error("writing a clearer sentence changed the identity of the experiment")
	}
}

func TestTheDigestMovesWithAnythingThatChangesTheComparison(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Slate)
	}{
		{"a run's value", func(s *Slate) { s.Runs[5].Value = "0.99" }},
		{"a run's knob", func(s *Slate) { s.Runs[5].Knob = "something else" }},
		{"a run's question", func(s *Slate) { s.Runs[5].Asks = "a different question" }},
		{"a run's reference", func(s *Slate) { s.Runs[5].Against = "B02" }},
		{"a run's seed", func(s *Slate) { s.Runs[5].Seed = 1 }},
		{"whether a run decides anything", func(s *Slate) { s.Runs[5].Decides = "" }},
		{"how much each run reads", func(s *Slate) { s.Tokens = 20_000_000_000 }},
		{"the model size", func(s *Slate) { s.Model = 700_000_000 }},
		{"the proxy", func(s *Slate) { s.Proxy = "vmlu" }},
		{"the price", func(s *Slate) { s.Compute.USD = 1 }},
		{"where it runs", func(s *Slate) { s.Compute.Instance = "8x A100" }},
		{"dropping a run", func(s *Slate) { s.Runs = s.Runs[:len(s.Runs)-1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Fixed()
			before := s.Digest()
			tc.change(&s)
			if s.Digest() == before {
				t.Errorf("changing %s left the slate with the same identity", tc.name)
			}
		})
	}
}

func TestTheDigestDoesNotDependOnTheOrderTheRunsAreWrittenIn(t *testing.T) {
	s := Fixed()
	before := s.Digest()
	slices := append([]Run(nil), s.Runs...)
	for i, j := 0, len(slices)-1; i < j; i, j = i+1, j-1 {
		slices[i], slices[j] = slices[j], slices[i]
	}
	s.Runs = slices
	if s.Digest() != before {
		t.Error("reordering the slate changed what it hashes to")
	}
}

func TestAnHonestSlateReadsClean(t *testing.T) {
	s, results := honest(t)
	rep := s.Read(results)
	if problems := rep.Publishable(); len(problems) > 0 {
		t.Errorf("an honest slate was refused:\n  %s", strings.Join(problems, "\n  "))
	}
	if rep.Real == 0 || rep.Null == 0 {
		t.Errorf("a slate of %d runs found %d effects and %d nulls", len(s.Runs), rep.Real, rep.Null)
	}
}

func TestTheNoiseFloorIsMeasuredRatherThanPicked(t *testing.T) {
	s, results := honest(t)
	rep := s.Read(results)
	if math.Abs(rep.Noise-0.004) > 1e-9 {
		t.Errorf("the noise floor is %v, want the spread between the baseline runs", rep.Noise)
	}
	if rep.Baselines != Repeats {
		t.Errorf("the report measured the floor from %d baseline runs", rep.Baselines)
	}
}

func TestAnEffectInsideTheNoiseIsNotAnEffect(t *testing.T) {
	s, results := honest(t)
	// Put one run three thousandths above its reference, which is inside a floor
	// of four thousandths.
	for i, r := range results {
		if r.Run == "R01" {
			results[i].Score = results[0].Score + 0.003
		}
	}
	rep := s.Read(results)
	for _, f := range rep.Findings {
		if f.Run == "R01" && f.Real {
			t.Errorf("an effect of %v cleared a floor of %v", f.Effect, rep.Noise)
		}
	}
}

func TestASlatePublishedWithTheRunsThatFinishedIsRefused(t *testing.T) {
	// The one that matters. Nothing in a published table of thirty one runs shows
	// that it was a slate of forty.
	s, results := honest(t)
	rep := s.Read(results[:31])
	faultAbout(t, rep.Publishable(), "an advertisement rather than a comparison")
}

func TestASlateWhereEveryRunWonIsRefused(t *testing.T) {
	s, results := honest(t)
	for i := range results {
		if results[i].Run[0] != 'B' {
			results[i].Score += 0.4
		}
	}
	rep := s.Read(results)
	faultAbout(t, rep.Publishable(), "has had its null results taken out")
}

func TestOneBaselineRunLeavesEveryEffectWithoutAnErrorBar(t *testing.T) {
	s, results := honest(t)
	kept := results[:0]
	for _, r := range results {
		if r.Run == "B02" || r.Run == "B03" {
			continue
		}
		kept = append(kept, r)
	}
	rep := s.Read(kept)
	faultAbout(t, rep.Publishable(), "a number with no error bar under it")
}

func TestIdenticalBaselineScoresAreNotWhatSeedsDo(t *testing.T) {
	s, results := honest(t)
	for i, r := range results {
		if r.Run == "B02" || r.Run == "B03" {
			results[i].Score = results[0].Score
		}
	}
	rep := s.Read(results)
	faultAbout(t, rep.Publishable(), "not what different seeds do")
}

func TestAResultWithoutHardwareUnderItIsRefused(t *testing.T) {
	s, results := honest(t)
	for i, r := range results {
		if r.Run == "V03" {
			results[i].Box = ""
		}
	}
	rep := s.Read(results)
	faultAbout(t, rep.Publishable(), "nobody can price or reproduce")
}

func TestARunAddedAfterTheSlateWasClosedIsCaught(t *testing.T) {
	s, results := honest(t)
	results = append(results, Result{Slate: s.Digest(), Run: "Z01", Score: 0.7, Box: "8x H100 SXM"})
	rep := s.Read(results)
	faultAbout(t, rep.Publishable(), "a run added after the slate was closed")
}

func TestAResultFromADifferentSlateIsCaught(t *testing.T) {
	// A prompt or a threshold quietly improved after the runs started produces
	// exactly this, and nothing else in the pipeline would notice.
	s, results := honest(t)
	for i, r := range results {
		if r.Run == "E02" {
			results[i].Slate = doc.SumString("a slate somebody edited")
		}
	}
	rep := s.Read(results)
	faultAbout(t, rep.Publishable(), "something on the slate moved after the runs started")
}

func TestAnOverrunIsHeardFromTheReportRatherThanTheInvoice(t *testing.T) {
	s, results := honest(t)
	for i := range results {
		results[i].GPUHours = 900
	}
	rep := s.Read(results)
	faultAbout(t, rep.Publishable(), "rather than from the invoice")
}

func TestTheFindingsAreOrderedByHowMuchTheyMoved(t *testing.T) {
	s, results := honest(t)
	rep := s.Read(results)
	for i := 1; i < len(rep.Findings); i++ {
		if math.Abs(rep.Findings[i-1].Effect) < math.Abs(rep.Findings[i].Effect) {
			t.Fatalf("%s moved less than %s and is printed first", rep.Findings[i-1].Run, rep.Findings[i].Run)
		}
	}
}

func TestAKnobThatMadeThingsWorseKeepsItsSign(t *testing.T) {
	// Rounding a regression toward zero is how a table starts flattering itself.
	s, results := honest(t)
	rep := s.Read(results)
	for _, f := range rep.Findings {
		if f.Run == "Q01" && f.Effect >= 0 {
			t.Errorf("turning the quality classifier off scored %v against the baseline", f.Effect)
		}
	}
}

func TestEveryDecisiveRunIsReportedAsSettlingSomething(t *testing.T) {
	s, results := honest(t)
	rep := s.Read(results)
	if len(rep.Settled()) != s.Decisive() {
		t.Errorf("the slate has %d decisive runs and the report settles %d", s.Decisive(), len(rep.Settled()))
	}
}

func TestTheSlateSweepsMoreThanOneKindOfThing(t *testing.T) {
	// A slate of forty runs over three knobs is a sweep wearing a slate's name.
	if got := len(Fixed().Knobs()); got < 10 {
		t.Errorf("the slate varies %d things across %d runs", got, Runs)
	}
}

func TestResultsAreReadFromWhatTheRunsAppend(t *testing.T) {
	s, results := honest(t)
	lines := make([]string, 0, len(results))
	for _, r := range results {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	path := filepath.Join(t.TempDir(), "results.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(results) {
		t.Fatalf("read %d results, wrote %d", len(got), len(results))
	}
	if problems := s.Read(got).Publishable(); len(problems) > 0 {
		t.Errorf("a round trip through the file changed the verdict:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestAResultsFileWithAFieldNobodyDeclaredIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.jsonl")
	if err := os.WriteFile(path, []byte(`{"run":"B01","score":0.6,"vmlu":0.4}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadResults(path); err == nil {
		t.Error("a result carrying a score nobody declared was accepted")
	}
}

func TestAnEmptyResultsFileSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadResults(path); err == nil {
		t.Error("an empty file was read as a set of results")
	}
}

func TestASlateIsReadFromJSON(t *testing.T) {
	s := Fixed()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "slate.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSlate(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest() != s.Digest() {
		t.Error("a slate through JSON is a different slate")
	}
}
