package harvest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/gao/harvest"
)

// The schedule is tested against a clock that does not run. A politeness test
// that actually waited a second between two requests would be a test nobody runs
// often enough to catch anything, and the thing being tested is arithmetic about
// time rather than the passage of it.
type clock struct {
	mu   sync.Mutex
	now  time.Time
	rest []time.Duration

	// over is how much longer than it was asked for each sleep takes, which is
	// what a real timer does on a box that is doing something else.
	over time.Duration
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep advances the clock instead of waiting, and writes down what it was
// asked for, which is the thing under test.
func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rest = append(c.rest, d)
	if d > 0 {
		c.now = c.now.Add(d + c.over)
	}
	return nil
}

// pass moves the clock without anybody sleeping through it, which is how a test
// says that time went by while the crawl was busy elsewhere.
func (c *clock) pass(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *clock) waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.rest...)
}

func polite(c *clock, o harvest.PoliteOptions) *harvest.Polite {
	o.Now, o.Sleep = c.Now, c.Sleep
	return harvest.NewPolite(o)
}

// fetch is one request through the scheduler, finished immediately, which is
// what most of these tests want.
func fetch(t *testing.T, p *harvest.Polite, host string) {
	t.Helper()
	done, err := p.Wait(context.Background(), host)
	if err != nil {
		t.Fatalf("waiting on %s: %v", host, err)
	}
	done()
}

func TestTheFirstRequestToAHostGoesOutImmediately(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{})

	fetch(t, p, "vnexpress.net")

	if got := c.waits(); len(got) != 1 || got[0] != 0 {
		t.Errorf("the first request waited %v, and nobody has been asked for anything yet", got)
	}
}

func TestTwoRequestsToOneHostAreSpacedApart(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: 2 * time.Second})

	fetch(t, p, "tinhte.vn")
	fetch(t, p, "tinhte.vn")
	fetch(t, p, "tinhte.vn")

	want := []time.Duration{0, 2 * time.Second, 2 * time.Second}
	got := c.waits()
	if len(got) != len(want) {
		t.Fatalf("three requests produced %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d waited %v, want %v", i+1, got[i], want[i])
		}
	}
}

// The gap is measured from one start to the next and not from a finish, so a
// host that answers slowly does not also get made to wait afterwards. This is
// what Crawl-delay has always meant and it is the reading that costs the site
// less.
func TestTheGapIsMeasuredFromOneRequestStartingToTheNext(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: 5 * time.Second})

	done, err := p.Wait(context.Background(), "vietnamnet.vn")
	if err != nil {
		t.Fatal(err)
	}
	// The request itself took four seconds.
	_ = c.Sleep(context.Background(), 4*time.Second)
	done()

	fetch(t, p, "vietnamnet.vn")

	got := c.waits()
	if last := got[len(got)-1]; last != time.Second {
		t.Errorf("a request that took 4s of a 5s gap then waited %v, want 1s", last)
	}
}

// A timer fires at or after its deadline. The request that wakes late has eaten
// the overshoot out of the gap in front of it, so the schedule has to move by
// however late it was, or the site sees two requests closer together than the
// delay it was given. The first fleet run had 24 pairs of them.
func TestARequestThatWokeLateDoesNotShortenTheNextGap(t *testing.T) {
	c := newClock()
	c.over = 250 * time.Millisecond
	p := polite(c, harvest.PoliteOptions{Delay: time.Second})

	// Three requests to one host. The first goes out at once and waits for
	// nothing, so it is on time. The second sleeps a second and wakes 250ms
	// late, which is the one that would otherwise cost the third its gap.
	for range 3 {
		fetch(t, p, "baoquangninh.vn")
	}

	got := c.waits()
	if len(got) != 3 {
		t.Fatalf("%d waits, want one per request: %v", len(got), got)
	}
	if got[2] != time.Second {
		t.Errorf("the request after a late one waited %v, want the whole second: %v", got[2], got)
	}
}

// One slow host must not hold up the rest of the frontier, which is the whole
// reason the schedule is per host rather than global.
func TestOneHostWaitingDoesNotMakeAnotherWait(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: time.Minute})

	fetch(t, p, "a.vn")
	fetch(t, p, "b.vn")
	fetch(t, p, "c.vn")

	for i, d := range c.waits() {
		if d != 0 {
			t.Errorf("request %d to a host nobody had touched waited %v", i+1, d)
		}
	}
	if p.Hosts() != 3 {
		t.Errorf("the scheduler is tracking %d hosts after seeing three", p.Hosts())
	}
}

