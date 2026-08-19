package needle

// Reading a set of answers into a shape rather than a number.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/normalize"
)

// A Reply is one answer a model gave to one item.
type Reply struct {
	Item  string   `json:"item"`
	Frame doc.Hash `json:"frame"`

	// Text is the whole answer rather than a span pulled out of it, because the
	// model deciding what part of its own answer counts is the model grading
	// itself.
	Text string `json:"text"`

	// Box is where it was produced, and Context is how many tokens it was
	// actually given. A result at 128k from a run that silently truncated to 8k
	// is the most flattering result in this file and the easiest to produce by
	// accident.
	Box     string `json:"box"`
	Context int    `json:"context"`
}

// An Outcome is what one answer turned out to be.
type Outcome string

const (
	// Found is the needle, returned.
	Found Outcome = "found"

	// Tone is the near miss: the same word with different marks. It is not a
	// miss and it is not a find, and rolling it into either loses the only
	// evidence that a model has folded Vietnamese down to its letters.
	Tone Outcome = "tone"

	// Decoyed is one of the decoy sentences, which is a model that matched the
	// surface form instead of answering the question.
	Decoyed Outcome = "decoyed"

	// Invented is an answer to an item with no answer.
	Invented Outcome = "invented"

	// Declined is the model saying the document does not say, which is right on
	// an absent item and is a miss anywhere else.
	Declined Outcome = "declined"

	// Missed is everything else: the model neither found it, nor said it could
	// not, nor landed on anything the set anticipated.
	Missed Outcome = "missed"
)

// A Point is the score at one length and depth, which is the unit the position
// curve is drawn from.
type Point struct {
	Length int     `json:"length"`
	Depth  float64 `json:"depth"`
	Asked  int     `json:"asked"`
	Found  int     `json:"found"`
	Recall float64 `json:"recall"`
}

// A Score is what the set says about a model.
type Score struct {
	Frame doc.Hash `json:"frame"`
	Boxes []string `json:"boxes"`

	Asked  int `json:"asked"`
	Graded int `json:"graded"`

	// The outcomes, counted apart, because four of these are different bugs.
	Counts map[Outcome]int `json:"counts"`

	// Recall over every item that had a needle, and the same at the longest
	// length on its own, since that is the number the model is sold on.
	Recall float64 `json:"recall"`
	Long   float64 `json:"long"`

	// Curve is recall per length and depth, and Spread is the gap between the
	// best depth and the worst once every length is pooled.
	Curve  []Point `json:"curve"`
	Spread float64 `json:"spread"`
	Worst  float64 `json:"worst_depth"`

	// Tone is the share of toned items answered with the near miss, and Invent
	// is the share of absent items answered with a span.
	Tone   float64 `json:"tone"`
	Invent float64 `json:"invent"`

	// Passes is the gate. Failing is a normal outcome and it is reported rather
	// than thrown.
	Passes bool `json:"passes"`

	// The disagreements, named rather than counted.
	Unknown   []string `json:"unknown"`
	Twice     []string `json:"twice"`
	Nowhere   []string `json:"nowhere"`
	Truncated []string `json:"truncated"`
	Elsewhere []string `json:"elsewhere"`
	Silent    []string `json:"silent"`
}

// ErrNoReplies is what an empty file comes back as.
var ErrNoReplies = errors.New("needle: no answers to read")

// Grade reads answers against the items they were asked from.
//
// It refuses nothing. Every reply that cannot be scored comes back named in one
// of the lists and the numbers are computed from the rest, because a grader
// that stops on the first bad row is a grader somebody runs once and then works
// around.
func Grade(items []Item, replies []Reply) Score {
	g := Score{Frame: Digest(), Counts: map[Outcome]int{}, Asked: len(items)}

	by := make(map[string]Item, len(items))
	for _, it := range items {
		by[it.ID] = it
	}

	boxes := map[string]bool{}
	seen := map[string]bool{}
	answered := map[string]Outcome{}
	for _, r := range replies {
		it, ok := by[r.Item]
		switch {
		case !ok:
			g.Unknown = append(g.Unknown, r.Item)
			continue
		case seen[r.Item]:
			g.Twice = append(g.Twice, r.Item)
			continue
		}
		seen[r.Item] = true

		if r.Box == "" {
			g.Nowhere = append(g.Nowhere, r.Item)
		} else {
			boxes[r.Box] = true
		}
		if !r.Frame.IsZero() && r.Frame != g.Frame {
			g.Elsewhere = append(g.Elsewhere, r.Item)
		}
		// A run that was handed less context than the item is built at did not
		// answer this item, whatever it returned. It is the failure that makes a
		// model look best and it is invisible in the answers themselves.
		if r.Context > 0 && r.Context < it.Length {
			g.Truncated = append(g.Truncated, r.Item)
			continue
		}

		out := Read(it, r.Text)
		answered[r.Item] = out
		g.Counts[out]++
		g.Graded++
	}

	for _, it := range items {
		if !seen[it.ID] {
			g.Silent = append(g.Silent, it.ID)
		}
	}

	g.Boxes = keys(boxes)
	g.score(items, answered)
	return g
}

