// Package mark builds the diacritic restoration task set out of the corpus.
//
// Dấu is the mark: the tone above the vowel and the hook on the ơ. Taking them
// off is a function, and putting them back is not, and everything here follows
// from that asymmetry.
//
// # Why this task and not another one
//
// Every other thing this project measures needs somebody to say what the right
// answer was. Whether a document is good enough is a judgment. Whether two
// documents are the same document is a threshold. Whether a page is Vietnamese
// is a classifier with an error rate of its own. Each of those costs annotation,
// and the annotation is the expensive, slow, arguable part.
//
// Diacritic restoration costs none. Take the marks off a page of Vietnamese and
// you have a question whose answer is the page you started with, exact to the
// character, for as many pages as the corpus holds. There is no annotator, no
// disagreement, and no ceiling on the size of the set. It is the one place in
// the whole pipeline where the corpus grades itself.
//
// It is also not a toy. Half the Vietnamese on the web is typed without marks,
// so restoration is what any model reading real Vietnamese input has to do
// whether or not anybody asked it to, and it cannot be done without knowing the
// language. Ma, má, mà, mả, mã and mạ are six unrelated words that share one
// bare spelling, so the model has to pick from context, which is exactly the
// thing an n-gram count cannot fake.
//
// # Why it is dangerous
//
// The answers are in the training corpus. A model trained on gao has read every
// one of these pages with its marks on, and a benchmark whose answer key is in
// the training data measures memory rather than ability.
//
// That is the only reason an [Item] records a document identity. The identity
// is what lets the items be held out of the corpus before training, and
// checked for afterwards by the same machinery pick uses on everybody else's
// benchmarks. A task set built from the corpus and not held out of it is worse
// than no benchmark, because it produces a high number that somebody will
// quote.
//
// # What makes a usable item
//
// Not every document is an answer. About half of Vietnamese online is typed
// without marks, and a document typed that way is not an answer key, it is a
// second copy of the question. Feeding it in teaches a scorer that bare text is
// correct.
//
// So an item has to carry marks at roughly the rate the language does. That
// rate is measured rather than picked: inspect puts it at about a quarter of
// characters marked and a sixth toned, and the floor here is set below that
// with room for a document about a subject that happens to be short of marked
// vowels. A document under the floor is rejected and counted, because a
// builder that silently drops most of what it is given is a builder nobody can
// debug.
//
// # What a result has to be published with
//
// Two floors, and neither of them is optional.
//
// The first is answering with the question. A model that hands back the bare
// text unchanged restores nothing at all and still scores around 76% character
// accuracy, because only about a quarter of the characters carry a mark. Any
// restoration result quoted as character accuracy is quoting a number that
// starts in the seventies and has nowhere much to go, which is why the number
// this package reports is the share of the marks that came back.
//
// The same answer gets about one syllable in nine exactly right, because that
// many Vietnamese syllables are written with no mark at all. That is the floor
// under [Report.SyllableAccuracy] and it is measured on real pages in
// TestAnsweringWithTheQuestionRestoresNothing rather than assumed.
//
// The second is the [Lexicon]: answer every bare syllable with the marked
// spelling it most often has, counted off the corpus, with no context at all.
// That is the whole task minus the only interesting part of it, and it is
// strong, because most bare syllables have one common answer. A model that does
// not beat it has learned the dictionary and not the language.
//
// Reporting is inspect's job. This package builds the questions, keeps the
// answers, and hands both to [inspect.Measure], because a restoration is a
// reading of a page and the thing worth counting about it is which marks came
// back.
package mark

import (
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/inspect"
	"github.com/tamnd/gao/normalize"
)

// Name is what the task set is called wherever it is published or checked
// against, including in pick's contamination roster.
const Name = "vi-diacritic"

// An Item is one question and the answer it was made from.
type Item struct {
	// DocID is the document the item came from. It is here so the item can be
	// held out of training and found again afterwards, and it is the item's
	// identity: there is one item per document.
	DocID doc.Hash `json:"doc_id"`

	// Prompt is the text with every mark taken off, which is what a model is
	// given.
	Prompt string `json:"prompt"`

	// Answer is what the page said.
	Answer string `json:"answer"`

	// Chars is the length of the answer and Marked is how much of it carries a
	// mark. Marked is the denominator every rate in a report is over, so an item
	// carries its own rather than having it recounted by whoever reads the file.
	Chars  int `json:"chars"`
	Marked int `json:"marked"`
}

// MarkedShare is the fraction of the answer's characters that carry a mark,
// which is what the selection floor is set on and what says at a glance whether
// an item is worth anything.
func (it Item) MarkedShare() float64 {
	if it.Chars == 0 {
		return 0
	}
	return float64(it.Marked) / float64(it.Chars)
}

// NewItem makes an item out of one document's text.
//
// Nothing is checked here. Whether the text is long enough, marked enough, or
// wanted at all is [Builder]'s to decide, and this is the part that is the same
// either way.
func NewItem(id doc.Hash, text string) Item {
	chars, marked := marks(text)
	return Item{
		DocID:  id,
		Prompt: normalize.Bare(text),
		Answer: text,
		Chars:  chars,
		Marked: marked,
	}
}

// marks counts the characters and the ones carrying a mark.
func marks(text string) (chars, marked int) {
	letters := inspect.Letters(text)
	for _, l := range letters {
		if l.Marked() {
			marked++
		}
	}
	return len(letters), marked
}

// markedShare is the fraction of the text carrying a mark, which is the one
// measurement that separates Vietnamese from Vietnamese typed without its
// marks.
func markedShare(text string) float64 {
	chars, marked := marks(text)
	if chars == 0 {
		return 0
	}
	return float64(marked) / float64(chars)
}
