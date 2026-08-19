package yield

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tally builds a class tally whose outcomes account for every fetch, so a test
// can vary one thing without breaking the arithmetic on everything else.
func tally(fetches, documents, tokens, hosts int64) Tally {
	rest := fetches - documents
	t := Tally{
		Fetches:   fetches,
		Documents: documents,
		Tokens:    tokens,
		Hosts:     hosts,
	}
	// Spread what did not become a document over the outcomes, in the rough
	// shape a real crawl produces: mostly duplicates, then rejects, then the
	// rest.
	t.Duplicates = rest / 2
	t.Rejected = rest / 4
	t.Empty = rest / 8
	t.Failed = rest / 16
	t.Refused = rest - t.Duplicates - t.Rejected - t.Empty - t.Failed
	return t
}

// point builds a cumulative point at n fetches with the given net yield, split
// across the classes the way the plan expects.
func point(at int64, yield float64, box string) Point {
	docs := int64(float64(at) * yield)
	p := Point{At: at, Box: box, By: map[Class]Tally{}}
	// Shares of fetches, and of documents, per class.
	shares := []struct {
		c            Class
		fetch, doc   float64
		tokensPerDoc int64
	}{
		{Forum, 0.34, 0.42, 900},
		{News, 0.24, 0.24, 500},
		{Government, 0.08, 0.07, 700},
		{Education, 0.07, 0.08, 1100},
		{Commerce, 0.19, 0.11, 200},
		{Other, 0.08, 0.08, 300},
	}
	var fetched, kept int64
	for i, s := range shares {
		f := int64(float64(at) * s.fetch)
		d := int64(float64(docs) * s.doc)
		if i == len(shares)-1 {
			f, d = at-fetched, docs-kept
		}
		fetched, kept = fetched+f, kept+d
		p.By[s.c] = tally(f, d, d*s.tokensPerDoc, f/400)
	}
	return p
}

// run builds a healthy run of cumulative points one stride apart.
func run(points int, yield float64) *Run {
	r := &Run{Crawl: "gao-crawl-2026-09"}
	for i := 1; i <= points; i++ {
		r.Points = append(r.Points, point(int64(i)*Stride, yield, "server1"))
	}
	return r
}

func faultAbout(t *testing.T, faults []string, want string) {
	t.Helper()
	for _, f := range faults {
		if strings.Contains(f, want) {
			return
		}
	}
	t.Errorf("no fault mentions %q, got:\n  %s", want, strings.Join(faults, "\n  "))
}

func TestAHealthyRunHasNoFaults(t *testing.T) {
	if faults := run(6, 0.18).Faults(); len(faults) > 0 {
		t.Errorf("a good run was faulted:\n  %s", strings.Join(faults, "\n  "))
	}
}

func TestYieldIsDocumentsKeptRatherThanPagesFetched(t *testing.T) {
	// A crawler that counted 200 responses would report almost 1.0 here. The
	// number that matters is what survived deduplication and the gates.
	tl := tally(1000, 150, 135_000, 3)
	if got := tl.Yield(); got < 0.149 || got > 0.151 {
		t.Errorf("yield is %v, want 0.15", got)
	}
	if tl.Accounted() != tl.Fetches {
		t.Errorf("%d fetches and %d accounted for", tl.Fetches, tl.Accounted())
	}
}

func TestFetchesThatWentNowhereAreCaught(t *testing.T) {
	// This is the failure that flatters a crawl: drop a category and the
	// denominator quietly shrinks.
	r := run(3, 0.18)
	p := r.Points[1]
	tl := p.By[Forum]
	tl.Duplicates -= 10_000
	p.By[Forum] = tl

	faultAbout(t, r.Faults(), "went somewhere nobody wrote down")
}

func TestAYieldMeasuredAtTheEndIsNotAYieldCurve(t *testing.T) {
	// The whole reason this package exists before the crawl does.
	r := &Run{Crawl: "gao-crawl-2026-09", Points: []Point{
		point(Stride, 0.18, "server1"),
		point(200_000_000, 0.18, "server1"),
	}}
	faultAbout(t, r.Faults(), "measured afterward rather than while it ran")
}

