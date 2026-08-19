package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/nhat"
)

// bangScores writes a scores file and returns the path to it.
func bangScores(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scores.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// bangSuite scores every benchmark on the repository roster, a fixed margin for
// the ones written in Vietnamese and another for the ones translated into it.
func bangSuite(t *testing.T, native, translated float64) string {
	t.Helper()
	ros, err := nhat.Rostered()
	if err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, len(ros.Benchmarks))
	for _, e := range ros.Benchmarks {
		m := native
		if e.Origin == nhat.Translated {
			m = translated
		}
		lines = append(lines, fmt.Sprintf(
			`{"benchmark":%q,"score":%.1f,"baseline":50.0,"against":"sailor2-8b","runs":3,"spread":0.4,"box":"gamingpc","measured_on":"2026-08-12"}`,
			e.Name, 50+m))
	}
	return bangScores(t, lines...)
}

func TestBangKeepsTheTwoArmsApart(t *testing.T) {
	// Exit 2 rather than 0, because nine entries on today's roster have no
	// pinned revision and the board carries that wherever it is printed.
	out, _, code := exec(t, "bang", "board", bangSuite(t, 3.0, 3.0))
	if code != 2 {
		t.Fatalf("gao board board: exit %d, want 2", code)
	}

	for _, want := range []string{
		"written in Vietnamese",
		"translated into Vietnamese",
		"the two are not added together",
		"no pinned revision",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the board does not say %q:\n%s", want, out)
		}
	}
	// The arm the claim is about is printed first.
	if strings.Index(out, "translated into Vietnamese") < strings.Index(out, "written in Vietnamese") {
		t.Errorf("the translated arm is printed first:\n%s", out)
	}
}

func TestBangNamesTheModelThatOnlyReadsTranslatedEnglish(t *testing.T) {
	out, _, code := exec(t, "bang", "board", bangSuite(t, 0.5, 6.0))
	if code != 2 {
		t.Fatalf("gao board board on a translationese gap: exit %d, want 2", code)
	}
	if !strings.Contains(out, "reads translated English rather than one that writes Vietnamese") {
		t.Errorf("the board does not name the gap:\n%s", out)
	}
	if !strings.Contains(out, "This board cannot be published as it stands") {
		t.Errorf("the board does not refuse to be published:\n%s", out)
	}
}

func TestBangRowsMarksTheBenchmarksGaoBuiltItself(t *testing.T) {
	out, _, code := exec(t, "bang", "rows", bangSuite(t, 3.0, 3.0))
	if code != 2 {
		t.Fatalf("gao board rows: exit %d, want 2", code)
	}

	var rows, own int
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "sailor2-8b"), strings.Contains(line, "benchmark  "):
			continue
		case strings.Contains(line, " ahead"), strings.Contains(line, " behind"), strings.Contains(line, " level"):
			rows++
			if strings.Contains(line, "gao ") {
				own++
			}
		}
	}
	if rows != 24 {
		t.Errorf("the table lists %d rows, the roster holds 24:\n%s", rows, out)
	}
	if own != 6 {
		t.Errorf("%d rows are marked as built here, gao built 6 of them:\n%s", own, out)
	}
}

