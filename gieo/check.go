package gieo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

// CardName is the file a generator card lives in, beside the data it describes.
const CardName = "generator-card.json"

// Repo is the only dataset a synthesis run may publish to. It is not a default
// and it is not configurable: a synthetic document that lands in a repo of
// natural Vietnamese is a document somebody downloads believing a person wrote
// it, and no amount of metadata further down undoes the first impression.
const Repo = "vietnamese-synthetic-text"

// Against reports every way a card and the recipe it names disagree, or the
// card disagrees with itself, in a fixed order so two runs of the checker read
// the same.
func (c Card) Against(r Recipe) []string {
	var faults []string
	if err := r.check(); err != nil {
		for line := range strings.SplitSeq(err.Error(), "\n") {
			faults = append(faults, strings.TrimPrefix(line, ErrBadRecipe.Error()+": "))
		}
	}

	if c.Recipe != r.Digest() {
		faults = append(faults, fmt.Sprintf(
			"the card was produced under %s and the recipe it names hashes to %s, so something in the recipe moved after the run",
			short(c.Recipe), short(r.Digest())))
	}
	if c.Version != "" && r.Version != "" && c.Version != r.Version {
		faults = append(faults, fmt.Sprintf("the card says recipe %s and the recipe says %s", c.Version, r.Version))
	}
	if c.Box == "" {
		faults = append(faults, "the card does not say which box it ran on")
	}
	if c.Batch == "" {
		faults = append(faults, "the card does not say how it was batched, so the throughput is not a number anybody can reproduce")
	}
	if c.RanAt.IsZero() {
		faults = append(faults, "the card does not say when it ran")
	}

	switch {
	case c.Generated <= 0:
		faults = append(faults, "the card reports nothing generated")
	case c.Kept > c.Generated:
		faults = append(faults, fmt.Sprintf("the card keeps %d documents out of %d generated", c.Kept, c.Generated))
	case c.Kept <= 0:
		faults = append(faults, "the card keeps nothing, and a run with no output is not a run worth publishing a card for")
	}

	if c.Tokens > 0 && c.Tokenizer == "" {
		faults = append(faults, "a token count without a tokenizer is not a token count")
	}
	if c.Kept > 0 && c.Tokens <= 0 {
		faults = append(faults, "the card keeps documents and counts no tokens, and the mixture spends tokens")
	}

	faults = append(faults, c.rejects(r)...)

	if c.Contaminated < 0 {
		faults = append(faults, "the contamination count is negative")
	}
	if c.GPUHours <= 0 {
		faults = append(faults, "the card does not say what it cost")
	} else if c.GPUHours > Budget {
		faults = append(faults, fmt.Sprintf(
			"the run spent %.0f GPU hours against a budget of %d, which is the kind of thing somebody should hear from the card rather than from the electricity",
			c.GPUHours, Budget))
	}
	return faults
}

// rejects checks the accounting of what the gates threw away. The arithmetic
// has to close, because a card whose numbers do not add up is describing a run
// nobody was watching, and the reject rate has to be a rate, because zero is
// what a gate that never ran reports.
func (c Card) rejects(r Recipe) []string {
	var faults []string
	var total int64
	for _, n := range c.Rejects {
		total += n
	}
	dropped := c.Generated - c.Kept
	if total != dropped {
		faults = append(faults, fmt.Sprintf(
			"the gates account for %d rejected documents and %d did not survive, so the card is missing an account of %s",
			total, dropped, plural(int(abs(dropped-total)), "document")))
	}

	names := make([]string, 0, len(c.Rejects))
	for name := range c.Rejects {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if _, ok := r.Filter(name); !ok {
			faults = append(faults, fmt.Sprintf("%s rejected %d documents and is not a filter in the recipe", name, c.Rejects[name]))
		}
		if c.Rejects[name] < 0 {
			faults = append(faults, fmt.Sprintf("%s reports a negative count", name))
		}
	}
	for _, f := range r.Filters {
		if _, ok := c.Rejects[f.Name]; !ok {
			faults = append(faults, fmt.Sprintf(
				"%s is in the recipe and the card says nothing about it, which reads the same as a filter that ran and rejected nothing", f.Name))
		}
	}

	if c.Generated > 0 && c.Kept == c.Generated {
		faults = append(faults, "every generated document was kept, and text nothing rejected is text nothing checked rather than text that was very good")
	}
	return faults
}

// Publishable reports whether the card describes something that may go out, on
// top of whether it is internally consistent.
func (c Card) Publishable() []string {
	var faults []string
	d, ok := kho.Lookup(Repo)
	if !ok {
		return []string{fmt.Sprintf("%s is not a dataset in the hub, which is a bug in the hub rather than in the card", Repo)}
	}
	if d.Tier != kho.Published {
		faults = append(faults, fmt.Sprintf("%s is not a release, so a generator card has nowhere to go", Repo))
	}
	if c.Contaminated > 0 {
		faults = append(faults, fmt.Sprintf(
			"%s held a benchmark item and the card does not say they were removed, so the evaluation would be scoring a model on its own training data",
			plural(int(c.Contaminated), "generated document")))
	}
	return faults
}

// Natural is what a synthetic card contributes to a natural document count,
// which is nothing, ever. It exists as a function so that the rule has one
// place to live rather than being a comment somebody has to remember.
func (c Card) Natural() int64 { return 0 }

// Source is the license class synthetic text carries. Model output over open
// source text is not the source's license and it is not nothing, so it goes out
// under its own class and the hub decides where that can land.
func (c Card) Source() doc.Source { return doc.SourceSynth }

// ReadRecipe loads a recipe from a file.
func ReadRecipe(path string) (Recipe, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Recipe{}, fmt.Errorf("gieo: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var r Recipe
	if err := dec.Decode(&r); err != nil {
		return Recipe{}, fmt.Errorf("gieo: %s: %w", path, err)
	}
	return r, nil
}

// ReadCard loads a card from a directory, which is where it sits beside the
// data it describes.
func ReadCard(dir string) (Card, error) {
	path := dir
	if filepath.Base(dir) != CardName {
		path = filepath.Join(dir, CardName)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Card{}, fmt.Errorf("gieo: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var c Card
	if err := dec.Decode(&c); err != nil {
		return Card{}, fmt.Errorf("gieo: %s: %w", path, err)
	}
	if len(c.Rejects) == 0 {
		return Card{}, fmt.Errorf("%w: %s: the card accounts for no rejections at all", ErrBadCard, path)
	}
	return c, nil
}

// WriteCard writes a card beside the data. It refuses to overwrite one, because
// a card is a record of a run that happened and a second run is a second card.
func WriteCard(dir string, c Card) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("gieo: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, CardName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("gieo: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("gieo: %w", err)
	}
	return f.Close()
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func short(h doc.Hash) string {
	s := h.String()
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
