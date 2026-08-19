package normalize

// Vietnamese that was typed before Unicode.
//
// Between about 1993 and 2005 Vietnamese was written with fonts rather than with
// an encoding. A page picked one of a dozen eight bit charsets, shipped the font
// that drew it, and the bytes in the file meant nothing without it. TCVN3 is the
// one the .VnTime fonts are drawn for and it is most of that period's text,
// VNI-Windows is most of the rest, VPS is what the diaspora press used, and the
// two BK HCM encodings are what came out of the university tooling in the south.
// None of it is Unicode and all of it is still on the web, in the archives the
// older half of a Vietnamese corpus comes out of.
//
// It does not arrive here as bytes. A crawler that meets a page of TCVN3 finds
// it declared as windows-1252, or declared as nothing and sniffed, and the bytes
// come out the other side as the characters that encoding draws them as. That is
// why the damage has a look: "TiÕng ViÖt" is TCVN3 for "Tiếng Việt" seen through
// windows-1252, and anybody who reads Vietnamese has seen it a thousand times.
// So the first thing this stage does is put the characters back to the bytes
// they were made of, which windows-1252 and Latin-1 both allow because neither
// of them throws anything away on the way in.
//
// Then it has to pick the encoding, and it has to be right. All six occupy the
// same byte range and every one of them decodes every high byte to some
// Vietnamese letter, so the wrong choice does not produce nonsense that somebody
// would notice on the way past. It produces fluent looking Vietnamese made of
// words that do not exist, in a corpus nobody is going to read. The test is
// therefore not whether the result is Vietnamese letters, which it always is,
// but whether it is Vietnamese words: a document is transcoded when one encoding
// turns it into text with the common function words in it and the other five do
// not, by a margin. A document that two encodings can read, or that is too short
// to tell, is left exactly as it arrived and counted as nothing.

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// MinLegacyWords is how many distinct Vietnamese function words a decoding has
// to produce before this stage will believe it.
//
// Three is low as a bar for Vietnamese and high as a bar for a coincidence. The
// encodings here are near enough to permutations of one another that reading a
// document under the wrong one scrambles every marked word in it, so the wrong
// answer scores zero on nearly every document and one on the occasional
// accident. What three really buys is protection from the short document, where
// the right answer scores two and the argument for it is thin.
const MinLegacyWords = 3

// MinLegacyBytes is how many characters have to come back to a byte of 0x80 or
// above before it is worth decoding the document six ways to find out.
//
// It is a cost floor rather than evidence. A document with a dozen accented
// characters in it cannot reach [MinLegacyWords] under any encoding, so running
// the detector over it would only be a way of spending time on the great
// majority of pages that are not legacy encoded at all.
const MinLegacyBytes = 16

// LegacySample is how much of a document the decision is taken over, in bytes.
//
// The whole document is transcoded once the decision is made. It is only the
// decision that is capped, because a page is written in one encoding from top to
// bottom, and sixty four kilobytes of it is several hundred function words,
// which is two orders of magnitude more evidence than the answer needs.
const LegacySample = 1 << 16

// A Charset is one legacy Vietnamese font encoding.
type Charset struct {
	name   string
	single map[byte]rune
	pairs  map[[2]byte]rune
}

// Name is what the encoding is called, in the spelling the fonts and the input
// methods use for it.
func (c *Charset) Name() string { return c.name }

// charsets is every encoding this stage reads. The order does not decide
// anything, because a winner has to beat the runner up rather than come first.
var charsets = []*Charset{tcvn3, vniWin, vps, viscii, bkhcm1, bkhcm2}

// Charsets returns the encodings this stage can read.
func Charsets() []*Charset { return slices.Clone(charsets) }

// Decode reads a document out of the encoding, and returns it along with how
// many letters it recovered.
//
// It touches nothing below 0x80. Two of these encodings do put letters down
// there, on punctuation ASCII has other plans for, and reading those would mean
// a document this stage was wrong about came out with its braces turned into
// vowels. Losing a capital on a page that really was BK HCM1 is the smaller of
// the two wrongs and it is the one taken here.
//
// A high byte the encoding does not use is left as the character it arrived as.
// That happens on a real page, because a document in a font encoding still gets
// a curly quote pasted into it from somewhere else, and dropping the character
// would be repairing something that is not broken.
func (c *Charset) Decode(text string) (string, int) {
	var b strings.Builder
	b.Grow(len(text))
	letters := 0
	for i := 0; i < len(text); {
		r, w := utf8.DecodeRuneInString(text[i:])
		base, ok := legacyByte(r)
		if !ok {
			b.WriteRune(r)
			i += w
			continue
		}
		if len(c.pairs) > 0 && i+w < len(text) {
			next, nw := utf8.DecodeRuneInString(text[i+w:])
			if mark, ok := legacyByte(next); ok {
				if letter, ok := c.pairs[[2]byte{base, mark}]; ok {
					b.WriteRune(letter)
					letters++
					i += w + nw
					continue
				}
			}
		}
		if letter, ok := c.single[base]; ok {
			b.WriteRune(letter)
			letters++
			i += w
			continue
		}
		b.WriteRune(r)
		i += w
	}
	return b.String(), letters
}

