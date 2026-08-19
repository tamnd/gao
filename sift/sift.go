// Package sift is the sifting stage: it measures a document and says whether it
// is prose somebody wrote.
//
// The measurements are the well known ones, from Gopher and C4 and everything
// that followed them, and none of the thresholds are. Every published pipeline
// counts space separated tokens and calls them words. Vietnamese writes a space
// between syllables, so a space separated token here is a syllable and a word is
// one or two of them, and a threshold carried over unchanged is not a stricter
// or looser version of the same filter, it is a different filter.
//
// The clearest case is mean token length. Gopher removes a document whose mean
// word length falls outside 3 to 10 characters, which in English removes
// gibberish at one end and long runs of code at the other. The mean Vietnamese
// syllable across the five documents this repository keeps as golden files is
// 3.36 letters. Gopher's lower bound is 3. That is not a filter with a margin,
// it is a filter sitting a third of a letter under the middle of the
// distribution it is being applied to, and what it removes there is not
// gibberish, it is the language.
//
// So the window here is 2.0 to 5.5, and the upper end is the load bearing one.
// The longest Vietnamese syllable is seven letters, nghiêng, so a document
// averaging more than five is not made of Vietnamese syllables at all. The
// lower end catches a page of single letters.
//
// # What is measured and what is decided
//
// Nothing here is a verdict stored on a document. [Measure] returns raw values,
// [Result.Heuristics] hands them to the row as they are, and [Limits.Reject]
// applies thresholds that live in one struct with defaults that can be replaced.
// A corpus that recorded "passed the length filter" cannot be re-filtered at a
// different threshold without going back to the text. One that recorded the
// length can, and the ablation that sets these thresholds properly needs exactly
// that.
//
// The repetition measures follow Gopher's shape and not its n. Its n-grams are
// over words, these are over syllables, and the same n covers about half as much
// text, so the sizes are scaled rather than copied: where Gopher looks at 2, 3
// and 4 word grams, this looks at 3, 5 and 7 syllable grams.
//
// # Which language, and how it is answered
//
// [Identify] is the language identifier, and it is a lookup rather than a model.
// Vietnamese has a closed syllable inventory, so the question of whether a
// document is Vietnamese has an exact answer that does not degrade on a short
// page or on a register nobody trained for. What that buys and what it costs is
// written down in syllable.go, next to the inventory.
//
// # What this stage does not do
//
// It does not judge quality. gao-qual is a classifier trained against a
// hand built reference set, and a document that passes everything here has only
// been found to be Vietnamese prose of some length, which is the floor rather
// than the bar.
package sift

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Result is what one document measures. Every field is a count or a ratio taken
// from the text, and none of them is a decision.
type Result struct {
	// Runes is the length of the document.
	Runes int

	// Tokens is every whitespace separated run in the document, including the
	// ones that hold no letter at all: bare numbers, bullets, dividers made of
	// punctuation.
	Tokens int

	// Syllables is the tokens that hold at least one letter. In Vietnamese that
	// is a syllable rather than a word, which is why every threshold in this
	// package had to be reconsidered rather than inherited.
	Syllables int

	// Letters is how many letters those syllables hold between them, so that
	// Letters over Syllables is the mean syllable length.
	Letters int

	// Diacritics is how many syllables carry a Vietnamese diacritic. Vietnamese
	// typed without them is still Vietnamese, so this is recorded rather than
	// judged.
	Diacritics int

	// StopWords is how many distinct Vietnamese function words the document
	// holds. Distinct rather than total, because a page repeating one of them a
	// thousand times is not more Vietnamese than a page using three once.
	StopWords int

	// Symbols is the count of hashes and ellipses, which is Gopher's symbol to
	// word ratio measured over syllables.
	Symbols int

	// Lines is the non-empty lines of the document, and Bullets and Ellipses are
	// how many of them begin with a bullet or trail off.
	Lines    int
	Bullets  int
	Ellipses int

	// DuplicateLines is the lines that had already appeared, and
	// DuplicateLineRunes is how much of the document they are.
	DuplicateLines     int
	DuplicateLineRunes int
	LineRunes          int

	// Language is what the document measures against the Vietnamese syllable
	// inventory. It is a separate struct because it answers a separate question,
	// and because it is the one part of this stage that can be run on its own
	// against a labeled set.
	Language Language

	// Top holds, for each size in [TopGramSizes], the share of the document
	// taken by the single most repeated syllable gram of that size, and zero
	// when no gram of that size occurs twice.
	Top [3]float64

	// Repeat holds, for each size in [RepeatGramSizes], the share of the
	// document sitting inside a syllable gram of that size that occurs more
	// than once.
	Repeat [3]float64
}

