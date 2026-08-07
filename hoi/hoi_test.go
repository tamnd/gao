package hoi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// question is one item that came back the way a good one comes back: a document
// long enough to be one, evidence in two places far apart, asked closed book
// first, and read by two people who agreed.
func question(n int, tokens int, kind string) Question {
	return Question{
		ID:       fmt.Sprintf("vi-longdoc-%04d", n),
		Document: fmt.Sprintf("vbpl-%d-%03d", 2004+n%20, n%97),
		Kind:     kind,
		Tokens:   tokens,
		Spans: []Span{
			{Start: tokens / 12, End: tokens/12 + 180},
			{Start: tokens * 8 / 10, End: tokens*8/10 + 240},
		},
		ClosedBook: true,
		Graders:    2,
		Agreed:     2,
	}
}

// composed builds a set that fills all three rungs and leans on no document.
func composed(n int) Set {
	s := Set{Name: "vi-longdoc-qa-1.0"}
	lengths := []int{38_000, 44_000, 71_000, 88_000, 140_000, 210_000}
	for i := range n {
		s.Questions = append(s.Questions, question(i, lengths[i%len(lengths)], Kinds[i%len(Kinds)]))
	}
	return s
}

func refuses(t *testing.T, s Set, want string) {
	t.Helper()
	why := s.Blocking()
	if len(why) == 0 {
		t.Fatalf("the set was accepted and it should have been refused for %q", want)
	}
	for _, w := range why {
		if strings.Contains(w, want) {
			return
		}
	}
	t.Errorf("no refusal mentions %q, and what came back was:\n  %s", want, strings.Join(why, "\n  "))
}

func rejects(t *testing.T, q Question, want string) {
	t.Helper()
	why := q.Blocking()
	if len(why) == 0 {
		t.Fatalf("the question was admitted and it should have been rejected for %q", want)
	}
	for _, w := range why {
		if strings.Contains(w, want) {
			return
		}
	}
	t.Errorf("no rejection mentions %q, and what came back was:\n  %s", want, strings.Join(why, "\n  "))
}

func TestASetThatFillsTheLadderSaysSoInOneSentence(t *testing.T) {
	s := composed(600)
	if !s.Settled() {
		t.Fatalf("a composed set was refused: %v", s.Blocking())
	}
	if !s.Holds() {
		t.Fatalf("a composed set does not hold: %s", s.Verdict())
	}
	if len(s.In()) != 600 {
		t.Errorf("%d of 600 questions were admitted", len(s.In()))
	}
	if v := s.Verdict(); !strings.Contains(v, "Every rung of the context ladder is filled") {
		t.Errorf("the verdict does not say the ladder is filled:\n  %s", v)
	}
}

func TestAQuestionAnsweredWithoutTheDocumentIsAMemoryQuestion(t *testing.T) {
	q := question(1, 88_000, Synthesis)
	q.Recalled = true
	rejects(t, q, "memory question rather than a reading one")

	// The check being skipped is worse than the check failing, since a set full
	// of unasked questions looks exactly like a set full of good ones.
	unasked := question(2, 88_000, Synthesis)
	unasked.ClosedBook = false
	rejects(t, unasked, "never put to a model without the document")
}

func TestAQuestionWithOneSpanIsRetrievalAndNeedleAlreadyMeasuresThat(t *testing.T) {
	q := question(3, 88_000, Comparison)
	q.Spans = q.Spans[:1]
	rejects(t, q, "one span is retrieval")
	if q.Reach() != 0 {
		t.Errorf("a one span question reaches %.2f of its document", q.Reach())
	}
}

func TestEvidenceBunchedAtTheFrontIsAShortQuestionWithPaddingAfterIt(t *testing.T) {
	q := question(4, 120_000, Sequence)
	q.Spans = []Span{{Start: 400, End: 700}, {Start: 3_100, End: 3_600}}
	rejects(t, q, "a model that reads the opening answers it")
	if r := q.Reach(); r > 0.05 {
		t.Errorf("evidence inside the first four thousand tokens of a 120,000 token document reaches %.2f", r)
	}
}

func TestADocumentShorterThanTheFirstRungIsNotALongDocument(t *testing.T) {
	q := question(5, 12_000, Amendment)
	rejects(t, q, "so it is a reading comprehension question")
	if q.Rung() != 0 {
		t.Errorf("a 12,000 token document sits on the %d rung", q.Rung())
	}
}

func TestAQuestionTwoPeopleAnswerDifferentlyIsAQuestion(t *testing.T) {
	split := question(6, 88_000, Counting)
	split.Graders, split.Agreed = 3, 2
	rejects(t, split, "so it is a question rather than a test item")

	alone := question(7, 88_000, Counting)
	alone.Graders, alone.Agreed = 1, 1
	rejects(t, alone, "two is the fewest that can disagree")
	if alone.Settled() {
		t.Error("a question one person read is settled")
	}
}

