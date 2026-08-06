package kho

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// removable builds a snapshot whose manifest carries the whole accounting, not
// just the document count, because the thing a removal is most likely to get
// wrong is the arithmetic and a fixture with zeroes in it would not notice.
func removable(t *testing.T, n, shards int, opts ...ShardOption) (dir string, priv ed25519.PrivateKey) {
	t.Helper()
	dir = t.TempDir()
	recs := fan(t, dir, n, shards, opts...)

	m := &Manifest{
		Snapshot:  "2026-09",
		CreatedAt: sealedAt,
		Pipeline:  "0.1.0",
		Box:       "server1",
		Stages:    []Stage{{Name: "gat@0.1.0", ConfigHash: doc.SumString("gat config")}},
		Shards:    recs,
	}
	m.Counts.BySource = map[string]int64{}
	m.Counts.Tokenizer = "gao-64k@1"
	for i := range n {
		d := sample(i)
		m.Counts.Documents++
		m.Counts.Bytes += int64(len(d.Text))
		m.Counts.Chars += int64(d.NChars)
		m.Counts.Syllables += int64(d.NSyllables)
		m.Counts.Tokens += int64(d.NTokens)
		m.Counts.Natural++
		m.Counts.BySource[string(d.Source)]++
	}

	priv = signingKey(t)
	if err := m.Seal(priv, sealedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	return dir, priv
}

// ids reads every document identity a snapshot holds, which is how the tests
// below ask the only question that matters: is it still in there.
func ids(t *testing.T, dir string) map[doc.Hash]bool {
	t.Helper()
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	out := map[doc.Hash]bool{}
	for _, sh := range m.Shards {
		seg, err := Open[*doc.Document](filepath.Join(dir, sh.Name))
		if err != nil {
			t.Fatalf("Open %s: %v", sh.Name, err)
		}
		for d, err := range seg.All() {
			if err != nil {
				t.Fatalf("reading %s: %v", sh.Name, err)
			}
			out[d.DocID] = true
		}
		_ = seg.Close()
	}
	return out
}

// The whole thing, once, with a real document. Everything else in this file is
// one clause of this test pulled out and pushed on.
func TestARemovalTakesTheDocumentOutAndSaysSo(t *testing.T) {
	src, priv := removable(t, 300, 8)
	gone := sample(42)
	dst := filepath.Join(t.TempDir(), "2026-09-r1")

	report, err := Remove(src, dst, "2026-09-r1", priv,
		[]Removal{{DocID: gone.DocID, Reason: ReasonTakedown, Note: "request of 2026-10-02, reference 118"}},
		RemoveAt(sealedAt))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := Verify(dst, TrustKey(priv.Public().(ed25519.PublicKey))); err != nil {
		t.Fatalf("the new snapshot does not verify: %v", err)
	}
	if ids(t, dst)[gone.DocID] {
		t.Error("the document is still in the new snapshot")
	}
	if !ids(t, src)[gone.DocID] {
		t.Error("the parent was modified, and a snapshot is immutable")
	}

	m, err := ReadManifest(dst)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m.Parent != "2026-09" {
		t.Errorf("the new snapshot names %q as its parent, want 2026-09", m.Parent)
	}
	if len(m.Tombstones) != 1 {
		t.Fatalf("the new snapshot carries %d tombstones, want 1", len(m.Tombstones))
	}
	if ts := m.Tombstones[0]; ts.DocID != gone.DocID || ts.Reason != ReasonTakedown || !ts.RemovedAt.Equal(sealedAt) {
		t.Errorf("the tombstone is %+v", ts)
	}

	// One document out of 300, and every number moves by exactly what that one
	// document was worth.
	parent, err := ReadManifest(src)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if got, want := m.Counts.Documents, parent.Counts.Documents-1; got != want {
		t.Errorf("documents = %d, want %d", got, want)
	}
	if got, want := m.Counts.Bytes, parent.Counts.Bytes-int64(len(gone.Text)); got != want {
		t.Errorf("bytes = %d, want %d", got, want)
	}
	if got, want := m.Counts.Chars, parent.Counts.Chars-int64(gone.NChars); got != want {
		t.Errorf("chars = %d, want %d", got, want)
	}
	if got, want := m.Counts.Natural, parent.Counts.Natural-1; got != want {
		t.Errorf("natural = %d, want %d", got, want)
	}
	if got, want := m.Counts.BySource[string(gone.Source)], parent.Counts.BySource[string(gone.Source)]-1; got != want {
		t.Errorf("the source row reads %d, want %d", got, want)
	}

	if len(report.Removed) != 1 || report.Removed[0] != gone.DocID {
		t.Errorf("the report says it removed %v", report.Removed)
	}
	if len(report.NotFound) != 0 || len(report.Tombstoned) != 0 {
		t.Errorf("the report has stragglers: %+v", report)
	}
}

// The property the design is built around. A takedown that rewrote 750 shards
// costs a full re-upload of the corpus and a takedown that rewrote one costs one
// file, and that is the difference between answering a request this afternoon
// and answering it next week.
func TestOnlyTheShardThatHeldItIsRewritten(t *testing.T) {
	src, priv := removable(t, 300, 8)
	gone := sample(42)
	dst := filepath.Join(t.TempDir(), "r1")

	report, err := Remove(src, dst, "r1", priv,
		[]Removal{{DocID: gone.DocID, Reason: ReasonPrivacy}}, RemoveAt(sealedAt))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(report.Rewritten) != 1 {
		t.Fatalf("%d shards were rewritten, want 1: %v", len(report.Rewritten), report.Rewritten)
	}
	if len(report.Copied) != 7 {
		t.Fatalf("%d shards were copied, want 7", len(report.Copied))
	}

	// Copied means copied. The bytes are the same and so is the hash the parent
	// manifest recorded, which is what lets a publisher upload the difference.
	parent, _ := ReadManifest(src)
	child, _ := ReadManifest(dst)
	for i, sh := range parent.Shards {
		if slices.Contains(report.Rewritten, sh.Name) {
			if child.Shards[i].Hash == sh.Hash {
				t.Errorf("%s was rewritten and kept the same hash", sh.Name)
			}
			continue
		}
		if child.Shards[i].Hash != sh.Hash {
			t.Errorf("%s was copied and its hash changed", sh.Name)
		}
		a, err := os.ReadFile(filepath.Join(src, sh.Name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dst, sh.Name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s was copied and its bytes changed", sh.Name)
		}
	}
}

// A tombstone that quotes what it removed has not removed it. This walks every
// byte the new snapshot consists of, decompressed, and looks for the text and
// for the URL, because the URL is frequently the thing somebody wanted gone.
func TestNothingOfTheDocumentSurvivesInTheNewSnapshot(t *testing.T) {
	src, priv := removable(t, 120, 4)
	gone := sample(7)
	dst := filepath.Join(t.TempDir(), "r1")

	if _, err := Remove(src, dst, "r1", priv,
		[]Removal{{DocID: gone.DocID, Reason: ReasonLegal}}, RemoveAt(sealedAt)); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	manifest, err := os.ReadFile(filepath.Join(dst, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{gone.Text, gone.URL, gone.Host, gone.SourceLocator} {
		if strings.Contains(string(manifest), secret) {
			t.Errorf("the manifest still holds %q", secret)
		}
	}
	// The identity is the one thing that is meant to survive, since a later
	// crawl has to recognize the page and not fetch it again.
	if !strings.Contains(string(manifest), gone.DocID.String()) {
		t.Error("the manifest does not carry the identity of what it removed")
	}

	m, err := ReadManifest(dst)
	if err != nil {
		t.Fatal(err)
	}
	for _, sh := range m.Shards {
		seg, err := Open[*doc.Document](filepath.Join(dst, sh.Name))
		if err != nil {
			t.Fatal(err)
		}
		for d, err := range seg.All() {
			if err != nil {
				t.Fatal(err)
			}
			if d.Text == gone.Text || d.URL == gone.URL {
				t.Errorf("%s still holds the document", sh.Name)
			}
		}
		_ = seg.Close()
	}
}

// Answering a takedown request with a signed snapshot and a report saying
// nothing was removed is the worst outcome available here, because everybody
// involved goes away believing the document is gone. An identity that is not in
// the snapshot is far more likely to be the wrong identity than an empty
// request, so it is an error and nothing is written.
func TestRemovingSomethingThatIsNotThereIsAnError(t *testing.T) {
	src, priv := removable(t, 60, 4)
	dst := filepath.Join(t.TempDir(), "r1")

	report, err := Remove(src, dst, "r1", priv,
		[]Removal{{DocID: doc.SumString("a document that was never here"), Reason: ReasonTakedown}},
		RemoveAt(sealedAt))
	if !errors.Is(err, ErrNoSuchDocument) {
		t.Fatalf("Remove = %v, want ErrNoSuchDocument", err)
	}
	if len(report.NotFound) != 1 {
		t.Errorf("the report names %d missing documents, want 1", len(report.NotFound))
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("a failed removal left a directory behind, and it would read as a snapshot")
	}
}

// A removal in a script may run twice, and the second run has to be safe. The
// documents come back as already tombstoned rather than as missing, which is
// the distinction between a re-run and a mistake.
func TestRunningTheSameRemovalAgainIsNotAMistake(t *testing.T) {
	src, priv := removable(t, 60, 4)
	gone := sample(3)
	rs := []Removal{{DocID: gone.DocID, Reason: ReasonTakedown}}
	tmp := t.TempDir()

	first := filepath.Join(tmp, "r1")
	if _, err := Remove(src, first, "r1", priv, rs, RemoveAt(sealedAt)); err != nil {
		t.Fatalf("the first removal: %v", err)
	}

	second := filepath.Join(tmp, "r2")
	report, err := Remove(first, second, "r2", priv, rs, RemoveAt(sealedAt))
	if err != nil {
		t.Fatalf("the second removal: %v", err)
	}
	if len(report.Tombstoned) != 1 || report.Tombstoned[0] != gone.DocID {
		t.Errorf("the second run reports %+v", report)
	}
	if len(report.Removed) != 0 {
		t.Errorf("the second run removed %d documents, and the first one took it", len(report.Removed))
	}
	if len(report.Rewritten) != 0 {
		t.Errorf("the second run rewrote %v, and there was nothing to rewrite", report.Rewritten)
	}
	if _, err := Verify(second, TrustKey(priv.Public().(ed25519.PublicKey))); err != nil {
		t.Fatalf("the second snapshot does not verify: %v", err)
	}
}

// The point of a tombstone is that it outlives the thing it describes, so it
// has to outlive the next removal too. Without this the record of a takedown
// survives exactly one further takedown.
func TestTombstonesAccumulateDownTheChain(t *testing.T) {
	src, priv := removable(t, 90, 4)
	tmp := t.TempDir()
	first, second := sample(5), sample(60)

	a := filepath.Join(tmp, "r1")
	if _, err := Remove(src, a, "r1", priv,
		[]Removal{{DocID: first.DocID, Reason: ReasonTakedown, Note: "reference 118"}}, RemoveAt(sealedAt)); err != nil {
		t.Fatalf("the first removal: %v", err)
	}
	b := filepath.Join(tmp, "r2")
	if _, err := Remove(a, b, "r2", priv,
		[]Removal{{DocID: second.DocID, Reason: ReasonPrivacy, Note: "che missed a phone number"}}, RemoveAt(sealedAt)); err != nil {
		t.Fatalf("the second removal: %v", err)
	}

	m, err := ReadManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tombstones) != 2 {
		t.Fatalf("the second snapshot carries %d tombstones, want 2", len(m.Tombstones))
	}
	held := map[doc.Hash]string{}
	for _, ts := range m.Tombstones {
		held[ts.DocID] = ts.Reason
	}
	if held[first.DocID] != ReasonTakedown {
		t.Errorf("the first removal's tombstone reads %q", held[first.DocID])
	}
	if held[second.DocID] != ReasonPrivacy {
		t.Errorf("the second removal's tombstone reads %q", held[second.DocID])
	}
	if ids(t, b)[first.DocID] || ids(t, b)[second.DocID] {
		t.Error("a document came back down the chain")
	}
	if m.Counts.Documents != 88 {
		t.Errorf("two removals from 90 documents left %d", m.Counts.Documents)
	}
}

// A removal is the operation somebody will be performing in a hurry, so the
// request shapes that produce an unreadable record later are refused up front
// rather than accepted and regretted.
func TestARemovalHasToBeWellFormed(t *testing.T) {
	src, priv := removable(t, 30, 2)
	good := sample(1).DocID

	for _, c := range []struct {
		name string
		rs   []Removal
	}{
		{"nothing to remove", nil},
		{"no identity", []Removal{{Reason: ReasonTakedown}}},
		{"no reason", []Removal{{DocID: good}}},
		{"a reason nobody defined", []Removal{{DocID: good, Reason: "because"}}},
		{"the same document twice", []Removal{
			{DocID: good, Reason: ReasonTakedown},
			{DocID: good, Reason: ReasonLegal},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "r1")
			if _, err := Remove(src, dst, "r1", priv, c.rs, RemoveAt(sealedAt)); !errors.Is(err, ErrBadRemoval) {
				t.Fatalf("Remove = %v, want ErrBadRemoval", err)
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Error("a rejected removal wrote a directory")
			}
		})
	}

	t.Run("no name for the new snapshot", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "r1")
		_, err := Remove(src, dst, "", priv, []Removal{{DocID: good, Reason: ReasonTakedown}}, RemoveAt(sealedAt))
		if !errors.Is(err, ErrBadRemoval) {
			t.Fatalf("Remove = %v, want ErrBadRemoval", err)
		}
	})
}

