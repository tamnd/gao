package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/hieu"
)

func TestTheArchitectureIsPrintedWithItsArithmetic(t *testing.T) {
	out, errOut, code := exec(t, "hieu", "model")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"com-30B-A3B-base", "30.5B", "2.9B", "128 routed", "GFLOPs"} {
		if !strings.Contains(out, want) {
			t.Errorf("the reading does not mention %q:\n%s", want, out)
		}
	}
}

func TestTheModelIsAvailableAsJSON(t *testing.T) {
	out, errOut, code := exec(t, "hieu", "model", "-json")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got hieuModelReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Phases) != 3 {
		t.Fatalf("%d phases, want the three the curriculum has", len(got.Phases))
	}
	for i := 1; i < len(got.Phases); i++ {
		if got.Phases[i].AttentionShare <= got.Phases[i-1].AttentionShare {
			t.Errorf("attention did not grow with the sequence: %+v", got.Phases)
		}
	}
}

// The compute has to exist and be booked before this slice starts, and booking
// is done in accelerator hours rather than in adjectives.
func TestThePlanIsAPurchaseOrder(t *testing.T) {
	out, errOut, code := exec(t, "hieu", "plan")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"H100 SXM5", "accelerator hours", "40% utilization", "RTX 4090"} {
		if !strings.Contains(out, want) {
			t.Errorf("the reading does not mention %q:\n%s", want, out)
		}
	}
}

// Half the utilization is twice the invoice, which is the entire reason this
// number is a gate rather than a statistic.
func TestHalfTheUtilizationIsTwiceTheCompute(t *testing.T) {
	at := func(mfu string) hieuPlanReport {
		t.Helper()
		out, errOut, code := exec(t, "hieu", "plan", "-json", "-mfu", mfu)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		var got hieuPlanReport
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	full, half := at("0.40"), at("0.20")
	if math.Abs(half.Hours-2*full.Hours) > 1 {
		t.Errorf("%.0f hours at 20%% against %.0f at 40%%", half.Hours, full.Hours)
	}
	if half.Days <= full.Days {
		t.Error("the same hardware finished no later at half the utilization")
	}
}

// An A100 does not run FP8 slowly, and finding that out while planning is worth
// more than finding it out in week two.
func TestPlanningFP8OntoHardwareWithoutItFails(t *testing.T) {
	_, errOut, code := exec(t, "hieu", "plan", "-instance", "a100-sxm")
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errOut)
	}
	if !strings.Contains(errOut, "it does not run") {
		t.Errorf("the reason is too gentle: %s", errOut)
	}

	_, errOut, code = exec(t, "hieu", "plan", "-instance", "a100-sxm", "-precision", "bf16")
	if code != 0 {
		t.Fatalf("exit %d on a precision the hardware has: %s", code, errOut)
	}
}

func TestHardwareNobodyPricedIsAUsageError(t *testing.T) {
	_, errOut, code := exec(t, "hieu", "plan", "-instance", "tpu-v6")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
	if !strings.Contains(errOut, "h100-sxm") {
		t.Errorf("the error does not say what the choices are: %s", errOut)
	}
}

// stepLog writes a training log whose utilization follows the fractions given.
func stepLog(t *testing.T, at ...float64) string {
	t.Helper()
	m := hieu.Com()
	h, _ := hieu.Lookup("h100-sxm")
	peak, _ := h.Peak(hieu.FP8)

	var b strings.Builder
	for i, mfu := range at {
		tokens := int64(math.Round(mfu * peak * 64 / m.FLOPs(4096) * 30))
		fmt.Fprintf(&b, `{"step":%d,"tokens":%d,"seconds":30,"gpus":64,"instance":"h100-sxm","precision":"fp8","seq":4096}`+"\n", i+1, tokens)
	}
	path := filepath.Join(t.TempDir(), "steps.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func held(mfu float64, n int) []float64 {
	out := make([]float64, 0, n)
	for range n {
		out = append(out, mfu)
	}
	return out
}

func TestARunThatHoldsItsNumberReadsClean(t *testing.T) {
	out, errOut, code := exec(t, "hieu", "read", stepLog(t, held(0.44, 100)...))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"clears the 40% gate", "windows", "the number to distrust"} {
		if !strings.Contains(out, want) {
			t.Errorf("the reading does not mention %q:\n%s", want, out)
		}
	}
}

// The point of measuring continuously. This run averages above the kill line and
// is dying, and the exit code has to say so because a pipeline reads that and
// not the prose.
func TestARunThatIsDyingExitsNonZero(t *testing.T) {
	decline := make([]float64, 0, 100)
	for i := range 100 {
		decline = append(decline, 0.45-0.23*float64(i)/99)
	}
	out, _, code := exec(t, "hieu", "read", stepLog(t, decline...))
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "points from the first tenth") {
		t.Errorf("the decline is not reported:\n%s", out)
	}
	if strings.Contains(out, "clears") {
		t.Errorf("a dying run cleared the gate:\n%s", out)
	}
}

