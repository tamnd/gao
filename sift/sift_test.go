package sift

import (
	"math"
	"strings"
	"testing"

	"github.com/tamnd/gao/reject"
)

// The document this stage exists to keep. Every threshold in it is a threshold
// that can remove ordinary Vietnamese, so the first test is that none of them
// does.
func TestOrdinaryVietnameseProseGoesThrough(t *testing.T) {
	if reason, detail, ok := Default().Reject(Measure(article)); ok {
		t.Errorf("a news article was rejected as %q: %s", string(reason), detail)
	}
}

// The measurement the whole package turns on. Gopher removes a document whose
// mean word length is under 3, and Vietnamese prose sits just above that line,
// so a pipeline that inherited the number would be removing the language.
func TestTheMeanVietnameseSyllableSitsOnGophersLowerBound(t *testing.T) {
	got := Measure(article).MeanSyllable()
	if got < 3.0 || got > 3.6 {
		t.Fatalf("the mean syllable of a Vietnamese article is %.2f letters, which is not what this package was tuned against", got)
	}
	if got-3.0 > 0.5 {
		t.Errorf("the mean syllable is %.2f, far enough above 3 that Gopher's bound would be harmless and this package would not be needed", got)
	}
	if Default().MinMeanSyllable >= 3.0 {
		t.Errorf("the lower bound is %.1f, which is the English number and cuts into Vietnamese prose", Default().MinMeanSyllable)
	}
}

// Vietnamese typed without tone marks is most of what people type on a phone.
// It is a different distribution and it is not a defect, so it comes through
// this stage labeled rather than rejected. Rewriting the marks back on would
// mean guessing which word was meant, which is what normalize refuses to do
// with input method residue and for the same reason.
func TestVietnameseWithoutToneMarksIsKeptAndLabelled(t *testing.T) {
	r := Measure(unmarked)
	if reason, detail, ok := Default().Reject(r); ok {
		t.Errorf("Vietnamese typed without tone marks was rejected as %q: %s", string(reason), detail)
	}
	if got := r.Diacritic(); got != "absent" {
		t.Errorf("the document is labeled %q, want absent", got)
	}
	if got := Measure(article).Diacritic(); got != "present" {
		t.Errorf("a marked document is labeled %q, want present", got)
	}
	if r.StopWords < 2 {
		t.Errorf("the function words were not recognized without their marks, only %d of them", r.StopWords)
	}
}

// The label has three values because the middle one is real: a page that quotes
// a marked headline above an unmarked comment thread is neither.
func TestAMixtureIsLabelledAsOne(t *testing.T) {
	half := article[:len(article)/2] + unmarked[len(unmarked)/2:]
	if got := Measure(half).Diacritic(); got != "present" && got != "mixed" {
		t.Errorf("half of each is labeled %q", got)
	}
	mostlyBare := unmarked + "\n" + article[:len(article)/6]
	if got := Measure(mostlyBare).Diacritic(); got != "mixed" {
		t.Errorf("a mostly unmarked document with some marked text is labeled %q, want mixed", got)
	}
}

// Each kind of page has to go to the reason a person would look for it under.
// A menu counted as a language failure is a true statement filed where nobody
// will find it.
func TestEachKindOfPageGoesToItsOwnReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		text   string
		reason string
	}{
		{"a photograph caption", caption, "short"},
		{"a flattened price table", prices, "short"},
		{"a navigation bar", menu, "boilerplate"},
		{"an index of teasers", listing, "boilerplate"},
		{"a page that repeats a paragraph", looped, "repetition"},
		{"a page of one sentence with a word changed", chanted, "repetition"},
		{"a document in another language", english, "language"},
	} {
		reason, detail, ok := Default().Reject(Measure(tc.text))
		if !ok {
			t.Errorf("%s went through", tc.name)
			continue
		}
		if string(reason) != tc.reason {
			t.Errorf("%s was rejected as %q, want %q: %s", tc.name, string(reason), tc.reason, detail)
		}
	}
}

