package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/dem"
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
	"github.com/tamnd/gao/may"
)

func runDem(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		demUsage(stderr)
		return 2
	}
	switch args[0] {
	case "model":
		return runDemModel(stdout, stderr, args[1:])
	case "counts":
		return runDemCounts(stdout, stderr, args[1:])
	case "gates":
		return runDemGates(stdout, stderr, args[1:])
	case "keys":
		return runDemKeys(stdout, stderr, args[1:])
	case "overlap":
		return runDemOverlap(stdout, stderr, args[1:])
	case "verify":
		return runDemVerify(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		demUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao dem: unknown subcommand %q\n", args[0])
		demUsage(stderr)
		return 2
	}
}

func demUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao dem <subcommand> [flags]

Counting, in the units gao publishes. A token count means nothing until the
tokenizer is named, so the tokenizer is a pinned file with a digest rather than
whatever was installed on the box.

subcommands:
  model    fetch the tokenizer that defines a gao token, and verify it
  gates    put a tokenizer through the ten gates, and measure its fertility
  counts   print the counts an ingest produced, or several added together
  keys     read the document identities of a snapshot out of the store
  overlap  print what the sources have in common, from their key files
  verify   check a published count against the store it came from

run 'gao dem <subcommand> -h' for the flags of a single subcommand.
`)
}

func runDemModel(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dem model", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "write the tokenizer here")
	timeout := fs.Duration("timeout", 2*time.Minute, "how long to wait for the download")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao dem model [-o PATH]

Fetches the tokenizer that defines a gao token and checks it against its pinned
digest. With no -o it prints the pin and downloads nothing.

One gao token is one token under the Gemma-3 vocabulary of 262144 pieces. That
file is gated at Google's own repositories, which means reaching it there
requires accepting a license in a browser, and a program cannot do that. It is
fetched from a mirror instead, and the digest is what makes that acceptable: it
does not matter who serves the bytes if the bytes are known. Four separately
uploaded repositories across the Gemma-3 family carry this file identically.

The pinned digest is also what catches the failure that actually happens. Ask a
gated repository for a file without credentials and the refusal arrives as a
body: 129 bytes of English prose written into the file where a protobuf should
be, with nothing about it that looks like an error to a program that only checks
whether the write succeeded.

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

	m := dem.Gemma3
	fmt.Fprintf(stdout, "%s\n", m.Name)
	fmt.Fprintf(stdout, "  vocabulary: %d\n", m.Vocab)
	fmt.Fprintf(stdout, "  size:       %d bytes\n", m.Bytes)
	fmt.Fprintf(stdout, "  sha256:     %s\n", m.Digest)
	fmt.Fprintf(stdout, "  origin:     %s\n", m.Origin)
	fmt.Fprintf(stdout, "  fetched:    %s\n", m.From)

	if *out == "" {
		fmt.Fprint(stdout, "\nrun with -o PATH to download it.\n")
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	b, err := m.Fetch(ctx, nil)
	if err != nil {
		fmt.Fprintf(stderr, "\ngao dem model: %v\n", err)
		return 1
	}
	if err := m.Save(*out, b); err != nil {
		fmt.Fprintf(stderr, "\ngao dem model: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nverified and written to %s\n", *out)
	return 0
}

func runDemCounts(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dem counts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao dem counts DIR [DIR ...]

Prints the counts an ingest produced. Given more than one directory it adds them
up, which is how four boxes that each did part of the work report one number.

Every count here was produced by counting. The conversion constants in doc live
somewhere else on purpose: they answer what a hundred gigabytes is roughly worth
before anything has been fetched, and an estimate that reaches a release note
becomes a measurement in the reader's mind.

Adding up counts from two different tokenizers is an error rather than a warning.
Two tokenizers disagree on Vietnamese by something like a third, so their sum is
not slightly wrong, it corresponds to no tokenizer at all, and it would be quoted
as a corpus size.
`)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	reports := make([]dem.Report, 0, fs.NArg())
	for _, dir := range fs.Args() {
		r, err := dem.ReadReport(dir)
		if err != nil {
			fmt.Fprintf(stderr, "gao dem counts: %v\n", err)
			return 1
		}
		reports = append(reports, r)
	}

	merged, err := dem.Merge(reports...)
	if err != nil {
		fmt.Fprintf(stderr, "gao dem counts: %v\n", err)
		return 1
	}
	printCounts(stdout, merged, reports)
	return 0
}

