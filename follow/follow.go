// Package follow is vi-adherence: whether the answer follows the question into
// the language it was asked in.
//
// Theo is to follow. A model trained mostly on English and finetuned into
// Vietnamese answers a Vietnamese question in English often enough that anybody
// who has used one has seen it, and it is not a subtle failure. It is the first
// thing a user notices and the last thing a benchmark suite measures, because
// every standard evaluation scores the content of an answer and none of them
// score the language it arrived in.
//
// There are two ways to get this wrong and they are not the same failure. An
// answer in English is a model that ignored the question. An answer in
// Vietnamese with the tone marks left off is a model that is writing the
// language badly, which is a different problem with a different cause, and
// summing them into one number means neither can be fixed. So they are counted
// apart.
//
// The measurement reads the whole answer rather than the top of it. Drift is the
// normal shape of this failure: the first paragraph comes back in Vietnamese and
// the model finishes in English, which scores perfectly against anything that
// samples the opening. Where the answer changed language is reported, because a
// model that drifts at the end of every long answer and a model that answers in
// English outright are the same number and different bugs.
//
// Not every item wants Vietnamese. A prompt that asks for a translation into
// English is answered in English, and a set without those items measures a model
// that has learned to refuse to write English, which is the failure this one
// would cause if it were built carelessly.
package follow

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/zeebo/blake3"
)

// Repo is where the set is published.
const Repo = "vi-adherence"

// The gate. Adherence is high by construction for a model that works at all,
// which is why the floor is where it is: this measures a failure that is either
// absent or obvious, and a model at 90% is answering one question in ten in the
// wrong language.
const (
	MinAdherence = 0.98
	MaxBare      = 0.01
)

// Items is the fewest items the set may hold, and English is the fewest of them
// that must want an answer in English, since a set that only ever wants
// Vietnamese rewards a model that has learned never to write anything else.
const (
	Items   = 20
	English = 3
)

// A Lang is what a stretch of text is written in.
type Lang string

const (
	// Viet is Vietnamese with its tone marks, which is Vietnamese.
	Viet Lang = "vi"

	// Bare is Vietnamese with the marks left off. It is a real register and the
	// corpus is supposed to contain it, and it is not an answer to a question
	// asked with marks on.
	Bare Lang = "bare"

	// Eng is English, or near enough that the words carrying the sentence are
	// English.
	Eng Lang = "en"

	// Other is a stretch nothing can be decided from: a number, a code block, a
	// name, a line of punctuation. It is neither counted for nor against.
	Other Lang = "other"
)

// An Item is one prompt and the language its answer has to be in.
type Item struct {
	ID string `json:"id"`

	// Prompt is Vietnamese in every item, including the ones whose answer is
	// English, because the question being Vietnamese is the whole premise.
	Prompt string `json:"prompt"`

	// Wants is the language the answer belongs in.
	Wants Lang `json:"wants"`

	// Kind is what about the prompt makes it worth asking: whether it invites
	// English, runs long enough for a model to drift inside it, or carries
	// technical vocabulary that has no Vietnamese form.
	Kind string `json:"kind"`

	// Why says why this item is in the set. An item nobody can explain is one
	// somebody argues about after seeing a score.
	Why string `json:"why"`
}

// A Set is the whole benchmark.
type Set struct {
	Version string `json:"version"`
	Items   []Item `json:"items"`
	Note    string `json:"note,omitempty"`
}

// A Reply is what a model did with one item.
type Reply struct {
	ID string `json:"id"`

	// Set, when carried, is the digest of the set the prompt came out of.
	Set doc.Hash `json:"set,omitempty"`

	// Text is the answer verbatim, kept rather than reduced to a verdict, since a
	// benchmark that publishes only its own grading cannot be argued with.
	Text string `json:"text"`

	// Wrote, when set, is a person's call about what language the answer is in,
	// and it wins over the detector.
	Wrote Lang `json:"wrote,omitempty"`
}

// Errors a set or a grading can fail with.
var (
	ErrBadSet     = errors.New("theo: the set does not measure what it says")
	ErrBadReplies = errors.New("theo: the replies do not cover the set")
)

