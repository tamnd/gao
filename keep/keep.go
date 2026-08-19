// Package keep measures what a distilled model kept of each specialist's gain.
//
// Giữ is to keep. Seven specialists are trained in parallel with verifiable
// rewards, each one good at a thing the base model was not, and then all seven
// are distilled back into a single model that has to serve all of it. The
// question this package answers is how much of each specialist survived that,
// and the word that matters in the milestone is individually.
//
// A mean retention is the number everybody reports and it is the one number
// that cannot be acted on. Seven specialists, six of them at 95% and one at
// 20%, average 84%, and 84% reads as a good result while the model in question
// is worse at legal citation than the base model was before any of this
// started. The average is the arithmetic of a model that works and a model that
// does not, and nobody trains against it. So the worst line is what the verdict
// quotes, and the mean is printed beside it rather than instead of it.
//
// Retention is a ratio of two differences and that is its weakness. Distilled
// minus base, over specialist minus base. Both differences carry whatever the
// evaluation's own spread is, so a specialist that gained 1.2 points on a
// benchmark whose runs vary by 0.8 has a retention number that is mostly noise
// with a percent sign on it. This refuses those rather than reporting them,
// because a retention of 130% and a retention of 40% look like different
// findings and are the same measurement taken twice.
//
// The other half of P09-2 is the baseline. Distillation recovering 90% is not a
// result on its own, since the cheap thing it is supposed to beat is averaging
// the seven checkpoints' weights, which costs one afternoon and no GPU hours.
// The prediction is that merging recovers 70% or less, and if merging comes back
// at 88% then the honest reading is that the pipeline bought two points for
// seven training runs. Both halves are checked, and the second one is checked
// against a merged model evaluated the same way on the same benchmarks, since a
// baseline scored differently is not a baseline.
package keep

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
)

// Recovers is the share of a specialist's gain that multi-teacher on-policy
// distillation is predicted to keep. It is the first half of P09-2.
const Recovers = 0.90

// Merges is the most of a specialist's gain that naive weight merging is
// predicted to keep. It is the second half of P09-2, and it is what makes the
// first half a result rather than a number.
const Merges = 0.70

// Specialists is how many the plan trains. A retention reported over the ones
// somebody got round to evaluating is a retention over the ones that worked.
const Specialists = 7

// Spread is how far the mean retention may sit above the worst specialist
// before quoting the mean alone is quoting the arithmetic of a model that works
// and a model that does not.
const Spread = 0.20

// MinGain is the smallest gain a specialist can have and still support a
// retention number. Below a point the ratio is two differences of the same size
// as the evaluation's own noise, and it will read as a finding either way.
const MinGain = 1.0

// A Specialist is one reinforcement learning run and the three scores that say
// what happened to it.
type Specialist struct {
	Name string `json:"name"`

	// Benchmark is what the gain is measured on. It is per specialist because a
	// legal citation specialist and a diacritic restoration specialist are not
	// good at the same thing and are not scored on the same set.
	Benchmark string `json:"benchmark"`

	// Base is the model before any post-training, Own is the specialist itself,
	// Distilled is the one model all seven were distilled into, and Merged is
	// the same seven checkpoints averaged in weight space.
	Base      float64 `json:"base"`
	Own       float64 `json:"own"`
	Distilled float64 `json:"distilled"`
	Merged    float64 `json:"merged"`

	// Runs is how many evaluation runs each score above is the mean of, and
	// Spread is how far apart those runs were. A retention is a ratio of two
	// differences, so it inherits this twice.
	Runs   int     `json:"runs"`
	Spread float64 `json:"spread"`

	// Box is the machine the scores came off. All of them have to come off the
	// same one, since a difference between two scores measured on two cards is a
	// difference between two cards.
	Box string `json:"box"`
}

// Gain is what the specialist bought over the base model.
func (s Specialist) Gain() float64 { return s.Own - s.Base }

// Kept is what the distilled model has of that gain, in points.
func (s Specialist) Kept() float64 { return s.Distilled - s.Base }

// Retention is the share of the gain the distilled model kept. It is not
// clamped: below zero means the distilled model is worse than the base at
// something a specialist was trained for, and that is worth seeing as a negative
// number rather than as a floor.
func (s Specialist) Retention() float64 {
	if s.Gain() == 0 {
		return 0
	}
	return s.Kept() / s.Gain()
}

