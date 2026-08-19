package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keepPanel writes a retention file where every specialist gained twelve points,
// the distillation kept the given share of that, and merging the same
// checkpoints kept two thirds.
func keepPanel(t *testing.T, kept, merged float64) string {
	t.Helper()
	names := [][2]string{
		{"diacritics", "vlsp-diacritics"},
		{"legal-citation", "vi-legal-qa"},
		{"math", "vi-gsm8k"},
		{"code", "vi-humaneval"},
		{"ocr-correction", "ocr-eval-vi"},
		{"dialect", "vi-dialect-nlu"},
		{"summary", "vi-xlsum"},
	}
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, `{"name":%q,"benchmark":%q,"base":50,"own":62,"distilled":%.3f,"merged":%.3f,"runs":5,"spread":1.2,"box":"gamingpc"}`+"\n",
			n[0], n[1], 50+12*kept, 50+12*merged)
	}
	path := filepath.Join(t.TempDir(), "retention.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRetentionIsReportedOneSpecialistAtATime(t *testing.T) {
	out, errOut, code := exec(t, "keep", keepPanel(t, 0.93, 0.66))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"diacritics", "legal-citation", "ocr-correction", "vi-humaneval", "93%", "66%"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not print %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "every specialist kept at least") {
		t.Errorf("the verdict is not written against the worst line:\n%s", out)
	}
}

// The distillation can carry every specialist and still not be worth the seven
// training runs, and that exits non zero because P09-2 is what the slice is for.
func TestAPanelThatMergingMatchesExitsNonZero(t *testing.T) {
	out, errOut, code := exec(t, "keep", keepPanel(t, 0.93, 0.89))
	if code != 2 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "an afternoon of weight arithmetic") {
		t.Errorf("the report does not say what the pipeline bought:\n%s", out)
	}
}

// A panel that cannot carry a retention exits 1 rather than reporting one, which
// is the difference between a measurement and a number.
func TestAPanelThatCannotCarryARetentionIsRefused(t *testing.T) {
	path := keepPanel(t, 0.93, 0.66)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if err := os.WriteFile(path, []byte(strings.Join(lines[:4], "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := exec(t, "keep", path)
	if code != 1 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "4 of the 7 specialists were measured") {
		t.Errorf("the report does not say what is missing:\n%s", out)
	}
	if !strings.Contains(out, "fault") {
		t.Errorf("the faults are not printed as faults:\n%s", out)
	}
}

// A mean well above the worst line is a finding rather than a fault, and the
// report says so in the same breath as the mean.
func TestTheMeanIsNotPrintedWithoutWhatItHides(t *testing.T) {
	path := keepPanel(t, 0.95, 0.66)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	lines[1] = strings.Replace(lines[1], `"distilled":61.400`, `"distilled":52.400`, 1)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := exec(t, "keep", path)
	if code != 2 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"not a number to quote on its own", "legal-citation kept 20%", "a model that works and a model that does not"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
}

func TestTheRetentionIsAlsoMachineReadable(t *testing.T) {
	out, errOut, code := exec(t, "keep", "-json", keepPanel(t, 0.93, 0.66))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got struct {
		Specialists int     `json:"specialists"`
		Worst       string  `json:"worst"`
		WorstKept   float64 `json:"worst_kept"`
		Merged      float64 `json:"merged_mean"`
		Holds       bool    `json:"holds"`
		Kept        []struct {
			Name     string  `json:"name"`
			Retained float64 `json:"retained"`
		} `json:"kept"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Specialists != 7 || len(got.Kept) != 7 {
		t.Errorf("read %d specialists and %d retentions", got.Specialists, len(got.Kept))
	}
	if !got.Holds || got.WorstKept < 0.90 || got.Merged > 0.70 {
		t.Errorf("P09-2 came back as %+v", got)
	}
	if got.Kept[0].Name != got.Worst {
		t.Errorf("the list leads with %s and the worst is %s", got.Kept[0].Name, got.Worst)
	}
}

func TestKeepRefusesWhatItCannotRead(t *testing.T) {
	if _, _, code := exec(t, "keep"); code != 2 {
		t.Errorf("no argument exited %d", code)
	}
	if _, _, code := exec(t, "keep", "a.jsonl", "b.jsonl"); code != 2 {
		t.Errorf("two arguments exited %d", code)
	}
	if _, _, code := exec(t, "keep", filepath.Join(t.TempDir(), "missing.jsonl")); code != 1 {
		t.Errorf("a file that is not there exited %d", code)
	}
}
