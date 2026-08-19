package main

// Running the ten gates over a tokenizer.

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/count"
	"github.com/tamnd/gao/doc"
)

func runCountGates(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("count gates", flag.ContinueOnError)
	fs.SetOutput(stderr)
	model := fs.String("tokenizer", "", "the pinned tokenizer to put through the gates")
	oneIn := fs.Int("one-in", 1, "check one document in this many, chosen by document identity so the run is reproducible")
	limit := fs.Int("n", 0, "stop after this many documents")
	sample := fs.Int("sample", 5, "how many failing examples each gate names")
	coverage := fs.Bool("coverage", false, "run the built in coverage set instead of files, which leaves no gate unrun")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao count gates -tokenizer PATH [flags] <file...>

Runs the ten gates from doc 07 over a tokenizer and reports the fertility it
gets on the same text.

The gates are what a tokenizer passes before anything else about it is
discussed. They are cheap, they are absolute, and the first three are the same
question asked of text that fails in different ways: a round trip on the corpus,
on the corpus with its marks taken off, and on the documents that mix Vietnamese
with foreign words and with code.

T1 is at 100.000% and not at 99.99% on purpose. One failure in ten thousand
documents is fifty thousand corrupted documents in a corpus this size, and they
are not spread evenly. They collect in the old orthography, the minority
language quotations and the mathematical notation, which is the text that is
hardest to notice missing and most worth having kept.

T5 is the one this project added. gao allows a token to span a syllable
boundary, so Việt Nam may be one token, and imposes diacritic atomicity
instead: a base letter is never parted from its marks. A tokenizer that can
split them produces a model that can emit the letter and then fail to emit the
mark, which is the tone loss failure from doc 04 arriving at generation time
instead of at ingest.

A gate that found nothing in the sample to run on is reported as not run, and a
tokenizer is eligible only when all nine measured gates ran and passed. T10 is
an audit rather than a threshold: it prints the vocabulary pieces made of
characters this project strips, because such a piece is a fact about the corpus
the tokenizer was trained on rather than about the text it will see here.

Files are Parquet parts or text files, and a text file is one document.

-coverage runs the built in set instead: a few kilobytes holding every letter of
the language, the same letters with their marks written separately, one document
for each legacy encoding, and the mixed and numeric text T3 and T7 need. It
leaves no gate unrun, it takes a millisecond, and it is fixed, so the same
command on two boxes produces the same report or the two boxes differ. What it
is not is a sample of the corpus: the fertility it prints is the fertility of a
letter chart and it is not a number about gao.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *model == "" || (fs.NArg() == 0) != *coverage {
		fs.Usage()
		return 2
	}

	tok, err := count.Open(count.Gemma3, *model)
	if err != nil {
		fmt.Fprintf(stderr, "gao count gates: %v\n", err)
		return 1
	}

	g := count.NewGates(tok, count.GateOptions{OneIn: *oneIn, Limit: *limit, Sample: *sample})
	if *coverage {
		for _, c := range count.Coverage() {
			g.Add(c.ID(), c.Text)
		}
		return printCoverage(stdout, stderr, g.Report())
	}
	for _, name := range fs.Args() {
		if g.Full() {
			break
		}
		err := eachIdentifiedDocument(name, func(id doc.Hash, text string) error {
			g.Add(id, text)
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gao count gates: %s: %v\n", name, err)
			return 1
		}
	}
	return printGates(stdout, stderr, g.Report())
}

// printCoverage reports a run over the built in set.
//
// It judges the correctness gates and says nothing about eligibility, because
// four kilobytes is not a sample of the corpus and T9 needs more text than that
// before the rate it computes is a measurement. A run here answers the question
// that comes first, which is whether the suite is measuring anything.
func printCoverage(stdout, stderr io.Writer, report count.GateReport) int {
	printReport(stdout, report)
	if report.Correct() {
		fmt.Fprintf(stdout, "\nok, every gate about what %s does to text ran and passed on the coverage set\n", report.Tokenizer)
		fmt.Fprint(stdout, "this is not eligibility, which needs the corpus and a throughput measured on it\n")
		return 0
	}
	fmt.Fprintf(stderr, "\ngao dem gates: %s does not pass the coverage set\n", report.Tokenizer)
	printFailures(stderr, report, func(g count.Gate) bool { return g.Name == count.ThroughputGate })
	return 1
}

// printGates is the report, split out from the run so that what a failing gate
// looks like on a terminal is testable without a 4.7 MB protobuf on the box.
//
// A tokenizer is ineligible in two ways and they exit differently, the way they
// do everywhere else in gao. A gate that ran and failed is a measurement of the
// tokenizer and exits 2. A gate that found nothing in the sample to run on is a
// fact about the sample, the tokenizer has not been judged, and that exits 1,
// because the answer to it is more text rather than another tokenizer. Both
// printed 1 until real documents were put through the suite and the failures
// came back with an exit code that said go and find a bigger corpus.
func printGates(stdout, stderr io.Writer, report count.GateReport) int {
	printReport(stdout, report)
	if report.Eligible() {
		fmt.Fprintf(stdout, "\nok, %s passed every gate on this sample\n", report.Tokenizer)
		return 0
	}

	fmt.Fprintf(stderr, "\ngao dem gates: %s is not eligible\n", report.Tokenizer)
	printFailures(stderr, report, nil)
	for _, g := range report.Gates {
		if !g.Audit && g.Ran && g.Failed > 0 {
			return 2
		}
	}
	return 1
}

// printReport writes the table and the examples, which read the same whether the
// text came off a disk or out of the coverage set.
func printReport(stdout io.Writer, report count.GateReport) {
	fmt.Fprintf(stdout, "tokenizer  %s, %d pieces\n", report.Tokenizer, report.Vocab)
	fmt.Fprintf(stdout, "documents  %d\n", report.Documents)
	fmt.Fprintf(stdout, "fertility  %.2f characters per token, %.2f tokens per syllable\n",
		report.CharsPerToken(), report.TokensPerSyllable())
	fmt.Fprintln(stdout)

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, gate := range report.Gates {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", gate.Name, gate.Status(), gate.What, detail(gate))
		if gate.Skipped > 0 {
			fmt.Fprintf(tw, "\t\tit could not look at %d documents, whose pieces do not add up to the document\t\n", gate.Skipped)
		}
	}
	_ = tw.Flush()

	for _, gate := range report.Gates {
		if len(gate.Sample) == 0 {
			continue
		}
		fmt.Fprintf(stdout, "\n%s, starting with:\n", gate.Name)
		for _, s := range gate.Sample {
			fmt.Fprintf(stdout, "  %s\n", s)
		}
	}

}

// printFailures names what went wrong, in the order the gates are in. except is
// the gates the caller did not judge the run by, which on the coverage set is
// the throughput gate and nowhere else.
func printFailures(stderr io.Writer, report count.GateReport, except func(count.Gate) bool) {
	for _, gate := range report.Gates {
		switch {
		case gate.Audit || gate.Passed():
		case except != nil && except(gate):
		case !gate.Ran:
			fmt.Fprintf(stderr, "  %s did not run: %s\n", gate.Name, gate.Why)
		default:
			fmt.Fprintf(stderr, "  %s failed %d of %d %s\n", gate.Name, gate.Failed, gate.Checked, gate.Unit)
		}
	}
}

// detail is the right hand column: whatever the gate has to say for itself,
// which is a note when it has one, a count when the count is the finding, and
// the reason when it did not run.
func detail(g count.Gate) string {
	switch {
	case !g.Ran && g.Why != "":
		return g.Why
	case g.Note != "":
		return g.Note
	case g.Unit == "":
		return ""
	default:
		return fmt.Sprintf("%d of %d %s", g.Failed, g.Checked, g.Unit)
	}
}
