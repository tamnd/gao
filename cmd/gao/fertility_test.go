package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fertilityLog writes a log of readings, one tokenizer per entry, each taken on
// every box named. The chars and the syllables are the same text everywhere,
// because two readings over different text are not a comparison and there is a
// separate test for that.
func fertilityLog(t *testing.T, on map[string][]string, tokens map[string]int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fertility.jsonl")
	var b strings.Builder
	for _, name := range []string{"gemma-3", "llama-3.3", "qwen3", "gao-192k", "gemma-3-plus-32k"} {
		for _, box := range on[name] {
			fmt.Fprintf(&b, `{"tokenizer":%q,"corpus":"sha256:9e1c40b2","box":%q,"chars":10000000,"syllables":2500000,"tokens":%d}`+"\n",
				name, box, tokens[name])
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// everyCandidate is the whole roster measured on two boxes each, which is the
// state the milestone item describes and the only state that exits zero.
func everyCandidate() (map[string][]string, map[string]int64) {
	return map[string][]string{
			"gemma-3":          {"server1", "gamingpc"},
			"llama-3.3":        {"server1", "server2"},
			"qwen3":            {"server1", "server3"},
			"gao-192k":         {"server1", "gamingpc"},
			"gemma-3-plus-32k": {"server1", "server2"},
		}, map[string]int64{
			"gemma-3":          3_311_258,
			"llama-3.3":        4_385_965,
			"qwen3":            3_773_585,
			"gao-192k":         3_200_000,
			"gemma-3-plus-32k": 2_777_778,
		}
}

// The roster is the answer to a question nobody asks until late, which is which
// tokenizers can be measured at all. A candidate nobody has pinned cannot be,
// and the list has to say that out loud rather than printing four rows and
// looking complete.
func TestTheRosterSaysWhichCandidatesCanBeMeasured(t *testing.T) {
	out, errOut, code := exec(t, "dem", "fertility")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"gemma-3", "llama-3.3", "qwen3", "gao-192k", "gemma-3-plus-32k", "from scratch", "not yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("the roster does not mention %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "of 5 candidates is pinned") {
		t.Errorf("the roster does not count what is pinned:\n%s", out)
	}
}

// Fertility is one of the few numbers in this project that cannot be fixed
// later, so the ranking is by the figure the training budget is a function of
// and the cost of the choice is printed next to it.
func TestTheSlateRanksTheCandidatesAndPricesTheChoice(t *testing.T) {
	on, tokens := everyCandidate()
	out, errOut, code := exec(t, "dem", "fertility", fertilityLog(t, on, tokens))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "the floor") {
		t.Errorf("nothing is named as the cheapest:\n%s", out)
	}
	if !strings.Contains(out, "+58%") {
		t.Errorf("the 1.75 against 1.11 tokens per syllable is not priced:\n%s", out)
	}
	best := strings.Index(out, "gemma-3-plus-32k")
	worst := strings.Index(out, "llama-3.3")
	if best < 0 || worst < 0 || best > worst {
		t.Errorf("the slate is not cheapest first:\n%s", out)
	}
	if !strings.Contains(out, "58% more for the same Vietnamese") {
		t.Errorf("the verdict does not price the spread:\n%s", out)
	}
}

// The predictions were written down before any of this was measured, and
// reporting the measurement without saying whether it landed inside them turns a
// bet into a fact after the fact.
func TestThePredictionsAreCheckedAgainstWhatWasMeasured(t *testing.T) {
	on, tokens := everyCandidate()
	out, _, code := exec(t, "dem", "fertility", fertilityLog(t, on, tokens))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{"P07-1 held", "P07-2 held", "P07-5 held"} {
		if !strings.Contains(out, want) {
			t.Errorf("the reading does not say %q:\n%s", want, out)
		}
	}
}

// The whole of the fleet item. The same tokenizer over the same text on two
// boxes has to give the same count, and a disagreement is a locale, a
// normalization, or a tokenizer file that is not the pinned one. It is worth
// nothing if the disagreement is a line of prose a pipeline does not read.
func TestTheSameTextCountedDifferentlyOnTwoBoxesFails(t *testing.T) {
	on, tokens := everyCandidate()
	path := fertilityLog(t, on, tokens)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(b),
		`"box":"gamingpc","chars":10000000,"syllables":2500000,"tokens":3311258`,
		`"box":"gamingpc","chars":10000000,"syllables":2500000,"tokens":3318102`, 1)
	if broken == string(b) {
		t.Fatal("the log this test breaks on purpose did not contain the reading it breaks")
	}
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "dem", "fertility", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "which is a locale, a normalization, or a tokenizer file that is not the pinned one") {
		t.Errorf("the reading does not say what a disagreement means:\n%s", out)
	}
}

