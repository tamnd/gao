package main

import (
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
	"github.com/tamnd/gao/may"
)

func runKho(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		khoUsage(stderr)
		return 2
	}
	switch args[0] {
	case "verify":
		return runKhoVerify(stdout, stderr, args[1:])
	case "remove":
		return runKhoRemove(stdout, stderr, args[1:])
	case "reproduce":
		return runKhoReproduce(stdout, stderr, args[1:])
	case "keygen":
		return runKhoKeygen(stdout, stderr, args[1:])
	case "datasets":
		return runKhoDatasets(stdout, stderr, args[1:])
	case "columns":
		return runKhoColumns(stdout, stderr, args[1:])
	case "schema":
		return runKhoSchema(stdout, stderr, args[1:])
	case "push":
		return runKhoPush(stdout, stderr, args[1:])
	case "order":
		return runKhoOrder(stdout, stderr, args[1:])
	case "card":
		return runKhoCard(stdout, stderr, args[1:])
	case "move":
		return runKhoMove(stdout, stderr, args[1:])
	case "index":
		return runKhoIndex(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		khoUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao store: unknown subcommand %q\n", args[0])
		khoUsage(stderr)
		return 2
	}
}

func khoUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao store <subcommand> [flags]

subcommands:
  verify    check a snapshot against its manifest
  remove    take documents out of a snapshot, into a new one
  reproduce rebuild a snapshot's bytes and check they come out the same
  keygen    generate a snapshot signing key
  datasets  print the dataset repos processed data is written to
  columns   print the columns a published parquet file carries
  schema    print the full record schema, every column and what it means
  push      upload a file to a dataset repo at the path it belongs at
  card      generate a dataset card from a snapshot manifest
  index     read every part's footer and write the parts index
  move      re-lay a dataset repo into another one without the bytes traveling
  order     what sorting a shard by host buys, and what holding it resident costs