func TestAPointWithoutABoxIsNotAMeasurement(t *testing.T) {
	r := run(2, 0.18)
	r.Points[1].Box = ""
	faultAbout(t, r.Faults(), "which box it came from")
}

func TestACrawlDoesNotRunBackwards(t *testing.T) {
	r := run(3, 0.18)
	r.Points[1], r.Points[2] = r.Points[2], r.Points[1]
	faultAbout(t, r.Faults(), "ran backwards")
}

func TestAPointIsCumulativeRatherThanAWindow(t *testing.T) {
	r := run(3, 0.18)
	p := r.Points[2]
	p.At = Stride // the window, not the total
	r.Points[2] = p
	faultAbout(t, r.Faults(), "a point is cumulative")
}

func TestAClassNobodyDeclaredIsNamed(t *testing.T) {
	r := run(2, 0.18)
	r.Points[1].By["blog"] = tally(10, 1, 100, 1)
	faultAbout(t, r.Faults(), "blog is not a target class")
}

func TestAHealthyCrawlIsToldToCarryOn(t *testing.T) {
	v := run(6, 0.18).Read()
	if v.Call != Continue {
		t.Fatalf("a crawl at 0.18 was told to %s: %s", v.Call, v.Why)
	}
	if !strings.Contains(v.Why, "0.180") {
		t.Errorf("the verdict does not carry the number it was made from: %s", v.Why)
	}
}

func TestACrawlBetweenThePlanAndTheKillLineIsABudgetProblem(t *testing.T) {
	v := run(6, 0.11).Read()
	if v.Call != Continue {
		t.Fatalf("a crawl at 0.11 was told to %s", v.Call)
	}
	if !strings.Contains(v.Why, "budget moves between classes") {
		t.Errorf("the verdict does not say what to do at 0.11: %s", v.Why)
	}
}

func TestTheKillCriterionFiresOnceTheCrawlHasSettled(t *testing.T) {
	r := &Run{Crawl: "gao-crawl-2026-09"}
	for at := Settled; at <= Settled+2*Stride; at += Stride {
		r.Points = append(r.Points, point(int64(at), 0.06, "server1"))
	}
	v := r.Read()
	if v.Call != Stop {
		t.Fatalf("a settled crawl at 0.06 was told to %s: %s", v.Call, v.Why)
	}
	if !strings.Contains(v.Why, "9B rather than 60B") {
		t.Errorf("the verdict does not say what stopping costs: %s", v.Why)
	}
}

func TestAYoungCrawlIsNotStoppedForBeingYoung(t *testing.T) {
	// Yield in the first tens of millions of fetches measures the seed list.
	v := run(4, 0.06).Read()
	if v.Call != Continue {
		t.Fatalf("a crawl at %s was stopped at 0.06", count(4*Stride))
	}
	if v.Settled {
		t.Error("a crawl well short of the settling point reported itself settled")
	}
	if !strings.Contains(v.Why, "seed list being measured rather than the web") {
		t.Errorf("the verdict does not say why it is not acting: %s", v.Why)
	}
}

func TestObjectionsAreAnsweredBeforeYieldIs(t *testing.T) {
	// An operator asking us to stop is a thing to answer today. A disappointing
	// yield is not.
	r := run(6, 0.05)
	p := r.Points[len(r.Points)-1]
	for _, c := range Classes {
		tl := p.By[c]
		tl.Objected = tl.Hosts // everybody objected
		p.By[c] = tl
	}
	v := r.Read()
	if v.Call != Slow {
		t.Fatalf("a crawl every host objected to was told to %s", v.Call)
	}
	if !strings.Contains(v.Why, "half rate") {
		t.Errorf("the verdict does not say what the response is: %s", v.Why)
	}
}

func TestObjectionsAreCountedPerHostRatherThanPerFetch(t *testing.T) {
	// One operator objecting once about a host we fetched ten thousand pages
	// from is one objection.
	tl := Tally{Fetches: 10_000, Hosts: 100, Objected: 1}
	if got := tl.Objection(); got < 0.0099 || got > 0.0101 {
		t.Errorf("objection rate is %v, want 0.01", got)
	}
}

func TestMoreHostsObjectedThanWereCrawledIsAFault(t *testing.T) {
	r := run(2, 0.18)
	p := r.Points[1]
	tl := p.By[Forum]
	tl.Objected = tl.Hosts + 1
	p.By[Forum] = tl
	faultAbout(t, r.Faults(), "objecting out of")
}

