// Package inspect measures how wrong a reading of Vietnamese is.
//
// Soi is what you do to a grain of rice: hold it up to the light and look for
// the fracture. This package holds a machine's reading of a page up against
// what the page says, and reports what did not survive.
//
// # Why the ordinary metric does not work here
//
// Character error rate is the number everybody quotes for OCR, and on
// Vietnamese it hides the failure that matters most.
//
// Start with the shape of the language. About a quarter of the characters in
// running Vietnamese carry a mark of some kind, and about a sixth carry a tone.
// Those two figures are measured rather than asserted, in
// TestTheShapeOfTheLanguage, because everything below is arithmetic on them.
//
// Now take a reading that is genuinely good by anyone's standard: 2% character
// error rate, better than most engines manage on a clean scan. Suppose all of
// that 2% is tone marks, which is exactly where an engine trained mostly on
// Latin script puts its errors. A sixth of the characters carry a tone, so 2%
// of the characters is one tone in eight, which is a wrong word in most
// sentences. The report says 98% accurate. The corpus it produced is unusable,
// and worse than unusable, because ma, má, mà, mả, mã and mạ are six unrelated
// words and a page that has lost the difference between them is still fluent
// Vietnamese made of words that exist. Every quality filter downstream passes
// it. A page with 2% of its letters smudged instead gets caught by the first
// filter it meets, and the two readings score the same.
//
// That is the whole problem. One number cannot separate the damage that is
// visible from the damage that is not, and on Vietnamese the invisible kind is
// both commoner and far more expensive. So this package reports the rates
// separately and refuses to average them. Character error rate says how much of
// the page came through. Diacritic error rate says how much of the meaning did,
// and it is the one the OCR gate in S4 is set on. On the 2% reading above they
// read 2%, 8% and 12% respectively, and the last two are the ones somebody can
// act on.
//
// # What a Vietnamese letter is made of
//
// Three things, and they fail independently.
//
// The base letter, a or e or d. The letter's own mark, which is part of its
// identity rather than decoration: the circumflex of â, the breve of ă, the
// horn of ư, the stroke of đ. And the tone, one of six, of which five are
// written and one is the absence of the other five.
//
// An engine that reads ế as e has made one character error and lost two things.
// An engine that reads ế as ề has made one character error and lost one. An
// engine that reads ế as é has made one character error and lost a different
// one, and the word it produces is a real word that means something else. All
// three are reported separately, because they come from different failures in
// the engine and they are fixed in different places.
//
// The đ and d confusion has its own line for the same reason. It is not a tone
// error, the stroke is not a combining mark and survives every amount of
// Unicode normalization, and it is the single commonest thing a Latin trained
// engine gets wrong on Vietnamese.
//
// # What this does not do
//
// It does not decide whether a reading is good enough. The gates are written
// down in S4 and they are checked against these numbers, not inside them.
//
// It does not know what the page said, only what it was told the page said. A
// reference transcript is somebody's work and every number here inherits its
// mistakes, which is why the evaluation set is a separate deliverable from the
// metric that reads it.
package inspect

import (
	"golang.org/x/text/unicode/norm"
)

// The combining marks a Vietnamese letter can carry, split by what they do.
//
// These are spelled out here rather than taken from phoi, which knows the same
// five tone marks for its own reasons. The two lists say different things:
// phoi's is about which mark moves when a syllable is retoned, and this one is
// about which mark carries meaning that a reading can lose. They agree today
// and there is no reason they have to, so neither imports the other.
const (
	grave = '̀' // huyền
	acute = '́' // sắc
	tilde = '̃' // ngã
	hook  = '̉' // hỏi
	dot   = '̣' // nặng

	circumflex = '̂' // â ê ô
	breve      = '̆' // ă
	horn       = '̛' // ơ ư

	// stroke stands for the bar through đ, which has no decomposition in
	// Unicode at all. A d with a stroke is one code point and stays one under
	// every normalization form, so there is no combining mark to find and the
	// letter has to be taken apart by hand. U+0335 is the combining short
	// stroke overlay, borrowed here as a name for something Unicode does not
	// name, and it never appears in text this package reads.
	stroke = '̵'
)

