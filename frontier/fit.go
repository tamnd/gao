package frontier

// Whether the frontier fits on the box that has to hold it.
//
// The gate on the crawl says the frontier and the seen set have to fit inside
// server1's memory before the first fetch, or the crawl does not start.
// Discovering otherwise at a hundred million fetches is discovering it too
// late: the frontier is the one structure that cannot be rebuilt from the WARCs
// afterwards, and a crawler that is killed by the kernel at 4 a.m. loses the
// record of what it has already asked for, which is the only thing standing
// between it and asking again.
//
// So the arithmetic is written down and it is checkable two ways. Cost works it
// out from the sizes of the structures that actually hold it, taken from the
// types rather than typed in by hand. Measure builds a real frontier at a
// fraction of the scale and reads the heap, which is the number that settles an
// argument, and it runs on the box rather than on a whiteboard.
//
// The first version of this said no, which is the whole reason it was worth
// writing. Holding a ledger for every one of the 900,000 hosts, each tracked at
// two dozen templates with a few dozen URLs queued behind it, comes to 12.26 GB,
// and server1 has 5.01 GB once the reserve is off. Nothing about that is
// recoverable at run time, and a crawl that finds it out by being killed finds
// it out after losing the seen set. The answer is the Active field: the ledgers
// page out with the frontier they belong to, and only the hosts being fetched
// from right now are resident. That is a design decision this file forced, and
// it is the kind of thing a gate is supposed to force before the first fetch
// rather than after a hundred million of them.

import (
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"

	"github.com/tamnd/gao/fleet"
)

// Reserve is what the crawl does not get: the kernel, the socket buffers under
// several hundred concurrent fetches, and the WARC writer's roll buffers. It
// leaves server1 with almost exactly the 5 GB the gate is written against,
// which is where that number comes from rather than from rounding.
const Reserve = 800 << 20

// A Plan is the crawl the frontier has to hold. Every field is a number that
// moves the answer, which is the reason each one is a field rather than a
// constant buried in the arithmetic.
type Plan struct {
	// URLs is the seed frontier and Hosts is what it spreads across.
	URLs  int64 `json:"urls"`
	Hosts int64 `json:"hosts"`

	// Active is how many of those hosts are resident at once. The rest of the
	// ledgers are on disk with the rest of the frontier, and a host comes back
	// into memory when it comes back into rotation. Everything below except the
	// seen filter is charged against this number rather than against Hosts,
	// which is the difference between a frontier that fits and one that does
	// not.
	Active int64 `json:"active"`

	// SeenBits is bits per URL in the resident filter that sits in front of the
	// exact seen set. The exact set is on disk. The filter is what stops a disk
	// read for every URL offered, and it is the single largest resident thing
	// here, so it is the first number to argue about.
	SeenBits int `json:"seen_bits"`

	// ShapesPerHost is how many distinct URL templates a host is tracked at, and
	// it is the number that decides whether the per host ledgers are a rounding
	// error or the whole budget.
	ShapesPerHost int `json:"shapes_per_host"`

	// FacetPathsPerHost and FacetsPerPath are the facet trap detector's memory:
	// per path, the distinct query key combinations seen. A catalog with four
	// filters on it is what this exists to catch and it is also what makes it
	// expensive.
	FacetPathsPerHost int `json:"facet_paths_per_host"`
	FacetsPerPath     int `json:"facets_per_path"`

	// ReadyPerHost is how many URLs are held in memory per active host awaiting
	// a fetch. At one request every two seconds to a host, thirty two of them is
	// a minute of work queued, and the next batch has a minute to come off disk.
	// Holding all 280 million resident is not a design, it is the thing this
	// check exists to refuse.
	ReadyPerHost int `json:"ready_per_host"`

	// URLBytes and HostBytes are mean lengths. Vietnamese URLs run long because
	// the slug is usually the headline with the tone marks stripped.
	URLBytes  int `json:"url_bytes"`
	HostBytes int `json:"host_bytes"`
}

// Frontier is the crawl S3 is written against.
func Frontier() Plan {
	return Plan{
		URLs:              280_000_000,
		Hosts:             900_000,
		Active:            50_000,
		SeenBits:          10,
		ShapesPerHost:     24,
		FacetPathsPerHost: 4,
		FacetsPerPath:     8,
		ReadyPerHost:      32,
		URLBytes:          96,
		HostBytes:         20,
	}
}

