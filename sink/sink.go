// Package sink checks an FP8 E4M3 training step for the values that sank.
//
// Chìm is to sink. E4M3 has four exponent bits and three mantissa bits, which
// puts its largest finite value at 448 and its smallest subnormal at 2^-9, a
// little under two thousandths. That is about eighteen binades of dynamic
// range, against roughly two hundred and fifty for BF16, and everything about
// training in FP8 follows from that one number. Weights fit comfortably.
// Activations mostly fit. Gradients late in a long run do not, because a
// gradient tensor's live values spread over more than eighteen binades and
// there is no scale factor that holds both ends of it at once.
//
// What makes this worth a package is that the failure is silent by
// construction. A value that falls under the subnormal floor becomes zero, zero
// is a legal number, the matrix multiply succeeds, the optimizer steps, and the
// loss curve keeps going down. It keeps going down because most of the signal
// is in the large values and they are all still there. A run can flush a fifth
// of one layer's gradient to zero for ten thousand steps and the only evidence
// is a model that is slightly worse than the BF16 run at the end, by which
// point the tensors nobody recorded are gone.
//
// So the check is not the loss curve. The loss curve agreeing with BF16 is the
// thing that convinces people the path is fine, and this package treats a
// tensor that lost values while the curve held as the worst case rather than
// the reassuring one. What is checked is the share of live values that landed
// on zero, the share that saturated at 448, whether the per-tensor scale came
// off an amax history long enough to describe the tensor it was applied to, and
// the cosine against the same tensor computed in BF16 on the same step. All
// four, because each one alone has a way of looking clean while the run is
// losing gradient.
//
// The dynamic range check is the one that says stop rather than retune. If a
// tensor needs more range than E4M3 has, then no scale exists that holds it,
// and the answer is that this tensor stays in BF16 rather than that somebody
// picks a better margin.
package sink

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
)

// MaxNormal is the largest finite value E4M3 represents, which is 1.75 times
// 2^8. Above it there is nothing to round to, so a value that lands here has
// been clipped rather than represented.
const MaxNormal = 448.0

// MinNormal is 2^-6, the smallest value E4M3 holds with the full three bits of
// mantissa.
const MinNormal = 0.015625

// MinSubnormal is 2^-9, the smallest value the format holds at all. Below it
// there is only zero, and this is the floor everything in this package is
// about.
const MinSubnormal = 0.001953125

// Range is how much dynamic range E4M3 has, top to floor. A tensor that needs
// more than this cannot be scaled into the format, and no choice of scale
// changes that.
const Range = MaxNormal / MinSubnormal

// Eps is the largest relative error round to nearest can put on a value that is
// inside the range, which is half an ulp on three mantissa bits. It is here so
// that a cosine below the floor can be read against what the format costs even
// when nothing underflowed.
const Eps = 0.0625

// Flushed is the most of a tensor's live values that may land on zero before
// the cast is losing gradient rather than losing precision. It is a tenth of a
// percent because gradients are long tailed and the tail is where the rare
// updates live.
const Flushed = 0.001

// Saturated is the most of a tensor that may clip at 448. It is stricter than
// the underflow line because saturation means the scale is set from an amax
// that no longer describes the tensor, which is a bug in the scaling rather
// than a property of the format.
const Saturated = 0.0001

// Aligned is the lowest cosine against the same tensor in BF16 that still
// counts as the same tensor. Three mantissa bits cost something, and this is
// the line between paying that and losing direction.
const Aligned = 0.999

// Margin is how much headroom under 448 a scale is expected to leave, so that
// the step after the one the amax was read on does not clip. A tensor scaled to
// sit exactly at the top is a tensor scaled for the past.
const Margin = 2.0

// History is the shortest amax window a delayed scale may come off. Below this
// the scale is the previous step's tensor with extra arithmetic, and delayed
// scaling exists precisely so that it is not.
const History = 16

