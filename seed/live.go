package seed

// Screening a host list for hosts that answer.
//
// A seed list is leads rather than sites, and for Certificate Transparency that
// is the right trade: a dead lead costs one request that fails fast. At the size
// the crawl now works at the trade stops holding, because the requests are not
// fast and there are not a few of them.
//
// The measurement that put this file here. A crawl seeded with 20,000 hosts out
// of the published corpus fetched 1,482 pages and failed 4,924 times, and 3,548
// of those failures were timeouts rather than refusals. At the 20 second default
// that is 70,960 worker seconds spent waiting on hosts that never replied,
// against 100,000 worker seconds available in the run. Roughly 71% of the crawl
// went on hosts that were not there. Throughput fell from 33 pages a second on a
// 151 host seed list to 7.4 on the wide one, so the extra breadth cost more than
// it bought.
//
// Cutting the timeout does not fix it. The same list at -timeout 5s gives 0.8
// pages a second, because a short deadline turns slow but real hosts into
// failures too. The fix is to find out which hosts answer before the crawl
// spends a worker on them, which is what this does: one DNS lookup and one TCP
// connect per host, on no workers at all, at a cost per host of milliseconds
// rather than the 20 seconds a dead host costs the crawler.
//
// This does not fetch anything. It answers "is there something listening" and
// nothing else. Whether the host serves Vietnamese, whether robots.txt allows
// the fetch, whether the page is worth keeping are all questions for the crawl,
// and asking any of them here would mean making requests, which is the cost this
// exists to avoid.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultProbeTimeout bounds one host's lookup and one host's connect
// separately, so a host gets at most twice this before it is called dead.
//
// Three seconds rather than the crawler's twenty. This is deliberately less
// forgiving than a fetch, because the two are answering different questions. A
// fetch that gives up early loses the page. A probe that gives up early costs a
// live host its place in the seed list, and the crawl finds it again through a
// link from somewhere else. The asymmetry is worth the speed: at twenty seconds
// a screening pass over 774,205 hosts is not a thing anybody runs.
const DefaultProbeTimeout = 3 * time.Second

// DefaultProbeBatch is how many hosts are probed between pauses.
//
// This is not a concurrency limit, it is a volume limit, and the difference is
// the whole reason the field exists. Probing 2,000 hosts at concurrency 32, 100
// and 400 gives 64.5%, 64.8% and 64.7% live, so concurrency does not move the
// answer. Probing 20,000 in one unbroken pass reports 95.6% with no DNS, which
// is not a fact about those hosts. It is the resolver falling over partway
// through and returning failures for everything after that.
//
// A screening pass that silently turns into a resolver outage is worse than no
// screening pass, because it produces a short list of live hosts that looks
// exactly like a correct answer. Pausing between batches keeps the resolver
// inside the volume it survives.
const DefaultProbeBatch = 1500

// DefaultProbeRest is how long to wait between batches.
const DefaultProbeRest = 10 * time.Second

// Liveness is what a probe found out about one host.
type Liveness struct {
	Name string
	Live bool

	// Why is empty when Live, and otherwise says which step failed. It is kept
	// rather than reduced to the boolean because the two failures mean different
	// things to whoever reads the list. A host with no DNS is gone. A host that
	// resolves and refuses a connection is a host whose name still points
	// somewhere, which is worth probing again later.
	Why string

	// Addr is the address that answered, so a caller can see a whole seed list
	// collapse onto one parking page's IP, which is what a registrar's expired
	// domain service looks like from here.
	Addr string
}

// ProbeOptions configures [Probe]. The zero value is usable and uses the
// defaults above.
type ProbeOptions struct {
	Timeout     time.Duration
	Concurrency int
	Batch       int
	Rest        time.Duration

	// Resolver is the resolver to use. A caller screening the whole inventory
	// should set this to one pointed at a resolver it controls rather than the
	// box's, because the box's is the thing that falls over.
	Resolver *net.Resolver

	// Dialer is how the TCP connect is made. Left nil, one is built from
	// Timeout.
	Dialer *net.Dialer

	// Ports are tried in order until one answers. Left nil, 443 then 80, which
	// is the order that matters: a host that serves both is reached over TLS by
	// the crawler anyway, so probing 443 first means the probe and the fetch are
	// asking about the same listener.
	Ports []string

	// Progress, if set, is called after each batch with how many hosts have been
	// probed and how many of those were live. A pass over the full inventory
	// takes long enough that a caller with no output looks hung.
	Progress func(done, live int)
}

func (o *ProbeOptions) fill() {
	if o.Timeout <= 0 {
		o.Timeout = DefaultProbeTimeout
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 100
	}
	if o.Batch <= 0 {
		o.Batch = DefaultProbeBatch
	}
	if o.Rest < 0 {
		o.Rest = DefaultProbeRest
	}
	if o.Resolver == nil {
		o.Resolver = net.DefaultResolver
	}
	if o.Dialer == nil {
		o.Dialer = &net.Dialer{Timeout: o.Timeout}
	}
	if len(o.Ports) == 0 {
		o.Ports = []string{"443", "80"}
	}
}