// printCounts is separate from the command so the table can be tested without a
// directory on disk, which is where the interesting cases are.
func printCounts(w io.Writer, r dem.Report, from []dem.Report) {
	fmt.Fprintf(w, "%s\n", boxes(from))
	if r.Tokenizer == "" {
		fmt.Fprint(w, "no tokenizer, so the token column is empty and the rest are exact\n")
	} else {
		fmt.Fprintf(w, "tokenizer %s, so a token here means the same thing it means everywhere else in gao\n", r.Tokenizer)
	}
	fmt.Fprintln(w)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "source\tdocuments\ttext\tchars\tsyllables\ttokens\tchars/token\n")
	for _, s := range r.Sources {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%d\t%s\t%s\n",
			s.Source, s.Documents, may.GB(s.Bytes), s.Chars, s.Syllables, tokenColumn(s.Counts), ratio(s.CharsPerToken()))
	}
	fmt.Fprint(tw, "\t\t\t\t\t\t\n")
	fmt.Fprintf(tw, "corpus\t%d\t%s\t%d\t%d\t%s\t%s\n",
		r.Natural.Documents, may.GB(r.Natural.Bytes), r.Natural.Chars, r.Natural.Syllables,
		tokenColumn(r.Natural), ratio(r.Natural.CharsPerToken()))
	if r.Total != r.Natural {
		fmt.Fprintf(tw, "total\t%d\t%s\t%d\t%d\t%s\t%s\n",
			r.Total.Documents, may.GB(r.Total.Bytes), r.Total.Chars, r.Total.Syllables,
			tokenColumn(r.Total), ratio(r.Total.CharsPerToken()))
	}
	_ = tw.Flush()

	if r.Total != r.Natural {
		fmt.Fprint(w, "\ncorpus is the natural sources. Model generated text is in the total and never in a headline.\n")
	}
	if r.Natural.Tokens > 0 {
		fmt.Fprintf(w, "\nmeasured on this material: %.2f characters per token, %.2f tokens per syllable, %.2f bytes per character\n",
			r.Natural.CharsPerToken(), r.Natural.TokensPerSyllable(), r.Natural.BytesPerChar())
	}
	if running := unfinished(from); len(running) > 0 {
		fmt.Fprintf(w, "\n%s still running, so these are the files that have finished so far and not a source total.\n", stillRunning(running))
	}
}

// unfinished names the boxes whose run had not ended when they last wrote. The
// counts are rewritten after every file, so reading them mid run is the point
// rather than a mistake, and saying so is what keeps a prefix from being quoted
// as a total.
func unfinished(from []dem.Report) []string {
	var running []string
	for _, r := range from {
		if !r.Complete {
			running = append(running, r.Box)
		}
	}
	return running
}

func stillRunning(boxes []string) string {
	if len(boxes) == 1 {
		return boxes[0] + " was"
	}
	return strings.Join(boxes, ", ") + " were"
}

// boxes names where the numbers came from. A count that does not say which
// machine produced it cannot be checked against a rerun.
func boxes(from []dem.Report) string {
	switch len(from) {
	case 0:
		return "no counts"
	case 1:
		return "counted on " + from[0].Box
	}
	names := make([]string, 0, len(from))
	for _, r := range from {
		names = append(names, r.Box)
	}
	return fmt.Sprintf("counted on %d boxes: %s", len(from), strings.Join(names, ", "))
}

