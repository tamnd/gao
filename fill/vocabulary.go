package fill

// The frequency ranking the distractors are drawn from.

import (
	"slices"
	"strings"
	"unicode"

	"github.com/tamnd/gao/normalize"
)

// A Vocabulary is how often each syllable appears, and the ranking that comes
// out of it.
//
// It is the whole of what this benchmark knows about Vietnamese. The function
// words it refuses to blank are the top of this ranking, and the distractors it
// offers are the syllables nearest the answer in it, so a table that was counted
// over the wrong text produces a benchmark that is wrong in both directions at
// once.
//
// The counting text has to be text the item set was not built from, for the
// reason dau's lexicon gives: a table is trivially perfect on the pages it was
// counted over, and a distractor drawn from a ranking that saw this passage is
// a distractor chosen with the answer in view.
type Vocabulary struct {
	count  map[string]int
	ranked []string
	rank   map[string]int
}

// NewVocabulary returns an empty vocabulary.
func NewVocabulary() *Vocabulary {
	return &Vocabulary{count: map[string]int{}}
}

// Add counts the syllables of one document, and reports whether it counted it.
//
// A document typed without its marks is refused, by the floor in
// [Options.MinMarked]. Half the Vietnamese online is typed bare, and every bare
// occurrence of co in it is evidence, to a counter, that co is a common
// spelling. A ranking built with that evidence in it offers bare spellings as
// distractors, and an item whose four candidates include the bare form of its
// own answer is not asking anybody anything.
func (v *Vocabulary) Add(text string) bool {
	if markedShare(text) < Default().MinMarked {
		return false
	}
	for _, s := range syllables(text) {
		v.count[strings.ToLower(s.text)]++
	}
	v.ranked, v.rank = nil, nil
	return true
}

// Size is how many distinct syllables were counted.
func (v *Vocabulary) Size() int { return len(v.count) }

// Count is how often a syllable appeared.
func (v *Vocabulary) Count(s string) int { return v.count[strings.ToLower(s)] }

// Rank is where a syllable sits in the ranking, with 0 the commonest. It
// reports false for a syllable that was never counted.
//
// Ties are broken by the spelling rather than by whichever the map yielded,
// because a benchmark whose distractors depend on map iteration order is a
// different benchmark on every build.
func (v *Vocabulary) Rank(s string) (int, bool) {
	v.settle()
	i, ok := v.rank[strings.ToLower(s)]
	return i, ok
}

// At is the syllable at a rank, and false past the end of the ranking.
func (v *Vocabulary) At(rank int) (string, bool) {
	v.settle()
	if rank < 0 || rank >= len(v.ranked) {
		return "", false
	}
	return v.ranked[rank], true
}

// Neighbors is the syllables within width ranks of one syllable, nearest first,
// split into the ones more common than it and the ones less common.
//
// The split is what lets an item be built with the answer at a chosen frequency
// rank among its candidates, which is what holds the frequency baseline down to
// chance.
func (v *Vocabulary) Neighbors(s string, width int) (above, below []string) {
	at, ok := v.Rank(s)
	if !ok {
		return nil, nil
	}
	for d := 1; d <= width; d++ {
		if w, ok := v.At(at - d); ok {
			above = append(above, w)
		}
		if w, ok := v.At(at + d); ok {
			below = append(below, w)
		}
	}
	return above, below
}

// settle builds the ranking.
func (v *Vocabulary) settle() {
	if v.ranked != nil {
		return
	}
	v.ranked = make([]string, 0, len(v.count))
	for s := range v.count {
		v.ranked = append(v.ranked, s)
	}
	slices.SortFunc(v.ranked, func(a, b string) int {
		if v.count[a] != v.count[b] {
			return v.count[b] - v.count[a]
		}
		return strings.Compare(a, b)
	})
	v.rank = make(map[string]int, len(v.ranked))
	for i, s := range v.ranked {
		v.rank[s] = i
	}
}

// A span is one syllable and where it sits in the text.
//
// The offsets are kept because the blank has to replace one particular
// occurrence, and a string replace would find whichever came first.
type span struct {
	text       string
	start, end int
}

// syllables splits text the way the rest of the project counts it: every
// maximal run of letters is one, and everything else is a separator. Vietnamese
// puts a space between syllables rather than between words, so this is the unit
// a reader judges the question by.
func syllables(text string) []span {
	var out []span
	start, in := 0, false
	for i, r := range text {
		if unicode.IsLetter(r) {
			if !in {
				start, in = i, true
			}
			continue
		}
		if in {
			out = append(out, span{text[start:i], start, i})
			in = false
		}
	}
	if in {
		out = append(out, span{text[start:], start, len(text)})
	}
	return out
}

// markedShare is the fraction of the letters that carry a mark, which is how a
// page typed bare is told from a page typed properly.
func markedShare(text string) float64 {
	letters, marked := 0, 0
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if normalize.Bare(string(r)) != string(r) {
			marked++
		}
	}
	if letters == 0 {
		return 0
	}
	return float64(marked) / float64(letters)
}
