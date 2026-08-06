package lat

// Checking a slice against the snapshot it is a view over, and against the repo
// it is published as.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"
	"github.com/tamnd/gao/kho"
)

// Against reports every way the slice and its parent disagree. An empty result
// means the slice is a view over exactly the snapshot it says it is.
//
// The faults come out in a fixed order, because the thing somebody does with two
// of these is diff them.
func (s *Slice) Against(m *kho.Manifest) []string {
	var faults []string
	if err := s.check(); err != nil {
		faults = append(faults, err.Error())
	}

	if m.Snapshot != s.Parent {
		faults = append(faults, fmt.Sprintf("the slice is a view over %s and this manifest is %s", s.Parent, m.Snapshot))
		return faults
	}
	if d := m.Digest(); d != s.ParentDigest {
		faults = append(faults, fmt.Sprintf("the slice was taken from manifest %s and this %s is manifest %s, so the snapshot was resealed under the same name",
			short(s.ParentDigest), m.Snapshot, short(d)))
	}
	if !s.ParentRoot.IsZero() && s.ParentRoot != m.Root {
		faults = append(faults, fmt.Sprintf("the slice carries merkle root %s and the parent's is %s", short(s.ParentRoot), short(m.Root)))
	}

	shards := make(map[int]kho.Shard, len(m.Shards))
	for _, sh := range m.Shards {
		shards[sh.Index] = sh
	}
	var documents int64
	for _, mem := range s.Members {
		parent, ok := shards[mem.Shard]
		if !ok {
			faults = append(faults, fmt.Sprintf("the slice draws from shard %d and the parent does not have one", mem.Shard))
			continue
		}
		if parent.Hash != mem.Hash {
			faults = append(faults, fmt.Sprintf("shard %d hashed %s when the slice was derived and hashes %s now, so the rows underneath it are not the rows that were selected",
				mem.Shard, short(mem.Hash), short(parent.Hash)))
		}
		if mem.Documents > parent.Documents {
			faults = append(faults, fmt.Sprintf("the slice takes %d documents out of shard %d and the shard holds %d",
				mem.Documents, mem.Shard, parent.Documents))
		}
		documents += int64(mem.Documents)
	}

	c := s.Counts()
	switch {
	case documents > m.Counts.Documents:
		faults = append(faults, fmt.Sprintf("the slice selects %d documents and the parent holds %d", documents, m.Counts.Documents))
	case documents == m.Counts.Documents && len(s.Members) == len(m.Shards):
		// Not an error in the sense of arithmetic, and worth failing anyway. A
		// slice that selects the whole corpus is the corpus published a second
		// time under a name that suggests it is something smaller, which is a
		// thing a reader will believe.
		faults = append(faults, fmt.Sprintf("the slice selects all %d documents of %s, which is the parent under another name rather than a slice of it",
			documents, m.Snapshot))
	}
	if c.Bytes > m.Counts.Bytes {
		faults = append(faults, fmt.Sprintf("the slice holds %d bytes of text and the parent holds %d", c.Bytes, m.Counts.Bytes))
	}
	if c.Tokens > m.Counts.Tokens && m.Counts.Tokens > 0 {
		faults = append(faults, fmt.Sprintf("the slice counts %d tokens and the parent counts %d", c.Tokens, m.Counts.Tokens))
	}
	if s.Tokenizer != "" && m.Counts.Tokenizer != "" && s.Tokenizer != m.Counts.Tokenizer {
		faults = append(faults, fmt.Sprintf("the slice counted tokens with %s and the parent with %s, so the two token counts are not the same unit",
			s.Tokenizer, m.Counts.Tokenizer))
	}
	return faults
}