// tokenColumn prints a dash rather than a zero for a run that did not tokenize,
// because a zero in a token column reads as a measurement.
func tokenColumn(c dem.Counts) string {
	if c.Tokens == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", c.Tokens)
}

func ratio(f float64) string {
	if f == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", f)
}

func runDemKeys(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dem keys", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", kho.StageRepo, "the dataset repo the parts are in")
	dir := fs.String("dir", "keys", "where the key files are written")
	out := fs.String("o", "", "write the snapshot's key file here instead of DIR/SNAPSHOT.keys")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao dem keys [-repo NAME] [-dir DIR] [-o PATH] [SNAPSHOT]

Reads the document identities of one snapshot out of the store and writes them
sorted to a key file. With no snapshot it prints the snapshots the repo holds.

The corpus does not come back down for this. A part is parquet and identity is
one fixed width column, so the pass opens each part over HTTP, reads the doc_id
chunk of every row group, and asks for none of the pages the text is in. What
moves is around thirty two bytes per document, which is the difference between
an afternoon and a week at the size gao is.

The pass is resumable at the part. It writes one key file per part under DIR and
skips a part it already has, so a run that was killed after a hundred parts reads
the rest and merges. Those files are left behind on purpose, since they are the
expensive part.

Reading a working repo needs a token in `+may.TokenEnv+` with access to it.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return 2
	}

	d, ok := kho.Lookup(*repo)
	if !ok {
		fmt.Fprintf(stderr, "gao dem keys: no dataset named %q\n", *repo)
		fmt.Fprintln(stderr, "run 'gao kho datasets' for the list")
		return 1
	}
	s := &dem.Store{Repo: d.Repo(), Token: may.Token(), API: pushAPI()}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if fs.NArg() == 0 {
		snapshots, err := s.Snapshots(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "gao dem keys: %v\n", err)
			return 1
		}
		if len(snapshots) == 0 {
			fmt.Fprintf(stdout, "%s holds no ingested snapshots\n", d.Repo())
			return 0
		}
		fmt.Fprintf(stdout, "%s\n", d.Repo())
		for _, snapshot := range snapshots {
			fmt.Fprintf(stdout, "  %-40s %s\n", snapshot, dem.SourceOf(snapshot))
		}
		fmt.Fprint(stdout, "\nrun 'gao dem keys SNAPSHOT' to read one of them.\n")
		return 0
	}

	snapshot := fs.Arg(0)
	work := filepath.Join(*dir, snapshot)
	path := *out
	if path == "" {
		path = filepath.Join(*dir, snapshot+dem.KeysExt)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		fmt.Fprintf(stderr, "gao dem keys: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "reading %s out of %s\n", snapshot, d.Repo())
	var parts int
	keys, err := dem.KeysOf(ctx, s, snapshot, work, path, func(part kho.Stored, i, of int, k dem.Keys, moved int64) {
		parts = of
		fmt.Fprintf(stdout, "  %4d/%d  %-52s %10d documents, %s read so far\n",
			i, of, filepath.Base(part.Path), k.Documents, may.Size(moved))
	})
	if err != nil {
		fmt.Fprintf(stderr, "gao dem keys: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "\n%s\n", snapshot)
	fmt.Fprintf(stdout, "  parts      %d\n", parts)
	fmt.Fprintf(stdout, "  documents  %d\n", keys.Documents)
	fmt.Fprintf(stdout, "  distinct   %d\n", keys.Distinct)
	fmt.Fprintf(stdout, "  repeats    %s of the source is a copy of something already in it\n", percent(keys.Duplication()))
	fmt.Fprintf(stdout, "\nwritten to %s\n", path)
	return 0
}

func runDemOverlap(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dem overlap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the matrix as json instead of a table")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao dem overlap FILE [FILE ...]

Prints what the sources have in common, from the key files 'gao dem keys' wrote.
Every number here was counted rather than sampled or estimated.

The pass reads all of the files once, together. The key files are sorted, so
walking them in step yields each distinct document with the set of sources that
hold it, and that set answers every pairwise intersection, the union, and what
each source contributes that nothing else does. Five sources is ten pairs, and
measuring the pairs one at a time would read the same document three times to
learn what one read already said.

Overlap is printed from both sides because it is not symmetric. All of a small
source can sit inside a large one while very little of the large one sits inside
the small one, and a single number for the pair reports neither.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	m, err := dem.Measure(fs.Args()...)
	if err != nil {
		fmt.Fprintf(stderr, "gao dem overlap: %v\n", err)
		return 1
	}
	if *asJSON {
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "gao dem overlap: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", b)
		return 0
	}
	printOverlap(stdout, m)
	return 0
}

// printOverlap is separate from the command so the tables can be tested without
// key files on disk.
func printOverlap(w io.Writer, m dem.Matrix) {
	fmt.Fprintf(w, "%d sources, %d documents read, %d of them different\n\n", len(m.Sources), m.Documents, m.Distinct)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "source\tdocuments\tdistinct\tonly here\trepeats\n")
	for _, s := range m.Sources {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\n", s.Name, s.Documents, s.Distinct, s.Only, percent(s.Duplication()))
	}
	fmt.Fprint(tw, "\t\t\t\t\n")
	fmt.Fprintf(tw, "union\t%d\t%d\t\t%s\n", m.Documents, m.Distinct, percent(m.Duplication()))
	_ = tw.Flush()

	if len(m.Pairs) == 0 {
		fmt.Fprint(w, "\none source, so there is nothing to compare it with\n")
		return
	}

	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "pair\tin both\tof the first\tof the second\n")
	for _, p := range m.Pairs {
		fmt.Fprintf(tw, "%s and %s\t%d\t%s\t%s\n", p.A, p.B, p.Both, percent(m.Share(p.A, p.B)), percent(m.Share(p.B, p.A)))
	}
	_ = tw.Flush()
}

