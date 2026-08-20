package harvest

// The name cache the crawl dials through.
//
// Go's resolver does not cache. Every dial is a fresh lookup, /etc/hosts is
// read and searched before every one of them, and a crawl that goes back to a
// host once a second for the length of that host's queue asks about the same
// name hundreds of times. The goroutine dumps off the fleet say what that costs:
// 480 goroutines parked in [net.Resolver.LookupIPAddr] and 200 more blocked on
// the mutex inside net's own hosts file reader, on a box doing eighty pages a
// second.
//
// A crawl is the easy case for caching. It does not care that a record is five
// minutes stale, because it is archiving what a name pointed at rather than
// authenticating it, and the fetch time is written into every record so a reader
// who cares can tell when the crawl looked. What it does care about is asking
// once.
//
// The other half is coalescing. Two hundred workers reaching a host at the same
// moment is normal in a crawl and it used to be two hundred queries; here it is
// one query and a hundred and ninety nine goroutines waiting on a channel.

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultNameTTL is how long a name that resolved is believed, and
// DefaultMissTTL how long one that did not is not asked about again.
//
// Neither is the record's own TTL, because Go's resolver does not hand that
// back and a crawl has no use for it. Five minutes is roughly how long one
// host's queue keeps the crawl busy, so a host is asked about once on the way
// in and about once more if it turns out to be deep.
//
// The negative one is shorter and it earns its keep on a different failure. Of
// 28,038 requests in the seed crawl, 1,041 were a host that does not resolve at
// all, and those hosts stay in the frontier: a dead host with four hundred
// queued URLs used to be four hundred lookups that each waited out the
// resolver.
const (
	DefaultNameTTL = 5 * time.Minute
	DefaultMissTTL = time.Minute
)

// DefaultNames is how many hosts the cache holds before it starts forgetting.
//
// The frontier knows about millions of hosts and the crawl is working on a few
// thousand of them at a time, so the cache does not have to be large, it has to
// be bounded. A crawl that keeps every name it has ever resolved is a crawl with
// a slow leak on a box whose disk is already a cache.
const DefaultNames = 1 << 16

// DefaultAddrs is how many addresses of a name the dialer will try before it
// gives up on that name for this attempt.
//
// A large site answers with six addresses and a dial timeout is five seconds,
// so trying all of them is thirty seconds spent on one URL. Two is a working
// address and one spare, which is what a round robin in front of a healthy
// site actually needs, and a host that needs the third one is a host the crawl
// can come back to.
const DefaultAddrs = 2

// NameOptions configures a [Names]. The zero value is the defaults above.
type NameOptions struct {
	// TTL is how long a resolved name is believed, Miss how long a failure is
	// remembered. Zero means the defaults.
	TTL  time.Duration
	Miss time.Duration

	// Size is how many hosts to hold. Zero means [DefaultNames].
	Size int

	// Addrs is how many addresses of one name to try. Zero means
	// [DefaultAddrs].
	Addrs int

	// Resolver is who to ask. Nil means [net.DefaultResolver], which is what
	// the crawl uses and what the tests replace.
	Resolver *net.Resolver
}

// A dialFunc is what [net.Dialer.DialContext] is, named so that the cache can
// be handed one that is not a real dialer.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// A Names is a bounded cache of host lookups, safe for many goroutines.
type Names struct {
	o NameOptions

	// mu guards live, old and flight together. It is held to look in the maps
	// and to join or start a lookup, and it is not held across the lookup
	// itself, which is the whole point of flight.
	mu sync.Mutex

	// live and old are the two generations the cache forgets by.
	//
	// A proper LRU wants a list and a pointer per entry and a write on every
	// read, and what this needs is a bound. When live fills up it becomes old
	// and a fresh map takes its place, so a host still being crawled is found
	// in old and copied forward and a host the crawl has finished with is
	// dropped whole the next time that happens. The cost is holding up to
	// twice Size and the benefit is that a read of a warm entry takes one map
	// lookup and no bookkeeping.
	live map[string]*record
	old  map[string]*record

	// flight is the lookups happening right now, so that the second goroutine
	// to want a host waits on the first one's answer rather than asking again.
	flight map[string]*lookup

	hits     atomic.Int64
	misses   atomic.Int64
	joined   atomic.Int64
	failures atomic.Int64
}

// A record is what the cache holds for one host: the addresses if it resolved,
// the error if it did not, and when to stop believing either.
type record struct {
	addrs []string
	err   error
	until time.Time
}

// A lookup is one query in flight. done is closed when the answer is in, and
// the fields are read only after that.
type lookup struct {
	done  chan struct{}
	addrs []string
	err   error
}

// NewNames builds the cache.
func NewNames(o NameOptions) *Names {
	if o.TTL <= 0 {
		o.TTL = DefaultNameTTL
	}
	if o.Miss <= 0 {
		o.Miss = DefaultMissTTL
	}
	if o.Size <= 0 {
		o.Size = DefaultNames
	}
	if o.Addrs <= 0 {
		o.Addrs = DefaultAddrs
	}
	if o.Resolver == nil {
		o.Resolver = net.DefaultResolver
	}
	return &Names{
		o:      o,
		live:   make(map[string]*record, o.Size),
		old:    map[string]*record{},
		flight: map[string]*lookup{},
	}
}

