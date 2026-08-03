package main

import (
	"crypto/ed25519"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
  verify  check a snapshot against its manifest
  keygen  generate a snapshot signing key

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
