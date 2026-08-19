package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/layers"
	"github.com/tamnd/gao/sample"
)

func runSample(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("sample", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	takes := fs.Bool("takes", false, "print the read list itself, one shard and its byte count per line")
	source := fs.String("source", "", "the source being read")
	seed := fs.String("seed", "", "the seed the shards are drawn with, so a third party draws the same ones")
	layerFile := fs.String("layers", "", "the layer file, the same one gao layers reads")
	want := fs.Int64("bytes", sample.Want, "how much to read off each unread layer")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao sample -source name -seed s -layers layers.jsonl [-bytes N] [-takes] [-json] files.jsonl

Decide which shards of the layers nobody has read get read, before anybody reads
them.

The layer file is the one gao layers reads, and the listing is one JSON object per
shard: its layer, its path, and its size, as the source publishes them. Nothing
here is measured. The whole point of a plan is that it exists before the fetching
does.

A compressed shard cannot be entered in the middle, so every read starts at the
front of a file and stops when it has had enough. That leaves the file count as
the only dial that spreads a sample across a bucket. Forty megabytes off one
shard is forty megabytes of whichever domains the crawl put at the front of it,
and it costs exactly what forty megabytes off sixteen shards costs, so the
spread is a gate here rather than a detail.

The draw is blake3 of the seed with the path, which is the draw gao count verify
uses. A third party with the seed and the listing gets the same shards, and the
digest is over the takes themselves, so a plan regenerated against a different
listing is a different plan rather than the same one with different files in it.

Exits 1 when this is not a plan anybody can run, and 2 when it runs and is not
the sample it looks like.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *layerFile == "" {
		fs.Usage()
		return 2
	}

	read, err := layers.ReadLayers(*layerFile)
	if err != nil {
		fmt.Fprintf(stderr, "gao sample: %v\n", err)
		return 1
	}
	files, err := sample.ReadFiles(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao sample: %v\n", err)
		return 1
	}

	p := sample.ReadPlan(*source, *seed, *want, read, files)
	covered, of := p.Covers()
	report := sampleReport{
		Plan:     p,
		Covered:  covered,
		Layers:   of,
		Faults:   p.Faults(),
		Blocking: p.Blocking(),
		Holds:    p.Holds(),
		Verdict:  p.Verdict(),
	}

	switch {
	case *asJSON:
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	case *takes:
		printTakes(stdout, report)
	default:
		printSample(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

type sampleReport struct {
	sample.Plan

	Covered int `json:"covered"`
	Layers  int `json:"layers"`

	Faults   []string `json:"faults,omitempty"`
	Blocking []string `json:"blocking,omitempty"`
	Holds    bool     `json:"holds"`
	Verdict  string   `json:"verdict"`
}

func printSample(w io.Writer, r sampleReport) {
	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "This is not a plan anybody can run, so no shard was drawn:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	fmt.Fprintf(w, "%s, %d of %s already read, %s to open at %s each.\n",
		r.Source, r.Lit, plural(r.Layers, "layer"), plural(r.Opens, "layer"), corpusSize(r.Want))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "layer\trank\ton disk\tshards\tdrawn\tto read\tof the layer\n")
	for _, read := range r.Reads {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%d\t%s\t%s\n",
			read.Layer, read.Rank, corpusSize(read.Stored), read.Files, len(read.Takes), corpusSize(read.Bytes), fine(read.Share))
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\nseed %s, digest %s.\n", r.Seed, r.Digest.String()[:16])

	if len(r.Faults) > 0 {
		fmt.Fprint(w, "\nThis is not the sample it looks like:\n")
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// printTakes prints the read list and nothing else, which is what a fetcher
// consumes. It is the plan rather than a summary of it, so it stays parseable by
// whatever ends up doing the reading.
func printTakes(w io.Writer, r sampleReport) {
	if len(r.Blocking) > 0 {
		printSample(w, r)
		return
	}
	for _, read := range r.Reads {
		for _, t := range read.Takes {
			fmt.Fprintf(w, "%s %d %d\n", t.Path, t.Bytes, t.Of)
		}
	}
}

// fine prints a share small enough that the usual one decimal place rounds it to
// nothing, which is every share in this report since the whole argument is that
// the reading is tiny against the layer.
func fine(f float64) string { return fmt.Sprintf("%.4f%%", f*100) }