func TestASpanOutsideTheDocumentIsNotEvidence(t *testing.T) {
	q := question(8, 44_000, Synthesis)
	q.Spans[1].End = 60_000
	rejects(t, q, "of a document that is 44000 long")

	empty := question(9, 44_000, Synthesis)
	empty.Spans[0].End = empty.Spans[0].Start
	rejects(t, empty, "cites tokens")
}

func TestASetLeaningOnOneDocumentMeasuresThatDocument(t *testing.T) {
	s := composed(600)
	for i := range 120 {
		s.Questions[i].Document = "luat-dat-dai-2024"
	}
	refuses(t, s, "so what this measures is that document")
	if doc, share := s.Heaviest(); doc != "luat-dat-dai-2024" || share < 0.19 {
		t.Errorf("the heaviest document is %s at %.2f of the set", doc, share)
	}
}

func TestALadderWithAHoleInItCannotSayWhetherTheExtensionWorked(t *testing.T) {
	// Every document between 38,000 and 90,000 tokens, which is a perfectly
	// reasonable set to end up with and says nothing about 131k.
	s := Set{Name: "vi-longdoc-qa-1.0"}
	lengths := []int{38_000, 44_000, 71_000, 88_000}
	for i := range 600 {
		s.Questions = append(s.Questions, question(i, lengths[i%len(lengths)], Kinds[i%len(Kinds)]))
	}
	if !s.Settled() {
		t.Fatalf("a set with a thin rung was refused rather than reported: %v", s.Blocking())
	}
	if s.Holds() {
		t.Fatal("a set with nothing above 88,000 tokens holds")
	}
	if thin := s.Thin(); len(thin) != 1 || !strings.Contains(thin[0], "131,072") {
		t.Errorf("the thin rung is not named: %v", thin)
	}
	if v := s.Verdict(); !strings.Contains(v, "cannot say whether the extension to that length worked") {
		t.Errorf("the verdict does not say what the hole costs:\n  %s", v)
	}
}

func TestTheLadderAndTheKindsAddUpToTheAdmittedSet(t *testing.T) {
	s := composed(600)
	var rungs, kinds int
	for _, row := range s.Ladder() {
		rungs += row.Questions
		if row.Reach < MinReach {
			t.Errorf("rung %s has a mean reach of %.2f", row.Name, row.Reach)
		}
		if row.Spans < MinSpans {
			t.Errorf("rung %s averages %.1f spans", row.Name, row.Spans)
		}
	}
	for _, row := range s.Composition() {
		kinds += row.Questions
		if !row.Holds {
			t.Errorf("the set holds nothing of kind %s", row.Name)
		}
	}
	if rungs != len(s.In()) || kinds != len(s.In()) {
		t.Errorf("the ladder holds %d and the kinds hold %d of %d admitted questions", rungs, kinds, len(s.In()))
	}
}

func TestTheQuestionsThatDidNotSurviveAreCountedRatherThanDeleted(t *testing.T) {
	s := composed(640)
	for i := range 40 {
		s.Questions[i*16].Recalled = true
	}
	if len(s.Out()) != 40 {
		t.Fatalf("%d questions were rejected and forty should have been", len(s.Out()))
	}
	if s.Recalled() != 40 {
		t.Errorf("%d questions read as answered with no document", s.Recalled())
	}
	if !s.Settled() {
		t.Fatalf("a set with forty rejects was refused: %v", s.Blocking())
	}
	if v := s.Verdict(); !strings.Contains(v, "the check most sets of this kind skip") {
		t.Errorf("the verdict does not report the closed book run:\n  %s", v)
	}
}

func TestASetOfDraftsIsNotABenchmark(t *testing.T) {
	var s Set
	refuses(t, s, "no questions were read")
	if s.Holds() || len(s.In()) != 0 {
		t.Error("an empty set reported on a benchmark")
	}
	if s.Verdict() != s.Blocking()[0] {
		t.Error("the verdict does not lead with the reason there is nothing to report")
	}

	drafts := composed(40)
	for i := range drafts.Questions {
		drafts.Questions[i].ClosedBook = false
	}
	refuses(t, drafts, "the set is a list of drafts")

	refuses(t, composed(200), "against the 600 questions it was composed to")
}

func TestReadingASetOffDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vi-longdoc-qa.jsonl")
	lines := make([]string, 0, 12)
	for i := range 12 {
		lines = append(lines, fmt.Sprintf(
			`{"id":"vi-longdoc-%04d","document":"vbpl-2016-%03d","kind":"tong-hop","tokens":96000,"spans":[{"start":2400,"end":2600},{"start":78000,"end":78300}],"closed_book":true,"graders":2,"agreed":2}`,
			i, i))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSet("vi-longdoc-qa-1.0", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.In()) != 12 {
		t.Fatalf("%d of 12 questions were admitted: %v", len(s.In()), s.Out())
	}
	if s.Documents() != 12 {
		t.Errorf("%d documents came off twelve questions", s.Documents())
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"id":"x","answer":"forty"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSet("x", bad); err == nil {
		t.Error("a question carrying an undeclared column was read")
	}
	if _, err := ReadSet("x", filepath.Join(dir, "nothing.jsonl")); err == nil {
		t.Error("a file that is not there read as a set")
	}
}
