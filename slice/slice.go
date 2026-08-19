// Package slice publishes a release slice as a view over a sealed snapshot rather
// than as a second copy of the bytes.
//
// A slice is a named subset of the corpus with its own dataset repo and its own
// dataset card: the educational shard, the legal shard, the part small enough
// for somebody with one card to fine tune on. The obvious way to ship one is to
// select the rows and write them out again, and that is what most corpora do.
//
// The reason not to is not the disk, although a slice built that way costs the
// full size of what it selects and several slices over the same corpus cost
// more than the corpus. The reason is that a copy is a second place a document
// lives. When a takedown arrives, [github.com/tamnd/gao/store.Remove] rewrites the
// snapshot that holds the document and seals a new one carrying a tombstone.
// A copy does not hear about that. The document stays published in the slice,
// under our name, after we have told somebody it was removed, and nothing in the
// system knows.
//
// So a slice names its parent snapshot by manifest digest and names the parent's
// shards, and holds no bytes at all. Reading the slice means reading the
// parent's Parquet with the slice's predicate applied, which is one line of SQL
// against the Hub. A removal in the parent is a removal in the slice by
// construction, because there is only one copy of the row.
//
// What that costs is that a slice can go stale. The parent digest is pinned, so
// when the parent is superseded by a removal the slice is a view over a snapshot
// that is no longer the head of its lineage, and [Slice.Against] says so rather
// than resolving it quietly. Re-deriving a stale slice is cheap: it is running
// the predicate again. Not noticing that it went stale is the failure this
// package exists to make impossible, and a check that repaired itself silently
// would hide exactly the event somebody has to be told about.
//
// The membership digest is what makes a view checkable. A slice records, per
// shard, how many documents it selects and the digest over their identities in
// sorted order. Somebody who runs the predicate themselves can compare that
// digest to ours and find out whether they got the rows we meant, which a
// predicate alone cannot tell them: a filter that means something slightly
// different on a different engine still returns rows, and they still look fine.
package slice

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/store"
	"github.com/zeebo/blake3"
)

// SliceName is the file a slice lives in inside its own directory.
const SliceName = "slice.toml"

// SliceVersion is the version of the slice layout, kept separate from the
// manifest version because a slice and a snapshot change for different reasons.
const SliceVersion uint16 = 1

// A Rule is how a slice was selected, written so that somebody else can select
// the same rows.
type Rule struct {
	// Where is the predicate over the record columns, as SQL, verbatim. It is
	// SQL because that is what a reader has: the corpus is Parquet on the Hub and
	// the thing anybody points at it is DuckDB. A rule written as prose is a rule
	// only we can apply.
	Where string `toml:"where"`

	// Note is what the slice is for, in a sentence, and it goes at the top of the
	// dataset card. It is not part of the digest.
	Note string `toml:"note,omitempty"`
}

// A Member is one of the parent's shards, and how much of it the slice takes.
// The bytes stay in the parent's file: this records what is selected out of it,
// not a new file.
type Member struct {
	// Shard is the parent's shard index, which is what names the file.
	Shard int `toml:"shard"`

	// Hash is the parent shard's content hash at the time the slice was derived.
	// A parent whose shard now hashes differently is not the parent this slice
	// was taken from, whatever the snapshot is still called.
	Hash doc.Hash `toml:"hash"`

	Documents int `toml:"documents"`

	// Bytes is UTF-8 bytes of text selected, which is the number a slice's size
	// is quoted in. It is not a file size, because the slice has no files.
	Bytes     int64 `toml:"bytes"`
	Chars     int64 `toml:"chars"`
	Syllables int64 `toml:"syllables"`
	Tokens    int64 `toml:"tokens"`

	// Docs is the digest over the selected document identities, sorted. It is
	// what lets somebody who ran the predicate themselves find out whether they
	// selected the same rows we did.
	Docs doc.Hash `toml:"docs"`
}

