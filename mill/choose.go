package mill

// Choosing the deduplication threshold from what was measured.
//
// The retention curve in [Index.Curve] says what each threshold would cost in
// documents. It does not say which one is right, and reading a knee out of it is
// how a default gets a story attached to it rather than how a number gets
// chosen. The threshold is chosen by training on either side of it and looking
// at what comes out, which is what an ablation is, and this is the rule that
// turns a handful of ablation runs into one number.
//
// The rule has to refuse more often than it answers. Three points, an eval with
// a standard error, and a winner half a point ahead of the field is a winner
// picked out of noise, and a number picked out of noise is worse than a default,
// because a default is at least honest about being one. So the refusals below
// are the substance here and the arithmetic is almost nothing.

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// MinAblations is the fewest measured thresholds a choice can be made from.
//
// Two points have no shape and three is the fewest that can show one. Below
// that, taking the higher of two noisy numbers is taking the noise, and the
// answer is to run another training job rather than to squint.
const MinAblations = 3

// Sigma is how many combined standard errors one threshold has to beat another
// by before the difference is called real.
//
// Two is the usual place to draw it and it is drawn here rather than left to the
// reader, because a rule that lets whoever runs it pick the confidence is a rule
// that always finds a winner.
const Sigma = 2.0

// An Ablation is one training run at one deduplication threshold.
//
// Everything except Threshold has to be identical across the set. A run that
// changed the token count and the threshold at the same time measured the two
// against each other, and no rule below can pull them apart again.
type Ablation struct {
	// Threshold is the similarity this run deduplicated at.
	Threshold float64 `json:"threshold"`

	// Retention is the share of documents the corpus kept at this threshold,
	// which comes off the curve and costs nothing to know.
	Retention float64 `json:"retention"`

	// Score is what the eval said, in whatever unit the eval reports.
	Score float64 `json:"score"`

	// Noise is the standard error on Score. An eval quoted without one cannot
	// be compared against another eval, so this is required rather than
	// defaulted.
	Noise float64 `json:"noise"`

	// Tokens is how many tokens the run trained on.
	Tokens int64 `json:"tokens"`

	// Eval names what was measured, so two sets scored on different benchmarks
	// are not silently mixed.
	Eval string `json:"eval"`

	// Box is the fleet machine the run happened on. A score with no hardware
	// behind it is not reproducible, and a set of scores from different machines
	// is a hardware comparison wearing a threshold comparison's clothes.
	Box string `json:"box"`
}

// A Choice is what a set of ablations supports.
type Choice struct {
	// Threshold is the number to run the pipeline at.
	Threshold float64 `json:"threshold"`

	// Measured says whether the ablations moved the threshold off the default.
	// It is false when the runs were fine and simply did not separate, which is
	// a result rather than a failure.
	Measured bool `json:"measured"`

	// Why is the rule that produced Threshold, in the words a person would use.
	Why string `json:"why"`

	// Best is the highest scoring run, whether or not it won by enough.
	Best Ablation `json:"best"`

	// Tied is every threshold whose score is inside Sigma of Best, Best
	// included. It is the honest width of the answer, and a set where every
	// threshold is tied is a set that measured nothing.
	Tied []float64 `json:"tied"`

	// Ablations is the input, sorted by threshold, so the choice publishes the
	// measurement it came from rather than pointing at it.
	Ablations []Ablation `json:"ablations"`
}

// ErrNotMeasured is returned when a choice is asked for and no ablation was run.
var ErrNotMeasured = errors.New("mill: no ablation was run, so 0.71 is a default rather than a measurement")