// The three levels a verification run can go to. They are cumulative: reading
// text without having added the columns up first would check a sample of a
// corpus whose total nobody had checked.
const (
	levelPlan = iota
	levelCounts
	levelText
)

// dirs is a flag that can be given more than once, which is what four boxes each
// writing their own counts need.
type dirs []string

func (d *dirs) String() string { return strings.Join(*d, ", ") }

func (d *dirs) Set(v string) error {
	*d = append(*d, v)
	return nil
}

func runDemVerify(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dem verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var counts dirs
	repo := fs.String("repo", kho.StageRepo, "the dataset repo the parts are in")
	dir := fs.String("dir", "verify", "where the resume records are kept")
	fs.Var(&counts, "counts", "an ingest directory whose counts to check, repeatable")
	level := fs.String("level", "plan", "how far to go: plan, counts or text")
	share := fs.Float64("share", 0.05, "the share of bad parts the sample is sized to catch")
	confidence := fs.Float64("confidence", 0.99, "how sure to be of that")
	seed := fs.String("seed", "", "the sample seed, defaulting to the snapshot name")
	rate := fs.Float64("rate", 100, "the link rate in megabits the budget assumes")
	model := fs.String("tokenizer", "", "the pinned tokenizer, without which the token column is not checked")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao dem verify [-level plan|counts|text] [flags] [SNAPSHOT ...]

Checks a published count against the store it came from, without downloading the
corpus. With no snapshot it checks every snapshot the repo holds.

The obvious way to verify a corpus size is to count the text again, and at the
size gao is that is a week of somebody's bandwidth, so nobody does it, and a
number nobody checks is a number nobody has to be right about. This is the way
that can actually be run.

Level one adds up n_chars, n_syllables and n_tokens over every part in the store.
Those are fixed width columns, so it moves twelve bytes per document rather than
the document, and it covers the corpus completely. What it proves is that the
published total is the sum of what is stored, and what it catches is a report
written from a run that did not finish, a source counted twice, a part that never
arrived, and arithmetic.

Level two reads a sample of parts all the way through and counts each document
from its own text. That is the only way to catch a column that lies, which level
one cannot see: a stage that rewrote text and forgot to recount leaves columns
adding up perfectly to a total describing text nobody has.

The sample is sized from the bound wanted rather than from what seemed like
enough. Missing a fifth of a corpus takes 21 parts at 99% confidence, a twentieth
takes 90, a hundredth takes 459, so the cost is driven by how localized a fault
you want to catch and almost not at all by how sure you want to be. It is picked
by hashing the seed with each part's path, which makes it the same sample on
anybody's machine and leaves the parts already checked alone when the snapshot
grows.

What neither level catches is a corpus that is uniformly a little off, since
level one reads the columns the report was made from and level two would have to
read every part. A bound over how many parts are wrong says nothing about how
wrong any one of them is.

Both levels are resumable at the part, and reading a working repo needs a token
in `+may.TokenEnv+` with access to it.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	want, ok := verifyLevel(*level)
	if !ok {
		fmt.Fprintf(stderr, "gao dem verify: no level named %q, and there are three: plan, counts, text\n", *level)
		return 2
	}

	d, ok := kho.Lookup(*repo)
	if !ok {
		fmt.Fprintf(stderr, "gao dem verify: no dataset named %q\n", *repo)
		fmt.Fprintln(stderr, "run 'gao kho datasets' for the list")
		return 1
	}
	s := &dem.Store{Repo: d.Repo(), Token: may.Token(), API: pushAPI()}

	var claimed dem.Report
	if len(counts) > 0 {
		reports := make([]dem.Report, 0, len(counts))
		for _, c := range counts {
			r, err := dem.ReadReport(c)
			if err != nil {
				fmt.Fprintf(stderr, "gao dem verify: %v\n", err)
				return 1
			}
			reports = append(reports, r)
		}
		merged, err := dem.Merge(reports...)
		if err != nil {
			fmt.Fprintf(stderr, "gao dem verify: %v\n", err)
			return 1
		}
		claimed = merged
	}

	var tok *dem.Tokenizer
	if *model != "" {
		t, err := dem.Open(dem.Gemma3, *model)
		if err != nil {
			fmt.Fprintf(stderr, "gao dem verify: %v\n", err)
			return 1
		}
		tok = t
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	snapshots := fs.Args()
	if len(snapshots) == 0 {
		found, err := s.Snapshots(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "gao dem verify: %v\n", err)
			return 1
		}
		if len(found) == 0 {
			fmt.Fprintf(stdout, "%s holds no ingested snapshots\n", d.Repo())
			return 0
		}
		snapshots = found
	}

	stored := map[doc.Source]dem.Counts{}
	var spots []dem.Spot
	for _, snapshot := range snapshots {
		parts, err := s.Parts(ctx, snapshot)
		if err != nil {
			fmt.Fprintf(stderr, "gao dem verify: %v\n", err)
			return 1
		}
		if len(parts) == 0 {
			fmt.Fprintf(stderr, "gao dem verify: %s holds no parts of %s\n", d.Repo(), snapshot)
			return 1
		}
		source := doc.Source(dem.SourceOf(snapshot))
		sown := *seed
		if sown == "" {
			sown = snapshot
		}
		printVerifyPlan(stdout, dem.Planned(snapshot, parts, claimedDocuments(claimed, source), *share, *confidence, sown), d.Repo(), *rate)
		if want == levelPlan {
			continue
		}

		fmt.Fprintf(stdout, "\nadding up the shape columns of %d parts\n", len(parts))
		c, err := dem.RecountOf(ctx, s, snapshot, *dir, func(part kho.Stored, i, of int, c dem.Counts, moved int64) {
			fmt.Fprintf(stdout, "  %4d/%d  %-52s %10d documents, %s read so far\n",
				i, of, filepath.Base(part.Path), c.Documents, may.Size(moved))
		})
		if err != nil {
			fmt.Fprintf(stderr, "gao dem verify: %v\n", err)
			return 1
		}
		printShape(stdout, snapshot, c)

		into := stored[source]
		into.Merge(c)
		stored[source] = into
		if want < levelText {
			continue
		}

		sample := dem.Sample(parts, dem.SampleSize(len(parts), *share, *confidence), sown)
		fmt.Fprintf(stdout, "\nreading %d of %d parts all the way through\n", len(sample), len(parts))
		for _, part := range sample {
			spot, err := dem.SpotPart(ctx, s, part, tok)
			if err != nil {
				fmt.Fprintf(stderr, "gao dem verify: %v\n", err)
				return 1
			}
			printSpot(stdout, spot)
			spots = append(spots, spot)
		}
	}

	ok = true
	if len(counts) > 0 && want >= levelCounts {
		ok = printDifferences(stdout, dem.Compare(claimed, stored))
	}
	if len(spots) > 0 {
		ok = printSpots(stdout, spots, *share, *confidence) && ok
	}
	if !ok {
		return 1
	}
	return 0
}

