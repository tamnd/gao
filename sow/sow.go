// Package sow is the generator card for gao-synth, and the recipe it is
// written against.
//
// Gieo is to sow. Everything else in this project harvests: the crawl, the
// Hugging Face union, the PDFs, the transcripts are all Vietnamese somebody
// already wrote. The deduplicated Vietnamese web is a bounded object, and past
// that boundary the only move left is to make text rather than to find it.
//
// Synthesis here is a rephrase pass over the top of the quality distribution
// rather than generation from nothing, which is what makes it defensible: every
// output has a real Vietnamese document behind it that a person wrote. That
// property is only worth anything if it is enforced, so a recipe names the slice
// it reads by digest and a card that does not is invention wearing a rephrase's
// name.
//
// The split between [Recipe] and [Card] is the point of the package. The recipe
// is everything that decides what the text will be: the generator, the styles
// and their prompts verbatim, the decoding settings and the seed, the filters,
// and the source. It is fixed and hashed before a single token is generated. The
// card is the recipe plus what actually happened: how much came out, what the
// gates rejected and why, what the contamination check found, which box it ran
// on, and what it cost. A card carries the digest of the recipe it was produced
// under, so a prompt quietly improved after seeing the output produces a card
// that does not match the recipe it claims.
//
// Two failures are worth naming because they do not look like failures.
//
// The first is narrowing. A model asked to rephrase produces a narrower
// distribution than it was given, every time, and 150 billion tokens of narrowed
// Vietnamese in a 1 trillion token mixture is a real change to what the model
// learns. The defense is styles, and the defense only works if the styles differ,
// so two styles sharing a prompt is refused rather than counted twice.
//
// The second is a reject rate of zero. Generated text that passed every gate did
// not pass them, it did not meet them: a generator whose output is never
// rejected is a generator whose gates are not running, and that reads in a
// release note exactly like a generator that is very good.
package sow

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/zeebo/blake3"
)

// Styles is the least number of distinct styles a recipe may sow with.
//
// The training mixture asks for four, and the floor is here rather than there
// because the mixture states an intention and this refuses a run that does not
// meet it. One style is a rephrase of the corpus into one voice, which is the
// narrowing failure with no defense against it at all.
const Styles = 4

// Budget is what the synthesis slice is allowed to cost, in GPU hours. It is
// roughly fourteen thousand, and a card that overran it says so rather than
// leaving somebody to notice from an invoice.
const Budget = 14000

// A Style is one voice the source is rephrased into.
type Style struct {
	// Name is the register, in Vietnamese, because that is what it names.
	Name string `json:"name"`

	// Prompt is the instruction verbatim, with {{item}} where the source
	// document goes. It is in the digest, since it is the whole of what makes
	// this style a different style.
	Prompt string `json:"prompt"`

	// Note is why this register is in the set. It is not in the digest.
	Note string `json:"note,omitempty"`
}

// Decoding is how the generator is sampled. All of it is in the digest, because
// a temperature change is a different generator as far as the output is
// concerned.
type Decoding struct {
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	MaxTokens   int     `json:"max_tokens"`

	// Seed is what makes a run repeatable. Generation without one produces text
	// nobody can produce again, including us, which puts the corpus outside its
	// own reproducibility rule.
	Seed int64 `json:"seed"`
}

// A Filter is one gate the generated text has to pass.
type Filter struct {
	Name string `json:"name"`

	// ConfigHash pins the gate's settings. A filter recorded without one is a
	// filter nobody can rerun, which is the same fault a stage without a config
	// hash has in a snapshot manifest.
	ConfigHash doc.Hash `json:"config_hash"`

	// Why is the sentence that goes on the card.
	Why string `json:"why,omitempty"`
}

// A Recipe is everything that decides what the generated text will be, fixed
// before any of it exists.
type Recipe struct {
	Version string `json:"version"`

	// Generator is the model doing the rephrasing, named and pinned.
	Generator string `json:"generator"`
	Revision  string `json:"revision"`

	// ReadGao says whether the generator was trained on gao. It is a field
	// rather than an assumption because the answer decides whether the whole
	// slice is worth generating: a model trained on gao rephrasing gao is the
	// corpus fed back into itself, and the tokens it produces carry no
	// information the corpus did not already have.
	ReadGao bool `json:"read_gao"`

	// Source is the slice being rephrased, by name and by the digest of the
	// slice record. The digest is what makes this a rephrase of something
	// specific rather than a claim about one.
	Source       string   `json:"source"`
	SourceDigest doc.Hash `json:"source_digest"`

	// Target is how many tokens the slice is meant to produce, which the
	// training mixture already spends.
	Target int64 `json:"target"`

	Styles   []Style  `json:"styles"`
	Decoding Decoding `json:"decoding"`
	Filters  []Filter `json:"filters"`

	// Roster is the benchmark roster version the output is checked against. It
	// is in the recipe rather than in the card because deciding what counts as
	// contamination after seeing the contamination is the fault the whole
	// project is arranged against.
	Roster string `json:"roster"`

	// Note is prose. It is not in the digest.
	Note string `json:"note,omitempty"`
}