// The gram sizes the repetition measures run at.
//
// Gopher uses 2, 3 and 4 word grams for the most frequent gram and 5 to 10 for
// the repeated ones. A Vietnamese word is one or two syllables, so an n
// syllable gram covers about half of what an n word gram does, and these are
// its sizes scaled rather than its sizes copied.
var (
	TopGramSizes    = [3]int{3, 5, 7}
	RepeatGramSizes = [3]int{8, 12, 17}
)

// MeanSyllable is the mean length of a syllable in letters. It is the
// measurement that carries over worst from an English pipeline, and the one
// worth looking at first when a threshold here starts removing good documents.
func (r Result) MeanSyllable() float64 {
	if r.Syllables == 0 {
		return 0
	}
	return float64(r.Letters) / float64(r.Syllables)
}

// AlphaRate is the share of tokens that hold a letter. A directory listing, a
// table of prices and a page of dates all come back low.
func (r Result) AlphaRate() float64 {
	if r.Tokens == 0 {
		return 0
	}
	return float64(r.Syllables) / float64(r.Tokens)
}

// DiacriticRate is the share of syllables carrying a Vietnamese diacritic.
func (r Result) DiacriticRate() float64 {
	if r.Syllables == 0 {
		return 0
	}
	return float64(r.Diacritics) / float64(r.Syllables)
}

// SymbolRate is hashes and ellipses over syllables. A page of teaser text cut
// off mid sentence is full of both.
func (r Result) SymbolRate() float64 {
	if r.Syllables == 0 {
		return 0
	}
	return float64(r.Symbols) / float64(r.Syllables)
}

// BulletRate is the share of lines that begin with a bullet, and EllipsisRate
// the share that trail off. A menu is nearly all bullets and an index of
// articles is nearly all ellipses.
func (r Result) BulletRate() float64 {
	if r.Lines == 0 {
		return 0
	}
	return float64(r.Bullets) / float64(r.Lines)
}

func (r Result) EllipsisRate() float64 {
	if r.Lines == 0 {
		return 0
	}
	return float64(r.Ellipses) / float64(r.Lines)
}

// DuplicateLineRate is the share of lines that had appeared before, and
// DuplicateLineRuneRate the share of the text they hold. Both are kept because
// they disagree in the case that matters: a page whose every second line is the
// same short label is not the same thing as a page that repeats a paragraph.
func (r Result) DuplicateLineRate() float64 {
	if r.Lines == 0 {
		return 0
	}
	return float64(r.DuplicateLines) / float64(r.Lines)
}

func (r Result) DuplicateLineRuneRate() float64 {
	if r.LineRunes == 0 {
		return 0
	}
	return float64(r.DuplicateLineRunes) / float64(r.LineRunes)
}

// Diacritic returns the value the record's diacritics column takes: present,
// mixed, or absent.
//
// Vietnamese written without tone marks is a register rather than a defect. It
// is most of what people type on a phone, restoring the marks means guessing
// which word was meant, and a guess written into a corpus is indistinguishable
// from a fact afterwards. So this is a label and never a rejection.
func (r Result) Diacritic() string {
	switch rate := r.DiacriticRate(); {
	case rate >= 0.5:
		return "present"
	case rate >= 0.05:
		return "mixed"
	}
	return "absent"
}

