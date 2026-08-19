// Package trust decides whether the fast benchmark can be believed about the
// slow one.
//
// tin is to believe. The ablation slate is forty runs of a 1.4B parameter model
// scored by vi-cloze, and every threshold in this project is set from it. None
// of that is worth anything unless the ordering vi-cloze produces at 1.4B is the
// ordering the real benchmark produces at 8B, and that is a claim about the
// instrument rather than about any recipe. It has to be measured, it has to be
// measured against runs at the scale the answer will actually be used at, and
// the answer has to be published whichever way it comes out.
//
// The kill criterion for the slice is written on this number. Below a rank
// correlation of 0.5 the whole slate is reported as exploratory rather than
// decisive, every threshold falls back to a published default, and each one is
// flagged as unvalidated in the release notes. That is a real outcome with a
// real cost, which is exactly why the measurement has to be built before the
// runs rather than after somebody has seen a number they like.
//
// Two things here are load bearing and neither is the correlation.
//
// The first is the noise floor. The slate runs its baseline recipe three times
// with different seeds, and the spread between those three is what two runs of
// the same recipe differ by for no reason at all. A pair of recipes whose real
// scores differ by less than that is a coin flip, and counting a coin flip the
// proxy happened to call correctly as an agreement is how a proxy that knows
// nothing reports 70%. Those comparisons are counted, named and left out.
//
// The second is that the three baseline runs are one recipe. Ranking them as
// three points puts three nearly identical scores into the correlation and pulls
// it toward whatever they happen to say. They contribute the floor and one
// representative, which is what they are.
package trust

import (
	"errors"
	"fmt"

	"github.com/tamnd/gao/doc"
)

// Proxy is the fast benchmark the slate is scored by and Anchor is the slow one
// it has to agree with. They are named here rather than passed in because a
// validity study that can be pointed at any two benchmarks is a study somebody
// can retarget after seeing the answer.
const (
	Proxy  = "vi-cloze"
	Anchor = "vmlu"
)

// Scales are what each of the two is run at. The proxy is only worth having
// because it is cheap, and it is only worth believing because it was checked
// against something expensive.
const (
	ProxyScale  = "1.4B parameters over 40B tokens"
	AnchorScale = "8B parameters at full scale"
)

// The three numbers this slice is gated on.
//
// Believable is P10-1 and it is the bar. Kill is the criterion from the plan,
// and the gap between the two is deliberate: a correlation between them is a
// proxy that is neither trustworthy nor useless, and the honest report of that
// is a slate whose findings carry a caveat rather than one that is thrown out.
// Agree is P10-4, and it is a separate bar because it is a separate question.
const (
	Believable = 0.70
	Kill       = 0.50
	Agree      = 0.80
)

// MinRecipes is how many distinct recipes have to be scored both ways before
// the correlation means anything.
//
// Twelve is not a statistical result, it is a floor under a number that is
// otherwise trivially gamed: a rank correlation over four recipes takes one
// value out of a handful and lands on 0.8 by accident often enough that nobody
// should have to argue about it afterwards. The anchor runs are 8B runs and they
// cost real money, so this is also the line item somebody will try to cut, which
// is the other reason it is written down first.
const MinRecipes = 12

// MinBaselines is how many repeats of the one baseline recipe the noise floor
// needs. Two runs give a difference rather than a spread, and a floor taken
// from a single difference is a floor that moves with the seed.
const MinBaselines = 3

// A Pair is one recipe scored at both scales.
//
// Both boxes are recorded because a proxy validated on one machine and used on
// another has not been validated, and because the slice already requires every
// ablation result to say where it was produced.
type Pair struct {
	// Run is the run's ID on the slate and Slate is the digest of the slate it
	// was produced under.
	Run   string   `json:"run"`
	Slate doc.Hash `json:"slate"`

	// Baseline says this is one of the repeats of the baseline recipe. Those
	// are where the noise floor comes from, and they are one recipe rather than
	// several however many times it was run.
	Baseline bool `json:"baseline,omitempty"`

	// Proxy is the fast score and Anchor is the slow one.
	Proxy  float64 `json:"proxy"`
	Anchor float64 `json:"anchor"`

	// ProxyBox and AnchorBox are where each was produced.
	ProxyBox  string `json:"proxy_box"`
	AnchorBox string `json:"anchor_box"`

	Note string `json:"note,omitempty"`
}

var (
	// ErrNoPairs is what a study with nothing in it comes back as.
	ErrNoPairs = errors.New("trust: nothing has been scored at both scales")

	// ErrNoFloor is a study that cannot say what two runs of one recipe differ
	// by, which makes every comparison in it read against a floor of zero.
	ErrNoFloor = errors.New("trust: the baseline was not repeated, so there is no noise floor")
)

// A Miss is a comparison the proxy called backwards.
//
// It carries how far apart the anchor put the two, because a proxy that gets
// close calls wrong is a proxy with a resolution limit and a proxy that gets
// wide calls wrong is a proxy measuring something else.
type Miss struct {
	Better    string  `json:"better"`
	Worse     string  `json:"worse"`
	AnchorGap float64 `json:"anchor_gap"`
	ProxyGap  float64 `json:"proxy_gap"`
}

// Describe is what the study is, in a sentence, for the top of a report.
func Describe() string {
	return fmt.Sprintf("whether %s at %s orders recipes the way %s at %s does, measured over at least %s scored both ways, with %s of the baseline recipe setting the floor a comparison has to clear to be a comparison at all",
		Proxy, ProxyScale, Anchor, AnchorScale, plural(MinRecipes, "recipe"), plural(MinBaselines, "repeat"))
}

// plural writes a count with its noun, which every report here needs and no
// standard library has.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
