package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/ask"
)

func runAsk(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	name := fs.String("name", "vi-longdoc-qa-1.0", "the benchmark these questions compose")
	rejects := fs.Bool("rejects", false, "print every question that did not survive its checks, and why")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao ask [-name set] [-rejects] [-json] questions.jsonl

Check that a long document question set measures reading a long document.

One JSON object per line, one line per question: which document it is about and
how many tokens that document is, the spans of it the answer needs, whether the
question was put to a model with no document attached and whether that model
answered it anyway, and how many people read the question and agreed.

Three things go wrong when a set like this is built, and all three are invisible
once it is finished. A question that can be answered with no document measures
what the pretraining corpus held. A question whose answer sits in one span is
retrieval, which gao needle already measures with a needle. And a set whose
documents are all around forty thousand tokens says nothing about whether the
extension to 131k worked, since nothing in it lives up there.

So the closed book run is recorded per question rather than described, the spans
are part of the record and both their count and their spread are checked, and
every question is placed on a rung of the S8 context ladder with a floor on how
thin a rung may get.

Exits 1 when the file is not a benchmark, and 2 when it is one that cannot reach
the top of the ladder.

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

	s, err := ask.ReadSet(*name, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao ask: %v\n", err)
		return 1
	}

	heaviest, leans := s.Heaviest()
	report := askReport{
		Name:        s.Name,
		Target:      ask.Target,
		Read:        len(s.Questions),
		Admitted:    len(s.In()),
		Recalled:    s.Recalled(),
		Documents:   s.Documents(),
		Heaviest:    heaviest,
		Leans:       leans,
		MaxPerDoc:   ask.MaxPerDocument,
		Reach:       s.Reach(),
		MinReach:    ask.MinReach,
		Ladder:      s.Ladder(),
		Composition: s.Composition(),
		Thin:        s.Thin(),
		Holds:       s.Holds(),
		Blocking:    s.Blocking(),
		Verdict:     s.Verdict(),
	}
	for _, q := range s.Out() {
		report.Rejected = append(report.Rejected, askReject{ID: q.ID, Why: q.Blocking()})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printAsk(stdout, report, *rejects)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

type askReject struct {
	ID  string   `json:"id"`
	Why []string `json:"why"`
}

type askReport struct {
	Name   string `json:"name"`
	Target int    `json:"target"`

	Read     int `json:"read"`
	Admitted int `json:"admitted"`

	// Recalled is how many questions a model answered with no document attached.
	// It is published on its own because it is the number that says whether the
	// set measures reading, and almost nobody publishes it.
	Recalled int `json:"recalled"`

	Documents int     `json:"documents"`
	Heaviest  string  `json:"heaviest"`
	Leans     float64 `json:"leans"`
	MaxPerDoc float64 `json:"max_per_document"`

	Reach    float64 `json:"reach"`
	MinReach float64 `json:"min_reach"`

	Ladder      []ask.Row `json:"ladder"`
	Composition []ask.Row `json:"composition"`
	Thin        []string  `json:"thin,omitempty"`

	Rejected []askReject `json:"rejected,omitempty"`

	Holds    bool     `json:"holds"`
	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printAsk(w io.Writer, r askReport, rejects bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "rung\tquestions\tshare\tfloor\tmean reach\tmean spans\tfills\n")
	for _, row := range r.Ladder {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%.1f\t%s\n",
			row.Name, row.Questions, percent(row.Share), percent(row.Floor),
			percent(row.Reach), row.Spans, yesno(row.Holds))
	}
	_ = tw.Flush()

	fmt.Fprint(w, "\n")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "kind\tquestions\tshare\tmean reach\tmean spans\n")
	for _, row := range r.Composition {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%.1f\n",
			row.Name, row.Questions, percent(row.Share), percent(row.Reach), row.Spans)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%d of %d questions survived their own checks, %d of them thrown out for being answered with no document attached.\n",
		r.Admitted, r.Read, r.Recalled)
	fmt.Fprintf(w, "They come off %d documents and the one the set leans on hardest is %s at %s of it, against a %s ceiling.\n",
		r.Documents, r.Heaviest, percent(r.Leans), percent(r.MaxPerDoc))

	if rejects && len(r.Rejected) > 0 {
		fmt.Fprintf(w, "\n%s did not survive:\n", plural(len(r.Rejected), "question"))
		for _, q := range r.Rejected {
			fmt.Fprintf(w, "  %s: %s\n", q.ID, strings.Join(q.Why, "; "))
		}
	}

	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "\nThis is not a benchmark yet:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}
	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}
