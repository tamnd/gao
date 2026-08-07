package tron

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// slice is one source of examples that passes everything except whatever the
// test under it changes.
func slice(source, capability, origin string, examples int64) Slice {
	s := Slice{
		Source:     source,
		Capability: capability,
		Origin:     origin,
		Examples:   examples,
		Turns:      examples * 3, // instruction data averages a few turns an example
		License:    doc.LicenseOpen,
	}
	if origin == Native {
		s.Audited = 400
		s.Passed = 392 // 98%, above the floor and not on it
	}
	return s
}

func aside(source, capability string, examples int64) Slice {
	s := slice(source, capability, Translated, examples)
	s.Held = true
	return s
}

// composed is a set that meets the slate, with a translated arm held aside whose
// capability mix follows the native mixture's. The numbers are invented and the
// proportions are not: they are the slate's shares times the target, split by
// origin at each capability's floor with room over it.
func composed() Set {
	s := Set{Name: "com-1.0-sft"}
	for _, m := range []struct {
		capability                  string
		native, translated, holdout int64
	}{
		{"hoi-dap", 148_000, 28_000, 39_000},
		{"viet", 138_000, 6_000, 36_000},
		{"doc-hieu", 98_000, 14_000, 26_000},
		{"tom-tat", 84_000, 12_000, 22_000},
		{"dau-cau", 80_000, 0, 0},
		{"ma-nguon", 30_000, 50_000, 8_000},
		{"phap-ly", 62_000, 2_000, 16_000},
		{"dich", 12_000, 36_000, 3_000},
	} {
		s.Slices = append(s.Slices, slice("nguoi-viet-"+m.capability, m.capability, Native, m.native))
		if m.translated > 0 {
			s.Slices = append(s.Slices, slice("dich-may-"+m.capability, m.capability, Translated, m.translated))
		}
		if m.holdout > 0 {
			s.Slices = append(s.Slices, aside("arm-"+m.capability, m.capability, m.holdout))
		}
	}
	return s
}

func refuses(t *testing.T, s Set, want string) {
	t.Helper()
	why := s.Blocking()
	if len(why) == 0 {
		t.Fatalf("the set was accepted and it should have been refused for %q", want)
	}
	for _, w := range why {
		if strings.Contains(w, want) {
			return
		}
	}
	t.Errorf("no refusal mentions %q, and what came back was:\n  %s", want, strings.Join(why, "\n  "))
}

func find(t *testing.T, s Set, capability string) Row {
	t.Helper()
	for _, r := range s.Composition() {
		if r.Capability == capability {
			return r
		}
	}
	t.Fatalf("%s is not on the slate", capability)
	return Row{}
}

func TestTheMixtureIsTheEightHundredThousandAndTheArmIsNotInIt(t *testing.T) {
	s := composed()
	if !s.Settled() {
		t.Fatalf("a set composed to the slate was refused: %v", s.Blocking())
	}
	if got := s.Examples(); got != Target {
		t.Errorf("the mixture holds %d against a target of %d", got, Target)
	}
	// The arm is composed, counted and trained on, and it is not in the pot.
	held := s.Aside()
	if len(held) == 0 {
		t.Fatal("nothing was held aside for the comparison")
	}
	var aside int64
	for _, sl := range held {
		aside += sl.Examples
	}
	if s.Examples() != s.Origin(Native).Examples+s.Origin(Translated).Examples+s.Origin(Made).Examples {
		t.Error("the three origins do not account for the mixture")
	}
	if aside == 0 || s.Origin(Translated).Examples == 0 {
		t.Error("either the arm or the in mixture translated data went missing")
	}
	if !s.Holds() {
		t.Errorf("a set with matched arms did not hold: %s", s.Verdict())
	}
}

func TestTheArmsAreTheSameSizeAndTheSameMix(t *testing.T) {
	s := composed()
	if got, want := s.ArmSize(), int64(150_000); got != want {
		t.Errorf("the arms run at %d and the smaller side holds %d", got, want)
	}
	if s.ArmSize() < MinArm {
		t.Errorf("the arms run at %d, under the %d that is a training run", s.ArmSize(), MinArm)
	}
	if d := s.Drift(); d > MaxDrift {
		t.Errorf("the arms differ by %.1f points on %s against a %.1f point line", d*100, s.Drifted(), MaxDrift*100)
	}
	if !s.Matched() {
		t.Error("two arms of the same size and mix were called unmatched")
	}
	v := s.Verdict()
	if !strings.Contains(v, "what a comparison of them measures is origin") {
		t.Errorf("the verdict does not say what the comparison is worth:\n  %s", v)
	}
}

