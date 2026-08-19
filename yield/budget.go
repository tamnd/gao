package yield

import (
	"fmt"
	"slices"
	"strings"
)

// Exhausted is how far a class's tokens per fetch can fall from its own average
// before the class is read as worked out rather than as having a bad stretch.
// Half is deliberately generous: the hosts in a class that had text get read
// first, so every class falls, and the question is whether it fell off a cliff.
const Exhausted = 0.5

// Ahead is how far above the crawl's own tokens per fetch a class has to be
// before it earns more of the next stretch. A class one percent above average
// is a class inside the noise, and moving budget on that is moving budget on
// nothing.
const Ahead = 1.25

// Floor is the smallest share any class still in the crawl gets. A class cut to
// zero stops being measured, and a class that is not measured cannot be found
// to have recovered, which turns one bad stretch into a permanent decision.
const Floor = 0.05

// Sample is the fewest fetches a window can hold and still say anything about a
// class. Below it the tokens per fetch is a statement about which threads the
// crawler happened to reach. It is a quarter of a million rather than the five
// million of a [Stride], because a stride is the whole crawl and the smallest
// target class is a twelfth of it.
const Sample = 250_000

// A Move is what happens to a class's share of the next stretch of fetches.
type Move string

// The moves, worst first, which is also the order a report sorts ties in.
const (
	// Halt is the class whose operators are asking us to stop. It is not a
	// yield decision and it does not compete with one.
	Halt Move = "halt"

	// Less is a class whose tokens per fetch has fallen off its own average,
	// which is the shape of a class that has been read.
	Less Move = "less"

	// Hold is a class doing about what the crawl as a whole does.
	Hold Move = "hold"

	// More is a class paying better than the crawl average by enough to be
	// outside the noise.
	More Move = "more"
)

// A Slice is one class's case for the next stretch of fetches, and the numbers
// the case was made from.
type Slice struct {
	Class Class  `json:"class"`
	Move  Move   `json:"move"`
	Why   string `json:"why"`

	// Fetches and Yield are the window rather than the crawl, because a
	// cumulative number over hundreds of millions of fetches does not move and
	// a budget decided on it is a budget decided on history.
	Fetches int64   `json:"fetches"`
	Yield   float64 `json:"yield"`

	// PerFetch is tokens per fetch in the window, which is what a fetch handed
	// to this class actually buys. Documents are not interchangeable, so yield
	// alone ranks a forum thread and a two line news brief the same.
	PerFetch float64 `json:"per_fetch"`

	// Average is the same number over the whole crawl. The gap between the two
	// is the only evidence there is that a class is being worked out.
	Average float64 `json:"average"`

	Objection float64 `json:"objection"`

	// Share is the fraction of the next stretch this class gets.
	Share float64 `json:"share"`
}

// A Budget is the next stretch of fetches, divided.
type Budget struct {
	// Stretch is how many fetches are being handed out.
	Stretch int64 `json:"stretch"`

	// Window is how many fetches the decision was made on. It is not Stretch,
	// and a report that shows one without the other invites the reader to think
	// the plan was measured at the size it will run at.
	Window int64 `json:"window"`

	// PerFetch is the crawl's own tokens per fetch in the window, which is the
	// line every class is judged against.
	PerFetch float64 `json:"per_fetch"`

	Slices []Slice  `json:"slices"`
	Faults []string `json:"faults,omitempty"`
}

// window is what one class did between the last two points.
func (r *Run) window(c Class) Tally {
	if len(r.Points) < 2 {
		return Tally{}
	}
	last := r.Points[len(r.Points)-1]
	prev := r.Points[len(r.Points)-2]
	return last.By[c].Sub(prev.By[c])
}

// before is the class cumulatively up to the point the window opens, which is
// the history the window is judged against. Taking it from the last point
// instead would fold the window into its own baseline, and a class that stopped
// paying would drag down the average it is being compared to.
func before(r *Run, c Class) Tally {
	if len(r.Points) < 2 {
		return r.Points[len(r.Points)-1].By[c]
	}
	return r.Points[len(r.Points)-2].By[c]
}

