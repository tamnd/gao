package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/try"
)

func runTry(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		tryUsage(stderr)
		return 2
	}
	switch args[0] {
	case "slate":
		return runTrySlate(stdout, stderr, args[1:])
	case "read":
		return runTryRead(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		tryUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao try: no subcommand named %s\n", args[0])
		tryUsage(stderr)
		return 2
	}
}

func tryUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao try <command> [flags]

The ablation slate: forty runs, fixed before any of them runs.

Almost every threshold in this project is a number somebody picked, and the
honest way to defend a picked number is to run the thing twice and look. Forty
runs of a 1.4B parameter model over 40B tokens each is what this project can
afford to spend answering that, and the slate is the list of what those runs ask.

Three rules do the work. One run varies one thing, because a run that changes two
answers neither. The baseline is run three times at different seeds, because
without that there is no measured gap between two runs of the same recipe and
every effect reported is a difference against a number with no error bar. And
every run is published, including the ones that found nothing, because a slate
that reports only where it moved the number is an advertisement.

commands:
  slate   print the slate and its digest, or check one from a file
  read    read a set of results against the slate they name
`)
}

func runTrySlate(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("try slate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	knobs := fs.Bool("knobs", false, "print what the slate varies rather than every run")
	path := fs.String("slate", "", "read the slate from a file instead of the one this build ships")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao try slate [-knobs] [-json] [-slate file]\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	s, code := readTrySlate(stderr, *path)
	if code != 0 {
		return code
	}
	faults := s.Faults()

	switch {
	case *asJSON:
		if code := printJSON(stdout, stderr, trySlateReport{Slate: s, Digest: s.Digest().String(), Faults: faults}); code != 0 {
			return code
		}
	case *knobs:
		printTryKnobs(stdout, s)
	default:
		printTrySlate(stdout, s)
	}

	if len(faults) > 0 {
		if !*asJSON {
			fmt.Fprintf(stdout, "\n%s:\n", plural(len(faults), "fault"))
			for _, f := range faults {
				fmt.Fprintf(stdout, "  %s\n", f)
			}
		}
		return 1
	}
	return 0
}

func runTryRead(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("try read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	path := fs.String("slate", "", "read the slate from a file instead of the one this build ships")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao try read [-json] [-slate file] results.jsonl

Read a set of results against the slate they were produced under.

Every run is read against what it was measured from, and an effect counts only
if it is larger than the gap between two runs of the baseline. That gap is
measured from the repeats rather than picked, which is the whole reason the
repeats are on the slate.

Exits 1 if the results may not be published as they stand, which includes a slate
missing runs, a slate with no null results in it, and a result with no hardware
recorded against it.

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

	s, code := readTrySlate(stderr, *path)
	if code != 0 {
		return code
	}
	results, err := try.ReadResults(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao try: %v\n", err)
		return 1
	}

	report := s.Read(results)
	out := tryReadReport{Report: report, Faults: append(s.Faults(), report.Publishable()...)}
	if *asJSON {
		if code := printJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		printTryReport(stdout, s, out)
	}
	if len(out.Faults) > 0 {
		return 1
	}
	return 0
}

type trySlateReport struct {
	Slate  try.Slate `json:"slate"`
	Digest string    `json:"digest"`
	Faults []string  `json:"faults,omitempty"`
}

type tryReadReport struct {
	try.Report
	Faults []string `json:"faults,omitempty"`
}

func readTrySlate(stderr io.Writer, path string) (try.Slate, int) {
	if path == "" {
		return try.Fixed(), 0
	}
	s, err := try.ReadSlate(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao try: %v\n", err)
		return try.Slate{}, 1
	}
	return s, 0
}

func printTrySlate(w io.Writer, s try.Slate) {
	fmt.Fprintf(w, "%s\n\n", s.Describe())

	hw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(hw, "model\t%s parameters over %s tokens\n", scale(s.Model), scale(s.Tokens))
	fmt.Fprintf(hw, "scored by\t%s\n", s.Proxy)
	fmt.Fprintf(hw, "runs on\t%s %s\n", s.Compute.Provider, s.Compute.Instance)
	fmt.Fprintf(hw, "costs\t%.0f GPU hours, $%.0f quoted %s\n", s.Compute.GPUHours, s.Compute.USD, s.Compute.Quoted)
	fmt.Fprintf(hw, "digest\t%s\n", s.Digest())
	_ = hw.Flush()

	fmt.Fprintf(w, "\n%s:\n", plural(len(s.Runs), "run"))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "  run\tvaries\tto\tagainst\tsettles\n")
	for _, r := range s.Runs {
		if r.Baseline() {
			fmt.Fprintf(tw, "  %s\tthe baseline\tseed %d\t\tthe noise floor\n", r.ID, r.Seed)
			continue
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", r.ID, r.Knob, r.Value, r.Against, r.Decides)
	}
	_ = tw.Flush()
}

func printTryKnobs(w io.Writer, s try.Slate) {
	fmt.Fprintf(w, "%s\n\n", s.Describe())
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, k := range s.Knobs() {
		var runs []string
		asks := ""
		for _, r := range s.Runs {
			if r.Knob == k {
				runs = append(runs, r.ID)
				if asks == "" {
					asks = r.Asks
				}
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", k, plural(len(runs), "run"), asks)
	}
	_ = tw.Flush()
}

func printTryReport(w io.Writer, s try.Slate, out tryReadReport) {
	r := out.Report
	fmt.Fprintf(w, "%s\n\n", s.Describe())

	hw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(hw, "slate\t%s\t%s\n", r.Version, shortHash(r.Slate.String()))
	fmt.Fprintf(hw, "noise floor\t%.3f\tfrom %s of the baseline\n", r.Noise, plural(r.Baselines, "run"))
	fmt.Fprintf(hw, "moved the number\t%d\t\n", r.Real)
	fmt.Fprintf(hw, "found nothing\t%d\t\n", r.Null)
	fmt.Fprintf(hw, "spent\t%.0f GPU hours\tagainst %.0f budgeted\n", r.Spent, r.Budget)
	_ = hw.Flush()

	fmt.Fprintf(w, "\n%s, largest effect first:\n", plural(len(r.Findings), "run"))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "  run\tvaries\tto\tscore\teffect\tverdict\tsettles\n")
	for _, f := range r.Findings {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%.3f\t%+.3f\t%s\t%s\n",
			f.Run, f.Knob, f.Value, f.Score, f.Effect, effect(f.Real), f.Decides)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%d of %s moved the number by more than the baseline moves against itself.\n",
		r.Real, plural(len(r.Findings), "run"))
	fmt.Fprintf(w, "The other %d are published too, because a knob nobody has to think about again is worth more to the next person than another win.\n", r.Null)

	if len(out.Faults) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(out.Faults), "fault"))
		for _, f := range out.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
		return
	}
	fmt.Fprintf(w, "Every run on the slate came back, and all of them go out.\n")
}

// effect is the word that goes in the verdict column, and the null case is
// spelled out rather than left blank so that it reads as a result.
func effect(real bool) string {
	if real {
		return "moved it"
	}
	return "no effect"
}