// Lookup returns the addresses of a host, from the cache when it can and from
// the resolver when it cannot.
//
// A host that did not resolve comes back as an error for as long as the cache
// remembers the failure, and it is the resolver's own error rather than a
// substitute, so a caller that distinguishes a refused name from a timeout
// still can.
func (n *Names) Lookup(ctx context.Context, host string) ([]string, error) {
	now := time.Now()

	n.mu.Lock()
	if r := n.find(host, now); r != nil {
		n.mu.Unlock()
		n.hits.Add(1)
		return r.addrs, r.err
	}
	if f, ok := n.flight[host]; ok {
		n.mu.Unlock()
		n.joined.Add(1)
		select {
		case <-f.done:
			return f.addrs, f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f := &lookup{done: make(chan struct{})}
	n.flight[host] = f
	n.mu.Unlock()

	n.misses.Add(1)
	f.addrs, f.err = n.ask(ctx, host)
	close(f.done)

	// A failure under a cancelled context says something about this caller and
	// nothing about the host, so it is not written down as a fact about the
	// host. The waiters already have it and the next caller asks again.
	keep := f.err == nil || ctx.Err() == nil

	n.mu.Lock()
	delete(n.flight, host)
	if keep {
		ttl := n.o.TTL
		if f.err != nil {
			ttl = n.o.Miss
		}
		n.store(host, &record{addrs: f.addrs, err: f.err, until: now.Add(ttl)})
	}
	n.mu.Unlock()

	if f.err != nil {
		n.failures.Add(1)
	}
	return f.addrs, f.err
}

// ask does the actual query and turns the answer into strings, since that is
// what the dialer wants and holding [net.IPAddr] would mean rebuilding them on
// every cache hit.
func (n *Names) ask(ctx context.Context, host string) ([]string, error) {
	ips, err := n.o.Resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("harvest: %s resolved to nothing", host)
	}
	addrs := make([]string, 0, len(ips))
	for _, ip := range ips {
		addrs = append(addrs, ip.String())
	}
	return addrs, nil
}

// find returns a live record for a host, moving one it found in the older
// generation forward so that a host still being crawled survives the next
// flip. The caller holds mu.
func (n *Names) find(host string, now time.Time) *record {
	if r, ok := n.live[host]; ok {
		if now.Before(r.until) {
			return r
		}
		delete(n.live, host)
		return nil
	}
	r, ok := n.old[host]
	if !ok {
		return nil
	}
	delete(n.old, host)
	if !now.Before(r.until) {
		return nil
	}
	n.live[host] = r
	return r
}

// store writes a record and flips the generations when the live one is full.
// The caller holds mu.
func (n *Names) store(host string, r *record) {
	if len(n.live) >= n.o.Size {
		n.old = n.live
		n.live = make(map[string]*record, n.o.Size)
	}
	n.live[host] = r
}

// NameStats is what the cache reports, for the progress line and for a test.
type NameStats struct {
	// Hits is lookups answered from the cache and Misses lookups that asked
	// the resolver. Joined is lookups that waited on a query somebody else had
	// already started, which is the number that says whether coalescing is
	// doing anything.
	Hits     int64
	Misses   int64
	Joined   int64
	Failures int64

	// Held is how many hosts the cache is holding across both generations.
	Held int
}

// Stats reports what the cache has been doing.
func (n *Names) Stats() NameStats {
	n.mu.Lock()
	held := len(n.live) + len(n.old)
	n.mu.Unlock()
	return NameStats{
		Hits:     n.hits.Load(),
		Misses:   n.misses.Load(),
		Joined:   n.joined.Load(),
		Failures: n.failures.Load(),
		Held:     held,
	}
}

// DialContext returns the dial function a transport should use, which resolves
// through the cache and then dials the addresses it got back.
//
// The dialer is handed an address rather than a name, so net's own resolution
// is out of the path entirely. TLS is unaffected: [http.Transport] takes the
// server name from the URL and not from what was dialed, so a host on a shared
// address still gets its own certificate.
func (n *Names) DialContext(d *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return n.dialWith(d.DialContext)
}

// dialWith is [Names.DialContext] with the dialer given as a function rather
// than as a [net.Dialer], which is how a test counts the addresses actually
// tried without depending on what is listening where.
func (n *Names) dialWith(dial dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		// A URL that already carries an address has nothing to look up, and a
		// crawl does meet those.
		if net.ParseIP(host) != nil {
			return dial(ctx, network, addr)
		}
		addrs, err := n.Lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		var last error
		for i, a := range addrs {
			if i >= n.o.Addrs {
				break
			}
			conn, err := dial(ctx, network, net.JoinHostPort(a, port))
			if err == nil {
				return conn, nil
			}
			last = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, last
	}
}
