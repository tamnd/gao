package bien

// Deciding what a host has earned.
//
// The usual way to bound a crawl is a cap per host: no more than N URLs from
// anywhere. It is wrong in both directions at once. A Vietnamese forum with
// twenty years of threads is worth far more than N and gets cut off, while a
// shop with one product template and a color filter produces forty thousand
// near identical pages and reaches N without ever having said anything.
//
// So the cap is per template rather than per host, and it is earned rather than
// granted. Every shape starts with enough allowance to prove itself and buys
// more with pages that produced text the corpus did not already have. A template
// that produces articles grows without a ceiling anyone has to guess. A template
// that produces empty pages stops on its own, and the crawl spends what it saved
// somewhere else.
//
// The word for spending here is deliberate. A budget that is only ever checked
// and never debited is a limit that does not bind, so Offer both answers and
// charges, and a caller that asks twice has spent twice.

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// A Result is what came back from one fetch, as far as the frontier cares.
//
// The frontier does not care what the page said. It cares whether asking for it
// was worth the request, and there are exactly three answers.
type Result int

const (
	// Empty is a page that produced no usable text: a redirect chain, an error
	// body, a listing with nothing on it, a calendar day where nothing happened.
	Empty Result = iota

	// Repeat is a page that produced text the corpus already has. It is the
	// signature of a print view, a session id, or a shape that spells the same
	// document forty different ways, and it is told apart from Empty because a
	// host producing nothing but repeats is a different problem from a host
	// producing nothing at all.
	Repeat

	// New is a page that produced text nothing else in the corpus had. This is
	// the only result that earns anything.
	New
)

func (r Result) String() string {
	switch r {
	case New:
		return "new"
	case Repeat:
		return "repeat"
	default:
		return "empty"
	}
}

// Options are the numbers a budget runs on.
//
// They are all in one struct and all exported because they are the knobs that
// get turned on the fleet after the first real crawl, and a number buried in a
// constant is a number nobody tunes.
type Options struct {
	// HostStart is what a host may spend before it has earned anything, across
	// every template on it. It exists to bound the long tail: most of 900,000
	// hosts are a parked domain or a shop with eleven pages, and a crawl that
	// gave each of them a serious allowance would spend its whole budget on
	// sites with nothing on them.
	HostStart int

	// HostEarn is how many URLs a host buys with one page of new text.
	HostEarn int

	// HostCap is the ceiling no amount of earning passes. It is the answer to a
	// site that generates infinite genuinely distinct text, which sounds
	// impossible and is what a wiki mirror with a diff view looks like.
	HostCap int

	// ShapeStart is what one template may spend before it has earned anything.
	// This is the number that does the work. It is small enough that a facet
	// grid or a filter loop stops quickly and large enough that a real template
	// gets a fair reading before it is judged.
	ShapeStart int

	// ShapeEarn is how many URLs a template buys with one page of new text. It
	// is larger than one on purpose: a template earning at parity can never grow
	// past where it started, and a forum that is worth a million fetches has to
	// be able to get there from fifty.
	ShapeEarn int

	// DatedStart is ShapeStart for a template with a date in it. Lower, because
	// a date is the one segment kind that can be filled in forever. An archive
	// is finite and produces text on nearly every page, so it earns its way past
	// this within the first few dozen fetches. A calendar produces a page for
	// every day since 1970 and text on almost none of them, so it does not.
	DatedStart int

	// Barren is how many fetches in a row may produce nothing before a template
	// is closed. It is the fast path out of a trap that the arithmetic would
	// reach eventually anyway, and it matters because eventually is measured in
	// requests to somebody else's server.
	Barren int

	// Facets is how many distinct combinations of query keys one path may
	// produce before further combinations are refused and only the single key
	// views stay open. A faceted catalog multiplies: with color, size, sort and
	// page there are fifteen subsets of the filters and each one is a template
	// of its own. Refusing the combinations loses nothing, because every product
	// on such a site is reachable from the unfiltered listing.
	Facets int

	// Depth is how deep a path may go before it is refused without a fetch.
	// Vietnamese forums nest genuinely deep, a board inside a board inside a
	// thread inside a page, so this is set well past where a real site stops
	// rather than at where a tidy site would.
	Depth int

	// Repeats is how many times one path segment may occur in a row before the
	// URL is refused without a fetch. Three is not a guess. Two is an ordinary
	// path that says the same word twice, and three is a relative link that has
	// been resolved against itself twice, which never stops on its own.
	Repeats int
}

