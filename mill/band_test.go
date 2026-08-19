package mill

import (
	"math"
	"testing"
)

// A banding that does not cover the signature would ignore the tail of every
// one of them and nothing about the result would look wrong.
func TestABandingHasToCoverTheSignature(t *testing.T) {
	for _, b := range []Banding{
		{Bands: 16, Rows: 8},
		{Bands: 32, Rows: 4},
		{Bands: 8, Rows: 16},
	} {
		if !b.Valid() {
			t.Errorf("%d bands of %d rows is %d of %d and was refused", b.Bands, b.Rows, b.Bands*b.Rows, Perms)
		}
	}
	for _, b := range []Banding{
		{Bands: 16, Rows: 7},
		{Bands: 0, Rows: 8},
		{Bands: 16, Rows: 0},
		{Bands: 100, Rows: 2},
	} {
		if b.Valid() {
			t.Errorf("%d bands of %d rows is %d of %d and was accepted", b.Bands, b.Rows, b.Bands*b.Rows, Perms)
		}
	}
}

// The knee is the threshold. If the arithmetic behind it drifts, a run that
// says it deduplicated at 0.7 is deduplicating somewhere else and the ablation
// curve is measuring a banding that nobody chose.
func TestTheKneeIsWhereTheThresholdIs(t *testing.T) {
	if got := Default().Knee(); math.Abs(got-0.707) > 0.005 {
		t.Errorf("the operating banding has its knee at %.3f, want 0.707", got)
	}
	if got := Wide().Knee(); math.Abs(got-0.420) > 0.005 {
		t.Errorf("the wide banding has its knee at %.3f, want 0.420", got)
	}
}

// The curve is a step and the point of it is that it is steep. What the numbers
// say is that at the operating point a pair at 0.9 is caught essentially always
// and a pair at 0.5 essentially never, which is what makes a threshold mean
// something rather than being a preference applied to whatever turned up.
func TestDetectionIsASteepStep(t *testing.T) {
	b := Default()
	if got := b.Detection(0.9); got < 0.99 {
		t.Errorf("a pair at 0.9 is proposed %.3f of the time, want almost always", got)
	}
	if got := b.Detection(0.5); got > 0.1 {
		t.Errorf("a pair at 0.5 is proposed %.3f of the time, want almost never", got)
	}

	last := 0.0
	for s := 0.0; s <= 1.0001; s += 0.05 {
		got := b.Detection(s)
		if got < last {
			t.Fatalf("detection fell from %.3f to %.3f at similarity %.2f", last, got, s)
		}
		last = got
	}
}

// The wide banding is for the ablation, and it earns its place by proposing the
// pairs a lower threshold would want to score. A curve that could only see the
// pairs the operating point proposes would show every threshold under 0.7
// keeping the same documents, which is a flat line rather than a measurement.
func TestTheWideBandingSeesWhatALowerThresholdWouldKeep(t *testing.T) {
	if got := Wide().Detection(0.5); got < 0.8 {
		t.Errorf("the wide banding proposes a pair at 0.5 only %.3f of the time, which is not enough to score one", got)
	}
	if got := Default().Detection(0.5); got > Wide().Detection(0.5) {
		t.Error("the operating banding proposes more at 0.5 than the wide one, which is the wrong way round")
	}
}

// Eight equal rows in band three are not eight equal rows in band eleven. Two
// documents that share one run of shingles would otherwise be proposed as
// candidates for a reason that is not a reason.
func TestTheBandNumberIsPartOfTheHash(t *testing.T) {
	var s Signature
	for i := range s {
		s[i] = 7
	}
	h := Default().Hashes(s)
	seen := map[uint64]int{}
	for i, v := range h {
		if j, ok := seen[v]; ok {
			t.Errorf("band %d and band %d of a signature of one repeated value hash the same", j, i)
		}
		seen[v] = i
	}
}

func TestTheHashesAreOnePerBand(t *testing.T) {
	for _, b := range []Banding{Default(), Wide()} {
		if got := len(b.Hashes(Sign(article))); got != b.Bands {
			t.Errorf("%d bands of %d rows produced %d hashes", b.Bands, b.Rows, got)
		}
	}
}

// Two documents that agree everywhere agree in every band, and two that agree
// nowhere agree in none. Anything else and the index is proposing or missing
// candidates for reasons that have nothing to do with the documents.
func TestBandsMatchWhenTheSignaturesDo(t *testing.T) {
	b := Default()
	same := b.Hashes(Sign(article))
	copied := b.Hashes(Sign(retyped))
	for i := range same {
		if same[i] != copied[i] {
			t.Errorf("band %d differs between a document and a retyped copy with the same key", i)
		}
	}

	other := b.Hashes(Sign(river))
	for i := range same {
		if same[i] == other[i] {
			t.Errorf("band %d of two unrelated documents hashes the same", i)
		}
	}
}
