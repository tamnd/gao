package cham

import (
	"strings"
	"testing"
)

func TestTheRosterNamesSevenArmsAndSaysWhichAreBuilt(t *testing.T) {
	roster := Specialists()
	if len(roster) != 7 {
		t.Fatalf("roster holds %d specialists, want 7", len(roster))
	}

	seen := map[string]bool{}
	written := 0
	for _, s := range roster {
		if seen[s.Name] {
			t.Errorf("%q appears twice in the roster", s.Name)
		}
		seen[s.Name] = true
		if s.Task == "" || s.Checked == "" || s.Source == "" {
			t.Errorf("%q is on the roster without saying what it does, what it counts, or where the truth comes from", s.Name)
		}
		if s.Written {
			written++
		}
	}
	if written != 2 {
		t.Errorf("%d verifiers claim to be written, want 2", written)
	}
	for _, name := range []string{"dau", "trich"} {
		s, ok := Lookup(name)
		if !ok {
			t.Fatalf("%q is not on the roster", name)
		}
		if !s.Written {
			t.Errorf("%q is written and the roster says it is not", name)
		}
	}
}

func TestEveryVerifierInThisPackageIsOnTheRosterAsWritten(t *testing.T) {
	built := []Verifier{NewDau(), NewTrich(NewRegister())}

	for _, v := range built {
		s, ok := Lookup(v.Specialist())
		if !ok {
			t.Fatalf("%q has a verifier and no roster entry", v.Specialist())
		}
		if !s.Written {
			t.Errorf("%q has a verifier and the roster calls it unwritten", v.Specialist())
		}
	}
}

func TestLookupSaysNoRatherThanReturningAnEmptySpecialist(t *testing.T) {
	if _, ok := Lookup("khong-co"); ok {
		t.Fatal("a name nobody registered came back found")
	}
}

func TestARewardOutsideTheRangeIsABugAndIsClamped(t *testing.T) {
	if got := checked("x", 1.4, "over").Reward; got != 1 {
		t.Errorf("reward 1.4 came through as %v, want 1", got)
	}
	if got := checked("x", -0.2, "under").Reward; got != 0 {
		t.Errorf("reward -0.2 came through as %v, want 0", got)
	}
}

func TestAnAnswerTheVerifierCouldNotLookAtIsNotAZero(t *testing.T) {
	v := unchecked("x", "no key for this prompt")
	if v.Checked {
		t.Fatal("an unchecked verdict says it was checked")
	}
	if v.Reward != 0 {
		t.Fatalf("an unchecked verdict carries reward %v, and a caller reading it as a score would be reading a number nobody measured", v.Reward)
	}
	if !strings.Contains(v.String(), "not checked") {
		t.Errorf("the log line does not say the answer was not checked: %q", v.String())
	}
}

func TestARolloutStoppedAtTheLimitIsAMissingMeasurement(t *testing.T) {
	v := Overlong("trich")
	if v.Checked {
		t.Fatal("a truncated rollout came back as a graded one")
	}
	if v.Specialist != "trich" {
		t.Errorf("the verdict is attributed to %q", v.Specialist)
	}
}

func TestAVerdictReadsTheWayItIsLogged(t *testing.T) {
	v := checked("dau", 0.75, "%d of %d marks came back", 3, 4)
	got := v.String()
	if !strings.Contains(got, "0.750") || !strings.Contains(got, "3 of 4 marks came back") {
		t.Errorf("the log line is %q", got)
	}
}

func TestABlankAnswerIsBlankHoweverItIsSpaced(t *testing.T) {
	for _, s := range []string{"", " ", "\n", "\t \n "} {
		if !blank(s) {
			t.Errorf("%q is not counted as empty", s)
		}
	}
	if blank("có") {
		t.Error("a one syllable answer is counted as empty")
	}
}
