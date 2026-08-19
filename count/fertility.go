package count

// Fertility: how many tokens a tokenizer spends on the same Vietnamese.
//
// It is the one measurement in this project that is a multiplier on everything
// downstream and cannot be undone later. A tokenizer that spends 1.99 tokens per
// syllable where another spends 1.50 makes every training run 33% more
// expensive, every context window 33% shorter, and every inference bill 33%
// larger, for the life of the model. No amount of engineering after the fact
// recovers it, which is why the base model choice is a tokenizer choice first.
//
// Two things follow, and they are the two the checklist asks for.
//
// Fertility is measured on every candidate rather than on the one already
// chosen, because a number with nothing to compare it to does not decide
// anything. A candidate nobody has pinned is a candidate nobody has measured, so
// the roster carries the pin and reports the holes rather than quietly leaving
// them out.
//
// And the same measurement on the same text has to come back the same on all
// four boxes. It is the cheapest reproducibility check in the project: the
// arithmetic is trivial, the inputs are fixed, and a disagreement means a locale
// difference, a normalization difference, or a tokenizer file that is not the
// one that was pinned. Finding that here costs an afternoon. Finding it after
// the counts are published costs the counts.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// A Path is which of the two training routes a candidate belongs to.
//
// It is on the roster because the two are not in competition. A vocabulary that
// can only exist in a from scratch run does not rule out one that can be bolted
// onto an existing model, and reporting them in one ranked list without saying
// so invites picking the top row.
type Path string

const (
	// Continued is the path that starts from somebody else's weights, where the
	// tokenizer is inherited and can at most be extended.
	Continued Path = "continued pretraining"

	// Scratch is the path where the vocabulary is trained on gao text and the
	// fertility is a thing this project decides rather than accepts.
	Scratch Path = "from scratch"
)

// A Predicted is a prediction standing against a candidate, from the register.
//
// A measurement without one is a fact and a measurement with one is a bet
// somebody wrote down before running it, which is the difference between
// reporting a number and having been right about it.
type Predicted struct {
	ID string

	// Lo and Hi bracket the predicted value, and Unit says which of the two
	// fertility figures the bracket is about. Most of these predictions are one
	// sided, so a zero on either end is no bound on that end rather than a bound
	// at zero, which no fertility figure could satisfy anyway.
	Lo, Hi float64
	Unit   string
}

const (
	// PerToken is characters of Vietnamese per token, which is the figure
	// comparable across languages.
	PerToken = "characters per token"

	// PerSyllable is tokens per Vietnamese syllable, which is the figure the
	// budget is actually a function of.
	PerSyllable = "tokens per syllable"
)

// A Candidate is a tokenizer under consideration, pinned if anybody has pinned
// it.
type Candidate struct {
	// Model carries the pin. A candidate whose model has no digest has not been
	// pinned, which is a fact about the roster rather than a reason to leave it
	// off.
	Model Model

	Path Path
	Why  string

	// Reported is a fertility somebody else has published for this tokenizer on
	// Vietnamese, in characters per token, and zero when nobody has. It is a
	// place to start rather than a measurement, and the whole point of the item
	// is to replace it with one taken on gao text.
	Reported float64

	Predicts Predicted
}

// Pinned reports whether the candidate can actually be measured, which needs a
// file somebody has fixed by digest.
func (c Candidate) Pinned() bool { return c.Model.Digest != "" }