// Heuristics returns the values the row carries, keyed the way a query would
// ask for them. Raw values, so that a threshold can be moved later without
// going back to the text.
func (r Result) Heuristics() map[string]float32 {
	h := map[string]float32{
		"syllables":       float32(r.Syllables),
		"mean_syllable":   float32(r.MeanSyllable()),
		"alpha_rate":      float32(r.AlphaRate()),
		"diacritic_rate":  float32(r.DiacriticRate()),
		"stop_words":      float32(r.StopWords),
		"symbol_rate":     float32(r.SymbolRate()),
		"bullet_rate":     float32(r.BulletRate()),
		"ellipsis_rate":   float32(r.EllipsisRate()),
		"dup_line_rate":   float32(r.DuplicateLineRate()),
		"dup_line_runes":  float32(r.DuplicateLineRuneRate()),
		"top_gram_max":    float32(max3(r.Top)),
		"repeat_gram_max": float32(max3(r.Repeat)),
		"syllable_rate":   float32(r.Language.Rate()),
		"bare_rate":       float32(r.Language.BareRate()),
		"mark_rate":       float32(r.Language.MarkRate()),
	}
	return h
}

func max3(v [3]float64) float64 {
	out := v[0]
	for _, f := range v[1:] {
		if f > out {
			out = f
		}
	}
	return out
}

// Measure reads a document once and returns everything the thresholds need.
//
// It expects text that has already been through normalize. Measuring
// unnormalized text would count two spellings of one syllable as two, which is
// the whole reason normalization runs first.
func Measure(text string) Result {
	var r Result
	r.Runes = len([]rune(text))

	syllables := make([]string, 0, 256)
	for _, tok := range strings.Fields(text) {
		r.Tokens++
		letters, diacritic := 0, false
		for _, c := range tok {
			if unicode.IsLetter(c) {
				letters++
				if isDiacritic(c) {
					diacritic = true
				}
			}
		}
		if letters == 0 {
			continue
		}
		r.Syllables++
		r.Letters += letters
		if diacritic {
			r.Diacritics++
		}
		syllables = append(syllables, strings.ToLower(strip(tok)))
	}
	r.Symbols = countSymbols(text)
	r.StopWords = countStopWords(syllables)
	r.Language = Identify(text)

	measureLines(&r, text)
	measureGrams(&r, syllables)
	return r
}