// perFetch is tokens bought per fetch made, and zero where nothing was fetched.
func perFetch(t Tally) float64 {
	if t.Fetches <= 0 {
		return 0
	}
	return float64(t.Tokens) / float64(t.Fetches)
}

// Budget divides the next stretch of fetches between the target classes.
//
// It is decided on the window rather than on the crawl, on tokens per fetch
// rather than on yield, and it is the reason the per class breakdown is
// measured continuously instead of at the end. A class that produced well over
// the first two hundred million fetches goes on looking good in the cumulative
// number long after its hosts have been read, and a budget that reads the
// cumulative number will keep feeding it. The gap between what a class pays now
// and what it has paid on average is the whole signal.
//
// Objections are not a yield question. A class whose operators are asking us to
// stop gets nothing regardless of what it pays, because more fetches there buys
// tokens with the takedown path, and Other gets nothing because a budget cannot
// be aimed at a class nobody named.
func (r *Run) Budget(stretch int64) Budget {
	b := Budget{Stretch: stretch, Faults: r.Faults()}

	last, ok := r.Latest()
	if !ok {
		b.Faults = append(b.Faults, "nothing has been measured, so there is no margin to divide anything on")
		return b
	}
	if len(r.Points) < 2 {
		b.Faults = append(b.Faults,
			"the crawl holds one checkpoint, so every number here is cumulative, and a budget moved on what a class has produced since fetch one is a budget moved on history rather than on what the next fetch would buy")
	}

	whole := r.Points[len(r.Points)-1].Total()
	if len(r.Points) >= 2 {
		whole = whole.Sub(r.Points[len(r.Points)-2].Total())
	}
	b.Window, b.PerFetch = whole.Fetches, perFetch(whole)

	if o := last.By[Other]; o.Fetches > 0 && float64(o.Fetches)/float64(last.Total().Fetches) > 0.25 {
		b.Faults = append(b.Faults, fmt.Sprintf(
			"the classifier left %.0f%% of fetches in other, and a stretch cannot be aimed at a class nobody named, so this divides three quarters of a crawl and calls it the whole one",
			100*float64(o.Fetches)/float64(last.Total().Fetches)))
	}

	for _, c := range Classes {
		if c == Other {
			continue
		}
		w := r.window(c)
		if len(r.Points) < 2 {
			w = last.By[c]
		}
		s := Slice{
			Class: c, Fetches: w.Fetches, Yield: w.Yield(),
			PerFetch: perFetch(w), Average: perFetch(before(r, c)),
			Objection: last.By[c].Objection(),
		}
		s.Move, s.Why = call(s, b.PerFetch)
		b.Slices = append(b.Slices, s)

		if w.Fetches > 0 && w.Fetches < Sample && s.Move != Halt {
			b.Faults = append(b.Faults, fmt.Sprintf(
				"%s made %s in the window against the %s a class needs to say anything, so its share turns on which threads the crawler happened to reach",
				c, fetchCount(w.Fetches), fetchCount(Sample)))
		}
	}
	divide(b.Slices)
	slices.SortStableFunc(b.Slices, func(x, y Slice) int {
		switch {
		case x.Share != y.Share:
			if x.Share > y.Share {
				return -1
			}
			return 1
		default:
			return strings.Compare(string(x.Class), string(y.Class))
		}
	})
	return b
}

// call is the move for one class and the sentence that justifies it. Objections
// come before yield for the same reason they do in Run.Read: an operator asking
// us to stop is answered today and a class that merely pays badly is not.
func call(s Slice, average float64) (Move, string) {
	switch {
	case s.Objection > Objections:
		return Halt, fmt.Sprintf(
			"%.2f%% of %s hosts have objected against a ceiling of %.0f%%, and more fetches into a class whose operators are asking us to stop buys tokens with the takedown path",
			100*s.Objection, s.Class, 100*Objections)
	case s.Fetches == 0:
		return Hold, fmt.Sprintf("%s was not fetched in this window, so there is nothing here that says to move it either way", s.Class)
	case s.Average > 0 && s.PerFetch < Exhausted*s.Average:
		return Less, fmt.Sprintf(
			"%s pays %.1f tokens a fetch now against %.1f over the crawl, which is the shape of a class whose hosts with text have already been read",
			s.Class, s.PerFetch, s.Average)
	case average > 0 && s.PerFetch >= Ahead*average:
		return More, fmt.Sprintf(
			"%s pays %.1f tokens a fetch against %.1f across the crawl, which is far enough above the line to be worth moving budget on",
			s.Class, s.PerFetch, average)
	default:
		return Hold, fmt.Sprintf(
			"%s pays %.1f tokens a fetch against %.1f across the crawl, which is inside the noise",
			s.Class, s.PerFetch, average)
	}
}

