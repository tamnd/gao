package suat

import (
	"strings"
	"testing"
)

// share finds one class's slice, since the report is sorted by what it got.
func share(t *testing.T, b Budget, c Class) Slice {
	t.Helper()
	for _, s := range b.Slices {
		if s.Class == c {
			return s
		}
	}
	t.Fatalf("%s is not in the budget", c)
	return Slice{}
}

// tokens overwrites what one class is worth at the last point, which is how a
// test makes a class start or stop paying without disturbing the fetch
// accounting the run checks.
func tokens(r *Run, c Class, n int64) {
	p := r.Points[len(r.Points)-1]
	tl := p.By[c]
	tl.Tokens = n
	p.By[c] = tl
}

func TestABudgetMovesOnWhatAFetchBuysNowRatherThanOnTheWholeCrawl(t *testing.T) {
	r := run(6, 0.18)
	before := r.Budget(100_000_000)
	if m := share(t, before, Forum).Move; m != More && m != Hold {
		t.Fatalf("forums were %s before anything was changed", m)
	}

	// Forums have been read. The cumulative number barely notices, since five
	// stretches of good production are still in it, and that is the entire
	// reason this is measured on the window.
	last := r.Points[len(r.Points)-1]
	prev := r.Points[len(r.Points)-2]
	tokens(r, Forum, prev.By[Forum].Tokens+(last.By[Forum].Tokens-prev.By[Forum].Tokens)/10)

	after := r.Budget(100_000_000)
	f := share(t, after, Forum)
	if f.Move != Less {
		t.Errorf("forums paying a tenth of what they used to were called %s: %s", f.Move, f.Why)
	}
	if f.PerFetch >= f.Average {
		t.Errorf("the window did not fall below the crawl average: %.1f against %.1f", f.PerFetch, f.Average)
	}
	if !strings.Contains(f.Why, "already been read") {
		t.Errorf("the reason does not say what happened: %s", f.Why)
	}
	if after.Verdict() == before.Verdict() {
		t.Error("the verdict did not change when the best class stopped paying")
	}
}

// A class cut to nothing produces no more measurements, so it can never be
// found to have recovered, and one bad stretch becomes permanent.
func TestAClassIsNeverCutToNothingOnYieldAlone(t *testing.T) {
	r := run(6, 0.18)
	tokens(r, Commerce, r.Points[len(r.Points)-2].By[Commerce].Tokens)
	b := r.Budget(100_000_000)

	c := share(t, b, Commerce)
	if c.Move != Less {
		t.Fatalf("a class that produced nothing in the window was called %s", c.Move)
	}
	if c.Share < Floor {
		t.Errorf("commerce was cut to %.3f, under the floor of %.2f", c.Share, Floor)
	}
	if c.Gets(100_000_000) <= 0 {
		t.Error("a class still in the crawl was given no fetches")
	}

	var total float64
	for _, s := range b.Slices {
		total += s.Share
	}
	if total < 0.999 || total > 1.001 {
		t.Errorf("the shares add up to %.4f", total)
	}
}

// Objections are answered today. A class that pays well and is being asked to
// stop does not get to argue about yield.
func TestAClassBeingAskedToStopDoesNotCompeteOnYield(t *testing.T) {
	r := run(6, 0.18)
	p := r.Points[len(r.Points)-1]
	tl := p.By[Forum]
	tl.Tokens *= 4
	tl.Objected = tl.Hosts / 10
	p.By[Forum] = tl

	b := r.Budget(100_000_000)
	f := share(t, b, Forum)
	if f.Move != Halt {
		t.Fatalf("a class with a tenth of its hosts objecting was called %s", f.Move)
	}
	if f.Share != 0 || f.Gets(100_000_000) != 0 {
		t.Errorf("a halted class was given %.3f of the next stretch", f.Share)
	}
	if !strings.Contains(f.Why, "buys tokens with the takedown path") {
		t.Errorf("the reason does not say why yield is beside the point: %s", f.Why)
	}
	if top, ok := b.Biggest(); !ok || top.Class == Forum {
		t.Errorf("the halted class came back as the one taking the most: %+v", top)
	}
}

