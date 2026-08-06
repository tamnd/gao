package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/xep"
)

func runXep(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		xepUsage(stderr)
		return 2
	}
	switch args[0] {
	case "frame":
		return runXepFrame(stdout, stderr, args[1:])
	case "read":
		return runXepRead(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		xepUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao xep: no subcommand named %s\n", args[0])
		xepUsage(stderr)
		return 2
	}
}

func xepUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao xep <command> [flags]

The reference set gao-refset is labeled against: which documents get drawn,
which bands they get placed in, and what decides which.

A quality classifier is a function from a document to a band and it is only
ever as good as the bands somebody placed documents in by hand. Everything
downstream of gao-qual, which is to say the share of the corpus that survives
to training, comes out of 200,000 human judgments that nobody audits the way
they audit the model trained on them.

So the draw and the rubric are fixed and hashed before a document is labeled.
A rubric written during labeling gets written toward the labels already
collected, a rubric written afterwards gets written toward the classifier, and
neither is visible in the finished set.

commands:
  frame   print the draw and the rubric with the digest they are fixed under
  read    read a labeling back: coverage, agreement, and who did the labeling
`)
}

func runXepFrame(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("xep frame", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	rubric := fs.Bool("rubric", false, "print the rubric in full rather than a line per band")
	path := fs.String("frame", "", "read the frame from a file instead of the one this build ships")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao xep frame [-rubric] [-json] [-frame file]\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	f, code := readXepFrame(stderr, *path)
	if code != 0 {
		return code
	}
	faults := f.Faults()

	switch {
	case *asJSON:
		if code := printJSON(stdout, stderr, xepFrameReport{Frame: f, Digest: f.Digest().String(), Faults: faults}); code != 0 {
			return code
		}
	case *rubric:
		printXepRubric(stdout, f)
	default:
		printXepFrame(stdout, f)
	}

	if len(faults) > 0 {
		if !*asJSON {
			fmt.Fprintf(stdout, "\n%s:\n", plural(len(faults), "fault"))
			for _, x := range faults {
				fmt.Fprintf(stdout, "  %s\n", x)
			}
		}
		return 1
	}
	return 0
}

func runXepRead(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("xep read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	path := fs.String("frame", "", "read the frame from a file instead of the one this build ships")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao xep read [-json] [-frame file] labels.jsonl

Read a labeling back against the frame it was drawn from.

One label per line, each naming the document, the source it came from, the band
it was placed in, and who placed it. A label may carry the digest of the frame
it was labeled against, and one carrying a different digest is reported rather
than folded in.

The first label on a document is the band of record. A second one measures the
rubric rather than overruling the first, because a set where disagreements are
resolved on the way in has no disagreements left to report.

Exits 1 if the labeling may not be published as it stands.

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

	f, code := readXepFrame(stderr, *path)
	if code != 0 {
		return code
	}
	labels, err := xep.ReadLabels(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao xep: %v\n", err)
		return 1
	}

	score := f.Read(labels)
	out := xepReadReport{Score: score, Faults: append(f.Faults(), score.Publishable()...)}
	if *asJSON {
		if code := printJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		printXepScore(stdout, f, out)
	}
	if len(out.Faults) > 0 || !score.Passed {
		return 1
	}
	return 0
}

type xepFrameReport struct {
	Frame  xep.Frame `json:"frame"`
	Digest string    `json:"digest"`
	Faults []string  `json:"faults,omitempty"`
}

type xepReadReport struct {
	xep.Score
	Faults []string `json:"faults,omitempty"`
}

func readXepFrame(stderr io.Writer, path string) (xep.Frame, int) {
	if path == "" {
		return xep.Fixed(), 0
	}
	f, err := xep.ReadFrame(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao xep: %v\n", err)
		return xep.Frame{}, 1
	}
	return f, 0
}

func printXepFrame(w io.Writer, f xep.Frame) {
	fmt.Fprintf(w, "%s\n\nthe draw:\n", f.Describe())
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "  source\tshare\tdocuments\twhy it gets that share\n")
	for _, sl := range f.Slices {
		fmt.Fprintf(tw, "  %s\t%.0f%%\t%d\t%s\n", sl.Source, 100*sl.Share, sl.Wanted(f.Size), sl.Why)
	}
	_ = tw.Flush()

	fmt.Fprint(w, "\nthe scale, richest first:\n")
	bw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(bw, "  band\tconfused with\tand told apart by\n")
	for _, b := range xep.Bands {
		r, ok := f.Rule(b)
		if !ok {
			fmt.Fprintf(bw, "  %s\t\tnothing, because the rubric does not describe it\n", b)
			continue
		}
		fmt.Fprintf(bw, "  %s\t%s\t%s\n", r.Band, r.Confused, r.Apart)
	}
	_ = bw.Flush()

	fmt.Fprintf(w, "\ndigest %s, published as %s\n", f.Digest(), xep.Repo)
	fmt.Fprint(w, "Run 'gao xep frame -rubric' for what puts a document in each band.\n")
}

func printXepRubric(w io.Writer, f xep.Frame) {
	fmt.Fprintf(w, "%s\n", f.Describe())
	for _, b := range xep.Bands {
		r, ok := f.Rule(b)
		if !ok {
			continue
		}
		fmt.Fprintf(w, "\n%s\n  %s\n", r.Band, r.Decides)
		if r.Confused != "" {
			fmt.Fprintf(w, "  against %s: %s\n", r.Confused, r.Apart)
		}
		for _, ex := range r.Examples {
			fmt.Fprintf(w, "  - %s\n", ex)
		}
	}
}

func printXepScore(w io.Writer, f xep.Frame, out xepReadReport) {
	sc := out.Score
	fmt.Fprintf(w, "%s\n\n", f.Describe())

	hw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(hw, "frame\t%s\t%s\n", sc.Version, shortHash(sc.Frame.String()))
	fmt.Fprintf(hw, "labeled\t%d\tof the %d the frame draws\n", sc.Labeled, sc.Wanted)
	fmt.Fprintf(hw, "placed twice\t%d\t%.1f%%, against a floor of %.0f%%\n", sc.Double, 100*sc.DoubleRate, 100*xep.Double)
	fmt.Fprintf(hw, "same band\t%.3f\tover %s, against a floor of %.2f\n", sc.Exact, plural(sc.Pairs, "comparison"), xep.MinExact)
	fmt.Fprintf(hw, "within one band\t%.3f\tagainst a floor of %.2f\n", sc.Adjacent, xep.MinAdjacent)
	fmt.Fprintf(hw, "labelers\t%d\t%s between them\n", len(sc.ByPerson), plural(sc.Labels, "label"))
	_ = hw.Flush()

	fmt.Fprint(w, "\nper band:\n")
	bw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(bw, "  band\tdocuments\tshare\n")
	for _, b := range sc.ByBand {
		fmt.Fprintf(bw, "  %s\t%d\t%.1f%%\n", b.Band, b.Documents, 100*b.Share)
	}
	_ = bw.Flush()

	fmt.Fprint(w, "\nper source, furthest from the draw first:\n")
	sw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(sw, "  source\tlabeled\twanted\tshare\tasked\n")
	for _, s := range sc.Worst() {
		fmt.Fprintf(sw, "  %s\t%d\t%d\t%.1f%%\t%.0f%%\n", s.Source, s.Labeled, s.Wanted, 100*s.Share, 100*s.Asked)
	}
	_ = sw.Flush()

	fmt.Fprintln(w)
	if sc.Passed {
		fmt.Fprintf(w, "pass: two people chose the same band %.1f%% of the time and were never more than one apart.\n", 100*sc.Exact)
	} else {
		fmt.Fprintf(w, "fail: the labeling does not meet the gate it was drawn under.\n")
	}
	if sc.Wanted < xep.Documents {
		fmt.Fprintf(w, "This frame draws %d and %s is %d, so this is a pilot: it says whether the rubric decides anything and it is not the reference set.\n",
			sc.Wanted, xep.Repo, xep.Documents)
	}

	if len(out.Faults) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(out.Faults), "fault"))
		for _, x := range out.Faults {
			fmt.Fprintf(w, "  %s\n", x)
		}
	}
}
