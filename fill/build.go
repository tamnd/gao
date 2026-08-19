package fill

// Choosing which passages become questions, and what the wrong answers are.

import (
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/normalize"
)

// A Reason is why a document did not become an item.
//
// Every document handed to a [Builder] leaves with an item or with exactly one
// of these, and the counts are printed. The interesting failure of a task set
// builder is the one where it quietly rejects nine documents in ten and nobody
// notices until the set is too small to say anything.
type Reason string

// The reasons, in the order they are checked.
const (
	// Duplicate is a document that was already offered. One item per document,
	// so that holding the set out of training is a list of identities.
	Duplicate Reason = "duplicate"

	// NotSampled is a document the sample did not take.
	NotSampled Reason = "not sampled"

	// TooShort is a passage with less text than the blank needs context from.
	TooShort Reason = "too short"

	// Unmarked is a page typed without its diacritics. It is Vietnamese, and it
	// is not a passage anybody can be asked to fill a blank in, because the
	// bare form of the answer would be sitting in the choices.
	Unmarked Reason = "unmarked"

	// NoBlank is a passage with nothing worth taking out: every syllable in it
	// is either a function word, too close to an edge, or repeated elsewhere in
	// the passage where the answer could be copied from.
	NoBlank Reason = "no blank"

	// ThinBand is an answer with too few syllables near it in the frequency
	// ranking to build the candidates at the frequency rank this item was meant
	// to have. Taking a different rank instead would bend the spread that holds
	// the frequency baseline down to chance, so the item is dropped and counted.
	ThinBand Reason = "thin band"
)

// Reasons is every rejection reason, which a report iterates so that a run
// prints a zero rather than omitting the line.
func Reasons() []Reason {
	return []Reason{Duplicate, NotSampled, TooShort, Unmarked, NoBlank, ThinBand}
}

// Options is how the set is selected.
type Options struct {
	// Sample is the share of documents offered that are considered at all.
	Sample float64

	// MinChars and MaxChars are the passage length window, in characters.
	MinChars, MaxChars int

	// Context is how many syllables have to sit on each side of the blank. A
	// blank in the first three syllables of a passage is a question with no
	// question in it.
	Context int

	// Function is how many of the commonest syllables are never blanked. The top
	// of a Vietnamese frequency ranking is của, và, là, các, có, được and the
	// rest of the grammar, and a blank over one of them is answered by syntax
	// alone.
	Function int

	// Band is how far up and down the frequency ranking a distractor may be
	// drawn from.
	Band int

	// MinMarked is the share of letters that have to carry a mark for the page
	// to count as properly typed Vietnamese.
	MinMarked float64
}

// Default is the selection the published set is built with.
//
// The passage window is 200 to 1200 characters: long enough that the context is
// doing the work and short enough that four scored continuations of it are
// cheap, which is the whole point of this benchmark.
//
// Three syllables of context on each side is the floor rather than the target.
// Most blanks land in the middle of a passage, and the floor is there to stop
// the pathological item rather than to shape the ordinary one.
//
// The top 200 syllables are off limits. In Vietnamese that is the function
// words and a little of the commonest content vocabulary, and it is drawn
// generously because a proxy that saturates is worth nothing and a proxy that
// is slightly too hard is still ranking recipes.
//
// The band is 200 ranks either side. Wide enough that a syllable in the middle
// of the ranking has neighbors to spare, narrow enough that the neighbors are
// genuinely comparable in frequency, and it is a default until an ablation
// moves it like every other threshold here.
//
// The marked floor is 0.12 against a language that runs at about 0.24, which is
// the floor dau selects on and it is set for the same reason: half the average
// keeps everything except text that was actually typed bare.
func Default() Options {
	return Options{
		Sample:    1,
		MinChars:  200,
		MaxChars:  1200,
		Context:   3,
		Function:  200,
		Band:      200,
		MinMarked: 0.12,
	}
}

