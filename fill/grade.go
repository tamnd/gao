package fill

// Scoring a run, and the two floors a score has to be published against.

import (
	"fmt"
	"strings"

	"github.com/tamnd/gao/doc"
)

// Chance is what a model that reads nothing scores.
const Chance = 1.0 / Candidates

// A Result is one item answered.
type Result struct {
	DocID doc.Hash `json:"doc_id"`

	// Chose is the index the model picked, and Right is whether it was the one
	// the page had.
	Chose int  `json:"chose"`
	Right bool `json:"right"`

	// Rank is the item's frequency rank, carried through so a report can say
	// whether the run did better on the items where the answer was the common
	// word. A model that only wins those has learned the unigram distribution.
	Rank int `json:"rank"`
}

// Grade scores one answer, given as an index into the item's choices.
func Grade(it Item, chose int) Result {
	return Result{DocID: it.DocID, Chose: chose, Right: chose == it.Answer, Rank: it.Rank}
}

// Frequent is the baseline: pick the most common candidate and never read the
// passage.
//
// It is here to be run rather than argued about. By construction the answer's
// frequency rank among the candidates is spread evenly across the set, so this
// scores chance, and a build that broke the spread shows up as this baseline
// scoring well. That is the failure worth catching, because a benchmark the
// unigram distribution can win looks exactly like a benchmark a model is
// winning.
func Frequent(v *Vocabulary, it Item) int {
	best, at := -1, 0
	for i, s := range it.Choices {
		if n := v.Count(s); n > best {
			best, at = n, i
		}
	}
	return at
}

// A Report is a whole run scored, which is what gets published.
type Report struct {
	// Box is the machine the run happened on. A proxy score with no hardware
	// behind it is not reproducible, and this benchmark exists to be run forty
	// times and compared.
	Box string `json:"box"`

	Items int `json:"items"`
	Right int `json:"right"`

	// ByRank is how many items sat at each frequency rank and how many of them
	// were answered right. The first is the spread that holds the frequency
	// baseline down, and it belongs in the output rather than in a test.
	ByRank    [Candidates]int `json:"by_rank"`
	RightRank [Candidates]int `json:"right_rank"`
}

// NewReport starts a report on a box.
func NewReport(box string) *Report { return &Report{Box: box} }

// Add folds one result in.
func (r *Report) Add(res Result) {
	r.Items++
	if res.Right {
		r.Right++
	}
	if res.Rank >= 0 && res.Rank < Candidates {
		r.ByRank[res.Rank]++
		if res.Right {
			r.RightRank[res.Rank]++
		}
	}
}

// Accuracy is the headline number.
func (r Report) Accuracy() float64 {
	if r.Items == 0 {
		return 0
	}
	return float64(r.Right) / float64(r.Items)
}

// RankAccuracy is the accuracy on the items whose answer had that many more
// common candidates beside it.
func (r Report) RankAccuracy(rank int) float64 {
	if rank < 0 || rank >= Candidates || r.ByRank[rank] == 0 {
		return 0
	}
	return float64(r.RightRank[rank]) / float64(r.ByRank[rank])
}

// Skew is how far the frequency ranks are from evenly spread, as a share of the
// items.
//
// Zero is a set where the answer is the most common candidate exactly a quarter
// of the time. Anything much above zero is a set the frequency baseline can
// beat chance on, and the number is reported rather than asserted because the
// spread is a property of the corpus the vocabulary was counted over and not
// something the builder can promise.
func (r Report) Skew() float64 {
	if r.Items == 0 {
		return 0
	}
	even := float64(r.Items) / Candidates
	var off float64
	for _, n := range r.ByRank {
		d := float64(n) - even
		if d < 0 {
			d = -d
		}
		off += d
	}
	return off / (2 * float64(r.Items))
}

// String renders the report the way it is published.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d of %d, which is %.1f%% against %.1f%% for reading nothing, on %s\n",
		Name, r.Right, r.Items, 100*r.Accuracy(), 100*Chance, box(r.Box))
	fmt.Fprintf(&b, "\n%-28s %8s %8s\n", "answer beaten on frequency by", "items", "right")
	for i := range Candidates {
		fmt.Fprintf(&b, "%-28d %8d %7.1f%%\n", i, r.ByRank[i], 100*r.RankAccuracy(i))
	}
	fmt.Fprintf(&b, "\nThe ranks are %.1f%% off an even spread. An even spread is what holds picking the commonest candidate to %.1f%%.\n",
		100*r.Skew(), 100*Chance)
	return b.String()
}

func box(name string) string {
	if name == "" {
		return "a box that did not say which it was"
	}
	return name
}
