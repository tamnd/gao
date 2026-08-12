// Package lap reads a set of generated documents and says whether it is a
// corpus or one prompt run a million times.
//
// Lặp is to repeat. Everything else in this project that judges text judges one
// document at a time. sang asks whether a document repeats itself, xay asks
// whether two documents are the same document, and both of them are answering
// questions about web text, where the failure is a page of boilerplate or a
// mirror of a site already in the store. Neither of them can see the failure
// that matters for generated text, because it is not in any document.
//
// # Why a set can pass every filter a document has to pass and still be worthless
//
// A model asked for a hundred thousand articles about Vietnamese administrative
// procedure returns a hundred thousand fluent, varied, well formed articles.
// Every one of them is Vietnamese, none of them repeats itself, no two of them
// are near duplicates, and the set is still four hundred sentence shapes with
// the nouns swapped. Read one and it is fine. Read the set in order and the
// hundred thousandth document teaches a model nothing the ten thousandth did not
// already, and the difference between those two facts is 14,000 GPU hours.
//
// So the measure here is over the set and in the order the set was generated.
// Novelty is the share of five syllable grams in the last tenth of the set that
// had not appeared anywhere in the first nine tenths. A set that keeps producing
// new material stays high. A set that has run out saturates, and it saturates
// long before any per document filter notices, since each document on its own
// is new to the filter reading it.
//
// # Why the reject rate is reported at both ends
//
// A generator's own filter is part of the generator, and the share of output it
// threw away belongs next to the data the same way. Two numbers are wrong. A
// reject rate of nothing is a filter that did not run, whatever the code says it
// did, because no generator writes a hundred thousand documents worth keeping.
// A reject rate over half means what ships is not the generator's output. It is
// the tail of the generator's output that passed gao's own filter, which is a
// different object and has to be described as one, in the generator card, in the
// same words used here.
//
// # Why every document names the prompt it came from
//
// One prompt producing most of what ships is the same failure as low novelty
// arriving through a different door, and it is the one a targeting plan causes
// on its own: whichever prompt turned out to be cheapest to run gets run the
// most. A document that cannot be traced back to a prompt cannot be checked for
// either, so a set holding one is refused rather than measured.
package lap

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// MinDocs is the smallest set this package will read. A novelty curve off forty
// documents is a reading of which forty, and a generated set below this is a
// sample of a run rather than a run.
const MinDocs = 200

// Gram is the window novelty is measured over, in syllables. Five is long
// enough that ordinary Vietnamese collocations do not fill it on their own and
// short enough that a set which has genuinely run out of things to say cannot
// hide behind rewording.
const Gram = 5

// Open is how many syllables of a document count as its opening, which is where
// a template shows first and where a reader of the set would notice it.
const Open = 8

// MinNovelty is the share of the last tenth that has to be material the first
// nine tenths did not already hold. A quarter is low, deliberately: this is the
// line under which the tokens are length rather than content, not the line a
// good generator clears.
const MinNovelty = 0.25

// MaxTemplate is the share of a set that may open with the same eight
// syllables.
const MaxTemplate = 0.05

// MaxPrompt is the share of a set that may come from one prompt.
const MaxPrompt = 0.05

// MinReject and MaxReject are the two ends the reject rate is read against. See
// the package comment: nothing rejected is a filter that did not run, and over
// half rejected is a different artifact than the one the card describes.
const (
	MinReject = 0.005
	MaxReject = 0.50
)

// A Doc is one generated document, in the order it was generated.
type Doc struct {
	ID string `json:"id"`

	// Prompt is the identity of the prompt that produced it, off the generator
	// card rather than the prompt text, since the text of a prompt is on the
	// card once and in the set a hundred thousand times.
	Prompt string `json:"prompt"`

	Domain string `json:"domain,omitempty"`
	Text   string `json:"text"`

	// Kept is what the generator's own filter decided. Rejected documents stay
	// in the file, because the share it threw away is a fact about the
	// generator and deleting them is how that fact gets lost.
	Kept bool `json:"kept"`
}

