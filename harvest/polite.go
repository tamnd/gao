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

// PoliteOptions configures a [Polite]. The zero value is the defaults.
type PoliteOptions struct {
	// Delay is the gap between two requests to one host, before a site's own
	// Crawl-delay is taken into account. Zero means [DefaultDelay].
	Delay time.Duration

	// PerHost is how many requests may be in flight to one host. Zero means
	// one, and one is the answer unless somebody has a reason.
	PerHost int

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
	delay   time.Duration
	perHost int
	now     func() time.Time
	sleep   func(ctx context.Context, d time.Duration) error

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
}

// NewPolite returns a scheduler with these options.
func NewPolite(o PoliteOptions) *Polite {
	p := &Polite{
		delay:   o.Delay,
		perHost: o.PerHost,
		now:     o.Now,
		sleep:   o.Sleep,
		hosts:   map[string]*politeHost{},
	}
	if p.delay <= 0 {
		p.delay = DefaultDelay
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
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	wait := p.reserve(h)
	if err := p.sleep(ctx, wait); err != nil {
		// The slot goes back so the host stays usable. The gap does not: see
		// the note above on why an abandoned reservation is left standing.
		p.release(h)
		return nil, err
	}

	var once sync.Once
	return func() { once.Do(func() { p.release(h) }) }, nil
}

// reserve takes the next start time for this host and moves it forward.
func (p *Polite) reserve(h *politeHost) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	start := now
	if h.next.After(start) {
		start = h.next
	}
	h.next = start.Add(h.delay)
	return start.Sub(now)
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
	return h
}

// Hosts is how many hosts this scheduler is tracking, which is the number a long
// crawl watches for growth, since one record per host is one map entry per host
// for as long as the process runs.
func (p *Polite) Hosts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.hosts)
}