// Rejects is what the gates threw away, by filter name.
type Rejects map[string]int64

// A Card is a recipe and what happened when it was run. It is what gets
// published beside the data.
type Card struct {
	// Recipe is the digest of the recipe this was produced under.
	Recipe doc.Hash `json:"recipe"`

	// Version is the recipe version, carried in plain text so a person reading
	// the card does not have to resolve a hash to know which one it was.
	Version string `json:"version"`

	// Box is the machine it ran on, which is provenance the same way a stage
	// version is, and the fleet item asks for it by name.
	Box string `json:"box"`

	// Batch is the batch size and anything else about how it was driven, which
	// the fleet item also asks for, since throughput numbers without it are not
	// numbers anybody can reproduce.
	Batch string `json:"batch,omitempty"`

	RanAt time.Time `json:"ran_at"`

	// Generated and Kept are documents. Kept is what survived the gates.
	Generated int64 `json:"generated"`
	Kept      int64 `json:"kept"`

	// Tokens is what was kept, measured with the named tokenizer, and it is the
	// number the mixture spends. It is never added to a natural count.
	Tokens    int64  `json:"tokens"`
	Tokenizer string `json:"tokenizer"`

	// Rejects accounts for every document that did not survive, by the filter
	// that stopped it. It has to add up to what came out minus what was kept,
	// because a card whose arithmetic does not close is a card describing a run
	// nobody watched.
	Rejects Rejects `json:"rejects"`

	// Contaminated is how many generated documents held a benchmark item and
	// were dropped for it. Zero is a result and it is reported as one.
	Contaminated int64 `json:"contaminated"`

	// GPUHours is what it cost.
	GPUHours float64 `json:"gpu_hours"`

	Note string `json:"note,omitempty"`
}

// Errors a recipe or a card can fail with.
var (
	ErrBadRecipe = errors.New("gieo: recipe is incomplete")
	ErrBadCard   = errors.New("gieo: card is incomplete")
)

// Digest identifies the recipe by what it will produce. The notes are outside
// it, here as everywhere else in this project, because an identifier that moves
// when somebody writes a clearer sentence teaches people to stop writing
// sentences.
func (r Recipe) Digest() doc.Hash {
	d := blake3.New()
	write := func(key, value string) {
		fmt.Fprintf(d, "%s %d:%s\n", key, len(value), value)
	}
	num := func(key string, v int64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(v))
		_, _ = d.Write([]byte(key))
		_, _ = d.Write(b[:])
	}

	write("version", r.Version)
	write("generator", r.Generator)
	write("revision", r.Revision)
	write("read_gao", fmt.Sprint(r.ReadGao))
	write("source", r.Source)
	write("source_digest", r.SourceDigest.String())
	write("roster", r.Roster)
	num("target", r.Target)
	write("temperature", fmt.Sprint(r.Decoding.Temperature))
	write("top_p", fmt.Sprint(r.Decoding.TopP))
	num("max_tokens", int64(r.Decoding.MaxTokens))
	num("seed", r.Decoding.Seed)

	styles := slices.Clone(r.Styles)
	slices.SortStableFunc(styles, func(a, b Style) int { return strings.Compare(a.Name, b.Name) })
	num("styles", int64(len(styles)))
	for _, s := range styles {
		write("style", s.Name)
		write("prompt", s.Prompt)
	}

	filters := slices.Clone(r.Filters)
	slices.SortStableFunc(filters, func(a, b Filter) int { return strings.Compare(a.Name, b.Name) })
	num("filters", int64(len(filters)))
	for _, f := range filters {
		write("filter", f.Name)
		write("config", f.ConfigHash.String())
	}
	return doc.Hash(d.Sum(nil))
}

// Style returns the named style.
func (r Recipe) Style(name string) (Style, bool) {
	for _, s := range r.Styles {
		if s.Name == name {
			return s, true
		}
	}
	return Style{}, false
}

