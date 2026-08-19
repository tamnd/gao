package store

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
)

var sealedAt = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// manifest builds a complete, internally consistent manifest for n shards.
func manifest(n int) *Manifest {
	m := &Manifest{
		Snapshot:  "2026-09",
		CreatedAt: sealedAt,
		Pipeline:  "0.1.0",
		Box:       "server3",
		Stages: []Stage{
			{Name: "harvest@0.1.0", ConfigHash: doc.SumString("harvest config")},
			{Name: "sift@0.1.0", ConfigHash: doc.SumString("sift config"), Inputs: []string{"2026-08"}},
		},
		Counts: Counts{
			Bytes:          4_000,
			Chars:          3_030,
			Syllables:      669,
			Tokens:         1_010,
			Tokenizer:      "gao-bpe-64k@0.1.0",
			Rejected:       17,
			BySource:       map[string]int64{"crawl": 8, "hf": 2},
			ByRejectReason: map[string]int64{"quality": 17},
		},
	}
	for i := range n {
		m.Shards = append(m.Shards, Shard{
			Name:      ShardName(i, n),
			Index:     i,
			Documents: 10,
			Bytes:     1_000,
			Hash:      doc.SumString(ShardName(i, n)),
		})
		m.Counts.Documents += 10
	}
	m.Counts.Natural = m.Counts.Documents - 2
	m.Counts.Synthetic = 2
	return m
}

func signingKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return priv
}

