// Package can weighs the three continued pretraining arms against each other
// and says whether what came back is a comparison.
//
// Cân is to weigh, and it is the same word for a balance: two pans, one thing
// in each, and a reading that only means something if nothing else is in either
// pan. That is exactly the shape of this slice. The arms are locked in nau: gao,
// CulturaX, and CulturaX run through gao's cleaning, differing in the data and
// in nothing else. This package reads what the three runs actually did and
// checks that the promise held.
//
// It exists because the promise is easy to keep on paper and hard to keep on a
// cluster. An arm gets restarted from a checkpoint with a slightly different
// batch size. One arm finishes ten billion tokens short because the reservation
// ran out. Two arms are evaluated under a harness that gained a benchmark
// between them. None of these are dishonest and every one of them turns a four
// point gap into a number nobody can attribute. The comparison is worth more
// than any single arm, so the arms are held to each other rather than to a
// recipe alone: where nau fixes a value the runs are checked against it, and
// everywhere else the three are checked against one another, which catches the
// settings nobody thought to write down.
//
// The seed is one of those. With a single run per arm, a four point gate rests
// on one draw, and three arms on three seeds cannot tell a data effect from a
// seed effect at all. Sharing the seed does not make the comparison a study, but
// it removes the one difference that would be free to remove.
//
// The gate is not reported on an uncontrolled comparison. If the arms differ in
// something other than the data, this exits with the differences and does not
// print whether gao beat CulturaX, because a number produced by a comparison
// that was not controlled is worse than no number: it is the number everybody
// quotes afterwards. E6 is the row of this project most likely to be softened
// under sunk cost pressure, and the softening does not have to touch the
// threshold. It only has to touch the runs.
package can

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/tamnd/gao/nau"
)

// Metric is the benchmark the gate is written against.
const Metric = "vmlu"

// E6 is what gao has to beat CulturaX by, and E7 is what it has to beat the base
// model it continued from by. Both are in points of Metric. Beating CulturaX
// while barely moving off the base would mean the comparison found a bad
// baseline rather than a good corpus.
const (
	E6 = 4.0
	E7 = 6.0
)

// Captured is the share of gao's advantage the filters only arm has to take
// before the honest finding changes from "the corpus is better" to "the cleaning
// is better and the scale did not matter", which is P08-3.
const Captured = 0.5

// Adherence is the ceiling P10-2 predicts the continued pretraining model comes
// in under, before anything is done about it. It is reported rather than gated,
// since the fix for it is in S9.
const Adherence = 0.90

// MaxTokenGap is how far apart two arms may finish and still be the same run
// length. Two percent of a 200B token run is 4B tokens, which is far more than
// a scheduler hiccup and far less than a shortened arm.
const MaxTokenGap = 0.02

// MinSpike is the rise over the trailing median loss that counts as a spike, and
// Trailing is how many points that median is taken over. A run that spiked and
// was rolled back is fine. A run that spiked and carried on is a different run
// from the two it is being compared against.
const (
	MinSpike = 0.15
	Trailing = 8
)

// A Point is one reading off a training curve.
type Point struct {
	Step   int     `json:"step"`
	Tokens int64   `json:"tokens"`
	Loss   float64 `json:"loss"`
}

// A Run is one arm, as it came back.
type Run struct {
	// Arm is the model name, which has to be one of the three locked in nau,
	// and Data is what it trained on, which has to be that arm's data.
	Arm  string `json:"arm"`
	Data string `json:"data"`

	// Base is the checkpoint every arm continued from, by digest. Three arms
	// that continued from two checkpoints are two experiments.
	Base      string `json:"base"`
	Tokenizer string `json:"tokenizer"`

	Tokens    int64   `json:"tokens"`
	Batch     int64   `json:"batch"`
	Sequence  int     `json:"sequence"`
	PeakLR    float64 `json:"peak_lr"`
	Warmup    float64 `json:"warmup"`
	Decay     float64 `json:"decay"`
	Precision string  `json:"precision"`
	Seed      int     `json:"seed"`

	// Instance is the hardware the arm trained on and EvalBox is where it was
	// scored. The training happens on rented compute and the evaluation is
	// meant to happen on the fleet, so the two are separate fields and both are
	// published.
	Instance string `json:"instance"`
	EvalBox  string `json:"eval_box"`

	// Harness is the digest of the evaluation harness the scores came from,
	// which gao seal fixes before any of this runs.
	Harness string `json:"harness"`

	// Restarts is how many times the run was rolled back to a checkpoint.
	Restarts int `json:"restarts"`

	Curve []Point `json:"curve"`

	// Scores is the arm's result per benchmark and BaseScores is the base
	// model's, carried on every arm so that three arms compared against two
	// different baselines is a fault rather than a footnote.
	Scores     map[string]float64 `json:"scores"`
	BaseScores map[string]float64 `json:"base_scores"`
}

