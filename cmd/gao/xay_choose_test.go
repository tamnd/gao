package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/xay"
)

func writeRuns(t *testing.T, runs []xay.Ablation) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runs.json")
	b, err := json.Marshal(runs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func ablation(threshold, retention, score float64) xay.Ablation {
	return xay.Ablation{
		Threshold: threshold,
		Retention: retention,
		Score:     score,
		Noise:     0.3,
		Tokens:    8_000_000_000,
		Eval:      "vi-cloze",
		Box:       "gamingpc",
	}
}

func TestXayChoosesTheThresholdFromTheRuns(t *testing.T) {
	path := writeRuns(t, []xay.Ablation{
		ablation(0.60, 0.71, 41.0),
		ablation(0.70, 0.79, 42.0),
		ablation(0.80, 0.84, 46.0),
		ablation(0.90, 0.91, 41.5),
	})

	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-choose", path, "-json"}); code != 0 {
		t.Fatalf("gao mill -choose = %d, want 0\n%s", code, stderr.String())
	}
	var c xay.Choice
	if err := json.Unmarshal(stdout.Bytes(), &c); err != nil {
		t.Fatalf("the choice is not JSON: %v\n%s", err, stdout.String())
	}
	if !c.Measured || c.Threshold != 0.80 {
		t.Errorf("chose %.2f measured=%v, want 0.80 measured", c.Threshold, c.Measured)
	}
	if len(c.Ablations) != 4 {
		t.Errorf("the choice carries %d runs, want the 4 it was made from", len(c.Ablations))
	}
}

// A set that did not separate is a result rather than a failure, so it exits
// zero and says the default stands.
func TestXayReportsACurveThatDidNotSeparate(t *testing.T) {
	path := writeRuns(t, []xay.Ablation{
		ablation(0.60, 0.71, 42.0),
		ablation(0.70, 0.79, 42.2),
		ablation(0.80, 0.84, 41.9),
		ablation(0.90, 0.91, 42.1),
	})

	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-choose", path}); code != 0 {
		t.Fatalf("gao mill -choose = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "0.71") {
		t.Errorf("a flat curve did not come back with the default:\n%s", out)
	}
	if !strings.Contains(out, "did not separate") {
		t.Errorf("the output does not say the runs failed to separate:\n%s", out)
	}
}

// A set that cannot support any choice exits non zero, so a pipeline asking for
// a measured threshold stops instead of quietly running on the default.
func TestXayExitsNonZeroOnRunsThatCannotChoose(t *testing.T) {
	path := writeRuns(t, []xay.Ablation{
		ablation(0.60, 0.71, 41.0),
		ablation(0.80, 0.84, 46.0),
	})

	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-choose", path}); code != 1 {
		t.Errorf("gao mill -choose on two runs = %d, want 1\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot choose a threshold") {
		t.Errorf("the refusal does not say why: %q", stderr.String())
	}
}

func TestXayChooseSaysWhichFileItCouldNotRead(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "khong-co.json")

	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-choose", missing}); code != 1 {
		t.Errorf("gao mill -choose on a missing file = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "khong-co.json") {
		t.Errorf("stderr does not name the file: %q", stderr.String())
	}
}

// The threshold is published with the runs under it, because a number quoted on
// its own is a default with a story attached.
func TestXayChoosePrintsTheRunsUnderTheAnswer(t *testing.T) {
	path := writeRuns(t, []xay.Ablation{
		ablation(0.60, 0.71, 41.0),
		ablation(0.70, 0.79, 42.0),
		ablation(0.80, 0.84, 46.0),
		ablation(0.90, 0.91, 41.5),
	})

	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-choose", path}); code != 0 {
		t.Fatalf("gao mill -choose = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"threshold 0.80", "retention", "vi-cloze", "gamingpc"} {
		if !strings.Contains(out, want) {
			t.Errorf("the published choice does not say %q:\n%s", want, out)
		}
	}
}
