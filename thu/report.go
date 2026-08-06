package thu

// Reading the slate once the runs have come back, which is the half of this
// package that decides what gets published.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
)

// A Finding is one run read against what it was measured from.
type Finding struct {
	Run   string `json:"run"`
	Knob  string `json:"knob,omitempty"`
	Value string `json:"value,omitempty"`

	// Score is what the proxy said, Against is what the reference scored, and
	// Effect is the difference. Effect is signed, because a knob that made things
	// worse is the more useful result and rounding it toward zero is how a table
	// starts flattering itself.
	Score   float64 `json:"score"`
	Against float64 `json:"against"`
	Effect  float64 `json:"effect"`

	// Real says the effect cleared the noise floor. It is not a claim about
	// significance, which three baseline runs cannot support. It is the weaker
	// and more honest statement: this difference is larger than the difference
	// between two runs of the same recipe.
	Real bool `json:"real"`

	Decides string `json:"decides,omitempty"`
	Box     string `json:"box,omitempty"`
}

// A Report is the whole slate read at once.
type Report struct {
	Slate   doc.Hash `json:"slate"`
	Version string   `json:"version"`

	// Noise is the spread between the best and the worst run of the baseline. It
	// is the floor an effect has to clear, and it is measured rather than picked.
	Noise     float64 `json:"noise"`
	Baselines int     `json:"baselines"`

	Findings []Finding `json:"findings"`

	// Missing is every run on the slate with no result. It is a field on the
	// report rather than a silence, because a slate published with a hole in it
	// is the failure this package exists to make loud.
	Missing []string `json:"missing,omitempty"`

	// Strays are results naming runs the slate does not hold, which is what a run
	// added after the fact looks like from here.
	Strays []string `json:"strays,omitempty"`

	// Elsewhere is results produced under a different slate digest.
	Elsewhere []string `json:"elsewhere,omitempty"`

	Real int `json:"real"`
	Null int `json:"null"`

	// Spent is what the runs actually cost against what the slate budgeted.
	Spent  float64 `json:"spent"`
	Budget float64 `json:"budget"`
}

// Read reads a set of results against the slate.
//
// It does not refuse anything. Every way the results and the slate disagree
// comes back as a field on the report, so that a person or [Report.Publishable]
// can look at all of it at once rather than at whichever problem happened to be
// checked first.
func (s Slate) Read(results []Result) Report {
	digest := s.Digest()
	rep := Report{Slate: digest, Version: s.Version, Budget: s.Compute.GPUHours}

	byRun := make(map[string]Result, len(results))
	for _, r := range results {
		if _, ok := s.Lookup(r.Run); !ok {
			rep.Strays = append(rep.Strays, r.Run)
			continue
		}
		if !r.Slate.IsZero() && r.Slate != digest {
			rep.Elsewhere = append(rep.Elsewhere, r.Run)
			continue
		}
		byRun[r.Run] = r
		rep.Spent += r.GPUHours
	}

	// The noise floor first, since every effect below is read against it.
	var scores []float64
	for _, b := range s.Baselines() {
		if r, ok := byRun[b.ID]; ok {
			scores = append(scores, r.Score)
		}
	}
	rep.Baselines = len(scores)
	if len(scores) >= 2 {
		rep.Noise = slices.Max(scores) - slices.Min(scores)
	}

	for _, run := range s.Runs {
		got, ok := byRun[run.ID]
		if !ok {
			rep.Missing = append(rep.Missing, run.ID)
			continue
		}
		if run.Baseline() {
			continue
		}
		f := Finding{
			Run: run.ID, Knob: run.Knob, Value: run.Value,
			Score: got.Score, Decides: run.Decides, Box: got.Box,
		}
		if ref, ok := byRun[run.Against]; ok {
			f.Against = ref.Score
			f.Effect = got.Score - ref.Score
			f.Real = math.Abs(f.Effect) > rep.Noise && rep.Noise > 0
		}
		if f.Real {
			rep.Real++
		} else {
			rep.Null++
		}
		rep.Findings = append(rep.Findings, f)
	}
	slices.SortStableFunc(rep.Findings, func(a, b Finding) int {
		return -cmpFloat(math.Abs(a.Effect), math.Abs(b.Effect))
	})
	return rep
}

// Publishable is every reason this report does not go out as it stands.
func (r Report) Publishable() []string {
	var out []string
	if len(r.Missing) > 0 {
		out = append(out, fmt.Sprintf("%s came back and %s did not, and a slate published with the runs that finished is an advertisement rather than a comparison: %s",
			plural(len(r.Findings)+r.Baselines, "run"), plural(len(r.Missing), "run"), strings.Join(r.Missing, ", ")))
	}
	if r.Baselines < Repeats {
		out = append(out, fmt.Sprintf("the results carry %s and %d is the floor, so there is no measured gap between two runs of the same recipe and every effect here is a difference against a number with no error bar under it",
			plural(r.Baselines, "baseline run"), Repeats))
	}
	if r.Noise == 0 && r.Baselines >= 2 {
		out = append(out, "the baseline runs scored identically, which is not what different seeds do, so either the seed is not reaching the run or the same result was written down more than once")
	}
	if len(r.Findings) > 0 && r.Null == 0 {
		out = append(out, fmt.Sprintf("every one of %s cleared the noise floor, and a slate where nothing failed to move the number is not a slate that swept anything, it is one that has had its null results taken out",
			plural(len(r.Findings), "run")))
	}
	for _, f := range r.Findings {
		if f.Box == "" {
			out = append(out, fmt.Sprintf("%s does not say what it ran on, and a training result without the hardware under it is a number nobody can price or reproduce", f.Run))
		}
	}
	if len(r.Strays) > 0 {
		out = append(out, fmt.Sprintf("%s: %s, which is what a run added after the slate was closed looks like from here",
			plural(len(r.Strays), "result names a run the slate does not hold"), strings.Join(r.Strays, ", ")))
	}
	if len(r.Elsewhere) > 0 {
		out = append(out, fmt.Sprintf("%s was produced under a different slate: %s, so something on the slate moved after the runs started",
			plural(len(r.Elsewhere), "result"), strings.Join(r.Elsewhere, ", ")))
	}
	if r.Budget > 0 && r.Spent > r.Budget {
		out = append(out, fmt.Sprintf("the slate was priced at %.0f GPU hours and the runs spent %.0f, which is the kind of thing somebody should hear from the report rather than from the invoice",
			r.Budget, r.Spent))
	}
	return out
}

// Settled is every threshold the slate set, with the run that set it. A run that
// was written down as decisive and came back null settled its question in the
// other direction, which is a result and is reported as one.
func (r Report) Settled() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Decides != "" {
			out = append(out, f)
		}
	}
	return out
}

// ReadSlate reads a slate from JSON, for checking one that is not the fixed one.
func ReadSlate(path string) (Slate, error) {
	f, err := os.Open(path)
	if err != nil {
		return Slate{}, err
	}
	defer func() { _ = f.Close() }()

	var s Slate
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return Slate{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// ReadResults reads one JSON result per line, which is what forty runs finishing
// over a fortnight actually produce.
func ReadResults(path string) ([]Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Result
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Result
		d := json.NewDecoder(strings.NewReader(line))
		d.DisallowUnknownFields()
		if err := d.Decode(&r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %w: it holds no results", path, ErrBadResults)
	}
	return out, nil
}

// cmpFloat orders two floats, with the larger first when negated by the caller.
func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
