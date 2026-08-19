package nau

// Reconciling the budget against the curriculum.
//
// The budget says what the run buys and the curriculum says what it spends, and
// they are written by different arguments. The budget comes from what exists,
// how many times it is safe to read, and what an anchor language is worth. The
// curriculum comes from what a model needs early against what it needs late.
// Nothing makes the two agree except somebody multiplying them out, which is
// what this file does, and they do not agree today.
//
// A disagreement is not a bug in the code, it is a decision nobody has made
// yet, so it is recorded as a question with the component it is about. What
// `gao cook check` enforces is that every gap is written down: a component the
// curriculum spends at a different rate than the budget buys it, with nobody
// having said which of the two moves, fails. So does a question about a gap
// that has since closed, because a register of open questions that still lists
// settled ones is a register nobody reads.

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Tolerance is how far apart, in points of the whole run, the budget and the
// curriculum may be on one component without somebody having to say why.
//
// A point of a trillion token run is ten billion tokens, which is three weeks
// of the crawl or the entire legal corpus twice over. It is not a rounding
// error and the tolerance is not generous.
const Tolerance = 1.0

// scheduled maps a line of the curriculum onto the budget components it spends.
// The curriculum groups the two smallest Vietnamese slices on one line, because
// scheduling four billion tokens of statute separately from eight billion of
// transcribed speech is a precision the schedule does not have.
var scheduled = map[string][]string{
	"gao-legal+gao-voice": {"gao-legal", "gao-voice"},
}

// A Gap is one component's disagreement between the two tables.
type Gap struct {
	// Component is the budget line.
	Component string
	// Buys is the share of the run the budget holds for it, as a percentage.
	Buys float64
	// Spends is the share the curriculum gives it, as a percentage.
	Spends float64
	// Epochs is how many passes over this component's own text the curriculum's
	// line for it implies, which is the form the gao-web gap is easiest to argue
	// about in. For an extra pass line it is passes on top of whatever the
	// component it sits inside already gives, the same as the budget's column.
	Epochs float64
}

// Off is how far apart the two are, in points of the run.
func (g Gap) Off() float64 { return g.Spends - g.Buys }

// Reconcile multiplies the curriculum out and compares it to the budget, worst
// disagreement first.
func Reconcile() []Gap {
	spend := map[string]float64{}
	for _, p := range Curriculum() {
		for _, s := range p.Mix {
			// A grouped line is split in proportion to how much of each
			// component the budget holds, since that is what reading the group
			// at one rate actually does.
			names := spends(s.Component)
			var whole int64
			for _, name := range names {
				whole += instancesOf(name)
			}
			for _, name := range names {
				share := float64(instancesOf(name)) / float64(whole)
				spend[name] += p.Share * s.Percent / 100 * share
			}
		}
	}

	out := make([]Gap, 0, len(Budget()))
	for _, c := range Budget() {
		g := Gap{Component: c.Name, Buys: Share(c.Name), Spends: spend[c.Name]}
		g.Epochs = g.Spends / 100 * float64(Instances()) / float64(c.Unique)
		out = append(out, g)
	}
	slices.SortStableFunc(out, func(a, b Gap) int {
		return int(math.Abs(b.Off())*1000) - int(math.Abs(a.Off())*1000)
	})
	return out
}

// instancesOf is what the budget spends on one component, and zero for a name
// the budget does not hold, which checkCurriculum reports separately.
func instancesOf(name string) int64 {
	c, _ := component(name)
	return c.Instances()
}

// spends resolves a curriculum line to the budget components under it.
func spends(line string) []string {
	if names, ok := scheduled[line]; ok {
		return names
	}
	return []string{line}
}

// A Question is a disagreement somebody has to settle before the run starts.
//
// It names the component and says which way the answer probably goes, and it
// deliberately does not carry the numbers. A question with the numbers typed
// into it is a question that is wrong the first time either table is edited,
// and the numbers are one multiplication away.
type Question struct {
	ID        string
	Component string
	Ask       string
}

// Questions is what has to be settled before the mixture is locked.
//
// Every one of them came out of the multiplication rather than out of a review.
// The tables were both written by hand, both internally consistent, and neither
// one has ever been true at the same time as the other.
func Questions() []Question {
	return []Question{
		{"Q08-1", "gao-web", "the curriculum reads the general web slice well over once, and the budget buys it once. Either phase 1 leans on the web harder than the corpus can support, or gao-web is repeated, and repeating the lowest quality tier in the mixture is the last place a repetition budget should go"},
		{"Q08-2", "english", "the budget buys more English than the curriculum spends. Either the anchor share comes down, which is a claim about how much reasoning transfer costs, or English rises in phase 1, which is the phase where the model is learning Vietnamese"},
		{"Q08-3", "gao-edu", "the budget pays for four passes over the educational slice and the curriculum reads it fewer times than that. The passes are the expensive part of the natural Vietnamese line, so this is the arithmetic behind the whole repetition argument"},
		{"Q08-4", "gao-web-hq", "same shape as the educational slice, and the two move together, because both are extra passes over text that is already in gao-web"},
		{"Q08-5", "math", "the budget buys mathematics and reasoning traces and the curriculum only reads them in the anneal. Either the budget line comes down or the traces start earlier than the last tenth"},
		{"Q08-6", "gao-synth", "synthesis is fourteen thousand GPU-hours, so a component the curriculum underspends is compute already bought and not used"},
		{"Q08-7", "chinese", "the anchor closest to Vietnamese is read only in phase 1, which spends less of it than the budget holds"},
		{"Q08-8", "vi-translated", "machine translated Vietnamese has a budget line and no place in any phase. Either it is scheduled or the line goes, and a labeled component nobody reads is the easier of the two to delete"},
	}
}

