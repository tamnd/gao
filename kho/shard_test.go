package kho

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// docID is the key every corpus shard set is built with.
func docID(d *doc.Document) doc.Hash { return d.DocID }

// fan writes n documents into a shard set of shards shards under dir and returns
// the shard records.
func fan(t *testing.T, dir string, n, shards int, opts ...ShardOption) []Shard {
	t.Helper()
	set, err := NewShardSet[*doc.Document](dir, shards, docID, opts...)
	if err != nil {
		t.Fatalf("NewShardSet: %v", err)
	}
	defer set.Abandon()

	for i := range n {
		if err := set.Append(sample(i)); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	out, err := set.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return out
}

func TestShardNameCarriesTheCount(t *testing.T) {
	name := ShardName(7, 750)
	if want := "shard-00007-of-00750.jsonl.zst"; name != want {
		t.Fatalf("ShardName(7, 750) = %q, want %q", name, want)
	}
	i, n, ok := ParseShardName(name)
	if !ok || i != 7 || n != 750 {
		t.Fatalf("ParseShardName(%q) = %d, %d, %v", name, i, n, ok)
	}
}

func TestParseShardNameRejectsAnythingElse(t *testing.T) {
	for _, name := range []string{
		"",
		"shard-7-of-750.jsonl.zst",            // unpadded, so it sorts wrong
		"shard-00007-of-00750.jsonl",          // not compressed
		"shard-00007-of-00750.jsonl.zst.part", // still being written
		"shard-00750-of-00750.jsonl.zst",      // index equal to the count
		"shard-00800-of-00750.jsonl.zst",      // index past the count
		"rejects-00007-of-00750.jsonl.zst",    // another store
		"manifest.toml",
	} {
		if _, _, ok := ParseShardName(name); ok {
			t.Errorf("ParseShardName(%q) accepted it", name)
		}
	}
}

func TestEveryDocumentLandsInItsHashShard(t *testing.T) {
	dir := t.TempDir()
	const n, shards = 400, 8
	recs := fan(t, dir, n, shards)

	var total int
	for _, rec := range recs {
		seg, err := Open[*doc.Document](filepath.Join(dir, rec.Name))
		if err != nil {
			t.Fatalf("Open %s: %v", rec.Name, err)
		}
		for d, err := range seg.All() {
			if err != nil {
				t.Fatalf("reading %s: %v", rec.Name, err)
			}
			if got := doc.Shard(d.DocID, shards); got != rec.Index {
				t.Fatalf("%s holds a document that hashes to shard %d", rec.Name, got)
			}
			total++
		}
		if err := seg.Close(); err != nil {
			t.Fatalf("Close %s: %v", rec.Name, err)
		}
	}
	if total != n {
		t.Fatalf("read back %d documents, wrote %d", total, n)
	}
}

func TestShardRecordsMatchTheFilesOnDisk(t *testing.T) {
	dir := t.TempDir()
	recs := fan(t, dir, 200, 6)

	if len(recs) == 0 {
		t.Fatal("no shards were written")
	}
	var documents int
	for i, rec := range recs {
		if i > 0 && rec.Index <= recs[i-1].Index {
			t.Fatalf("shards are not in index order: %d after %d", rec.Index, recs[i-1].Index)
		}
		hash, size, err := hashFile(filepath.Join(dir, rec.Name))
		if err != nil {
			t.Fatalf("hashing %s: %v", rec.Name, err)
		}
		if hash != rec.Hash {
			t.Errorf("%s: recorded hash %s, the file hashes to %s", rec.Name, rec.Hash, hash)
		}
		if size != rec.Bytes {
			t.Errorf("%s: recorded %d bytes, the file is %d", rec.Name, rec.Bytes, size)
		}
		documents += rec.Documents
	}
	if documents != 200 {
		t.Fatalf("the shard records add up to %d documents, want 200", documents)
	}
}

func TestPartialShardsAreNotLeftBehind(t *testing.T) {
	dir := t.TempDir()
	fan(t, dir, 50, 4)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), partExt) {
			t.Errorf("%s was left behind by a clean close", e.Name())
		}
	}
}