// Fresh reports whether the slice is a view over the head of its lineage.
//
// A removal seals a new snapshot rather than editing the old one, so a slice
// pinned to a superseded parent is a view over rows a takedown has already been
// applied to somewhere else. Re-deriving it costs one pass of the predicate.
// Resolving it quietly would cost the only thing this package is for.
func (s *Slice) Fresh(head *kho.Manifest) error {
	if head.Snapshot == s.Parent && head.Digest() == s.ParentDigest {
		return nil
	}
	if len(head.Tombstones) > 0 {
		return fmt.Errorf("%w: %s is a view over %s, which %s has superseded carrying %s, so re-derive it before it is published again",
			ErrStale, s.Name, s.Parent, head.Snapshot, plural(len(head.Tombstones), "tombstone"))
	}
	return fmt.Errorf("%w: %s is a view over %s and the head of that lineage is %s",
		ErrStale, s.Name, s.Parent, head.Snapshot)
}

// Publishable reports every reason the slice may not go to the repo it names.
//
// A slice inherits nothing here. The repo table says which license classes each
// repo carries and whether it carries text at all, and a slice that selects rows
// of a class its target does not admit is the containment failure the table
// exists to prevent, arriving through the one path that does not copy anything.
func (s *Slice) Publishable() []string {
	var faults []string
	d, ok := kho.Lookup(s.Dataset)
	if !ok {
		return []string{fmt.Sprintf("%s is not a repo in the dataset table, and a slice cannot be published to a repo nobody declared", s.Dataset)}
	}
	if !d.Public() {
		faults = append(faults, fmt.Sprintf("%s is a working repo, and a working repo is deleted when the snapshot that consumed it seals", d.Repo()))
	}
	for _, c := range s.Classes {
		if !d.Admits(c) {
			faults = append(faults, fmt.Sprintf("the slice carries %s documents and %s does not admit them", c, d.Repo()))
		}
	}
	return faults
}

// Repo is the dataset the slice publishes as.
func (s *Slice) Repo() (kho.Dataset, bool) { return kho.Lookup(s.Dataset) }

// Read reads a slice from a directory.
func Read(dir string) (*Slice, error) {
	var s Slice
	path := filepath.Join(dir, SliceName)
	if _, err := toml.DecodeFile(path, &s); err != nil {
		return nil, fmt.Errorf("lat: reading %s: %w", path, err)
	}
	if s.SliceVersion > SliceVersion {
		return nil, fmt.Errorf("lat: %s is slice version %d, this build understands %d", path, s.SliceVersion, SliceVersion)
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Write writes a slice into a directory. It refuses to overwrite, because a
// slice is published and a published thing that can be edited in place is not
// one anybody can pin.
func Write(dir string, s *Slice) error {
	if err := s.check(); err != nil {
		return err
	}
	path := filepath.Join(dir, SliceName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("lat: writing the slice: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := toml.NewEncoder(f).Encode(s); err != nil {
		return fmt.Errorf("lat: encoding the slice: %w", err)
	}
	return f.Close()
}

// Overlap is how many of the parent's shards two slices both draw from, and how
// many documents they could at most have in common.
//
// Slices are allowed to overlap and normally do: a document can be both
// educational and legal. This exists so that a release note can say how much,
// because the sum of the slices is not the corpus and a reader adding them up
// will otherwise get a number larger than what was published.
func Overlap(a, b *Slice) (shards int, documents int64) {
	in := make(map[int]Member, len(a.Members))
	for _, m := range a.Members {
		in[m.Shard] = m
	}
	for _, m := range b.Members {
		other, ok := in[m.Shard]
		if !ok {
			continue
		}
		shards++
		documents += int64(min(m.Documents, other.Documents))
	}
	return shards, documents
}

// Sort orders slices by name, so that a report over several of them reads the
// same on every box.
func Sort(ss []*Slice) {
	slices.SortStableFunc(ss, func(a, b *Slice) int {
		if a.Name != b.Name {
			if a.Name < b.Name {
				return -1
			}
			return 1
		}
		return 0
	})
}

func short(h interface{ String() string }) string {
	s := h.String()
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
