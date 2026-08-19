package sink

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// elements is one tensor's worth of a 30B mixture of experts model, which is
// large enough that a tenth of a percent is still sixteen thousand values.
const elements int64 = 16_777_216

// tensor is one reading that recorded everything the item asks for and lost
// nothing, scaled the way amax scaling with the expected margin would scale it.
func tensor(name, kind string) Tensor {
	return Tensor{
		Name: name, Kind: kind, Step: 42_000,
		Count: elements, Amax: 0.5, Amin: 1e-5,
		Scale: 448, Window: 32, Cosine: 0.9997, Box: "gamingpc",
	}
}

// step is a forward, a backward, and the weights they came off.
func step() Step {
	return Step{
		Model: "com-30B-A3B-base", At: 42_000, Loss: 2.3141, Reference: 2.3139,
		Tensors: []Tensor{
			tensor("blocks.12.mlp.experts.7.down_proj.weight", "weight"),
			tensor("blocks.12.attn.out_proj.act", "activation"),
			tensor("blocks.12.mlp.experts.7.down_proj.grad", "gradient"),
		},
	}
}

func refuses(t *testing.T, s Step, want string) {
	t.Helper()
	for _, why := range s.Blocking() {
		if strings.Contains(why, want) {
			return
		}
	}
	t.Errorf("nothing blocking mentions %q, got:\n  %s", want, strings.Join(s.Blocking(), "\n  "))
}

func TestAStepThatKeptItsValuesSaysWhatItChecked(t *testing.T) {
	s := step()
	if !s.Settled() {
		t.Fatalf("a clean step was refused: %v", s.Blocking())
	}
	if !s.Holds() {
		t.Fatalf("a clean step did not hold: %s", s.Verdict())
	}
	g := s.Tensors[2]
	if got := g.Headroom(); got < 1.9 || got > 2.1 {
		t.Errorf("half a unit scaled by 448 left %.2fx of headroom under 448", got)
	}
	if got := g.Floor(); got <= MinSubnormal {
		t.Errorf("the smallest live value landed at %g, under the %g floor", got, MinSubnormal)
	}
	if !g.Fits() {
		t.Errorf("a tensor spreading over %.0f did not fit in %.0f", g.Spread(), Range)
	}
	for _, want := range []string{"step 42000", "kept its live values inside E4M3", "0.10% line", "2.0x of headroom"} {
		if !strings.Contains(s.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, s.Verdict())
		}
	}
}

// This is the whole of the item. The curve agrees, the cosine agrees, and a
// twentieth of one gradient is gone.
func TestTheLossCurveAgreeingIsNotTheCheck(t *testing.T) {
	s := step()
	s.Tensors[2].Zeros = elements / 20
	s.Tensors[2].Amin = 3.4e-6
	s.Tensors[2].Cosine = 0.9995

	if !s.Settled() {
		t.Fatalf("the step was refused on something else: %v", s.Blocking())
	}
	if s.Divergence() > 0.001 {
		t.Fatalf("the two curves are %.4f apart, which is not the silent case", s.Divergence())
	}
	if s.Holds() {
		t.Fatal("a step that emptied a twentieth of a gradient held the FP8 path")
	}
	silent := s.Silent()
	if len(silent) != 1 || !strings.HasSuffix(silent[0].Name, ".grad") {
		t.Fatalf("the silent tensors came back as %v", silent)
	}
	if got := silent[0].Underflow(); got < 0.049 || got > 0.051 {
		t.Errorf("a twentieth of the live values read as %.4f", got)
	}
	for _, want := range []string{"flushed 5.0% of its live values", "cosine held at 0.9995", "why the curve is not the check"} {
		if !strings.Contains(s.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, s.Verdict())
		}
	}

	// And a tensor that lost the same share and also lost direction is the loud
	// case, which is the one that gets caught anyway.
	loud := step()
	loud.Tensors[2].Zeros = elements / 20
	loud.Tensors[2].Amin = 3.4e-6
	loud.Tensors[2].Cosine = 0.94
	if len(loud.Silent()) != 0 {
		t.Error("a tensor whose cosine collapsed was called silent")
	}
	if !strings.Contains(loud.Verdict(), "losing gradient rather than precision") {
		t.Errorf("the loud case reads %q", loud.Verdict())
	}
}

