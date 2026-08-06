// Package suat measures what the crawl is producing while it is producing it.
//
// Suất is a rate. The rate that decides whether this crawl was worth running is
// net yield: unique documents kept per fetch made. The plan says 0.15 or better
// and the kill criterion says stop below 0.08 after the first hundred million
// fetches, and both of those numbers are worthless unless the meter exists
// before the crawl starts. A yield computed at the end is a post mortem. The
// whole point of a kill criterion is that somebody can act on it at fetch one
// hundred million rather than read about it at fetch seven hundred million,
// which is why this package is written now and not later.
//
// Net rather than gross, and the distinction is where crawlers flatter
// themselves. A fetch that returns 200 with a full page of HTML has produced
// nothing if the page is a duplicate of one already in the store, or is
// boilerplate with no text under it, or is a calendar page for the year 2031.
// So a [Point] records fetches on one side and documents that survived
// deduplication on the other, and everything in between is accounted for by
// name rather than dropped.
//
// The per class breakdown is not decoration. The crawl exists because Common
// Crawl caps fetches per host, which is right for covering the web and wrong for
// covering one language, and forums are the class that cap hurts most. P03-5
// predicts forums contribute more tokens than news archives. A per class yield
// reported only at the end cannot change how the budget is spent, and a budget
// that cannot move is the same as not having measured.
//
// The one thing this package refuses to do is compute a yield curve out of a
// single measurement. A [Run] holds checkpoints, [Run.Check] refuses gaps wider
// than [Stride], and a run with one point is reported as a number rather than
// dressed up as a trend.
package suat

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Target is the number the crawl is planned against: unique documents kept per
// fetch made. It is P03-4.
const Target = 0.15

// Kill is the yield below which the crawl stops. It is not a warning threshold.
const Kill = 0.08

// Settled is how many fetches have to be behind the crawl before the kill
// criterion means anything. Yield early in a crawl is a measurement of the seed
// list rather than of the web, and firing on it stops a crawl for being young.
const Settled = 100_000_000

// Stride is the widest gap between checkpoints that still counts as measuring
// continuously. It is five million fetches, which at the planned rate is a few
// hours, because the point of continuous measurement is that somebody can act
// inside a shift.
const Stride = 5_000_000

// Objections is the share of crawled hosts that may issue a removal request or
// a block before the crawl is halted rather than merely slowed. P03-8 predicts
// under 0.005 and the operational response fires at 0.02.
const Objections = 0.02

// A Class is a kind of site, which is the unit the crawl budget moves in.
type Class string

// The target classes, which are the ones the plan makes a prediction about.
const (
	Forum      Class = "forum"
	News       Class = "news"
	Government Class = "government"
	Education  Class = "education"
	Commerce   Class = "commerce"

	// Other is everything the classifier did not place. It is a class rather
	// than an omission, because a large Other is a fact about the classifier
	// and hiding it makes the other five look better than they are.
	Other Class = "other"
)

// Classes is every class, in the order a report prints them.
var Classes = []Class{Forum, News, Government, Education, Commerce, Other}

// Valid reports whether c is one of the classes.
func (c Class) Valid() bool { return slices.Contains(Classes, c) }

// A Tally is what one class produced between two checkpoints, or over a whole
// run. Every fetch that was made is in exactly one of the outcome fields, so the
// arithmetic closes and a yield cannot be improved by forgetting a category.
type Tally struct {
	// Fetches is requests actually sent. It is the denominator.
	Fetches int64 `json:"fetches"`

	// Documents is what came out the far end of the pipeline and stayed: unique,
	// Vietnamese, past the quality gate. It is the numerator, and it is not the
	// count of pages that returned 200.
	Documents int64 `json:"documents"`

	// The outcomes between the two. A fetch is counted once.
	Duplicates int64 `json:"duplicates"` // fetched, extracted, already in the store
	Empty      int64 `json:"empty"`      // fetched, nothing under the boilerplate
	Rejected   int64 `json:"rejected"`   // fetched, extracted, failed a gate
	Refused    int64 `json:"refused"`    // robots, TDMRep, noai, or a block
	Failed     int64 `json:"failed"`     // network, DNS, timeout, 5xx

	// Tokens is what the kept documents are worth, which is the number the
	// forums against news archives prediction is settled on. Documents are not
	// interchangeable: a forum thread and a news lede are both one document.
	Tokens int64 `json:"tokens"`

	// Hosts is how many distinct hosts were fetched from, and Objected is how
	// many of them asked us to stop. Objections are counted per host rather than
	// per fetch, because one operator objecting once about a host we fetched ten
	// thousand pages from is one objection.
	Hosts    int64 `json:"hosts"`
	Objected int64 `json:"objected"`
}

