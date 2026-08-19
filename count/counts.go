package count

import (
	"fmt"
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
//
// Documents land in a staging area first and only join the totals when the file
// they came out of finishes. An ingest works one file at a time and stops at the
// first failure, so the file being read is the only one staged, and the totals
// are always the counts of files that are also in the ledger. [Tally.Commit] and
// [Tally.Drop] are the two ends of that.
type Tally struct {
	// Tokenizer names the tokenizer, or is empty when the run did not tokenize.
	Tokenizer string

	mu   sync.Mutex
	by   map[doc.Source]*Counts
	open map[doc.Source]*Counts
}

// Add folds a document into the counts staged for the file being read.
func (t *Tally) Add(d *doc.Document) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open == nil {
		t.open = make(map[doc.Source]*Counts)
	}
	c, ok := t.open[d.Source]
	if !ok {
		c = &Counts{}
		t.open[d.Source] = c
	}
	c.Add(d)
}

// Commit folds what the finished file counted into the totals.
//
// Called once a file is through and in the ledger. Calling it on a file that
// counted nothing is a no-op, which is what a run of a source with no documents
// in it should be.
func (t *Tally) Commit() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by == nil {
		t.by = make(map[doc.Source]*Counts)
	}
	for s, staged := range t.open {
		c, ok := t.by[s]
		if !ok {
			c = &Counts{}
			t.by[s] = c
		}
		c.Merge(*staged)
	}
	clear(t.open)
}

// Drop throws away what a file counted before it failed.
//
// This is the whole reason the staging exists. A file that dies partway through
// leaves no ledger entry, so the next run fetches it again from the front and
// counts every document in it a second time, and the counts that were kept from
// the first attempt are added on top. gamingpc read most of a 25.2 GB HPLT shard
// before hitting a record that does not parse, and the counts.json it left
// carried 17683770 documents that the completed run then counted again, which
// put the box 34% over on a number nothing else in the directory disagreed with.
func (t *Tally) Drop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	clear(t.open)
}

// Staged is what the file being read has counted so far, which is not part of
// any total until it finishes.
func (t *Tally) Staged() Counts {
	t.mu.Lock()
	defer t.mu.Unlock()
	var c Counts
	for _, staged := range t.open {
		c.Merge(*staged)
	}
	return c
}

// Counting returns a function that folds each document into the tally, and, when
// a tokenizer is given, fills in the document's token count on the way past.
//
// This is deliberately the only place tokens are set. The ingest contract runs
// before this, so a document that was turned away is never tokenized, and the
// measured 0.5 to 1.1 MB per second per core is spent only on text that is
// actually in the corpus. That it is one core is the whole cost: this runs on
// the goroutine decoding the file, so an ingest with a tokenizer moves at the
// tokenizer's rate however many cores the box has.
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

// Seed starts a tally from counts that were taken before it, which is what a
// resumed ingest does with the report the earlier run left in the directory.
//
// A file already in the ledger is not fetched again, so nothing re-counts it,
// and a tally that starts empty writes a counts.json describing the session
// rather than the corpus in the directory. server1 fetched three FineWeb2 files,
// 6962000 documents and 29043690013 characters, and the resumed run zeroed all
// of it: the parts were in the store and the ledger still named the files, and
// the only record of what was in them was a terminal.
//
// Two different tokenizers are refused rather than added, for the reason in
// [ErrMixedTokenizers]. An untokenized report seeding a tokenized run is the
// same refusal, since the token column would then cover some of the corpus and
// nothing would say which part.
func (t *Tally) Seed(r Report) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r.Tokenizer != t.Tokenizer {
		return fmt.Errorf("%w: the counts already here were taken with %s and this run is using %s",
			ErrMixedTokenizers, named(r.Tokenizer), named(t.Tokenizer))
	}
	if t.by == nil {
		t.by = make(map[doc.Source]*Counts)
	}
	for _, sc := range r.Sources {
		c, ok := t.by[sc.Source]
		if !ok {
			c = &Counts{}
			t.by[sc.Source] = c
		}
		c.Merge(sc.Counts)
	}
	return nil
}

// named is a tokenizer for an error message, where the empty one is a sentence
// rather than a gap between two spaces.
func named(tokenizer string) string {
	if tokenizer == "" {
		return "no tokenizer"
	}
	return tokenizer
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
