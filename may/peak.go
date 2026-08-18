package may

// What a box actually held while a stage ran, against what the design said it
// would.
//
// [PeakBytes] is arithmetic: two shards per worker, four workers on server1,
// 4.1 GB, and it does not read the size of the corpus because a worker pushes a
// finished shard off-box and deletes it before taking the next one. That is the
// claim offload buys and the entire reason a box with 118 GB free can process a
// terabyte.
//
// It is also a model, and the plan gates on a measurement rather than on the
// model. The ceiling is 90 GB, which is twenty two times what the arithmetic
// predicts, and the gap is deliberate: it is room for everything the model does
// not know about, which is the Parquet writer's row group buffer, a part that
// failed its upload and is waiting for a retry, a download resuming into a
// partial file, and whatever the operating system decided to keep in a temporary
// directory. A run that stays under 90 GB passes the gate.
//
// A run that stays under 90 GB and peaks at 60 also falsifies the model, and
// that is worth catching, because a pipeline whose disk nobody can predict is a
// pipeline that will surprise somebody on a box with less room. So this reports
// both: the gate, which is what the milestone asks for, and the drift from the
// prediction, which is what the milestone will need next time the fleet changes.
//
// The drift is read against the workers the run had rather than against
// [PeakBytes]. Those are two numbers and they are far apart on a large box:
// PeakBytes prices every hardware thread, and an ingest runs one worker, so a
// complete FinePDFs run on a thirty two thread machine came back at 0.02x of the
// plan with a fault saying it had measured a run too small to mean anything.
// Every sample carries its worker count and this refuses a trace without one,
// which made using the box's instead a defect nothing but a real trace was going
// to find. Both are reported now, since what the plan would allow the box is a
// real question and it is not the question the drift answers.

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// Ceiling is the most disk an ingestion may hold at once. It is the number the
// milestone gates on, and it is not the arithmetic: [PeakBytes] predicts 4.1 GB
// on server1, and the difference is room for the things a model of shards in
// flight does not know about.
const Ceiling int64 = 90_000_000_000

// Resolution is the widest gap between two disk readings that still measures a
// peak. Thirty seconds is set by the thing that allocates: a worker holds a
// shard while it writes it, and a shard written and pushed inside a sampling
// gap is a shard the watcher never saw.
const Resolution = 30 * time.Second

// Drift is how far the measured peak may sit from the predicted one before the
// model behind [PeakBytes] is reported as wrong. It cuts both ways. Three times
// over is a pipeline holding disk nobody accounted for, and a third of the
// prediction is a run smaller than the one the ceiling was written about.
const Drift = 3.0

// A Sample is the disk one box was holding at one moment during a run.
type Sample struct {
	// Second is seconds since the run started, which is the clock a disk trace
	// is read on. Wall time is in the log next to it and is not what this
	// compares.
	Second int64 `json:"second"`

	// Bytes is scratch held: the shards in flight, the partial downloads, and
	// anything else the run put on the disk. It is what the run is holding
	// rather than what the filesystem contains.
	Bytes int64 `json:"bytes"`

	Box   string `json:"box"`
	Stage string `json:"stage"`

	// Workers is how many workers were running when the sample was taken. Peak
	// disk is a number per worker, so a sample without it cannot be read against
	// a prediction that is.
	Workers int `json:"workers"`
}

// A Peak is a run's disk, measured.
type Peak struct {
	Run string `json:"run"`
	Box string `json:"box"`

	// Held is the highest sample, At is the second it was taken, and During is
	// the stage that was running. The stage is carried because "the peak was in
	// the upload retry" and "the peak was in the download" are different
	// problems with the same number.
	Held   int64  `json:"held"`
	At     int64  `json:"at"`
	During string `json:"during"`

	// Predicted is [ShardsPerWorker] shards for each worker the trace says was
	// running, and Ceiling is what the milestone allows.
	//
	// It is read off the trace and not off [PeakBytes], because those are two
	// different numbers and this command spent its first three real traces
	// printing one under the name of the other. [PeakBytes] prices a box running
	// every worker it has threads for, which is what the fleet plan hands out.
	// A run holds what its own workers hold. gao gat hf ingests with one worker,
	// so a complete FinePDFs run on a 32 thread box measured 0.6 GB against a
	// PeakBytes of 32.8 and was reported as a smaller run than the ceiling is
	// about, when what it was is a full run of a single worker stage.
	Predicted int64 `json:"predicted"`
	Ceiling   int64 `json:"ceiling"`

	// Workers is the most workers any sample says were running, which is what
	// Predicted is per.
	Workers int `json:"workers"`

	// Planned is [PeakBytes] for this box, which is what the fleet plan would
	// let the box hold if a stage used all of it. It is carried so that a run
	// using a fraction of the box can be seen to be doing that.
	Planned int64 `json:"planned"`

	// Free is the box's free disk, which is the number that decides whether the
	// ceiling was ever the binding constraint.
	Free int64 `json:"free"`

	// Watched is how long the trace covers and Ran is how long the run lasted.
	// They are separate because a watcher that stopped early cannot report the
	// peak it stopped watching for.
	Watched time.Duration `json:"watched"`
	Ran     time.Duration `json:"ran"`

	// Widest is the largest gap between two readings, which is the resolution
	// this peak was actually measured at rather than the one somebody intended.
	Widest time.Duration `json:"widest"`

	Samples int `json:"samples"`

	// Refused is why the trace cannot support a peak at all, and Faults is what
	// is wrong with the run the trace does describe. They are separate because
	// they call for different work and because they mean different things to
	// whatever ran the command: a refusal is fixed by watching the next run
	// properly, and a fault is fixed by the run or by the box it ran on.
	Refused []string `json:"refused,omitempty"`
	Faults  []string `json:"faults,omitempty"`
}

