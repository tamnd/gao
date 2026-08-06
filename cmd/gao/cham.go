package main

// Running the verifiers the reinforcement learning stage is trained against.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tamnd/gao/cham"
	"github.com/tamnd/gao/doc"
)

func runCham(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		chamUsage(stderr)
		return 2
	}
	switch args[0] {
	case "roster":
		return runChamRoster(stdout, stderr, args[1:])
	case "dau":
		return runChamDau(stdout, stderr, args[1:])
	case "trich":
		return runChamTrich(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		chamUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao cham: unknown subcommand %q\n", args[0])
		chamUsage(stderr)
		return 2
	}
}

func chamUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao cham <subcommand> [flags] [files...]

Grades sampled answers against something that can be checked.

There is no reward model here. A reward model is a second model whose mistakes
become the first model's objective, and in Vietnamese, where the preference data
would itself be translated, those mistakes would be systematic rather than
random. So every specialist is trained against a program that says whether an
answer is right, and every one of those programs is in this repository. An
unpublished verifier is an unfalsifiable reward.

subcommands:
  roster  the seven specialists, including the ones not built yet
  dau     grade diacritic restoration rollouts against the pages they came from
  trich   grade legal citation rollouts against a register of instruments

run 'gao cham <subcommand> -h' for the flags of a single subcommand.
`)
}

func runChamRoster(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("cham roster", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the roster as JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao cham roster [-json]

Prints the seven arms of the reinforcement learning stage, what each is asked to
do, what its reward is computed from, and where its ground truth comes from.

The arms that are specified and not written are printed too. A roster that only
listed the built verifiers would make the unbuilt ones invisible, and what is
missing from a reward stack is the part worth knowing about.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	roster := cham.Specialists()
	if *asJSON {
		return printJSON(stdout, stderr, roster)
	}

	written := 0
	for _, s := range roster {
		mark := "not written"
		if s.Written {
			mark = "written"
		} else {
			written++
		}
		fmt.Fprintf(stdout, "%s (%s)\n", s.Name, mark)
		fmt.Fprintf(stdout, "  task     %s\n", s.Task)
		fmt.Fprintf(stdout, "  checked  %s\n", s.Checked)
		fmt.Fprintf(stdout, "  source   %s\n\n", s.Source)
	}
	fmt.Fprintf(stdout, "%d of %d verifiers are specified and not built\n", written, len(roster))
	return 0
}

// A chamRollouts is one prompt and everything sampled from it.
type chamRollouts struct {
	// Prompt is the question. For dau it is the page with its marks off, which
	// is what the key is dictated on.
	Prompt string `json:"prompt"`

	// Must is what an answer has to rest on, for the arms whose key is per
	// prompt rather than per corpus.
	Must []string `json:"must,omitempty"`

	// Answers is what the model produced.
	Answers []string `json:"answers"`

	// Overlong marks the answers the sampler stopped at the token limit, in the
	// same order. The sampler has to say so, because most verifiers cannot tell
	// a truncated answer from a bad one by looking at it.
	Overlong []bool `json:"overlong,omitempty"`
}

func (r chamRollouts) cutOff(i int) bool {
	return i < len(r.Overlong) && r.Overlong[i]
}

// readChamRollouts reads a rollout file, one prompt per line.
func readChamRollouts(path string) ([]chamRollouts, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []chamRollouts
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		if len(s.Bytes()) == 0 {
			continue
		}
		var r chamRollouts
		if err := json.Unmarshal(s.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, r)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no rollouts", path)
	}
	return out, nil
}

// A chamReport is one graded rollout file.
type chamReport struct {
	Specialist string      `json:"specialist"`
	Batch      cham.Batch  `json:"batch"`
	Groups     []chamGroup `json:"groups"`
}

// A chamGroup is one prompt graded, in the form the sample log takes.
type chamGroup struct {
	Prompt    string         `json:"prompt"`
	Sampled   int            `json:"sampled"`
	Checked   int            `json:"checked"`
	Mean      float64        `json:"mean"`
	Deviation float64        `json:"deviation"`
	Teaches   bool           `json:"teaches"`
	Why       string         `json:"why,omitempty"`
	Rollouts  []cham.Rollout `json:"rollouts"`
}

// gradeCham runs one verifier over a rollout file and folds the result up.
func gradeCham(v cham.Verifier, sets []chamRollouts) chamReport {
	report := chamReport{Specialist: v.Specialist()}
	for _, set := range sets {
		g := cham.NewGroup(v.Specialist(), set.Prompt)
		for i, answer := range set.Answers {
			if set.cutOff(i) {
				g.Add(answer, cham.Overlong(v.Specialist()))
				continue
			}
			g.Add(answer, v.Verify(set.Prompt, answer))
		}
		report.Batch.Add(g)
		teaches, why := g.Teaches()
		report.Groups = append(report.Groups, chamGroup{
			Prompt:    set.Prompt,
			Sampled:   g.Sampled(),
			Checked:   g.Checked(),
			Mean:      g.Mean(),
			Deviation: g.Deviation(),
			Teaches:   teaches,
			Why:       why,
			Rollouts:  g.Rollouts(),
		})
	}
	return report
}

// printCham writes a graded rollout file the way it goes into a training log,
// and reports whether the run bought anything.
func printCham(stdout io.Writer, report chamReport, verbose bool) int {
	if verbose {
		for _, g := range report.Groups {
			fmt.Fprintf(stdout, "%s\n", ellipsis(g.Prompt, 72))
			fmt.Fprintf(stdout, "  %d rollouts, %d checked, mean %.3f, spread %.3f\n",
				g.Sampled, g.Checked, g.Mean, g.Deviation)
			if !g.Teaches {
				fmt.Fprintf(stdout, "  dropped: %s\n", g.Why)
			}
			for _, r := range g.Rollouts {
				fmt.Fprintf(stdout, "  %+.2f  %s\n", r.Advantage, r.Verdict)
			}
			fmt.Fprintln(stdout)
		}
	}
	fmt.Fprintf(stdout, "%s: %s\n", report.Specialist, report.Batch)
	if report.Batch.Kept == 0 {
		fmt.Fprintln(stdout, "no group produced a gradient, so this batch cost a forward pass and bought nothing")
		return 1
	}
	return 0
}

// ellipsis cuts a prompt down to a log line without splitting a character.
func ellipsis(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func runChamDau(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("cham dau", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rollouts := fs.String("rollouts", "", "the sampled answers to grade, one prompt per line")
	asJSON := fs.Bool("json", false, "print the graded batch as JSON")
	verbose := fs.Bool("v", false, "print every group and every rollout, which is the sample log")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao cham dau -rollouts FILE <corpus file...>

Grades diacritic restoration rollouts. The key is built from the corpus files
given here: each page is dictated as its own answer, and the prompt is that page
with the marks off. This is the arm that costs no annotator.

The reward is the share of the page's marks that came back. It is not character
accuracy, which starts around 76% for a model that does nothing, because only
about a quarter of Vietnamese characters carry a mark.

An answer that changed the letters rather than only the marks scores zero rather
than being aligned against the page, since a specialist that can collect partial
credit for a paraphrase will learn to paraphrase.

An answer that is the beginning of the right page and then nothing is not
graded. That is a rollout the sampler cut off, not a wrong restoration, and
scoring it zero teaches the model to answer briefly.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 || *rollouts == "" {
		fs.Usage()
		return 2
	}

	sets, err := readChamRollouts(*rollouts)
	if err != nil {
		fmt.Fprintf(stderr, "gao cham dau: %v\n", err)
		return 1
	}

	v := cham.NewDau()
	refused := 0
	for _, name := range fs.Args() {
		err := eachIdentifiedDocument(name, func(_ doc.Hash, text string) error {
			if !v.Learn(text) {
				refused++
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gao cham dau: %s: %v\n", name, err)
			return 1
		}
	}
	if v.Items() == 0 {
		fmt.Fprintf(stderr, "gao cham dau: the key holds no pages, and %d were refused for being typed without marks\n", refused)
		return 1
	}

	report := gradeCham(v, sets)
	if *asJSON {
		return printJSON(stdout, stderr, report)
	}
	fmt.Fprintf(stdout, "the key holds %d pages and refused %d\n\n", v.Items(), refused)
	return printCham(stdout, report, *verbose)
}

func runChamTrich(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("cham trich", flag.ContinueOnError)
	fs.SetOutput(stderr)
	registry := fs.String("register", "", "the instruments that exist, one JSON object per line with kind, id, and articles")
	asJSON := fs.Bool("json", false, "print the graded batch as JSON")
	verbose := fs.Bool("v", false, "print every group and every rollout, which is the sample log")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao cham trich -register FILE <rollouts.jsonl>

Grades legal citation rollouts. Each line of the rollout file carries the
prompt, the instruments an answer has to rest on in its "must" field, and the
sampled answers.

Vietnamese legal drafting numbers instruments to a fixed form, which is the only
reason this arm is checkable: a document is a number, a year, and a code that
says who issued it. Only the Government issues a nghị định, so a nghị định whose
code is not NĐ-CP is wrong however plausible it reads, and that is the exact
shape a hallucinated citation takes.

The reward is the harmonic mean of precision and recall, because either one
alone is trivially collected. Precision alone is had by citing one safe
instrument in every answer. Recall alone is had by listing the whole register.
The arm exists to remove invented citations without teaching the model to stop
citing, and only the pair does that.

A required instrument that is not in the register is refused rather than graded
against, since an unwinnable item is a group where every rollout scores the same
and a step that teaches nothing still costs a pass.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *registry == "" {
		fs.Usage()
		return 2
	}

	reg, err := readChamRegister(*registry)
	if err != nil {
		fmt.Fprintf(stderr, "gao cham trich: %v\n", err)
		return 1
	}
	sets, err := readChamRollouts(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao cham trich: %v\n", err)
		return 1
	}

	v := cham.NewTrich(reg)
	var graded []chamRollouts
	for _, set := range sets {
		if !v.Ask(set.Prompt, set.Must...) {
			fmt.Fprintf(stderr, "gao cham trich: %q asks for something the register does not hold, so no answer to it could win\n",
				ellipsis(set.Prompt, 60))
			return 1
		}
		graded = append(graded, set)
	}

	report := gradeCham(v, graded)
	if *asJSON {
		return printJSON(stdout, stderr, report)
	}
	fmt.Fprintf(stdout, "the register holds %d instruments and the key %d prompts\n\n", reg.Size(), v.Items())
	return printCham(stdout, report, *verbose)
}

// readChamRegister reads the instruments that exist.
func readChamRegister(path string) (*cham.Register, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	reg := cham.NewRegister()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for s.Scan() {
		line++
		if len(s.Bytes()) == 0 {
			continue
		}
		var e struct {
			Kind     string `json:"kind"`
			ID       string `json:"id"`
			Articles int    `json:"articles"`
		}
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if !reg.Add(e.Kind, e.ID, e.Articles) {
			return nil, fmt.Errorf("%s:%d: %q is not an identifier a %q can have, and a register holding it could not catch one",
				path, line, e.ID, e.Kind)
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if reg.Size() == 0 {
		return nil, fmt.Errorf("%s holds no instruments, so every citation would be invented", path)
	}
	return reg, nil
}
