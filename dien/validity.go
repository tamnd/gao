package dien

// Whether the proxy agrees with the thing it stands in for.

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// MinRecipes is the fewest recipes a validity measurement can be made from.
//
// A rank correlation over three recipes takes one of nine values and every one
// of them is a coincidence. Five is still few, and it is the fewest that can
// distinguish agreement from luck at all, which is why the number that comes
// out of it is reported with the count beside it rather than on its own.
const MinRecipes = 5

// KillCorrelation is where the slice's kill criterion sits.
//
// Below this, the forty run slate is reported as exploratory rather than
// decisive, every threshold it set falls back to a published default from the
// literature, and every one of those is flagged as unvalidated in the release
// notes rather than presented as tuned.
const KillCorrelation = 0.5

// PredictedCorrelation is P10-1 and PredictedAgreement is P10-4. They are
// predictions rather than gates, and they are here so that the run that settles
// them is the same run that reports the answer.
const (
	PredictedCorrelation = 0.7
	PredictedAgreement   = 0.80
)

// A Recipe is one training recipe scored twice: once by the proxy at ablation
// scale and once by the benchmark the proxy stands in for at full scale.
type Recipe struct {
	// Name is what the recipe is called in the slate.
	Name string `json:"name"`

	// Proxy is what vi-cloze said at ablation scale.
	Proxy float64 `json:"proxy"`

	// Full is what the full scale evaluation said, which for this project is
	// VMLU on an 8B run.
	Full float64 `json:"full"`

	// Box names where each score came off, since the two are produced on
	// different hardware and the pair is the unit that gets published.
	ProxyBox string `json:"proxy_box"`
	FullBox  string `json:"full_box"`
}

// A Validity is what a set of recipes says about the proxy.
type Validity struct {
	// Recipes is how many pairs the measurement is over.
	Recipes int `json:"recipes"`

	// Correlation is Spearman's rank correlation between the proxy ranking and
	// the full scale ranking.
	Correlation float64 `json:"correlation"`

	// Agree and Pairs are how many of the pairwise comparisons the two rankings
	// made the same way. It is the number that answers the question anybody
	// running a slate actually has, which is whether the proxy picks the same
	// winner out of two recipes.
	Agree int `json:"agree"`
	Pairs int `json:"pairs"`

	// Decisive says whether the slate can be presented as having chosen
	// anything, which is the kill criterion and nothing else.
	Decisive bool `json:"decisive"`

	// Why is the verdict in the words it goes in the release notes with.
	Why string `json:"why"`
}

// Agreement is the share of pairwise comparisons the two rankings made the same
// way.
func (v Validity) Agreement() float64 {
	if v.Pairs == 0 {
		return 0
	}
	return float64(v.Agree) / float64(v.Pairs)
}

// ErrTooFew is returned when there are not enough recipes to say anything.
var ErrTooFew = errors.New("dien: too few recipes scored at both scales to measure whether the proxy agrees with anything")

