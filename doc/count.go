package doc

import (
	"unicode"
	"unicode/utf8"
)

// Counting the two shape columns that are a property of the text alone.
//
// Both are computed once at ingest and stored in a column, and both are
// recomputed later by anything checking that a stored column and the text beside
// it still agree. There is one function for each rather than one per caller, on
// purpose. A check that counts the text its own way measures the distance
// between two implementations, and the question it has to answer is whether the
// column matches the text next to it, not whether two people agree about what a
// syllable is.
//
// Tokens are not here. A token is defined by a pinned tokenizer file rather than
// by a rule anybody can write down, which is why the third shape column costs a
// download and these two do not.

// Chars is the number of characters in the text, which is the n_chars column.
func Chars(text string) uint32 {
	return uint32(utf8.RuneCountInString(text))
}

// Syllables is the number of syllables in the text, which is the n_syllables
// column.
//
// Every maximal run of letters is one syllable, and digits and punctuation are
// none. Vietnamese puts a space between syllables rather than between words, so
// this does for Vietnamese roughly what a word count does for English, and the
// two are not the same number: Việt Nam is one word and counts as two here.
// Every threshold inherited from an English pipeline is wrong by about that
// factor.
//
// This is a count and not an estimate, which is the difference between it and
// the conversions in units.go. Those exist to size a download before it happens.
// This is what a per source number in a release note is allowed to be built
// from.
func Syllables(text string) uint32 {
	var n uint32
	inWord := false
	for _, r := range text {
		if unicode.IsLetter(r) {
			if !inWord {
				n++
				inWord = true
			}
			continue
		}
		inWord = false
	}
	return n
}
