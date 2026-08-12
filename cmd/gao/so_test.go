package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	soNative     = "com-8b-sft-native"
	soTranslated = "com-8b-sft-translated"
)

// soProtocol writes an evaluation that was run properly: every fifth item read
// twice, the second reading in the opposite order, and a preference of about two
// to one for the native system. pick names the system the rater chose.
func soProtocol(t *testing.T, items int, pick func(item int) string) string {
	t.Helper()

	lines := make([]string, 0, items)
	for i := range items {
		reads := 1
		if i%5 == 0 {
			reads = 2
		}
		for k := range reads {
			left, right := soNative, soTranslated
			if (i%2 == 0) != (k == 1) {
				left, right = soTranslated, soNative
			}

			choice := "tie"
			switch pick(i) {
			case left:
				choice = "left"
			case right:
				choice = "right"
			}

			line, err := json.Marshal(map[string]any{
				"item":            fmt.Sprintf("prompt-%04d", i),
				"rater":           fmt.Sprintf("r%02d", (i*3+k*5)%8),
				"left":            left,
				"right":           right,
				"left_syllables":  120,
				"right_syllables": 118,
				"choice":          choice,
			})
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(line))
		}
	}

	path := filepath.Join(t.TempDir(), "pairs.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// twoToOne is the preference a working protocol would find.
func twoToOne(item int) string {
	if item%3 == 0 {
		return soTranslated
	}
	return soNative
}

func TestSoReadsAProtocolThatWasRunProperlyAsAResult(t *testing.T) {
	out, errOut, code := exec(t, "so", soProtocol(t, 400, twoToOne))

	if code != 0 {
		t.Fatalf("an ordinary protocol: exit %d, want 0\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"480 judgements over 400 items, read by 8 people",
		"the answer shown first won",
		"com-8b-sft-native was shown first in",
		"the longer answer won",
		"raters agreed",
		"The people who read the most of it:",
		"beat com-8b-sft-translated here",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not a result about the answers") {
		t.Errorf("an ordinary protocol reported faults:\n%s", out)
	}
}

// The number this command exists to stop somebody publishing.
func TestSoSaysWhenAWinRateIsATie(t *testing.T) {
	out, errOut, code := exec(t, "so", soProtocol(t, 400, func(item int) string {
		if item%25 == 0 {
			return soNative
		}
		if item%2 == 0 {
			return soTranslated
		}
		return soNative
	}))

	if code != 2 {
		t.Fatalf("a near tie: exit %d, want 2\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "covers a half, so this evaluation does not say either system won") {
		t.Errorf("the report reads a tie as a win:\n%s", out)
	}
}

func TestSoCatchesAnEvaluationOfItsOwnLayout(t *testing.T) {
	path := soProtocol(t, 400, twoToOne)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Every rater takes the left hand answer, whatever is in it.
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(strings.ReplaceAll(string(body), `"choice":"right"`, `"choice":"left"`), `"choice":"tie"`, `"choice":"left"`)), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "so", path)
	if code != 2 {
		t.Fatalf("a protocol that measured its own layout: exit %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "the layout rather than the answers") {
		t.Errorf("the report does not name the position effect:\n%s", out)
	}
}

func TestSoPrintsTheSameReadingAsJSON(t *testing.T) {
	out, _, code := exec(t, "so", "-json", soProtocol(t, 400, twoToOne))
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}

	var got struct {
		A           string  `json:"a"`
		B           string  `json:"b"`
		Pairs       int     `json:"pairs"`
		Items       int     `json:"items"`
		Decided     int     `json:"decided"`
		Rate        float64 `json:"rate"`
		Low         float64 `json:"low"`
		High        float64 `json:"high"`
		First       float64 `json:"first"`
		Order       float64 `json:"order"`
		DoubleShare float64 `json:"double_share"`
		Pi          float64 `json:"pi"`
		Raters      []struct {
			Rater string `json:"rater"`
			Pairs int    `json:"pairs"`
		} `json:"raters"`
		Separates bool     `json:"separates"`
		Holds     bool     `json:"holds"`
		Faults    []string `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the JSON does not parse: %v\n%s", err, out)
	}

	if got.A != soNative || got.B != soTranslated {
		t.Errorf("the systems came back as %s against %s", got.A, got.B)
	}
	if got.Pairs != 480 || got.Items != 400 {
		t.Errorf("%d judgements over %d items, want 480 over 400", got.Pairs, got.Items)
	}
	if got.Low >= got.Rate || got.High <= got.Rate {
		t.Errorf("the win rate %v does not sit inside %v to %v", got.Rate, got.Low, got.High)
	}
	if got.Low <= 0.5 {
		t.Errorf("a two to one preference came back with an interval reaching %v", got.Low)
	}
	if got.First > 0.55 || got.Order > 0.55 {
		t.Errorf("a protocol shown in both orders came back at %v first and %v order", got.First, got.Order)
	}
	if got.DoubleShare < 0.2 || got.Pi < 0.4 {
		t.Errorf("%v of the items were read twice, agreeing at pi %v", got.DoubleShare, got.Pi)
	}
	if len(got.Raters) != 8 {
		t.Errorf("%d raters came back, want 8", len(got.Raters))
	}
	if !got.Separates || !got.Holds || len(got.Faults) != 0 {
		t.Errorf("an ordinary protocol came back separates=%v holds=%v with %d faults", got.Separates, got.Holds, len(got.Faults))
	}
}

func TestSoRefusesAThirdSystem(t *testing.T) {
	path := soProtocol(t, 400, twoToOne)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	lines[3] = strings.Replace(lines[3], soTranslated, "com-8b-sft-mixed", 1)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := exec(t, "so", path)
	if code != 1 {
		t.Fatalf("three systems in a two system protocol: exit %d, want 1\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "two system protocol") {
		t.Errorf("the refusal does not say what is wrong:\n%s", out)
	}
	if !strings.Contains(out, "This is not an evaluation anybody can read") {
		t.Errorf("the refusal does not lead with what it refused:\n%s", out)
	}
}

func TestSoRefusesAnEvaluationTooSmallToReadAnIntervalOff(t *testing.T) {
	out, _, code := exec(t, "so", soProtocol(t, 100, twoToOne))

	if code != 1 {
		t.Fatalf("120 judgements: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "under the 200 this reading needs") {
		t.Errorf("the refusal does not say how many judgements it needs:\n%s", out)
	}
}

func TestSoSaysWhichLineOfTheProtocolIsWrong(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairs.jsonl")
	if err := os.WriteFile(path, []byte(`{"item":"prompt-0001","rater":"r01","left":"a","right":"b","choice":"left"}
{"item":"prompt-0002","rater":"r01","left":"a","right":"b","choice":"left","fluency":4}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := exec(t, "so", path)
	if code != 1 {
		t.Fatalf("a protocol file with a column nobody reads: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, ":2:") || !strings.Contains(errOut, "fluency") {
		t.Errorf("the failure does not name the line and the column:\n%s", errOut)
	}
}

func TestSoUsageErrors(t *testing.T) {
	if _, _, code := exec(t, "so"); code != 2 {
		t.Errorf("no file: exit %d, want 2", code)
	}

	_, errOut, code := exec(t, "so", "-h")
	if code != 2 {
		t.Errorf("gao so -h: exit %d, want 2", code)
	}
	for _, want := range []string{
		"or reading the layout",
		"a system tuned to write more beats a system",
		"54% win over 200",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the usage does not explain %q:\n%s", want, errOut)
		}
	}
}

func TestSoIsInTheCommandList(t *testing.T) {
	out, _, code := exec(t, "help")
	if code != 0 {
		t.Fatalf("gao help: exit %d", code)
	}
	if !strings.Contains(out, "whether the raters read the answers or the layout") {
		t.Errorf("so is not in the command list:\n%s", out)
	}
}
