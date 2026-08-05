package sang

// The Vietnamese syllable, which is a closed set.

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/tamnd/gao/phoi"
)

// Vietnamese is unusual among the languages a crawl turns up in that its
// syllables can be listed. A syllable is an onset from a list of 27, a rhyme
// from a list of about 180, and one of six tones, and the spelling of each part
// is fixed by rules that have not moved since the orthography settled. Nothing
// else is a Vietnamese syllable. Not a syllable with two consonants at the end,
// because the language has none. Not one ending in s or l or r or f, because the
// coda is one of eight sounds and none of them is those. Not one written with
// k before a back vowel, because the orthography spells that sound c there.
//
// That is worth a great deal here. Every published language identifier answers
// which of a hundred and seventy six languages a document is in, from character
// n-grams, and a document that is short or unusual gets an answer from whichever
// language happened to score highest. The question this pipeline actually asks
// is narrower and has an exact answer: are these Vietnamese syllables. Listing
// them turns identification into a lookup, and a lookup does not degrade on a
// short document or on a register the model did not see.
//
// The inventory below is generated from the parts rather than written out, so
// that a mistake is one line rather than one entry, and every syllable in every
// document this repository keeps is checked against it by a test. It
// over-generates: bôn and quôn are formed here and neither is a word. That is
// the safe direction. A syllable inventory that is missing a rhyme rejects real
// Vietnamese, and one that admits a spelling nobody uses costs a fraction of a
// point of precision on text that has to fail the other checks anyway.

// onsets is every initial the orthography writes, plus the empty one.
//
// q is not among them because it never appears without u, and qu is handled on
// its own further down: what follows it is written as though the glide were not
// there, which is why quan and hoan rhyme and are spelled with different
// letters.
var onsets = []string{
	"", "b", "c", "ch", "d", "đ", "g", "gh", "gi", "h", "k", "kh", "l", "m",
	"n", "ng", "ngh", "nh", "p", "ph", "r", "s", "t", "th", "tr", "v", "x",
}

// plainRhymes is every rhyme that begins with its own vowel.
var plainRhymes = []string{
	"a", "ac", "ach", "ai", "am", "an", "ang", "anh", "ao", "ap", "at", "au", "ay",
	"ăc", "ăm", "ăn", "ăng", "ăp", "ăt",
	"âc", "âm", "ân", "âng", "âp", "ât", "âu", "ây",
	"e", "ec", "em", "en", "eng", "eo", "ep", "et",
	"ê", "êch", "êm", "ên", "ênh", "êp", "êt", "êu",
	"i", "ich", "im", "in", "inh", "ip", "it", "iu",
	"ia", "iêc", "iêm", "iên", "iêng", "iêp", "iêt", "iêu",
	"o", "oc", "oi", "om", "on", "ong", "op", "ot", "ooc", "oong",
	"ô", "ôc", "ôi", "ôm", "ôn", "ông", "ôp", "ôt",
	"ơ", "ơi", "ơm", "ơn", "ơp", "ơt",
	"u", "uc", "ui", "um", "un", "ung", "up", "ut",
	"ua", "uôc", "uôi", "uôm", "uôn", "uông", "uôt",
	"ư", "ưc", "ưi", "ưm", "ưng", "ưt", "ưu",
	"ưa", "ươc", "ươi", "ươm", "ươn", "ương", "ươp", "ươt", "ươu",
	"y", "yêm", "yên", "yêng", "yêt", "yêu",
}

// glideRhymes is the rhymes that begin with the glide, which is written o
// before a and ă and e, and u before everything else. It is the same sound in
// both cases and the spelling is decided by what follows it, which is one of the
// rules that makes an inventory cheaper than a model.
var glideRhymes = []string{
	"oa", "oac", "oach", "oai", "oam", "oan", "oang", "oanh", "oao", "oap", "oat", "oay",
	"oăc", "oăm", "oăn", "oăng", "oăt",
	"oe", "oec", "oem", "oen", "oeo", "oet",
	"uâ", "uân", "uâng", "uât", "uây",
	"uê", "uêch", "uên", "uênh",
	"uơ",
	"uy", "uya", "uych", "uyn", "uynh", "uyp", "uyt", "uyu",
	"uyên", "uyêt",
}

// quRhymes is what may follow qu.
//
// The glide is already spelled by the u of qu, so what comes after it is a plain
// rhyme: quan and hoan are the same rhyme written twice. The four y forms at the
// end are the exception, because i after qu is written y in quýt and quynh and
// quyên the way it is written y after the glide everywhere else.
var quRhymes = append(append([]string{}, plainRhymes...), "yt", "ych", "yn", "ynh")

