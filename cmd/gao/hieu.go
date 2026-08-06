package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/hieu"
	"github.com/tamnd/gao/nau"
)

func runHieu(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		hieuUsage(stderr)
		return 2
	}
	switch args[0] {
	case "model":
		return runHieuModel(stdout, stderr, args[1:])
	case "plan":
		return runHieuPlan(stdout, stderr, args[1:])
	case "read":
		return runHieuRead(stdout, stderr, args[1:])
	case "help":
		hieuUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao hieu: no subcommand named %s\n\n", args[0])
		hieuUsage(stderr)
		return 2
	}
}

func hieuUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao hieu model [-json]
       gao hieu plan  [-instance NAME] [-precision fp8|bf16] [-gpus N] [-mfu F] [-seq N] [-json]
       gao hieu read  steps.jsonl [-windows N] [-json]

The effect: what fraction of the hardware a training run turns into gradient.

The gate on the from scratch run is 40% model FLOPs utilization in FP8 and the
kill criterion is 25% after a week of tuning, so the number decides whether a
run continues. Two things follow.

Utilization without hardware is not a number. Forty percent of an H100 and forty
percent of a 4090 differ by a factor of three in tokens per second, so every
reading here carries the instance type and a step that does not say what it ran
on is a fault rather than a row with a blank in it.

A single measurement is not a measurement either. A run that starts at 45% and
ends at 22% averages 34%, which is above the line the architecture would be
changed at and is a run that is dying. read cuts the log into windows, reports
the worst one, and writes the verdict against that rather than against the mean.

