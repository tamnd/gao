// Package needle is vi-needle: whether a long context is read or skimmed, in
// Vietnamese.
//
// Kim is a needle. The test everybody runs is the same one: hide a fact in a
// long document, ask for it back, and see whether the model finds it. The
// English versions of it have been saturated for years, which is usually
// reported as the problem being solved and is mostly the test being easy. Three
// things make it easy, and all three are fixable.
//
// The first is the haystack. A haystack made of one paragraph repeated until it
// is long enough leaves the needle as the only novel text in the context, so a
// model finds it by noticing something new rather than by retrieving something
// asked for. The haystack here is real corpus prose, different all the way
// down, which is the one thing this project has more of than it needs.
//
// The second is that the answer is usually the only thing of its shape in the
// whole context. Ask which resolution number authorized something, put exactly
// one resolution number in the haystack, and the test is string search: a model
// that can attend to a rare token passes it without reading the question. Every
// item here carries decoys, which are other values of the same shape sitting
// elsewhere in the haystack in sentences that answer a different question. A
// model matching on the wording lands on one of those, and the grading says so
// rather than calling it a miss.
//
// The third is Vietnamese, and it is the part no English test can have. Hoà and
// họa and hóa are three words that differ only in a mark, and a model whose
// tokenizer was built for English tends to treat them as one. So a portion of
// the set hides a needle whose near miss is identical to it with the marks off,
// and a model that has folded the marks away internally cannot tell which it
// was asked for. It answers, it answers confidently, and it is wrong for a
// reason no aggregate score would have shown. Those are counted apart from
// ordinary misses, because they are a different bug with a different fix.
//
// One more thing is here for the same reason the over-refusal set exists: some
// items have no needle at all. A retrieval test where the answer is always
// present rewards a model that produces its most plausible span every time, and
// that model scores well here and invents a citation in front of a user. The
// right answer to those is to say the document does not contain it.
package needle

import (
	"encoding/binary"
	"fmt"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/normalize"
	"github.com/zeebo/blake3"
)

// Repo is where the set is published.
const Repo = "vi-needle"

// Lengths is the context sizes the set is built at, in gao tokens.
//
// Four rather than a dozen, because every length multiplies the whole grid and
// the cost of this benchmark is the number of tokens pushed through a card we
// own. The interesting thing is the shape of the curve rather than its
// resolution, and four points show a shape.
var Lengths = []int{4_000, 16_000, 64_000, 128_000}

// Depths is where in the haystack the needle goes, as a fraction of the way
// through.
//
// The ends are in the list and so are the points just inside them, because the
// failure this measures is not uniform. Models read the beginning and the end
// and skim the middle, and a grid of three depths reports that as one number
// slightly below one.
var Depths = []float64{0, 0.1, 0.25, 0.5, 0.75, 0.9, 1}

// PerCell is how many items sit at each length, depth and kind.
//
// Two, and the budget went into depths instead. A third repeat at one depth
// says less about a model than a seventh point on the position curve.
const PerCell = 2

// A Kind is what about an item makes it worth asking.
type Kind string

const (
	// Plain is a fact stated once, in ordinary prose, with decoys elsewhere.
	Plain Kind = "plain"

	// Toned is the Vietnamese one. The needle and its near miss are the same
	// letters with different marks, so a model that has folded the marks cannot
	// answer it even though it can find it.
	Toned Kind = "toned"

	// Split needs two facts from two depths, which is the difference between
	// retrieving a span and reading a document. A model that can find one
	// needle and not two has a retrieval window rather than a context.
	Split Kind = "split"

	// Absent has no needle. The answer is that the document does not say, and
	// it is the item that stops a model scoring well by always producing its
	// most plausible span.
	Absent Kind = "absent"
)

// Kinds is every kind, in the order the frame lays them out.
var Kinds = []Kind{Plain, Toned, Split, Absent}

// Pairs is how many depth pairs a split item is built at, per length. The pairs
// are the two ends, the two quarters, and one across the middle.
const Pairs = 3

