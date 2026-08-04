package phoi

// The spellings gao keeps, and the key that lets two of them meet.
//
// Mỹ and Mĩ, kỹ and kĩ, lý and lí, quý and quí are the same words under two
// positions on an orthographic reform that never finished. Both are correct,
// both are in print, and one of each pair is often a proper noun: Mỹ is America
// and Mỹ is a name. Rewriting one into the other changes what a document says,
// so the text keeps whichever spelling it arrived with and the model learns
// both, which is right, because both exist.
//
// Deduplication cannot live with that. Two copies of one article that differ
// only in whether they write lý or lí are two documents to a shingle hash, and
// they are one document to a reader. So there is a second key, computed from the
// text and never written back into it, that folds the pair together. It is used
// where the question is whether two strings are the same text, which is
// deduplication and benchmark matching, and nowhere else.

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Fold returns the key that two spellings of the same word share.
//
// The reform is about the y that is a syllable's whole vowel, and only that y.
// Everywhere else the two letters are two different sounds and folding them
// would fold two different words together: tay is a hand and tai is an ear, hay
// is good and hai is two, and a deduplicator that could not tell them apart
// would throw away the second of every pair. So the fold fires when the y has no
// vowel on either side of it, and qu is the one exception, because the u there
// belongs to the consonant and quý is the pair of quí.
//
// Case is somebody else's decision, made where the key is built, because a
// function that folded both would be two decisions with one test and neither of
// them arguable on its own.
func Fold(text string) string {
	if !hasY(text) {
		return text
	}
	r := []rune(norm.NFC.String(text))
	for i, c := range r {
		if b := base(c); b != 'y' && b != 'Y' {
			continue
		}
		if vowelAt(r, i-1) && !afterQu(r, i) {
			continue
		}
		if vowelAt(r, i+1) {
			continue
		}
		r[i] = toI(c)
	}
	return string(r)
}

// Folded reports whether two strings are the same text once the i and y
// spellings are folded together.
func Folded(a, b string) bool { return Fold(a) == Fold(b) }

// hasY is the cheap test that keeps the rest of this file off the great majority
// of documents. The letter carries a tone mark in most of the words that matter,
// so the precomposed forms are in the set as well as the bare letter.
func hasY(text string) bool {
	for _, c := range text {
		switch c {
		case 'y', 'Y',
			'ý', 'Ý', 'ỳ', 'Ỳ', 'ỷ', 'Ỷ', 'ỹ', 'Ỹ', 'ỵ', 'Ỵ':
			return true
		}
	}
	return false
}

// base is the letter under the marks. Vietnamese vowels carry up to two of them
// and the neighbour test is about the letter rather than the tone on it.
func base(c rune) rune {
	for _, b := range norm.NFD.String(string(c)) {
		return b
	}
	return c
}

// toI moves a y to the i that carries the same tone, by taking the marks off,
// changing the letter, and putting them back.
func toI(c rune) rune {
	d := []rune(norm.NFD.String(string(c)))
	switch d[0] {
	case 'y':
		d[0] = 'i'
	case 'Y':
		d[0] = 'I'
	default:
		return c
	}
	for _, r := range norm.NFC.String(string(d)) {
		return r
	}
	return c
}

// vowels is the alphabet's vowels with their marks taken off, which is what
// base returns.
const vowels = "aeiouyAEIOUY"

func vowelAt(r []rune, i int) bool {
	return i >= 0 && i < len(r) && strings.ContainsRune(vowels, base(r[i]))
}

// afterQu reports whether the letter before this one is the u of qu, where the u
// is part of the consonant rather than part of the vowel.
func afterQu(r []rune, i int) bool {
	if i < 2 {
		return false
	}
	u, q := base(r[i-1]), base(r[i-2])
	return (u == 'u' || u == 'U') && (q == 'q' || q == 'Q')
}