// Merging is the same share for the weight merged baseline.
func (s Specialist) Merging() float64 {
	if s.Gain() == 0 {
		return 0
	}
	return (s.Merged - s.Base) / s.Gain()
}

// Blocking is every reason this line cannot carry a retention number.
func (s Specialist) Blocking() []string {
	var why []string
	switch {
	case s.Name == "":
		why = append(why, "a specialist with no name cannot be reported individually, which is the whole of what this item asks for")
	case s.Benchmark == "":
		why = append(why, fmt.Sprintf("%s does not say what benchmark its gain was measured on", s.Name))
	}
	if s.Box == "" {
		why = append(why, fmt.Sprintf(
			"%s does not say which box it was scored on, and a retention is a difference of two scores, so both have to come off the same card",
			s.Name))
	}
	if s.Runs < 2 {
		why = append(why, fmt.Sprintf(
			"%s was evaluated %s, so there is no spread to read its gain against and a retention computed from it cannot be told from noise",
			s.Name, plural(s.Runs, "time")))
	}
	if g := s.Gain(); g < MinGain {
		why = append(why, fmt.Sprintf(
			"%s gained %.1f points on %s, under the %.1f a retention needs, so the ratio is two differences the size of the evaluation itself",
			s.Name, g, s.Benchmark, MinGain))
	} else if s.Spread > 0 && g < 2*s.Spread {
		why = append(why, fmt.Sprintf(
			"%s gained %.1f points on %s against a spread of %.1f across %d runs, and a retention computed on that is a ratio of two numbers that are mostly noise",
			s.Name, g, s.Benchmark, s.Spread, s.Runs))
	}
	if s.Merged == 0 {
		why = append(why, fmt.Sprintf(
			"%s has no merged score, and distillation keeping %.0f%% is only a result next to what averaging the checkpoints keeps",
			s.Name, 100*s.Retention()))
	}
	if r := s.Retention(); r > 1.05 {
		why = append(why, fmt.Sprintf(
			"%s kept %.0f%% of its own gain, which is the distilled model beating the teacher, and the two explanations for that are a specialist nobody trained to convergence and a benchmark that is in the distillation set",
			s.Name, 100*r))
	}
	return why
}

// A Panel is every specialist, which is the only unit this question can be
// answered in.
type Panel struct {
	Model       string       `json:"model"`
	Specialists []Specialist `json:"specialists"`
}

// Mean is the average retention, which is the number this package exists to
// stop anybody quoting on its own.
func (p Panel) Mean() float64 {
	if len(p.Specialists) == 0 {
		return 0
	}
	var sum float64
	for _, s := range p.Specialists {
		sum += s.Retention()
	}
	return sum / float64(len(p.Specialists))
}

// MergedMean is the same average for the weight merged baseline.
func (p Panel) MergedMean() float64 {
	if len(p.Specialists) == 0 {
		return 0
	}
	var sum float64
	for _, s := range p.Specialists {
		sum += s.Merging()
	}
	return sum / float64(len(p.Specialists))
}

// Worst is the specialist that kept the least of its gain, and false if there
// are none. It is what the verdict quotes.
func (p Panel) Worst() (Specialist, bool) {
	if len(p.Specialists) == 0 {
		return Specialist{}, false
	}
	// Taken off the ranking rather than found again, so that a panel where two
	// specialists kept the same share names the same one in the table and in the
	// verdict.
	return p.Ranked()[0], true
}

// Ranked is the specialists worst first, since the line that decides whether
// this shipped is the bottom one.
func (p Panel) Ranked() []Specialist {
	out := slices.Clone(p.Specialists)
	slices.SortStableFunc(out, func(a, b Specialist) int {
		switch {
		case a.Retention() < b.Retention():
			return -1
		case a.Retention() > b.Retention():
			return 1
		default:
			return strings.Compare(a.Name, b.Name)
		}
	})
	return out
}

