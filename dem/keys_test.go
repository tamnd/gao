package dem

import (
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tamnd/gao/doc"
)

// keyed builds a key file from the hashes of the given strings and returns its
// path, so that a test reads what a build would have written rather than what a
// test helper decided the format was.
func keyed(t *testing.T, name string, texts ...string) string {
	t.Helper()
	dir := t.TempDir()
	b := NewBuilder(dir)
	for _, s := range texts {
		if err := b.Add(doc.SumString(s)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	path := filepath.Join(dir, name+KeysExt)
	if _, err := b.Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return path
}

// read returns everything in a key file, in the order it comes out.
func read(t *testing.T, path string) (Keys, []Key) {
	t.Helper()
	r, err := OpenKeys(path)
	if err != nil {
		t.Fatalf("OpenKeys: %v", err)
	}
	defer func() { _ = r.Close() }()

	var keys []Key
	for {
		k, ok, err := r.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			return r.Keys(), keys
		}
		keys = append(keys, k)
	}
}

func TestAKeyFileComesOutSortedAndWithoutRepeats(t *testing.T) {
	path := keyed(t, "s", "một", "hai", "ba", "hai", "một")

	got, keys := read(t, path)
	if got.Documents != 5 {
		t.Errorf("the file says %d documents, want 5", got.Documents)
	}
	if got.Distinct != 3 {
		t.Errorf("the file says %d distinct, want 3", got.Distinct)
	}
	if int64(len(keys)) != got.Distinct {
		t.Errorf("the header says %d keys and the file holds %d", got.Distinct, len(keys))
	}
	if !slices.IsSorted(keys) {
		t.Error("the keys are not in order, so a merge over this file is wrong")
	}
}

// The gap between the two counts is a source repeating itself, which is a fact
// about that source rather than about its overlap with anything else.
func TestAKeyFileSaysHowMuchOfASourceIsARepeatOfItself(t *testing.T) {
	path := keyed(t, "s", "một", "một", "một", "hai")

	got, _ := read(t, path)
	if want := 0.5; got.Duplication() != want {
		t.Errorf("Duplication = %v, want %v", got.Duplication(), want)
	}
	if (Keys{}).Duplication() != 0 {
		t.Error("an empty source reports duplication rather than nothing")
	}
}

// This is the case the whole design is for. A source larger than memory spills
// runs and merges them, and the answer has to be the same one an in memory sort
// would have given.
func TestASourceLargerThanMemorySpillsAndComesOutTheSame(t *testing.T) {
	const n = 5000
	texts := make([]string, n)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range texts {
		// Repeats on purpose and out of order on purpose, since a merge that
		// only ever saw distinct keys arriving in order would pass while being
		// wrong about both.
		texts[i] = string(rune('a' + r.IntN(26)))
	}

	dir := t.TempDir()
	small := NewBuilder(dir)
	small.max = 64
	for _, s := range texts {
		if err := small.Add(doc.SumString(s)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	spilled := filepath.Join(dir, "spilled"+KeysExt)
	if _, err := small.Write(spilled); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if small.Runs() < 2 {
		t.Fatalf("the build spilled %d runs, so this test is not testing a merge", small.Runs())
	}

	whole := keyed(t, "whole", texts...)

	wantHeader, want := read(t, whole)
	gotHeader, got := read(t, spilled)
	if !slices.Equal(got, want) {
		t.Errorf("the merged file holds %d keys and the in memory one holds %d", len(got), len(want))
	}
	if gotHeader != wantHeader {
		t.Errorf("the merged header is %+v and the in memory one is %+v", gotHeader, wantHeader)
	}
	if gotHeader.Distinct != 26 {
		t.Errorf("twenty six distinct documents came out as %d", gotHeader.Distinct)
	}
}

func TestMergingKeyFilesDropsWhatTheyHaveInCommon(t *testing.T) {
	a := keyed(t, "a", "một", "hai", "ba")
	b := keyed(t, "b", "ba", "bốn")

	out := filepath.Join(t.TempDir(), "union"+KeysExt)
	got, err := MergeKeys(out, 5, a, b)
	if err != nil {
		t.Fatalf("MergeKeys: %v", err)
	}
	if got.Distinct != 4 {
		t.Errorf("the union of three and two overlapping in one came to %d, want 4", got.Distinct)
	}
	if got.Documents != 5 {
		t.Errorf("the union says %d documents were read, want 5", got.Documents)
	}
	header, keys := read(t, out)
	if header != got {
		t.Errorf("MergeKeys returned %+v and wrote %+v", got, header)
	}
	if !slices.IsSorted(keys) {
		t.Error("a merged file is not in order")
	}
}

// Zero is a key a document can have, and a merge that used a zero last key as
// its own initial state would drop it.
func TestTheZeroKeySurvivesAMerge(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one"+KeysExt)
	if err := writeKeys(one, 2, []Key{0, 7}); err != nil {
		t.Fatal(err)
	}
	two := filepath.Join(dir, "two"+KeysExt)
	if err := writeKeys(two, 1, []Key{9}); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "union"+KeysExt)
	if _, err := MergeKeys(out, 3, one, two); err != nil {
		t.Fatalf("MergeKeys: %v", err)
	}
	_, keys := read(t, out)
	if !slices.Equal(keys, []Key{0, 7, 9}) {
		t.Errorf("the merge came out as %v, want [0 7 9]", keys)
	}
}

func TestAnEmptySourceWritesAFileThatSaysSo(t *testing.T) {
	path := keyed(t, "empty")

	got, keys := read(t, path)
	if got.Documents != 0 || got.Distinct != 0 {
		t.Errorf("an empty build wrote %+v", got)
	}
	if len(keys) != 0 {
		t.Errorf("an empty build wrote %d keys", len(keys))
	}
}

// A merge fed the wrong file would answer rather than fail, and the answer would
// be a number somebody publishes.
func TestSomethingThatIsNotAKeyFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body string }{
		{"short", "gao"},
		{"wrong", "not a gao key file at all, but long enough to have a header"},
	} {
		path := filepath.Join(dir, tc.name)
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenKeys(path); !errors.Is(err, ErrNotKeys) {
			t.Errorf("OpenKeys(%s) returned %v, want ErrNotKeys", tc.name, err)
		}
	}
}

// The key is the front of the hash, and it has to stay the front of the hash:
// key files outlive the process that wrote them, and a build that shortened a
// hash differently from the one before it would report two copies of the same
// corpus as having nothing in common.
func TestAKeyIsTheFrontOfTheHash(t *testing.T) {
	h := doc.SumString("một tài liệu")
	if got, want := KeyOf(h), uint64(0); got == want {
		t.Skip("this document hashes to zero, which is not what is being tested")
	}
	var want uint64
	for _, b := range h[:8] {
		want = want<<8 | uint64(b)
	}
	if got := KeyOf(h); got != want {
		t.Errorf("KeyOf = %d, want %d", got, want)
	}
}