func verifyLevel(name string) (int, bool) {
	switch name {
	case "plan":
		return levelPlan, true
	case "counts":
		return levelCounts, true
	case "text":
		return levelText, true
	}
	return 0, false
}

// claimedDocuments is what a report says a source is, or zero when nothing claims
// anything about it yet. It sizes level one and level one runs either way.
func claimedDocuments(r dem.Report, source doc.Source) int64 {
	for _, s := range r.Sources {
		if s.Source == source {
			return s.Documents
		}
	}
	return 0
}

// printPlan says what the run is about to do and what it will cost, before it
// starts. A protocol whose cost is only knowable by starting it is a protocol
// that gets started on a Friday.
func printVerifyPlan(w io.Writer, p dem.Plan, repo string, rate float64) {
	fmt.Fprintf(w, "\n%s in %s\n", p.Snapshot, repo)
	fmt.Fprintf(w, "  parts      %d, %s in the store\n", p.Parts, may.GB(p.Bytes))
	if p.Documents > 0 {
		fmt.Fprintf(w, "  level one  every part, %s of columns over %d documents, %s\n",
			may.Size(p.Columns), p.Documents, budget(p.Columns, rate))
	} else {
		fmt.Fprint(w, "  level one  every part, columns only, and no report yet to size that from\n")
	}
	fmt.Fprintf(w, "  level two  %d parts read in full, %s, %s\n", len(p.Sample), may.GB(p.SampleBytes), budget(p.SampleBytes, rate))
	fmt.Fprintf(w, "  bound      no more than %s of parts wrong, at %s confidence\n", percent(p.Share), percent(p.Confidence))
	fmt.Fprintf(w, "  seed       %s\n", p.Seed)
	fmt.Fprintf(w, "  counting the text again instead would be %s\n", budget(p.Bytes, rate))
}