// A Point is the whole crawl at one moment, by class. It is cumulative rather
// than incremental, so a point that arrives after a restart is still readable
// against the one before it.
type Point struct {
	// At is total fetches behind the crawl when this point was taken. It is the
	// clock, because wall time does not say how much crawling happened and
	// fetches do.
	At int64 `json:"at"`

	// Box is the machine that produced it. The crawl runs from server1 and the
	// coordinator runs on server2, and a number without a box on it is a number
	// nobody can go and look at.
	Box string `json:"box"`

	By map[Class]Tally `json:"by"`
}

// A Run is the whole crawl so far.
type Run struct {
	Crawl  string  `json:"crawl"`
	Points []Point `json:"points"`
}

// Errors a run can fail with.
var (
	ErrBadRun = errors.New("suat: the yield was not measured continuously")
)

// Yield is unique documents kept per fetch made.
func (t Tally) Yield() float64 {
	if t.Fetches <= 0 {
		return 0
	}
	return float64(t.Documents) / float64(t.Fetches)
}

// Accounted is every fetch the tally has an outcome for. It should equal
// Fetches, and where it does not the difference is fetches nobody wrote down.
func (t Tally) Accounted() int64 {
	return t.Documents + t.Duplicates + t.Empty + t.Rejected + t.Refused + t.Failed
}

// Objection is the share of hosts that asked us to stop.
func (t Tally) Objection() float64 {
	if t.Hosts <= 0 {
		return 0
	}
	return float64(t.Objected) / float64(t.Hosts)
}

// Add sums two tallies, which is how a point becomes a total.
func (t Tally) Add(o Tally) Tally {
	return Tally{
		Fetches:    t.Fetches + o.Fetches,
		Documents:  t.Documents + o.Documents,
		Duplicates: t.Duplicates + o.Duplicates,
		Empty:      t.Empty + o.Empty,
		Rejected:   t.Rejected + o.Rejected,
		Refused:    t.Refused + o.Refused,
		Failed:     t.Failed + o.Failed,
		Tokens:     t.Tokens + o.Tokens,
		Hosts:      t.Hosts + o.Hosts,
		Objected:   t.Objected + o.Objected,
	}
}

// Sub is the difference between two cumulative points, which is what actually
// happened in the window between them.
func (t Tally) Sub(o Tally) Tally {
	return Tally{
		Fetches:    t.Fetches - o.Fetches,
		Documents:  t.Documents - o.Documents,
		Duplicates: t.Duplicates - o.Duplicates,
		Empty:      t.Empty - o.Empty,
		Rejected:   t.Rejected - o.Rejected,
		Refused:    t.Refused - o.Refused,
		Failed:     t.Failed - o.Failed,
		Tokens:     t.Tokens - o.Tokens,
		Hosts:      t.Hosts - o.Hosts,
		Objected:   t.Objected - o.Objected,
	}
}

// Total is every class at a point summed into one tally.
func (p Point) Total() Tally {
	var t Tally
	for _, c := range Classes {
		t = t.Add(p.By[c])
	}
	return t
}

// Yield is the crawl's net yield at a point.
func (p Point) Yield() float64 { return p.Total().Yield() }

