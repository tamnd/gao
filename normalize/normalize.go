// Package normalize normalizes Vietnamese text. It is the first stage of the
// cleaning pipeline and it runs before anything else reads a character.
//
// Normalization is not tidying. Every stage after this one compares strings:
// language identification counts characters, deduplication hashes shingles,
// decontamination matches n-grams against benchmark text, and the PII detectors
// are regular expressions. Unnormalized Vietnamese breaks all of them, and it
// breaks them quietly. "hoà" and "hòa" are the same word under two tone mark
// conventions, and left alone they hash differently, so a document that arrived
// from two sources under two conventions survives deduplication as two
// documents, trains as two, and occupies two rows of the embedding table. A
// zero width space inside a syllable does the same thing forever.
//
// So this stage is a correctness problem rather than a cosmetic one, and the
// rules are written down one at a time with the cases where each one must not
// fire. What it deliberately does not do is as load bearing as what it does.
// Input method residue is flagged and left alone, because repairing it is
// guessing at what somebody meant to type. The i and y spellings are both kept,
// because one of the pair is often a proper noun and collapsing them changes
// what the text says.
package normalize

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ControlLimit is the share of control characters above which a document is
// almost always a binary that survived a content type sniff rather than text
// with damage in it.
const ControlLimit = 0.001

// ResidueLimit is the share of syllables that may look like input method
// residue before the document is not worth keeping.
//
// It is a default rather than a truth. A forum post with one broken word is
// still a good forum post, and a page where one word in fifty came out as
// keystrokes is not text anybody meant to publish. The ablation harness turns
// this knob like every other threshold in the pipeline.
const ResidueLimit = 0.02

// Result is a normalized document and an account of what had to be done to it.
//
// The counts are not diagnostics. A corpus that says how much of it was repaired
// and where can be argued with, and one that says "normalized" cannot.
type Result struct {
	// Text is the document as every later stage will see it.
	Text string

	// Changed says whether the document differs from what came in by at least
	// one byte.
	//
	// Nearly every document does. Layout runs on all of them and a document that
	// arrived without a final newline leaves with one, so this counts what the
	// stage touched rather than what it found, and [Result.Repaired] is the half
	// of it that is about the text. Both are worth having: this one is what a
	// hash taken before this stage is worth, which is nothing.
	Changed bool

	// Legacy names the font encoding the document was written in, for the few
	// that were written in one, and is empty for everything else.
	Legacy string

	// Transcoded is how many letters were read out of that encoding.
	Transcoded int

	// Homoglyphs is characters that were wrong rather than variant: eth for đ,
	// a caron where a breve belongs, Cyrillic letters shaped like Latin ones.
	Homoglyphs int

	// Invisible is zero width characters, soft hyphens and byte order marks
	// taken out of the middle of the text.
	Invisible int

	// Controls is control characters other than tab and newline, removed.
	Controls int

	// Composed is syllables that arrived in some other Unicode composition and
	// are now in NFC.
	Composed int

	// Tones is syllables whose tone mark moved to the convention gao writes.
	Tones int

	// Residue is syllables that look like the keystrokes of a Vietnamese input
	// method rather than its output. They are counted and left alone.
	Residue int

	// Syllables is how many syllables the normalized document holds, which is
	// what ResidueRate is taken over.
	Syllables int

	// Runes is how many runes arrived, which is what ControlRate is taken over.
	// It counts the input rather than the output, because what makes a document
	// a binary is what was in it before the control characters came out.
	Runes int
}

// Repaired says the document changed by something other than its layout: a
// character was wrong, or invisible, or in the wrong composition, or carried its
// tone mark on the wrong vowel, or the whole document was in a font encoding.
//
// The distinction is the difference between a measurement and a tautology.
// Whitespace and a final newline are settled on every document that goes past,
// so the share of documents that changed at all is close to all of them and says
// nothing about the material. The share that had a character repaired says which
// upstreams produce damaged Vietnamese and how much.
func (r Result) Repaired() bool {
	return r.Homoglyphs+r.Invisible+r.Controls+r.Composed+r.Tones+r.Transcoded > 0
}

// ControlRate is the share of the incoming runes that were control characters.
func (r Result) ControlRate() float64 {
	if r.Runes == 0 {
		return 0
	}
	return float64(r.Controls) / float64(r.Runes)
}

// ResidueRate is the share of syllables that look like input method residue.
func (r Result) ResidueRate() float64 {
	if r.Syllables == 0 {
		return 0
	}
	return float64(r.Residue) / float64(r.Syllables)
}

// Normalize brings a document to the form the rest of the pipeline assumes.
//
// The order is fixed and each step depends on the one before it. A document in a
// font encoding is read out of it first, because until that is done its bytes do
// not mean what any later rule thinks they mean. Homoglyphs are repaired next,
// because an eth is not a Vietnamese letter and no composition rule will make it
// one. The look-alike letters go after that, once the fullwidth forms have come
// back to ASCII, because the test for those is whether the word around them is
// Latin. Composition and tone placement come next, on the repaired letters.
// Layout comes last, because the earlier steps remove characters and leave the
// whitespace around them behind.
func Normalize(text string) Result {
	var r Result
	r.Runes = utf8.RuneCountInString(text)

	out := layout(r.reshape(r.unmix(r.repair(lineEndings(r.transcode(text))))))
	r.Text = out
	r.Changed = out != text
	r.Syllables, r.Residue = survey(out)
	return r
}

