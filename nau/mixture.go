// Package nau holds the training plan: the token budget, the curriculum that
// spends it, the three arms of the continued pretraining comparison, and what
// the fleet is allowed to do while all of that runs.
//
// It is a package rather than a section of a document because a mixture table
// is arithmetic, and arithmetic written in prose is arithmetic nobody checks. A
// budget whose components do not add up to the budget, a curriculum that spends
// a component at a different rate than the budget buys it, and a comparison
// whose arms differ in two things at once are all mistakes that survive review
// and are found halfway through a run that costs tens of thousands of dollars.
// Here they are a failing test.
//
// Nothing in this package trains anything. It is the part of the plan that has
// to be fixed before a run starts, which is what makes it worth writing down
// while there is still nothing to lose by changing it.
package nau

import "fmt"

// billion is the unit every token count here is written in. The plan is stated
// in billions of tokens everywhere it is discussed, so the constants read the
// way the argument reads.
const billion = 1_000_000_000

// A Kind is what a component of the mixture is made of.
//
// It exists because design rule 2 says natural and synthetic are never summed
// into one headline, and a rule about counting needs the counts to carry the
// distinction rather than the person doing the adding to remember it.
type Kind int

const (
	// Natural is Vietnamese written by people.
	Natural Kind = iota
	// Synthetic is Vietnamese a model produced.
	Synthetic
	// Translated is Vietnamese that started in another language.
	Translated
	// Anchor is everything deliberately not Vietnamese.
	Anchor
)