// A Tensor is one tensor on one step, as the FP8 cast recorded it against a
// BF16 reference computed on the same step.
type Tensor struct {
	// Name is the tensor as the framework names it, which is what somebody
	// types when they go looking for it. A share of values lost is only
	// actionable if it says which layer lost them.
	Name string `json:"name"`

	// Kind is weight, activation, or gradient. It is recorded because the three
	// fail differently: weights are well conditioned, activations spike, and
	// gradients spread over more range than the format has.
	Kind string `json:"kind"`

	Step int `json:"step"`

	// Count is how many elements the tensor has, Zeros is how many landed on
	// zero after the cast, and Was is how many were already zero in the
	// reference. The share that matters is over live values, since a ReLU
	// output that is 60% zeros did not lose anything by being 60% zeros.
	Count int64 `json:"count"`
	Zeros int64 `json:"zeros"`
	Was   int64 `json:"was_zero"`

	// Clipped is how many elements landed on 448.
	Clipped int64 `json:"clipped"`

	// Amax and Amin are the largest and smallest nonzero magnitudes in the
	// reference tensor, before scaling. Their ratio is what the format is asked
	// to hold. Before matters: a tensor that lost values while its smallest one
	// still lands above the floor is a tensor whose amin was read after the
	// cast, which is the ordinary way this check gets instrumented wrong and
	// then reports that nothing sank.
	Amax float64 `json:"amax"`
	Amin float64 `json:"amin"`

	// Scale is the per-tensor factor applied before the cast, and Window is how
	// many steps of amax history it was computed from.
	Scale  float64 `json:"scale"`
	Window int     `json:"window"`

	// Cosine is this tensor against the same tensor in BF16 on this step. It is
	// the check that catches the case where the shares look fine, and it costs
	// one BF16 forward and backward on a step somebody chooses.
	Cosine float64 `json:"cosine"`

	// Box is the machine the step ran on, since a numeric result is a fact
	// about a kernel and a kernel is a fact about a card.
	Box string `json:"box"`
}

// Live is how many elements had something in them to lose.
func (t Tensor) Live() int64 { return t.Count - t.Was }

// Sank is how many live values the cast put on zero.
func (t Tensor) Sank() int64 { return t.Zeros - t.Was }

// Underflow is the share of live values that sank, which is the number this
// package exists to put in front of somebody.
func (t Tensor) Underflow() float64 {
	if t.Live() <= 0 {
		return 0
	}
	return float64(t.Sank()) / float64(t.Live())
}

// Saturation is the share of the tensor that clipped at the top.
func (t Tensor) Saturation() float64 {
	if t.Count <= 0 {
		return 0
	}
	return float64(t.Clipped) / float64(t.Count)
}

// Top is where the largest value lands once the scale is applied.
func (t Tensor) Top() float64 { return t.Amax * t.Scale }

// Floor is where the smallest live value lands. Under MinSubnormal it is zero,
// and the tensor is shorter than it was.
func (t Tensor) Floor() float64 { return t.Amin * t.Scale }

// Headroom is how many times over the scaled tensor would fit under 448. Under
// Margin the scale is set for the step the amax was read on rather than the
// step it is being used on.
func (t Tensor) Headroom() float64 {
	if t.Top() <= 0 {
		return 0
	}
	return MaxNormal / t.Top()
}

// Spread is the dynamic range the tensor actually needs, top to smallest live
// value. It does not depend on the scale, which is the point of it.
func (t Tensor) Spread() float64 {
	if t.Amin <= 0 {
		return 0
	}
	return t.Amax / t.Amin
}

// Fits reports whether any scale at all holds this tensor in E4M3. When it does
// not, the answer is that the tensor stays in BF16, not that somebody tunes the
// margin.
func (t Tensor) Fits() bool { return t.Spread() > 0 && t.Spread() <= Range }

// Wanted is the scale the tensor would have been given by amax scaling with the
// margin this package expects.
func (t Tensor) Wanted() float64 {
	if t.Amax <= 0 {
		return 0
	}
	return MaxNormal / (t.Amax * Margin)
}