// Measure reads a disk trace as a peak.
func Measure(run string, ran time.Duration, ceiling int64, samples []Sample) Peak {
	p := Peak{Run: run, Ceiling: ceiling, Ran: ran, Samples: len(samples)}
	if len(samples) == 0 {
		p.Refused = []string{"nothing was watched, so there is no peak here and the arithmetic is still the only thing anybody has"}
		return p
	}

	ordered := slices.Clone(samples)
	slices.SortStableFunc(ordered, func(a, b Sample) int { return int(a.Second - b.Second) })

	boxes := map[string]bool{}
	for _, s := range ordered {
		boxes[s.Box] = true
	}
	if len(boxes) != 1 {
		names := make([]string, 0, len(boxes))
		for b := range boxes {
			names = append(names, b)
		}
		slices.Sort(names)
		p.Refused = append(p.Refused, fmt.Sprintf(
			"the trace holds samples from %s, and a peak is a fact about one machine's disk rather than about a fleet",
			strings.Join(names, " and ")))
	}
	p.Box = ordered[0].Box

	box, known := Lookup(p.Box)
	if !known {
		p.Refused = append(p.Refused, fmt.Sprintf(
			"%s is not a box on the fleet, so there is nothing to read this peak against", p.Box))
	} else {
		p.Planned, p.Free = PeakBytes(box), box.FreeDisk
		if p.Planned == 0 {
			// The first real trace this command read came off a box in exactly
			// this state, and it printed a prediction of 0.0 GB with no drift
			// line under it and said nothing about why. A box under the reserve
			// runs no workers, so there is no per worker arithmetic to read the
			// measurement against, and a run happened on it anyway. That is a
			// fact about the run rather than about the trace, so it is a fault
			// and not a refusal: the peak is real and the plan did not have a
			// place to put it.
			p.Faults = append(p.Faults, fmt.Sprintf(
				"%s has %s free, under the %s reserve, so the plan runs no workers on it and this is a run on a box the arithmetic gives nothing to spend",
				box.Name, GB(box.FreeDisk), GB(ReserveBytes)))
		}
		if box.FreeDisk < ceiling {
			p.Faults = append(p.Faults, fmt.Sprintf(
				"the ceiling is %s and %s has %s free, so a run that stayed under the ceiling still filled the box",
				GB(ceiling), box.Name, GB(box.FreeDisk)))
		}
	}

	var prev int64
	for i, s := range ordered {
		switch {
		case s.Second < 0:
			p.Refused = append(p.Refused, fmt.Sprintf("a sample is taken at %d seconds, and a run does not start before it starts", s.Second))
		case s.Bytes < 0:
			p.Refused = append(p.Refused, fmt.Sprintf("the sample at %s holds %d bytes", clock(s.Second), s.Bytes))
		case s.Workers <= 0:
			p.Refused = append(p.Refused, fmt.Sprintf(
				"the sample at %s does not say how many workers were running, and peak disk is a number per worker rather than a number per box",
				clock(s.Second)))
		}
		if known && s.Bytes > box.FreeDisk {
			p.Refused = append(p.Refused, fmt.Sprintf(
				"the sample at %s holds %s on a box with %s free, so this trace is not of %s",
				clock(s.Second), GB(s.Bytes), GB(box.FreeDisk), box.Name))
		}
		if s.Bytes > p.Held {
			p.Held, p.At, p.During = s.Bytes, s.Second, s.Stage
		}
		if s.Workers > p.Workers {
			p.Workers = s.Workers
		}
		if i > 0 {
			if gap := time.Duration(s.Second-prev) * time.Second; gap > p.Widest {
				p.Widest = gap
			}
		}
		prev = s.Second
	}
	p.Watched = time.Duration(ordered[len(ordered)-1].Second) * time.Second
	p.Predicted = int64(p.Workers) * ShardsPerWorker * ShardBytes

	if p.Widest > Resolution {
		p.Refused = append(p.Refused, fmt.Sprintf(
			"the widest gap between two readings is %s against a resolution of %s, and a worker can take a shard, write it, push it and delete it inside that, so this is the disk at some moments rather than its peak",
			p.Widest, Resolution))
	}
	switch {
	case ran <= 0:
		p.Refused = append(p.Refused, fmt.Sprintf(
			"nobody said how long the run lasted, so the trace is being taken as the run, and a watcher that stopped at %s cannot report what happened after it",
			p.Watched))
	case p.Watched < ran-Resolution || time.Duration(ordered[0].Second)*time.Second > Resolution:
		p.Refused = append(p.Refused, fmt.Sprintf(
			"the trace covers %s of a %s run, and a run allocates hardest when it starts and when it flushes, which is exactly the disk nobody watched",
			p.Watched-time.Duration(ordered[0].Second)*time.Second, ran))
	}
	if p.Predicted > 0 {
		switch ratio := float64(p.Held) / float64(p.Predicted); {
		case ratio > Drift:
			p.Faults = append(p.Faults, fmt.Sprintf(
				"the run peaked at %s against the %s the design predicts, which is %.0f times over, and a pipeline whose disk nobody can predict is a pipeline that will surprise somebody on a box with less room",
				GB(p.Held), GB(p.Predicted), ratio))
		case ratio < 1/Drift:
			p.Faults = append(p.Faults, fmt.Sprintf(
				"the run peaked at %s against the %s the %d workers it ran would hold, which is under a third of it, so either the stage never filled a shard or the trace missed the moments it did",
				GB(p.Held), GB(p.Predicted), p.Workers))
		}
	}
	return p
}

