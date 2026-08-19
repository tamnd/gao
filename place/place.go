// Package place is the reference set gao-refset is labeled against: which
// documents get drawn, which bands they get placed in, and what decides which.
//
// Xep is to place. A quality classifier is a function from a document to a
// band, and it is only ever as good as the bands somebody placed documents in
// by hand. Everything downstream of gao-qual, which is to say the share of the
// corpus that survives to training, comes out of 200,000 human judgments, and
// nobody audits those the way they audit the model trained on them.
//
// So the frame and the rubric are fixed and hashed before a document is
// labeled. The reason is the same one behind the ablation slate and the
// evaluation harness: a rubric written while the labeling is underway gets
// written toward the labels already collected, and a rubric written after the
// classifier is trained gets written toward the classifier. Neither is
// detectable in the finished set. Both are prevented by a digest with a date
// on it.
//
// The part of the rubric that does the work is not the description of each
// band. It is the sentence on each band saying which neighboring band it is
// confused with and how to tell them apart, because every disagreement between
// two labelers is a boundary case and none of them are in the middle of a
// band. A rubric that describes four bands beautifully and says nothing about
// the three lines between them has not been written yet.
//
// The frame matters as much as the rubric. A reference set drawn from whatever
// was convenient labels the corpus somebody had open rather than the corpus
// that exists, and a classifier trained on it learns the shape of that
// convenience. Every source gets a share, the share is written down with the
// reason for it, and the draw is a hash of the seed and the document identity
// so a third party with the seed draws the same 200,000 documents.
package place

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/zeebo/blake3"
)

// Repo is where the labeled set is published.
const Repo = "gao-refset"

// The size of the set, and the share of it that has to be labeled twice. Ten
// percent is what makes the agreement number mean anything: below that the
// number is an estimate off a sample too small to separate a rubric that works
// from a labeler having a good week.
const (
	Documents = 200_000
	Double    = 0.10
)

// The gate on agreement. Exact is two labelers choosing the same band, and it
// is the number that says whether the rubric decides anything. Adjacent is two
// labelers landing within one band of each other, and it is high because a
// rubric that cannot even do that is not a scale, it is four words in a list.
const (
	MinExact    = 0.70
	MinAdjacent = 0.95
)

// A Band is where a document gets placed. The order is the scale, from the text
// worth paying for down to the text that is not text.
type Band string

const (
	// Rich is the writing that is expensive to get and that the corpus is short
	// of: edited long form, technical explanation, legal and medical prose,
	// anything somebody was paid to write carefully.
	Rich Band = "rich"

	// Plain is ordinary Vietnamese that somebody wrote for a reason. Most of a
	// good corpus is this and a classifier that drops it has learned to prefer
	// the register of a textbook to the register of the language.
	Plain Band = "plain"

	// Thin is Vietnamese prose with nothing in it: search engine filler, spun
	// rewrites, a paragraph of boilerplate repeated under a different headline.
	Thin Band = "thin"

	// Unusable is not prose at all, or not Vietnamese: navigation, tag lists,
	// tables of numbers, machine translation that never became a sentence.
	Unusable Band = "unusable"
)

// Bands is the scale in order, which is what makes adjacency mean something.
var Bands = []Band{Rich, Plain, Thin, Unusable}

// Distance is how many steps apart two bands are on the scale.
func Distance(a, b Band) int {
	i, j := slices.Index(Bands, a), slices.Index(Bands, b)
	if i < 0 || j < 0 {
		return -1
	}
	return max(i-j, j-i)
}

// A Rule is one band with the sentence that decides it.
type Rule struct {
	Band Band `json:"band"`

	// Decides is what puts a document here rather than anywhere else.
	Decides string `json:"decides"`

	// Confused is the band this one gets mixed up with, and Apart is how to tell
	// them apart. Every disagreement between two labelers is a boundary, so this
	// is the field the rubric lives or dies by.
	Confused Band   `json:"confused,omitempty"`
	Apart    string `json:"apart,omitempty"`

	// Examples are the calls that look wrong until the rule is read, kept because
	// a rubric argued about at labeling time is a rubric with no examples in it.
	Examples []string `json:"examples,omitempty"`
}

