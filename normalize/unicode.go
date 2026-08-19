package normalize

// The characters that are wrong rather than variant, and the ones that are not
// there at all.

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// homoglyphs is the repair table for the characters that are wrong wherever they
// appear. Eth for the d with stroke is the most common single error in
// Vietnamese text, and it comes from fonts and from transliteration tables that
// had a slot for the letter and put the wrong codepoint in it. The caron for the
// breve comes from tooling built for Pinyin, where the caron is a tone mark and
// the breve is not. Neither is a spelling anybody chose and neither is a letter
// of any language this corpus holds.
var homoglyphs = map[rune]rune{
	'\u00f0': '\u0111', '\u00d0': '\u0110', // eth
	'\u01ce': '\u0103', '\u01cd': '\u0102', // caron for breve
}

// confusables is the other kind: letters of another alphabet that are drawn like
// Latin ones. A page that writes "c\u043eng ty" with a Cyrillic o reads as
// Vietnamese to a person and as a different string to everything else, which is
// exactly the property whoever wrote it was after. It is search engine cloaking,
// and it is also ordinary copy and paste damage.
//
// These are folded only inside a word that is otherwise Latin, which is what
// separates the two tables. A Cyrillic o in the middle of "cong ty" is damage. A
// Cyrillic o in the middle of a Russian word is a Russian word, and a corpus that
// rewrote it would be corrupting the text it was cleaning.
var confusables = map[rune]rune{
	'\u0430': 'a', '\u0410': 'A', // Cyrillic
	'\u0435': 'e', '\u0415': 'E',
	'\u043e': 'o', '\u041e': 'O',
	'\u0440': 'p', '\u0420': 'P',
	'\u0441': 'c', '\u0421': 'C',
	'\u0443': 'y', '\u0423': 'Y',
	'\u0445': 'x', '\u0425': 'X',
	'\u0456': 'i', '\u0406': 'I',
	'\u0458': 'j', '\u0408': 'J',
	'\u0455': 's', '\u0405': 'S',
	'\u0501': 'd', '\u051b': 'q', '\u051d': 'w',
	'\u0412': 'B', '\u041a': 'K', '\u041c': 'M', '\u041d': 'H', '\u0422': 'T',

	'\u03bf': 'o', '\u039f': 'O', // Greek
	'\u0391': 'A', '\u0392': 'B', '\u0395': 'E', '\u0396': 'Z', '\u0397': 'H', '\u0399': 'I',
	'\u039a': 'K', '\u039c': 'M', '\u039d': 'N', '\u03a1': 'P', '\u03a4': 'T', '\u03a5': 'Y', '\u03a7': 'X',
}

// invisible is the set of characters that occupy no width and change every
// string they sit in.
//
// A zero width space inside a syllable is not a typo that a reader corrects. It
// makes that syllable a different string from every other copy of it, forever,
// through tokenization and into the embedding table. The soft hyphen and the
// byte order mark in the middle of a document do the same thing, and all three
// arrive from word processors and content management systems rather than from
// anybody typing.
//
// The bidirectional controls are in the set for a second reason. They reorder
// what is displayed without touching what is stored, so a line can read one way
// to a person and mean another to everything that consumes it. That is a known
// attack on source code and it is the same trick in prose.
func invisible(c rune) bool {
	switch {
	case c == '\u00ad', // soft hyphen
		c == '\u034f', // combining grapheme joiner
		c == '\u180e', // Mongolian vowel separator
		c == '\u200b', // zero width space
		c == '\u200c', // zero width non-joiner
		c == '\u200d', // zero width joiner
		c == '\u200e', // left to right mark
		c == '\u200f', // right to left mark
		c == '\u2060', // word joiner
		c == '\ufeff': // byte order mark, in the middle of a document
		return true
	case c >= '\u202a' && c <= '\u202e': // the bidirectional embeddings
		return true
	case c >= '\u2066' && c <= '\u2069': // the bidirectional isolates
		return true
	}
	return false
}

// blank is the set of characters that are a space with something extra: a space
// that does not break, a space of a particular width, an ideographic space. They
// all become an ordinary space, because the extra is typography and everything
// downstream splits on whitespace.
func blank(c rune) bool {
	switch {
	case c == '\u00a0', // no-break space
		c == '\u1680', // Ogham space mark
		c == '\u202f', // narrow no-break space
		c == '\u205f', // medium mathematical space
		c == '\u3000': // ideographic space
		return true
	case c >= '\u2000' && c <= '\u200a': // en quad through hair space
		return true
	}
	return false
}

