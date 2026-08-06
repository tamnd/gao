package kho

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/tamnd/gao/doc"
	"github.com/zeebo/blake3"
)

// ManifestName is the file a snapshot's manifest lives in.
const ManifestName = "manifest.toml"

// ManifestVersion is the version of the manifest layout. It is separate from
// doc.SchemaVersion because the record schema and the snapshot bookkeeping
// change for different reasons and at different times.
const ManifestVersion uint16 = 1

// Stage records one pipeline stage that produced this snapshot, by name and by
// the hash of its configuration. Reproducing a snapshot means running these
// stages, in this order, with these configurations, and a stage recorded without
// its config hash is a stage nobody can rerun.
type Stage struct {
	// Name is name@semver. The version is not decoration: two documents cleaned
	// by different versions of the same stage are not comparable.
	Name string `toml:"name"`

	// ConfigHash is blake3 over the stage's serialized configuration.
	ConfigHash doc.Hash `toml:"config_hash"`

	// Inputs names the snapshots or the external datasets this stage consumed,
	// each already pinned by hash elsewhere in this manifest or by commit SHA in
	// the ingest manifest.
	Inputs []string `toml:"inputs,omitempty"`
}

// Counts is the snapshot's own accounting. Every number here has to agree with
// the data, and [Verify] checks the ones it can check without a tokenizer.
type Counts struct {
	Documents int64 `toml:"documents"`

	// Bytes is the UTF-8 bytes of text the snapshot holds, which is the number
	// a corpus size is quoted in. It is not the size of the files: those are
	// compressed, they are about a third of this, and the two get confused
	// because both are called bytes. [Shard.Bytes] is the other one.
	Bytes int64 `toml:"bytes"`

	Chars     int64 `toml:"chars"`
	Syllables int64 `toml:"syllables"`

	// Tokens is measured with a named tokenizer, never converted from bytes. The
	// conversions in doc/units.go are for estimates and this is not an estimate.
	Tokens    int64  `toml:"tokens"`
	Tokenizer string `toml:"tokenizer"`

	// Natural is the count that goes in a headline. Synthetic is counted and
	// reported and never added to it.
	Natural   int64 `toml:"natural"`
	Synthetic int64 `toml:"synthetic"`

	// Rejected is how many documents went to the reject store while this
	// snapshot was being built.
	Rejected int64 `toml:"rejected"`

	// BySource and ByRejectReason are the breakdowns every release note prints.
	BySource       map[string]int64 `toml:"by_source,omitempty"`
	ByRejectReason map[string]int64 `toml:"by_reject_reason,omitempty"`

	// Licenses is the snapshot broken down by what may be redistributed, one
	// row per class, in class order.
	//
	// It is here rather than derived at publication time because the total and
	// the shippable subset are two different numbers and a corpus that quotes
	// only the first has told you the size of something you cannot download.
	// The split is a property of the snapshot: it is fixed when the documents
	// are, it does not change when somebody builds a release out of them, and
	// it is covered by the signature so it cannot be restated afterwards.
	Licenses []License `toml:"license,omitempty"`
}

// License is the part of a snapshot that carries one redistribution status.
//
// Three numbers rather than one because the three are quoted in three different
// places and they do not move together. A corpus size is quoted in tokens, a
// download size is quoted in bytes, and a document count is what a card's size
// category is computed from, and a restricted slice can be a tenth of the
// documents and a third of the tokens.
type License struct {
	Class doc.LicenseClass `toml:"class"`

	Documents int64 `toml:"documents"`
	Bytes     int64 `toml:"bytes"`
	Tokens    int64 `toml:"tokens"`
}

// Add sums two rows, for the totals that span classes.
func (l License) Add(o License) License {
	l.Documents += o.Documents
	l.Bytes += o.Bytes
	l.Tokens += o.Tokens
	return l
}

// Publishable is the part of the snapshot that may appear in a published
// artifact, which is the open and permissive-attribution classes and nothing
// else. It is the number a release note has to state next to the total rather
// than instead of it.
func (c Counts) Publishable() License {
	var out License
	for _, l := range c.Licenses {
		if l.Class.Publishable() {
			out = out.Add(l)
		}
	}
	return out
}

// Withheld is the rest: processed, counted, and not passed on. A withheld
// document is still counted, because a number that quietly disappears reads as
// a number that was never there.
func (c Counts) Withheld() License {
	var out License
	for _, l := range c.Licenses {
		if !l.Class.Publishable() {
			out = out.Add(l)
		}
	}
	return out
}

