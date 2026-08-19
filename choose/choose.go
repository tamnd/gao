package choose

// Choosing a base model, in the order the criteria were written down.
//
// Six criteria, ranked, and the ranking is the whole content of the package. A
// table that scores six things and adds them up is a table where a candidate
// that loses on the one criterion that cannot be traded still wins, because
// enough small advantages elsewhere outvoted it. The spec is explicit about two
// of them in particular. The license either permits derivative weights and
// commercial use or the candidate is out, whatever else is true of it. And
// fertility is enough to break a tie on base quality and not enough to override
// it, which is a sentence about arithmetic and has to be implemented as one.
//
// So the comparison is lexicographic with a band. Two bases whose measured
// quality is within the band are tied on criterion 2 and criterion 3 decides
// between them. Two bases further apart than the band are not tied, and no
// fertility figure moves them.
//
// The other half of this is what happens before anything can be ranked. Four of
// the six are measurements somebody has to take: quality measured directly
// rather than read off a model card, fertility measured on gao text, Vietnamese
// exposure probed before any training, and context length checked rather than
// quoted. An unmeasured criterion is not a zero. It is a hole, and a table that
// silently scores around it produces a ranking that looks like a decision.

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

// Band is how close two bases have to be on measured quality before fertility
// gets to decide between them, in points on the same scale the quality
// measurement is quoted in.
//
// It exists because the alternative is a threshold at zero, where two bases are
// tied only if they measure identically and criterion 3 therefore never fires.
const Band = 2.0

// A Criterion is one of the six, in the order they were written.
type Criterion struct {
	Rank int
	Name string
	Why  string

	// Gate is a criterion that removes a candidate rather than scoring it.
	Gate bool

	// Tie is a criterion that only decides between candidates already tied on
	// something above it.
	Tie bool

	// Measured is a criterion somebody has to take a reading for, as opposed to
	// one that can be read off what the model is.
	Measured bool
}

// Criteria is the list, and the order is binding rather than presentational.
func Criteria() []Criterion {
	return []Criterion{
		{Rank: 1, Name: "license", Gate: true,
			Why: "permits derivative weights and commercial use, which is not negotiable and not a score"},
		{Rank: 2, Name: "base quality", Measured: true,
			Why: "multilingual reasoning measured directly, because a model card is a claim by the party being evaluated"},
		{Rank: 3, Name: "fertility", Tie: true, Measured: true,
			Why: "a base at 1.50 tokens per syllable gives 33% more Vietnamese per FLOP than one at 1.99, which breaks a tie and does not overturn criterion 2"},
		{Rank: 4, Name: "vietnamese exposure", Measured: true,
			Why: "probed before any training, since teaching a base the language and deepening a base that already has 2% of it are different problems"},
		{Rank: 5, Name: "long context", Measured: true,
			Why: "already present, so the extension to 131k is an extension rather than a construction"},
		{Rank: 6, Name: "architecture",
			Why: "a 2026 architecture, so what is learned continuing it transfers to the run that starts from nothing"},
	}
}

// A Base is a candidate to continue pretraining from.
type Base struct {
	Name   string `json:"name"`
	Family string `json:"family"`

	// Params and Active are the total and the per token cost, equal on a dense
	// model, which is most of what decides whether a continued pretraining run
	// is affordable at all.
	Params int64 `json:"params,omitempty"`
	Active int64 `json:"active,omitempty"`

	// Tokenizer is the name this base's vocabulary goes by on the fertility
	// roster. A base whose tokenizer is not on that roster is a base criterion 3
	// cannot be applied to, which is a fact worth printing rather than a reason
	// to leave it out.
	Tokenizer string `json:"tokenizer"`
	Vocab     int    `json:"vocab,omitempty"`

	// Context is the length the weights were trained to handle, which criterion
	// 5 is about.
	Context int `json:"context,omitempty"`

	License string `json:"license"`

	// Derivatives is criterion 1, and it is the only field here that can end a
	// candidacy on its own.
	Derivatives bool `json:"derivatives"`

	// Modern is criterion 6.
	Modern bool `json:"modern"`

	Why string `json:"why"`
}