// divide sets the share of the next stretch each class gets.
//
// Every class still in the crawl gets Floor before anything is divided on merit,
// and what is left goes out in proportion to what a fetch buys. The floor is not
// politeness. A class cut to nothing stops producing measurements, so it can
// never be found to have recovered, and one bad stretch becomes a decision
// nobody revisits.
func divide(in []Slice) {
	live := make([]int, 0, len(in))
	var total float64
	for i := range in {
		if in[i].Move == Halt {
			in[i].Share = 0
			continue
		}
		live = append(live, i)
		total += weigh(in[i])
	}
	if len(live) == 0 {
		return
	}
	rest := 1 - Floor*float64(len(live))
	if rest <= 0 || total <= 0 {
		for _, i := range live {
			in[i].Share = 1 / float64(len(live))
		}
		return
	}
	for _, i := range live {
		in[i].Share = Floor + rest*weigh(in[i])/total
	}
}

// weigh is what a class is worth in the division. A class being cut still counts
// for half of what it pays, since Less is a class worth less rather than a class
// worth nothing, and a class that paid nothing at all weighs nothing rather than
// weighing backwards.
func weigh(s Slice) float64 {
	w := s.PerFetch
	if w < 0 {
		w = 0
	}
	if s.Move == Less {
		w /= 2
	}
	return w
}

// Gets is how many of the next stretch this class gets.
func (s Slice) Gets(stretch int64) int64 { return int64(s.Share * float64(stretch)) }

// Biggest is the class taking the largest share, and false if nothing is left in
// the crawl to give it to.
func (b Budget) Biggest() (Slice, bool) {
	for _, s := range b.Slices {
		if s.Move != Halt {
			return s, true
		}
	}
	return Slice{}, false
}

// Moving is every class whose share is being changed on the evidence, which is
// the only part of a budget worth arguing about.
func (b Budget) Moving() []Slice {
	out := make([]Slice, 0, len(b.Slices))
	for _, s := range b.Slices {
		if s.Move != Hold {
			out = append(out, s)
		}
	}
	return out
}

// Blocking is every reason this division is not a decision yet.
func (b Budget) Blocking() []string { return b.Faults }

// Settled reports whether the numbers behind the division are worth acting on.
func (b Budget) Settled() bool { return len(b.Faults) == 0 }

// Verdict is the division in one sentence.
func (b Budget) Verdict() string {
	if !b.Settled() {
		return fmt.Sprintf("%s, so the next %s are not divided on this",
			b.Faults[0], fetchCount(b.Stretch))
	}
	top, ok := b.Biggest()
	if !ok {
		return fmt.Sprintf("every target class is halted on objections, so there is nothing to spend the next %s on", fetchCount(b.Stretch))
	}
	moving := b.Moving()
	if len(moving) == 0 {
		return fmt.Sprintf(
			"every class is inside the noise around %.1f tokens a fetch, so the next %s go out as they went out last time",
			b.PerFetch, fetchCount(b.Stretch))
	}
	return fmt.Sprintf(
		"%s takes %.0f%% of the next %s at %.1f tokens a fetch against %.1f across the crawl, decided on the last %s rather than on the whole run",
		top.Class, 100*top.Share, fetchCount(b.Stretch), top.PerFetch, b.PerFetch, fetchCount(b.Window))
}

// fetchCount prints a crawl scale number the way anybody discussing this crawl
// says it out loud.
func fetchCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprint(n)
	}
}