// budget prints a download in the unit somebody would plan it in. Hours for an
// afternoon, days for the thing this whole approach exists to avoid.
func budget(bytes int64, mbit float64) string {
	h := dem.Hours(bytes, mbit)
	if h == 0 {
		return "no budget without a link rate"
	}
	at := fmt.Sprintf(" at %g Mbit", mbit)
	switch m := h * 60; {
	case m < 1:
		return "under a minute" + at
	case h < 1:
		return fmt.Sprintf("%.0f minutes%s", m, at)
	case h > 48:
		return fmt.Sprintf("%.1f days%s", h/24, at)
	default:
		return fmt.Sprintf("%.1f hours%s", h, at)
	}
}

// printShape prints what one snapshot's columns add up to.
func printShape(w io.Writer, snapshot string, c dem.Counts) {
	fmt.Fprintf(w, "\n%s adds up out of its own columns to\n", snapshot)
	fmt.Fprintf(w, "  documents  %d\n", c.Documents)
	fmt.Fprintf(w, "  chars      %d\n", c.Chars)
	fmt.Fprintf(w, "  syllables  %d\n", c.Syllables)
	fmt.Fprintf(w, "  tokens     %s\n", tokenColumn(c))
	fmt.Fprint(w, "  bytes      nothing stores the byte length of the text, so level two is where that comes from\n")
}

