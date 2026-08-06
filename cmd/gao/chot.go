package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/chot"
	"github.com/tamnd/gao/nhat"
)

func runChot(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		chotUsage(stderr)
		return 2
	}
	switch args[0] {
	case "harness":
		return runChotHarness(stdout, stderr, args[1:])
	case "digest":
		return runChotDigest(stdout, stderr, args[1:])
	case "audit":
		return runChotAudit(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		chotUsage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "gao chot: unknown subcommand %q\n", args[0])
	chotUsage(stderr)
	return 2
}

func chotUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao chot <harness | digest | audit> [flags]

The evaluation harness, closed before any result exists.

Chốt sổ is to close the ledger. The continued pretraining slice compares three
arms, one of which is gao's own corpus, and the person running that comparison
is the person who wants gao to win. So everything that decides what the
comparison says is written down and hashed first: the benchmarks, the prompts
verbatim, the shot counts and the seed they are drawn with, the metric, and the
rule for taking an answer out of the output.

The digest is the enforcement. A published result carries the digest of the
harness it was scored under, and two result sets whose digests differ were not
measuring the same thing whatever their columns are called.

subcommands:
  harness   print the harness, and what stands between it and a reproducible run
  digest    print the digest every result has to carry
  audit     check a set of results against the harness they claim to come from

