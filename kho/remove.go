package kho

// Taking a document back out of a corpus that has already been published.
//
// This is the one operation in the store that destroys data on purpose, and it
// is the one somebody will be doing under time pressure, in a hurry, possibly
// with a lawyer on the phone. So it is written to be hard to get half right:
// either a new snapshot exists that verifies and does not contain the document,
// or nothing has changed and there is an error saying why.
//
// # Why a removal is a new snapshot
//
// A snapshot is immutable and its manifest is signed. Editing one in place
// would mean re-signing it, which makes every copy anybody already downloaded
// indistinguishable from a tampered one, and it would mean the record of the
// removal lives only in the thing that no longer holds the document. So a
// removal writes a new snapshot that names the old one as its parent and
// carries a tombstone for every document taken out.
//
// The old snapshot is not touched. What happens to it is a publication
// decision rather than a storage one: whether it is withdrawn, kept for people
// who already have it, or left up, is a question for whoever answered the
// request, and this code refuses to answer it for them.
//
// # What a tombstone can and cannot say
//
// It keeps the document identity, because a later crawl that meets the same
// page has to recognize it and not fetch it again, and because a person asking
// whether their document was removed needs an answer that is checkable.
//
// It keeps nothing else. No text, no URL, no host. A tombstone that quotes what
// it removed has not removed it, and a tombstone that names the URL has
// published the fact that a particular page was the subject of a request, which
// is frequently the thing the person wanted taken down.
//
// # What this refuses to do
//
// A removal that finds nothing is an error. A takedown request answered with a
// new snapshot, a signature and a report saying nothing was removed is the
// worst outcome available here, because everybody involved will believe the
// document is gone. If an identity is not in the parent, that is far more
// likely to be the wrong identity than an empty request.
//
// Re-running the same removal is not an error. The second run finds the
// documents already tombstoned and says so, which is what makes this safe to
// put in a script that might run twice.

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tamnd/gao/doc"
)

// Errors a removal can fail with. They are separate because the response to
// each is different: a bad request is fixed by the person asking, a dirty
// parent is fixed before anything else happens, and a destination that already
// exists means somebody else is midway through this.
var (
	ErrNoSuchDocument = errors.New("kho: no such document in the snapshot")
	ErrNothingRemoved = errors.New("kho: the removal did not name a document that is in the snapshot")
	ErrBadRemoval     = errors.New("kho: the removal is not well formed")
	ErrDestExists     = errors.New("kho: the destination already holds a snapshot")
)

// Reasons a document can be removed. It is a closed set for the same reason
// [doc.Source] is: the tombstone table in a release note is generated from it,
// and a free string means one reason becomes three spellings of itself.
const (
	// ReasonTakedown is a request from somebody with a claim on the document,
	// honored without conceding the claim.
	ReasonTakedown = "takedown"

	// ReasonLegal is a removal we were ordered to make or decided we had to.
	ReasonLegal = "legal"

	// ReasonPrivacy is personal data that the covering stage should have caught
	// and did not. Each one of these is also a bug report against che.
	ReasonPrivacy = "privacy"
)

// Reasons returns the removal reasons, which a command line uses to check an
// argument and a release note uses to lay out a table.
func Reasons() []string { return []string{ReasonTakedown, ReasonLegal, ReasonPrivacy} }

// A Removal names one document to take out and why.
type Removal struct {
	DocID  doc.Hash
	Reason string

	// Note is for the person reading the record in two years. It must not
	// quote the document, and nothing here checks that, because no check can:
	// it is a rule for whoever types it.
	Note string
}

// RemoveReport is what a removal did, and it is the thing that gets kept.
type RemoveReport struct {
	// Snapshot is the new snapshot and Parent is the one it was made from.
	Snapshot string
	Parent   string

	// Removed, Tombstoned and NotFound account for every identity that was
	// asked for, exactly once. Tombstoned is the ones the parent had already
	// removed, which is a re-run rather than a mistake.
	Removed    []doc.Hash
	Tombstoned []doc.Hash
	NotFound   []doc.Hash

	// Rewritten is the shards that held one of the documents and were written
	// again without it. Copied is the rest, which came across byte for byte and
	// kept the hashes the parent manifest recorded for them.
	//
	// The split is worth printing. A removal that rewrote most of the corpus
	// costs a full re-upload, and one that rewrote two shards out of 750 costs
	// two files, which is the difference between a takedown answered in minutes
	// and one answered tomorrow.
	Rewritten []string
	Copied    []string

	// Counts is the new snapshot's accounting.
	Counts Counts
}

