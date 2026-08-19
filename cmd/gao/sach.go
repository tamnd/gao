package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/tamnd/gao/dem"
	"github.com/tamnd/gao/kho"
	"github.com/tamnd/gao/may"
	"github.com/tamnd/gao/sach"
	"github.com/tamnd/gao/vo"
)

func runSach(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "where a part is built before it is pushed")
	source := fs.String("source", "", "clean these sources rather than all of them, named and separated by commas")
	limit := fs.Int("limit", 0, "stop after this many parts, which is how a new box is tried out")
	workers := fs.Int("workers", 4, "how many parts are cleaned at once")
	keys := fs.Int("keys", sach.DefaultKeys, "how many documents the deduplication set is sized for")
	push := fs.Bool("push", false, "push each clean part as it closes and delete the local copy")
	plan := fs.Bool("plan", false, "print what would be cleaned and clean nothing")
	report := fs.String("report", "", "write the run report to this file as JSON")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao clean -dir DIR [-source NAMES] [-limit N] [-workers N] [-keys N] [-push] [-plan] [-report FILE] [-json]

Runs the cleaning line over the raw corpus and publishes what comes out.

The raw repo is four public corpora as gao read them, one contract and one
schema, and nothing else done to them. That is the right thing for a repo whose
job is to be re-readable, and it is not a corpus anybody should train on: the
same page arrives from two projects as two documents, a page of navigation is a
document, a document typed in a 2003 font encoding is mojibake, and a phone
number in a forum post is a phone number in a forum post.

The line is four stages in a fixed order and the order is the design. phoi
normalizes, because every stage after it compares strings and two spellings of
one word are two documents to a hash. sang measures and sifts, because there is
no point asking whether a page is good before knowing it is Vietnamese prose.
xay removes the documents this run has already seen, on a key that ignores what
a republisher changes. che covers the personal identifiers, last of the four, so
that what is covered is covered in the document that ships.

The stage that is missing from the middle is the quality classifier, and it is
missing rather than stubbed. gao-qual is trained against a reference set that
does not exist yet, and a filter with an untrained model behind it removes
documents for a reason nobody can defend. What comes out of this line is
Vietnamese prose with the duplicates and the identifiers taken out, and picking
the good Vietnamese prose out of that is a later stage.

A part is read out of the store over HTTP, cleaned in memory, written to one
local file, pushed, and deleted before the next one opens, so peak disk is one
part per worker whatever the corpus weighs. The unit of resume is one part: the
clean repo is listed before the first read and a part already up there is not
read, not cleaned and not pushed, so a run that died after two hundred parts is
resumed by running it again.

Deduplication is exact and it is per run. The set is sized by -keys and costs
eight bytes a document, and a run that fills it stops checking and says so in
the report rather than quietly keeping everything. What that leaves is a
question a query answers: every row carries dup_cluster, so counting what two
boxes or two runs kept twice is a group by over the clean repo rather than a
second pass over the corpus.

Nothing is deleted from the raw repo and nothing is written back to it. This
stage reads it, and a stage that overwrote its own input could be run once.

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
	if *dir == "" && !*plan {
		fmt.Fprint(stderr, "gao clean: -dir is required, because a run that picks its own directory writes parts somewhere nobody looks\n")
		return 2
	}

	raw, clean := kho.Staging(), sach.Clean()
	token := may.Token()
	if token == "" {
		fmt.Fprintf(stderr, "gao clean: %s is not set, and both repos need it\n", may.TokenEnv)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	from := &dem.Store{Repo: raw.Repo(), Token: token}
	to := &kho.Pusher{Repo: clean.Repo(), Token: token}

	parts, err := rawParts(ctx, from, *source)
	if err != nil {
		fmt.Fprintf(stderr, "gao clean: %v\n", err)
		return 1
	}
	if len(parts) == 0 {
		fmt.Fprintf(stderr, "gao clean: %s holds no parts%s\n", raw.Repo(), ofSource(*source))
		return 1
	}
	if *limit > 0 && *limit < len(parts) {
		parts = parts[:*limit]
	}

	var bytes int64
	for _, p := range parts {
		bytes += p.Bytes
	}
	fmt.Fprintf(stdout, "%s -> %s\n", raw.Repo(), clean.Repo())
	fmt.Fprintf(stdout, "%d parts%s, %s to read, %d workers, dedup set %s for %s documents\n",
		len(parts), ofSource(*source), may.GB(bytes), *workers,
		may.Size(sach.NewSeen(*keys).Bytes()), thousands(int64(*keys)))
	if *plan {
		return 0
	}

	// Before the first byte moves, so that a wrong token costs a second rather
	// than the time it takes to clean the first part.
	if *push {
		if err := to.EnsureRepo(ctx, clean); err != nil {
			fmt.Fprintf(stderr, "gao clean: %v\n", err)
			return 1
		}
	}

	pass := &sach.Pass{
		From: from, To: to, Clean: clean,
		Dir: *dir, Box: may.Label(), Workers: *workers, Keys: *keys, Push: *push,
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	note := func(c sach.Cleaned, done, of int) {
		if c.Skipped {
			fmt.Fprintf(tw, "%d/%d\t%s\thad it already\n", done, of, c.From.Path)
		} else {
			fmt.Fprintf(tw, "%d/%d\t%s\t%s kept of %s\t%s\t%s\n", done, of, c.From.Path,
				thousands(c.Kept), thousands(c.Documents), may.Size(c.Bytes), round(c.Took))
		}
		_ = tw.Flush()
	}

	rep, runErr := pass.Run(ctx, parts, note)
	if *report != "" {
		if err := writeReport(*report, rep); err != nil {
			fmt.Fprintf(stderr, "gao clean: %v\n", err)
			return 1
		}
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		printSach(stdout, rep)
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "gao clean: %v\n", runErr)
		return 1
	}
	return 0
}

