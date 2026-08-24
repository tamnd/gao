package harvest

// Giving up on a host that is not there.
//
// A frontier grows by following links, and a link is a claim about a host that
// nobody has checked. Most of those claims are good. The ones that are not are
// hosts that no longer resolve, servers that accept a connection and then say
// nothing, and domains that were sold and now answer with a parking page over a
// certificate for somebody else. There are a lot of them: the four minute run
// on the Common Crawl seed met 4,657 hosts that produced a fetch failure, and
// they produced 8,139 of them between them.
//
// The cost of one of those is not the failure, it is the deadline in front of
// it. The whole point of the short deadlines in transport.go is to make a dead
// host cheap; the point of this file is to stop paying even that, more than
// once or twice, for a host that has already shown what it is.
//
// The rule is deliberately blunt in one direction and generous in the other. A
// host that has never sent a response is cut off after a small number of
// failures. A host that has ever sent one is never cut off at all, no matter how
// many times it fails afterwards, because a host that answers sometimes is a
// busy host rather than a dead one, and a crawl that abandoned those would
// abandon exactly the popular Vietnamese sites it is here for.

import (
	"errors"
	"sync"
	"time"
)

// DefaultStrikes is how many failures a host that has never answered gets
// before the crawl stops asking.
//
// Three rather than one. A single failure is not evidence: a DNS lookup can
// lose a packet, a handshake can land in the middle of a deploy, and the first
// URL the crawl has for a host is often the one link on the page that was
// already broken when it was written. Three consecutive failures with no
// response ever seen is a different claim, and on the seed crawl the hosts that
// reached three went on to fail every remaining time.
const DefaultStrikes = 3

// ErrDead is returned for a host that has failed [DefaultStrikes] times without
// ever answering. Nothing is sent.
var ErrDead = errors.New("harvest: the host has not answered")

// breakerShards is how many independent maps the breaker keeps, and it is the
// difference between a crawl that scales with workers and one that does not.
//
// Every URL takes this lock twice, once to ask whether the host is worth trying
// and once to record what came back, and it was one lock over one map for the
// whole box. A goroutine dump of a 2,000 worker run on server2 had 1,456 workers
// standing in it: 705 in dead, 686 in answered and 65 in failed, out of 1,670
// waiting on any mutex anywhere in the process. It was the largest single group
// in the dump, ahead of the 1,851 actually on the network, and it was larger
// than every other lock in the crawl put together. The frontier, which is the
// thing that looks expensive, had six.
//
// A host only ever needs its own record, so nothing here needs one lock. The
// shard is picked by the host's name, which means two hosts collide only by
// accident rather than by design, and 256 of them turn a queue two thousand deep
// into a queue that is almost always empty.
const breakerShards = 256

// A breaker remembers which hosts have answered and which have only failed.
//
// It is safe for concurrent use and is shared by every worker, since whether a
// host is reachable is a fact about the network rather than about a goroutine.
type breaker struct {
	strikes int
	now     func() time.Time

	shards [breakerShards]breakerShard
}

// breakerShard is one of the maps and the lock over it.
//
// The padding is not decoration. A mutex and a map header are sixteen bytes
// between them, so four shards would share a cache line and a worker taking one
// would be invalidating the line under three others it never touches. Filling
// the line means a shard is contended only when two workers want the same one.
type breakerShard struct {
	mu    sync.Mutex
	hosts map[string]*hostHealth
	_     [40]byte
}

// shard is the map a host's record lives in.
//
// FNV-1a written out rather than through hash/fnv, because that returns an
// interface holding a pointer and this is called twice for every URL the crawl
// fetches. A host name is short and this is a few nanoseconds of arithmetic.
func (b *breaker) shard(host string) *breakerShard {
	h := uint32(2166136261)
	for i := 0; i < len(host); i++ {
		h ^= uint32(host[i])
		h *= 16777619
	}
	return &b.shards[h%breakerShards]
}

type hostHealth struct {
	// fails counts failures since the host last answered, which for a host
	// that has never answered is every failure it has had.
	fails int

	// alive is set the first time the host sends anything at all, including a
	// 404 or a 500. It is never unset. A server that returns an error is a
	// server, and the next URL on it may well be a page.
	alive bool

	// at is the last time anything asked about this host, which is what decides
	// whether the entry is still worth its memory. See [breaker.forget].
	at time.Time
}

func newBreaker(strikes int, now func() time.Time) *breaker {
	if strikes <= 0 {
		strikes = DefaultStrikes
	}
	if now == nil {
		now = time.Now
	}
	b := &breaker{strikes: strikes, now: now}
	for i := range b.shards {
		b.shards[i].hosts = map[string]*hostHealth{}
	}
	return b
}

// dead reports whether the crawl has given up on a host.
func (b *breaker) dead(host string) bool {
	// Read the clock before taking the lock rather than under it. It is the
	// cheapest call in this file and it was still being made with the busiest
	// lock in the crawl held.
	now := b.now()
	s := b.shard(host)

	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.hosts[host]
	if !ok {
		return false
	}
	h.at = now
	return !h.alive && h.fails >= b.strikes
}

// answered records that a host sent a response, which immunizes it for the rest
// of the run.
func (b *breaker) answered(host string) {
	now := b.now()
	s := b.shard(host)

	s.mu.Lock()
	defer s.mu.Unlock()

	h := s.health(host, now)
	h.alive = true
	h.fails = 0
}

// failed records that a request to a host produced no response at all.
func (b *breaker) failed(host string) {
	now := b.now()
	s := b.shard(host)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.health(host, now).fails++
}

// health returns the record for a host, creating it on first sight. The caller
// holds the shard's lock and has already read the clock.
func (s *breakerShard) health(name string, now time.Time) *hostHealth {
	h, ok := s.hosts[name]
	if !ok {
		h = &hostHealth{}
		s.hosts[name] = h
	}
	h.at = now
	return h
}

// forget drops what is remembered about hosts nothing has asked about in older,
// and reports how many it dropped. See [Crawler.Forget] for why.
//
// A forgotten host starts again with no strikes against it, which is the right
// answer rather than a cost. The hosts this cuts off are ones that did not
// answer, and a host that did not answer an hour ago deserves the three tries it
// gets when the crawl next finds a link to it.
func (b *breaker) forget(now time.Time, older time.Duration) int {
	var dropped int
	// A shard at a time rather than all of them at once. Nothing here needs the
	// shards to agree with each other, and holding one lock at a time means a
	// worker whose host is in another shard never waits for this at all.
	for i := range b.shards {
		s := &b.shards[i]
		s.mu.Lock()
		for name, h := range s.hosts {
			if now.Sub(h.at) <= older {
				continue
			}
			delete(s.hosts, name)
			dropped++
		}
		s.mu.Unlock()
	}
	return dropped
}

// Dropped is how many hosts the crawl has given up on, which is the number a
// long run watches: a crawl dropping a rising share of what it meets has either
// found a bad neighborhood of the web or has broken its own networking.
func (b *breaker) dropped() int {
	n := 0
	for i := range b.shards {
		s := &b.shards[i]
		s.mu.Lock()
		for _, h := range s.hosts {
			if !h.alive && h.fails >= b.strikes {
				n++
			}
		}
		s.mu.Unlock()
	}
	return n
}
