package xay

// What a document looks like to the part of the pipeline whose only question is
// whether two of them are the same text.

import (
	"strings"
	"unicode"

	"github.com/tamnd/gao/phoi"
)

// ShingleSize is the length of the character n-gram, in runes.
//
// Five is the usual choice and it is a better one for Vietnamese than for
// English. A Vietnamese syllable is one to five letters and the space between
// syllables is written, so a five-gram covers a syllable and reaches into the
// one beside it, which is what makes the shingle carry word order rather than
// just vocabulary. Three would collide across unrelated documents and eight
// would miss a copy that changed one word in a sentence.
const ShingleSize = 5

// Key is the text as deduplication sees it, and it is never written back.
//
// Two copies of one article differ in the places a republisher touches. The
// quotes come out curly, a comma moves, the headline is capitalized differently,
// and one of them writes lý where the other writes lí. None of that is the
// document being different and all of it changes a hash, so the key drops case,
// drops punctuation, folds the i and y pair, and reduces the spacing to one
// space between syllables.
//
// Digits stay. It is tempting to drop them with the punctuation, and it would be
// wrong: a table of figures with different figures in it is a different table,
// and a corpus of Vietnamese statistics that deduplicated on the prose alone
// would keep one year of everything.
func Key(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	space := false
	for _, c := range text {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !unicode.Is(unicode.Mn, c) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(unicode.ToLower(c))
	}
	return phoi.Fold(b.String())
}

// Shingles yields the hash of every character n-gram of the key, in order.
//
// The hashes are not deduplicated. A repeated shingle cannot change a minimum,
// so paying for a set here would buy nothing, and the sets these feed are large
// enough that the allocation would show up.
//
// A key shorter than the shingle is one shingle. Otherwise a two syllable
// document would have no shingles at all, every one of them would look identical
// to every other, and the shortest documents in the corpus would collapse into
// one cluster.
func Shingles(key string) func(func(uint64) bool) {
	return func(yield func(uint64) bool) {
		r := []rune(key)
		if len(r) == 0 {
			return
		}
		if len(r) <= ShingleSize {
			yield(hashRunes(r))
			return
		}
		for i := 0; i+ShingleSize <= len(r); i++ {
			if !yield(hashRunes(r[i : i+ShingleSize])) {
				return
			}
		}
	}
}

// hashRunes is FNV-1a over the runes of one shingle rather than over its bytes.
// Runes because the window is a window of characters, and a byte window would
// cut a Vietnamese letter in half and hash the halves.
func hashRunes(r []rune) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for _, c := range r {
		h ^= uint64(c)
		h *= prime
	}
	return h
}