// Silent reports whether this tensor lost live values while everything anybody
// watches stayed clean. It is the case the milestone item is named after: the
// cosine holds, the loss curve holds, and a share of the gradient is gone.
func (t Tensor) Silent() bool {
	return t.Underflow() > Flushed && t.Cosine >= Aligned && t.Saturation() <= Saturated
}

// Blocking is every reason this line cannot be read as a numerical check.
func (t Tensor) Blocking() []string {
	var why []string
	if t.Name == "" {
		why = append(why, "a tensor with no name cannot be gone and looked at, and a share of values lost is only actionable if it says which one lost them")
		return why
	}
	if t.Count <= 0 {
		why = append(why, fmt.Sprintf("%s reports no elements, and a share of nothing that underflowed is zero for the wrong reason", t.Name))
		return why
	}
	if t.Box == "" {
		why = append(why, fmt.Sprintf("%s does not say which box the step ran on, and what a cast does is a fact about a kernel", t.Name))
	}
	if t.Kind == "" {
		why = append(why, fmt.Sprintf("%s does not say whether it is a weight, an activation, or a gradient, and the three of them fail this differently", t.Name))
	}
	if t.Zeros < t.Was {
		why = append(why, fmt.Sprintf(
			"%s came back with %d zeros against %d in the reference, so the cast filled in values the reference did not have and the two tensors are not the same tensor",
			t.Name, t.Zeros, t.Was))
	}
	if t.Was >= t.Count {
		why = append(why, fmt.Sprintf("%s was already all zeros before the cast, so either the layer is dead or the reference is the wrong step", t.Name))
	}
	if t.Amax <= 0 {
		why = append(why, fmt.Sprintf("%s has an amax of zero, so either nothing flowed through it or nobody read it, and both are worth stopping the run for", t.Name))
	}
	if t.Amin <= 0 {
		why = append(why, fmt.Sprintf(
			"%s records no smallest live value, so there is nothing to say whether the floor of it landed above %g and the underflow share is a count without a cause",
			t.Name, MinSubnormal))
	} else if t.Amax > 0 && t.Amin > t.Amax {
		why = append(why, fmt.Sprintf("%s reports a smallest value larger than its largest, which is two tensors or a swapped pair of fields", t.Name))
	}
	if t.Amin > 0 && t.Scale > 0 && t.Sank() > 0 && t.Floor() >= MinSubnormal {
		why = append(why, fmt.Sprintf(
			"%s put %s of its live values on zero with its smallest one landing at %.2e, above the %g that rounds to zero, so the amin was read off the tensor after the cast rather than before it",
			t.Name, share(t.Underflow()), t.Floor(), MinSubnormal))
	}
	if t.Scale <= 0 {
		why = append(why, fmt.Sprintf(
			"%s was cast with no scale recorded, and unscaled E4M3 puts its floor at %g, so this is a cast rather than a training path",
			t.Name, MinSubnormal))
	}
	if t.Window < History {
		why = append(why, fmt.Sprintf(
			"%s took its scale off %s of amax history against a window of %d, and a delayed scale that short is the previous step's tensor with extra arithmetic",
			t.Name, plural(t.Window, "step"), History))
	}
	if t.Cosine <= 0 {
		why = append(why, fmt.Sprintf(
			"%s has no reference reading, and an FP8 tensor with nothing to be compared against is a number that cannot be wrong",
			t.Name))
	} else if t.Cosine > 1.0000001 {
		why = append(why, fmt.Sprintf("%s reports a cosine of %.4f against BF16, which is not a cosine", t.Name, t.Cosine))
	}
	return why
}

// A Step is every tensor recorded on one step, which is the unit this question
// can be answered in. One tensor is a spot check and the run has thousands.
type Step struct {
	Model string `json:"model"`

	// At is the training step these readings came off.
	At int `json:"at"`

	// Loss is the FP8 run's loss on this step and Reference is the BF16 run's
	// loss on the same step and the same batch. They are here to be printed
	// next to the underflow share rather than instead of it.
	Loss      float64 `json:"loss"`
	Reference float64 `json:"reference"`

	Tensors []Tensor `json:"tensors"`
}

