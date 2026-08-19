// Package efficiency is model FLOPs utilization: what fraction of the hardware a
// training run actually turns into gradient.
//
// The gate on the from scratch run is 40% utilization in FP8 and the kill
// criterion is 25% after a week of tuning, so the number decides whether a
// several hundred thousand dollar run continues. Two things follow from that,
// and both of them are the reason this is a package rather than a spreadsheet.
//
// The first is that utilization without hardware is not a number. Forty percent
// of an H100 in FP8 and forty percent of a 4090 in FP8 differ by a factor of
// three in tokens per second and by more than that in money, and a run that
// reports 42% without saying what it ran on has reported nothing. So every
// reading here carries the instance type, and a step in a log that does not say
// what produced it is a fault rather than a row with a blank in it.
//
// The second is that a single measurement is not a measurement. A run that
// starts at 45% and finishes at 22% averages 34%, which is above the kill line
// and is a run that is dying. Utilization drifts as the sequence length
// extends, as the routing goes imbalanced, as a node degrades, and averaging
// over the whole run is exactly the operation that hides all three. So the run
// reader reports the distribution, the lowest sustained window, and the drift
// from the beginning to the end, and the verdict is written against the
// sustained figure rather than the mean.
//
// Nothing here trains anything or watches anything. It is the arithmetic that
// says what the hardware could do, and the reader that says what it did.
package efficiency

import (
	"fmt"
	"sort"
)

// Gate is the utilization the from scratch run has to sustain, from P08-6.
const Gate = 0.40

// Kill is the utilization below which the run is reconfigured or the
// architecture changes, after a week of tuning.
const Kill = 0.25

// A Precision is the numeric format the matmuls run in.
//
// It is part of every reading because peak throughput is a property of the pair
// rather than of the chip. The same H100 is worth 1979 TFLOP/s in FP8 and half
// that in BF16, and an A100 is worth nothing at all in FP8 because it has no
// hardware for it, which is a fact better discovered here than in week two.
type Precision string

const (
	FP8  Precision = "fp8"
	BF16 Precision = "bf16"
)

// A Model is the architecture, in the fields the FLOP count depends on.
//
// The rest of the design (QK normalization, z-loss, the optimizer split, the
// stability protocol) matters enormously to whether the run works and not at
// all to how much arithmetic it does, so it is not here.
type Model struct {
	Name string

	Layers  int
	Dim     int
	Vocab   int
	Heads   int
	KVHeads int
	HeadDim int

	// Experts is the routed expert count, Route is how many of them each token
	// goes to, and Shared is the expert every token goes to regardless.
	Experts   int
	Route     int
	Shared    int
	ExpertDim int

	// Window is the sliding attention window and Interleave is how often a full
	// attention layer appears in the stack. One in four means three windowed
	// layers for every global one.
	Window     int
	Interleave int

	// MTP is how many extra tokens the multi-token prediction head predicts.
	// Each one costs a module the size of a layer plus a pass through the output
	// projection, which is a real cost and is routinely left out of a FLOP count.
	MTP int
}

// Com is com-30B-A3B-base, the from scratch model.
func Com() Model {
	return Model{
		Name:       "com-30B-A3B-base",
		Layers:     48,
		Dim:        2048,
		Vocab:      192_000,
		Heads:      16,
		KVHeads:    2,
		HeadDim:    128,
		Experts:    128,
		Route:      8,
		Shared:     1,
		ExpertDim:  768,
		Window:     4096,
		Interleave: 4,
		MTP:        1,
	}
}

// Embedding is the input table, which is looked up rather than multiplied and
// so counts toward the parameters and not toward the arithmetic.
func (m Model) Embedding() int64 { return int64(m.Vocab) * int64(m.Dim) }

// Output is the projection back to the vocabulary. It is a separate matrix from
// the embedding, and it is the single largest matmul any token does.
func (m Model) Output() int64 { return int64(m.Vocab) * int64(m.Dim) }

// Attention is one layer's projections.
//
// Grouped query attention shrinks this and does not shrink the scores, which is
// the thing people expect it to do. Two key value heads instead of sixteen take
// seven eighths off the key and value projections and off the cache, and leave
// the quadratic term exactly where it was.
func (m Model) Attention() int64 {
	q := int64(m.Dim) * int64(m.Heads*m.HeadDim)
	kv := 2 * int64(m.Dim) * int64(m.KVHeads*m.HeadDim)
	o := int64(m.Heads*m.HeadDim) * int64(m.Dim)
	return q + kv + o
}

// Expert is one expert, which is three matrices because the feed forward is
// gated.
func (m Model) Expert() int64 { return 3 * int64(m.Dim) * int64(m.ExpertDim) }

// Router is the layer's gate, small enough to ignore and cheap enough to count.
func (m Model) Router() int64 { return int64(m.Dim) * int64(m.Experts) }

// Params is every parameter in the model, which is the number the name claims.
func (m Model) Params() int64 {
	perLayer := m.Attention() + int64(m.Experts+m.Shared)*m.Expert() + m.Router()
	return m.Embedding() + m.Output() + int64(m.Layers)*perLayer
}

