package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/tieng"
)

func runTieng(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("syllable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	source := fs.String("source", "", "the text this reading was taken over")
	slots := fs.Int("slots", tieng.Slots, "how many vocabulary slots the arm without the rule may spend on cross-syllable runs")
	top := fs.Int("top", 12, "how many of those runs to print")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao syllable -source name [-slots n] [-top n] [-json] file...

Measure what a syllable-atomic tokenizer would govern, and what it gives up,
over real text and before anything is trained.

Vietnamese writes a space between syllables, so a tokenizer can be told never to
merge across one. Under that rule a syllable costs at least one token and no
vocabulary size moves it. Without the rule the same vocabulary has one extra
freedom: it may spend a slot on a run of syllables that keeps turning up and pay
one token for it instead of two. That is the entire difference between the two
arms, which makes the cost of the rule countable off text with nothing fetched
and nothing trained.

So this reads the sample, counts every whitespace unit as governed or not, finds
the runs that repeat often enough to pay for a slot, spends the slots on the ones
that buy most, and prints what the same vocabulary costs with the rule and
without it.

What the rule buys is a claim about what a model learns from a boundary it did
not have to find, and no reading taken off text settles it. That is P07-3 and it
needs the slate. This prints the cost with the other side of the ledger left
visibly empty rather than quietly at zero.

Exits 1 when this is not a reading of anything, and 2 when it runs and is not
the sample it looks like.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	if *top < 0 {
		fmt.Fprintf(stderr, "gao syllable: a table cannot hold %d rows\n", *top)
		return 2
	}

	docs, err := tieng.ReadDocs(fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "gao syllable: %v\n", err)
		return 1
	}

	r := tieng.Read(*source, *slots, docs)
	report := tiengReport{
		Reading:  r,
		Atomic:   tieng.Atomic,
		Faults:   r.Faults(),
		Blocking: r.Blocking(),
		Holds:    r.Holds(),
		Verdict:  r.Verdict(),
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printTieng(stdout, report, *top)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

type tiengReport struct {
	tieng.Reading

	Atomic float64 `json:"atomic"`

	Faults   []string `json:"faults,omitempty"`
	Blocking []string `json:"blocking,omitempty"`
	Holds    bool     `json:"holds"`
	Verdict  string   `json:"verdict"`
}

func printTieng(w io.Writer, r tiengReport, top int) {
	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "This is not a reading of anything, so nothing was counted:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	fmt.Fprintf(w, "%s, %s syllables over %s.\n", r.Source, thousands(r.Syllables), plural(r.Docs, "document"))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "unit\tcount\tshare\tgoverned\n")
	for _, row := range []struct {
		name string
		n    int64
		rule bool
	}{
		{"marked syllable", r.Marked, true},
		{"bare syllable", r.Bare, true},
		{"other letters", r.Foreign, false},
		{"number", r.Number, false},
		{"letters and digits", r.Mixed, false},
		{"punctuation", r.Symbol, false},
	} {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", row.name, thousands(row.n), percent(over(row.n, r.Units)), yes(row.rule))
	}
	_ = tw.Flush()

	if len(r.Runs) > 0 {
		shown := min(top, len(r.Runs))
		fmt.Fprintf(w, "\nthe %s that %s most, of %s spent:\n", plural(shown, "slot"), buy(shown), plural(len(r.Runs), "slot"))
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprint(tw, "run\tsyllables\tseen\ttokens saved\n")
		for i, run := range r.Runs {
			if i >= top {
				break
			}
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", run.Run, run.Words, thousands(int64(run.Count)), thousands(int64(run.Saves)))
		}
		_ = tw.Flush()
	}

	fmt.Fprintf(w, "\nsyllable atomic %.2f tokens per syllable, merges allowed %.2f, the rule costs %s.\n",
		r.Atomic, r.Crossing, percent(r.Cost))

	if len(r.Faults) > 0 {
		fmt.Fprint(w, "\nThis is not the sample it looks like:\n")
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// over is a share that does not divide by zero, which the unit table needs
// because a reading with no units at all is a reading that got blocked and still
// has to print its own table without panicking.
func over(part, whole int64) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func buy(n int) string {
	if n == 1 {
		return "buys"
	}
	return "buy"
}
