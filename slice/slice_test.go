package slice

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/store"
)

// id makes a document identity out of a label, so a test can say which document
// it means.
func id(s string) doc.Hash { return doc.SumString(s) }

// parent is a small sealed snapshot: two shards, ten documents, with the
// counts agreeing with the shards the way store requires.
func parent() *store.Manifest {
	return &store.Manifest{
		ManifestVersion: store.ManifestVersion,
		SchemaVersion:   doc.SchemaVersion,
		Snapshot:        "gao-v1.0",
		CreatedAt:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		Pipeline:        "0.1.0",
		Stages:          []store.Stage{{Name: "normalize@0.1.0", ConfigHash: id("normalize")}},
		Counts: store.Counts{
			Documents: 10, Natural: 10,
			Bytes: 10000, Chars: 9000, Syllables: 2000,
			Tokens: 3000, Tokenizer: "gemma-3",
		},
		Shards: []store.Shard{
			{Name: "part-00000.parquet", Index: 0, Documents: 6, Bytes: 600, Hash: id("shard 0")},
			{Name: "part-00001.parquet", Index: 1, Documents: 4, Bytes: 400, Hash: id("shard 1")},
		},
		Root: id("root"),
	}
}

// edu is a slice over that snapshot: four documents out of the two shards.
func edu() *Slice {
	p := parent()
	return &Slice{
		SliceVersion: SliceVersion,
		Name:         "gao-edu",
		Dataset:      "vietnamese-web-text",
		Parent:       p.Snapshot,
		ParentDigest: p.Digest(),
		ParentRoot:   p.Root,
		CreatedAt:    time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
		Tokenizer:    "gemma-3",
		Classes:      []doc.LicenseClass{doc.LicenseOpen},
		Rule:         Rule{Where: "quality >= 0.8 AND source = 'edu'", Note: "the part worth fine tuning on"},
		Members: []Member{
			{Shard: 0, Hash: id("shard 0"), Documents: 3, Bytes: 300, Chars: 270, Syllables: 60, Tokens: 90, Docs: Members([]doc.Hash{id("a"), id("b"), id("c")})},
			{Shard: 1, Hash: id("shard 1"), Documents: 1, Bytes: 100, Chars: 90, Syllables: 20, Tokens: 30, Docs: Members([]doc.Hash{id("d")})},
		},
	}
}

func faultAbout(faults []string, s string) bool {
	for _, f := range faults {
		if strings.Contains(f, s) {
			return true
		}
	}
	return false
}

func TestASliceOverItsOwnParentChecksClean(t *testing.T) {
	if faults := edu().Against(parent()); len(faults) > 0 {
		t.Fatalf("an honest slice was faulted: %v", faults)
	}
	if got := edu().Documents(); got != 4 {
		t.Errorf("the slice selects %d documents, want 4", got)
	}
	if got := edu().Counts().Bytes; got != 400 {
		t.Errorf("the slice holds %d bytes, want 400", got)
	}
}

func TestASliceHoldsNoBytesOfItsOwn(t *testing.T) {
	// The number worth printing is what publishing this as a copy would have
	// duplicated, since that is what the view does not spend.
	s := edu()
	if s.Saved() != s.Counts().Bytes {
		t.Errorf("the slice saves %d and selects %d", s.Saved(), s.Counts().Bytes)
	}
	if s.Shards() != 2 {
		t.Errorf("the slice draws from %d shards, want 2", s.Shards())
	}
}

func TestAResealedParentIsNotTheParentTheSliceWasTakenFrom(t *testing.T) {
	// Same snapshot name, different manifest. This is the case a slice pinned by
	// name alone would sail straight through.
	p := parent()
	p.Counts.Rejected = 7

	faults := edu().Against(p)
	if len(faults) == 0 {
		t.Fatal("a slice over a resealed snapshot checked clean")
	}
	if !faultAbout(faults, "resealed") {
		t.Errorf("the fault does not say what happened: %v", faults)
	}
}

func TestAShardThatChangedUnderneathIsCaught(t *testing.T) {
	p := parent()
	p.Shards[0].Hash = id("shard 0 rewritten")

	faults := edu().Against(p)
	if !faultAbout(faults, "not the rows that were selected") {
		t.Errorf("a shard rewritten under the slice was not reported: %v", faults)
	}
}

func TestASliceCannotTakeMoreOutOfAShardThanIsInIt(t *testing.T) {
	s := edu()
	s.Members[1].Documents = 9 // the shard holds 4

	if faults := s.Against(parent()); !faultAbout(faults, "the shard holds 4") {
		t.Errorf("a slice taking more than the shard holds was accepted: %v", faults)
	}
}

func TestASliceOfEverythingIsTheCorpusUnderAnotherName(t *testing.T) {
	// Arithmetically fine and worth failing anyway: a reader who sees gao-edu
	// beside gao-v1.0 will believe it is smaller.
	s := edu()
	s.Members[0].Documents = 6
	s.Members[1].Documents = 4

	faults := s.Against(parent())
	if !faultAbout(faults, "under another name") {
		t.Errorf("a slice that selects the whole corpus was accepted as a slice: %v", faults)
	}
}

