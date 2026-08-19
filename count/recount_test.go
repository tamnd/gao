package count

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/store"
)

// stored is what a run of texts comes to when it is counted honestly, which is
// what the columns in the hub should add up to.
func stored(texts []string) Counts {
	c := Counts{Documents: int64(len(texts))}
	for _, text := range texts {
		c.Bytes += int64(len(text))
		c.Chars += int64(doc.Chars(text))
		c.Syllables += int64(doc.Syllables(text))
	}
	return c
}

// The whole claim of level one: the corpus adds up out of its own columns, and
// the text stays where it is.
func TestTheCorpusRecountsOutOfItsColumns(t *testing.T) {
	s := newStore(t)
	written := texts(0, 80)
	s.put(snapshot, 0, 0, written[:40]...)
	s.put(snapshot, 1, 0, written[40:]...)

	got, err := RecountOf(t.Context(), s.client(), snapshot, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("RecountOf: %v", err)
	}
	want := stored(written)
	if got.Documents != want.Documents {
		t.Errorf("recounted %d documents, want %d", got.Documents, want.Documents)
	}
	if got.Chars != want.Chars {
		t.Errorf("recounted %d characters, want %d", got.Chars, want.Chars)
	}
	if got.Syllables != want.Syllables {
		t.Errorf("recounted %d syllables, want %d", got.Syllables, want.Syllables)
	}
}

// The byte length of the text is the one published unit with no column behind
// it, so a recount has to come back saying it does not know rather than saying
// zero and letting a report print a corpus of no size.
func TestTheRecountDoesNotClaimAByteCountItCannotHave(t *testing.T) {
	s := newStore(t)
	written := texts(0, 20)
	s.put(snapshot, 0, 0, written...)

	got, err := RecountOf(t.Context(), s.client(), snapshot, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("RecountOf: %v", err)
	}
	if got.Bytes != 0 {
		t.Errorf("the recount reports %d bytes of text, and no column holds that", got.Bytes)
	}
	if stored(written).Bytes == 0 {
		t.Fatal("the fixture has no text in it, so this proves nothing")
	}
}

// The point of the whole approach. If summing the shape columns pulled the text
// along with it, this would be a download of the corpus with extra steps.
func TestRecountingDoesNotMoveTheText(t *testing.T) {
	s := newStore(t)
	written := texts(0, 200)
	s.put(snapshot, 0, 0, written...)

	var text int64
	for _, w := range written {
		text += int64(len(w))
	}
	if _, err := RecountOf(t.Context(), s.client(), snapshot, t.TempDir(), nil); err != nil {
		t.Fatalf("RecountOf: %v", err)
	}

	s.mu.Lock()
	served := s.served
	s.mu.Unlock()
	if served >= text {
		t.Errorf("the recount moved %d bytes to measure %d bytes of text, which is a download", served, text)
	}
}

// A pass over a thousand parts will be interrupted, and the parts it already read
// are the expensive part of what it did.
func TestARecountResumesAtThePart(t *testing.T) {
	s := newStore(t)
	written := texts(0, 60)
	s.put(snapshot, 0, 0, written[:30]...)
	s.put(snapshot, 1, 0, written[30:]...)

	work := t.TempDir()
	first, err := RecountOf(t.Context(), s.client(), snapshot, work, nil)
	if err != nil {
		t.Fatalf("RecountOf: %v", err)
	}

	s.mu.Lock()
	s.ranged = 0
	s.mu.Unlock()

	second, err := RecountOf(t.Context(), s.client(), snapshot, work, nil)
	if err != nil {
		t.Fatalf("RecountOf again: %v", err)
	}
	if first != second {
		t.Errorf("the second pass got %+v and the first got %+v", second, first)
	}

	s.mu.Lock()
	ranged := s.ranged
	s.mu.Unlock()
	if ranged != 0 {
		t.Errorf("the second pass made %d requests, and every part it needed was already recorded", ranged)
	}
}

// The counts a fully resumed pass reports are correct and it did not check them,
// and those two facts have to reach the caller together. They did not, and a
// parquet-go bump was very nearly landed on the strength of a verify run that
// printed matching columns next to zero bytes read.
func TestARecountSaysWhichPartsItTookOffTheLog(t *testing.T) {
	s := newStore(t)
	written := texts(0, 60)
	s.put(snapshot, 0, 0, written[:30]...)
	s.put(snapshot, 1, 0, written[30:]...)

	work := t.TempDir()
	var fresh, held int
	note := func(_ store.Stored, _, _ int, _ Counts, _ int64, resumed bool) {
		if resumed {
			held++
			return
		}
		fresh++
	}
	if _, err := RecountOf(t.Context(), s.client(), snapshot, work, note); err != nil {
		t.Fatalf("RecountOf: %v", err)
	}
	if fresh != 2 || held != 0 {
		t.Errorf("the first pass read %d parts and resumed %d, and there was nothing to resume from", fresh, held)
	}

	fresh, held = 0, 0
	if _, err := RecountOf(t.Context(), s.client(), snapshot, work, note); err != nil {
		t.Fatalf("RecountOf again: %v", err)
	}
	if fresh != 0 || held != 2 {
		t.Errorf("the second pass read %d parts and resumed %d, and every part was already in the log", fresh, held)
	}
}

