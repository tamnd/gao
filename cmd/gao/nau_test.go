package main

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/nau"
)

func TestNauCheckIsTheOneThatCanFail(t *testing.T) {
	out, _, code := exec(t, "nau", "check")
	if code != 0 {
		t.Fatalf("gao cook check: exit %d, want 0\n%s", code, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("gao cook check said nothing, so a passing plan and a silent one look the same")
	}
}

func TestNauBudgetPrintsEveryComponentAndWhatItAddsUpTo(t *testing.T) {
	out, _, code := exec(t, "nau", "budget")
	if code != 0 {
		t.Fatalf("gao cook budget: exit %d, want 0", code)
	}
	for _, c := range nau.Budget() {
		if !strings.Contains(out, c.Name) {
			t.Errorf("gao cook budget did not print %s", c.Name)
		}
	}
	for _, want := range []string{"66% Vietnamese", "34% anchor", "309B"} {
		if !strings.Contains(out, want) {
			t.Errorf("gao cook budget did not print %q, which is the line the mixture is argued from", want)
		}
	}
}

// The distinct text number is the one another team works against, so the
// command has to print it rather than leave it to be added up off the table.
func TestNauBudgetDoesNotLeaveTheCorpusTargetToBeAddedUp(t *testing.T) {
	out, _, _ := exec(t, "nau", "budget")
	if strings.Contains(out, "379B") {
		t.Error("gao cook budget printed the sum of every unique count, which asks the crawl for the quality tiers twice")
	}
}

func TestNauCurriculumPrintsEveryPhaseAndWhatItReads(t *testing.T) {
	out, _, code := exec(t, "nau", "curriculum")
	if code != 0 {
		t.Fatalf("gao cook curriculum: exit %d, want 0", code)
	}
	for _, p := range nau.Curriculum() {
		if !strings.Contains(out, p.Name) {
			t.Errorf("gao cook curriculum did not print the %s phase", p.Name)
		}
		if !strings.Contains(out, p.Why) {
			t.Errorf("gao cook curriculum printed %s without the argument for it", p.Name)
		}
		for _, s := range p.Mix {
			if !strings.Contains(out, s.Component) {
				t.Errorf("gao cook curriculum did not print %s in the %s phase", s.Component, p.Name)
			}
		}
	}
}

func TestNauReconcilePrintsBothSidesAndTheDifference(t *testing.T) {
	out, _, code := exec(t, "nau", "reconcile")
	if code != 0 {
		t.Fatalf("gao cook reconcile: exit %d, want 0", code)
	}
	for _, want := range []string{"buys", "spends", "off", "epochs"} {
		if !strings.Contains(out, want) {
			t.Errorf("gao cook reconcile has no %q column", want)
		}
	}
	if !strings.Contains(out, "gao-web") {
		t.Error("gao cook reconcile did not print the component with the widest gap")
	}
}

func TestNauQuestionsPrintsWhatIsStillOpen(t *testing.T) {
	out, _, code := exec(t, "nau", "questions")
	if code != 0 {
		t.Fatalf("gao cook questions: exit %d, want 0", code)
	}
	for _, q := range nau.Questions() {
		if !strings.Contains(out, q.ID) {
			t.Errorf("gao cook questions did not print %s", q.ID)
		}
		if !strings.Contains(out, q.Ask) {
			t.Errorf("gao cook questions printed %s without what it asks", q.ID)
		}
	}
}

func TestNauArmsPrintsTheRecipeOnceForAllThree(t *testing.T) {
	out, _, code := exec(t, "nau", "arms")
	if code != 0 {
		t.Fatalf("gao cook arms: exit %d, want 0", code)
	}
	for _, a := range nau.Arms() {
		if !strings.Contains(out, a.ID) {
			t.Errorf("gao cook arms did not print %s", a.ID)
		}
	}
	r := nau.Matched()
	if strings.Count(out, r.LR) != 1 {
		t.Error("the learning rate schedule is printed once per arm or not at all, and it is one schedule shared by all three")
	}
	if !strings.Contains(out, r.Gate) {
		t.Error("gao cook arms did not print the gate, so a result has nothing to fail against")
	}
}

// Somebody reading the fleet inventory and the training plan on the same day
// will ask whether the run goes on the boxes we own. The answer has to be in
// the output rather than in a doc comment.
func TestNauFleetSaysTheRunDoesNotGoOnTheBoxesWeOwn(t *testing.T) {
	out, _, code := exec(t, "nau", "fleet")
	if code != 0 {
		t.Fatalf("gao cook fleet: exit %d, want 0", code)
	}
	if !strings.Contains(out, "gamingpc") {
		t.Error("gao cook fleet did not name the box with the only GPU on it")
	}
	if !strings.Contains(out, "times short") {
		t.Error("gao cook fleet did not print how far short the fleet is, which is the number that ends the argument")
	}
}

func TestNauWithoutASubcommandSaysWhatThereIs(t *testing.T) {
	_, errOut, code := exec(t, "nau")
	if code != 2 {
		t.Errorf("gao cook: exit %d, want 2", code)
	}
	for _, want := range []string{"budget", "curriculum", "reconcile", "questions", "arms", "fleet", "check"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("gao cook did not list %q", want)
		}
	}
}

func TestNauRefusesASubcommandItDoesNotHave(t *testing.T) {
	_, errOut, code := exec(t, "nau", "train")
	if code != 2 {
		t.Errorf("gao cook train: exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "train") {
		t.Error("gao cook did not say which subcommand it did not recognize")
	}
}
