// Package board is the release scoreboard, with the native benchmarks kept apart
// from the translated ones.
//
// Bảng is the board. What goes on it at the end of post-training is a row per
// benchmark and a number, and the thing that decides whether those numbers mean
// what a release note says they mean is not any one of them. It is whether they
// were added together.
//
// # Why one average is the wrong number
//
// Of the twenty four benchmarks on nhat's roster, sixteen are Vietnamese
// written by Vietnamese speakers, six are English benchmarks translated into
// Vietnamese, and two are code. A model that reads translated English well
// scores well on the middle six, and translated English is exactly the register
// this milestone exists to avoid training the model to write. So an average
// across the whole board pays the model for the failure the slice is trying to
// prevent, and pays it in the same units it pays for the thing it wants.
//
// The two arms are therefore never summed. The verdict names both, in that
// order, and the gap between them is a reported number rather than something a
// reader has to work out from a table. A model that is four points ahead of the
// baseline on the translated arm and half a point ahead on the native one has
// not learned Vietnamese, it has learned the other thing, and the single figure
// that would have been published says it went well.
//
// # Why the benchmarks gao built are separated too
//
// Six of the entries on the roster are gao's own: vi-cloze, vi-diacritic,
// vi-needle, vi-longdoc-qa, vi-overrefusal and vi-adherence exist because
// nothing measured those capabilities in Vietnamese before. They are also the
// benchmarks whose design the model's own authors chose, and a suite where the
// builder's benchmarks carry the claim is a suite that has to say so out loud
// rather than in a footnote. So the margin over the baseline on gao's own
// benchmarks and the margin on everybody else's are separate numbers, and the
// board reports the first as a fault when it runs well ahead of the second.
//
// # Why a row nobody can run again is still reported
//
// Nine of the twenty four entries have no pinned revision yet, for reasons the
// roster spells out one by one. A score taken on one of those is a real
// measurement of something, but nobody can take it again against what it was
// taken on, so it is carried as a fault on the board rather than thrown away or
// waved through. Today that means gao cannot publish a scoreboard, and the
// reason is a list of nine names rather than a shrug.
//
// # Why a win under the noise floor is not a win
//
// Every score carries how many runs it is over and what those runs spread
// across, because two runs of the same model on the same benchmark differ, and
// a margin narrower than that difference is a coin flip somebody called. Those
// rows are named and counted rather than added to the tally. It is the single
// most common way a table of small positive numbers becomes a claim.
package board

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tamnd/gao/pick"
)

const (
	// MinNative is how many native benchmarks a claim about Vietnamese ability
	// has to rest on. Two of them are a pair of numbers, not an arm.
	MinNative = 6

	// MaxTranslationEdge is how far the translated arm's margin may run ahead of
	// the native arm's before the board says the model was scored on the wrong
	// language. It is in benchmark points.
	MaxTranslationEdge = 3.0

	// MaxOwnEdge is how far the margin on the benchmarks gao built itself may
	// run ahead of the margin on everybody else's. Past it, the claim is being
	// carried by instruments the claimant designed.
	MaxOwnEdge = 3.0
)

// A Score is one benchmark, run.
type Score struct {
	// Benchmark is the roster name. A score for anything not on the roster is
	// refused rather than shown, since the roster is what decides which arm a
	// number belongs in and an unlisted benchmark has no arm.
	Benchmark string `json:"benchmark"`

	// Score is what the release model got and Baseline is what the best open
	// Vietnamese model got on the same revision of the same benchmark. Both are
	// on the benchmark's own scale, which is points out of a hundred everywhere
	// on this roster.
	Score    float64 `json:"score"`
	Baseline float64 `json:"baseline"`

	// Against names the model the baseline came off, because a margin over an
	// unnamed baseline is a margin over nothing.
	Against string `json:"against"`

	// Runs is how many times the benchmark was run and Spread is the range
	// those runs covered. One run has a spread of zero and says so, which is
	// different from a spread nobody measured.
	Runs   int     `json:"runs"`
	Spread float64 `json:"spread"`

	// Box is the fleet label it was run on and On is when.
	Box string `json:"box"`
	On  string `json:"measured_on"`
}

// Margin is the release model against the baseline, in benchmark points.
func (s Score) Margin() float64 { return s.Score - s.Baseline }

// Decided says whether the margin is wider than two runs of the same model
// differ by. A margin inside the spread is a coin flip that landed, and it is
// not evidence of anything whichever way it landed.
func (s Score) Decided() bool { return abs(s.Margin()) > s.Spread }

// An Arm is the benchmarks of one origin, scored.
type Arm struct {
	Origin  string  `json:"origin"`
	Scores  []Score `json:"scores"`
	Mean    float64 `json:"mean"`
	Margin  float64 `json:"margin"`
	Decided int     `json:"decided"`
}

