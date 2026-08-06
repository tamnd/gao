// Package nhip publishes what each pipeline stage runs at, with the box on it.
//
// Nhịp is the beat. The milestone item is one sentence long and reads like
// bookkeeping: throughput published per stage with the box label attached to
// every number. It is on the list because a rate without a box is not a rate.
// Normalization on gamingpc has twenty four cores under it and on server1 it
// has four, so the same stage differs by six times between two machines in the
// same rack, and a plan built from a figure somebody measured on whichever box
// was free is wrong in both directions at once. It says the pipeline is fast
// enough when it is not, or it books weeks of a machine that would have taken
// days.
//
// The box label is necessary and it is not sufficient. A rate also has to say
// how many workers produced it, since eight workers on eight cores and one
// worker on the same box are the same stage and not the same number, and the
// unit anybody plans in is the second one multiplied out. So every reading here
// carries the worker count, the threads the box has, and the physical memory,
// and a reading that oversubscribes its box is refused rather than divided.
//
// The memory line is the other half. server3 is the box of record for pipeline
// throughput and it has 23 GB, it wants all eight cores busy, and eight workers
// at 2.5 GB each is 20 GB. That leaves about three for the operating system and
// for the page cache every parquet read goes through, which is tight and is the
// reason the ceiling is 2.5 rather than 3. A stage that holds more than that
// per worker does not get to keep its throughput number, because the run that
// produced it was either using fewer workers or was on a box doing nothing
// else, and neither is the pipeline.
//
// Parallel efficiency is here for the same reason. A stage that runs at eight
// workers and gets four workers' worth of throughput has all eight cores busy
// in the sense that top shows them busy, and the item's phrasing is about the
// second sense. Efficiency under Scales is reported as a fault rather than
// divided into a per core figure that flatters the stage.
package nhip

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Ceiling is the most resident memory one worker may hold. It is 2.5 GiB
// because server3 has eight cores and 23 GB, and eight workers at 2.5 leaves
// three for everything else on the box.
const Ceiling int64 = 2_684_354_560

// Reserve is the share of a box's memory that is not the pipeline's to spend.
// The parquet reader does its work through the page cache, so a run that fills
// memory to the last page reads every shard off disk twice.
const Reserve = 0.10

// MinDocs is the fewest documents a rate may be measured over. Under it the
// number is the first shard, the warm cache, and whatever the scheduler was
// doing that minute.
const MinDocs int64 = 10_000

// MinSeconds is the shortest run a rate may be read off, for the same reason.
const MinSeconds = 60.0

// Scales is the least parallel efficiency that counts as keeping the cores
// busy. Below it the stage is bound by something other than the cores, and the
// honest report of that is the efficiency rather than a per core rate.
const Scales = 0.70

// Corpus is how many documents the pipeline is planned against. It is an
// estimate off roughly 205B tokens of deduplicated Vietnamese HTML at about a
// thousand tokens a document, and it is labeled as an estimate everywhere it
// is printed, since a measured count moves every hours figure below it.
const Corpus int64 = 200_000_000

// Roster is the stages the pipeline is published in. Per stage is the phrase on
// the milestone, and a table with one row in it is a stage rather than a
// pipeline.
func Roster() []string { return []string{"normalize", "filter", "classify", "dedup"} }

// A Stage is one pass over the corpus, as one run of it on one box measured it.
type Stage struct {
	Name string `json:"name"`

	// Box is where this reading was taken. It is the label the milestone item
	// is about, and nothing here is publishable without it.
	Box string `json:"box"`

	// Runs is the box this stage runs on in production, when that is not the
	// box the reading came off. A rate measured somewhere else is worth having
	// and is not the number the plan uses, and saying which is which is the
	// difference between a measurement and an estimate.
	Runs string `json:"runs,omitempty"`

	// Threads and Memory are what the box has. They are recorded on the reading
	// rather than looked up, so that a reading which claims a box it did not
	// run on can be caught by the box disagreeing with itself.
	Threads int   `json:"threads"`
	Memory  int64 `json:"memory"`

	Workers int     `json:"workers"`
	Docs    int64   `json:"docs"`
	Seconds float64 `json:"seconds"`

	// Bytes is the input text read, which is the other unit a stage's cost is
	// quoted in and the one that does not move when the document length does.
	Bytes int64 `json:"bytes"`

	// PeakRSS is the highest resident memory any one worker reached. It is the
	// worst worker rather than the mean, because the box runs out of memory
	// when the worst one does.
	PeakRSS int64 `json:"peak_rss"`

	// Solo is the same stage's rate at one worker, when somebody measured it.
	// Without it there is no parallel efficiency, only a number that goes up
	// with cores.
	Solo float64 `json:"solo"`
}

