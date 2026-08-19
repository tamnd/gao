// Package try is the ablation slate: forty training runs, fixed before any of
// them runs, and every one of them published afterward.
//
// Thử is to try. Almost every threshold in this project is a number somebody
// picked, and the honest way to defend a picked number is to run the thing twice
// and look. The deduplication threshold, the quality classifier's cut, how much
// synthetic text the mixture can carry, how many passes over the educational
// slice stop helping: none of those has a right answer that can be read off a
// paper written about English. Forty runs of a 1.4 billion parameter model over
// 40 billion tokens is what this project can afford to spend answering them, and
// the slate is the list of what those forty runs ask.
//
// The slate is fixed and hashed before the first run starts, for the same reason
// the evaluation harness is. A slate written while the results come in is a
// slate that grows a run whenever a number disappoints and loses one whenever a
// number is embarrassing, and nothing in the published table shows that
// happened. So a [Result] carries the digest of the [Slate] it was produced
// under, and a slate edited afterward produces results that no longer match what
// they claim to be.
//
// Three rules do most of the work here.
//
// One run varies one thing. A run that changes the deduplication threshold and
// the quality cut together answers neither question, and it is the most tempting
// mistake on a slate this size because it is the one that fits forty questions
// into twenty runs.
//
// The baseline is run more than once, at different seeds. Without that there is
// no noise floor, and every difference the slate reports is a difference against
// a number with no error bar under it. Most published ablation tables are that,
// and it is invisible from the outside, because a table of effects looks the same
// whether or not anybody knew how large an effect had to be to mean anything.
//
// Every run is published, including the ones that found nothing. A slate that
// reports only where it moved the number is an advertisement, and a null result
// is the more useful half of the output here: it says a knob nobody has to think
// about again, which is worth more to the next person than another win.
package try

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fleet"
	"github.com/zeebo/blake3"
)

// Runs is how many runs the slate holds. The slice is written against forty and
// the number is here so that a slate that quietly became thirty one is refused
// rather than reported as the slate.
const Runs = 40

// Repeats is the fewest times the baseline is run, at different seeds.
//
// Three is few. It is also the fewest that produces a range rather than a
// difference, and the point of it is not a confidence interval but a floor: an
// effect smaller than the gap between two runs of the same recipe is not an
// effect, and without repeats nobody can say where that gap is.
const Repeats = 3

// The model the slate is run at. It is small enough that forty of them is a
// budget somebody will approve and large enough that the ranking it produces
// has been shown to survive to 8B, which is what [github.com/tamnd/gao/fill]
// measures rather than assumes.
const (
	Params = 1_400_000_000
	Tokens = 40_000_000_000
)

// Proxy is what every run is scored by, and it is the fast one on purpose.
// Scoring forty runs on the full evaluation suite costs more than training them.
const Proxy = "vi-cloze"

// A Run is one trial: the baseline with exactly one thing changed, and the
// question that change answers.
type Run struct {
	// ID is what the run is called in the published table.
	ID string `json:"id"`

	// Asks is the question this run exists to answer, in a sentence. A run
	// without one is a run somebody will find a question for after seeing the
	// number, which is the whole failure this package is arranged against.
	Asks string `json:"asks"`

	// Knob is the one thing this run varies, and Value is what it is set to. A
	// run with no knob is a baseline repeat.
	Knob  string `json:"knob,omitempty"`
	Value string `json:"value,omitempty"`

	// Against is the run this one is a difference from. It is normally the first
	// baseline, and it is a field rather than an assumption because a sweep read
	// against the wrong reference reports the shape of the sweep correctly and
	// the size of every effect in it wrongly.
	Against string `json:"against,omitempty"`

	// Seed is what the run is trained with. Two runs of the same recipe at the
	// same seed measure the machine rather than the recipe.
	Seed int64 `json:"seed"`

	// Decides names the threshold or the choice this run settles, and is empty
	// for a run that is exploratory. It is on the slate rather than added later
	// because a run promoted to decisive after it came out well is not evidence.
	Decides string `json:"decides,omitempty"`

	// Note is prose. It is not in the digest.
	Note string `json:"note,omitempty"`
}

// Compute is where the slate runs and what it costs.
//
// It is on the slate and inside the digest because the gate for this slice says
// the compute is sourced and priced before the slate locks. A slate nobody has
// costed is a slate nobody has agreed to run, and forty runs is the size of thing
// that gets halved in a meeting once the invoice is a surprise.
type Compute struct {
	Provider string  `json:"provider"`
	Instance string  `json:"instance"`
	GPUHours float64 `json:"gpu_hours"`
	USD      float64 `json:"usd"`

	// Quoted is when the price was taken, as a plain date, since a price with no
	// date on it is a price somebody remembers rather than one anybody quoted.
	Quoted string `json:"quoted"`
}

