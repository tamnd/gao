package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/thu"
)

// thuResults writes a full set of results for the fixed slate, with change
// applied to them first so a test can break exactly one thing.
func thuResults(t *testing.T, change func([]thu.Result) []thu.Result) string {
	t.Helper()
	s := thu.Fixed()
	digest := s.Digest()

	scores := map[string]float64{"B01": 0.612, "B02": 0.609, "B03": 0.613}
	moved := map[string]float64{
		"D05": -0.021, "Q01": -0.038, "V04": 0.017, "S01": -0.024,
		"N01": -0.031, "P01": -0.026, "R04": -0.019,
	}
	results := make([]thu.Result, 0, len(s.Runs))
	for i, r := range s.Runs {
		score, ok := scores[r.ID]
		if !ok {
			score = scores["B01"] + float64(i%5)*0.001 - 0.002 + moved[r.ID]
		}
		results = append(results, thu.Result{
			Slate: digest, Run: r.ID, Score: score, Box: "8x H100 SXM", GPUHours: 233,
		})
	}
	if change != nil {
		results = change(results)
	}

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
	return path
}

func TestTheSlatePrintsEveryRunItHolds(t *testing.T) {
	out, errOut, code := exec(t, "thu", "slate")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, id := range []string{"B01", "B02", "B03", "D01", "Q01", "V04", "F02"} {
		if !strings.Contains(out, id) {
			t.Errorf("%s is missing from the slate:\n%s", id, out)
		}
	}
	if !strings.Contains(out, thu.Fixed().Digest().String()) {
		t.Errorf("the slate does not print its digest:\n%s", out)
	}
}

func TestTheSlateSaysWhatItCostsBeforeItRuns(t *testing.T) {
	out, _, code := exec(t, "thu", "slate")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "GPU hours") || !strings.Contains(out, "quoted") {
		t.Errorf("the slate does not carry a price with a date on it:\n%s", out)
	}
}

func TestTheKnobsViewSaysWhatTheSlateIsFor(t *testing.T) {
	out, _, code := exec(t, "thu", "slate", "-knobs")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, knob := range []string{"dedup", "quality", "vocabulary", "synthetic", "epochs"} {
		if !strings.Contains(out, knob) {
			t.Errorf("%s is missing from the knobs:\n%s", knob, out)
		}
	}
	if strings.Contains(out, "B01") {
		t.Errorf("the knobs view lists individual runs:\n%s", out)
	}
}

func TestTheSlateSpeaksJSON(t *testing.T) {
	out, _, code := exec(t, "thu", "slate", "-json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var report struct {
		Slate  thu.Slate `json:"slate"`
		Digest string    `json:"digest"`
		Faults []string  `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the slate is not JSON: %v\n%s", err, out)
	}
	if len(report.Slate.Runs) != thu.Runs {
		t.Errorf("the JSON carries %d runs", len(report.Slate.Runs))
	}
	if len(report.Faults) != 0 {
		t.Errorf("the slate we are going to run was faulted: %v", report.Faults)
	}
}

func TestABrokenSlateFromAFileIsReported(t *testing.T) {
	s := thu.Fixed()
	s.Runs = s.Runs[:20]
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "slate.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := exec(t, "thu", "slate", "-slate", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "either it lost runs or it grew them") {
		t.Errorf("the report does not say what is wrong with a slate of 20:\n%s", out)
	}
}

func TestTheReportPublishesTheRunsThatFoundNothing(t *testing.T) {
	out, errOut, code := exec(t, "thu", "read", thuResults(t, nil))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "no effect") {
		t.Errorf("the report does not name the null results:\n%s", out)
	}
	if !strings.Contains(out, "worth more to the next person than another win") {
		t.Errorf("the report does not say why the nulls are in it:\n%s", out)
	}
}

func TestTheNoiseFloorIsPrintedWithWhatItWasMeasuredFrom(t *testing.T) {
	out, _, code := exec(t, "thu", "read", thuResults(t, nil))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "noise floor") || !strings.Contains(out, "0.004") {
		t.Errorf("the report does not print the measured floor:\n%s", out)
	}
}

func TestASlateMissingRunsIsRefused(t *testing.T) {
	path := thuResults(t, func(r []thu.Result) []thu.Result { return r[:33] })
	out, _, code := exec(t, "thu", "read", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "an advertisement rather than a comparison") {
		t.Errorf("the report does not refuse a slate with holes in it:\n%s", out)
	}
}

func TestAResultWithNoHardwareIsRefused(t *testing.T) {
	path := thuResults(t, func(rs []thu.Result) []thu.Result {
		for i, r := range rs {
			if r.Run == "E02" {
				rs[i].Box = ""
			}
		}
		return rs
	})
	out, _, code := exec(t, "thu", "read", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "nobody can price or reproduce") {
		t.Errorf("the report does not catch a result with no box:\n%s", out)
	}
}

func TestTheReportSpeaksJSONToo(t *testing.T) {
	out, _, code := exec(t, "thu", "read", "-json", thuResults(t, nil))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var report struct {
		Noise     float64 `json:"noise"`
		Baselines int     `json:"baselines"`
		Real      int     `json:"real"`
		Null      int     `json:"null"`
		Findings  []struct {
			Run    string  `json:"run"`
			Effect float64 `json:"effect"`
			Real   bool    `json:"real"`
		} `json:"findings"`
		Faults []string `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out)
	}
	if report.Baselines != thu.Repeats || report.Noise <= 0 {
		t.Errorf("the JSON does not carry a measured floor: %+v", report)
	}
	if len(report.Findings) != thu.Runs-thu.Repeats {
		t.Errorf("the JSON carries %d findings", len(report.Findings))
	}
	if report.Real == 0 || report.Null == 0 {
		t.Errorf("the JSON reports %d effects and %d nulls", report.Real, report.Null)
	}
	if len(report.Faults) != 0 {
		t.Errorf("an honest slate was faulted: %v", report.Faults)
	}
}

func TestNoResultsFileAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "thu", "read")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "the gap between two runs of the baseline") {
		t.Errorf("the usage does not say how an effect is decided: %s", errOut)
	}
}

func TestAResultsFileThatIsNotThereSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "thu", "read", filepath.Join(t.TempDir(), "nope.jsonl"))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao thu:") {
		t.Errorf("the error is not attributed: %s", errOut)
	}
}

func TestNoSubcommandAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "thu")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "forty runs") {
		t.Errorf("the usage does not say what the slate is: %s", errOut)
	}
}
