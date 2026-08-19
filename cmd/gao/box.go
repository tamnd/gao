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
	if len(args) > 0 && args[0] == "check" {
		return runBoxCheck(stdout, stderr, args[1:])
	}
	fs := flag.NewFlagSet("box", flag.ContinueOnError)
	fs.SetOutput(stderr)
	label := fs.Bool("label", false, "print only the provenance label for this machine")
	tokens := fs.Int64("tokens", may.TargetTokens, "token count to compute the disk budget for")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao box [-label] [-tokens N]\n       gao box peak [-ran duration] disk.jsonl\n       gao box check [-dir DIR] [-json]\n\nPrints the fleet inventory and the disk budget a corpus of the given size needs.\nThe inventory is measured, not specified, and it carries the date it was taken.\n\nThe peak subcommand reads a watcher's disk trace from a run and grades it\nagainst the ceiling, and against the arithmetic the ceiling was written over.\n\nThe check subcommand measures this box and says how far the record has drifted\nfrom it, which is the thing nobody notices until a plan is built on it.\n\nflags:\n")
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
	// Tabbed rather than padded by hand. The labels were padded to line up with
	// "compressed at 3.0x", and the ratio being measured rather than assumed made
	// it "compressed at 2.07x", which pushed one number out of the column.
	tw = tabwriter.NewWriter(stdout, 0, 0, 1, ' ', 0)
	fmt.Fprintf(tw, "  extracted text\t%s\n", may.GB(p.Text))
	fmt.Fprintf(tw, "  compressed at %.2fx\t%s in %d shards\n", may.Compression, may.GB(p.Compressed), p.Shards)
	fmt.Fprintf(tw, "  fleet free disk\t%s across %d boxes\n", may.GB(p.FleetFree), t.Boxes)
	fmt.Fprintf(tw, "  largest single box\t%s on %s\n", may.GB(p.Largest.FreeDisk), p.Largest.Name)
	if !p.Resident {
		fmt.Fprintf(tw, "  working set\t%d shards at a time on %s, after the reserve\n", p.ShardsResident, p.Largest.Name)
	}
	_ = tw.Flush()
	// The conclusion goes under the numbers rather than between them, because a
	// line with no second column ends the column block and takes the lines after
	// it out of the table.
	if p.Resident {
		fmt.Fprintf(stdout, "  the corpus fits on %s\n", p.Largest.Name)
	} else {
		fmt.Fprint(stdout, "  the corpus does not fit on any one box, so the store of record is off-box and every stage streams\n")
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

	fmt.Fprintf(stdout, "\nstore of record: public dataset repos on the Hugging Face Hub, from %s\n", may.StoreEnv)
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

The prediction is per the workers the trace says were running, and what the plan
would allow the box is printed beside it. They are not the same number. An
ingest runs one worker on a box with thirty two threads, and dividing what one
held by what thirty two may hold reports a full run as a run too small to mean
anything.

The refusals are about how the trace was taken. A peak sampled every five
minutes cannot see a shard that was written, pushed and deleted inside a gap. A
watcher that started late or stopped early missed the start and the flush, which
is where a run allocates hardest. And a run allocates on one machine, so a trace
from two of them is not a peak.

Exits 1 when the trace cannot support the number and 2 when it can and the run
failed its gate.

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
	// One for a trace that cannot support a peak and two for a peak that failed
	// its gate, which is what every other measurement in gao does. They were the
	// same code until a real trace off server3 reported a true fault about the
	// box and exited as though the file were unreadable.
	if len(p.Blocking()) > 0 {
		return 1
	}
	if !p.Settled() {
		return 2
	}
	return 0
}