// Shard is one shard file in the snapshot.
type Shard struct {
	// Name is the file name, relative to the snapshot directory.
	Name string `toml:"name"`

	// Index is the shard number, which is blake3(doc_id) mod Total for every
	// document in it.
	Index int `toml:"index"`

	Documents int `toml:"documents"`

	// Bytes is the size of the file on disk, compressed, which is what verify
	// checks it against. It is not the amount of text in the shard: that is
	// several times larger and it is counted in [Counts.Bytes].
	Bytes int64 `toml:"bytes"`

	// Hash is the segment's content hash, over the bytes of the file.
	Hash doc.Hash `toml:"hash"`
}

// Tombstone records a document removed after the snapshot it was in was sealed.
// Snapshots are immutable, so a removal is a new snapshot carrying a tombstone
// rather than an edit to an old one. The document identity is kept so that a
// later crawl recognizes the document and does not fetch it again, and the text
// is not.
type Tombstone struct {
	DocID doc.Hash `toml:"doc_id"`

	// Reason is takedown, legal, or privacy.
	Reason string `toml:"reason"`

	// RemovedAt is when the removal was applied, in UTC.
	RemovedAt time.Time `toml:"removed_at"`

	// Note is free text for the human reading the record later. It must not
	// contain anything from the document, because a tombstone that quotes the
	// text it removed has not removed it.
	Note string `toml:"note,omitempty"`
}

// Signature is an ed25519 signature over the manifest digest.
type Signature struct {
	// PublicKey is the verifying key as hex.
	PublicKey string `toml:"public_key"`

	// Value is the signature as hex.
	Value string `toml:"value"`

	// SignedAt is when the snapshot was sealed, in UTC.
	SignedAt time.Time `toml:"signed_at"`
}

// Manifest is the snapshot's record of itself: what produced it, what is in it,
// and what commits to the bytes.
type Manifest struct {
	ManifestVersion uint16 `toml:"manifest_version"`
	SchemaVersion   uint16 `toml:"schema_version"`

	// Snapshot is the identifier, conventionally a date such as 2026-09.
	Snapshot string `toml:"snapshot"`

	// Parent is the snapshot this one descends from, empty for the first. A
	// snapshot that removes documents names the snapshot it removed them from.
	Parent string `toml:"parent,omitempty"`

	CreatedAt time.Time `toml:"created_at"`

	// Pipeline is the semver of the pipeline build that produced the snapshot.
	Pipeline string `toml:"pipeline"`

	// Box is the machine that produced the snapshot, which is provenance in the
	// same way the stage versions are.
	Box string `toml:"box,omitempty"`

	Stages     []Stage     `toml:"stage"`
	Counts     Counts      `toml:"counts"`
	Shards     []Shard     `toml:"shard"`
	Tombstones []Tombstone `toml:"tombstone,omitempty"`

	// Root is the merkle root over the shard hashes, in shard order.
	Root doc.Hash `toml:"root"`

	Signature Signature `toml:"signature"`
}

// Errors a manifest can fail with. They are distinguished because the responses
// are different: a bad signature means somebody handed you a different corpus,
// a bad shard hash means a byte flipped in storage, and an incomplete manifest
// means it was written by a broken pipeline.
var (
	ErrUnsigned      = errors.New("kho: manifest is not signed")
	ErrBadSignature  = errors.New("kho: manifest signature does not verify")
	ErrBadRoot       = errors.New("kho: merkle root does not match the shard hashes")
	ErrBadShard      = errors.New("kho: shard hash does not match its bytes")
	ErrBadManifest   = errors.New("kho: manifest is incomplete")
	ErrSnapshotDirty = errors.New("kho: snapshot directory does not match its manifest")
)

// ComputeRoot returns the merkle root over the shard hashes as listed.
func (m *Manifest) ComputeRoot() doc.Hash {
	leaves := make([]doc.Hash, len(m.Shards))
	for i, s := range m.Shards {
		leaves[i] = s.Hash
	}
	return MerkleRoot(leaves)
}

