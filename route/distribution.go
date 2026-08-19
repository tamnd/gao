package route

import (
	"fmt"
	"strings"
)

// A Distribution is how a pile of documents split across the routes.
//
// It is the number this whole slice is costed from. Route T and route L are
// milliseconds a page on any box. Route O is a GPU second a page, there is one
// GPU on the fleet, and it is also the route whose output carries an error
// rate. So the difference between a pile that is 10% scans and one that is 40%
// scans is not a detail of the extraction stage, it is whether the extraction
// stage takes a week or a season, and it is not knowable from anything except
// counting.
type Distribution struct {
	// Box is what the counting ran on, and it is required rather than optional
	// for the same reason every other measurement in this project carries it: a
	// throughput number with no hardware attached is not a number.
	Box string

	counts map[Route]int
	pages  map[Route]int
	sets   map[string]int
}

// NewDistribution starts a count on one box.
func NewDistribution(box string) *Distribution {
	return &Distribution{
		Box:    box,
		counts: map[Route]int{},
		pages:  map[Route]int{},
		sets:   map[string]int{},
	}
}

// Add records one routed document.
func (d *Distribution) Add(r Reading) {
	d.counts[r.Route]++
	d.pages[r.Route] += r.Pages
	if r.Charset != "" {
		d.sets[r.Charset]++
	}
}

// Routes is the order a distribution is read in, which is the order the routes
// cost money in.
var Routes = []Route{Text, Legacy, Scan, Unroutable}

// Documents is how many documents took a route.
func (d *Distribution) Documents(r Route) int { return d.counts[r] }

// Pages is how many pages took a route, which is the unit OCR is billed in and
// therefore the one the cost follows.
func (d *Distribution) Pages(r Route) int { return d.pages[r] }

// Total is how many documents were routed.
func (d *Distribution) Total() int {
	var n int
	for _, r := range Routes {
		n += d.counts[r]
	}
	return n
}

// Share is the fraction of documents that took a route, as a percentage.
func (d *Distribution) Share(r Route) float64 {
	if d.Total() == 0 {
		return 0
	}
	return 100 * float64(d.counts[r]) / float64(d.Total())
}

// Charsets is how many documents were found in each legacy encoding, which is
// worth reporting separately because the six encodings are not equally likely
// and a run that finds only one of them has probably found a bug in the
// detector rather than a fact about Vietnamese typesetting.
func (d *Distribution) Charsets() map[string]int {
	out := make(map[string]int, len(d.sets))
	for k, v := range d.sets {
		out[k] = v
	}
	return out
}

// String renders the distribution the way it is read and published.
func (d *Distribution) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d documents routed on %s\n\n", d.Total(), d.Box)
	fmt.Fprintf(&b, "%-12s %6s %8s %10s\n", "route", "docs", "share", "pages")
	for _, r := range Routes {
		fmt.Fprintf(&b, "%-12s %6d %7.1f%% %10d\n", r.Letter()+" "+r.String(), d.Documents(r), d.Share(r), d.Pages(r))
	}
	return b.String()
}
