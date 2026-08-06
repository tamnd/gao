// Package xoa is the takedown register: who asked us to remove something, when
// they asked, and when it was actually done.
//
// The contact page promises a response inside 72 hours and says the real time
// for each request is recorded in public. This is that record, and it is a file
// in the repository rather than a database, because a promise about response
// times that only the operator can audit is a promise nobody can check.
//
// A takedown path is the part of a crawler that is easiest to have on paper and
// hardest to have working. Publishing an address and honoring what arrives at it
// are different things, and the difference only shows up when somebody writes.
// So the register does three jobs rather than one: it binds, at the fetch and
// again at the write into the store, it measures how long each request took, and
// it refuses to report a path nobody has used as a path that works.
package xoa

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/tamnd/gao/bien"
)

// Name is where the register lives. It is at the root of the repository next to
// the contact page that points at it.
const Name = "GO-BO.toml"

// Target is the response time the contact page promises.
const Target = 72 * time.Hour

// Scope is what a request asked for.
//
// The two are different promises with different costs, and the contact page asks
// which one is meant because guessing gets it wrong in both directions. Stopping
// is immediate. Rebuilding a published release is not, and pretending otherwise
// would mean a response time that looks good and a release that still has the
// documents in it.
type Scope string

const (
	// Stop means stop crawling. Published releases keep what they already have.
	Stop Scope = "stop"
	// Erase means stop crawling and rebuild published releases without it.
	Erase Scope = "erase"
)

// Request is one takedown, as filed.
type Request struct {
	Issue    int       `toml:"issue"`              // the public issue it was made in
	Host     string    `toml:"host"`               // the host, covering its subdomains
	Paths    []string  `toml:"paths,omitempty"`    // path prefixes, or the whole host when empty
	Scope    Scope     `toml:"scope"`              // stop or erase
	Asked    time.Time `toml:"asked"`              // when the issue was opened
	Stopped  time.Time `toml:"stopped,omitempty"`  // when the crawl stopped
	Rebuilt  time.Time `toml:"rebuilt,omitempty"`  // when the last affected release was republished
	Releases []string  `toml:"releases,omitempty"` // which releases had to be rebuilt
	Note     string    `toml:"note,omitempty"`
}

// Answered is how long it took to stop crawling.
//
// The clock starts when the request was made and not when we read it. Measuring
// from the moment somebody noticed would make every response time zero, which is
// the number a report like this is most tempting to produce and the one that
// says nothing at all.
func (r Request) Answered() (time.Duration, bool) {
	if r.Stopped.IsZero() {
		return 0, false
	}
	return r.Stopped.Sub(r.Asked), true
}

// Done says whether everything this request asked for has happened.
func (r Request) Done() bool {
	if r.Stopped.IsZero() {
		return false
	}
	return r.Scope != Erase || !r.Rebuilt.IsZero()
}

// Register is the whole file.
type Register struct {
	Requests []Request `toml:"request"`
}

// Read parses a register.
func Read(rd io.Reader) (*Register, error) {
	b, err := io.ReadAll(rd)
	if err != nil {
		return nil, fmt.Errorf("xoa: reading the register: %w", err)
	}
	var reg Register
	if err := toml.Unmarshal(b, &reg); err != nil {
		return nil, fmt.Errorf("xoa: reading the register: %w", err)
	}
	for i := range reg.Requests {
		reg.Requests[i].Host = strings.ToLower(strings.TrimSpace(reg.Requests[i].Host))
	}
	return &reg, nil
}

// Load reads the register from a path.
func Load(path string) (*Register, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("xoa: %w", err)
	}
	defer func() { _ = f.Close() }()
	return Read(f)
}

// ErrNothingFiled is what an empty register says when asked how well the
// takedown path works.
//
// A path nobody has used is a path nobody has tested, and the honest report of
// zero requests is that the question is open. A report that instead prints a
// median of zero hours and a hundred percent honored describes a system that has
// never done anything as one that has never failed.
var ErrNothingFiled = errors.New("xoa: nothing has been filed, so nothing has been measured")

// Blocked says whether a URL is one we have been asked not to fetch.
//
// This is the gate at the fetch. It binds from the moment the request was made,
// including on requests we have not yet marked stopped, because the alternative
// is a crawler that keeps fetching from a site that has asked it to stop for
// however long it takes somebody to edit a file.
func (g *Register) Blocked(rawurl string) (Request, bool) {
	u, err := bien.Parse(rawurl)
	if err != nil {
		return Request{}, false
	}
	for _, r := range g.Requests {
		if covers(r, u.Host, u.Path) {
			return r, true
		}
	}
	return Request{}, false
}

// Erased says whether a document has to be kept out of what gets published.
//
// This is the second gate, at the write into the store, and it is deliberately
// not the same question as [Register.Blocked]. Two things make it different.
//
// A request scoped to stop leaves published releases alone, so a document
// fetched before the request was made stays. A document fetched after it was made
// does not, whatever the scope, because that is a fetch that should never have
// happened and the gap between somebody asking and somebody acting is ours
// rather than theirs.
//
// A request scoped to erase takes everything, whenever it was fetched.
func (g *Register) Erased(rawurl string, fetched time.Time) (Request, bool) {
	u, err := bien.Parse(rawurl)
	if err != nil {
		return Request{}, false
	}
	for _, r := range g.Requests {
		if !covers(r, u.Host, u.Path) {
			continue
		}
		if r.Scope == Erase || !fetched.Before(r.Asked) {
			return r, true
		}
	}
	return Request{}, false
}