func TestSealComputesTheRootAndSigns(t *testing.T) {
	m := manifest(5)
	priv := signingKey(t)
	if err := m.Seal(priv, sealedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if m.ManifestVersion != ManifestVersion {
		t.Errorf("manifest version = %d, want %d", m.ManifestVersion, ManifestVersion)
	}
	if m.SchemaVersion != doc.SchemaVersion {
		t.Errorf("schema version = %d, want %d", m.SchemaVersion, doc.SchemaVersion)
	}
	if m.Root != m.ComputeRoot() {
		t.Error("Seal did not set the merkle root")
	}
	if err := m.VerifySignature(); err != nil {
		t.Fatalf("VerifySignature after Seal: %v", err)
	}
	if !m.Signature.SignedAt.Equal(sealedAt) {
		t.Errorf("signed_at = %s, want %s", m.Signature.SignedAt, sealedAt)
	}

	signer, err := m.SignerKey()
	if err != nil {
		t.Fatalf("SignerKey: %v", err)
	}
	if !signer.Equal(priv.Public()) {
		t.Error("SignerKey is not the key that signed")
	}
}

func TestSealRefusesAnIncompleteManifest(t *testing.T) {
	cases := []struct {
		name   string
		damage func(*Manifest)
		want   string
	}{
		{"no identifier", func(m *Manifest) { m.Snapshot = "" }, "no identifier"},
		{"no pipeline version", func(m *Manifest) { m.Pipeline = "" }, "pipeline version is empty"},
		{"no created_at", func(m *Manifest) { m.CreatedAt = time.Time{} }, "created_at is unset"},
		{"created_at is local", func(m *Manifest) {
			m.CreatedAt = m.CreatedAt.In(time.FixedZone("ICT", 7*3600))
		}, "not UTC"},
		{"no stages", func(m *Manifest) { m.Stages = nil }, "cannot be reproduced"},
		{"stage without a name", func(m *Manifest) { m.Stages[0].Name = "" }, "has no name"},
		{"stage without a config hash", func(m *Manifest) { m.Stages[0].ConfigHash = doc.Hash{} }, "rerun"},
		{"no shards", func(m *Manifest) { m.Shards = nil }, "no shards recorded"},
		{"shard without a name", func(m *Manifest) { m.Shards[1].Name = "" }, "no file name"},
		{"shard without a hash", func(m *Manifest) { m.Shards[1].Hash = doc.Hash{} }, "no content hash"},
		{"duplicate shard index", func(m *Manifest) { m.Shards[1].Index = m.Shards[0].Index }, "appears twice"},
		{"counts disagree with the shards", func(m *Manifest) { m.Counts.Documents += 3 }, "add up to"},
		{"natural and synthetic do not add up", func(m *Manifest) { m.Counts.Synthetic = 99 }, "is not the document count"},
		{"tokens without a tokenizer", func(m *Manifest) { m.Counts.Tokenizer = "" }, "not a token count"},
		{"tombstone without an identity", func(m *Manifest) {
			m.Tombstones = []Tombstone{{Reason: "takedown", RemovedAt: sealedAt}}
		}, "no document identity"},
		{"tombstone without a reason", func(m *Manifest) {
			m.Tombstones = []Tombstone{{DocID: doc.SumString("x"), RemovedAt: sealedAt}}
		}, "no reason"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := manifest(4)
			tc.damage(m)
			err := m.Seal(signingKey(t), sealedAt)
			if err == nil {
				t.Fatal("Seal accepted an incomplete manifest")
			}
			if !errors.Is(err, ErrBadManifest) {
				t.Errorf("error is not ErrBadManifest: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
		})
	}
}

func TestCheckReportsEveryProblemAtOnce(t *testing.T) {
	m := manifest(3)
	m.Snapshot = ""
	m.Pipeline = ""
	m.Stages = nil

	err := m.Seal(signingKey(t), sealedAt)
	if err == nil {
		t.Fatal("Seal accepted a manifest with three problems")
	}
	for _, want := range []string{"no identifier", "pipeline version is empty", "cannot be reproduced"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// The signature covers the values, so anything a reader would act on breaks it.
func TestEveryFieldTheSignatureCoversBreaksIt(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(*Manifest)
	}{
		{"snapshot", func(m *Manifest) { m.Snapshot = "2026-10" }},
		{"parent", func(m *Manifest) { m.Parent = "2026-08" }},
		{"created_at", func(m *Manifest) { m.CreatedAt = m.CreatedAt.Add(time.Second) }},
		{"pipeline", func(m *Manifest) { m.Pipeline = "0.2.0" }},
		{"box", func(m *Manifest) { m.Box = "gamingpc" }},
		{"stage name", func(m *Manifest) { m.Stages[0].Name = "harvest@0.2.0" }},
		{"stage config", func(m *Manifest) { m.Stages[0].ConfigHash = doc.SumString("other") }},
		{"stage inputs", func(m *Manifest) { m.Stages[1].Inputs = []string{"2026-07"} }},
		{"documents", func(m *Manifest) { m.Counts.Documents++ }},
		{"bytes", func(m *Manifest) { m.Counts.Bytes++ }},
		{"chars", func(m *Manifest) { m.Counts.Chars++ }},
		{"syllables", func(m *Manifest) { m.Counts.Syllables++ }},
		{"tokens", func(m *Manifest) { m.Counts.Tokens++ }},
		{"tokenizer", func(m *Manifest) { m.Counts.Tokenizer = "tiktoken" }},
		{"natural", func(m *Manifest) { m.Counts.Natural++ }},
		{"synthetic", func(m *Manifest) { m.Counts.Synthetic++ }},
		{"rejected", func(m *Manifest) { m.Counts.Rejected++ }},
		{"by source", func(m *Manifest) { m.Counts.BySource["crawl"] = 9 }},
		{"a new source", func(m *Manifest) { m.Counts.BySource["books"] = 1 }},
		{"by reject reason", func(m *Manifest) { m.Counts.ByRejectReason["privacy"] = 1 }},
		{"shard name", func(m *Manifest) { m.Shards[0].Name = "elsewhere.jsonl.zst" }},
		{"shard index", func(m *Manifest) { m.Shards[0].Index = 99 }},
		{"shard documents", func(m *Manifest) { m.Shards[0].Documents++ }},
		{"shard bytes", func(m *Manifest) { m.Shards[0].Bytes++ }},
		{"shard hash", func(m *Manifest) { m.Shards[0].Hash = doc.SumString("swapped") }},
		{"a new shard", func(m *Manifest) { m.Shards = append(m.Shards, m.Shards[0]) }},
		{"a tombstone", func(m *Manifest) {
			m.Tombstones = []Tombstone{{DocID: doc.SumString("gone"), Reason: "takedown", RemovedAt: sealedAt}}
		}},
		{"the root", func(m *Manifest) { m.Root = doc.SumString("not the root") }},
		{"a license class", func(m *Manifest) { m.Counts.Licenses[1].Class = doc.LicenseOpen }},
		{"a license document count", func(m *Manifest) { m.Counts.Licenses[0].Documents++ }},
		{"a license byte count", func(m *Manifest) { m.Counts.Licenses[0].Bytes++ }},
		{"a license token count", func(m *Manifest) { m.Counts.Licenses[0].Tokens++ }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := licensed(manifest(4))
			if err := m.Seal(signingKey(t), sealedAt); err != nil {
				t.Fatalf("Seal: %v", err)
			}
			tc.tamper(m)
			if err := m.VerifySignature(); !errors.Is(err, ErrBadSignature) {
				t.Fatalf("tampering with the %s left the signature valid: %v", tc.name, err)
			}
		})
	}
}

// The digest is over values and not over the encoding, so a manifest that went
// through TOML and came back verifies. This is the property that lets the format
// change without invalidating a signature that was correct when it was made.
func TestSignatureSurvivesARoundTripThroughTOML(t *testing.T) {
	dir := t.TempDir()
	m := manifest(6)
	if err := m.Seal(signingKey(t), sealedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	back, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if err := back.VerifySignature(); err != nil {
		t.Fatalf("VerifySignature after a round trip: %v", err)
	}
	if back.Digest() != m.Digest() {
		t.Error("the digest changed across a round trip through TOML")
	}
	if back.Root != m.Root {
		t.Errorf("root = %s, want %s", back.Root, m.Root)
	}
	if len(back.Shards) != len(m.Shards) {
		t.Fatalf("read back %d shards, wrote %d", len(back.Shards), len(m.Shards))
	}
	if back.Counts.BySource["crawl"] != m.Counts.BySource["crawl"] {
		t.Error("the source breakdown did not survive the round trip")
	}
	if !back.CreatedAt.Equal(m.CreatedAt) {
		t.Errorf("created_at = %s, want %s", back.CreatedAt, m.CreatedAt)
	}
}

func TestAnUnsignedManifestIsNotVerified(t *testing.T) {
	m := manifest(2)
	if err := m.VerifySignature(); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("VerifySignature on an unsigned manifest = %v, want ErrUnsigned", err)
	}
}

func TestAMalformedSignatureIsNotAVerificationFailure(t *testing.T) {
	// Garbage in the signature block should read as a bad signature and never as
	// a panic or a pass.
	cases := map[string]Signature{
		"key is not hex":  {PublicKey: "zzzz", Value: strings.Repeat("00", ed25519.SignatureSize)},
		"key is short":    {PublicKey: "00ff", Value: strings.Repeat("00", ed25519.SignatureSize)},
		"value not hex":   {PublicKey: strings.Repeat("00", ed25519.PublicKeySize), Value: "nothex"},
		"value is short":  {PublicKey: strings.Repeat("00", ed25519.PublicKeySize), Value: "00ff"},
		"value is zeroes": {PublicKey: strings.Repeat("00", ed25519.PublicKeySize), Value: strings.Repeat("00", ed25519.SignatureSize)},
	}
	for name, sig := range cases {
		t.Run(name, func(t *testing.T) {
			m := manifest(2)
			m.Root = m.ComputeRoot()
			m.Signature = sig
			if err := m.VerifySignature(); !errors.Is(err, ErrBadSignature) {
				t.Fatalf("VerifySignature = %v, want ErrBadSignature", err)
			}
		})
	}
}

func TestWriteManifestRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	m := manifest(3)
	if err := m.Seal(signingKey(t), sealedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if err := WriteManifest(dir, m); err == nil {
		t.Fatal("WriteManifest overwrote a sealed snapshot")
	} else if !errors.Is(err, os.ErrExist) {
		t.Errorf("error is not os.ErrExist: %v", err)
	}
}

func TestReadManifestRefusesAFutureVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestName)
	if err := os.WriteFile(path, []byte("manifest_version = 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadManifest(dir)
	if err == nil {
		t.Fatal("ReadManifest accepted a manifest from the future")
	}
	if !strings.Contains(err.Error(), "this build understands") {
		t.Errorf("error does not explain the version gap: %v", err)
	}
}

func TestReadManifestReportsAMissingFile(t *testing.T) {
	if _, err := ReadManifest(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadManifest on an empty directory = %v, want os.ErrNotExist", err)
	}
}

// licensed fills in a breakdown that adds up, splitting a manifest's documents
// across the four classes a determination can produce.
func licensed(m *Manifest) *Manifest {
	c := &m.Counts
	rest := License{Documents: c.Documents, Bytes: c.Bytes, Tokens: c.Tokens}
	take := func(class doc.LicenseClass, docs, bytes, tokens int64) {
		c.Licenses = append(c.Licenses, License{Class: class, Documents: docs, Bytes: bytes, Tokens: tokens})
		rest = rest.Add(License{Documents: -docs, Bytes: -bytes, Tokens: -tokens})
	}
	take(doc.LicenseOpen, c.Documents/2, c.Bytes/2, c.Tokens/2)
	take(doc.LicenseRestricted, c.Documents/4, c.Bytes/4, c.Tokens/4)
	take(doc.LicenseUnredistributable, c.Documents/8, c.Bytes/8, c.Tokens/8)
	take(doc.LicensePermissiveAttribution, rest.Documents, rest.Bytes, rest.Tokens)
	return m
}

// The number a corpus quotes and the number somebody can download are two
// numbers, and a release that states only the first has described the size of
// something nobody can have.
func TestThePublishableSubsetIsStatedApartFromTheTotal(t *testing.T) {
	m := licensed(manifest(4))
	if err := m.Seal(signingKey(t), sealedAt); err != nil {
		t.Fatal(err)
	}

	pub, held := m.Counts.Publishable(), m.Counts.Withheld()
	if pub.Documents+held.Documents != m.Counts.Documents {
		t.Fatalf("published %d plus withheld %d is not %d", pub.Documents, held.Documents, m.Counts.Documents)
	}
	if pub.Documents >= m.Counts.Documents {
		t.Error("everything in the snapshot came back publishable, so the split is not being read")
	}
	if held.Documents == 0 {
		t.Error("nothing came back withheld, and the restricted rows are what the number exists for")
	}
	for _, l := range m.Counts.Licenses {
		if l.Class == doc.LicenseRestricted && l.Documents == 0 {
			t.Error("the restricted row is empty, so the fixture is not testing anything")
		}
	}
}

// A partial breakdown produces a publishable count smaller than the truth for
// no stated reason, and nobody reading it can tell that from a corpus that is
// genuinely mostly withheld.
func TestALicenseBreakdownThatDoesNotAddUpIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name  string
		spoil func(*Manifest)
		want  string
	}{
		{"a class left out", func(m *Manifest) { m.Counts.Licenses = m.Counts.Licenses[:2] }, "adds up to"},
		{"the same class twice", func(m *Manifest) {
			m.Counts.Licenses = append(m.Counts.Licenses, License{Class: doc.LicenseOpen})
		}, "two rows for open"},
		{"bytes that do not agree", func(m *Manifest) { m.Counts.Licenses[0].Bytes++ }, "bytes of text"},
		{"tokens that do not agree", func(m *Manifest) { m.Counts.Licenses[0].Tokens++ }, "tokens"},
		{"a document with no determination", func(m *Manifest) {
			m.Counts.Licenses = append(m.Counts.Licenses, License{Class: doc.LicenseUnknown, Documents: 1})
			m.Counts.Documents++
			m.Shards[0].Documents++
		}, "no license determination"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := licensed(manifest(4))
			tt.spoil(m)
			err := m.Seal(signingKey(t), sealedAt)
			if err == nil {
				t.Fatal("sealed anyway")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("the error does not say what is wrong: %v", err)
			}
		})
	}
}

// A snapshot from a stage that has not made the determination yet has nothing
// to break down, and an empty list says so rather than claiming everything is
// publishable or nothing is.
func TestASnapshotWithNoDeterminationYetSealsWithoutABreakdown(t *testing.T) {
	m := manifest(4)
	if err := m.Seal(signingKey(t), sealedAt); err != nil {
		t.Fatal(err)
	}
	if got := m.Counts.Publishable(); got.Documents != 0 {
		t.Errorf("a snapshot with no breakdown reported %d publishable documents", got.Documents)
	}
}