// A Slice is a published subset of a snapshot, holding no bytes of its own.
type Slice struct {
	SliceVersion uint16 `toml:"slice_version"`

	// Name is the slice, conventionally gao-edu or gao-legal.
	Name string `toml:"name"`

	// Dataset is the repo it is published as, which has to admit what the slice
	// carries.
	Dataset string `toml:"dataset"`

	// Parent is the snapshot the rows live in.
	Parent string `toml:"parent"`

	// ParentDigest is the parent manifest's digest, which pins the slice to one
	// exact snapshot rather than to a name somebody can reseal.
	ParentDigest doc.Hash `toml:"parent_digest"`

	// ParentRoot is the parent's merkle root, carried so that a reader holding
	// only the slice can tell which corpus it is looking at.
	ParentRoot doc.Hash `toml:"parent_root"`

	CreatedAt time.Time `toml:"created_at"`

	// Tokenizer names what counted the tokens, since a token count without one
	// is not a token count.
	Tokenizer string `toml:"tokenizer,omitempty"`

	// Classes is the license classes the selected rows carry. It is recorded
	// rather than inherited from the parent, because a slice selects and the
	// point of selecting is that the result is not the parent. Wikipedia lives
	// in a repo of its own so that its share alike term stays contained, and a
	// slice that pulls those rows into a permissively licensed repo undoes that
	// containment while looking like a view.
	Classes []doc.LicenseClass `toml:"classes"`

	Rule    Rule     `toml:"rule"`
	Members []Member `toml:"member"`
}

// Errors a slice can fail with.
var (
	ErrBadSlice = errors.New("slice: slice is incomplete")
	ErrStale    = errors.New("slice: stale")
)

// Documents is how many rows the slice selects.
func (s *Slice) Documents() int64 {
	var n int64
	for _, m := range s.Members {
		n += int64(m.Documents)
	}
	return n
}

// Counts is the slice's own accounting, summed over the shards it draws from.
func (s *Slice) Counts() store.Counts {
	c := store.Counts{Tokenizer: s.Tokenizer}
	for _, m := range s.Members {
		c.Documents += int64(m.Documents)
		c.Bytes += m.Bytes
		c.Chars += m.Chars
		c.Syllables += m.Syllables
		c.Tokens += m.Tokens
	}
	return c
}

// Shards is how many of the parent's shards the slice draws from.
func (s *Slice) Shards() int { return len(s.Members) }

// Digest identifies the slice by what it selects rather than by what it is
// called. The note is outside it, for the same reason a note is outside every
// other digest in this project: making a clearer explanation invalidate a
// published identifier teaches people to stop writing explanations.
func (s *Slice) Digest() doc.Hash {
	d := blake3.New()
	str := func(v string) {
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(len(v)))
		_, _ = d.Write(n[:])
		_, _ = d.Write([]byte(v))
	}
	num := func(v int64) {
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(v))
		_, _ = d.Write(n[:])
	}

	num(int64(s.SliceVersion))
	str(s.Name)
	str(s.Dataset)
	str(s.Parent)
	str(s.ParentDigest.String())
	str(s.ParentRoot.String())
	str(s.Tokenizer)
	str(s.Rule.Where)

	classes := slices.Clone(s.Classes)
	slices.Sort(classes)
	num(int64(len(classes)))
	for _, c := range classes {
		str(c.String())
	}

	members := slices.Clone(s.Members)
	slices.SortStableFunc(members, func(a, b Member) int { return a.Shard - b.Shard })
	num(int64(len(members)))
	for _, m := range members {
		num(int64(m.Shard))
		str(m.Hash.String())
		num(int64(m.Documents))
		num(m.Bytes)
		num(m.Chars)
		num(m.Syllables)
		num(m.Tokens)
		str(m.Docs.String())
	}
	return doc.Hash(d.Sum(nil))
}

