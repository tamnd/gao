package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/lap"
)

func runLap(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("repeat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	generator := fs.String("generator", "", "the generator that wrote the set, as the card names it")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao repeat -generator name [-json] run.jsonl

Read a set of generated documents in the order it was generated and say whether
it is a corpus or one prompt run a million times.

One JSON object per line, one line per document, in generation order: its
identity, the prompt that produced it, its text, and whether the generator's own
filter kept it. Rejected documents stay in the file, because the share a
generator threw away is a fact about the generator.

Every other measure in gao reads one document at a time. A model asked for a
hundred thousand articles returns a hundred thousand fluent, varied, well formed
articles that no per document filter can fault and that are still four hundred
sentence shapes with the nouns swapped. What this reports is the share of five
syllable grams in the last tenth of the set that the first nine tenths did not
already hold, which is the number that saturates when a run has stopped saying
anything new.

The reject rate is read at both ends. Nothing rejected is a filter that did not
run, whatever the code says. Over half rejected means what ships is the tail of
the generator's output that passed gao's own filter, which is a different
artifact and has to be described as one.

Exits 1 when the set cannot be measured, and 2 when it measures and says the run
should stop.

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

	docs, err := lap.ReadDocs(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao repeat: %v\n", err)
		return 1
	}

	s := lap.Read(*generator, docs)
	report := lapReport{
		Generator:  s.Generator,
		Docs:       s.Docs,
		Kept:       s.Kept,
		Rejected:   s.Rejected,
		RejectRate: s.RejectRate(),
		Novelty:    s.Novelty,
		Grams:      s.Grams,
		Tail:       s.Tail,
		Shapes:     s.Shapes,
		Prompts:    s.Prompts,
		Faults:     s.Faults(),
		Blocking:   s.Blocking(),
		Holds:      s.Holds(),
		Verdict:    s.Verdict(),
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printLap(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

type lapReport struct {
	Generator string `json:"generator"`

	Docs       int     `json:"docs"`
	Kept       int     `json:"kept"`
	Rejected   int     `json:"rejected"`
	RejectRate float64 `json:"reject_rate"`

	Novelty float64 `json:"novelty"`
	Grams   int     `json:"grams"`
	Tail    int     `json:"tail"`

	Shapes  []lap.Shape `json:"shapes,omitempty"`
	Prompts []lap.Shape `json:"prompts,omitempty"`

	Faults   []string `json:"faults,omitempty"`
	Blocking []string `json:"blocking,omitempty"`
	Holds    bool     `json:"holds"`
	Verdict  string   `json:"verdict"`
}

func printLap(w io.Writer, r lapReport) {
	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "This is not a set anybody can measure, so nothing was counted:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	fmt.Fprintf(w, "%s wrote %s and its own filter kept %d of them, which is %s rejected.\n",
		r.Generator, count(r.Docs, "document"), r.Kept, percent(r.RejectRate))
	fmt.Fprintf(w, "The last tenth of what it kept is %s material the first nine tenths did not already hold, read over %s grams of five syllables against the %s distinct grams the whole set holds.\n",
		percent(r.Novelty), thousands(int64(r.Tail)), thousands(int64(r.Grams)))

	if len(r.Shapes) > 0 {
		fmt.Fprintf(w, "\nThe openings the most documents share, at %d syllables each:\n", lap.Open)
		printShapes(w, r.Shapes)
	}
	if len(r.Prompts) > 0 {
		fmt.Fprint(w, "\nThe prompts the most of what shipped came from:\n")
		printShapes(w, r.Prompts)
	}

	if len(r.Faults) > 0 {
		fmt.Fprint(w, "\nThis set is shorter than its token count:\n")
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// printShapes prints at most five of them, because the ten in the JSON are for
// a script and the point of the table is which one is at the top.
func printShapes(w io.Writer, shapes []lap.Shape) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, s := range shapes {
		if i == 5 {
			break
		}
		fmt.Fprintf(tw, "  %s\t%d\t%s\n", s.Text, s.Docs, percent(s.Share))
	}
	_ = tw.Flush()
}
