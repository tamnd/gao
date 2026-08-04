package xay

import (
	"math"
	"testing"
)

// The permutations are part of the corpus format. A document fingerprinted last
// year and one fingerprinted today are compared against each other, so a change
// to the generator is a change that invalidates every signature ever written,
// and it has to be loud rather than silent.
func TestThePermutationsNeverChange(t *testing.T) {
	got := Sign("Hà Nội là thủ đô của Việt Nam.")
	want := [4]uint64{2047406967508478322, 691093914351096153, 1496014748090093526, 1018481956877778400}
	for i, v := range [4]uint64{got[0], got[1], got[2], got[127]} {
		if v != want[i] {
			t.Fatalf("the signature has moved: got %v, want %v.\nEvery signature in the store was taken with the old permutations and cannot be compared with a new one.", [4]uint64{got[0], got[1], got[2], got[127]}, want)
		}
	}
}

func TestTheSameTextSignsTheSameWay(t *testing.T) {
	first, second := Sign(article), Sign(article)
	if first != second {
		t.Error("signing one document twice gave two signatures")
	}
	if got := Sign(article).Similarity(Sign(article)); got != 1 {
		t.Errorf("a document is %v similar to itself, want 1", got)
	}
}

// Everything the key drops has to be invisible here as well, or the stage that
// exists to find republished text is defeated by a content management system
// changing the quotes.
func TestWhatTheKeyDropsTheSignatureCannotSee(t *testing.T) {
	if Sign(article) != Sign(retyped) {
		t.Error("the same article under different capitals and punctuation signed differently")
	}
}

// The signature exists to estimate the Jaccard similarity of two shingle sets
// without holding either of them. Over 128 permutations the standard error is
// about 0.09, so a tenth is the width to hold it to.
func TestTheSignatureEstimatesTheRealSimilarity(t *testing.T) {
	for _, tc := range []struct{ name, a, b string }{
		{"a syndicated copy", article, syndicated},
		{"a corrected copy", article, corrected},
		{"a quotation", article, quoted},
		{"two unrelated documents", article, river},
		{"two other unrelated documents", river, reform},
	} {
		want := jaccard(tc.a, tc.b)
		got := Sign(tc.a).Similarity(Sign(tc.b))
		if math.Abs(got-want) > 0.1 {
			t.Errorf("%s: the signature says %.3f and the real similarity is %.3f", tc.name, got, want)
		}
	}
}

// The two populations the threshold has to separate. A republished article is a
// copy of the document. A forum post that quotes two sentences of it is not, and
// a stage that could not tell them apart would delete the answer along with the
// question.
func TestARepublishedArticleIsACopyAndAQuotationIsNot(t *testing.T) {
	a := Sign(article)
	knee := Default().Knee()

	for _, tc := range []struct{ name, text string }{
		{"a second site's copy", syndicated},
		{"a corrected copy", corrected},
	} {
		if got := a.Similarity(Sign(tc.text)); got < knee {
			t.Errorf("%s is %.3f similar to the article, want at least the knee at %.3f", tc.name, got, knee)
		}
	}
	for _, tc := range []struct{ name, text string }{
		{"a forum post quoting it", quoted},
		{"an article about something else", river},
	} {
		if got := a.Similarity(Sign(tc.text)); got >= knee {
			t.Errorf("%s is %.3f similar to the article, want it under the knee at %.3f", tc.name, got, knee)
		}
	}
}

// An empty document is a real document with a real signature. It is the ingest
// contract's business whether it should exist, and this package must not report
// it as an unset fingerprint.
func TestAnEmptyDocumentStillSigns(t *testing.T) {
	if Sign("").IsZero() {
		t.Error("an empty document signed as the zero signature")
	}
	if got := Sign("").Similarity(Sign("")); got != 1 {
		t.Errorf("two empty documents are %v similar, want 1", got)
	}
}
