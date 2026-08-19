package efficiency

import (
	"math"
	"strings"
	"testing"
)

// The name is a claim about the parameter count, and a name that has drifted
// away from the architecture beside it is how a model ends up being described in
// a release note by a number nobody recomputed.
func TestTheNameIsAClaimAboutTheArithmetic(t *testing.T) {
	m := Com()
	if got := float64(m.Params()) / 1e9; got < 28 || got > 32 {
		t.Errorf("com-30B has %.2fB parameters", got)
	}
	if got := float64(m.Active()) / 1e9; got < 2.7 || got > 3.3 {
		t.Errorf("A3B activates %.2fB parameters per token", got)
	}
	if m.Active() >= m.Params() {
		t.Error("a mixture of experts model that activates all of itself is a dense model")
	}
}

// The embedding is a lookup rather than a multiply, and counting it as active is
// how a sparse model gets reported as more expensive than it is.
func TestTheEmbeddingIsNotArithmetic(t *testing.T) {
	m := Com()
	dense := m.Attention() + int64(m.Route+m.Shared)*m.Expert() + m.Router()
	want := m.Output() + int64(m.Layers)*dense
	if m.Active() != want {
		t.Errorf("active parameters are %d, want %d", m.Active(), want)
	}
	if m.Active() >= want+m.Embedding() {
		t.Error("the input table is being multiplied against")
	}
}

// At 4k the attention term is a correction and at 131k it is the bill, which is
// the entire reason the long context extension is a separate phase with its own
// utilization number.
func TestAttentionStopsBeingACorrection(t *testing.T) {
	m := Com()
	short, long := m.FLOPs(4096), m.FLOPs(131072)
	if long <= short {
		t.Fatalf("a longer sequence cost less per token: %.2fG against %.2fG", long/1e9, short/1e9)
	}
	matmul := 6 * float64(m.Active())
	if (short-matmul)/short > 0.25 {
		t.Errorf("attention is %.0f%% of a 4k token, which is not a correction", (short-matmul)/short*100)
	}
	if (long-matmul)/long < 0.4 {
		t.Errorf("attention is only %.0f%% of a 131k token", (long-matmul)/long*100)
	}
}

// A windowed layer averages the window once the sequence is past it, and the
// ramp at the start is most of the sequence during the 4k phase.
func TestAWindowedLayerCostsTheWindowAndNotTheSequence(t *testing.T) {
	if got := attended(4096, 4096); got != 2048.5 {
		t.Errorf("a full 4k layer attends %.1f positions, want 2048.5", got)
	}
	if got := attended(4096, 0); got != 2048.5 {
		t.Errorf("no window should be the same as a window the size of the sequence, got %.1f", got)
	}
	got, full := attended(131072, 4096), attended(131072, 0)
	if got >= full/8 {
		t.Errorf("a 4k window on a 131k sequence attends %.0f positions against %.0f unwindowed", got, full)
	}
	if got > 4096 {
		t.Errorf("a windowed layer attends %.0f positions, which is more than the window", got)
	}
}

// Three windowed layers for every global one, which is where most of the saving
// at long context comes from.
func TestTheInterleaveIsThreeToOne(t *testing.T) {
	m := Com()
	if m.Full() != 12 || m.Windowed() != 36 {
		t.Errorf("%d full and %d windowed layers out of %d", m.Full(), m.Windowed(), m.Layers)
	}
	flat := Com()
	flat.Interleave = 1
	if flat.FLOPs(131072) <= m.FLOPs(131072) {
		t.Error("interleaving sliding window attention did not make a long sequence cheaper")
	}
}