// A Cost is what the plan costs to hold, broken into the parts worth arguing
// with separately.
type Cost struct {
	Plan Plan `json:"plan"`

	// Seen is the resident filter, Ledgers is one record per host, Shapes is the
	// per template tallies, Facets is the trap detector, and Ready is the URLs
	// held in memory waiting to be fetched.
	Seen    int64 `json:"seen"`
	Ledgers int64 `json:"ledgers"`
	Shapes  int64 `json:"shapes"`
	Facets  int64 `json:"facets"`
	Ready   int64 `json:"ready"`
	Total   int64 `json:"total"`

	// PerHost is what one active host costs in ledger, tallies and facets. It
	// leaves out the ready queue on purpose, because this is the number that
	// gets compared against a measurement and the measurement cannot see a
	// queue the budget does not hold.
	PerHost int64 `json:"per_host"`

	// FalsePositive is the filter's error rate at this many bits per URL, and
	// Reads is how many disk reads that costs over the whole crawl. A false
	// positive here costs a lookup in the exact set rather than a lost URL,
	// which is the entire reason the exact set is on disk instead of nowhere.
	FalsePositive float64 `json:"false_positive"`
	Reads         int64   `json:"reads"`
}

// Sizes taken from the structures rather than typed in, so that adding a field
// to a ledger moves this number instead of quietly invalidating it.
var (
	wordSize   = int64(reflect.TypeFor[uintptr]().Size())
	stringSize = int64(reflect.TypeFor[string]().Size())
	ledgerSize = int64(reflect.TypeFor[ledger]().Size())
	tallySize  = int64(reflect.TypeFor[tally]().Size())
	boolMap    = int64(reflect.TypeFor[map[string]bool]().Size())
)

// mapEntry is what one entry in a Go map costs beyond its key and its value:
// the slot's share of the control word and of the empty space a hash table is
// grown to keep. The runtime's map layout is not a documented interface, so this
// is an estimate, and it is why Measure exists. An arithmetic nobody checks
// against a heap is a guess with a number beside it.
const mapEntry = 16

// Cost works out what the plan holds resident.
func (p Plan) Cost() Cost {
	c := Cost{Plan: p}

	// The seen filter is the one thing charged against the whole crawl rather
	// than against the active set, because the point of it is to answer for a
	// URL on a host nobody has touched in a week.
	c.Seen = (p.URLs*int64(p.SeenBits) + 7) / 8

	// One map entry per active host: the key, a pointer to the ledger, and the
	// ledger. Each ledger carries two maps of its own, whose headers come with
	// it.
	c.Ledgers = p.Active * (stringSize + int64(p.HostBytes) + wordSize + mapEntry + ledgerSize)

	// One map entry per template per active host, keyed by the shape as text.
	shapeKey := int64(p.URLBytes / 2)
	c.Shapes = p.Active * int64(p.ShapesPerHost) * (stringSize + shapeKey + wordSize + mapEntry + tallySize)

	// The facet detector holds a map per path and a set inside it.
	perPath := stringSize + shapeKey + boolMap + mapEntry
	perFacet := stringSize + int64(p.URLBytes/4) + 1 + mapEntry
	c.Facets = p.Active * int64(p.FacetPathsPerHost) * (perPath + int64(p.FacetsPerPath)*perFacet)

	c.Ready = p.Active * int64(p.ReadyPerHost) * (stringSize + int64(p.URLBytes))

	c.Total = c.Seen + c.Ledgers + c.Shapes + c.Facets + c.Ready
	if p.Active > 0 {
		// Ready is left out, because the ready queues are not held by the budget
		// and the measurement below cannot see them. A number compared against a
		// measurement has to be the number the measurement covers.
		c.PerHost = (c.Ledgers + c.Shapes + c.Facets) / p.Active
	}

	c.FalsePositive = math.Pow(0.6185, float64(p.SeenBits))
	c.Reads = int64(c.FalsePositive * float64(p.URLs))
	return c
}

// Available is what a box has for the frontier once the reserve is taken off.
func Available(b fleet.Box) int64 {
	if n := b.Memory - Reserve; n > 0 {
		return n
	}
	return 0
}

// Fits reports whether the plan holds on the box.
func (c Cost) Fits(b fleet.Box) bool { return c.Total <= Available(b) }

// Headroom is the share of the box's frontier memory the plan does not use.
func (c Cost) Headroom(b fleet.Box) float64 {
	a := Available(b)
	if a <= 0 {
		return 0
	}
	return 1 - float64(c.Total)/float64(a)
}