// Read decides what one answer was.
//
// The needle is looked for anywhere in the answer rather than at the start,
// because a model that explains itself first and answers second is answering.
// The order the checks run in matters: the near miss is tested before the
// needle, since a bare spelling of one is a substring match against the other
// often enough that a looser order would report tone confusion as a find.
func Read(it Item, text string) Outcome {
	said := flat(text)

	if it.Kind == Absent {
		if declines(said) {
			return Declined
		}
		return Invented
	}

	answer := flat(it.Answer)
	for _, n := range it.Near {
		if strings.Contains(said, flat(n)) && !strings.Contains(said, answer) {
			return Tone
		}
	}
	if answer != "" && strings.Contains(said, answer) {
		return Found
	}
	for _, d := range it.Decoy {
		if strings.Contains(said, flat(d)) {
			return Decoyed
		}
	}
	if declines(said) {
		return Declined
	}
	return Missed
}

// declines reports whether the answer is the model saying the document does not
// say.
//
// The phrases are matched with their tone marks off, because this is exactly
// the register where a model writes khong instead of không, and a model that
// correctly declines gets marked as inventing if the match is strict.
// flat is the form everything here is compared in: normalized the way every
// other stage in this project normalizes, and lowercased. Both sides go through
// it, because a needle written hoà and an answer written hòa are the same word
// and only one of the two spellings survives normalization.
func flat(s string) string {
	// Trimmed, because normalizing settles the layout of a document and leaves a
	// trailing newline behind, and a needle with a newline on the end of it is
	// not a substring of the sentence it was found in.
	return strings.TrimSpace(strings.ToLower(normalize.Normalize(s).Text))
}

func declines(said string) bool {
	bare := normalize.Bare(said)
	for _, p := range []string{
		"khong co", "khong duoc de cap", "khong de cap", "khong noi",
		"khong tim thay", "khong xuat hien", "khong nhac den", "khong chua",
		"does not say", "not mentioned", "no mention", "cannot find", "not in the document",
	} {
		if strings.Contains(bare, p) {
			return true
		}
	}
	return false
}

// score turns the outcomes into the curve, the rates and the gate.
func (g *Score) score(items []Item, answered map[string]Outcome) {
	type bucket struct{ asked, found int }
	byPoint := map[[2]float64]*bucket{}
	byDepth := map[float64]*bucket{}
	var needled, hit, long, longHit, toned, tonedWrong, absent, invented int

	for _, it := range items {
		out, ok := answered[it.ID]
		if !ok {
			continue
		}
		if it.Kind == Absent {
			absent++
			if out == Invented {
				invented++
			}
			continue
		}
		found := out == Found
		needled++
		if found {
			hit++
		}
		if it.Length == slices.Max(Lengths) {
			long++
			if found {
				longHit++
			}
		}
		if it.Kind == Toned {
			toned++
			if out == Tone {
				tonedWrong++
			}
		}
		// A split item is counted at both of its depths, since it is a claim
		// about reaching each of them.
		for _, d := range depthsOf(it) {
			k := [2]float64{float64(it.Length), d}
			if byPoint[k] == nil {
				byPoint[k] = &bucket{}
			}
			if byDepth[d] == nil {
				byDepth[d] = &bucket{}
			}
			byPoint[k].asked++
			byDepth[d].asked++
			if found {
				byPoint[k].found++
				byDepth[d].found++
			}
		}
	}

	g.Recall = rate(hit, needled)
	g.Long = rate(longHit, long)
	g.Tone = rate(tonedWrong, toned)
	g.Invent = rate(invented, absent)

	g.Curve = make([]Point, 0, len(byPoint))
	for k, b := range byPoint {
		g.Curve = append(g.Curve, Point{
			Length: int(k[0]), Depth: k[1],
			Asked: b.asked, Found: b.found, Recall: rate(b.found, b.asked),
		})
	}
	slices.SortFunc(g.Curve, func(a, b Point) int {
		if a.Length != b.Length {
			return a.Length - b.Length
		}
		switch {
		case a.Depth < b.Depth:
			return -1
		case a.Depth > b.Depth:
			return 1
		default:
			return 0
		}
	})

	// The spread is taken over depths pooled across lengths rather than over
	// every square, because one square holds a handful of items and its best to
	// worst gap is mostly sampling noise.
	best, worst := 0.0, 1.0
	for d, b := range byDepth {
		if b.asked == 0 {
			continue
		}
		r := rate(b.found, b.asked)
		best = max(best, r)
		if r <= worst {
			worst, g.Worst = r, d
		}
	}
	if len(byDepth) > 0 {
		g.Spread = best - worst
	}

	g.Passes = g.Graded > 0 && len(g.Silent) == 0 &&
		g.Recall >= MinRecall && g.Long >= MinLong &&
		g.Spread <= MaxSpread && g.Tone <= MaxTone && g.Invent <= MaxInvent
}

