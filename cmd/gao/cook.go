package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/cook"
)

func runCook(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("cook", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao cook <subcommand>

The training plan, as arithmetic rather than as prose.

subcommands:
  budget      the 1 T token mixture, one line per component
  curriculum  the three phases and what each one reads
  reconcile   what the budget buys against what the curriculum spends
  questions   the disagreements between the two, still open
  arms        the continued pretraining comparison and the recipe it shares
  fleet       what server1, server2, server3 and gamingpc do during a run
  check       report everything in the plan that cannot be true at once
`)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	switch fs.Arg(0) {
	case "budget":
		return runCookBudget(stdout)
	case "curriculum":
		return runCookCurriculum(stdout)
	case "reconcile":
		return runCookReconcile(stdout)
	case "questions":
		return runCookQuestions(stdout)
	case "arms":
		return runCookArms(stdout)
	case "fleet":
		return runCookFleet(stdout)
	case "check":
		return runCookCheck(stdout)
	default:
		fmt.Fprintf(stderr, "gao cook: unknown subcommand %q\n", fs.Arg(0))
		fs.Usage()
		return 2
	}
}

func runCookBudget(stdout io.Writer) int {
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "component\tunique\tepochs\tpasses\tinstances\tshare\tkind\n")
	for _, c := range cook.Budget() {
		fmt.Fprintf(tw, "%s\t%s\t%.1f\t%.1f\t%s\t%.1f%%\t%s\n",
			c.Name, tokens(c.Unique), c.Epochs, c.Passes(), tokens(c.Instances()), cook.Share(c.Name), c.Kind)
	}
	_ = tw.Flush()
	fmt.Fprintf(stdout, "\n%s of token instances over %s of distinct text.\n", tokens(cook.Instances()), tokens(cook.Unique()))
	fmt.Fprintf(stdout, "%.0f%% Vietnamese, %.0f%% anchor languages.\n", cook.VietnameseShare(), cook.KindShare(cook.Anchor))
	fmt.Fprintf(stdout, "Natural Vietnamese: %s of distinct text, read %.2f times on average.\n", tokens(cook.NaturalUnique()), cook.NaturalEpochs())
	return 0
}

func runCookCurriculum(stdout io.Writer) int {
	for i, p := range cook.Curriculum() {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "%s  %.0f%% of the run, %s at %d tokens of context, %s\n",
			p.Name, p.Share, tokens(p.Tokens()), p.Sequence, p.LR)
		fmt.Fprintf(stdout, "  %s\n", p.Why)
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, s := range p.Mix {
			fmt.Fprintf(tw, "  \t%s\t%.0f%%\n", s.Component, s.Percent)
		}
		_ = tw.Flush()
	}
	return 0
}

func runCookReconcile(stdout io.Writer) int {
	fmt.Fprint(stdout, cook.Report())
	fmt.Fprintf(stdout, "\nA component more than %.0f point off is a decision somebody owes, and gao cook questions has them.\n", cook.Tolerance)
	return 0
}

func runCookQuestions(stdout io.Writer) int {
	for i, q := range cook.Questions() {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "%s  %s\n", q.ID, q.Component)
		fmt.Fprintf(stdout, "  %s\n", q.Ask)
	}
	return 0
}

func runCookArms(stdout io.Writer) int {
	r := cook.Matched()
	fmt.Fprintf(stdout, "Every arm runs %s, %.0f%% Vietnamese and %.0f%% replay, %.0fM tokens per step.\n",
		tokens(r.Tokens), r.Vietnamese, r.Replay, float64(r.Batch)/(1<<20))
	fmt.Fprintf(stdout, "Learning rate: %s.\n", r.LR)
	fmt.Fprintf(stdout, "Tokenizer: %s.\n", r.Tokenizer)
	fmt.Fprintf(stdout, "\nGate: %s.\n\n", r.Gate)
	for i, a := range cook.Arms() {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "%s\n  trains on %s\n  %s\n", a.ID, a.Data, a.Why)
	}
	return 0
}

func runCookFleet(stdout io.Writer) int {
	fmt.Fprintln(stdout, cook.Fleet())
	need, have, times := cook.Shortfall()
	fmt.Fprintf(stdout, "\nA from scratch run needs %s of accelerator memory and the fleet has %s, which is %.0f times short.\n",
		bytesOf(need), bytesOf(have), times)
	return 0
}

func runCookCheck(stdout io.Writer) int {
	problems := append(cook.Check(), cook.CheckArms()...)
	if len(problems) == 0 {
		fmt.Fprintln(stdout, "The plan holds: the budget adds up, the curriculum spends it, and every gap between them is written down.")
		return 0
	}
	for _, p := range problems {
		fmt.Fprintln(stdout, p)
	}
	return 1
}

// bytesOf renders accelerator memory in the unit the cards are sold in.
func bytesOf(n int64) string {
	if n >= 1<<40 {
		return fmt.Sprintf("%.0f TB", float64(n)/(1<<40))
	}
	return fmt.Sprintf("%.0f GB", float64(n)/(1<<30))
}