// This is the item the milestone asks for in as many words. Utilization without
// hardware is not a number, so it is not reported as one.
func TestAUtilizationFigureWithoutHardwareIsRefused(t *testing.T) {
	r := Reading{Model: Com(), GPUs: 64, Precision: FP8, Seq: 4096, Tokens: 1 << 22, Seconds: 4}
	if r.Ok() {
		t.Fatal("a reading with no instance on it was accepted")
	}
	if !strings.Contains(r.Blocking()[0], "not a number") {
		t.Errorf("the reason is not given: %v", r.Blocking())
	}
	if !strings.Contains(r.Verdict(), "not a utilization figure") {
		t.Errorf("it was reported anyway: %s", r.Verdict())
	}
	if r.MFU() != 0 {
		t.Errorf("a figure came back anyway: %.2f", r.MFU())
	}
}

// An A100 does not run FP8 slowly. It does not run it, and that is a fact worth
// discovering while the run is being planned.
func TestHardwareThatCannotDoThePrecisionSaysSo(t *testing.T) {
	a, ok := Lookup("a100-sxm")
	if !ok {
		t.Fatal("the A100 is not on the list, and it is on the list precisely to be ruled out")
	}
	r := Reading{Model: Com(), Instance: a, GPUs: 256, Precision: FP8, Seq: 4096, Tokens: 1 << 22, Seconds: 4}
	if r.Ok() {
		t.Fatal("an FP8 run was planned onto hardware with no FP8 in it")
	}
	if !strings.Contains(strings.Join(r.Blocking(), " "), "it does not run") {
		t.Errorf("the reason is too gentle: %v", r.Blocking())
	}
	if _, ok := Hours(Com(), a, FP8, 1e12, 4096, Gate); ok {
		t.Error("a price came back for a run that cannot happen")
	}
	if _, ok := a.Peak(BF16); !ok {
		t.Error("the A100 lost its BF16 as well")
	}
}

// Every reason comes back rather than the first, because the person reading this
// is deciding what to fix and fixing one thing to find the next is a slow way to
// spend an afternoon.
func TestEveryReasonComesBack(t *testing.T) {
	r := Reading{Model: Com(), Precision: FP8}
	if got := len(r.Blocking()); got < 4 {
		t.Errorf("%d reasons for a reading with nothing in it: %v", got, r.Blocking())
	}
}

func TestAReadingIsMeasuredAgainstTheHardwareItRanOn(t *testing.T) {
	h, _ := Lookup("h100-sxm")
	m := Com()

	// The tokens per second that would be exactly the gate on one accelerator.
	peak, _ := h.Peak(FP8)
	rate := Gate * peak / m.FLOPs(4096)
	r := Reading{Model: m, Instance: h, GPUs: 1, Precision: FP8, Seq: 4096, Tokens: int64(math.Ceil(rate * 60)), Seconds: 60}
	if got := r.MFU(); math.Abs(got-Gate) > 0.001 {
		t.Errorf("utilization came back as %.4f, want %.2f", got, Gate)
	}
	if !r.Passes() {
		t.Errorf("a reading exactly at the gate did not pass: %s", r.Verdict())
	}

	// The same tokens per second on a chip that is worth more of them is a worse
	// number, which is the whole content of reporting against the instance type.
	b, _ := Lookup("b200")
	on := r
	on.Instance = b
	if on.MFU() >= r.MFU() {
		t.Error("the same rate on faster hardware did not read as lower utilization")
	}
	if on.Passes() {
		t.Errorf("the same rate passed the gate on a B200: %s", on.Verdict())
	}
}

func TestTheVerdictSeparatesTuningFromChangingTheArchitecture(t *testing.T) {
	h, _ := Lookup("h100-sxm")
	m := Com()
	peak, _ := h.Peak(FP8)
	at := func(mfu float64) Reading {
		rate := mfu * peak / m.FLOPs(4096)
		return Reading{Model: m, Instance: h, GPUs: 1, Precision: FP8, Seq: 4096, Tokens: int64(math.Ceil(rate * 60)), Seconds: 60}
	}
	for _, tt := range []struct {
		mfu  float64
		want string
	}{
		{0.44, "clears the gate"},
		{0.33, "above the point where the architecture changes"},
		{0.18, "kill criterion rather than a tuning problem"},
	} {
		if got := at(tt.mfu).Verdict(); !strings.Contains(got, tt.want) {
			t.Errorf("at %.0f%%: %s", tt.mfu*100, got)
		}
	}
}

