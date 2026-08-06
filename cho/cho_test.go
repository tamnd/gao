package cho

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// host is one site watched for an hour on server1 while the box had four
// hundred fetches in flight, which is what the item means by under load.
func host(name string, delay, robots, minGap float64) Host {
	return Host{
		Host: name, Box: "server1", Fetches: 840, Seconds: 3600,
		Delay: delay, Robots: robots, MinGap: minGap, MeanGap: minGap + 0.6,
		Cap: 2, Peak: 2, Load: 400, Throttled: 2, Unavailable: 1,
	}
}

func run() Run {
	return Run{Crawl: "gao-crawl-2026-09", Hosts: []Host{
		host("vnexpress.net", 4, 0, 4.1),
		host("tuoitre.vn", 4, 0, 4.4),
		host("diendan.example.vn", 4, 0, 5.2),
	}}
}

func refuses(t *testing.T, r Run, want string) {
	t.Helper()
	for _, why := range r.Blocking() {
		if strings.Contains(why, want) {
			return
		}
	}
	t.Errorf("nothing blocking mentions %q, got:\n  %s", want, strings.Join(r.Blocking(), "\n  "))
}

func TestTheDelayIsReadOffTheWireRatherThanOffTheConfiguration(t *testing.T) {
	r := run()
	if !r.Settled() {
		t.Fatalf("a clean run was refused: %v", r.Blocking())
	}
	if !r.Holds() {
		t.Fatalf("a polite crawl did not hold: %s", r.Verdict())
	}
	// The host that came nearest is the one worth reading, so it leads.
	h, _ := r.Closest()
	if h.Host != "vnexpress.net" {
		t.Errorf("the closest host came back as %s", h.Host)
	}
	for _, want := range []string{"the crawl held its delay on 3 hosts", "under 400 fetches in flight on server1", "the closest it came was 4.10s"} {
		if !strings.Contains(r.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, r.Verdict())
		}
	}
}

// The failure the package exists for, and the reason the minimum is the number
// rather than the mean.
func TestAMeanGapHidesTheGapThatWasNotHeld(t *testing.T) {
	r := run()
	r.Hosts[1].MinGap = 0.3
	r.Hosts[1].MeanGap = 4.9
	if r.Holds() {
		t.Fatal("a crawl that put two requests 300 ms apart held")
	}
	if got := len(r.Broken()); got != 1 {
		t.Errorf("%d hosts came back with the delay broken", got)
	}
	if !strings.Contains(r.Verdict(), "the delay is a number in a config file and the socket did something else") {
		t.Errorf("the verdict does not say what a broken delay is: %s", r.Verdict())
	}
	// The mean is above the configured delay throughout, which is exactly how
	// this passes unnoticed.
	if r.Hosts[1].MeanGap <= r.Hosts[1].Delay {
		t.Error("the fixture no longer has a polite looking mean")
	}
}

// Robots is the other half of the delay that binds, and it is the half nobody
// gets to choose.
func TestTheLargerOfTheTwoDelaysIsTheOneThatBinds(t *testing.T) {
	// A gap of 4.1 seconds is polite against our own 4 and not against the 10
	// the host asked for, and the second of those is the delay that binds.
	r := run()
	r.Hosts[0].Robots = 10
	if got := r.Hosts[0].Required(); got != 10 {
		t.Errorf("a host asking for 10s against a configured 4s required %.0fs", got)
	}
	if r.Holds() {
		t.Fatal("a 4.1s gap held against a host that asked for 10s")
	}
	if !strings.Contains(r.Verdict(), "against the 10s it owed") {
		t.Errorf("the verdict does not name the delay that binds: %s", r.Verdict())
	}
	// Robots asking for more is not itself a fault, since the configured delay
	// is a floor and robots is read per host at run time. Holding it is the
	// requirement, and this one held it.
	honored := run()
	honored.Hosts[0].Robots = 10
	honored.Hosts[0].MinGap = 10.4
	if !honored.Settled() || !honored.Holds() {
		t.Errorf("a host that got the longer gap it asked for was refused: %v", honored.Blocking())
	}
	if got := len(honored.Asked()); got != 1 {
		t.Errorf("%d hosts came back asking for more than the crawl's own delay", got)
	}

	// Asking for less than we give is not a fault either, since we are the
	// slower of the two and that is allowed.
	quick := run()
	quick.Hosts[0].Robots = 1
	if !quick.Settled() || !quick.Holds() {
		t.Errorf("a host that asked for less than it got was refused: %v", quick.Blocking())
	}
	if len(quick.Asked()) != 0 {
		t.Error("a host that asked for less came back in the set that asked for more")
	}
}

