// Package taste reads the sample a plan drew and says what a stored byte of each
// layer holds.
//
// Nếm is to taste. mau decides which shards of the layers nobody opened get
// read, and this is the part that opens them. The two are one command apart on
// purpose: the plan is a claim about which bytes will be read, and this is the
// record of which bytes were, and a project that publishes an estimate needs to
// be able to hand somebody both.
//
// # What a reading is and what it is not
//
// A take is the front of a compressed shard. That is not a preference, it is
// what a compressed stream allows, and it is the reason mau exists at all. So
// what comes back here is the text at the front of the shards the seed drew,
// counted the same way the ingest counts: documents, text bytes, characters,
// syllables, and tokens when a tokenizer is loaded.
//
// The number the estimate wants out of it is a rate, tokens or text per stored
// byte, and a rate is a ratio of two things this measures separately. Text is
// counted off the documents that came out. Read is the compressed bytes that
// went in. Neither is inferred from the other, and a layer whose shards were
// read at a different rate than they were listed at says so rather than being
// averaged into agreement.
//
// # The tail of a prefix
//
// A prefix of a compressed stream ends inside a document, always, unless the
// take was the whole file. That last partial document is dropped, and its
// compressed bytes are not. So Read is a hair larger than the compressed bytes
// that produced the text, and the rate comes back a hair low. On a 40 MB take
// over documents averaging a few kilobytes that is one part in ten thousand and
// it is in the conservative direction, which is the only direction an error in
// this project is allowed to be in without an argument. It is reported rather
// than corrected, because correcting it means guessing how much of the tail
// belonged to the document that got dropped.
package taste

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tamnd/gao/count"
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/layers"
	"github.com/tamnd/gao/sample"
)

// MinDocs is how many documents a take has to produce before the take is a
// reading rather than an accident. A 40 MB prefix of Vietnamese web text is tens
// of thousands of documents, so a take in single figures means the decoder got
// one enormous record or the bytes were not what the listing said they were.
const MinDocs = 100

// A Take is one shard prefix that was read.
type Take struct {
	Layer string `json:"layer"`
	Path  string `json:"path"`

	// Bytes is the compressed bytes fetched and Of is what the whole shard
	// holds, both as the plan asked for them.
	Bytes int64 `json:"bytes"`
	Of    int64 `json:"of"`

	// Digest is the sha256 of the prefix that was read, which is what makes this
	// checkable byte for byte rather than file by file. A third party with the
	// seed draws the same shards, and with this they can confirm they read the
	// same bytes of them.
	Digest string `json:"digest"`

	// Whole is set when the take was the entire shard, which is the one case
	// where the tail is not dropped and the rate is exact.
	Whole bool `json:"whole"`

	count.Counts
}

// A Reading is one layer's takes folded together.
type Reading struct {
	Layer  string `json:"layer"`
	Rank   int    `json:"rank"`
	Stored int64  `json:"stored"`

	// Files is how many shards the layer has and Drawn is how many of them this
	// read, carried through from the plan so that a reading can be read without
	// the plan beside it.
	Files int `json:"files"`
	Drawn int `json:"drawn"`

	// Read is the compressed bytes that went in.
	Read int64 `json:"read"`

	count.Counts

	Takes []Take `json:"takes"`
}

// Pack is text bytes per stored byte, which is the quantity that turns the
// manifest's compressed sizes into an amount of text.
func (r Reading) Pack() float64 {
	if r.Read <= 0 {
		return 0
	}
	return float64(r.Bytes) / float64(r.Read)
}

// Yield is tokens per stored byte, zero when the run did not tokenize.
func (r Reading) Yield() float64 {
	if r.Read <= 0 {
		return 0
	}
	return float64(r.Tokens) / float64(r.Read)
}

// Text is the whole layer as this reading scales it, which is the number the
// estimate is built out of and the reason a rate measured off the front of a few
// shards is worth arguing about.
func (r Reading) Text() int64 { return int64(r.Pack() * float64(r.Stored)) }

// A Sample is one run of a plan.
type Sample struct {
	Source string `json:"source"`
	Seed   string `json:"seed"`

	// Digest is the plan's digest, over the takes it drew. It is recorded here
	// so that a reading and the plan it came from can be matched without
	// trusting that whoever ran it ran the right one.
	Digest string `json:"digest"`

	// Tokenizer names what counted the tokens, or is empty when nothing did. A
	// reading with no tokenizer has a token column of zeroes, and this is what
	// says so rather than leaving it to be guessed.
	Tokenizer string `json:"tokenizer,omitempty"`

	Readings []Reading `json:"readings"`

	count.Counts

	// Read is the compressed bytes fetched across every take.
	Read int64 `json:"read"`
}