// Defaults are the numbers the crawl starts with.
//
// Every one of them is a starting point to be replaced by a measurement from the
// fleet, and none of them has been through a real crawl yet.
func Defaults() Options {
	return Options{
		HostStart:  500,
		HostEarn:   4,
		HostCap:    2_000_000,
		ShapeStart: 50,
		ShapeEarn:  4,
		DatedStart: 16,
		Barren:     10,
		Facets:     24,
		Depth:      12,
		Repeats:    3,
	}
}

// A Budget is the ledger of what every host and every template has spent and
// earned. It is safe for concurrent use, because a per worker idea of what a
// host has been asked for is no idea at all.
type Budget struct {
	o Options

	mu    sync.Mutex
	hosts map[string]*ledger
}

type ledger struct {
	spent  int
	gained int

	shapes map[string]*tally

	// facets counts the distinct query key combinations seen per path, which is
	// what tells a catalog with four filters on it from a page with a
	// parameter.
	facets map[string]map[string]bool
}

type tally struct {
	shape  Shape
	spent  int
	gained int
	repeat int
	empty  int
	barren int
	closed string
}

// NewBudget returns a budget running on the given options. A zero field takes
// its default, so a caller changing one number does not have to restate the
// other ten.
func NewBudget(o Options) *Budget {
	d := Defaults()
	if o.HostStart == 0 {
		o.HostStart = d.HostStart
	}
	if o.HostEarn == 0 {
		o.HostEarn = d.HostEarn
	}
	if o.HostCap == 0 {
		o.HostCap = d.HostCap
	}
	if o.ShapeStart == 0 {
		o.ShapeStart = d.ShapeStart
	}
	if o.ShapeEarn == 0 {
		o.ShapeEarn = d.ShapeEarn
	}
	if o.DatedStart == 0 {
		o.DatedStart = d.DatedStart
	}
	if o.Barren == 0 {
		o.Barren = d.Barren
	}
	if o.Facets == 0 {
		o.Facets = d.Facets
	}
	if o.Depth == 0 {
		o.Depth = d.Depth
	}
	if o.Repeats == 0 {
		o.Repeats = d.Repeats
	}
	return &Budget{o: o, hosts: map[string]*ledger{}}
}

