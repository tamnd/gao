package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syllablePool is marked Vietnamese, most of it function words. The carrier walks
// it at a stride so that no pair of syllables in it turns up often enough to
// take a slot, which leaves the phrase under test at the top of the table
// instead of the boilerplate around it.
var syllablePool = strings.Fields(`của là có và với một này đã để các
	thì cũng rằng đến từ sẽ vì tại được những
	không người phải nếu hoặc nhưng bảo cáo điều đó
	trước sau giữa bằng hơn nữa rất khá mới cũ
	trên dưới ngoài cùng riêng chung nhiều ít lớn nhỏ
	đầu cuối giờ ngày tháng năm tuần buổi sáng chiều`)

// syllableSample writes docs documents, each holding the phrase each times behind
// a carrier, and hands back the paths.
func syllableSample(t *testing.T, docs, each int, phrase string) []string {
	t.Helper()

	dir := t.TempDir()
	paths := make([]string, 0, docs)
	for d := range docs {
		var b strings.Builder
		for i := range each {
			line := make([]string, 0, 6)
			for k := range 6 {
				line = append(line, syllablePool[((d*each+i)*7+k)%len(syllablePool)])
			}
			fmt.Fprintf(&b, "%s. %s.\n", strings.Join(line, " "), phrase)
		}
		path := filepath.Join(dir, fmt.Sprintf("bai-%03d.txt", d))
		if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

func TestSyllablePrintsWhatTheRuleGovernsAndWhatItGivesUp(t *testing.T) {
	args := append([]string{"syllable", "-source", "a sample of gao"}, syllableSample(t, 4, 60, "việt nam")...)

	out, errOut, code := exec(t, args...)

	if code == 0 {
		t.Errorf("a reading over four documents reported that it holds:\n%s", out)
	}
	if errOut != "" {
		t.Errorf("something went to stderr: %s", errOut)
	}
	for _, want := range []string{
		"a sample of gao",
		"marked syllable",
		"governed",
		"việt nam",
		"tokens saved",
		"syllable atomic 1.00 tokens per syllable",
		"the rule costs",
		"P07-3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

// The exit code is the argument. A reading that cannot answer the question has
// to be worth an exit code, or the next person quotes the cost off a sample of
// four documents.
func TestSyllableExitsTwoWhenTheSampleCannotAnswerTheQuestion(t *testing.T) {
	args := append([]string{"syllable", "-source", "a sample of gao"}, syllableSample(t, 2, 4, "việt nam")...)

	out, _, code := exec(t, args...)

	if code != 2 {
		t.Errorf("a two document sample exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "This is not the sample it looks like:") {
		t.Errorf("the report does not say why:\n%s", out)
	}
}

func TestSyllableExitsOneWhenThereIsNoReadingToTake(t *testing.T) {
	args := append([]string{"syllable"}, syllableSample(t, 1, 60, "việt nam")...)

	out, _, code := exec(t, args...)

	if code != 1 {
		t.Errorf("a reading with no source exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "This is not a reading of anything") {
		t.Errorf("the report does not say why:\n%s", out)
	}
}

func TestSyllablePrintsJSON(t *testing.T) {
	args := append([]string{"syllable", "-source", "a sample of gao", "-json"}, syllableSample(t, 4, 60, "việt nam")...)

	out, _, _ := exec(t, args...)

	var got struct {
		Source    string  `json:"source"`
		Syllables int64   `json:"syllables"`
		Governed  float64 `json:"governed"`
		Atomic    float64 `json:"atomic"`
		Crossing  float64 `json:"crossing"`
		Cost      float64 `json:"cost"`
		Runs      []struct {
			Run   string `json:"run"`
			Count int    `json:"count"`
			Saves int    `json:"saves"`
		} `json:"runs"`
		Faults  []string `json:"faults"`
		Holds   bool     `json:"holds"`
		Verdict string   `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out)
	}
	if got.Source != "a sample of gao" {
		t.Errorf("the source came back as %q", got.Source)
	}
	if got.Atomic != 1 {
		t.Errorf("the arm with the rule came back at %.2f tokens per syllable", got.Atomic)
	}
	if got.Crossing >= got.Atomic {
		t.Errorf("the arm without the rule came back at %.4f, no cheaper than the arm with it", got.Crossing)
	}
	if len(got.Runs) == 0 || got.Runs[0].Run != "việt nam" {
		t.Errorf("the run table came back as %+v", got.Runs)
	}
	if got.Holds || len(got.Faults) == 0 || got.Verdict == "" {
		t.Errorf("a four document reading came back holding: %+v", got)
	}
}

// The table is printed at a length somebody reads, and the reading behind it is
// not cut down to match.
func TestSyllablePrintsAsManyRunsAsItWasAsked(t *testing.T) {
	args := append([]string{"syllable", "-source", "a sample of gao", "-top", "1"}, syllableSample(t, 4, 60, "việt nam")...)

	out, _, _ := exec(t, args...)

	if strings.Contains(out, "the 1 slot that buys most") == false {
		t.Errorf("the table header does not say how much of it is printed:\n%s", out)
	}
	if n := strings.Count(out, "tokens saved"); n != 1 {
		t.Errorf("the run table was printed %d times", n)
	}
}

func TestSyllableRefusesTheUsageErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"no documents", []string{"syllable", "-source", "a sample"}},
		{"a table of no rows at all", []string{"syllable", "-source", "a sample", "-top", "-1", "x.txt"}},
		{"a flag nobody has", []string{"syllable", "-nope"}},
		{"the help", []string{"syllable", "-h"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := exec(t, c.args...)
			if code != 2 {
				t.Errorf("exited %d, want 2\n%s%s", code, out, errOut)
			}
			if errOut == "" {
				t.Error("nothing was said about it on stderr")
			}
		})
	}
}

func TestSyllableRefusesADocumentThatIsNotThere(t *testing.T) {
	out, errOut, code := exec(t, "syllable", "-source", "a sample", filepath.Join(t.TempDir(), "khong-co.txt"))

	if code != 1 {
		t.Errorf("exited %d, want 1\n%s", code, out)
	}
	if !strings.Contains(errOut, "khong-co.txt") {
		t.Errorf("stderr does not name the file: %s", errOut)
	}
}

func TestSyllableIsInTheCommandList(t *testing.T) {
	out, _, _ := exec(t, "help")

	if !strings.Contains(out, "tieng") {
		t.Error("gao help does not list tieng")
	}
}
