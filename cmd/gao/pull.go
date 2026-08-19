package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/pull"
)

func runPull(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	run := fs.String("run", "com-8B-cpt-gao", "the run the resumes were recorded on")
	params := fs.Int64("params", 8_000_000_000, "the model's parameter count, which a checkpoint size is read against")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao pull [-run NAME] [-params N] [-json] resumes.jsonl

To pull: what it costs to get back into a run once the host is gone.

A resume tested on the machine that wrote the checkpoint reads it out of the
page cache, never crosses a network, never checks it against its digest, and
reads it back at the rank count that wrote it. All four of those are paths that
will not run on the day it matters, because on that day the host has been
reclaimed and the only copy left is the one that streamed off it.

So a resume is three claims and they fail differently. The bytes came back, which
is a digest computed after the pull against the one written with the checkpoint.
The state came back, which is the loss at the first step after the resume against
the loss at the step it was written, because a loader that restores the weights
and drops the optimizer moments trains fine and recovers over a few hundred steps.
And it came back onto different hardware, since a reclaimed host is replaced by
whatever capacity was free.

Cost is kept apart from all three. A resume can be perfectly correct and still be
a restart nobody can afford, and those are different answers.

Exits 1 if this is not a test of a resume, or 2 if it is one that says the run
cannot be restarted or cannot afford to be.

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

	d, err := pull.ReadDrill(*run, *params, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao pull: %v\n", err)
		return 1
	}

	// A fleet resume names one of our own machines, and the inventory is the
	// only thing that can say whether it named one that exists.
	var claims []string
	for _, r := range d.Fleet() {
		if _, ok := fleet.Lookup(r.Source); !ok && r.Source != "" {
			claims = append(claims, fmt.Sprintf(
				"step %d says it pulled the checkpoint off %s, which is not a box on this fleet, so the copy it read is not the copy that survives a reclaim",
				r.Step, r.Source))
		}
	}

	report := pullReport{
		Run: d.Run, Params: d.Params, State: d.State(), Resumes: len(d.Resumes),
		Offhost: len(d.Offhost()), Fleet: len(d.Fleet()), Resharded: len(d.Resharded()),
		Unaffordable: len(d.Unaffordable()), Budget: pull.Budget,
		Holds:    d.Holds() && len(claims) == 0,
		Blocking: append(d.Blocking(), claims...), Verdict: d.Verdict(),
	}
	if f, ok := d.Fastest(); ok {
		report.Cheapest = f.Source
		report.CheapestFrom = f.From
		report.Overhead = f.Overhead()
	}
	for _, r := range d.Ranked() {
		report.Readings = append(report.Readings, pullReading{
			Step: r.Step, From: r.From, Source: r.Source, Instance: r.Instance,
			Bytes: r.Bytes, Rate: r.Rate(), Ranks: r.ReadRanks, Wrote: r.WroteRanks,
			Restart: r.Restart(), Cost: r.Cost(), Overhead: r.Overhead(),
			Drift: r.Drift(), Matched: r.Matched(), Reshards: r.Reshards(),
			Intact: r.Intact(), Fits: r.Fits(),
		})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printPull(stdout, d, claims)
	}
	if len(d.Blocking()) > 0 || len(claims) > 0 {
		return 1
	}
	if !d.Holds() {
		return 2
	}
	return 0
}

// pullReading is one resume as the table carries it.
type pullReading struct {
	Step int `json:"step"`

	// From is host, fleet or store, and it is the field the milestone item is
	// about rather than a label on the row.
	From   string `json:"from"`
	Source string `json:"source"`

	Instance string `json:"instance"`

	Bytes int64   `json:"bytes"`
	Rate  float64 `json:"rate"`

	Ranks int `json:"read_ranks"`
	Wrote int `json:"wrote_ranks"`

	// Restart is what getting back to the checkpoint costs and Cost adds the
	// training that has to be done again.
	Restart  float64 `json:"restart"`
	Cost     float64 `json:"cost"`
	Overhead float64 `json:"overhead"`

	Drift float64 `json:"drift"`

	Matched  bool `json:"matched"`
	Reshards bool `json:"reshards"`
	Intact   bool `json:"intact"`
	Fits     bool `json:"fits"`
}

type pullReport struct {
	Run    string `json:"run"`
	Params int64  `json:"params"`
	State  int64  `json:"state"`

	Resumes   int `json:"resumes"`
	Offhost   int `json:"offhost"`
	Fleet     int `json:"fleet"`
	Resharded int `json:"resharded"`

	Readings []pullReading `json:"readings"`

	Cheapest     string  `json:"cheapest"`
	CheapestFrom string  `json:"cheapest_from"`
	Overhead     float64 `json:"overhead"`

	Unaffordable int     `json:"unaffordable"`
	Budget       float64 `json:"budget"`

	Holds bool `json:"holds"`

	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printPull(w io.Writer, d pull.Drill, claims []string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "step\tfrom\tsource\tsize\tpull\tranks\tprovision\tload\trestart\tof interval\tdrift\tdigest\n")
	for _, r := range d.Ranked() {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s/s\t%d of %d\t%s\t%s\t%s\t%.0f%%\t%+.4f\t%s\n",
			r.Step, r.From, r.Source, gigabytes(r.Bytes), megabytes(int64(r.Rate())),
			r.ReadRanks, r.WroteRanks, pull.Duration(r.Provision), pull.Duration(r.Load),
			pull.Duration(r.Restart()), 100*r.Overhead(), r.Drift(), matched(r))
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s, %s of training state at %s of parameters.\n",
		d.Run, gigabytes(d.State()), millions(d.Params))
	fmt.Fprintf(w, "A restart may cost %.0f%% of a checkpoint interval in provisioning, pull and load, before a step of the lost training is recomputed.\n",
		100*pull.Budget)
	fmt.Fprintf(w, "The loss either side of a resume may move %g, and a resume that verified its bytes and came back higher than that kept the weights and dropped the moments.\n",
		pull.Noise)

	why := append(d.Blocking(), claims...)
	if len(why) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(why), "fault"))
		for _, one := range why {
			fmt.Fprintf(w, "  %s\n", one)
		}
		return
	}
	if bad := d.Unaffordable(); len(bad) > 0 {
		r := bad[0]
		fmt.Fprintf(w, "The %s copy came back intact and costs %s to get back into, which is %.0f%% of a %s interval, so it is the copy that survives rather than the copy a live restart reads.\n",
			r.From, pull.Duration(r.Restart()), 100*r.Overhead(), pull.Duration(r.Interval))
	}
	fmt.Fprintf(w, "\n%s.\n", d.Verdict())
}

// matched renders the digest column, which has three states rather than two:
// checked and equal, checked and not, and never checked.
func matched(r pull.Resume) string {
	switch {
	case r.Digest == "" || r.Verified == "":
		return "unchecked"
	case r.Matched():
		return "ok"
	default:
		return "differs"
	}
}

// millions renders a parameter count the way a model name does.
func millions(n int64) string {
	if n >= 1e9 {
		return fmt.Sprintf("%.0fB", float64(n)/1e9)
	}
	return fmt.Sprintf("%.0fM", float64(n)/1e6)
}
