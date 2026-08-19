package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/cook"
	"github.com/tamnd/gao/weigh"
)

// weighRun is one arm the way one is meant to come back: the locked recipe, one
// checkpoint, one harness, one seed, and a curve that goes down.
func weighRun(arm, data string, vmlu float64) weigh.Run {
	r := weigh.Run{
		Arm:        arm,
		Data:       data,
		Base:       "b3e0f1a9c2d4",
		Tokenizer:  "gemma-3",
		Tokens:     cook.Matched().Tokens,
		Batch:      cook.Matched().Batch,
		Sequence:   8192,
		PeakLR:     3e-5,
		Warmup:     0.01,
		Decay:      0.2,
		Precision:  "bf16",
		Seed:       17,
		Instance:   "8xH100-80GB",
		EvalBox:    "gamingpc",
		Harness:    "9d41c0b7ae52f6",
		Scores:     map[string]float64{weigh.Metric: vmlu, "vi-adherence": 0.86},
		BaseScores: map[string]float64{weigh.Metric: 44.1, "vi-adherence": 0.91},
	}
	loss := 2.31
	for step := range 40 {
		loss -= 0.004
		r.Curve = append(r.Curve, weigh.Point{Step: step * 500, Tokens: int64(step) * 5_000_000_000, Loss: loss})
	}
	return r
}

// weighThree returns the three locked arms with the scores given, in the order nau
// locks them.
func weighThree(gao, culturax, filtered float64) []weigh.Run {
	arms := cook.Arms()
	return []weigh.Run{
		weighRun(arms[0].ID, arms[0].Data, gao),
		weighRun(arms[1].ID, arms[1].Data, culturax),
		weighRun(arms[2].ID, arms[2].Data, filtered),
	}
}

func weighArms(t *testing.T, runs []weigh.Run) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "arms.jsonl")
	var b strings.Builder
	for _, r := range runs {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWeighReportsTheGateOnThreeMatchedArms(t *testing.T) {
	out, errOut, code := exec(t, "weigh", weighArms(t, weighThree(52.4, 46.9, 48.2)))
	if code != 0 {
		t.Fatalf("a matched comparison exited %d: %s\n%s", code, errOut, out)
	}
	for _, want := range []string{
		"E6, gao over CulturaX",
		"E7, gao over its own base",
		"P08-3, the cleaning's share",
		"com-8B-cpt-gao",
		"E6 and E7 both pass",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "8xH100-80GB") || !strings.Contains(out, "gamingpc") {
		t.Errorf("the arm table does not say what each number came off:\n%s", out)
	}
}

func TestWeighReportsAdherenceWithoutGatingOnIt(t *testing.T) {
	out, _, code := exec(t, "weigh", weighArms(t, weighThree(52.4, 46.9, 48.2)))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "P10-2, vi-adherence on the gao arm") {
		t.Errorf("the adherence reading is not printed:\n%s", out)
	}
	if !strings.Contains(out, "86.0% against the 90.0%") {
		t.Errorf("the adherence reading does not print against the line it was predicted under:\n%s", out)
	}
}

func TestWeighExitsTwoWhenTheGapIsUnderE6(t *testing.T) {
	out, _, code := exec(t, "weigh", weighArms(t, weighThree(48.9, 46.9, 48.4)))
	if code != 2 {
		t.Fatalf("a gap of two points exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "the from scratch run does not start") {
		t.Errorf("the verdict does not say what a failed E6 costs:\n%s", out)
	}
}

func TestWeighRefusesToQuoteTheGateOnAnUncontrolledComparison(t *testing.T) {
	runs := weighThree(52.4, 46.9, 48.2)
	runs[1].Seed = 23
	out, _, code := exec(t, "weigh", weighArms(t, runs))
	if code != 1 {
		t.Fatalf("arms on two seeds exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "This is not a controlled comparison") {
		t.Errorf("the report does not say why it stopped:\n%s", out)
	}
	if strings.Contains(out, "E6 and E7 both pass") {
		t.Errorf("the report quotes a gate that came off an uncontrolled comparison:\n%s", out)
	}

	// An arm that finished ten percent short is a different run, whatever it scored.
	short := weighThree(52.4, 46.9, 48.2)
	short[2].Tokens -= 20_000_000_000
	out, _, code = exec(t, "weigh", weighArms(t, short))
	if code != 1 {
		t.Fatalf("an arm that finished short exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "apart against a 2.0% line") {
		t.Errorf("the refusal does not say how far apart the arms finished:\n%s", out)
	}
}

func TestWeighRefusesAnArmScoredOffTheFleet(t *testing.T) {
	runs := weighThree(52.4, 46.9, 48.2)
	runs[0].EvalBox = "colab"
	out, _, code := exec(t, "weigh", weighArms(t, runs))
	if code != 1 {
		t.Fatalf("an arm scored off the fleet exited %d\n%s", code, out)
	}
	if !strings.Contains(out, `was scored on "colab", which is not a box in the fleet`) {
		t.Errorf("the refusal does not name the box:\n%s", out)
	}

	blank := weighThree(52.4, 46.9, 48.2)
	blank[2].EvalBox = ""
	out, _, code = exec(t, "weigh", weighArms(t, blank))
	if code != 1 {
		t.Fatalf("an arm that does not say where it was scored exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "does not say where it was scored") {
		t.Errorf("the refusal does not say what is missing:\n%s", out)
	}
}

func TestWeighJSONCarriesTheGapAndWhatItRestsOn(t *testing.T) {
	out, _, code := exec(t, "weigh", "-json", weighArms(t, weighThree(52.4, 46.9, 48.2)))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{`"gap"`, `"e6"`, `"lift"`, `"e7"`, `"cleaning"`, `"controlled"`, `"arms"`, `"verdict"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the JSON does not carry %s:\n%s", want, out)
		}
	}
	if !strings.Contains(out, `"name": "com-8B-cpt"`) {
		t.Errorf("the JSON does not carry the comparison name:\n%s", out)
	}
}

func TestWeighWithoutAnArmsFileIsAUsageError(t *testing.T) {
	if _, _, code := exec(t, "weigh"); code != 2 {
		t.Error("gao weigh with no arms file did not exit 2")
	}
	if _, _, code := exec(t, "weigh", "a.jsonl", "b.jsonl"); code != 2 {
		t.Error("gao weigh with two arms files did not exit 2")
	}
	if _, _, code := exec(t, "weigh", filepath.Join(t.TempDir(), "nothing.jsonl")); code != 1 {
		t.Error("an arms file that is not there did not exit 1")
	}
}
