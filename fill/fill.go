// Package fill builds vi-cloze, the fast proxy benchmark the ablation slate is
// read off.
//
// Điền is to fill in: a passage with one syllable taken out and four spellings
// offered, one of which the rest of the passage decides.
//
// # Why a proxy is needed at all
//
// The slate is forty ablation runs, and every one of them has to be scored
// before the next is designed. A benchmark that takes an hour a run costs more
// wall clock than the runs do, and a benchmark that has to be generated from
// and then judged costs a second model and an argument about the judge. So the
// inner loop of the slate needs a benchmark that is a forward pass and an
// argmax: four candidates scored by likelihood, the highest one wins, nothing
// is generated and nothing is judged. Four thousand items at four candidates is
// sixteen thousand scored continuations, which is minutes on one card rather
// than an hour, and it is the same number every time it runs.
//
// A proxy is only worth having if it agrees with the thing it stands in for.
// That agreement is a measurement, it is in [Validate], and it has a kill
// criterion attached: below 0.5 rank correlation against full scale runs the
// slate is reported as exploratory and every threshold it set is flagged as
// unvalidated rather than presented as tuned.
//
// # Why cloze and not something easier to build
//
// The answer key is free, the same way it is free in dau. The passage came out
// of the corpus with the syllable in it, so the answer is what the page said,
// exact to the character, for as many passages as the corpus holds. Nobody
// annotates anything and there is no ceiling on the size of the set.
//
// What is not free is making the question hard enough to be worth asking, and
// that is where all the work in this package went. Three ways of building this
// benchmark produce a number that looks fine and measures nothing:
//
// Blank a syllable at random and most of what gets blanked is của, và, là or
// một. Those are decided by grammar rather than by meaning, a model learns them
// in its first billion tokens, and a proxy made of them saturates before the
// ablation slate starts and stops separating recipes exactly when it is needed.
// So nothing inside the commonest [Options.Function] syllables is ever blanked.
//
// Draw the three distractors at random from the vocabulary and the answer is
// the only common word among four rare ones, so picking the most frequent
// candidate scores near the top without reading the passage. So the distractors
// come from within [Options.Band] ranks of the answer in the corpus frequency
// ranking, and they are chosen so that the answer's frequency rank among the
// four candidates is spread evenly across the set. That is not a detail. It is
// what makes the unigram frequency baseline in [Frequent] score chance rather
// than score well, and the baseline is run and published rather than argued
// about.
//
// Let a distractor be the same syllable with different marks and the item is
// diacritic restoration wearing a cloze costume. Ma, má, mà, mả, mã and mạ are
// six words, choosing between them is a real task, and it is dau's task, which
// already has its own benchmark and its own baseline. An item here is rejected
// if any candidate folds to the answer's bare spelling.
//
// # Why it is dangerous, in the same way dau is
//
// The passages are in the training corpus. An item records its document
// identity so the set can be held out before training and found again
// afterwards with the machinery pick uses on everybody else's benchmarks. A
// proxy built from the corpus and not held out of it measures memory, and it
// will produce a high number that somebody will quote.
package fill

import (
	"strings"

	"github.com/tamnd/gao/doc"
)

// Name is what the task set is called wherever it is published or checked
// against, including in pick's contamination roster.
const Name = "vi-cloze"

// Candidates is how many spellings an item offers.
//
// Four, which puts chance at 25%: high enough that a run of a few thousand
// items separates two recipes that differ by a point, and low enough that the
// scoring cost is four forward passes rather than ten. It is a constant rather
// than an option because the chance floor moves with it, and a slate whose
// items had different chance floors could not be added up.
const Candidates = 4

// Blank is what stands where the syllable was.
const Blank = "___"

// An Item is one passage with one syllable taken out.
type Item struct {
	// DocID is the document the passage came from. There is one item per
	// document, and this is what holds the item out of training and finds it
	// again afterwards.
	DocID doc.Hash `json:"doc_id"`

	// Prompt is the passage with [Blank] where the syllable was.
	Prompt string `json:"prompt"`

	// Choices are the spellings offered, in the order they are shown. The order
	// is decided by the document identity rather than by how they were found, so
	// the set rebuilds the same way and the answer is not always in the same
	// place.
	Choices []string `json:"choices"`

	// Answer is the index into Choices of what the page said.
	Answer int `json:"answer"`

	// Rank is how many of the choices are more common in the corpus than the
	// answer is, from 0 to [Candidates]-1. Spread evenly across a set, it is
	// what holds the frequency baseline down to chance, and it is carried on the
	// item so that a reader can check the spread rather than take it on trust.
	Rank int `json:"rank"`
}

// Right is the spelling the page had.
func (it Item) Right() string {
	if it.Answer < 0 || it.Answer >= len(it.Choices) {
		return ""
	}
	return it.Choices[it.Answer]
}

// Filled is the prompt with a choice put back into it, which is what a model is
// asked to score.
func (it Item) Filled(choice int) string {
	if choice < 0 || choice >= len(it.Choices) {
		return it.Prompt
	}
	return strings.Replace(it.Prompt, Blank, it.Choices[choice], 1)
}

// Passage is the prompt with the right answer back in it, which is the page as
// it was written.
func (it Item) Passage() string { return it.Filled(it.Answer) }