// A reason without the number behind it sends whoever reads the reject store
// back to the text to work out which threshold fired.
func TestTheRejectionSaysWhichNumberFailed(t *testing.T) {
	_, detail, ok := Default().Reject(Measure(caption))
	if !ok {
		t.Fatal("a caption went through")
	}
	if !strings.Contains(detail, "13") || !strings.Contains(detail, "60") {
		t.Errorf("the detail is %q, and it names neither what the document measured nor what it needed", detail)
	}
}

// The two repetition measures answer different questions and the second one is
// here because the first misses this case: nothing repeats a whole line, and
// nine tenths of the text is still the same sentence.
func TestRepetitionInsideALineIsCaughtWhenNoLineRepeats(t *testing.T) {
	r := Measure(chanted)
	if r.DuplicateLineRate() != 0 {
		t.Fatalf("the fixture repeats a line, which is not the case this test is about")
	}
	if r.Repeat[0] < 0.5 {
		t.Errorf("only %.2f of the text is inside a repeated gram, so the gram measure is not seeing it", r.Repeat[0])
	}
}

// Overlapping occurrences of a gram must not be counted twice. Gopher's
// formulation multiplies a count by a length and can report that more than all
// of a document sits inside one gram, which is not a share of anything.
func TestAShareOfTheDocumentIsNeverMoreThanAllOfIt(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"a price table", prices},
		{"a repeated paragraph", looped},
		{"an article", article},
		{"one repeated syllable", strings.Repeat("nhà ", 200)},
	} {
		r := Measure(tc.text)
		for i, f := range r.Top {
			if f < 0 || f > 1 {
				t.Errorf("%s: the top %d syllable gram covers %.2f of it", tc.name, TopGramSizes[i], f)
			}
		}
		for i, f := range r.Repeat {
			if f < 0 || f > 1 {
				t.Errorf("%s: repeated %d syllable grams cover %.2f of it", tc.name, RepeatGramSizes[i], f)
			}
		}
	}
}

// An empty document has no rates, and every one of them is a division. This is
// the case that turns a filter into a panic on the first shard.
func TestAnEmptyDocumentMeasuresZeroRatherThanPanicking(t *testing.T) {
	r := Measure("")
	for name, got := range map[string]float64{
		"mean syllable":  r.MeanSyllable(),
		"alpha":          r.AlphaRate(),
		"diacritic":      r.DiacriticRate(),
		"symbol":         r.SymbolRate(),
		"bullet":         r.BulletRate(),
		"ellipsis":       r.EllipsisRate(),
		"duplicate line": r.DuplicateLineRate(),
		"duplicate rune": r.DuplicateLineRuneRate(),
	} {
		if got != 0 || math.IsNaN(got) {
			t.Errorf("the %s rate of an empty document is %v", name, got)
		}
	}
	if reason, _, ok := Default().Reject(r); !ok || string(reason) != "short" {
		t.Errorf("an empty document was rejected as %q, %v", string(reason), ok)
	}
}

// The heuristics go on the row as raw values, because a corpus that recorded
// "passed the length filter" cannot be re-filtered at another threshold without
// going back to text that is no longer on the box.
func TestTheRowCarriesTheValuesRatherThanTheVerdicts(t *testing.T) {
	h := Measure(article).Heuristics()
	for _, key := range []string{"syllables", "mean_syllable", "alpha_rate", "diacritic_rate", "stop_words", "symbol_rate", "dup_line_rate", "top_gram_max", "repeat_gram_max"} {
		if _, ok := h[key]; !ok {
			t.Errorf("the row does not carry %s", key)
		}
	}
	if got := h["syllables"]; got != 180 {
		t.Errorf("the row says %v syllables, want 180", got)
	}
	for key, got := range h {
		if math.IsNaN(float64(got)) || math.IsInf(float64(got), 0) {
			t.Errorf("%s is %v, which is not a number a Parquet reader can do anything with", key, got)
		}
	}
}