// Rate is documents a second for the whole stage.
func (s Stage) Rate() float64 {
	if s.Seconds <= 0 {
		return 0
	}
	return float64(s.Docs) / s.Seconds
}

// PerWorker is the rate one worker contributed, which is the figure that
// carries across boxes and the reason the worker count has to be on the line.
func (s Stage) PerWorker() float64 {
	if s.Workers <= 0 {
		return 0
	}
	return s.Rate() / float64(s.Workers)
}

// Read is input bytes a second.
func (s Stage) Read() float64 {
	if s.Seconds <= 0 {
		return 0
	}
	return float64(s.Bytes) / s.Seconds
}

// Scaling is what the workers bought against what adding them should buy. One
// is linear and nothing is ever one.
func (s Stage) Scaling() float64 {
	if s.Solo <= 0 || s.Workers <= 0 {
		return 0
	}
	return s.Rate() / (s.Solo * float64(s.Workers))
}

// Resident is what the whole stage holds on the box at once.
func (s Stage) Resident() int64 { return s.PeakRSS * int64(s.Workers) }

// Usable is the memory the pipeline may spend, which is the box's less what the
// page cache and the operating system need to stay out of the way.
func (s Stage) Usable() int64 { return int64(float64(s.Memory) * (1 - Reserve)) }

// Over reports whether one worker held more than the milestone allows.
func (s Stage) Over() bool { return s.PeakRSS > Ceiling }

// Swaps reports whether all the workers together want more than the box has to
// give. On server3 this never fires while the per worker ceiling holds, since
// eight workers at 2.5 GB is 20 of its 23, and that is the reason the ceiling is
// 2.5. It fires on the smaller boxes, which makes it the check that catches
// somebody moving a stage onto server1 because server3 was busy.
func (s Stage) Swaps() bool { return s.Memory > 0 && s.Resident() > s.Usable() }

// Hours is what this stage costs over a given number of documents.
func (s Stage) Hours(docs int64) float64 {
	if s.Rate() <= 0 {
		return 0
	}
	return float64(docs) / s.Rate() / 3600
}

