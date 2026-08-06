package xoa_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/xoa"
)

// A takedown path is the part of a crawler that is easiest to have on paper and
// hardest to have working, so these tests are about the gap between publishing an
// address and honoring what arrives at it.

func day(d int) time.Time {
	return time.Date(2026, 3, d, 9, 0, 0, 0, time.UTC)
}

func read(t *testing.T, body string) *xoa.Register {
	t.Helper()
	g, err := xoa.Read(strings.NewReader(body))
	if err != nil {
		t.Fatalf("reading the register: %v", err)
	}
	return g
}

const oneRequest = `
[[request]]
issue = 41
host = "example.vn"
scope = "stop"
asked = 2026-03-01T09:00:00Z
stopped = 2026-03-02T15:00:00Z
`

func TestARequestStopsTheCrawlOnThatSite(t *testing.T) {
	g := read(t, oneRequest)

	for _, blocked := range []string{
		"https://example.vn/tin-tuc",
		"https://www.example.vn/",
		"http://tin.example.vn/bai/1",
	} {
		if _, ok := g.Blocked(blocked); !ok {
			t.Errorf("%s was not blocked", blocked)
		}
	}
}

// A takedown for example.vn is about their site. Reading it as a string suffix
// would drop a stranger's site out of the corpus on the strength of a request
// that was never about them.
func TestAStrangerWithASimilarNameIsNotCovered(t *testing.T) {
	g := read(t, oneRequest)
	for _, fine := range []string{"https://notexample.vn/a", "https://example.vn.other.com/a"} {
		if r, ok := g.Blocked(fine); ok {
			t.Errorf("%s was blocked by issue %d", fine, r.Issue)
		}
	}
}

func TestARequestCanCoverPartOfASite(t *testing.T) {
	g := read(t, `
[[request]]
issue = 42
host = "diendan.vn"
paths = ["/thanh-vien/", "/tin-nhan"]
scope = "stop"
asked = 2026-03-01T09:00:00Z
`)

	if _, ok := g.Blocked("https://diendan.vn/thanh-vien/nguoi-dung"); !ok {
		t.Error("a path the request named was not blocked")
	}
	if _, ok := g.Blocked("https://diendan.vn/bai-viet/1"); ok {
		t.Error("a partial request took the whole site")
	}
}

// The gate at the fetch binds from the moment the request was made, not from the
// moment somebody got around to editing a file. Otherwise a site that has asked
// us to stop keeps being crawled for as long as the operator is asleep.
func TestTheBlockBindsBeforeAnybodyHasActedOnIt(t *testing.T) {
	g := read(t, `
[[request]]
issue = 43
host = "example.vn"
scope = "stop"
asked = 2026-03-01T09:00:00Z
`)
	if _, ok := g.Blocked("https://example.vn/a"); !ok {
		t.Error("a request nobody has acted on yet did not stop the crawl")
	}
	if len(g.Open()) != 1 {
		t.Error("a request with no stop date is not open")
	}
}

// The store gate is a different question from the fetch gate. A request scoped
// to stop leaves published releases alone, so what was already fetched stays.
func TestAStopLeavesWhatWasAlreadyPublished(t *testing.T) {
	g := read(t, oneRequest)
	if r, ok := g.Erased("https://example.vn/bai-cu", time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC)); ok {
		t.Errorf("a document fetched before the request was erased by issue %d", r.Issue)
	}
}

// A document fetched after somebody asked us to stop is a fetch that should
// never have happened, and the gap between asking and acting is ours rather than
// theirs. So it goes, whatever the request was scoped to.
func TestWhatWeFetchedAfterTheyAskedDoesNotStay(t *testing.T) {
	g := read(t, oneRequest)
	if _, ok := g.Erased("https://example.vn/bai-moi", day(2)); !ok {
		t.Error("a page fetched after the request was made was kept")
	}
}

func TestAnEraseTakesEverythingWheneverItWasFetched(t *testing.T) {
	g := read(t, `
[[request]]
issue = 44
host = "example.vn"
scope = "erase"
asked = 2026-03-01T09:00:00Z
stopped = 2026-03-01T11:00:00Z
rebuilt = 2026-03-09T00:00:00Z
releases = ["gao-v0.3"]
`)
	if _, ok := g.Erased("https://example.vn/bai-cu", time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)); !ok {
		t.Error("an erase left a document from 2019 in the corpus")
	}
}

// Stopping and rebuilding are different promises with different costs. A request
// that has been stopped and not yet rebuilt is not finished.
func TestAnEraseIsNotDoneWhenTheCrawlStops(t *testing.T) {
	g := read(t, `
[[request]]
issue = 45
host = "example.vn"
scope = "erase"
asked = 2026-03-01T09:00:00Z
stopped = 2026-03-01T11:00:00Z
`)
	if len(g.Open()) != 1 {
		t.Error("an erase with no rebuild date was reported as finished")
	}
	// And it is not late, because the promise about 72 hours is about stopping.
	if len(g.Late(day(20))) != 0 {
		t.Error("a request that was stopped in two hours was called late")
	}
}

func TestWhatIsPastThePromise(t *testing.T) {
	g := read(t, `
[[request]]
issue = 46
host = "cham.vn"
scope = "stop"
asked = 2026-03-01T09:00:00Z

[[request]]
issue = 47
host = "nhanh.vn"
scope = "stop"
asked = 2026-03-05T09:00:00Z
stopped = 2026-03-05T10:00:00Z
`)
	late := g.Late(day(6))
	if len(late) != 1 || late[0].Issue != 46 {
		t.Errorf("the late list is %v", late)
	}
	if len(g.Late(day(2))) != 0 {
		t.Error("a request one day old was called late against a 72 hour promise")
	}
}