func TestASliceOverADifferentSnapshotSaysSoAndStopsThere(t *testing.T) {
	p := parent()
	p.Snapshot = "gao-v2.0"

	faults := edu().Against(p)
	if len(faults) != 1 {
		t.Fatalf("comparing a slice to an unrelated snapshot produced %d faults, and every one after the first is noise: %v", len(faults), faults)
	}
	if !faultAbout(faults, "gao-v2.0") {
		t.Errorf("the fault does not name the manifest it was handed: %v", faults)
	}
}

func TestASupersededParentMakesTheSliceStale(t *testing.T) {
	// A takedown seals a new snapshot. The slice is still a valid view over the
	// old one, and that is exactly the problem.
	head := parent()
	head.Snapshot = "gao-v1.1"
	head.Parent = "gao-v1.0"
	head.Tombstones = []store.Tombstone{{DocID: id("a"), Reason: store.ReasonTakedown, RemovedAt: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)}}

	err := edu().Fresh(head)
	if err == nil {
		t.Fatal("a slice over a superseded snapshot came back fresh")
	}
	if !strings.Contains(err.Error(), "1 tombstone") {
		t.Errorf("the error does not say what the head carries: %v", err)
	}
	if !strings.Contains(err.Error(), "re-derive") {
		t.Errorf("the error does not say what to do about it: %v", err)
	}
}

func TestASliceOverTheHeadIsFresh(t *testing.T) {
	if err := edu().Fresh(parent()); err != nil {
		t.Errorf("a slice over the head of its lineage came back stale: %v", err)
	}
}

func TestARepoThatDoesNotAdmitTheClassRefusesTheSlice(t *testing.T) {
	// The one containment failure a view can still cause, since it copies
	// nothing and so looks harmless.
	s := edu()
	s.Classes = []doc.LicenseClass{doc.LicenseRestricted}

	faults := s.Publishable()
	if !faultAbout(faults, "does not admit") {
		t.Errorf("restricted rows were accepted into the public text repo: %v", faults)
	}
}

func TestAWorkingRepoIsNotSomewhereToPublishASlice(t *testing.T) {
	s := edu()
	s.Dataset = store.StageRepo

	if faults := s.Publishable(); !faultAbout(faults, "working repo") {
		t.Errorf("a slice published into staging was accepted: %v", faults)
	}
}

func TestARepoNobodyDeclaredIsRefused(t *testing.T) {
	s := edu()
	s.Dataset = "vietnamese-something-else"

	faults := s.Publishable()
	if len(faults) != 1 || !faultAbout(faults, "not a repo in the dataset table") {
		t.Errorf("a slice named an undeclared repo and got: %v", faults)
	}
}

func TestTheHonestSliceIsPublishable(t *testing.T) {
	if faults := edu().Publishable(); len(faults) > 0 {
		t.Errorf("the slice cannot go to the repo it names: %v", faults)
	}
	d, ok := edu().Repo()
	if !ok || d.Name != "vietnamese-web-text" {
		t.Error("the slice does not resolve to its repo")
	}
}

func TestTheMembershipDigestDoesNotDependOnTheOrderRowsCameBack(t *testing.T) {
	// Two engines running the same predicate return the same rows in whatever
	// order they like, and a digest that disagreed about that would report a
	// difference nobody can act on.
	a := Members([]doc.Hash{id("a"), id("b"), id("c")})
	b := Members([]doc.Hash{id("c"), id("a"), id("b")})
	if a != b {
		t.Error("the same rows in a different order digest differently")
	}
	if a == Members([]doc.Hash{id("a"), id("b")}) {
		t.Error("a different set of rows digests the same")
	}
	if a != Members([]doc.Hash{id("a"), id("b"), id("c"), id("a")}) {
		t.Error("a row selected twice changed the membership")
	}
}

func TestTheDigestMovesWithWhatIsSelectedAndNotWithTheProse(t *testing.T) {
	base := edu().Digest()

	note := edu()
	note.Rule.Note = "a clearer sentence about what this slice is for"
	if note.Digest() != base {
		t.Error("improving the note changed the digest, which teaches people to stop writing notes")
	}

	order := edu()
	order.Members = []Member{order.Members[1], order.Members[0]}
	if order.Digest() != base {
		t.Error("listing the shards in the other order changed the digest")
	}

	for name, change := range map[string]func(*Slice){
		"the predicate":  func(s *Slice) { s.Rule.Where = "quality >= 0.9" },
		"the parent":     func(s *Slice) { s.Parent = "gao-v1.1" },
		"the pin":        func(s *Slice) { s.ParentDigest = id("somewhere else") },
		"the membership": func(s *Slice) { s.Members[0].Docs = Members([]doc.Hash{id("a")}) },
		"a count":        func(s *Slice) { s.Members[0].Documents = 2 },
		"the repo":       func(s *Slice) { s.Dataset = "vietnamese-legal-text" },
		"a class":        func(s *Slice) { s.Classes = []doc.LicenseClass{doc.LicensePermissiveAttribution} },
		"the tokenizer":  func(s *Slice) { s.Tokenizer = "something-else" },
	} {
		s := edu()
		change(s)
		if s.Digest() == base {
			t.Errorf("changing %s did not change the digest", name)
		}
	}
}

