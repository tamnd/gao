package normalize

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
	"iter"
	"strings"
	"unicode"
	"unicode/utf8"

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
	if settled(s) {
		return s
	}
	if units, ok := split(Decomposed(s)); ok {
		return norm.NFC.String(join(units))
	}
	// split refuses a syllable it cannot read, and what it refuses is text that
	// is damaged rather than text this stage disagrees with, so it goes on as it
	// came apart from being composed. Composing it directly is the same string
	// the decompose and recompose used to hand back, because NFC of NFD is NFC.
	return norm.NFC.String(s)
}

// settled reports whether a syllable already carries its marks inside its
// letters, which is the case the work below has nothing to do to.
//
// Two conditions. A syllable with no combining mark left in it has nothing whose
// order could be wrong, because a mark that composed into its letter is inside
// the codepoint and a codepoint has no order to get wrong. A syllable that is
// already NFC is one the decompose and recompose would hand back unchanged.
//
// It is worth asking because of what it costs not to. Over one WARC volume off a
// live crawl, 310MB holding 41,969,270 syllables, 98.9% of the ones that were
// not plain ASCII were settled, and compose over that volume went from 2,241ms
// to 267ms. On the profile that motivated it, compose was 17.8% of the whole
// crawler's CPU.
//
// It is not only a fast path, which is the part worth reading twice. The work it
// skips is wrong for a letter that is not Vietnamese. U+0303 is the Vietnamese
// ngã and it is also the tilde of ȭ, so decomposing ȭ hands split a tilde and a
// macron, split calls the tilde a tone and moves it after the macron, and what
// comes back is o with a macron and a loose tilde beside it rather than the
// letter that went in. That is a different character. It happened 91 times in
// that volume and [TestALetterThatIsNotVietnameseKeepsItsMarks] holds the fix
// down.
func settled(s string) bool {
	for _, c := range s {
		if unicode.Is(unicode.Mn, c) {
			return false
		}
	}
	return norm.NFC.IsNormalString(s)
}

// retone moves the tone mark of a syllable to where gao writes it, and reports
// whether it moved anything.
func retone(s string) (string, bool) {
	if !mayRetone(s) {
		return s, false
	}
	units, ok := split(Decomposed(s))
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
//
// It was not cheap. It was written as a walk over norm.NFD.String(s), which is
// the decomposition it exists to avoid, and it paid for one on every non-ASCII
// syllable of every page in order to answer a question about five codepoints. On
// a live crawl that made it the largest single source of garbage in the whole
// program: an allocation profile of server2 mid-run had norm.Form.String holding
// 34.68 GB of the 139.01 GB the process had ever allocated, a quarter of every
// byte, reached from here and from [retone]. Around 40% of that box's CPU was
// going to the collector, so the string this function threw away was being paid
// for twice.
//
// The question does not need the string. A tone mark is in the decomposition of
// a syllable when some rune of it either is one or decomposes to one, and
// [norm.Properties.Decomposition] hands back the decomposition of a single rune
// as a slice of the package's own tables rather than as a new string. So the
// walk is over the syllable as it arrived, and the only thing consulted per rune
// is a table this program does not own and does not copy.
//
// ASCII is skipped without asking, because no ASCII rune decomposes to anything
// and a Vietnamese page carries a great many of them. Hangul is the one case
// where Decomposition returns nothing although the rune does decompose, since
// that decomposition is arithmetic rather than a table, and it is correct to
// skip: a Hangul syllable has no Vietnamese tone in it.
func mayRetone(s string) bool {
	for i := 0; i < len(s); {
		c, w := utf8.DecodeRuneInString(s[i:])
		if c < utf8.RuneSelf {
			i++
			continue
		}
		if tone(c) {
			return true
		}
		d := norm.NFD.PropertiesString(s[i:]).Decomposition()
		for j := 0; j < len(d); {
			r, n := utf8.DecodeRune(d[j:])
			if tone(r) {
				return true
			}
			j += n
		}
		i += w
	}
	return false
}

// split takes a decomposed syllable apart into letters and their marks. It
// refuses a syllable that starts with a combining mark or carries two tones,
// because both mean the text is damaged in a way this stage does not repair.
//
// It reads the decomposition rather than a decomposed string so that the caller
// never has to make one: see [Decomposed].
func split(seq iter.Seq[rune]) ([]unit, bool) {
	var units []unit
	for c := range seq {
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