// Removing from a snapshot whose bytes do not match its manifest would produce
// a fresh signature over a corpus nobody checked, and the moment to find that
// out is before the signature rather than after it.
func TestARemovalRefusesAParentThatDoesNotVerify(t *testing.T) {
	src, priv := removable(t, 60, 4)
	m, err := ReadManifest(src)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(src, m.Shards[0].Name)
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0x01
	if err := os.WriteFile(target, b, 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "r1")
	_, err = Remove(src, dst, "r1", priv,
		[]Removal{{DocID: sample(1).DocID, Reason: ReasonTakedown}}, RemoveAt(sealedAt))
	if err == nil {
		t.Fatal("Remove signed a new snapshot from a parent that does not verify")
	}
	if !strings.Contains(err.Error(), "does not verify") {
		t.Errorf("the error does not say the parent is the problem: %v", err)
	}
}

// Writing into a directory that already holds a snapshot would half overwrite
// somebody else's work, which is worse than not running.
func TestARemovalWillNotWriteOverASnapshot(t *testing.T) {
	src, priv := removable(t, 30, 2)
	other, _ := removable(t, 30, 2)

	_, err := Remove(src, other, "r1", priv,
		[]Removal{{DocID: sample(1).DocID, Reason: ReasonTakedown}}, RemoveAt(sealedAt))
	if !errors.Is(err, ErrDestExists) {
		t.Fatalf("Remove = %v, want ErrDestExists", err)
	}
}