// Blocking is every reason this panel does not settle P09-2.
func (p Panel) Blocking() []string {
	var why []string
	if len(p.Specialists) == 0 {
		return []string{"no specialist was measured, so there is nothing here about what the distillation kept"}
	}
	seen := map[string]bool{}
	boxes := map[string]bool{}
	for _, s := range p.Specialists {
		if seen[s.Name] {
			why = append(why, fmt.Sprintf("%s appears twice, and two readings of one specialist are not two specialists", s.Name))
		}
		seen[s.Name] = true
		if s.Box != "" {
			boxes[s.Box] = true
		}
		why = append(why, s.Blocking()...)
	}
	if len(boxes) > 1 {
		names := make([]string, 0, len(boxes))
		for b := range boxes {
			names = append(names, b)
		}
		slices.Sort(names)
		why = append(why, fmt.Sprintf(
			"the scores came off %s, and a retention is a difference between two of them, so a panel spread across boxes is measuring the boxes",
			strings.Join(names, " and ")))
	}
	if n := len(p.Specialists); n < Specialists {
		why = append(why, fmt.Sprintf(
			"%d of the %d specialists were measured, and a distillation that kept the ones somebody got round to evaluating is a distillation that kept the ones that worked",
			n, Specialists))
	}
	return why
}

// Hides reports whether the mean retention is far enough above the worst
// specialist to be misleading on its own. It is not a fault, since a panel that
// carried six specialists and dropped one is a real result rather than a broken
// measurement, but it is the case the mean must not be quoted alone in.
func (p Panel) Hides() bool {
	w, ok := p.Worst()
	return ok && math.Abs(p.Mean()-w.Retention()) > Spread
}

// Holds reports whether P09-2 holds, which takes both halves: distillation keeps
// 90% or more of every specialist's gain, and merging keeps 70% or less. A panel
// that is not settled holds nothing, since a prediction cannot be read off a
// measurement that is still being argued with.
func (p Panel) Holds() bool {
	w, ok := p.Worst()
	return ok && p.Settled() && w.Retention() >= Recovers && p.MergedMean() <= Merges
}

// Settled reports whether the panel is worth reading a prediction off.
func (p Panel) Settled() bool { return len(p.Blocking()) == 0 }

// Verdict is the panel in one sentence, quoting the worst line rather than the
// mean.
func (p Panel) Verdict() string {
	w, ok := p.Worst()
	if !ok {
		return "nothing was distilled and nothing was measured, so P09-2 is where it started"
	}
	if why := p.Blocking(); len(why) > 0 {
		return why[0]
	}
	switch {
	case w.Retention() < Recovers && p.Hides():
		return fmt.Sprintf(
			"%s kept %.0f%% of its gain on %s against a floor of %.0f%%, and the panel averages %.0f%%, which is the arithmetic of a model that works and a model that does not",
			w.Name, 100*w.Retention(), w.Benchmark, 100*Recovers, 100*p.Mean())
	case w.Retention() < Recovers:
		return fmt.Sprintf(
			"%s kept %.0f%% of its gain on %s against a floor of %.0f%%, so the distillation is not carrying every specialist and P09-2 fails on its first half",
			w.Name, 100*w.Retention(), w.Benchmark, 100*Recovers)
	case p.MergedMean() > Merges:
		return fmt.Sprintf(
			"distillation keeps %.0f%% at worst and averaging the checkpoints keeps %.0f%% against a ceiling of %.0f%%, so seven training runs bought %.1f points over an afternoon of weight arithmetic",
			100*w.Retention(), 100*p.MergedMean(), 100*Merges, 100*(p.Mean()-p.MergedMean()))
	default:
		return fmt.Sprintf(
			"every specialist kept at least %.0f%% of its gain, worst is %s on %s, against %.0f%% for averaging the same checkpoints",
			100*w.Retention(), w.Name, w.Benchmark, 100*p.MergedMean())
	}
}

// ReadPanel loads a panel from a file of one JSON specialist per line, which is
// what an evaluation run appends to.
func ReadPanel(model, path string) (Panel, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Panel{}, fmt.Errorf("giu: %w", err)
	}
	p := Panel{Model: model}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var s Specialist
		if err := dec.Decode(&s); err != nil {
			return Panel{}, fmt.Errorf("giu: %s line %d: %w", path, i+1, err)
		}
		p.Specialists = append(p.Specialists, s)
	}
	if len(p.Specialists) == 0 {
		return Panel{}, fmt.Errorf("giu: %s holds no specialists", path)
	}
	return p, nil
}

// plural prints a count with its noun, which is the difference between a report
// somebody reads and a report somebody parses.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