// A Tone is one of the six tones of Vietnamese.
//
// Ngang, the level tone, is written by writing nothing, which is why it is the
// zero value and why it has to be a value at all rather than an absence. An
// engine that drops a tone has not produced a character without a tone. It has
// produced a character with the wrong tone, and the confusion matrix says so.
type Tone int

// The six tones, in the order they are taught.
const (
	Ngang Tone = iota // level, unwritten
	Huyen             // grave, à
	Sac               // acute, á
	Ask               // hook above, ả
	Nga               // tilde, ã
	Nang              // dot below, ạ
)

// Tones is every tone, which is what a report iterates to lay out the confusion
// matrix.
func Tones() []Tone { return []Tone{Ngang, Huyen, Sac, Ask, Nga, Nang} }

// String is the Vietnamese name of the tone, because a confusion matrix labeled
// with Unicode code points is a matrix nobody reads.
func (t Tone) String() string {
	switch t {
	case Huyen:
		return "huyền"
	case Sac:
		return "sắc"
	case Ask:
		return "hỏi"
	case Nga:
		return "ngã"
	case Nang:
		return "nặng"
	}
	return "ngang"
}

// MarshalText puts the name into JSON rather than the number, so that a
// published result is readable without this package to hand.
func (t Tone) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

func toneOf(mark rune) (Tone, bool) {
	switch mark {
	case grave:
		return Huyen, true
	case acute:
		return Sac, true
	case hook:
		return Ask, true
	case tilde:
		return Nga, true
	case dot:
		return Nang, true
	}
	return Ngang, false
}

// A Letter is one character taken apart into the three things that can go wrong
// with it separately.
type Letter struct {
	// Base is the letter with everything taken off, so ế and e are both e. Case
	// is kept, because a reading that lost the capital lost something, and it is
	// a character error rather than a diacritic one.
	Base rune

	// Mod is the letter's own mark, which is part of which letter it is rather
	// than decoration on it. There is at most one in Vietnamese: no letter
	// carries both a horn and a breve, and none carries two of anything.
	Mod rune

	// Tone is the tone on top.
	Tone Tone
}

// Split takes a character apart.
//
// Anything that is not a Vietnamese letter comes back as itself with no mark and
// the level tone, which is the right answer for a digit, a space and a comma:
// they cannot lose a diacritic, so they take no part in the diacritic rate and
// every error on them is a character error.
func Split(r rune) Letter {
	if r == 'đ' {
		return Letter{Base: 'd', Mod: stroke}
	}
	if r == 'Đ' {
		return Letter{Base: 'D', Mod: stroke}
	}
	l := Letter{Base: r}
	first := true
	for _, c := range norm.NFD.String(string(r)) {
		if first {
			l.Base, first = c, false
			continue
		}
		if t, ok := toneOf(c); ok {
			l.Tone = t
			continue
		}
		switch c {
		case circumflex, breve, horn:
			l.Mod = c
		}
	}
	return l
}

// Marked reports whether the letter carries anything a reading can lose. It is
// the population the diacritic error rate is measured over: a character with
// nothing on it cannot drop a mark, and counting it in the denominator would
// mean the rate went down when the page got longer.
func (l Letter) Marked() bool { return l.Mod != 0 || l.Tone != Ngang }

// Letters takes a whole string apart, one Letter per character.
//
// The string is composed first. Vietnamese arrives written both ways, ế as one
// code point and as e followed by two combining marks, and the two are the same
// letter. A metric that counted them as three characters against one would
// report an error rate that depended on which normalization form somebody's
// tooling happened to emit.
func Letters(s string) []Letter {
	out := make([]Letter, 0, len(s))
	for _, r := range norm.NFC.String(s) {
		out = append(out, Split(r))
	}
	return out
}