func (k Kind) String() string {
	switch k {
	case Natural:
		return "natural"
	case Synthetic:
		return "synthetic"
	case Translated:
		return "translated"
	case Anchor:
		return "anchor"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// Vietnamese reports whether the kind counts toward the Vietnamese share, which
// is every kind but the anchors.
func (k Kind) Vietnamese() bool { return k != Anchor }

// A Component is one line of the token budget.
//
// Unique is how much of it exists, Epochs is how many times the run reads it,
// and the product is what the run actually spends. The two are kept apart
// because they fail differently: repeating a component is a decision with a
// known ceiling around four passes, and having less of it than the plan assumed
// is a measurement that arrives later and moves everything downstream.
type Component struct {
	Name   string
	Unique int64
	Epochs float64
	Kind   Kind
	Why    string

	// Within names the component whose text this one re-reads, and is empty when
	// the text is its own.
	//
	// The quality tiers are not separate corpora. gao-web-hq is the top of
	// gao-web and gao-edu is the top of that, so their unique tokens are already
	// counted in gao-web's and adding all three together says the crawl has to
	// produce seventy billion tokens that do not need to exist. That is the
	// difference between the budget's component rows and its own subtotal row,
	// and it is the kind of double count a table cannot catch about itself.
	Within string
}

// Instances is what the component contributes to the run, which is the unique
// tokens times the passes over them.
func (c Component) Instances() int64 { return int64(float64(c.Unique)*c.Epochs + 0.5) }

// Passes is how many times the run reads this component's text in total,
// counting the passes it gets from whatever it sits inside.
//
// It is the number the four epoch ceiling is about. Epochs on its own says
// gao-edu is read three times, which is true of the gao-edu line and not true
// of the text, since gao-web reads it once more.
func (c Component) Passes() float64 {
	if o, ok := component(c.Within); ok {
		return c.Epochs + o.Passes()
	}
	return c.Epochs
}

// component looks a budget line up by name.
func component(name string) (Component, bool) {
	for _, c := range Budget() {
		if c.Name == name {
			return c, true
		}
	}
	return Component{}, false
}

// Budget is the from scratch run's mixture, at 1 T token instances.
//
// The tension the whole table is downstream of: the budget is a trillion tokens
// and there are about three hundred billion natural Vietnamese tokens in
// existence. Three moves close the gap, and each one is counted on its own line
// because each one fails in its own way. Repetition degrades past roughly four
// passes. Synthesis narrows the distribution. Anchor languages buy reasoning
// that Vietnamese web text does not contain, and dilute the Vietnamese the run
// exists to learn.
func Budget() []Component {
	return []Component{
		{Name: "gao-web", Unique: 265 * billion, Epochs: 1.0, Kind: Natural,
			Why: "the cleaned Vietnamese web, read once"},
		{Name: "gao-web-hq", Unique: 45 * billion, Epochs: 2.0, Kind: Natural, Within: "gao-web",
			Why: "the high quality slice, given two passes on top of the one it gets as part of the web"},
		{Name: "gao-edu", Unique: 25 * billion, Epochs: 3.0, Kind: Natural, Within: "gao-web",
			Why: "the educational slice, the most repeated text in the run, and the only line sitting on the four pass ceiling"},
		{Name: "gao-pdf", Unique: 32 * billion, Epochs: 2.0, Kind: Natural,
			Why: "Vietnamese that was typeset rather than posted, which is where the long formal registers are"},
		{Name: "gao-voice", Unique: 8 * billion, Epochs: 2.0, Kind: Natural,
			Why: "transcribed speech, which is the only spoken register in the mixture"},
		{Name: "gao-legal", Unique: 4 * billion, Epochs: 2.0, Kind: Natural,
			Why: "statutes and gazettes, small and structurally unlike everything else"},
		{Name: "gao-synth", Unique: 150 * billion, Epochs: 1.0, Kind: Synthetic,
			Why: "a rephrase pass over the top of the quality distribution, in four styles so it does not narrow"},
		{Name: "vi-translated", Unique: 10 * billion, Epochs: 1.0, Kind: Translated,
			Why: "machine translated Vietnamese, kept small and kept labeled"},
		{Name: "english", Unique: 180 * billion, Epochs: 1.0, Kind: Anchor,
			Why: "curated English, which is where most of the reasoning transfer comes from"},
		{Name: "code", Unique: 100 * billion, Epochs: 1.0, Kind: Anchor,
			Why: "code, for the structure it teaches rather than for the code"},
		{Name: "chinese", Unique: 40 * billion, Epochs: 1.0, Kind: Anchor,
			Why: "Chinese, the anchor closest to Vietnamese in vocabulary and in register"},
		{Name: "math", Unique: 30 * billion, Epochs: 1.0, Kind: Anchor,
			Why: "mathematics and reasoning traces, thin on the Vietnamese web and load bearing at evaluation"},
	}
}

// Instances is the size of the run in token instances, which is what the
// compute is bought against.
func Instances() int64 {
	var n int64
	for _, c := range Budget() {
		n += c.Instances()
	}
	return n
}

// Unique is how much distinct text the run reads, which is what the corpus has
// to actually hold.
//
// The extra pass lines are skipped, since their text is counted where it lives.
func Unique() int64 {
	var n int64
	for _, c := range Budget() {
		if c.Within == "" {
			n += c.Unique
		}
	}
	return n
}

// Share is the fraction of the run one component is, as a percentage.
func Share(name string) float64 {
	for _, c := range Budget() {
		if c.Name == name {
			return 100 * float64(c.Instances()) / float64(Instances())
		}
	}
	return 0
}

// KindShare is the fraction of the run a kind is, as a percentage.
func KindShare(k Kind) float64 {
	var n int64
	for _, c := range Budget() {
		if c.Kind == k {
			n += c.Instances()
		}
	}
	return 100 * float64(n) / float64(Instances())
}

// VietnameseShare is how much of the run is Vietnamese of any kind.
//
// It is 66 rather than 90 on purpose. A mixture that is almost entirely
// Vietnamese produces a model that speaks Vietnamese and cannot reason, because
// reasoning transfers across languages and the Vietnamese web holds very little
// of it. The other 34 is the price of a model that thinks in Vietnamese rather
// than one that only produces it.
func VietnameseShare() float64 { return 100 - KindShare(Anchor) }

// NaturalUnique is how much natural Vietnamese the run needs to exist, which is
// the number the whole corpus effort is measured against.
//
// It is the one number in this package that is a target for other people's
// work, so it is the one where the double count would have done real damage. A
// crawl told to produce 379 billion tokens rather than 309 is a crawl given a
// year of work that the plan never asked for.
func NaturalUnique() int64 {
	var n int64
	for _, c := range Budget() {
		if c.Kind == Natural && c.Within == "" {
			n += c.Unique
		}
	}
	return n
}

// NaturalEpochs is the average number of passes over natural Vietnamese.
//
// The average is not the number that carries the risk. The web is read once and
// the educational slice four times, and it is the four that sits at the edge of
// where repetition stops being nearly as good as fresh text.
func NaturalEpochs() float64 {
	var instances int64
	for _, c := range Budget() {
		if c.Kind == Natural {
			instances += c.Instances()
		}
	}
	return float64(instances) / float64(NaturalUnique())
}
