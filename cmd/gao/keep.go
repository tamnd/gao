package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/keep"
)

func runKeep(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("keep", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	model := fs.String("model", "gao-8b-distilled", "the model the seven specialists were distilled into")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao keep [-model name] [-json] retention.jsonl

To keep: what the distilled model kept of each specialist's gain.

Seven specialists are trained separately with verifiable rewards, each one good
at something the base model was not, and then all seven are distilled back into
a single model that has to serve all of it. This reads what survived that, per
specialist, which is the word the milestone uses and the reason this command
exists.

A mean retention is the number everybody reports and the one number nobody can
act on. Six specialists at 95% and one at 20% average 84%, and 84% reads as a
good result while the model is worse at legal citation than the base model was.
So the worst line is what the verdict quotes and the mean is printed beside it.

Retention is a ratio of two differences, distilled minus base over specialist
minus base, so it carries the evaluation's own spread twice. A specialist that
gained a point and a half on a benchmark whose runs vary by one has a retention
number that is mostly noise with a percent sign on it, and those are refused
rather than reported.

The baseline is averaging the same seven checkpoints in weight space, which
costs an afternoon and no GPU hours. Distillation keeping 90% is not a result
until it is next to what that keeps, so a specialist without a merged score is a
fault, and the verdict says what the seven training runs bought.

Exits 1 if the panel cannot carry a retention, or 2 if P09-2 does not hold.

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

	p, err := keep.ReadPanel(*model, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao keep: %v\n", err)
		return 1
	}

	report := keepReport{
		Model: p.Model, Specialists: len(p.Specialists),
		Mean: p.Mean(), Merged: p.MergedMean(),
		Recovers: keep.Recovers, Merges: keep.Merges,
		Hides: p.Hides(), Holds: p.Holds(),
		Blocking: p.Blocking(), Verdict: p.Verdict(),
	}
	if w, ok := p.Worst(); ok {
		report.Worst = w.Name
		report.WorstKept = w.Retention()
	}
	for _, s := range p.Ranked() {
		report.Kept = append(report.Kept, keepKept{
			Name: s.Name, Benchmark: s.Benchmark, Box: s.Box,
			Gain: s.Gain(), Retained: s.Retention(), Merging: s.Merging(),
			Runs: s.Runs, Spread: s.Spread,
		})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printKeep(stdout, p)
	}
	if len(p.Blocking()) > 0 {
		return 1
	}
	if !p.Holds() {
		return 2
	}
	return 0
}

// keepKept is one specialist as the report carries it, which is a retention
// rather than the four scores it was computed from.
type keepKept struct {
	Name      string  `json:"name"`
	Benchmark string  `json:"benchmark"`
	Box       string  `json:"box"`
	Gain      float64 `json:"gain"`
	Retained  float64 `json:"retained"`
	Merging   float64 `json:"merging"`
	Runs      int     `json:"runs"`
	Spread    float64 `json:"spread"`
}

type keepReport struct {
	Model       string `json:"model"`
	Specialists int    `json:"specialists"`

	// Kept is worst first, because the line that decides whether this shipped is
	// the bottom one rather than the average of all of them.
	Kept []keepKept `json:"kept"`

	Mean   float64 `json:"mean"`
	Merged float64 `json:"merged_mean"`

	// Worst and WorstKept are pulled out because they are what the verdict is
	// written against.
	Worst     string  `json:"worst"`
	WorstKept float64 `json:"worst_kept"`

	Recovers float64 `json:"recovers"`
	Merges   float64 `json:"merges"`

	// Hides says the mean is far enough above the worst to mislead on its own,
	// which is a finding rather than a fault.
	Hides bool `json:"hides"`
	Holds bool `json:"holds"`

	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printKeep(w io.Writer, p keep.Panel) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "specialist\tbenchmark\tgain\tkept\tmerging\truns\tspread\n")
	for _, s := range p.Ranked() {
		fmt.Fprintf(tw, "%s\t%s\t%+.1f\t%.0f%%\t%.0f%%\t%d\t%.1f\n",
			s.Name, s.Benchmark, s.Gain(), 100*s.Retention(), 100*s.Merging(), s.Runs, s.Spread)
	}
	fmt.Fprint(tw, "\t\t\t\t\t\t\n")
	fmt.Fprintf(tw, "mean\t\t\t%.0f%%\t%.0f%%\t\t\n", 100*p.Mean(), 100*p.MergedMean())
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s, distilled from %s.\n", p.Model, plural(len(p.Specialists), "specialist"))
	fmt.Fprintf(w, "P09-2 asks for %.0f%% kept by distillation and %.0f%% or less by averaging the same checkpoints.\n",
		100*keep.Recovers, 100*keep.Merges)
	if p.Hides() {
		if worst, ok := p.Worst(); ok {
			fmt.Fprintf(w, "The mean of %.0f%% is %.0f points above %s, so it is not a number to quote on its own.\n",
				100*p.Mean(), 100*(p.Mean()-worst.Retention()), worst.Name)
		}
	}

	if why := p.Blocking(); len(why) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(why), "fault"))
		for _, one := range why {
			fmt.Fprintf(w, "  %s\n", one)
		}
		return
	}
	fmt.Fprintf(w, "\n%s.\n", p.Verdict())
}