// Headroom is what was left of the ceiling.
func (p Peak) Headroom() int64 { return p.Ceiling - p.Held }

// Ratio is the measured peak over the predicted one, and zero where there is no
// prediction to compare against.
func (p Peak) Ratio() float64 {
	if p.Predicted <= 0 {
		return 0
	}
	return float64(p.Held) / float64(p.Predicted)
}

// Passed reports whether the run stayed under the ceiling, which is the gate the
// milestone actually asks for.
func (p Peak) Passed() bool { return p.Held > 0 && p.Held <= p.Ceiling }

// Blocking is every reason this measurement is not one.
func (p Peak) Blocking() []string { return p.Refused }

// Settled reports whether the gate was met by a measurement worth having.
func (p Peak) Settled() bool { return len(p.Refused) == 0 && p.Passed() && len(p.Faults) == 0 }

// Verdict is the run's disk in one sentence.
func (p Peak) Verdict() string {
	// A refusal comes first, because a trace that cannot support a peak cannot
	// support a verdict about one either.
	if len(p.Refused) > 0 {
		return p.Refused[0]
	}
	// Then the gate. A run that did not fit is the headline whatever else is
	// wrong with it.
	if p.Held > 0 && !p.Passed() {
		return fmt.Sprintf(
			"%s peaked at %s of a %s ceiling during %s, %s over, so the ingestion does not fit on the box it was planned for",
			p.Box, GB(p.Held), GB(p.Ceiling), p.During, GB(-p.Headroom()))
	}
	if len(p.Faults) > 0 {
		return p.Faults[0]
	}
	return fmt.Sprintf(
		"%s peaked at %s of a %s ceiling during %s, %.1f times the %s the design predicts, watched every %s across %s",
		p.Box, GB(p.Held), GB(p.Ceiling), p.During, p.Ratio(), GB(p.Predicted), p.Widest, p.Ran)
}

// ReadTrace loads a disk trace from a file of one JSON sample per line, which is
// what a watcher appends to while a run goes.
func ReadTrace(path string) ([]Sample, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("may: %w", err)
	}
	var out []Sample
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var s Sample
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("may: %s line %d: %w", path, i+1, err)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("may: %s holds no readings", path)
	}
	return out, nil
}

// clock prints a second of a run the way somebody reading a trace says it.
func clock(sec int64) string { return (time.Duration(sec) * time.Second).String() }