// Spikes returns the points where the loss rose past MinSpike over its trailing
// median. The curve is read in order and the first Trailing points are the
// window rather than candidates, since a run has no trailing median at step
// zero.
func (r Run) Spikes() []Point {
	var out []Point
	for i := Trailing; i < len(r.Curve); i++ {
		window := make([]float64, 0, Trailing)
		for _, p := range r.Curve[i-Trailing : i] {
			window = append(window, p.Loss)
		}
		sort.Float64s(window)
		median := window[len(window)/2]
		if r.Curve[i].Loss-median >= MinSpike {
			out = append(out, r.Curve[i])
		}
	}
	return out
}

// Score returns the arm's reading on a benchmark and whether it has one.
func (r Run) Score(metric string) (float64, bool) {
	v, ok := r.Scores[metric]
	return v, ok
}

// Blocking returns what is wrong with this arm on its own, before it is held
// against the others.
func (r Run) Blocking() []string {
	var why []string
	arm, ok := armByID(r.Arm)
	if !ok {
		return []string{fmt.Sprintf("%q is not one of the three arms the comparison was locked with", r.Arm)}
	}
	if r.Data != arm.Data {
		why = append(why, fmt.Sprintf("%s came back trained on %q and the arm is defined as %q", r.Arm, r.Data, arm.Data))
	}
	if r.Base == "" {
		why = append(why, fmt.Sprintf("%s does not name the checkpoint it continued from", r.Arm))
	}
	if r.Harness == "" {
		why = append(why, fmt.Sprintf("%s was scored under an unnamed harness, so nothing says the benchmarks were fixed before the run", r.Arm))
	}
	if r.Instance == "" {
		why = append(why, fmt.Sprintf("%s does not say what it trained on, and a curve with no hardware under it cannot be read against another arm's", r.Arm))
	}
	if _, ok := r.Score(Metric); !ok {
		why = append(why, fmt.Sprintf("%s carries no %s score, which is the metric the gate is written in", r.Arm, strings.ToUpper(Metric)))
	}
	if _, ok := r.BaseScores[Metric]; !ok {
		why = append(why, fmt.Sprintf("%s does not carry the base model's %s, so there is nothing to read E7 against", r.Arm, strings.ToUpper(Metric)))
	}
	if len(r.Curve) < Trailing+1 {
		why = append(why, fmt.Sprintf("%s came back with %s of training curve, which is not enough to see a spike in", r.Arm, count(len(r.Curve), "point")))
	}
	if spikes := r.Spikes(); len(spikes) > 0 && r.Restarts == 0 {
		why = append(why, fmt.Sprintf("%s spiked at step %d, to a loss of %.3f, and carries no restart, so it trained through something the other arms did not",
			r.Arm, spikes[0].Step, spikes[0].Loss))
	}
	return why
}

func armByID(id string) (nau.Arm, bool) {
	for _, a := range nau.Arms() {
		if a.ID == id {
			return a, true
		}
	}
	return nau.Arm{}, false
}

// A Comparison is the three arms together.
type Comparison struct {
	Name string
	Runs []Run
}

// Arm returns the run for an arm id.
func (c Comparison) Arm(id string) (Run, bool) {
	for _, r := range c.Runs {
		if r.Arm == id {
			return r, true
		}
	}
	return Run{}, false
}

