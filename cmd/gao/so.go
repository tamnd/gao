package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/so"
)

func runSo(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao compare [-json] pairs.jsonl

Read a human evaluation back and say whether the raters were reading the answers
or reading the layout.

One JSON object per line, one line per judgement: the prompt both answers came
from, who read it, the two systems in the order they were shown, the length of
each answer in syllables, and which side the rater picked.

The win rate is the easiest number in this project to produce and the easiest to
produce wrongly, so three things are printed before it. The share of picks that
went to whichever answer was shown first, because a rater takes the left hand
answer more often than the right one whether or not it is better. The share of
pairs each system was shown first in, because showing every pair in both orders
only cancels that out if the harness actually did it. And the share of decided
pairs the longer answer won, because a system tuned to write more beats a system
tuned to write better.

The win rate itself is printed with an interval around it, and the report says
in words when that interval covers a half, which is what a 54% win over 200
pairs amounts to.

Exits 1 when the evaluation cannot be read, and 2 when it reads and does not
support the claim that one system beat the other.

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

	pairs, err := so.ReadPairs(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao compare: %v\n", err)
		return 1
	}

	r := so.Read(pairs)
	report := soReport{Reading: r, Faults: r.Faults(), Blocking: r.Blocking(), Separates: r.Separates(), Holds: r.Holds(), Verdict: r.Verdict()}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printSo(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

type soReport struct {
	so.Reading

	Faults    []string `json:"faults,omitempty"`
	Blocking  []string `json:"blocking,omitempty"`
	Separates bool     `json:"separates"`
	Holds     bool     `json:"holds"`
	Verdict   string   `json:"verdict"`
}

// people is the one noun in this report the shared count helper gets wrong.
func people(n int) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d people", n)
}

func printSo(w io.Writer, r soReport) {
	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "This is not an evaluation anybody can read, so no win rate was taken:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	fmt.Fprintf(w, "%s over %d items, read by %s.\n", plural(r.Pairs, "judgement"), r.Items, people(len(r.Raters)))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "the answer shown first won\t%s\tof %d\t(line %s)\n", percent(r.First), r.Pairs, percent(so.MaxFirst))
	fmt.Fprintf(tw, "%s was shown first in\t%s\tof %d\t(line %s)\n", r.A, percent(r.Order), r.Pairs, percent(so.MaxOrder))
	fmt.Fprintf(tw, "the longer answer won\t%s\tof %d\t(line %s)\n", percent(r.Longer), r.Compared, percent(so.MaxLonger))
	fmt.Fprintf(tw, "read by more than one person\t%s\tof %d\t(line %s)\n", percent(r.DoubleShare), r.Items, percent(so.MinDouble))
	fmt.Fprintf(tw, "raters agreed\t%s\tof %d\t(%.2f once chance is out, line %.2f)\n", percent(r.Exact), r.Comparisons, r.Pi, so.MinPi)
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s won %s of the %d pairs somebody decided, from %s to %s, with %s called a tie.\n",
		r.A, percent(r.Rate), r.Decided, percent(r.Low), percent(r.High), percent(float64(r.Ties)/float64(r.Pairs)))

	if len(r.Raters) > 0 {
		fmt.Fprint(w, "\nThe people who read the most of it:\n")
		rw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for i, who := range r.Raters {
			if i == 5 {
				break
			}
			fmt.Fprintf(rw, "  %s\t%d\t%s\t%s called a tie\n", who.Rater, who.Pairs, percent(who.Share), percent(float64(who.Ties)/float64(who.Pairs)))
		}
		_ = rw.Flush()
	}

	if len(r.Faults) > 0 {
		fmt.Fprint(w, "\nThis is not a result about the answers:\n")
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}
