package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"

	"github.com/tamnd/gao/nhat"
)

func runNhat(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("pick", flag.ContinueOnError)
	fs.SetOutput(stderr)
	list := fs.String("list", "", "the benchmark list, which is the roster with every item's text filled in")
	roster := fs.String("roster", "", "check the list against this roster instead of the one in the repository")
	benchmarks := fs.Bool("benchmarks", false, "print the roster and stop")
	show := fs.Int("show", 0, "list this many of the documents that were flagged")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao pick -benchmarks [-roster file] [-json]
       gao pick -list file [-roster file] [-show n] [-json] file...

Decontaminate: find the documents that hold the text of a benchmark gao is
judged on. A model trained on a corpus holding its own test set scores well and
has learned nothing.

The check is 13-gram exact overlap over the deduplication key, so a benchmark
item and a copy of it that changed the quotes, the capitals or the i and y
spelling are the same text here. A document that shares one window with a
benchmark is reported, and one that shares three is removed, because windows
overlap and three of them is one run of fifteen syllables rather than three
separate coincidences.

Thirteen syllables of Vietnamese is about eight words, so this is a stricter
check than the English one the number is borrowed from. That is the direction to
err in: a false flag costs one person reading one document, and a miss costs a
published score that is not real.

With -benchmarks it prints the roster, which is the versioned record of what gao
is judged on and the file the only-grows rule applies to. The roster carries the
names and revisions, and a list carries the items, because the items are tens of
megabytes of other people's test sets and belong in a build artifact rather than
in the repository. A list is checked against the roster before the scan starts,
so a benchmark that failed to fetch fails the run instead of coming back clean.

Contamination is reported, never hidden. A benchmark found in the corpus is
reported as contaminated and stays in the eval table with the annotation, and
every benchmark on the roster gets a row whether anything touched it or not.
Finding contamination is not an error and this exits zero when it does.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *show < 0 {
		fmt.Fprintf(stderr, "gao pick: -show is how many flagged documents to list, so it is not %d\n", *show)
		return 2
	}

	ros, err := readRoster(*roster)
	if err != nil {
		fmt.Fprintf(stderr, "gao pick: %v\n", err)
		return 1
	}

	if *benchmarks {
		if *list != "" || len(fs.Args()) > 0 {
			fmt.Fprintln(stderr, "gao pick: -benchmarks prints the roster and stops, so it takes no list and no files")
			return 2
		}
		if *asJSON {
			return printJSON(stdout, stderr, ros)
		}
		printRoster(stdout, ros)
		return 0
	}

	if *list == "" {
		fmt.Fprintln(stderr, "gao pick: nothing to check against. Give it -list, or -benchmarks to see the roster")
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(stderr, "gao pick: nothing to check. Give it parquet parts or text files")
		return 2
	}

	l, err := nhat.ReadList(*list)
	if err != nil {
		fmt.Fprintf(stderr, "gao pick: %v\n", err)
		return 1
	}
	// Before the scan rather than after it, because a list missing a benchmark
	// produces exactly the report a clean benchmark produces.
	if err := l.Covers(ros); err != nil {
		fmt.Fprintf(stderr, "gao pick: %v\n", err)
		return 1
	}
	x, err := nhat.NewIndex(l)
	if err != nil {
		fmt.Fprintf(stderr, "gao pick: %v\n", err)
		return 1
	}

	tally := nhat.NewTally(x)
	var flagged []nhatDocument
	for _, name := range files {
		i := 0
		if err := eachDocument(name, func(text string) error {
			r := x.Check(text)
			tally.Add(x, text, r)
			if r.Flagged() && len(flagged) < *show {
				flagged = append(flagged, nhatDocument{Document: row(name, i), Result: r})
			}
			i++
			return nil
		}); err != nil {
			fmt.Fprintf(stderr, "gao pick: %v\n", err)
			return 1
		}
	}

	run := nhatRun{Roster: ros.Version, List: l.Version, Grams: x.Grams(), Tally: tally, Flagged: flagged}
	if *asJSON {
		return printJSON(stdout, stderr, run)
	}
	printNhat(stdout, run)
	return 0
}