// Choose picks the deduplication threshold from a set of ablation runs.
//
// It returns an error when the set cannot support any choice, which is a
// different thing from a set that supports keeping the default. The first is a
// measurement that has to be fixed and the second is a measurement that came
// back flat.
//
// The rule, in full:
//
// The best run wins only if it beats the run nearest the default by more than
// Sigma combined standard errors. Otherwise the default stands, because a run
// that cannot separate itself from the number already in use has not earned the
// change.
//
// Among the runs tied with the best, the one that keeps the most documents wins.
// A tie means the corpus does not care, and between two answers the corpus does
// not care about, the one that throws away less is right. Deduplicating harder
// than the evidence supports removes documents for reasons an ablation at
// ablation scale cannot see, and a low threshold folds together two reports of
// the same event that share a wire copy paragraph and nothing else.
func Choose(ablations []Ablation) (Choice, error) {
	if len(ablations) == 0 {
		return fallback(ErrNotMeasured.Error()), ErrNotMeasured
	}
	if problems := CheckAblations(ablations); len(problems) > 0 {
		return fallback(problems[0]), errors.New("mill: " + problems[0])
	}

	runs := slices.Clone(ablations)
	slices.SortFunc(runs, func(a, b Ablation) int {
		switch {
		case a.Threshold < b.Threshold:
			return -1
		case a.Threshold > b.Threshold:
			return 1
		}
		return 0
	})

	best := runs[0]
	for _, a := range runs[1:] {
		if a.Score > best.Score {
			best = a
		}
	}

	var tied []float64
	for _, a := range runs {
		if !beats(best, a) {
			tied = append(tied, a.Threshold)
		}
	}

	c := Choice{Best: best, Tied: tied, Ablations: runs}

	// A set where nothing separates from anything did not measure the threshold,
	// it measured the eval's noise floor. Saying so is the whole value of the
	// run, and quoting a winner out of it would throw that away.
	if len(tied) == len(runs) {
		c.Threshold = DefaultThreshold
		c.Why = fmt.Sprintf("every threshold from %.2f to %.2f scored within %.0f standard errors of every other one, so these runs did not separate them and %.2f stands as the default it always was",
			runs[0].Threshold, runs[len(runs)-1].Threshold, Sigma, DefaultThreshold)
		return c, nil
	}

	// The optimum has to be bracketed. A winner at the edge of what was measured
	// says the range was drawn in the wrong place, and the answer to that is
	// another run past the edge rather than the edge itself.
	if best.Threshold == runs[0].Threshold || best.Threshold == runs[len(runs)-1].Threshold {
		c.Threshold = DefaultThreshold
		c.Why = fmt.Sprintf("%.2f scored highest and it is the edge of the measured range, so the best threshold is somewhere past it and has not been run yet. Widen the range rather than take the edge",
			best.Threshold)
		return c, nil
	}

	baseline := nearest(runs, DefaultThreshold)
	if !beats(best, baseline) {
		c.Threshold = DefaultThreshold
		c.Why = fmt.Sprintf("%.2f scored highest at %.2f and the default's nearest run scored %.2f, which is inside %.0f standard errors, so the default stands",
			best.Threshold, best.Score, baseline.Score, Sigma)
		return c, nil
	}

	// Among the runs the evidence cannot tell apart, take the one that removes
	// the least.
	pick := best
	for _, a := range runs {
		if slices.Contains(tied, a.Threshold) && a.Retention > pick.Retention {
			pick = a
		}
	}
	c.Threshold = pick.Threshold
	c.Measured = true
	if pick.Threshold == best.Threshold {
		c.Why = fmt.Sprintf("%.2f scored %.2f against %.2f at the default's nearest run, which is more than %.0f standard errors, and nothing tied with it keeps more documents",
			pick.Threshold, pick.Score, baseline.Score, Sigma)
	} else {
		c.Why = fmt.Sprintf("%.2f scored highest and %.2f is inside %.0f standard errors of it while keeping %s of the corpus against %s, so the tie goes to the threshold that removes less",
			best.Threshold, pick.Threshold, Sigma, pct(pick.Retention), pct(best.Retention))
	}
	return c, nil
}

