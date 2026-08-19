package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/sow"
)

func runSow(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		sowUsage(stderr)
		return 2
	}
	switch args[0] {
	case "recipe":
		return runSowRecipe(stdout, stderr, args[1:])
	case "card":
		return runSowCard(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		sowUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao sow: no subcommand named %s\n", args[0])
		sowUsage(stderr)
		return 2
	}
}

func sowUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao sow <command> [flags]

The generator card for gao-synth, and the recipe it is written against.

Everything else in this project harvests. Past the edge of the deduplicated
Vietnamese web the only move left is to make text, and the mixture spends 150
billion tokens doing it. This is a rephrase pass over the educational slice, in
four registers, which is what makes it defensible: every output has a real
document behind it that a person wrote.

The recipe is fixed and hashed before a token exists. It holds the generator and
its revision, the four registers with their prompts verbatim, the decoding
settings and the seed, the gates with their config hashes, and the slice being
rephrased. The card is that recipe plus what happened, and it carries the recipe
digest, so a prompt improved after seeing the output produces a card that does
not match what it claims to be.

commands:
  recipe   print the recipe, or the prompts, and its digest
  card     check a generator card against the recipe it names
`)
}

func runSowRecipe(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("sow recipe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	prompts := fs.Bool("prompts", false, "print the prompts verbatim, which is what anybody reproducing this needs")
	path := fs.String("recipe", "", "read the recipe from a file instead of the one this build ships")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao sow recipe [-prompts] [-json] [-recipe file]\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	r, code := readSowRecipe(stderr, *path)
	if code != 0 {
		return code
	}

	if *asJSON {
		return printJSON(stdout, stderr, sowRecipeReport{Recipe: r, Digest: r.Digest().String(), Faults: sowFaults(r)})
	}
	if *prompts {
		printSowPrompts(stdout, r)
		return 0
	}
	printSowRecipe(stdout, r)
	if faults := sowFaults(r); len(faults) > 0 {
		fmt.Fprintf(stdout, "\n%s:\n", plural(len(faults), "fault"))
		for _, f := range faults {
			fmt.Fprintf(stdout, "  %s\n", f)
		}
		return 1
	}
	return 0
}

func runSowCard(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("sow card", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	path := fs.String("recipe", "", "read the recipe from a file instead of the one this build ships")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao sow card [-json] [-recipe file] dir

Check the generator card in dir against the recipe it names.

Exits 1 if the card and the recipe disagree, if the card's own arithmetic does
not close, or if what it describes may not be published.

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

	r, code := readSowRecipe(stderr, *path)
	if code != 0 {
		return code
	}
	c, err := sow.ReadCard(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao sow: %v\n", err)
		return 1
	}

	report := sowCardReport{
		Card:   c,
		Repo:   sow.Repo,
		Faults: append(c.Against(r), c.Publishable()...),
	}
	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printSowCard(stdout, r, report)
	}
	if len(report.Faults) > 0 {
		return 1
	}
	return 0
}

type sowRecipeReport struct {
	Recipe sow.Recipe `json:"recipe"`
	Digest string     `json:"digest"`
	Faults []string   `json:"faults,omitempty"`
}

type sowCardReport struct {
	Card   sow.Card `json:"card"`
	Repo   string   `json:"repo"`
	Faults []string `json:"faults,omitempty"`
}

func readSowRecipe(stderr io.Writer, path string) (sow.Recipe, int) {
	if path == "" {
		return sow.Fixed(), 0
	}
	r, err := sow.ReadRecipe(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao sow: %v\n", err)
		return sow.Recipe{}, 1
	}
	return r, 0
}

// sowFaults reports a recipe's own problems as lines, since the package returns
// them joined into one error and a report wants them one to a row.
func sowFaults(r sow.Recipe) []string {
	c := sow.Card{Recipe: r.Digest(), Version: r.Version, Box: ".", Batch: ".",
		Generated: 1, Kept: 1, Tokens: 1, Tokenizer: ".", GPUHours: 1}
	against := c.Against(r)
	faults := make([]string, 0, len(against))
	for _, f := range against {
		switch {
		case strings.Contains(f, "text nothing checked"),
			strings.Contains(f, "is in the recipe and the card says nothing about it"),
			strings.Contains(f, "when it ran"):
			continue // the stand-in card's problems, not the recipe's
		}
		faults = append(faults, f)
	}
	return faults
}

func printSowRecipe(w io.Writer, r sow.Recipe) {
	fmt.Fprintf(w, "%s\n\n", r.Describe())

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "generator\t%s@%s\n", r.Generator, r.Revision)
	fmt.Fprintf(tw, "read gao\t%s\n", yesNo(r.ReadGao))
	fmt.Fprintf(tw, "source\t%s at %s\n", r.Source, shortHash(r.SourceDigest.String()))
	fmt.Fprintf(tw, "target\t%s tokens\n", scale(r.Target))
	fmt.Fprintf(tw, "decoding\ttemperature %v, top_p %v, %d tokens, seed %d\n",
		r.Decoding.Temperature, r.Decoding.TopP, r.Decoding.MaxTokens, r.Decoding.Seed)
	fmt.Fprintf(tw, "roster\t%s\n", r.Roster)
	fmt.Fprintf(tw, "digest\t%s\n", r.Digest())
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s:\n", plural(len(r.Styles), "register"))
	sw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, s := range r.Styles {
		fmt.Fprintf(sw, "  %s\t%s\n", s.Name, s.Note)
	}
	_ = sw.Flush()

	fmt.Fprintf(w, "\n%s:\n", plural(len(r.Filters), "gate"))
	fw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, f := range r.Filters {
		fmt.Fprintf(fw, "  %s\t%s\t%s\n", f.Name, shortHash(f.ConfigHash.String()), f.Why)
	}
	_ = fw.Flush()

	if r.Note != "" {
		fmt.Fprintf(w, "\n%s\n", r.Note)
	}
}

func printSowPrompts(w io.Writer, r sow.Recipe) {
	fmt.Fprintf(w, "# %s@%s, temperature %v, top_p %v, seed %d\n",
		r.Generator, r.Revision, r.Decoding.Temperature, r.Decoding.TopP, r.Decoding.Seed)
	fmt.Fprintf(w, "# recipe %s\n", r.Digest())
	for _, s := range r.Styles {
		fmt.Fprintf(w, "\n## %s\n%s\n", s.Name, s.Prompt)
	}
}

func printSowCard(w io.Writer, r sow.Recipe, report sowCardReport) {
	c := report.Card
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "recipe\t%s\t%s\n", c.Version, shortHash(c.Recipe.String()))
	fmt.Fprintf(tw, "box\t%s\t%s\n", c.Box, c.Batch)
	fmt.Fprintf(tw, "generated\t%s\t\n", scale(c.Generated))
	fmt.Fprintf(tw, "kept\t%s\t%.1f%% of what came out\n", scale(c.Kept), 100*c.Yield())
	fmt.Fprintf(tw, "tokens\t%s\t%s\n", scale(c.Tokens), c.Tokenizer)
	fmt.Fprintf(tw, "cost\t%.1f GPU hours\t\n", c.GPUHours)
	_ = tw.Flush()

	fmt.Fprintf(w, "\nthe gates rejected %.1f%% of it:\n", 100*c.RejectRate())
	gw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	names := make([]string, 0, len(c.Rejects))
	for name := range c.Rejects {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		why := ""
		if f, ok := r.Filter(name); ok {
			why = f.Why
		}
		fmt.Fprintf(gw, "  %s\t%s\t%s\t%s\n", name, scale(c.Rejects[name]), share(c.Rejects[name], c.Generated), why)
	}
	_ = gw.Flush()

	fmt.Fprint(w, "\n")
	if c.Contaminated == 0 {
		fmt.Fprintf(w, "None of it carried a benchmark item from %s.\n", r.Roster)
	} else {
		fmt.Fprintf(w, "%s of it carried a benchmark item from %s.\n", plural(int(c.Contaminated), "document"), r.Roster)
	}
	fmt.Fprintf(w, "It goes to %s and nowhere else, and none of it is ever added to a natural count.\n", report.Repo)

	if len(report.Faults) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(report.Faults), "fault"))
		for _, f := range report.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
		return
	}
	fmt.Fprint(w, "The card matches the recipe it was produced under, and every document it generated is accounted for.\n")
}

func shortHash(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// scale prints a large number the way somebody reads it out loud, since the
// counts here run to billions and the digits stop being legible well before
// that.
func scale(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprint(n)
	}
}