// removeOptions is the settled configuration for [Remove].
type removeOptions struct {
	at       time.Time
	progress func(name string, rewritten bool)
}

// RemoveOption configures a removal.
type RemoveOption func(*removeOptions)

// RemoveAt sets the time recorded on the tombstones and on the signature.
//
// It exists because a snapshot is supposed to be reproducible, and a function
// that reaches for the wall clock cannot be tested for byte identity. The
// default is now, in UTC.
func RemoveAt(t time.Time) RemoveOption {
	return func(o *removeOptions) { o.at = t.UTC() }
}

// RemoveProgress reports each shard as it is dealt with, so that a removal over
// 750 shards says something while it runs.
func RemoveProgress(f func(name string, rewritten bool)) RemoveOption {
	return func(o *removeOptions) { o.progress = f }
}

// Remove writes dst as src with the named documents taken out.
//
// src must verify completely before anything is written. Removing a document
// from a snapshot whose bytes do not match its manifest produces a signed
// snapshot attesting to a corpus nobody checked, and the moment to find that
// out is before the signature, not after.
//
// dst must not already hold a snapshot. The name is the new snapshot's
// identifier, conventionally the parent's with a suffix that says why.
func Remove(src, dst, snapshot string, key ed25519.PrivateKey, rs []Removal, opts ...RemoveOption) (*RemoveReport, error) {
	o := removeOptions{at: time.Now().UTC()}
	for _, opt := range opts {
		opt(&o)
	}
	if snapshot == "" {
		return nil, fmt.Errorf("%w: the new snapshot has no name", ErrBadRemoval)
	}
	wanted, err := checkRemovals(rs)
	if err != nil {
		return nil, err
	}

	parent, err := ReadManifest(src)
	if err != nil {
		return nil, err
	}
	if _, err := Verify(src, TrustKey(nil)); err != nil {
		return nil, fmt.Errorf("kho: the snapshot to remove from does not verify: %w", err)
	}
	if err := agreesWithItsShards(parent); err != nil {
		return nil, err
	}
	if err := emptyDir(dst); err != nil {
		return nil, err
	}

	report := &RemoveReport{Snapshot: snapshot, Parent: parent.Snapshot}
	already := make(map[doc.Hash]bool, len(parent.Tombstones))
	for _, t := range parent.Tombstones {
		already[t.DocID] = true
	}

	// The counts come off the parent's by subtraction rather than by counting
	// the survivors, because Tokens is measured with a tokenizer that is not
	// loaded here and a recount without one would have to leave it stale or
	// wrong. Every document carries its own numbers, so the subtraction is
	// exact, and the document total is checked against the shards afterwards.
	counts := parent.Counts
	counts.BySource = cloneCounts(parent.Counts.BySource)

	next := *parent
	next.Snapshot = snapshot
	next.Parent = parent.Snapshot
	next.CreatedAt = o.at
	next.Shards = slices.Clone(parent.Shards)
	next.Signature = Signature{}

	for i, sh := range parent.Shards {
		hit, err := shardHolds(filepath.Join(src, sh.Name), wanted)
		if err != nil {
			return nil, err
		}
		if len(hit) == 0 {
			if err := copyFile(filepath.Join(src, sh.Name), filepath.Join(dst, sh.Name)); err != nil {
				return nil, err
			}
			report.Copied = append(report.Copied, sh.Name)
			if o.progress != nil {
				o.progress(sh.Name, false)
			}
			continue
		}
		written, err := rewriteShard(filepath.Join(src, sh.Name), filepath.Join(dst, sh.Name), wanted, &counts)
		if err != nil {
			return nil, err
		}
		written.Name, written.Index = sh.Name, sh.Index
		next.Shards[i] = written
		report.Rewritten = append(report.Rewritten, sh.Name)
		report.Removed = append(report.Removed, hit...)
		if o.progress != nil {
			o.progress(sh.Name, true)
		}
	}

	slices.SortFunc(report.Removed, compareHash)
	found := make(map[doc.Hash]bool, len(report.Removed))
	for _, id := range report.Removed {
		found[id] = true
	}
	for _, r := range rs {
		switch {
		case found[r.DocID]:
		case already[r.DocID]:
			report.Tombstoned = append(report.Tombstoned, r.DocID)
		default:
			report.NotFound = append(report.NotFound, r.DocID)
		}
	}

	if len(report.NotFound) > 0 {
		// Nothing has been signed yet, so the half written destination is the
		// only thing to undo, and leaving it would look like a snapshot.
		_ = os.RemoveAll(dst)
		return report, fmt.Errorf("%w: %s", ErrNoSuchDocument, joinHashes(report.NotFound))
	}
	if len(report.Removed) == 0 && len(report.Tombstoned) == 0 {
		_ = os.RemoveAll(dst)
		return report, ErrNothingRemoved
	}

	next.Tombstones = mergeTombstones(parent.Tombstones, rs, found, o.at)
	next.Counts = counts
	if err := agreesWithItsShards(&next); err != nil {
		return report, err
	}
	next.Root = next.ComputeRoot()
	if err := next.Seal(key, o.at); err != nil {
		return report, err
	}
	if err := WriteManifest(dst, &next); err != nil {
		return report, err
	}
	report.Counts = counts
	return report, nil
}

