package efficiency

// A run, read back step by step.
//
// The checklist says utilization is measured continuously and reported, not
// estimated once, and the difference between those is not diligence. A run that
// starts at 45% and ends at 22% averages 34%, which is above the line the
// architecture would be changed at and is a run that is dying. Utilization
// drifts for reasons that all arrive gradually: the sequence length extends, the
// routing goes imbalanced and a quarter of the experts take most of the tokens,
// a node degrades and the whole step waits on it at every all-reduce. Averaging
// over the run is exactly the operation that hides all three.
//
// So the reader keeps the steps, reports the distribution rather than a mean,
// and writes the verdict against the worst sustained window. A run is judged on
// what it is doing now, not on what it did in the first hour when the sequence
// was short and nothing had degraded yet.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

// A Step is one optimizer step as the trainer wrote it down.
//
// Instance and Precision are on every step rather than in a header, because the
// case worth catching is a run that moved. A job that restarted onto different
// hardware after a preemption and carried on reporting against the old peak is
// not a hypothetical, it is what spot instances do, and the same milestone that
// asks for continuous utilization also asks for spot handling that survives
// preemption.
type Step struct {
	Step      int       `json:"step"`
	Tokens    int64     `json:"tokens"`
	Seconds   float64   `json:"seconds"`
	GPUs      int       `json:"gpus"`
	Instance  string    `json:"instance"`
	Precision Precision `json:"precision"`
	Seq       int       `json:"seq"`
	At        time.Time `json:"at,omitzero"`

	// Loss is carried through untouched. It is not part of the arithmetic and it
	// is the first thing anybody looks at next to a utilization drop, so a reader
	// that dropped it would send them back to a second file.
	Loss float64 `json:"loss,omitempty"`
}

// A Run is a training log read back against a model.
type Run struct {
	Model Model
	Steps []Step

	// MFUs is the utilization of each step, in the order the steps came, which is
	// the series a drift is visible in and a mean is not.
	MFUs []float64

	// Faults is what the log says went wrong, one sentence each, naming the step.
	Faults []string
}

// ReadLog reads a training log, one step per line.
//
// An unknown field is an error rather than something to skip. The trainer and
// this reader are the same project, and a field one of them does not know about
// means they have drifted apart, which is worth finding out before a month of
// GPU time is behind it.
func ReadLog(path string) ([]Step, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hieu: %w", err)
	}
	var out []Step
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var s Step
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("hieu: %s line %d: %w", path, i+1, err)
		}
		if s.Tokens <= 0 {
			return nil, fmt.Errorf("hieu: %s line %d: a step with no tokens in it, and a training log is a record of tokens", path, i+1)
		}
		out = append(out, s)
	}
	return out, nil
}

// Read folds a log into a run.
//
// It refuses nothing and returns everything. A log with a bad step in it is a
// log whose other steps are still the only record of what the hardware did, and
// a reader that stopped at the first problem would be a reader nobody could use
// at the hour it mattered.
func Read(m Model, steps []Step) Run {
	r := Run{Model: m, Steps: steps, MFUs: make([]float64, 0, len(steps))}

	seen := map[string]bool{}
	for _, s := range steps {
		i, ok := Lookup(s.Instance)
		reading := Reading{Model: m, Instance: i, GPUs: s.GPUs, Precision: s.Precision, Seq: s.Seq, Tokens: s.Tokens, Seconds: s.Seconds}
		if !ok {
			if s.Instance == "" {
				r.Faults = append(r.Faults, fmt.Sprintf("step %d does not say what hardware it ran on, and a utilization figure without hardware is not a number", s.Step))
			} else {
				r.Faults = append(r.Faults, fmt.Sprintf("step %d ran on %s, which is not hardware this project has priced, so there is no peak to divide by", s.Step, s.Instance))
			}
			r.MFUs = append(r.MFUs, 0)
			continue
		}
		if why := reading.Blocking(); len(why) > 0 {
			r.Faults = append(r.Faults, fmt.Sprintf("step %d cannot be read: %s", s.Step, why[0]))
			r.MFUs = append(r.MFUs, 0)
			continue
		}
		seen[s.Instance+" at "+string(s.Precision)] = true
		r.MFUs = append(r.MFUs, reading.MFU())
	}

	if len(seen) > 1 {
		names := make([]string, 0, len(seen))
		for k := range seen {
			names = append(names, k)
		}
		sort.Strings(names)
		r.Faults = append(r.Faults, fmt.Sprintf("the run moved between %s, so one utilization figure over the whole log is an average of two different machines",
			strings.Join(names, " and ")))
	}
	return r
}

// Tokens is what the run got through.
func (r Run) Tokens() int64 {
	var n int64
	for _, s := range r.Steps {
		n += s.Tokens
	}
	return n
}

