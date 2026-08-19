package frontier

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/fleet"
)

// faults is the fault list as one string, which is what a test wants to look
// inside.
func faults(ss []string) string { return strings.Join(ss, "\n") }

func TestTheFrontierWeAreBuildingFitsOnTheBoxThatHasToHoldIt(t *testing.T) {
	p := Frontier()
	if bad := p.Faults(); len(bad) != 0 {
		t.Fatalf("the plan we publish is not a plan: %v", bad)
	}

	c := p.Cost()
	b, ok := fleet.Lookup("server1")
	if !ok {
		t.Fatal("server1 is not in the fleet")
	}
	if !c.Fits(b) {
		t.Fatalf("the frontier does not fit on server1: %s", faults(c.Blocking(b)))
	}
	if why := c.Blocking(b); len(why) != 0 {
		t.Fatalf("the frontier fits and the crawl is still blocked: %s", faults(why))
	}
	t.Logf("total %s, seen %s, ledgers %s, shapes %s, facets %s, ready %s, per host %d bytes, headroom %.1f%%",
		Bytes(c.Total), Bytes(c.Seen), Bytes(c.Ledgers), Bytes(c.Shapes), Bytes(c.Facets), Bytes(c.Ready),
		c.PerHost, 100*c.Headroom(b))
}

// This is the arithmetic that came out first, and it is kept as a test because
// the reason the plan has an active set in it is that this one did not fit.
func TestHoldingEveryHostResidentDoesNotFitAndThatIsWhyTheActiveSetExists(t *testing.T) {
	p := Frontier()
	p.Active = p.Hosts
	p.ReadyPerHost = 64
	c := p.Cost()
	b := mustBox(t, "server1")
	if c.Fits(b) {
		t.Fatalf("900k resident hosts now fit in %s, so the active set is no longer buying anything", Bytes(Available(b)))
	}
	why := faults(c.Blocking(b))
	if !strings.Contains(why, "is what fits, against the") {
		t.Errorf("the report does not say how many hosts would fit instead: %s", why)
	}
	t.Logf("%s against %s: %s", Bytes(c.Total), Bytes(Available(b)), why)
}

func TestTheSeenFilterIsTheLargestResidentThing(t *testing.T) {
	c := Frontier().Cost()
	for name, part := range map[string]int64{"ledgers": c.Ledgers, "shapes": c.Shapes, "facets": c.Facets, "ready": c.Ready} {
		if part >= c.Seen {
			t.Errorf("%s holds %s and the seen filter holds %s, so the frontier is being held resident rather than on disk",
				name, Bytes(part), Bytes(c.Seen))
		}
	}
}

func TestTheFilterErrorRateIsReportedAsDiskReadsRatherThanLostURLs(t *testing.T) {
	c := Frontier().Cost()
	if c.FalsePositive <= 0 || c.FalsePositive > 0.02 {
		t.Errorf("a filter at %d bits per URL errs %.4f of the time, which is not the rate ten bits gives",
			c.Plan.SeenBits, c.FalsePositive)
	}
	if c.Reads == 0 {
		t.Error("the filter is reported as never erring, which is a filter reported as an exact set")
	}
	if want := int64(c.FalsePositive * float64(c.Plan.URLs)); c.Reads != want {
		t.Errorf("the error rate and the read count disagree: %d against %d", c.Reads, want)
	}
}

func TestAFrontierHeldResidentDoesNotStart(t *testing.T) {
	p := Frontier()
	p.ReadyPerHost = 300_000
	why := p.Cost().Blocking(box(64 << 30))
	if !strings.Contains(faults(why), "a frontier that is resident rather than on disk") {
		t.Errorf("a frontier held entirely in memory was allowed to start: %s", faults(why))
	}
}

func TestAFrontierTooBigForTheBoxDoesNotStart(t *testing.T) {
	c := Frontier().Cost()
	why := c.Blocking(box(1 << 30))
	if !strings.Contains(faults(why), "The crawl does not start until the plan changes or the box does") {
		t.Errorf("a frontier that does not fit was allowed to start: %s", faults(why))
	}
	if !strings.Contains(faults(why), "short") {
		t.Errorf("the report does not say by how much: %s", faults(why))
	}
}

func TestABoxWithNothingLeftAfterTheReserveSaysOnlyThat(t *testing.T) {
	small := box(512 << 20)
	if n := Available(small); n != 0 {
		t.Errorf("a box smaller than the reserve has %d bytes for a frontier", n)
	}
	why := Frontier().Cost().Blocking(small)
	if len(why) != 1 || !strings.Contains(why[0], "there is nothing left for a frontier") {
		t.Errorf("a box with no room got a list of reasons rather than the one: %s", faults(why))
	}
	if h := Frontier().Cost().Headroom(small); h != 0 {
		t.Errorf("a box with no room reports %.2f headroom", h)
	}
}

func TestFittingWithNothingToSpareIsReportedAsNotFitting(t *testing.T) {
	c := Frontier().Cost()
	why := c.Blocking(box(c.Total + Reserve + (1 << 20)))
	if !strings.Contains(faults(why), "forty thousand templates on it rather than twenty four") {
		t.Errorf("a frontier with no headroom was waved through: %s", faults(why))
	}
}

