package harvest

// The client the crawl fetches pages with.
//
// Go's default transport is built for a program that talks to a handful of
// services. A crawl talks to a hundred thousand, most of them once, and the
// defaults are wrong for it in three separate ways: the idle pool is a hundred
// connections shared by every host, the whole pool is behind one mutex that
// every worker takes on every request, and the only deadline is on the request
// as a whole, so a server that accepts the connection and then says nothing
// costs the full timeout.
//
// The third one is the expensive one and it was measured rather than guessed.
// On a four minute run over the Common Crawl seed, 795 of 28,038 requests ended
// in a timeout at thirty seconds each, which is 23,850 worker seconds out of the
// 130,500 the run had: eighteen percent of the crawl spent waiting for servers
// that were never going to answer. Another 1,041 were a host that does not
// resolve and 643 were a TLS handshake that never completed.

import (
	"crypto/tls"
	"hash/fnv"
	"net"
	"net/http"
	"time"
)

// DefaultShards is how many keep-alive pools the crawl spreads its hosts over.
//
// One [http.Transport] holds its idle connections and its per-host accounting
// under a single mutex. At twenty workers that mutex is never contended and at
// five hundred it is taken five hundred times a second by goroutines that are
// otherwise doing nothing but waiting on a socket. Sharding is the whole fix: a
// host is hashed to a shard and stays there, so its keep-alive connection is
// still found on the next request to it, and the lock behind that connection is
// shared with a thirty second of the crawl rather than all of it.
const DefaultShards = 32

// DefaultHeaderTimeout is how long a server has to begin its answer.
//
// This is the deadline that matters, and it is deliberately much shorter than
// the one on the request as a whole. A server that has completed a TCP
// connection and a TLS handshake and has then sent no header byte for five
// seconds is, on the evidence of the seed crawl, not a slow server. It is a
// parked domain, a default vhost, or a load balancer with nothing behind it,
// and the page it eventually does not send is not worth thirty seconds of a
// worker.
//
// A slow but real page is not what this cuts. The body still gets
// [DefaultFetchTimeout] once the header has arrived, which is where a large
// page on a distant host actually spends its time.
const DefaultHeaderTimeout = 5 * time.Second

// DefaultFetchTimeout bounds the whole exchange, header and body together.
const DefaultFetchTimeout = 20 * time.Second

// DefaultDialTimeout bounds the TCP connection, and DefaultTLSTimeout the
// handshake after it. Both are separate from the header deadline because a host
// that is unreachable should not be discovered by waiting out a deadline meant
// for a host that is.
const (
	DefaultDialTimeout = 5 * time.Second
	DefaultTLSTimeout  = 5 * time.Second
)

// DefaultIdleConns is how many keep-alive connections the crawl holds open
// across all shards, and DefaultIdleTimeout is how long an unused one is kept.
//
// A crawl returns to a host about once a second while it has URLs for it and
// then never again, so the useful lifetime of a connection is the length of one
// host's queue. Holding it much longer is a socket and a file descriptor spent
// on a host the crawl has finished with, and on a box with a 1024 descriptor
// limit that is the thing that breaks first.
const (
	DefaultIdleConns   = 4096
	DefaultIdleTimeout = 30 * time.Second
)

// TransportOptions configures a [Fleet]. The zero value is the defaults above.
type TransportOptions struct {
	// Shards is how many transports to build. Zero means [DefaultShards].
	Shards int

	// Names is the name cache to dial through, shared by every shard. Nil
	// means one built with the defaults. See [Names] for why a crawl wants
	// one at all.
	Names *Names

	// Header, Dial and TLS are the three deadlines before the body starts.
	// Zero means the defaults.
	Header time.Duration
	Dial   time.Duration
	TLS    time.Duration

	// IdleConns and IdleTimeout size the keep-alive pool. Zero means the
	// defaults.
	IdleConns   int
	IdleTimeout time.Duration

	// Verify turns certificate checking back on.
	//
	// It is off by default and that is a deliberate choice rather than an
	// oversight. The crawl reads what a site publishes to anyone with a
	// browser, it does not authenticate the site, and it does not act on what
	// it reads. An expired or self signed certificate on a provincial news
	// site is the most common TLS failure in the seed crawl, 643 of 28,038
	// requests, and the pages behind those certificates are ordinary Vietnamese
	// pages that a corpus wants.
	//
	// What that costs is real and worth saying plainly. A response the crawl
	// cannot authenticate is a response somebody on the path could have
	// written, so a page fetched this way is evidence about what was on the
	// wire and not about what the site published. Every page carries its host,
	// its fetch time and its archive locator, so a reader who cares can go back
	// and check. Set this when that is not good enough.
	Verify bool
}