func printPeak(w io.Writer, p may.Peak) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "run\t%s\ton %s, %s of wall clock\n", p.Run, p.Box, p.Ran)
	fmt.Fprintf(tw, "peak\t%s\tat %s, during %s\n", may.GB(p.Held), (time.Duration(p.At) * time.Second).String(), p.During)
	fmt.Fprintf(tw, "ceiling\t%s\t%s of it left\n", may.GB(p.Ceiling), may.GB(p.Headroom()))
	if p.Predicted > 0 {
		fmt.Fprintf(tw, "predicted\t%s\ttwo shards each for the %s this run had going\n", may.GB(p.Predicted), plural(p.Workers, "worker"))
		if p.Ratio() > 0 {
			fmt.Fprintf(tw, "drift\t%.1fx\tthe measurement over the arithmetic\n", p.Ratio())
		}
	} else {
		// Not "0.0 GB". A trace that does not say how many workers were running
		// has no prediction rather than a prediction of nothing, and the two read
		// the same in a column of gigabytes with no drift line under it.
		fmt.Fprintf(tw, "predicted\tnone\tthe trace does not say how many workers were running, and this is a number per worker\n")
	}
	// What the plan would let the box hold is a different number from what the
	// run's own workers were priced at, and printing only one of them is how a
	// single worker ingest on a 32 thread box got reported as a run too small to
	// mean anything.
	if p.Planned > 0 {
		fmt.Fprintf(tw, "plan allows\t%s\tif a stage used every worker %s has threads for\n", may.GB(p.Planned), p.Box)
	}
	fmt.Fprintf(tw, "watched\t%s\tacross %s, widest gap %s\n", plural(p.Samples, "reading"), p.Watched, p.Widest)
	// Only where the gap is wide enough to be worth pricing. On a trace the
	// watcher kept up with, the blind window is narrower than the resolution and
	// the line is noise.
	if p.Widest > may.Resolution {
		fmt.Fprintf(tw, "blind spot\t%s\tthe most a %s gap hides at the %s a second this run was measured allocating\n",
			may.GB(p.Hidden), p.Widest, may.Size(p.Rise))
	}
	fmt.Fprintf(tw, "free\t%s\ton %s\n", may.GB(p.Free), p.Box)
	_ = tw.Flush()

	if refused := p.Blocking(); len(refused) > 0 {
		fmt.Fprintf(w, "\n%s the trace cannot answer this:\n", plural(len(refused), "reason"))
		for _, r := range refused {
			fmt.Fprintf(w, "  %s\n", r)
		}
	}
	if len(p.Faults) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(p.Faults), "fault"))
		for _, f := range p.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
	fmt.Fprintf(w, "\n%s\n", p.Verdict())
}

// boxCheck is what this box has against what the inventory says it has.
type boxCheck struct {
	Box      string   `json:"box"`
	Path     string   `json:"path"`
	Free     int64    `json:"free"`
	Recorded int64    `json:"recorded"`
	Threads  int      `json:"threads"`
	Taken    string   `json:"inventory_taken"`
	Drift    []string `json:"drift,omitempty"`
	Holds    bool     `json:"holds"`
	Verdict  string   `json:"verdict"`
}

// runBoxCheck measures the box the process is on and grades the record against
// it.
//
// It exists because the inventory is code with a date on it, and code with a
// date on it goes stale silently. Every other measurement in gao is refused
// when it cannot be trusted. This one was simply read.
func runBoxCheck(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("box check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", ".", "the directory to measure free disk on, which is the one the work would run in")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao box check [-dir DIR] [-json]

Measures this box and reports how far the recorded inventory has drifted from
it. Free disk and hardware threads are measured, because those are the numbers
that move. Memory and the card are not, because a portable guess at them is
worse than an old number with a date attached.

Free disk is measured on -dir, since a box has more than one filesystem and the
answer is a property of the one a stage would write to.

Exits 1 when the record has drifted far enough to change a decision, which is
10.0 GB of free disk, a thread count that has moved, or a box crossing the line
where it stops being able to hold corpus bytes at all.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	live, err := may.Now(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "gao box check: %v\n", err)
		return 1
	}
	recorded := int64(0)
	if b, ok := may.Lookup(live.Box); ok {
		recorded = b.FreeDisk
	}
	c := boxCheck{
		Box: live.Box, Path: live.Path, Free: live.Free, Recorded: recorded,
		Threads: live.Threads, Taken: may.MeasuredOn,
		Drift: live.Drift(), Holds: live.Holds(), Verdict: live.Verdict(),
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, c); code != 0 {
			return code
		}
	} else {
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(tw, "box\t%s\n", c.Box)
		fmt.Fprintf(tw, "measured on\t%s\t%s\n", c.Path, may.GB(c.Free))
		fmt.Fprintf(tw, "recorded\t%s\t%s\n", c.Taken, may.GB(c.Recorded))
		fmt.Fprintf(tw, "threads\t%d\n", c.Threads)
		_ = tw.Flush()

		if len(c.Drift) > 0 {
			fmt.Fprint(stdout, "\nthe record has moved:\n")
			for _, why := range c.Drift {
				fmt.Fprintf(stdout, "  %s\n", why)
			}
		}
		fmt.Fprintf(stdout, "\n%s\n", c.Verdict)
	}

	if !c.Holds {
		return 1
	}
	return 0
}