// Active is the parameters one token is multiplied against, which is the number
// the compute is bought against and the second half of the name.
//
// The embedding is not in it. A token reads one row of that table and multiplies
// against none of it, and including it is how a mixture of experts model gets
// reported as more expensive than it is.
func (m Model) Active() int64 {
	perLayer := m.Attention() + int64(m.Route+m.Shared)*m.Expert() + m.Router()
	return m.Output() + int64(m.Layers)*perLayer
}

// Full is how many layers attend to the whole sequence.
func (m Model) Full() int {
	if m.Interleave <= 0 {
		return m.Layers
	}
	return m.Layers / m.Interleave
}

// Windowed is how many layers attend to a window.
func (m Model) Windowed() int { return m.Layers - m.Full() }

// attended is the mean number of positions a query attends to, which is not the
// sequence length and is not the window either.
//
// Causal masking means the first token attends to one position and the last to
// all of them, so a full attention layer averages half the sequence. A windowed
// layer averages the window once the sequence is past it, minus the ramp at the
// start, and that correction is worth carrying because the ramp is most of the
// sequence during the 4k phase and none of it during the 131k phase.
func attended(seq, window int) float64 {
	s := float64(seq)
	if window <= 0 || window >= seq {
		return (s + 1) / 2
	}
	w := float64(window)
	return (w*(w+1)/2 + (s-w)*w) / s
}

// FLOPs is the arithmetic one token of training costs, forward and backward.
//
// The matmul term is six times the active parameters: two FLOPs per parameter
// forward, four back. The attention term is separate because it does not scale
// with parameters at all, it scales with how far each query looks, and at 131k
// context it stops being a correction and starts being the bill.
func (m Model) FLOPs(seq int) float64 {
	matmul := 6 * float64(m.Active())

	// Four FLOPs per attended position per dimension forward, twice that back.
	perLayer := func(reach int) float64 { return 12 * float64(m.Dim) * attended(seq, reach) }
	attn := float64(m.Full())*perLayer(0) + float64(m.Windowed())*perLayer(m.Window)

	mtp := float64(m.MTP) * 6 * float64(m.Attention()+int64(m.Route+m.Shared)*m.Expert()+m.Output())

	return matmul + attn + mtp
}

// Run is the whole training run in FLOPs, which is the quantity compute is
// booked in.
func (m Model) Run(tokens int64, seq int) float64 { return m.FLOPs(seq) * float64(tokens) }

// An Instance is a machine somebody can rent or owns, and the peak arithmetic
// rate of one of its accelerators.
//
// The peaks are dense rather than the sparsity-doubled figures on the marketing
// page, because nothing here trains with structured sparsity and dividing by the
// larger number is how a run reports half the utilization it is getting.
type Instance struct {
	Name string
	GPU  string

	// FP8 and BF16 are dense tensor core peaks per accelerator, in FLOP/s. A zero
	// means the hardware cannot do that precision at all.
	FP8  float64
	BF16 float64

	// Memory is one accelerator's memory in bytes, which decides what fits before
	// utilization is a question anybody gets to ask.
	Memory int64

	Why string
}

const tera = 1e12

// Instances is the hardware this project has, could rent, or has to rule out.
func Instances() []Instance {
	return []Instance{
		{Name: "h100-sxm", GPU: "H100 SXM5", FP8: 1979 * tera, BF16: 989.5 * tera, Memory: 80 << 30,
			Why: "the default unit of rented training compute, and what the 40% gate was written against"},
		{Name: "h200-sxm", GPU: "H200 SXM5", FP8: 1979 * tera, BF16: 989.5 * tera, Memory: 141 << 30,
			Why: "the same arithmetic as an H100 with 76% more memory, which buys larger batches rather than more FLOPs"},
		{Name: "b200", GPU: "B200", FP8: 4500 * tera, BF16: 2250 * tera, Memory: 180 << 30,
			Why: "2.3 times an H100 per accelerator, at a price that has to be checked rather than assumed"},
		{Name: "a100-sxm", GPU: "A100 SXM4", FP8: 0, BF16: 312 * tera, Memory: 80 << 30,
			Why: "no FP8 hardware at all, which is here so that planning an FP8 run onto one fails as arithmetic"},
		{Name: "gamingpc", GPU: "RTX 4090", FP8: 660.6 * tera, BF16: 165.2 * tera, Memory: 24 << 30,
			Why: "the only accelerator this project owns, which is what the synthesis and evaluation work runs on"},
	}
}

// Lookup finds an instance by name.
func Lookup(name string) (Instance, bool) {
	for _, i := range Instances() {
		if i.Name == name {
			return i, true
		}
	}
	return Instance{}, false
}

// Peak is the dense arithmetic rate of one accelerator at this precision, and
// whether it has any.
func (i Instance) Peak(p Precision) (float64, bool) {
	var f float64
	switch p {
	case FP8:
		f = i.FP8
	case BF16:
		f = i.BF16
	}
	return f, f > 0
}