// A share of live values is over the live ones. An activation that is mostly
// zeros before the cast did not lose anything by being mostly zeros after it.
func TestValuesThatWereAlreadyZeroAreNotLosses(t *testing.T) {
	s := step()
	s.Tensors[1].Was = elements * 3 / 5
	s.Tensors[1].Zeros = elements * 3 / 5

	if u := s.Tensors[1].Underflow(); u != 0 {
		t.Errorf("a ReLU output read as %.4f underflowed", u)
	}
	if !s.Holds() {
		t.Errorf("a sparse activation broke the step: %s", s.Verdict())
	}

	filled := step()
	filled.Tensors[1].Was = elements / 2
	refuses(t, filled, "the cast filled in values the reference did not have")
}

// The dynamic range check is the one that says stop rather than retune, since
// no choice of scale holds a tensor wider than the format.
func TestATensorWiderThanTheFormatStaysInBF16(t *testing.T) {
	s := step()
	s.Tensors[2].Amin = 1e-9

	if s.Tensors[2].Fits() {
		t.Fatalf("a tensor spreading over %.0f fit in %.0f", s.Tensors[2].Spread(), Range)
	}
	if s.Holds() {
		t.Fatal("a tensor with no scale that holds it held the FP8 path")
	}
	if got := len(s.Unfittable()); got != 1 {
		t.Fatalf("%d tensors came back unfittable", got)
	}
	for _, want := range []string{"no scale exists that keeps both ends of it", "stays in BF16"} {
		if !strings.Contains(s.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, s.Verdict())
		}
	}
}

// Clipping is not a property of the format, it is a scale computed off a tensor
// that has since moved.
func TestClippingIsAScaleSetFromAnOlderTensor(t *testing.T) {
	s := step()
	s.Tensors[2].Clipped = elements / 100
	s.Tensors[2].Scale = 896

	if s.Holds() {
		t.Fatal("a step that clipped a percent of a gradient held")
	}
	if len(s.Silent()) != 0 {
		t.Error("a tensor that clipped a percent of itself was called silent")
	}
	for _, want := range []string{"clipped 1.00% of its elements at 448", "an amax older than the tensor"} {
		if !strings.Contains(s.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, s.Verdict())
		}
	}

	// Three mantissa bits cost something, and a tensor that lost direction
	// without losing a single value is that cost rather than an underflow.
	rounded := step()
	rounded.Tensors[0].Cosine = 0.982
	if rounded.Holds() {
		t.Fatal("a tensor at 0.982 against BF16 held")
	}
	if !strings.Contains(rounded.Verdict(), "costing more than rounding on this tensor") {
		t.Errorf("the rounding case reads %q", rounded.Verdict())
	}
}

func TestAReadingNobodyCanActOnIsRefused(t *testing.T) {
	short := step()
	short.Tensors[2].Window = 4
	refuses(t, short, "the previous step's tensor with extra arithmetic")

	unscaled := step()
	unscaled.Tensors[1].Scale = 0
	refuses(t, unscaled, "a cast rather than a training path")

	blind := step()
	blind.Tensors[1].Cosine = 0
	refuses(t, blind, "a number that cannot be wrong")

	impossible := step()
	impossible.Tensors[1].Cosine = 1.4
	refuses(t, impossible, "which is not a cosine")

	nameless := step()
	nameless.Tensors[0].Name = ""
	refuses(t, nameless, "cannot be gone and looked at")

	dead := step()
	dead.Tensors[2].Amax = 0
	refuses(t, dead, "worth stopping the run for")

	nofloor := step()
	nofloor.Tensors[2].Amin = 0
	refuses(t, nofloor, "a count without a cause")

	swapped := step()
	swapped.Tensors[2].Amin = 4
	refuses(t, swapped, "a swapped pair of fields")

	nokind := step()
	nokind.Tensors[0].Kind = ""
	refuses(t, nokind, "the three of them fail this differently")
}

