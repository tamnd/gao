package main

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/may"
)

func TestBoxListsEveryMachineAndTheBudget(t *testing.T) {
	out, _, code := exec(t, "box")
	if code != 0 {
		t.Fatalf("gao box: exit %d, want 0", code)
	}
	for _, b := range may.Boxes {
		if !strings.Contains(out, b.Name) {
			t.Errorf("gao box did not mention %s", b.Name)
		}
	}
	if !strings.Contains(out, may.MeasuredOn) {
		t.Error("gao box did not print the measurement date, so a stale inventory reads as a current one")
	}
	// The conclusion is the point of the command, not the table.
	if !strings.Contains(out, "does not fit") {
		t.Error("gao box did not state that the corpus does not fit on one box")
	}
}

func TestBoxLabelPrintsOnlyTheLabel(t *testing.T) {
	t.Setenv(may.BoxEnv, "server3")
	out, _, code := exec(t, "box", "-label")
	if code != 0 {
		t.Fatalf("gao box -label: exit %d, want 0", code)
	}
	if got := strings.TrimSpace(out); got != "server3" {
		t.Errorf("gao box -label printed %q, want server3", got)
	}
	if strings.Contains(out, "\n\n") {
		t.Error("gao box -label printed more than the label, and it is meant to be usable in a shell substitution")
	}
}

func TestBoxTakesATokenCount(t *testing.T) {
	// A smaller corpus does fit, and the command has to say so rather than
	// printing the same conclusion whatever it is given.
	out, _, code := exec(t, "box", "-tokens", "10000000000")
	if code != 0 {
		t.Fatalf("gao box -tokens: exit %d, want 0", code)
	}
	if !strings.Contains(out, "fits on") {
		t.Errorf("a 10B token corpus should fit on the largest box, got:\n%s", out)
	}
}

func TestBoxPrintsWhatEachBoxCanRun(t *testing.T) {
	out, _, code := exec(t, "box")
	if code != 0 {
		t.Fatalf("gao box: exit %d, want 0", code)
	}
	if !strings.Contains(out, "no corpus bytes land here") {
		t.Errorf("gao box did not say that server2 holds no corpus bytes:\n%s", out)
	}
	// The fleet worker count is the number that sets how long a pass takes, so
	// it has to be in the output rather than derivable from it.
	if !strings.Contains(out, "workers") {
		t.Errorf("gao box did not print the worker column:\n%s", out)
	}
}

func TestBoxPrintsTheStoreOfRecord(t *testing.T) {
	t.Setenv(may.StoreEnv, "")
	out, _, code := exec(t, "box")
	if code != 0 {
		t.Fatalf("gao box: exit %d, want 0", code)
	}
	if !strings.Contains(out, "unset") {
		t.Errorf("gao box did not say the store of record is unset:\n%s", out)
	}

	t.Setenv(may.StoreEnv, "s3://gao-store")
	out, _, code = exec(t, "box")
	if code != 0 {
		t.Fatalf("gao box: exit %d, want 0", code)
	}
	if !strings.Contains(out, "s3://gao-store") {
		t.Errorf("gao box did not print the configured store:\n%s", out)
	}
}