// A Builder turns documents into items, one at a time, keeping the counts.
//
// It streams, because the corpus is several hundred gigabytes and a builder
// that wanted the documents in memory could not be pointed at it.
type Builder struct {
	o        Options
	v        *Vocabulary
	items    []Item
	read     int
	rejected map[Reason]int
	seen     map[doc.Hash]bool
}

// NewBuilder returns a builder over a vocabulary. A zero value in an option
// means the default for that option and not zero, because a passage window of
// nothing is never what somebody meant.
func NewBuilder(o Options, v *Vocabulary) *Builder {
	d := Default()
	if o.Sample <= 0 {
		o.Sample = d.Sample
	}
	if o.MinChars <= 0 {
		o.MinChars = d.MinChars
	}
	if o.MaxChars <= 0 {
		o.MaxChars = d.MaxChars
	}
	if o.Context <= 0 {
		o.Context = d.Context
	}
	if o.Function <= 0 {
		o.Function = d.Function
	}
	if o.Band <= 0 {
		o.Band = d.Band
	}
	if o.MinMarked <= 0 {
		o.MinMarked = d.MinMarked
	}
	return &Builder{o: o, v: v, rejected: map[Reason]int{}, seen: map[doc.Hash]bool{}}
}

// Add offers one document. It reports the item it made, or the reason it did
// not make one.
func (b *Builder) Add(id doc.Hash, text string) (Item, Reason, bool) {
	b.read++
	if b.seen[id] {
		return b.reject(Duplicate)
	}
	b.seen[id] = true
	if float64(draw(id, 1)%1_000_000)/1_000_000 >= b.o.Sample {
		return b.reject(NotSampled)
	}

	passage := window(text, b.o.MaxChars)
	if len([]rune(passage)) < b.o.MinChars {
		return b.reject(TooShort)
	}
	if markedShare(passage) < b.o.MinMarked {
		return b.reject(Unmarked)
	}

	spans := syllables(passage)
	open := b.blankable(passage, spans)
	if len(open) == 0 {
		return b.reject(NoBlank)
	}

	// Which of the eligible positions gets blanked is decided by the document
	// identity rather than by taking the first, so the set is not made entirely
	// of the opening sentence of every page and it still rebuilds identically.
	at := spans[open[draw(id, 2)%uint64(len(open))]]
	answer := strings.ToLower(at.text)

	rank := int(draw(id, 3) % Candidates)
	distractors, ok := b.distractors(passage, answer, rank)
	if !ok {
		return b.reject(ThinBand)
	}

	it := Item{
		DocID:  id,
		Prompt: passage[:at.start] + Blank + passage[at.end:],
		Rank:   rank,
	}
	it.Choices, it.Answer = arrange(id, answer, distractors)
	b.items = append(b.items, it)
	return it, "", true
}

// blankable is the indexes into spans of every syllable that could be taken out.
//
// A syllable qualifies when it is far enough from both edges to have context on
// each side, is not one of the commonest [Options.Function] syllables, is
// written in lower case, is known to the vocabulary, and appears nowhere else in
// the passage.
//
// The last two conditions are the ones that matter. A syllable the vocabulary
// has never seen has no frequency neighbors, so there is nothing to build
// comparable distractors out of. A syllable that appears twice in the passage
// can be copied from its other occurrence, and a benchmark a model can win by
// pattern matching inside the prompt measures pattern matching.
//
// Lower case only, because a capitalized syllable is the opening of a sentence
// or part of a name, and either way the item stops being about the language and
// starts being about capitalization.
func (b *Builder) blankable(passage string, spans []span) []int {
	seen := map[string]int{}
	for _, s := range spans {
		seen[normalize.Fold(s.text)]++
	}

	var out []int
	for i, s := range spans {
		switch {
		case i < b.o.Context || i >= len(spans)-b.o.Context:
			continue
		case s.text != strings.ToLower(s.text):
			continue
		case seen[normalize.Fold(s.text)] > 1:
			continue
		}
		at, ok := b.v.Rank(strings.ToLower(s.text))
		if !ok || at < b.o.Function {
			continue
		}
		out = append(out, i)
	}
	return out
}