// A Fleet is the sharded set of transports a crawl fetches through.
//
// It implements [http.RoundTripper], so a caller hands it to an [http.Client]
// and never thinks about the shards again. The sharding is by host, so every
// request to one host goes through the same pool and finds the same keep-alive
// connection.
type Fleet struct {
	shards []*http.Transport

	// names is the cache every shard dials through. It is here rather than
	// per shard because a host belongs to one shard and its address does not:
	// two hosts on one address, which is most of the shared hosting in the
	// seed, should be one entry and not two.
	names *Names
}

// NewFleet builds the transports.
func NewFleet(o TransportOptions) *Fleet {
	if o.Shards <= 0 {
		o.Shards = DefaultShards
	}
	if o.Header <= 0 {
		o.Header = DefaultHeaderTimeout
	}
	if o.Dial <= 0 {
		o.Dial = DefaultDialTimeout
	}
	if o.TLS <= 0 {
		o.TLS = DefaultTLSTimeout
	}
	if o.IdleConns <= 0 {
		o.IdleConns = DefaultIdleConns
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = DefaultIdleTimeout
	}

	if o.Names == nil {
		o.Names = NewNames(NameOptions{})
	}

	f := &Fleet{shards: make([]*http.Transport, o.Shards), names: o.Names}
	dial := o.Names.DialContext(&net.Dialer{
		Timeout: o.Dial,
		// Keep-alive probes on a socket the crawl is about to stop using are
		// packets nobody reads, and a crawl's sockets are all about to stop
		// being used.
		KeepAlive: -1,
	})
	per := o.IdleConns / o.Shards
	if per < 1 {
		per = 1
	}
	for i := range f.shards {
		f.shards[i] = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dial,
			TLSHandshakeTimeout:   o.TLS,
			ResponseHeaderTimeout: o.Header,
			ExpectContinueTimeout: time.Second,
			MaxIdleConns:          per,
			// Two per host rather than the default's two, said explicitly
			// because the number is load bearing here: the schedule allows one
			// request to a host at a time, so one connection is what a host
			// needs and the second is headroom for the moment a redirect or a
			// robots fetch overlaps the page.
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     o.IdleTimeout,
			ForceAttemptHTTP2:   true,
			TLSClientConfig:     tlsConfig(o.Verify),
		}
	}
	return f
}

// tlsConfig returns the client config, which is the zero value when the caller
// asked for verification and one with it turned off when it did not. See
// [TransportOptions.Verify] for why the second one is the default.
func tlsConfig(verify bool) *tls.Config {
	if verify {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	//nolint:gosec // G402: archiving public pages, not authenticating peers.
	return &tls.Config{InsecureSkipVerify: true}
}

// RoundTrip implements [http.RoundTripper], sending the request through the
// shard its host belongs to.
func (f *Fleet) RoundTrip(r *http.Request) (*http.Response, error) {
	return f.shards[f.shard(r.URL.Hostname())].RoundTrip(r)
}

// shard picks the pool for a host. FNV rather than anything stronger because
// the input is a hostname and the output is an index: an attacker who can
// unbalance the shards has achieved a slightly warmer mutex.
func (f *Fleet) shard(host string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(host))
	return int(h.Sum32() % uint32(len(f.shards)))
}

// Shards is how many pools this fleet has, which is what a test asserts on and
// what a report prints.
func (f *Fleet) Shards() int { return len(f.shards) }

// Names is the cache this fleet dials through, so a caller can report what it
// has been doing.
func (f *Fleet) Names() *Names { return f.names }

// CloseIdle closes every idle connection in every shard, which is what a crawl
// does when it has finished and wants its file descriptors back before it
// starts writing Parquet.
func (f *Fleet) CloseIdle() {
	for _, t := range f.shards {
		t.CloseIdleConnections()
	}
}

// NewClient returns the [http.Client] a crawl fetches with: a [Fleet]
// underneath, a whole-request deadline on top, and redirects returned rather
// than followed.
//
// The redirect policy is not a detail. A redirect can cross to another host,
// where a different robots.txt applies and a different schedule is owed, so
// following one inside the client would make a request that nothing checked.
// [Crawler.Get] hands the Location back to the frontier instead.
func NewClient(o TransportOptions, total time.Duration) *http.Client {
	if total <= 0 {
		total = DefaultFetchTimeout
	}
	return &http.Client{
		Transport: NewFleet(o),
		Timeout:   total,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
