package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/chim"
)

func runChim(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("chim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	model := fs.String("model", "com-30B-A3B-base", "the model the step came off")
	loss := fs.Float64("loss", 0, "the FP8 run's loss on this step")
	ref := fs.Float64("bf16", 0, "the BF16 run's loss on the same step and the same batch")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao chim [-model name] [-loss x] [-bf16 x] [-json] step.jsonl

To sink: what an FP8 E4M3 step lost to zero.

E4M3 has four exponent bits and three mantissa bits, which puts its largest
finite value at 448 and its smallest subnormal a little under two thousandths.
That is about eighteen binades against BF16's two hundred and fifty, and
everything about training in this format follows from that one number. Weights
fit. Activations mostly fit. Gradients late in a long run spread over more range
than the format has, and no scale factor holds both ends of one at once.

The failure is silent by construction. A value under the floor becomes zero,
zero is a legal number, the matrix multiply succeeds, the optimizer steps, and
the loss curve keeps going down, because most of the signal is in the large
values and those are all still there. A run can empty a fifth of one layer's
gradient for ten thousand steps and the only evidence is a model that comes out
slightly worse than the BF16 run.

So the loss curve is not the check. It is printed here next to the share of
values that sank rather than instead of it, and a tensor that lost values while
the curve and the cosine both held is reported as the worst case rather than the
reassuring one. Four things are read: the share of live values that landed on
zero, the share that clipped at 448, whether the per-tensor scale came off an
amax window long enough to describe the tensor it was applied to, and the cosine
against the same tensor in BF16 on the same step.

A tensor that needs more dynamic range than E4M3 has is the one result that says
stop rather than retune, since there is no margin anybody can pick that holds
it. That tensor stays in BF16.

Exits 1 if the readings are not a numerical check, or 2 if the FP8 path lost
something.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	s, err := chim.ReadStep(*model, *loss, *ref, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao chim: %v\n", err)
		return 1
	}

	report := chimReport{
		Model: s.Model, Step: s.At, Tensors: len(s.Tensors),
		Loss: s.Loss, Reference: s.Reference, Divergence: s.Divergence(),
		Silent: len(s.Silent()), Unfittable: len(s.Unfittable()),
		Flushed: chim.Flushed, Saturated: chim.Saturated, Aligned: chim.Aligned,
		Holds: s.Holds(), Blocking: s.Blocking(), Verdict: s.Verdict(),
	}
	if w, ok := s.Worst(); ok {
		report.Worst = w.Name
		report.WorstUnderflow = w.Underflow()
	}
	for _, t := range s.Ranked() {
		report.Readings = append(report.Readings, chimReading{
			Name: t.Name, Kind: t.Kind, Count: t.Count, Live: t.Live(),
			Underflow: t.Underflow(), Saturation: t.Saturation(),
			Scale: t.Scale, Headroom: t.Headroom(), Floor: t.Floor(),
			Spread: t.Spread(), Fits: t.Fits(), Cosine: t.Cosine,
			Silent: t.Silent(),
		})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printChim(stdout, s)
	}
	if len(s.Blocking()) > 0 {
		return 1
	}
	if !s.Holds() {
		return 2
	}
	return 0
}

// chimReading is one tensor as the report carries it, which is shares and where
// the two ends of it landed rather than the counts underneath.
type chimReading struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
	Live  int64  `json:"live"`

	Underflow  float64 `json:"underflow"`
	Saturation float64 `json:"saturation"`

	Scale    float64 `json:"scale"`
	Headroom float64 `json:"headroom"`
	Floor    float64 `json:"floor"`

	// Spread is the dynamic range the tensor needs and Fits says whether any
	// scale at all holds it, which is the difference between retuning and
	// leaving this tensor in BF16.
	Spread float64 `json:"spread"`
	Fits   bool    `json:"fits"`

	Cosine float64 `json:"cosine"`

	// Silent is a tensor that lost live values while everything anybody watches
	// stayed clean, which is the case this command is named for.
	Silent bool `json:"silent"`
}

type chimReport struct {
	Model   string `json:"model"`
	Step    int    `json:"step"`
	Tensors int    `json:"tensors"`

	// Readings is worst first, by the share of live values lost.
	Readings []chimReading `json:"readings"`

	// Loss and Reference are the two curves, and Divergence is how far apart
	// they are. They are here to be read next to the underflow share, since the
	// point of the check is how little this moves while a tensor is emptied.
	Loss       float64 `json:"loss"`
	Reference  float64 `json:"reference"`
	Divergence float64 `json:"divergence"`

	Silent     int `json:"silent"`
	Unfittable int `json:"unfittable"`

	Worst          string  `json:"worst"`
	WorstUnderflow float64 `json:"worst_underflow"`

	Flushed   float64 `json:"flushed_line"`
	Saturated float64 `json:"saturated_line"`
	Aligned   float64 `json:"aligned_line"`

	Holds bool `json:"holds"`

	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printChim(w io.Writer, s chim.Step) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "tensor\tkind\tlive\tflushed\tclipped\tscale\tfloor\thead\tcosine\trange\n")
	for _, t := range s.Ranked() {
		fits := "fits"
		if t.Amin > 0 && !t.Fits() {
			fits = "too wide"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%g\t%.2e\t%.1fx\t%.4f\t%s\n",
			t.Name, t.Kind, elementCount(t.Live()), chimShare(t.Underflow()), chimShare(t.Saturation()),
			t.Scale, t.Floor(), t.Headroom(), t.Cosine, fits)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s at step %d of %s, on %s.\n",
		plural(len(s.Tensors), "tensor"), s.At, s.Model, box(s))
	fmt.Fprintf(w, "E4M3 tops out at %g and its floor is %g, so a value under %.2e times the scale is a zero.\n",
		chim.MaxNormal, chim.MinSubnormal, chim.MinSubnormal)
	fmt.Fprintf(w, "The lines are %s of live values flushed, %s clipped, and %.3f against the same tensor in BF16.\n",
		chimShare(chim.Flushed), chimShare(chim.Saturated), chim.Aligned)
	if s.Reference > 0 {
		fmt.Fprintf(w, "The FP8 loss is %.4f against BF16's %.4f on the same batch, which is %.4f apart and is not the check.\n",
			s.Loss, s.Reference, s.Divergence())
	}

	if why := s.Blocking(); len(why) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(why), "fault"))
		for _, one := range why {
			fmt.Fprintf(w, "  %s\n", one)
		}
		return
	}
	fmt.Fprintf(w, "\n%s.\n", s.Verdict())
}

// box is the machine the step ran on, which every reading has to agree about
// before the report prints at all.
func box(s chim.Step) string {
	for _, t := range s.Tensors {
		if t.Box != "" {
			return t.Box
		}
	}
	return "a box nobody recorded"
}

// chimShare prints a fraction of a tensor. These run from a whole percent down
// to a few parts in a hundred thousand, so a fixed width either rounds the small
// ones away or pads the large ones.
func chimShare(f float64) string {
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

// count prints an element count the way somebody reads a tensor shape, since
// sixteen million and seven hundred thousand are the same number of digits at a
// glance and not the same tensor.
func elementCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}