// depthsOf is where an item's needles sit, which is two of them for a split.
func depthsOf(it Item) []float64 {
	if it.Kind == Split {
		return []float64{it.Depth, it.Second}
	}
	return []float64{it.Depth}
}

// Blocking is every reason the grade may not go out as a result.
func (g Score) Blocking() []string {
	var out []string
	if g.Graded == 0 {
		return []string{"nothing was graded, so there is no result here"}
	}
	if len(g.Silent) > 0 {
		out = append(out, fmt.Sprintf("%s went unanswered: %s. A recall computed over the items that came back is a recall over the ones the run got through, and the ones it did not are the long ones",
			plural(len(g.Silent), "item"), join(g.Silent)))
	}
	if len(g.Truncated) > 0 {
		out = append(out, fmt.Sprintf("%s were answered from less context than they are built at: %s. That is not a hard item answered well, it is a different item",
			plural(len(g.Truncated), "item"), join(g.Truncated)))
	}
	if len(g.Elsewhere) > 0 {
		out = append(out, fmt.Sprintf("%s name a different frame: %s. A set with a different grid is not this set and the numbers do not compare",
			plural(len(g.Elsewhere), "reply"), join(g.Elsewhere)))
	}
	if len(g.Unknown) > 0 {
		out = append(out, fmt.Sprintf("%s answer items that are not in the set: %s",
			plural(len(g.Unknown), "reply"), join(g.Unknown)))
	}
	if len(g.Twice) > 0 {
		out = append(out, fmt.Sprintf("%s were answered twice: %s. The first was kept, and an item answered twice is an item somebody re-ran after reading the first answer",
			plural(len(g.Twice), "item"), join(g.Twice)))
	}
	if len(g.Nowhere) > 0 {
		out = append(out, fmt.Sprintf("%s carry no box: %s. A long context result with no machine on it cannot be reproduced, and this is the one benchmark where the machine decides the answer",
			plural(len(g.Nowhere), "reply"), join(g.Nowhere)))
	}
	if len(g.Boxes) > 1 {
		out = append(out, fmt.Sprintf("the answers came off more than one box: %s. Context length is a property of the machine as much as the model, so a run split across boxes is two runs",
			strings.Join(g.Boxes, ", ")))
	}
	return out
}

// Verdict is the answer in a sentence, including the one nobody wants.
func (g Score) Verdict() string {
	if g.Passes {
		return fmt.Sprintf("%s passes: %.0f%% recall overall and %.0f%% at %dk, %.0f points between the best depth and the worst, %.0f%% tone confusion, %.0f%% invented",
			Repo, 100*g.Recall, 100*g.Long, slices.Max(Lengths)/1000,
			100*g.Spread, 100*g.Tone, 100*g.Invent)
	}
	var why []string
	if g.Recall < MinRecall {
		why = append(why, fmt.Sprintf("recall is %.0f%% against a floor of %.0f%%", 100*g.Recall, 100*MinRecall))
	}
	if g.Long < MinLong {
		why = append(why, fmt.Sprintf("recall at %dk is %.0f%% against a floor of %.0f%%", slices.Max(Lengths)/1000, 100*g.Long, 100*MinLong))
	}
	if g.Spread > MaxSpread {
		why = append(why, fmt.Sprintf("there are %.0f points between the best depth and the worst, which is a model that reads the ends of a context rather than the context, and the worst is at %.0f%% of the way through",
			100*g.Spread, 100*g.Worst))
	}
	if g.Tone > MaxTone {
		why = append(why, fmt.Sprintf("%.0f%% of the toned items came back as the near miss, which is a model that has folded the tone marks away and cannot tell two Vietnamese words apart", 100*g.Tone))
	}
	if g.Invent > MaxInvent {
		why = append(why, fmt.Sprintf("%.0f%% of the items with no needle in them got an answer anyway", 100*g.Invent))
	}
	if len(why) == 0 {
		why = append(why, "the run did not cover the set")
	}
	return fmt.Sprintf("%s fails: %s", Repo, strings.Join(why, "; "))
}

func rate(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return float64(n) / float64(of)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ReadItems reads a built set, one JSON object per line.
func ReadItems(path string) ([]Item, error) {
	return readLines[Item](path, func(it Item) error {
		if it.ID == "" {
			return errors.New("an item with no ID cannot be matched to an answer")
		}
		return nil
	})
}

// ReadReplies reads a run's answers, one JSON object per line.
func ReadReplies(path string) ([]Reply, error) {
	rs, err := readLines[Reply](path, func(r Reply) error {
		if r.Item == "" {
			return errors.New("an answer with no item on it cannot be graded")
		}
		return nil
	})
	if err == nil && len(rs) == 0 {
		return nil, ErrNoReplies
	}
	return rs, err
}

func readLines[T any](path string, check func(T) error) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []T
	sc := bufio.NewScanner(f)
	// The haystack is not in these files, but an answer to a 128k question can
	// still be long, so the line budget is generous.
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		var v T
		dec := json.NewDecoder(strings.NewReader(text))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if err := check(v); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