func TestACapabilityOneSideHasNothingOfIsExcludedByName(t *testing.T) {
	s := composed()
	// Diacritic restoration comes out of the corpus with its answers known and
	// has no translated form, so it cannot be in a comparison of origins.
	if got := s.Excluded(); len(got) != 1 || got[0] != "dau-cau" {
		t.Errorf("the excluded capabilities are %v and dau-cau is the one with no translated side", got)
	}
	if len(s.Compared()) != len(Slate)-1 {
		t.Errorf("%d of %d capabilities are compared", len(s.Compared()), len(Slate))
	}
	// And it is still in the mixture, since being outside the comparison is not
	// the same thing as being outside the training set.
	if find(t, s, "dau-cau").Examples != 80_000 {
		t.Error("a capability excluded from the comparison fell out of the mixture")
	}
}

func TestAnArmTooSmallToTrainIsNotAComparison(t *testing.T) {
	s := composed()
	for i := range s.Slices {
		if s.Slices[i].Held {
			s.Slices[i].Examples /= 6
		}
	}
	if !s.Settled() {
		t.Fatalf("shrinking the arm broke the mixture: %v", s.Blocking())
	}
	if s.Matched() || s.Holds() {
		t.Fatalf("an arm of %d was called a comparison", s.ArmSize())
	}
	v := s.Verdict()
	if !strings.Contains(v, "how little each one read") {
		t.Errorf("the verdict does not say what a small arm measures:\n  %s", v)
	}
}

func TestArmsWithDifferentMixesMeasureTheMix(t *testing.T) {
	s := composed()
	// The arm is composed the way somebody would compose it if translated writing
	// data were the easiest thing to get, which it is.
	for i := range s.Slices {
		if !s.Slices[i].Held {
			continue
		}
		if s.Slices[i].Capability == "viet" {
			s.Slices[i].Examples = 90_000
		} else {
			s.Slices[i].Examples /= 2
		}
	}
	if s.Matched() {
		t.Fatal("arms with different capability mixes were called matched")
	}
	if s.Drifted() != "viet" {
		t.Errorf("the arms disagree most on %s and the writing share is what was moved", s.Drifted())
	}
	if v := s.Verdict(); !strings.Contains(v, "capability mix rather than of the origin") {
		t.Errorf("the verdict does not say what the mix costs:\n  %s", v)
	}
}

func TestACapabilityThatIsMostlyTranslatedCannotCarryTheClaim(t *testing.T) {
	s := composed()
	for i := range s.Slices {
		if s.Slices[i].Capability != "viet" || s.Slices[i].Held {
			continue
		}
		// The same 144,000 examples of writing, two thirds of them out of a
		// translator, which is what every published Vietnamese instruction set
		// looks like.
		switch s.Slices[i].Origin {
		case Native:
			s.Slices[i].Examples = 48_000
		case Translated:
			s.Slices[i].Examples = 96_000
		}
	}
	refuses(t, s, "a claim about the translator")
	if find(t, s, "viet").Holds {
		t.Error("a capability that is a third native was reported as holding")
	}
}

func TestANativeLabelNobodyReadIsNotAMeasurement(t *testing.T) {
	unread := composed()
	for i := range unread.Slices {
		if unread.Slices[i].Origin == Native {
			unread.Slices[i].Audited, unread.Slices[i].Passed = 0, 0
		}
	}
	refuses(t, unread, "on the word of whoever uploaded them")

	thin := composed()
	for i := range thin.Slices {
		if thin.Slices[i].Source == "nguoi-viet-viet" {
			thin.Slices[i].Audited, thin.Slices[i].Passed = 40, 40
		}
	}
	refuses(t, thin, "a native label nobody checked is a metadata field")

	failed := composed()
	for i := range failed.Slices {
		if failed.Slices[i].Source == "nguoi-viet-viet" {
			failed.Slices[i].Passed = 284 // 71% of 400
		}
	}
	refuses(t, failed, "is not established")
	// And the examples do not quietly become translated. They stop counting.
	for _, sl := range failed.Slices {
		if sl.Source == "nguoi-viet-viet" && sl.Proven() {
			t.Error("a slice whose audit failed is still proven native")
		}
	}
}