// The gate.
//
// Recall is the bar at every length the fleet can hold comfortably, and Long is
// the bar at the longest, which is lower on purpose: a model that holds 90% out
// to 64k and 80% at 128k is a useful model, and pretending otherwise sets a bar
// that gets met by shortening the benchmark.
//
// Spread is the one that is not in the usual write-up. It is the gap between
// the best depth and the worst, and it is the number that says whether a
// context was read or its ends were. A model can clear 0.90 overall while
// answering nothing placed past the halfway mark, and that model is sold as a
// 128k model and behaves like an 8k one.
//
// Tone is the share of answers that came back as the near miss rather than the
// needle. It is capped low because it is not a hard question: the two spellings
// are different words to anybody who reads Vietnamese.
//
// Invented is the share of absent items answered with a span anyway. It is the
// only number here a model gets worse at by trying harder.
const (
	MinRecall = 0.90
	MinLong   = 0.80
	MaxSpread = 0.15
	MaxTone   = 0.05
	MaxInvent = 0.05
)

// A Cell is one square of the grid: a length, a kind, and where the needle sits.
type Cell struct {
	Length int  `json:"length"`
	Kind   Kind `json:"kind"`

	// Depth is where the needle goes. Split items use Depth and Second, and
	// absent items use neither.
	Depth  float64 `json:"depth"`
	Second float64 `json:"second,omitempty"`

	// Items is how many items are drawn for this cell.
	Items int `json:"items"`
}