// A Slice is one source's share of the draw.
type Slice struct {
	Source string `json:"source"`

	// Share is the fraction of the set drawn from this source. The shares are
	// written down rather than left to whatever finished ingesting first.
	Share float64 `json:"share"`

	// Why says why this source gets this share. A share nobody can explain is a
	// share somebody argues about after the classifier is trained.
	Why string `json:"why"`
}

// Wanted is how many documents this slice contributes to a set of n.
func (sl Slice) Wanted(n int) int {
	return int(sl.Share * float64(n))
}

// A Frame is the whole thing fixed before labeling: the draw and the rubric.
type Frame struct {
	Version string `json:"version"`

	// Size is how many documents get labeled.
	Size int `json:"size"`

	// Seed is what the draw is a hash of, so the draw is reproducible by anybody
	// who has the seed and the corpus rather than by whoever ran it.
	Seed string `json:"seed"`

	Slices []Slice `json:"slices"`
	Rules  []Rule  `json:"rules"`

	Note string `json:"note,omitempty"`
}

// A Label is one person placing one document.
type Label struct {
	// Doc is the document's identity in the store, which is the fixed width
	// column every part carries.
	Doc doc.Hash `json:"doc"`

	// Source is which slice of the frame it was drawn from.
	Source string `json:"source"`

	// By is who placed it. Labels are attributed because agreement between two
	// people is the measurement and agreement with yourself is not.
	By string `json:"by"`

	Band Band `json:"band"`

	// Frame, when carried, is the digest of the frame this was labeled against.
	Frame doc.Hash `json:"frame,omitempty"`

	// Note is why, for the calls that were not obvious. It is not hashed and it
	// does not have to be filled in, but a boundary call without one is how a
	// rubric fails to get fixed.
	Note string `json:"note,omitempty"`
}

// Errors a frame or a reading can fail with.
var (
	ErrBadFrame  = errors.New("xep: the frame does not draw what it says it draws")
	ErrBadLabels = errors.New("xep: the labels do not cover the frame")
)

// Digest identifies the frame by the draw and the rubric. The Why and Apart
// prose is hashed too, unlike elsewhere in this repository, because here the
// prose is the instrument: change what tells two bands apart and the labels
// change with it.
func (f Frame) Digest() doc.Hash {
	d := blake3.New()
	write := func(key, value string) {
		fmt.Fprintf(d, "%s %d:%s\n", key, len(value), value)
	}
	num := func(key string, n uint64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], n)
		_, _ = d.Write([]byte(key))
		_, _ = d.Write(b[:])
	}

	write("version", f.Version)
	write("seed", f.Seed)
	num("size", uint64(f.Size))

	slices_ := slices.Clone(f.Slices)
	slices.SortStableFunc(slices_, func(a, b Slice) int { return strings.Compare(a.Source, b.Source) })
	for _, sl := range slices_ {
		write("source", sl.Source)
		num("share", uint64(sl.Share*1e9))
		write("why", sl.Why)
	}

	rules := slices.Clone(f.Rules)
	slices.SortStableFunc(rules, func(a, b Rule) int { return strings.Compare(string(a.Band), string(b.Band)) })
	for _, r := range rules {
		write("band", string(r.Band))
		write("decides", r.Decides)
		write("confused", string(r.Confused))
		write("apart", r.Apart)
		for _, ex := range r.Examples {
			write("example", ex)
		}
	}
	return doc.Hash(d.Sum(nil))
}

// Rule returns the rule for a band.
func (f Frame) Rule(b Band) (Rule, bool) {
	for _, r := range f.Rules {
		if r.Band == b {
			return r, true
		}
	}
	return Rule{}, false
}

// Slice returns the slice for a source.
func (f Frame) Slice(source string) (Slice, bool) {
	for _, sl := range f.Slices {
		if sl.Source == source {
			return sl, true
		}
	}
	return Slice{}, false
}