// A threshold that cannot be moved is a threshold nobody can measure, and every
// number in this package is one the ablation is expected to move.
func TestEveryThresholdCanBeMoved(t *testing.T) {
	loose := Default()
	loose.MinSyllables = 5
	loose.MinStopWords = 1
	if _, _, ok := loose.Reject(Measure(caption)); ok {
		t.Error("a caption is still rejected after the thresholds that removed it were lowered under it")
	}

	strict := Default()
	strict.MinSyllables = 500
	if _, _, ok := strict.Reject(Measure(article)); !ok {
		t.Error("an article goes through a length threshold set above it")
	}
}

// The tally is what a run reports, and a run that could not say what it removed
// and why would leave every threshold in the package unmeasurable.
func TestTheTallyCountsWhatWentAndWhy(t *testing.T) {
	var tally Tally
	l := Default()
	for _, text := range []string{article, unmarked, caption, menu, looped, english} {
		tally.Add(l, Measure(text))
	}

	if tally.Documents != 6 {
		t.Errorf("the tally counted %d documents, want 6", tally.Documents)
	}
	if tally.Kept != 2 {
		t.Errorf("the tally kept %d documents, want the article and the unmarked one", tally.Kept)
	}
	if got := tally.Retention(); math.Abs(got-2.0/6.0) > 0.001 {
		t.Errorf("retention is %v, want %v", got, 2.0/6.0)
	}
	for reason, want := range map[string]int64{"short": 1, "boilerplate": 1, "repetition": 1, "language": 1} {
		if got := tally.Rejected[reasonOf(reason)]; got != want {
			t.Errorf("the tally counted %d rejections for %s, want %d", got, reason, want)
		}
	}
	if got := tally.Diacritics["absent"]; got != 2 {
		t.Errorf("the tally counted %d documents without tone marks, want the unmarked one and the English one", got)
	}
}

// An empty tally is a run that read nothing, not a run that kept nothing.
func TestAnEmptyTallyRetainsNothingWithoutDividingByZero(t *testing.T) {
	var tally Tally
	if got := tally.Retention(); got != 0 {
		t.Errorf("a tally of no documents retains %v", got)
	}
}

func TestTheFunctionWordListIsStableAndNotEmpty(t *testing.T) {
	words := StopWords()
	if len(words) < 20 {
		t.Errorf("the function word list holds %d words, which is too few to be a language check", len(words))
	}
	for i := 1; i < len(words); i++ {
		if words[i] <= words[i-1] {
			t.Fatalf("the list is not in a stable order: %q comes after %q", words[i], words[i-1])
		}
	}
}

// reasonOf keeps the table above readable. The reasons are reject's, and a typo in
// one of these strings would otherwise pass as a zero count.
func reasonOf(name string) reject.Reason {
	r := reject.Reason(name)
	if !r.Valid() {
		panic("not a defined rejection reason: " + name)
	}
	return r
}

// Measuring one document twice has to give one answer. It did not: the most
// frequent gram was picked by walking a map, so when two grams tied on both
// count and length the winner was whichever one Go's randomized iteration
// yielded, and the coverage of the two is not the same number. On a real GlotCC
// part that was four documents in five thousand whose Top came back differently
// on a second reading of the same bytes, which for a corpus whose entire claim
// is that a row can be reproduced from its inputs is not a rounding difference.
func TestMeasuringOneDocumentTwiceGivesOneAnswer(t *testing.T) {
	// Two grams of three syllables, tied on count and tied on length, covering
	// different amounts of the document because one of them repeats across
	// itself and the other does not. "ba ba ba" occurs twice inside a run of
	// four and covers four syllables; "ca da ea" occurs twice apart and covers
	// six. Which of them is called the most frequent decides the answer, and
	// nothing in the document decides between them.
	text := "ba ba ba ba ca da ea xa ya za ca da ea"

	first := Measure(text)
	for i := range 200 {
		got := Measure(text)
		if got.Top != first.Top {
			t.Fatalf("reading %d measured Top as %v, and the first reading measured %v", i, got.Top, first.Top)
		}
		if got.Repeat != first.Repeat {
			t.Fatalf("reading %d measured Repeat as %v, and the first reading measured %v", i, got.Repeat, first.Repeat)
		}
	}
}