// Digest is what the signature covers. It is built from the manifest's values
// field by field rather than from its TOML encoding, because a signature over a
// serialization is a signature over formatting: a re-encode with a different key
// order, a different quoting style, or a different library version would break a
// signature that nothing had actually invalidated.
//
// The signature block itself is not covered, for the obvious reason.
func (m *Manifest) Digest() doc.Hash {
	d := blake3.New()
	str := func(s string) {
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(len(s)))
		_, _ = d.Write(n[:])
		_, _ = d.Write([]byte(s))
	}
	num := func(v int64) {
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(v))
		_, _ = d.Write(n[:])
	}
	hash := func(h doc.Hash) { _, _ = d.Write(h[:]) }
	table := func(m map[string]int64) {
		num(int64(len(m)))
		for _, k := range slices.Sorted(maps.Keys(m)) {
			str(k)
			num(m[k])
		}
	}

	str("gao manifest v1")
	num(int64(m.ManifestVersion))
	num(int64(m.SchemaVersion))
	str(m.Snapshot)
	str(m.Parent)
	num(m.CreatedAt.UTC().Unix())
	str(m.Pipeline)
	str(m.Box)

	num(int64(len(m.Stages)))
	for _, s := range m.Stages {
		str(s.Name)
		hash(s.ConfigHash)
		num(int64(len(s.Inputs)))
		for _, in := range s.Inputs {
			str(in)
		}
	}

	c := m.Counts
	num(c.Documents)
	num(c.Bytes)
	num(c.Chars)
	num(c.Syllables)
	num(c.Tokens)
	str(c.Tokenizer)
	num(c.Natural)
	num(c.Synthetic)
	num(c.Rejected)
	table(c.BySource)
	table(c.ByRejectReason)
	num(int64(len(c.Licenses)))
	for _, l := range c.Licenses {
		num(int64(l.Class))
		num(l.Documents)
		num(l.Bytes)
		num(l.Tokens)
	}

	num(int64(len(m.Shards)))
	for _, s := range m.Shards {
		str(s.Name)
		num(int64(s.Index))
		num(int64(s.Documents))
		num(s.Bytes)
		hash(s.Hash)
	}

	num(int64(len(m.Tombstones)))
	for _, t := range m.Tombstones {
		hash(t.DocID)
		str(t.Reason)
		num(t.RemovedAt.UTC().Unix())
		str(t.Note)
	}

	hash(m.Root)
	return doc.Hash(d.Sum(nil)[:doc.HashSize])
}

// Seal computes the merkle root, checks the manifest is complete, and signs it.
// After Seal the snapshot is closed: anything else that needs to change makes a
// new snapshot with this one as its parent.
func (m *Manifest) Seal(key ed25519.PrivateKey, at time.Time) error {
	m.ManifestVersion = ManifestVersion
	m.SchemaVersion = doc.SchemaVersion
	m.Root = m.ComputeRoot()
	if err := m.check(); err != nil {
		return err
	}
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return errors.New("kho: signing key has no ed25519 public key")
	}
	digest := m.Digest()
	m.Signature = Signature{
		PublicKey: hex.EncodeToString(pub),
		Value:     hex.EncodeToString(ed25519.Sign(key, digest[:])),
		SignedAt:  at.UTC(),
	}
	return nil
}

// VerifySignature checks the signature against the manifest's own values. It
// says nothing about whose key signed it: a caller that cares which key, and
// every caller should, compares [Manifest.SignerKey] against a key it already
// trusts.
func (m *Manifest) VerifySignature() error {
	if m.Signature.Value == "" || m.Signature.PublicKey == "" {
		return ErrUnsigned
	}
	pub, err := hex.DecodeString(m.Signature.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key is not %d hex-encoded bytes", ErrBadSignature, ed25519.PublicKeySize)
	}
	sig, err := hex.DecodeString(m.Signature.Value)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature is not %d hex-encoded bytes", ErrBadSignature, ed25519.SignatureSize)
	}
	digest := m.Digest()
	if !ed25519.Verify(pub, digest[:], sig) {
		return ErrBadSignature
	}
	return nil
}

