package dau

// The baseline that has to be beaten before a result means anything.

import (
	"sort"
	"strings"
	"unicode"

	"github.com/tamnd/gao/phoi"
)

// A Lexicon answers every bare syllable with the marked spelling it most often
// has, counted off text, with no context at all.
//
// This is the floor of the task. Most bare syllables have one common answer:
// nguoi is nearly always người and khong is nearly always không, and a table
// gets both right without knowing anything. What a table cannot do is choose
// between ma, má, mà, mả, mã and mạ, which is the part of Vietnamese
// restoration that requires reading the sentence.
//
// So the gap between a model and this table is the model's whole contribution,
// and a model that does not clear it has learned the dictionary. Publishing the
// result without publishing this number leaves the reader unable to tell those
// two apart, which is why [Build] is in this package rather than in a script
// somebody keeps locally.
//
// The counting text has to be text the task set was not built from. The table
// is trivially perfect on the pages it was counted over, and a baseline
// measured on its own training data is the same mistake as a benchmark measured
// on the model's.
type Lexicon struct {
	best  map[string]string
	count map[string]map[string]int
}

// NewLexicon returns an empty lexicon.
func NewLexicon() *Lexicon {
	return &Lexicon{
		best:  map[string]string{},
		count: map[string]map[string]int{},
	}
}

// Add counts the syllables of one document, and reports whether it counted it.
//
// A document typed without its marks is refused, by the same floor [Builder]
// selects items with. This is not tidiness. Roughly half the Vietnamese online
// is typed bare, and every bare occurrence of co in it is evidence, to a
// counter, that the right spelling of co is co. Feed that in and the table
// learns to answer the question with the question.
//
// Within a document that is properly marked, a syllable that carries no marks
// is real evidence and is counted. Co and có are both words, and a table that
// only counted the marked one would answer có every time.
//
// Everything is folded to lower case. Vietnamese capitalizes at the start of a
// sentence and in names, so counting Hà and hà apart splits the evidence for
// one word across two entries and makes the commonest spelling depend on where
// in a sentence it happened to appear.
func (l *Lexicon) Add(text string) bool {
	if markedShare(text) < Default().MinMarked {
		return false
	}
	for _, s := range syllables(text) {
		s = strings.ToLower(s)
		bare := phoi.Bare(s)
		if l.count[bare] == nil {
			l.count[bare] = map[string]int{}
		}
		l.count[bare][s]++
	}
	l.best = nil
	return true
}

// Size is how many bare syllables the lexicon has an answer for.
func (l *Lexicon) Size() int { return len(l.count) }

// Restore answers a prompt, one syllable at a time.
//
// A syllable the table has never seen comes back unchanged, which is the honest
// answer and also the right one often enough: plenty of Vietnamese syllables
// carry no marks.
func (l *Lexicon) Restore(prompt string) string {
	l.settle()
	var b strings.Builder
	b.Grow(len(prompt))
	start, in := 0, false
	for i, r := range prompt {
		if unicode.IsLetter(r) {
			if !in {
				start, in = i, true
			}
			continue
		}
		if in {
			b.WriteString(l.one(prompt[start:i]))
			in = false
		}
		b.WriteRune(r)
	}
	if in {
		b.WriteString(l.one(prompt[start:]))
	}
	return b.String()
}

// one answers a single syllable, keeping whatever case it arrived in.
func (l *Lexicon) one(s string) string {
	want, ok := l.best[strings.ToLower(s)]
	if !ok {
		return s
	}
	return recase(s, want)
}

// settle picks the commonest spelling for each bare form.
//
// Ties are broken by the spelling itself rather than by whichever the map
// happened to yield, because a lexicon that answers differently on two runs
// over the same text is not a baseline anybody can publish a number against.
func (l *Lexicon) settle() {
	if l.best != nil {
		return
	}
	l.best = make(map[string]string, len(l.count))
	for bare, forms := range l.count {
		keys := make([]string, 0, len(forms))
		for k := range forms {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if forms[keys[i]] != forms[keys[j]] {
				return forms[keys[i]] > forms[keys[j]]
			}
			return keys[i] < keys[j]
		})
		l.best[bare] = keys[0]
	}
}

// recase puts the answer into the case the question was asked in.
//
// The question is bare and the answer is lower case, so the marks are on the
// answer and the capitals are on the question, and both have to survive. The
// two strings have the same number of characters because taking marks off a
// Vietnamese letter never changes how many there are.
func recase(asked, answer string) string {
	a, w := []rune(asked), []rune(answer)
	if len(a) != len(w) {
		return answer
	}
	out := make([]rune, len(w))
	for i, r := range w {
		if unicode.IsUpper(a[i]) {
			out[i] = unicode.ToUpper(r)
			continue
		}
		out[i] = r
	}
	return string(out)
}
