package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/yield"
)

// yieldPoint builds a cumulative point at n fetches with a given net yield,
// split across the classes the way the plan expects.
func yieldPoint(at int64, net float64) yield.Point {
	docs := int64(float64(at) * net)
	p := yield.Point{At: at, Box: "server1", By: map[yield.Class]yield.Tally{}}
	shares := []struct {
		c          yield.Class
		fetch, doc float64
		perDoc     int64
	}{
		{yield.Forum, 0.34, 0.42, 900},
		{yield.News, 0.24, 0.24, 500},
		{yield.Government, 0.08, 0.07, 700},
		{yield.Education, 0.07, 0.08, 1100},
		{yield.Commerce, 0.19, 0.11, 200},
		{yield.Other, 0.08, 0.08, 300},
	}
	var fetched, kept int64
	for i, s := range shares {
		f := int64(float64(at) * s.fetch)
		d := int64(float64(docs) * s.doc)
		if i == len(shares)-1 {
			f, d = at-fetched, docs-kept
		}
		fetched, kept = fetched+f, kept+d
		rest := f - d
		p.By[s.c] = yield.Tally{
			Fetches: f, Documents: d, Tokens: d * s.perDoc, Hosts: f / 400,
			Duplicates: rest / 2, Rejected: rest / 4, Empty: rest / 8, Failed: rest / 16,
			Refused: rest - rest/2 - rest/4 - rest/8 - rest/16,
		}
	}
	return p
}

// yieldFile writes the file a crawl appends its points to.
func yieldFile(t *testing.T, points ...yield.Point) string {
	t.Helper()
	lines := make([]string, 0, len(points))
	for _, p := range points {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	path := filepath.Join(t.TempDir(), "yield.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// yieldRun writes n cumulative points one stride apart at a given yield.
func yieldRun(t *testing.T, n int, net float64) string {
	t.Helper()
	points := make([]yield.Point, 0, n)
	for i := 1; i <= n; i++ {
		points = append(points, yieldPoint(int64(i)*yield.Stride, net))
	}
	return yieldFile(t, points...)
}

func TestTheYieldIsBrokenOutByTargetClass(t *testing.T) {
	out, errOut, code := exec(t, "yield", yieldRun(t, 4, 0.18))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, c := range yield.Classes {
		if !strings.Contains(out, string(c)) {
			t.Errorf("%s is missing from the report:\n%s", c, out)
		}
	}
	if !strings.Contains(out, "0.180") {
		t.Errorf("the report does not print the net yield:\n%s", out)
	}
}

func TestTheClassesArePrintedInTokenOrder(t *testing.T) {
	// The budget conversation is about tokens, and forums are why this crawl
	// exists, so the table is ordered the way the argument goes.
	out, _, code := exec(t, "yield", yieldRun(t, 4, 0.18))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	forum := strings.Index(out, "forum")
	news := strings.Index(out, "news")
	commerce := strings.Index(out, "commerce")
	if forum < 0 || news < 0 || commerce < 0 || forum > news || news > commerce {
		t.Errorf("the classes are not in token order:\n%s", out)
	}
}

func TestTheForumPredictionIsReportedWhileTheCrawlRuns(t *testing.T) {
	out, _, code := exec(t, "yield", yieldRun(t, 4, 0.18))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "P03-5 is holding") {
		t.Errorf("the report does not settle the forums prediction:\n%s", out)
	}
}

func TestTheForumPredictionCanAlsoFail(t *testing.T) {
	p := yieldPoint(4*yield.Stride, 0.18)
	forum, news := p.By[yield.Forum], p.By[yield.News]
	forum.Tokens, news.Tokens = news.Tokens, forum.Tokens
	p.By[yield.Forum], p.By[yield.News] = forum, news

	out, _, code := exec(t, "yield", yieldFile(t, yieldPoint(3*yield.Stride, 0.18), p))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "P03-5 is not holding") {
		t.Errorf("a failing prediction was not reported as one:\n%s", out)
	}
}

func TestTheCommandTellsAHealthyCrawlToCarryOn(t *testing.T) {
	out, _, code := exec(t, "yield", yieldRun(t, 4, 0.18))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "continue:") {
		t.Errorf("the report does not say what to do next:\n%s", out)
	}
}

func TestTheKillCriterionExitsDifferentlyFromAFault(t *testing.T) {
	// A crawl that should stop is not the same event as a report that cannot be
	// trusted, and a script driving this has to be able to tell them apart.
	var points []yield.Point
	for at := int64(yield.Settled); at <= yield.Settled+2*yield.Stride; at += yield.Stride {
		points = append(points, yieldPoint(at, 0.06))
	}
	out, _, code := exec(t, "yield", yieldFile(t, points...))
	if code != 2 {
		t.Fatalf("a crawl below the kill line exited %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, "stop:") {
		t.Errorf("the report does not say to stop:\n%s", out)
	}
}

func TestAYoungCrawlBelowTheKillLineIsNotStopped(t *testing.T) {
	out, _, code := exec(t, "yield", yieldRun(t, 4, 0.06))
	if code != 0 {
		t.Fatalf("a young crawl at 0.06 exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "seed list being measured") {
		t.Errorf("the report does not say why it is not acting:\n%s", out)
	}
}

func TestObjectionsGetTheOperationalResponse(t *testing.T) {
	p := yieldPoint(4*yield.Stride, 0.18)
	for _, c := range yield.Classes {
		tl := p.By[c]
		tl.Objected = tl.Hosts / 10
		p.By[c] = tl
	}
	out, _, code := exec(t, "yield", yieldFile(t, yieldPoint(3*yield.Stride, 0.18), p))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "slow:") || !strings.Contains(out, "half rate") {
		t.Errorf("the report does not give the operational response:\n%s", out)
	}
}

func TestAYieldMeasuredOnlyAtTheEndIsAFault(t *testing.T) {
	out, _, code := exec(t, "yield", yieldFile(t, yieldPoint(yield.Stride, 0.18), yieldPoint(300_000_000, 0.18)))
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "measured afterward rather than while it ran") {
		t.Errorf("the report does not say what is wrong with two distant points:\n%s", out)
	}
}

