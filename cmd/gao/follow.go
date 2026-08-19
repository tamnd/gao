package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/follow"
)

func runFollow(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		followUsage(stderr)
		return 2
	}
	switch args[0] {
	case "items":
		return runFollowItems(stdout, stderr, args[1:])
	case "grade":
		return runFollowGrade(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		followUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao follow: no subcommand named %s\n", args[0])
		followUsage(stderr)
		return 2
	}
}

func followUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao follow <command> [flags]

vi-adherence: whether the answer follows the question into the language it was
asked in.

A model trained mostly on English and finetuned into Vietnamese answers a
Vietnamese question in English often enough that anybody who has used one has
seen it. It is the first thing a user notices and the last thing a benchmark
suite measures, because every standard evaluation scores the content of an
answer and none of them score the language it arrived in.

The whole answer is read rather than the top of it. Drift is the normal shape
of the failure: the first paragraph comes back in Vietnamese and the model
finishes in English, which scores perfectly against anything that samples the
opening. An answer in English and an answer in Vietnamese with the tone marks
left off are counted apart, because they are different bugs.

commands:
  items   print the set and its digest, or check one from a file
  grade   score a model's replies against the set they answer
`)
}

func runFollowItems(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("follow items", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	prompts := fs.Bool("prompts", false, "print the prompts themselves rather than a line per kind")
	path := fs.String("set", "", "read the set from a file instead of the one this build ships")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao follow items [-prompts] [-json] [-set file]\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	s, code := readFollowSet(stderr, *path)
	if code != 0 {
		return code
	}
	faults := s.Faults()

	switch {
	case *asJSON:
		if code := printJSON(stdout, stderr, followItemsReport{Set: s, Digest: s.Digest().String(), Faults: faults}); code != 0 {
			return code
		}
	case *prompts:
		printFollowPrompts(stdout, s)
	default:
		printFollowKinds(stdout, s)
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

func runFollowGrade(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("follow grade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	path := fs.String("set", "", "read the set from a file instead of the one this build ships")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao follow grade [-json] [-set file] replies.jsonl

Score a model's replies against the set.

One reply per line, each naming the item it answers and carrying the response
verbatim. A reply may say what language it is in and that call wins over the
classifier; left unset, the classifier reads every sentence of the answer and
the score reports how many verdicts it made.

Exits 1 if the score may not be published as it stands, which includes an item
that never came back and an answer with nothing readable in it.

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

	s, code := readFollowSet(stderr, *path)
	if code != 0 {
		return code
	}
	replies, err := follow.ReadReplies(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao follow: %v\n", err)
		return 1
	}

	score := s.Grade(replies)
	out := followGradeReport{Score: score, Faults: append(s.Faults(), score.Publishable()...)}
	if *asJSON {
		if code := printJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		printFollowScore(stdout, s, out)
	}
	if len(out.Faults) > 0 || !score.Passed {
		return 1
	}
	return 0
}

type followItemsReport struct {
	Set    follow.Set `json:"set"`
	Digest string     `json:"digest"`
	Faults []string   `json:"faults,omitempty"`
}

type followGradeReport struct {
	follow.Score
	Faults []string `json:"faults,omitempty"`
}

func readFollowSet(stderr io.Writer, path string) (follow.Set, int) {
	if path == "" {
		return follow.Fixed(), 0
	}
	s, err := follow.ReadSet(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao follow: %v\n", err)
		return follow.Set{}, 1
	}
	return s, 0
}

func printFollowKinds(w io.Writer, s follow.Set) {
	fmt.Fprintf(w, "%s\n\n", s.Describe())

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "kind\titems\twants\twhy the first of them is in the set\n")
	for _, kind := range s.Kinds() {
		n, wants, why := 0, follow.Lang(""), ""
		for _, it := range s.Items {
			if it.Kind != kind {
				continue
			}
			n++
			if why == "" {
				wants, why = it.Wants, it.Why
			}
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", kind, n, wants, why)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\ndigest %s, published as %s\n", s.Digest(), follow.Repo)
	fmt.Fprintf(w, "Run 'gao follow items -prompts' for the prompts themselves.\n")
}

func printFollowPrompts(w io.Writer, s follow.Set) {
	fmt.Fprintf(w, "%s\n", s.Describe())
	for _, it := range s.Items {
		fmt.Fprintf(w, "\n%s  wants %s\n  %s\n  %s\n", it.ID, it.Wants, it.Prompt, it.Why)
	}
}

func printFollowScore(w io.Writer, s follow.Set, out followGradeReport) {
	sc := out.Score
	fmt.Fprintf(w, "%s\n\n", s.Describe())

	hw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(hw, "set\t%s\t%s\n", sc.Version, shortHash(sc.Set.String()))
	fmt.Fprintf(hw, "adherence\t%.3f\tagainst a floor of %.2f\n", sc.Adherence, follow.MinAdherence)
	fmt.Fprintf(hw, "answered elsewhere\t%d\tin a language the item did not ask for\n", sc.InOther)
	fmt.Fprintf(hw, "answered without marks\t%d\tagainst a ceiling of %.0f%%\n", sc.Bare, 100*follow.MaxBare)
	if sc.Drifted > 0 {
		fmt.Fprintf(hw, "drifted\t%d\tturning %.0f%% of the way in on average\n", sc.Drifted, 100*sc.DriftedAt)
	} else {
		fmt.Fprintf(hw, "drifted\t%d\t\n", sc.Drifted)
	}
	fmt.Fprintf(hw, "read by the classifier\t%d\tof %d items\n", sc.Detected, sc.Items)
	_ = hw.Flush()

	fmt.Fprint(w, "\nper kind:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "  kind\titems\tadherent\tdrifted\n")
	for _, k := range sc.ByKind {
		fmt.Fprintf(tw, "  %s\t%d\t%d\t%d\n", k.Kind, k.Items, k.Adherent, k.Drifted)
	}
	_ = tw.Flush()

	fmt.Fprintln(w)
	if sc.Passed {
		fmt.Fprintf(w, "pass: %.1f%% of the answers came back in the language the item asked for, end to end.\n", 100*sc.Adherence)
	} else {
		fmt.Fprintf(w, "fail: %.1f%% of the answers came back in the language the item asked for, against a floor of %.0f%%.\n",
			100*sc.Adherence, 100*follow.MinAdherence)
	}
	if sc.Drifted > 0 {
		fmt.Fprintf(w, "%s opened in the right language and did not stay there, which is the failure that any measure reading only the first paragraph would have missed.\n",
			plural(sc.Drifted, "answer"))
	}
	if sc.Bare > 0 {
		fmt.Fprintf(w, "%s came back in Vietnamese with the tone marks left off, which is a different problem from answering in English and is counted as one.\n",
			plural(sc.Bare, "answer"))
	}

	if len(out.Faults) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(out.Faults), "fault"))
		for _, f := range out.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
}
