package main

import (
	"crypto/ed25519"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

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
	case "keygen":
		return runKhoKeygen(stdout, stderr, args[1:])
	case "datasets":
		return runKhoDatasets(stdout, stderr, args[1:])
	case "columns":
		return runKhoColumns(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		khoUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao kho: unknown subcommand %q\n", args[0])
		khoUsage(stderr)
		return 2
	}
}

func khoUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao kho <subcommand> [flags]

subcommands:
  verify    check a snapshot against its manifest
  keygen    generate a snapshot signing key
  datasets  print the dataset repos processed data is written to
  columns   print the columns a published parquet file carries

run 'gao kho <subcommand> -h' for the flags of a single subcommand.
`)
}

func runKhoVerify(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("kho verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.String("key", "", "the public key to require, as hex or as a path to a key file")
	quick := fs.Bool("quick", false, "check the manifest, the root, and the signature, and skip the shard bytes")
	verbose := fs.Bool("v", false, "print each shard as it is checked")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao kho verify [-key KEY] [-quick] [-v] <dir>

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
			fmt.Fprintf(stderr, "gao kho verify: %v\n", err)
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
		fmt.Fprintf(stderr, "gao kho verify: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "snapshot %s\n", report.Snapshot)
	if report.Parent != "" {
		fmt.Fprintf(stdout, "  parent    %s\n", report.Parent)
	}
	fmt.Fprintf(stdout, "  signer    %s\n", report.Signer)
	fmt.Fprintf(stdout, "  documents %d\n", report.Documents)
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

func runKhoKeygen(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("kho keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "gao", "prefix for the key files, written as PREFIX.key and PREFIX.pub")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao kho keygen [-out PREFIX]

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
			fmt.Fprintf(stderr, "gao kho keygen: %v\n", err)
			return 1
		}
	}

	pub, priv, err := kho.GenerateKey()
	if err != nil {
		fmt.Fprintf(stderr, "gao kho keygen: %v\n", err)
		return 1
	}
	if err := kho.WritePrivateKey(privPath, priv); err != nil {
		fmt.Fprintf(stderr, "gao kho keygen: %v\n", err)
		return 1
	}
	if err := kho.WritePublicKey(pubPath, pub); err != nil {
		// The private key is on disk and the public key is not, which is a state
		// nobody should have to reason about. Take the private key back out.
		_ = os.Remove(privPath)
		fmt.Fprintf(stderr, "gao kho keygen: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "wrote %s and %s\n", privPath, pubPath)
	fmt.Fprintf(stdout, "public key %x\n", []byte(pub))
	fmt.Fprintln(stdout, "\npublish the public key with the corpus and keep the private key off every box that runs a crawler")
	return 0
}

func runKhoDatasets(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("kho datasets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	snapshot := fs.String("snapshot", "gao-v1.0", "the snapshot to print read queries for")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao kho datasets [-snapshot NAME]\n\nPrints the dataset repos processed data is written to, what each one holds, and\nthe query that reads one snapshot of it straight off the Hub.\n\nflags:\n")
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
	fmt.Fprint(stdout, "\nworking, private, and deleted when the snapshot that consumed it seals\n")
	printDatasets(stdout, kho.Working, *snapshot)

	fmt.Fprintf(stdout, "\none parquet file per shard, at %s\n", kho.DataPath(*snapshot, 1, 774))
	fmt.Fprint(stdout, "the path is a function of the snapshot and the shard, so pushing a shard twice\noverwrites rather than duplicates and a retry after a dropped connection is safe\n")
	return 0
}

func runKhoColumns(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("kho columns", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("dataset", "vietnamese-web-text", "the dataset repo whose columns to print")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao kho columns [-dataset NAME] [file.parquet]

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
		fmt.Fprintf(stderr, "gao kho columns: no dataset named %q\n", *name)
		fmt.Fprintln(stderr, "run 'gao kho datasets' for the list")
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

func printFileColumns(stdout, stderr io.Writer, path string) int {
	columns, err := kho.PartColumns(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao kho columns: %v\n", err)
		return 1
	}
	meta, err := kho.PartMetadata(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao kho columns: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "%s\n", path)
	for _, k := range slices.Sorted(maps.Keys(meta)) {
		fmt.Fprintf(stdout, "  %-18s %s\n", k, meta[k])
	}
	fmt.Fprintf(stdout, "\n%d columns\n\n", len(columns))
	for _, c := range columns {
		fmt.Fprintf(stdout, "  %s\n", c)
	}
	if !slices.Contains(columns, kho.TextColumn) {
		fmt.Fprintf(stdout, "\nthis file withholds %s, so the column is absent and not empty\n", kho.TextColumn)
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
		if d.Public() {
			fmt.Fprintf(w, "    %s\n", d.Query(snapshot))
		}
	}
}
