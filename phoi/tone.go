package phoi

// Where the tone mark goes.
//
// Vietnamese has two accepted conventions for a syllable whose nucleus is a
// glide followed by a vowel and nothing after it. One writes hoà, khoẻ, thuý,
// nguỵ, the other writes hòa, khỏe, thúy, ngụy. Both are correct and everybody
// reads both, so this is not a spelling error and nothing here is a correction.
//
// It matters anyway, because the two spellings are two strings. A document that
// arrived from two sources under two conventions survives deduplication as two
// documents, tokenizes as two sequences, and takes two rows in the embedding
// table for one word. gao writes hòa, because that is what modern publishing and
// the default input method setting produce, so it is the majority form in the
// corpus and canonicalizing toward the majority moves the fewest bytes.
//
// The rule fires on three rhymes and only when nothing follows them. Once there
// is a final consonant the placement is not optional and there is nothing to
// canonicalize: hoàn is the only spelling of hoàn, and moving that mark would
// invent one. The other exception is the syllables that begin qu, where the u
// belongs to the onset rather than to the nucleus, so quý is already where it
// should be and quy with the mark moved to the u is not a word.

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// The combining tone marks, which are what moves. The other combining marks a
// Vietnamese letter carries are part of the letter: the circumflex of â, the
// breve of ă, the horn of ư. Those never move.
const (
	grave = '\u0300' // huyền
	acute = '\u0301' // sắc
	tilde = '\u0303' // ngã
	hook  = '\u0309' // hỏi
	dot   = '\u0323' // nặng
)

func tone(c rune) bool {
	switch c {
	case grave, acute, tilde, hook, dot:
		return true
	}
	return false
}

// rhymes is the set of two vowel nuclei whose tone placement is a convention
// rather than a rule.
var rhymes = map[[2]rune]bool{
	{'o', 'a'}: true,
	{'o', 'e'}: true,
	{'u', 'y'}: true,
}

// unit is one letter of a decomposed syllable: the letter itself, the marks that
// make it a different letter, and the tone that sits on top.
type unit struct {
	base rune
	mods []rune
	tone rune
}

// compose brings a syllable to NFC, and puts a tone mark that was typed ahead of
// the letter's own mark back on top of it first.
//
// Composition alone does not do that. Two marks of the same combining class keep
// the order they arrived in, and the circumflex of ê and the acute of ế are both
// of that class, so e followed by acute followed by circumflex is not ế and no
// amount of normalizing makes it one. It renders close enough that nobody
// notices and it is a different string from every other copy of that word, which
// is the failure a zero width space causes, arriving through a different door.
func compose(s string) string {
	d := norm.NFD.String(s)
	if units, ok := split(d); ok {
		d = join(units)
	}
	return norm.NFC.String(d)
}

// retone moves the tone mark of a syllable to where gao writes it, and reports
// whether it moved anything.
func retone(s string) (string, bool) {
	if !mayRetone(s) {
		return s, false
	}
	units, ok := split(norm.NFD.String(s))
	if !ok {
		return s, false
	}

	// The nucleus has to be the last two letters of the syllable. Anything
	// after it is a final consonant, and a final consonant settles the question
	// without anybody having a convention about it.
	if len(units) < 2 {
		return s, false
	}
	first, second := &units[len(units)-2], &units[len(units)-1]
	if len(first.mods) != 0 || len(second.mods) != 0 {
		return s, false
	}
	if !rhymes[[2]rune{unicode.ToLower(first.base), unicode.ToLower(second.base)}] {
		return s, false
	}

	// The u of qu is part of the onset, so the nucleus of quý is the y alone
	// and the mark is already where it belongs.
	if unicode.ToLower(first.base) == 'u' && len(units) > 2 &&
		unicode.ToLower(units[len(units)-3].base) == 'q' {
		return s, false
	}

	// One tone in the syllable, and it is on the second letter of the nucleus.
	if second.tone == 0 || first.tone != 0 {
		return s, false
	}
	for _, u := range units[:len(units)-2] {
		if u.tone != 0 {
			return s, false
		}
	}

	first.tone, second.tone = second.tone, 0
	return norm.NFC.String(join(units)), true
}

// mayRetone is the cheap test that keeps the decomposition off the great
// majority of syllables. A syllable with no tone mark in it has nothing to move,
// and a document is mostly those.
func mayRetone(s string) bool {
	for _, c := range norm.NFD.String(s) {
		if tone(c) {
			return true
		}
	}
	return false
}

// split takes a decomposed syllable apart into letters and their marks. It
// refuses a syllable that starts with a combining mark or carries two tones,
// because both mean the text is damaged in a way this stage does not repair.
func split(s string) ([]unit, bool) {
	var units []unit
	for _, c := range s {
		if unicode.Is(unicode.Mn, c) {
			if len(units) == 0 {
				return nil, false
			}
			u := &units[len(units)-1]
			if !tone(c) {
				u.mods = append(u.mods, c)
				continue
			}
			if u.tone != 0 {
				return nil, false
			}
			u.tone = c
			continue
		}
		units = append(units, unit{base: c})
	}
	return units, len(units) > 0
}

// join puts a syllable back together. The marks come out in whatever order they
// went in, which is not canonical order and does not need to be: composing the
// result orders them.
func join(units []unit) string {
	var b strings.Builder
	for _, u := range units {
		b.WriteRune(u.base)
		for _, m := range u.mods {
			b.WriteRune(m)
		}
		if u.tone != 0 {
			b.WriteRune(u.tone)
		}
	}
	return b.String()
}
