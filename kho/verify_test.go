package kho

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// snapshot builds a real signed snapshot on disk: documents fanned into shards,
// a manifest over the shards that were actually written, sealed with a fresh key.
func snapshot(t *testing.T, n, shards int) (dir string, pub ed25519.PublicKey) {
	t.Helper()
	dir = t.TempDir()
	recs := fan(t, dir, n, shards)

	m := &Manifest{
		Snapshot:  "2026-09",
		CreatedAt: sealedAt,
		Pipeline:  "0.1.0",
		Box:       "server1",
		Stages:    []Stage{{Name: "gat@0.1.0", ConfigHash: doc.SumString("gat config")}},
		Shards:    recs,
	}
	for _, rec := range recs {
		m.Counts.Documents += int64(rec.Documents)
		m.Counts.Bytes += rec.Bytes
	}
	m.Counts.Natural = m.Counts.Documents

	priv := signingKey(t)
	if err := m.Seal(priv, sealedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	return dir, priv.Public().(ed25519.PublicKey)
}

func TestVerifyAcceptsASnapshotItJustBuilt(t *testing.T) {
	dir, pub := snapshot(t, 300, 8)

	report, err := Verify(dir, TrustKey(pub))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Snapshot != "2026-09" {
		t.Errorf("snapshot = %q, want 2026-09", report.Snapshot)
	}
	if report.Checked != report.Shards {
		t.Errorf("checked %d of %d shards", report.Checked, report.Shards)
	}
	if report.Documents != 300 {
		t.Errorf("documents = %d, want 300", report.Documents)
	}
	if len(report.Failures) != 0 {
		t.Errorf("failures = %v, want none", report.Failures)
	}
	if report.Bytes == 0 {
		t.Error("the report has no byte count")
	}
}

func TestVerifyCatchesAFlippedByte(t *testing.T) {
	dir, pub := snapshot(t, 200, 4)
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, m.Shards[0].Name)

	// A single bit inside a shard, which is what storage corruption looks like:
	// the file is the right length, the manifest is untouched, and the signature
	// still verifies, because the signature covers the manifest and only the
	// shard hash covers the bytes.
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0x01
	if err := os.WriteFile(target, b, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Verify(dir, TrustKey(pub))
	if !errors.Is(err, ErrBadShard) {
		t.Fatalf("Verify = %v, want ErrBadShard", err)
	}
	if len(report.Failures) != 1 || report.Failures[0] != m.Shards[0].Name {
		t.Errorf("failures = %v, want just %s", report.Failures, m.Shards[0].Name)
	}
	if report.Checked != report.Shards-1 {
		t.Errorf("checked %d shards, want %d", report.Checked, report.Shards-1)
	}
}

func TestVerifyCatchesATruncatedShard(t *testing.T) {
	dir, _ := snapshot(t, 100, 4)
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, m.Shards[0].Name)
	if err := os.Truncate(target, m.Shards[0].Bytes-16); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); !errors.Is(err, ErrBadShard) {
		t.Fatalf("Verify = %v, want ErrBadShard", err)
	}
}

func TestVerifyCatchesAMissingShard(t *testing.T) {
	dir, _ := snapshot(t, 100, 4)
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, m.Shards[0].Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); !errors.Is(err, ErrBadShard) {
		t.Fatalf("Verify = %v, want ErrBadShard", err)
	}
}

// A shard nobody listed is the interesting case. Checking only the shards the
// manifest names would pass, and the extra file is exactly where documents that
// should not be in the corpus would go.
func TestVerifyCatchesAnUnlistedShard(t *testing.T) {
	dir, _ := snapshot(t, 100, 4)
	stray := filepath.Join(dir, ShardName(999, 1000))
	if err := os.WriteFile(stray, []byte("smuggled"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Verify(dir)
	if !errors.Is(err, ErrSnapshotDirty) {
		t.Fatalf("Verify = %v, want ErrSnapshotDirty", err)
	}
	if !strings.Contains(err.Error(), filepath.Base(stray)) {
		t.Errorf("error does not name the stray file: %v", err)
	}
}

func TestVerifyCatchesARewrittenManifest(t *testing.T) {
	dir, _ := snapshot(t, 100, 4)
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Somebody edits the headline number and leaves everything else alone.
	m.Counts.Documents *= 10
	m.Counts.Natural = m.Counts.Documents
	if err := os.Remove(filepath.Join(dir, ManifestName)); err != nil {
		t.Fatal(err)
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	// The count no longer agrees with the shards, which is caught before the
	// signature is even consulted.
	if _, err := Verify(dir); !errors.Is(err, ErrBadManifest) {
		t.Fatalf("Verify = %v, want ErrBadManifest", err)
	}
}

func TestVerifyCatchesASwappedRoot(t *testing.T) {
	dir, _ := snapshot(t, 100, 4)
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Root = doc.SumString("a root from somewhere else")
	if err := os.Remove(filepath.Join(dir, ManifestName)); err != nil {
		t.Fatal(err)
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); !errors.Is(err, ErrBadRoot) {
		t.Fatalf("Verify = %v, want ErrBadRoot", err)
	}
}

func TestVerifyCatchesTheWrongSigner(t *testing.T) {
	dir, pub := snapshot(t, 100, 4)

	other, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir, TrustKey(other)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Verify with the wrong key = %v, want ErrBadSignature", err)
	}
	// The same snapshot verifies against the key that actually signed it, so the
	// failure above is about the key and not about the snapshot.
	if _, err := Verify(dir, TrustKey(pub)); err != nil {
		t.Fatalf("Verify with the right key: %v", err)
	}
}

func TestQuickSkipsTheShardsAndSaysSo(t *testing.T) {
	dir, pub := snapshot(t, 100, 4)
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt a shard. A quick check passes, which is the documented and
	// dangerous part, and a full check does not.
	if err := os.WriteFile(filepath.Join(dir, m.Shards[0].Name), []byte("gone"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Verify(dir, TrustKey(pub), Quick())
	if err != nil {
		t.Fatalf("quick Verify: %v", err)
	}
	if report.Checked != 0 {
		t.Errorf("a quick check hashed %d shards", report.Checked)
	}
	if _, err := Verify(dir, TrustKey(pub)); err == nil {
		t.Fatal("a full check passed on a corrupted shard")
	}
}

func TestProgressReportsEveryShardOnce(t *testing.T) {
	dir, _ := snapshot(t, 200, 6)
	seen := make(map[string]int)
	report, err := Verify(dir, Progress(func(s Shard, err error) {
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
		}
		seen[s.Name]++
	}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(seen) != report.Shards {
		t.Fatalf("progress reported %d shards, the manifest lists %d", len(seen), report.Shards)
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("%s was reported %d times", name, n)
		}
	}
}

func TestVerifyReportsAMissingManifest(t *testing.T) {
	if _, err := Verify(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Verify on an empty directory = %v, want os.ErrNotExist", err)
	}
}

func TestSealedTellsASnapshotFromAWorkingDirectory(t *testing.T) {
	empty := t.TempDir()
	if Sealed(empty) {
		t.Error("an empty directory reads as sealed")
	}
	dir, _ := snapshot(t, 20, 2)
	if !Sealed(dir) {
		t.Error("a written snapshot does not read as sealed")
	}
}