// printDifferences puts the published counts beside the store, and reports
// whether they are the same numbers.
func printDifferences(w io.Writer, diffs []dem.Difference) bool {
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "source\t\tdocuments\tchars\tsyllables\ttokens\n")
	for _, d := range diffs {
		fmt.Fprintf(tw, "%s\tclaimed\t%d\t%d\t%d\t%s\n",
			d.Source, d.Claimed.Documents, d.Claimed.Chars, d.Claimed.Syllables, tokenColumn(d.Claimed))
		fmt.Fprintf(tw, "\tstored\t%d\t%d\t%d\t%s\n",
			d.Stored.Documents, d.Stored.Chars, d.Stored.Syllables, tokenColumn(d.Stored))
		if !d.Agrees() {
			fmt.Fprintf(tw, "\tdiffers on\t%s\n", strings.Join(d.Off(), ", "))
		}
	}
	_ = tw.Flush()

	if dem.Agree(diffs) {
		fmt.Fprint(w, "\nthe published counts are the counts in the store, in every unit a column holds\n")
		return true
	}
	fmt.Fprint(w, "\nthe published counts are not the counts in the store, and the rows above say where\n")
	return false
}

// printSpot prints one part read in full.
func printSpot(w io.Writer, s dem.Spot) {
	if s.Agrees() {
		fmt.Fprintf(w, "  %-52s %8d documents, %s of text, every column describes it\n",
			filepath.Base(s.Part), s.Documents, may.Size(s.Counted.Bytes))
		return
	}
	fmt.Fprintf(w, "  %-52s %8d documents, %d of them wrong\n", filepath.Base(s.Part), s.Documents, s.Wrong)
	for _, m := range s.Mismatches {
		fmt.Fprintf(w, "      row %d, %s: %s says %d and its text counts %d\n", m.Row, m.DocID, m.Column, m.Stored, m.Counted)
	}
	if rest := s.Wrong - int64(len(s.Mismatches)); rest > 0 {
		fmt.Fprintf(w, "      and %d more like it\n", rest)
	}
}

// printSpots says what the sample proves and, as importantly, what it does not.
func printSpots(w io.Writer, spots []dem.Spot, share, confidence float64) bool {
	var counted dem.Counts
	var wrong, parts int64
	for _, s := range spots {
		counted.Merge(s.Counted)
		wrong += s.Wrong
		if !s.Agrees() {
			parts++
		}
	}

	fmt.Fprintf(w, "\nread %d parts in full, %s of text, %d documents\n", len(spots), may.GB(counted.Bytes), counted.Documents)
	fmt.Fprintf(w, "columns checked: %s\n", strings.Join(spots[0].Checked, ", "))
	if counted.Chars > 0 {
		fmt.Fprintf(w, "measured on the sample: %.2f bytes per character\n", counted.BytesPerChar())
	}
	if wrong > 0 {
		fmt.Fprintf(w, "\n%d documents across %d parts carry columns that do not describe their text\n", wrong, parts)
		return false
	}
	fmt.Fprintf(w, "\nno more than %s of parts are wrong, at %s confidence\n", percent(share), percent(confidence))
	fmt.Fprint(w, "a corpus that is uniformly a little off would pass this, since both levels would have to read every part to see it\n")
	return true
}

// percent prints a share the way a release note reads it. A tenth of a point is
// as fine as these numbers are worth reporting, and zero prints as zero rather
// than as a rounded nothing.
func percent(f float64) string {
	if f <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", f*100)
}
