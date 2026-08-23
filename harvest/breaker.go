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

// A breaker remembers which hosts have answered and which have only failed.
//
// It is safe for concurrent use and is shared by every worker, since whether a
// host is reachable is a fact about the network rather than about a goroutine.
type breaker struct {
	strikes int
	now     func() time.Time

	mu    sync.Mutex
	hosts map[string]*hostHealth
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
	return &breaker{strikes: strikes, now: now, hosts: map[string]*hostHealth{}}
}

// dead reports whether the crawl has given up on a host.
func (b *breaker) dead(host string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	h, ok := b.hosts[host]
	if !ok {
		return false
	}
	h.at = b.now()
	return !h.alive && h.fails >= b.strikes
}

// answered records that a host sent a response, which immunizes it for the rest
// of the run.
func (b *breaker) answered(host string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	h := b.health(host)
	h.alive = true
	h.fails = 0
}

// failed records that a request to a host produced no response at all.
func (b *breaker) failed(host string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.health(host).fails++
}

// health returns the record for a host, creating it on first sight. The caller
// holds the lock.
func (b *breaker) health(name string) *hostHealth {
	h, ok := b.hosts[name]
	if !ok {
		h = &hostHealth{}
		b.hosts[name] = h
	}
	h.at = b.now()
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
	b.mu.Lock()
	defer b.mu.Unlock()

	var dropped int
	for name, h := range b.hosts {
		if now.Sub(h.at) <= older {
			continue
		}
		delete(b.hosts, name)
		dropped++
	}
	return dropped
}

// Dropped is how many hosts the crawl has given up on, which is the number a
// long run watches: a crawl dropping a rising share of what it meets has either
// found a bad neighborhood of the web or has broken its own networking.
func (b *breaker) dropped() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := 0
	for _, h := range b.hosts {
		if !h.alive && h.fails >= b.strikes {
			n++
		}
	}
	return n
}