// The site's number wins when it is the longer one. This is the same rule two
// reservations combine by, and for the same reason.
func TestASiteAskingForLongerGetsLonger(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: time.Second})
	r := harvest.ReadRobots([]byte("User-agent: " + harvest.Bot + "\nCrawl-delay: 30\n"))

	got, ok := p.Learn("baomoi.com", r)
	if !ok {
		t.Fatal("thirty seconds was refused, and it is a number a site is allowed to ask for")
	}
	if got != 30*time.Second {
		t.Errorf("a site asking for 30s got %v", got)
	}

	fetch(t, p, "baomoi.com")
	fetch(t, p, "baomoi.com")
	if last := c.waits()[1]; last != 30*time.Second {
		t.Errorf("the second request waited %v after the site asked for 30s", last)
	}
}

// The one gap a site can be certain we had read its file before is the gap after
// the request that fetched the file, and it is the one gap a scheduler that only
// applied the new number going forward would get wrong. Thirty seconds asked for
// has to reach the request already queued behind the robots.txt fetch.
func TestTheNumberASiteAsksForReachesTheGapAlreadyReserved(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: time.Second})

	// This is the fetch of robots.txt, which happens before anybody can know
	// what is in it, so it is reserved at the default.
	fetch(t, p, "baomoi.com")

	r := harvest.ReadRobots([]byte("User-agent: " + harvest.Bot + "\nCrawl-delay: 30\n"))
	if _, ok := p.Learn("baomoi.com", r); !ok {
		t.Fatal("thirty seconds was refused")
	}

	fetch(t, p, "baomoi.com")
	if first := c.waits()[1]; first != 30*time.Second {
		t.Errorf("the first page after robots.txt waited %v and the file asked for 30s", first)
	}
}

// And a site asking for less than we were going to give it does not get its way,
// because the default is what we are willing to do rather than what we are
// obliged to.
func TestASiteAskingForShorterDoesNotSpeedUsUp(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: 5 * time.Second})
	r := harvest.ReadRobots([]byte("User-agent: *\nCrawl-delay: 1\n"))

	if got, _ := p.Learn("kenh14.vn", r); got != 5*time.Second {
		t.Errorf("a site asking for 1s moved us to %v from 5s", got)
	}
}

// A site that asked for an hour has said no in a way that reads as yes. Waiting
// that out is twenty four fetches a day forever, so the host is reported back
// rather than scheduled.
func TestAHostThatCannotBeCrawledPolitelyIsReportedRatherThanQueued(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{})
	r := harvest.ReadRobots([]byte("User-agent: " + harvest.Bot + "\nCrawl-delay: 3600\n"))

	asked, ok := p.Learn("cham.vn", r)
	if ok {
		t.Error("an hour between requests was accepted as a schedule")
	}
	if asked != time.Hour {
		t.Errorf("the refusal reports %v, and the site asked for an hour", asked)
	}
	// Refusing it must not quietly leave the host on the default either, which
	// would be the worst of both readings.
	if got := p.Delay("cham.vn"); got != harvest.DefaultDelay {
		t.Errorf("a refused host is on %v", got)
	}
}

// A robots.txt that says nothing about delay leaves us on ours. A site is not
// obliged to have an opinion.
func TestASilentFileLeavesUsOnOurOwnNumber(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: 3 * time.Second})

	if got, ok := p.Learn("thanhnien.vn", harvest.ReadRobots(nil)); !ok || got != 3*time.Second {
		t.Errorf("an empty robots.txt put us on %v, ok %v", got, ok)
	}
}

// The concurrency cap. Two workers picking URLs off one host must not become two
// connections to one server, and the second worker is turned away rather than
// put in a line behind the first.
func TestOnlyOneRequestPerHostIsInFlight(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{})

	first, err := p.Wait(context.Background(), "otofun.net")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Wait(context.Background(), "otofun.net"); !errors.Is(err, harvest.ErrBusy) {
		t.Fatalf("a second request to the same host got %v", err)
	}

	// And the host is usable again the moment the first one is done, so the
	// refusal was about this instant rather than about the host.
	first()
	second, err := p.Wait(context.Background(), "otofun.net")
	if err != nil {
		t.Fatalf("the host was unusable after the first request finished: %v", err)
	}
	second()
}