// Ranked is the tensors worst first, by the share of live values they lost.
func (s Step) Ranked() []Tensor {
	out := slices.Clone(s.Tensors)
	slices.SortStableFunc(out, func(a, b Tensor) int {
		switch {
		case a.Underflow() > b.Underflow():
			return -1
		case a.Underflow() < b.Underflow():
			return 1
		default:
			return strings.Compare(a.Name, b.Name)
		}
	})
	return out
}

// Worst is the tensor that lost the most, and false if nothing was recorded.
func (s Step) Worst() (Tensor, bool) {
	if len(s.Tensors) == 0 {
		return Tensor{}, false
	}
	// Taken off the ranking rather than found again, so that a step where two
	// tensors lost the same share names the same one in the table and in the
	// verdict.
	return s.Ranked()[0], true
}

// Silent is every tensor that lost live values while the cosine held.
func (s Step) Silent() []Tensor {
	var out []Tensor
	for _, t := range s.Ranked() {
		if t.Silent() {
			out = append(out, t)
		}
	}
	return out
}

// Unfittable is every tensor that needs more dynamic range than E4M3 has. These
// are not a tuning problem.
func (s Step) Unfittable() []Tensor {
	var out []Tensor
	for _, t := range s.Ranked() {
		if t.Amin > 0 && !t.Fits() {
			out = append(out, t)
		}
	}
	return out
}

// Divergence is how far the FP8 loss is from the BF16 loss on the same batch.
// It is the number people watch, and this package prints it to show how little
// it moves while a tensor is being emptied.
func (s Step) Divergence() float64 { return math.Abs(s.Loss - s.Reference) }

// Blocking is every reason this step is not a numerical check.
func (s Step) Blocking() []string {
	if len(s.Tensors) == 0 {
		return []string{"no tensor was recorded, so there is nothing here about what the cast did"}
	}
	var why []string
	seen := map[string]bool{}
	boxes := map[string]bool{}
	steps := map[int]bool{}
	kinds := map[string]bool{}
	for _, t := range s.Tensors {
		if seen[t.Name] {
			why = append(why, fmt.Sprintf("%s appears twice, and two readings of one tensor are not two tensors", t.Name))
		}
		seen[t.Name] = true
		if t.Box != "" {
			boxes[t.Box] = true
		}
		if t.Kind != "" {
			kinds[t.Kind] = true
		}
		steps[t.Step] = true
		why = append(why, t.Blocking()...)
	}
	if len(boxes) > 1 {
		why = append(why, fmt.Sprintf(
			"the tensors were read on %s, and a cast is a fact about a kernel, so a step spread across boxes is measuring the boxes",
			join(boxes)))
	}
	if len(steps) > 1 {
		why = append(why, "the tensors come off different steps, and a gradient at step 400 and one at step 40,000 are two runs rather than one reading")
	}
	if !kinds["gradient"] {
		why = append(why, "no gradient was recorded, and weights and activations are the two that fit, so a check without gradients is a check of the easy half")
	}
	if s.Reference <= 0 {
		why = append(why, "no BF16 loss was recorded for this step, and the whole of this check is that the FP8 curve agrees while the tensors do not")
	}
	return why
}

// Settled reports whether the step is worth reading a decision off.
func (s Step) Settled() bool { return len(s.Blocking()) == 0 }

// Holds reports whether the FP8 path is safe to keep running: every tensor kept
// its live values, nothing clipped, nothing lost direction, and everything
// recorded fits in the format at all.
func (s Step) Holds() bool {
	if !s.Settled() {
		return false
	}
	if len(s.Unfittable()) > 0 {
		return false
	}
	for _, t := range s.Tensors {
		if t.Underflow() > Flushed || t.Saturation() > Saturated || t.Cosine < Aligned {
			return false
		}
	}
	return true
}