// Of folds the takes of a finished run into a sample, in the order the plan
// listed the layers.
//
// The plan is passed in rather than reconstructed because the digest belongs to
// it: a sample says which plan it performed, and a sample that worked that out
// from its own takes would agree with itself no matter which plan was run.
func Of(p sample.Plan, tokenizer string, takes []Take) Sample {
	s := Sample{
		Source:    p.Source,
		Seed:      p.Seed,
		Digest:    p.Digest.String()[:16],
		Tokenizer: tokenizer,
	}

	by := make(map[string][]Take, len(p.Reads))
	for _, t := range takes {
		by[t.Layer] = append(by[t.Layer], t)
	}
	for _, r := range p.Reads {
		one := Reading{Layer: r.Layer, Rank: r.Rank, Stored: r.Stored, Files: r.Files}
		for _, t := range by[r.Layer] {
			one.Drawn++
			one.Read += t.Bytes
			one.Merge(t.Counts)
			one.Takes = append(one.Takes, t)
		}
		sort.Slice(one.Takes, func(i, j int) bool { return one.Takes[i].Path < one.Takes[j].Path })
		s.Readings = append(s.Readings, one)
		s.Read += one.Read
		s.Merge(one.Counts)
	}
	return s
}

// Layers turns the sample into the layer file layers reads, carrying every
// layer of the plan through whether it was read or not, so that what comes out
// is a file that describes the source rather than a file that describes the
// run.
//
// Read is the compressed bytes fetched and Text is the text they held, which
// are the two numbers layers divides. The layers the sample has no reading for
// come through with their stored size and nothing else, which is exactly what
// layers bounds.
func (s Sample) Layers(all []layers.Layer) []layers.Layer {
	got := make(map[string]Reading, len(s.Readings))
	for _, r := range s.Readings {
		got[r.Layer] = r
	}

	out := make([]layers.Layer, 0, len(all))
	for _, l := range all {
		r, ok := got[l.Name]
		if !ok || r.Read == 0 {
			out = append(out, l)
			continue
		}
		l.Read = r.Read
		l.Text = r.Bytes
		l.Tokens = r.Tokens
		l.Tokenizer = s.Tokenizer
		out = append(out, l)
	}
	return out
}

// Blocking is the reasons this is not a reading anybody should publish, as
// opposed to one that is worth less than it looks.
func (s Sample) Blocking() []string {
	var out []string
	if s.Source == "" {
		out = append(out, "the sample does not say what source it read")
	}
	if s.Seed == "" {
		out = append(out, "the sample names no seed, so nobody can draw the same shards again")
	}
	if len(s.Readings) == 0 {
		out = append(out, "no layer was read")
	}
	if s.Read == 0 && len(s.Readings) > 0 {
		out = append(out, "no byte was fetched, so this is a plan and not a reading")
	}

	var empty []Reading
	for _, r := range s.Readings {
		if r.Read > 0 && r.Documents == 0 {
			empty = append(empty, r)
		}
	}
	switch n := len(empty); {
	case n == 1:
		out = append(out, fmt.Sprintf(
			"%s was read and produced no documents, so what came back was not the text this layer holds",
			empty[0].Layer))
	case n > 1:
		out = append(out, fmt.Sprintf(
			"%d layers were read and produced no documents, starting with %s, so what came back was not text",
			n, empty[0].Layer))
	}
	return out
}

