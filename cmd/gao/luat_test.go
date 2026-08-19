package main

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/luat"
)

func TestLuatPrintsEveryQuestionWithSomethingToActOn(t *testing.T) {
	out, _, code := exec(t, "luat")
	if code != 0 {
		t.Fatalf("gao law: exit %d, want 0", code)
	}
	if !strings.Contains(out, luat.FiledOn) {
		t.Error("gao law did not print the filing date, so an old agenda reads as a current one")
	}
	for _, q := range luat.Questions() {
		if !strings.Contains(out, q.ID) {
			t.Errorf("gao law did not print %s", q.ID)
		}
		if !strings.Contains(out, q.Position()) {
			t.Errorf("gao law printed %s without the position gao acts on", q.ID)
		}
	}
}

func TestLuatPrintsEveryDeterminationAndWhatShips(t *testing.T) {
	out, _, code := exec(t, "luat")
	if code != 0 {
		t.Fatalf("gao law: exit %d, want 0", code)
	}
	for _, d := range luat.Determinations() {
		if !strings.Contains(out, d.Subject) {
			t.Errorf("gao law did not print the determination for %s", d.Subject)
		}
	}
	for _, p := range luat.Publications() {
		if !strings.Contains(out, p.Class.String()) {
			t.Errorf("gao law did not print what ships for %s", p.Class)
		}
	}
	// Both numbers, which is the whole rule about the headline.
	if !strings.Contains(out, "210B") || !strings.Contains(out, "300B") {
		t.Error("gao law printed one of the two token numbers rather than both")
	}
}

// The fallback is what somebody reads on the day the answer to Q1 is the wrong
// one, so it has to be in the default output rather than behind a flag.
func TestLuatPrintsTheFallbackWithoutBeingAsked(t *testing.T) {
	out, _, code := exec(t, "luat")
	if code != 0 {
		t.Fatalf("gao law: exit %d, want 0", code)
	}
	if !strings.Contains(out, luat.RecipeOnly.Then) {
		t.Error("gao law did not print the fallback")
	}
	for _, s := range luat.RecipeOnly.Publishes {
		if !strings.Contains(out, s) {
			t.Errorf("gao law did not print that the fallback still ships %q", s)
		}
	}
	if !strings.Contains(out, luat.RecipeOnly.Withholds) {
		t.Error("gao law did not print what the fallback withholds")
	}
}

func TestLuatVerboseAddsTheEvidenceAndTheStakes(t *testing.T) {
	plain, _, code := exec(t, "luat")
	if code != 0 {
		t.Fatalf("gao law: exit %d, want 0", code)
	}
	full, _, code := exec(t, "luat", "-v")
	if code != 0 {
		t.Fatalf("gao law -v: exit %d, want 0", code)
	}
	if len(full) <= len(plain) {
		t.Error("gao law -v printed no more than the plain listing")
	}
	for _, d := range luat.Determinations() {
		if !strings.Contains(full, d.Evidence) {
			t.Errorf("gao law -v did not print the evidence for %s", d.Subject)
		}
		if strings.Contains(plain, d.Evidence) {
			t.Errorf("gao law printed the evidence for %s without -v", d.Subject)
		}
	}
	q, _ := luat.Ask("Q5")
	if !strings.Contains(full, q.Stakes) {
		t.Error("gao law -v did not print what Q5 changes")
	}
}

func TestLuatPrintsOneQuestion(t *testing.T) {
	out, _, code := exec(t, "luat", "-q", "Q5")
	if code != 0 {
		t.Fatalf("gao law -q Q5: exit %d, want 0", code)
	}
	q, ok := luat.Ask("Q5")
	if !ok {
		t.Fatal("Q5 is not on the agenda")
	}
	if !strings.Contains(out, q.Ask) || !strings.Contains(out, q.Position()) {
		t.Errorf("gao law -q Q5 printed %q", out)
	}
	// One question means one question, not the agenda with the rest still on it.
	if strings.Contains(out, "Q1 ") {
		t.Error("gao law -q Q5 printed the whole agenda")
	}
}

// An id that does not exist is a typo in a script rather than a reason to print
// nothing and succeed, so it fails and says what the ids are.
func TestLuatRejectsAQuestionThatDoesNotExist(t *testing.T) {
	out, errOut, code := exec(t, "luat", "-q", "Q99")
	if code != 2 {
		t.Fatalf("gao law -q Q99: exit %d, want 2", code)
	}
	if out != "" {
		t.Errorf("gao law -q Q99 printed to stdout: %q", out)
	}
	if !strings.Contains(errOut, "Q5") {
		t.Error("the error does not list the questions that do exist")
	}
}

func TestLuatPrintsOnePath(t *testing.T) {
	out, _, code := exec(t, "luat", "-source", string(doc.SourceCrawl))
	if code != 0 {
		t.Fatalf("gao law -source: exit %d, want 0", code)
	}
	if !strings.Contains(out, doc.SourceCrawl.Describe()) {
		t.Error("gao law -source did not describe the path")
	}
	for _, d := range luat.For(doc.SourceCrawl) {
		if !strings.Contains(out, d.Subject) || !strings.Contains(out, d.Evidence) {
			t.Errorf("gao law -source did not print %s in full", d.Subject)
		}
	}
	// The crawl fetches statutes and forum threads down the same pipe, and the
	// command has to show both rather than the first one it finds.
	if !strings.Contains(out, "open") || !strings.Contains(out, "restricted") {
		t.Error("gao law -source printed one license class for the crawl")
	}
}

func TestLuatRejectsAPathThatDoesNotExist(t *testing.T) {
	_, errOut, code := exec(t, "luat", "-source", "gao-vibes")
	if code != 2 {
		t.Fatalf("gao law -source gao-vibes: exit %d, want 2", code)
	}
	if !strings.Contains(errOut, string(doc.SourceHPLT3)) {
		t.Error("the error does not list the paths that do exist")
	}
}

func TestLuatRejectsAnUnknownFlag(t *testing.T) {
	if _, _, code := exec(t, "luat", "-nope"); code != 2 {
		t.Errorf("gao law -nope: exit %d, want 2", code)
	}
}