// The milestone says the model does not fit on the fleet and does not start
// until the compute exists and is booked. Both halves of that are arithmetic.
func TestTheComputeThisRunNeedsIsAPurchaseOrder(t *testing.T) {
	m, tokens := Com(), int64(1_000_000_000_000)
	h, _ := Lookup("h100-sxm")

	hours, ok := Hours(m, h, FP8, tokens, 4096, Gate)
	if !ok || hours < 5_000 || hours > 12_000 {
		t.Errorf("a trillion tokens at the gate costs %.0f accelerator hours", hours)
	}
	n, ok := GPUs(m, h, FP8, tokens, 4096, Gate, 30)
	if !ok || n < 5 || n > 30 {
		t.Errorf("%d accelerators to finish in 30 days", n)
	}

	// Utilization is what is being bought, so half of it is twice the hardware.
	half, _ := GPUs(m, h, FP8, tokens, 4096, Gate/2, 30)
	if half < 2*n-1 {
		t.Errorf("half the utilization needed %d accelerators against %d", half, n)
	}

	// And the fleet's one accelerator is not in this conversation.
	g, _ := Lookup("gamingpc")
	days, ok := Days(m, g, FP8, tokens, 4096, Gate, 1)
	if !ok || days < 365 {
		t.Errorf("the run finishes on one RTX 4090 in %.0f days", days)
	}
}

func TestSourcingComputeIsAnOrderedQuestion(t *testing.T) {
	got := Ranked(FP8)
	if len(got) != 4 {
		t.Fatalf("%d instances can do FP8, want 4", len(got))
	}
	if got[0].Name != "b200" {
		t.Errorf("the fastest FP8 accelerator is %s", got[0].Name)
	}
	for i := 1; i < len(got); i++ {
		a, _ := got[i-1].Peak(FP8)
		b, _ := got[i].Peak(FP8)
		if b > a {
			t.Errorf("%s came back before %s", got[i-1].Name, got[i].Name)
		}
	}
	for _, i := range Ranked(BF16) {
		if i.Why == "" {
			t.Errorf("%s is on the list with no reason to be", i.Name)
		}
	}
}

// The peaks are dense rather than the sparsity-doubled numbers on the marketing
// page, and dividing by the larger one is how a run reports half the utilization
// it is getting.
func TestThePeaksAreDense(t *testing.T) {
	for _, i := range Instances() {
		if fp8, ok := i.Peak(FP8); ok {
			if bf16, _ := i.Peak(BF16); fp8 < bf16 {
				t.Errorf("%s is slower in FP8 than in BF16", i.Name)
			}
		}
		if i.Memory <= 0 {
			t.Errorf("%s has no memory on it", i.Name)
		}
	}
	h, _ := Lookup("h100-sxm")
	if got, _ := h.Peak(FP8); got != 1979*tera {
		t.Errorf("the H100 is listed at %.0f TFLOP/s in FP8, want the dense figure of 1979", got/tera)
	}
	if _, ok := Lookup("h200"); ok {
		t.Error("a name nobody uses was accepted")
	}
}

// A step that took no time is a bug in the trainer's clock, and the answer to it
// is a refusal rather than an infinity.
func TestARateOverNoTimeIsNotARate(t *testing.T) {
	h, _ := Lookup("h100-sxm")
	r := Reading{Model: Com(), Instance: h, GPUs: 8, Precision: FP8, Seq: 4096, Tokens: 1 << 20}
	if r.Rate() != 0 || r.MFU() != 0 {
		t.Errorf("a step over no seconds produced a rate of %.2f", r.Rate())
	}
	if r.Ok() {
		t.Error("it was accepted")
	}
}
