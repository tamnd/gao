package mark

// Scoring an answer against the page it was made from.

import (
	"unicode"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/inspect"
	"github.com/tamnd/gao/normalize"
)

// A Result is one answer scored.
type Result struct {
	DocID doc.Hash `json:"doc_id"`

	// Score is soi's reading of the answer against the page, and the number
	// worth quoting off it is [inspect.Score.DER], the share of the page's marks
	// that did not come back.
	Score inspect.Score `json:"score"`

	// Syllables is how many the page has and Right is how many came back
	// exactly, mark for mark. It is the number a reader actually wants, because
	// a wrong syllable is a wrong word and half a restored mark is nothing.
	Syllables int `json:"syllables"`
	Right     int `json:"right"`

	// Faithful reports whether the answer is the question with marks added and
	// nothing else. An answer that fails this has rewritten the text rather than
	// restored it, which soi will score anyway and which is not the task. It is
	// counted separately rather than thrown away, because a model that
	// paraphrases half the time is a fact about the model.
	Faithful bool `json:"faithful"`
}

// Grade scores one answer.
func Grade(it Item, answer string) Result {
	r := Result{
		DocID:    it.DocID,
		Score:    inspect.Measure(it.Answer, answer),
		Faithful: normalize.Bare(answer) == it.Prompt,
	}
	want, got := syllables(it.Answer), syllables(answer)
	r.Syllables = len(want)
	if r.Faithful {
		// The bare forms agree, so the syllables line up one for one and the
		// comparison is exact. This is the whole reason faithfulness is checked
		// first: without it the two sequences have to be aligned, and an
		// alignment introduces a judgment into a count that does not need one.
		for i := range want {
			if want[i] == got[i] {
				r.Right++
			}
		}
	}
	return r
}

// A Report is a whole run scored, which is what gets published.
type Report struct {
	Items      int           `json:"items"`
	Unfaithful int           `json:"unfaithful"`
	Score      inspect.Score `json:"score"`
	Syllables  int           `json:"syllables"`
	Right      int           `json:"right"`
}

// Add folds one result in.
func (r *Report) Add(res Result) {
	r.Items++
	if !res.Faithful {
		r.Unfaithful++
	}
	r.Score.Add(res.Score)
	r.Syllables += res.Syllables
	r.Right += res.Right
}

// Restored is the share of the page's marks that came back, which is the
// headline number.
//
// It is one minus soi's diacritic error rate rather than a character accuracy,
// for the reason in the package comment: character accuracy on this task starts
// at about 76% for a model that does nothing at all.
func (r Report) Restored() float64 { return 1 - r.Score.DER() }

// SyllableAccuracy is the share of syllables that came back exactly right. It
// is always the lower of the two and it is the one to quote to somebody who has
// to read the output.
func (r Report) SyllableAccuracy() float64 {
	if r.Syllables == 0 {
		return 0
	}
	return float64(r.Right) / float64(r.Syllables)
}

// syllables splits text the way [doc.Syllables] counts it: every maximal run of
// letters is one, and everything else is a separator. Vietnamese puts a space
// between syllables rather than between words, so this is the unit a reader
// judges a restoration by.
func syllables(text string) []string {
	var out []string
	start, in := 0, false
	for i, r := range text {
		if unicode.IsLetter(r) {
			if !in {
				start, in = i, true
			}
			continue
		}
		if in {
			out = append(out, text[start:i])
			in = false
		}
	}
	if in {
		out = append(out, text[start:])
	}
	return out
}
