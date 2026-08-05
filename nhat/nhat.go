// Package nhat is decontamination: it finds the documents that hold the text of
// a benchmark gao is judged on.
//
// The name is nhặt sạn, picking the grit out of the rice. The grit here is small
// and it is the only contaminant in the pipeline that gets into the corpus by
// being wanted somewhere else: a benchmark item published on the web, quoted in
// a blog post, discussed on a forum, or scraped into a dataset that was scraped
// again. A model trained on a corpus holding its own test set scores well and
// has learned nothing, and the number that comes out is not wrong by a little.
//
// # Why this runs last and separately
//
// Every other stage in the pipeline takes the corpus as its input. This one
// takes the corpus and a list of benchmarks, and the list changes without the
// corpus changing, because a benchmark published next year is a benchmark this
// corpus has to be checked against. Running the check last means a new benchmark
// costs one scan rather than a rerun of everything.
//
// The list only grows. A benchmark that comes off it is a result that stops
// being checked, and the failure this rule exists to prevent is a contaminated
// benchmark quietly disappearing from a table of scores rather than appearing in
// it with the contamination written next to it. The rule is enforced on the
// roster, which is the names and revisions in benchmarks.json, and a run's list
// is checked against the roster before the scan starts so that a benchmark that
// failed to fetch fails the run rather than coming back clean.
//
// # What a gram is here, and why it is not what it is in English
//
// The check is 13-gram exact overlap, which is the number the field settled on,
// and the field settled on it counting English words. Vietnamese writes a space
// between syllables rather than between words, so a 13-gram of what lies
// between the spaces is about 8 words of Vietnamese and not 13. That makes this
// check more sensitive than the English one it is borrowed from, which is the
// direction a decontamination check should err in: the cost of a false flag is
// one document reported and one reader looking at it, and the cost of a miss is
// a published score that is not real.
//
// Grams are taken over the deduplication key, so a benchmark item and a copy of
// it that changed the quotes, the capitals, or the i and y spelling are the same
// text here. That is the same key [xay.Key] builds, on purpose: a decontamination
// check that could be defeated by the things a republisher changes would be a
// check against careful copying only.
//
// # What is not here
//
// The embedding neighbor check is not written. N-grams cannot see a benchmark
// item that was translated or paraphrased into the corpus, and for Vietnamese
// benchmarks that were themselves translated from English that is the channel
// that matters most. It needs an index this project does not have yet, and
// saying so is better than reporting an n-gram number as though it were the
// whole answer.
package nhat

import (
	"strings"

	"github.com/tamnd/gao/xay"
)

// GramSize is the length of the window, in whatever the deduplication key puts
// between spaces, which for Vietnamese is a syllable.
const GramSize = 13

// DropAt is how many distinct grams a document has to share with one benchmark
// before it is removed rather than reported.
//
// Windows overlap, so three grams is one run of 15 syllables rather than three
// separate quotations. A document that shares one gram has a sentence fragment
// in common with a test item, which happens by chance in a corpus this size. A
// document that shares fifteen consecutive syllables with a test item has the
// item.
//
// A benchmark built by holding text out of gao's own corpus is the exception and
// drops at one, because for those the overlap is not evidence of contamination,
// it is the hold out that did not happen.
const DropAt = 3

// MaxBenchmarks is how many benchmarks one index holds.
//
// It is a real limit rather than a resource one. Each gram carries the set of
// benchmarks it came from as one word of bits, which makes the check an AND, and
// the day the list passes 64 the representation changes rather than the limit
// being raised quietly.
const MaxBenchmarks = 64

// Index holds the grams of every benchmark on a list and answers which of them a
// document carries.
//
// It is small. A benchmark is thousands of items rather than millions of
// documents, so the whole list fits in memory on any box in the fleet, and the
// scan cost is the corpus rather than the index.
//
// An Index is read only once built and is safe for concurrent use.
type Index struct {
	list  List
	items []item
	grams map[uint64]gram
}

// item is one benchmark item, kept for the report rather than for the check.
type item struct {
	benchmark int
	n         int // grams the item produced, which is how much of it can be found
}

// gram is one window of a benchmark, and where it came from.
//
// The bits are which benchmarks hold it, so a window that two benchmarks share
// is attributed to both. The item is the first one that produced it, which
// undercounts an item that shares a window with an earlier item and cannot cause
// a document to be missed, because a document that matches the window matches it
// either way.
type gram struct {
	benchmarks uint64
	item       uint32
}

