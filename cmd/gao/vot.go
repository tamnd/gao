package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/vot"
)

func runVot(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("spike", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	run := fs.String("run", "", "the run this log came off")
	total := fs.Int("total", 0, "how many steps the run is, which is what a rewind is priced against")
	every := fs.Int("checkpoint", 0, "how often the run takes a checkpoint, in steps")
	top := fs.Int("top", 10, "how many spikes to print")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao spike -run name -total n -checkpoint n [-top n] [-json] log.jsonl

Read a training log back and say whether it spiked, what the response would have
cost, and whether the log could have held the answer at all.

The response to a loss spike is to rewind to the last checkpoint before it, skip
the span of data that produced it, and resume lower. Every part of that is a
decision somebody makes at three in the morning while the run is burning money,
which is the worst possible time to decide what counts as a spike. So it is
decided in the package, in numbers, and this runs them.

A step is a spike when it sits a tenth over the trailing median and six times the
run's own scatter at once. One test alone is wrong in a different direction on
each kind of run: the first fires on every third decimal of a flat curve, the
second on every step of a noisy one. A spike that comes back inside the band on
its own is a blip. One that does not is a run that has been writing into the
weights off a curve that already left.

The checkpoint cadence is priced here rather than treated as an operational
detail, because it is what a rewind costs. A run that checkpoints every four
hours has agreed to throw away up to four hours per spike, and that is a fault
about the cadence rather than a discovery made after the second one.

This does not say why the loss spiked. A bad batch, an overflow in a low
precision cast, a rate that was always too high, and a corrupt shard draw the
same shape, and telling them apart needs the gradient norm, the data span, and
somebody looking. So a log with no gradient norm beside the loss is read and then
said to be a log that cannot answer the next question.

Exits 1 when the log cannot be read against the protocol, and 2 when it is read
and is not the run it looks like.

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
	if *top < 0 {
		fmt.Fprintf(stderr, "gao spike: a table cannot hold %d rows\n", *top)
		return 2
	}

	steps, err := vot.ReadSteps(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao spike: %v\n", err)
		return 1
	}

	c := vot.ReadCurve(*run, *total, *every, steps)
	report := votReport{
		Curve:    c,
		Window:   vot.Window,
		Rise:     vot.Rise,
		Scatter:  vot.Scatter,
		Faults:   c.Faults(),
		Blocking: c.Blocking(),
		Holds:    c.Holds(),
		Verdict:  c.Verdict(),
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printVot(stdout, report, *top)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

type votReport struct {
	vot.Curve

	// The band is on the report because a spike nobody can recompute is a spike
	// somebody argues with at three in the morning.
	Window  int     `json:"window"`
	Rise    float64 `json:"rise"`
	Scatter float64 `json:"scatter"`

	Faults   []string `json:"faults,omitempty"`
	Blocking []string `json:"blocking,omitempty"`
	Holds    bool     `json:"holds"`
	Verdict  string   `json:"verdict"`
}

func printVot(w io.Writer, r votReport, top int) {
	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "This log cannot be read against the protocol:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	fmt.Fprintf(w, "%s, %s from step %s to step %s, every %s.\n",
		r.Run, plural(r.Rows, "row"), thousands(int64(r.First)), thousands(int64(r.Last)), plural(r.Every, "step"))
	fmt.Fprintf(w, "median loss %.4f, scatter %.4f, band %.0f%% over the trailing %s and %.1f times the scatter, checkpoint every %s.\n",
		r.Median, r.MAD, r.Rise*100, plural(r.Window, "row"), r.Scatter, plural(r.Checkpoint, "step"))

	if len(r.Spikes) > 0 {
		shown := min(top, len(r.Spikes))
		fmt.Fprintf(w, "\n%s over the band%s:\n", plural(len(r.Spikes), "spike"), only(shown, len(r.Spikes)))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprint(tw, "step\tloss\tband\tover\tgrad\trows out\tcame back\trewind\n")
		for i, s := range r.Spikes {
			if i >= top {
				break
			}
			fmt.Fprintf(tw, "%s\t%.4f\t%.4f\t%s\t%.3f\t%d\t%s\t%s\n",
				thousands(int64(s.Step)), s.Loss, s.Band, percent(s.Over), s.Grad, s.Rows,
				back(s), plural(s.Rewind, "step"))
		}
		_ = tw.Flush()
	}

	fmt.Fprintf(w, "\nrewinding to the checkpoint before each costs %s, %s of a %s step run.\n",
		plural(r.Rewind, "step"), percent(r.Cost), thousands(int64(r.Total)))

	if len(r.Faults) > 0 {
		fmt.Fprint(w, "\nThis is not the run it looks like:\n")
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// only says how much of the table is printed, and says nothing at all when the
// table is all of it.
func only(shown, all int) string {
	if shown == all {
		return ""
	}
	return fmt.Sprintf(", the first %d", shown)
}

// back is the step a spike came back at, or the word for the one that never did,
// which is the column somebody reads first.
func back(s vot.Spike) string {
	if !s.Recovered() {
		return "never"
	}
	return thousands(int64(s.Back))
}
