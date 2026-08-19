package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/needle"
)

func runNeedle(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		needleUsage(stderr)
		return 2
	}
	switch args[0] {
	case "frame":
		return runNeedleFrame(stdout, stderr, args[1:])
	case "check":
		return runNeedleCheck(stdout, stderr, args[1:])
	case "grade":
		return runNeedleGrade(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		needleUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao needle: no subcommand named %s\n", args[0])
		needleUsage(stderr)
		return 2
	}
}

func needleUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao needle <subcommand> [flags] [file]

subcommands:
  frame  print the grid vi-needle is built on, and what running it costs
  check  check a built set against the grid before anything is asked of a model
  grade  read a run's answers and report the shape rather than the number

The needle in a haystack test is the easiest benchmark in the field to pass and
the easiest to build badly. Three things here are not in the usual version: the
haystack is real corpus prose rather than one paragraph repeated, so the needle
is not the only novel text in it; every item carries decoys, which are other
values of the same shape elsewhere in the context, so the item cannot be solved
by finding the only thing that looks like an answer; and some items have no
needle at all, so a model that always produces its most plausible span is caught
rather than rewarded.

The Vietnamese part is the toned items. Hoà and họa differ by a mark and mean
different things, and a model whose tokenizer was built for English tends not to
tell them apart. Answering with the near miss is counted separately from missing,
because it is a different bug.

grade reports the position curve rather than one number, since a model can clear
90% overall while answering nothing placed past the halfway mark. It exits non
zero when the run does not pass or may not be published.
`)
}

// needleFrameReport is the grid as data.
type needleFrameReport struct {
	Repo     string        `json:"repo"`
	Frame    doc.Hash      `json:"frame"`
	Items    int           `json:"items"`
	Tokens   int           `json:"tokens"`
	Lengths  []int         `json:"lengths"`
	Depths   []float64     `json:"depths"`
	Cells    []needle.Cell `json:"cells,omitempty"`
	Describe string        `json:"describe"`
}

// needleGradeReport is the score plus why it may not go out.
type needleGradeReport struct {
	needle.Score
	Blocking []string `json:"blocking"`
	Verdict  string   `json:"verdict"`
}

func runNeedleFrame(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("needle frame", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the frame as JSON")
	cells := fs.Bool("cells", false, "print every square of the grid")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	out := needleFrameReport{
		Repo: needle.Repo, Frame: needle.Digest(),
		Items: needle.Size(), Tokens: needle.Tokens(),
		Lengths: needle.Lengths, Depths: needle.Depths,
		Describe: needle.Describe(),
	}
	if *cells {
		out.Cells = needle.Frame()
	}
	if *asJSON {
		printJSON(stdout, stderr, out)
		return 0
	}

	fmt.Fprintf(stdout, "%s\n\nframe %s\n\n", needle.Describe(), needle.Digest())
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, k := range needle.Kinds {
		var items, tokens int
		for _, c := range needle.Frame() {
			if c.Kind == k {
				items += c.Items
				tokens += c.Length * c.Items
			}
		}
		fmt.Fprintf(tw, "%s\t%d items\t%.1fM tokens\t%s\n", k, items, float64(tokens)/1e6, needleWhy(k))
	}
	_ = tw.Flush()

	if *cells {
		fmt.Fprintln(stdout)
		ct := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, c := range needle.Frame() {
			where := fmt.Sprintf("%.0f%% in", 100*c.Depth)
			switch c.Kind {
			case needle.Absent:
				where = "nothing to find"
			case needle.Split:
				where = fmt.Sprintf("%.0f%% and %.0f%% in", 100*c.Depth, 100*c.Second)
			}
			fmt.Fprintf(ct, "  %6d tokens\t%s\t%s\t%d items\n", c.Length, c.Kind, where, c.Items)
		}
		_ = ct.Flush()
	}

	fmt.Fprintf(stdout, `
The gate is %.0f%% recall overall, %.0f%% at the longest length, at most %.0f points
between the best depth and the worst, at most %.0f%% tone confusion, and at most
%.0f%% of the items with no needle answered anyway.