// Candidates is the roster.
//
// Gemma-3 is the only one pinned, because it is the one already in use and
// pinning the others means fetching each file, checking it against its origin,
// and writing the digest down. Until that happens the roster says so rather than
// reporting a shorter list that looks complete.
func Candidates() []Candidate {
	return []Candidate{
		{
			Model:    Gemma3,
			Path:     Continued,
			Why:      "the incumbent, and the tokenizer every published gao count is currently quoted in",
			Reported: 3.02,
			Predicts: Predicted{ID: "P07-5", Lo: 2.85, Hi: 3.15, Unit: PerToken},
		},
		{
			Model:    Model{Name: "llama-3.3", Vocab: 128256, Origin: "meta-llama/Llama-3.3-70B-Instruct, which is gated"},
			Path:     Continued,
			Why:      "the widest deployed alternative, and the reason the base model choice is a tokenizer choice: a third fewer characters per token than Gemma-3 on the same Vietnamese",
			Reported: 2.28,
		},
		{
			Model: Model{Name: "qwen3", Vocab: 151936, Origin: "Qwen/Qwen3-30B-A3B"},
			Path:  Continued,
			Why:   "the strongest open model in the region and the closest anchor language to Vietnamese in its training mixture",
		},
		{
			Model:    Model{Name: "gao-192k", Vocab: 192000},
			Path:     Scratch,
			Why:      "a vocabulary trained on gao text, which is the only candidate whose fertility this project gets to decide rather than accept",
			Predicts: Predicted{ID: "P07-1", Lo: 0, Hi: 1.35, Unit: PerSyllable},
		},
		{
			Model: Model{Name: "gemma-3-plus-32k", Vocab: 294144},
			Path:  Continued,
			Why:   "Gemma-3 with 32k Vietnamese pieces added, which is the only way the continued pretraining path gets to move its fertility at all",

			// P07-2 predicts the expansion improves fertility by at least 15%,
			// which against the 3.02 characters per token P07-5 fixes for the
			// unexpanded tokenizer is 3.47 and up. The other half of P07-2, that
			// the expansion does not beat the baseline at matched compute, is a
			// claim about a training run rather than about a tokenizer, and is
			// not something a fertility reading can settle.
			Predicts: Predicted{ID: "P07-2", Lo: 3.47, Unit: PerToken},
		},
	}
}

// Candidate finds one by name.
func FindCandidate(name string) (Candidate, bool) {
	for _, c := range Candidates() {
		if c.Model.Name == name {
			return c, true
		}
	}
	return Candidate{}, false
}

// A Fertility is one measurement of one tokenizer on one text on one box.
//
// Corpus and Box are not decoration. Two fertility figures are comparable only
// if they were taken on the same text, and the whole reproducibility check is
// the same text on different boxes coming back the same, so a reading missing
// either is a reading nothing can be concluded from.
type Fertility struct {
	Tokenizer string `json:"tokenizer"`
	Vocab     int    `json:"vocab,omitempty"`

	// Corpus is the digest of the text this was measured on.
	Corpus string `json:"corpus"`
	Box    string `json:"box"`

	Chars     int64 `json:"chars"`
	Syllables int64 `json:"syllables"`
	Tokens    int64 `json:"tokens"`
}

// Chars per token, which is the figure comparable across languages.
func (f Fertility) PerToken() float64 {
	if f.Tokens == 0 {
		return 0
	}
	return float64(f.Chars) / float64(f.Tokens)
}

// Tokens per syllable, which is the figure the training budget and the context
// window are functions of.
func (f Fertility) PerSyllable() float64 {
	if f.Syllables == 0 {
		return 0
	}
	return float64(f.Tokens) / float64(f.Syllables)
}

// Blocking is every reason this reading cannot be compared to another one.
func (f Fertility) Blocking() []string {
	var out []string
	if f.Tokenizer == "" {
		out = append(out, "the reading does not say which tokenizer produced it")
	}
	if f.Corpus == "" {
		out = append(out, "the reading does not say what text it was taken on, and two fertility figures over different text are not a comparison")
	}
	if f.Box == "" {
		out = append(out, "the reading does not say which box it came off, which is the whole content of the reproducibility check")
	}
	if f.Tokens <= 0 || f.Chars <= 0 || f.Syllables <= 0 {
		out = append(out, "the reading has no text in it, and a ratio over nothing is not a measurement")
	}
	return out
}

// Ok reports whether the reading can be used.
func (f Fertility) Ok() bool { return len(f.Blocking()) == 0 }

// Cost is what this fertility does to every downstream number, relative to
// another one.
//
// The training run is Cost times as expensive at a fixed amount of Vietnamese,
// the context window holds 1/Cost as much of it, and both of those are permanent
// once the vocabulary is fixed.
func (f Fertility) Cost(base Fertility) float64 {
	if base.PerSyllable() == 0 || f.PerSyllable() == 0 {
		return 0
	}
	return f.PerSyllable() / base.PerSyllable()
}

