package grade

// Turning a set of graded rollouts into the thing a policy gradient can use.

import (
	"fmt"
	"math"
	"strings"
)

// MinRollouts is the fewest checked rollouts a group can produce a baseline
// from.
//
// The whole point of grouping is that the group is its own baseline: no value
// network, just the mean of what the same prompt produced this time. A mean
// over two samples is not a baseline, it is one sample and its opposite, and
// the advantage it implies is noise with a sign.
const MinRollouts = 4

// MinDeviation is the spread under which a group is dropped rather than
// normalized.
//
// Dividing the centered reward by the group's standard deviation is what makes
// advantages comparable across prompts of different difficulty, and it is also
// the part that goes wrong quietly. A group where every rollout scored 0.500
// except one at 0.505 has a deviation near zero, and dividing by it turns a
// rounding difference into a full sized gradient. Those groups then dominate
// the batch, because they are the ones with the largest advantages, and the
// model is trained hardest on the prompts that told it the least.
const MinDeviation = 0.01

// A Rollout is one sampled answer and what the verifier said about it.
type Rollout struct {
	Answer  string  `json:"answer"`
	Verdict Verdict `json:"verdict"`

	// Advantage is the centered and scaled reward, and it is only meaningful
	// once the group is complete. It is zero for a rollout the verifier could
	// not check, which is the same as saying that rollout contributes nothing.
	Advantage float64 `json:"advantage"`
}

// A Group is one prompt, the rollouts sampled from it, and the baseline they
// form for each other.
type Group struct {
	Prompt     string `json:"prompt"`
	Specialist string `json:"specialist"`

	rollouts []Rollout
	settled  bool
}

// NewGroup starts a group for one prompt.
func NewGroup(specialist, prompt string) *Group {
	return &Group{Specialist: specialist, Prompt: prompt}
}

// Add records one graded rollout.
func (g *Group) Add(answer string, v Verdict) {
	g.rollouts = append(g.rollouts, Rollout{Answer: answer, Verdict: v})
	g.settled = false
}

// Sampled is how many rollouts the group holds, checked or not.
func (g *Group) Sampled() int { return len(g.rollouts) }

// Checked is how many of them the verifier managed to grade.
func (g *Group) Checked() int {
	n := 0
	for _, r := range g.rollouts {
		if r.Verdict.Checked {
			n++
		}
	}
	return n
}

// Dropped is how many were filtered out before the baseline was computed.
//
// This is the overlong filtering the plan calls for, and it is a count rather
// than a silent exclusion because a run where a third of the rollouts are being
// cut off is a run whose length limit is wrong, and the only place that shows
// up is here.
func (g *Group) Dropped() int { return g.Sampled() - g.Checked() }

// Mean is the average reward over the rollouts that were checked, which is the
// baseline. Rollouts the verifier could not look at are not in it, because
// averaging in a zero for an answer nobody graded lowers the bar for every
// other answer to the same prompt.
func (g *Group) Mean() float64 {
	n, sum := 0, 0.0
	for _, r := range g.rollouts {
		if r.Verdict.Checked {
			n++
			sum += r.Verdict.Reward
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// Deviation is the spread of the checked rewards around the mean, over the
// group rather than over a sample, because the group is the whole population
// the baseline is about.
func (g *Group) Deviation() float64 {
	mean := g.Mean()
	n, sum := 0, 0.0
	for _, r := range g.rollouts {
		if r.Verdict.Checked {
			n++
			d := r.Verdict.Reward - mean
			sum += d * d
		}
	}
	if n == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(n))
}

// Teaches reports whether this group is worth putting through a backward pass,
// and says why when it is not.
//
// This is the dynamic sampling filter. A group where every rollout got the same
// reward has an advantage of zero everywhere and produces no gradient at all,
// so it costs a full forward and backward pass to learn nothing. On a task the
// model has already solved, or one it cannot touch yet, that is most of the
// batch.
func (g *Group) Teaches() (bool, string) {
	switch {
	case g.Checked() < MinRollouts:
		return false, fmt.Sprintf("%d of %d rollouts could be checked, which is under the %d a baseline needs",
			g.Checked(), g.Sampled(), MinRollouts)
	case g.Deviation() == 0:
		return false, fmt.Sprintf("every rollout scored %.3f, so there is nothing here to prefer over anything else", g.Mean())
	case g.Deviation() < MinDeviation:
		return false, fmt.Sprintf("the rewards span %.4f around %.3f, which is under the %.2f floor, and scaling by a spread that small turns rounding into a gradient",
			g.Deviation(), g.Mean(), MinDeviation)
	}
	return true, ""
}

// Rollouts returns the rollouts with their advantages filled in.
//
// The advantage is the reward minus the group mean, over the group deviation.
// A rollout the verifier could not check gets zero, and a group that teaches
// nothing gets zero throughout, so a caller that ignores [Group.Teaches] wastes
// the pass rather than taking a step in a direction nobody measured.
func (g *Group) Rollouts() []Rollout {
	g.settle()
	out := make([]Rollout, len(g.rollouts))
	copy(out, g.rollouts)
	return out
}

func (g *Group) settle() {
	if g.settled {
		return
	}
	g.settled = true
	for i := range g.rollouts {
		g.rollouts[i].Advantage = 0
	}
	if ok, _ := g.Teaches(); !ok {
		return
	}
	mean, dev := g.Mean(), g.Deviation()
	for i, r := range g.rollouts {
		if r.Verdict.Checked {
			g.rollouts[i].Advantage = (r.Verdict.Reward - mean) / dev
		}
	}
}

// String renders the group the way it goes into a sample log.
func (g *Group) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d rollouts, %d checked, mean %.3f, spread %.3f\n",
		g.Specialist, g.Sampled(), g.Checked(), g.Mean(), g.Deviation())
	if ok, why := g.Teaches(); !ok {
		fmt.Fprintf(&b, "  dropped: %s\n", why)
	}
	for _, r := range g.Rollouts() {
		fmt.Fprintf(&b, "  %+.2f  %s\n", r.Advantage, r.Verdict)
	}
	return b.String()
}

// A Batch is what a step of training actually consumed, which is not the same
// as what it was given.
type Batch struct {
	Groups   int `json:"groups"`
	Kept     int `json:"kept"`
	Rollouts int `json:"rollouts"`
	Checked  int `json:"checked"`
}

// Add folds one group in.
func (b *Batch) Add(g *Group) {
	b.Groups++
	b.Rollouts += g.Sampled()
	b.Checked += g.Checked()
	if ok, _ := g.Teaches(); ok {
		b.Kept++
	}
}

// Yield is the share of groups that produced a gradient. It is the number that
// says what a step of this training run cost against what it bought.
func (b Batch) Yield() float64 {
	if b.Groups == 0 {
		return 0
	}
	return float64(b.Kept) / float64(b.Groups)
}

// Unchecked is the share of rollouts no verifier could grade. A run where this
// climbs is a run whose length limit is cutting answers off, and it is worth
// watching for its own sake rather than only as an input to the yield.
func (b Batch) Unchecked() float64 {
	if b.Rollouts == 0 {
		return 0
	}
	return float64(b.Rollouts-b.Checked) / float64(b.Rollouts)
}

// String renders the batch the way it goes into the training log.
func (b Batch) String() string {
	return fmt.Sprintf("%d groups, %d kept, which is %.0f%% yield. %d rollouts, %.0f%% of them unchecked",
		b.Groups, b.Kept, 100*b.Yield(), b.Rollouts, 100*b.Unchecked())
}
