package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDocument(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// With no flags it is a filter, so that a document can be piped through it and
// looked at. Everything else the command does is a report about that.
func TestPhoiWritesTheNormalizedText(t *testing.T) {
	path := writeDocument(t, "Hoà bình\r\n")

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{path}); code != 0 {
		t.Fatalf("gao phoi = %d, want 0\n%s", code, stderr.String())
	}
	if got := stdout.String(); got != "Hòa bình\n" {
		t.Errorf("gao phoi wrote %q, want %q", got, "Hòa bình\n")
	}
}

func TestPhoiReportsWhatItDid(t *testing.T) {
	path := writeDocument(t, "Hoà bình và dduwowngj dài\n")

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{"-report", path}); code != 0 {
		t.Fatalf("gao phoi -report = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"tones", "residue", "syllables", "input method keystrokes"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Hòa") {
		t.Errorf("the report printed the text as well as the counts:\n%s", out)
	}
}

// A document this stage drops is dropped for one of two reasons, and a report
// that said only that it was dropped would send whoever read it through the
// counts looking for which limit was the one.
func TestPhoiSaysWhyADocumentDoesNotGoOn(t *testing.T) {
	path := writeDocument(t, strings.Repeat("dduwowngj ddaji hocj ", 20)+"\n")

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{"-report", path}); code != 0 {
		t.Fatalf("gao phoi -report = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "no, residue") {
		t.Errorf("the report does not name the reason in the table:\n%s", out)
	}
	if !strings.Contains(out, "does not go on") {
		t.Errorf("the report does not say the document was dropped:\n%s", out)
	}
}

func TestPhoiReportsSeveralDocumentsWithATotal(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for name, text := range map[string]string{
		"a.txt": "Hoà bình\n",
		"b.txt": "Thuỷ chung\n",
		"c.txt": "Hà Nội\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, append([]string{"-report"}, paths...)); code != 0 {
		t.Fatalf("gao phoi -report = %d, want 0\n%s", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "3 documents") {
		t.Errorf("the report has no total line:\n%s", got)
	}
}

func TestPhoiReportsJSON(t *testing.T) {
	path := writeDocument(t, "Hoà bình\n")

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{"-report", "-json", path}); code != 0 {
		t.Fatalf("gao phoi -report -json = %d, want 0\n%s", code, stderr.String())
	}
	var got phoiReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, stdout.String())
	}
	if len(got.Documents) != 1 {
		t.Fatalf("the report holds %d documents, want 1", len(got.Documents))
	}
	if got.Documents[0].Tones != 1 {
		t.Errorf("the report says %d tone marks moved, want 1", got.Documents[0].Tones)
	}
	if got.Total.Documents != 1 || got.Total.Changed != 1 {
		t.Errorf("the total is %+v, want one document and one changed", got.Total)
	}
}

// A flag that does nothing on its own is a flag somebody will believe did
// something, so it is an error rather than a no-op.
func TestPhoiRefusesJSONWithoutReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{"-json"}); code != 2 {
		t.Fatalf("gao phoi -json = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-report") {
		t.Errorf("the error does not say what to do about it: %q", stderr.String())
	}
}

func TestPhoiSaysWhichFileItCouldNotRead(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{filepath.Join(t.TempDir(), "gone.txt")}); code != 1 {
		t.Fatalf("gao phoi = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "gone.txt") {
		t.Errorf("the error does not name the file: %q", stderr.String())
	}
}

func TestPhoiIsInTheUsage(t *testing.T) {
	var stdout bytes.Buffer
	usage(&stdout)
	if !strings.Contains(stdout.String(), "phoi") {
		t.Errorf("gao help does not list phoi:\n%s", stdout.String())
	}
}