// Validate measures whether the proxy stands in for the full scale benchmark.
//
// It refuses rather than reporting a number it does not believe. Fewer than
// [MinRecipes] pairs is a correlation made of coincidences. A recipe scored on
// one scale and not the other is not a pair. Two recipes with the same name are
// one recipe scored twice, and folding them in counts one result as evidence
// about two.
func Validate(recipes []Recipe) (Validity, error) {
	if problems := CheckRecipes(recipes); len(problems) > 0 {
		return Validity{}, errors.New("dien: " + problems[0])
	}
	if len(recipes) < MinRecipes {
		return Validity{}, ErrTooFew
	}

	proxy := ranks(recipes, func(r Recipe) float64 { return r.Proxy })
	full := ranks(recipes, func(r Recipe) float64 { return r.Full })

	v := Validity{Recipes: len(recipes), Correlation: pearson(proxy, full)}
	for i := range recipes {
		for j := i + 1; j < len(recipes); j++ {
			a := recipes[i].Proxy - recipes[j].Proxy
			b := recipes[i].Full - recipes[j].Full
			v.Pairs++
			if a*b > 0 || (a == 0 && b == 0) {
				v.Agree++
			}
		}
	}

	v.Decisive = v.Correlation >= KillCorrelation
	switch {
	case !v.Decisive:
		v.Why = fmt.Sprintf("the proxy agrees with full scale at %.2f, under the %.2f the slice set as its kill criterion, so the slate is exploratory. Every threshold it set falls back to a published default and ships flagged as unvalidated",
			v.Correlation, KillCorrelation)
	case v.Correlation >= PredictedCorrelation:
		v.Why = fmt.Sprintf("the proxy agrees with full scale at %.2f over %d recipes and picks the same winner in %.0f%% of pairs, which clears both the kill criterion and P10-1",
			v.Correlation, v.Recipes, 100*v.Agreement())
	default:
		v.Why = fmt.Sprintf("the proxy agrees with full scale at %.2f over %d recipes, which clears the %.2f kill criterion and misses the %.2f P10-1 predicted. The slate stands and the prediction does not",
			v.Correlation, v.Recipes, KillCorrelation, PredictedCorrelation)
	}
	return v, nil
}

// CheckRecipes says everything wrong with a set of pairs, in the order a person
// would fix them.
func CheckRecipes(recipes []Recipe) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range recipes {
		switch {
		case r.Name == "":
			out = append(out, "a recipe with no name cannot be reported against, since the slate is read by name")
		case seen[r.Name]:
			out = append(out, fmt.Sprintf("%q appears twice, and one recipe scored twice is one piece of evidence rather than two", r.Name))
		}
		seen[r.Name] = true
		if r.ProxyBox == "" || r.FullBox == "" {
			out = append(out, fmt.Sprintf("%q does not say which box each of its two scores came off, and a pair nobody can reproduce is not evidence about a proxy", r.Name))
		}
	}
	return out
}

// ranks is the positions of the values in their own ordering, with ties sharing
// the average of the positions they cover.
//
// Ties are the case that matters. Two recipes that scored the same at ablation
// scale are the proxy declining to separate them, and giving one of them the
// better rank because it was listed first would credit the proxy with a call it
// did not make.
func ranks(recipes []Recipe, of func(Recipe) float64) []float64 {
	order := make([]int, len(recipes))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		switch {
		case of(recipes[a]) < of(recipes[b]):
			return -1
		case of(recipes[a]) > of(recipes[b]):
			return 1
		}
		return 0
	})

	out := make([]float64, len(recipes))
	for i := 0; i < len(order); {
		j := i
		for j < len(order) && of(recipes[order[j]]) == of(recipes[order[i]]) {
			j++
		}
		mean := float64(i+j-1) / 2
		for _, k := range order[i:j] {
			out[k] = mean
		}
		i = j
	}
	return out
}

// pearson over ranks is Spearman, and it is written this way rather than with
// the six d squared formula because that formula is wrong in the presence of
// ties and the ties are the interesting case.
func pearson(a, b []float64) float64 {
	n := float64(len(a))
	if n == 0 {
		return 0
	}
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma, mb = ma/n, mb/n

	var num, da, db float64
	for i := range a {
		x, y := a[i]-ma, b[i]-mb
		num += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

// String renders the validity the way it goes into the release notes.
func (v Validity) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s against full scale: rank correlation %.2f over %d recipes, same winner in %d of %d pairs, which is %.0f%%\n",
		Name, v.Correlation, v.Recipes, v.Agree, v.Pairs, 100*v.Agreement())
	fmt.Fprintf(&b, "%s\n", v.Why)
	if v.Agreement() < PredictedAgreement {
		fmt.Fprintf(&b, "P10-4 predicted %.0f%% pairwise agreement and this run came in under it.\n", 100*PredictedAgreement)
	}
	return b.String()
}