// A Reading is one utilization measurement, which is a rate against a peak.
//
// Every field is required. That is the point of it being a struct rather than a
// function of two floats: a utilization figure that has lost track of which
// hardware, how many of them, and at what precision is a figure nobody can
// reproduce or argue with, and losing track is the normal outcome of putting the
// number in a chat message.
type Reading struct {
	Model     Model
	Instance  Instance
	GPUs      int
	Precision Precision
	Seq       int

	// Tokens and Seconds are what was observed. They are kept as the two numbers
	// rather than as a rate so that a step that took no time is a fault rather
	// than an infinity.
	Tokens  int64
	Seconds float64
}

// Rate is tokens per second across the whole job.
func (r Reading) Rate() float64 {
	if r.Seconds <= 0 {
		return 0
	}
	return float64(r.Tokens) / r.Seconds
}

// Peak is what the hardware could do if every cycle went into the matmuls.
func (r Reading) Peak() float64 {
	f, ok := r.Instance.Peak(r.Precision)
	if !ok {
		return 0
	}
	return f * float64(r.GPUs)
}

// MFU is the fraction of the hardware the run turned into gradient.
func (r Reading) MFU() float64 {
	peak := r.Peak()
	if peak <= 0 {
		return 0
	}
	return r.Model.FLOPs(r.Seq) * r.Rate() / peak
}

// Blocking is every reason this reading cannot be reported as a utilization
// figure, in sentences, all of them rather than the first.
func (r Reading) Blocking() []string {
	var out []string
	if r.Instance.Name == "" {
		out = append(out, "the reading does not say what hardware it came off, and a utilization figure without hardware is not a number")
	}
	if r.GPUs <= 0 {
		out = append(out, "the reading does not say how many accelerators were involved, so there is nothing to divide by")
	}
	if _, ok := r.Instance.Peak(r.Precision); !ok && r.Instance.Name != "" {
		out = append(out, fmt.Sprintf("%s has no %s hardware, so a run planned at that precision does not run slowly, it does not run",
			r.Instance.GPU, r.Precision))
	}
	if r.Seconds <= 0 {
		out = append(out, "the reading covers no time, and a rate over no time is not a rate")
	}
	if r.Seq <= 0 {
		out = append(out, "the reading does not say the sequence length, and attention is most of the difference between the 4k phase and the 131k phase")
	}
	return out
}

// Ok reports whether the reading can be reported at all.
func (r Reading) Ok() bool { return len(r.Blocking()) == 0 }

// Passes reports whether the reading clears the gate.
func (r Reading) Passes() bool { return r.Ok() && r.MFU() >= Gate }

// Verdict is the reading in one sentence, against the gate and the hardware
// rather than against nothing.
func (r Reading) Verdict() string {
	if !r.Ok() {
		return "this is not a utilization figure: " + r.Blocking()[0]
	}
	switch mfu := r.MFU(); {
	case mfu >= Gate:
		return fmt.Sprintf("%s on %d %s at %s clears the gate at %.0f%% of peak", r.Model.Name, r.GPUs, r.Instance.GPU, r.Precision, mfu*100)
	case mfu >= Kill:
		return fmt.Sprintf("%s on %d %s at %s runs at %.0f%% of peak, under the %.0f%% gate and above the point where the architecture changes",
			r.Model.Name, r.GPUs, r.Instance.GPU, r.Precision, mfu*100, Gate*100)
	default:
		return fmt.Sprintf("%s on %d %s at %s runs at %.0f%% of peak, which is the kill criterion rather than a tuning problem",
			r.Model.Name, r.GPUs, r.Instance.GPU, r.Precision, mfu*100)
	}
}

// Hours is the accelerator hours a run of this many tokens costs at this
// utilization, which is the number compute is booked and paid in.
func Hours(m Model, i Instance, p Precision, tokens int64, seq int, mfu float64) (float64, bool) {
	peak, ok := i.Peak(p)
	if !ok || mfu <= 0 {
		return 0, false
	}
	return m.Run(tokens, seq) / (peak * mfu) / 3600, true
}

// GPUs is how many accelerators a run of this many tokens needs to finish in
// this many days, rounded up, since a fraction of a GPU is not something anybody
// rents.
func GPUs(m Model, i Instance, p Precision, tokens int64, seq int, mfu, days float64) (int, bool) {
	hours, ok := Hours(m, i, p, tokens, seq, mfu)
	if !ok || days <= 0 {
		return 0, false
	}
	n := int(hours/(days*24) + 0.999999)
	return max(n, 1), true
}

// Days is how long a run of this many tokens takes on this many accelerators.
func Days(m Model, i Instance, p Precision, tokens int64, seq int, mfu float64, gpus int) (float64, bool) {
	hours, ok := Hours(m, i, p, tokens, seq, mfu)
	if !ok || gpus <= 0 {
		return 0, false
	}
	return hours / float64(gpus) / 24, true
}

// Ranked is the instances that can do this precision, fastest first, which is
// the shape of the question when compute is being sourced rather than checked.
func Ranked(p Precision) []Instance {
	out := make([]Instance, 0, len(Instances()))
	for _, i := range Instances() {
		if _, ok := i.Peak(p); ok {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		x, _ := out[a].Peak(p)
		y, _ := out[b].Peak(p)
		return x > y
	})
	return out
}
