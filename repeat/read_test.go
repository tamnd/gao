package repeat_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/repeat"
)

func writeDocs(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The order on disk is the order the run happened in, and the whole measurement
// is about what came later, so a reader that sorted would be reading a
// different set.
func TestDocumentsComeBackInTheOrderTheyWereGenerated(t *testing.T) {
	path := writeDocs(t,
		`{"id":"synth-0002","prompt":"p01","domain":"administrative","text":"hồ sơ nộp tại ủy ban","kept":true}`,
		``,
		`{"id":"synth-0001","prompt":"p02","text":"giấy tờ còn giá trị","kept":false}`,
	)

	docs, err := repeat.ReadDocs(path)
	if err != nil {
		t.Fatalf("reading two documents: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("%d documents came back, want 2", len(docs))
	}
	if docs[0].ID != "synth-0002" || docs[1].ID != "synth-0001" {
		t.Errorf("the documents came back as %s then %s", docs[0].ID, docs[1].ID)
	}
	if !docs[0].Kept || docs[1].Kept {
		t.Error("what the generator's own filter decided did not survive the round trip")
	}
	if docs[0].Domain != "administrative" {
		t.Errorf("the domain came back as %q", docs[0].Domain)
	}
}

// A generation file is the sort of thing somebody extends with a second filter's
// verdict, and a reader that skips the column it does not know reports one
// filter's reject rate as though it were the reject rate.
func TestAColumnTheReaderDoesNotKnowAboutIsAnError(t *testing.T) {
	path := writeDocs(t, `{"id":"synth-0001","prompt":"p01","text":"hồ sơ","kept":true,"score":0.91}`)

	if _, err := repeat.ReadDocs(path); err == nil {
		t.Fatal("a document carrying a column nobody reads was accepted")
	} else if !strings.Contains(err.Error(), "score") {
		t.Errorf("the error does not name the column: %v", err)
	}
}

func TestABadLineIsNamedByItsLineNumber(t *testing.T) {
	path := writeDocs(t,
		`{"id":"synth-0001","prompt":"p01","text":"hồ sơ","kept":true}`,
		`{"id":"synth-0002","kept":"yes"}`,
	)

	_, err := repeat.ReadDocs(path)
	if err == nil {
		t.Fatal("a verdict that is not a boolean was accepted")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("the error does not say which line is wrong: %v", err)
	}
}

// A generated document is a document rather than a log line, so the reader is
// given room for one.
func TestADocumentLongerThanAScannerLineIsReadWhole(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("thủ tục hành chính công dân ", 40_000))
	path := writeDocs(t, `{"id":"synth-0001","prompt":"p01","kept":true,"text":"`+long+`"}`)

	docs, err := repeat.ReadDocs(path)
	if err != nil {
		t.Fatalf("reading a document of %d bytes: %v", len(long), err)
	}
	if len(docs) != 1 || docs[0].Text != long {
		t.Errorf("a document of %d bytes came back as %d documents holding %d bytes", len(long), len(docs), len(docs[0].Text))
	}
}

func TestAFileThatIsNotThereSaysSo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.jsonl")

	_, err := repeat.ReadDocs(missing)
	if err == nil {
		t.Fatal("a file that does not exist was read")
	}
	if !strings.Contains(err.Error(), "nope.jsonl") {
		t.Errorf("the error does not name the file: %v", err)
	}
}