Nothing has been built yet. The haystacks come out of the corpus and the corpus
is not ingested, so what is fixed here is the grid and the rules, which is the
half a benchmark gets wrong by leaving until after it has results.
`, 100*needle.MinRecall, 100*needle.MinLong, 100*needle.MaxSpread, 100*needle.MaxTone, 100*needle.MaxInvent)
	return 0
}

// needleWhy is why each kind is on the grid, which belongs next to the count
// rather than in a paper nobody reads.
func needleWhy(k needle.Kind) string {
	switch k {
	case needle.Plain:
		return "a fact stated once, with other values of its shape elsewhere in the context"
	case needle.Toned:
		return "the near miss is the same word marked differently, which is the item an English set cannot have"
	case needle.Split:
		return "two facts at two depths, which is reading a document rather than retrieving a span"
	case needle.Absent:
		return "no needle at all, so a model that always answers is caught rather than rewarded"
	default:
		return ""
	}
}

func runNeedleCheck(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("needle check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		needleUsage(stderr)
		return 2
	}

	items, err := needle.ReadItems(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao needle: %v\n", err)
		return 1
	}
	faults := needle.Check(items)
	if len(faults) == 0 {
		fmt.Fprintf(stdout, "%s: %d items, built to frame %s\n", needle.Repo, len(items), needle.Digest())
		return 0
	}
	fmt.Fprintf(stdout, "%s: %d items against a frame that calls for %d\n\n", needle.Repo, len(items), needle.Size())
	for _, f := range faults {
		fmt.Fprintf(stdout, "%s\n", f)
	}
	return 1
}

func runNeedleGrade(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("needle grade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	items := fs.String("items", "", "the built set the answers were produced from")
	asJSON := fs.Bool("json", false, "print the whole score as JSON")
	curve := fs.Bool("curve", false, "print recall at every length and depth")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *items == "" {
		needleUsage(stderr)
		return 2
	}

	set, err := needle.ReadItems(*items)
	if err != nil {
		fmt.Fprintf(stderr, "gao needle: %v\n", err)
		return 1
	}
	replies, err := needle.ReadReplies(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao needle: %v\n", err)
		return 1
	}

	score := needle.Grade(set, replies)
	out := needleGradeReport{Score: score, Blocking: score.Blocking(), Verdict: score.Verdict()}
	if *asJSON {
		printJSON(stdout, stderr, out)
	} else {
		printNeedleGrade(stdout, out, *curve)
	}
	if !score.Passes || len(out.Blocking) > 0 {
		return 1
	}
	return 0
}

func printNeedleGrade(w io.Writer, out needleGradeReport, curve bool) {
	g := out.Score
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "answered\t%d of %d\ton %s\n", g.Graded, g.Asked, needleBoxes(g.Boxes))
	fmt.Fprintf(tw, "recall\t%.1f%%\tagainst a floor of %.0f%%\n", 100*g.Recall, 100*needle.MinRecall)
	fmt.Fprintf(tw, "recall at the longest\t%.1f%%\tagainst a floor of %.0f%%\n", 100*g.Long, 100*needle.MinLong)
	fmt.Fprintf(tw, "best depth to worst\t%.1f points\tagainst a ceiling of %.0f, worst at %.0f%% in\n",
		100*g.Spread, 100*needle.MaxSpread, 100*g.Worst)
	fmt.Fprintf(tw, "tone confusion\t%.1f%%\tanswered with the near miss rather than the needle\n", 100*g.Tone)
	fmt.Fprintf(tw, "invented\t%.1f%%\tof the items with nothing in them to find\n", 100*g.Invent)
	_ = tw.Flush()

	fmt.Fprintln(w)
	ot := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, o := range []needle.Outcome{needle.Found, needle.Tone, needle.Decoyed, needle.Declined, needle.Invented, needle.Missed} {
		fmt.Fprintf(ot, "  %s\t%d\n", o, g.Counts[o])
	}
	_ = ot.Flush()

	if curve && len(g.Curve) > 0 {
		fmt.Fprintf(w, "\nrecall by length and depth:\n")
		ct := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, p := range g.Curve {
			fmt.Fprintf(ct, "  %6d tokens\t%3.0f%% in\t%d of %d\t%.0f%%\n",
				p.Length, 100*p.Depth, p.Found, p.Asked, 100*p.Recall)
		}
		_ = ct.Flush()
	}

	fmt.Fprintf(w, "\n%s\n", out.Verdict)
	if len(out.Blocking) > 0 {
		fmt.Fprintln(w)
		for _, why := range out.Blocking {
			fmt.Fprintf(w, "not publishable: %s\n", why)
		}
	}
}

// boxes names where the answers came from, which is not optional on this
// benchmark because context length is a property of the machine.
func needleBoxes(names []string) string {
	if len(names) == 0 {
		return "no box anybody wrote down"
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}
