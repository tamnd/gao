package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/tamnd/gao/can"
	"github.com/tamnd/gao/may"
)

func runCan(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("weigh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	name := fs.String("name", "com-8B-cpt", "what the comparison is called")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao weigh [-name comparison] [-json] arms.jsonl

Weigh the three continued pretraining arms against each other.

One JSON object per arm: what it trained on, the checkpoint it continued from,
the recipe it ran under, its training curve, the harness it was scored by, and
its scores next to the base model's.

The arms were locked to differ in their data and in nothing else. That promise
is easy to keep on paper and hard to keep on a cluster, where an arm gets
resumed at a different batch size, or finishes short because a reservation ran
out, or is scored under a harness that gained a benchmark between arms. None of
those are dishonest and every one of them turns a four point gap into a number
nobody can attribute.

So the arms are held to the locked recipe where it fixes a value and to each
other everywhere else, which is what catches the settings nobody wrote down. If
they differ in anything but the data, the gate is not reported: a number off a
comparison that was not controlled is worse than no number, because it is the
number that gets quoted afterwards.

Exits 1 when this is not a comparison, and 2 when it is one that does not clear
E6 and E7.

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

	c, err := can.ReadComparison(*name, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao weigh: %v\n", err)
		return 1
	}

	blocking := c.Blocking()
	for _, r := range c.Runs {
		if r.EvalBox == "" {
			blocking = append(blocking, fmt.Sprintf("%s does not say where it was scored, and the evaluation is the part of this slice that runs on hardware we own", r.Arm))
			continue
		}
		if _, ok := may.Lookup(r.EvalBox); !ok {
			blocking = append(blocking, fmt.Sprintf("%s was scored on %q, which is not a box in the fleet, so the numbers that decide the gate came off hardware somebody else controls", r.Arm, r.EvalBox))
		}
	}
	sort.Strings(blocking)

	report := canReport{
		Name:        c.Name,
		Metric:      can.Metric,
		E6:          can.E6,
		E7:          can.E7,
		Captured:    can.Captured,
		Controlled:  c.Controlled(),
		Missing:     c.Missing(),
		Differences: c.Differences(),
		Holds:       c.Holds(),
		Blocking:    blocking,
		Verdict:     c.Verdict(),
	}
	// The three readings are carried only when the comparison earned them. A gap
	// off arms that differed in something other than their data is the number
	// that gets quoted afterwards, so it is not published at all.
	if len(blocking) == 0 {
		gap, lift, cleaning := c.Gap(), c.Lift(), c.Cleaning()
		report.Gap, report.Lift, report.Cleaning = &gap, &lift, &cleaning
	}
	for _, r := range c.Runs {
		row := canArm{
			Arm: r.Arm, Data: r.Data, Tokens: r.Tokens,
			Instance: r.Instance, EvalBox: r.EvalBox,
			Restarts: r.Restarts, Spikes: len(r.Spikes()),
		}
		if len(r.Curve) > 0 {
			row.Loss = r.Curve[len(r.Curve)-1].Loss
		}
		row.Score = r.Scores[can.Metric]
		row.Base = r.BaseScores[can.Metric]
		if v, ok := r.Scores["vi-adherence"]; ok {
			row.Adherence = v
		}
		report.Arms = append(report.Arms, row)
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printCan(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

type canArm struct {
	Arm      string `json:"arm"`
	Data     string `json:"data"`
	Tokens   int64  `json:"tokens"`
	Instance string `json:"instance"`
	EvalBox  string `json:"eval_box"`

	Loss     float64 `json:"final_loss"`
	Restarts int     `json:"restarts"`
	Spikes   int     `json:"spikes"`

	Score float64 `json:"score"`
	Base  float64 `json:"base_score"`

	// Adherence is P10-2's number, reported rather than gated, since what is
	// done about it belongs to post-training.
	Adherence float64 `json:"adherence,omitempty"`
}

type canReport struct {
	Name   string `json:"name"`
	Metric string `json:"metric"`

	Arms []canArm `json:"arms"`

	// Gap and Lift are absent on an uncontrolled comparison, since a reading off
	// arms that differed in more than their data is worse than no reading.
	Gap  *float64 `json:"gap,omitempty"`
	E6   float64  `json:"e6"`
	Lift *float64 `json:"lift,omitempty"`
	E7   float64  `json:"e7"`

	// Cleaning is the share of the gap the filters only arm took, which is the
	// number that decides what the result is a finding about.
	Cleaning *float64 `json:"cleaning,omitempty"`
	Captured float64  `json:"captured_line"`

	Controlled  bool     `json:"controlled"`
	Missing     []string `json:"missing,omitempty"`
	Differences []string `json:"differences,omitempty"`

	Holds    bool     `json:"holds"`
	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printCan(w io.Writer, r canReport) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "arm\tdata\ttokens\tfinal loss\tspikes\trestarts\t%s\tover base\ttrained on\tscored on\n", r.Metric)
	for _, a := range r.Arms {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%.3f\t%d\t%d\t%.1f\t%+.1f\t%s\t%s\n",
			a.Arm, a.Data, billions(a.Tokens), a.Loss, a.Spikes, a.Restarts,
			a.Score, a.Score-a.Base, a.Instance, a.EvalBox)
	}
	_ = tw.Flush()

	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "\nThis is not a controlled comparison, so the gate is not reported against it:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	fmt.Fprint(w, "\n")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "E6, gao over CulturaX\t%+.1f\tagainst %.1f\t%s\n", *r.Gap, r.E6, yesno(*r.Gap >= r.E6))
	fmt.Fprintf(tw, "E7, gao over its own base\t%+.1f\tagainst %.1f\t%s\n", *r.Lift, r.E7, yesno(*r.Lift >= r.E7))
	// P08-3 is a prediction rather than a gate, so the last column says which
	// side of the line it came down on instead of whether something passed.
	took := "under"
	if *r.Cleaning >= r.Captured {
		took = "at or past"
	}
	fmt.Fprintf(tw, "P08-3, the cleaning's share\t%s\tagainst %s\t%s\n", percent(*r.Cleaning), percent(r.Captured), took)
	_ = tw.Flush()

	if adherence := adherenceOf(r); adherence > 0 {
		fmt.Fprintf(w, "\nP10-2, vi-adherence on the gao arm before anything is done about it, reads %s against the %s it was predicted under.\n",
			percent(adherence), percent(can.Adherence))
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// adherenceOf returns the gao arm's vi-adherence reading, which is the arm
// P10-2 was written about.
func adherenceOf(r canReport) float64 {
	for _, a := range r.Arms {
		if a.Arm == "com-8B-cpt-gao" {
			return a.Adherence
		}
	}
	return 0
}