// Verdict is the step in one sentence, which quotes the tensor that lost the
// most rather than the loss curve.
func (s Step) Verdict() string {
	w, ok := s.Worst()
	if !ok {
		return "nothing was cast and nothing was read, so the FP8 path is where it started"
	}
	if why := s.Blocking(); len(why) > 0 {
		return why[0]
	}
	switch {
	case len(s.Unfittable()) > 0:
		t := s.Unfittable()[0]
		return fmt.Sprintf(
			"%s spreads over %.0f of dynamic range and E4M3 holds %.0f, so no scale exists that keeps both ends of it and this tensor stays in BF16",
			t.Name, t.Spread(), Range)
	case len(s.Silent()) > 0:
		t := s.Silent()[0]
		return fmt.Sprintf(
			"%s flushed %s of its live values to zero at step %d while its cosine held at %.4f and the loss stayed within %.4f of BF16, which is what silent means and why the curve is not the check",
			t.Name, share(t.Underflow()), s.At, t.Cosine, s.Divergence())
	case w.Underflow() > Flushed:
		return fmt.Sprintf(
			"%s flushed %s of its live values to zero at step %d and its cosine against BF16 fell to %.4f, so the FP8 path is losing gradient rather than precision",
			w.Name, share(w.Underflow()), s.At, w.Cosine)
	case worstSaturation(s) > Saturated:
		t := mostClipped(s)
		return fmt.Sprintf(
			"%s clipped %s of its elements at 448 with %.1fx of headroom against a margin of %.1fx, so the scale is set from an amax older than the tensor it was applied to",
			t.Name, share(t.Saturation()), t.Headroom(), Margin)
	case worstCosine(s) < Aligned:
		t := leastAligned(s)
		return fmt.Sprintf(
			"%s lost nothing to zero and came back at %.4f against BF16, under a floor of %.4f, so three mantissa bits are costing more than rounding on this tensor",
			t.Name, t.Cosine, Aligned)
	default:
		return fmt.Sprintf(
			"every tensor at step %d kept its live values inside E4M3, worst is %s at %s flushed against a %s line, with %.1fx of headroom under 448",
			s.At, w.Name, share(w.Underflow()), share(Flushed), w.Headroom())
	}
}

func worstSaturation(s Step) float64 { return mostClipped(s).Saturation() }

func mostClipped(s Step) Tensor {
	var out Tensor
	for _, t := range s.Tensors {
		if t.Saturation() > out.Saturation() {
			out = t
		}
	}
	return out
}

func worstCosine(s Step) float64 { return leastAligned(s).Cosine }

func leastAligned(s Step) Tensor {
	out := s.Tensors[0]
	for _, t := range s.Tensors[1:] {
		if t.Cosine < out.Cosine {
			out = t
		}
	}
	return out
}

// ReadStep loads a step from a file of one JSON tensor per line, which is what
// a training loop with the check turned on appends to.
func ReadStep(model string, loss, reference float64, path string) (Step, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Step{}, fmt.Errorf("chim: %w", err)
	}
	s := Step{Model: model, Loss: loss, Reference: reference}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var t Tensor
		if err := dec.Decode(&t); err != nil {
			return Step{}, fmt.Errorf("chim: %s line %d: %w", path, i+1, err)
		}
		s.Tensors = append(s.Tensors, t)
	}
	if len(s.Tensors) == 0 {
		return Step{}, fmt.Errorf("chim: %s holds no tensors", path)
	}
	s.At = s.Tensors[0].Step
	return s, nil
}

// share prints a fraction of a tensor. These run from a whole percent down to a
// few parts in a hundred thousand, so a fixed number of decimal places either
// rounds the small ones to nothing or pads the large ones with zeros.
func share(f float64) string {
	switch p := 100 * f; {
	case f <= 0:
		return "0%"
	case p >= 1:
		return fmt.Sprintf("%.1f%%", p)
	case p >= 0.01:
		return fmt.Sprintf("%.2f%%", p)
	default:
		return fmt.Sprintf("%.3f%%", p)
	}
}

func join(set map[string]bool) string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	if len(out) < 2 {
		return strings.Join(out, "")
	}
	return strings.Join(out[:len(out)-1], ", ") + " and " + out[len(out)-1]
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
