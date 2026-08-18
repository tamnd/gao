package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// diskTrace writes what a watcher appends to while a run goes: one reading every
// interval seconds, holding hold bytes, with one spike partway through.
func diskTrace(t *testing.T, ran, every, hold, spike int64) string {
	t.Helper()
	var lines []string
	for s := int64(0); s <= ran; s += every {
		b, stage := hold, "download"
		if s > ran/2 {
			stage = "push"
		}
		if s == ran/2+ran/2%every {
			b, stage = spike, "push"
		}
		lines = append(lines, fmt.Sprintf(
			`{"second":%d,"bytes":%d,"box":"server1","stage":%q,"workers":4}`, s, b, stage))
	}
	path := filepath.Join(t.TempDir(), "disk.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPeakDiskIsMeasuredRatherThanTrustedFromTheArithmetic(t *testing.T) {
	out, errOut, code := exec(t, "box", "peak", "-run", "hplt-v3", "-ran", "6h",
		diskTrace(t, 21600, 20, 3_000_000_000, 11_200_000_000))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"11.2 GB", "90.0 GB", "4.1 GB", "drift", "widest gap 20s"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is missing from the reading:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "the design predicts") {
		t.Errorf("the verdict does not carry the arithmetic it was read against:\n%s", out)
	}
}

// Two is a gate that failed, which is what the rest of gao exits on a
// measurement that came in over its limit.
func TestARunOverTheCeilingExitsTwo(t *testing.T) {
	out, _, code := exec(t, "box", "peak", "-ran", "6h",
		diskTrace(t, 21600, 20, 80_000_000_000, 104_000_000_000))
	if code != 2 {
		t.Fatalf("exit %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, "does not fit on the box it was planned for") {
		t.Errorf("the reading does not say what going over costs:\n%s", out)
	}
}

// One is a trace that cannot answer the question, which is a different problem
// with a different fix, and the two shared a code until a real run made the
// difference visible.
func TestATraceThatCannotSupportAPeakExitsOne(t *testing.T) {
	out, _, code := exec(t, "box", "peak", "-ran", "6h",
		diskTrace(t, 21600, 300, 3_000_000_000, 11_200_000_000))
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "the trace cannot answer this") {
		t.Errorf("the reading does not separate a refusal from a fault:\n%s", out)
	}
}

// A peak sampled every five minutes is not a peak, and the reading says so
// rather than printing the largest number it happened to see.
func TestAPeakTakenTooRarelyIsRefused(t *testing.T) {
	out, _, code := exec(t, "box", "peak", "-ran", "6h",
		diskTrace(t, 21600, 300, 3_000_000_000, 11_200_000_000))
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "the disk at some moments rather than its peak") {
		t.Errorf("a trace sampled every five minutes was accepted:\n%s", out)
	}
}

func TestThePeakIsAlsoMachineReadable(t *testing.T) {
	out, _, code := exec(t, "box", "peak", "-json", "-ran", "6h",
		diskTrace(t, 21600, 20, 3_000_000_000, 11_200_000_000))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var got struct {
		Box       string `json:"box"`
		Held      int64  `json:"held"`
		Predicted int64  `json:"predicted"`
		Ceiling   int64  `json:"ceiling"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got.Box != "server1" || got.Held != 11_200_000_000 || got.Ceiling != may.Ceiling {
		t.Errorf("%+v", got)
	}
	if got.Predicted != may.PeakBytes(mustBox(t, "server1")) {
		t.Errorf("the prediction came back as %d", got.Predicted)
	}
}

func mustBox(t *testing.T, name string) may.Box {
	t.Helper()
	b, ok := may.Lookup(name)
	if !ok {
		t.Fatalf("%s is not on the fleet", name)
	}
	return b
}

func TestPeakRefusesWhatItCannotRead(t *testing.T) {
	if _, _, code := exec(t, "box", "peak"); code != 2 {
		t.Errorf("a peak with no trace exited %d, want 2", code)
	}
	if _, _, code := exec(t, "box", "peak", "a.jsonl", "b.jsonl"); code != 2 {
		t.Errorf("two traces exited %d, want 2", code)
	}
	if _, _, code := exec(t, "box", "peak", filepath.Join(t.TempDir(), "gone.jsonl")); code != 1 {
		t.Errorf("a missing trace exited %d, want 1", code)
	}
}

// The check has to work off a real filesystem, since drift is a fact about one.
// What it says depends on where it runs, so the test asserts the shape of the
// reading and the one thing that is true everywhere: the numbers are the box's
// own rather than the record's.
func TestBoxCheckMeasuresThisMachine(t *testing.T) {
	out, errOut, code := exec(t, "box", "check", "-dir", t.TempDir(), "-json")
	if code != 0 && code != 1 {
		t.Fatalf("gao box check: exit %d, %s", code, errOut)
	}

	var c struct {
		Box      string   `json:"box"`
		Path     string   `json:"path"`
		Free     int64    `json:"free"`
		Recorded int64    `json:"recorded"`
		Threads  int      `json:"threads"`
		Taken    string   `json:"inventory_taken"`
		Drift    []string `json:"drift"`
		Holds    bool     `json:"holds"`
		Verdict  string   `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("the reading is not JSON: %v\n%s", err, out)
	}
	if c.Free <= 0 || c.Threads <= 0 {
		t.Errorf("measured %d bytes free and %d threads on a directory that exists", c.Free, c.Threads)
	}
	if c.Taken != may.MeasuredOn {
		t.Errorf("the reading is against an inventory taken on %q, want %q", c.Taken, may.MeasuredOn)
	}
	if c.Holds != (len(c.Drift) == 0) {
		t.Errorf("it holds %v with %d sentences of drift", c.Holds, len(c.Drift))
	}
	if c.Verdict == "" {
		t.Error("no verdict")
	}
}

// A run that has drifted exits 1, the way every other reading in gao that
// cannot be trusted does. CI runs on machines that are not on the fleet, which
// is itself a drift, so this asserts the pairing rather than the exit code
// alone.
func TestBoxCheckExitsOnDrift(t *testing.T) {
	out, _, code := exec(t, "box", "check", "-dir", t.TempDir())
	drifted := strings.Contains(out, "the record has moved")
	if drifted && code != 1 {
		t.Errorf("exit %d after reporting drift, want 1", code)
	}
	if !drifted && code != 0 {
		t.Errorf("exit %d with nothing to report, want 0", code)
	}
}

func TestBoxCheckTakesNoArguments(t *testing.T) {
	if _, _, code := exec(t, "box", "check", "disk.jsonl"); code != 2 {
		t.Errorf("exit %d, want 2 for an argument the command does not take", code)
	}
}
