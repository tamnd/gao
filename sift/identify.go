package sift

// Whether a document is in Vietnamese.

import (
	"strings"
	"unicode"

	"github.com/tamnd/gao/normalize"
)

// Language is what one document measures against the syllable inventory.
//
// Like everything else in this package these are counts rather than a verdict,
// and they go on the row as counts. A corpus that recorded "Vietnamese, 0.98"
// cannot be re-filtered at a different threshold without going back to the text,
// and the identifier is the one part of the pipeline most likely to have its
// threshold moved after somebody looks at what it let through.
type Language struct {
	// Tokens is every whitespace separated run that holds a letter. It is the
	// denominator of everything below.
	Tokens int

	// Syllables is how many of those are Vietnamese syllables written with
	// their tone marks.
	Syllables int

	// Bare is how many are Vietnamese syllables once the marks are taken off
	// both sides. It is never smaller than Syllables and it is the measure the
	// unmarked register is judged on.
	Bare int

	// Marked is how many tokens carry a Vietnamese mark at all, which is what
	// says which of the two registers the document is written in.
	Marked int

	// StopWords is how many distinct function words the document holds, matched
	// with the marks off, and MarkedStopWords is how many of those were written
	// with their marks. The pair is what separates Vietnamese typed without
	// marks from another language that happens to spell a few short words the
	// same way.
	StopWords       int
	MarkedStopWords int
}

// Rate is the share of tokens that are Vietnamese syllables as written.
func (l Language) Rate() float64 {
	if l.Tokens == 0 {
		return 0
	}
	return float64(l.Syllables) / float64(l.Tokens)
}

// BareRate is the share that are Vietnamese syllables with the marks off.
func (l Language) BareRate() float64 {
	if l.Tokens == 0 {
		return 0
	}
	return float64(l.Bare) / float64(l.Tokens)
}

// MarkRate is the share of tokens carrying a Vietnamese mark.
func (l Language) MarkRate() float64 {
	if l.Tokens == 0 {
		return 0
	}
	return float64(l.Marked) / float64(l.Tokens)
}

// The two registers, and the two bars.
//
// Vietnamese written with its tone marks is decided at MinRate. On the labeled
// set the marked documents score from 0.81 to 1.00 and everything that is not
// Vietnamese scores 0.27 or less, so the interval between them is most of the
// range and the threshold has room to sit well clear of both ends. It sits at
// three quarters rather than higher because the document that pulls the bottom
// of that range down is the one this whole piece of work exists for: Vietnamese
// technical writing with the English terms left in, which every model trained on
// clean prose calls English.
//
// Vietnamese written without its marks is decided at MinBareRate, and the bar is
// higher rather than lower, which is the opposite of what it looks like it
// should be. Taking the marks off collapses the inventory, since da is one token
// and it is đá and dạ and da and đã, so the test admits far more than it should
// and has to be paid for somewhere. It is paid for here, with a stricter share
// and with more function words required. It is also the bar the labeled set
// constrains least, because both unmarked documents in it score 1.00, so it is
// the first number an ablation should move.
//
// MinMarkRate is only which of the two questions gets asked. A page with a
// marked article and an unmarked comment thread under it is judged as marked
// text, which is the stricter of the two.
const (
	MinRate         = 0.75
	MinBareRate     = 0.90
	MinMarkRate     = 0.15
	MinIDStopWords  = 3
	MinIDMarkedStop = 1
)

// Identify measures a document against the Vietnamese syllable inventory.
//
// It expects text that has already been through normalize, for the same reason
// [Measure] does, and for one more: a page in a legacy font encoding is not
// Vietnamese to any test that reads characters, and it is Vietnamese to a
// reader who has the font. Normalization is what settles that, and running the
// identifier ahead of it would file a large part of the older web under the
// wrong language and never look at it again.
func Identify(text string) Language {
	var l Language
	seen := make(map[string]bool)
	marked := make(map[string]bool)
	for _, tok := range strings.Fields(text) {
		word := trimToLetters(tok)
		if word == "" {
			continue
		}
		l.Tokens++
		lower := strings.ToLower(word)
		if hasMark(lower) {
			l.Marked++
		}
		if Syllable(lower) {
			l.Syllables++
		}
		if BareSyllable(lower) {
			l.Bare++
		}
		if bare := normalize.Bare(lower); bareStopWords[bare] {
			seen[bare] = true
			if stopWords[lower] {
				marked[lower] = true
			}
		}
	}
	l.StopWords, l.MarkedStopWords = len(seen), len(marked)
	return l
}

// Vietnamese is the verdict, and it is built to be wrong in one direction.
//
// A document this says yes to goes into the corpus and is never looked at again
// by a person. A document it says no to goes to the reject store with its
// reason and its counts, where a query can find it, and where the whole class of
// them can be pulled back out if the threshold turns out to have been wrong.
// Those are not the same mistake and they should not be traded off as though
// they were.
//
// Length is not checked here. The length filter runs before this and rejects
// anything under sixty syllables, so a document that reaches this function is
// long enough for a share to mean something. Calling Identify on a sentence
// works and answers on that sentence, which is what the tests do.
func (l Language) Vietnamese() bool {
	if l.Tokens == 0 {
		return false
	}
	if l.MarkRate() >= MinMarkRate {
		return l.Rate() >= MinRate && l.MarkedStopWords >= MinIDMarkedStop
	}
	return l.BareRate() >= MinBareRate && l.StopWords >= MinIDStopWords
}

// trimToLetters cuts the punctuation off both ends of a token and leaves the
// inside alone, so that quoted syllables and syllables at the end of a sentence
// are the same token, and a hyphenated loan like ki-lô stays one thing that is
// not a syllable rather than becoming two that are.
func trimToLetters(tok string) string {
	return strings.TrimFunc(tok, func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.Is(unicode.Mn, c)
	})
}

// hasMark reports whether a token carries a mark only Vietnamese and a handful
// of other languages write. It is the test for which register the document is
// in rather than for which language, so it asks about the marks and not about
// the inventory.
func hasMark(tok string) bool {
	for _, c := range tok {
		if isDiacritic(c) {
			return true
		}
	}
	return false
}
