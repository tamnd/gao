package cook

// The curriculum, which is how the budget is spent over the run.
//
// The mixture at a tenth of the way through is not the mixture at nine tenths.
// Cheap broad text early, while the model is learning the shape of the
// language, and curated text late, while it is learning to be good at it. The
// schedule is fixed here rather than tuned during the run, because a mixture
// reweighted online makes a run unreproducible for a gain nobody has shown at
// this scale, and because a schedule published with the model is a schedule
// somebody else can argue with.

// A Slot is one component's share of one phase, as a percentage of that phase.
type Slot struct {
	Component string
	Percent   float64
}

// A Phase is one stage of the run.
type Phase struct {
	// Name is what the phase is for.
	Name string
	// Share is how much of the run it is, as a percentage.
	Share float64
	// Sequence is the context length trained at.
	Sequence int
	// LR is what the learning rate does during it.
	LR string
	// Mix is what the phase reads, in shares of the phase.
	Mix []Slot
	// Why is the argument for this phase looking the way it does.
	Why string
}

// Tokens is how many token instances the phase spends, out of the whole run.
func (p Phase) Tokens() int64 { return int64(float64(Instances())*p.Share/100 + 0.5) }

// Curriculum is the three phases, in order.
//
// The Vietnamese specific decision in it is that gao-pdf and gao-legal rise
// steadily rather than arriving in the last tenth. Vietnamese formal registers
// are structurally far enough from web Vietnamese that a model which meets them
// only at the end has seen them without internalizing them.
func Curriculum() []Phase {
	return []Phase{
		{
			Name:     "bulk",
			Share:    60,
			Sequence: 4096,
			LR:       "warmup then constant",
			Why:      "the broad half of the corpus, read once, while the model is learning what Vietnamese looks like at all",
			Mix: []Slot{
				{"gao-web", 55},
				{"english", 15},
				{"code", 12},
				{"gao-synth", 8},
				{"chinese", 4},
				{"gao-pdf", 4},
				{"gao-legal+gao-voice", 2},
			},
		},
		{
			Name:     "ramp",
			Share:    30,
			Sequence: 32768,
			LR:       "constant",
			Why:      "the curated slices arrive and the context lengthens, with long documents upweighted rather than short ones concatenated",
			Mix: []Slot{
				{"gao-web", 30},
				{"gao-synth", 20},
				{"gao-web-hq", 12},
				{"code", 10},
				{"gao-edu", 8},
				{"gao-pdf", 8},
				{"english", 8},
				{"gao-legal+gao-voice", 4},
			},
		},
		{
			Name:     "anneal",
			Share:    10,
			Sequence: 131072,
			LR:       "linear decay to zero",
			Why:      "the last tenth, where most of the measurable quality lands and which is cheap enough to run three times with three mixtures and keep the best",
			Mix: []Slot{
				{"gao-web-hq", 25},
				{"gao-edu", 20},
				{"gao-synth", 20},
				{"gao-pdf", 10},
				{"gao-legal+gao-voice", 8},
				{"math", 8},
				{"code", 5},
				{"english", 4},
			},
		},
	}
}

// Spend is the share of the whole run one component gets under the curriculum,
// as a percentage, which is the number that has to agree with what the budget
// bought.
func Spend(component string) float64 {
	var total float64
	for _, p := range Curriculum() {
		for _, s := range p.Mix {
			if s.Component == component {
				total += p.Share * s.Percent / 100
			}
		}
	}
	return total
}

// Components is every name the curriculum spends on, in the order it first
// meets them.
func Components() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range Curriculum() {
		for _, s := range p.Mix {
			if !seen[s.Component] {
				seen[s.Component] = true
				out = append(out, s.Component)
			}
		}
	}
	return out
}
