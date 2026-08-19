package compare_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/compare"
)

func write(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pairs.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestJudgementsAreReadInTheOrderTheyWereCollected(t *testing.T) {
	path := write(t,
		`{"item":"prompt-0001","rater":"r01","left":"com-8b-sft-native","right":"com-8b-sft-translated","left_syllables":142,"right_syllables":118,"choice":"left"}`,
		``,
		`{"item":"prompt-0001","rater":"r02","left":"com-8b-sft-translated","right":"com-8b-sft-native","left_syllables":118,"right_syllables":142,"choice":"tie"}`,
	)

	pairs, err := compare.ReadPairs(path)
	if err != nil {
		t.Fatalf("reading two judgements: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("%d judgements came back, want 2", len(pairs))
	}
	if pairs[0].Rater != "r01" || pairs[1].Rater != "r02" {
		t.Errorf("the judgements came back as %s then %s", pairs[0].Rater, pairs[1].Rater)
	}
	if pairs[0].Choice != compare.Left || pairs[1].Choice != compare.Tie {
		t.Error("what the raters picked did not survive the round trip")
	}
	if pairs[0].LeftSyllables != 142 {
		t.Errorf("the length of the left hand answer came back as %d", pairs[0].LeftSyllables)
	}
}

// A protocol file is exactly the sort of thing somebody extends with a second
// question for the raters, and a reader that skips the column it does not know
// reports the answer to one question as though it were the answer to the
// protocol.
func TestAColumnTheReaderDoesNotKnowAboutIsAnError(t *testing.T) {
	path := write(t, `{"item":"prompt-0001","rater":"r01","left":"a","right":"b","choice":"left","fluency":4}`)

	if _, err := compare.ReadPairs(path); err == nil {
		t.Fatal("a judgement carrying a column nobody reads was accepted")
	} else if !strings.Contains(err.Error(), "fluency") {
		t.Errorf("the error does not name the column: %v", err)
	}
}

func TestABadLineIsNamedByItsLineNumber(t *testing.T) {
	path := write(t,
		`{"item":"prompt-0001","rater":"r01","left":"a","right":"b","choice":"left"}`,
		`{"item":"prompt-0002","rater":"r01","left":"a","right":"b","left_syllables":"long"}`,
	)

	_, err := compare.ReadPairs(path)
	if err == nil {
		t.Fatal("a length that is not a number was accepted")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("the error does not say which line is wrong: %v", err)
	}
}

// A choice nobody recognizes is read rather than rejected, because the reader's
// job is to say what is in the file and the refusal that names the rater and the
// item is more use than a parse error that names a byte offset.
func TestAChoiceTheProtocolDoesNotDefineIsReadAndRefusedLater(t *testing.T) {
	path := write(t, `{"item":"prompt-0001","rater":"r01","left":"a","right":"b","choice":"better"}`)

	pairs, err := compare.ReadPairs(path)
	if err != nil {
		t.Fatalf("reading a choice nobody defined: %v", err)
	}
	why := compare.Read(pairs).Blocking()
	found := false
	for _, l := range why {
		if strings.Contains(l, `"better" on prompt-0001`) {
			found = true
		}
	}
	if !found {
		t.Errorf("the refusal does not name the choice and the item:\n  %s", strings.Join(why, "\n  "))
	}
}

func TestAFileThatIsNotThereSaysSo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.jsonl")

	_, err := compare.ReadPairs(missing)
	if err == nil {
		t.Fatal("a file that does not exist was read")
	}
	if !strings.Contains(err.Error(), "nope.jsonl") {
		t.Errorf("the error does not name the file: %v", err)
	}
}