// Blocking is every reason this reading cannot be published as a throughput.
func (s Stage) Blocking() []string {
	var why []string
	if s.Name == "" {
		return []string{"a stage with no name cannot be published per stage, which is the whole of what this item asks for"}
	}
	if s.Box == "" {
		why = append(why, fmt.Sprintf(
			"%s does not say which box it ran on, and the same stage differs by six times across this fleet, so a rate without a box is not a rate",
			s.Name))
	}
	if s.Workers <= 0 {
		why = append(why, fmt.Sprintf(
			"%s does not say how many workers produced its rate, and a throughput over an unknown number of cores cannot be planned against a box with a known number",
			s.Name))
	} else if s.Threads > 0 && s.Workers > s.Threads {
		why = append(why, fmt.Sprintf(
			"%s ran %s on a box with %s, so the rate is oversubscription reported as throughput and the per worker figure is a division nobody should do",
			s.Name, plural(s.Workers, "worker"), plural(s.Threads, "thread")))
	}
	if s.Threads <= 0 || s.Memory <= 0 {
		why = append(why, fmt.Sprintf(
			"%s does not record what the box has, so the reading cannot be checked against the machine it claims to have come off",
			s.Name))
	}
	if s.Docs < MinDocs {
		why = append(why, fmt.Sprintf(
			"%s was measured over %s, under the %s a rate needs, so it is the first shard and a warm cache",
			s.Name, plural64(s.Docs, "document"), plural64(MinDocs, "document")))
	}
	if s.Seconds < MinSeconds {
		why = append(why, fmt.Sprintf(
			"%s ran for %.0f seconds, and a rate off less than a minute is mostly whatever else the box was doing that minute",
			s.Name, s.Seconds))
	}
	if s.PeakRSS <= 0 {
		why = append(why, fmt.Sprintf(
			"%s does not record what a worker held, and the memory line on this milestone is per worker rather than per box",
			s.Name))
	}
	if s.Solo <= 0 {
		why = append(why, fmt.Sprintf(
			"%s has no single worker reading, so there is no efficiency to put next to its rate and a stage that got half of what the cores could give reads the same as one that got all of it",
			s.Name))
	} else if e := s.Scaling(); e < Scales {
		why = append(why, fmt.Sprintf(
			"%s reached %.0f%% of %s times its single worker rate, under %.0f%%, so the cores are busy in the sense that top says they are busy",
			s.Name, 100*e, plural(s.Workers, "worker"), 100*Scales))
	} else if e > 1.15 {
		why = append(why, fmt.Sprintf(
			"%s reached %.0f%% of linear, which is more throughput than there are cores to produce it, so one of the two readings came off a warm cache",
			s.Name, 100*e))
	}
	if s.Runs != "" && s.Runs != s.Box {
		why = append(why, fmt.Sprintf(
			"%s was measured on %s and runs on %s, so this is an observation rather than the number the plan is built on",
			s.Name, s.Box, s.Runs))
	}
	return why
}

// A Pipeline is every stage, which is what per stage means.
type Pipeline struct {
	// Docs is how many documents the hours are computed over. It is Corpus
	// unless somebody has counted.
	Docs int64 `json:"docs"`

	// Measured says the document count came off a real ingest rather than off
	// the plan estimate, since every hours figure below is linear in it.
	Measured bool `json:"measured"`

	Stages []Stage `json:"stages"`
}

// Ranked is the stages slowest first, since the slowest one is what anybody
// wants off this table.
func (p Pipeline) Ranked() []Stage {
	out := slices.Clone(p.Stages)
	slices.SortStableFunc(out, func(a, b Stage) int {
		switch {
		case a.Rate() < b.Rate():
			return -1
		case a.Rate() > b.Rate():
			return 1
		default:
			return strings.Compare(a.Name, b.Name)
		}
	})
	return out
}

// Bottleneck is the slowest stage, and false when nothing was measured.
func (p Pipeline) Bottleneck() (Stage, bool) {
	if len(p.Stages) == 0 {
		return Stage{}, false
	}
	// Taken off the ranking rather than found again, so that two stages at the
	// same rate name the same one in the table and in the verdict.
	return p.Ranked()[0], true
}

// Hours is what one pass of the whole pipeline costs. The stages are separate
// passes over parquet rather than one streamed graph, so this is a sum and not
// the slowest stage.
func (p Pipeline) Hours() float64 {
	var sum float64
	for _, s := range p.Stages {
		sum += s.Hours(p.Docs)
	}
	return sum
}

// Missing is the stages on the roster nobody published a rate for.
func (p Pipeline) Missing() []string {
	have := map[string]bool{}
	for _, s := range p.Stages {
		have[s.Name] = true
	}
	var out []string
	for _, name := range Roster() {
		if !have[name] {
			out = append(out, name)
		}
	}
	return out
}

// Over is every stage whose worst worker held more than the ceiling.
func (p Pipeline) Over() []Stage {
	var out []Stage
	for _, s := range p.Ranked() {
		if s.Over() {
			out = append(out, s)
		}
	}
	return out
}

// Swapping is every stage that wants more than its box has.
func (p Pipeline) Swapping() []Stage {
	var out []Stage
	for _, s := range p.Ranked() {
		if s.Swaps() {
			out = append(out, s)
		}
	}
	return out
}

