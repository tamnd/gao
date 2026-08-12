package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/siet"
)

func runSiet(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		sietUsage(stderr)
		return 2
	}
	switch args[0] {
	case "recipe":
		return runSietRecipe(stdout, stderr, args[1:])
	case "read":
		return runSietRead(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		sietUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao siet: no subcommand named %s\n", args[0])
		sietUsage(stderr)
		return 2
	}
}

func sietUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao siet recipe [-config recipe.json] [-why] [-json]
       gao siet read [-config recipe.json] [-specialist name] [-json] steps.jsonl

The GRPO step the specialists are trained with, and a run read back against it.

Four settings decide what a reinforcement learning run becomes and every one of
them is left to the caller by the loops that implement the algorithm: decoupled
clipping with a higher upper bound, a loss aggregated over tokens, prompts whose
rollouts all agree dropped rather than trained on, and answers cut off by the
length limit filtered rather than penalized. Each is the fix for a failure with
a name, so each is written down here with the failure next to it.

recipe checks a configuration before it runs. read checks what it did, which is
a different question: an upper clip bound that never binds is symmetric clipping
under another name, and it will otherwise be reported as the reason an entropy
collapse did not happen.

The log is one JSON object per step: the groups sampled and kept, the rollouts
generated and truncated, the share of tokens clipped at each bound, the entropy
and the mean reward, and the hardware it ran on.

Exits 1 when the log is not one run, and 2 when the configuration cannot be what
it says it is or the run has something in it worth reading before the reward.