func TestAStepWithNoHardwareOnItFailsTheRun(t *testing.T) {
	path := stepLog(t, held(0.44, 10)...)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	lines[4] = strings.ReplaceAll(lines[4], `"instance":"h100-sxm",`, "")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "hieu", "read", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "not a number") {
		t.Errorf("the fault does not say why:\n%s", out)
	}
}

func TestTheRunIsAvailableAsJSON(t *testing.T) {
	out, errOut, code := exec(t, "hieu", "read", "-json", stepLog(t, held(0.44, 50)...))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got hieuReadReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Passes || got.Steps != 50 || len(got.Windows) != hieu.WindowCount {
		t.Errorf("a clean run read as %+v", got)
	}
	if got.Sustained > got.Mean+0.01 {
		t.Errorf("the sustained figure %.3f is above the mean %.3f", got.Sustained, got.Mean)
	}
}

func TestATrainingLogThatIsNotThereIsAFailure(t *testing.T) {
	_, errOut, code := exec(t, "hieu", "read", filepath.Join(t.TempDir(), "nothing.jsonl"))
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errOut)
	}
	if !strings.Contains(errOut, "gao efficiency:") {
		t.Errorf("the failure was not attributed: %s", errOut)
	}
}

func TestHieuWithoutASubcommandPrintsUsage(t *testing.T) {
	_, errOut, code := exec(t, "hieu")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
	if !strings.Contains(errOut, "usage: gao efficiency") {
		t.Errorf("no usage printed: %s", errOut)
	}
}

func TestAnUnknownHieuSubcommandSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "hieu", "mfu")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
	if !strings.Contains(errOut, "no subcommand named mfu") {
		t.Errorf("the unknown subcommand was not named: %s", errOut)
	}
}

func TestHieuHelpGoesToStdout(t *testing.T) {
	out, _, code := exec(t, "hieu", "help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "usage: gao efficiency") {
		t.Errorf("help did not go to stdout: %s", out)
	}
}

func TestHieuReadWantsExactlyOneLog(t *testing.T) {
	_, errOut, code := exec(t, "hieu", "read")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
}

// Compute this size is bought on capacity that gets taken back, so how often
// the run checkpoints is what decides how much of the invoice becomes gradient.
func TestTheCheckpointIntervalIsComputedRatherThanChosen(t *testing.T) {
	out, errOut, code := exec(t, "hieu", "spot")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"427 GB", "14 bytes a parameter", "square root of twice the write", "at risk", "retained"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not mention %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "the fleet holds exactly one") {
		t.Errorf("the retention budget against 467 GB is not stated:\n%s", out)
	}
}

// The regime worth detecting rather than tuning inside: a write that outlasts
// the capacity means the run never lands a checkpoint at all, and the formula
// still returns a number there.
func TestCapacityTakenBackFasterThanASaveIsNotAnIntervalProblem(t *testing.T) {
	out, _, code := exec(t, "hieu", "spot", "-mean", "90s")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "no interval fixes that") {
		t.Errorf("the verdict suggests tuning the interval:\n%s", out)
	}
}

// Weights are a seventh of a resumable checkpoint, and the two get confused
// constantly, so the retention budget has to say which one it counted.
func TestAPublishableCheckpointIsNotAResumableOne(t *testing.T) {
	out, _, code := exec(t, "hieu", "spot", "-weights")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "61 GB") || !strings.Contains(out, "the fleet holds 7 of them") {
		t.Errorf("the weights only retention budget is not reported:\n%s", out)
	}
}

func TestTheSpotPlanIsAlsoMachineReadable(t *testing.T) {
	out, _, code := exec(t, "hieu", "spot", "-json")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var report struct {
		Bytes    int     `json:"bytes_per_param"`
		Interval float64 `json:"interval_seconds"`
		Overhead float64 `json:"overhead"`
		Ceiling  float64 `json:"ceiling"`
		Survives bool    `json:"survives"`
		Keeps    int     `json:"keeps"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if report.Bytes != 14 || report.Keeps != 1 || !report.Survives {
		t.Errorf("the report came back %+v", report)
	}
	if report.Interval < 1500 || report.Interval > 2100 {
		t.Errorf("the interval is %.0f seconds", report.Interval)
	}
	if report.Overhead <= 0 || report.Overhead >= report.Ceiling {
		t.Errorf("the overhead is %.3f against a ceiling of %.3f", report.Overhead, report.Ceiling)
	}
}

func TestSpotRefusesNumbersItCannotComputeFrom(t *testing.T) {
	if _, _, code := exec(t, "hieu", "spot", "-rate", "0"); code != 2 {
		t.Error("a write rate of zero did not read as a usage error")
	}
	if _, _, code := exec(t, "hieu", "spot", "extra"); code != 2 {
		t.Error("an argument spot does not take was accepted")
	}
}