// Filter returns the named filter.
func (r Recipe) Filter(name string) (Filter, bool) {
	for _, f := range r.Filters {
		if f.Name == name {
			return f, true
		}
	}
	return Filter{}, false
}

// Yield is the share of generated documents a card kept.
func (c Card) Yield() float64 {
	if c.Generated <= 0 {
		return 0
	}
	return float64(c.Kept) / float64(c.Generated)
}

// RejectRate is the share the gates threw away, which is the number that has to
// be reported and the number a run with its gates switched off reports as zero.
func (c Card) RejectRate() float64 {
	if c.Generated <= 0 {
		return 0
	}
	return float64(c.Generated-c.Kept) / float64(c.Generated)
}

// check reports every way a recipe is not one.
func (r Recipe) check() error {
	var problems []error
	if r.Version == "" {
		problems = append(problems, errors.New("the recipe has no version"))
	}
	if r.Generator == "" {
		problems = append(problems, errors.New("the recipe does not name a generator"))
	}
	if r.Revision == "" {
		problems = append(problems, errors.New("the generator is named and not pinned, so the recipe describes whatever that name points at today"))
	}
	if r.ReadGao {
		problems = append(problems, errors.New("the generator was trained on gao, so rephrasing gao with it feeds the corpus back into itself and the tokens carry nothing the corpus did not already have"))
	}
	if r.Source == "" {
		problems = append(problems, errors.New("the recipe does not name a source, and synthesis without one is generation from nothing rather than a rephrase"))
	}
	if r.SourceDigest.IsZero() {
		problems = append(problems, errors.New("the source is named and not pinned, so nobody can tell which text was rephrased"))
	}
	if r.Target <= 0 {
		problems = append(problems, errors.New("the recipe does not say how much it is meant to produce"))
	}
	if r.Roster == "" {
		problems = append(problems, errors.New("the recipe does not name the benchmark roster the output is checked against"))
	}

	if len(r.Styles) < Styles {
		problems = append(problems, fmt.Errorf("the recipe sows in %d styles and %d is the floor, since a rephrase narrows the distribution it was given and the styles are the only thing standing against that",
			len(r.Styles), Styles))
	}
	seen := make(map[string]bool, len(r.Styles))
	prompts := make(map[string]string, len(r.Styles))
	for i, s := range r.Styles {
		switch {
		case s.Name == "":
			problems = append(problems, fmt.Errorf("style %d has no name", i))
		case seen[s.Name]:
			problems = append(problems, fmt.Errorf("%s appears twice", s.Name))
		}
		seen[s.Name] = true
		if strings.TrimSpace(s.Prompt) == "" {
			problems = append(problems, fmt.Errorf("%s has no prompt", s.Name))
			continue
		}
		if !strings.Contains(s.Prompt, "{{item}}") {
			problems = append(problems, fmt.Errorf("%s has nowhere for the source document to go, so it does not rephrase anything", s.Name))
		}
		if other, ok := prompts[s.Prompt]; ok {
			problems = append(problems, fmt.Errorf("%s and %s are the same prompt, which is one style counted twice and the narrowing this set exists to prevent", other, s.Name))
		}
		prompts[s.Prompt] = s.Name
	}

	if r.Decoding.Seed == 0 {
		problems = append(problems, errors.New("no seed, so the run produces text nobody can produce again, including us"))
	}
	if r.Decoding.Temperature <= 0 {
		problems = append(problems, errors.New("temperature is zero, so every style is the greedy continuation of its prompt and the four of them collapse toward one voice"))
	}
	if r.Decoding.TopP <= 0 || r.Decoding.TopP > 1 {
		problems = append(problems, fmt.Errorf("top_p is %v and it is a probability", r.Decoding.TopP))
	}
	if r.Decoding.MaxTokens <= 0 {
		problems = append(problems, errors.New("no token limit, so a generator that starts repeating is never stopped"))
	}

	if len(r.Filters) == 0 {
		problems = append(problems, errors.New("no filters, and text nothing was allowed to reject is text nothing checked"))
	}
	names := make(map[string]bool, len(r.Filters))
	for i, f := range r.Filters {
		switch {
		case f.Name == "":
			problems = append(problems, fmt.Errorf("filter %d has no name", i))
		case names[f.Name]:
			problems = append(problems, fmt.Errorf("filter %s appears twice", f.Name))
		}
		names[f.Name] = true
		if f.ConfigHash.IsZero() {
			problems = append(problems, fmt.Errorf("filter %s has no config hash, so nobody can rerun it", f.Name))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrBadRecipe, errors.Join(problems...))
	}
	return nil
}