func TestAFilterTooSmallToBeWorthHavingIsReported(t *testing.T) {
	p := Frontier()
	p.SeenBits = 4
	why := p.Cost().Blocking(box(64 << 30))
	if !strings.Contains(faults(why), "costing more in seeks than it saves in memory") {
		t.Errorf("a filter that errs one time in eight was allowed: %s", faults(why))
	}
}

func TestEveryWayAPlanIsNotAPlanIsNamed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Plan)
		want   string
	}{
		{"no urls", func(p *Plan) { p.URLs = 0 }, "the frontier holds no URLs"},
		{"no hosts", func(p *Plan) { p.Hosts = 0 }, "spreads across no hosts"},
		{"a seed list", func(p *Plan) { p.URLs = 1000; p.Hosts = 900; p.Active = 900 }, "which is a seed list rather than a frontier"},
		{"nothing resident", func(p *Plan) { p.Active = 0 }, "so there is nothing to fetch from"},
		{"more resident than there are", func(p *Plan) { p.Active = 2_000_000 }, "more hosts in memory than there are hosts"},
		{"no filter", func(p *Plan) { p.SeenBits = 0 }, "every URL offered is a disk read"},
		{"no lengths", func(p *Plan) { p.URLBytes = 0 }, "is zero bytes long"},
		{"no templates", func(p *Plan) { p.ShapesPerHost = 0 }, "turns the per template budget off"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := Frontier()
			tt.change(&p)
			if got := faults(p.Faults()); !strings.Contains(got, tt.want) {
				t.Errorf("want %q, got %q", tt.want, got)
			}
		})
	}
}

func TestAPlanWithNoHostsCostsNothingPerHostRatherThanDividingByZero(t *testing.T) {
	p := Frontier()
	p.Hosts = 0
	p.Active = 0
	if c := p.Cost(); c.PerHost != 0 {
		t.Errorf("a plan across no hosts costs %d bytes per host", c.PerHost)
	}
}

func TestTheArithmeticIsCheckedAgainstAHeapRatherThanTrusted(t *testing.T) {
	if testing.Short() {
		t.Skip("Measure allocates a real frontier")
	}
	p := Frontier()
	s := Measure(4_000, p.ShapesPerHost)
	if s.Offered == 0 {
		t.Fatal("the measurement offered no URLs, so it measured an empty budget")
	}
	if s.PerHost <= 0 {
		t.Fatalf("a frontier of %d hosts measured %d bytes", s.Hosts, s.Heap)
	}

	c := p.Cost()
	ratio := float64(s.PerHost) / float64(c.PerHost)
	t.Logf("measured %d bytes per host against %d worked out, ratio %.2f, whole plan %s against %s",
		s.PerHost, c.PerHost, ratio, Bytes(s.Scaled(p)), Bytes(c.Total))
	if ratio < 0.5 || ratio > 2 {
		t.Errorf("the arithmetic and the heap are a factor of %.2f apart, which is far enough that one of them is wrong",
			ratio)
	}
	if s.Scaled(p) > Available(mustBox(t, "server1")) {
		t.Errorf("the measurement says the frontier does not fit even though the arithmetic says it does: %s against %s",
			Bytes(s.Scaled(p)), Bytes(Available(mustBox(t, "server1"))))
	}
}

func TestMeasuringNothingMeasuresNothing(t *testing.T) {
	if s := Measure(0, 8); s.Heap != 0 || s.Offered != 0 {
		t.Errorf("measuring no hosts returned %+v", s)
	}
	if s := Measure(8, 0); s.PerHost != 0 {
		t.Errorf("measuring no templates returned %+v", s)
	}
}

func TestThePlanAndTheBoxComeOutAsSentences(t *testing.T) {
	p := Frontier()
	if d := p.Describe(); !strings.Contains(d, "280 million URLs") || !strings.Contains(d, "900k hosts") || !strings.Contains(d, "50k hosts are resident") {
		t.Errorf("the plan does not describe itself in units anyone says out loud: %s", d)
	}
	if w := p.Cost().Where(); !strings.Contains(w, "server1") || !strings.Contains(w, "spare") {
		t.Errorf("the cost does not say where it has to fit: %s", w)
	}
	for n, want := range map[int64]string{900: "900 bytes", 2048: "2.0 kB", 5 << 20: "5.0 MB", 3 << 30: "3.00 GB"} {
		if got := Bytes(n); got != want {
			t.Errorf("%d came out as %q, want %q", n, got, want)
		}
	}
	if got := Count(12, "host"); got != "12 hosts" {
		t.Errorf("a small count came out as %q", got)
	}
}

// box is a machine with the given memory and nothing else that matters here.
func box(memory int64) fleet.Box {
	return fleet.Box{Name: "bench", Memory: memory}
}

func mustBox(t *testing.T, name string) fleet.Box {
	t.Helper()
	b, ok := fleet.Lookup(name)
	if !ok {
		t.Fatalf("%s is not in the fleet", name)
	}
	return b
}