// NewIndex builds the index for a list.
func NewIndex(list List) (*Index, error) {
	if err := list.check(); err != nil {
		return nil, err
	}
	x := &Index{list: list, grams: make(map[uint64]gram)}
	for b := range list.Benchmarks {
		bit := uint64(1) << uint(b)
		for _, text := range list.Benchmarks[b].Items {
			id := uint32(len(x.items))
			n := 0
			for h := range Grams(text) {
				n++
				g, seen := x.grams[h]
				if !seen {
					g.item = id
				}
				g.benchmarks |= bit
				x.grams[h] = g
			}
			x.items = append(x.items, item{benchmark: b, n: n})
		}
	}
	return x, nil
}

// List returns the list the index was built from.
func (x *Index) List() List { return x.list }

// Grams is how many distinct windows the whole list holds.
func (x *Index) Grams() int { return len(x.grams) }

// Result is what one document shares with the benchmarks.
type Result struct {
	// Grams is how many distinct windows the document holds, which is what
	// Share is taken over. Distinct rather than total, so that Share is a
	// fraction of the same thing Hits counts and a document that repeats a line
	// is not diluted by its own repetition.
	Grams int `json:"grams"`

	// Hits is how many distinct windows of it are in the list.
	Hits int `json:"hits"`

	// Benchmarks is one entry per benchmark the document touched, in list
	// order. A document that touched nothing has none.
	Benchmarks []Touch `json:"benchmarks,omitempty"`
}

// Touch is what one document shares with one benchmark.
type Touch struct {
	Benchmark string `json:"benchmark"`

	// Grams is the distinct windows shared with this benchmark.
	Grams int `json:"grams"`

	// Items is how many of the benchmark's items those windows came from.
	Items int `json:"items"`

	// Dropped says this benchmark alone is enough to remove the document.
	Dropped bool `json:"dropped"`
}

// Flagged says the document shares text with a benchmark and is worth a look.
func (r Result) Flagged() bool { return r.Hits > 0 }

// Dropped says the document shares enough with one benchmark to be removed. It
// is one benchmark rather than the total, because a document with one gram from
// each of three unrelated benchmarks is three coincidences and not a leak.
func (r Result) Dropped() bool {
	for _, t := range r.Benchmarks {
		if t.Dropped {
			return true
		}
	}
	return false
}

// Share is the fraction of the document's windows that are in the list, which is
// what says whether a document quotes an item or is one.
func (r Result) Share() float64 {
	if r.Grams == 0 {
		return 0
	}
	return float64(r.Hits) / float64(r.Grams)
}

// Check reports what one document shares with the list.
func (x *Index) Check(text string) Result {
	var r Result
	// Distinct windows, because a document that repeats a line would otherwise
	// report the same overlap once per copy and cross the drop threshold on a
	// single shared sentence.
	seen := make(map[uint64]bool)
	perBenchmark := make([]int, len(x.list.Benchmarks))
	items := make(map[uint32]bool)
	for h := range Grams(text) {
		if seen[h] {
			continue
		}
		seen[h] = true
		r.Grams++
		g, ok := x.grams[h]
		if !ok {
			continue
		}
		r.Hits++
		items[g.item] = true
		for b := range perBenchmark {
			if g.benchmarks&(uint64(1)<<uint(b)) != 0 {
				perBenchmark[b]++
			}
		}
	}
	if r.Hits == 0 {
		return r
	}

	perItem := make([]int, len(x.list.Benchmarks))
	for id := range items {
		perItem[x.items[id].benchmark]++
	}
	for b, n := range perBenchmark {
		if n == 0 {
			continue
		}
		r.Benchmarks = append(r.Benchmarks, Touch{
			Benchmark: x.list.Benchmarks[b].Name,
			Grams:     n,
			Items:     perItem[b],
			Dropped:   n >= x.list.Benchmarks[b].dropAt(),
		})
	}
	return r
}

// Tally adds up a run over a corpus.
//
// It carries a row for every benchmark on the list, including the ones nothing
// touched, because a table that holds only the contaminated benchmarks is a
// table that cannot be read as a clean bill of health for the others.
type Tally struct {
	Documents  int64             `json:"documents"`
	Flagged    int64             `json:"flagged"`
	Dropped    int64             `json:"dropped"`
	Benchmarks []BenchmarkReport `json:"benchmarks"`

	byName map[string]int
}

