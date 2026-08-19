package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/trust"
)

func runTrust(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		trustUsage(stderr)
		return 2
	}
	switch args[0] {
	case "study":
		return runTrustStudy(stdout, stderr, args[1:])
	case "read":
		return runTrustRead(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		trustUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao trust: no subcommand named %s\n", args[0])
		trustUsage(stderr)
		return 2
	}
}

func trustUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao trust <subcommand> [flags] [file]

subcommands:
  study  print what the validity study is, and the bars it is read against
  read   read paired scores and say whether the fast benchmark can be believed

The ablation slate is forty runs of a 1.4B parameter model scored by vi-cloze,
and every threshold in this project is set from it. That is worth nothing unless
the ordering vi-cloze produces at 1.4B is the ordering vmlu produces at 8B, which
is a claim about the instrument rather than about any recipe.

read takes one file of paired scores, one JSON object per line, each carrying a
run, its slate, both scores and the box each was produced on. The repeats of the
baseline recipe set the floor: two recipes closer together than two runs of one
recipe are a comparison nobody can be right about, and those are counted and
left out rather than counted and won.

It exits non zero when the study may not be published, which includes the case
where the proxy is fine and the study is too small to say so.
`)
}

// trustStudyReport is what study prints with -json.
type trustStudyReport struct {
	Proxy      string  `json:"proxy"`
	Anchor     string  `json:"anchor"`
	Believable float64 `json:"believable"`
	Kill       float64 `json:"kill"`
	Agree      float64 `json:"agree"`
	MinRecipes int     `json:"min_recipes"`
	Describe   string  `json:"describe"`
}

// trustReadReport is the score plus the reasons it may not go out.
type trustReadReport struct {
	trust.Score
	Publishable []string `json:"publishable"`
	Verdict     string   `json:"verdict"`
}

// runTrustStudy prints what is being measured and what it is read against, which
// has to be readable before any of it has run.
func runTrustStudy(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("trust study", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the study as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	out := trustStudyReport{
		Proxy: trust.Proxy, Anchor: trust.Anchor,
		Believable: trust.Believable, Kill: trust.Kill, Agree: trust.Agree,
		MinRecipes: trust.MinRecipes,
		Describe:   trust.Describe(),
	}
	if *asJSON {
		printJSON(stdout, stderr, out)
		return 0
	}

	fmt.Fprintf(stdout, "%s\n\n", trust.Describe())
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "proxy\t%s\t%s\n", trust.Proxy, trust.ProxyScale)
	fmt.Fprintf(tw, "anchor\t%s\t%s\n", trust.Anchor, trust.AnchorScale)
	fmt.Fprintf(tw, "believable at\t%.2f\trank correlation, and the pairwise rate at %.2f\n", trust.Believable, trust.Agree)
	fmt.Fprintf(tw, "killed below\t%.2f\trank correlation, and then the slate is exploratory\n", trust.Kill)
	fmt.Fprintf(tw, "recipes\t%d\tscored both ways before the correlation means anything\n", trust.MinRecipes)
	fmt.Fprintf(tw, "baselines\t%d\truns of one recipe, which is where the noise floor comes from\n", trust.MinBaselines)
	_ = tw.Flush()

	fmt.Fprint(stdout, `
Below the kill criterion the slate is reported as exploratory rather than
decisive, every threshold falls back to a published default from the literature,
and each one goes into the release notes flagged as unvalidated rather than
presented as tuned. That is a real outcome with a real cost, which is why this
is fixed before the runs rather than after somebody has seen a number.

Nothing has been scored at either scale yet.
`)
	return 0
}

// runTrustRead reads the paired scores and says whether the proxy can be believed.
func runTrustRead(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("trust read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the whole score as JSON")
	missed := fs.Bool("missed", false, "print every comparison the proxy called backwards")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		trustUsage(stderr)
		return 2
	}

	pairs, err := trust.ReadPairs(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao trust: %v\n", err)
		return 1
	}

	score := trust.Read(pairs)
	out := trustReadReport{Score: score, Publishable: score.Publishable(), Verdict: score.Verdict()}
	if *asJSON {
		printJSON(stdout, stderr, out)
	} else {
		printTrustScore(stdout, out, *missed)
	}
	if len(out.Publishable) > 0 || !score.Believable {
		return 1
	}
	return 0
}

func printTrustScore(w io.Writer, out trustReadReport, missed bool) {
	sc := out.Score
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "scored both ways\t%d\t%s once the baseline repeats fold into one\n",
		sc.Pairs, plural(sc.Recipes, "recipe"))
	fmt.Fprintf(tw, "noise floor\t%.3f\tat the anchor, from %s of the baseline\n", sc.NoiseAnchor, plural(sc.Baselines, "repeat"))
	fmt.Fprintf(tw, "rank correlation\t%.3f\tagainst a bar of %.2f and a kill criterion of %.2f\n",
		sc.Spearman, trust.Believable, trust.Kill)
	fmt.Fprintf(tw, "pairwise\t%.3f\t%d of %d real comparisons ordered the same way, bar %.2f\n",
		sc.Agreement, sc.Agreed, sc.Compared, trust.Agree)
	fmt.Fprintf(tw, "too close to call\t%d\tinside the floor, so the anchor did not say which was better\n", sc.TooClose)
	if sc.Flat > 0 {
		fmt.Fprintf(tw, "scored identically\t%d\tby the proxy, which is declining to answer rather than answering wrong\n", sc.Flat)
	}
	_ = tw.Flush()

	if missed && len(sc.Missed) > 0 {
		fmt.Fprintf(w, "\ncalled backwards, widest gap at the anchor first:\n")
		mt := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, m := range sc.Missed {
			fmt.Fprintf(mt, "  %s over %s\t%.3f at the anchor\t%.3f the other way at the proxy\n",
				m.Better, m.Worse, m.AnchorGap, m.ProxyGap)
		}
		_ = mt.Flush()
	}

	fmt.Fprintf(w, "\n%s\n", out.Verdict)
	if len(out.Publishable) > 0 {
		fmt.Fprintln(w)
		for _, why := range out.Publishable {
			fmt.Fprintf(w, "not publishable: %s\n", why)
		}
	}
}
