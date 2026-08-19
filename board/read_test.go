package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scores.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScoresAreReadInTheOrderTheyWereWritten(t *testing.T) {
	path := write(t,
		`{"benchmark":"vmlu","score":58.2,"baseline":54.1,"against":"sailor2-8b-chat","runs":3,"spread":0.4,"box":"gamingpc","measured_on":"2026-08-12"}`,
		``,
		`{"benchmark":"mmlu-vi","score":61.0,"baseline":57.4,"against":"sailor2-8b-chat","runs":3,"spread":0.6,"box":"gamingpc","measured_on":"2026-08-12"}`,
	)

	got, err := ReadScores(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d scores from two lines and a blank one", len(got))
	}
	if got[0].Benchmark != "vmlu" || got[1].Benchmark != "mmlu-vi" {
		t.Errorf("read %s then %s", got[0].Benchmark, got[1].Benchmark)
	}
	if got[0].Score != 58.2 || got[0].Baseline != 54.1 || got[0].Runs != 3 {
		t.Errorf("read %+v", got[0])
	}
	if got[1].Box != "gamingpc" || got[1].On != "2026-08-12" {
		t.Errorf("read %+v", got[1])
	}
}

func TestAColumnTheBoardDoesNotKnowAboutIsAnError(t *testing.T) {
	// The reason to refuse it: somebody adds a second metric, the board keeps
	// reading, and the arms are averaged over a column nobody looked at.
	path := write(t, `{"benchmark":"vmlu","score":58.2,"baseline":54.1,"against":"sailor2-8b-chat","runs":3,"f1":0.81}`)

	_, err := ReadScores(path)
	if err == nil {
		t.Fatal("a score with a field nobody reads was accepted")
	}
	if !strings.Contains(err.Error(), "f1") {
		t.Errorf("the error does not name the field: %v", err)
	}
	if !strings.Contains(err.Error(), ":1:") {
		t.Errorf("the error does not say which line: %v", err)
	}
}

func TestABadLineIsNamedByItsLineNumber(t *testing.T) {
	path := write(t,
		`{"benchmark":"vmlu","score":58.2,"baseline":54.1,"against":"sailor2-8b-chat","runs":3}`,
		`{"benchmark":"mmlu-vi",`,
	)

	_, err := ReadScores(path)
	if err == nil {
		t.Fatal("half a line of JSON was accepted")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("the error does not point at line 2: %v", err)
	}
}

func TestAFileThatIsNotThereSaysSo(t *testing.T) {
	if _, err := ReadScores(filepath.Join(t.TempDir(), "nothing.jsonl")); err == nil {
		t.Fatal("a file that does not exist read as no scores")
	}
}