// An Item is one built question against one built haystack.
//
// It is the output of building the set rather than part of the frame, because
// the questions come out of the corpus and the corpus is not ingested yet. What
// is fixed before any of that is the grid and the rules, which is the half a
// benchmark gets wrong by leaving until after it has results.
type Item struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`

	Length int     `json:"length"`
	Depth  float64 `json:"depth"`
	Second float64 `json:"second,omitempty"`

	// Haystack is the digest of the filler this item was built against, so an
	// answer can be traced to the exact context it was given.
	Haystack doc.Hash `json:"haystack"`

	Question string `json:"question"`

	// Answer is what the needle says. It is empty for an absent item, where
	// there is nothing to find and saying so is the answer.
	Answer string `json:"answer,omitempty"`

	// Near are the wrong answers that are almost this one. For a toned item
	// they differ only in their marks, and the set is refused if they do not.
	Near []string `json:"near,omitempty"`

	// Decoy are the other values of the same shape that the haystack contains,
	// which is what stops the item from being solvable by finding the only
	// thing in the context that looks like an answer.
	Decoy []string `json:"decoy,omitempty"`
}

// Frame is the grid, fixed and hashable before a document is drawn.
func Frame() []Cell {
	out := make([]Cell, 0, (2*len(Depths)+Pairs+1)*len(Lengths))
	for _, n := range Lengths {
		for _, d := range Depths {
			out = append(out, Cell{Length: n, Kind: Plain, Depth: d, Items: PerCell})
			out = append(out, Cell{Length: n, Kind: Toned, Depth: d, Items: PerCell})
		}
		// The pairs are the two ends, the two quarters, and one that runs from
		// near the front to near the back, which is the one a retrieval window
		// cannot cover in a single reach.
		for _, p := range [Pairs][2]float64{{0, 1}, {0.25, 0.75}, {0.1, 0.9}} {
			out = append(out, Cell{Length: n, Kind: Split, Depth: p[0], Second: p[1], Items: PerCell})
		}
		out = append(out, Cell{Length: n, Kind: Absent, Items: PerCell})
	}
	return out
}

// Size is how many items the frame calls for.
func Size() int {
	var n int
	for _, c := range Frame() {
		n += c.Items
	}
	return n
}

// Tokens is how many tokens running the whole set costs once, which is the
// number that decides whether it can be run on a card we own.
func Tokens() int {
	var n int
	for _, c := range Frame() {
		n += c.Length * c.Items
	}
	return n
}

// Digest is the frame's identity. A published result names it, so a set built
// against a different grid cannot be compared to one built against this.
func Digest() doc.Hash {
	h := blake3.New()
	fmt.Fprintf(h, "%s\n", Repo)
	for _, c := range Frame() {
		fmt.Fprintf(h, "%d\t%s\t", c.Length, c.Kind)
		_ = binary.Write(h, binary.BigEndian, c.Depth)
		_ = binary.Write(h, binary.BigEndian, c.Second)
		fmt.Fprintf(h, "\t%d\n", c.Items)
	}
	return doc.Hash(h.Sum(nil))
}

// Describe is the frame in a sentence.
func Describe() string {
	return fmt.Sprintf("%s: %d items over %s and %s, %s apiece, %.1f million tokens to run once",
		Repo, Size(), plural(len(Lengths), "context length"), plural(len(Depths), "depth"),
		plural(PerCell, "item"), float64(Tokens())/1e6)
}

// Check reports every way a built set disagrees with the frame it was supposed
// to be built against.
//
// It is separate from grading and it runs first, because a set with the wrong
// shape produces a number rather than an error, and that number goes into a
// release note looking exactly like a real one.
func Check(items []Item) []string {
	var out []string

	want := map[string]int{}
	for _, c := range Frame() {
		want[cellKey(c.Length, c.Kind, c.Depth, c.Second)] += c.Items
	}

	got := map[string]int{}
	seen := map[string]bool{}
	var twice, loose, unmarked, findable, answered []string
	for _, it := range items {
		if seen[it.ID] {
			twice = append(twice, it.ID)
			continue
		}
		seen[it.ID] = true
		got[cellKey(it.Length, it.Kind, it.Depth, it.Second)]++

		switch it.Kind {
		case Absent:
			if it.Answer != "" {
				answered = append(answered, it.ID)
			}
		case Toned:
			if len(it.Near) == 0 {
				unmarked = append(unmarked, it.ID)
				break
			}
			// The whole point of a toned item is that the near miss is the same
			// word with different marks. One that is merely a different word is
			// a plain item wearing the label, and it would be counted as a tone
			// confusion when a model got it wrong.
			for _, n := range it.Near {
				if !sameBare(n, it.Answer) {
					loose = append(loose, it.ID)
					break
				}
			}
		}
		if it.Kind != Absent && len(it.Decoy) == 0 {
			findable = append(findable, it.ID)
		}
	}

	var missing, extra int
	for k, n := range want {
		if got[k] < n {
			missing += n - got[k]
		}
	}
	for k, n := range got {
		if n > want[k] {
			extra += n - want[k]
		}
	}
	if missing > 0 {
		out = append(out, fmt.Sprintf("the grid calls for %s and %s of that is not here, so the position curve has holes in it and the average over what is left is an average over the easy squares",
			plural(Size(), "item"), plural(missing, "item")))
	}
	if extra > 0 {
		out = append(out, fmt.Sprintf("%s sit outside the grid, and an item added to a fixed set is an item somebody wanted in it",
			plural(extra, "item")))
	}
	if len(twice) > 0 {
		out = append(out, fmt.Sprintf("%s appear twice: %s", plural(len(twice), "item"), join(twice)))
	}
	if len(answered) > 0 {
		out = append(out, fmt.Sprintf("%s are marked absent and carry an answer: %s. An absent item with an answer in it is a plain item that will be scored as an invention",
			plural(len(answered), "item"), join(answered)))
	}
	if len(unmarked) > 0 {
		out = append(out, fmt.Sprintf("%s are toned and carry no near miss: %s. Without one there is nothing for the marks to distinguish and the item measures ordinary retrieval",
			plural(len(unmarked), "item"), join(unmarked)))
	}
	if len(loose) > 0 {
		out = append(out, fmt.Sprintf("%s are toned and their near miss is a different word rather than the same word marked differently: %s. A model that gets one wrong would be reported as folding tone marks when it did something else",
			plural(len(loose), "item"), join(loose)))
	}
	if len(findable) > 0 {
		out = append(out, fmt.Sprintf("%s carry no decoy: %s. Nothing else in the context looks like an answer to the question, so the item can be solved by finding the only value of the right shape and never reading the question",
			plural(len(findable), "item"), join(findable)))
	}
	return out
}

func cellKey(length int, kind Kind, depth, second float64) string {
	if kind == Absent {
		return fmt.Sprintf("%d/%s", length, kind)
	}
	return fmt.Sprintf("%d/%s/%.2f/%.2f", length, kind, depth, second)
}

// sameBare reports whether two strings are the same letters once the tone marks
// come off, which is what makes a near miss near.
func sameBare(a, b string) bool {
	return a != b && strings.EqualFold(normalize.Bare(a), normalize.Bare(b))
}

// join names the items and stops once the list is longer than somebody reads.
func join(ids []string) string {
	out := slices.Clone(ids)
	slices.Sort(out)
	if len(out) <= 5 {
		return strings.Join(out, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(out[:5], ", "), len(out)-5)
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
