package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/may"
)

func runGat(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		gatUsage(stderr)
		return 2
	}
	switch args[0] {
	case "pins":
		return runGatPins(stdout, stderr, args[1:])
	case "drift":
		return runGatDrift(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		gatUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao gat: unknown subcommand %q\n", args[0])
		gatUsage(stderr)
		return 2
	}
}

func gatUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao gat <subcommand> [flags]

subcommands:
  pins   print the ingest manifest: what gets downloaded and at which revision
  drift  ask every host whether it still serves the revision we pinned

run 'gao gat <subcommand> -h' for the flags of a single subcommand.
`)
}

func runGatPins(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("gat pins", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "", "print one source in full, by name")
	files := fs.Bool("files", false, "list every pinned file with its size and digest")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao gat pins [-source NAME] [-files]

Prints the ingest manifest, which is every file gao downloads and the exact
revision it downloads it at. Hub sources are pinned to a commit SHA and direct
sources to the digest of the file that fixes their file list, because a corpus
pinned to a branch cannot be rebuilt from its own manifest.

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

	if *source != "" {
		p, ok := gat.Pin(doc.Source(*source))
		if !ok {
			fmt.Fprintf(stderr, "gao gat pins: %q is not a pinned source\n", *source)
			return 1
		}
		printPin(stdout, p, true)
		return 0
	}

	fmt.Fprintf(stdout, "ingest manifest, pinned %s\n", gat.PinnedOn())
	fmt.Fprintf(stdout, "%d sources, %d files, %s to download\n\n", len(gat.Sources()), gat.Files(), may.GB(gat.TotalBytes()))

	if *files {
		for i, p := range gat.Sources() {
			if i > 0 {
				fmt.Fprintln(stdout)
			}
			printPin(stdout, p, true)
		}
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "order\tsource\trepo\trevision\tconfig\tfiles\tsize\tclass\n")
	for _, p := range gat.Sources() {
		repo := p.Repo
		if p.Gated {
			repo += " (gated)"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			p.Order, p.Source, repo, shortRevision(p.Revision), p.Config, len(p.Files), may.GB(p.Bytes()), p.Class)
	}
	_ = tw.Flush()
	fmt.Fprint(stdout, "\nrun 'gao gat pins -source NAME' for one source in full.\n")
	return 0
}

func printPin(w io.Writer, p gat.Pinned, verbose bool) {
	fmt.Fprintf(w, "%s  %s\n", p.Source, p.Source.Describe())
	fmt.Fprintf(w, "  order:     %d\n", p.Order)
	fmt.Fprintf(w, "  origin:    %s\n", p.Origin)
	fmt.Fprintf(w, "  repo:      %s\n", p.Repo)
	fmt.Fprintf(w, "  page:      %s\n", p.Page())
	fmt.Fprintf(w, "  revision:  %s\n", p.Revision)
	fmt.Fprintf(w, "  config:    %s\n", p.Config)
	fmt.Fprintf(w, "  class:     %s\n", p.Class)
	if p.Gated {
		fmt.Fprint(w, "  gated:     yes, and the ingest fails at the first fetch without an accepted agreement\n")
	}
	fmt.Fprintf(w, "  download:  %d files, %s\n", len(p.Files), may.GB(p.Bytes()))
	if len(p.Excluded) > 0 {
		fmt.Fprintf(w, "  held back: %d files, %s\n", len(p.Excluded), may.GB(p.ExcludedBytes()))
		fmt.Fprintf(w, "             %s\n", p.ExcludedBecause)
	}
	fmt.Fprintf(w, "  note:      %s\n", p.Note)

	if !verbose {
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, f := range p.Files {
		if f.Digest == "" {
			fmt.Fprintf(tw, "  %s\t%s\n", f.Path, may.GB(f.Bytes))
			continue
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", f.Path, may.GB(f.Bytes), shortRevision(f.Digest))
	}
	_ = tw.Flush()
}

// shortRevision trims a commit or a digest to something a table can hold,
// keeping the algorithm prefix so a digest does not read as a commit.
func shortRevision(rev string) string {
	const n = 12
	if algo, hash, ok := strings.Cut(rev, ":"); ok {
		if len(hash) > n {
			return algo + ":" + hash[:n]
		}
		return rev
	}
	if len(rev) > n {
		return rev[:n]
	}
	return rev
}

func runGatDrift(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("gat drift", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 30*time.Second, "how long to wait for all the hosts together")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao gat drift [-timeout DURATION]

Asks every host in the ingest manifest what revision it serves now and reports
the ones that have moved since they were pinned. It reads the network and it
never writes the manifest: re-pinning is a commit somebody makes deliberately,
with the new file lists and byte counts read at the same time, because a
manifest that re-pins itself silently changes what a released corpus was built
from.

Exits 0 when nothing has moved and 1 when something has, so it can run on a
schedule.

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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	results := make([]driftResult, 0, len(gat.Sources()))
	for _, p := range gat.Sources() {
		d, err := gat.Check(ctx, nil, p)
		results = append(results, driftResult{Source: p.Source, Drift: d, Err: err})
	}
	return reportDrift(stdout, stderr, results)
}

// driftResult is one host's answer, or its refusal to give one.
type driftResult struct {
	Source doc.Source
	Drift  gat.Drift
	Err    error
}

// reportDrift is separate from the command so that the reporting can be tested
// without a network, which is the half of this that has branches in it.
func reportDrift(stdout, stderr io.Writer, results []driftResult) int {
	var moved, failed int
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, r := range results {
		switch {
		case r.Err != nil:
			failed++
			fmt.Fprintf(tw, "%s\tunreachable\t%v\n", r.Source, r.Err)
		case r.Drift.Moved():
			moved++
			fmt.Fprintf(tw, "%s\tmoved\tpinned %s, now %s\n", r.Source, shortRevision(r.Drift.Pinned), shortRevision(r.Drift.Current))
		default:
			fmt.Fprintf(tw, "%s\tunchanged\t%s\n", r.Source, shortRevision(r.Drift.Pinned))
		}
	}
	_ = tw.Flush()

	switch {
	case moved > 0:
		fmt.Fprintf(stdout, "\n%d of %d sources have moved upstream. The pins are unchanged and that is deliberate:\na released corpus is built from what was there, not from what is there now.\n",
			moved, len(results))
		return 1
	case failed > 0:
		fmt.Fprintf(stderr, "\ngao gat drift: %d of %d sources could not be reached\n", failed, len(results))
		return 1
	}
	fmt.Fprintf(stdout, "\nall %d sources still serve the revision they were pinned at on %s\n", len(results), gat.PinnedOn())
	return 0
}