func TestASliceThatSelectsNothingIsNotASlice(t *testing.T) {
	s := edu()
	s.Members = nil
	if err := s.check(); err == nil {
		t.Error("a slice selecting nothing was accepted")
	}
}

func TestAShardTakenNothingFromIsNotAMember(t *testing.T) {
	s := edu()
	s.Members[1].Documents = 0
	if err := s.check(); err == nil {
		t.Error("a member the slice takes nothing out of was accepted")
	}
}

func TestASliceMissingWhatMakesItCheckableIsRejected(t *testing.T) {
	for name, spoil := range map[string]func(*Slice){
		"no name":           func(s *Slice) { s.Name = "" },
		"no repo":           func(s *Slice) { s.Dataset = "" },
		"no parent":         func(s *Slice) { s.Parent = "" },
		"no pin":            func(s *Slice) { s.ParentDigest = doc.Hash{} },
		"no predicate":      func(s *Slice) { s.Rule.Where = "  " },
		"no membership":     func(s *Slice) { s.Members[0].Docs = doc.Hash{} },
		"no shard hash":     func(s *Slice) { s.Members[0].Hash = doc.Hash{} },
		"the same shard":    func(s *Slice) { s.Members[1].Shard = s.Members[0].Shard },
		"no classes":        func(s *Slice) { s.Classes = nil },
		"an unknown class":  func(s *Slice) { s.Classes = []doc.LicenseClass{doc.LicenseUnknown} },
		"no tokenizer":      func(s *Slice) { s.Tokenizer = "" },
		"a local timestamp": func(s *Slice) { s.CreatedAt = time.Date(2026, 8, 2, 0, 0, 0, 0, time.FixedZone("ICT", 7*3600)) },
	} {
		s := edu()
		spoil(s)
		if err := s.check(); err == nil {
			t.Errorf("a slice with %s was accepted", name)
		}
	}
}

func TestTheQueryIsTheParentsParquetWithThePredicateOnIt(t *testing.T) {
	d, ok := store.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the repo is not in the table")
	}
	q := edu().Query(d)
	for _, want := range []string{"/gao-v1.0/", "quality >= 0.8", d.Repo()} {
		if !strings.Contains(q, want) {
			t.Errorf("the query does not carry %s:\n%s", want, q)
		}
	}
	if strings.Contains(q, "gao-edu") {
		t.Errorf("the query reads from a repo of the slice's own, which is the copy this exists to avoid:\n%s", q)
	}
}

func TestSlicesOverlapAndTheOverlapIsCounted(t *testing.T) {
	// Two slices summing to more than the corpus is normal and a reader adding
	// them up will get a number larger than what was published, so the release
	// note has to be able to say how much they share.
	legal := edu()
	legal.Name = "gao-legal"
	legal.Members = []Member{legal.Members[0]}
	legal.Members[0].Documents = 2

	shards, documents := Overlap(edu(), legal)
	if shards != 1 {
		t.Errorf("the two slices share %d shards, want 1", shards)
	}
	if documents != 2 {
		t.Errorf("the two slices share at most %d documents, want 2", documents)
	}
}

func TestASliceRoundTripsThroughItsFile(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, edu()); err != nil {
		t.Fatal(err)
	}
	back, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if back.Digest() != edu().Digest() {
		t.Error("the slice does not survive its own file")
	}
	if faults := back.Against(parent()); len(faults) > 0 {
		t.Errorf("the slice read back does not check against its parent: %v", faults)
	}
}

func TestASliceFileIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, edu()); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, edu()); err == nil {
		t.Error("a published slice was overwritten in place")
	}
}

func TestASliceThatCannotBeCheckedIsNotWritten(t *testing.T) {
	s := edu()
	s.ParentDigest = doc.Hash{}
	dir := t.TempDir()
	if err := Write(dir, s); err == nil {
		t.Error("a slice pinned to nothing was written out")
	}
	if _, err := Read(dir); err == nil {
		t.Errorf("%s exists after the write was refused", filepath.Join(dir, SliceName))
	}
}

func TestTheFaultsComeOutInTheSameOrderEveryTime(t *testing.T) {
	p := parent()
	p.Shards[0].Hash = id("moved")
	s := edu()
	s.Members[1].Documents = 99

	first := s.Against(p)
	for range 8 {
		got := s.Against(p)
		if len(got) != len(first) {
			t.Fatalf("the fault count moved between runs: %v\n%v", first, got)
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("the faults came out in a different order:\n%v\n%v", first, got)
			}
		}
	}
}