// front reports whether a rhyme begins with a front vowel, which is what decides
// between c and k, g and gh, ng and ngh. The rule is exceptionless in Vietnamese
// spelling and it is one of the cheapest tests there is for text that was
// generated to look Vietnamese rather than written in it.
func front(rhyme string) bool {
	switch []rune(rhyme)[0] {
	case 'i', 'e', 'ê', 'y':
		return true
	}
	return false
}

// syllables is every spelling the parts above form, without its tone.
var syllables = buildSyllables()

func buildSyllables() map[string]bool {
	out := make(map[string]bool, 8192)
	add := func(onset, rhyme string) {
		// gi already ends in the i the rhyme begins with, and the orthography
		// writes one of them rather than two: gì and gìn and giếng are gi with
		// the rhymes i and in and iêng.
		if onset == "gi" && strings.HasPrefix(rhyme, "i") {
			rhyme = rhyme[len("i"):]
		}
		out[onset+rhyme] = true
	}

	for _, onset := range onsets {
		for _, rhyme := range slices.Concat(plainRhymes, glideRhymes) {
			switch onset {
			case "k", "gh", "ngh":
				// These three spell a sound that is written c, g and ng
				// everywhere else, and they are written only before a front
				// vowel.
				if !front(rhyme) {
					continue
				}
			case "c", "g", "ng":
				if front(rhyme) {
					continue
				}
			case "":
				// A zero onset writes the ia diphthong as yê, so yên and yêu
				// are syllables and iên and iêu on their own are not.
				if strings.HasPrefix(rhyme, "iê") {
					continue
				}
			}
			add(onset, rhyme)
		}
	}
	for _, rhyme := range quRhymes {
		add("qu", rhyme)
	}
	return out
}

// The five tone marks, as the combining characters a decomposed syllable holds.
// The sixth tone is the absence of all of them.
const (
	grave  = '̀' // huyền
	acute  = '́' // sắc
	hook   = '̉' // hỏi
	tilde  = '̃' // ngã
	dot    = '̣' // nặng
	noTone = rune(0)
)

// stops is the codas that close a syllable in a consonant the voice stops on.
// A syllable ending in one of them carries the rising or the heavy tone and can
// carry no other, which is a fact about the language rather than a convention,
// and it holds in text typed by somebody who has never heard of it.
var stops = []string{"p", "t", "c", "ch"}

// Syllable reports whether a token is a Vietnamese syllable, written with its
// tone mark.
//
// It answers on the spelling alone and it is exact in both directions for text
// that carries its marks. Text that does not is a different question, and
// [BareSyllable] is the one that answers it.
func Syllable(tok string) bool {
	tok = strings.ToLower(tok)
	bare, tone, ok := untone(tok)
	if !ok || !syllables[bare] {
		return false
	}
	switch tone {
	case acute, dot, noTone:
		return true
	}
	for _, coda := range stops {
		if strings.HasSuffix(bare, coda) {
			return false
		}
	}
	return true
}

// BareSyllable reports whether a token is a Vietnamese syllable once the tone
// marks are taken off both sides.
//
// This is the register a phone types in, and most identifiers get it wrong,
// because unmarked Vietnamese looks to a character n-gram model like whichever
// language it saw most of that writes short syllables in Latin letters. It is
// still Vietnamese and it is a large share of everything written since the
// smartphone. The cost of asking the question this way is real and worth
// stating: dan and cam and man are Vietnamese syllables unmarked, and they are
// also words in several other languages, so this test alone admits more than it
// should and is never used alone.
func BareSyllable(tok string) bool {
	return bareSyllables[phoi.Bare(strings.ToLower(tok))]
}

var bareSyllables = func() map[string]bool {
	out := make(map[string]bool, len(syllables))
	for s := range syllables {
		out[phoi.Bare(s)] = true
	}
	return out
}()

// untone splits a syllable into its spelling without a tone mark and the mark it
// carried. It reports false for anything that is not letters, or that carries
// more than one tone mark, which no syllable does.
//
// The circumflex of ê, the breve of ă and the horn of ơ stay where they are.
// They are part of the letter rather than the tone, and a syllable that lost
// them would be a different syllable.
func untone(tok string) (string, rune, bool) {
	if tok == "" {
		return "", 0, false
	}
	var b strings.Builder
	tone := noTone
	for _, c := range norm.NFD.String(tok) {
		switch c {
		case grave, acute, hook, tilde, dot:
			if tone != noTone {
				return "", 0, false
			}
			tone = c
			continue
		}
		if !unicode.IsLetter(c) && !unicode.Is(unicode.Mn, c) {
			return "", 0, false
		}
		b.WriteRune(c)
	}
	return norm.NFC.String(b.String()), tone, true
}

// Syllables returns the inventory, without tone marks and sorted, so that a
// test or a report can count it or print it rather than describe it.
func Syllables() []string {
	out := make([]string, 0, len(syllables))
	for s := range syllables {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}
