package count

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTwoSourcesReportWhatTheyHaveInCommon(t *testing.T) {
	a := keyed(t, "glotcc", "một", "hai", "ba", "bốn")
	b := keyed(t, "fineweb2", "ba", "bốn", "năm")

	got, err := Measure(a, b)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if got.Distinct != 5 {
		t.Errorf("four and three documents overlapping in two came to %d distinct, want 5", got.Distinct)
	}
	if got.Documents != 7 {
		t.Errorf("the pass says %d documents were read, want 7", got.Documents)
	}
	if n := got.Both("glotcc", "fineweb2"); n != 2 {
		t.Errorf("the two sources have %d documents in common, want 2", n)
	}
	if len(got.Pairs) != 1 {
		t.Fatalf("two sources came out as %d pairs, want 1", len(got.Pairs))
	}
	if got.Pairs[0].A != "glotcc" || got.Pairs[0].B != "fineweb2" {
		t.Errorf("the pair is %s and %s, want them in the order the files were given", got.Pairs[0].A, got.Pairs[0].B)
	}
}

// Half of a small source being inside a large one and half of a large one being
// inside a small one are different facts, and one number cannot carry both.
func TestOverlapIsReportedFromEachSidesPointOfView(t *testing.T) {
	small := keyed(t, "small", "một", "hai")
	large := keyed(t, "large", "một", "hai", "ba", "bốn", "năm", "sáu", "bảy", "tám")

	got, err := Measure(small, large)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if want := 1.0; got.Share("small", "large") != want {
		t.Errorf("all of the small source is in the large one and Share says %v", got.Share("small", "large"))
	}
	if want := 0.25; got.Share("large", "small") != want {
		t.Errorf("a quarter of the large source is in the small one and Share says %v", got.Share("large", "small"))
	}
}

// A document three sources hold is in three pairs and in the union once. This is
// the case a pass that measured each pair on its own would still get right, and
// the case a pass that stopped at the first source holding a document would not.
func TestADocumentInThreeSourcesIsInEveryPairAndInTheUnionOnce(t *testing.T) {
	a := keyed(t, "a", "chung", "riêng a")
	b := keyed(t, "b", "chung", "riêng b")
	c := keyed(t, "c", "chung", "riêng c")

	got, err := Measure(a, b, c)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if got.Distinct != 4 {
		t.Errorf("three sources sharing one document came to %d distinct, want 4", got.Distinct)
	}
	if len(got.Pairs) != 3 {
		t.Fatalf("three sources came out as %d pairs, want 3", len(got.Pairs))
	}
	for _, p := range got.Pairs {
		if p.Both != 1 {
			t.Errorf("%s and %s have %d in common, want 1", p.A, p.B, p.Both)
		}
	}
	for _, s := range got.Sources {
		if s.Only != 1 {
			t.Errorf("%s contributes %d documents nothing else has, want 1", s.Name, s.Only)
		}
	}
}

// What a source is worth is what it has that nothing else does, and that is not
// what any pair says. A source can overlap every other source heavily and still
// be the only place a tenth of the corpus comes from.
func TestASourceReportsWhatOnlyItHas(t *testing.T) {
	a := keyed(t, "a", "một", "hai", "ba")
	b := keyed(t, "b", "một", "hai", "ba")

	got, err := Measure(a, b)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	for _, s := range got.Sources {
		if s.Only != 0 {
			t.Errorf("%s is a copy of the other source and reports %d of its own", s.Name, s.Only)
		}
	}
	if got.Distinct != 3 {
		t.Errorf("two copies of the same three documents came to %d distinct, want 3", got.Distinct)
	}
	if want := 0.5; got.Duplication() != want {
		t.Errorf("Duplication = %v, want %v", got.Duplication(), want)
	}
}

// A source repeating itself and two sources carrying the same document are
// different things, and the pass has to keep them apart: the first is a fact
// about that source and the second is a fact about the pair.
func TestASourceRepeatingItselfIsNotOverlapWithAnythingElse(t *testing.T) {
	a := keyed(t, "a", "một", "một", "một", "hai")
	b := keyed(t, "b", "ba")

	got, err := Measure(a, b)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if n := got.Both("a", "b"); n != 0 {
		t.Errorf("the sources share nothing and the pass says %d", n)
	}
	if want := 0.5; got.Sources[0].Duplication() != want {
		t.Errorf("the first source duplicates %v of itself, want %v", got.Sources[0].Duplication(), want)
	}
	if got.Distinct != 3 {
		t.Errorf("the union came to %d, want 3", got.Distinct)
	}
	if got.Documents != 5 {
		t.Errorf("the pass read %d documents, want 5", got.Documents)
	}
}

func TestOneSourceIsMeasuredAgainstNothing(t *testing.T) {
	got, err := Measure(keyed(t, "only", "một", "hai", "một"))
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if len(got.Pairs) != 0 {
		t.Errorf("one source came out as %d pairs", len(got.Pairs))
	}
	if got.Distinct != 2 || got.Sources[0].Only != 2 {
		t.Errorf("one source of two distinct documents came out as %+v", got)
	}
	if n := got.Both("only", "only"); n != 2 {
		t.Errorf("a source has %d in common with itself, want 2", n)
	}
}

// A disk that filled up while a key file was being written leaves a file that
// opens, sorts and merges. The overlap it produces is smaller than the truth and
// there is nothing about it that looks wrong, so the header is checked against
// what came out.
func TestAKeyFileThatLostItsTailIsRefused(t *testing.T) {
	path := keyed(t, "short", "một", "hai", "ba")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, st.Size()-8); err != nil {
		t.Fatal(err)
	}

	_, err = Measure(path, keyed(t, "other", "bốn"))
	if err == nil {
		t.Fatal("a truncated key file measured without complaint")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("Measure returned %v, which does not say the file is short", err)
	}
}

// Two sources called the same thing would make every answer about them
// ambiguous, and the answers are published.
func TestTwoKeyFilesWithTheSameNameAreRefused(t *testing.T) {
	if _, err := Measure(keyed(t, "s", "một"), keyed(t, "s", "hai")); err == nil {
		t.Fatal("two key files with the same name measured without complaint")
	}
}

func TestMeasuringNothingIsAnErrorRatherThanAnEmptyAnswer(t *testing.T) {
	if _, err := Measure(); err == nil {
		t.Fatal("measuring no key files produced an answer")
	}
}

func TestASourceIsNamedAfterItsKeyFile(t *testing.T) {
	if got := SourceName(filepath.Join("/tmp", "work", "glotcc-1a2b3c"+KeysExt)); got != "glotcc-1a2b3c" {
		t.Errorf("SourceName = %q, want %q", got, "glotcc-1a2b3c")
	}
}

// Nothing in common is as much of a result as anything else, and it has to come
// out as a zero rather than as a missing pair.
func TestSourcesWithNothingInCommonSaySo(t *testing.T) {
	got, err := Measure(keyed(t, "a", "một"), keyed(t, "b", "hai"))
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if len(got.Pairs) != 1 || got.Pairs[0].Both != 0 {
		t.Errorf("two sources with nothing in common came out as %+v", got.Pairs)
	}
	if got.Distinct != 2 || got.Duplication() != 0 {
		t.Errorf("two sources with nothing in common came out as %+v", got)
	}
}
