package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/ngai"
)

func runNgai(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		ngaiUsage(stderr)
		return 2
	}
	switch args[0] {
	case "items":
		return runNgaiItems(stdout, stderr, args[1:])
	case "grade":
		return runNgaiGrade(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		ngaiUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao hesitate: no subcommand named %s\n", args[0])
		ngaiUsage(stderr)
		return 2
	}
}

func ngaiUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao hesitate <command> [flags]

vi-overrefusal: whether a model will talk about Vietnam.

A model tuned on English safety data declines to discuss land reform, the boat
people, the 1979 border war, and what a folk remedy is for. Nothing in a harm
refusal score catches that, and the model looks safer the worse it gets, which
is why both numbers come off one set here and go out together.

Every item that should be answered has a twin that should be refused, on the
same topic, in the same register, sharing most of its vocabulary. That pairing
is what makes the third number possible: how often a model treated a pair
identically, which is the difference between making a judgment and matching a
string.

commands:
  items   print the set and its digest, or check one from a file
  grade   score a model's replies against the set they answer
`)
}

func runNgaiItems(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("hesitate items", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	pairs := fs.Bool("pairs", false, "print every pair rather than a line per topic")
	path := fs.String("set", "", "read the set from a file instead of the one this build ships")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao hesitate items [-pairs] [-json] [-set file]\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	s, code := readNgaiSet(stderr, *path)
	if code != 0 {
		return code
	}
	faults := s.Faults()

	switch {
	case *asJSON:
		if code := printJSON(stdout, stderr, ngaiItemsReport{Set: s, Digest: s.Digest().String(), Faults: faults}); code != 0 {
			return code
		}
	case *pairs:
		printNgaiPairs(stdout, s)
	default:
		printNgaiTopics(stdout, s)
	}

	if len(faults) > 0 {
		if !*asJSON {
			fmt.Fprintf(stdout, "\n%s:\n", plural(len(faults), "fault"))
			for _, f := range faults {
				fmt.Fprintf(stdout, "  %s\n", f)
			}
		}
		return 1
	}
	return 0
}

func runNgaiGrade(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("hesitate grade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	path := fs.String("set", "", "read the set from a file instead of the one this build ships")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao hesitate grade [-json] [-set file] replies.jsonl

Score a model's replies against the set.

One reply per line, each naming the item it answers and carrying the response
verbatim. A reply may say outright whether it was a refusal, and that call wins
over the detector; left unset, the detector reads the top of the response and
the score reports how many verdicts it made.

Exits 1 if the score may not be published as it stands, which includes an item
that never came back, a response that came back empty, and replies produced
against a different version of the set.

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

	s, code := readNgaiSet(stderr, *path)
	if code != 0 {
		return code
	}
	replies, err := ngai.ReadReplies(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao hesitate: %v\n", err)
		return 1
	}

	score := s.Grade(replies)
	out := ngaiGradeReport{Score: score, Faults: append(s.Faults(), score.Publishable()...)}
	if *asJSON {
		if code := printJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		printNgaiScore(stdout, s, out)
	}
	if len(out.Faults) > 0 || !score.Passed {
		return 1
	}
	return 0
}

type ngaiItemsReport struct {
	Set    ngai.Set `json:"set"`
	Digest string   `json:"digest"`
	Faults []string `json:"faults,omitempty"`
}

type ngaiGradeReport struct {
	ngai.Score
	Faults []string `json:"faults,omitempty"`
}

func readNgaiSet(stderr io.Writer, path string) (ngai.Set, int) {
	if path == "" {
		return ngai.Fixed(), 0
	}
	s, err := ngai.ReadSet(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao hesitate: %v\n", err)
		return ngai.Set{}, 1
	}
	return s, 0
}

func printNgaiTopics(w io.Writer, s ngai.Set) {
	fmt.Fprintf(w, "%s\n\n", s.Describe())

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "topic\tpairs\tthe line the pairs on it are drawn on\n")
	for _, topic := range s.Topics() {
		n, why := 0, ""
		for _, it := range s.Items {
			if it.Topic == topic && it.Kind == ngai.Answer {
				n++
				if why == "" {
					why = it.Why
				}
			}
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\n", topic, n, why)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\ndigest %s, published as %s\n", s.Digest(), ngai.Repo)
	fmt.Fprintf(w, "Run 'gao hesitate items -pairs' for the prompts themselves.\n")
}

func printNgaiPairs(w io.Writer, s ngai.Set) {
	fmt.Fprintf(w, "%s\n", s.Describe())
	for _, it := range s.Items {
		if it.Kind != ngai.Answer {
			continue
		}
		twin, ok := s.Lookup(it.Twin)
		if !ok {
			continue
		}
		fmt.Fprintf(w, "\n%s  answer  %s\n", it.ID, it.Prompt)
		fmt.Fprintf(w, "%s  refuse  %s\n", twin.ID, twin.Prompt)
		fmt.Fprintf(w, "%s          %s\n", strings.Repeat(" ", len(it.ID)), it.Why)
	}
}

func printNgaiScore(w io.Writer, s ngai.Set, out ngaiGradeReport) {
	sc := out.Score
	fmt.Fprintf(w, "%s\n\n", s.Describe())

	hw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(hw, "set\t%s\t%s\n", sc.Version, shortHash(sc.Set.String()))
	fmt.Fprintf(hw, "over refusal\t%.3f\tagainst a ceiling of %.2f\n", sc.OverRefusal, ngai.MaxOverRefusal)
	fmt.Fprintf(hw, "harm refusal\t%.3f\tagainst a floor of %.2f\n", sc.HarmRefusal, ngai.MinHarmRefusal)
	fmt.Fprintf(hw, "undiscriminating\t%.3f\t%d pairs refused on both halves, %d answered on both\n",
		sc.Undiscriminating, sc.BothRefused, sc.BothAnswered)
	fmt.Fprintf(hw, "read by the detector\t%d\tof %d items\n", sc.Detected, sc.Items)
	_ = hw.Flush()

	fmt.Fprint(w, "\nper topic, worst over refusal first:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "  topic\tanswerable\trefused\trate\tharmful\trefused\n")
	for _, t := range sc.Worst() {
		fmt.Fprintf(tw, "  %s\t%d\t%d\t%.3f\t%d\t%d\n",
			t.Topic, t.Answerable, t.OverRefused, t.Rate(), t.Harmful, t.Refused)
	}
	_ = tw.Flush()

	fmt.Fprintln(w)
	if sc.Passed {
		fmt.Fprintf(w, "pass: the model declined %.1f%% of what it should answer and %.1f%% of what it should not.\n",
			100*sc.OverRefusal, 100*sc.HarmRefusal)
	} else {
		fmt.Fprintf(w, "fail: the model declined %.1f%% of what it should answer and %.1f%% of what it should not, against a ceiling of %.0f%% and a floor of %.0f%%.\n",
			100*sc.OverRefusal, 100*sc.HarmRefusal, 100*ngai.MaxOverRefusal, 100*ngai.MinHarmRefusal)
	}
	if sc.Undiscriminating > 0 {
		fmt.Fprintf(w, "%.1f%% of the pairs were treated identically, which is the share of this set the model read as a keyword rather than as a request.\n",
			100*sc.Undiscriminating)
	}

	if len(out.Faults) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(out.Faults), "fault"))
		for _, f := range out.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
}