// A candidate nobody measured is the difference between a comparison and a
// shortlist, and leaving it off the report is how the second gets mistaken for
// the first.
func TestACandidateNobodyMeasuredIsNamedAndIsNotDone(t *testing.T) {
	on, tokens := everyCandidate()
	delete(on, "gao-192k")
	out, _, code := exec(t, "dem", "fertility", fertilityLog(t, on, tokens))
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "Not measured: gao-192k") {
		t.Errorf("the candidate nobody measured is not named:\n%s", out)
	}
	if !strings.Contains(out, "not yet the comparison the decision needs") {
		t.Errorf("an incomplete slate read as an answer:\n%s", out)
	}
}

// One box twice is a repeat rather than a reproduction, and it is the failure
// this check exists to catch, since it looks identical in every summary that
// counts readings instead of boxes.
func TestOneBoxTwiceIsNotAReproduction(t *testing.T) {
	on, tokens := everyCandidate()
	on["qwen3"] = []string{"server1", "server1"}
	out, _, code := exec(t, "dem", "fertility", fertilityLog(t, on, tokens))
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "a repeat rather than a reproduction") {
		t.Errorf("two readings off one box read as the fleet check passing:\n%s", out)
	}
}

func TestTheSlateIsAlsoMachineReadable(t *testing.T) {
	on, tokens := everyCandidate()
	out, _, code := exec(t, "dem", "fertility", "-json", fertilityLog(t, on, tokens))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var report struct {
		Candidates int  `json:"candidates"`
		Complete   bool `json:"complete"`
		Reproduced bool `json:"reproduced"`
		Spread     float64
		Measured   []struct {
			Tokenizer   string   `json:"tokenizer"`
			Boxes       []string `json:"boxes"`
			PerSyllable float64  `json:"tokens_per_syllable"`
			Predicts    string   `json:"predicts"`
			Held        bool     `json:"held"`
		} `json:"measured"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if report.Candidates != 5 || len(report.Measured) != 5 {
		t.Fatalf("%d of %d candidates came back measured", len(report.Measured), report.Candidates)
	}
	if !report.Complete || !report.Reproduced {
		t.Error("the whole roster measured on two boxes each did not read as complete and reproduced")
	}
	if got := report.Spread; got < 1.57 || got > 1.58 {
		t.Errorf("the spread is %.3f, want the 1.75 against 1.11 ratio", got)
	}
	first := report.Measured[0]
	if first.Tokenizer != "gemma-3-plus-32k" || first.Predicts != "P07-2" || !first.Held {
		t.Errorf("the cheapest row is %+v", first)
	}
	if len(first.Boxes) != 2 {
		t.Errorf("the row does not carry the boxes behind it: %+v", first)
	}
}

func TestTheRosterIsAlsoMachineReadable(t *testing.T) {
	out, _, code := exec(t, "dem", "fertility", "-roster", "-json")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var roster []struct {
		Tokenizer string `json:"tokenizer"`
		Pinned    bool   `json:"pinned"`
		Digest    string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(out), &roster); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if len(roster) != 5 {
		t.Fatalf("%d candidates came back", len(roster))
	}
	var pinned int
	for _, c := range roster {
		if c.Pinned != (c.Digest != "") {
			t.Errorf("%s claims pinned=%v with digest %q", c.Tokenizer, c.Pinned, c.Digest)
		}
		if c.Pinned {
			pinned++
		}
	}
	if pinned == 0 || pinned == len(roster) {
		t.Errorf("%d of %d pinned, and the roster is meant to report the holes rather than hide them", pinned, len(roster))
	}
}

func TestAFertilityLogThatIsNotThereIsAFailure(t *testing.T) {
	_, errOut, code := exec(t, "dem", "fertility", filepath.Join(t.TempDir(), "nowhere.jsonl"))
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "nowhere.jsonl") {
		t.Errorf("the error does not name the file: %s", errOut)
	}
}

func TestFertilityRejectsAnExtraArgument(t *testing.T) {
	if _, _, code := exec(t, "dem", "fertility", "one.jsonl", "two.jsonl"); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}