// CheckAblations says everything wrong with a set of runs, in the order a person
// would fix them.
//
// It is separate from [Choose] so a set can be checked while the runs are still
// going, rather than after the last one finishes and the answer is refused.
func CheckAblations(ablations []Ablation) []string {
	var out []string
	if len(ablations) < MinAblations {
		out = append(out, fmt.Sprintf("%d ablation runs cannot choose a threshold, since %d is the fewest that shows a shape rather than two numbers and their noise", len(ablations), MinAblations))
	}

	seen := map[float64]bool{}
	var low, high bool
	for _, a := range ablations {
		switch {
		case a.Threshold <= 0 || a.Threshold > 1:
			out = append(out, fmt.Sprintf("%v is not a similarity, which is above 0 and at most 1", a.Threshold))
		case seen[a.Threshold]:
			out = append(out, fmt.Sprintf("threshold %.2f was run twice, and two scores for one threshold is a repeat measurement rather than a second point", a.Threshold))
		}
		seen[a.Threshold] = true
		if a.Threshold <= DefaultThreshold {
			low = true
		}
		if a.Threshold >= DefaultThreshold {
			high = true
		}
		if a.Noise <= 0 {
			out = append(out, fmt.Sprintf("the run at %.2f quotes no standard error, and a score without one cannot be compared against another score", a.Threshold))
		}
		if a.Retention <= 0 || a.Retention > 1 {
			out = append(out, fmt.Sprintf("the run at %.2f retained %v of the corpus, which is not a share", a.Threshold, a.Retention))
		}
		if a.Box == "" {
			out = append(out, fmt.Sprintf("the run at %.2f does not say which box it ran on, so nobody can reproduce it", a.Threshold))
		}
	}
	if len(ablations) > 0 && (!low || !high) {
		out = append(out, fmt.Sprintf("no run sits on each side of %.2f, so this set cannot say whether the default is worth moving off", DefaultThreshold))
	}

	// One thing varies or nothing was compared.
	if len(ablations) == 0 {
		return out
	}
	for _, a := range ablations[1:] {
		if a.Tokens != ablations[0].Tokens {
			out = append(out, fmt.Sprintf("the run at %.2f trained on %d tokens and the run at %.2f trained on %d, so these two measured the token count as much as the threshold", a.Threshold, a.Tokens, ablations[0].Threshold, ablations[0].Tokens))
		}
		if a.Eval != ablations[0].Eval {
			out = append(out, fmt.Sprintf("the run at %.2f was scored on %q and the run at %.2f on %q, and two benchmarks do not compare", a.Threshold, a.Eval, ablations[0].Threshold, ablations[0].Eval))
		}
		if a.Box != "" && a.Box != ablations[0].Box {
			out = append(out, fmt.Sprintf("the run at %.2f was on %s and the run at %.2f on %s, which puts the hardware in the comparison alongside the threshold", a.Threshold, a.Box, ablations[0].Threshold, ablations[0].Box))
		}
	}
	return out
}

// beats says whether a is ahead of b by more than the noise on both of them
// together.
func beats(a, b Ablation) bool {
	return a.Score-b.Score > Sigma*math.Hypot(a.Noise, b.Noise)
}

// nearest is the run closest to a threshold, which is what the default is
// compared against when the default itself was not one of the runs.
func nearest(runs []Ablation, threshold float64) Ablation {
	best := runs[0]
	for _, a := range runs[1:] {
		if math.Abs(a.Threshold-threshold) < math.Abs(best.Threshold-threshold) {
			best = a
		}
	}
	return best
}

// fallback is what comes back when no choice can be made, which is the default
// with the reason attached rather than a zero.
func fallback(why string) Choice {
	return Choice{Threshold: DefaultThreshold, Why: why}
}

func pct(f float64) string { return fmt.Sprintf("%.1f%%", 100*f) }

// count is a token count the way a person says it out loud.
func count(n int64) string {
	if n >= 1_000_000_000 {
		return fmt.Sprintf("%gB", float64(n)/1e9)
	}
	return fmt.Sprintf("%d", n)
}

// String renders the choice the way it is published, which is with the runs
// under it, because a threshold quoted without the ablation behind it is a
// default again.
func (c Choice) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "threshold %.2f, %s\n%s\n", c.Threshold, measured(c.Measured), c.Why)
	if len(c.Ablations) == 0 {
		return b.String()
	}
	fmt.Fprintf(&b, "\n%-10s %10s %8s  %s\n", "threshold", "retention", "score", "tied")
	for _, a := range c.Ablations {
		tie := ""
		if slices.Contains(c.Tied, a.Threshold) {
			tie = "yes"
		}
		row := fmt.Sprintf("%-10.2f %10s %8.2f  %s", a.Threshold, pct(a.Retention), a.Score, tie)
		b.WriteString(strings.TrimRight(row, " ") + "\n")
	}
	fmt.Fprintf(&b, "\n%d runs of %s tokens each on %s, scored on %s, with the score plus or minus its own standard error.\n",
		len(c.Ablations), count(c.Ablations[0].Tokens), c.Ablations[0].Box, c.Ablations[0].Eval)
	return b.String()
}

func measured(b bool) string {
	if b {
		return "chosen from the ablation"
	}
	return "the default, which the ablation did not move"
}