// Sources is every source in the frame, in the order it appears.
func (f Frame) Sources() []string {
	out := make([]string, 0, len(f.Slices))
	for _, sl := range f.Slices {
		out = append(out, sl.Source)
	}
	return out
}

// check reports every way the frame does not draw what it says it draws.
func (f Frame) check() error {
	var problems []error
	if f.Version == "" {
		problems = append(problems, errors.New("the frame has no version"))
	}
	if strings.TrimSpace(f.Seed) == "" {
		problems = append(problems, errors.New("the frame has no seed, so the draw is whatever the run that made it happened to pick and nobody else can make the same one"))
	}
	if f.Size <= 0 {
		problems = append(problems, errors.New("the frame draws nothing"))
	}

	total := 0.0
	seen := map[string]bool{}
	for i, sl := range f.Slices {
		switch {
		case sl.Source == "":
			problems = append(problems, fmt.Errorf("slice %d names no source", i))
		case seen[sl.Source]:
			problems = append(problems, fmt.Errorf("%s gets a share twice", sl.Source))
		}
		seen[sl.Source] = true
		if sl.Share <= 0 || sl.Share > 1 {
			problems = append(problems, fmt.Errorf("%s is drawn at %.3f, which is not a share of anything", sl.Source, sl.Share))
		}
		if strings.TrimSpace(sl.Why) == "" {
			problems = append(problems, fmt.Errorf("%s does not say why it gets the share it gets", sl.Source))
		}
		total += sl.Share
	}
	if len(f.Slices) == 0 {
		problems = append(problems, errors.New("the frame draws from nowhere"))
	} else if d := total - 1; d > 0.0005 || d < -0.0005 {
		problems = append(problems, fmt.Errorf("the shares come to %.3f rather than to 1, so the set is drawn from a corpus that is not the one it labels", total))
	}

	problems = append(problems, f.rubric()...)
	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrBadFrame, errors.Join(problems...))
	}
	return nil
}

// rubric is every way the bands fail to decide anything.
func (f Frame) rubric() []error {
	var problems []error
	seen := map[Band]bool{}
	for _, r := range f.Rules {
		if seen[r.Band] {
			problems = append(problems, fmt.Errorf("%s is described twice", r.Band))
		}
		seen[r.Band] = true
		if slices.Index(Bands, r.Band) < 0 {
			problems = append(problems, fmt.Errorf("%q is not on the scale, and a band off the scale has no neighbors to be adjacent to", r.Band))
		}
		if strings.TrimSpace(r.Decides) == "" {
			problems = append(problems, fmt.Errorf("%s does not say what puts a document in it", r.Band))
		}
		if r.Confused == "" || strings.TrimSpace(r.Apart) == "" {
			problems = append(problems, fmt.Errorf("%s does not say which band it gets mixed up with and how to tell them apart, which is the only part of a rubric two labelers ever need", r.Band))
			continue
		}
		if r.Confused == r.Band {
			problems = append(problems, fmt.Errorf("%s is confused with itself", r.Band))
		}
		if Distance(r.Band, r.Confused) > 1 {
			problems = append(problems, fmt.Errorf("%s says it gets mixed up with %s, and those are not next to each other, so either the scale is in the wrong order or one of the two rules is describing something else",
				r.Band, r.Confused))
		}
		if len(r.Examples) == 0 {
			problems = append(problems, fmt.Errorf("%s carries no example of a call that looks wrong until the rule is read, and a rubric with no examples is one that gets argued about at labeling time", r.Band))
		}
	}
	for _, b := range Bands {
		if !seen[b] {
			problems = append(problems, fmt.Errorf("%s is on the scale and the rubric does not describe it", b))
		}
	}
	return problems
}

// Faults is check as lines.
func (f Frame) Faults() []string {
	err := f.check()
	if err == nil {
		return nil
	}
	var faults []string
	for line := range strings.SplitSeq(err.Error(), "\n") {
		faults = append(faults, strings.TrimPrefix(line, ErrBadFrame.Error()+": "))
	}
	return faults
}

// plural writes a count with its noun, adding the s only when it belongs.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