// distractors picks the wrong answers.
//
// It takes rank of them from above the answer in the frequency ranking and the
// rest from below, nearest first, so the answer ends up with exactly rank
// candidates more common than it is. Spread over a set, that puts the answer at
// every frequency position equally often, and the strategy of picking the most
// common candidate scores chance.
//
// A candidate that folds to the answer's bare spelling is refused. Choosing
// between ma, má and mà is a real task and it is dau's task, and letting it in
// here would mean two benchmarks measuring the same thing while appearing to
// measure two.
func (b *Builder) distractors(passage, answer string, rank int) ([]string, bool) {
	above, below := b.v.Neighbors(answer, b.o.Band)
	want := normalize.Fold(answer)
	in := map[string]bool{}
	for _, s := range syllables(passage) {
		in[normalize.Fold(s.text)] = true
	}

	take := func(from []string, n int) ([]string, bool) {
		out := make([]string, 0, n)
		for _, s := range from {
			if len(out) == n {
				break
			}
			if normalize.Fold(s) == want || in[normalize.Fold(s)] {
				continue
			}
			out = append(out, s)
		}
		return out, len(out) == n
	}

	hi, ok := take(above, rank)
	if !ok {
		return nil, false
	}
	lo, ok := take(below, Candidates-1-rank)
	if !ok {
		return nil, false
	}
	return append(hi, lo...), true
}

// arrange puts the answer among the distractors in an order the document
// identity decides, and reports where it landed.
//
// The order has to be reproducible, or the set is a different set on every
// build and no two runs of the slate are comparable. It also has to not be the
// same position every time, which is the oldest way a multiple choice benchmark
// gets won without being read.
func arrange(id doc.Hash, answer string, distractors []string) ([]string, int) {
	choices := make([]string, 0, Candidates)
	choices = append(choices, answer)
	choices = append(choices, distractors...)

	// Fisher and Yates, drawing from the identity rather than from a clock.
	for i := len(choices) - 1; i > 0; i-- {
		j := int(draw(id, uint64(10+i)) % uint64(i+1))
		choices[i], choices[j] = choices[j], choices[i]
	}
	for i, s := range choices {
		if s == answer {
			return choices, i
		}
	}
	return choices, 0
}

// Items is what the builder kept.
func (b *Builder) Items() []Item { return b.items }

// Read is how many documents were offered.
func (b *Builder) Read() int { return b.read }

// Rejected is how many were turned away for one reason. Read accounts for every
// item plus every rejection.
func (b *Builder) Rejected(r Reason) int { return b.rejected[r] }

func (b *Builder) reject(r Reason) (Item, Reason, bool) {
	b.rejected[r]++
	return Item{}, r, false
}

// window cuts the text to at most max characters, at a boundary.
//
// It prefers to end at a line, then at a sentence, and only cuts mid text if
// neither is available in the second half of the window. A passage that ends
// halfway through a sentence is a passage whose last clause is missing, and the
// blank might be the thing that clause was about.
func window(text string, max int) string {
	r := []rune(text)
	if len(r) <= max {
		return strings.TrimSpace(text)
	}
	cut := string(r[:max])
	for _, end := range []string{"\n", ". "} {
		if i := strings.LastIndex(cut, end); i > len(cut)/2 {
			return strings.TrimSpace(cut[:i+len(end)])
		}
	}
	return strings.TrimSpace(cut)
}

// draw is a number the document identity decides, salted so that the choice of
// position, the choice of frequency rank and the shuffle do not move together.
//
// It is splitmix over the first eight bytes of the identity, which is blake3
// and is already uniform, so this is about separating the salts rather than
// about mixing.
func draw(id doc.Hash, salt uint64) uint64 {
	var x uint64
	for i := range 8 {
		x = x<<8 | uint64(id[i])
	}
	x += salt * 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