// Detect reports the legacy encoding a document was written in, or nil when it
// was not written in one, which is what nearly every document was.
//
// The margin at the end is the whole design. Six encodings are tried, the best
// has to have found real Vietnamese words and to have found at least twice as
// many of them as the second best, and anything short of that is a document this
// stage leaves alone. That is the precision first trade the rest of this package
// makes: a page left in mojibake is a page a later filter throws away, and a
// page transcoded under the wrong table is invented Vietnamese sitting in the
// corpus with nothing marking it.
func Detect(text string) *Charset {
	text = sample(text)
	if !mayBeLegacy(text) {
		return nil
	}
	var best *Charset
	first, second := 0, 0
	for _, c := range charsets {
		decoded, letters := c.Decode(text)
		if letters == 0 {
			continue
		}
		switch words := legacyWords(decoded); {
		case words > first:
			best, first, second = c, words, first
		case words > second:
			second = words
		}
	}
	if first < MinLegacyWords || first < 2*second {
		return nil
	}
	return best
}

// mayBeLegacy is the cheap test that keeps the six decodings off the documents
// that cannot need them.
//
// The second condition is the interesting one. A document that already holds a
// Vietnamese letter Unicode has and windows-1252 does not is a document that was
// already decoded correctly, and this stage does not touch it even if the rest
// of it looks like a font encoding. A page that mixes the two is left alone for
// the same reason: transcoding it would rewrite the half that was already right,
// because é and ô are ordinary characters in Unicode Vietnamese and they are
// also ordinary bytes in every encoding here.
func mayBeLegacy(text string) bool {
	high := 0
	for _, c := range text {
		if unicodeVietnamese(c) {
			return false
		}
		if b, ok := legacyByte(c); ok && b >= 0x80 {
			high++
		}
	}
	return high >= MinLegacyBytes
}

// unicodeVietnamese reports whether a character is a Vietnamese letter that no
// reading of a single byte encoding could have produced.
//
// The Latin-1 vowels are deliberately not in it. à and é and ô are Vietnamese
// letters, and they are also what windows-1252 makes of bytes that four of these
// encodings use for something else, so their presence says nothing either way.
// What says something is the letters Unicode had to add: ă, đ, ơ, ư, and the
// whole Latin Extended Additional block the tone marks live in.
func unicodeVietnamese(c rune) bool {
	switch c {
	case 'Ă', 'ă', 'Đ', 'đ', 'Ơ', 'ơ', 'Ư', 'ư':
		return true
	}
	return c >= 'Ạ' && c <= 'ỹ'
}

// legacyByte puts a character back to the byte it was made of.
//
// Everything below U+0100 is its own byte, which covers ASCII, the Latin-1
// letters, and the C1 controls a Latin-1 reading turns 0x80 to 0x9f into.
// Everything else that has a byte is one of the twenty seven characters
// windows-1252 puts in that range instead.
func legacyByte(c rune) (byte, bool) {
	if c < 0x100 {
		return byte(c), true
	}
	b, ok := cp1252[c]
	return b, ok
}

// sample cuts a document down to the part the decision is taken over, on a
// character boundary.
func sample(text string) string {
	if len(text) <= LegacySample {
		return text
	}
	text = text[:LegacySample]
	for len(text) > 0 {
		r, w := utf8.DecodeLastRuneInString(text)
		if r == utf8.RuneError && w <= 1 {
			text = text[:len(text)-1]
			continue
		}
		break
	}
	return text
}

// legacyWords is how many distinct words of [evidence] a decoding produced.
//
// Distinct rather than total, so that a page which repeats one word four hundred
// times counts once for it. The evidence for an encoding is how much of the
// language it recovered, not how long the document was.
func legacyWords(text string) int {
	seen := map[string]bool{}
	for word := range words(text) {
		w := strings.ToLower(word)
		if evidence[w] {
			seen[w] = true
		}
	}
	return len(seen)
}

// evidence is the Vietnamese function words the detector looks for, written with
// their marks on.
//
// The marks are the point. sift has a list like this one and matches it with
// the marks taken off, because a great deal of Vietnamese is typed without
// them and that register is still Vietnamese. Here the opposite is true: an
// unmarked word is spelled the same under all six encodings and under none of
// them, so it can tell them apart from nothing. Every word below carries at
// least one letter that only exists in the document because an encoding put it
// there, which is what makes counting them a measurement of the encoding
// rather than of the language.
//
// They are common words and they are short, and the list is long enough that a
// page of any real length hits several of them. A page that hits none is a page
// this stage cannot speak for, which is a good deal better than a page it
// transcoded on a hunch.
var evidence = map[string]bool{
	"các": true, "cả": true, "có": true, "chỉ": true, "của": true,
	"cùng": true, "cũng": true, "đã": true, "để": true, "đến": true,
	"đó": true, "được": true, "hơn": true, "hoặc": true, "là": true,
	"lại": true, "làm": true, "một": true, "mình": true, "này": true,
	"năm": true, "nếu": true, "nói": true, "nữa": true, "người": true,
	"nhưng": true, "những": true, "không": true, "ông": true, "ở": true,
	"phải": true, "rất": true, "sẽ": true, "sự": true, "thì": true,
	"thể": true, "tại": true, "trên": true, "trước": true, "từ": true,
	"vào": true, "về": true, "vẫn": true, "vì": true, "việc": true,
	"với": true, "xã": true, "còn": true,
}

// transcode reads the document out of a legacy font encoding, when it turns out
// to be in one.
//
// It runs before everything else in this package. Two of these encodings put
// letters in 0x80 to 0x9f, which a Latin-1 reading turns into control
// characters, and the stage that strips control characters would take them out
// before anybody could tell they were the letter ộ. The same goes for the byte
// 0x85, which the line ending rules turn into a newline and which VPS and VISCII
// both use.
func (r *Result) transcode(text string) string {
	c := Detect(text)
	if c == nil {
		return text
	}
	out, letters := c.Decode(text)
	r.Legacy, r.Transcoded = c.Name(), letters
	return out
}