// A Board is the whole suite, read.
type Board struct {
	Roster string `json:"roster"`
	Arms   []Arm  `json:"arms"`

	// Own is the margin over the benchmarks gao built and Others is the margin
	// over the ones it did not. They are the same measurement over two disjoint
	// halves of the board, which is the only way to see the builder's thumb.
	Own    float64 `json:"own"`
	Others float64 `json:"others"`

	// Missing is every benchmark on the roster with no score. A suite reported
	// over the benchmarks that happened to run is a suite chosen after the
	// results.
	Missing []string `json:"missing,omitempty"`

	// Coin is every row whose margin is inside its own noise.
	Coin []string `json:"coin,omitempty"`

	// Unpinned is every benchmark that was scored but has no pinned revision on
	// the roster, so the number cannot be taken again against what it was taken
	// on.
	Unpinned []string `json:"unpinned,omitempty"`

	roster pick.Roster
	scores []Score
}

// Read reads a set of scores against a roster.
//
// The roster is passed in rather than reached for, because it is the thing that
// decides which arm every number lands in, and a board that fetches its own
// roster is a board that changes what it said when somebody adds a benchmark.
func Read(ros pick.Roster, scores []Score) Board {
	b := Board{Roster: ros.Version, roster: ros, scores: scores}

	by := map[string]Score{}
	for _, s := range scores {
		by[s.Benchmark] = s
	}

	byOrigin := map[string][]Score{}
	var own, others []Score
	for _, e := range ros.Benchmarks {
		s, ok := by[e.Name]
		if !ok {
			b.Missing = append(b.Missing, e.Name)
			continue
		}
		byOrigin[e.Origin] = append(byOrigin[e.Origin], s)
		if Built(e.Name) {
			own = append(own, s)
		} else {
			others = append(others, s)
		}
		if e.Version == pick.Unpinned {
			b.Unpinned = append(b.Unpinned, e.Name)
		}
		if !s.Decided() {
			b.Coin = append(b.Coin, fmt.Sprintf("%s is %s and two runs of it differ by %s",
				e.Name, margin(s.Margin()), points(s.Spread)))
		}
	}

	for _, origin := range Origins() {
		got := byOrigin[origin]
		if len(got) == 0 {
			continue
		}
		arm := Arm{Origin: origin, Scores: got}
		for _, s := range got {
			arm.Mean += s.Score
			arm.Margin += s.Margin()
			if s.Decided() {
				arm.Decided++
			}
		}
		arm.Mean /= float64(len(got))
		arm.Margin /= float64(len(got))
		b.Arms = append(b.Arms, arm)
	}
	b.Own = mean(own)
	b.Others = mean(others)
	return b
}

// Origins is the three arms in the order a board prints them, which is native
// first because it is the one the claim is about.
func Origins() []string { return []string{pick.Native, pick.Translated, pick.Neutral} }

// Built says whether the benchmark is one gao made. Every one of them is named
// for the project and nothing else on the roster is, so the test is the name
// rather than a second column that could disagree with it.
func Built(name string) bool { return strings.HasPrefix(name, "vi-") }

// Arm returns the arm of the given origin.
func (b Board) Arm(origin string) (Arm, bool) {
	for _, a := range b.Arms {
		if a.Origin == origin {
			return a, true
		}
	}
	return Arm{}, false
}

// Edge is how far the translated arm's margin runs ahead of the native arm's,
// which is the number that says whether the model was scored on Vietnamese or
// on translated English.
func (b Board) Edge() float64 {
	native, ok := b.Arm(pick.Native)
	translated, also := b.Arm(pick.Translated)
	if !ok || !also {
		return 0
	}
	return translated.Margin - native.Margin
}

// Blocking is everything that stops this from being a scoreboard. Nothing below
// it is read while there is anything here.
func (b Board) Blocking() []string {
	known := map[string]bool{}
	for _, e := range b.roster.Benchmarks {
		known[e.Name] = true
	}

	var why []string
	if len(b.scores) == 0 {
		why = append(why, "no scores were given, and a board with nothing on it is not a result")
	}
	seen := map[string]bool{}
	for _, s := range b.scores {
		switch {
		case !known[s.Benchmark]:
			why = append(why, fmt.Sprintf("%s is not on the roster, so there is no arm to put it in and no revision it was scored against", s.Benchmark))
			continue
		case seen[s.Benchmark]:
			why = append(why, fmt.Sprintf("%s is scored twice, and two numbers for one benchmark is a choice somebody has to make out loud", s.Benchmark))
			continue
		}
		seen[s.Benchmark] = true
		switch {
		case s.Score < 0 || s.Score > 100 || s.Baseline < 0 || s.Baseline > 100:
			why = append(why, fmt.Sprintf("%s scores %.1f against %.1f, and this roster is points out of a hundred", s.Benchmark, s.Score, s.Baseline))
		case s.Runs < 1:
			why = append(why, fmt.Sprintf("%s says it was run %d times", s.Benchmark, s.Runs))
		case s.Against == "":
			why = append(why, fmt.Sprintf("%s does not name the model its baseline came off, and a margin over an unnamed baseline is a margin over nothing", s.Benchmark))
		}
	}
	if len(b.Missing) > 0 {
		why = append(why, fmt.Sprintf("%s on the roster went unscored, starting with %s, and a suite reported over the benchmarks that happened to run is a suite chosen after the results",
			plural(len(b.Missing), "benchmark"), b.Missing[0]))
	}
	return why
}