// Missing names the locked arms that did not come back. Three matched arms that
// become two for budget reasons are not a controlled comparison, and the whole
// gate says so.
func (c Comparison) Missing() []string {
	var out []string
	for _, a := range nau.Arms() {
		if _, ok := c.Arm(a.ID); !ok {
			out = append(out, a.ID)
		}
	}
	return out
}

// Differences returns everything the arms do not share that they were meant to.
// It is the reason this package exists, and it is checked between the arms
// rather than against a written recipe wherever the recipe does not fix a value,
// because the settings that drift are the ones nobody wrote down.
func (c Comparison) Differences() []string {
	if len(c.Runs) < 2 {
		return nil
	}
	first := c.Runs[0]
	var why []string
	same := func(name string, get func(Run) string) {
		for _, r := range c.Runs[1:] {
			if got, want := get(r), get(first); got != want {
				why = append(why, fmt.Sprintf("%s ran at %s %s and %s ran at %s, so the arms differ in something other than their data",
					r.Arm, name, got, first.Arm, want))
			}
		}
	}
	same("the base checkpoint", func(r Run) string { return r.Base })
	same("tokenizer", func(r Run) string { return r.Tokenizer })
	// Batch is not checked between the arms, because the recipe fixes it below
	// and any arm that drifted is caught there. Sequence length and the rest are
	// not fixed anywhere, which is exactly why they are checked here.
	same("a sequence length of", func(r Run) string { return fmt.Sprintf("%d", r.Sequence) })
	same("a peak learning rate of", func(r Run) string { return fmt.Sprintf("%g", r.PeakLR) })
	same("a warmup of", func(r Run) string { return percent(r.Warmup) })
	same("a decay of", func(r Run) string { return percent(r.Decay) })
	same("precision", func(r Run) string { return r.Precision })
	same("seed", func(r Run) string { return fmt.Sprintf("%d", r.Seed) })
	same("harness", func(r Run) string { return short(r.Harness) })
	same("a base model scoring", func(r Run) string { return fmt.Sprintf("%.1f", r.BaseScores[Metric]) })

	for _, r := range c.Runs[1:] {
		if gap := share(r.Tokens, first.Tokens); gap > MaxTokenGap {
			why = append(why, fmt.Sprintf("%s consumed %s and %s consumed %s, which is %s apart against a %s line",
				r.Arm, billions(r.Tokens), first.Arm, billions(first.Tokens), percent(gap), percent(MaxTokenGap)))
		}
	}

	recipe := nau.Matched()
	for _, r := range c.Runs {
		if gap := share(r.Tokens, recipe.Tokens); gap > MaxTokenGap {
			why = append(why, fmt.Sprintf("%s consumed %s against the %s the recipe locks, which is %s short of the run it was meant to be",
				r.Arm, billions(r.Tokens), billions(recipe.Tokens), percent(gap)))
		}
		if r.Batch != recipe.Batch {
			why = append(why, fmt.Sprintf("%s ran a batch of %d tokens and the recipe locks %d", r.Arm, r.Batch, recipe.Batch))
		}
	}
	sort.Strings(why)
	return why
}

// Controlled reports whether the arms differ in their data and in nothing else.
func (c Comparison) Controlled() bool {
	return len(c.Missing()) == 0 && len(c.Differences()) == 0
}

// Blocking returns everything that stops this being read as a comparison.
func (c Comparison) Blocking() []string {
	if len(c.Runs) == 0 {
		return []string{"no arm came back, so there is nothing to weigh"}
	}
	var why []string
	seen := map[string]bool{}
	for _, r := range c.Runs {
		if seen[r.Arm] {
			why = append(why, fmt.Sprintf("%s came back twice and nothing says which run is the one", r.Arm))
		}
		seen[r.Arm] = true
		why = append(why, r.Blocking()...)
	}
	for _, id := range c.Missing() {
		why = append(why, fmt.Sprintf("%s did not come back, and three arms that become two are not a controlled comparison", id))
	}
	why = append(why, c.Differences()...)
	sort.Strings(why)
	return why
}