// A run killed mid-write leaves a last line with no newline on it. That line is
// a number that was not finished, and a number that was not finished parses.
func TestARecountThrowsAwayTheLineItWasWritingWhenItDied(t *testing.T) {
	s := newStore(t)
	written := texts(0, 30)
	s.put(snapshot, 0, 0, written...)

	work := t.TempDir()
	want, err := RecountOf(t.Context(), s.client(), snapshot, work, nil)
	if err != nil {
		t.Fatalf("RecountOf: %v", err)
	}

	path := filepath.Join(work, snapshot+ShapesExt)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	torn := append(append([]byte{}, b...), []byte(`{"part":"parts/f00001-p00`)...)
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := RecountOf(t.Context(), s.client(), snapshot, work, nil)
	if err != nil {
		t.Fatalf("RecountOf over a torn resume file: %v", err)
	}
	if got != want {
		t.Errorf("a torn tail changed the answer from %+v to %+v", want, got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(b) {
		t.Errorf("the unfinished line is still in the file:\n%s", after)
	}
}

// A whole line that does not parse is not a torn tail, it is somebody else's
// file, and appending to it would make two problems out of one.
func TestARecountRefusesAResumeFileItDidNotWrite(t *testing.T) {
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 10)...)

	work := t.TempDir()
	path := filepath.Join(work, snapshot+ShapesExt)
	if err := os.WriteFile(path, []byte("this is not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := RecountOf(t.Context(), s.client(), snapshot, work, nil)
	if err == nil {
		t.Fatal("a resume file gao did not write was appended to")
	}
	if !strings.Contains(err.Error(), "resume file") {
		t.Errorf("the error does not say what the file is: %v", err)
	}
}

func TestARecountOfASnapshotThatIsNotThereSaysSo(t *testing.T) {
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 10)...)

	_, err := RecountOf(t.Context(), s.client(), "hplt-v3-0000000000", t.TempDir(), nil)
	if err == nil {
		t.Fatal("recounting a snapshot the hub does not hold succeeded")
	}
}

func TestAReportThatMatchesTheStoreAgrees(t *testing.T) {
	written := texts(0, 40)
	claimed := Report{Sources: []SourceCounts{{Source: doc.SourceGlotCC, Counts: onlyColumns(stored(written))}}}
	diffs := Compare(claimed, map[doc.Source]Counts{doc.SourceGlotCC: onlyColumns(stored(written))})
	if !Agree(diffs) {
		t.Errorf("a report that matches the hub was reported as a difference: %+v", diffs)
	}
}

// The failure this is for: a report written from a run that stopped early, which
// is a real number about less corpus than there is.
func TestAReportShortOfTheStoreIsCaughtAndTheColumnIsNamed(t *testing.T) {
	full := onlyColumns(stored(texts(0, 40)))
	short := onlyColumns(stored(texts(0, 30)))

	claimed := Report{Sources: []SourceCounts{{Source: doc.SourceGlotCC, Counts: short}}}
	diffs := Compare(claimed, map[doc.Source]Counts{doc.SourceGlotCC: full})
	if Agree(diffs) {
		t.Fatal("a report ten documents short of the hub agreed with it")
	}
	off := diffs[0].Off()
	for _, want := range []string{"documents", "chars", "syllables"} {
		if !contains(off, want) {
			t.Errorf("the difference does not name %s: %v", want, off)
		}
	}
}

// Bytes is not compared because no column holds it, and a check that quietly
// treated an uncompared column as one that matched would be the appearance of a
// check rather than a check.
func TestTheByteCountIsNotWhatMakesTheReportAgree(t *testing.T) {
	c := onlyColumns(stored(texts(0, 20)))
	claimed := c
	claimed.Bytes = 999999

	diffs := Compare(
		Report{Sources: []SourceCounts{{Source: doc.SourceGlotCC, Counts: claimed}}},
		map[doc.Source]Counts{doc.SourceGlotCC: c},
	)
	if !Agree(diffs) {
		t.Error("a byte count nothing read decided the verdict")
	}
}

// A source the hub holds and the report never mentions is the difference worth
// catching most, and an absence is not something a table shows.
func TestASourceMissingFromTheReportIsALineRatherThanAnAbsence(t *testing.T) {
	diffs := Compare(
		Report{Sources: []SourceCounts{{Source: doc.SourceGlotCC, Counts: Counts{Documents: 10}}}},
		map[doc.Source]Counts{
			doc.SourceGlotCC:   {Documents: 10},
			doc.SourceFineWeb2: {Documents: 7},
		},
	)
	if len(diffs) != 2 {
		t.Fatalf("Compare returned %d lines for two sources", len(diffs))
	}
	if Agree(diffs) {
		t.Error("a source the report never mentions agreed with the report")
	}
	for _, d := range diffs {
		if d.Source == doc.SourceFineWeb2 && d.Claimed.Documents != 0 {
			t.Errorf("%s is claimed at %d documents by a report that does not name it", d.Source, d.Claimed.Documents)
		}
	}
}

// onlyColumns is the counts as the hub can know them, which is everything but
// the byte length of the text.
func onlyColumns(c Counts) Counts {
	c.Bytes = 0
	return c
}

func contains(s []string, want string) bool {
	for _, got := range s {
		if got == want {
			return true
		}
	}
	return false
}
