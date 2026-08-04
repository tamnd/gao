package xay

import (
	"testing"

	"github.com/tamnd/gao/doc"
)

func index(t *testing.T, b Banding, texts ...string) *Index {
	t.Helper()
	x, err := New(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range texts {
		x.AddText(s)
	}
	return x
}

func TestABandingThatDoesNotCoverTheSignatureIsRefused(t *testing.T) {
	if _, err := New(Banding{Bands: 16, Rows: 7}); err == nil {
		t.Error("an index was built on a banding that ignores half of every signature")
	}
}

func TestAnEmptyIndexReportsNothing(t *testing.T) {
	x := index(t, Default())
	got := x.Cluster(0.7)
	if got.Documents != 0 || got.Kept != 0 || got.Clusters != 0 {
		t.Errorf("an empty index reports %+v", got)
	}
	if got.Retention() != 0 {
		t.Errorf("an empty index retains %v, want 0", got.Retention())
	}
}

// The two kinds of duplicate are counted apart from each other, because they are
// two different facts about a corpus. Exact copies are a scraper that ran twice.
// Near copies are the web republishing itself, and they are the ones a threshold
// is tuned against.
func TestExactCopiesAreCountedApartFromNearOnes(t *testing.T) {
	x := index(t, Default(), article, article, article, syndicated, river)

	got := x.Cluster(0.7)
	if got.Documents != 5 {
		t.Errorf("counted %d documents, want 5", got.Documents)
	}
	if got.Exact != 2 {
		t.Errorf("counted %d exact copies, want 2", got.Exact)
	}
	if got.Near != 1 {
		t.Errorf("counted %d near copies, want 1", got.Near)
	}
	if got.Kept != 2 {
		t.Errorf("kept %d documents, want 2, the article and the one about the river", got.Kept)
	}
	if got.Clusters != 1 {
		t.Errorf("found %d clusters of more than one document, want 1", got.Clusters)
	}
	if got.Largest != 4 {
		t.Errorf("the largest cluster holds %d documents, want 4", got.Largest)
	}
	if want := 2.0 / 5.0; got.Retention() != want {
		t.Errorf("retention is %v, want %v", got.Retention(), want)
	}
}

// The case the stage exists for: one article, four sites, four documents that a
// reader would call one.
func TestARepublishedArticleIsOneDocument(t *testing.T) {
	x := index(t, Default(), article, syndicated, corrected, retyped)
	if got := x.Cluster(0.7); got.Kept != 1 {
		t.Errorf("four copies of one article came to %d documents, want 1:\n%s", got.Kept, got)
	}
}

// The case a threshold set too low destroys. A forum post that quotes two
// sentences of an article shares real text with it and is a different document,
// and a corpus that dropped it would be dropping the answer and keeping the
// question.
func TestAQuotationIsNotACopy(t *testing.T) {
	x := index(t, Default(), article, quoted, river, reform)
	got := x.Cluster(0.7)
	if got.Kept != 4 {
		t.Errorf("four documents came to %d, want all four kept:\n%s", got.Kept, got)
	}
	if got.Clusters != 0 {
		t.Errorf("found %d clusters among four unrelated documents", got.Clusters)
	}
}

// Near duplicates differ by what one of them is missing: a truncated copy, a
// page that lost its last paragraph to an extractor. Keeping the longest is
// keeping the one the others are missing something from.
func TestTheRepresentativeIsTheLongestOfTheCluster(t *testing.T) {
	x := index(t, Default(), article, syndicated, corrected)

	var rep string
	for _, a := range x.Assign(0.7) {
		if a.Representative {
			if rep != "" {
				t.Fatal("one cluster has two representatives")
			}
			rep = a.ID.String()
		}
	}
	if want := doc.SumString(syndicated).String(); rep != want {
		t.Errorf("the cluster is represented by %s, want the longest copy %s", rep[:12], want[:12])
	}
}

// Documents arrive in whatever order a shard holds them, and a stage whose
// answer depends on that order produces a different corpus on every rebuild.
func TestTheOrderDocumentsArriveInDoesNotChangeTheAnswer(t *testing.T) {
	forwards := index(t, Default(), article, syndicated, corrected, quoted, river, reform)
	backwards := index(t, Default(), reform, river, quoted, corrected, syndicated, article)

	if a, b := forwards.Cluster(0.7), backwards.Cluster(0.7); a != b {
		t.Errorf("the same documents in another order report\n%+v\nand\n%+v", a, b)
	}

	clusters := func(x *Index) map[doc.Hash]doc.Cluster {
		out := map[doc.Hash]doc.Cluster{}
		for _, a := range x.Assign(0.7) {
			out[a.ID] = a.Cluster
		}
		return out
	}
	a, b := clusters(forwards), clusters(backwards)
	for id, c := range a {
		if b[id] != c {
			t.Errorf("%s is in cluster %s one way round and %s the other", id.String()[:12], c, b[id])
		}
	}
}

// A cluster identity is the identity of the document it keeps, so two documents
// in one cluster carry one cluster id and it is not the zero value.
func TestEveryDocumentCarriesTheIdentityOfItsCluster(t *testing.T) {
	x := index(t, Default(), article, syndicated, river)

	byCluster := map[doc.Cluster][]Assignment{}
	for _, a := range x.Assign(0.7) {
		if a.Cluster.IsZero() {
			t.Errorf("%s carries no cluster", a.ID.String()[:12])
		}
		byCluster[a.Cluster] = append(byCluster[a.Cluster], a)
	}
	if len(byCluster) != 2 {
		t.Fatalf("three documents fell into %d clusters, want 2", len(byCluster))
	}
	for c, members := range byCluster {
		for _, m := range members {
			if m.Size != len(members) {
				t.Errorf("cluster %s holds %d documents and one of them says %d", c, len(members), m.Size)
			}
			if m.Dedup().DupClusterSize != uint32(len(members)) {
				t.Errorf("the document form of the assignment lost the cluster size")
			}
		}
	}
}

// Every distinct document is assigned exactly once, and the representatives are
// the documents the report says are kept. A report that did not agree with the
// assignments would be a headline number nobody could reproduce from the data.
func TestTheAssignmentsAgreeWithTheReport(t *testing.T) {
	x := index(t, Default(), article, article, syndicated, corrected, quoted, river, reform)

	report := x.Cluster(0.7)
	assignments := x.Assign(0.7)
	if len(assignments) != x.Distinct() {
		t.Fatalf("%d assignments for %d distinct documents", len(assignments), x.Distinct())
	}

	kept, total := 0, 0
	seen := map[doc.Hash]bool{}
	for _, a := range assignments {
		if seen[a.ID] {
			t.Errorf("%s was assigned twice", a.ID.String()[:12])
		}
		seen[a.ID] = true
		if a.Representative {
			kept++
			total += a.Size
		}
	}
	if kept != report.Kept {
		t.Errorf("%d representatives against a report of %d kept", kept, report.Kept)
	}
	if total != report.Documents {
		t.Errorf("the clusters hold %d documents and %d went in", total, report.Documents)
	}
}

// The curve is the deliverable, not the threshold. It has to be monotone, since
// a higher threshold is a stricter test and cannot merge anything a lower one
// left apart, and a curve that was not would mean the clustering depends on
// something other than the number it is given.
func TestRaisingTheThresholdKeepsMore(t *testing.T) {
	x := index(t, Wide(), article, syndicated, corrected, retyped, quoted, river, reform)

	curve := x.Curve(0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.95)
	for i := 1; i < len(curve); i++ {
		if curve[i].Kept < curve[i-1].Kept {
			t.Errorf("threshold %.2f keeps %d and threshold %.2f keeps %d",
				curve[i-1].Threshold, curve[i-1].Kept, curve[i].Threshold, curve[i].Kept)
		}
	}
	if curve[0].Kept >= curve[len(curve)-1].Kept {
		t.Errorf("the curve is flat: %d kept at %.2f and %d at %.2f",
			curve[0].Kept, curve[0].Threshold, curve[len(curve)-1].Kept, curve[len(curve)-1].Threshold)
	}
}

// The wide banding exists to make the low end of the curve real. At the
// operating point the pairs below 0.7 are mostly never proposed, so a curve
// built there would report that a threshold of 0.5 keeps what 0.7 keeps, which
// is a statement about the banding rather than about the corpus.
func TestTheCurveIsBuiltWideEnoughToSeeTheLowEnd(t *testing.T) {
	docs := []string{article, syndicated, corrected, quoted, river, reform}
	wide := index(t, Wide(), docs...).Cluster(0.3)
	narrow := index(t, Default(), docs...).Cluster(0.3)
	if wide.Kept >= narrow.Kept {
		t.Errorf("at a threshold of 0.3 the wide banding kept %d and the operating one kept %d, so the wide banding is finding nothing extra", wide.Kept, narrow.Kept)
	}
}

// Clustering must not consume the index. The whole reason the signatures are
// held is that one pass over a shard answers at every threshold the ablation
// asks about.
func TestClusteringTwiceGivesTheSameAnswer(t *testing.T) {
	x := index(t, Default(), article, syndicated, corrected, river)
	if a, b := x.Cluster(0.7), x.Cluster(0.7); a != b {
		t.Errorf("clustering the same index twice gave %+v and %+v", a, b)
	}
}

// A signature and a size are all the index needs, so a caller that fingerprinted
// documents somewhere else can add them without the text. That is what makes a
// run over a shard that no longer fits on the box possible at all.
func TestADocumentCanBeAddedWithoutItsText(t *testing.T) {
	x := index(t, Default())
	id := doc.SumString(article)
	x.Add(id, len([]rune(article)), Sign(article))
	x.AddText(article)

	if got := x.Cluster(0.7); got.Documents != 2 || got.Exact != 1 || got.Kept != 1 {
		t.Errorf("adding one document twice by two routes reported %+v", got)
	}
}