// A cap above one is allowed and is a decision somebody makes, not a default.
func TestTheCapCanBeRaisedDeliberately(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{PerHost: 2, Delay: time.Hour, Patience: 2 * time.Hour})

	a, err := p.Wait(context.Background(), "voz.vn")
	if err != nil {
		t.Fatal(err)
	}
	defer a()

	// The second slot is free, so the second request is waiting on the delay
	// rather than on the first request, and the fake clock lets it through.
	b, err := p.Wait(context.Background(), "voz.vn")
	if err != nil {
		t.Fatalf("a cap of two only let one request through: %v", err)
	}
	b()
}

// A 429 is the server telling us something about itself. The answer is to leave
// it alone for the time it named, on top of whatever it was already owed.
func TestABackoffPushesTheNextRequestOut(t *testing.T) {
	c := newClock()
	// The patience is the arithmetic's, not the crawl's: what is being measured
	// here is where the schedule put the next request, and a run that would have
	// come back for it later is tested in its own case below.
	p := polite(c, harvest.PoliteOptions{Delay: time.Second, Patience: 5 * time.Minute})

	fetch(t, p, "cafef.vn")
	p.Backoff("cafef.vn", 2*time.Minute)
	fetch(t, p, "cafef.vn")

	if last := c.waits()[1]; last != 2*time.Minute+time.Second {
		t.Errorf("after a two minute backoff the next request waited %v", last)
	}
}

func TestABackoffOfNothingChangesNothing(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: time.Second})

	fetch(t, p, "24h.com.vn")
	p.Backoff("24h.com.vn", 0)
	fetch(t, p, "24h.com.vn")

	if last := c.waits()[1]; last != time.Second {
		t.Errorf("a zero backoff moved the next request to %v", last)
	}
}

// A crawl that is shutting down must not spend a minute waiting to be polite to
// a host it is never going to ask. The context wins and the fetch is not counted
// against the host, because a request that never went out did not use up
// anybody's patience.
func TestACanceledCrawlStopsWaiting(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: time.Hour, Patience: 2 * time.Hour})

	fetch(t, p, "dantri.com.vn")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Wait(ctx, "dantri.com.vn"); err == nil {
		t.Fatal("a canceled crawl waited an hour to be polite")
	}

	// And the slot came back, so a scheduler that is asked again later still
	// works rather than being one host poorer for every cancellation.
	done, err := p.Wait(context.Background(), "dantri.com.vn")
	if err != nil {
		t.Fatalf("the host was left unusable after a cancellation: %v", err)
	}
	done()
}

// Releasing twice is the mistake a deferred close makes on a retry path, and it
// must not hand out a slot that nobody is holding.
func TestFinishingTwiceDoesNotFreeAHostTwice(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{})

	done, err := p.Wait(context.Background(), "kienthuc.net.vn")
	if err != nil {
		t.Fatal(err)
	}
	done()
	done()

	// One slot, so a second holder here would mean the first release freed a
	// slot that was already free.
	first, err := p.Wait(context.Background(), "kienthuc.net.vn")
	if err != nil {
		t.Fatal(err)
	}
	defer first()

	// One slot, so a second holder here would mean the first release freed a
	// slot that was already free.
	if _, err := p.Wait(context.Background(), "kienthuc.net.vn"); !errors.Is(err, harvest.ErrBusy) {
		t.Fatalf("two requests to one host were in flight after a double release: %v", err)
	}
}

// The whole point, under the load it is for: many workers, a handful of hosts,
// and no host ever seeing two at once. The workers that could not have a host
// are told so and are free to go and fetch something else, which is the whole
// difference between a slow host and a stopped crawl.
func TestManyWorkersOnAFewHostsStayWithinTheCap(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{})
	hosts := []string{"a.vn", "b.vn", "c.vn"}

	var mu sync.Mutex
	inFlight := map[string]int{}
	var worst, went int

	var wg sync.WaitGroup
	for i := range 60 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			host := hosts[i%len(hosts)]
			done, err := p.Wait(context.Background(), host)
			if errors.Is(err, harvest.ErrBusy) {
				return
			}
			if err != nil {
				t.Errorf("waiting on %s: %v", host, err)
				return
			}
			mu.Lock()
			inFlight[host]++
			if inFlight[host] > worst {
				worst = inFlight[host]
			}
			went++
			mu.Unlock()

			mu.Lock()
			inFlight[host]--
			mu.Unlock()
			done()
		}()
	}
	wg.Wait()

	if worst > 1 {
		t.Errorf("%d requests were in flight to one host at once", worst)
	}
	if went == 0 {
		t.Error("sixty workers on three hosts and not one request went out")
	}
}