run 'gao chot <subcommand> -h' for the flags of one.
`)
}

func runChotHarness(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("chot harness", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	prompts := fs.Bool("prompts", false, "print each task's prompt, which is part of the measurement")
	path := fs.String("harness", "", "read the harness from a file instead of the one in the repository")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao chot harness [-prompts] [-json] [-harness file]\n\nPrints the closed harness.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	h, code := readChotHarness(stderr, *path)
	if code != 0 {
		return code
	}
	roster, err := nhat.Rostered()
	if err != nil {
		fmt.Fprintf(stderr, "gao chot: %v\n", err)
		return 1
	}

	if *asJSON {
		return printJSON(stdout, stderr, chotHarnessReport{
			Harness:  h,
			Digest:   h.Digest().String(),
			Faults:   h.Against(roster),
			Unpinned: h.Unpinned(roster),
		})
	}

	fmt.Fprintf(stdout, "harness %s, closed against roster %s\n%s\n\n", h.Version, h.Roster, h.Digest())
	fmt.Fprintf(stdout, "arms, named before any of them was trained:\n")
	for _, a := range h.Arms {
		fmt.Fprintf(stdout, "  %s\n", a)
	}
	fmt.Fprintln(stdout)

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "benchmark\torigin\tmetric\tshots\tseed\tanswer from\trevision\n")
	for _, t := range h.Tasks {
		e, _ := chotEntry(roster, t.Benchmark)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			t.Benchmark, dashIfEmpty(e.Origin), t.Metric, t.Shots, seedOrDash(t.Seed), t.Extract, revision(e))
	}
	_ = tw.Flush()

	fmt.Fprintf(stdout, "\n%d tasks over %d arms, so this harness promises %d numbers.\n",
		len(h.Tasks), len(h.Arms), len(h.Tasks)*len(h.Arms))

	if faults := h.Against(roster); len(faults) > 0 {
		fmt.Fprintln(stdout, "\nThe harness and the roster do not agree:")
		for _, f := range faults {
			fmt.Fprintf(stdout, "  %s\n", f)
		}
	}
	if un := h.Unpinned(roster); len(un) > 0 {
		fmt.Fprintf(stdout, "\n%d of these run on a benchmark whose revision the roster has not pinned:\n  %s\nA result on an unpinned benchmark is a number nobody else can reproduce, so these are what stands between this harness and a published comparison.\n",
			len(un), strings.Join(un, ", "))
	}

	if *prompts {
		fmt.Fprint(stdout, "\nThe prompts, which are part of the measurement rather than a detail of it.\n")
		for _, t := range h.Tasks {
			fmt.Fprintf(stdout, "\n%s, %d shot\n", t.Benchmark, t.Shots)
			for _, line := range strings.Split(t.Prompt, "\n") {
				fmt.Fprintf(stdout, "  | %s\n", line)
			}
			if t.Note != "" {
				fmt.Fprintf(stdout, "  %s\n", t.Note)
			}
		}
	}
	return 0
}

func runChotDigest(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("chot digest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("harness", "", "read the harness from a file instead of the one in the repository")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao chot digest [-harness file]\n\nPrints the digest every result produced under this harness has to carry.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	h, code := readChotHarness(stderr, *path)
	if code != 0 {
		return code
	}
	fmt.Fprintln(stdout, h.Digest())
	return 0
}

func runChotAudit(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("chot audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	path := fs.String("harness", "", "read the harness from a file instead of the one in the repository")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao chot audit [-json] [-harness file] results.json

Check a set of results against the harness they claim to come from.

A missing number fails as loudly as an extra one. Adding a benchmark after
seeing the numbers is the fault everybody names, and dropping one is the same
fault in the other direction, done more often, and easier to explain away as a
run that did not finish.

Exits 1 if the results and the harness disagree.

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

	h, code := readChotHarness(stderr, *path)
	if code != 0 {
		return code
	}
	results, err := readChotResults(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao chot: %v\n", err)
		return 1
	}

	audit := h.Audit(results)
	if *asJSON {
		if code := printJSON(stdout, stderr, audit); code != 0 {
			return code
		}
	} else {
		printChotAudit(stdout, h, results, audit)
	}
	if audit.OK() {
		return 0
	}
	return 1
}

type chotHarnessReport struct {
	Harness  chot.Harness `json:"harness"`
	Digest   string       `json:"digest"`
	Faults   []string     `json:"faults,omitempty"`
	Unpinned []string     `json:"unpinned,omitempty"`
}

func readChotHarness(stderr io.Writer, path string) (chot.Harness, int) {
	if path == "" {
		h, err := chot.Fixed()
		if err != nil {
			fmt.Fprintf(stderr, "gao chot: %v\n", err)
			return chot.Harness{}, 1
		}
		return h, 0
	}
	h, err := chot.Read(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao chot: %v\n", err)
		return chot.Harness{}, 1
	}
	return h, 0
}

// readChotResults reads one result per line, which is the shape every other
// per-run file in this repository has, so that a run can append to it.
func readChotResults(path string) ([]chot.Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []chot.Result
	for i, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r chot.Result
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no results", path)
	}
	return out, nil
}

func printChotAudit(w io.Writer, h chot.Harness, results []chot.Result, a chot.Audit) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "benchmark\tmetric")
	for _, arm := range h.Arms {
		fmt.Fprintf(tw, "\t%s", arm)
	}
	fmt.Fprint(tw, "\twon by\n")

	table := h.Table(results)
	for i, t := range h.Tasks {
		fmt.Fprintf(tw, "%s\t%s", t.Benchmark, t.Metric)
		for _, score := range table[i] {
			if score == nil {
				fmt.Fprint(tw, "\t.")
				continue
			}
			fmt.Fprintf(tw, "\t%.3f", *score)
		}
		winner, ok := h.Winner(t, results)
		if !ok {
			winner = "."
		}
		fmt.Fprintf(tw, "\t%s\n", winner)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\nharness %s, %s\n", a.Version, a.Harness)
	fmt.Fprintf(w, "%d of the %d numbers this harness promised were reported.\n", a.Reported, a.Promised)

	if a.OK() {
		fmt.Fprint(w, "The results are the ones the harness asked for, which is what makes them comparable to anything else scored under it.\n")
		return
	}
	fmt.Fprintf(w, "\n%s:\n", plural(len(a.Faults), "fault"))
	for _, f := range a.Faults {
		fmt.Fprintf(w, "  %s\n", f)
	}
}

func chotEntry(r nhat.Roster, name string) (nhat.Entry, bool) {
	for _, e := range r.Benchmarks {
		if e.Name == name {
			return e, true
		}
	}
	return nhat.Entry{}, false
}

func revision(e nhat.Entry) string {
	switch {
	case e.Version == "":
		return "not on the roster"
	case e.Version == nhat.Unpinned:
		return nhat.Unpinned
	case len(e.Version) > 12:
		return e.Version[:12]
	}
	return e.Version
}

func seedOrDash(seed int64) string {
	if seed == 0 {
		return "."
	}
	return fmt.Sprint(seed)
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "."
	}
	return s
}