// Digest identifies the set by the prompts and what each one wants back.
func (s Set) Digest() doc.Hash {
	d := blake3.New()
	write := func(key, value string) {
		fmt.Fprintf(d, "%s %d:%s\n", key, len(value), value)
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(len(s.Items)))
	_, _ = d.Write([]byte("items"))
	_, _ = d.Write(b[:])

	write("version", s.Version)
	items := slices.Clone(s.Items)
	slices.SortStableFunc(items, func(a, b Item) int { return strings.Compare(a.ID, b.ID) })
	for _, it := range items {
		write("id", it.ID)
		write("prompt", it.Prompt)
		write("wants", string(it.Wants))
		write("kind", it.Kind)
	}
	return doc.Hash(d.Sum(nil))
}

// Lookup returns the item with the given ID.
func (s Set) Lookup(id string) (Item, bool) {
	for _, it := range s.Items {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

// Kinds is every kind in the set, in the order it first appears.
func (s Set) Kinds() []string {
	var out []string
	seen := map[string]bool{}
	for _, it := range s.Items {
		if !seen[it.Kind] {
			seen[it.Kind] = true
			out = append(out, it.Kind)
		}
	}
	return out
}

// Wanting is how many items want an answer in a given language.
func (s Set) Wanting(l Lang) int {
	n := 0
	for _, it := range s.Items {
		if it.Wants == l {
			n++
		}
	}
	return n
}

// check reports every way the set does not measure what it says it does.
func (s Set) check() error {
	var problems []error
	if s.Version == "" {
		problems = append(problems, errors.New("the set has no version"))
	}

	seen := make(map[string]bool, len(s.Items))
	prompts := make(map[string]string, len(s.Items))
	for i, it := range s.Items {
		switch {
		case it.ID == "":
			problems = append(problems, fmt.Errorf("item %d has no id", i))
		case seen[it.ID]:
			problems = append(problems, fmt.Errorf("%s appears twice", it.ID))
		}
		seen[it.ID] = true

		if it.Wants != Viet && it.Wants != Eng {
			problems = append(problems, fmt.Errorf("%s wants %q back, and an answer is either in the language it was asked in or in the one the question asked for", it.ID, it.Wants))
		}
		if it.Kind == "" {
			problems = append(problems, fmt.Errorf("%s does not say what makes it worth asking", it.ID))
		}
		if strings.TrimSpace(it.Why) == "" {
			problems = append(problems, fmt.Errorf("%s does not say why it is in the set", it.ID))
		}
		if strings.TrimSpace(it.Prompt) == "" {
			problems = append(problems, fmt.Errorf("%s has no prompt", it.ID))
			continue
		}
		if !marked(it.Prompt) {
			problems = append(problems, fmt.Errorf("%s is asked without tone marks, and the premise of every item here is a question written in Vietnamese", it.ID))
		}
		if other, ok := prompts[it.Prompt]; ok {
			problems = append(problems, fmt.Errorf("%s and %s are the same prompt", other, it.ID))
		}
		prompts[it.Prompt] = it.ID
	}

	if n := len(s.Items); n < Items {
		problems = append(problems, fmt.Errorf("the set holds %s and %d is the floor, since one item in ten going the wrong way is not something a short set can see",
			plural(n, "item"), Items))
	}
	if n := s.Wanting(Eng); n < English {
		problems = append(problems, fmt.Errorf("the set asks for an answer in English %s and %d is the floor, because a set that always wants Vietnamese back is passed by a model that has learned never to write anything else",
			plural(n, "time"), English))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrBadSet, errors.Join(problems...))
	}
	return nil
}

// Faults is check as lines.
func (s Set) Faults() []string {
	err := s.check()
	if err == nil {
		return nil
	}
	var faults []string
	for line := range strings.SplitSeq(err.Error(), "\n") {
		faults = append(faults, strings.TrimPrefix(line, ErrBadSet.Error()+": "))
	}
	return faults
}

// marked reports whether the text carries a Vietnamese tone mark or one of the
// letters only Vietnamese uses.
func marked(s string) bool {
	for _, r := range s {
		switch {
		case r >= 0x1EA0 && r <= 0x1EF9:
			return true
		case r == 'đ' || r == 'Đ' || r == 'ơ' || r == 'ư' || r == 'ă' || r == 'ê' || r == 'ô':
			return true
		case r >= 0x00C0 && r <= 0x0169:
			return true
		}
	}
	return false
}

// plural writes a count with its noun, adding the s only when it belongs.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