run 'gao siet <command> -h' for the flags of one of them.
`)
}

// sietConfig reads the configuration a subcommand was pointed at, which is the
// plan's own when nobody pointed it anywhere.
func sietConfig(stderr io.Writer, path string) (siet.Recipe, bool) {
	if path == "" {
		return siet.Plan(), true
	}
	r, err := siet.ReadRecipe(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao siet: %v\n", err)
		return siet.Recipe{}, false
	}
	return r, true
}

func runSietRecipe(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("siet recipe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	config := fs.String("config", "", "read the configuration from a JSON file rather than taking the plan's")
	why := fs.Bool("why", false, "print the reason each setting is what it is")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao siet recipe [-config recipe.json] [-why] [-json]\n\nThe GRPO step as it is configured, checked before anything runs under it.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	recipe, ok := sietConfig(stderr, *config)
	if !ok {
		return 1
	}
	return sietRecipe(stdout, recipe, *asJSON, *why)
}

func runSietRead(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("siet read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	config := fs.String("config", "", "read the configuration from a JSON file rather than taking the plan's")
	specialist := fs.String("specialist", "", "which specialist the log came off")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao siet read [-config recipe.json] [-specialist name] [-json] steps.jsonl\n\nA training log read against the configuration it was taken under.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	recipe, ok := sietConfig(stderr, *config)
	if !ok {
		return 1
	}
	return sietRead(stdout, stderr, recipe, *specialist, fs.Arg(0), *asJSON)
}

type sietRow struct {
	Element string `json:"element"`
	Setting string `json:"setting"`
	Why     string `json:"why"`
}

type sietRecipeReport struct {
	Recipe   siet.Recipe `json:"recipe"`
	Rows     []sietRow   `json:"rows"`
	Context  int         `json:"context"`
	Holds    bool        `json:"holds"`
	Blocking []string    `json:"blocking,omitempty"`
}

func sietRecipe(stdout io.Writer, r siet.Recipe, asJSON, why bool) int {
	report := sietRecipeReport{
		Recipe:   r,
		Context:  siet.Context,
		Holds:    r.Holds(),
		Blocking: r.Blocking(),
	}
	for _, row := range r.Rows() {
		report.Rows = append(report.Rows, sietRow(row))
	}

	if asJSON {
		if code := printJSON(stdout, stdout, report); code != 0 {
			return code
		}
	} else {
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprint(tw, "element\tsetting\n")
		for _, row := range report.Rows {
			fmt.Fprintf(tw, "%s\t%s\n", row.Element, row.Setting)
		}
		_ = tw.Flush()

		if why {
			fmt.Fprintln(stdout)
			for _, row := range report.Rows {
				fmt.Fprintf(stdout, "%s: %s.\n", row.Element, row.Why)
			}
		}

		if len(report.Blocking) > 0 {
			fmt.Fprint(stdout, "\nThis configuration is not what it says it is:\n")
			for _, w := range report.Blocking {
				fmt.Fprintf(stdout, "  %s\n", w)
			}
		} else {
			fmt.Fprintf(stdout, "\nA prompt of %d and an answer of %d sit inside the %d the base model has, and the settings above are the ones the plan fixed.\n",
				r.Prompt, r.MaxResponse, siet.Context)
		}
	}

	if !report.Holds {
		return 2
	}
	return 0
}

type sietRunReport struct {
	Specialist string `json:"specialist,omitempty"`
	Box        string `json:"box,omitempty"`
	Steps      int    `json:"steps"`

	Yield float64 `json:"yield"`
	Late  float64 `json:"late_yield"`

	EntropyStart float64 `json:"entropy_start"`
	EntropyNow   float64 `json:"entropy_now"`
	RewardStart  float64 `json:"reward_start"`
	RewardNow    float64 `json:"reward_now"`

	Truncated float64 `json:"truncated"`
	Binds     bool    `json:"upper_bound_binds"`
	Fills     bool    `json:"batch_fills"`
	Needed    float64 `json:"oversample_needed"`

	Window   int      `json:"window"`
	Holds    bool     `json:"holds"`
	Faults   []string `json:"faults,omitempty"`
	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func sietRead(stdout, stderr io.Writer, recipe siet.Recipe, specialist, path string, asJSON bool) int {
	r, err := siet.ReadRun(specialist, path, recipe)
	if err != nil {
		fmt.Fprintf(stderr, "gao siet: %v\n", err)
		return 1
	}

	entropyStart, entropyNow := r.Entropy()
	rewardStart, rewardNow := r.Reward()
	report := sietRunReport{
		Specialist:   r.Specialist,
		Box:          r.Box(),
		Steps:        len(r.Steps),
		Yield:        r.Yield(),
		Late:         r.Late(),
		EntropyStart: entropyStart,
		EntropyNow:   entropyNow,
		RewardStart:  rewardStart,
		RewardNow:    rewardNow,
		Truncated:    r.Truncation(),
		Binds:        r.Binds(),
		Fills:        r.Fills(),
		Needed:       r.Needed(),
		Window:       siet.Window,
		Holds:        r.Holds(),
		Faults:       r.Faults(),
		Blocking:     r.Blocking(),
		Verdict:      r.Verdict(),
	}

	if asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printSiet(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

func printSiet(w io.Writer, r sietRunReport) {
	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "This log is not one training run, so nothing in it is read:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "reading\tfirst %d steps\tlast %d steps\n", r.Window, r.Window)
	fmt.Fprintf(tw, "reward\t%.3f\t%.3f\n", r.RewardStart, r.RewardNow)
	fmt.Fprintf(tw, "entropy\t%.3f\t%.3f\n", r.EntropyStart, r.EntropyNow)
	fmt.Fprintf(tw, "groups that taught\t%s\t%s\n", percent(r.Yield), percent(r.Late))
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s, %s on %s.\n", whose(r.Specialist), plural(r.Steps, "step"), r.Box)
	fmt.Fprintf(w, "%s of rollouts hit the length limit, the upper clip bound %s, and the batch %s at the yield the run is at now.\n",
		percent(r.Truncated), bound(r.Binds), fills(r.Fills, r.Needed))

	if len(r.Faults) > 0 {
		fmt.Fprintf(w, "\n%s to read before the reward is:\n", count(len(r.Faults), "thing"))
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// whose is what the run is called when nobody said which specialist it came
// off, since a log with no name on it is still a log worth reading.
func whose(s string) string {
	if s == "" {
		return "an unnamed specialist"
	}
	return s
}

func bound(binds bool) string {
	if binds {
		return "clipped tokens rather than sitting unused"
	}
	return "never bound anything"
}

func fills(ok bool, needed float64) string {
	if ok {
		return "fills"
	}
	return fmt.Sprintf("wants %.1fx sampling to fill", needed)
}
