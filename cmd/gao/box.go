package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/may"
)

func runBox(stdout, stderr io.Writer, args []string) int {
	if len(args) > 0 && args[0] == "peak" {
		return runBoxPeak(stdout, stderr, args[1:])
	}
	fs := flag.NewFlagSet("box", flag.ContinueOnError)
	fs.SetOutput(stderr)
	label := fs.Bool("label", false, "print only the provenance label for this machine")
	tokens := fs.Int64("tokens", may.TargetTokens, "token count to compute the disk budget for")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao box [-label] [-tokens N]\n       gao box peak [-ran duration] disk.jsonl\n\nPrints the fleet inventory and the disk budget a corpus of the given size needs.\nThe inventory is measured, not specified, and it carries the date it was taken.\n\nThe peak subcommand reads a watcher's disk trace from a run and grades it\nagainst the ceiling, and against the arithmetic the ceiling was written over.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *label {
		fmt.Fprintln(stdout, may.Label())
		return 0
	}

	fmt.Fprintf(stdout, "fleet as measured on %s\n\n", may.MeasuredOn)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "box\tos\tcores\tmemory\tfree disk\tgpu\n")
	for _, b := range may.Boxes {
		gpu := b.GPU
		if gpu == "" {
			gpu = "none"
		} else {
			gpu = fmt.Sprintf("%s, %s", gpu, may.GB(b.GPUMemory))
		}
		fmt.Fprintf(tw, "%s\t%s\t%d/%d\t%s\t%s\t%s\n",
			b.Name, b.OS, b.Cores, b.Threads, may.GB(b.Memory), may.GB(b.FreeDisk), gpu)
	}
	t := may.Total()
	fmt.Fprintf(tw, "total\t\t%d/%d\t%s\t%s\t%d\n",
		t.Cores, t.Threads, may.GB(t.Memory), may.GB(t.FreeDisk), t.GPUs)
	_ = tw.Flush()

	fmt.Fprint(stdout, "\nroles\n")
	for _, b := range may.Boxes {
		fmt.Fprintf(stdout, "  %s: %s\n", b.Name, b.Role)
	}

	p := may.Plan(*tokens)
	fmt.Fprintf(stdout, "\ndisk budget for %.0fB natural tokens\n", float64(p.Tokens)/1e9)
	fmt.Fprintf(stdout, "  extracted text     %s\n", may.GB(p.Text))
	fmt.Fprintf(stdout, "  compressed at %.1fx %s in %d shards\n", may.AssumedCompression, may.GB(p.Compressed), p.Shards)
	fmt.Fprintf(stdout, "  fleet free disk    %s across %d boxes\n", may.GB(p.FleetFree), t.Boxes)
	fmt.Fprintf(stdout, "  largest single box %s on %s\n", may.GB(p.Largest.FreeDisk), p.Largest.Name)
	if p.Resident {
		fmt.Fprintf(stdout, "  the corpus fits on %s\n", p.Largest.Name)
	} else {
		fmt.Fprintf(stdout, "  the corpus does not fit on any one box, so the store of record is off-box and every stage streams\n")
		fmt.Fprintf(stdout, "  working set        %d shards at a time on %s\n", p.ShardsResident, p.Largest.Name)
	}

	fmt.Fprint(stdout, "\nwhat each box can run, after leaving ")
	fmt.Fprintf(stdout, "%s of reserve alone\n", may.GB(may.ReserveBytes))
	tw = tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "box\tscratch\tshards\tworkers\n")
	for _, pl := range may.Placements() {
		if !pl.Holds {
			fmt.Fprintf(tw, "%s\t%s\tnone\tno corpus bytes land here\n", pl.Box.Name, may.GB(pl.Scratch))
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\n", pl.Box.Name, may.GB(pl.Scratch), pl.Shards, pl.Workers)
	}
	fmt.Fprintf(tw, "fleet\t\t\t%d\n", may.FleetWorkers())
	_ = tw.Flush()

	fmt.Fprintf(stdout, "\nstore of record: an S3-compatible object store, from %s\n", may.StoreEnv)
	if store, ok := may.Store(); ok {
		fmt.Fprintf(stdout, "  %s\n", store)
	} else {
		fmt.Fprintf(stdout, "  unset, so no stage would know where to write\n")
	}

	fmt.Fprintf(stdout, "\nthis process is running on %s\n", may.Label())
	return 0
}

func runBoxPeak(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("box peak", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	name := fs.String("run", "ingest", "what the run was")
	ran := fs.Duration("ran", 0, "how long the run lasted, which is not how long the watcher watched")
	ceiling := fs.Int64("ceiling", may.Ceiling, "the most disk the run may hold, in bytes")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao box peak [-run name] [-ran duration] [-ceiling bytes] [-json] disk.jsonl

Read what a run actually held on disk, against what it was allowed to hold and
against what the design predicted.

Peak disk on this pipeline is arithmetic: two shards per worker, four workers on
server1, 4.1 GB, and it does not read the size of the corpus because a worker
pushes a finished shard off-box and deletes it before taking the next one. The
plan gates on 90 GB instead, and the gap is room for what the model does not
know about, which is the row group buffer, a part waiting on an upload retry, a
download resuming into a partial file, and whatever the operating system kept.

So both are reported. A run that stays under the ceiling passes the gate. A run
that stays under the ceiling and peaks at fifteen times the prediction also says
the model is wrong, which is what somebody needs before running the same stage
on a box with less room.

The refusals are about how the trace was taken. A peak sampled every five
minutes cannot see a shard that was written, pushed and deleted inside a gap. A
watcher that started late or stopped early missed the start and the flush, which
is where a run allocates hardest. And a run allocates on one machine, so a trace
from two of them is not a peak.

Exits 1 if the run went over the ceiling or the trace cannot support the number.

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

	samples, err := may.ReadTrace(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao box peak: %v\n", err)
		return 1
	}
	p := may.Measure(*name, *ran, *ceiling, samples)

	if *asJSON {
		if code := printJSON(stdout, stderr, p); code != 0 {
			return code
		}
	} else {
		printPeak(stdout, p)
	}
	if !p.Settled() {
		return 1
	}
	return 0
}

func printPeak(w io.Writer, p may.Peak) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "run\t%s\ton %s, %s of wall clock\n", p.Run, p.Box, p.Ran)
	fmt.Fprintf(tw, "peak\t%s\tat %s, during %s\n", may.GB(p.Held), (time.Duration(p.At) * time.Second).String(), p.During)
	fmt.Fprintf(tw, "ceiling\t%s\t%s of it left\n", may.GB(p.Ceiling), may.GB(p.Headroom()))
	fmt.Fprintf(tw, "predicted\t%s\ttwo shards for each of the workers the box runs\n", may.GB(p.Predicted))
	if p.Ratio() > 0 {
		fmt.Fprintf(tw, "drift\t%.1fx\tthe measurement over the arithmetic\n", p.Ratio())
	}
	fmt.Fprintf(tw, "watched\t%s\tacross %s, widest gap %s\n", plural(p.Samples, "reading"), p.Watched, p.Widest)
	fmt.Fprintf(tw, "free\t%s\ton %s\n", may.GB(p.Free), p.Box)
	_ = tw.Flush()

	if faults := p.Blocking(); len(faults) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(faults), "fault"))
		for _, f := range faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
	fmt.Fprintf(w, "\n%s\n", p.Verdict())
}
