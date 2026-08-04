package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/may"
	"github.com/tamnd/gao/vo"
)

func runGatHF(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("gat hf", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "the ingest directory, which is where the ledger lives")
	source := fs.String("source", "", "fetch one source rather than all of them, by name")
	limit := fs.Int("limit", 0, "stop after this many files, which is how a new box is tried out")
	plan := fs.Bool("plan", false, "print what would be fetched and fetch nothing")
	decode := fs.Bool("decode", false, "decode each file into documents and put them to the ingest contract")
	rejects := fs.String("rejects", "", "write the documents the contract turned away to this file, which implies -decode")
	sample := fs.Float64("sample", 0.01, "the share of rejects that keep their text, since the rest outgrow the corpus")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao gat hf -dir DIR [-source NAME] [-limit N] [-plan] [-decode]

Fetches the files in the ingest manifest at the revisions they are pinned to.

Nothing is written to disk except the ledger. The largest pinned file is 26.6 GB
and the box that fetches it peaks at 4.1 GB, so a file is streamed through
whatever consumes it and the bytes are never all in one place at once. A dropped
connection resumes at the byte it stopped at rather than starting the file over,
because a transfer this size will be dropped.

Progress is the ledger, one line per finished file, synced as it is written. A
run that is interrupted is resumed by running the same command again: files
already in the ledger at their pinned revision are skipped, and re-pinning a
source invalidates its entries rather than letting a restart mix two revisions
into one corpus.

Without -decode the bytes are counted and thrown away, which is what checks that
a source can be fetched at all. With it, every record is mapped onto a gao
document and put to the ingest contract: the ones that carry their provenance
are admitted and counted, and the ones that do not go to -rejects with the reason
they failed. Two sources decode today, HPLT v3 and MADLAD-400. The four that ship
Parquet do not, because Parquet keeps its schema in a footer at the end of the
file and cannot be read from a stream that only goes forwards.

Gated sources need a token in `+gat.TokenEnv+`, and CulturaX is the gated one.

There is no default for -dir. A command that starts a 608.9 GB download into
whichever directory it happened to be run from is a command that will do it once
by accident, and the ledger it leaves behind is the record of an ingest nobody
meant to start.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	if *dir == "" {
		fmt.Fprint(stderr, "gao gat hf: -dir is required, because an ingest that picks its own directory is one nobody meant to start\n")
		return 2
	}

	sources, err := hfSources(*source)
	if err != nil {
		fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
		return 1
	}
	if *rejects != "" {
		*decode = true
	}
	if *decode {
		if ok, missing := gat.Decodable(sources); !ok {
			fmt.Fprintf(stderr, "gao gat hf: %v: %s\n", gat.ErrNoDecoder, sourceList(missing))
			fmt.Fprint(stderr, "pick a source with -source, or drop -decode to fetch and count the bytes\n")
			return 1
		}
	}

	ledger, err := gat.OpenLedger(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
		return 1
	}
	defer func() { _ = ledger.Close() }()

	todo, doneFiles, doneBytes := ledger.Plan(sources)
	printPlan(stdout, sources, todo, doneFiles, doneBytes)
	if len(todo) == 0 {
		return 0
	}
	if *limit > 0 && *limit < len(todo) {
		todo = todo[:*limit]
		fmt.Fprintf(stdout, "stopping after %d files, %s\n", len(todo), may.GB(gat.Remaining(todo)))
	}
	if *plan {
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	in := &gat.Ingest{
		Fetcher:  &gat.Fetcher{Token: gat.TokenFromEnv()},
		Ledger:   ledger,
		Box:      may.Label(),
		Progress: func(r gat.Report) { printFetched(stdout, r) },
	}

	var docs *gat.Docs
	if *decode {
		var closeRejects func() error
		docs, closeRejects, err = openDocs(*rejects, *sample)
		if err != nil {
			fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
			return 1
		}
		// Closed here rather than deferred, so that the segment is finished and
		// its error reported before the summary claims the run went well.
		defer func() {
			if err := closeRejects(); err != nil {
				fmt.Fprintf(stderr, "gao gat hf: closing the reject store: %v\n", err)
			}
		}()
		in.Sink = docs
	}
	fmt.Fprintln(stdout)

	n, err := in.Run(ctx, todo)
	fmt.Fprintf(stdout, "\n%d of %d files fetched, %s in the ledger\n", n, len(todo), may.GB(ledger.Bytes()))
	printAdmitted(stdout, docs)
	if err != nil {
		return hfError(stderr, err)
	}
	return 0
}

// openDocs builds the decoding sink and, when a path was given, the reject store
// under it. The returned function closes both the segment and the file, in that
// order, because a segment that is not closed has no index and a reject store
// with no index cannot be read.
func openDocs(path string, sample float64) (*gat.Docs, func() error, error) {
	if path == "" {
		return &gat.Docs{}, func() error { return nil }, nil
	}
	if sample < 0 || sample > 1 {
		return nil, nil, fmt.Errorf("-sample %v is not a share between 0 and 1", sample)
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	w, err := vo.NewWriter(f, sample)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return &gat.Docs{Rejects: w}, func() error {
		return errors.Join(w.Close(), f.Close())
	}, nil
}

// printAdmitted is the part of a decoding run that the byte counts do not say:
// how many of the records in those bytes gao is allowed to keep.
func printAdmitted(w io.Writer, docs *gat.Docs) {
	if docs == nil {
		return
	}
	admitted, rejected := docs.Admitted(), docs.Rejected()
	fmt.Fprintf(w, "%d documents admitted, %d turned away\n", admitted, rejected)

	reasons := docs.Reasons()
	for _, r := range vo.Reasons() {
		if n := reasons[r]; n > 0 {
			fmt.Fprintf(w, "  %-14s %d\n", r, n)
		}
	}
}

// sourceList is a readable list of source names for an error message.
func sourceList(sources []doc.Source) string {
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

// hfSources returns the sources to fetch, which is all of them or the one named.
func hfSources(name string) ([]gat.Pinned, error) {
	if name == "" {
		return gat.Sources(), nil
	}
	p, ok := gat.Pin(doc.Source(name))
	if !ok {
		return nil, fmt.Errorf("%q is not a pinned source", name)
	}
	return []gat.Pinned{p}, nil
}

// printPlan says what is left before anything is fetched, because the first
// question about a 608.9 GB pull is how much of it is still to come.
func printPlan(w io.Writer, sources []gat.Pinned, todo []gat.Work, doneFiles int, doneBytes int64) {
	total := 0
	var totalBytes int64
	for _, p := range sources {
		total += len(p.Files)
		totalBytes += p.Bytes()
	}
	fmt.Fprintf(w, "%d of %d files done, %s of %s\n", doneFiles, total, may.GB(doneBytes), may.GB(totalBytes))
	if len(todo) == 0 {
		fmt.Fprintln(w, "nothing left to fetch")
		return
	}
	fmt.Fprintf(w, "%d files to fetch, %s to move\n", len(todo), may.GB(gat.Remaining(todo)))
}

// printFetched is one line per file, which over 154 files and several days is
// the only thing anybody watches.
func printFetched(w io.Writer, r gat.Report) {
	if r.Err != nil {
		fmt.Fprintf(w, "%-10s %-44s failed after %s\n", r.Pin.Source, r.File.Path, round(r.Elapsed))
		return
	}
	line := fmt.Sprintf("%-10s %-44s %8s  %s", r.Pin.Source, r.File.Path, may.GB(r.File.Bytes), round(r.Elapsed))
	if r.Documents > 0 {
		line += fmt.Sprintf("  %d documents", r.Documents)
	}
	if r.Reconnects > 0 {
		line += fmt.Sprintf("  %d reconnects", r.Reconnects)
	}
	fmt.Fprintln(w, line)
}

// round drops the sub-second noise from a duration that is usually minutes.
func round(d time.Duration) time.Duration { return d.Round(time.Second) }

// hfError turns a failed run into a message and an exit code.
//
// A canceled run is the person at the keyboard pressing ctrl-C, and it exits 0
// with what it has, because everything it finished is in the ledger and the next
// run picks up from there. Everything else is a failure.
func hfError(stderr io.Writer, err error) int {
	switch {
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(stderr, "gao gat hf: stopped, and the ledger has everything that finished")
		return 0
	case errors.Is(err, gat.ErrGated):
		fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
		return 1
	default:
		fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
		return 1
	}
}

// runGatLedger prints what an ingest directory has already done, without
// opening the ledger for writing, so it is safe to run against a directory an
// ingest is using.
func runGatLedger(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("gat ledger", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "the ingest directory")
	files := fs.Bool("files", false, "list every finished file rather than a total per source")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao gat ledger -dir DIR [-files]

Prints what an ingest has finished. It opens the ledger read only, so it is safe
to run on a box that is fetching into the same directory.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	if *dir == "" {
		fs.Usage()
		return 2
	}

	entries, err := gat.ReadLedger(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "gao gat ledger: %v\n", err)
		return 1
	}
	printLedger(stdout, entries, *files)
	return 0
}

// printLedger is separate from the command so the formatting can be tested
// without a directory on disk.
func printLedger(w io.Writer, entries []gat.Entry, files bool) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "nothing fetched yet")
		return
	}

	if files {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprint(tw, "source\tfile\tsize\tdigest\tbox\n")
		for _, p := range gat.Sources() {
			for _, e := range entries {
				if e.Source != p.Source {
					continue
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					e.Source, e.Path, may.GB(e.Bytes), shortRevision(e.Digest), e.Box)
			}
		}
		_ = tw.Flush()
		fmt.Fprintln(w)
	}

	type total struct {
		files      int
		bytes      int64
		documents  int64
		reconnects int
	}
	by := make(map[doc.Source]total)
	var all total
	for _, e := range entries {
		t := by[e.Source]
		t.files++
		t.bytes += e.Bytes
		t.documents += e.Documents
		t.reconnects += e.Reconnects
		by[e.Source] = t
		all.files++
		all.bytes += e.Bytes
		all.documents += e.Documents
		all.reconnects += e.Reconnects
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "source\tfiles\tof\tfetched\tdocuments\treconnects\n")
	for _, p := range gat.Sources() {
		t, ok := by[p.Source]
		if !ok {
			continue
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%d\t%d\n",
			p.Source, t.files, len(p.Files), may.GB(t.bytes), t.documents, t.reconnects)
	}
	fmt.Fprintf(tw, "total\t%d\t%d\t%s\t%d\t%d\n",
		all.files, gat.Files(), may.GB(all.bytes), all.documents, all.reconnects)
	_ = tw.Flush()
}