// readRoster is the one in the repository unless another was named. The
// repository one is the default because a run that has to be told what gao is
// judged on is a run that can be told less than the truth.
func readRoster(path string) (nhat.Roster, error) {
	if path == "" {
		return nhat.Rostered()
	}
	return nhat.ReadRoster(path)
}

// row names a document while the file is still being streamed, so a part is
// named by the row inside it and a text file by itself. It cannot use label,
// which wants to know how many documents the file holds, and knowing that costs
// a second pass over a part.
func row(name string, i int) string {
	if filepath.Ext(name) != ".parquet" {
		return name
	}
	return fmt.Sprintf("%s#%d", name, i)
}

type nhatDocument struct {
	Document string      `json:"document"`
	Result   nhat.Result `json:"result"`
}

type nhatRun struct {
	Roster  string         `json:"roster"`
	List    string         `json:"list"`
	Grams   int            `json:"index_grams"`
	Tally   *nhat.Tally    `json:"tally"`
	Flagged []nhatDocument `json:"flagged,omitempty"`
}

func printRoster(w io.Writer, ros nhat.Roster) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "benchmark\torigin\trevision\thome\tdrops at\tsource\n")
	for _, e := range ros.Benchmarks {
		held := fmt.Sprintf("%d windows", nhat.DropAt)
		if e.HeldOut {
			held = "1 window, held out"
		}
		home := e.Home
		if home == "" {
			home = "none"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Name, e.Origin, shortRevision(e.Version), home, held, e.Source)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\nRoster %s, %d benchmarks. It only grows.\n", ros.Version, len(ros.Benchmarks))
	blocking := ros.Blocking()
	if len(blocking) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%d of them have no revision pinned. A release cannot go out until they do, because a release note that says a benchmark was checked has to say which revision of it was checked.\n", len(blocking))
	for _, b := range blocking {
		fmt.Fprintf(w, "\n%s\n", b)
	}
}

func printNhat(w io.Writer, run nhatRun) {
	t := run.Tally
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "benchmark\torigin\trevision\titems\tfound\tshare\tdocuments\tdropped\n")
	for _, b := range t.Benchmarks {
		origin := b.Origin
		if b.HeldOut {
			origin += ", held out"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\t%d\t%d\n",
			b.Benchmark, origin, b.Version, b.Items, b.ItemsTouched, percent(b.ItemShare()), b.Documents, b.Dropped)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%d documents checked against %d benchmarks over %d windows, roster %s, list %s.\n",
		t.Documents, len(t.Benchmarks), run.Grams, run.Roster, run.List)
	if !t.Contaminated() {
		fmt.Fprint(w, "Nothing was found. Every benchmark above has a row saying so, because a table holding only the contaminated ones cannot be read as a clean bill of health for the rest.\n")
		return
	}
	fmt.Fprintf(w, "%d documents share text with a benchmark and %d of them share enough to be removed.\n", t.Flagged, t.Dropped)
	fmt.Fprint(w, "The contaminated benchmarks stay in the eval table with the contamination written next to them. Dropping one quietly is how contaminated scores become published scores.\n")

	for _, d := range run.Flagged {
		fmt.Fprintf(w, "\n%s, %d of %d windows in the list\n", d.Document, d.Result.Hits, d.Result.Grams)
		for _, b := range d.Result.Benchmarks {
			verdict := "reported"
			if b.Dropped {
				verdict = "dropped"
			}
			fmt.Fprintf(w, "  %-16s %s from %s, %s\n",
				b.Benchmark, count(b.Grams, "window"), count(b.Items, "item"), verdict)
		}
	}
}

// count is a number and its noun, made to read.
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