// Holds reports whether the reading falls inside the prediction, and whether the
// prediction has anything to say about this reading at all.
func (p Predicted) Holds(f Fertility) (holds, applies bool) {
	if p.ID == "" {
		return false, false
	}
	var got float64
	switch p.Unit {
	case PerToken:
		got = f.PerToken()
	case PerSyllable:
		got = f.PerSyllable()
	default:
		return false, false
	}
	if got == 0 {
		return false, false
	}
	return got >= p.Lo && (p.Hi == 0 || got <= p.Hi), true
}

// ReadFertility reads a fertility log, one reading per line.
//
// An unknown field is an error rather than something to skip, on the same
// reasoning as everywhere else here: the thing that wrote the log and the thing
// reading it are the same project, and a field one of them does not know about
// means they have drifted apart.
func ReadFertility(path string) ([]Fertility, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}
	var out []Fertility
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var f Fertility
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("count: %s line %d: %w", path, i+1, err)
		}
		out = append(out, f)
	}
	return out, nil
}

// A Measured is one candidate with every reading anybody took of it.
type Measured struct {
	Candidate Candidate
	Readings  []Fertility

	// Boxes is which machines produced those readings, sorted, which is the
	// evidence behind the claim that the number reproduces.
	Boxes []string
}

// Reading is the measurement to report for this candidate, which is the first
// one, since every other one has been checked against it.
func (m Measured) Reading() (Fertility, bool) {
	if len(m.Readings) == 0 {
		return Fertility{}, false
	}
	return m.Readings[0], true
}

// A Slate is the roster with whatever has been measured folded onto it.
type Slate struct {
	Measured []Measured

	// Missing is the candidates nobody has measured, named, because the item is
	// to measure every candidate and a list that quietly omits the ones nobody
	// got to reads as if the work were done.
	Missing []string

	// Faults is what makes the readings unusable or non-comparable, one sentence
	// each.
	Faults []string
}

// Fold puts a set of readings against the roster.
func Fold(readings []Fertility) Slate {
	var s Slate
	byName := map[string][]Fertility{}
	for _, f := range readings {
		if why := f.Blocking(); len(why) > 0 {
			s.Faults = append(s.Faults, fmt.Sprintf("a reading of %q is unusable: %s", f.Tokenizer, why[0]))
			continue
		}
		byName[f.Tokenizer] = append(byName[f.Tokenizer], f)
	}

	for _, c := range Candidates() {
		got := byName[c.Model.Name]
		delete(byName, c.Model.Name)
		if len(got) == 0 {
			s.Missing = append(s.Missing, c.Model.Name)
			continue
		}
		m := Measured{Candidate: c, Readings: got}
		for _, f := range got {
			m.Boxes = append(m.Boxes, f.Box)
		}
		sort.Strings(m.Boxes)
		s.Measured = append(s.Measured, m)
		s.Faults = append(s.Faults, disagreements(c, got)...)
	}

	for name := range byName {
		s.Faults = append(s.Faults, fmt.Sprintf("%s was measured and is not on the roster, so either the roster is out of date or the reading is of something nobody is considering", name))
	}
	sort.Strings(s.Faults)
	return s
}

// disagreements is the reproducibility check, which is the cheapest one in the
// project and catches the largest class of quiet mistake.
func disagreements(c Candidate, got []Fertility) []string {
	var out []string
	first := got[0]
	for _, f := range got[1:] {
		switch {
		case f.Corpus != first.Corpus:
			out = append(out, fmt.Sprintf("%s was measured on different text on %s and %s, so the two numbers are not a comparison and neither of them is the reproducibility check",
				c.Model.Name, first.Box, f.Box))
		case f.Tokens != first.Tokens || f.Chars != first.Chars || f.Syllables != first.Syllables:
			out = append(out, fmt.Sprintf("%s counted %d tokens on %s and %d on %s over the same text, which is a locale, a normalization, or a tokenizer file that is not the pinned one",
				c.Model.Name, first.Tokens, first.Box, f.Tokens, f.Box))
		case f.Box == first.Box:
			out = append(out, fmt.Sprintf("%s was measured twice on %s and never anywhere else, which is a repeat rather than a reproduction",
				c.Model.Name, f.Box))
		}
	}
	if c.Pinned() && first.Vocab != 0 && first.Vocab != c.Model.Vocab {
		out = append(out, fmt.Sprintf("%s was measured with a vocabulary of %d against a pin of %d, so the file that was measured is not the file that was pinned",
			c.Model.Name, first.Vocab, c.Model.Vocab))
	}
	return out
}

