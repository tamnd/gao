package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
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
	documents := map[string]string{
		"a.txt": "Hoà bình\n",
		"b.txt": "Thuỷ chung\n",
		"c.txt": "Hà Nội\n",
	}
	paths := make([]string, 0, len(documents))
	for name, text := range documents {
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

// writePart writes a parquet part holding one row per text, which is the shape
// the ingest writes and the shape a stage is pointed at.
func writePart(t *testing.T, texts ...string) string {
	t.Helper()
	d, ok := kho.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the dataset is not in the table")
	}
	dir := t.TempDir()
	part, err := kho.CreatePart(dir, "part-00000.parquet", d, kho.Stamp{
		Snapshot: "gao-v1.0", Stage: "test@0.1.0", Box: "server1",
	})
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	for i, text := range texts {
		row := document(t, i)
		row.Text = text
		row.DocID = doc.SumString(text)
		row.NChars = uint32(len([]rune(text)))
		if err := part.Append(row); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	file, err := part.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return filepath.Join(dir, file.Path)
}

// The material this stage runs on arrives as parts, so a report that only reads
// text files can be run on the fixtures and not on the corpus.
func TestPhoiReportsEveryRowOfAPart(t *testing.T) {
	path := writePart(t, "Hoà bình\n", "Hà Nội\n", "Thuỷ chung\n")

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{"-report", "-json", path}); code != 0 {
		t.Fatalf("gao phoi -report PART = %d, want 0\n%s", code, stderr.String())
	}
	var got phoiReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, stdout.String())
	}
	if len(got.Documents) != 3 {
		t.Fatalf("the part holds three rows and the report has %d", len(got.Documents))
	}
	if got.Total.Changed != 2 {
		t.Errorf("the report says %d of the rows changed, want the two with a moved tone mark", got.Total.Changed)
	}
	if !strings.HasSuffix(got.Documents[1].Name, "#1") {
		t.Errorf("the second row is named %q, and a row of a part needs its number", got.Documents[1].Name)
	}
}

// A part off the fleet holds a few hundred thousand rows. Printing a line each
// is not a report, it is the corpus again, and holding those lines to print
// them is what a run over the whole corpus cannot afford.
func TestPhoiPrintsATotalWithoutALinePerDocument(t *testing.T) {
	path := writePart(t, "Hoà bình\n", "Hà Nội\n", "Thuỷ chung\n")

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{"-report", "-total", path}); code != 0 {
		t.Fatalf("gao phoi -report -total = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "3 documents") {
		t.Errorf("the total line is not there:\n%s", out)
	}
	if strings.Contains(out, "#0") {
		t.Errorf("-total printed a line per document anyway:\n%s", out)
	}
	if !strings.Contains(out, "changed 66.7% of the documents") {
		t.Errorf("the report does not say what share changed:\n%s", out)
	}
}

// Layout runs on every document, so the share that changed at all is a fact
// about trailing whitespace and the share that had a character repaired is a
// fact about the material. A report that gave only the first would be quoted as
// if it were the second.
func TestPhoiSeparatesRepairFromLayout(t *testing.T) {
	path := writePart(t, "Hoà bình\n", "Thuỷ chung\n", "Hà Nội  \n", "Hà Nội\n")

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{"-report", "-total", path}); code != 0 {
		t.Fatalf("gao phoi -report -total = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "changed 75.0% of the documents by at least one byte, and 50.0% of them by something other than layout") {
		t.Errorf("the report does not separate the two:\n%s", out)
	}
}

func TestPhoiRefusesTotalWithoutReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{"-total"}); code != 2 {
		t.Fatalf("gao phoi -total = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-report") {
		t.Errorf("the error does not say what to do about it: %q", stderr.String())
	}
}

// As a filter it writes the normalized text, and many documents down one stream
// is a file that has lost where each of them ended.
func TestPhoiRefusesToNormalizeAPartOntoOneStream(t *testing.T) {
	path := writePart(t, "Hoà bình\n", "Hà Nội\n")

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{path}); code != 2 {
		t.Fatalf("gao phoi PART = %d, want 2\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-report") {
		t.Errorf("the error does not say what to do instead: %q", stderr.String())
	}
	if stdout.Len() > 0 {
		t.Errorf("it wrote text before refusing:\n%s", stdout.String())
	}
}

func TestPhoiIsInTheUsage(t *testing.T) {
	var stdout bytes.Buffer
	usage(&stdout)
	if !strings.Contains(stdout.String(), "phoi") {
		t.Errorf("gao help does not list phoi:\n%s", stdout.String())
	}
}

// tcvn3Page and vniPage are one Vietnamese paragraph as a crawler hands it over
// after reading a TCVN3 page and a VNI page as windows-1252. They are written
// out as the characters somebody sees on a broken page rather than as bytes,
// because that is the form anybody checking them would recognize. The one escape
// is TCVN3's ư, which is the soft hyphen and would otherwise be invisible here.
const (
	tcvn3Page = "Hµ Néi mïa nµy trêi trë l¹nh, vµ nh÷ng ng\u00adêi ®i lµm vÒ muén " +
		"vÉn dõng l¹i ë gãc phè cò ®Ó mua mét gãi x«i.\n"
	vniPage = "Haø Noäi muøa naøy trôøi trôû laïnh, vaø nhöõng ngöôøi ñi laøm veà muoän " +
		"vaãn döøng laïi ôû goùc phoá cuõ ñeå mua moät goùi xoâi.\n"
	unicodePage = "Hà Nội mùa này trời trở lạnh, và những người đi làm về muộn " +
		"vẫn dừng lại ở góc phố cũ để mua một gói xôi.\n"
)

// A page in a font encoding is the one thing this stage does that rewrites a
// document from end to end, so the report has to say it happened and say which
// encoding it was.
func TestPhoiNamesTheFontEncodingItReadADocumentOutOf(t *testing.T) {
	path := writePart(t, tcvn3Page, "Hà Nội\n")

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{"-report", path}); code != 0 {
		t.Fatalf("gao phoi -report = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "TCVN3") {
		t.Errorf("the report does not name the encoding:\n%s", out)
	}
	if !strings.Contains(out, "One document was written in a font encoding rather than in Unicode, and it was TCVN3.") {
		t.Errorf("the report does not say how many documents were in one:\n%s", out)
	}
}

// The text a font encoding is read out of is the text every later stage sees, so
// the filter has to hand back Vietnamese and not the mojibake it was given.
func TestPhoiWritesTheTranscodedText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.txt")
	if err := os.WriteFile(path, []byte(tcvn3Page), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{path}); code != 0 {
		t.Fatalf("gao phoi = %d, want 0\n%s", code, stderr.String())
	}
	if stdout.String() != unicodePage {
		t.Errorf("the filter wrote\n%q\nwant\n%q", stdout.String(), unicodePage)
	}
}

// A source is rarely in one encoding. The breakdown is what says whether a crawl
// reached the archives of one publisher or of several, so the sentence has to
// hold every encoding the run met and put the most of them first.
func TestPhoiBreaksTheFontEncodingsDownByName(t *testing.T) {
	path := writePart(t, tcvn3Page, tcvn3Page, vniPage)

	var stdout, stderr bytes.Buffer
	if code := runPhoi(&stdout, &stderr, []string{"-report", "-total", path}); code != 0 {
		t.Fatalf("gao phoi -report -total = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "3 documents were written in a font encoding rather than in Unicode: 2 in TCVN3, 1 in VNI-WIN.") {
		t.Errorf("the report does not break the encodings down:\n%s", out)
	}
}