// A Shape is one opening or one prompt with how much of the set it accounts
// for.
type Shape struct {
	Text  string  `json:"text"`
	Docs  int     `json:"docs"`
	Share float64 `json:"share"`
}

// A Set is a generated run, read in order.
type Set struct {
	Generator string `json:"generator"`

	Docs     int `json:"docs"`
	Kept     int `json:"kept"`
	Rejected int `json:"rejected"`

	// Novelty is the share of the grams in the last tenth of what shipped that
	// the first nine tenths did not already hold.
	Novelty float64 `json:"novelty"`

	// Grams is how many distinct grams the whole of what shipped holds, and
	// Tail is how many the last tenth was measured over, so a novelty figure
	// can be read against the amount of text behind it.
	Grams int `json:"grams"`
	Tail  int `json:"tail"`

	// Shapes and Prompts are the ten biggest of each, largest first.
	Shapes  []Shape `json:"shapes,omitempty"`
	Prompts []Shape `json:"prompts,omitempty"`

	docs []Doc
}

// Read measures a set. The order of docs is the order it was generated in, and
// nothing here sorts it, because the whole measurement is about what came later.
func Read(generator string, docs []Doc) Set {
	s := Set{Generator: generator, Docs: len(docs), docs: docs}

	kept := make([]Doc, 0, len(docs))
	for _, d := range docs {
		if d.Kept {
			kept = append(kept, d)
		}
	}
	s.Kept = len(kept)
	s.Rejected = len(docs) - len(kept)
	if len(kept) == 0 {
		return s
	}

	grams := map[string]bool{}
	opens := map[string]int{}
	prompts := map[string]int{}
	// The tail is the last tenth of what shipped, which is where a set that has
	// run out shows it.
	head := len(kept) - len(kept)/10
	if head >= len(kept) {
		head = len(kept) - 1
	}
	var tail, fresh int

	for i, d := range kept {
		syllables := split(d.Text)
		if len(syllables) > 0 {
			n := min(Open, len(syllables))
			opens[strings.Join(syllables[:n], " ")]++
		}
		prompts[d.Prompt]++

		for j := 0; j+Gram <= len(syllables); j++ {
			g := strings.Join(syllables[j:j+Gram], " ")
			if i >= head {
				tail++
				if !grams[g] {
					fresh++
				}
			}
			grams[g] = true
		}
	}

	s.Grams = len(grams)
	s.Tail = tail
	if tail > 0 {
		s.Novelty = float64(fresh) / float64(tail)
	}
	s.Shapes = top(opens, len(kept))
	s.Prompts = top(prompts, len(kept))
	return s
}

// RejectRate is the share of what was generated that the generator's own filter
// threw away.
func (s Set) RejectRate() float64 {
	if s.Docs == 0 {
		return 0
	}
	return float64(s.Rejected) / float64(s.Docs)
}

// Blocking is every reason this is not a set anybody can measure.
func (s Set) Blocking() []string {
	if len(s.docs) == 0 {
		return []string{"the file holds no documents, so there is no set to read"}
	}
	var why []string
	if s.Generator == "" {
		return append(why, "the set does not name the generator that wrote it, and generated text with no generator on it is the one thing this project will not publish")
	}

	seen := map[string]bool{}
	for _, d := range s.docs {
		switch {
		case d.ID == "":
			why = append(why, "a document arrived with no identity, so it cannot be pointed at, counted once, or taken back out")
		case seen[d.ID]:
			why = append(why, fmt.Sprintf("%s appears twice, and a document counted twice is a set that looks more varied than it is", d.ID))
		}
		seen[d.ID] = true
		if d.Prompt == "" {
			why = append(why, fmt.Sprintf("%s does not say which prompt made it, so nothing can be said about how much of this set one prompt is", d.ID))
		}
		if d.Kept && strings.TrimSpace(d.Text) == "" {
			why = append(why, fmt.Sprintf("%s was kept and holds no text", d.ID))
		}
	}
	if len(s.docs) < MinDocs {
		why = append(why, fmt.Sprintf(
			"%d documents is under the %d this measure needs, and a novelty curve off that few is a reading of which few rather than of the run",
			len(s.docs), MinDocs))
	}
	if s.Kept == 0 {
		why = append(why, "every document was rejected, so there is nothing here that would ship")
	}
	return why
}