// The defect the patience exists for. Twenty workers reaching for a host that is
// not due must not be twenty workers doing nothing, which is what the third
// shard of the first fleet run spent five hours being.
func TestAHostThatIsNotDueDoesNotHoldTheWorker(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: time.Second, Patience: 30 * time.Second})

	fetch(t, p, "vov.vn")
	p.Backoff("vov.vn", time.Hour)

	before := len(c.waits())
	if _, err := p.Wait(context.Background(), "vov.vn"); !errors.Is(err, harvest.ErrBusy) {
		t.Fatalf("a host an hour out gave %v", err)
	}
	if got := c.waits(); len(got) != before {
		t.Errorf("the worker slept %v on a host it was told to come back to", got[before:])
	}

	// And the host is not spoiled by having been asked. Once the hour is up it
	// is an ordinary host again, on the gap it was always owed.
	c.pass(time.Hour)
	done, err := p.Wait(context.Background(), "vov.vn")
	if err != nil {
		t.Fatalf("the host was still refused after its hour was up: %v", err)
	}
	done()
}

// Asking has to be free. A worker that gave up and still moved the host's next
// slot out would be paying for a request it never made, and twenty of them would
// push a host that is merely busy into next week.
func TestARefusalDoesNotMoveTheSchedule(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: time.Second, Patience: time.Minute})

	fetch(t, p, "tuoitre.vn")
	p.Backoff("tuoitre.vn", 10*time.Minute)

	for range 20 {
		if _, err := p.Wait(context.Background(), "tuoitre.vn"); !errors.Is(err, harvest.ErrBusy) {
			t.Fatalf("a host ten minutes out gave %v", err)
		}
	}

	c.pass(10 * time.Minute)
	done, err := p.Wait(context.Background(), "tuoitre.vn")
	if err != nil {
		t.Fatalf("twenty refusals pushed the host out to %v", err)
	}
	done()
}

// The gap itself is still waited for when it is short, since a trip back to the
// queue for the ordinary one second gap would be a lot of bookkeeping to save a
// second.
func TestAShortGapIsStillWaitedFor(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{Delay: 5 * time.Second, Patience: 30 * time.Second})

	fetch(t, p, "zingnews.vn")
	fetch(t, p, "zingnews.vn")

	if got := c.waits(); len(got) != 2 || got[1] != 5*time.Second {
		t.Errorf("the second request to a host on a five second gap waited %v", got)
	}
}

// The schedule is one map entry per host for as long as the process runs, and
// on a crawl whose purpose is to find hosts it has not seen before that is one
// map entry per host on the web. Hosts nothing has asked for in a while go.
func TestPoliteForgetsHostsNothingHasAskedFor(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{})

	fetch(t, p, "old.example")
	c.pass(2 * time.Hour)
	fetch(t, p, "fresh.example")

	if n := p.Forget(time.Hour); n != 1 {
		t.Errorf("forgot %d hosts, want 1", n)
	}
	if n := p.Hosts(); n != 1 {
		t.Errorf("%d hosts left, want 1", n)
	}
}

// A host that is being fetched right now is a host a worker is holding a slot
// on, and dropping it would hand the next worker a fresh slot and let two
// requests go to one site at once. Age is not the only question.
func TestPoliteKeepsAHostWithARequestInFlight(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{})

	done, err := p.Wait(context.Background(), "busy.example")
	if err != nil {
		t.Fatalf("waiting: %v", err)
	}
	c.pass(2 * time.Hour)

	if n := p.Forget(time.Hour); n != 0 {
		t.Errorf("forgot %d hosts, want 0", n)
	}
	done()
	if n := p.Forget(time.Hour); n != 1 {
		t.Errorf("forgot %d hosts after the request finished, want 1", n)
	}
}

// A window of zero is not a window of nothing. A caller that has not decided how
// long to keep hosts should keep them, rather than drop every one on every pass.
func TestPoliteForgetsNothingWithoutAWindow(t *testing.T) {
	c := newClock()
	p := polite(c, harvest.PoliteOptions{})

	fetch(t, p, "one.example")
	c.pass(2 * time.Hour)

	if n := p.Forget(0); n != 0 {
		t.Errorf("forgot %d hosts, want 0", n)
	}
	if n := p.Hosts(); n != 1 {
		t.Errorf("%d hosts left, want 1", n)
	}
}