// Seconds is how long it took, which is the sum of the steps rather than the
// wall clock, since the gap between two steps is where a preemption sits.
func (r Run) Seconds() float64 {
	var f float64
	for _, s := range r.Steps {
		f += s.Seconds
	}
	return f
}

// Mean is the utilization over the whole run.
//
// It is here because it is the number everybody asks for and it is not the
// number the verdict is written against, which is a distinction worth making by
// having both.
func (r Run) Mean() float64 {
	var sum float64
	var n int
	for _, f := range r.MFUs {
		if f > 0 {
			sum += f
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// Quantile is the utilization at this point in the sorted distribution.
func (r Run) Quantile(q float64) float64 {
	got := make([]float64, 0, len(r.MFUs))
	for _, f := range r.MFUs {
		if f > 0 {
			got = append(got, f)
		}
	}
	if len(got) == 0 {
		return 0
	}
	sort.Float64s(got)
	i := int(q * float64(len(got)-1))
	return got[max(min(i, len(got)-1), 0)]
}

// Median is the middle step, which is the honest single number when one is
// wanted, since a mean is moved by the one step that waited on a checkpoint.
func (r Run) Median() float64 { return r.Quantile(0.5) }

// Windows is the run cut into n consecutive pieces, each reported as its mean.
//
// This is what continuously means in practice. The series says whether the
// number is holding, and holding is the claim the gate is about: sustains 40% or
// better, not touched 40% once in the first hour.
func (r Run) Windows(n int) []float64 {
	if n <= 0 || len(r.MFUs) == 0 {
		return nil
	}
	n = min(n, len(r.MFUs))
	out := make([]float64, 0, n)
	for i := range n {
		lo := i * len(r.MFUs) / n
		hi := (i + 1) * len(r.MFUs) / n
		var sum float64
		var seen int
		for _, f := range r.MFUs[lo:hi] {
			if f > 0 {
				sum += f
				seen++
			}
		}
		if seen == 0 {
			out = append(out, 0)
			continue
		}
		out = append(out, sum/float64(seen))
	}
	return out
}

// Sustained is the lowest window, which is what the run can be counted on to do
// rather than what it managed at its best.
func (r Run) Sustained(n int) float64 {
	w := r.Windows(n)
	if len(w) == 0 {
		return 0
	}
	out := w[0]
	for _, f := range w[1:] {
		out = min(out, f)
	}
	return out
}

// Drift is the last window minus the first, which is negative for a run that is
// degrading and is the number a mean is specifically unable to show.
func (r Run) Drift(n int) float64 {
	w := r.Windows(n)
	if len(w) < 2 {
		return 0
	}
	return w[len(w)-1] - w[0]
}

// Windows is the number of pieces a run is cut into for the sustained figure.
// Ten is enough that a window is a real stretch of the run and enough of them
// that a decline has somewhere to show up.
const WindowCount = 10

// Sound reports whether every step in the log could be read.
func (r Run) Sound() bool { return len(r.Faults) == 0 }

// Passes reports whether the run clears the gate on the figure that matters,
// which is the sustained one.
func (r Run) Passes() bool {
	return r.Sound() && len(r.MFUs) > 0 && r.Sustained(WindowCount) >= Gate
}

// Verdict is the run in one sentence.
func (r Run) Verdict() string {
	if len(r.Steps) == 0 {
		return "there are no steps in this log, which is what a run that has not started says and not that anything is wrong"
	}
	if !r.Sound() {
		return fmt.Sprintf("this run cannot be read as a utilization figure: %d of %d steps are unusable, starting with %s",
			len(r.Faults), len(r.Steps), r.Faults[0])
	}
	sustained, drift := r.Sustained(WindowCount), r.Drift(WindowCount)
	tail := ""
	if math.Abs(drift) >= 0.02 {
		tail = fmt.Sprintf(", and it has moved %+.0f points from the first tenth of the run to the last", drift*100)
	}
	switch {
	case sustained >= Gate:
		return fmt.Sprintf("%s sustains %.0f%% of peak across every tenth of the run, which clears the %.0f%% gate%s",
			r.Model.Name, sustained*100, Gate*100, tail)
	case sustained >= Kill:
		return fmt.Sprintf("%s sustains %.0f%% of peak at its worst against a mean of %.0f%%, under the %.0f%% gate and above the point where the architecture changes%s",
			r.Model.Name, sustained*100, r.Mean()*100, Gate*100, tail)
	default:
		return fmt.Sprintf("%s sustains %.0f%% of peak at its worst against a mean of %.0f%%, which is the kill criterion rather than a tuning problem%s",
			r.Model.Name, sustained*100, r.Mean()*100, tail)
	}
}