// strip reduces a token to its letters and digits, so that the same syllable
// under two sets of quotation marks is one gram rather than two.
func strip(tok string) string {
	var b strings.Builder
	for _, c := range tok {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || unicode.IsMark(c) {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func countSymbols(text string) int {
	n := strings.Count(text, "#") + strings.Count(text, "…")
	// Three dots typed out are the same symbol as the ellipsis character, and
	// the pages that carry either carry a great many of them.
	rest := text
	for {
		i := strings.Index(rest, "...")
		if i < 0 {
			break
		}
		n++
		rest = rest[i+3:]
	}
	return n
}

func measureLines(r *Result, text string) {
	seen := make(map[string]bool)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		r.Lines++
		runes := len([]rune(line))
		r.LineRunes += runes
		if isBullet(line) {
			r.Bullets++
		}
		if strings.HasSuffix(line, "...") || strings.HasSuffix(line, "…") {
			r.Ellipses++
		}
		if seen[line] {
			r.DuplicateLines++
			r.DuplicateLineRunes += runes
			continue
		}
		seen[line] = true
	}
}

// isBullet reports whether a line begins with a list marker. The hyphen is in
// the set and the en dash is not, because a line beginning with a hyphen is a
// bullet and a line beginning with an en dash is usually speech in a story, which is
// prose and is common in Vietnamese fiction.
func isBullet(line string) bool {
	for _, c := range []string{"•", "‣", "▪", "●", "◦", "*", "-"} {
		if strings.HasPrefix(line, c) {
			return true
		}
	}
	return false
}

func measureGrams(r *Result, syllables []string) {
	g := newGrams(syllables)
	if g.total == 0 {
		return
	}
	for i, n := range TopGramSizes {
		r.Top[i] = g.top(n)
	}
	for i, n := range RepeatGramSizes {
		r.Repeat[i] = g.repeat(n)
	}
}

// grams is the document as gram identifiers rather than as gram strings.
//
// The straightforward way to count grams is to join each one into a string and
// count the strings, and that is what this used to do. It is also, on a corpus
// of a hundred million documents, most of the cost of the sift: six gram sizes,
// the longest of them seventeen syllables, each joined twice over every position
// in the document. On a real GlotCC part it was a fifth of the whole cleaning
// line and the joins alone were an eighth of it.
//
// So a gram is an integer here. Each distinct syllable gets an identifier, each
// distinct pair of identifiers gets one, and a gram of any length is folded down
// to a single identifier a pair at a time. Two grams have the same identifier
// exactly when they are the same sequence of syllables, so the counting is the
// counting it always was, with no strings built and nothing hashed twice.
type grams struct {
	syllables []string

	// id is the identifier of the syllable at each position, and runes is its
	// length, which coverage would otherwise recount for every gram size.
	id    []int32
	runes []int32
	total int

	word map[string]int32
	pair map[uint64]int32
	next int32

	// buf is reused across gram sizes, since only one size is in flight.
	buf []int32
}

func newGrams(syllables []string) *grams {
	g := &grams{
		syllables: syllables,
		id:        make([]int32, len(syllables)),
		runes:     make([]int32, len(syllables)),
		word:      make(map[string]int32, len(syllables)),
		pair:      make(map[uint64]int32, len(syllables)),
	}
	for i, s := range syllables {
		id, ok := g.word[s]
		if !ok {
			id = g.next
			g.next++
			g.word[s] = id
		}
		g.id[i] = id
		g.runes[i] = int32(utf8.RuneCountInString(s))
		g.total += int(g.runes[i])
	}
	return g
}

// ids fills buf with the identifier of the gram of n syllables starting at each
// position, and returns it.
func (g *grams) ids(n int) []int32 {
	count := len(g.id) - n + 1
	g.buf = append(g.buf[:0], g.id[:count]...)
	for j := 1; j < n; j++ {
		for i := range count {
			key := uint64(uint32(g.buf[i]))<<32 | uint64(uint32(g.id[i+j]))
			id, ok := g.pair[key]
			if !ok {
				id = g.next
				g.next++
				g.pair[key] = id
			}
			g.buf[i] = id
		}
	}
	return g.buf
}

// top is the share of the document covered by the most frequent gram of n
// syllables, and zero when nothing repeats.
//
// Two departures from Gopher, both because the number has to mean what it says.
// Its version multiplies the count of the most frequent gram by its length,
// which double counts the overlap when a gram repeats inside itself and can
// report that more than all of a document sits inside one gram. And its version
// answers with the longest gram even when every gram occurs exactly once, so a
// caption comes back at a quarter of itself with nothing repeated in it at all.
// Gopher never sees that because it filters on length first. This package does
// too, and the measure still should not lie when read on its own.
func (g *grams) top(n int) float64 {
	if len(g.syllables) < n {
		return 0
	}
	ids := g.ids(n)
	counts := make(map[int32]int, len(ids))
	for _, id := range ids {
		counts[id]++
	}

	// Walked in document order rather than in map order, so that two grams tied
	// on both count and length resolve to the first one in the document instead
	// of to whichever one the map happened to yield.
	best, bestCount, bestRunes := int32(-1), 0, int32(0)
	for i, id := range ids {
		var runes int32
		for j := i; j < i+n; j++ {
			runes += g.runes[j]
		}
		if c := counts[id]; c > bestCount || (c == bestCount && runes > bestRunes) {
			best, bestCount, bestRunes = id, c, runes
		}
	}
	if bestCount < 2 {
		return 0
	}
	return g.coverage(n, ids, func(id int32) bool { return id == best })
}

// repeat is the share of the document sitting inside a gram of n syllables that
// occurs more than once. A syllable covered by two repeated grams is counted
// once, so the result is a share of the text rather than a count of
// coincidences.
func (g *grams) repeat(n int) float64 {
	if len(g.syllables) < n {
		return 0
	}
	ids := g.ids(n)
	counts := make(map[int32]int, len(ids))
	for _, id := range ids {
		counts[id]++
	}
	return g.coverage(n, ids, func(id int32) bool { return counts[id] > 1 })
}

// coverage is how much of the text sits inside a gram the predicate accepts,
// counting every syllable once however many grams cover it.
func (g *grams) coverage(n int, ids []int32, want func(int32) bool) float64 {
	covered := make([]bool, len(g.syllables))
	for i, id := range ids {
		if want(id) {
			for j := i; j < i+n; j++ {
				covered[j] = true
			}
		}
	}
	var runes int32
	for i, ok := range covered {
		if ok {
			runes += g.runes[i]
		}
	}
	return float64(runes) / float64(g.total)
}