func TestAbandonRemovesEverythingItWrote(t *testing.T) {
	dir := t.TempDir()
	set, err := NewShardSet[*doc.Document](dir, 4, docID)
	if err != nil {
		t.Fatalf("NewShardSet: %v", err)
	}
	for i := range 50 {
		if err := set.Append(sample(i)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if set.Open() == 0 {
		t.Fatal("no shards were opened")
	}
	set.Abandon()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("Abandon left %d files behind, starting with %s", len(entries), entries[0].Name())
	}
}

// A worker that owns a range must refuse the documents it does not own, out
// loud. Dropping them silently produces a snapshot that verifies and is missing
// whatever the other workers also dropped.
func TestAShardRangeRefusesDocumentsItDoesNotOwn(t *testing.T) {
	dir := t.TempDir()
	const shards = 8
	set, err := NewShardSet[*doc.Document](dir, shards, docID, ShardRange(0, 2))
	if err != nil {
		t.Fatalf("NewShardSet: %v", err)
	}
	defer set.Abandon()

	var mine, theirs int
	for i := range 300 {
		d := sample(i)
		err := set.Append(d)
		switch {
		case err == nil:
			mine++
			if got := set.Shard(d); got >= 2 {
				t.Fatalf("accepted a document belonging to shard %d", got)
			}
		case errors.Is(err, ErrNotThisWorker):
			theirs++
		default:
			t.Fatalf("Append: %v", err)
		}
	}
	if mine == 0 || theirs == 0 {
		t.Fatalf("the range did not split the documents: %d mine, %d theirs", mine, theirs)
	}
	if set.Open() > 2 {
		t.Fatalf("a range of two shards held %d files open", set.Open())
	}
}

// Four workers with adjacent ranges build one snapshot, and between them they
// write every document exactly once.
func TestAdjacentRangesCoverTheCorpusExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	const n, shards, workers = 400, 8, 4

	var all []Shard
	for w := range workers {
		from, to := w*shards/workers, (w+1)*shards/workers
		set, err := NewShardSet[*doc.Document](dir, shards, docID, ShardRange(from, to))
		if err != nil {
			t.Fatalf("worker %d: %v", w, err)
		}
		for i := range n {
			d := sample(i)
			if !set.Owns(set.Shard(d)) {
				continue
			}
			if err := set.Append(d); err != nil {
				t.Fatalf("worker %d append: %v", w, err)
			}
		}
		recs, err := set.Close()
		if err != nil {
			t.Fatalf("worker %d close: %v", w, err)
		}
		all = append(all, recs...)
	}

	var documents int
	seen := make(map[int]bool, len(all))
	for _, rec := range all {
		if seen[rec.Index] {
			t.Fatalf("two workers wrote shard %d", rec.Index)
		}
		seen[rec.Index] = true
		documents += rec.Documents
	}
	if documents != n {
		t.Fatalf("the workers wrote %d documents between them, want %d", documents, n)
	}
}

func TestEmptyShardsAreNotWritten(t *testing.T) {
	dir := t.TempDir()
	// Ten documents cannot populate a thousand shards, and the ones nothing
	// landed in should not exist.
	recs := fan(t, dir, 10, 1000)
	if len(recs) > 10 {
		t.Fatalf("ten documents produced %d shards", len(recs))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(recs) {
		t.Fatalf("%d files on disk, %d shard records", len(entries), len(recs))
	}
}

func TestShardSetRefusesASealedSnapshot(t *testing.T) {
	dir := t.TempDir()
	m := manifest(3)
	if err := m.Seal(signingKey(t), sealedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if _, err := NewShardSet[*doc.Document](dir, 4, docID); !errors.Is(err, ErrSealed) {
		t.Fatalf("NewShardSet into a sealed snapshot = %v, want ErrSealed", err)
	}
}

func TestShardSetArgumentsAreChecked(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name  string
		build func() error
	}{
		{"no shards", func() error {
			_, err := NewShardSet[*doc.Document](dir, 0, docID)
			return err
		}},
		{"no key function", func() error {
			_, err := NewShardSet[*doc.Document](dir, 4, nil)
			return err
		}},
		{"range starts below zero", func() error {
			_, err := NewShardSet[*doc.Document](dir, 4, docID, ShardRange(-1, 2))
			return err
		}},
		{"range ends past the count", func() error {
			_, err := NewShardSet[*doc.Document](dir, 4, docID, ShardRange(0, 5))
			return err
		}},
		{"empty range", func() error {
			_, err := NewShardSet[*doc.Document](dir, 4, docID, ShardRange(2, 2))
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.build(); err == nil {
				t.Fatal("NewShardSet accepted it")
			}
		})
	}
}

func TestAppendAfterCloseIsRefused(t *testing.T) {
	dir := t.TempDir()
	set, err := NewShardSet[*doc.Document](dir, 4, docID)
	if err != nil {
		t.Fatalf("NewShardSet: %v", err)
	}
	if err := set.Append(sample(1)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := set.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := set.Append(sample(2)); err == nil {
		t.Fatal("Append to a closed shard set was accepted")
	}
	// Close is idempotent, because a deferred Close after an explicit one is
	// how this gets called in practice.
	if _, err := set.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func BenchmarkShardAppend(b *testing.B) {
	dir := b.TempDir()
	set, err := NewShardSet[*doc.Document](dir, 64, docID)
	if err != nil {
		b.Fatal(err)
	}
	defer set.Abandon()
	d := sample(1)
	b.ResetTimer()
	for b.Loop() {
		if err := set.Append(d); err != nil {
			b.Fatal(err)
		}
	}
}
