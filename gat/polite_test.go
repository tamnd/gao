package gat_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/gao/gat"
)

// The schedule is tested against a clock that does not run. A politeness test
// that actually waited a second between two requests would be a test nobody runs
// often enough to catch anything, and the thing being tested is arithmetic about
// time rather than the passage of it.
type clock struct {
	mu   sync.Mutex
	now  time.Time
	rest []time.Duration
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
		c.now = c.now.Add(d)
	}
	return nil
}

func (c *clock) waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.rest...)
}

func polite(c *clock, o gat.PoliteOptions) *gat.Polite {
	o.Now, o.Sleep = c.Now, c.Sleep
	return gat.NewPolite(o)
}

// fetch is one request through the scheduler, finished immediately, which is
// what most of these tests want.
func fetch(t *testing.T, p *gat.Polite, host string) {
	t.Helper()
	done, err := p.Wait(context.Background(), host)
	if err != nil {
		t.Fatalf("waiting on %s: %v", host, err)
	}
	done()
}

func TestTheFirstRequestToAHostGoesOutImmediately(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{})

	fetch(t, p, "vnexpress.net")

	if got := c.waits(); len(got) != 1 || got[0] != 0 {
		t.Errorf("the first request waited %v, and nobody has been asked for anything yet", got)
	}
}

func TestTwoRequestsToOneHostAreSpacedApart(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{Delay: 2 * time.Second})

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
	p := polite(c, gat.PoliteOptions{Delay: 5 * time.Second})

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

// One slow host must not hold up the rest of the frontier, which is the whole
// reason the schedule is per host rather than global.
func TestOneHostWaitingDoesNotMakeAnotherWait(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{Delay: time.Minute})

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
	p := polite(c, gat.PoliteOptions{Delay: time.Second})
	r := gat.ReadRobots([]byte("User-agent: " + gat.Bot + "\nCrawl-delay: 30\n"))

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

// And a site asking for less than we were going to give it does not get its way,
// because the default is what we are willing to do rather than what we are
// obliged to.
func TestASiteAskingForShorterDoesNotSpeedUsUp(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{Delay: 5 * time.Second})
	r := gat.ReadRobots([]byte("User-agent: *\nCrawl-delay: 1\n"))

	if got, _ := p.Learn("kenh14.vn", r); got != 5*time.Second {
		t.Errorf("a site asking for 1s moved us to %v from 5s", got)
	}
}

// A site that asked for an hour has said no in a way that reads as yes. Waiting
// that out is twenty four fetches a day forever, so the host is reported back
// rather than scheduled.
func TestAHostThatCannotBeCrawledPolitelyIsReportedRatherThanQueued(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{})
	r := gat.ReadRobots([]byte("User-agent: " + gat.Bot + "\nCrawl-delay: 3600\n"))

	asked, ok := p.Learn("cham.vn", r)
	if ok {
		t.Error("an hour between requests was accepted as a schedule")
	}
	if asked != time.Hour {
		t.Errorf("the refusal reports %v, and the site asked for an hour", asked)
	}
	// Refusing it must not quietly leave the host on the default either, which
	// would be the worst of both readings.
	if got := p.Delay("cham.vn"); got != gat.DefaultDelay {
		t.Errorf("a refused host is on %v", got)
	}
}

// A robots.txt that says nothing about delay leaves us on ours. A site is not
// obliged to have an opinion.
func TestASilentFileLeavesUsOnOurOwnNumber(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{Delay: 3 * time.Second})

	if got, ok := p.Learn("thanhnien.vn", gat.ReadRobots(nil)); !ok || got != 3*time.Second {
		t.Errorf("an empty robots.txt put us on %v, ok %v", got, ok)
	}
}

// The concurrency cap. Two workers picking URLs off one host must not become two
// connections to one server, and the second one has to actually block rather
// than be counted afterwards.
func TestOnlyOneRequestPerHostIsInFlight(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{})

	first, err := p.Wait(context.Background(), "otofun.net")
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		close(started)
		second, err := p.Wait(context.Background(), "otofun.net")
		if err == nil {
			second()
		}
		close(finished)
	}()

	<-started
	select {
	case <-finished:
		t.Fatal("a second request to the same host went out while the first was in flight")
	case <-time.After(20 * time.Millisecond):
	}

	first()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("the second request never went out after the first finished")
	}
}

// A cap above one is allowed and is a decision somebody makes, not a default.
func TestTheCapCanBeRaisedDeliberately(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{PerHost: 2, Delay: time.Hour})

	a, err := p.Wait(context.Background(), "voz.vn")
	if err != nil {
		t.Fatal(err)
	}
	defer a()

	held := make(chan struct{})
	go func() {
		b, err := p.Wait(context.Background(), "voz.vn")
		if err == nil {
			defer b()
		}
		close(held)
	}()

	// The second slot is free, so the second request is waiting on the delay
	// rather than on the first request, and the fake clock lets it through.
	select {
	case <-held:
	case <-time.After(time.Second):
		t.Fatal("a cap of two only let one request through")
	}
}

// A 429 is the server telling us something about itself. The answer is to leave
// it alone for the time it named, on top of whatever it was already owed.
func TestABackoffPushesTheNextRequestOut(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{Delay: time.Second})

	fetch(t, p, "cafef.vn")
	p.Backoff("cafef.vn", 2*time.Minute)
	fetch(t, p, "cafef.vn")

	if last := c.waits()[1]; last != 2*time.Minute+time.Second {
		t.Errorf("after a two minute backoff the next request waited %v", last)
	}
}

func TestABackoffOfNothingChangesNothing(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{Delay: time.Second})

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
	p := polite(c, gat.PoliteOptions{Delay: time.Hour})

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
	p := polite(c, gat.PoliteOptions{})

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

	blocked := make(chan struct{})
	go func() {
		second, err := p.Wait(context.Background(), "kienthuc.net.vn")
		if err == nil {
			second()
		}
		close(blocked)
	}()
	select {
	case <-blocked:
		t.Fatal("two requests to one host were in flight after a double release")
	case <-time.After(20 * time.Millisecond):
	}
}

// The whole point, under the load it is for: many workers, a handful of hosts,
// and no host ever seeing two at once.
func TestManyWorkersOnAFewHostsStayWithinTheCap(t *testing.T) {
	c := newClock()
	p := polite(c, gat.PoliteOptions{})
	hosts := []string{"a.vn", "b.vn", "c.vn"}

	var mu sync.Mutex
	inFlight := map[string]int{}
	var worst int

	var wg sync.WaitGroup
	for i := range 60 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			host := hosts[i%len(hosts)]
			done, err := p.Wait(context.Background(), host)
			if err != nil {
				t.Errorf("waiting on %s: %v", host, err)
				return
			}
			mu.Lock()
			inFlight[host]++
			if inFlight[host] > worst {
				worst = inFlight[host]
			}
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
}
