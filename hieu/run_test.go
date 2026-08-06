package hieu

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// steps builds a log whose utilization follows the fractions given, one step
// each, on hardware that exists.
func steps(at ...float64) []Step {
	h, _ := Lookup("h100-sxm")
	peak, _ := h.Peak(FP8)
	m := Com()

	out := make([]Step, 0, len(at))
	for i, mfu := range at {
		rate := mfu * peak * 64 / m.FLOPs(4096)
		out = append(out, Step{
			Step: i + 1, Tokens: int64(math.Round(rate * 30)), Seconds: 30,
			GPUs: 64, Instance: "h100-sxm", Precision: FP8, Seq: 4096,
		})
	}
	return out
}

// flat repeats one utilization n times, which is a run that is behaving.
func flat(mfu float64, n int) []Step {
	at := make([]float64, 0, n)
	for range n {
		at = append(at, mfu)
	}
	return steps(at...)
}

// ramp walks from one utilization to another over n steps, which is what a run
// that is quietly degrading looks like from the inside.
func ramp(from, to float64, n int) []Step {
	at := make([]float64, 0, n)
	for i := range n {
		at = append(at, from+(to-from)*float64(i)/float64(n-1))
	}
	return steps(at...)
}

func TestARunThatHoldsItsNumberPasses(t *testing.T) {
	r := Read(Com(), flat(0.44, 200))
	if !r.Sound() {
		t.Fatalf("faults in a clean log: %v", r.Faults)
	}
	if got := r.Mean(); math.Abs(got-0.44) > 0.005 {
		t.Errorf("the run averaged %.3f", got)
	}
	if !r.Passes() {
		t.Errorf("a run at 44%% throughout did not pass: %s", r.Verdict())
	}
	if math.Abs(r.Drift(WindowCount)) > 0.01 {
		t.Errorf("a flat run drifted by %.3f", r.Drift(WindowCount))
	}
}

// This is the whole reason the checklist says continuously rather than once. A
// run that starts at 45% and ends at 22% averages above the kill line and is a
// run that is dying.
func TestARunThatIsDyingDoesNotPassOnItsAverage(t *testing.T) {
	r := Read(Com(), ramp(0.45, 0.22, 200))
	if got := r.Mean(); got < Kill {
		t.Fatalf("the mean is %.3f, which already fails and makes this test prove nothing", got)
	}
	if r.Passes() {
		t.Errorf("a run that halved did not fail: %s", r.Verdict())
	}
	if got := r.Sustained(WindowCount); got >= Kill {
		t.Errorf("the worst tenth of the run read as %.3f", got)
	}
	if got := r.Drift(WindowCount); got > -0.15 {
		t.Errorf("a 23 point decline came back as %.3f", got)
	}
	if !strings.Contains(r.Verdict(), "moved -22 points") && !strings.Contains(r.Verdict(), "moved -21 points") {
		t.Errorf("the decline is not in the verdict: %s", r.Verdict())
	}
}

// One step that waited on a checkpoint moves a mean and does not move a run,
// which is the other half of not judging on one number.
func TestOneBadStepIsNotABadRun(t *testing.T) {
	at := flat(0.44, 200)
	at[100].Seconds = 600
	r := Read(Com(), at)
	if !r.Passes() {
		t.Errorf("one slow step failed the run: %s", r.Verdict())
	}
	if r.Median() <= r.Mean() {
		t.Errorf("the mean %.3f was not dragged below the median %.3f by a 20x step", r.Mean(), r.Median())
	}
}

// The milestone asks for this in as many words, and it is the fault that reads
// like a number rather than like a gap.
func TestAStepThatDoesNotSayWhatItRanOnIsAFault(t *testing.T) {
	at := flat(0.44, 20)
	at[7].Instance = ""
	r := Read(Com(), at)
	if r.Sound() {
		t.Fatal("a step with no hardware on it was read as a utilization figure")
	}
	if !strings.Contains(r.Faults[0], "not a number") {
		t.Errorf("the fault does not say why: %s", r.Faults[0])
	}
	if r.Passes() {
		t.Error("the run passed with an unreadable step in it")
	}
	if len(r.MFUs) != 20 {
		t.Errorf("%d steps came back out of 20", len(r.MFUs))
	}
}

