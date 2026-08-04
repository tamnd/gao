package phoi

// Keystrokes that were meant to be letters.
//
// Vietnamese is typed through an input method. Telex spells the letters with
// doubled and following consonants, so đường is typed dduwowngj, and VNI spells
// them with digits, so đường is d9u7o7ng2. When the input method is off, or the
// page swallowed it, or the text was copied out of a field that never had it,
// the keystrokes land in the document as they were typed. It is common in forum
// and comment text and it is not rare enough to ignore.
//
// It is flagged and never repaired. Repairing it means deciding what somebody
// meant to type, and dduwowngj is only obviously đường to a reader who already
// knows the word. A pipeline that guesses here is writing text into the corpus
// that nobody wrote, which is the one thing this project does not do quietly.
//
// The detector is built for precision rather than for recall, because a false
// positive throws away a document that is fine. Everything it flags is something
// that cannot be a Vietnamese word and is unlikely to be an English one either,
// and the things it misses are things it would have had to guess about.

import (
	"slices"
	"strings"
)

// Residue reports whether a word looks like input method keystrokes rather than
// text.
func Residue(word string) bool {
	return vniResidue(word) || telexResidue(word)
}

// vniResidue is a digit sitting inside a word where VNI would have put one.
//
// The digit alone says nothing, because covid19 and mp3 and IPv6 have one. Three
// conditions together say something. The digit has letters on both sides of it,
// somewhere, so a version number on the end of a word is not it. The word
// carries at least two digits and is long enough to be a syllable somebody
// typed, because VNI spells one Vietnamese word with a digit per mark and never
// with only one. And the digit comes straight after a vowel or a d, which is the
// only place VNI ever puts one: the digit marks the letter in front of it, and
// the d is there because 9 after a d is how đ is typed.
//
// The last condition is what keeps the identifiers out. Win32API and utf8mb4
// have two digits with letters around them and neither digit follows a vowel, so
// neither word looks like VNI to this rule, which is right, because neither is.
func vniResidue(word string) bool {
	r := []rune(word)
	if len(r) < 5 {
		return false
	}
	digits, found := 0, false
	vni := make([]bool, len(r))
	for i, c := range r {
		if !isDigit(c) {
			continue
		}
		digits++
		// A digit is where VNI puts one if it marks the letter in front of it,
		// or if it follows another digit that does. Two marks on one letter are
		// two digits in a row: ế is e then 6 then 1.
		vni[i] = i > 0 && (vniCarrier(r[i-1]) || (isDigit(r[i-1]) && vni[i-1]))
		if vni[i] && anyLetter(r[:i]) && anyLetter(r[i+1:]) {
			found = true
		}
	}
	return found && digits >= 2
}

// vniCarrier is a letter a VNI digit can be marking: a vowel, or the d of đ.
func vniCarrier(c rune) bool {
	switch c {
	case 'a', 'e', 'i', 'o', 'u', 'y', 'A', 'E', 'I', 'O', 'U', 'Y', 'd', 'D':
		return true
	}
	return false
}

func anyLetter(r []rune) bool { return slices.ContainsFunc(r, isLetter) }

// telexPairs are the keystrokes that make a Vietnamese letter under Telex: a
// doubled vowel for the circumflex, a following w for the horn and the breve, a
// doubled d for đ.
var telexPairs = []string{"aa", "ee", "oo", "aw", "ow", "uw", "dd"}

// telexTones are the keys that put the tone on, and they are typed at the end of
// the syllable.
const telexTones = "sfrxj"

// telexResidue is a word that is spelled the way Telex is typed.
//
// Ending in a tone key is necessary and nowhere near sufficient, because
// "goodness" and "flows" and "under" all do. So one of three further things has
// to hold, and each of them is a thing an English word does not do. The word
// starts dd, which is the only way to type đ and is not how any word is spelled.
// It ends in j, which is not a Vietnamese letter and does not end an English
// word either. Or it carries two of the letter keystrokes, which is what a
// syllable with a horn and a circumflex in it needs.
//
// What that leaves out is the point of the paragraph above it. "veef" for về and
// "lamf" for làm and "minhf" for mình are residue and none of them is caught,
// because the rule that caught them would have to say that a short word ending
// in f is Vietnamese, and "half" and "chief" and "brief" are not. The detector
// finds the documents where the input method was off, because those documents
// have the long syllables in them too, and it does not find every word in them.
func telexResidue(word string) bool {
	if len(word) < 4 || !ascii(word) {
		return false
	}
	w := strings.ToLower(word)
	last := rune(w[len(w)-1])
	if !strings.ContainsRune(telexTones, last) {
		return false
	}
	if strings.HasPrefix(w, "dd") || last == 'j' {
		return true
	}
	pairs := 0
	for _, p := range telexPairs {
		pairs += strings.Count(w, p)
	}
	return pairs >= 2
}

// ascii reports whether a word is ASCII letters alone. Anything carrying a
// Vietnamese letter came out of an input method that was working.
func ascii(word string) bool {
	for i := range len(word) {
		c := word[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			continue
		}
		return false
	}
	return true
}

func isDigit(c rune) bool { return c >= '0' && c <= '9' }

func isLetter(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
