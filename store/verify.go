package store

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Report is what a verification found. It is returned even when verification
// fails, because "which shard" is the first thing anybody asks.
type Report struct {
	Snapshot  string
	Parent    string
	Signer    string
	Shards    int
	Checked   int
	Documents int64
	Bytes     int64

	// Publishable is the part of the snapshot that may be redistributed, which
	// is a smaller number than Documents and is the one a reader downloading
	// this corpus actually gets. It is zero when the snapshot carries no
	// license breakdown.
	Publishable License

	// Failures lists the shards whose bytes did not match, by name.
	Failures []string
}

// verifyOptions is the settled configuration for [Verify].
type verifyOptions struct {
	trusted  ed25519.PublicKey
	quick    bool
	progress func(Shard, error)
}

// VerifyOption configures [Verify].
type VerifyOption func(*verifyOptions)

// TrustKey requires the manifest to be signed by this key.
//
// Without it, verification proves the snapshot is internally consistent and
// signed by somebody, which is a weaker claim than most callers think they are
// getting. A signature nobody checked the key of is a checksum with extra steps.
func TrustKey(pub ed25519.PublicKey) VerifyOption {
	return func(o *verifyOptions) { o.trusted = pub }
}

// Quick skips rehashing the shard files and checks only the manifest, the root,
// and the signature. It answers "is this manifest coherent and signed" in
// milliseconds and it does not answer "are these bytes the right bytes", so it
// is for a fast precheck and never for accepting a snapshot.
func Quick() VerifyOption {
	return func(o *verifyOptions) { o.quick = true }
}

// Progress calls f as each shard is checked, so a command line tool can show
// what it is doing during the several minutes a full verification takes.
func Progress(f func(Shard, error)) VerifyOption {
	return func(o *verifyOptions) { o.progress = f }
}

// Verify checks a snapshot directory against its manifest: the manifest is
// complete, the merkle root matches the shard hashes, the signature verifies,
// every shard file is present and hashes to what the manifest says, and there
// are no shard files present that the manifest does not list.
//
// That last check is the one people leave out. A snapshot with an extra shard in
// it verifies perfectly if you only check the shards you were told about, and
// the extra shard is exactly where somebody would put documents they wanted in
// the corpus without them appearing in the counts.
func Verify(dir string, opts ...VerifyOption) (*Report, error) {
	var cfg verifyOptions
	for _, opt := range opts {
		opt(&cfg)
	}

	m, err := ReadManifest(dir)
	if err != nil {
		return nil, err
	}
	report := &Report{
		Snapshot:  m.Snapshot,
		Parent:    m.Parent,
		Signer:    m.Signature.PublicKey,
		Shards:    len(m.Shards),
		Documents: m.Counts.Documents,

		Publishable: m.Counts.Publishable(),
	}

	if err := m.check(); err != nil {
		return report, err
	}
	if got := m.ComputeRoot(); got != m.Root {
		return report, fmt.Errorf("%w: manifest says %s, the shard hashes give %s", ErrBadRoot, m.Root, got)
	}
	if err := m.VerifySignature(); err != nil {
		return report, err
	}
	if cfg.trusted != nil {
		signer, err := m.SignerKey()
		if err != nil {
			return report, err
		}
		if !bytes.Equal(signer, cfg.trusted) {
			return report, fmt.Errorf("%w: signed by %s, which is not the key you trust", ErrBadSignature, m.Signature.PublicKey)
		}
	}

	listed := make(map[string]bool, len(m.Shards))
	for _, s := range m.Shards {
		listed[s.Name] = true
	}
	if err := checkForStrays(dir, listed); err != nil {
		return report, err
	}

	if cfg.quick {
		return report, nil
	}

	for _, s := range m.Shards {
		err := verifyShard(dir, s)
		if cfg.progress != nil {
			cfg.progress(s, err)
		}
		if err != nil {
			report.Failures = append(report.Failures, s.Name)
			continue
		}
		report.Checked++
		report.Bytes += s.Bytes
	}
	if len(report.Failures) > 0 {
		return report, fmt.Errorf("%w: %d of %d shards did not match, starting with %s",
			ErrBadShard, len(report.Failures), len(m.Shards), report.Failures[0])
	}
	return report, nil
}

func verifyShard(dir string, s Shard) error {
	path := filepath.Join(dir, s.Name)
	got, n, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrBadShard, s.Name, err)
	}
	if n != s.Bytes {
		return fmt.Errorf("%w: %s is %d bytes, the manifest says %d", ErrBadShard, s.Name, n, s.Bytes)
	}
	if got != s.Hash {
		return fmt.Errorf("%w: %s hashes to %s, the manifest says %s", ErrBadShard, s.Name, got, s.Hash)
	}
	return nil
}

// checkForStrays reports segment files in the directory that the manifest does
// not list.
func checkForStrays(dir string, listed map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("store: reading the snapshot directory: %w", err)
	}
	var strays []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, SegmentExt) || listed[name] {
			continue
		}
		strays = append(strays, name)
	}
	if len(strays) > 0 {
		return fmt.Errorf("%w: %d segment files are not in the manifest, starting with %s",
			ErrSnapshotDirty, len(strays), strays[0])
	}
	return nil
}

// Sealed reports whether the directory already holds a sealed snapshot. A sealed
// snapshot is never written to again: a correction is a new snapshot that names
// this one as its parent and carries the tombstones.
func Sealed(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ManifestName))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}
