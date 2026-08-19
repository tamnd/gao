package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/pick"
)

// One benchmark item, and prose that shares nothing with it.
const (
	pickItem  = "Thủ đô của nước Việt Nam là thành phố nào sau đây trong bốn lựa chọn"
	pickOther = "Diện tích của đồng bằng sông Cửu Long lớn hơn diện tích của đồng bằng sông Hồng bao nhiêu lần"
	pickProse = "Buổi sáng hôm ấy trời trở lạnh và những người bán hàng rong đi ngang qua con phố nhỏ."
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

// The revisions in these fixtures are object ids because that is what a roster
// takes. They are made up rather than real, and they are not the same on the two
// rows, so a report that printed one where the other belongs would show it.
const (
	pickVMLU = "1111111111111111111111111111111111111111"
	pickMMLU = "2222222222222222222222222222222222222222"
)

// pickRoster is a two benchmark roster, one native and one translated, so that a
// report has both to show.
func pickRoster(t *testing.T, dir string) string {
	t.Helper()
	return writeJSON(t, dir, "roster.json", `{
	  "version": "test-1",
	  "benchmarks": [
	    {"name": "vmlu", "version": "`+pickVMLU+`", "home": "git:https://example.vn/vmlu", "origin": "native", "source": "vmlu.ai", "note": "questions and options"},
	    {"name": "mmlu-vi", "version": "`+pickMMLU+`", "home": "hf:example/mmlu-vi", "origin": "translated", "source": "the harness", "note": "questions and options"}
	  ]
	}`)
}

func pickList(t *testing.T, dir string) string {
	t.Helper()
	return writeJSON(t, dir, "list.json", `{
	  "version": "list-1",
	  "roster": "test-1",
	  "benchmarks": [
	    {"name": "vmlu", "version": "`+pickVMLU+`", "origin": "native", "items": ["`+pickItem+`"]},
	    {"name": "mmlu-vi", "version": "`+pickMMLU+`", "origin": "translated", "items": ["`+pickOther+`"]}
	  ]
	}`)
}

// The roster is the answer to what gao is judged on, and it prints without a
// corpus, a list, or anything downloaded.
func TestPickPrintsTheRosterInTheRepository(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPick(&stdout, &stderr, []string{"-benchmarks"}); code != 0 {
		t.Fatalf("gao pick -benchmarks = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"vmlu", "mmlu-vi", "translated", "held out", "It only grows"} {
		if !strings.Contains(out, want) {
			t.Errorf("the roster does not mention %q\n%s", want, out)
		}
	}
}

func TestPickPrintsTheRosterAsJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPick(&stdout, &stderr, []string{"-benchmarks", "-json"}); code != 0 {
		t.Fatalf("gao pick -benchmarks -json = %d, want 0\n%s", code, stderr.String())
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

func TestPickFindsATestItemInADocument(t *testing.T) {
	dir := t.TempDir()
	contaminated := writeText(t, dir, "a.txt", pickProse+"\n\n"+pickItem+"\n")
	clean := writeText(t, dir, "b.txt", pickProse+"\n")

	var stdout, stderr bytes.Buffer
	code := runPick(&stdout, &stderr, []string{
		"-roster", pickRoster(t, dir), "-list", pickList(t, dir), "-json", contaminated, clean,
	})
	if code != 0 {
		t.Fatalf("gao pick = %d, want 0, since finding contamination is a result rather than an error\n%s", code, stderr.String())
	}
	var got pickRun
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
func TestPickReportsTheBenchmarksNothingTouched(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", pickProse+"\n\n"+pickItem+"\n")

	var stdout, stderr bytes.Buffer
	if code := runPick(&stdout, &stderr, []string{
		"-roster", pickRoster(t, dir), "-list", pickList(t, dir), path,
	}); code != 0 {
		t.Fatalf("gao pick = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"vmlu", "mmlu-vi", "translated", "stay in the eval table"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q\n%s", want, out)
		}
	}
}

func TestPickSaysWhenItFoundNothing(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", pickProse+"\n")

	var stdout, stderr bytes.Buffer
	if code := runPick(&stdout, &stderr, []string{
		"-roster", pickRoster(t, dir), "-list", pickList(t, dir), path,
	}); code != 0 {
		t.Fatalf("gao pick = %d, want 0\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Nothing was found") {
		t.Errorf("a clean run does not say so\n%s", stdout.String())
	}
}

// The list is built by a fetch that can fail one benchmark at a time, and a
// benchmark that failed to fetch produces the same report as a clean one. So the
// run stops before the scan rather than after it.
func TestPickRefusesAListThatIsMissingARosteredBenchmark(t *testing.T) {
	dir := t.TempDir()
	short := writeJSON(t, dir, "short.json", `{
	  "version": "list-1",
	  "roster": "test-1",
	  "benchmarks": [
	    {"name": "vmlu", "version": "`+pickVMLU+`", "origin": "native", "items": ["`+pickItem+`"]}
	  ]
	}`)
	path := writeText(t, dir, "a.txt", pickProse+"\n")

	var stdout, stderr bytes.Buffer
	code := runPick(&stdout, &stderr, []string{"-roster", pickRoster(t, dir), "-list", short, path})
	if code != 1 {
		t.Fatalf("gao pick = %d, want 1\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "mmlu-vi") {
		t.Errorf("it does not name the benchmark that is missing: %s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("it printed a report anyway:\n%s", stdout.String())
	}
}

func TestPickShowsTheDocumentsItFlagged(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", pickProse+"\n\n"+pickItem+"\n")

	var stdout, stderr bytes.Buffer
	if code := runPick(&stdout, &stderr, []string{
		"-roster", pickRoster(t, dir), "-list", pickList(t, dir), "-show", "5", path,
	}); code != 0 {
		t.Fatalf("gao pick -show 5 = %d, want 0\n%s", code, stderr.String())
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
func TestPickNamesTheRowOfAPart(t *testing.T) {
	dir := t.TempDir()
	part := writeHostedPart(t,
		hostedRow{host: "vnbao.vn", text: pickProse + "\n"},
		hostedRow{host: "vnbao.vn", text: pickProse + "\n\n" + pickItem + "\n"},
	)

	var stdout, stderr bytes.Buffer
	if code := runPick(&stdout, &stderr, []string{
		"-roster", pickRoster(t, dir), "-list", pickList(t, dir), "-show", "5", part,
	}); code != 0 {
		t.Fatalf("gao pick = %d, want 0\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "#1") {
		t.Errorf("the flagged row of the part is not named\n%s", stdout.String())
	}
}

func TestPickNeedsAListToCheckAgainst(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", pickProse+"\n")

	var stdout, stderr bytes.Buffer
	if code := runPick(&stdout, &stderr, []string{path}); code != 2 {
		t.Fatalf("gao pick with no list = %d, want 2\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-benchmarks") {
		t.Errorf("it does not say what to do instead: %s", stderr.String())
	}
}

func TestPickNeedsSomethingToCheck(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runPick(&stdout, &stderr, []string{"-roster", pickRoster(t, dir), "-list", pickList(t, dir)}); code != 2 {
		t.Fatalf("gao pick with no files = %d, want 2\n%s", code, stderr.String())
	}
}

func TestPickBenchmarksTakesNoFiles(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", pickProse+"\n")

	var stdout, stderr bytes.Buffer
	if code := runPick(&stdout, &stderr, []string{"-benchmarks", path}); code != 2 {
		t.Fatalf("gao pick -benchmarks a.txt = %d, want 2\n%s", code, stderr.String())
	}
}

func TestPickSaysWhenTheRosterIsNotThere(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runPick(&stdout, &stderr, []string{"-roster", filepath.Join(dir, "gone.json"), "-benchmarks"}); code != 1 {
		t.Fatalf("gao pick with a roster that is not there = %d, want 1\n%s", code, stderr.String())
	}
}

// TestEveryDigestOnTheRosterIsStillWhatTheCommandPrints is the check that makes
// a gao: pin worth writing down.
//
// A benchmark this repository builds is pinned at the digest of its own frame,
// and the whole value of that is that anybody with the repository can print the
// digest and compare. So this test does exactly what a reader would do: it runs
// the command the roster names and looks for the revision the roster claims. If
// somebody adds an item to a set and does not repin it, the roster is quietly
// describing a set that no longer exists, and this is where that stops.
func TestEveryDigestOnTheRosterIsStillWhatTheCommandPrints(t *testing.T) {
	ros, err := pick.Rostered()
	if err != nil {
		t.Fatal(err)
	}

	var built int
	for _, e := range ros.Benchmarks {
		if e.Home == "" {
			continue
		}
		home, err := pick.ParseHome(e.Home)
		if err != nil {
			t.Fatalf("%s: %v", e.Name, err)
		}
		if home.Scheme != pick.Built {
			continue
		}
		built++

		out, errOut, code := exec(t, strings.Fields(home.Path)...)
		if code != 0 {
			t.Errorf("%s: %q exited %d: %s", e.Name, home.Ask(), code, errOut)
			continue
		}
		if !strings.Contains(out, e.Version) {
			t.Errorf("%s is pinned at %s and %q does not print it, so the roster is describing a set that has changed since it was pinned:\n%s",
				e.Name, e.Version, home.Ask(), out)
		}
	}
	if built == 0 {
		t.Fatal("no benchmark on the roster is pinned to a set built here, and this check was written because several are")
	}
}