// A step is one step on one box, and the checks that make it one are the same
// checks every table in this repo carries.
func TestAStepIsOneStepOnOneBox(t *testing.T) {
	boxes := step()
	boxes.Tensors[1].Box = "server3"
	refuses(t, boxes, "a step spread across boxes is measuring the boxes")

	steps := step()
	steps.Tensors[1].Step = 400
	refuses(t, steps, "two runs rather than one reading")

	twice := step()
	twice.Tensors = append(twice.Tensors, twice.Tensors[0])
	refuses(t, twice, "two readings of one tensor are not two tensors")

	easy := step()
	easy.Tensors = easy.Tensors[:2]
	refuses(t, easy, "a check of the easy half")

	nocurve := step()
	nocurve.Reference = 0
	refuses(t, nocurve, "the FP8 curve agrees while the tensors do not")

	empty := Step{Model: "com-30B-A3B-base"}
	if empty.Settled() || empty.Holds() {
		t.Error("an empty step settled the FP8 path")
	}
	if _, ok := empty.Worst(); ok {
		t.Error("an empty step has a worst tensor")
	}
	if !strings.Contains(empty.Verdict(), "where it started") {
		t.Errorf("the verdict on nothing reads %q", empty.Verdict())
	}

	blank := step()
	blank.Tensors[1].Count = 0
	refuses(t, blank, "zero for the wrong reason")

	allzero := step()
	allzero.Tensors[1].Was = elements
	allzero.Tensors[1].Zeros = elements
	refuses(t, allzero, "either the layer is dead or the reference is the wrong step")
}

func TestAStepIsReadFromWhatTheTrainingLoopAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "step.jsonl")
	body := `{"name":"blocks.0.mlp.down_proj.weight","kind":"weight","step":42000,"count":16777216,"amax":0.5,"amin":0.00001,"scale":448,"window":32,"cosine":0.9997,"box":"gamingpc"}

{"name":"blocks.0.mlp.down_proj.grad","kind":"gradient","step":42000,"count":16777216,"zeros":838860,"amax":0.5,"amin":0.00001,"scale":448,"window":32,"cosine":0.9995,"box":"gamingpc"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadStep("com-30B-A3B-base", 2.3141, 2.3139, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tensors) != 2 || s.At != 42_000 {
		t.Fatalf("read %d tensors at step %d", len(s.Tensors), s.At)
	}
	if w, _ := s.Worst(); !strings.HasSuffix(w.Name, ".grad") {
		t.Errorf("the worst tensor came back as %s", w.Name)
	}
	if len(s.Silent()) != 1 {
		t.Errorf("the silent tensors came back as %v", s.Silent())
	}

	// A column nobody declared is the training loop and the reader disagreeing
	// about what was written down.
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"name":"blocks.0.mlp.down_proj.grad","underflow":0.05}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStep("com-30B-A3B-base", 2.3, 2.3, bad); err == nil {
		t.Error("a line with a column nobody declared was read")
	}

	blank := filepath.Join(dir, "blank.jsonl")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStep("com-30B-A3B-base", 2.3, 2.3, blank); err == nil {
		t.Error("an empty file was read as a step")
	}
	if _, err := ReadStep("com-30B-A3B-base", 2.3, 2.3, filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a step that is not there was read")
	}
}

// The format's own numbers are the ones every threshold here is derived from,
// so they are checked rather than trusted.
func TestTheFormatIsWhatTheDocumentationSaysItIs(t *testing.T) {
	if MaxNormal != 1.75*256 {
		t.Errorf("E4M3 tops out at %g", MaxNormal)
	}
	if MinNormal != 1.0/64 || MinSubnormal != 1.0/512 {
		t.Errorf("E4M3 bottoms out at %g normal and %g subnormal", MinNormal, MinSubnormal)
	}
	if got := Range; got < 229_000 || got > 230_000 {
		t.Errorf("E4M3 holds %.0f of dynamic range", got)
	}
	if t2 := tensor("g", "gradient"); t2.Wanted() != 448 {
		t.Errorf("amax scaling with a %.1fx margin wanted %g", Margin, t2.Wanted())
	}
	for _, c := range []struct {
		f    float64
		want string
	}{{0, "0%"}, {0.05, "5.0%"}, {0.001, "0.10%"}, {0.00002, "0.002%"}} {
		if got := share(c.f); got != c.want {
			t.Errorf("%g printed as %s", c.f, got)
		}
	}
}