// Blocking is every reason the crawl does not start on this box.
//
// Nothing here is advice. The gate says the frontier fits before the first
// fetch or the crawl does not start, and each of these is a way it does not.
func (c Cost) Blocking(b fleet.Box) []string {
	var out []string
	a := Available(b)
	if a <= 0 {
		out = append(out, fmt.Sprintf("%s has %s of memory and the reserve is %s, so there is nothing left for a frontier",
			b.Name, Bytes(b.Memory), Bytes(Reserve)))
		return out
	}
	if c.Total > a {
		out = append(out, fmt.Sprintf("the frontier holds %s resident and %s has %s for it, which is %s short. The crawl does not start until the plan changes or the box does",
			Bytes(c.Total), b.Name, Bytes(a), Bytes(c.Total-a)))
		if c.Plan.Active > 0 {
			out = append(out, fmt.Sprintf("at %d bytes per active host, %s is what fits, against the %s the plan asks to keep resident",
				c.PerHost, Count(c.fitsActive(a), "host"), Count(c.Plan.Active, "host")))
		}
	}
	if h := c.Headroom(b); c.Total <= a && h < 0.15 {
		out = append(out, fmt.Sprintf("the frontier fits with %.1f%% to spare, which is not enough to survive a host that turns out to have forty thousand templates on it rather than twenty four",
			100*h))
	}
	if c.Plan.SeenBits < 8 {
		out = append(out, fmt.Sprintf("the filter is %d bits per URL, which puts its error rate at %.1f%% and sends %s of the crawl to the exact set on disk. Below eight bits the filter is costing more in seeks than it saves in memory",
			c.Plan.SeenBits, 100*c.FalsePositive, plural(int(c.Reads), "lookup")))
	}
	if c.Plan.ReadyPerHost > 0 && c.Ready > c.Seen {
		out = append(out, fmt.Sprintf("the ready queues hold %s and the seen filter holds %s, so more memory is going to URLs waiting than to the record of what has already been asked for, which is the shape of a frontier that is resident rather than on disk",
			Bytes(c.Ready), Bytes(c.Seen)))
	}
	return out
}

// facetKeys are the query forms a Vietnamese catalog puts on a listing page:
// type, page, sort, price. Measure walks them so the facet maps get filled the
// way a real crawl fills them.
var facetKeys = [][]string{
	{"loai"},
	{"trang"},
	{"loai", "trang"},
	{"sap-xep"},
	{"loai", "sap-xep"},
	{"trang", "sap-xep"},
	{"gia", "loai", "trang"},
	{"gia"},
}

// fitsActive is how many hosts could be resident in the room a box has, which
// is the number somebody has to change the plan to when it does not fit.
func (c Cost) fitsActive(available int64) int64 {
	each := c.PerHost + int64(c.Plan.ReadyPerHost)*(stringSize+int64(c.Plan.URLBytes))
	if each <= 0 {
		return 0
	}
	if n := (available - c.Seen) / each; n > 0 {
		return n
	}
	return 0
}

// A Sample is a measurement rather than an arithmetic: a real frontier built at
// a fraction of the scale, with the heap read on either side of it.
type Sample struct {
	Hosts   int   `json:"hosts"`
	Shapes  int   `json:"shapes"`
	Offered int   `json:"offered"`
	Heap    int64 `json:"heap"`
	PerHost int64 `json:"per_host"`
}

