// Command gao builds, cleans, and publishes the gao Vietnamese corpus.
//
// The subcommands are named for the stages of rice processing, because rice
// processing and corpus processing turn out to be the same handful of verbs:
// harvest (gat), dry (phoi), sift (sang), mill (xay), and store (kho).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/tamnd/gao/doc"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// command is one subcommand of the gao binary.
type command struct {
	name  string
	short string
	run   func(stdout, stderr io.Writer, args []string) int
}

func commands() []command {
	return []command{
		{"box", "print the fleet inventory and the disk budget it implies", runBox},
		{"plan", "print the build plan: slices, gates, and kill criteria", runPlan},
		{"version", "print the version", runVersion},
		{"help", "print this help", nil}, // handled in main so it can see the table
	}
}

func main() {
	os.Exit(run(os.Stdout, os.Stderr, os.Args[1:]))
}

func run(stdout, stderr io.Writer, args []string) int {
	// -version is accepted as a flag as well as a subcommand, because release
	// tooling reaches for the flag and people reach for the subcommand.
	if len(args) == 1 && (args[0] == "-version" || args[0] == "--version") {
		return runVersion(stdout, stderr, nil)
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stdout)
		return 0
	}

	name := args[0]
	for _, c := range commands() {
		if c.name == name && c.run != nil {
			return c.run(stdout, stderr, args[1:])
		}
	}

	fmt.Fprintf(stderr, "gao: unknown command %q\n", name)
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, "gao builds the largest Vietnamese text corpus.\n\nusage: gao <command> [flags]\n\ncommands:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands() {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.short)
	}
	_ = tw.Flush()
	fmt.Fprint(w, "\nrun 'gao <command> -h' for the flags of a single command.\n")
}

func runVersion(stdout, _ io.Writer, _ []string) int {
	fmt.Fprintln(stdout, version)
	return 0
}

func runPlan(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	full := fs.Bool("full", false, "include the gate and kill criterion for each slice")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao plan [-full]\n\nPrints the build plan. Each slice ships something usable even if the next one never runs.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if !*full {
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, s := range doc.Slices {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.ID, s.Title, s.Ships)
		}
		_ = tw.Flush()
		return 0
	}

	for i, s := range doc.Slices {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "%s  %s\n", s.ID, s.Title)
		fmt.Fprintf(stdout, "  ships: %s\n", s.Ships)
		fmt.Fprintf(stdout, "  gate:  %s\n", s.Gate)
		fmt.Fprintf(stdout, "  kill:  %s\n", s.Kill)
	}
	return 0
}
