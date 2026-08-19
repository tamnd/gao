package sang

// What makes a run of letters Vietnamese rather than merely Latin.

import (
	"slices"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/tamnd/gao/phoi"
)

// isDiacritic reports whether a letter carries a Vietnamese diacritic: any of
// the six tone marks, or one of the letters that is drawn with a mark of its
// own.
//
// The test is decomposition rather than a table, because the same letter
// arrives composed from one source and decomposed from another and a table
// would have to hold both. The one letter decomposition does not answer for is
// d with stroke, which has no combining form.
//
// The answer for a letter is cached, because this is called once per rune of
// every document in the corpus and decomposing one rune allocates two strings
// to do it. On a real GlotCC part that was seven percent of the whole cleaning
// line. The cache is the Latin range only, which is where every letter the
// question is ever asked about lives; anything above it falls through to the
// decomposition and pays what it always paid.
func isDiacritic(c rune) bool {
	if c >= 0 && c < rune(len(diacritic)) {
		return diacritic[c]
	}
	return decomposesToAMark(c)
}

// diacritic answers isDiacritic for the Latin ranges, filled once at startup.
// The bound is U+2000, which is past Latin Extended Additional, where the
// precomposed Vietnamese vowels live, and past every combining mark that can
// stand in for one.
var diacritic = func() [0x2000]bool {
	var table [0x2000]bool
	for c := range len(table) {
		table[c] = decomposesToAMark(rune(c))
	}
	return table
}()

func decomposesToAMark(c rune) bool {
	if c == 'đ' || c == 'Đ' {
		return true
	}
	for _, r := range norm.NFD.String(string(c)) {
		if unicode.Is(unicode.Mn, r) {
			return true
		}
	}
	return false
}

// stopWords is the Vietnamese function words, written the way somebody writes
// them and matched with the tone marks taken off.
//
// Gopher requires an English document to hold at least two of the, be, to, of,
// and, that, have and with, which works because those words are unavoidable in
// English and rare in everything else. The Vietnamese version of that test has
// to answer a question English does not raise. A great deal of Vietnamese is
// typed without tone marks, on a phone or on a keyboard nobody configured, and
// it is still Vietnamese. Matching only the marked spellings would send that
// whole register to the reject store under the wrong reason.
//
// So the list is written marked and matched unmarked, which is a deliberate
// loosening: co and la and de are also words in other languages, and a document
// in one of them can collect two matches by accident. This check is a floor
// rather than language identification, the identifier is a separate piece of
// work, and a cheap check that lets a few documents through to it is the right
// error to make.
var stopWords = map[string]bool{
	"và":    true,
	"của":   true,
	"là":    true,
	"có":    true,
	"được":  true,
	"những": true,
	"không": true,
	"trong": true,
	"với":   true,
	"cho":   true,
	"một":   true,
	"người": true,
	"này":   true,
	"đã":    true,
	"khi":   true,
	"để":    true,
	"các":   true,
	"thì":   true,
	"nhưng": true,
	"cũng":  true,
	"rằng":  true,
	"đến":   true,
	"từ":    true,
	"phải":  true,
	"sẽ":    true,
	"nếu":   true,
	"vì":    true,
	"tại":   true,
	"theo":  true,
	"hoặc":  true,
}

// bareStopWords is the list with the marks taken off, built once.
var bareStopWords = func() map[string]bool {
	out := make(map[string]bool, len(stopWords))
	for w := range stopWords {
		out[phoi.Bare(w)] = true
	}
	return out
}()

// countStopWords returns how many distinct function words the document holds.
//
// Distinct rather than total, because a navigation bar that repeats one word
// forty times is not more Vietnamese than a paragraph that uses three once, and
// a total would say it was.
func countStopWords(syllables []string) int {
	seen := make(map[string]bool)
	for _, s := range syllables {
		if w := phoi.Bare(s); bareStopWords[w] {
			seen[w] = true
		}
	}
	return len(seen)
}

// StopWords returns the function word list, so that a report or a test can name
// what a document was measured against rather than describing it.
func StopWords() []string {
	out := make([]string, 0, len(stopWords))
	for w := range stopWords {
		out = append(out, w)
	}
	slices.Sort(out)
	return out
}