// Markup brings a marked up document to the same form [Normalize] does, minus
// the one step that would destroy it.
//
// The characters are repaired the same way: a page in a font encoding is read
// out of it, homoglyphs and look-alikes go back to the letters they are
// impersonating, invisible and control characters come out, and every syllable
// comes to NFC with its tone mark where gao writes it. Those are all repairs to
// what a character is, and markdown does not care what its letters are.
//
// What does not run is the layout step. In extracted text the indentation is a
// leftover of the markup it came out of and collapsing it is a repair. In
// markdown the indentation is the markup: two spaces at the end of a line are a
// line break, four at the start are a nested list item, and a run of spaces
// inside a fenced block is somebody's code. Collapsing them would turn a
// document into a different document that renders wrong.
//
// The charset is detected separately from the text pass rather than handed over.
// The two can in principle disagree, since they are looking at different amounts
// of the same page, and in practice the marked up version is the longer of the
// two and so has the more evidence behind it.
func Markup(text string) string {
	var r Result
	return r.reshape(r.unmix(r.repair(lineEndings(r.transcode(text)))))
}

// reshape walks the document a syllable at a time, brings each to NFC, and puts
// the tone mark where gao writes it.
//
// Composition is done per syllable rather than over the document so that the
// count means something. A document that reports two hundred recomposed
// syllables is telling you which upstream produced it, and one that reports
// "not NFC" is telling you nothing.
func (r *Result) reshape(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		c, w := utf8.DecodeRuneInString(text[i:])
		if !inSyllable(c) {
			b.WriteRune(c)
			i += w
			continue
		}
		j := i + w
		for j < len(text) {
			c, w := utf8.DecodeRuneInString(text[j:])
			if !inSyllable(c) {
				break
			}
			j += w
		}
		r.syllable(&b, text[i:j])
		i = j
	}
	return b.String()
}

// syllable normalizes one run of letters and writes it out.
func (r *Result) syllable(b *strings.Builder, s string) {
	out := compose(s)
	if out != s {
		r.Composed++
	}
	if moved, ok := retone(out); ok {
		r.Tones++
		out = moved
	}
	b.WriteString(out)
}

// inSyllable reports whether a rune belongs to the run of letters around it: a
// letter, or a combining mark sitting on one. A decomposed syllable is a letter
// followed by its marks, and a walk that stopped at the first mark would split
// the syllable it is there to normalize.
func inSyllable(c rune) bool {
	return unicode.IsLetter(c) || unicode.Is(unicode.Mn, c)
}

// survey counts the syllables of a normalized document and how many of them look
// like input method residue.
func survey(text string) (syllables, residue int) {
	for word := range words(text) {
		if !hasLetter(word) {
			continue
		}
		syllables++
		if Residue(word) {
			residue++
		}
	}
	return syllables, residue
}

// words yields every maximal run of letters and digits.
//
// Digits are part of a word here, which they are not for counting syllables,
// because the residue this stage looks for is a digit sitting inside one.
func words(text string) func(func(string) bool) {
	return func(yield func(string) bool) {
		start, in := 0, false
		for i, c := range text {
			part := unicode.IsLetter(c) || unicode.IsDigit(c) || unicode.Is(unicode.Mn, c)
			switch {
			case part && !in:
				start, in = i, true
			case !part && in:
				if !yield(text[start:i]) {
					return
				}
				in = false
			}
		}
		if in {
			yield(text[start:])
		}
	}
}

func hasLetter(s string) bool {
	for _, c := range s {
		if unicode.IsLetter(c) {
			return true
		}
	}
	return false
}

// Tally adds up what normalization did to a run of documents.
//
// The number this exists for is the share of documents that changed at all. It
// is the one claim about this stage that can be checked from outside, and the
// project has a prediction riding on it.
type Tally struct {
	Documents  int64 `json:"documents"`
	Changed    int64 `json:"changed"`
	Repaired   int64 `json:"repaired"`
	Homoglyphs int64 `json:"homoglyphs"`
	Invisible  int64 `json:"invisible"`
	Controls   int64 `json:"controls"`
	Composed   int64 `json:"composed"`
	Tones      int64 `json:"tones"`
	Residue    int64 `json:"residue"`
	Syllables  int64 `json:"syllables"`
	Rejected   int64 `json:"rejected"`

	// Legacy is how many documents came out of each font encoding, by the name
	// of the encoding. It is a breakdown rather than a total because which
	// encodings a source turns out to be full of says when and where its pages
	// were written, and a source with none of them is a source that was crawled
	// after 2010.
	Legacy map[string]int64 `json:"legacy,omitempty"`
}

// Add folds one document's result into the tally.
func (t *Tally) Add(r Result) {
	t.Documents++
	if r.Changed {
		t.Changed++
	}
	if r.Repaired() {
		t.Repaired++
	}
	if _, ok := Reject(r); ok {
		t.Rejected++
	}
	if r.Legacy != "" {
		if t.Legacy == nil {
			t.Legacy = map[string]int64{}
		}
		t.Legacy[r.Legacy]++
	}
	t.Homoglyphs += int64(r.Homoglyphs)
	t.Invisible += int64(r.Invisible)
	t.Controls += int64(r.Controls)
	t.Composed += int64(r.Composed)
	t.Tones += int64(r.Tones)
	t.Residue += int64(r.Residue)
	t.Syllables += int64(r.Syllables)
}

// ChangedShare is the share of documents normalization changed by at least one
// byte, between 0 and 1.
func (t Tally) ChangedShare() float64 {
	if t.Documents == 0 {
		return 0
	}
	return float64(t.Changed) / float64(t.Documents)
}

// RepairedShare is the share of documents that changed by something other than
// their layout, between 0 and 1.
func (t Tally) RepairedShare() float64 {
	if t.Documents == 0 {
		return 0
	}
	return float64(t.Repaired) / float64(t.Documents)
}