func TestABudgetCannotBeAimedAtAClassNobodyNamed(t *testing.T) {
	r := run(6, 0.18)
	p := r.Points[len(r.Points)-1]

	// Push other past a quarter of the crawl by taking duplicate fetches off the
	// classes that have them, which is what a classifier giving up looks like
	// from here. Duplicates only, since documents are cumulative and a class
	// cannot keep fewer of them than it kept last time.
	need := p.At/4 + p.At/50 - p.By[Other].Fetches
	o := p.By[Other]
	for _, c := range []Class{News, Commerce, Government, Education} {
		if need <= 0 {
			break
		}
		t := p.By[c]
		take := min(need, t.Duplicates)
		t.Fetches, t.Duplicates = t.Fetches-take, t.Duplicates-take
		o.Fetches, o.Duplicates = o.Fetches+take, o.Duplicates+take
		p.By[c], need = t, need-take
	}
	p.By[Other] = o

	b := r.Budget(100_000_000)
	if b.Settled() {
		t.Fatal("a budget divided over a crawl the classifier gave up on came back settled")
	}
	faultAbout(t, b.Blocking(), "a class nobody named")
	for _, s := range b.Slices {
		if s.Class == Other {
			t.Error("other was given a share of the next stretch")
		}
	}
}

func TestOneCheckpointIsHistoryRatherThanAMargin(t *testing.T) {
	one := run(1, 0.18).Budget(100_000_000)
	if one.Settled() {
		t.Fatal("a crawl with one checkpoint settled the next hundred million fetches")
	}
	faultAbout(t, one.Blocking(), "moved on history")

	none := (&Run{Crawl: "gao-crawl-2026-09"}).Budget(100_000_000)
	if none.Settled() || len(none.Slices) != 0 {
		t.Errorf("nothing measured came back with %d slices", len(none.Slices))
	}
	if !strings.Contains(none.Verdict(), "not divided on this") {
		t.Errorf("the verdict on an empty run reads %q", none.Verdict())
	}
}

// A window too short to say anything about a class says so, rather than dividing
// a hundred million fetches on which threads the crawler happened to reach.
func TestAWindowTooShortToSayAnythingSaysSo(t *testing.T) {
	r := &Run{Crawl: "gao-crawl-2026-09", Points: []Point{
		point(Stride, 0.18, "server1"),
		point(Stride+400_000, 0.18, "server1"),
	}}
	b := r.Budget(100_000_000)
	if b.Settled() {
		t.Fatal("400k fetches settled the division of a hundred million")
	}
	faultAbout(t, b.Blocking(), "which threads the crawler happened to reach")
	if g := share(t, b, Government); g.Fetches >= Sample {
		t.Errorf("government came back with %d fetches in the window", g.Fetches)
	}
}

// Most of the time nothing has changed, and a budget that finds a reason to move
// every checkpoint is a budget chasing noise.
func TestEveryClassInsideTheNoiseIsNotAMove(t *testing.T) {
	b := run(6, 0.18).Budget(100_000_000)
	if !b.Settled() {
		t.Fatalf("a healthy run was faulted: %v", b.Blocking())
	}
	for _, s := range b.Slices {
		if s.Move != Hold && s.Move != More && s.Move != Less {
			t.Errorf("%s came back as %s", s.Class, s.Move)
		}
		if s.Fetches <= 0 {
			t.Errorf("%s has no window behind its share of %.3f", s.Class, s.Share)
		}
	}
	if top, ok := b.Biggest(); !ok || top.Share <= 0 {
		t.Errorf("nothing took the largest share of a healthy crawl")
	}
	if len(b.Moving()) == len(b.Slices) {
		t.Error("every single class was moved, which is a budget chasing noise")
	}

	// And the stretch being divided is not the stretch it was measured on, which
	// a report has to keep separate.
	if b.Window == b.Stretch {
		t.Errorf("the window and the stretch are both %d", b.Window)
	}
	if !strings.Contains(b.Verdict(), "rather than on the whole run") && !strings.Contains(b.Verdict(), "as they went out last time") {
		t.Errorf("the verdict does not say what it was decided on: %s", b.Verdict())
	}
}

// A class that was not fetched at all in the window is not evidence of anything,
// and it is not treated as a class that produced nothing.
func TestAClassNobodyFetchedIsNotAClassThatFailed(t *testing.T) {
	r := run(6, 0.18)
	p := r.Points[len(r.Points)-1]
	prev := r.Points[len(r.Points)-2]
	p.By[Government] = prev.By[Government]

	// Give the fetches the window lost to another class so the accounting still
	// closes, since a point that does not sum to its fetches is refused first.
	gap := p.At - p.Total().Fetches
	c := p.By[Commerce]
	c.Fetches, c.Duplicates = c.Fetches+gap, c.Duplicates+gap
	p.By[Commerce] = c

	b := r.Budget(100_000_000)
	g := share(t, b, Government)
	if g.Move != Hold || !strings.Contains(g.Why, "not fetched in this window") {
		t.Errorf("a class nobody fetched was called %s: %s", g.Move, g.Why)
	}
	if g.Share < Floor {
		t.Errorf("a class nobody fetched was cut to %.3f", g.Share)
	}
}