func TestFetchesThatWentNowhereAreReported(t *testing.T) {
	p := yieldPoint(4*yield.Stride, 0.18)
	tl := p.By[yield.Forum]
	tl.Duplicates -= 100_000
	p.By[yield.Forum] = tl

	out, _, code := exec(t, "yield", yieldFile(t, yieldPoint(3*yield.Stride, 0.18), p))
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "nobody wrote down") {
		t.Errorf("the report does not catch the missing outcomes:\n%s", out)
	}
}

func TestTheReportSaysWhatTheClassifierCouldNotPlace(t *testing.T) {
	out, _, code := exec(t, "yield", yieldRun(t, 4, 0.18))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "target classes") {
		t.Errorf("the report does not say how much was classified:\n%s", out)
	}
}

func TestTheYieldReportSpeaksJSON(t *testing.T) {
	out, _, code := exec(t, "yield", "-json", yieldRun(t, 4, 0.18))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var report struct {
		Crawl   string `json:"crawl"`
		At      int64  `json:"at"`
		Box     string `json:"box"`
		Points  int    `json:"points"`
		Verdict struct {
			Call    string  `json:"call"`
			Yield   float64 `json:"yield"`
			Settled bool    `json:"settled"`
		} `json:"verdict"`
		Faults []string `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out)
	}
	if report.Crawl != "gao-crawl-2026-09" || report.Box != "server1" || report.Points != 4 {
		t.Errorf("the JSON does not carry the run: %+v", report)
	}
	if report.Verdict.Call != "continue" || report.Verdict.Settled {
		t.Errorf("the JSON does not carry the verdict: %+v", report.Verdict)
	}
	if len(report.Faults) != 0 {
		t.Errorf("a good run was faulted: %v", report.Faults)
	}
}

func TestNoYieldFileAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "yield")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "net yield") {
		t.Errorf("the usage does not say what this measures: %s", errOut)
	}
}

func TestAYieldFileThatIsNotThereSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "yield", filepath.Join(t.TempDir(), "nope.jsonl"))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao yield:") {
		t.Errorf("the error is not attributed: %s", errOut)
	}
}

// yieldFalling writes a run where forums stop paying in the last window while
// their cumulative number, five good stretches deep, barely notices.
func yieldFalling(t *testing.T, n int) string {
	t.Helper()
	points := make([]yield.Point, 0, n)
	for i := 1; i <= n; i++ {
		p := yieldPoint(int64(i)*yield.Stride, 0.18)
		if i == n {
			prev := points[len(points)-1].By[yield.Forum]
			f := p.By[yield.Forum]
			f.Tokens = prev.Tokens + (f.Tokens-prev.Tokens)/8
			p.By[yield.Forum] = f
		}
		points = append(points, p)
	}
	return yieldFile(t, points...)
}

func TestTheNextStretchIsDividedOnWhatAFetchBuysNow(t *testing.T) {
	out, errOut, code := exec(t, "yield", "-next", "100000000", yieldFalling(t, 6))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"the next 100.0M, divided on the last 5.0M",
		"share",
		"already been read",
		"decided on the last",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is missing from the division:\n%s", want, out)
		}
	}

	// Forums are still the largest class in the table above and are still being
	// cut in the division below it, which is the whole point of measuring the
	// window rather than the crawl.
	table := out[strings.Index(out, "the next 100.0M"):]
	for _, line := range strings.Split(table, "\n") {
		if strings.Contains(line, "forum") && strings.Contains(line, "less") {
			return
		}
	}
	t.Errorf("forums were not cut after they stopped paying:\n%s", out)
}

func TestWithoutTheFlagThereIsNoDivision(t *testing.T) {
	out, _, code := exec(t, "yield", yieldRun(t, 4, 0.18))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out, "divided on the last") {
		t.Errorf("a division nobody asked for was printed:\n%s", out)
	}
}

// A division nobody can act on is refused rather than printed with a caveat.
func TestADivisionMadeOnOneCheckpointIsRefused(t *testing.T) {
	out, _, code := exec(t, "yield", "-next", "100000000", yieldRun(t, 1, 0.18))
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "moved on history") {
		t.Errorf("the report does not say why the division was refused:\n%s", out)
	}
}

func TestTheDivisionIsAlsoMachineReadable(t *testing.T) {
	out, _, code := exec(t, "yield", "-next", "100000000", "-json", yieldFalling(t, 6))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var got struct {
		Budget struct {
			Stretch int64 `json:"stretch"`
			Window  int64 `json:"window"`
			Slices  []struct {
				Class string  `json:"class"`
				Move  string  `json:"move"`
				Share float64 `json:"share"`
			} `json:"slices"`
		} `json:"budget"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got.Budget.Stretch != 100_000_000 || got.Budget.Window != yield.Stride {
		t.Errorf("stretch %d on a window of %d", got.Budget.Stretch, got.Budget.Window)
	}
	var total float64
	for _, s := range got.Budget.Slices {
		total += s.Share
	}
	if len(got.Budget.Slices) != len(yield.Classes)-1 {
		t.Errorf("%d classes were given a share", len(got.Budget.Slices))
	}
	if total < 0.999 || total > 1.001 {
		t.Errorf("the shares add up to %.4f", total)
	}
}