// Faults are the things about this board that have to be said wherever it is
// published.
func (b Board) Faults() []string {
	if len(b.Blocking()) > 0 {
		return nil
	}
	var out []string

	native, ok := b.Arm(pick.Native)
	if !ok || len(native.Scores) < MinNative {
		out = append(out, fmt.Sprintf("the native arm is %s, and a claim about Vietnamese ability needs at least %d",
			plural(len(native.Scores), "benchmark"), MinNative))
	}
	if edge := b.Edge(); edge > MaxTranslationEdge {
		translated, _ := b.Arm(pick.Translated)
		out = append(out, fmt.Sprintf("the translated arm is %s of the baseline and the native arm is %s, a gap of %s, which is a model that reads translated English rather than one that writes Vietnamese",
			margin(translated.Margin), margin(native.Margin), points(edge)))
	}
	if edge := b.Own - b.Others; edge > MaxOwnEdge {
		out = append(out, fmt.Sprintf("the benchmarks gao built put the model %s and the ones it did not put it %s, so %s of the claim rests on instruments its own authors designed",
			margin(b.Own), margin(b.Others), points(edge)))
	}
	switch n := len(b.Unpinned); {
	case n == 1:
		out = append(out, fmt.Sprintf("%s has no pinned revision on the roster, so that number cannot be taken again against what it was taken on", b.Unpinned[0]))
	case n > 1:
		out = append(out, fmt.Sprintf("%d rows were scored on benchmarks with no pinned revision, starting with %s, so those numbers cannot be taken again against what they were taken on",
			n, b.Unpinned[0]))
	}
	switch n := len(b.Coin); {
	case n == 1:
		out = append(out, fmt.Sprintf("one row is inside its own noise, since %s", b.Coin[0]))
	case n > 1:
		out = append(out, fmt.Sprintf("%d rows are inside their own noise, the first of which is that %s", n, b.Coin[0]))
	}
	return out
}

// Holds says whether the board is one to publish as it stands.
func (b Board) Holds() bool { return len(b.Blocking()) == 0 && len(b.Faults()) == 0 }

// Verdict is the sentence that goes in the release note. It never quotes one
// average, because there is no one average to quote.
func (b Board) Verdict() string {
	if why := b.Blocking(); len(why) > 0 {
		return "This is not a scoreboard: " + why[0] + "."
	}
	native, _ := b.Arm(pick.Native)
	translated, hasTranslated := b.Arm(pick.Translated)

	head := fmt.Sprintf("On %s written in Vietnamese the model scores %.1f, %s of %s.",
		plural(len(native.Scores), "benchmark"), native.Mean, margin(native.Margin), against(native.Scores))
	if hasTranslated {
		head += fmt.Sprintf(" On %s translated into it, %.1f and %s.",
			plural(len(translated.Scores), "benchmark"), translated.Mean, margin(translated.Margin))
	}
	if faults := b.Faults(); len(faults) > 0 {
		if len(faults) == 1 {
			return head + " One reading says the board cannot be published as it stands: " + faults[0] + "."
		}
		return fmt.Sprintf("%s %d readings say the board cannot be published as it stands, the first of which is that %s.", head, len(faults), faults[0])
	}
	return head + " The two arms are not added together, and the gap between them is " + points(b.Edge()) + "."
}

// against is the baseline every row was taken against, or a sentence saying the
// rows disagree about it, since a suite scored against two different models is
// two suites.
func against(scores []Score) string {
	var names []string
	seen := map[string]bool{}
	for _, s := range scores {
		if !seen[s.Against] {
			seen[s.Against] = true
			names = append(names, s.Against)
		}
	}
	sort.Strings(names)
	switch len(names) {
	case 0:
		return "nothing"
	case 1:
		return names[0]
	}
	return fmt.Sprintf("%s different baselines", itoa(len(names)))
}

// mean is the average margin over a set of rows, and zero over none.
func mean(scores []Score) float64 {
	if len(scores) == 0 {
		return 0
	}
	var sum float64
	for _, s := range scores {
		sum += s.Margin()
	}
	return sum / float64(len(scores))
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