// A crawl can hold every gap on every connection and still open six of them.
func TestEveryGapHeldInParallelIsNotPoliteness(t *testing.T) {
	r := run()
	r.Hosts[2].Peak = 6
	if r.Holds() {
		t.Fatal("six requests in flight against a cap of two held")
	}
	if got := len(r.Overrun()); got != 1 {
		t.Errorf("%d hosts came back over the cap", got)
	}
	if !strings.Contains(r.Verdict(), "the same load on the site arriving in parallel instead of in sequence") {
		t.Errorf("the verdict does not say what an overrun cap is: %s", r.Verdict())
	}
}

// The site gets an opinion, and it outranks ours.
func TestAHostSayingNoOutranksItsOwnRobotsFile(t *testing.T) {
	r := run()
	r.Hosts[0].Throttled = 34
	if r.Holds() {
		t.Fatal("a host answering 4% of requests with 429 held")
	}
	if !strings.Contains(r.Verdict(), "it outranks the one we read out of its robots file") {
		t.Errorf("the verdict does not say whose opinion wins: %s", r.Verdict())
	}
	if got := len(r.Refused()); got != 1 {
		t.Errorf("%d hosts came back refusing", got)
	}
}

func TestAReadingHasToHaveBeenTakenUnderLoad(t *testing.T) {
	idle := run()
	idle.Hosts[0].Load = 3
	refuses(t, idle, "a simulator with a real network stack under it")

	short := run()
	short.Hosts[0].Seconds = 120
	refuses(t, short, "what was measured is a burst rather than a rate")

	few := run()
	few.Hosts[0].Fetches = 9
	refuses(t, few, "the shortest gap in a handful of requests")

	nobox := run()
	nobox.Hosts[0].Box = ""
	refuses(t, nobox, "the delay was held on a real one")

	nodelay := run()
	nodelay.Hosts[0].Delay = 0
	refuses(t, nodelay, "nothing for the gaps to have been held against")

	nomin := run()
	nomin.Hosts[0].MinGap = 0
	refuses(t, nomin, "the mean gap is the number that hides exactly the failure this looks for")

	nocap := run()
	nocap.Hosts[0].Cap = 0
	refuses(t, nocap, "still open six of them")

	twice := run()
	twice.Hosts[1] = twice.Hosts[0]
	refuses(t, twice, "two readings of one host are not two hosts")

	nocrawl := run()
	nocrawl.Crawl = ""
	refuses(t, nocrawl, "belongs to a run rather than to a repository")

	nohost := Run{Crawl: "gao-crawl-2026-09", Hosts: []Host{{Box: "server1"}}}
	refuses(t, nohost, "a promise made to one site at a time")

	empty := Run{Crawl: "gao-crawl-2026-09"}
	if empty.Settled() || empty.Holds() {
		t.Error("a run with no hosts in it verified politeness")
	}
	if _, ok := empty.Closest(); ok {
		t.Error("a run with no hosts in it has a closest one")
	}
	if !strings.Contains(empty.Verdict(), "the configuration rather than the behavior") {
		t.Errorf("the verdict on nothing reads %q", empty.Verdict())
	}
}

func TestARunIsReadFromWhatTheCrawlerAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.jsonl")
	body := `{"host":"vnexpress.net","box":"server1","fetches":840,"seconds":3600,"delay":4,"robots":0,"min_gap":4.1,"mean_gap":4.7,"cap":2,"peak":2,"load":400,"throttled":2,"unavailable":1}

{"host":"tuoitre.vn","box":"server1","fetches":840,"seconds":3600,"delay":4,"robots":0,"min_gap":4.4,"mean_gap":5,"cap":2,"peak":2,"load":400,"throttled":2,"unavailable":1}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := ReadRun("gao-crawl-2026-09", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hosts) != 2 || !r.Holds() {
		t.Fatalf("read %d hosts, holds %v: %s", len(r.Hosts), r.Holds(), r.Verdict())
	}
	if got := r.Load(); got != 400 {
		t.Errorf("the lowest load came back as %d", got)
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"host":"vnexpress.net","gap":4.1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRun("gao-crawl-2026-09", bad); err == nil {
		t.Error("a line with a column nobody declared was read")
	}

	blank := filepath.Join(dir, "blank.jsonl")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRun("gao-crawl-2026-09", blank); err == nil {
		t.Error("an empty file was read as a run")
	}
	if _, err := ReadRun("gao-crawl-2026-09", filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a run that is not there was read")
	}
}