// Faults are the reasons a set that measures should not be generated any
// further, or should not be described the way the card describes it.
func (s Set) Faults() []string {
	if len(s.Blocking()) > 0 {
		return nil
	}
	var out []string

	if s.Novelty < MinNovelty {
		out = append(out, fmt.Sprintf(
			"the last tenth of the set is %s material the rest of it did not already hold, against a %s line, so past that point the run is producing length rather than content",
			share(s.Novelty), share(MinNovelty)))
	}
	if len(s.Shapes) > 0 && s.Shapes[0].Share > MaxTemplate {
		out = append(out, fmt.Sprintf(
			"%s of the documents open with the same %d syllables, %q, so that much of the set is one shape with the nouns changed",
			share(s.Shapes[0].Share), Open, s.Shapes[0].Text))
	}
	if len(s.Prompts) > 0 && s.Prompts[0].Share > MaxPrompt {
		out = append(out, fmt.Sprintf(
			"%s of what shipped came from the prompt %s, so the set is exactly as varied as that one prompt is",
			share(s.Prompts[0].Share), s.Prompts[0].Text))
	}
	switch r := s.RejectRate(); {
	case r < MinReject:
		out = append(out, fmt.Sprintf(
			"%s of what was generated was rejected, which is a filter that did not run rather than a generator that needed none",
			share(r)))
	case r > MaxReject:
		out = append(out, fmt.Sprintf(
			"%s of what was generated was rejected, so what ships is the part of this generator's output that passed gao's own filter and the card has to say that rather than describe it as the generator's output",
			share(r)))
	}
	return out
}

// Holds reports whether the run should carry on and whether the card can
// describe the set the way a reader will read it.
func (s Set) Holds() bool { return len(s.Blocking()) == 0 && len(s.Faults()) == 0 }

// Verdict is the set in one paragraph.
func (s Set) Verdict() string {
	if why := s.Blocking(); len(why) > 0 {
		return why[0]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s kept %d of %d documents and the last tenth of what it kept is %s material the rest did not already hold, read over %s grams of five syllables.",
		s.Generator, s.Kept, s.Docs, share(s.Novelty), thousands(s.Tail))

	faults := s.Faults()
	switch n := len(faults); n {
	case 0:
		fmt.Fprint(&b, " Nothing in the set says it has run out of things to say, so the tokens after this point are worth what the ones before it were.")
	case 1:
		fmt.Fprintf(&b, " One reading says this set is shorter than its token count: %s.", faults[0])
	default:
		fmt.Fprintf(&b, " %d readings say this set is shorter than its token count: %s.", n, strings.Join(faults, "; and "))
	}
	return b.String()
}

// split reduces a document to its syllables, lowercased and stripped of
// everything that is not a letter or a digit, which is the same reduction sang
// makes for the same reason: two spellings of a syllable are one syllable.
func split(text string) []string {
	fields := strings.Fields(text)
	out := make([]string, 0, len(fields))
	for _, tok := range fields {
		var b strings.Builder
		for _, c := range strings.ToLower(tok) {
			if unicode.IsLetter(c) || unicode.IsDigit(c) {
				b.WriteRune(c)
			}
		}
		if b.Len() > 0 {
			out = append(out, b.String())
		}
	}
	return out
}

// top is the ten biggest counts, largest first, with ties broken by name so the
// same set reads the same way twice.
func top(counts map[string]int, of int) []Shape {
	out := make([]Shape, 0, len(counts))
	for text, docs := range counts {
		out = append(out, Shape{Text: text, Docs: docs, Share: float64(docs) / float64(of)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Docs != out[j].Docs {
			return out[i].Docs > out[j].Docs
		}
		return out[i].Text < out[j].Text
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func share(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

// thousands writes a count with separators, since 16778 grams and 167779 of
// them look alike at a glance and differ by an order of magnitude.
func thousands(n int) string {
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
