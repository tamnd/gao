package xay

// Banding: how a signature turns into a small number of lookups.

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Banding is the signature cut into bands of rows. Two documents are candidates
// when any one band matches exactly.
//
// The banding is the threshold. A pair whose similarity is s agrees on a given
// row with probability s, on a whole band of r rows with probability s^r, and on
// at least one of b bands with probability 1 - (1 - s^r)^b. That curve is a step
// with its knee near (1/b)^(1/r), and moving the knee means choosing different
// numbers here rather than filtering harder afterwards.
type Banding struct {
	Bands int
	Rows  int
}

// The operating point: 16 bands of 8 rows, a knee at 0.71.
//
// It is where it is because of what a Vietnamese near duplicate looks like. A
// syndicated news article republished by a second site keeps the body and
// changes the headline, the byline and the boilerplate, which lands between 0.7
// and 0.9. A forum quote of another post, or a page that shares only a legal
// footer, lands well below 0.5. A knee at 0.71 separates those two populations,
// and the deduplication ablation is what says whether it separates them in the
// right place.
const (
	DefaultBands = 16
	DefaultRows  = 8
)

// DefaultThreshold is the knee of the default banding, rounded to where a person
// can type it. Running at a threshold far from the knee is not wrong, it just
// wastes one of the two mechanisms: below the knee the candidates were mostly
// never proposed, and far above it the verification is doing all the work.
const DefaultThreshold = 0.71

// CurveThresholds is what an ablation curve is taken at. It runs from well below
// anything the pipeline would use to well above it, because the shape of the
// curve on both sides of the choice is what says whether the choice is on a
// cliff or on a plateau.
var CurveThresholds = []float64{0.5, 0.6, 0.7, 0.75, 0.8, 0.85, 0.9, 0.95}

// Default is the banding the pipeline runs at.
func Default() Banding { return Banding{Bands: DefaultBands, Rows: DefaultRows} }

// Wide is the banding used to build an ablation curve. Its knee is at 0.42, so
// it proposes far more candidates than the pipeline would act on, which is the
// point: a curve has to be able to see what a lower threshold would have kept,
// and candidates that were never generated cannot be scored.
func Wide() Banding { return Banding{Bands: 32, Rows: 4} }

// Valid reports whether the bands and rows multiply out to a whole signature. A
// banding that did not would silently ignore the tail of every signature.
func (b Banding) Valid() bool {
	return b.Bands > 0 && b.Rows > 0 && b.Bands*b.Rows == Perms
}

func (b Banding) check() error {
	if !b.Valid() {
		return fmt.Errorf("xay: banding of %d bands by %d rows does not cover a signature of %d", b.Bands, b.Rows, Perms)
	}
	return nil
}

// Knee is the similarity at which a pair has an even chance of being proposed as
// a candidate, which is the threshold this banding is built for.
func (b Banding) Knee() float64 {
	return math.Pow(1/float64(b.Bands), 1/float64(b.Rows))
}

// Detection is the probability that a pair of the given similarity is proposed
// as a candidate by at least one band. It is what the recall of a deduplication
// run actually is, and it is worth printing beside a threshold rather than
// leaving to be assumed.
func (b Banding) Detection(similarity float64) float64 {
	return 1 - math.Pow(1-math.Pow(similarity, float64(b.Rows)), float64(b.Bands))
}

// Hashes returns one hash per band of the signature.
//
// The band index goes into the hash. Without it, a band of eight equal values in
// band 3 would collide with the same eight values in band 11, which happens the
// moment two documents share a run of shingles, and the pair would be proposed
// for a reason that is not a reason.
func (b Banding) Hashes(s Signature) []uint64 {
	out := make([]uint64, b.Bands)
	var buf [8]byte
	for i := range out {
		const (
			offset = 14695981039346656037
			prime  = 1099511628211
		)
		h := uint64(offset)
		h ^= uint64(i)
		h *= prime
		for _, v := range s[i*b.Rows : (i+1)*b.Rows] {
			binary.LittleEndian.PutUint64(buf[:], v)
			for _, c := range buf {
				h ^= uint64(c)
				h *= prime
			}
		}
		out[i] = h
	}
	return out
}
