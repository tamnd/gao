package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/suat"
)

// suatPoint builds a cumulative point at n fetches with a given net yield,
// split across the classes the way the plan expects.
func suatPoint(at int64, yield float64) suat.Point {
	docs := int64(float64(at) * yield)
	p := suat.Point{At: at, Box: "server1", By: map[suat.Class]suat.Tally{}}
	shares := []struct {
		c          suat.Class
		fetch, doc float64
		perDoc     int64
	}{
		{suat.Forum, 0.34, 0.42, 900},
		{suat.News, 0.24, 0.24, 500},
		{suat.Government, 0.08, 0.07, 700},
		{suat.Education, 0.07, 0.08, 1100},
		{suat.Commerce, 0.19, 0.11, 200},
		{suat.Other, 0.08, 0.08, 300},
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
		p.By[s.c] = suat.Tally{
			Fetches: f, Documents: d, Tokens: d * s.perDoc, Hosts: f / 400,
			Duplicates: rest / 2, Rejected: rest / 4, Empty: rest / 8, Failed: rest / 16,
			Refused: rest - rest/2 - rest/4 - rest/8 - rest/16,
		}
	}
	return p
}

// suatFile writes the file a crawl appends its points to.
func suatFile(t *testing.T, points ...suat.Point) string {
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

// suatRun writes n cumulative points one stride apart at a given yield.
func suatRun(t *testing.T, n int, yield float64) string {
	t.Helper()
	points := make([]suat.Point, 0, n)
	for i := 1; i <= n; i++ {
		points = append(points, suatPoint(int64(i)*suat.Stride, yield))
	}
	return suatFile(t, points...)
}

func TestTheYieldIsBrokenOutByTargetClass(t *testing.T) {
	out, errOut, code := exec(t, "suat", suatRun(t, 4, 0.18))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, c := range suat.Classes {
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
	out, _, code := exec(t, "suat", suatRun(t, 4, 0.18))
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
	out, _, code := exec(t, "suat", suatRun(t, 4, 0.18))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "P03-5 is holding") {
		t.Errorf("the report does not settle the forums prediction:\n%s", out)
	}
}

func TestTheForumPredictionCanAlsoFail(t *testing.T) {
	p := suatPoint(4*suat.Stride, 0.18)
	forum, news := p.By[suat.Forum], p.By[suat.News]
	forum.Tokens, news.Tokens = news.Tokens, forum.Tokens
	p.By[suat.Forum], p.By[suat.News] = forum, news

	out, _, code := exec(t, "suat", suatFile(t, suatPoint(3*suat.Stride, 0.18), p))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "P03-5 is not holding") {
		t.Errorf("a failing prediction was not reported as one:\n%s", out)
	}
}

func TestTheCommandTellsAHealthyCrawlToCarryOn(t *testing.T) {
	out, _, code := exec(t, "suat", suatRun(t, 4, 0.18))
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
	var points []suat.Point
	for at := int64(suat.Settled); at <= suat.Settled+2*suat.Stride; at += suat.Stride {
		points = append(points, suatPoint(at, 0.06))
	}
	out, _, code := exec(t, "suat", suatFile(t, points...))
	if code != 2 {
		t.Fatalf("a crawl below the kill line exited %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, "stop:") {
		t.Errorf("the report does not say to stop:\n%s", out)
	}
}

func TestAYoungCrawlBelowTheKillLineIsNotStopped(t *testing.T) {
	out, _, code := exec(t, "suat", suatRun(t, 4, 0.06))
	if code != 0 {
		t.Fatalf("a young crawl at 0.06 exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "seed list being measured") {
		t.Errorf("the report does not say why it is not acting:\n%s", out)
	}
}

func TestObjectionsGetTheOperationalResponse(t *testing.T) {
	p := suatPoint(4*suat.Stride, 0.18)
	for _, c := range suat.Classes {
		tl := p.By[c]
		tl.Objected = tl.Hosts / 10
		p.By[c] = tl
	}
	out, _, code := exec(t, "suat", suatFile(t, suatPoint(3*suat.Stride, 0.18), p))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "slow:") || !strings.Contains(out, "half rate") {
		t.Errorf("the report does not give the operational response:\n%s", out)
	}
}

func TestAYieldMeasuredOnlyAtTheEndIsAFault(t *testing.T) {
	out, _, code := exec(t, "suat", suatFile(t, suatPoint(suat.Stride, 0.18), suatPoint(300_000_000, 0.18)))
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "measured afterward rather than while it ran") {
		t.Errorf("the report does not say what is wrong with two distant points:\n%s", out)
	}
}

func TestFetchesThatWentNowhereAreReported(t *testing.T) {
	p := suatPoint(4*suat.Stride, 0.18)
	tl := p.By[suat.Forum]
	tl.Duplicates -= 100_000
	p.By[suat.Forum] = tl

	out, _, code := exec(t, "suat", suatFile(t, suatPoint(3*suat.Stride, 0.18), p))
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "nobody wrote down") {
		t.Errorf("the report does not catch the missing outcomes:\n%s", out)
	}
}

func TestTheReportSaysWhatTheClassifierCouldNotPlace(t *testing.T) {
	out, _, code := exec(t, "suat", suatRun(t, 4, 0.18))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "target classes") {
		t.Errorf("the report does not say how much was classified:\n%s", out)
	}
}

func TestTheYieldReportSpeaksJSON(t *testing.T) {
	out, _, code := exec(t, "suat", "-json", suatRun(t, 4, 0.18))
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
	_, errOut, code := exec(t, "suat")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "net yield") {
		t.Errorf("the usage does not say what this measures: %s", errOut)
	}
}

func TestAYieldFileThatIsNotThereSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "suat", filepath.Join(t.TempDir(), "nope.jsonl"))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao suat:") {
		t.Errorf("the error is not attributed: %s", errOut)
	}
}
