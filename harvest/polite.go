package harvest

// Deciding when the next request to a host may go out.
//
// Politeness is not a setting. A crawler aiming at 700M fetches will find every
// small server in the country, and most Vietnamese forums worth crawling run on
// one box that also serves the database. The fastest we can go is the slowest
// thing we would be willing to explain afterwards, and this is where that is
// enforced rather than intended.
//
// Two rules, and they compose. One request in flight per host at a time, so a
// burst of URLs from one frontier shard cannot become a burst of connections to
// one server. And a gap between the start of one request to a host and the start
// of the next, which is where a site's own Crawl-delay lands. A site that asked
// for thirty seconds gets thirty seconds even when that costs us the host.

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultDelay is the gap between two requests to one host when the site has not
// asked for a different one.
//
// One second is slower than a general crawler and it is the number a site owner
// would agree to without being asked. At one request a second a host gives up
// 86,400 pages a day, which is more than most Vietnamese forums have, so this is
// not the constraint on the crawl. The frontier being wide is what makes the
// rate, not any single host being hit hard.
const DefaultDelay = time.Second

// MaxDelay caps what a site can ask for before the host is dropped instead.
//
// A Crawl-delay of an hour is a site saying no in a way that reads as yes, and a
// crawler that honors it schedules twenty four fetches a day forever and calls
// that politeness. The robots parser already rejects anything over a day as not
// a number. This is the lower bound where waiting stops being a plan, and it is
// reported rather than silently clamped, because a host that cannot be crawled
// politely is a host that does not get crawled.
const MaxDelay = 5 * time.Minute

// DefaultPatience is the longest a worker holds still for one host.
//
// The two rules above are about the host. This one is about the crawl, and the
// first fleet run is what wrote it. A worker that queues for a host is a worker
// that is not fetching anything else, and the third shard of that run spent an
// afternoon proving what that costs. All twenty of its workers were on the same
// host: nineteen waiting for the one in front to finish, and that one asleep on
// a gap twenty seven minutes wide. Between two and a half hours and four and a
// half hours in, the shard fetched one page. It had no sockets open and used no
// CPU over a five second sample, and the other two shards were doing three and
// three and a half pages a second on the same code and the same seeds.
//
// So a host that is not ready is not waited for. Thirty seconds is long enough
// that the ordinary one second gap is never worth a trip back to the queue and
// short enough that no host can hold a worker while there are a million URLs
// queued behind it.
const DefaultPatience = 30 * time.Second