// rawParts is every part of the raw repo, or of the named sources of it, in
// the order the parts were written.
//
// The names are a list rather than one name because the fleet is sharded by
// source. Two boxes given overlapping lists clean the same part twice, race
// each other writing it, and publish whichever finished last, so the way to
// hand one box everything except what another box has is to say so.
func rawParts(ctx context.Context, s *dem.Store, source string) ([]kho.Stored, error) {
	snapshots, err := s.Snapshots(ctx)
	if err != nil {
		return nil, err
	}
	want := sources(source)
	var out []kho.Stored
	for _, snapshot := range snapshots {
		if len(want) > 0 && !want[dem.SourceOf(snapshot)] {
			continue
		}
		parts, err := s.Parts(ctx, snapshot)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	return out, nil
}

// sources reads the -source flag, which is empty for the whole corpus and a
// comma separated list otherwise. An empty entry is dropped rather than
// matching a snapshot with no source, since a trailing comma is a typo and not
// a request for nothing.
func sources(flag string) map[string]bool {
	if flag == "" {
		return nil
	}
	want := make(map[string]bool)
	for _, name := range strings.Split(flag, ",") {
		if name = strings.TrimSpace(name); name != "" {
			want[name] = true
		}
	}
	return want
}

func ofSource(source string) string {
	if source == "" {
		return ""
	}
	return " of " + strings.Join(slices.Sorted(maps.Keys(sources(source))), ", ")
}

func writeReport(path string, rep sach.Report) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func printSach(w io.Writer, r sach.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "\nread\t%s documents\t%s of text\t%d parts\n",
		thousands(r.Documents), may.GB(r.TextIn), r.Parts)
	fmt.Fprintf(tw, "kept\t%s documents\t%s of text\t%s of parquet\n",
		thousands(r.Kept), may.GB(r.TextOut), may.GB(r.Bytes))
	if r.Skipped > 0 {
		fmt.Fprintf(tw, "had\t%d parts already\n", r.Skipped)
	}
	_ = tw.Flush()

	if r.Documents == 0 {
		return
	}
	fmt.Fprintf(w, "\nremoved, by the stage that removed it:\n\n")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, stage := range []sach.Stage{sach.StageNormalize, sach.StageSift, sach.StageMill, sach.StageContract} {
		reasons := r.Removed[stage]
		if len(reasons) == 0 {
			continue
		}
		for _, line := range byReason(reasons) {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%.1f%%\n", stage, line.reason, thousands(line.n),
				100*float64(line.n)/float64(r.Documents))
		}
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%.1f%% of the documents read were published, and %s of the text.\n",
		100*r.Retention(), share(r.TextOut, r.TextIn))
	fmt.Fprintf(w, "Normalization changed %.1f%% of them by at least one byte and repaired %.1f%%.\n",
		100*r.Phoi.ChangedShare(), 100*r.Phoi.RepairedShare())
	fmt.Fprintf(w, "%s documents carried personal data, %s spans of it, and %s were covered at %s.\n",
		thousands(r.Che.Carrying), thousands(r.Che.Spans), thousands(r.Che.Covered), che1)
	fmt.Fprintf(w, "The deduplication set finished with %s of %s clusters.\n",
		thousands(int64(r.Clusters)), thousands(int64(sachCap(r))))
	if r.Unchecked > 0 {
		fmt.Fprintf(w, "It filled, and %s documents went out without being checked against it.\n",
			thousands(r.Unchecked))
	}
	if rate := r.Rate(); rate > 0 {
		fmt.Fprintf(w, "%s ran at %.0f documents a second over %s.\n", r.Box, rate, round(r.Finished.Sub(r.Started)))
	}
}

// che1 names the redaction level in the summary. The level is on every row as
// well, and the line above is the one somebody reads.
const che1 = "L1"

func sachCap(r sach.Report) int {
	if r.Clusters == 0 {
		return 0
	}
	return r.Clusters + int(r.Unchecked)
}

type reasonLine struct {
	reason vo.Reason
	n      int64
}

func byReason(m map[vo.Reason]int64) []reasonLine {
	out := make([]reasonLine, 0, len(m))
	for reason, n := range m {
		out = append(out, reasonLine{reason, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].reason < out[j].reason
	})
	return out
}
