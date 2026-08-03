package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/luat"
)

func runLuat(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("luat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	question := fs.String("q", "", "print one question in full, by id, such as Q5")
	source := fs.String("source", "", "print the license determinations for one acquisition path")
	verbose := fs.Bool("v", false, "include the evidence behind each determination and what each answer changes")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao luat [-q ID] [-source NAME] [-v]\n\nPrints the legal position: the questions filed with counsel and the position gao\nacts on until each is answered, the license determination for every source, and\nwhat actually ships for a document of each class.\n\nNarrowing to one question or one path always prints in full, so -v is for the\nwhole listing.\n\nNone of it is legal advice and none of it is counsel's answer.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *question != "" {
		q, ok := luat.Ask(*question)
		if !ok {
			fmt.Fprintf(stderr, "gao luat: %s is not on the agenda\n", *question)
			fmt.Fprint(stderr, "the questions are")
			for _, q := range luat.Questions() {
				fmt.Fprintf(stderr, " %s", q.ID)
			}
			fmt.Fprintln(stderr)
			return 2
		}
		printQuestion(stdout, q, true)
		return 0
	}

	if *source != "" {
		s := doc.Source(*source)
		if !s.Valid() {
			fmt.Fprintf(stderr, "gao luat: %q is not an acquisition path\n", *source)
			fmt.Fprint(stderr, "the paths are")
			for _, have := range doc.Sources() {
				fmt.Fprintf(stderr, " %s", have)
			}
			fmt.Fprintln(stderr)
			return 2
		}
		fmt.Fprintf(stdout, "%s: %s\n\n", s, s.Describe())
		printDeterminations(stdout, luat.For(s), true)
		return 0
	}

	fmt.Fprintf(stdout, "questions for counsel, filed %s\n\n", luat.FiledOn)
	for i, q := range luat.Questions() {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		printQuestion(stdout, q, *verbose)
	}

	fmt.Fprint(stdout, "\nlicense determination, one row per body of material\n\n")
	printDeterminations(stdout, luat.Determinations(), *verbose)

	fmt.Fprint(stdout, "\nwhat ships, per class\n\n")
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "class\ttext\tmetadata\tcounted\n")
	for _, p := range luat.Publications() {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Class, yesno(p.Text), yesno(p.Metadata), yesno(p.Counted))
	}
	_ = tw.Flush()
	if *verbose {
		fmt.Fprintln(stdout)
		for _, p := range luat.Publications() {
			fmt.Fprintf(stdout, "  %s: %s\n", p.Class, p.Note)
		}
	}

	fmt.Fprintf(stdout, "\nprojected split: %s of the corpus is publishable, of %s total\n",
		tokens(luat.ProjectedPublishableTokens), tokens(luat.ProjectedTotalTokens))
	fmt.Fprint(stdout, "both numbers go in the release notes rather than the flattering one, and they are\npredictions until the corpus is built\n")

	f := luat.RecipeOnly
	fmt.Fprintf(stdout, "\nfallback, if %s comes back the wrong way\n", f.Question)
	fmt.Fprintf(stdout, "  if   %s\n", f.If)
	fmt.Fprintf(stdout, "  then %s\n", f.Then)
	fmt.Fprint(stdout, "  ships\n")
	for _, s := range f.Publishes {
		fmt.Fprintf(stdout, "    %s\n", s)
	}
	fmt.Fprintf(stdout, "  withheld\n    %s\n", f.Withholds)
	return 0
}

func printQuestion(w io.Writer, q luat.Question, verbose bool) {
	fmt.Fprintf(w, "%-4s%s\n", q.ID, q.Ask)
	if q.Answered() {
		fmt.Fprintf(w, "    answered: %s\n", q.Answer)
	} else {
		fmt.Fprintf(w, "    open, acting on: %s\n", q.Default)
	}
	if verbose {
		fmt.Fprintf(w, "    at stake: %s\n", q.Stakes)
	}
}

func printDeterminations(w io.Writer, ds []luat.Determination, verbose bool) {
	if len(ds) == 0 {
		fmt.Fprint(w, "  nothing determined, which means nothing from here can be admitted\n")
		return
	}
	if verbose {
		for _, d := range ds {
			fmt.Fprintf(w, "  %s\n", d.Subject)
			fmt.Fprintf(w, "    %s%s\n", d.Class, perItem(d))
			fmt.Fprintf(w, "    %s\n", d.Evidence)
			if d.Question != "" {
				fmt.Fprintf(w, "    could move on %s\n", d.Question)
			}
		}
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, d := range ds {
		// The question column is written only when there is one, so that a row
		// with nothing outstanding ends at the class rather than trailing
		// whitespace out to a column that is empty for most of the table.
		if d.Question == "" {
			fmt.Fprintf(tw, "  %s\t%s%s\n", d.Subject, d.Class, perItem(d))
			continue
		}
		fmt.Fprintf(tw, "  %s\t%s%s\t%s\n", d.Subject, d.Class, perItem(d), d.Question)
	}
	_ = tw.Flush()
}

func perItem(d luat.Determination) string {
	if d.PerItem {
		return ", read per item"
	}
	return ""
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// tokens formats a token count the way the corpus is quoted everywhere else,
// which is billions with a B on the end.
func tokens(n int64) string {
	return fmt.Sprintf("%.0fB", float64(n)/1e9)
}
