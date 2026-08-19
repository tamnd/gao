package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseLog writes one reading per base, at the quality and fertility given, on
// one suite, since a table across two suites is a table of the suites.
func baseLog(t *testing.T, quality, fertility map[string]float64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bases.jsonl")
	var b strings.Builder
	for _, name := range []string{"gemma-3-27b-it", "qwen3-30b-a3b", "llama-3.3-70b-instruct", "mistral-small-3", "sailor2-8b"} {
		q, ok := quality[name]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, `{"base":%q,"quality":%.1f,"suite":"mmlu-pro multilingual","fertility":%.2f,"exposure":0.02,"box":"gamingpc"}`+"\n",
			name, q, fertility[name])
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func wholeRoster() (map[string]float64, map[string]float64) {
	return map[string]float64{
			"gemma-3-27b-it": 61.0, "qwen3-30b-a3b": 62.0, "llama-3.3-70b-instruct": 58.0,
			"mistral-small-3": 55.5, "sailor2-8b": 44.0,
		}, map[string]float64{
			"gemma-3-27b-it": 1.32, "qwen3-30b-a3b": 1.28, "llama-3.3-70b-instruct": 1.75,
			"mistral-small-3": 1.60, "sailor2-8b": 1.55,
		}
}

// The order of the criteria is the content, so the command that prints them has
// to print the order and say which two are not scores.
func TestTheCriteriaArePrintedInOrder(t *testing.T) {
	out, errOut, code := exec(t, "choose", "criteria")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"1  license", "a gate rather than a score", "3  fertility", "6  architecture"} {
		if !strings.Contains(out, want) {
			t.Errorf("the criteria do not mention %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "license") > strings.Index(out, "architecture") {
		t.Errorf("the criteria are not in order:\n%s", out)
	}
}

func TestTheRosterSaysWhatEachBaseIsBeforeAnybodyMeasuresIt(t *testing.T) {
	out, _, code := exec(t, "choose", "bases")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{"gemma-3-27b-it", "qwen3-30b-a3b", "llama-3.3-70b-instruct", "mistral-small-3", "sailor2-8b", "tekken"} {
		if !strings.Contains(out, want) {
			t.Errorf("the roster does not carry %q:\n%s", want, out)
		}
	}

	// The forward cost, which is the whole model everywhere here except the one
	// mixture, and is what makes a continued pretraining run affordable or not.
	if !strings.Contains(out, "3.0B") || !strings.Contains(out, "70.0B") {
		t.Errorf("the roster does not print what a token costs:\n%s", out)
	}
}

// The sentence this whole command exists to implement.
func TestFertilityDecidesInsideTheBandAndNotOutsideIt(t *testing.T) {
	quality, fertility := wholeRoster()
	out, _, code := exec(t, "choose", "score", baseLog(t, quality, fertility))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "criterion 3 decides and qwen3-30b-a3b wins it") {
		t.Errorf("a one point gap on criterion 2 did not hand the decision to criterion 3:\n%s", out)
	}

	// Now move the leader out of the band, keeping its fertility the worse of
	// the two, and the answer has to change.
	quality["gemma-3-27b-it"] = 68.0
	out, _, code = exec(t, "choose", "score", baseLog(t, quality, fertility))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "gemma-3-27b-it leads on criterion 2") {
		t.Errorf("a six point gap on criterion 2 was overturned by fertility:\n%s", out)
	}
	if strings.Contains(out, "criterion 3 decides") {
		t.Errorf("a decision on criterion 2 was reported as one on criterion 3:\n%s", out)
	}
}

// A leader out of two is not a choice out of five, and the difference is the
// only thing a reader of this table needs protecting from.
func TestATableWithHolesInItIsALeaderRatherThanAChoice(t *testing.T) {
	quality, fertility := wholeRoster()
	delete(quality, "sailor2-8b")
	delete(quality, "mistral-small-3")
	out, _, code := exec(t, "choose", "score", baseLog(t, quality, fertility))
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "Not yet comparable:") || !strings.Contains(out, "sailor2-8b") {
		t.Errorf("the bases nobody measured are not named:\n%s", out)
	}
	if !strings.Contains(out, "a leader rather than a choice") {
		t.Errorf("an incomplete table read as a decision:\n%s", out)
	}
}

func TestTwoSuitesIsNotARanking(t *testing.T) {
	quality, fertility := wholeRoster()
	path := baseLog(t, quality, fertility)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mixed := strings.Replace(string(b), `"base":"qwen3-30b-a3b","quality":62.0,"suite":"mmlu-pro multilingual"`,
		`"base":"qwen3-30b-a3b","quality":62.0,"suite":"vmlu"`, 1)
	if mixed == string(b) {
		t.Fatal("the log this test rewrites did not contain the line it rewrites")
	}
	if err := os.WriteFile(path, []byte(mixed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := exec(t, "choose", "score", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "a ranking across two suites is a ranking of the suites") {
		t.Errorf("scores from two suites were ranked without a word:\n%s", out)
	}
}

func TestTheTableIsAlsoMachineReadable(t *testing.T) {
	quality, fertility := wholeRoster()
	out, _, code := exec(t, "choose", "score", "-json", baseLog(t, quality, fertility))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var report struct {
		Bases   int     `json:"bases"`
		Band    float64 `json:"band"`
		Tied    bool    `json:"tied"`
		Choice  string  `json:"choice"`
		Decided bool    `json:"decided"`
		Ranked  []struct {
			Base      string  `json:"base"`
			Fertility float64 `json:"fertility"`
		} `json:"ranked"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if report.Bases != 5 || len(report.Ranked) != 5 {
		t.Fatalf("%d of %d bases came back ranked", len(report.Ranked), report.Bases)
	}
	if !report.Decided || report.Choice != "qwen3-30b-a3b" {
		t.Errorf("the table chose %q, decided=%v", report.Choice, report.Decided)
	}
	if !report.Tied || report.Band != 2 {
		t.Errorf("the band is %v and tied=%v, and a ranking is not readable without both", report.Band, report.Tied)
	}
}

func TestAChoiceLogThatIsNotThereIsAFailure(t *testing.T) {
	_, errOut, code := exec(t, "choose", "score", filepath.Join(t.TempDir(), "nowhere.jsonl"))
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "nowhere.jsonl") {
		t.Errorf("the error does not name the file: %s", errOut)
	}
}

func TestChooseRejectsWhatItDoesNotUnderstand(t *testing.T) {
	if _, _, code := exec(t, "choose"); code != 2 {
		t.Error("no subcommand did not read as a usage error")
	}
	if _, _, code := exec(t, "choose", "pick"); code != 2 {
		t.Error("a subcommand that does not exist did not read as a usage error")
	}
	if _, _, code := exec(t, "choose", "score"); code != 2 {
		t.Error("score with no log did not read as a usage error")
	}
	if out, _, code := exec(t, "choose", "help"); code != 0 || !strings.Contains(out, "in the order the criteria were written down") {
		t.Errorf("exit %d from help:\n%s", code, out)
	}
}