// Ranked is the measured candidates cheapest first, by the figure the budget is
// a function of.
func (s Slate) Ranked() []Measured {
	out := make([]Measured, 0, len(s.Measured))
	for _, m := range s.Measured {
		if f, ok := m.Reading(); ok && f.PerSyllable() > 0 {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		x, _ := out[a].Reading()
		y, _ := out[b].Reading()
		return x.PerSyllable() < y.PerSyllable()
	})
	return out
}

// Reproduced reports whether every measured candidate came back the same on more
// than one box, which is the fleet item on this milestone.
func (s Slate) Reproduced() bool {
	if len(s.Measured) == 0 {
		return false
	}
	for _, m := range s.Measured {
		if len(unique(m.Boxes)) < 2 {
			return false
		}
	}
	return len(s.Faults) == 0
}

func unique(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}

// Complete reports whether every candidate on the roster has been measured,
// which is what the checklist item actually says.
func (s Slate) Complete() bool { return len(s.Missing) == 0 && len(s.Faults) == 0 }

// Spread is what choosing the worst measured candidate over the best would cost,
// as a multiplier on every training run and a divisor on every context window.
func (s Slate) Spread() float64 {
	r := s.Ranked()
	if len(r) < 2 {
		return 0
	}
	best, _ := r[0].Reading()
	worst, _ := r[len(r)-1].Reading()
	return worst.Cost(best)
}

// Verdict is the slate in one sentence.
func (s Slate) Verdict() string {
	if len(s.Measured) == 0 {
		return fmt.Sprintf("nothing on the roster has been measured, and there are %d candidates on it", len(Candidates()))
	}
	r := s.Ranked()
	if len(r) == 0 {
		return fmt.Sprintf("nothing on the roster has been measured, and there are %d candidates on it", len(Candidates()))
	}
	best, _ := r[0].Reading()

	var tail string
	switch {
	case len(s.Faults) > 0:
		tail = fmt.Sprintf(", but %d of the readings cannot be used, starting with %s", len(s.Faults), s.Faults[0])
	case len(s.Missing) > 0:
		has := "has"
		if len(s.Missing) > 1 {
			has = "have"
		}
		tail = fmt.Sprintf(", and %s of %d candidates %s not been measured, so this is not yet the comparison the decision needs",
			plural(len(s.Missing)), len(Candidates()), has)
	}
	// One measurement has nothing to be cheaper than, and quoting a spread across
	// a single reading would print a number that means nothing.
	spread := ", and there is nothing else measured to be cheaper than"
	if s.Spread() > 0 {
		spread = fmt.Sprintf(", and the worst of them costs %.0f%% more for the same Vietnamese", (s.Spread()-1)*100)
	}
	return fmt.Sprintf("%s is the cheapest of %d measured at %.2f tokens per syllable%s%s",
		best.Tokenizer, len(r), best.PerSyllable(), spread, tail)
}

func plural(n int) string {
	if n == 1 {
		return "one"
	}
	return fmt.Sprintf("%d", n)
}

// Within reports whether two fertility figures agree to a tolerance, which is
// the form the anchor language part of P07-1 takes: a gao vocabulary has to stay
// within 8% of Gemma-3 on English and code while beating it on Vietnamese.
func Within(a, b, tolerance float64) bool {
	if a == 0 || b == 0 {
		return false
	}
	return math.Abs(a-b)/b <= tolerance
}