// Latest is the most recent point, and false if the run holds none.
func (r *Run) Latest() (Point, bool) {
	if len(r.Points) == 0 {
		return Point{}, false
	}
	return r.Points[len(r.Points)-1], true
}

// Window is what happened between the last two points, which is the number that
// moves when something changes. A cumulative yield over seven hundred million
// fetches barely moves at all, so a crawl watched only on the cumulative number
// is a crawl nobody is watching.
func (r *Run) Window() (Tally, bool) {
	if len(r.Points) < 2 {
		return Tally{}, false
	}
	last := r.Points[len(r.Points)-1]
	prev := r.Points[len(r.Points)-2]
	return last.Total().Sub(prev.Total()), true
}

// Leader is the class that has produced the most tokens, which is the form P03-5
// is settled in. Forums are predicted to beat news archives.
func (p Point) Leader() (Class, bool) {
	best, found := Other, false
	for _, c := range Classes {
		if c == Other {
			continue
		}
		if !found || p.By[c].Tokens > p.By[best].Tokens {
			best, found = c, true
		}
	}
	return best, found
}

// check reports every way a run is not a continuous measurement.
func (r *Run) check() error {
	var problems []error
	if r.Crawl == "" {
		problems = append(problems, errors.New("the run does not name the crawl it measures"))
	}
	if len(r.Points) == 0 {
		problems = append(problems, errors.New("the run holds no measurements"))
	}

	var prev Point
	for i, p := range r.Points {
		if p.Box == "" {
			problems = append(problems, fmt.Errorf("the point at %d does not say which box it came from", p.At))
		}
		if p.At <= 0 {
			problems = append(problems, fmt.Errorf("point %d is taken at %d fetches", i, p.At))
		}
		for c := range p.By {
			if !c.Valid() {
				problems = append(problems, fmt.Errorf("%s is not a target class", c))
			}
		}
		t := p.Total()
		if t.Accounted() != t.Fetches {
			problems = append(problems, fmt.Errorf(
				"the point at %d made %d fetches and accounts for %d of them, so %d went somewhere nobody wrote down",
				p.At, t.Fetches, t.Accounted(), t.Fetches-t.Accounted()))
		}
		if t.Fetches != p.At {
			problems = append(problems, fmt.Errorf(
				"the point at %d sums to %d fetches, and a point is cumulative rather than the window since the last one", p.At, t.Fetches))
		}
		for _, c := range Classes {
			if b := p.By[c]; b.Objected > b.Hosts {
				problems = append(problems, fmt.Errorf("the point at %d has %d %s hosts objecting out of %d crawled", p.At, b.Objected, c, b.Hosts))
			}
		}
		if i == 0 {
			prev = p
			continue
		}
		switch {
		case p.At <= prev.At:
			problems = append(problems, fmt.Errorf("the point at %d comes after the point at %d, so the crawl ran backwards", p.At, prev.At))
		case p.At-prev.At > Stride:
			problems = append(problems, fmt.Errorf(
				"%d fetches separate the points at %d and %d, and anything past %d is a yield measured afterward rather than while it ran",
				p.At-prev.At, prev.At, p.At, Stride))
		}
		if p.Total().Documents < prev.Total().Documents {
			problems = append(problems, fmt.Errorf("the point at %d keeps fewer documents than the point at %d, and a cumulative count does not go down", p.At, prev.At))
		}
		if p.Total().Tokens < prev.Total().Tokens {
			problems = append(problems, fmt.Errorf("the point at %d is worth fewer tokens than the point at %d, and a cumulative count does not go down", p.At, prev.At))
		}
		prev = p
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrBadRun, errors.Join(problems...))
	}
	return nil
}

// Faults is check as lines, since a report wants them one to a row.
func (r *Run) Faults() []string {
	err := r.check()
	if err == nil {
		return nil
	}
	var faults []string
	for line := range strings.SplitSeq(err.Error(), "\n") {
		faults = append(faults, strings.TrimPrefix(line, ErrBadRun.Error()+": "))
	}
	return faults
}