// Offer asks whether a URL is worth requesting, and charges it if it is.
//
// The second return is why not, in a sentence, and it is never empty when the
// first is false. A crawl that refuses URLs without saying why is a crawl nobody
// can tell from one that is broken.
func (b *Budget) Offer(canonical string) (bool, string) {
	s, err := Of(canonical)
	if err != nil {
		return false, err.Error()
	}

	if why := b.structural(s); why != "" {
		return false, why
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	h := b.host(s.Host)
	if h.spent >= b.allowedHost(h) {
		return false, "the host has spent its budget: " + plural(h.spent, "url") + " for " + plural(h.gained, "page") + " of new text"
	}

	if why := b.faceted(h, s); why != "" {
		return false, why
	}

	t := h.shape(s)
	if t.closed != "" {
		return false, t.closed
	}
	if allowed := b.allowedShape(t); t.spent >= allowed {
		t.closed = "the template " + s.String() + " has spent its budget: " + plural(t.spent, "url") + " for " + plural(t.gained, "page") + " of new text"
		return false, t.closed
	}

	h.spent++
	t.spent++
	b.markFacet(h, s)
	return true, ""
}

// structural is the half of the decision that needs no history. A URL refused
// here costs nothing, because it is refused before anything is asked for.
func (b *Budget) structural(s Shape) string {
	if n := s.Repeats(); n >= b.o.Repeats {
		return "the path repeats one segment " + plural(n, "time") + ", which is a relative link resolving against itself"
	}
	if n := s.Depth(); n > b.o.Depth {
		return "the path is " + plural(n, "segment") + " deep, past the " + plural(b.o.Depth, "segment") + " a real site stops at"
	}
	return ""
}

// faceted is the catalog rule, and what it counts is worth being exact about.
//
// Two filtered listings with the same filters set to different values are one
// shape already, since the shape keeps the query keys and drops their values, so
// the per template budget bounds them without any help. What it does not bound
// is the subsets. Eight filters over one listing is two hundred and fifty six
// distinct key combinations, each of them a separate shape with a separate
// starting allowance, and that is the arithmetic this rule is here to stop.
//
// Only combinations count, because only combinations multiply. The single filter
// views stay open however many there are, since a product reachable from
// `?mau=do` and from `?size=m` is reachable, and refusing the combinations of
// those two loses nothing.
func (b *Budget) faceted(h *ledger, s Shape) string {
	if len(s.Keys) < 2 {
		return ""
	}
	seen := h.facets[s.Path]
	if len(seen) < b.o.Facets || seen[strings.Join(s.Keys, "&")] {
		return ""
	}
	return "the path " + s.Path + " has already produced " + plural(len(seen), "combination") +
		" of filters, so only single filter views are still asked for"
}

func (b *Budget) markFacet(h *ledger, s Shape) {
	if len(s.Keys) < 2 {
		return
	}
	seen := h.facets[s.Path]
	if seen == nil {
		seen = map[string]bool{}
		h.facets[s.Path] = seen
	}
	seen[strings.Join(s.Keys, "&")] = true
}

func (b *Budget) allowedHost(h *ledger) int {
	allowed := b.o.HostStart + h.gained*b.o.HostEarn
	if allowed > b.o.HostCap {
		return b.o.HostCap
	}
	return allowed
}

func (b *Budget) allowedShape(t *tally) int {
	start := b.o.ShapeStart
	if t.shape.Dated() {
		start = b.o.DatedStart
	}
	return start + t.gained*b.o.ShapeEarn
}

// Fetched records what one URL produced. It is the only thing that earns
// anything, and a crawl that forgets to call it is a crawl that stops after
// ShapeStart pages per template and reports no reason a person would recognize.
func (b *Budget) Fetched(canonical string, r Result) {
	s, err := Of(canonical)
	if err != nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	h := b.host(s.Host)
	t := h.shape(s)

	switch r {
	case New:
		h.gained++
		t.gained++
		t.barren = 0
		return
	case Repeat:
		t.repeat++
	default:
		t.empty++
	}

	t.barren++
	if t.barren >= b.o.Barren && t.closed == "" {
		t.closed = "the template " + s.String() + " returned " +
			plural(t.barren, "page") + " in a row with nothing new on them"
	}
}

// Closed reports whether a template has been shut and why. It answers the
// question a person asks when a host they expected in the corpus is not in it,
// which is worth an exported method rather than a line in a log nobody kept.
func (b *Budget) Closed(canonical string) (string, bool) {
	s, err := Of(canonical)
	if err != nil {
		return err.Error(), true
	}
	if why := b.structural(s); why != "" {
		return why, true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	h, ok := b.hosts[s.Host]
	if !ok {
		return "", false
	}
	t, ok := h.shapes[s.String()]
	if !ok {
		return "", false
	}
	return t.closed, t.closed != ""
}

// A Line is one template and what it has done, for the report a person reads.
type Line struct {
	Shape   string
	Spent   int
	Gained  int
	Repeat  int
	Empty   int
	Allowed int
	Closed  string
}

// Lines returns what every template on a host has spent and earned, heaviest
// spender first, because the template that spent the most is the one to look at
// when a host produced less than it should have.
func (b *Budget) Lines(host string) []Line {
	b.mu.Lock()
	defer b.mu.Unlock()

	h, ok := b.hosts[host]
	if !ok {
		return nil
	}
	out := make([]Line, 0, len(h.shapes))
	for key, t := range h.shapes {
		out = append(out, Line{
			Shape:   key,
			Spent:   t.spent,
			Gained:  t.gained,
			Repeat:  t.repeat,
			Empty:   t.empty,
			Allowed: b.allowedShape(t),
			Closed:  t.closed,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Spent != out[j].Spent {
			return out[i].Spent > out[j].Spent
		}
		return out[i].Shape < out[j].Shape
	})
	return out
}

// Spent reports what a host has spent, what it earned, and how many templates it
// was spread over. Net yield is this divided out, and it is reported while the
// crawl runs rather than at the end, since a yield figure that arrives after the
// budget is gone is a postmortem.
func (b *Budget) Spent(host string) (spent, gained, shapes int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	h, ok := b.hosts[host]
	if !ok {
		return 0, 0, 0
	}
	return h.spent, h.gained, len(h.shapes)
}

// Hosts is how many hosts the budget has heard of, which is the frontier's own
// size rather than the crawl's.
func (b *Budget) Hosts() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.hosts)
}

func (b *Budget) host(name string) *ledger {
	h, ok := b.hosts[name]
	if !ok {
		h = &ledger{shapes: map[string]*tally{}, facets: map[string]map[string]bool{}}
		b.hosts[name] = h
	}
	return h
}

func (h *ledger) shape(s Shape) *tally {
	key := s.String()
	t, ok := h.shapes[key]
	if !ok {
		t = &tally{shape: s}
		h.shapes[key] = t
	}
	return t
}

// plural writes a count and its noun the way a person would. English is the
// language of the operator report and it needs the s; the reason strings are
// read by whoever is on the fleet at three in the morning and they should read
// like a sentence.
func plural(n int, noun string) string {
	out := strconv.Itoa(n) + " " + noun
	if n != 1 {
		out += "s"
	}
	return out
}
