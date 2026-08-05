package sang

// The thresholds, and the one place they live.

import (
	"fmt"

	"github.com/tamnd/gao/vo"
)

// Limits is every threshold this stage applies, in one struct, so that the
// ablation that sets them properly changes a value rather than a program.
//
// The defaults below are starting points and they are labeled as such. Two of
// them are derived from a property of the language and will not move much: the
// syllable length window comes from the shape of a Vietnamese syllable. The
// rest are Gopher's numbers at Vietnamese sizes, and the honest statement about
// them is that they are the wrong numbers until a curve says otherwise, in the
// same way the deduplication threshold is.
type Limits struct {
	// MinSyllables is the shortest thing that is a document rather than a
	// caption, a headline, or a cookie notice.
	MinSyllables int

	// MinMeanSyllable and MaxMeanSyllable bound the mean length of a syllable
	// in letters. The upper bound is the load bearing one: the longest
	// Vietnamese syllable is seven letters, so a document averaging more than
	// five is not made of Vietnamese syllables.
	MinMeanSyllable float64
	MaxMeanSyllable float64

	// MinStopWords is how many distinct function words a document has to hold.
	MinStopWords int

	// MinAlphaRate is the share of tokens that must hold a letter, which is
	// what separates prose from a table of prices or a list of dates.
	MinAlphaRate float64

	// MaxSymbolRate, MaxBulletRate and MaxEllipsisRate are the shapes a page
	// takes when it is a menu or an index of teasers rather than an article.
	MaxSymbolRate   float64
	MaxBulletRate   float64
	MaxEllipsisRate float64

	// MaxDuplicateLineRate and MaxDuplicateLineRuneRate are two views of the
	// same repetition, and both are here because they disagree on the case that
	// matters: a page whose every second line is one short label is not the
	// same thing as a page that repeats a paragraph.
	MaxDuplicateLineRate     float64
	MaxDuplicateLineRuneRate float64

	// MaxTop and MaxRepeat are the gram thresholds, one per size in
	// [TopGramSizes] and [RepeatGramSizes].
	MaxTop    [3]float64
	MaxRepeat [3]float64

	// Identify says whether the syllable inventory decides the language, which
	// it does. It is a switch rather than a constant so that the ablation can
	// measure what the identifier costs and what it admits by running the same
	// corpus with it off, which is the only way the claim that it admits
	// documents stock models reject can be checked.
	Identify bool
}

// Default returns the thresholds the pipeline runs at.
func Default() Limits {
	return Limits{
		MinSyllables:    60,
		MinMeanSyllable: 2.0,
		MaxMeanSyllable: 5.5,
		MinStopWords:    2,
		MinAlphaRate:    0.7,

		MaxSymbolRate:   0.1,
		MaxBulletRate:   0.9,
		MaxEllipsisRate: 0.3,

		MaxDuplicateLineRate:     0.3,
		MaxDuplicateLineRuneRate: 0.2,

		MaxTop:    [3]float64{0.20, 0.18, 0.16},
		MaxRepeat: [3]float64{0.15, 0.12, 0.10},

		Identify: true,
	}
}