// Bases is the roster the spec names: the Qwen3, Gemma-3, Llama-3.x, Mistral and
// SEA-tuned families.
//
// The choice is made on the table rather than here, because the field moves
// faster than any document about it, and a roster written into code at least
// dates itself honestly.
func Bases() []Base {
	return []Base{
		{
			Name: "gemma-3-27b-it", Family: "Gemma-3", Params: 27e9, Active: 27e9,
			Tokenizer: "gemma-3", Vocab: 262144, Context: 131072,
			License: "Gemma terms of use, which permit derivative weights and commercial use under a use policy", Derivatives: true, Modern: true,
			Why: "the incumbent, whose tokenizer every published gao count is currently quoted in",
		},
		{
			Name: "qwen3-30b-a3b", Family: "Qwen3", Params: 30e9, Active: 3e9,
			Tokenizer: "qwen3", Vocab: 151936, Context: 32768,
			License: "Apache 2.0", Derivatives: true, Modern: true,
			Why: "3B active per token, which is the only candidate here whose continued pretraining run costs a fraction of what its size suggests",
		},
		{
			Name: "llama-3.3-70b-instruct", Family: "Llama-3.x", Params: 70e9, Active: 70e9,
			Tokenizer: "llama-3.3", Vocab: 128256, Context: 131072,
			License: "Llama 3.3 community license, which permits derivative weights and commercial use below 700M monthly users", Derivatives: true, Modern: false,
			Why: "the widest deployed alternative, and dense at 70B, which is 23 times the forward cost of the Qwen3 mixture",
		},
		{
			Name: "mistral-small-3", Family: "Mistral", Params: 24e9, Active: 24e9,
			Tokenizer: "tekken", Vocab: 131072, Context: 131072,
			License: "Apache 2.0", Derivatives: true, Modern: true,
			Why: "the cleanest license on the roster, and a tokenizer nobody here has measured on Vietnamese",
		},
		{
			Name: "sailor2-8b", Family: "SEA-tuned", Params: 8e9, Active: 8e9,
			Tokenizer: "qwen2.5", Vocab: 151936, Context: 32768,
			License: "Apache 2.0", Derivatives: true, Modern: false,
			Why: "already continued on Southeast Asian text, so criterion 4 is the one it is here to win and the one nobody has probed",
		},
	}
}

// FindBase finds one by name.
func FindBase(name string) (Base, bool) {
	for _, b := range Bases() {
		if b.Name == name {
			return b, true
		}
	}
	return Base{}, false
}

// A Reading is what somebody measured about a base, and is separate from the
// base because everything in it has to be taken rather than looked up.
type Reading struct {
	Base string `json:"base"`

	// Quality is criterion 2, on whatever multilingual reasoning scale the run
	// used, and Suite names it, because two scores from two suites are not a
	// comparison and the point of criterion 2 is a comparison.
	Quality float64 `json:"quality"`
	Suite   string  `json:"suite"`

	// Fertility is criterion 3, in tokens per Vietnamese syllable, measured on
	// gao text by the fertility slate rather than quoted from anywhere.
	Fertility float64 `json:"fertility"`

	// Exposure is criterion 4, the share of the base's pretraining that was
	// Vietnamese, probed rather than taken from a paper.
	Exposure float64 `json:"exposure"`

	Box string `json:"box"`
}

// Blocking is every reason this reading cannot be scored.
func (r Reading) Blocking() []string {
	var out []string
	if r.Base == "" {
		out = append(out, "the reading does not say which base it is of")
	}
	if r.Quality <= 0 {
		out = append(out, "criterion 2 is the one that decides, and this reading has no quality measurement in it")
	}
	if r.Suite == "" {
		out = append(out, "the reading does not name the suite the quality was measured on, and two scores from two suites are not a comparison")
	}
	if r.Fertility <= 0 {
		out = append(out, "the reading has no fertility in it, so there is nothing to break a tie with")
	}
	if r.Box == "" {
		out = append(out, "the reading does not say which box it came off")
	}
	return out
}

// Ok reports whether the reading can be scored.
func (r Reading) Ok() bool { return len(r.Blocking()) == 0 }

