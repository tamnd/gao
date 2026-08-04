package dem

import (
	"slices"
	"sync"

	"github.com/tamnd/gao/doc"
)

// Counts is what gao knows about a body of text, in the four units it publishes.
//
// Bytes here is UTF-8 bytes of extracted text and nothing else. It is not the
// size of the file the text arrived in, not the compressed size, and not the
// Parquet size. Those numbers are three to ten times apart from each other and
// from this one, and a corpus that quotes whichever was to hand is a corpus
// whose size is unfalsifiable. The ingest ledger records transfer sizes and this
// records text sizes, and they are different columns in different files because
// they answer different questions.
type Counts struct {
	Documents int64 `json:"documents"`
	Bytes     int64 `json:"bytes"`
	Chars     int64 `json:"chars"`
	Syllables int64 `json:"syllables"`

	// Tokens is zero when the run did not tokenize. A report says which by
	// naming its tokenizer or leaving the name empty, rather than by leaving the
	// reader to guess whether zero tokens means none were counted or none were
	// found.
	Tokens int64 `json:"tokens"`
}

// Add folds one document in. The document's own shape columns are used rather
// than recounted, because they were computed at ingest against the normalized
// text and recounting here would be a second answer to a question that already
// has one.
func (c *Counts) Add(d *doc.Document) {
	c.Documents++
	c.Bytes += int64(len(d.Text))
	c.Chars += int64(d.NChars)
	c.Syllables += int64(d.NSyllables)
	c.Tokens += int64(d.NTokens)
}

// Merge folds another set of counts in, which is how per-shard counts become a
// per-source count and per-box counts become a corpus count.
func (c *Counts) Merge(o Counts) {
	c.Documents += o.Documents
	c.Bytes += o.Bytes
	c.Chars += o.Chars
	c.Syllables += o.Syllables
	c.Tokens += o.Tokens
}

// CharsPerToken is the measured fertility of the tokenizer on this text, and it
// is the number P07-5 predicts at 3.0 for Vietnamese. It returns zero when the
// text was not tokenized, since a ratio against an uncounted denominator is
// worse than no ratio.
func (c Counts) CharsPerToken() float64 {
	if c.Tokens == 0 {
		return 0
	}
	return float64(c.Chars) / float64(c.Tokens)
}

// TokensPerSyllable is the cost multiplier that shows up on every training run
// and as a divisor on every context window.
func (c Counts) TokensPerSyllable() float64 {
	if c.Syllables == 0 || c.Tokens == 0 {
		return 0
	}
	return float64(c.Tokens) / float64(c.Syllables)
}

// BytesPerChar is how much the diacritics cost in UTF-8. ASCII would be 1.00.
func (c Counts) BytesPerChar() float64 {
	if c.Chars == 0 {
		return 0
	}
	return float64(c.Bytes) / float64(c.Chars)
}

// A Tally accumulates counts per source across a run.
//
// It is written to from the ingest's decoding goroutines, so it locks. The lock
// is held for four additions and never across anything that can block, which is
// why one mutex is enough at the rate documents arrive.
type Tally struct {
	// Tokenizer names the tokenizer, or is empty when the run did not tokenize.
	Tokenizer string

	mu sync.Mutex
	by map[doc.Source]*Counts
}

// Add folds a document into its source's counts.
func (t *Tally) Add(d *doc.Document) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by == nil {
		t.by = make(map[doc.Source]*Counts)
	}
	c, ok := t.by[d.Source]
	if !ok {
		c = &Counts{}
		t.by[d.Source] = c
	}
	c.Add(d)
}

// Counting returns a function that folds each document into the tally, and, when
// a tokenizer is given, fills in the document's token count on the way past.
//
// This is deliberately the only place tokens are set. The ingest contract runs
// before this, so a document that was turned away is never tokenized, and the
// 11 MB per second per core is spent only on text that is actually in the
// corpus.
func (t *Tally) Counting(tok *Tokenizer, next func(*doc.Document) error) func(*doc.Document) error {
	if tok != nil {
		t.Tokenizer = tok.Model().Name
	}
	return func(d *doc.Document) error {
		if tok != nil {
			d.NTokens = uint32(tok.Count(d.Text))
		}
		t.Add(d)
		if next != nil {
			return next(d)
		}
		return nil
	}
}

// Source returns the counts for one source.
func (t *Tally) Source(s doc.Source) Counts {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.by[s]; ok {
		return *c
	}
	return Counts{}
}

// Sources returns the sources this tally has seen, in a stable order, so that
// two runs over the same material print the same report.
func (t *Tally) Sources() []doc.Source {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]doc.Source, 0, len(t.by))
	for s := range t.by {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// Total is every source added together.
func (t *Tally) Total() Counts {
	var c Counts
	for _, s := range t.Sources() {
		c.Merge(t.Source(s))
	}
	return c
}

// Natural is the corpus size. Model generated text is a separate artifact with
// its own name and its own count, so it is summed separately rather than folded
// into a headline that reads as a claim about Vietnamese people's writing.
func (t *Tally) Natural() Counts {
	var c Counts
	for _, s := range t.Sources() {
		if s.Natural() {
			c.Merge(t.Source(s))
		}
	}
	return c
}