// Blocking is every reason this is not a published throughput.
func (p Pipeline) Blocking() []string {
	if len(p.Stages) == 0 {
		return []string{"no stage was measured, so there is no throughput here to attach a box to"}
	}
	var why []string
	seen := map[string]bool{}
	for _, s := range p.Stages {
		if seen[s.Name] {
			why = append(why, fmt.Sprintf("%s appears twice, and two readings of one stage are not two stages", s.Name))
		}
		seen[s.Name] = true
		why = append(why, s.Blocking()...)
	}
	if m := p.Missing(); len(m) > 0 {
		why = append(why, fmt.Sprintf(
			"%s of the pipeline has no published rate, so the total below is the stages somebody got round to measuring",
			strings.Join(m, ", ")))
	}
	if p.Docs <= 0 {
		why = append(why, "the pipeline is costed over no documents, and every hours figure here is linear in that count")
	}
	return why
}

// Settled reports whether the table is worth reading a plan off.
func (p Pipeline) Settled() bool { return len(p.Blocking()) == 0 }

// Holds reports whether the memory half of the milestone holds: every worker
// under the ceiling, and every stage's workers together inside the box.
func (p Pipeline) Holds() bool {
	return p.Settled() && len(p.Over()) == 0 && len(p.Swapping()) == 0
}

// Verdict is the table in one sentence.
func (p Pipeline) Verdict() string {
	b, ok := p.Bottleneck()
	if !ok {
		return "nothing was measured, so the pipeline runs at whatever it runs at"
	}
	if why := p.Blocking(); len(why) > 0 {
		return why[0]
	}
	estimate := "an estimated"
	if p.Measured {
		estimate = "a counted"
	}
	switch {
	case len(p.Over()) > 0:
		s := p.Over()[0]
		return fmt.Sprintf(
			"%s held %s in its worst worker against a ceiling of %s, and %s of that is %s on a box with %s, so the stage runs at fewer workers or it does not run on %s",
			s.Name, gigabytes(s.PeakRSS), gigabytes(Ceiling), plural(s.Workers, "copy"),
			gigabytes(s.Resident()), gigabytes(s.Memory), s.Box)
	case len(p.Swapping()) > 0:
		s := p.Swapping()[0]
		return fmt.Sprintf(
			"%s at %s wants %s of %s's %s, which leaves %s for the page cache every parquet read goes through",
			s.Name, plural(s.Workers, "worker"), gigabytes(s.Resident()), s.Box,
			gigabytes(s.Memory), gigabytes(s.Memory-s.Resident()))
	default:
		return fmt.Sprintf(
			"%s is the slowest stage at %.0f documents a second on %s, so %s %s costs %.0f hours of the pipeline's %.0f, with the worst worker holding %s of a %s ceiling",
			b.Name, b.Rate(), b.Box, estimate, millions(p.Docs), b.Hours(p.Docs), p.Hours(),
			gigabytes(worstWorker(p)), gigabytes(Ceiling))
	}
}

func worstWorker(p Pipeline) int64 {
	var out int64
	for _, s := range p.Stages {
		if s.PeakRSS > out {
			out = s.PeakRSS
		}
	}
	return out
}

// ReadPipeline loads a pipeline from a file of one JSON stage per line, which is
// what a benchmark run appends to.
func ReadPipeline(docs int64, measured bool, path string) (Pipeline, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Pipeline{}, fmt.Errorf("nhip: %w", err)
	}
	p := Pipeline{Docs: docs, Measured: measured}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var s Stage
		if err := dec.Decode(&s); err != nil {
			return Pipeline{}, fmt.Errorf("nhip: %s line %d: %w", path, i+1, err)
		}
		p.Stages = append(p.Stages, s)
	}
	if len(p.Stages) == 0 {
		return Pipeline{}, fmt.Errorf("nhip: %s holds no stages", path)
	}
	return p, nil
}

func gigabytes(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/(1<<30)) }

func millions(n int64) string { return fmt.Sprintf("%.0fM documents", float64(n)/1e6) }

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	if base, ok := strings.CutSuffix(noun, "y"); ok {
		return fmt.Sprintf("%d %sies", n, base)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func plural64(n int64, noun string) string { return plural(int(n), noun) }
