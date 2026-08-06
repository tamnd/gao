package chot

// Checking a set of results against the harness it claims to have been produced
// under.

import (
	"fmt"
	"sort"

	"github.com/tamnd/gao/doc"
)

// A Result is one arm's numbers.
type Result struct {
	// Harness is the digest of the harness the run was scored under. A result
	// with no digest is not a result this can check, and it is rejected rather
	// than assumed to be fine, because the assumption is the failure.
	Harness doc.Hash `json:"harness"`

	Arm string `json:"arm"`

	// Scores is the benchmark name to the number, and a benchmark that was run
	// and lost is in here with its number. Leaving it out is the fault this
	// whole package exists to catch.
	Scores map[string]float64 `json:"scores"`

	// Note is anything the run wants to say for itself. It is not checked.
	Note string `json:"note,omitempty"`
}

// An Audit is what the harness says about a set of results.
type Audit struct {
	// Harness is the digest the results should carry.
	Harness doc.Hash `json:"harness"`

	Version string `json:"version"`

	Arms  int `json:"arms"`
	Tasks int `json:"tasks"`

	// Reported is how many arm and task pairs came back with a number, out of
	// the arms times tasks that were promised.
	Reported int `json:"reported"`
	Promised int `json:"promised"`

	// Faults is every way the results and the harness disagree, in a fixed
	// order so that two runs of this produce the same report.
	Faults []string `json:"faults,omitempty"`
}

// OK reports whether the results are the ones the harness asked for.
func (a Audit) OK() bool { return len(a.Faults) == 0 }

// Audit checks results against the harness.
//
// It fails a missing number exactly as loudly as an extra one. Adding a
// benchmark after seeing the numbers is the fault everybody names, and dropping
// one is the same fault in the other direction, done more often, and easier to
// explain away as a run that did not finish.
func (h Harness) Audit(results []Result) Audit {
	digest := h.Digest()
	a := Audit{
		Harness:  digest,
		Version:  h.Version,
		Arms:     len(h.Arms),
		Tasks:    len(h.Tasks),
		Promised: len(h.Arms) * len(h.Tasks),
	}

	seen := map[string]bool{}
	for _, r := range results {
		switch {
		case r.Arm == "":
			a.Faults = append(a.Faults, "a result with no arm on it")
			continue
		case seen[r.Arm]:
			a.Faults = append(a.Faults, fmt.Sprintf("%s reported twice", r.Arm))
			continue
		}
		seen[r.Arm] = true

		if !h.Has(r.Arm) {
			a.Faults = append(a.Faults, fmt.Sprintf("%s is not an arm this harness names, so its numbers are not part of this comparison", r.Arm))
			continue
		}
		if r.Harness.IsZero() {
			a.Faults = append(a.Faults, fmt.Sprintf("%s carries no harness digest, so there is nothing tying its numbers to a measurement", r.Arm))
		} else if r.Harness != digest {
			a.Faults = append(a.Faults, fmt.Sprintf("%s was scored under harness %s and this is harness %s, so the two sets of numbers are not comparable",
				r.Arm, short(r.Harness), short(digest)))
		}

		for _, t := range h.Tasks {
			score, ok := r.Scores[t.Benchmark]
			if !ok {
				a.Faults = append(a.Faults, fmt.Sprintf("%s reported nothing for %s, and a benchmark that was on the harness before the run does not come off it after",
					r.Arm, t.Benchmark))
				continue
			}
			a.Reported++
			if score < 0 || score > 1 {
				a.Faults = append(a.Faults, fmt.Sprintf("%s scored %v on %s, and every metric on this harness is a rate between zero and one",
					r.Arm, score, t.Benchmark))
			}
		}

		for _, name := range sorted(r.Scores) {
			if _, ok := h.Task(name); !ok {
				a.Faults = append(a.Faults, fmt.Sprintf("%s reported %s, which the harness does not hold, and a benchmark that arrives with the results arrived after them",
					r.Arm, name))
			}
		}
	}

	for _, arm := range h.Arms {
		if !seen[arm] {
			a.Faults = append(a.Faults, fmt.Sprintf("%s reported nothing at all, and an arm named before the run is in the table whatever it scored", arm))
		}
	}
	return a
}

// Table is the results laid out as the harness reads them: one row per task, one
// column per arm, in the harness's own order rather than in the order the
// results happened to arrive. A missing number is missing rather than zero,
// which is why this is a pointer.
func (h Harness) Table(results []Result) [][]*float64 {
	by := map[string]Result{}
	for _, r := range results {
		by[r.Arm] = r
	}
	rows := make([][]*float64, 0, len(h.Tasks))
	for _, t := range h.Tasks {
		row := make([]*float64, len(h.Arms))
		for i, arm := range h.Arms {
			if s, ok := by[arm].Scores[t.Benchmark]; ok {
				row[i] = &s
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// Winner is the arm with the best number on a task, and whether there is one.
// A tie has no winner, because two arms that scored the same did not produce a
// result worth writing a sentence about.
func (h Harness) Winner(task Task, results []Result) (string, bool) {
	best, found, tied := "", false, false
	var bestScore float64
	for _, arm := range h.Arms {
		for _, r := range results {
			if r.Arm != arm {
				continue
			}
			s, ok := r.Scores[task.Benchmark]
			if !ok {
				continue
			}
			switch {
			case !found:
				best, bestScore, found = arm, s, true
			case Better(task.Metric, s, bestScore):
				best, bestScore, tied = arm, s, false
			case s == bestScore:
				tied = true
			}
		}
	}
	if !found || tied {
		return "", false
	}
	return best, true
}

func sorted(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func short(h doc.Hash) string { return h.String()[:12] }