flags:
`)
}

func runHieuModel(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("hieu model", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() { hieuUsage(stderr); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	m := hieu.Com()
	phases := []int{4096, 32768, 131072}

	if *asJSON {
		report := hieuModelReport{
			Name: m.Name, Params: m.Params(), Active: m.Active(),
			Layers: m.Layers, Full: m.Full(), Windowed: m.Windowed(), Window: m.Window,
		}
		for _, seq := range phases {
			report.Phases = append(report.Phases, hieuPhase{
				Seq: seq, FLOPs: m.FLOPs(seq),
				AttentionShare: 1 - 6*float64(m.Active())/m.FLOPs(seq),
			})
		}
		return printJSON(stdout, stderr, report)
	}

	fmt.Fprintf(stdout, "%s\n", m.Name)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "params\t%s, of which %s are multiplied against per token\n", billions(m.Params()), billions(m.Active()))
	fmt.Fprintf(tw, "layers\t%d, %d attending to the whole sequence and %d to a %d window\n", m.Layers, m.Full(), m.Windowed(), m.Window)
	fmt.Fprintf(tw, "experts\t%d routed, %d per token, and %d shared by every token\n", m.Experts, m.Route, m.Shared)
	fmt.Fprintf(tw, "attention\t%d query heads and %d key value heads of %d\n", m.Heads, m.KVHeads, m.HeadDim)
	fmt.Fprintf(tw, "predict\t%d extra token per position, which costs a module the size of a layer\n", m.MTP)
	_ = tw.Flush()

	fmt.Fprint(stdout, "\n")
	tw = tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "sequence\tper token\tof which attention\n")
	for _, seq := range phases {
		f := m.FLOPs(seq)
		fmt.Fprintf(tw, "%s\t%.1f GFLOPs\t%.0f%%\n", seqName(seq), f/1e9, (1-6*float64(m.Active())/f)*100)
	}
	_ = tw.Flush()

	fmt.Fprint(stdout, "\nAttention is a quarter of a 4k token and most of a 128k one, which is why the long context extension is a phase with its own utilization number rather than a line on the end of the run.\n")
	return 0
}

type hieuPhase struct {
	Seq            int     `json:"seq"`
	FLOPs          float64 `json:"flops_per_token"`
	AttentionShare float64 `json:"attention_share"`
}

type hieuModelReport struct {
	Name     string      `json:"name"`
	Params   int64       `json:"params"`
	Active   int64       `json:"active_params"`
	Layers   int         `json:"layers"`
	Full     int         `json:"full_attention_layers"`
	Windowed int         `json:"windowed_layers"`
	Window   int         `json:"window"`
	Phases   []hieuPhase `json:"phases"`
}

func runHieuPlan(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("hieu plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	name := fs.String("instance", "h100-sxm", "the accelerator the run is booked on")
	precision := fs.String("precision", string(hieu.FP8), "the numeric format the matmuls run in")
	gpus := fs.Int("gpus", 64, "how many accelerators")
	mfu := fs.Float64("mfu", hieu.Gate, "the utilization the booking assumes")
	tokens := fs.Int64("tokens", nau.Instances(), "token instances in the run")
	seq := fs.Int("seq", 4096, "sequence length")
	fs.Usage = func() { hieuUsage(stderr); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	i, ok := hieu.Lookup(*name)
	if !ok {
		fmt.Fprintf(stderr, "gao hieu: %s is not hardware this project has priced, and the ones it has are %s\n", *name, named())
		return 2
	}
	p := hieu.Precision(*precision)
	m := hieu.Com()

	hours, ok := hieu.Hours(m, i, p, *tokens, *seq, *mfu)
	if !ok {
		fmt.Fprintf(stderr, "gao hieu: %s has no %s hardware, so a run planned at that precision does not run slowly, it does not run\n", i.GPU, p)
		return 1
	}
	days, _ := hieu.Days(m, i, p, *tokens, *seq, *mfu, *gpus)
	peak, _ := i.Peak(p)

	report := hieuPlanReport{
		Model: m.Name, Instance: i.Name, GPU: i.GPU, Precision: string(p),
		GPUs: *gpus, MFU: *mfu, Tokens: *tokens, Seq: *seq,
		FLOPsPerToken: m.FLOPs(*seq), RunFLOPs: m.Run(*tokens, *seq),
		Hours: hours, Days: days,
	}
	if fleet, ok := hieu.Lookup("gamingpc"); ok {
		if alone, ok := hieu.Days(m, fleet, p, *tokens, *seq, *mfu, 1); ok {
			report.FleetDays = alone
		}
	}

	if *asJSON {
		return printJSON(stdout, stderr, report)
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "model\t%s, %s parameters and %s active per token\n", m.Name, billions(m.Params()), billions(m.Active()))
	fmt.Fprintf(tw, "tokens\t%s instances at a sequence length of %s\n", trillions(*tokens), seqName(*seq))
	fmt.Fprintf(tw, "cost\t%.1f GFLOPs per token, %.2e for the run\n", m.FLOPs(*seq)/1e9, m.Run(*tokens, *seq))
	fmt.Fprintf(tw, "hardware\t%d %s at %s, %.0f dense TFLOP/s each\n", *gpus, i.GPU, p, peak/1e12)
	fmt.Fprintf(tw, "assumes\t%.0f%% utilization, and the gate is %.0f%%\n", *mfu*100, hieu.Gate*100)
	fmt.Fprintf(tw, "compute\t%.0f accelerator hours, which is %.1f days on %d of them\n", hours, days, *gpus)
	if report.FleetDays > 0 {
		fmt.Fprintf(tw, "fleet\t%.0f days on the one RTX 4090 this project owns\n", report.FleetDays)
	}
	_ = tw.Flush()

	fmt.Fprintf(stdout, "\nUtilization is what is being bought here. At %.0f%% this run costs %.0f accelerator hours, and at half of that it costs twice as many, which is the same run and twice the invoice.\n",
		*mfu*100, hours)
	return 0
}

type hieuPlanReport struct {
	Model     string  `json:"model"`
	Instance  string  `json:"instance"`
	GPU       string  `json:"gpu"`
	Precision string  `json:"precision"`
	GPUs      int     `json:"gpus"`
	MFU       float64 `json:"mfu"`
	Tokens    int64   `json:"tokens"`
	Seq       int     `json:"seq"`

	FLOPsPerToken float64 `json:"flops_per_token"`
	RunFLOPs      float64 `json:"run_flops"`
	Hours         float64 `json:"accelerator_hours"`
	Days          float64 `json:"days"`

	// FleetDays is the same run on the single accelerator this project owns,
	// which is the number behind the checklist item saying this slice does not
	// start until the compute exists and is booked.
	FleetDays float64 `json:"fleet_days,omitempty"`
}

func runHieuRead(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("hieu read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	windows := fs.Int("windows", hieu.WindowCount, "how many pieces the run is cut into")
	fs.Usage = func() { hieuUsage(stderr); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	steps, err := hieu.ReadLog(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao hieu: %v\n", err)
		return 1
	}
	r := hieu.Read(hieu.Com(), steps)

	report := hieuReadReport{
		Model: r.Model.Name, Steps: len(r.Steps), Tokens: r.Tokens(), Seconds: r.Seconds(),
		Mean: r.Mean(), Median: r.Median(), Worst: r.Quantile(0),
		Sustained: r.Sustained(*windows), Drift: r.Drift(*windows), Windows: r.Windows(*windows),
		Gate: hieu.Gate, Passes: r.Passes(), Faults: r.Faults, Verdict: r.Verdict(),
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printHieuRead(stdout, r, *windows)
	}
	if r.Passes() {
		return 0
	}
	return 1
}

type hieuReadReport struct {
	Model   string  `json:"model"`
	Steps   int     `json:"steps"`
	Tokens  int64   `json:"tokens"`
	Seconds float64 `json:"seconds"`

	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	Worst  float64 `json:"worst_step"`

	// Sustained is the lowest window and is the figure the gate is read against,
	// since sustaining 40% is the claim rather than touching it once.
	Sustained float64   `json:"sustained"`
	Drift     float64   `json:"drift"`
	Windows   []float64 `json:"windows,omitempty"`

	Gate    float64  `json:"gate"`
	Passes  bool     `json:"passes"`
	Faults  []string `json:"faults,omitempty"`
	Verdict string   `json:"verdict"`
}

func printHieuRead(w io.Writer, r hieu.Run, windows int) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "steps\t%d over %s of step time\n", len(r.Steps), span(time.Duration(r.Seconds()*float64(time.Second))))
	fmt.Fprintf(tw, "tokens\t%s instances\n", trillions(r.Tokens()))
	fmt.Fprintf(tw, "mean\t%.0f%% of peak, which is the number to distrust\n", r.Mean()*100)
	fmt.Fprintf(tw, "median\t%.0f%%\n", r.Median()*100)
	fmt.Fprintf(tw, "worst\t%.0f%% sustained over a tenth of the run, against a %.0f%% gate\n", r.Sustained(windows)*100, hieu.Gate*100)
	fmt.Fprintf(tw, "drift\t%+.0f points from the first tenth of the run to the last\n", r.Drift(windows)*100)
	_ = tw.Flush()

	if got := r.Windows(windows); len(got) > 0 {
		var b strings.Builder
		for _, f := range got {
			fmt.Fprintf(&b, " %.0f", f*100)
		}
		fmt.Fprintf(w, "\nwindows %s\n", strings.TrimSpace(b.String()))
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict())
	if len(r.Faults) > 1 {
		for _, fault := range r.Faults[1:] {
			fmt.Fprintf(w, "  and %s\n", fault)
		}
	}
}

// named is the hardware this project has priced, for the error that says a name
// is not one of them.
func named() string {
	out := make([]string, 0, len(hieu.Instances()))
	for _, i := range hieu.Instances() {
		out = append(out, i.Name)
	}
	return strings.Join(out, ", ")
}

// billions and trillions write a count the way the plan is discussed, which is
// never in digits.
func billions(n int64) string { return fmt.Sprintf("%.1fB", float64(n)/1e9) }

func trillions(n int64) string {
	if n < 1e12 {
		return fmt.Sprintf("%.0fB", float64(n)/1e9)
	}
	return fmt.Sprintf("%.2f T", float64(n)/1e12)
}

// seqName writes a sequence length the way the curriculum names it.
func seqName(seq int) string {
	if seq >= 1024 && seq%1024 == 0 {
		return fmt.Sprintf("%dk", seq/1024)
	}
	return fmt.Sprintf("%d", seq)
}