// The clock starts when the request was made and not when somebody read it.
// Measuring from the moment we noticed would make every response time zero.
func TestTheClockStartsWhenTheyAskedAndNotWhenWeNoticed(t *testing.T) {
	g := read(t, `
[[request]]
issue = 48
host = "example.vn"
scope = "stop"
asked = 2026-03-01T09:00:00Z
stopped = 2026-03-04T09:00:00Z
`)
	worst, err := g.Worst()
	if err != nil {
		t.Fatal(err)
	}
	if worst != 72*time.Hour {
		t.Errorf("the response time came out as %v, want 72h", worst)
	}
}

// A median hides exactly the request that broke the promise, so the number that
// describes the promise is the worst one.
func TestTheWorstCaseIsTheOneThatDescribesThePromise(t *testing.T) {
	g := read(t, `
[[request]]
issue = 50
host = "a.vn"
scope = "stop"
asked = 2026-03-01T09:00:00Z
stopped = 2026-03-01T10:00:00Z

[[request]]
issue = 51
host = "b.vn"
scope = "stop"
asked = 2026-03-01T09:00:00Z
stopped = 2026-03-01T11:00:00Z

[[request]]
issue = 52
host = "c.vn"
scope = "stop"
asked = 2026-03-01T09:00:00Z
stopped = 2026-03-11T09:00:00Z
`)
	median, err := g.Median()
	if err != nil {
		t.Fatal(err)
	}
	if median != 2*time.Hour {
		t.Errorf("the median came out as %v", median)
	}
	worst, err := g.Worst()
	if err != nil {
		t.Fatal(err)
	}
	if worst != 240*time.Hour {
		t.Errorf("the worst came out as %v", worst)
	}
}

// This is the one worth having. A path nobody has used is a path nobody has
// tested, and a report that prints a median of zero hours and everything honored
// describes a system that has never done anything as one that has never failed.
func TestAnEmptyRegisterIsNotAPerfectRecord(t *testing.T) {
	g := read(t, "")
	if _, err := g.Median(); !errors.Is(err, xoa.ErrNothingFiled) {
		t.Errorf("an empty register reported a median: %v", err)
	}
	if _, err := g.Worst(); !errors.Is(err, xoa.ErrNothingFiled) {
		t.Errorf("an empty register reported a worst case: %v", err)
	}
}

// P03-8 is a gate on the fraction of crawled hosts that objected.
func TestTheRateIsOverHostsWeActuallyCrawled(t *testing.T) {
	g := read(t, `
[[request]]
issue = 60
host = "a.vn"
scope = "stop"
asked = 2026-03-01T09:00:00Z

[[request]]
issue = 61
host = "a.vn"
paths = ["/rieng"]
scope = "stop"
asked = 2026-03-02T09:00:00Z

[[request]]
issue = 62
host = "b.vn"
scope = "stop"
asked = 2026-03-02T09:00:00Z
`)
	// Two hosts objected, not three requests. A site that writes twice is one
	// site, and counting requests would double a number that is a gate.
	rate, err := g.Rate(1000)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0.002 {
		t.Errorf("the rate came out as %v, want 0.002", rate)
	}
	if _, err := g.Rate(0); err == nil {
		t.Error("a rate over nothing was reported as a number")
	}
}

// The register is edited by hand under time pressure, by somebody who has just
// been asked to take something down.
func TestARowThatCannotBeTrue(t *testing.T) {
	g := read(t, `
[[request]]
issue = 70
host = "a.vn"
scope = "stop"
asked = 2026-03-05T09:00:00Z
stopped = 2026-03-01T09:00:00Z

[[request]]
host = "b.vn"
scope = "xoa het"
asked = 2026-03-01T09:00:00Z

[[request]]
issue = 70
host = ""
scope = "erase"
asked = 2027-01-01T09:00:00Z
rebuilt = 2027-02-01T09:00:00Z
`)
	bad := g.Check(day(20))
	joined := strings.Join(bad, "\n")
	for _, want := range []string{
		"stopped before it was asked",
		"neither stop nor erase",
		"no issue number",
		"appears twice",
		"names no host",
		"asked in the future",
		"does not say which",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the check missed %q:\n%s", want, joined)
		}
	}
}

func TestAGoodRegisterHasNothingWrongWithIt(t *testing.T) {
	g := read(t, oneRequest)
	if bad := g.Check(day(20)); len(bad) != 0 {
		t.Errorf("a well formed register was reported as broken: %v", bad)
	}
}

func TestSomethingThatIsNotARegister(t *testing.T) {
	if _, err := xoa.Read(strings.NewReader("day khong phai toml [[[")); err == nil {
		t.Error("a file that is not a register was read as one")
	}
}

func TestAHostSpelledLoudlyIsStillTheSameHost(t *testing.T) {
	g := read(t, `
[[request]]
issue = 80
host = "  Example.VN  "
scope = "stop"
asked = 2026-03-01T09:00:00Z
`)
	if _, ok := g.Blocked("https://example.vn/a"); !ok {
		t.Error("a host written with capitals in the register did not match")
	}
}

func TestSomethingThatIsNotAURLIsNotBlocked(t *testing.T) {
	g := read(t, oneRequest)
	if _, ok := g.Blocked("javascript:void(0)"); ok {
		t.Error("something that is not a URL matched a takedown")
	}
}