// Measure builds a budget with the given number of hosts and templates per host
// and reports what it cost. It allocates, so the caller picks the scale.
//
// This is the number that settles the argument the constants above start. It
// covers the ledgers, the tallies and the facet maps, which is everything the
// budget holds, and it does not cover the seen filter or the ready queues,
// which the budget does not hold.
func Measure(hosts, shapes int) Sample {
	s := Sample{Hosts: hosts, Shapes: shapes}
	if hosts <= 0 || shapes <= 0 {
		return s
	}

	urls := make([]string, 0, hosts*shapes)
	for h := range hosts {
		for i := range shapes {
			u := fmt.Sprintf("https://dd%06d.vn/muc-%d/bai-viet-so-%d-ve-mot-chu-de-nao-do", h, i%8, i)
			if i%4 == 0 {
				// A quarter of them carry a query, so the measurement covers the
				// facet maps as well as the tallies. A frontier measured against
				// URLs that never had a parameter on them is a frontier measured
				// against a web that does not exist.
				u += "?" + strings.Join(facetKeys[(i/4)%len(facetKeys)], "=1&") + "=1"
			}
			urls = append(urls, u)
		}
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	// Every allowance is set past anything this can reach, because what is being
	// measured is what a ledger of this many hosts and templates weighs. A
	// refusal here would shrink the answer by not building the entry.
	b := NewBudget(Options{HostStart: 1 << 30, Reach: 1 << 30, ShapeStart: 1 << 30})
	for _, u := range urls {
		if ok, _ := b.Offer(u); ok {
			s.Offered++
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(b)

	s.Heap = max(int64(after.HeapAlloc)-int64(before.HeapAlloc), 0)
	s.PerHost = s.Heap / int64(hosts)
	return s
}

// Scaled is what the sample says the whole plan costs, which is the measurement
// standing in for the parts of the arithmetic it covers.
func (s Sample) Scaled(p Plan) int64 {
	seen := (p.URLs*int64(p.SeenBits) + 7) / 8
	ready := p.Active * int64(p.ReadyPerHost) * (stringSize + int64(p.URLBytes))
	return seen + ready + s.PerHost*p.Active
}

// Bytes writes a byte count the way somebody reading a memory budget wants it.
func Bytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// Describe is the plan in a sentence.
func (p Plan) Describe() string {
	return fmt.Sprintf("%s across %s, of which %s are resident at a time with %s queued behind each. The exact seen set is on disk behind a filter of %d bits per URL, and so is everything else, because holding the frontier resident is the thing this check exists to refuse.",
		Count(p.URLs, "URL"), Count(p.Hosts, "host"), Count(p.Active, "host"), plural(p.ReadyPerHost, "URL"), p.SeenBits)
}

// Count writes a large count with its noun, in the units somebody says out
// loud. Nine hundred thousand hosts is 900k hosts in conversation and nobody
// counts the zeroes.
func Count(n int64, noun string) string {
	switch {
	case n >= 100_000_000:
		return fmt.Sprintf("%.0f million %ss", float64(n)/1e6, noun)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f million %ss", float64(n)/1e6, noun)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk %ss", float64(n)/1e3, noun)
	default:
		return plural(int(n), noun)
	}
}

// Faults is every way the plan is not a plan.
func (p Plan) Faults() []string {
	var out []string
	if p.URLs <= 0 {
		out = append(out, "the frontier holds no URLs")
	}
	if p.Hosts <= 0 {
		out = append(out, "the frontier spreads across no hosts")
	}
	if p.URLs > 0 && p.Hosts > 0 && p.URLs/p.Hosts < 2 {
		out = append(out, fmt.Sprintf("%s across %s is fewer than two apiece, which is a seed list rather than a frontier and does not need a budget at all",
			Count(p.URLs, "URL"), Count(p.Hosts, "host")))
	}
	if p.Active <= 0 {
		out = append(out, "no host is resident, so there is nothing to fetch from and the arithmetic below is about an idle crawler")
	}
	if p.Active > p.Hosts && p.Hosts > 0 {
		out = append(out, fmt.Sprintf("%s are resident out of %s, which is more hosts in memory than there are hosts",
			Count(p.Active, "host"), Count(p.Hosts, "host")))
	}
	if p.SeenBits <= 0 {
		out = append(out, "the seen filter is zero bits per URL, so every URL offered is a disk read and the filter is not there")
	}
	if p.URLBytes <= 0 || p.HostBytes <= 0 {
		out = append(out, "a URL or a host name is zero bytes long, and the arithmetic below is about the bytes")
	}
	if p.ShapesPerHost <= 0 {
		out = append(out, "no host is tracked at any template, which turns the per template budget off rather than costing nothing")
	}
	return out
}

// Where is the box the crawl is written to run from, and whether it holds.
func (c Cost) Where() string {
	b, ok := fleet.Lookup("server1")
	if !ok {
		return "server1 is not in the fleet"
	}
	if c.Fits(b) {
		return fmt.Sprintf("%s on %s, which has %s for it: %.0f%% spare",
			Bytes(c.Total), b.Name, Bytes(Available(b)), 100*c.Headroom(b))
	}
	return fmt.Sprintf("%s on %s, which has %s for it: %s short",
		Bytes(c.Total), b.Name, Bytes(Available(b)), Bytes(c.Total-Available(b)))
}