// Gap is what gao beat CulturaX by, in points of Metric.
func (c Comparison) Gap() float64 {
	gao, _ := c.Arm("com-8B-cpt-gao")
	base, _ := c.Arm("com-8B-cpt-culturax")
	return gao.Scores[Metric] - base.Scores[Metric]
}

// Lift is what gao beat the checkpoint it continued from by.
func (c Comparison) Lift() float64 {
	gao, _ := c.Arm("com-8B-cpt-gao")
	return gao.Scores[Metric] - gao.BaseScores[Metric]
}

// Cleaning is the share of gao's advantage the filters only arm took. It is
// negative when cleaning CulturaX made it worse, which is a real outcome and is
// printed rather than clamped.
func (c Comparison) Cleaning() float64 {
	filtered, _ := c.Arm("com-8B-cpt-culturax-filtered")
	plain, _ := c.Arm("com-8B-cpt-culturax")
	if c.Gap() == 0 {
		return 0
	}
	return (filtered.Scores[Metric] - plain.Scores[Metric]) / c.Gap()
}

// Holds reports whether the gate passed. It is only meaningful on a controlled
// comparison, which is why Verdict refuses to quote it otherwise.
func (c Comparison) Holds() bool {
	return c.Controlled() && c.Gap() >= E6 && c.Lift() >= E7
}

// Verdict is the comparison in one paragraph.
func (c Comparison) Verdict() string {
	if why := c.Blocking(); len(why) > 0 {
		return fmt.Sprintf("This is not a controlled comparison, so the gate is not reported against it. %s", why[0])
	}
	head := fmt.Sprintf("%s ran three arms that differ in their data and in nothing else, over %s each, from one checkpoint under one harness.",
		c.Name, billions(c.Runs[0].Tokens))
	switch {
	case c.Gap() < E6:
		return fmt.Sprintf("%s gao came in %s points ahead of CulturaX against the %.0f points E6 asks for, so the from scratch run does not start and the corpus, the ablations and this result get published as they are. The filters only arm took %s of the gap, which is where the diagnosis starts: a large share there means the finding is that cleaning matters and scale did not, and that is a more useful answer than the one this was hoping for.",
			head, points(c.Gap()), E6, percent(c.Cleaning()))
	case c.Lift() < E7:
		return fmt.Sprintf("%s gao beat CulturaX by %s points, which clears E6, and beat the checkpoint it continued from by only %s against the %.0f points E7 asks for. That combination says the comparison found a weak baseline rather than a strong corpus, and it is the one result here that looks like a pass and is not.",
			head, points(c.Gap()), points(c.Lift()), E7)
	case c.Cleaning() >= Captured:
		return fmt.Sprintf("%s gao beat CulturaX by %s points and its own base by %s, so E6 and E7 both pass. The filters only arm took %s of the gap, at or past the half P08-3 predicts, so the honest reading is that most of what gao bought was the cleaning rather than the scale, and the release notes say that rather than the headline.",
			head, points(c.Gap()), points(c.Lift()), percent(c.Cleaning()))
	}
	return fmt.Sprintf("%s gao beat CulturaX by %s points and its own base by %s, so E6 and E7 both pass and the from scratch run has a corpus that cleared its own gate. The filters only arm took %s of the gap, under the half P08-3 predicts, so the advantage is not explained by the cleaning alone.",
		head, points(c.Gap()), points(c.Lift()), percent(c.Cleaning()))
}

// ReadComparison reads one JSON object per arm. Unknown fields are refused
// rather than ignored, since a misspelled peak_lr would otherwise read as two
// arms that matched.
func ReadComparison(name, path string) (Comparison, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Comparison{}, err
	}
	c := Comparison{Name: name}
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var r Run
		if err := dec.Decode(&r); err != nil {
			return Comparison{}, fmt.Errorf("%s:%d: %w", path, n+1, err)
		}
		c.Runs = append(c.Runs, r)
	}
	return c, nil
}

func share(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return math.Abs(float64(a-b)) / float64(b)
}

func percent(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

func points(f float64) string { return fmt.Sprintf("%.1f", f) }

func billions(n int64) string { return fmt.Sprintf("%.1fB tokens", float64(n)/1e9) }

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