func TestASliceHeldAsideHasToBeTheThingWorthHoldingAside(t *testing.T) {
	s := composed()
	for i := range s.Slices {
		if s.Slices[i].Source == "nguoi-viet-viet" {
			s.Slices[i].Held = true
		}
	}
	refuses(t, s, "the only thing there is to hold aside")
}

func TestASliceThatDoesNotSayWhatWroteItIsRefused(t *testing.T) {
	s := composed()
	s.Slices[0].Origin = ""
	refuses(t, s, "an unstated origin is a translation more often than not")

	off := composed()
	off.Slices[0].Capability = "sang-tao"
	refuses(t, off, "which the slate does not name")

	turns := composed()
	turns.Slices[0].Turns = turns.Slices[0].Examples - 1
	refuses(t, turns, "an example is at least one turn")

	twice := composed()
	twice.Slices = append(twice.Slices, twice.Slices[0])
	refuses(t, twice, "appears twice")

	unlicensed := composed()
	unlicensed.Slices[0].License = doc.LicenseUnknown
	refuses(t, unlicensed, "rebuilt by anybody else is undecided")
}

func TestACapabilityTheSlateNamesAndTheSetDoesNotHoldIsAHole(t *testing.T) {
	s := composed()
	var kept []Slice
	for _, sl := range s.Slices {
		if sl.Capability != "phap-ly" {
			kept = append(kept, sl)
		}
	}
	s.Slices = kept
	refuses(t, s, "a hole rather than a shorter set")
	if find(t, s, "phap-ly").Examples != 0 {
		t.Error("a capability the set does not hold is not a row")
	}
}

func TestAMixtureThatFollowedItsLargestSourceIsCaught(t *testing.T) {
	s := composed()
	// Somebody finds a very large Vietnamese question answering dump and the
	// mixture follows it, which is how every mixture drifts.
	for i := range s.Slices {
		if s.Slices[i].Source == "nguoi-viet-hoi-dap" {
			s.Slices[i].Examples = 400_000
		}
	}
	refuses(t, s, "a mixture nobody chose")
	refuses(t, s, "past the")
}

func TestTheReproducibleShareIsStatedApartFromTheSize(t *testing.T) {
	s := composed()
	if got := s.Reproducible(); got != 1 {
		t.Errorf("a set of open slices is %.2f reproducible", got)
	}
	for i := range s.Slices {
		if s.Slices[i].Capability == "phap-ly" && !s.Slices[i].Held {
			s.Slices[i].License = doc.LicenseRestricted
		}
	}
	if got := s.Reproducible(); got >= 1 || got < 0.9 {
		t.Errorf("withholding the legal slices left the set %.2f reproducible", got)
	}
	if !s.Settled() {
		t.Errorf("a slice nobody else may redistribute blocked the set rather than being reported: %v", s.Blocking())
	}
}

func TestASetWithNothingInItSaysSoRatherThanReportingZero(t *testing.T) {
	var s Set
	refuses(t, s, "no slices were read")
	if s.Holds() || s.Matched() {
		t.Error("an empty set reached a verdict about the finetune")
	}
	if v := s.Verdict(); !strings.Contains(v, "no slices were read") {
		t.Errorf("the verdict of an empty set is %q", v)
	}
}

func TestReadingASetOffDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sft.jsonl")
	set := composed()
	lines := make([]string, 0, len(set.Slices))
	for _, sl := range set.Slices {
		held := ""
		if sl.Held {
			held = `,"held":true`
		}
		lines = append(lines, fmt.Sprintf(
			`{"source":%q,"capability":%q,"origin":%q,"examples":%d,"turns":%d,"audited":%d,"passed":%d%s,"license":"open"}`,
			sl.Source, sl.Capability, sl.Origin, sl.Examples, sl.Turns, sl.Audited, sl.Passed, held))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSet("com-1.0-sft", path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Settled() {
		t.Fatalf("the set that came off disk was refused: %v", s.Blocking())
	}
	if s.Examples() != Target || s.ArmSize() != 150_000 {
		t.Errorf("the set reads as %d examples with arms of %d", s.Examples(), s.ArmSize())
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"source":"a","rows":12}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSet("x", bad); err == nil {
		t.Error("a slice carrying an undeclared column was read")
	}
	if _, err := ReadSet("x", filepath.Join(dir, "nothing.jsonl")); err == nil {
		t.Error("a file that is not there read as a set")
	}
}