// Reject returns the reason a document does not go on, the value that failed,
// and whether it was rejected at all.
//
// The order is deliberate, because it decides which reason a document that
// fails several checks is filed under, and the reject store's whole value is
// being able to ask how many documents went for what.
//
// Length comes first. Every rate below it is a ratio over almost nothing when a
// document is thirteen syllables long, so a caption filed under any other
// reason would be filed under a number that meant nothing.
//
// Then the checks that say what kind of page it is, before the ones that say
// what language it is in. A navigation bar holds no Vietnamese sentence and no
// English one either, and filing it under language would be true by accident
// and useless on purpose. It is a menu, and boilerplate is what a menu is.
//
// Repetition comes last, because it is the only reason that needs the whole
// document to have been read and because a page that is both a template and a
// loop is more usefully counted as a template.
func (l Limits) Reject(r Result) (vo.Reason, string, bool) {
	if r.Syllables < l.MinSyllables {
		return vo.ReasonShort, fmt.Sprintf("%d syllables, under %d", r.Syllables, l.MinSyllables), true
	}

	if rate := r.AlphaRate(); rate < l.MinAlphaRate {
		return vo.ReasonBoilerplate, fmt.Sprintf("%.2f of tokens hold a letter, under %.2f", rate, l.MinAlphaRate), true
	}
	if rate := r.BulletRate(); rate > l.MaxBulletRate {
		return vo.ReasonBoilerplate, fmt.Sprintf("%.2f of lines are bullets, over %.2f", rate, l.MaxBulletRate), true
	}
	if rate := r.EllipsisRate(); rate > l.MaxEllipsisRate {
		return vo.ReasonBoilerplate, fmt.Sprintf("%.2f of lines trail off, over %.2f", rate, l.MaxEllipsisRate), true
	}
	if rate := r.SymbolRate(); rate > l.MaxSymbolRate {
		return vo.ReasonBoilerplate, fmt.Sprintf("symbol rate %.3f, over %.2f", rate, l.MaxSymbolRate), true
	}

	if mean := r.MeanSyllable(); mean < l.MinMeanSyllable || mean > l.MaxMeanSyllable {
		return vo.ReasonLanguage, fmt.Sprintf("mean syllable %.2f letters, outside %.1f to %.1f", mean, l.MinMeanSyllable, l.MaxMeanSyllable), true
	}
	if r.StopWords < l.MinStopWords {
		return vo.ReasonLanguage, fmt.Sprintf("%d distinct function words, under %d", r.StopWords, l.MinStopWords), true
	}
	if l.Identify && !r.Language.Vietnamese() {
		return vo.ReasonLanguage, languageDetail(r.Language), true
	}

	if rate := r.DuplicateLineRate(); rate > l.MaxDuplicateLineRate {
		return vo.ReasonRepetition, fmt.Sprintf("%.2f of lines had appeared before, over %.2f", rate, l.MaxDuplicateLineRate), true
	}
	if rate := r.DuplicateLineRuneRate(); rate > l.MaxDuplicateLineRuneRate {
		return vo.ReasonRepetition, fmt.Sprintf("%.2f of the text is repeated lines, over %.2f", rate, l.MaxDuplicateLineRuneRate), true
	}
	for i, n := range TopGramSizes {
		if r.Top[i] > l.MaxTop[i] {
			return vo.ReasonRepetition, fmt.Sprintf("%.2f of the text is one %d syllable gram, over %.2f", r.Top[i], n, l.MaxTop[i]), true
		}
	}
	for i, n := range RepeatGramSizes {
		if r.Repeat[i] > l.MaxRepeat[i] {
			return vo.ReasonRepetition, fmt.Sprintf("%.2f of the text is repeated %d syllable grams, over %.2f", r.Repeat[i], n, l.MaxRepeat[i]), true
		}
	}
	return "", "", false
}

// languageDetail says why the identifier turned a document down, in the terms
// the identifier used rather than in a score. Which register it was read in
// decides which of the two bars applied, so the message names it.
func languageDetail(l Language) string {
	if l.MarkRate() >= MinMarkRate {
		return fmt.Sprintf("%.2f of tokens are Vietnamese syllables and %d function words carry their marks, under %.2f and %d",
			l.Rate(), l.MarkedStopWords, MinRate, MinIDMarkedStop)
	}
	return fmt.Sprintf("written without tone marks, %.2f of tokens are Vietnamese syllables with the marks off and %d function words match, under %.2f and %d",
		l.BareRate(), l.StopWords, MinBareRate, MinIDStopWords)
}

// Tally is what a run of this stage reports.
type Tally struct {
	Documents int64               `json:"documents"`
	Kept      int64               `json:"kept"`
	Syllables int64               `json:"syllables"`
	Rejected  map[vo.Reason]int64 `json:"rejected,omitempty"`

	// Diacritics counts documents by the label they were given, which is the
	// number that says how much of the corpus is Vietnamese typed without tone
	// marks. It is not a rejection and it is worth knowing before anybody
	// decides what to do about it.
	Diacritics map[string]int64 `json:"diacritics,omitempty"`
}

// Add folds one document into the tally.
func (t *Tally) Add(l Limits, r Result) {
	t.Documents++
	t.Syllables += int64(r.Syllables)
	if t.Diacritics == nil {
		t.Diacritics = map[string]int64{}
	}
	t.Diacritics[r.Diacritic()]++
	if reason, _, ok := l.Reject(r); ok {
		if t.Rejected == nil {
			t.Rejected = map[vo.Reason]int64{}
		}
		t.Rejected[reason]++
		return
	}
	t.Kept++
}

// Retention is the share of documents that go on, which is the number an
// ablation moves a threshold to change.
func (t Tally) Retention() float64 {
	if t.Documents == 0 {
		return 0
	}
	return float64(t.Kept) / float64(t.Documents)
}