// Check reports everything about the plan that cannot be true at once. An empty
// result means the arithmetic holds and every disagreement between the two
// tables is one somebody has written down.
func Check() []string {
	out := checkBudget()
	out = append(out, checkCurriculum()...)
	out = append(out, checkQuestions()...)
	return out
}

func checkBudget() []string {
	var out []string
	for _, c := range Budget() {
		if c.Unique <= 0 {
			out = append(out, fmt.Sprintf("%s has no tokens in it", c.Name))
		}
		if c.Epochs < 1 {
			out = append(out, fmt.Sprintf("%s is read %.1f times, and a component read less than once is a component with a smaller unique count", c.Name, c.Epochs))
		}
		if c.Passes() > 4 {
			out = append(out, fmt.Sprintf("%s is read %.1f times counting the passes it gets from %s, past the four where repetition stops being nearly as good as fresh text", c.Name, c.Passes(), c.Within))
		}
		if c.Why == "" {
			out = append(out, fmt.Sprintf("%s is in the mixture with no argument for being there", c.Name))
		}
		if c.Within != "" {
			o, ok := component(c.Within)
			switch {
			case !ok:
				out = append(out, fmt.Sprintf("%s is extra passes over %q, which is not in the budget", c.Name, c.Within))
			case c.Unique > o.Unique:
				out = append(out, fmt.Sprintf("%s holds more text than the %s it is a slice of", c.Name, c.Within))
			}
		}
	}
	if v := VietnameseShare(); math.Round(v) != 66 {
		out = append(out, fmt.Sprintf("the mixture is %.1f%% Vietnamese, and the plan argues for 66", v))
	}
	if a := KindShare(Anchor); math.Round(a) != 34 {
		out = append(out, fmt.Sprintf("the anchors are %.1f%% of the mixture, and the plan argues for 34", a))
	}
	if e := NaturalEpochs(); math.Abs(e-1.7) > 0.05 {
		out = append(out, fmt.Sprintf("natural Vietnamese is read %.2f times on average, and the plan says 1.7", e))
	}
	return out
}

func checkCurriculum() []string {
	var out []string
	var share float64
	last := 0
	for _, p := range Curriculum() {
		share += p.Share
		var mix float64
		for _, s := range p.Mix {
			mix += s.Percent
			if !known(s.Component) {
				out = append(out, fmt.Sprintf("phase %s reads %q, which is not in the budget", p.Name, s.Component))
			}
		}
		if math.Abs(mix-100) > 0.05 {
			out = append(out, fmt.Sprintf("phase %s reads %.1f%% of itself", p.Name, mix))
		}
		if p.Sequence <= last {
			out = append(out, fmt.Sprintf("phase %s trains at %d, which is not longer than the phase before it", p.Name, p.Sequence))
		}
		last = p.Sequence
	}
	if math.Abs(share-100) > 0.05 {
		out = append(out, fmt.Sprintf("the phases are %.1f%% of the run", share))
	}
	return out
}

// known reports whether a curriculum line resolves to budget components.
func known(line string) bool {
	for _, name := range spends(line) {
		found := false
		for _, c := range Budget() {
			if c.Name == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func checkQuestions() []string {
	var out []string
	asked := map[string]string{}
	for _, q := range Questions() {
		if _, dup := asked[q.Component]; dup {
			out = append(out, fmt.Sprintf("%s asks about %s twice", q.ID, q.Component))
		}
		asked[q.Component] = q.ID
	}

	open := map[string]bool{}
	for _, g := range Reconcile() {
		if math.Abs(g.Off()) > Tolerance || g.Spends == 0 {
			open[g.Component] = true
			if _, ok := asked[g.Component]; !ok {
				out = append(out, fmt.Sprintf("the budget buys %.1f%% of the run as %s and the curriculum spends %.1f%%, and nobody has written down which one moves", g.Buys, g.Component, g.Spends))
			}
		}
	}
	for _, q := range Questions() {
		if !open[q.Component] {
			out = append(out, fmt.Sprintf("%s is still asked about %s, and the two tables agree about it now", q.ID, q.Component))
		}
	}
	return out
}

// Report renders the reconciliation the way it is printed and read.
func Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-14s %8s %8s %8s %8s\n", "component", "buys", "spends", "off", "epochs")
	for _, g := range Reconcile() {
		fmt.Fprintf(&b, "%-14s %7.1f%% %7.1f%% %+7.1f %8.2f\n", g.Component, g.Buys, g.Spends, g.Off(), g.Epochs)
	}
	return b.String()
}