func TestForumsAgainstNewsArchivesIsReportedWhileItRuns(t *testing.T) {
	// P03-5, which is a prediction rather than a hope, and a prediction settled
	// after the crawl cannot change how the budget was spent.
	p := point(6*Stride, 0.18, "server1")
	holding, forum, news := p.Holding()
	if !holding {
		t.Errorf("forums produced %d tokens and news produced %d", forum, news)
	}
	if c, ok := p.Leader(); !ok || c != Forum {
		t.Errorf("the leading class is %s", c)
	}
}

func TestTheClassesComeBackInTokenOrder(t *testing.T) {
	p := point(6*Stride, 0.18, "server1")
	ranked := p.Ranked()
	if len(ranked) != len(Classes) {
		t.Fatalf("%d classes came back out of %d", len(ranked), len(Classes))
	}
	for i := 1; i < len(ranked); i++ {
		if p.By[ranked[i-1]].Tokens < p.By[ranked[i]].Tokens {
			t.Errorf("%s is ranked above %s with fewer tokens", ranked[i-1], ranked[i])
		}
	}
}

func TestWhatTheClassifierCouldNotPlaceIsReported(t *testing.T) {
	// A large Other is a fact about the classifier, and hiding it makes the
	// other five look better than they are.
	p := point(6*Stride, 0.18, "server1")
	if got := p.Classified(); got < 0.91 || got > 0.93 {
		t.Errorf("classified share is %v, and the fixture leaves 8%% unplaced", got)
	}
}

func TestTheWindowMovesWhenTheCumulativeNumberWillNot(t *testing.T) {
	r := run(5, 0.18)
	// A bad stretch that barely dents the cumulative number.
	r.Points = append(r.Points, point(6*Stride, 0.155, "server1"))

	w, ok := r.Window()
	if !ok {
		t.Fatal("no window")
	}
	if w.Yield() > 0.05 {
		t.Errorf("the window yield is %v, and the stretch that produced it was far worse than the cumulative %v", w.Yield(), r.Points[5].Yield())
	}
}

func TestATrendNeedsThreePoints(t *testing.T) {
	if _, ok := run(2, 0.18).Trend(); ok {
		t.Error("two points produced a trend")
	}
	if _, ok := run(3, 0.18).Trend(); !ok {
		t.Error("three points produced no trend")
	}
}

func TestOnePointIsANumberRatherThanACurve(t *testing.T) {
	r := run(1, 0.18)
	if got := r.Curve(); len(got) != 1 {
		t.Errorf("one point produced %d values", len(got))
	}
	if _, ok := r.Window(); ok {
		t.Error("one point produced a window")
	}
}

func TestNothingMeasuredYetIsNotAVerdictAgainstTheCrawl(t *testing.T) {
	v := (&Run{Crawl: "gao-crawl-2026-09"}).Read()
	if v.Call != Continue {
		t.Errorf("an unmeasured crawl was told to %s", v.Call)
	}
}

func TestARunIsReadFromWhatTheCrawlAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yield.jsonl")
	points := run(3, 0.18).Points
	lines := make([]string, 0, len(points))
	for _, p := range points {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := ReadRun("gao-crawl-2026-09", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Points) != 3 {
		t.Fatalf("%d points came back", len(r.Points))
	}
	if faults := r.Faults(); len(faults) > 0 {
		t.Errorf("a good run read back faulted:\n  %s", strings.Join(faults, "\n  "))
	}
}

func TestATypoInAPointIsAnErrorRatherThanADefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yield.jsonl")
	if err := os.WriteFile(path, []byte(`{"at": 100, "boxes": "server1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRun("gao-crawl-2026-09", path); err == nil {
		t.Error("a misspelled field was read as an absent one")
	}
}

func TestAnEmptyFileIsNotARun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yield.jsonl")
	if err := os.WriteFile(path, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRun("gao-crawl-2026-09", path); err == nil {
		t.Error("a file with no measurements in it read as a run")
	}
}

func TestAFileThatIsNotThereSaysSo(t *testing.T) {
	if _, err := ReadRun("gao-crawl-2026-09", filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("a missing file read fine")
	}
}