// BenchmarkReport is one benchmark's row in the report.
type BenchmarkReport struct {
	Benchmark string `json:"benchmark"`
	Version   string `json:"version"`

	// Origin is carried into the report because a clean result on a translated
	// benchmark and a clean result on a native one are not the same claim, and a
	// table that does not say which is which invites the stronger reading.
	Origin string `json:"origin"`

	// HeldOut says the benchmark was built out of gao's own corpus, so a row
	// with documents on it is a hold out that leaked rather than the web
	// carrying a test set.
	HeldOut bool `json:"held_out,omitempty"`

	// Items is how many items the benchmark has, which is what ItemShare is
	// taken over and what makes a count of touched items mean something.
	Items int `json:"items"`

	// Documents is corpus documents that share at least one gram with it.
	Documents int64 `json:"documents"`

	// Dropped is documents removed on this benchmark's account.
	Dropped int64 `json:"dropped"`

	// ItemsTouched is how many of its items were found in the corpus. This is
	// the number that says a benchmark is contaminated, rather than the count of
	// documents, because one viral page carrying one item is not the same
	// problem as a thousand pages carrying half the test set.
	ItemsTouched int `json:"items_touched"`

	touched map[int]bool
}

// ItemShare is the fraction of the benchmark's items found in the corpus.
func (c BenchmarkReport) ItemShare() float64 {
	if c.Items == 0 {
		return 0
	}
	return float64(c.ItemsTouched) / float64(c.Items)
}

// NewTally returns a tally with a row per benchmark on the list, all of them
// empty.
func NewTally(x *Index) *Tally {
	t := &Tally{byName: make(map[string]int, len(x.list.Benchmarks))}
	for _, b := range x.list.Benchmarks {
		t.byName[b.Name] = len(t.Benchmarks)
		t.Benchmarks = append(t.Benchmarks, BenchmarkReport{
			Benchmark: b.Name,
			Version:   b.Version,
			Origin:    b.Origin,
			HeldOut:   b.HeldOut,
			Items:     len(b.Items),
			touched:   make(map[int]bool),
		})
	}
	return t
}

// Add folds one document's result into the tally.
//
// It takes the index as well, because which items a document touched is a fact
// about the index and keeping it on the result would make every document carry a
// list the size of what it matched.
func (t *Tally) Add(x *Index, text string, r Result) {
	t.Documents++
	if !r.Flagged() {
		return
	}
	t.Flagged++
	if r.Dropped() {
		t.Dropped++
	}
	for _, touch := range r.Benchmarks {
		i, ok := t.byName[touch.Benchmark]
		if !ok {
			continue
		}
		t.Benchmarks[i].Documents++
		if touch.Dropped {
			t.Benchmarks[i].Dropped++
		}
	}
	// Which items were found needs the windows again, and it is only paid for on
	// a document that matched something, which is a small share of any corpus.
	seen := make(map[uint64]bool)
	for h := range Grams(text) {
		if seen[h] {
			continue
		}
		seen[h] = true
		g, ok := x.grams[h]
		if !ok {
			continue
		}
		b := x.items[g.item].benchmark
		if b < len(t.Benchmarks) && !t.Benchmarks[b].touched[int(g.item)] {
			t.Benchmarks[b].touched[int(g.item)] = true
			t.Benchmarks[b].ItemsTouched++
		}
	}
}

// Contaminated says at least one benchmark on the list was found in the corpus,
// which is the one sentence a release note has to carry.
func (t *Tally) Contaminated() bool {
	for _, b := range t.Benchmarks {
		if b.Documents > 0 {
			return true
		}
	}
	return false
}

// Grams yields the hash of every window of a text, in order.
//
// The text is reduced to the deduplication key first, so what is hashed is what
// two copies of the same sentence have in common rather than how each of them
// was typed. A text shorter than the window has no grams at all, which is
// deliberate: a benchmark item of six syllables is a phrase that appears in
// ordinary Vietnamese, and indexing it would flag the corpus rather than
// measure it.
func Grams(text string) func(func(uint64) bool) {
	return func(yield func(uint64) bool) {
		s := strings.Fields(xay.Key(text))
		for i := 0; i+GramSize <= len(s); i++ {
			if !yield(hash(s[i : i+GramSize])) {
				return
			}
		}
	}
}

// hash is FNV-1a over the window, with the space between syllables hashed as
// well so that a window cannot be confused with a different split of the same
// letters.
func hash(window []string) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i, s := range window {
		if i > 0 {
			h ^= ' '
			h *= prime
		}
		for _, c := range s {
			h ^= uint64(c)
			h *= prime
		}
	}
	return h
}
