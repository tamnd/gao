package main

// Running the ten gates over a tokenizer.

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/dem"
	"github.com/tamnd/gao/doc"
)

func runDemGates(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dem gates", flag.ContinueOnError)
	fs.SetOutput(stderr)
	model := fs.String("tokenizer", "", "the pinned tokenizer to put through the gates")
	oneIn := fs.Int("one-in", 1, "check one document in this many, chosen by document identity so the run is reproducible")
	limit := fs.Int("n", 0, "stop after this many documents")
	sample := fs.Int("sample", 5, "how many failing examples each gate names")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao dem gates -tokenizer PATH [flags] <file...>

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

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *model == "" || fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	tok, err := dem.Open(dem.Gemma3, *model)
	if err != nil {
		fmt.Fprintf(stderr, "gao dem gates: %v\n", err)
		return 1
	}

	g := dem.NewGates(tok, dem.GateOptions{OneIn: *oneIn, Limit: *limit, Sample: *sample})
	for _, name := range fs.Args() {
		if g.Full() {
			break
		}
		err := eachIdentifiedDocument(name, func(id doc.Hash, text string) error {
			g.Add(id, text)
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gao dem gates: %s: %v\n", name, err)
			return 1
		}
	}
	return printGates(stdout, stderr, g.Report())
}

// printGates is the report, split out from the run so that what a failing gate
// looks like on a terminal is testable without a 4.7 MB protobuf on the box.
func printGates(stdout, stderr io.Writer, report dem.GateReport) int {
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

	if report.Eligible() {
		fmt.Fprintf(stdout, "\nok, %s passed every gate on this sample\n", report.Tokenizer)
		return 0
	}

	fmt.Fprintf(stderr, "\ngao dem gates: %s is not eligible\n", report.Tokenizer)
	for _, gate := range report.Gates {
		switch {
		case gate.Audit || gate.Passed():
		case !gate.Ran:
			fmt.Fprintf(stderr, "  %s did not run: %s\n", gate.Name, gate.Why)
		default:
			fmt.Fprintf(stderr, "  %s failed %d of %d %s\n", gate.Name, gate.Failed, gate.Checked, gate.Unit)
		}
	}
	return 1
}

// detail is the right hand column: whatever the gate has to say for itself,
// which is a note when it has one, a count when the count is the finding, and
// the reason when it did not run.
func detail(g dem.Gate) string {
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
