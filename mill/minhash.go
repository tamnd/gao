package mill

// The signature: 128 numbers that stand in for a document's whole shingle set.

import "math"

// Perms is how many permutations a signature is taken over.
//
// The signature estimates the Jaccard similarity of two shingle sets by the
// share of positions where the two agree, and the standard error of that
// estimate is one over the square root of the count. At 128 that is about 0.088,
// so a pair whose real similarity is 0.70 is measured somewhere near 0.70 with a
// spread that matters at the individual pair and washes out over a corpus. It is
// also the number that makes the banding arithmetic come out where the
// deduplication threshold wants to be, which is the other reason it is 128 and
// not 100.
const Perms = 128

// Signature is a document's minhash. It is 1 KB, which is the number that
// decides how a corpus scale run has to be arranged: see the package
// documentation.
type Signature [Perms]uint64

// perm is one of the hash functions the signature minimizes over. Multiply, add,
// and mix: a is odd so the multiply is a bijection on 64 bits, and the xorshift
// afterwards is what stops the low bits of the product from deciding the
// minimum on their own.
type perm struct{ a, b uint64 }

// perms is fixed for all time and generated rather than typed out. Two runs of
// gao that disagreed about these would produce signatures that cannot be
// compared, so a document fingerprinted last year and one fingerprinted today
// have to go through the same 128 functions.
var perms = makePerms()

func makePerms() [Perms]perm {
	// splitmix64 from a fixed seed. Any decent generator would do; what matters
	// is that it is written down here and never changes.
	state := uint64(0x9e3779b97f4a7c15)
	next := func() uint64 {
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		return z ^ (z >> 31)
	}
	var out [Perms]perm
	for i := range out {
		out[i] = perm{a: next() | 1, b: next()}
	}
	return out
}

// Sign returns the minhash signature of a document's text.
//
// The text is brought to the deduplication key first, so a caller passes the
// document as it is stored and does not have to know what the key drops.
func Sign(text string) Signature {
	var sig Signature
	for i := range sig {
		sig[i] = math.MaxUint64
	}
	for h := range Shingles(Key(text)) {
		for i, p := range perms {
			v := p.a*h + p.b
			v ^= v >> 29
			if v < sig[i] {
				sig[i] = v
			}
		}
	}
	return sig
}

// Similarity is the share of positions where two signatures agree, which
// estimates the Jaccard similarity of the two shingle sets.
func (s Signature) Similarity(o Signature) float64 {
	same := 0
	for i := range s {
		if s[i] == o[i] {
			same++
		}
	}
	return float64(same) / float64(Perms)
}

// IsZero reports whether the signature was never computed. An empty document
// signs as every position at the maximum, and that is a real signature of a real
// document rather than an unset one, so the test is the zero value.
func (s Signature) IsZero() bool { return s == Signature{} }
