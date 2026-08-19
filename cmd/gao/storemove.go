package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/tamnd/gao/count"
	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/store"
)

func runStoreMove(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store move", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("dataset", store.StageRepo, "the dataset repo to re-lay")
	run := fs.Bool("run", false, "do it, rather than printing what it would do")
	sweep := fs.Bool("sweep", false, "delete the old paths, which is only safe once the new ones have been read")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store move [-dataset NAME] [-run] [-sweep]

Puts every part in a repo at the path the current layout puts it at, without the
bytes traveling.

A large file on the Hub is an LFS object addressed by the sha256 of its content,
and a path is a pointer at that object. A commit that points a second path at an
object the repo already holds moves no content, so a repo of five hundred parts
and a quarter of a terabyte is re-laid in the time it takes to list it. Nothing
comes down to this machine and nothing goes back up, which is why this can run
while the fleet is busy.

The repo has to be the same one. LFS storage on the Hub is namespaced per repo,
so pointing a path in one repo at an object that lives in another gets a 404 and
the only way across is to upload the bytes again. Renaming a repo, on the other
hand, carries its storage with it, so a repo that wants both a new name and a new
layout gets the name from a rename and the layout from here.

Without -run it lists what it would write and stops.

The old paths are left in place, pointing at the same objects, until -sweep. Two
paths for one object cost nothing but a tree entry, and leaving them up until the
new ones have been read is what makes this reversible.

It is safe to re-run. A path already there pointing at the same content is
skipped, so a run that was killed halfway finishes from where it got to and a
finished one writes nothing.

Needs a token with write access in `+fleet.TokenEnv+`.

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

	d, ok := store.Lookup(*name)
	if !ok {
		fmt.Fprintf(stderr, "gao store move: no dataset named %q\n", *name)
		fmt.Fprintln(stderr, "run 'gao store datasets' for the list")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	p := &store.Pusher{Repo: d.Repo(), Token: fleet.Token(), API: pushAPI(),
		Message: "Re-lay the parts at the paths in " + store.Repository}

	if !*run {
		return printMovePlan(stdout, stderr, ctx, p)
	}

	report, err := p.MoveTo(ctx, p, store.DataDir, restage, func(batch []store.Move, done, of int) {
		fmt.Fprintf(stdout, "%d/%d  %s\n", done, of, batch[len(batch)-1].To)
	})
	if err != nil {
		fmt.Fprintf(stderr, "gao store move: %v\n", err)
		return 1
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "\nwrote\t%s\tin %s\n", plural(len(report.Moved), "path"), plural(report.Commits, "commit"))
	if len(report.Skipped) > 0 {
		fmt.Fprintf(tw, "already there\t%s\tpointing at the same content\n", plural(len(report.Skipped), "path"))
	}
	fmt.Fprintf(tw, "did not travel\t%s\tof parquet, because a pointer is not a copy\n", fleet.Size(report.Spared))
	_ = tw.Flush()

	if *sweep {
		gone, err := sweepOld(ctx, p, report)
		if err != nil {
			fmt.Fprintf(stderr, "gao store move: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "\nswept %s off the old layout, and the objects they pointed at are still there under the new one\n",
			plural(gone, "path"))
	}
	fmt.Fprintf(stdout, "\n%s is laid out the way %s reads it\n", p.Repo, store.Repository)
	return 0
}

// sweepOld deletes the paths a move replaced.
//
// It deletes what the move accounted for rather than everything that parses as
// an old path, so a path the move never looked at is not one this removes, and
// a run that stopped halfway sweeps only as far as it got.
func sweepOld(ctx context.Context, p *store.Pusher, report store.MoveReport) (int, error) {
	var old []string
	for _, m := range append(append([]store.Move{}, report.Moved...), report.Skipped...) {
		if m.From != m.To {
			old = append(old, m.From)
		}
	}
	if len(old) == 0 {
		return 0, nil
	}
	sort.Strings(old)
	if _, err := p.Delete(ctx, old); err != nil {
		return 0, err
	}
	return len(old), nil
}

// printMovePlan says what a move would write without writing it.
func printMovePlan(stdout, stderr io.Writer, ctx context.Context, p *store.Pusher) int {
	files, err := p.List(ctx, store.DataDir)
	if err != nil {
		fmt.Fprintf(stderr, "gao store move: %v\n", err)
		return 1
	}

	var write, held, left int
	var bytes int64
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	shown := 0
	for _, f := range files {
		path, ok := restage(f.Path)
		switch {
		case !ok:
			left++
			continue
		case path == f.Path:
			held++
			continue
		}
		write++
		bytes += f.Bytes
		if shown < 3 {
			fmt.Fprintf(tw, "%s\t->\t%s\n", f.Path, path)
			shown++
		}
	}
	_ = tw.Flush()

	if write == 0 {
		fmt.Fprintf(stdout, "every part in %s is already at the path this layout puts it at\n", p.Repo)
		return 0
	}
	if write > shown {
		fmt.Fprintf(stdout, "and %d more the same way\n", write-shown)
	}
	fmt.Fprintf(stdout, "\n%s of %s would be written in %s, and none of it would travel\n",
		plural(write, "path"), fleet.Size(bytes), p.Repo)
	if held > 0 {
		fmt.Fprintf(stdout, "%s are already where they belong\n", plural(held, "part"))
	}
	if left > 0 {
		fmt.Fprintf(stdout, "%s are not parts and would be left alone\n", plural(left, "file"))
	}
	fmt.Fprintln(stdout, "run it again with -run to write it")
	return 0
}

// hivePathPattern reads the Hive spelled layout the working repo had before
// this one: data/snapshot=glotcc-9ad140b6be3a/file=00003/part-00000.parquet.
//
// It is here rather than in kho because it is not a layout gao writes any more.
// It is a fact about one repo on the Hub, it stops mattering the day that repo
// is empty, and the parser that reads it should go the same day rather than
// sitting in the package that defines the real one.
var hivePathPattern = regexp.MustCompile(
	`^` + store.DataDir + `/snapshot=([A-Za-z0-9][A-Za-z0-9._-]*)/file=(\d{5,})/part-(\d{5,})\` + store.ParquetExt + `$`)

// restage reads a part path in either of the two layouts a working repo has had
// and returns where the current one puts it.
func restage(path string) (string, bool) {
	if snapshot, file, part, ok := store.ParseStagePath(path); ok {
		return store.StagePath(snapshot, file, part), true
	}
	m := hivePathPattern.FindStringSubmatch(path)
	if m == nil {
		return "", false
	}
	file, err := strconv.Atoi(m[2])
	if err != nil {
		return "", false
	}
	part, err := strconv.Atoi(m[3])
	if err != nil {
		return "", false
	}
	return store.StagePath(m[1], file, part), true
}

func runStoreIndex(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("dataset", store.StageRepo, "the dataset repo to index")
	out := fs.String("o", "", "write the index here as well as printing the summary")
	push := fs.Bool("push", false, "put the index and the card it generates on the repo")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store index [-dataset NAME] [-o PATH] [-push]

Reads the footer of every part in a repo and writes `+store.IndexName+`, which is
one row per part: the source, the snapshot, the input file and part it came
from, its path, its document count and its size.

It is the answer to what is in the repo, for a reader who has not got a Parquet
reader and for one who has but would rather not open five hundred files to find
out.

With -push it writes the card as well. A working repo has no manifest, so the
index is the only thing that knows what is in it, and a card is what the index
says arranged for a person. Generating one without the other would leave a repo
whose front page and whose index disagree.

The document count comes out of each part's Parquet footer rather than out of
what the run that wrote it reported, because a run that died between pushing a
part and writing down that it had is the case an index has to be right about.
A footer is a few kilobytes at the end of a half gigabyte file, so indexing a
quarter of a terabyte moves a few megabytes, and the run reports both numbers
rather than describing itself as cheap.

Reading a working repo needs a token in `+fleet.TokenEnv+` with access to it, and
-push needs write access.

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

	d, ok := store.Lookup(*name)
	if !ok {
		fmt.Fprintf(stderr, "gao store index: no dataset named %q\n", *name)
		fmt.Fprintln(stderr, "run 'gao store datasets' for the list")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	s := &count.Store{Repo: d.Repo(), Token: fleet.Token(), API: pushAPI()}
	report, err := count.IndexOf(ctx, s, func(row store.Indexed, i, of int, moved int64) {
		fmt.Fprintf(stderr, "\r%d/%d  %s  %s        ", i, of, row.Path, fleet.Size(moved))
	})
	fmt.Fprint(stderr, "\r\033[K")
	if err != nil {
		fmt.Fprintf(stderr, "gao store index: %v\n", err)
		return 1
	}

	var body strings.Builder
	if err := store.WriteIndex(&body, report.Rows); err != nil {
		fmt.Fprintf(stderr, "gao store index: %v\n", err)
		return 1
	}
	if *out != "" {
		if err := os.WriteFile(*out, []byte(body.String()), 0o600); err != nil {
			fmt.Fprintf(stderr, "gao store index: %v\n", err)
			return 1
		}
	}

	printIndex(stdout, d, report)

	if *push {
		p := &store.Pusher{Repo: d.Repo(), Token: fleet.Token(), API: pushAPI(),
			Message: "Index the parts and say so on the card"}
		fmt.Fprintln(stdout)
		for _, w := range []struct {
			what string
			body []byte
		}{
			{store.IndexName, []byte(body.String())},
			{store.CardName, []byte(store.Card(d, nil, report.Rows))},
		} {
			sent, pErr := p.PushText(ctx, w.what, w.body)
			if pErr != nil {
				fmt.Fprintf(stderr, "gao store index: %v\n", pErr)
				return 1
			}
			if sent.Skipped() {
				fmt.Fprintf(stdout, "%s already says this, so nothing moved\n", w.what)
				continue
			}
			fmt.Fprintf(stdout, "pushed %s to %s, %s\n", w.what, d.Repo(), fleet.Size(sent.Bytes))
		}
	}
	return 0
}

func printIndex(stdout io.Writer, d store.Dataset, report count.IndexReport) {
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "source\tsnapshot\tfiles\tparts\tdocuments\tsize\n")
	for _, s := range store.BySource(report.Rows) {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\n",
			s.Source, strings.Join(s.Snapshots, ", "), s.Files, s.Parts, s.Documents, fleet.Size(s.Bytes))
	}
	fmt.Fprintf(tw, "\t\t\t%d\t%d\t%s\n", len(report.Rows), report.Documents(), fleet.Size(report.Held))
	_ = tw.Flush()
	fmt.Fprintf(stdout, "\n%s, read with %s of footers against the %s they describe\n",
		d.Repo(), fleet.Size(report.Moved), fleet.Size(report.Held))
}
