package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/chon"
)

func runChon(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		chonUsage(stderr)
		return 2
	}
	switch args[0] {
	case "criteria":
		return runChonCriteria(stdout, stderr, args[1:])
	case "bases":
		return runChonBases(stdout, stderr, args[1:])
	case "score":
		return runChonScore(stdout, stderr, args[1:])
	case "help":
		chonUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao choose: no subcommand named %s\n\n", args[0])
		chonUsage(stderr)
		return 2
	}
}

func chonUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao choose criteria [-json]
       gao choose bases    [-json]
       gao choose score    bases.jsonl [-json]

Choosing a base model, in the order the criteria were written down.

There are six and the order is the whole content of them. A table that scores six
things and adds them up is a table where the one criterion that cannot be traded
gets traded, so the license is a gate rather than a column, and fertility is
enough to break a tie on measured quality and not enough to overturn it. That is
implemented as a comparison with a band: two bases within 2 points on criterion 2
are tied and criterion 3 decides between them, and two bases further apart are
not tied and no fertility figure moves them.

Four of the six have to be measured rather than looked up. An unmeasured
criterion is not a zero, it is a hole, and a table that scores around it names a
winner out of a field that was never assembled. score names every hole and
refuses to call the result a decision until there are none.

subcommands:
  criteria  the six, in order, and which of them are gates rather than scores
  bases     the roster: what each candidate is, before anybody measures it
  score     put the measurements against the roster, and see whether it decides

run 'gao choose <subcommand> -h' for the flags of a single subcommand.
`)
}

func runChonCriteria(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("choose criteria", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() { chonUsage(stderr); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	got := chon.Criteria()
	if *asJSON {
		return printJSON(stdout, stderr, got)
	}
	for _, c := range got {
		kind := ""
		switch {
		case c.Gate:
			kind = ", a gate rather than a score"
		case c.Tie:
			kind = ", which only decides between candidates already tied above it"
		}
		fmt.Fprintf(stdout, "%d  %s%s\n   %s\n", c.Rank, c.Name, kind, c.Why)
	}
	fmt.Fprintf(stdout, "\nCriteria 2 through 5 are measurements somebody has to take. The band on criterion 2 is %.0f points, and inside it criterion 3 decides.\n", chon.Band)
	return 0
}

func runChonBases(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("choose bases", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() { chonUsage(stderr); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	got := chon.Bases()
	if *asJSON {
		return printJSON(stdout, stderr, got)
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "base\tfamily\tforward\ttokenizer\tcontext\tderivatives\n")
	for _, b := range got {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			b.Name, b.Family, billions(b.Active), b.Tokenizer, seqName(b.Context), yesNo(b.Derivatives))
	}
	_ = tw.Flush()
	fmt.Fprintf(stdout, "\nForward is what a token costs, which on everything here except the Qwen3 mixture is the whole model.\n")
	fmt.Fprint(stdout, "The tokenizer column is what criterion 3 is a fact about, and a base whose vocabulary is not on the fertility roster is one criterion 3 cannot yet be applied to.\n")
	return 0
}

func runChonScore(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("choose score", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() { chonUsage(stderr); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	readings, err := chon.ReadReadings(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao choose: %v\n", err)
		return 1
	}
	table := chon.Score(readings)

	report := chonScoreReport{
		Bases: len(chon.Bases()), Band: chon.Band, Suites: table.Suites,
		Tied: table.Tied(), Missing: table.Missing(), Faults: table.Faults,
		Decided: table.Decided(), Verdict: table.Verdict(),
	}
	for _, r := range table.Ranked() {
		report.Ranked = append(report.Ranked, chonRow{
			Base: r.Base.Name, Quality: r.Reading.Quality, Suite: r.Reading.Suite,
			Fertility: r.Reading.Fertility, Exposure: r.Reading.Exposure, Context: r.Base.Context,
		})
	}
	if best, ok := table.Choose(); ok {
		report.Choice = best.Name
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printChonScore(stdout, table)
	}
	if table.Decided() {
		return 0
	}
	return 1
}

type chonRow struct {
	Base      string  `json:"base"`
	Quality   float64 `json:"quality"`
	Suite     string  `json:"suite"`
	Fertility float64 `json:"fertility"`
	Exposure  float64 `json:"exposure"`
	Context   int     `json:"context"`
}

type chonScoreReport struct {
	Bases  int       `json:"bases"`
	Ranked []chonRow `json:"ranked,omitempty"`

	// Band is the width of the tie on criterion 2, carried because a ranking is
	// not readable without it.
	Band   float64  `json:"band"`
	Tied   bool     `json:"tied"`
	Suites []string `json:"suites,omitempty"`

	Missing []string `json:"missing,omitempty"`
	Faults  []string `json:"faults,omitempty"`

	// Choice is empty until the table is entitled to name one.
	Choice  string `json:"choice,omitempty"`
	Decided bool   `json:"decided"`
	Verdict string `json:"verdict"`
}

func printChonScore(w io.Writer, table chon.Table) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "base\tquality\tfertility\texposure\tcontext\n")
	for _, r := range table.Ranked() {
		fmt.Fprintf(tw, "%s\t%.1f\t%.2f\t%.1f%%\t%s\n",
			r.Base.Name, r.Reading.Quality, r.Reading.Fertility, 100*r.Reading.Exposure, seqName(r.Base.Context))
	}
	_ = tw.Flush()

	if len(table.Suites) == 1 {
		fmt.Fprintf(w, "\nQuality is criterion 2, measured on %s. Fertility is criterion 3, in tokens per Vietnamese syllable.\n", table.Suites[0])
	}
	if missing := table.Missing(); len(missing) > 0 {
		fmt.Fprint(w, "\nNot yet comparable:\n")
		for _, m := range missing {
			fmt.Fprintf(w, "  %s\n", m)
		}
	}
	fmt.Fprintf(w, "\n%s\n", table.Verdict())
	if len(table.Faults) > 1 {
		for _, fault := range table.Faults[1:] {
			fmt.Fprintf(w, "  and %s\n", fault)
		}
	}
}
