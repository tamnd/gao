package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/may"
)

func runBox(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("box", flag.ContinueOnError)
	fs.SetOutput(stderr)
	label := fs.Bool("label", false, "print only the provenance label for this machine")
	tokens := fs.Int64("tokens", may.TargetTokens, "token count to compute the disk budget for")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao box [-label] [-tokens N]\n\nPrints the fleet inventory and the disk budget a corpus of the given size needs.\nThe inventory is measured, not specified, and it carries the date it was taken.\n\nflags:\n")
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

	fmt.Fprintf(stdout, "\nthis process is running on %s\n", may.Label())
	return 0
}
