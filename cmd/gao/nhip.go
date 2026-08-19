package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/may"
	"github.com/tamnd/gao/nhip"
)

func runNhip(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("throughput", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	docs := fs.Int64("docs", nhip.Corpus, "how many documents the pipeline is costed over, which every hours figure is linear in")
	counted := fs.Bool("counted", false, "the document count came off an ingest rather than off the plan estimate")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao throughput [-docs N] [-counted] [-json] stages.jsonl

The beat: what each pipeline stage runs at, with the box on every number.

A rate without a box is not a rate. Normalization has twenty four cores under it
on gamingpc and four on server1, so the same stage differs by six times across
this fleet, and a plan built from whichever box was free that afternoon is wrong
in both directions: it says the pipeline is fast enough when it is not, or it
books weeks of a machine that would have taken days.

The box label is necessary and not sufficient. A rate also has to say how many
workers produced it, since eight workers on eight cores and one worker on the
same box are the same stage and not the same number. And it has to say what a
worker held, because the memory line on this milestone is per worker: server3
has eight cores and 23 GB, wants all eight busy, and eight workers at 2.5 GB
each is 20, which leaves three for the operating system and for the page cache
every parquet read goes through.

Parallel efficiency is read against a single worker run of the same stage. A
stage at eight workers that returns four workers' worth of throughput has all
eight cores busy in the sense that top says they are busy, and the item is about
the other sense, so the efficiency is reported rather than divided away.

The document count is a plan estimate until somebody counts, and -docs is where
a counted one goes. Every hours figure is linear in it.

Exits 1 if the readings cannot be published as throughput, or 2 if a worker
crossed the memory line.

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

	p, err := nhip.ReadPipeline(*docs, *counted, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao throughput: %v\n", err)
		return 1
	}

	// A reading carries what the box has so that it can be checked against the
	// box it names. A stage claiming server3 with sixteen threads came off
	// something else, and the fleet inventory is the only place that can say so.
	var claims []string
	for _, s := range p.Stages {
		b, ok := may.Lookup(s.Box)
		if !ok {
			claims = append(claims, fmt.Sprintf(
				"%s ran on %s, which is not a box on this fleet, so nothing here can be checked against the machine it came off",
				s.Name, s.Box))
			continue
		}
		if s.Threads > 0 && s.Threads != b.Threads {
			claims = append(claims, fmt.Sprintf(
				"%s says %s has %d threads and the inventory says %d, so the reading came off a different machine or the inventory is stale",
				s.Name, s.Box, s.Threads, b.Threads))
		}
		if s.Memory > 0 && s.Memory != b.Memory {
			claims = append(claims, fmt.Sprintf(
				"%s says %s has %s and the inventory says %s, and both of those cannot be server3",
				s.Name, s.Box, gigabytes(s.Memory), gigabytes(b.Memory)))
		}
	}

	report := nhipReport{
		Docs: p.Docs, Measured: p.Measured, Stages: len(p.Stages),
		Hours: p.Hours(), Ceiling: nhip.Ceiling,
		Over: len(p.Over()), Swapping: len(p.Swapping()), Missing: p.Missing(),
		Holds:    p.Holds() && len(claims) == 0,
		Blocking: append(p.Blocking(), claims...), Verdict: p.Verdict(),
	}
	if b, ok := p.Bottleneck(); ok {
		report.Bottleneck = b.Name
		report.BottleneckBox = b.Box
	}
	for _, s := range p.Ranked() {
		report.Rates = append(report.Rates, nhipRate{
			Stage: s.Name, Box: s.Box, Runs: s.Runs, Workers: s.Workers,
			Rate: s.Rate(), PerWorker: s.PerWorker(), Read: s.Read(),
			Scaling: s.Scaling(), PeakRSS: s.PeakRSS, Resident: s.Resident(),
			Hours: s.Hours(p.Docs), Over: s.Over(), Swaps: s.Swaps(),
		})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printNhip(stdout, p, claims)
	}
	if len(p.Blocking()) > 0 || len(claims) > 0 {
		return 1
	}
	if !p.Holds() {
		return 2
	}
	return 0
}

// nhipRate is one stage as the table carries it, which is rates and what a
// worker held rather than the counts they were divided out of.
type nhipRate struct {
	Stage string `json:"stage"`

	// Box is on every row because that is the item, and Runs is here for the
	// rows measured somewhere other than where the stage lives.
	Box  string `json:"box"`
	Runs string `json:"runs,omitempty"`

	Workers   int     `json:"workers"`
	Rate      float64 `json:"rate"`
	PerWorker float64 `json:"per_worker"`
	Read      float64 `json:"read_bytes_per_second"`
	Scaling   float64 `json:"scaling"`

	PeakRSS  int64 `json:"peak_rss"`
	Resident int64 `json:"resident"`

	Hours float64 `json:"hours"`

	Over  bool `json:"over_ceiling"`
	Swaps bool `json:"swaps"`
}

type nhipReport struct {
	Docs int64 `json:"docs"`

	// Measured says the document count was counted rather than estimated, since
	// every hours figure in here is linear in it.
	Measured bool `json:"measured"`

	Stages int        `json:"stages"`
	Rates  []nhipRate `json:"rates"`

	Bottleneck    string `json:"bottleneck"`
	BottleneckBox string `json:"bottleneck_box"`

	Hours   float64 `json:"hours"`
	Ceiling int64   `json:"ceiling"`

	Over     int      `json:"over_ceiling"`
	Swapping int      `json:"swapping"`
	Missing  []string `json:"missing,omitempty"`

	Holds bool `json:"holds"`

	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printNhip(w io.Writer, p nhip.Pipeline, claims []string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "stage\tbox\tworkers\tdocs/s\tper worker\tread\tscaling\tpeak rss\tresident\thours\n")
	for _, s := range p.Ranked() {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%.0f\t%.1f\t%s/s\t%.0f%%\t%s\t%s\t%.0f\n",
			s.Name, s.Box, s.Workers, s.Rate(), s.PerWorker(), megabytes(int64(s.Read())),
			100*s.Scaling(), gigabytes(s.PeakRSS), gigabytes(s.Resident()), s.Hours(p.Docs))
	}
	_ = tw.Flush()

	source := "the plan estimate"
	if p.Measured {
		source = "a counted ingest"
	}
	fmt.Fprintf(w, "\n%s, costed over %.0fM documents from %s.\n",
		plural(len(p.Stages), "stage"), float64(p.Docs)/1e6, source)
	fmt.Fprintf(w, "One pass of the whole pipeline is %.0f hours, which is the sum of the stages rather than the slowest of them, since each one is its own pass over parquet.\n",
		p.Hours())
	fmt.Fprintf(w, "The memory line is %s per worker, because server3 has eight cores and %s and wants all eight busy.\n",
		gigabytes(nhip.Ceiling), gigabytes(server3Memory()))

	why := append(p.Blocking(), claims...)
	if len(why) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(why), "fault"))
		for _, one := range why {
			fmt.Fprintf(w, "  %s\n", one)
		}
		return
	}
	fmt.Fprintf(w, "\n%s.\n", p.Verdict())
}

// gigabytes renders memory to a tenth, since the ceiling on this milestone is
// 2.5 and rounding it prints the line as the number it is not.
func gigabytes(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/(1<<30)) }

// megabytes is the unit a stage's read throughput lands in.
func megabytes(n int64) string { return fmt.Sprintf("%.0f MB", float64(n)/(1<<20)) }

// server3Memory is the box of record's memory, read off the inventory rather
// than repeated, so that the sentence and the fleet cannot drift apart.
func server3Memory() int64 {
	if b, ok := may.Lookup("server3"); ok {
		return b.Memory
	}
	return 0
}