// checkRemovals turns the request into the set the shard pass matches against,
// and rejects the request shapes that would otherwise produce a snapshot nobody
// can read the record of later.
func checkRemovals(rs []Removal) (map[doc.Hash]bool, error) {
	if len(rs) == 0 {
		return nil, fmt.Errorf("%w: it names no documents", ErrBadRemoval)
	}
	wanted := make(map[doc.Hash]bool, len(rs))
	for _, r := range rs {
		if r.DocID.IsZero() {
			return nil, fmt.Errorf("%w: a removal with no document identity", ErrBadRemoval)
		}
		if !slices.Contains(Reasons(), r.Reason) {
			return nil, fmt.Errorf("%w: %q is not a removal reason, and they are %s",
				ErrBadRemoval, r.Reason, strings.Join(Reasons(), ", "))
		}
		if wanted[r.DocID] {
			return nil, fmt.Errorf("%w: %s is named twice", ErrBadRemoval, r.DocID)
		}
		wanted[r.DocID] = true
	}
	return wanted, nil
}

// shardHolds reports which of the wanted documents are in one shard.
//
// It is a separate pass over the file from the rewrite, which costs a second
// decompression of the shards that turn out to hold something. That buys the
// property the whole design rests on: a shard that holds none of them is never
// opened for writing, so it is copied with its bytes and its hash intact, and a
// removal touches the smallest number of files it can.
func shardHolds(path string, wanted map[doc.Hash]bool) ([]doc.Hash, error) {
	seg, err := Open[*doc.Document](path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = seg.Close() }()

	var hit []doc.Hash
	for d, err := range seg.All() {
		if err != nil {
			return nil, err
		}
		if wanted[d.DocID] {
			hit = append(hit, d.DocID)
		}
	}
	return hit, nil
}

// rewriteShard writes the shard again without the wanted documents, and takes
// what each removed document was worth out of the counts as it goes.
func rewriteShard(from, to string, wanted map[doc.Hash]bool, counts *Counts) (Shard, error) {
	seg, err := Open[*doc.Document](from)
	if err != nil {
		return Shard{}, err
	}
	defer func() { _ = seg.Close() }()

	// Written under a part extension and renamed, the same way a shard set
	// builds one, so a removal that dies halfway leaves nothing that reads as a
	// finished shard.
	part := to + partExt
	f, err := os.Create(part)
	if err != nil {
		return Shard{}, fmt.Errorf("kho: %w", err)
	}
	w, err := NewWriter[*doc.Document](f)
	if err != nil {
		_ = f.Close()
		return Shard{}, err
	}

	for d, err := range seg.All() {
		if err != nil {
			_ = f.Close()
			return Shard{}, err
		}
		if wanted[d.DocID] {
			subtract(counts, d)
			continue
		}
		if err := w.Append(d); err != nil {
			_ = f.Close()
			return Shard{}, err
		}
	}
	if err := w.Close(); err != nil {
		_ = f.Close()
		return Shard{}, err
	}
	if err := f.Close(); err != nil {
		return Shard{}, fmt.Errorf("kho: %w", err)
	}
	if err := os.Rename(part, to); err != nil {
		return Shard{}, fmt.Errorf("kho: %w", err)
	}

	// The hash and the size come off the file rather than off the writer, which
	// is what [verifyShard] checks them against. A shard whose recorded size is
	// the text it holds rather than the bytes it occupies fails verification on
	// a snapshot that is perfectly good.
	hash, size, err := hashFile(to)
	if err != nil {
		return Shard{}, err
	}
	return Shard{Documents: w.Count(), Bytes: size, Hash: hash}, nil
}