// Faults are the reasons a reading that happened is worth less than the line it
// fills in.
func (s Sample) Faults() []string {
	if len(s.Blocking()) > 0 {
		return nil
	}
	var out []string

	var thin []Take
	for _, r := range s.Readings {
		for _, t := range r.Takes {
			if t.Documents < MinDocs {
				thin = append(thin, t)
			}
		}
	}
	switch n := len(thin); {
	case n == 1:
		out = append(out, fmt.Sprintf(
			"%s gave %s off %s, under the %d a prefix of this size should hold, so what was read is one long record rather than a stretch of the corpus",
			thin[0].Path, plural(int(thin[0].Documents), "document"), size(thin[0].Bytes), MinDocs))
	case n > 1:
		out = append(out, fmt.Sprintf(
			"%d takes gave under %d documents each, starting with %s at %d, so what was read off them is one long record rather than a stretch of the corpus",
			n, MinDocs, thin[0].Path, thin[0].Documents))
	}

	var small []Reading
	for _, r := range s.Readings {
		if r.Read > 0 && r.Read < layers.MinRead {
			small = append(small, r)
		}
	}
	switch n := len(small); {
	case n == 1:
		out = append(out, fmt.Sprintf(
			"%s was read off %s, under the %s a layer's rate needs before one long page moves it, so layers will not scale anything by it",
			small[0].Layer, size(small[0].Read), size(layers.MinRead)))
	case n > 1:
		out = append(out, fmt.Sprintf(
			"%d layers were read off under %s each, starting with %s at %s, so layers will not scale anything by them",
			n, size(layers.MinRead), small[0].Layer, size(small[0].Read)))
	}

	if s.Tokenizer == "" {
		out = append(out, "nothing tokenized this reading, so the token column is zero throughout and the rate it produces is a rate for text bytes rather than for tokens")
	}

	// The spread the plan already reported, carried into the reading, because a
	// layer read off one shard is a layer whose rate belongs to that shard and
	// the file that records the rate is the one somebody quotes it out of.
	var narrow []Reading
	for _, r := range s.Readings {
		if r.Drawn < sample.MinFiles {
			narrow = append(narrow, r)
		}
	}
	switch n := len(narrow); {
	case n == 1:
		out = append(out, fmt.Sprintf(
			"%s was read off %s, under the %d a reading of a layer spreads across, so its rate is a rate for those shards",
			narrow[0].Layer, plural(narrow[0].Drawn, "shard"), sample.MinFiles))
	case n > 1:
		out = append(out, fmt.Sprintf(
			"%d layers were read off fewer than %d shards each, starting with %s at %d, so their rates are rates for the shards that were drawn",
			n, sample.MinFiles, narrow[0].Layer, narrow[0].Drawn))
	}

	if lo, hi, ok := s.packSpread(); ok && hi > lo*1.25 {
		out = append(out, fmt.Sprintf(
			"the layers pack their text at between %.2fx and %.2fx, a spread of %.0f%%, so weighting the unread part by stored size carries at least that much of its own error",
			lo, hi, (hi/lo-1)*100))
	}
	return out
}

// packSpread is the thinnest and richest packing measured, which is the number
// that says whether a stored byte means the same thing across the source.
func (s Sample) packSpread() (lo, hi float64, ok bool) {
	for _, r := range s.Readings {
		p := r.Pack()
		if p <= 0 {
			continue
		}
		if !ok || p < lo {
			lo = p
		}
		if !ok || p > hi {
			hi = p
		}
		ok = true
	}
	return lo, hi, ok
}

// Holds reports whether this is a reading worth putting next to an estimate.
func (s Sample) Holds() bool { return len(s.Blocking()) == 0 && len(s.Faults()) == 0 }

// Verdict is the sentence somebody reads.
func (s Sample) Verdict() string {
	if why := s.Blocking(); len(why) > 0 {
		return "This is not a reading: " + strings.Join(why, "; and ") + "."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "This read %s off %s across %s of %s, at seed %s, and found %s holding %s of text",
		size(s.Read), plural(s.takes(), "shard"), plural(len(s.Readings), "layer"),
		s.Source, s.Seed, plural(int(s.Documents), "document"), size(s.Bytes))
	if s.Tokenizer != "" {
		fmt.Fprintf(&b, " and %s under %s", plural(int(s.Tokens), "token"), s.Tokenizer)
	}
	b.WriteString(".")

	faults := s.Faults()
	switch n := len(faults); n {
	case 0:
	case 1:
		fmt.Fprintf(&b, " One reading says this is worth less than the line it fills: %s.", faults[0])
	default:
		fmt.Fprintf(&b, " %d readings say this is worth less than the line it fills: %s.", n, strings.Join(faults, "; and "))
	}
	return b.String()
}

func (s Sample) takes() int {
	n := 0
	for _, r := range s.Readings {
		n += r.Drawn
	}
	return n
}

// Source is the enum the decoder is chosen by, since a sample names its source
// as the string the plan carried and the fetching side needs the value.
func Source(name string) (doc.Source, bool) {
	s := doc.Source(name)
	return s, s.Valid()
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// size prints bytes the way the rest of this project's reports do, which is
// decimal. A report whose header counts in one base and whose sentence counts in
// the other reads as two different numbers for the same bytes, and the first run
// of this command against real shards printed 12.0 MB in its table and 11.4 MB
// in its verdict.
func size(n int64) string { return fleet.Size(n) }