// covers is host matching on a label boundary plus a path prefix.
//
// A takedown for example.vn covers www.example.vn and tin.example.vn, because
// that is what somebody filing one means by their site. It does not cover
// notexample.vn, which a suffix test on the string would take, and taking it
// would mean silently dropping a stranger's site out of the corpus on the
// strength of a request that was never about them.
func covers(r Request, host, path string) bool {
	if r.Host == "" {
		return false
	}
	if host != r.Host && !strings.HasSuffix(host, "."+r.Host) {
		return false
	}
	if len(r.Paths) == 0 {
		return true
	}
	for _, p := range r.Paths {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// Open is every request that has not been fully honored, oldest first.
func (g *Register) Open() []Request {
	var out []Request
	for _, r := range g.Requests {
		if !r.Done() {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Asked.Before(out[j].Asked) })
	return out
}

// Late is every request still not stopped past the target response time.
//
// It is about stopping rather than about finishing, because that is what the
// contact page promises inside 72 hours. Rebuilding a release takes longer and
// says so, and folding the two together would produce a list that is always
// overdue and therefore never read.
func (g *Register) Late(now time.Time) []Request {
	var out []Request
	for _, r := range g.Requests {
		if r.Stopped.IsZero() && now.Sub(r.Asked) > Target {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Asked.Before(out[j].Asked) })
	return out
}

// Times is how long each honored request took to stop, sorted.
func (g *Register) Times() []time.Duration {
	var out []time.Duration
	for _, r := range g.Requests {
		if d, ok := r.Answered(); ok {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out
}

// Worst is the longest anybody waited for a stop, which is the number that
// describes the promise. A median hides exactly the request that broke it.
func (g *Register) Worst() (time.Duration, error) {
	times := g.Times()
	if len(times) == 0 {
		return 0, ErrNothingFiled
	}
	return times[len(times)-1], nil
}

// Median is the middle response time.
func (g *Register) Median() (time.Duration, error) {
	times := g.Times()
	if len(times) == 0 {
		return 0, ErrNothingFiled
	}
	return times[len(times)/2], nil
}

// Rate is the fraction of crawled hosts that asked to be removed, which is what
// P03-8 is a gate on: under 0.5 percent, with a halt at 2 percent.
//
// The denominator is hosts crawled rather than hosts in the frontier, since a
// host we never fetched from had no occasion to object and counting it would
// dilute the number toward zero as the frontier grows.
func (g *Register) Rate(crawled int) (float64, error) {
	if crawled <= 0 {
		return 0, errors.New("xoa: a rate needs a count of hosts that were actually crawled")
	}
	hosts := make(map[string]bool, len(g.Requests))
	for _, r := range g.Requests {
		hosts[r.Host] = true
	}
	return float64(len(hosts)) / float64(crawled), nil
}

// Check reads the register for entries that cannot be true.
//
// This is here because the register is edited by hand under time pressure, by
// somebody who has just been asked to take something down, and a row with the
// dates the wrong way round reports a response time that never happened.
func (g *Register) Check(now time.Time) []string {
	var bad []string
	seen := make(map[int]bool, len(g.Requests))
	for i, r := range g.Requests {
		where := fmt.Sprintf("entry %d", i+1)
		if r.Issue > 0 {
			where = fmt.Sprintf("issue %d", r.Issue)
			if seen[r.Issue] {
				bad = append(bad, where+" appears twice")
			}
			seen[r.Issue] = true
		} else {
			bad = append(bad, where+" has no issue number, so there is nothing public to check it against")
		}
		if r.Host == "" {
			bad = append(bad, where+" names no host")
		}
		switch r.Scope {
		case Stop, Erase:
		default:
			bad = append(bad, where+" has scope "+string(r.Scope)+", which is neither stop nor erase")
		}
		if r.Asked.IsZero() {
			bad = append(bad, where+" has no date it was asked, so nothing about it can be measured")
		}
		if r.Asked.After(now) {
			bad = append(bad, where+" was asked in the future")
		}
		if !r.Stopped.IsZero() && r.Stopped.Before(r.Asked) {
			bad = append(bad, where+" was stopped before it was asked")
		}
		if !r.Rebuilt.IsZero() && r.Rebuilt.Before(r.Asked) {
			bad = append(bad, where+" was rebuilt before it was asked")
		}
		if r.Scope == Erase && !r.Rebuilt.IsZero() && len(r.Releases) == 0 {
			bad = append(bad, where+" says a release was rebuilt and does not say which")
		}
		if r.Scope == Stop && !r.Rebuilt.IsZero() {
			bad = append(bad, where+" asked us to stop and carries a rebuild date, which is more than it asked for")
		}
	}
	return bad
}