// A Slate is the whole comparison, fixed before any of it runs.
type Slate struct {
	Version string `json:"version"`

	// Model is what is trained, in parameters, and Tokens is how much each run
	// reads. They are on the slate because a run that read twice as much as
	// another run is not a comparison, and the only way to catch that later is to
	// have written down what everybody was supposed to read.
	Model  int64 `json:"model"`
	Tokens int64 `json:"tokens"`

	// Proxy is the benchmark every run is scored by.
	Proxy string `json:"proxy"`

	Compute Compute `json:"compute"`
	Runs    []Run   `json:"runs"`

	Note string `json:"note,omitempty"`
}

// A Result is what one run produced.
type Result struct {
	// Slate is the digest of the slate this was produced under.
	Slate doc.Hash `json:"slate"`

	// Run is the run's ID.
	Run string `json:"run"`

	// Score is what the proxy said.
	Score float64 `json:"score"`

	// Box is the machine or the instance type it was produced on. The fleet
	// cannot train this model, so most of these name a rented instance, and a
	// result without one is a number nobody can price or reproduce.
	Box string `json:"box"`

	// GPUHours is what this run actually cost, against what the slate budgeted
	// for all of them.
	GPUHours float64 `json:"gpu_hours"`

	Note string `json:"note,omitempty"`
}

// Errors a slate or a set of results can fail with.
var (
	ErrBadSlate   = errors.New("try: the slate is not a comparison")
	ErrBadResults = errors.New("try: the results do not belong to this slate")
)

// Digest identifies the slate by the comparison it makes. Notes are outside it,
// here as everywhere else, so that improving a sentence does not look like
// changing the experiment.
func (s Slate) Digest() doc.Hash {
	d := blake3.New()
	write := func(key, value string) {
		fmt.Fprintf(d, "%s %d:%s\n", key, len(value), value)
	}
	num := func(key string, v int64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(v))
		_, _ = d.Write([]byte(key))
		_, _ = d.Write(b[:])
	}

	write("version", s.Version)
	num("model", s.Model)
	num("tokens", s.Tokens)
	write("proxy", s.Proxy)
	write("provider", s.Compute.Provider)
	write("instance", s.Compute.Instance)
	write("gpu_hours", fmt.Sprint(s.Compute.GPUHours))
	write("usd", fmt.Sprint(s.Compute.USD))
	write("quoted", s.Compute.Quoted)

	runs := slices.Clone(s.Runs)
	slices.SortStableFunc(runs, func(a, b Run) int { return strings.Compare(a.ID, b.ID) })
	num("runs", int64(len(runs)))
	for _, r := range runs {
		write("id", r.ID)
		write("asks", r.Asks)
		write("knob", r.Knob)
		write("value", r.Value)
		write("against", r.Against)
		write("decides", r.Decides)
		num("seed", r.Seed)
	}
	return doc.Hash(d.Sum(nil))
}

// Baseline reports whether a run is a baseline repeat, which is a run that
// varies nothing.
func (r Run) Baseline() bool { return r.Knob == "" }

// Lookup returns the run with the given ID.
func (s Slate) Lookup(id string) (Run, bool) {
	for _, r := range s.Runs {
		if r.ID == id {
			return r, true
		}
	}
	return Run{}, false
}

// Baselines is every baseline repeat, which is what the noise floor is measured
// from.
func (s Slate) Baselines() []Run {
	var out []Run
	for _, r := range s.Runs {
		if r.Baseline() {
			out = append(out, r)
		}
	}
	return out
}

// Knobs is every distinct thing the slate varies, in the order it first varies
// them. It is the short answer to what forty runs are for.
func (s Slate) Knobs() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range s.Runs {
		if r.Baseline() || seen[r.Knob] {
			continue
		}
		seen[r.Knob] = true
		out = append(out, r.Knob)
	}
	return out
}

// Decisive is how many runs settle a threshold rather than explore. A slate
// where every run is decisive has not admitted that some questions are open, and
// a slate where none of them is has not committed to anything.
func (s Slate) Decisive() int {
	n := 0
	for _, r := range s.Runs {
		if r.Decides != "" {
			n++
		}
	}
	return n
}