func TestBangPrintsTheSameBoardAsJSON(t *testing.T) {
	out, _, code := exec(t, "bang", "board", "-json", bangSuite(t, 3.0, 3.0))
	if code != 2 {
		t.Fatalf("gao board board -json: exit %d, want 2", code)
	}

	var got struct {
		Roster string  `json:"roster"`
		Edge   float64 `json:"edge"`
		Arms   []struct {
			Origin     string  `json:"origin"`
			Benchmarks int     `json:"benchmarks"`
			Margin     float64 `json:"margin"`
		} `json:"arms"`
		Unpinned []string `json:"unpinned"`
		Holds    bool     `json:"holds"`
		Verdict  string   `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Roster == "" || got.Verdict == "" {
		t.Errorf("the JSON names no roster or has no verdict: %+v", got)
	}
	if len(got.Arms) != 3 {
		t.Fatalf("the JSON has %d arms, the roster has native, translated and neutral", len(got.Arms))
	}
	if got.Arms[0].Origin != nhat.Native || got.Arms[0].Benchmarks != 16 {
		t.Errorf("the first arm is %s over %d benchmarks", got.Arms[0].Origin, got.Arms[0].Benchmarks)
	}
	if got.Holds {
		t.Error("the JSON says a board with unpinned rows on it holds")
	}
	if len(got.Unpinned) == 0 {
		t.Error("the JSON carries no unpinned rows")
	}
}

func TestBangHoldsWhenEveryRowCanBeRunAgain(t *testing.T) {
	// The same board against a roster where nothing is waiting on an address,
	// which is what this one will read like once nhat's pending list is empty.
	dir := t.TempDir()
	ros := nhat.Roster{Version: "2026-08-12", Note: "every entry pinned"}
	for i, e := range []struct {
		name   string
		origin string
	}{
		{"vmlu", nhat.Native}, {"vimmrc", nhat.Native}, {"uit-vsfc", nhat.Native},
		{"visfd", nhat.Native}, {"vihsd", nhat.Native}, {"victsd", nhat.Native},
		{"mmlu-vi", nhat.Translated}, {"arc-vi", nhat.Translated},
	} {
		ros.Benchmarks = append(ros.Benchmarks, nhat.Entry{
			Name:    e.name,
			Version: strings.Repeat(fmt.Sprintf("%d", i), 40),
			Home:    "hf:someone/" + e.name,
			Origin:  e.origin,
		})
	}
	b, err := json.Marshal(ros)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "roster.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	lines := make([]string, 0, len(ros.Benchmarks))
	for _, e := range ros.Benchmarks {
		lines = append(lines, fmt.Sprintf(
			`{"benchmark":%q,"score":54.0,"baseline":50.0,"against":"sailor2-8b","runs":3,"spread":0.4,"box":"gamingpc","measured_on":"2026-08-12"}`,
			e.Name))
	}

	out, errOut, code := exec(t, "bang", "board", "-roster", path, bangScores(t, lines...))
	if code != 0 {
		t.Fatalf("gao board board on a pinned roster: exit %d, %s\n%s", code, errOut, out)
	}
	if strings.Contains(out, "cannot be published") {
		t.Errorf("a board with nothing wrong with it refuses to be published:\n%s", out)
	}
	if !strings.Contains(out, "The two arms are not added together") {
		t.Errorf("the verdict does not say what it is not doing:\n%s", out)
	}
}

func TestBangRefusesScoresThatAreNotAScoreboard(t *testing.T) {
	path := bangScores(t,
		`{"benchmark":"vmlu","score":58.2,"baseline":54.1,"against":"sailor2-8b","runs":3,"spread":0.4,"box":"gamingpc","measured_on":"2026-08-12"}`,
		`{"benchmark":"vimmrc","score":61.4,"baseline":58.9,"against":"sailor2-8b","runs":3,"spread":0.6,"box":"gamingpc","measured_on":"2026-08-12"}`,
	)

	out, _, code := exec(t, "bang", "board", path)
	if code != 1 {
		t.Fatalf("gao board board on two of twenty four benchmarks: exit %d, want 1", code)
	}
	if !strings.Contains(out, "chosen after the results") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
	if strings.Contains(out, "against the baseline") {
		t.Errorf("a refused board still prints an arm:\n%s", out)
	}
}

func TestBangSaysWhichLineOfTheScoresIsWrong(t *testing.T) {
	path := bangScores(t,
		`{"benchmark":"vmlu","score":58.2,"baseline":54.1,"against":"sailor2-8b","runs":3,"spread":0.4,"box":"gamingpc","measured_on":"2026-08-12"}`,
		`{"benchmark":"vimmrc","score":61.4,"baseline":58.9,"against":"sailor2-8b","runs":3,"f1":0.81}`,
	)

	_, errOut, code := exec(t, "bang", "board", path)
	if code != 1 {
		t.Fatalf("gao board board on a file it cannot read: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, ":2:") || !strings.Contains(errOut, "f1") {
		t.Errorf("the error does not say which line or which field: %q", errOut)
	}
}

func TestBangWithoutASubcommandSaysWhatItTakes(t *testing.T) {
	for _, args := range [][]string{
		{"bang"},
		{"bang", "publish"},
		{"bang", "board"},
		{"bang", "board", "one.jsonl", "two.jsonl"},
	} {
		_, errOut, code := exec(t, args...)
		if code != 2 {
			t.Errorf("gao %s: exit %d, want 2", strings.Join(args, " "), code)
		}
		if errOut == "" {
			t.Errorf("gao %s: exit 2 with nothing on stderr", strings.Join(args, " "))
		}
	}
}

func TestBangHelpSaysWhyTheArmsAreNotAdded(t *testing.T) {
	out, _, code := exec(t, "bang", "help")
	if code != 0 {
		t.Fatalf("gao board help: exit %d", code)
	}
	if !strings.Contains(out, "never added together") {
		t.Errorf("the help does not say what the board refuses to do:\n%s", out)
	}
}