// The counts are arrived at by subtraction, so a parent whose totals were
// already wrong would produce a child wrong by the same amount with nothing
// saying so. Both ends are checked, and this is the near end.
//
// The check is exercised directly rather than through Remove because there is
// no way to hand Remove a parent this broken: Seal refuses to sign a manifest
// whose counts disagree with its shards, so a manifest that got as far as being
// written is already past this. That is the belt, and agreesWithItsShards is
// the braces, for the day somebody hand edits a TOML file.
func TestARemovalRefusesAParentWhoseCountsAreWrong(t *testing.T) {
	src, _ := removable(t, 30, 2)
	m, err := ReadManifest(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := agreesWithItsShards(m); err != nil {
		t.Fatalf("a manifest straight off disk does not agree with its shards: %v", err)
	}

	m.Counts.Documents = 29
	err = agreesWithItsShards(m)
	if !errors.Is(err, ErrBadManifest) {
		t.Fatalf("agreesWithItsShards = %v, want ErrBadManifest", err)
	}
	if !strings.Contains(err.Error(), "29") || !strings.Contains(err.Error(), "30") {
		t.Errorf("the error does not say which two numbers disagree: %v", err)
	}
}

// A snapshot is supposed to be reproducible, and a removal that reached for the
// wall clock would produce a different manifest every time it ran. Two removals
// of the same document at the same recorded time are the same bytes.
func TestTheSameRemovalTwiceIsTheSameBytes(t *testing.T) {
	src, priv := removable(t, 90, 4)
	rs := []Removal{{DocID: sample(11).DocID, Reason: ReasonTakedown, Note: "reference 118"}}
	tmp := t.TempDir()

	a, b := filepath.Join(tmp, "a"), filepath.Join(tmp, "b")
	if _, err := Remove(src, a, "r1", priv, rs, RemoveAt(sealedAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(src, b, "r1", priv, rs, RemoveAt(sealedAt)); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		x, err := os.ReadFile(filepath.Join(a, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		y, err := os.ReadFile(filepath.Join(b, e.Name()))
		if err != nil {
			t.Fatalf("the second run did not write %s: %v", e.Name(), err)
		}
		if !bytes.Equal(x, y) {
			t.Errorf("%s differs between two runs of the same removal", e.Name())
		}
	}
}

// A request usually names more than one document and they will not be in one
// shard, which is the case where the accounting is easiest to get wrong.
func TestRemovingSeveralDocumentsAtOnce(t *testing.T) {
	src, priv := removable(t, 300, 8)
	rs := make([]Removal, 0, 5)
	want := map[doc.Hash]bool{}
	for _, i := range []int{1, 42, 99, 175, 260} {
		d := sample(i)
		rs = append(rs, Removal{DocID: d.DocID, Reason: ReasonTakedown})
		want[d.DocID] = true
	}

	dst := filepath.Join(t.TempDir(), "r1")
	report, err := Remove(src, dst, "r1", priv, rs, RemoveAt(sealedAt))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(report.Removed) != 5 {
		t.Fatalf("the report says it removed %d documents, want 5", len(report.Removed))
	}
	if _, err := Verify(dst, TrustKey(priv.Public().(ed25519.PublicKey))); err != nil {
		t.Fatalf("the new snapshot does not verify: %v", err)
	}

	held := ids(t, dst)
	for id := range want {
		if held[id] {
			t.Errorf("%s is still in the snapshot", id)
		}
	}
	m, err := ReadManifest(dst)
	if err != nil {
		t.Fatal(err)
	}
	if m.Counts.Documents != 295 {
		t.Errorf("five removals from 300 left %d documents", m.Counts.Documents)
	}
	if len(m.Tombstones) != 5 {
		t.Errorf("five removals left %d tombstones", len(m.Tombstones))
	}
	// Sorted by identity, so that two runs produce the same file and a reader
	// can find one.
	if !slices.IsSortedFunc(m.Tombstones, func(a, b Tombstone) int { return compareHash(a.DocID, b.DocID) }) {
		t.Error("the tombstones are not in a stable order")
	}
}

// The reasons are a closed set for the same reason the sources are: the table
// in a release note is generated from them and a free string turns one reason
// into three spellings of itself.
func TestTheRemovalReasons(t *testing.T) {
	if got := Reasons(); !slices.Equal(got, []string{"takedown", "legal", "privacy"}) {
		t.Errorf("Reasons() = %v", got)
	}
}