// A job that restarted onto different hardware after a preemption and carried on
// reporting against the old peak is not a hypothetical, it is what spot
// instances do.
func TestARunThatMovedIsNotOneNumber(t *testing.T) {
	at := flat(0.44, 40)
	for i := 20; i < 40; i++ {
		at[i].Instance = "b200"
	}
	r := Read(Com(), at)
	if r.Sound() {
		t.Fatal("a run that moved between two kinds of accelerator was folded into one figure")
	}
	if !strings.Contains(strings.Join(r.Faults, " "), "two different machines") {
		t.Errorf("the fault does not say what happened: %v", r.Faults)
	}
}

func TestHardwareNobodyPricedIsAFaultRatherThanAGuess(t *testing.T) {
	at := flat(0.44, 10)
	at[3].Instance = "mi300x"
	r := Read(Com(), at)
	if r.Sound() {
		t.Fatal("a step on unpriced hardware was accepted")
	}
	if !strings.Contains(r.Faults[0], "no peak to divide by") {
		t.Errorf("the fault does not say what is missing: %s", r.Faults[0])
	}
}

// Windows are what continuously means in practice, and they have to cut the run
// up rather than sample it.
func TestTheWindowsCoverTheWholeRun(t *testing.T) {
	r := Read(Com(), ramp(0.20, 0.60, 100))
	w := r.Windows(WindowCount)
	if len(w) != WindowCount {
		t.Fatalf("%d windows, want %d", len(w), WindowCount)
	}
	for i := 1; i < len(w); i++ {
		if w[i] <= w[i-1] {
			t.Errorf("window %d at %.3f is not above window %d at %.3f", i, w[i], i-1, w[i-1])
		}
	}
	if got := r.Sustained(WindowCount); got != w[0] {
		t.Errorf("the sustained figure is %.3f and the lowest window is %.3f", got, w[0])
	}

	// More windows than steps is a shorter run rather than an error.
	short := Read(Com(), flat(0.44, 3))
	if got := len(short.Windows(WindowCount)); got != 3 {
		t.Errorf("%d windows over 3 steps", got)
	}
	if Read(Com(), nil).Windows(WindowCount) != nil {
		t.Error("an empty run has windows in it")
	}
}

func TestARunThatHasNotStartedIsNotARunThatFailed(t *testing.T) {
	r := Read(Com(), nil)
	if !r.Sound() {
		t.Fatalf("an empty log reported faults: %v", r.Faults)
	}
	if !strings.Contains(r.Verdict(), "has not started") {
		t.Errorf("an empty log reads as a problem: %s", r.Verdict())
	}
	if r.Passes() {
		t.Error("a run with no steps passed the gate")
	}
	if r.Tokens() != 0 || r.Seconds() != 0 || r.Mean() != 0 {
		t.Error("an empty run got through some tokens")
	}
}

func TestALogIsReadFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steps.jsonl")
	var b strings.Builder
	for _, s := range flat(0.44, 12) {
		line, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 12 {
		t.Fatalf("%d steps read, want 12", len(got))
	}
	if !Read(Com(), got).Passes() {
		t.Error("a clean run did not survive the round trip")
	}
}

// A field this reader does not know about means the trainer and the reader have
// drifted apart, which is worth finding out before a month of GPU time is behind
// it.
func TestALogThisReaderDoesNotUnderstandIsRefused(t *testing.T) {
	for _, tt := range []struct{ name, line string }{
		{"a field nobody here knows", `{"step":1,"tokens":1,"seconds":1,"gpus":8,"instance":"h100-sxm","precision":"fp8","seq":4096,"grad_norm":1.2}`},
		{"a step with no tokens in it", `{"step":1,"seconds":30,"gpus":8,"instance":"h100-sxm","precision":"fp8","seq":4096}`},
		{"a line that is not JSON", `step 1 mfu 0.44`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "steps.jsonl")
			if err := os.WriteFile(path, []byte(tt.line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadLog(path); err == nil {
				t.Error("it was read anyway")
			}
		})
	}
	if _, err := ReadLog(filepath.Join(t.TempDir(), "nothing.jsonl")); err == nil {
		t.Error("a log that is not there was read")
	}
}

func TestTheRunReportsWhatItGotThrough(t *testing.T) {
	r := Read(Com(), flat(0.44, 100))
	if r.Seconds() != 3000 {
		t.Errorf("the run took %.0f seconds of step time", r.Seconds())
	}
	if got := float64(r.Tokens()) / 1e9; got < 1 {
		t.Errorf("100 steps on 64 H100s got through %.2fB tokens", got)
	}
	if got := r.Quantile(0); got <= 0 {
		t.Errorf("the worst step read as %.3f", got)
	}
}
