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

	"github.com/tamnd/gao/dem"
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/kho"
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
	tokenizer := fs.String("tokenizer", "", "count tokens with the tokenizer at this path, which implies -decode")
	out := fs.String("out", "", "write the documents the contract admits under this directory as parquet, which implies -decode")
	push := fs.Bool("push", false, "push each part to the store as it closes and delete the local copy, which needs -out")
	disk := fs.String("watch", "", "sample the disk this run holds into this file, which is what 'gao box peak' reads")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao gat hf -dir DIR [-source NAME] [-limit N] [-plan] [-decode] [-out DIR] [-push] [-tokenizer PATH] [-watch FILE]

Fetches the files in the ingest manifest at the revisions they are pinned to.

A source the manifest marks dropped is refused by name, with the reason, before
the ledger is opened. 'gao gat pins' lists them under the download total.

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
they failed. Five sources decode today. The sixth is CulturaX, which is gated and
whose terms have not been granted, so nobody has read a byte of it to write a
mapping against.

The three that ship Parquet are decoded by range request rather than by streaming,
because the format keeps its schema in a footer at the end of the file and cannot
be read forwards. Such a file has no digest in the ledger. It is read in pieces
and its bytes are never all in one place, so there is nothing to hash, and the
ledger records how it was read and how much of it moved instead. Without -decode
those sources are streamed and verified like the others.

With -out the admitted documents are written under that directory as Parquet, in
the layout the store of record uses, one part per 1.06 GB of text so that a worker
holds a part rather than a file. That number is the shard target multiplied by the
compression ratio the disk budget runs on, and it moved when the ratio stopped
being assumed. The paths are a function of the source revision, the input file,
and the part number, so a run that is interrupted mid file writes over what it
left rather than beside it. A file whose decode fails leaves no part at all.

With -push each part goes to the store as it closes and the local copy is
deleted before the next one opens. That is what makes a source larger than the
disk runnable: what the box holds is one part being written rather than the
corpus. A part that cannot be pushed fails the file it came from, because a run
that carried on would be filling the disk it was emptying. Pushing the same part
twice is safe and nearly free: the path is a function of what is in it, and the
store keys the bytes by their digest, so a run started again after a box reboots
skips what is already up there without sending it a second time.

With -watch the run samples the disk it is holding every ten seconds and writes
the trace to that file, which is what turns the disk budget from arithmetic into
a measurement. Run 'gao box peak' over it afterwards. The sample says which half
of the work it was taken during, since writing a part and sending it hold the
disk for different reasons.

A decoding run also counts what it read and writes counts.json beside the ledger:
documents, text bytes, characters, and syllables per source. With -tokenizer it
counts tokens too, which is the only number in gao that costs anything to
produce, at roughly 11 MB of text per second per core. Run 'gao dem model' to
fetch the tokenizer and 'gao dem counts' to read the result.

The counts are written before the first byte is fetched and rewritten after
every file, so a run that takes days can be read while it runs and cannot leave
an earlier run's numbers sitting in the directory. A report written mid run says
so, and 'gao dem counts' prints which boxes had not finished.

Counting happens here rather than in a later pass because the largest source is
around 700 GB of text, and a design where ingestion writes documents and
something else reads them back to count is a design that moves 700 GB twice.

Gated sources need a token in `+may.TokenEnv+`, and CulturaX is the gated one.