// PoliteOptions configures a [Polite]. The zero value is the defaults.
type PoliteOptions struct {
	// Delay is the gap between two requests to one host, before a site's own
	// Crawl-delay is taken into account. Zero means [DefaultDelay].
	Delay time.Duration

	// PerHost is how many requests may be in flight to one host. Zero means
	// one, and one is the answer unless somebody has a reason.
	PerHost int

	// Patience is the longest a caller waits for a host before being told to
	// come back with something else. Zero means [DefaultPatience].
	Patience time.Duration

	// Now and Sleep are the clock, injected so that a test of the schedule is
	// a test of the schedule rather than a test of how long it takes to run.
	// Zero values mean the real ones.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

// A Polite decides when the next request to each host may go out, and makes the
// caller wait for it.
//
// It is safe for concurrent use and is meant to be shared by every worker, since
// a per worker instance would be a per worker idea of how hard one host is being
// hit, which is no idea at all.
type Polite struct {
	delay    time.Duration
	perHost  int
	patience time.Duration
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error

	mu    sync.Mutex
	hosts map[string]*politeHost
}

type politeHost struct {
	// slots is the concurrency cap, held as a buffered channel so that waiting
	// for one is cancellable.
	slots chan struct{}

	// delay is what this host gets, which is the longer of ours and what the
	// site asked for.
	delay time.Duration

	// next is the earliest a request to this host may start.
	next time.Time

	// used is the last time anything asked for this host, stamped where the
	// pointer is handed out rather than where a request goes out. That is what
	// makes [Polite.Forget] safe: a worker holding this pointer and not yet
	// holding a slot has stamped it, so the entry it is about to use cannot be
	// dropped out from under it and replaced by a fresh one.
	used time.Time
}

// NewPolite returns a scheduler with these options.
func NewPolite(o PoliteOptions) *Polite {
	p := &Polite{
		delay:    o.Delay,
		perHost:  o.PerHost,
		patience: o.Patience,
		now:      o.Now,
		sleep:    o.Sleep,
		hosts:    map[string]*politeHost{},
	}
	if p.delay <= 0 {
		p.delay = DefaultDelay
	}
	if p.patience <= 0 {
		p.patience = DefaultPatience
	}
	if p.perHost <= 0 {
		p.perHost = 1
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.sleep == nil {
		p.sleep = sleepFor
	}
	return p
}

func sleepFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Learn records what a host's robots.txt asked for.
//
// The longer of the site's number and ours wins, which is the same rule two
// reservations combine by and for the same reason: a site that asked for ten
// seconds has asked for ten seconds, and taking the shorter of the two would be
// reading its file and then ignoring the one directive in it that costs us
// anything.
//
// It reports false when the site asked for more than [MaxDelay], which is not a
// delay we schedule around. The host does not get crawled and the caller says so
// rather than queueing a fetch every ten minutes for a week.
func (p *Polite) Learn(host string, r *Robots) (time.Duration, bool) {
	asked := r.Delay(Bot)
	if asked > MaxDelay {
		return asked, false
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.host(host)
	if asked > h.delay {
		// A gap already reserved was reserved under the shorter number, and the
		// first request to a host is always the one that fetched the file the
		// longer number was written in. Left alone, a site asking for thirty
		// seconds would get one second between its robots.txt and its first
		// page, which is the one gap it can be sure we read the file before.
		if !h.next.IsZero() {
			h.next = h.next.Add(asked - h.delay)
		}
		h.delay = asked
	}
	return h.delay, true
}

// Delay is what a host is currently being given between requests.
func (p *Polite) Delay(host string) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.host(host).delay
}

// Wait blocks until a request to host may start, and returns the function to
// call when it has finished.
//
// The returned function must be called, and calling it more than once is
// harmless. Not calling it holds a slot on that host forever, which on a
// concurrency of one means the host is never fetched again, so the loss is
// loud rather than quiet.
//
// It returns [ErrBusy] rather than waiting when the host already has a request
// in flight, and when the host's next gap is further out than the caller's
// patience. Both mean the same thing to a crawl, which is that this URL is not
// this worker's next fetch and the queue has plenty that are. Neither is a
// failure and neither costs the host anything: the URL goes back and the gap is
// left exactly where it was, so nothing is spent by asking.
//
// A canceled context returns the context's error and no release function. The
// slot comes back, so the host is usable afterwards, but a gap already reserved
// stays reserved: another worker may have queued behind it by then, and moving
// it back would make that worker early. The cost of leaving it is one unused gap
// on a host we did not fetch, which is an error in the direction of waiting.
func (p *Polite) Wait(ctx context.Context, host string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.mu.Lock()
	h := p.host(host)
	p.mu.Unlock()

	select {
	case h.slots <- struct{}{}:
	default:
		return nil, fmt.Errorf("%w: %s already has a request in flight", ErrBusy, host)
	}

	wait, ok := p.reserve(h)
	if !ok {
		// The slot goes back untouched and so does the gap, since this worker
		// never took a turn. The next one along finds the host exactly as it
		// was.
		p.release(h)
		return nil, fmt.Errorf("%w: %s is not due for another %v", ErrBusy, host, wait.Round(time.Second))
	}
	if err := p.sleep(ctx, wait); err != nil {
		// The slot goes back so the host stays usable. The gap does not: see
		// the note above on why an abandoned reservation is left standing.
		p.release(h)
		return nil, err
	}
	p.woke(h)

	var once sync.Once
	return func() { once.Do(func() { p.release(h) }) }, nil
}

// reserve takes the next start time for this host and moves it forward.
//
// It reports false and takes nothing when the wait is longer than the patience,
// and the duration it returns then is how long that would have been, so the
// caller can say so. Reading the schedule has to be free: a worker that gave up
// and still moved the host's next slot out would be paying for a request it
// never made, and twenty workers doing that would push a busy host into next
// week between them.
func (p *Polite) reserve(h *politeHost) (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	start := now
	if h.next.After(start) {
		start = h.next
	}
	if wait := start.Sub(now); wait > p.patience {
		return wait, false
	}
	h.next = start.Add(h.delay)
	return start.Sub(now), true
}

// woke pushes the schedule out by however late this request is, so that the
// request after it still gets the whole delay.
//
// The slots are absolute times and a timer fires at or after its deadline, never
// before it. On a box doing anything else at the time the after can be a fifth
// of a second, and the request that woke late has eaten that out of the gap in
// front of it: it goes out at its slot plus the overshoot, the next one goes out
// at its own slot, and the site is left with less than the delay between two
// requests it was promised the delay between.
//
// The first fleet run measured it. Over 11,956 consecutive pairs of requests to
// one host with a one second delay, 24 were closer than nine tenths of a second
// and the tightest was 0.764. Small, and it is the kind of small that a site
// operator reads off their own log rather than off ours.
//
// It never pulls the schedule back, since by now other workers may have queued
// behind this host and reserved slots of their own, and moving those forward is
// the only safe direction.
func (p *Polite) woke(h *politeHost) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if next := p.now().Add(h.delay); next.After(h.next) {
		h.next = next
	}
}

func (p *Polite) release(h *politeHost) {
	select {
	case <-h.slots:
	default:
	}
}

// Backoff pushes a host's next request out by at least d, on top of whatever it
// was already going to wait.
//
// This is what a 429 or a 503 means, and it is deliberately not a retry
// schedule. A server saying it is overloaded is a server that has told us
// something about itself, and the answer is to leave it alone for the time it
// named, not to come back with a shorter interval because our queue is full.
func (p *Polite) Backoff(host string, d time.Duration) {
	if d <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	h := p.host(host)
	from := p.now()
	if h.next.After(from) {
		from = h.next
	}
	h.next = from.Add(d)
}

// host returns the record for a host, creating it on first sight. The caller
// holds the lock.
func (p *Polite) host(name string) *politeHost {
	h, ok := p.hosts[name]
	if !ok {
		h = &politeHost{slots: make(chan struct{}, p.perHost), delay: p.delay}
		p.hosts[name] = h
	}
	h.used = p.now()
	return h
}

// Forget drops the schedule for hosts nothing has asked for in older, and
// reports how many it dropped.
//
// A schedule is one map entry per host for as long as the process runs, and on a
// crawl that is finding hosts it has never seen the number of hosts is the
// number of hosts on the web. See [Crawler.Forget] for why this is the thing to
// do rather than a size cap.
//
// Forgetting a host cannot make the crawl impolite. The gap is a second and the
// window is hours, so a host old enough to drop is a host whose next request was
// due long ago, and one with a request in flight is skipped whatever its age.
func (p *Polite) Forget(older time.Duration) int {
	if older <= 0 {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	var dropped int
	for name, h := range p.hosts {
		if now.Sub(h.used) <= older || len(h.slots) > 0 {
			continue
		}
		delete(p.hosts, name)
		dropped++
	}
	return dropped
}

// Hosts is how many hosts this scheduler is tracking, which is the number a long
// crawl watches for growth, since one record per host is one map entry per host
// for as long as the process runs.
func (p *Polite) Hosts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.hosts)
}