// Probe screens hosts and reports which of them answer.
//
// The results come back in the order the hosts were given rather than the order
// the probes finished, so a caller can diff two passes over the same list and
// see what changed rather than what got reordered.
//
// A canceled context stops the pass and returns what was found so far along
// with the context's error. That is deliberate: a screening pass over the whole
// inventory runs for an hour, and half a screened list is worth keeping.
func Probe(ctx context.Context, hosts []string, o ProbeOptions) ([]Liveness, error) {
	o.fill()

	out := make([]Liveness, len(hosts))
	var live int

	for start := 0; start < len(hosts); start += o.Batch {
		if err := ctx.Err(); err != nil {
			return out[:start], err
		}
		end := min(start+o.Batch, len(hosts))

		var wg sync.WaitGroup
		sem := make(chan struct{}, o.Concurrency)
		for i := start; i < end; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				out[i] = probeOne(ctx, hosts[i], o)
			}(i)
		}
		wg.Wait()

		for i := start; i < end; i++ {
			if out[i].Live {
				live++
			}
		}
		if o.Progress != nil {
			o.Progress(end, live)
		}
		// No rest after the last batch. Waiting ten seconds to return is ten
		// seconds of somebody watching a finished job.
		if end < len(hosts) && o.Rest > 0 {
			select {
			case <-ctx.Done():
				return out[:end], ctx.Err()
			case <-time.After(o.Rest):
			}
		}
	}
	return out, nil
}

// probeOne is the lookup and the connect for a single host.
func probeOne(ctx context.Context, host string, o ProbeOptions) Liveness {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return Liveness{Name: host, Why: "empty"}
	}

	lookup, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	addrs, err := o.Resolver.LookupHost(lookup, host)
	if err != nil || len(addrs) == 0 {
		return Liveness{Name: host, Why: dnsWhy(err)}
	}

	for _, port := range o.Ports {
		dial, cancel := context.WithTimeout(ctx, o.Timeout)
		c, err := o.Dialer.DialContext(dial, "tcp", net.JoinHostPort(host, port))
		cancel()
		if err == nil {
			addr := c.RemoteAddr().String()
			_ = c.Close()
			return Liveness{Name: host, Live: true, Addr: addr}
		}
	}
	return Liveness{Name: host, Why: "no listener", Addr: addrs[0]}
}

// dnsWhy separates a name that does not exist from a lookup that did not finish,
// because they are different facts. The first is settled and the second is this
// box's problem, and a screening pass whose failures are mostly the second one
// has measured the resolver rather than the hosts.
func dnsWhy(err error) string {
	if err == nil {
		return "no address"
	}
	if d, ok := errors.AsType[*net.DNSError](err); ok {
		switch {
		case d.IsNotFound:
			return "no such host"
		case d.IsTimeout:
			return "dns timeout"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "dns timeout"
	}
	return "dns error"
}

// Live is the hosts from a probe that answered, in the order they were given.
func Live(rs []Liveness) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		if r.Live {
			out = append(out, r.Name)
		}
	}
	return out
}

// ProbeStats is the tally of a pass, which is the thing worth printing.
type ProbeStats struct {
	Total int
	Live  int
	By    map[string]int // why, for the ones that were not live
}

// Tally counts a probe's results.
func Tally(rs []Liveness) ProbeStats {
	s := ProbeStats{Total: len(rs), By: map[string]int{}}
	for _, r := range rs {
		if r.Live {
			s.Live++
			continue
		}
		s.By[r.Why]++
	}
	return s
}

// String renders the tally as the lines a person reads.
//
// The share of failures that are DNS timeouts rather than names that do not
// exist is called out rather than left in the list, because that one number is
// how somebody notices they have measured their resolver instead of the web.
func (s ProbeStats) String() string {
	var b strings.Builder
	if s.Total == 0 {
		return "no hosts probed\n"
	}
	fmt.Fprintf(&b, "%d hosts, %d live (%.1f%%)\n", s.Total, s.Live, pct(s.Live, s.Total))

	type row struct {
		why string
		n   int
	}
	rows := make([]row, 0, len(s.By))
	for w, n := range s.By {
		rows = append(rows, row{w, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].why < rows[j].why
	})
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-14s %7d  %5.1f%%\n", r.why, r.n, pct(r.n, s.Total))
	}
	if t := s.By["dns timeout"]; t > 0 && s.Total-s.Live > 0 && pct(t, s.Total-s.Live) > 20 {
		fmt.Fprintf(&b, "\n%.0f%% of the failures are DNS timeouts rather than names that do not exist, which is the resolver rather than the hosts. Probe in smaller batches or point -resolver somewhere that can take it.\n",
			pct(t, s.Total-s.Live))
	}
	return b.String()
}

func pct(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return 100 * float64(n) / float64(of)
}
