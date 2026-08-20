package main

// Rewriting a published part whose strings are not text.
//
// A Parquet string column is defined as UTF-8 and the web does not agree. A link
// that percent encodes its path in something else decodes to bytes, the bytes
// became the url_template column, and DuckDB then refused the whole file rather
// than the one row that carried them: eight of the parts published from the first
// crawl runs could not be read at all. The crawler no longer writes them, and
// this is what fixes the files that were written before it stopped.
//
// It is deliberately narrow. It reads a part, puts every row back through the
// conversion that now checks, and writes the file again with the same stamp. It
// does not re-measure anything, it does not drop rows, and a part that is already
// text is left alone and said to be.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/store"
)

func runStoreRepair(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("store repair", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("dataset", "", "the dataset the parts belong to, which decides whether they carry rejection columns")
	push := fs.Bool("push", false, "send each repaired part back to the repo it came from")
	as := fs.String("prefix", "", "the directory inside the repo the parts sit under, which defaults to the dataset's own layout")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao store repair -dataset NAME [-push] [-prefix PATH] <part>...

Rewrites parts whose string columns hold bytes that are not UTF-8.

A Parquet string column is defined as UTF-8. A crawler takes what servers send,
and a link whose path is percent encoded in another encoding decodes to bytes
that are not text. Those bytes reached the published columns, and a reader that
checks the definition refuses the file rather than the row: DuckDB stops the
whole query with "value ... is not valid UTF8". One bad link cost a part.

Every row is read, put back through the conversion that now replaces those bytes,
and written again under the stamp the file already carried. Nothing is
re-measured and no row is dropped. A part whose strings are already text is left
untouched and reported as clean, so this is safe to run over a whole directory.

With -push each repaired part goes back to the repo at the path it came from,
which needs a token in `+fleet.TokenEnv+` with write access.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	d, ok := store.Lookup(*name)
	if !ok {
		fmt.Fprintf(stderr, "gao store repair: no dataset named %q\n", *name)
		fmt.Fprintln(stderr, "run 'gao store datasets' for the list")
		return 1
	}

	var pusher *store.Pusher
	ctx := context.Background()
	if *push {
		pusher = &store.Pusher{Repo: d.Repo(), Token: fleet.Token(), API: pushAPI()}
		if err := pusher.EnsureRepo(ctx, d); err != nil {
			fmt.Fprintf(stderr, "gao store repair: %v\n", err)
			return 1
		}
	}

	repaired, clean := 0, 0
	for _, path := range fs.Args() {
		rows, snapshot, err := repairPart(path, d)
		if err != nil {
			fmt.Fprintf(stderr, "gao store repair: %s: %v\n", path, err)
			return 1
		}
		if rows == 0 {
			clean++
			fmt.Fprintf(stdout, "clean    %s\n", filepath.Base(path))
			continue
		}
		repaired++
		fmt.Fprintf(stdout, "repaired %s, %d rows held bytes that are not text\n", filepath.Base(path), rows)
		if pusher == nil {
			continue
		}
		at := *as
		if at == "" {
			at = store.SourceDir(snapshot)
		}
		to := at + "/" + filepath.Base(path)
		sent, err := pusher.Push(ctx, path, to)
		if err != nil {
			fmt.Fprintf(stderr, "gao store repair: pushing %s: %v\n", to, err)
			return 1
		}
		fmt.Fprintf(stdout, "pushed   %s to %s, %s\n", to, d.Repo(), fleet.Size(sent.Bytes))
	}
	fmt.Fprintf(stdout, "\n%d repaired, %d already text\n", repaired, clean)
	return 0
}

// repairPart rewrites one part in place and returns how many of its rows carried
// bytes that are not text. Zero means the file was left alone.
func repairPart(path string, d store.Dataset) (bad int, snapshot string, err error) {
	meta, err := store.PartMetadata(path)
	if err != nil {
		return 0, "", err
	}
	stamp := store.Stamp{
		Snapshot:  meta["gao.snapshot"],
		Stage:     meta["gao.stage"],
		Box:       meta["gao.box"],
		Tokenizer: meta["gao.tokenizer"],
	}

	// Read the whole file. A part is a shard rather than a corpus and this runs
	// once over the handful of files a bug reached, so holding it is cheaper
	// than streaming it into a second file and swapping on success.
	var (
		rows []store.Row
		rej  []store.RejectRow
	)
	if d.Reject {
		rej, err = store.ReadRejectPart(path)
		if err != nil {
			return 0, "", err
		}
		for _, r := range rej {
			if !rowIsText(r.Row) || !utf8.ValidString(r.RejectDetail) {
				bad++
			}
		}
	} else {
		rows, err = store.ReadPart(path)
		if err != nil {
			return 0, "", err
		}
		for _, r := range rows {
			if !rowIsText(r) {
				bad++
			}
		}
	}
	if bad == 0 {
		return 0, stamp.Snapshot, nil
	}

	// Written beside the original and moved over it, so an interrupted repair
	// leaves the part that was there rather than half of a new one.
	tmp := path + ".repair"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, "", err
	}
	w := store.NewParquetWriter(f, d, stamp)
	write := func() error {
		for _, r := range rej {
			if err := w.AppendReject(store.DocumentOf(r.Row), r.RejectStage, r.RejectReason, r.RejectDetail); err != nil {
				return err
			}
		}
		for _, r := range rows {
			if err := w.Append(store.DocumentOf(r)); err != nil {
				return err
			}
		}
		return w.Close()
	}
	if err := write(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return 0, "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, "", err
	}
	return bad, stamp.Snapshot, nil
}

// rowIsText reports whether every string column of a row is what a Parquet
// string column is defined to hold.
//
// The columns listed are the ones that carry bytes somebody else chose. The rest
// are written by gao out of its own vocabulary and cannot be anything else.
func rowIsText(r store.Row) bool {
	for _, s := range []string{r.Text, r.URL, r.Host, r.URLTemplate, r.MediaType, r.RobotsRule, r.LicenseEvidence} {
		if !utf8.ValidString(s) {
			return false
		}
	}
	for _, m := range []map[string]string{r.TDMSignals, r.UpstreamFields} {
		for k, v := range m {
			if !utf8.ValidString(k) || !utf8.ValidString(v) {
				return false
			}
		}
	}
	return true
}