// Members digests a shard's selected document identities. The identities are
// sorted first, so that two runs that selected the same rows in a different
// order agree, and a run that selected different rows does not.
func Members(ids []doc.Hash) doc.Hash {
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, func(a, b doc.Hash) int { return strings.Compare(a.String(), b.String()) })

	d := blake3.New()
	for i, id := range sorted {
		if i > 0 && id == sorted[i-1] {
			continue // a document selected twice is selected once
		}
		_, _ = d.Write(id[:])
	}
	return doc.Hash(d.Sum(nil))
}

// Query is the line somebody pastes to read the slice. It is the parent's
// Parquet with the slice's predicate applied, because that is what a view is.
func (s *Slice) Query(d store.Dataset) string {
	return fmt.Sprintf("SELECT * FROM %s WHERE %s", d.Query(s.Parent), s.Rule.Where)
}

// Saved is the bytes of text this slice would have duplicated had it been
// published as a copy, which is the whole of what it selects.
func (s *Slice) Saved() int64 { return s.Counts().Bytes }

// check reports every way the slice is not a slice, without reference to the
// parent, which [Slice.Against] does separately.
func (s *Slice) check() error {
	var problems []error
	if s.Name == "" {
		problems = append(problems, errors.New("the slice has no name"))
	}
	if s.Dataset == "" {
		problems = append(problems, errors.New("the slice does not say which repo it is published as"))
	}
	if s.Parent == "" {
		problems = append(problems, errors.New("the slice does not name a parent snapshot, so there is nothing for it to be a view over"))
	}
	if s.ParentDigest.IsZero() {
		problems = append(problems, errors.New("the slice does not pin the parent manifest, so it is a view over whatever that snapshot name points at today"))
	}
	if s.CreatedAt.IsZero() {
		problems = append(problems, errors.New("created_at is unset"))
	} else if s.CreatedAt.Location() != time.UTC {
		problems = append(problems, errors.New("created_at is not UTC"))
	}
	if strings.TrimSpace(s.Rule.Where) == "" {
		problems = append(problems, errors.New("the slice has no predicate, so nobody outside can select the same rows"))
	}
	if len(s.Members) == 0 {
		problems = append(problems, errors.New("the slice selects nothing"))
	}

	seen := make(map[int]bool, len(s.Members))
	for _, m := range s.Members {
		if seen[m.Shard] {
			problems = append(problems, fmt.Errorf("shard %d appears twice", m.Shard))
		}
		seen[m.Shard] = true
		if m.Shard < 0 {
			problems = append(problems, fmt.Errorf("shard %d is not a shard index", m.Shard))
		}
		if m.Hash.IsZero() {
			problems = append(problems, fmt.Errorf("shard %d is named without the hash it had, so a parent that changed underneath would not show", m.Shard))
		}
		if m.Documents <= 0 {
			problems = append(problems, fmt.Errorf("shard %d is listed and selects %d documents, and a shard the slice takes nothing from is not a member of it", m.Shard, m.Documents))
		}
		if m.Docs.IsZero() {
			problems = append(problems, fmt.Errorf("shard %d has no membership digest, so nobody can check they selected the same rows", m.Shard))
		}
		if m.Bytes < 0 || m.Chars < 0 || m.Syllables < 0 || m.Tokens < 0 {
			problems = append(problems, fmt.Errorf("shard %d has a negative count", m.Shard))
		}
	}
	if s.Tokenizer == "" && s.Counts().Tokens > 0 {
		problems = append(problems, errors.New("a token count without a tokenizer is not a token count"))
	}
	if len(s.Classes) == 0 {
		problems = append(problems, errors.New("the slice does not say what license classes it carries, and a repo cannot be checked against nothing"))
	}
	for _, c := range s.Classes {
		if !c.Valid() || c == doc.LicenseUnknown {
			problems = append(problems, fmt.Errorf("%s is not a license class a slice can carry", c))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrBadSlice, errors.Join(problems...))
	}
	return nil
}
