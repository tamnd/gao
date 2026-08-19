package mark

// Choosing which documents become questions.

import (
	"strings"

	"github.com/tamnd/gao/doc"
)

// A Reason is why a document did not become an item. Every document handed to a
// [Builder] leaves with exactly one of these or with an item, and the counts are
// printed, because the interesting failure of a task set builder is the one
// where it quietly rejects nine documents in ten and nobody notices until the
// set is too small to say anything.
type Reason string

// The reasons, in the order they are checked.
const (
	// NotSampled means the document was not in the fraction asked for. It is a
	// reason rather than a silence so that the sampling rate can be read back
	// off a run instead of taken on trust.
	NotSampled Reason = "not sampled"

	// TooShort is a document with less text than the window needs. A one line
	// page restores from almost no context and says more about the tokenizer
	// than about the model.
	TooShort Reason = "too short"

	// Unmarked is a document typed without its marks, which is not an answer
	// key. This is the one that matters and it is usually the largest.
	Unmarked Reason = "typed without marks"

	// Duplicate is a document whose identity has already been taken. Two items
	// with one answer inflate whatever the model does with that answer.
	Duplicate Reason = "already taken"
)

// Reasons is every rejection reason, which a report iterates so that a run
// prints a zero rather than omitting the line.
func Reasons() []Reason { return []Reason{NotSampled, TooShort, Unmarked, Duplicate} }

// Options is how a task set is selected.
type Options struct {
	// MinChars and MaxChars bound the answer. The window is cut at a line or
	// sentence boundary below MaxChars rather than mid word, because a question
	// that stops in the middle of a syllable is asking something else.
	MinChars int
	MaxChars int

	// MinMarked is the floor on the share of characters carrying a mark, which
	// is what separates a page of Vietnamese from a page of Vietnamese typed
	// without its marks. See the package comment for where the number is from.
	MinMarked float64

	// OneIn keeps one document in this many, chosen by the document identity
	// rather than by a random number, so that a build is reproducible without a
	// seed and two boxes given the same corpus produce the same set. Zero and
	// one both mean keep everything.
	OneIn int

	// Limit stops after this many items. Zero means no limit.
	Limit int
}

// Default is the selection the published set is built with.
//
// The length window is two hundred to two thousand characters, which is a
// paragraph to a short article: long enough that context is doing the work and
// short enough that a model with a small window is being asked a fair question.
//
// The marked floor is 0.12, against a language that runs at about 0.24. The
// gap is deliberate and it is wide. A page about a subject whose vocabulary
// happens to be short of marked vowels is still a page of Vietnamese, and a
// floor set at the average would throw away the documents that are hardest and
// keep the ones that are easiest. Half the average keeps everything except text
// that was actually typed bare, which sits near zero and is not a close call.
func Default() Options {
	return Options{MinChars: 200, MaxChars: 2000, MinMarked: 0.12, OneIn: 1}
}

// A Builder turns documents into items, one at a time, keeping the counts.
//
// It streams, because the corpus is several hundred gigabytes and a builder
// that wanted the documents in memory could not be pointed at it.
type Builder struct {
	opts     Options
	items    []Item
	seen     map[doc.Hash]bool
	rejected map[Reason]int
	read     int
}

// NewBuilder returns a builder. A zero value in an option means the default for
// that option and not zero, because a length window of nothing is never what
// somebody meant.
func NewBuilder(o Options) *Builder {
	d := Default()
	if o.MinChars <= 0 {
		o.MinChars = d.MinChars
	}
	if o.MaxChars <= 0 {
		o.MaxChars = d.MaxChars
	}
	if o.MinMarked <= 0 {
		o.MinMarked = d.MinMarked
	}
	if o.OneIn <= 0 {
		o.OneIn = 1
	}
	return &Builder{
		opts:     o,
		seen:     map[doc.Hash]bool{},
		rejected: map[Reason]int{},
	}
}

// Add offers one document. It reports the item it made, or the reason it did
// not make one.
func (b *Builder) Add(id doc.Hash, text string) (Item, Reason, bool) {
	b.read++
	if b.opts.Limit > 0 && len(b.items) >= b.opts.Limit {
		return Item{}, NotSampled, false
	}
	// Sampling comes first so that the rate is over the corpus rather than over
	// whatever survived the other checks, which is the only version of it that
	// can be reasoned about before a run.
	if b.opts.OneIn > 1 && doc.Shard(id, b.opts.OneIn) != 0 {
		b.rejected[NotSampled]++
		return Item{}, NotSampled, false
	}
	if b.seen[id] {
		b.rejected[Duplicate]++
		return Item{}, Duplicate, false
	}

	answer := window(text, b.opts.MaxChars)
	it := NewItem(id, answer)
	if it.Chars < b.opts.MinChars {
		b.rejected[TooShort]++
		return Item{}, TooShort, false
	}
	if it.MarkedShare() < b.opts.MinMarked {
		b.rejected[Unmarked]++
		return Item{}, Unmarked, false
	}

	b.seen[id] = true
	b.items = append(b.items, it)
	return it, "", true
}

// Items is what the builder kept.
func (b *Builder) Items() []Item { return b.items }

// Read is how many documents were offered, and Rejected is how many were turned
// away, per reason. The two account for every document handed in.
func (b *Builder) Read() int { return b.read }

// Rejected returns the count for one reason.
func (b *Builder) Rejected(r Reason) int { return b.rejected[r] }

// window cuts the text to at most max characters, at a boundary.
//
// It prefers to end at a line, then at a sentence, and only cuts mid text if
// neither is available in the second half of the window. A question that ends
// halfway through a syllable is a different and easier question, because the
// fragment restricts the answer.
func window(text string, max int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	cut := string(runes[:max])
	half := max / 2
	for _, end := range []string{"\n", ". ", "! ", "? "} {
		if i := strings.LastIndex(cut, end); i > 0 && len([]rune(cut[:i])) >= half {
			return strings.TrimSpace(cut[:i+len(end)])
		}
	}
	// No boundary in the second half, so cut at the last space rather than
	// inside a syllable.
	if i := strings.LastIndex(cut, " "); i > 0 {
		return strings.TrimSpace(cut[:i])
	}
	return strings.TrimSpace(cut)
}