// endings is every way a line has ever been ended, mapped to the one gao writes.
// The last two come from word processors and from PDF extraction, and a reader
// that splits on a newline treats a document full of them as one line.
var endings = strings.NewReplacer(
	"\r\n", "\n",
	"\r", "\n",
	"\u2028", "\n", // line separator
	"\u2029", "\n", // paragraph separator
	"\u0085", "\n", // next line, from the old mainframe encodings
)

// lineEndings brings every line ending to a newline before anything counts a
// character. None of these is damage and none of them is counted as damage.
func lineEndings(text string) string {
	if !strings.ContainsAny(text, "\r\u2028\u2029\u0085") {
		return text
	}
	return endings.Replace(text)
}

// repair fixes the characters that are wrong and removes the ones that are not
// characters, counting each kind as it goes.
func (r *Result) repair(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, c := range text {
		switch {
		case invisible(c):
			r.Invisible++
		case c == '\t' || c == '\n':
			b.WriteRune(c)
		case unicode.IsControl(c):
			r.Controls++
		case blank(c):
			b.WriteRune(' ')
		default:
			if to, ok := homoglyphs[c]; ok {
				r.Homoglyphs++
				b.WriteRune(to)
				continue
			}
			if to, ok := narrow(c); ok {
				r.Homoglyphs++
				b.WriteRune(to)
				continue
			}
			b.WriteRune(c)
		}
	}
	return b.String()
}

// unmix folds the look-alike letters back, in the words where they are
// look-alikes.
//
// The test is the word rather than the character. A word with Latin letters in
// it and a Cyrillic o in the middle of them is a Vietnamese word somebody
// tampered with or copied badly, and the o is repaired. A word with no Latin
// letters in it is a word of another language, and this stage does not touch it,
// because a Vietnamese page that quotes a line of Russian is a page that quotes a
// line of Russian.
//
// It runs after repair, so the fullwidth letters have already come back to ASCII
// and count as Latin here.
func (r *Result) unmix(text string) string {
	if !anyConfusable(text) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		c, w := utf8.DecodeRuneInString(text[i:])
		if !unicode.IsLetter(c) {
			b.WriteRune(c)
			i += w
			continue
		}
		j := i + w
		for j < len(text) {
			c, w := utf8.DecodeRuneInString(text[j:])
			if !unicode.IsLetter(c) {
				break
			}
			j += w
		}
		r.word(&b, text[i:j])
		i = j
	}
	return b.String()
}

// word writes one run of letters, folding the look-alikes in it if the run is
// otherwise Latin.
func (r *Result) word(b *strings.Builder, s string) {
	latin, mixed := false, false
	for _, c := range s {
		switch {
		case unicode.Is(unicode.Latin, c):
			latin = true
		case confusables[c] != 0:
			mixed = true
		}
	}
	if !latin || !mixed {
		b.WriteString(s)
		return
	}
	for _, c := range s {
		if to, ok := confusables[c]; ok {
			r.Homoglyphs++
			b.WriteRune(to)
			continue
		}
		b.WriteRune(c)
	}
}

func anyConfusable(text string) bool {
	for _, c := range text {
		if _, ok := confusables[c]; ok {
			return true
		}
	}
	return false
}

// narrow folds a fullwidth ASCII character back to ASCII. They arrive from input
// methods built for Chinese, Japanese and Korean, where the width is meaningful,
// and in a Vietnamese document they are a fullwidth comma in a sentence of
// ordinary ones.
func narrow(c rune) (rune, bool) {
	if c >= '\uff01' && c <= '\uff5e' {
		return c - 0xfee0, true
	}
	return 0, false
}

// layout settles the whitespace, and it runs last because everything above it
// removes characters and leaves the spaces that were around them.
//
// Paragraph boundaries survive because a later stage deduplicates on them. Runs
// of spaces collapse to one, trailing whitespace goes, a run of blank lines
// becomes one blank line, and the document ends with exactly one newline.
func layout(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blanks := 0
	for _, line := range lines {
		line = strings.TrimRight(collapse(line), " \t")
		if line == "" {
			blanks++
			continue
		}
		if blanks > 0 && len(out) > 0 {
			out = append(out, "")
		}
		blanks = 0
		out = append(out, line)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// collapse turns every run of spaces and tabs inside a line into one space, and
// drops the run at the start of a line. Indentation in extracted text is a
// leftover of the markup it came out of rather than something about the
// sentence, and a later stage that compares paragraphs would see two of them.
func collapse(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	space := false
	for _, c := range line {
		if c == ' ' || c == '\t' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(c)
	}
	return b.String()
}