// SignerKey returns the public key that signed the manifest.
func (m *Manifest) SignerKey() (ed25519.PublicKey, error) {
	pub, err := hex.DecodeString(m.Signature.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: public key is not %d hex-encoded bytes", ErrBadSignature, ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(pub), nil
}

// check reports everything structurally wrong with the manifest at once, for the
// same reason the ingest contract does: fixing one problem at a time across a
// pipeline run is not a plan.
func (m *Manifest) check() error {
	var problems []error
	if m.Snapshot == "" {
		problems = append(problems, errors.New("snapshot has no identifier"))
	}
	if m.Pipeline == "" {
		problems = append(problems, errors.New("pipeline version is empty"))
	}
	if m.CreatedAt.IsZero() {
		problems = append(problems, errors.New("created_at is unset"))
	} else if m.CreatedAt.Location() != time.UTC {
		problems = append(problems, errors.New("created_at is not UTC"))
	}
	if len(m.Stages) == 0 {
		problems = append(problems, errors.New("no stages recorded, so the snapshot cannot be reproduced"))
	}
	for i, s := range m.Stages {
		if s.Name == "" {
			problems = append(problems, fmt.Errorf("stage %d has no name", i))
		}
		if s.ConfigHash.IsZero() {
			problems = append(problems, fmt.Errorf("stage %q has no config hash, so nobody can rerun it", s.Name))
		}
	}
	if len(m.Shards) == 0 {
		problems = append(problems, errors.New("no shards recorded"))
	}
	seen := make(map[int]bool, len(m.Shards))
	var documents int64
	for _, s := range m.Shards {
		if s.Name == "" {
			problems = append(problems, fmt.Errorf("shard %d has no file name", s.Index))
		}
		if s.Hash.IsZero() {
			problems = append(problems, fmt.Errorf("shard %q has no content hash", s.Name))
		}
		if seen[s.Index] {
			problems = append(problems, fmt.Errorf("shard index %d appears twice", s.Index))
		}
		seen[s.Index] = true
		documents += int64(s.Documents)
	}
	if m.Counts.Documents != documents {
		problems = append(problems, fmt.Errorf("the counts block says %d documents and the shards add up to %d",
			m.Counts.Documents, documents))
	}
	if m.Counts.Natural+m.Counts.Synthetic != m.Counts.Documents {
		problems = append(problems, fmt.Errorf("natural %d plus synthetic %d is not the document count %d",
			m.Counts.Natural, m.Counts.Synthetic, m.Counts.Documents))
	}
	problems = append(problems, m.Counts.checkLicenses()...)
	if m.Counts.Tokens > 0 && m.Counts.Tokenizer == "" {
		problems = append(problems, errors.New("a token count without a tokenizer is not a token count"))
	}
	for i, t := range m.Tombstones {
		if t.DocID.IsZero() {
			problems = append(problems, fmt.Errorf("tombstone %d has no document identity", i))
		}
		if t.Reason == "" {
			problems = append(problems, fmt.Errorf("tombstone %d has no reason", i))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrBadManifest, errors.Join(problems...))
	}
	return nil
}

// WriteManifest writes the manifest into the snapshot directory. It refuses to
// overwrite an existing one, because snapshots are immutable and the way that
// property gets lost is one convenient overwrite at a time.
func WriteManifest(dir string, m *Manifest) error {
	path := filepath.Join(dir, ManifestName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("kho: writing the manifest: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := toml.NewEncoder(f).Encode(m); err != nil {
		return fmt.Errorf("kho: encoding the manifest: %w", err)
	}
	return f.Close()
}

// ReadManifest reads a snapshot manifest.
func ReadManifest(dir string) (*Manifest, error) {
	var m Manifest
	path := filepath.Join(dir, ManifestName)
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return nil, fmt.Errorf("kho: reading %s: %w", path, err)
	}
	if m.ManifestVersion > ManifestVersion {
		return nil, fmt.Errorf("kho: %s is manifest version %d, this build understands %d",
			path, m.ManifestVersion, ManifestVersion)
	}
	return &m, nil
}

// hashFile returns the content hash of a file, streaming it, because a shard is
// half a gigabyte and there are hundreds of them.
func hashFile(path string) (doc.Hash, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return doc.Hash{}, 0, err
	}
	defer func() { _ = f.Close() }()
	d := blake3.New()
	n, err := io.Copy(d, f)
	if err != nil {
		return doc.Hash{}, 0, err
	}
	return doc.Hash(d.Sum(nil)[:doc.HashSize]), n, nil
}

// checkLicenses is the rule that keeps the publishable number honest.
//
// The breakdown is optional, because a snapshot from a stage that has not made
// the determination yet has nothing to break down and an empty list says so. If
// it is there it has to be complete, because a partial breakdown produces a
// publishable count that is smaller than the truth for no stated reason, and
// nobody reading a release note can tell which of those two it is looking at.
func (c Counts) checkLicenses() []error {
	if len(c.Licenses) == 0 {
		return nil
	}

	var problems []error
	seen := make(map[doc.LicenseClass]bool, len(c.Licenses))
	var total License
	for _, l := range c.Licenses {
		switch {
		case !l.Class.Valid():
			problems = append(problems, fmt.Errorf("the license breakdown has a row for %s, which is not a class", l.Class))
		case seen[l.Class]:
			problems = append(problems, fmt.Errorf("the license breakdown has two rows for %s", l.Class))
		case l.Class == doc.LicenseUnknown && l.Documents > 0:
			problems = append(problems, fmt.Errorf("%d documents have no license determination, and the ingest contract does not accept one", l.Documents))
		}
		seen[l.Class] = true
		total = total.Add(l)
	}

	if total.Documents != c.Documents {
		problems = append(problems, fmt.Errorf("the license breakdown adds up to %d documents and the snapshot holds %d",
			total.Documents, c.Documents))
	}
	if total.Bytes != c.Bytes {
		problems = append(problems, fmt.Errorf("the license breakdown adds up to %d bytes of text and the counts block says %d",
			total.Bytes, c.Bytes))
	}
	if c.Tokens > 0 && total.Tokens != c.Tokens {
		problems = append(problems, fmt.Errorf("the license breakdown adds up to %d tokens and the counts block says %d",
			total.Tokens, c.Tokens))
	}
	return problems
}