// check reports every way a slate is not a comparison.
func (s Slate) check() error {
	var problems []error
	if s.Version == "" {
		problems = append(problems, errors.New("the slate has no version"))
	}
	if s.Model <= 0 {
		problems = append(problems, errors.New("the slate does not say how large a model it trains"))
	}
	if s.Tokens <= 0 {
		problems = append(problems, errors.New("the slate does not say how much each run reads, and two runs that read different amounts are not a comparison"))
	}
	if s.Proxy == "" {
		problems = append(problems, errors.New("the slate does not name the benchmark every run is scored by, so the scoring is decided per run and after the fact"))
	}
	problems = append(problems, s.compute()...)

	if len(s.Runs) != Runs {
		problems = append(problems, fmt.Errorf("the slate holds %s and the slice is written against %d, so either it lost runs or it grew them",
			plural(len(s.Runs), "run"), Runs))
	}

	seen := make(map[string]bool, len(s.Runs))
	recipes := make(map[string]string, len(s.Runs))
	seeds := make(map[int64]string, Repeats)
	baselines := 0
	for i, r := range s.Runs {
		switch {
		case r.ID == "":
			problems = append(problems, fmt.Errorf("run %d has no id", i))
		case seen[r.ID]:
			problems = append(problems, fmt.Errorf("%s appears twice on the slate", r.ID))
		}
		seen[r.ID] = true

		if strings.TrimSpace(r.Asks) == "" {
			problems = append(problems, fmt.Errorf("%s does not say what it asks, so the question gets written after the number comes back", r.ID))
		}
		if r.Seed == 0 {
			problems = append(problems, fmt.Errorf("%s has no seed, so it is a run nobody can run again", r.ID))
		}

		if r.Baseline() {
			baselines++
			if other, ok := seeds[r.Seed]; ok {
				problems = append(problems, fmt.Errorf("%s and %s are the baseline at the same seed, which measures the machine rather than the seed and leaves the noise floor where it was", other, r.ID))
			}
			seeds[r.Seed] = r.ID
			if r.Against != "" {
				problems = append(problems, fmt.Errorf("%s is a baseline repeat and is measured against %s, and the baseline is what everything else is measured against", r.ID, r.Against))
			}
			continue
		}

		if r.Value == "" {
			problems = append(problems, fmt.Errorf("%s varies %s and does not say what to, so the run is a label rather than a recipe", r.ID, r.Knob))
		}
		if key := r.Knob + "=" + r.Value; recipes[key] != "" {
			problems = append(problems, fmt.Errorf("%s and %s are the same recipe, which is one run counted twice and a knob the slate never actually swept", recipes[key], r.ID))
		} else {
			recipes[key] = r.ID
		}
		if r.Against == "" {
			problems = append(problems, fmt.Errorf("%s does not say what it is a difference from", r.ID))
		}
	}

	problems = append(problems, s.against()...)
	if baselines < Repeats {
		problems = append(problems, fmt.Errorf("the slate runs the baseline %s and %d is the floor, since an effect smaller than the gap between two runs of the same recipe is not an effect and nobody can say where that gap is from one run",
			plural(baselines, "time"), Repeats))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrBadSlate, errors.Join(problems...))
	}
	return nil
}

// against checks what each run is measured against, which is where a slate stops
// answering one question at a time.
func (s Slate) against() []error {
	var problems []error
	for _, r := range s.Runs {
		if r.Baseline() || r.Against == "" {
			continue
		}
		ref, ok := s.Lookup(r.Against)
		if !ok {
			problems = append(problems, fmt.Errorf("%s is measured against %s, which is not on the slate", r.ID, r.Against))
			continue
		}
		if ref.ID == r.ID {
			problems = append(problems, fmt.Errorf("%s is measured against itself", r.ID))
			continue
		}
		if !ref.Baseline() && ref.Knob != r.Knob {
			problems = append(problems, fmt.Errorf("%s varies %s and is measured against %s which varies %s, so the difference between them is two things at once and answers neither question",
				r.ID, r.Knob, ref.ID, ref.Knob))
		}
	}
	return problems
}

// compute checks the slate has been costed, and that nobody has written down the
// fleet as the place forty of these will run.
func (s Slate) compute() []error {
	var problems []error
	c := s.Compute
	if c.Provider == "" || c.Instance == "" {
		problems = append(problems, errors.New("the slate does not say where it runs, and the fleet cannot train this model, so somewhere is a decision rather than a detail"))
	}
	if c.GPUHours <= 0 {
		problems = append(problems, errors.New("the slate does not say how many GPU hours it takes"))
	}
	if c.USD <= 0 {
		problems = append(problems, errors.New("the slate is not priced, and a slate nobody has costed is a slate nobody has agreed to run"))
	}
	if c.Quoted == "" && c.USD > 0 {
		problems = append(problems, errors.New("the price has no date on it, which makes it a price somebody remembers rather than one anybody quoted"))
	}
	if _, ok := fleet.Lookup(c.Instance); ok {
		problems = append(problems, fmt.Errorf("the slate says it runs on %s, and a 1.4B parameter run over 40B tokens does not fit on the one 24 GB card in the fleet, let alone forty times",
			c.Instance))
	}
	return problems
}

// Faults is check as lines, since a report wants them one to a row.
func (s Slate) Faults() []string {
	err := s.check()
	if err == nil {
		return nil
	}
	var faults []string
	for line := range strings.SplitSeq(err.Error(), "\n") {
		faults = append(faults, strings.TrimPrefix(line, ErrBadSlate.Error()+": "))
	}
	return faults
}

// plural writes a count with its noun, adding the s only when it belongs.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