run 'gao store <subcommand> -h' for the flags of a single subcommand.
`)
}

func runKhoVerify(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.String("key", "", "the public key to require, as hex or as a path to a key file")
	quick := fs.Bool("quick", false, "check the manifest, the root, and the signature, and skip the shard bytes")
	verbose := fs.Bool("v", false, "print each shard as it is checked")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store verify [-key KEY] [-quick] [-v] <dir>

Checks that a snapshot is what its manifest says it is: the manifest is
complete, the merkle root matches the shard hashes, the signature verifies, and
every shard file on disk hashes to the value recorded for it. A shard file that
the manifest does not list is a failure too.

Without -key the signature is checked against the key embedded in the manifest,
which proves the snapshot was signed by somebody and not that it was signed by
us. Pass -key with the published gao key to check the thing you meant to check.

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
	dir := fs.Arg(0)

	opts := []kho.VerifyOption{}
	if *key != "" {
		pub, err := loadVerifyKey(*key)
		if err != nil {
			fmt.Fprintf(stderr, "gao store verify: %v\n", err)
			return 1
		}
		opts = append(opts, kho.TrustKey(pub))
	}
	if *quick {
		opts = append(opts, kho.Quick())
	}
	if *verbose {
		opts = append(opts, kho.Progress(func(s kho.Shard, err error) {
			if err != nil {
				fmt.Fprintf(stdout, "  %s  FAILED\n", s.Name)
				return
			}
			fmt.Fprintf(stdout, "  %s  ok  %d documents, %s\n", s.Name, s.Documents, may.Size(s.Bytes))
		}))
	}

	report, err := kho.Verify(dir, opts...)
	if err != nil {
		fmt.Fprintf(stderr, "gao store verify: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "snapshot %s\n", report.Snapshot)
	if report.Parent != "" {
		fmt.Fprintf(stdout, "  parent    %s\n", report.Parent)
	}
	fmt.Fprintf(stdout, "  signer    %s\n", report.Signer)
	fmt.Fprintf(stdout, "  documents %d\n", report.Documents)
	if pub := report.Publishable; pub.Documents > 0 {
		fmt.Fprintf(stdout, "  of those  %d may be redistributed, %s of text\n", pub.Documents, may.Size(pub.Bytes))
	}
	if *quick {
		fmt.Fprintf(stdout, "  shards    %d listed, none hashed because -quick was given\n", report.Shards)
		fmt.Fprintln(stdout, "\nthe manifest is coherent and signed, and the bytes were not checked")
		return 0
	}
	fmt.Fprintf(stdout, "  shards    %d checked, %s\n", report.Checked, may.Size(report.Bytes))
	fmt.Fprintln(stdout, "\nok")
	return 0
}

// loadVerifyKey takes either the key itself or a path to a file holding it,
// because a release note carries the hex and a build script carries the path.
func loadVerifyKey(s string) (ed25519.PublicKey, error) {
	if pub, err := kho.ParsePublicKey(s); err == nil {
		return pub, nil
	}
	return kho.LoadPublicKey(s)
}

func runKhoRemove(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "the snapshot to remove from, which is not modified")
	to := fs.String("to", "", "the directory to write the new snapshot to, which must not already hold one")
	name := fs.String("snapshot", "", "the new snapshot's name")
	keyPath := fs.String("key", "", "path to the signing key for the new snapshot")
	reason := fs.String("reason", "", "why, one of "+strings.Join(kho.Reasons(), ", "))
	note := fs.String("note", "", "a line for whoever reads the record later, which must not quote the document")
	list := fs.String("list", "", "read document ids from a file, one per line, instead of from the arguments")
	verbose := fs.Bool("v", false, "print each shard as it is dealt with")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store remove -from DIR -to DIR -snapshot NAME -key FILE -reason REASON [flags] [docid...]

Takes documents out of a published snapshot. This is the one command here that
destroys data on purpose, so it is worth knowing exactly what it does.

It does not edit the snapshot named by -from. A snapshot is immutable and its
manifest is signed, so a removal writes a new snapshot that names the old one as
its parent and carries a tombstone for every document taken out. A tombstone
keeps the document identity and nothing else: no text, no url, no host. What
happens to the parent afterwards, whether it is withdrawn or left up for the
people who already have it, is a publication decision this command will not make
for you.

The parent has to verify completely before anything is written, and the shards
that held none of the named documents are copied across byte for byte with the
hashes the parent recorded for them. A takedown that touches two shards out of
750 rewrites two files.

Naming a document that is not in the parent fails the run and writes nothing,
even if the other identities were all found. A takedown answered with a
signature and a report that quietly covers three documents out of four is the
worst outcome available here, because everybody involved reads it as done, and
an identity that is not there is far more likely to be the wrong identity than
an empty request. Running the same removal twice is not an error: the second run
finds the documents already tombstoned and says so.

The key is read from a file rather than the command line, because an argument
ends up in somebody's shell history.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case *from == "", *to == "", *name == "", *keyPath == "":
		fs.Usage()
		return 2
	}
	if !slices.Contains(kho.Reasons(), *reason) {
		fmt.Fprintf(stderr, "gao store remove: -reason must be one of %s\n", strings.Join(kho.Reasons(), ", "))
		return 2
	}

	ids, err := removalIDs(fs.Args(), *list)
	if err != nil {
		fmt.Fprintf(stderr, "gao store remove: %v\n", err)
		return 2
	}
	if len(ids) == 0 {
		fmt.Fprintln(stderr, "gao store remove: no documents named, so there is nothing to remove")
		return 2
	}

	key, err := kho.LoadPrivateKey(*keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "gao store remove: %v\n", err)
		return 1
	}

	rs := make([]kho.Removal, len(ids))
	for i, id := range ids {
		rs[i] = kho.Removal{DocID: id, Reason: *reason, Note: *note}
	}

	opts := []kho.RemoveOption{}
	if *verbose {
		opts = append(opts, kho.RemoveProgress(func(shard string, rewritten bool) {
			what := "copied"
			if rewritten {
				what = "rewritten"
			}
			fmt.Fprintf(stdout, "  %s  %s\n", shard, what)
		}))
	}

	report, err := kho.Remove(*from, *to, *name, key, rs, opts...)
	if err != nil {
		// An identity that is not in the parent gets the identities printed out
		// rather than run together in one line, because the next thing anybody
		// does is go and look for the one they mistyped.
		if report != nil && len(report.NotFound) > 0 {
			fmt.Fprintf(stderr, "gao store remove: %d of the identities given are not in %s:\n", len(report.NotFound), report.Parent)
			for _, id := range report.NotFound {
				fmt.Fprintf(stderr, "  %s\n", id)
			}
			fmt.Fprintln(stderr, "nothing was written, because a removal that answers for some of a request and not the rest is the outcome everybody reads as done")
			return 1
		}
		fmt.Fprintf(stderr, "gao store remove: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "snapshot %s\n", report.Snapshot)
	fmt.Fprintf(stdout, "  parent    %s\n", report.Parent)
	fmt.Fprintf(stdout, "  removed   %d documents, reason %s\n", len(report.Removed), *reason)
	if len(report.Tombstoned) > 0 {
		fmt.Fprintf(stdout, "  already   %d were tombstoned by an earlier removal\n", len(report.Tombstoned))
	}
	fmt.Fprintf(stdout, "  shards    %d rewritten, %d copied byte for byte\n", len(report.Rewritten), len(report.Copied))
	fmt.Fprintf(stdout, "  documents %d remain\n", report.Counts.Documents)

	// The new snapshot is checked here as well as sealed, because the only thing
	// worse than a takedown that did not happen is one everybody believes did.
	if _, err := kho.Verify(*to, kho.TrustKey(key.Public().(ed25519.PublicKey))); err != nil {
		fmt.Fprintf(stderr, "gao store remove: the snapshot was written and does not verify: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "\nok")
	return 0
}

// removalIDs collects the document identities to remove, from the arguments or
// from a file.
//
// A file is worth supporting because a request naming forty documents is a
// request somebody has in a file already, and retyping forty hashes onto a
// command line is how the wrong one gets removed. Blank lines and lines starting
// with # are skipped, so the file can carry the reference number it came in
// under.
func removalIDs(args []string, list string) ([]doc.Hash, error) {
	lines := slices.Clone(args)
	if list != "" {
		b, err := os.ReadFile(list)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			lines = append(lines, line)
		}
	}

	out := make([]doc.Hash, 0, len(lines))
	for _, line := range lines {
		// Everything after the identity is left for whoever wrote the file, which
		// is usually the title or the reference the request came in under.
		id, err := doc.ParseHash(strings.Fields(line)[0])
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func runKhoKeygen(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "gao", "prefix for the key files, written as PREFIX.key and PREFIX.pub")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store keygen [-out PREFIX]

Generates an ed25519 snapshot signing key. The private key is written to
PREFIX.key with mode 0600 and the public key to PREFIX.pub, both as one line of
hex. Neither file is overwritten if it already exists, because generating a new
key over the one every published snapshot was signed with is not recoverable.

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

	privPath := *out + ".key"
	pubPath := *out + ".pub"
	if dir := filepath.Dir(privPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "gao store keygen: %v\n", err)
			return 1
		}
	}

	pub, priv, err := kho.GenerateKey()
	if err != nil {
		fmt.Fprintf(stderr, "gao store keygen: %v\n", err)
		return 1
	}
	if err := kho.WritePrivateKey(privPath, priv); err != nil {
		fmt.Fprintf(stderr, "gao store keygen: %v\n", err)
		return 1
	}
	if err := kho.WritePublicKey(pubPath, pub); err != nil {
		// The private key is on disk and the public key is not, which is a state
		// nobody should have to reason about. Take the private key back out.
		_ = os.Remove(privPath)
		fmt.Fprintf(stderr, "gao store keygen: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "wrote %s and %s\n", privPath, pubPath)
	fmt.Fprintf(stdout, "public key %x\n", []byte(pub))
	fmt.Fprintln(stdout, "\npublish the public key with the corpus and keep the private key off every box that runs a crawler")
	return 0
}

func runKhoDatasets(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store datasets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	snapshot := fs.String("snapshot", "gao-v1.0", "the snapshot to print read queries for")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao store datasets [-snapshot NAME]\n\nPrints the dataset repos processed data is written to, what each one holds, and\nthe query that reads one snapshot of it straight off the Hub.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	fmt.Fprintf(stdout, "store of record: %s\n", kho.HubStore)
	if store, ok := may.Store(); ok && store != kho.HubStore {
		fmt.Fprintf(stdout, "%s is set to %s, so this run writes there instead\n", may.StoreEnv, store)
	}

	fmt.Fprint(stdout, "\npublished\n")
	printDatasets(stdout, kho.Published, *snapshot)
	fmt.Fprint(stdout, "\nworking, public like the rest, rewritten when a source is pinned again\n")
	printDatasets(stdout, kho.Working, *snapshot)

	fmt.Fprintf(stdout, "\none parquet file per shard, at %s\n", kho.DataPath(*snapshot, 1, 774))
	fmt.Fprint(stdout, "the path is a function of the snapshot and the shard, so pushing a shard twice\noverwrites rather than duplicates and a retry after a dropped connection is safe\n")
	return 0
}

func runKhoColumns(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store columns", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("dataset", "vietnamese-web-text", "the dataset repo whose columns to print")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store columns [-dataset NAME] [file.parquet]

Prints the columns a parquet file in that repo carries, which is the contract a
reader gets to depend on. Adding a column is a minor version and removing or
retyping one is a major version, so the list is worth reading before writing a
query against it.

A repo that withholds text has no text column rather than an empty one, so a
query that selects it fails at plan time instead of returning blanks that read
like documents with nothing in them. Pass -dataset to see that difference.

Given a file, the columns are read out of that file's footer along with the
snapshot, stage, and box that wrote it, which is how you check what you actually
have rather than what the current build would write.

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
	if fs.NArg() == 1 {
		return printFileColumns(stdout, stderr, fs.Arg(0))
	}

	d, ok := kho.Lookup(*name)
	if !ok {
		fmt.Fprintf(stderr, "gao store columns: no dataset named %q\n", *name)
		fmt.Fprintln(stderr, "run 'gao store datasets' for the list")
		return 1
	}

	columns := kho.Columns(kho.SchemaFor(d))
	fmt.Fprintf(stdout, "%s\n", d.Repo())
	fmt.Fprintf(stdout, "%d columns, schema version %d\n\n", len(columns), doc.SchemaVersion)
	for _, c := range columns {
		fmt.Fprintf(stdout, "  %s\n", c)
	}
	if !d.Text {
		fmt.Fprintf(stdout, "\nthis repo withholds %s, so the column is absent and not empty\n", kho.TextColumn)
	}
	return 0
}

func runKhoSchema(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store schema", flag.ContinueOnError)
	fs.SetOutput(stderr)
	md := fs.Bool("md", false, "print the schema page that ships as SCHEMA.md")
	def := fs.Bool("parquet", false, "print the parquet message definition")
	name := fs.String("dataset", "vietnamese-web-text", "the dataset repo whose definition to print, with -parquet")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store schema [-md] [-parquet [-dataset NAME]]

Prints every column of the published record, its type, the stage that fills it,
and what it holds. Where 'gao store columns' answers what a file carries, this
answers what the columns mean, which is the question somebody who did not build
the corpus has to have answered before they can use it.

The column names and types are read off the type that writes the files, so they
cannot drift from what ships. The meanings are written down beside that type,
and a column added without one fails the build.

-md prints the same thing as the SCHEMA.md in the repository, and -parquet
prints the message definition a parquet tool would show.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 || (*md && *def) {
		fs.Usage()
		return 2
	}

	switch {
	case *md:
		fmt.Fprint(stdout, kho.Page())
	case *def:
		d, ok := kho.Lookup(*name)
		if !ok {
			fmt.Fprintf(stderr, "gao store schema: no dataset named %q\n", *name)
			fmt.Fprintln(stderr, "run 'gao store datasets' for the list")
			return 1
		}
		fmt.Fprintln(stdout, kho.Definition(d))
	default:
		cols := append(kho.Schema(), kho.Nested()...)
		fmt.Fprintf(stdout, "%d columns, schema version %d\n\n", len(kho.Schema()), doc.SchemaVersion)
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, c := range cols {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Name, c.Type, c.Stage, c.Meaning)
		}
		_ = tw.Flush()
	}
	return 0
}

func runKhoPush(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store push", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("dataset", kho.StageRepo, "the dataset repo to push to")
	as := fs.String("as", "", "the path inside the repo, which defaults to the path given")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store push [-dataset NAME] [-as PATH] <file>

Uploads one file to a dataset repo. An ingest pushes its own parts as it writes
them, so this is for the files that are not parts: a dataset card, a manifest, or
a part an interrupted run left on a disk somebody is about to reclaim.

The path inside the repo defaults to the path given, in slashes, so pushing from
the directory an ingest wrote to puts a part back where it belongs without
anybody having to retype the layout. Use -as for anything else.

Pushing the same file twice is safe and nearly free. The store keys the bytes by
their digest, so the second push of a file that is already there sends nothing,
and it says so rather than reporting an upload that did not happen.

Writing needs a token in `+may.TokenEnv+` with write access to the `+kho.Org+`
organization.

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
	local := fs.Arg(0)

	d, ok := kho.Lookup(*name)
	if !ok {
		fmt.Fprintf(stderr, "gao store push: no dataset named %q\n", *name)
		fmt.Fprintln(stderr, "run 'gao store datasets' for the list")
		return 1
	}
	path := *as
	if path == "" {
		path = filepath.ToSlash(local)
	}

	p := &kho.Pusher{Repo: d.Repo(), Token: may.Token(), API: pushAPI()}
	ctx := context.Background()
	if err := p.EnsureRepo(ctx, d); err != nil {
		fmt.Fprintf(stderr, "gao store push: %v\n", err)
		return 1
	}
	sent, err := p.Push(ctx, local, path)
	if err != nil {
		fmt.Fprintf(stderr, "gao store push: %v\n", err)
		return 1
	}

	switch {
	case sent.Skipped():
		fmt.Fprintf(stdout, "%s already holds %s, %s, so nothing moved\n", d.Repo(), path, may.Size(sent.Bytes))
	case !sent.Transferred:
		fmt.Fprintf(stdout, "committed %s to %s, %s, whose bytes the store already held\n", path, d.Repo(), may.Size(sent.Bytes))
	default:
		fmt.Fprintf(stdout, "pushed %s to %s, %s\n", path, d.Repo(), may.Size(sent.Bytes))
	}
	fmt.Fprintf(stdout, "%s\n", sent.OID)
	return 0
}

func runKhoCard(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store card", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("dataset", "", "the dataset repo the card is for")
	from := fs.String("from", "", "the snapshot directory holding the manifest the card is generated from")
	index := fs.String("index", "", "the parts index the card's counts come from, for a repo with no manifest")
	push := fs.Bool("push", false, "put the card on the repo instead of printing it")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store card -dataset NAME [-from DIR] [-index PATH] [-push]

Generates the dataset card for a repo and prints it. Every number in it comes
out of the manifest of the snapshot named by -from: the counts, the breakdown by
source, the stages that produced it and the versions they ran at, the merkle root
and who signed it.

A working repo never gets a manifest, so its card is generated from the parts
index instead. Pass one with -index, or let `+"`gao store index -push`"+` do both at
once, which is what the pipeline runs. With neither the card is the one a repo
carries before it holds anything, which says so rather than printing zeros.

A card is generated rather than written because a card written by hand describes
the release before last, and there is no way to tell by reading one which of its
numbers have gone stale.

With -push the card is committed to the repo as `+kho.CardName+`, which is what
a release does after it seals a snapshot. A card that already says the same thing
is left alone. Writing needs a token in `+may.TokenEnv+` with write access to the
`+kho.Org+` organization.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *name == "" {
		fs.Usage()
		return 2
	}

	d, ok := kho.Lookup(*name)
	if !ok {
		fmt.Fprintf(stderr, "gao store card: no dataset named %q\n", *name)
		fmt.Fprintln(stderr, "run 'gao store datasets' for the list")
		return 1
	}

	var m *kho.Manifest
	if *from != "" {
		loaded, err := kho.ReadManifest(*from)
		if err != nil {
			fmt.Fprintf(stderr, "gao store card: %v\n", err)
			return 1
		}
		m = loaded
	}

	var x []kho.Indexed
	if *index != "" {
		f, err := os.Open(*index) //nolint:gosec // the path is one the operator named
		if err != nil {
			fmt.Fprintf(stderr, "gao store card: %v\n", err)
			return 1
		}
		x, err = kho.ReadIndex(f)
		_ = f.Close()
		if err != nil {
			fmt.Fprintf(stderr, "gao store card: %v\n", err)
			return 1
		}
	}

	if !*push {
		fmt.Fprint(stdout, kho.Card(d, m, x))
		return 0
	}

	p := &kho.Pusher{Repo: d.Repo(), Token: may.Token(), API: pushAPI()}
	ctx := context.Background()
	if err := p.EnsureRepo(ctx, d); err != nil {
		fmt.Fprintf(stderr, "gao store card: %v\n", err)
		return 1
	}
	sent, err := p.PushCard(ctx, d, m, x)
	if err != nil {
		fmt.Fprintf(stderr, "gao store card: %v\n", err)
		return 1
	}
	if sent.Skipped() {
		fmt.Fprintf(stdout, "%s already carries this card, so nothing moved\n", d.Repo())
		return 0
	}
	fmt.Fprintf(stdout, "pushed the card to %s, %s\n", d.Repo(), may.Size(sent.Bytes))
	return 0
}

// pushAPI is the endpoint a push goes to.
//
// It is the Hub, unless the store of record names an http endpoint instead of
// an hf:// one, in which case it is that. A store URI is already the answer to
// where the corpus lives, so a hub that is not the public one belongs there
// rather than in a flag on one subcommand, and it is what the tests point at.
func pushAPI() string {
	store, ok := may.Store()
	if !ok {
		return ""
	}
	if strings.HasPrefix(store, "http://") || strings.HasPrefix(store, "https://") {
		return strings.TrimSuffix(store, "/")
	}
	return ""
}

func printFileColumns(stdout, stderr io.Writer, path string) int {
	columns, err := kho.PartColumns(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao store columns: %v\n", err)
		return 1
	}
	meta, err := kho.PartMetadata(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao store columns: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "%s\n", path)
	for _, k := range slices.Sorted(maps.Keys(meta)) {
		v := meta[k]
		if v == "" {
			v = "none"
		}
		fmt.Fprintf(stdout, "  %-18s %s\n", k, v)
	}
	fmt.Fprintf(stdout, "\n%d columns\n\n", len(columns))
	for _, c := range columns {
		fmt.Fprintf(stdout, "  %s\n", c)
	}
	if !slices.Contains(columns, kho.TextColumn) {
		fmt.Fprintf(stdout, "\nthis file withholds %s, so the column is absent and not empty\n", kho.TextColumn)
	}
	// Nothing is nullable here, so a column nobody filled is a column of zeros
	// and a reader summing it gets a number rather than a refusal. This is the
	// only place the difference is recoverable from the file alone.
	if slices.Contains(columns, kho.TokensColumn) && meta["gao.tokenizer"] == "" {
		fmt.Fprintf(stdout, "\nno tokenizer ran, so %s is zero for every document here rather than counted\n", kho.TokensColumn)
	}
	return 0
}

func printDatasets(w io.Writer, tier kho.Tier, snapshot string) {
	for _, d := range kho.Datasets() {
		if d.Tier != tier {
			continue
		}
		carries := "url and metadata, no text"
		if d.Text {
			carries = "text"
		}
		fmt.Fprintf(w, "\n  %s\n", d.Repo())
		fmt.Fprintf(w, "    %s\n", d.Holds)
		fmt.Fprintf(w, "    carries %s, admits", carries)
		for _, c := range d.Classes {
			fmt.Fprintf(w, " %s", c)
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "    %s\n", d.Query(snapshot))
	}
}

func runKhoOrder(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store order", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	target := fs.Int64("target", may.ShardBytes, "the compressed size each shard is aimed at, in bytes")
	text := fs.Int64("text", 0, "the size of the corpus in bytes of extracted text, for the shard count")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store order [-json] [-target bytes] [-text bytes] readings.jsonl

What sorting a shard by host buys, measured rather than assumed.

Shards are assigned by hash, which makes each one a uniform sample of the
corpus and scatters every host across the file. Pages from one site share
their navigation, their footer and their URL prefix, and scattered they never
land inside the same compression window. Sorting by host inside the shard puts
them back together without changing which shard anything is in.

What it costs is that a stream stops being a stream. The shard has to be held
in memory to be sorted, which is over a gigabyte of text at the target size, on
a fleet whose smallest box has 6.2 GB. So the saving is only worth having if
somebody measured it: the same shard, both ways, at the same compression level,
on more than one box we own.

One reading per line, each naming the shard, the ordering, the level, the bytes
before and after, and the box. Exits 1 if the readings do not settle it.

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

	readings, err := kho.ReadReadings(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao store: %v\n", err)
		return 1
	}

	c := kho.Compare(*target, readings)
	out := khoOrderReport{Comparison: c, Verdict: c.Verdict(), Settled: c.Settled()}
	if *text > 0 {
		out.Shards = c.Shards(*text)
	}
	if *asJSON {
		if code := printJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		printKhoOrder(stdout, out, *text)
	}
	if !c.Settled() {
		return 1
	}
	return 0
}

type khoOrderReport struct {
	kho.Comparison
	Shards  int    `json:"shards,omitempty"`
	Verdict string `json:"verdict"`
	Settled bool   `json:"settled"`
}

func printKhoOrder(w io.Writer, out khoOrderReport, text int64) {
	c := out.Comparison
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "measured\t%s\ton %s\n", plural(len(c.Gains), "shard"), strings.Join(c.Boxes, " and "))
	fmt.Fprintf(tw, "saved\t%.1f%%\ton the middle shard, against a floor of %.0f%%\n", 100*c.Median, 100*kho.MinGain)
	fmt.Fprintf(tw, "ratio\t%.2f to 1\tsorted by host, which is what the disk budget gets written against\n", c.Ratio)
	fmt.Fprintf(tw, "target\t%s\tcompressed per shard\n", may.Size(c.Target))
	fmt.Fprintf(tw, "resident\t%s\tof text held in memory while one shard is sorted and written\n", may.Size(c.Resident))
	if text > 0 {
		fmt.Fprintf(tw, "shards\t%d\tfor %s of text at that ratio\n", out.Shards, may.Size(text))
	}
	_ = tw.Flush()

	if len(c.Gains) > 0 {
		fmt.Fprint(w, "\nper shard, best first:\n")
		gw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprint(gw, "  shard\tarrival\tsorted\tsaved\thosts\tbiggest\n")
		for _, g := range c.Gains {
			fmt.Fprintf(gw, "  %s\t%s\t%s\t%.1f%%\t%d\t%.0f%%\n",
				g.Shard, may.Size(g.Arrival.Compressed), may.Size(g.Sorted.Compressed), 100*g.Fraction, g.Sorted.Hosts, 100*g.Sorted.Biggest)
		}
		_ = gw.Flush()
	}

	fmt.Fprintf(w, "\n%s\n", out.Verdict)
	if why := c.Blocking(); len(why) > 1 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(why), "fault"))
		for _, x := range why {
			fmt.Fprintf(w, "  %s\n", x)
		}
	}
}
