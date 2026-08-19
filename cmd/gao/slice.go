package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/slice"
	"github.com/tamnd/gao/store"
)

func runSlice(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("slice", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	snapshot := fs.String("snapshot", "", "the snapshot directory the slices are views over")
	head := fs.String("head", "", "the head of the lineage, if it is not the snapshot itself, to check the slices are not behind a removal")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao slice -snapshot dir [-head dir] [-json] slice-dir...

Check a release slice against the snapshot it is a view over.

A slice is a named subset of the corpus published as its own dataset: the
educational shard, the legal shard, the part small enough to fine tune on. It
holds no bytes. It names the parent's shards and carries a predicate, so reading
it is reading the parent's Parquet with a filter, and there is only ever one copy
of a row.

That is not mainly about disk. A copy is a second place a document lives, and
when a takedown arrives the copy does not hear about it. The document stays
published under our name after somebody was told it was removed.

The cost of a view is that it can go stale. A slice pins the parent manifest by
digest, so a parent superseded by a removal makes the slice a view over rows a
takedown has already been applied to somewhere else. Pass -head to check for
that. It is reported rather than resolved, because re-deriving a slice is one
pass of the predicate and not noticing is the failure.

Exits 1 if a slice and its parent disagree.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *snapshot == "" || fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	parent, err := store.ReadManifest(*snapshot)
	if err != nil {
		fmt.Fprintf(stderr, "gao slice: %v\n", err)
		return 1
	}
	var latest *store.Manifest
	if *head != "" {
		latest, err = store.ReadManifest(*head)
		if err != nil {
			fmt.Fprintf(stderr, "gao slice: %v\n", err)
			return 1
		}
	}

	report := sliceReport{Snapshot: parent.Snapshot, Documents: parent.Counts.Documents, Bytes: parent.Counts.Bytes}
	for _, dir := range fs.Args() {
		s, err := slice.Read(dir)
		if err != nil {
			fmt.Fprintf(stderr, "gao slice: %v\n", err)
			return 1
		}
		line := sliceLine{Slice: s, Faults: s.Against(parent)}
		line.Faults = append(line.Faults, s.Publishable()...)
		if latest != nil {
			if err := s.Fresh(latest); err != nil {
				line.Faults = append(line.Faults, err.Error())
			}
		}
		report.Slices = append(report.Slices, line)
		report.Saved += s.Saved()
		if len(line.Faults) > 0 {
			report.Faults += len(line.Faults)
		}
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printSlice(stdout, report)
	}
	if report.Faults > 0 {
		return 1
	}
	return 0
}

type sliceLine struct {
	Slice  *slice.Slice `json:"slice"`
	Faults []string     `json:"faults,omitempty"`
}

type sliceReport struct {
	Snapshot  string      `json:"snapshot"`
	Documents int64       `json:"documents"`
	Bytes     int64       `json:"bytes"`
	Slices    []sliceLine `json:"slices"`

	// Saved is the text these slices would have duplicated as copies, which is
	// the arithmetic of publishing them as views.
	Saved  int64 `json:"saved"`
	Faults int   `json:"faults"`
}

func printSlice(w io.Writer, r sliceReport) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "slice\trepo\tshards\tdocuments\tshare\ttext\tstate\n")
	for _, line := range r.Slices {
		s := line.Slice
		c := s.Counts()
		state := "a view over " + s.Parent
		if len(line.Faults) > 0 {
			state = plural(len(line.Faults), "fault")
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			s.Name, s.Dataset, s.Shards(), c.Documents, share(c.Documents, r.Documents), sliceSize(c.Bytes), state)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s over %s, holding %d of its %d documents.\n",
		plural(len(r.Slices), "slice"), r.Snapshot, sliceDocuments(r), r.Documents)
	fmt.Fprintf(w, "None of them holds a byte of its own, and published as copies they would have duplicated %s of text.\n", sliceSize(r.Saved))

	for _, line := range r.Slices {
		if len(line.Faults) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s:\n", line.Slice.Name)
		for _, f := range line.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
	if r.Faults == 0 {
		fmt.Fprint(w, "Every slice is a view over exactly the snapshot it names, and every one goes to a repo that admits what it carries.\n")
	}
}

func sliceDocuments(r sliceReport) int64 {
	var n int64
	for _, line := range r.Slices {
		n += line.Slice.Documents()
	}
	return n
}

func share(part, whole int64) string {
	if whole <= 0 {
		return "."
	}
	return fmt.Sprintf("%.1f%%", 100*float64(part)/float64(whole))
}

// sliceSize prints bytes of text the way the rest of the project quotes a corpus
// size, which is never a file size.
func sliceSize(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTP"[exp])
}