// ReadReadings reads a log of them, one per line.
func ReadReadings(path string) ([]Reading, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("chon: %w", err)
	}
	var out []Reading
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var r Reading
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("chon: %s line %d: %w", path, i+1, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// A Row is one base with whatever has been measured of it.
type Row struct {
	Base    Base
	Reading Reading

	// Out is why criterion 1 removed this candidate, empty if it did not.
	Out string

	// Holes is the criteria nobody has a reading for on this base.
	Holes []string
}

// Scored reports whether this row can take part in a ranking at all.
func (r Row) Scored() bool { return r.Out == "" && len(r.Holes) == 0 }

// A Table is the roster with the measurements folded onto it.
type Table struct {
	Rows []Row

	// Suites is every suite a quality figure came from, and more than one of
	// them is a table that cannot be ranked.
	Suites []string

	Faults []string
}

// Score puts a set of readings against the roster.
func Score(readings []Reading) Table {
	var t Table
	by := map[string]Reading{}
	for _, r := range readings {
		if why := r.Blocking(); len(why) > 0 {
			t.Faults = append(t.Faults, fmt.Sprintf("a reading of %q is unusable: %s", r.Base, why[0]))
			continue
		}
		if _, ok := FindBase(r.Base); !ok {
			t.Faults = append(t.Faults, fmt.Sprintf("%s was measured and is not on the roster, so either the roster is out of date or somebody evaluated a model nobody is considering", r.Base))
			continue
		}
		if prev, ok := by[r.Base]; ok && prev.Quality != r.Quality {
			t.Faults = append(t.Faults, fmt.Sprintf("%s was measured at %.1f on %s and %.1f on %s, and a criterion that decides cannot have two values",
				r.Base, prev.Quality, prev.Box, r.Quality, r.Box))
			continue
		}
		by[r.Base] = r
		if !contains(t.Suites, r.Suite) {
			t.Suites = append(t.Suites, r.Suite)
		}
	}
	sort.Strings(t.Suites)

	for _, b := range Bases() {
		row := Row{Base: b, Reading: by[b.Name]}
		if !b.Derivatives {
			row.Out = "the license does not permit derivative weights, which is criterion 1 and is not tradeable against anything below it"
		}
		row.Holes = holes(b, by[b.Name])
		t.Rows = append(t.Rows, row)
	}

	if len(t.Suites) > 1 {
		t.Faults = append(t.Faults, fmt.Sprintf("quality was measured on %s, and a ranking across two suites is a ranking of the suites", strings.Join(t.Suites, " and ")))
	}
	sort.Strings(t.Faults)
	return t
}

func holes(b Base, r Reading) []string {
	var out []string
	if r.Quality <= 0 {
		out = append(out, "criterion 2, base quality, has not been measured on it")
	}
	if r.Fertility <= 0 {
		out = append(out, "criterion 3, fertility on gao text, has not been measured on it")
	}
	if r.Exposure <= 0 {
		out = append(out, "criterion 4, Vietnamese exposure, has not been probed on it")
	}
	if b.Context <= 0 {
		out = append(out, "criterion 5, the context length the weights were trained to, is not written down")
	}
	return out
}

func contains(in []string, s string) bool { return slices.Contains(in, s) }

// Ranked is the scorable rows, best first, under the ordering the criteria
// describe.
//
// Quality decides, except between rows within Band of each other, where
// fertility does. That is not a total order in the mathematical sense, and it
// does not need to be: it is a comparison applied to a sorted list, and what it
// guarantees is that no row is ever moved above another one it is more than a
// band worse than.
func (t Table) Ranked() []Row {
	out := make([]Row, 0, len(t.Rows))
	for _, r := range t.Rows {
		if r.Scored() {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		x, y := out[a], out[b]
		if diff := x.Reading.Quality - y.Reading.Quality; diff > Band || diff < -Band {
			return diff > 0
		}
		if x.Reading.Fertility != y.Reading.Fertility {
			return x.Reading.Fertility < y.Reading.Fertility
		}
		return x.Reading.Quality > y.Reading.Quality
	})
	return out
}

// Tied reports whether fertility rather than quality is what put the first row
// above the second, which is the one thing about this table a reader has to be
// told rather than left to infer.
func (t Table) Tied() bool {
	r := t.Ranked()
	if len(r) < 2 {
		return false
	}
	diff := r[0].Reading.Quality - r[1].Reading.Quality
	return diff <= Band && diff >= -Band
}

// Missing is the bases nothing can be said about yet, with the reason.
func (t Table) Missing() []string {
	var out []string
	for _, r := range t.Rows {
		if r.Out != "" || len(r.Holes) == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s", r.Base.Name, strings.Join(r.Holes, ", and ")))
	}
	return out
}

// Decided reports whether the table is a decision rather than a progress report.
//
// Every candidate still in the running has to have been measured on everything.
// A ranking over the two somebody got to, with three untouched, names a winner
// out of a field that was never assembled.
func (t Table) Decided() bool {
	if len(t.Faults) > 0 || len(t.Ranked()) == 0 {
		return false
	}
	return len(t.Missing()) == 0
}

// Choose is the base the table picks, and whether the table is entitled to pick
// one.
func (t Table) Choose() (Base, bool) {
	r := t.Ranked()
	if len(r) == 0 {
		return Base{}, false
	}
	return r[0].Base, t.Decided()
}

// Verdict is the table in one sentence.
func (t Table) Verdict() string {
	r := t.Ranked()
	if len(r) == 0 {
		return fmt.Sprintf("nothing on the roster has been measured on all of the criteria that need measuring, and there are %d bases on it", len(Bases()))
	}
	best := r[0]

	lead := fmt.Sprintf("%s leads on criterion 2 at %.1f on %s", best.Base.Name, best.Reading.Quality, best.Reading.Suite)
	if t.Tied() {
		lead = fmt.Sprintf("%s and %s are inside the %.0f point band on criterion 2, so criterion 3 decides and %s wins it at %.2f tokens per syllable",
			best.Base.Name, r[1].Base.Name, Band, best.Base.Name, best.Reading.Fertility)
	}

	switch {
	case len(t.Faults) > 0:
		return fmt.Sprintf("%s, but %d of the readings cannot be used, starting with %s", lead, len(t.Faults), t.Faults[0])
	case len(t.Missing()) > 0:
		return fmt.Sprintf("%s, and %d of %d bases have not been measured on everything, so this is a leader rather than a choice",
			lead, len(t.Missing()), len(Bases()))
	}
	return fmt.Sprintf("%s, and every base on the roster has been measured on every criterion, so this is the choice", lead)
}