// subtract takes one document out of the accounting.
func subtract(c *Counts, d *doc.Document) {
	c.Documents--
	c.Bytes -= int64(len(d.Text))
	c.Chars -= int64(d.NChars)
	c.Syllables -= int64(d.NSyllables)
	c.Tokens -= int64(d.NTokens)
	if d.Source.Natural() {
		c.Natural--
	} else {
		c.Synthetic--
	}
	if c.BySource != nil {
		if n := c.BySource[string(d.Source)] - 1; n > 0 {
			c.BySource[string(d.Source)] = n
		} else {
			delete(c.BySource, string(d.Source))
		}
	}
}

// agreesWithItsShards checks the one thing a removal can get wrong quietly. The
// counts are arrived at by subtraction, so if the parent's document total was
// already wrong the child's would be wrong by the same amount and nothing would
// say so. Checking both ends means a bad parent is caught before it is copied
// and a bad subtraction is caught before it is signed.
func agreesWithItsShards(m *Manifest) error {
	var n int64
	for _, s := range m.Shards {
		n += int64(s.Documents)
	}
	if n != m.Counts.Documents {
		return fmt.Errorf("%w: %s counts %d documents and its shards hold %d",
			ErrBadManifest, m.Snapshot, m.Counts.Documents, n)
	}
	return nil
}

// mergeTombstones carries the parent's forward and adds one for each document
// this removal actually took out.
//
// The parent's are kept because otherwise the record of a removal survives
// exactly one further removal, and the whole point of a tombstone is that it
// outlives the thing it describes. They are sorted by identity so that two runs
// of the same removal produce the same file.
func mergeTombstones(parent []Tombstone, rs []Removal, found map[doc.Hash]bool, at time.Time) []Tombstone {
	out := slices.Clone(parent)
	for _, r := range rs {
		if !found[r.DocID] {
			continue
		}
		out = append(out, Tombstone{
			DocID:     r.DocID,
			Reason:    r.Reason,
			RemovedAt: at,
			Note:      r.Note,
		})
	}
	slices.SortStableFunc(out, func(a, b Tombstone) int { return compareHash(a.DocID, b.DocID) })
	return out
}

// emptyDir makes dst and refuses to write into one that already holds a
// snapshot, since a removal that half overwrote somebody else's is worse than
// one that did not run.
func emptyDir(dst string) error {
	if _, err := os.Stat(filepath.Join(dst, ManifestName)); err == nil {
		return fmt.Errorf("%w: %s", ErrDestExists, dst)
	}
	entries, err := os.ReadDir(dst)
	if err == nil && len(entries) > 0 {
		return fmt.Errorf("%w: %s is not empty", ErrDestExists, dst)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("kho: %w", err)
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return fmt.Errorf("kho: %w", err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(to)
	if err != nil {
		return fmt.Errorf("kho: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("kho: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("kho: %w", err)
	}
	return nil
}

func cloneCounts(in map[string]int64) map[string]int64 {
	if in == nil {
		return nil
	}
	out := make(map[string]int64, len(in))
	maps.Copy(out, in)
	return out
}

func compareHash(a, b doc.Hash) int { return strings.Compare(a.String(), b.String()) }

func joinHashes(hs []doc.Hash) string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.String()
	}
	return strings.Join(out, ", ")
}
