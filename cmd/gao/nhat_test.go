package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One benchmark item, and prose that shares nothing with it.
const (
	nhatItem  = "Thủ đô của nước Việt Nam là thành phố nào sau đây trong bốn lựa chọn"
	nhatOther = "Diện tích của đồng bằng sông Cửu Long lớn hơn diện tích của đồng bằng sông Hồng bao nhiêu lần"
	nhatProse = "Buổi sáng hôm ấy trời trở lạnh và những người bán hàng rong đi ngang qua con phố nhỏ."
)

// writeJSON puts a roster or a list where the command can read it.
func writeJSON(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

// nhatRoster is a two benchmark roster, one native and one translated, so that a
// report has both to show.
func nhatRoster(t *testing.T, dir string) string {
	t.Helper()
	return writeJSON(t, dir, "roster.json", `{
	  "version": "test-1",
	  "benchmarks": [
	    {"name": "vmlu", "version": "1", "origin": "native", "source": "vmlu.ai", "note": "questions and options"},
	    {"name": "mmlu-vi", "version": "1", "origin": "translated", "source": "the harness", "note": "questions and options"}
	  ]
	}`)
}

func nhatList(t *testing.T, dir string) string {
	t.Helper()
	return writeJSON(t, dir, "list.json", `{
	  "version": "list-1",
	  "roster": "test-1",
	  "benchmarks": [
	    {"name": "vmlu", "version": "1", "origin": "native", "items": ["`+nhatItem+`"]},
	    {"name": "mmlu-vi", "version": "1", "origin": "translated", "items": ["`+nhatOther+`"]}
	  ]
	}`)
}

// The roster is the answer to what gao is judged on, and it prints without a
// corpus, a list, or anything downloaded.
func TestNhatPrintsTheRosterInTheRepository(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runNhat(&stdout, &stderr, []string{"-benchmarks"}); code != 0 {
		t.Fatalf("gao nhat -benchmarks = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"vmlu", "mmlu-vi", "translated", "held out", "It only grows"} {
		if !strings.Contains(out, want) {
			t.Errorf("the roster does not mention %q\n%s", want, out)
		}
	}
}

func TestNhatPrintsTheRosterAsJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runNhat(&stdout, &stderr, []string{"-benchmarks", "-json"}); code != 0 {
		t.Fatalf("gao nhat -benchmarks -json = %d, want 0\n%s", code, stderr.String())
	}
	var got struct {
		Version    string `json:"version"`
		Benchmarks []struct {
			Name    string `json:"name"`
			Origin  string `json:"origin"`
			HeldOut bool   `json:"held_out"`
		} `json:"benchmarks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the roster is not JSON: %v\n%s", err, stdout.String())
	}
	if got.Version == "" {
		t.Error("the roster came out with no version")
	}
	if len(got.Benchmarks) < 13 {
		t.Errorf("the roster holds %d benchmarks, and doc 10 names thirteen standard ones", len(got.Benchmarks))
	}
}

func TestNhatFindsATestItemInADocument(t *testing.T) {
	dir := t.TempDir()
	contaminated := writeText(t, dir, "a.txt", nhatProse+"\n\n"+nhatItem+"\n")
	clean := writeText(t, dir, "b.txt", nhatProse+"\n")

	var stdout, stderr bytes.Buffer
	code := runNhat(&stdout, &stderr, []string{
		"-roster", nhatRoster(t, dir), "-list", nhatList(t, dir), "-json", contaminated, clean,
	})
	if code != 0 {
		t.Fatalf("gao nhat = %d, want 0, since finding contamination is a result rather than an error\n%s", code, stderr.String())
	}
	var got nhatRun
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, stdout.String())
	}
	if got.Tally.Documents != 2 {
		t.Errorf("checked %d documents, want 2", got.Tally.Documents)
	}
	if got.Tally.Flagged != 1 || got.Tally.Dropped != 1 {
		t.Errorf("flagged %d and dropped %d, want 1 and 1", got.Tally.Flagged, got.Tally.Dropped)
	}
	if got.Roster != "test-1" || got.List != "list-1" {
		t.Errorf("the report says roster %q and list %q, and a contamination table that cannot name them cannot be read later", got.Roster, got.List)
	}
	if len(got.Tally.Benchmarks) != 2 {
		t.Fatalf("the report holds %d rows, want one per benchmark", len(got.Tally.Benchmarks))
	}
	if got.Tally.Benchmarks[0].ItemsTouched != 1 {
		t.Errorf("vmlu reports %d items found, want 1", got.Tally.Benchmarks[0].ItemsTouched)
	}
	if got.Tally.Benchmarks[1].Documents != 0 {
		t.Errorf("mmlu-vi was found in %d documents, and nothing in the corpus holds it", got.Tally.Benchmarks[1].Documents)
	}
}

// A table holding only the contaminated benchmarks cannot be read as a clean
// bill of health for the rest, so the clean ones are in it too.
func TestNhatReportsTheBenchmarksNothingTouched(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", nhatProse+"\n\n"+nhatItem+"\n")

	var stdout, stderr bytes.Buffer
	if code := runNhat(&stdout, &stderr, []string{
		"-roster", nhatRoster(t, dir), "-list", nhatList(t, dir), path,
	}); code != 0 {
		t.Fatalf("gao nhat = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"vmlu", "mmlu-vi", "translated", "stay in the eval table"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q\n%s", want, out)
		}
	}
}

func TestNhatSaysWhenItFoundNothing(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", nhatProse+"\n")

	var stdout, stderr bytes.Buffer
	if code := runNhat(&stdout, &stderr, []string{
		"-roster", nhatRoster(t, dir), "-list", nhatList(t, dir), path,
	}); code != 0 {
		t.Fatalf("gao nhat = %d, want 0\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Nothing was found") {
		t.Errorf("a clean run does not say so\n%s", stdout.String())
	}
}

// The list is built by a fetch that can fail one benchmark at a time, and a
// benchmark that failed to fetch produces the same report as a clean one. So the
// run stops before the scan rather than after it.
func TestNhatRefusesAListThatIsMissingARosteredBenchmark(t *testing.T) {
	dir := t.TempDir()
	short := writeJSON(t, dir, "short.json", `{
	  "version": "list-1",
	  "roster": "test-1",
	  "benchmarks": [
	    {"name": "vmlu", "version": "1", "origin": "native", "items": ["`+nhatItem+`"]}
	  ]
	}`)
	path := writeText(t, dir, "a.txt", nhatProse+"\n")

	var stdout, stderr bytes.Buffer
	code := runNhat(&stdout, &stderr, []string{"-roster", nhatRoster(t, dir), "-list", short, path})
	if code != 1 {
		t.Fatalf("gao nhat = %d, want 1\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "mmlu-vi") {
		t.Errorf("it does not name the benchmark that is missing: %s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("it printed a report anyway:\n%s", stdout.String())
	}
}

func TestNhatShowsTheDocumentsItFlagged(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", nhatProse+"\n\n"+nhatItem+"\n")

	var stdout, stderr bytes.Buffer
	if code := runNhat(&stdout, &stderr, []string{
		"-roster", nhatRoster(t, dir), "-list", nhatList(t, dir), "-show", "5", path,
	}); code != 0 {
		t.Fatalf("gao nhat -show 5 = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "a.txt") {
		t.Errorf("the flagged document is not named\n%s", out)
	}
	if !strings.Contains(out, "dropped") {
		t.Errorf("the report does not say what happened to it\n%s", out)
	}
}

// A part holds many documents, so a flagged one is named by the row it is in.
func TestNhatNamesTheRowOfAPart(t *testing.T) {
	dir := t.TempDir()
	part := writeHostedPart(t,
		hostedRow{host: "vnbao.vn", text: nhatProse + "\n"},
		hostedRow{host: "vnbao.vn", text: nhatProse + "\n\n" + nhatItem + "\n"},
	)

	var stdout, stderr bytes.Buffer
	if code := runNhat(&stdout, &stderr, []string{
		"-roster", nhatRoster(t, dir), "-list", nhatList(t, dir), "-show", "5", part,
	}); code != 0 {
		t.Fatalf("gao nhat = %d, want 0\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "#1") {
		t.Errorf("the flagged row of the part is not named\n%s", stdout.String())
	}
}

func TestNhatNeedsAListToCheckAgainst(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", nhatProse+"\n")

	var stdout, stderr bytes.Buffer
	if code := runNhat(&stdout, &stderr, []string{path}); code != 2 {
		t.Fatalf("gao nhat with no list = %d, want 2\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-benchmarks") {
		t.Errorf("it does not say what to do instead: %s", stderr.String())
	}
}

func TestNhatNeedsSomethingToCheck(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runNhat(&stdout, &stderr, []string{"-roster", nhatRoster(t, dir), "-list", nhatList(t, dir)}); code != 2 {
		t.Fatalf("gao nhat with no files = %d, want 2\n%s", code, stderr.String())
	}
}

func TestNhatBenchmarksTakesNoFiles(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", nhatProse+"\n")

	var stdout, stderr bytes.Buffer
	if code := runNhat(&stdout, &stderr, []string{"-benchmarks", path}); code != 2 {
		t.Fatalf("gao nhat -benchmarks a.txt = %d, want 2\n%s", code, stderr.String())
	}
}

func TestNhatSaysWhenTheRosterIsNotThere(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runNhat(&stdout, &stderr, []string{"-roster", filepath.Join(dir, "gone.json"), "-benchmarks"}); code != 1 {
		t.Fatalf("gao nhat with a roster that is not there = %d, want 1\n%s", code, stderr.String())
	}
}