There is no default for -dir. A command that starts a 513.6 GB download into
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
	if *rejects != "" || *tokenizer != "" || *out != "" {
		*decode = true
	}
	if *push && *out == "" {
		fmt.Fprint(stderr, "gao gat hf: -push needs -out, because a part is written before it is pushed\n")
		return 2
	}

	// Loaded before anything is fetched. The tokenizer is 4.7 MB and the ingest
	// that follows it is measured in days, so finding out that the path was
	// wrong should not cost a download.
	var tok *dem.Tokenizer
	if *tokenizer != "" {
		tok, err = dem.Open(dem.Gemma3, *tokenizer)
		if err != nil {
			fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
			fmt.Fprint(stderr, "run 'gao dem model -o PATH' to fetch the tokenizer gao counts with\n")
			return 1
		}
	}
	if *decode {
		if ok, missing := gat.Decodable(sources); !ok {
			fmt.Fprintf(stderr, "gao gat hf: %v: %s\n", gat.ErrNoDecoder, sourceList(missing))
			fmt.Fprint(stderr, "pick a source with -source, or drop -decode to fetch and count the bytes\n")
			return 1
		}
	}

	// Taken before the ledger is read, because the plan a second ingest builds
	// from that ledger is the thing being prevented. A -plan run writes nothing
	// and takes no lock, so the plan stays readable while an ingest is running,
	// which is when somebody most wants to read it.
	if !*plan {
		lock, err := gat.LockDir(*dir, "gao gat hf")
		if err != nil {
			fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
			return 1
		}
		defer func() {
			if err := lock.Release(); err != nil {
				fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
			}
		}()
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
		Fetcher: &gat.Fetcher{Token: may.Token()},
		Ledger:  ledger,
		Box:     may.Label(),
	}

	var tally dem.Tally
	var docs *gat.Docs
	var written *parts
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

		// The order matters. What the decoder emits into is the writer, and the
		// tally wraps it, so a document is counted and written in one pass. The
		// alternative reads 700 GB of text twice.
		if *out != "" {
			written = newParts(*out, docs, in.Box, stdout)
			written.tokenizer = tokenizerLabel(tok)
			docs.Emit = written.write
			in.Sink = written
		}
		if *push {
			// Before the first byte is fetched. Finding out that the token is
			// wrong should cost a second rather than the twenty minutes it
			// takes to fill the first part.
			written.push = &kho.Pusher{Repo: written.dataset.Repo(), Token: may.Token()}
			if err := written.push.EnsureRepo(ctx, written.dataset); err != nil {
				fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
				return 1
			}
		}
		docs.Emit = tally.Counting(tok, docs.Emit)
	}

	// Started after the sink is built, because what the trace is worth depends
	// on it sampling the directory the parts are actually written to.
	stopWatch := func() {}
	if *disk != "" {
		stage := func() string { return "fetch" }
		if written != nil {
			stage = written.stage
		}
		w, err := watch(*disk, []string{*dir, *out}, in.Box, stage)
		if err != nil {
			fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
			return 1
		}
		stopWatch = func() {
			stopWatch = func() {}
			if err := w.Close(); err != nil {
				fmt.Fprintf(stderr, "gao gat hf: %v\n", err)
			}
		}
		// The deferred call is for the paths that leave early. The run itself
		// stops the trace by hand below, so that the file is closed and its
		// error reported before the summary claims the run went well.
		defer func() { stopWatch() }()
	}

	// The counts are rewritten after every file rather than at the end of the
	// run. A source the size of the ones here takes days, and the version that
	// wrote once at the end left the previous run's counts.json sitting in the
	// directory for all of it, which is worse than an empty file: it parses, it
	// names a source, and nothing about it says it describes a run that ended
	// yesterday. The first write happens before any bytes are fetched, so the
	// stale file is gone from the first second.
	in.Progress = func(r gat.Report) {
		printFetched(stdout, r)
		if *decode {
			saveCounts(stderr, *dir, in.Box, &tally, false)
		}
	}
	if *decode {
		saveCounts(stderr, *dir, in.Box, &tally, false)
	}
	fmt.Fprintln(stdout)

	n, err := in.Run(ctx, todo)
	stopWatch()
	fmt.Fprintf(stdout, "\n%d of %d files fetched, %s in the ledger\n", n, len(todo), may.GB(ledger.Bytes()))
	printAdmitted(stdout, docs)
	if written != nil {
		written.summary(stdout)
	}

	// Written even when the run failed, because the counts up to the failure are
	// the counts of what actually got read, and a run that dies on file ninety
	// should not throw away eighty-nine files of measurement. It is marked
	// complete either way, since the run is over and the counts are final for
	// the files that finished.
	if *decode {
		saveCounts(stderr, *dir, in.Box, &tally, true)
		printTally(stdout, &tally)
	}
	if err != nil {
		return hfError(stderr, err)
	}
	return 0
}

// saveCounts saves the counts so far into the ingest directory.
//
// A failure to write is reported and does not stop the run. The counts are a
// measurement of a download that is already paid for, and losing the download
// because the directory went read only would be the wrong trade in both
// directions.
func saveCounts(stderr io.Writer, dir, box string, tally *dem.Tally, complete bool) {
	r := tally.Report(box, time.Now())
	r.Complete = complete
	if err := r.Write(dir); err != nil {
		fmt.Fprintf(stderr, "gao gat hf: writing the counts: %v\n", err)
	}
}

// printTally is the line that says what was in the bytes, as opposed to how many
// of them there were.
func printTally(w io.Writer, tally *dem.Tally) {
	c := tally.Natural()
	if c.Documents == 0 {
		return
	}
	if c.Tokens == 0 {
		fmt.Fprintf(w, "%s of text, %d characters, %d syllables, counted in %s\n",
			may.GB(c.Bytes), c.Chars, c.Syllables, dem.File)
		return
	}
	fmt.Fprintf(w, "%s of text, %d characters, %d syllables, %d %s tokens at %.2f characters each, counted in %s\n",
		may.GB(c.Bytes), c.Chars, c.Syllables, c.Tokens, tally.Tokenizer, c.CharsPerToken(), dem.File)
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
	// Asking for it by name is deliberate, so the answer is the reason rather
	// than an empty plan and a clean exit.
	if p.Dropped {
		return nil, fmt.Errorf("%s is pinned and dropped from the ingest: %s", name, p.DroppedBecause)
	}
	return []gat.Pinned{p}, nil
}

// printPlan says what is left before anything is fetched, because the first
// question about a 513.6 GB pull is how much of it is still to come.
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

// printFetched is one line per file, which over 122 files and several days is
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
	if r.Access == gat.Random {
		// The size column is the size of the file, and a file read this way was
		// not moved, so what it cost is printed next to it rather than left to
		// be inferred from a number that means something else.
		line += fmt.Sprintf("  %s in %d requests", may.GB(r.Moved), r.Requests)
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

	// Whether an ingest is running changes what these totals mean, so the
	// holder is printed after them rather than left for the reader to guess at.
	// A lock that cannot be read is a note and not a failure, because this
	// command claims nothing and the numbers above it are still the numbers.
	if h, err := gat.ReadHolder(*dir); err != nil {
		fmt.Fprintf(stderr, "gao gat ledger: %v\n", err)
	} else if h.PID != 0 {
		fmt.Fprintf(stdout, "\nan ingest is running here: %s\n", h)
	}
	return 0
}

// verified is what the digest column says about one entry.
//
// A file read out of order has no digest, and printing an empty cell would read
// as a bug in the ledger rather than as the thing it is. Naming it says which of
// the two happened.
func verified(e gat.Entry) string {
	if e.Digest == "" {
		return "read in pieces"
	}
	return shortRevision(e.Digest)
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
		// AllSources rather than Sources, so a ledger written before a source
		// was dropped still lists what it fetched instead of losing the lines.
		for _, p := range gat.AllSources() {
			for _, e := range entries {
				if e.Source != p.Source {
					continue
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					e.Source, e.Path, may.GB(e.Bytes), verified(e), e.Box)
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
	for _, p := range gat.AllSources() {
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
