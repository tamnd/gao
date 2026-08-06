package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tensorLine writes one tensor's reading the way a training loop with the check
// turned on would append it.
func tensorLine(name, kind string, zeros, clipped int64, amin, cosine float64) string {
	return fmt.Sprintf(
		`{"name":%q,"kind":%q,"step":42000,"count":16777216,"zeros":%d,"clipped":%d,`+
			`"amax":0.5,"amin":%g,"scale":448,"window":32,"cosine":%g,"box":"gamingpc"}`,
		name, kind, zeros, clipped, amin, cosine)
}

// chimStep writes a step that lost nothing unless a test says otherwise.
func chimStep(t *testing.T, lines ...string) string {
	t.Helper()
	if len(lines) == 0 {
		lines = []string{
			tensorLine("blocks.12.mlp.experts.7.down_proj.weight", "weight", 0, 0, 1e-5, 0.9997),
			tensorLine("blocks.12.attn.out_proj.act", "activation", 0, 0, 1e-5, 0.9996),
			tensorLine("blocks.12.mlp.experts.7.down_proj.grad", "gradient", 0, 0, 1e-5, 0.9995),
		}
	}
	path := filepath.Join(t.TempDir(), "step.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAStepThatKeptItsValuesPrintsWhatItChecked(t *testing.T) {
	out, errOut, code := exec(t, "chim", "-loss", "2.3141", "-bf16", "2.3139", chimStep(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"gradient", "16.8M", "448", "gamingpc",
		"kept its live values inside E4M3",
		"is not the check",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
}

// The item is the check that catches a silent underflow, and this is what
// silent means: both curves agree and a twentieth of a gradient is gone.
func TestTheCurveAgreeingAndTheGradientEmptyingExitsNonZero(t *testing.T) {
	path := chimStep(t,
		tensorLine("blocks.12.mlp.experts.7.down_proj.weight", "weight", 0, 0, 1e-5, 0.9997),
		tensorLine("blocks.12.attn.out_proj.act", "activation", 0, 0, 1e-5, 0.9996),
		tensorLine("blocks.12.mlp.experts.7.down_proj.grad", "gradient", 838_860, 0, 3.4e-6, 0.9995),
	)
	out, errOut, code := exec(t, "chim", "-loss", "2.3141", "-bf16", "2.3139", path)
	if code != 2 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"flushed 5.0% of its live values", "why the curve is not the check", "0.0002 apart"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
}

// A tensor wider than the format is the one result that says stop rather than
// retune, and the table says so on its own row.
func TestATensorWiderThanTheFormatIsMarkedInTheTable(t *testing.T) {
	path := chimStep(t,
		tensorLine("blocks.12.mlp.experts.7.down_proj.weight", "weight", 0, 0, 1e-5, 0.9997),
		tensorLine("blocks.12.attn.out_proj.act", "activation", 0, 0, 1e-5, 0.9996),
		tensorLine("blocks.12.mlp.experts.7.down_proj.grad", "gradient", 0, 0, 1e-9, 0.9995),
	)
	out, _, code := exec(t, "chim", "-loss", "2.3141", "-bf16", "2.3139", path)
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"too wide", "no scale exists that keeps both ends of it", "stays in BF16"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
}

func TestAStepThatIsNotACheckExitsOne(t *testing.T) {
	path := chimStep(t,
		tensorLine("blocks.12.mlp.experts.7.down_proj.weight", "weight", 0, 0, 1e-5, 0.9997),
		tensorLine("blocks.12.attn.out_proj.act", "activation", 0, 0, 1e-5, 0.9996),
	)
	out, _, code := exec(t, "chim", "-loss", "2.3141", "-bf16", "2.3139", path)
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "a check of the easy half") {
		t.Errorf("the report does not say what is missing:\n%s", out)
	}

	// And a step with no BF16 loss beside it is the case this whole command
	// exists to argue with.
	alone, _, code := exec(t, "chim", "-loss", "2.3141", chimStep(t))
	if code != 1 {
		t.Fatalf("exit %d with no reference: %s", code, alone)
	}
	if !strings.Contains(alone, "the FP8 curve agrees while the tensors do not") {
		t.Errorf("the report does not ask for the reference:\n%s", alone)
	}
}

func TestTheStepIsAlsoMachineReadable(t *testing.T) {
	path := chimStep(t,
		tensorLine("blocks.12.mlp.experts.7.down_proj.weight", "weight", 0, 0, 1e-5, 0.9997),
		tensorLine("blocks.12.attn.out_proj.act", "activation", 0, 1_677, 1e-5, 0.9996),
		tensorLine("blocks.12.mlp.experts.7.down_proj.grad", "gradient", 838_860, 0, 3.4e-6, 0.9995),
	)
	out, errOut, code := exec(t, "chim", "-json", "-loss", "2.3141", "-bf16", "2.3139", path)
	if code != 2 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got struct {
		Model      string  `json:"model"`
		Step       int     `json:"step"`
		Tensors    int     `json:"tensors"`
		Silent     int     `json:"silent"`
		Unfittable int     `json:"unfittable"`
		Worst      string  `json:"worst"`
		Divergence float64 `json:"divergence"`
		Holds      bool    `json:"holds"`
		Readings   []struct {
			Name       string  `json:"name"`
			Kind       string  `json:"kind"`
			Live       int64   `json:"live"`
			Underflow  float64 `json:"underflow"`
			Saturation float64 `json:"saturation"`
			Headroom   float64 `json:"headroom"`
			Floor      float64 `json:"floor"`
			Fits       bool    `json:"fits"`
			Silent     bool    `json:"silent"`
		} `json:"readings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Step != 42_000 || got.Tensors != 3 || got.Holds {
		t.Errorf("the step came back as %+v", got)
	}
	if got.Silent != 1 || got.Unfittable != 0 || !strings.HasSuffix(got.Worst, ".grad") {
		t.Errorf("silent %d, unfittable %d, worst %s", got.Silent, got.Unfittable, got.Worst)
	}
	if got.Divergence < 0.0001 || got.Divergence > 0.0003 {
		t.Errorf("the two curves came back %g apart", got.Divergence)
	}
	first := got.Readings[0]
	if first.Kind != "gradient" || !first.Silent || first.Live != 16_777_216 {
		t.Errorf("the worst row came back as %+v", first)
	}
	if first.Underflow < 0.049 || first.Underflow > 0.051 {
		t.Errorf("a twentieth of the live values came back as %g", first.Underflow)
	}
	if first.Headroom < 1.9 || first.Headroom > 2.1 || !first.Fits {
		t.Errorf("headroom %g, fits %v", first.Headroom, first.Fits)
	}
	// The bottom of this tensor lands under the subnormal floor, which is where
	// the five percent went and the reason a scale that fits is not a scale that
	// keeps everything.
	if first.Floor >= 0.001953125 {
		t.Errorf("the smallest live value landed at %g, above the floor it is supposed to have sunk through", first.Floor)
	}
	var clipped bool
	for _, r := range got.Readings {
		if r.Saturation > 0 {
			clipped = true
		}
	}
	if !clipped {
		t.Error("the row that clipped came back with no saturation on it")
	}
}

func TestChimRefusesWhatItCannotRead(t *testing.T) {
	if _, _, code := exec(t, "chim"); code != 2 {
		t.Errorf("no argument exited %d", code)
	}
	if _, _, code := exec(t, "chim", "a.jsonl", "b.jsonl"); code != 2 {
		t.Errorf("two arguments exited %d", code)
	}
	if _, _, code := exec(t, "chim", filepath.Join(t.TempDir(), "missing.jsonl")); code != 1 {
		t.Errorf("a file that is not there exited %d", code)
	}
}
