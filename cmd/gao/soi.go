package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/soi"
)

func runSoi(stdout, stderr io.Writer, args []string) int {
	if len(args) > 0 && args[0] == "field" {
		return runSoiField(stdout, stderr, args[1:])
	}
	fs := flag.NewFlagSet("soi", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	matrix := fs.Bool("matrix", false, "print the tone confusion matrix")
	gate := fs.Bool("gate", false, "exit non zero if the reading fails the S4 gate")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao soi [-matrix] [-gate] [-json] page reading [page reading...]
       gao soi field [-pages N] [-json] engines.jsonl

Measure a machine's reading of Vietnamese against what the page actually says.

The arguments are pairs: the reference transcript first, then the reading to be
judged. Several pairs are one evaluation set and produce one score over all of
them, not an average of per page scores, because a caption and a page of body
text are not one vote each.

Two rates come out. Character error rate is what everybody quotes. Diacritic
error rate is the share of the page's marks that did not survive, and it is the
one that matters here: about a quarter of the characters in Vietnamese carry a
mark and about a sixth carry a tone, so an engine can score 2% character error
rate with all of it in the tones, which is a wrong word in most sentences and a
corpus that passes every quality filter downstream while meaning something else.

With -matrix it prints what each tone was read as, which is what says whether an
engine cannot see a mark or cannot tell two of them apart. Those are different
faults and they are fixed in different places.

With -gate it checks the numbers against the thresholds in S4 and exits 1 if any
of them fails, naming the ones that did, which is what a pipeline calls.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	files := fs.Args()
	if len(files) == 0 {
		fs.Usage()
		return 2
	}
	if len(files)%2 != 0 {
		fmt.Fprintf(stderr, "gao soi: %d files, and they are read as pairs of a page and a reading of it\n", len(files))
		return 2
	}

	var total soi.Score
	lines := make([]soiLine, 0, len(files)/2)
	for i := 0; i < len(files); i += 2 {
		ref, err := readDocument(files[i])
		if err != nil {
			fmt.Fprintf(stderr, "gao soi: %v\n", err)
			return 1
		}
		read, err := readDocument(files[i+1])
		if err != nil {
			fmt.Fprintf(stderr, "gao soi: %v\n", err)
			return 1
		}
		s := soi.Measure(string(ref), string(read))
		total.Add(s)
		lines = append(lines, soiLine{Page: files[i], Reading: files[i+1], Score: s})
	}

	fails := soi.S4.Check(total)
	if *asJSON {
		if code := printJSON(stdout, stderr, soiReport{Readings: lines, Total: total, Fails: fails}); code != 0 {
			return code
		}
	} else {
		printSoi(stdout, lines, total, *matrix)
	}

	if !*gate || len(fails) == 0 {
		return 0
	}
	if !*asJSON {
		fmt.Fprintln(stderr, "\ngao soi: this reading does not clear the S4 gate:")
		for _, f := range fails {
			fmt.Fprintf(stderr, "  %s\n", f)
		}
	}
	return 1
}

// soiLine is one page and the reading of it that was judged.
type soiLine struct {
	Page    string `json:"page"`
	Reading string `json:"reading"`
	soi.Score
}

type soiReport struct {
	Readings []soiLine `json:"readings"`
	Total    soi.Score `json:"total"`

	// Fails is what the gate said, in the report rather than only in the exit
	// code, so that a run kept as a file says why it failed a year later.
	Fails []string `json:"fails,omitempty"`
}

func printSoi(w io.Writer, lines []soiLine, total soi.Score, matrix bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "page\tchars\tmarked\tcer\tder\ttone lost\ttone wrong\tđ and d\n")
	for _, l := range lines {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\t%d\t%d\n",
			l.Page, l.Chars, l.Marked, percent(l.CER()), percent(l.DER()),
			percent(l.ToneDeletionRate()), l.ToneWrong, l.DD)
	}
	if len(lines) > 1 {
		fmt.Fprintf(tw, "all\t%d\t%d\t%s\t%s\t%s\t%d\t%d\n",
			total.Chars, total.Marked, percent(total.CER()), percent(total.DER()),
			percent(total.ToneDeletionRate()), total.ToneWrong, total.DD)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s of the characters came through as themselves, and %s of the marks did.\n",
		percent(total.Accuracy()), percent(1-total.DER()))
	if total.Dropped > 0 || total.Added > 0 {
		fmt.Fprintf(w, "%d characters are missing from the reading and %d are in it that are not on the page.\n",
			total.Dropped, total.Added)
	}

	if !matrix {
		return
	}
	fmt.Fprint(w, "\nWhat each tone was read as. The rows are the page and the columns are the reading.\n\n")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "page \\ read")
	for _, t := range soi.Tones() {
		fmt.Fprintf(tw, "\t%s", t)
	}
	fmt.Fprint(tw, "\n")
	for _, from := range soi.Tones() {
		fmt.Fprintf(tw, "%s", from)
		for _, to := range soi.Tones() {
			if n := total.Confusion[from][to]; n > 0 {
				fmt.Fprintf(tw, "\t%d", n)
				continue
			}
			fmt.Fprint(tw, "\t.")
		}
		fmt.Fprint(tw, "\n")
	}
	_ = tw.Flush()
	fmt.Fprint(w, "\nThe diagonal is the tones that came through. The ngang column is the tones the\nreading did not write, and the ngang row is the ones it invented. The corner\nwhere they meet is left empty, because filling it would count every space and\nevery consonant on the page.\n")
}
