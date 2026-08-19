package efficiency

// Surviving preemption, which is a question about arithmetic rather than about
// engineering.
//
// Spot capacity is where the compute for a run this size is affordable, and spot
// capacity is taken back. So the run is interrupted on a schedule nobody sets,
// and what decides whether that is survivable is one number: how often it
// checkpoints. Checkpoint too rarely and every preemption throws away hours of
// gradient. Checkpoint too often and the run spends its life writing to disk
// instead of training. Both failures look like a slow run from the outside.
//
// The interval between them is not a preference. Daly's first order result says
// the optimum is the square root of twice the checkpoint cost times the mean
// time between interruptions, and everything here is that formula plus the two
// things it does not tell you.
//
// The first is that there is a regime where no interval works. When writing a
// checkpoint takes a meaningful fraction of the time between preemptions, the
// run is preempted during the write often enough that it never lands one, and
// the optimum is a number describing a run that makes no progress at all. That
// is a state to detect rather than to optimize inside.
//
// The second is that a checkpoint that was written is not yet a checkpoint. It
// counts when the store confirms it holds those bytes, which is the same
// distinction the rotation package is built around, and it fails the same way:
// from the training host afterwards, a checkpoint that landed and one that was
// half written when the instance went away look identical.

import (
	"fmt"
	"math"
	"time"
)

// A Checkpoint is what one save costs.
type Checkpoint struct {
	// Params is the parameter count being saved, which is the whole model rather
	// than the active fraction, since the optimizer keeps state for all of it.
	Params int64

	// Bytes is what one parameter takes in a checkpoint. Weights in bf16 are 2,
	// and a resumable checkpoint is closer to 14 with the fp32 master copy and
	// the two AdamW moments, which is the difference between a file the fleet
	// can hold and one it cannot.
	Bytes int

	// Rate is how fast those bytes are written, in bytes per second, across
	// however many ranks write in parallel. It is one number because the thing
	// being asked is how long the run is not training for.
	Rate float64
}

// Bytes per parameter for the two kinds of checkpoint that get confused.
const (
	// WeightsOnly is what gets published, and it cannot resume a run.
	WeightsOnly = 2

	// Resumable is bf16 weights, the fp32 master copy, and the two AdamW
	// moments, which is what a preemption needs to be survivable.
	Resumable = 14
)

// Size is the checkpoint in bytes.
func (c Checkpoint) Size() int64 { return c.Params * int64(c.Bytes) }

// Cost is how long one save takes, which is time the run is not training.
func (c Checkpoint) Cost() time.Duration {
	if c.Rate <= 0 {
		return 0
	}
	return time.Duration(float64(c.Size()) / c.Rate * float64(time.Second))
}

// A Spot is a run on capacity that gets taken away.
type Spot struct {
	Checkpoint Checkpoint

	// Mean is the mean time between preemptions on the capacity being bought.
	Mean time.Duration

	// Restart is preemption to training again: capacity reacquired, the image
	// up, the checkpoint read back, and the step counter where it was.
	Restart time.Duration

	// Confirm is how long the store takes to confirm it holds a checkpoint after
	// the write returns. Until it does, the previous one is the last checkpoint
	// this run actually has.
	Confirm time.Duration
}

// Interval is how often to checkpoint, by Daly's first order optimum.
func (s Spot) Interval() time.Duration {
	w, m := s.Checkpoint.Cost().Seconds(), s.Mean.Seconds()
	if w <= 0 || m <= 0 {
		return 0
	}
	return time.Duration(math.Sqrt(2*w*m) * float64(time.Second))
}

// Lost is the work thrown away by one preemption at a given interval: on average
// half the interval, plus whatever was not yet confirmed, plus the restart.
func (s Spot) Lost(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	return interval/2 + s.Confirm + s.Restart
}

// Overhead is the fraction of wall clock that is not gradient, which is the
// number a budget is a function of.
//
// Two terms. Writing checkpoints costs the write divided by the interval, all
// the time. Preemptions cost whatever is lost each time, divided by how long
// runs between them.
func (s Spot) Overhead(interval time.Duration) float64 {
	if interval <= 0 || s.Mean <= 0 {
		return 0
	}
	writing := s.Checkpoint.Cost().Seconds() / interval.Seconds()
	losing := s.Lost(interval).Seconds() / s.Mean.Seconds()
	return writing + losing
}

// Best is the overhead at the interval this package recommends.
func (s Spot) Best() float64 { return s.Overhead(s.Interval()) }

// Survivable is the ceiling on overhead above which spot capacity has stopped
// being cheap. Spot is priced at roughly a third of on demand, so a run losing
// more than a third of its wall clock to interruption is paying on demand
// prices for spot reliability.
const Survivable = 0.33

// Blocking is every reason this run cannot be made to survive preemption by
// choosing an interval.
func (s Spot) Blocking() []string {
	var out []string
	if s.Mean <= 0 {
		out = append(out, "the run does not say how often the capacity gets taken back, and the checkpoint interval is a function of that and nothing else")
	}
	if s.Checkpoint.Rate <= 0 || s.Checkpoint.Params <= 0 {
		out = append(out, "the run does not say what a checkpoint costs to write, so there is no interval to compute")
	}
	if len(out) > 0 {
		return out
	}

	if cost := s.Checkpoint.Cost() + s.Confirm; cost >= s.Mean {
		out = append(out, fmt.Sprintf(
			"a checkpoint takes %s to write and confirm against %s between preemptions, so the run is interrupted during the save more often than it finishes one and no interval fixes that",
			span(cost), span(s.Mean)))
	} else if got := s.Best(); got > Survivable {
		out = append(out, fmt.Sprintf(
			"the best interval still loses %.0f%% of wall clock against a ceiling of %.0f%%, which is spot reliability at on demand prices",
			100*got, 100*Survivable))
	}
	if s.Confirm > 0 && s.Confirm >= s.Interval() {
		out = append(out, fmt.Sprintf(
			"the store takes %s to confirm a checkpoint and the interval is %s, so the run is never more than one unconfirmed save from having no checkpoint at all",
			span(s.Confirm), span(s.Interval())))
	}
	return out
}

// Survives reports whether this run can be run on spot capacity.
func (s Spot) Survives() bool { return len(s.Blocking()) == 0 }

// Verdict is the run in one sentence.
func (s Spot) Verdict() string {
	if why := s.Blocking(); len(why) > 0 {
		return why[0]
	}
	return fmt.Sprintf(
		"checkpoint every %s, which costs %.1f%% of wall clock and puts %s at risk per preemption against %s between them",
		span(s.Interval()), 100*s.Best(), span(s.Lost(s.Interval())), span(s.Mean))
}

// Retention is how many checkpoints a disk holds, and how far back that reaches.
//
// The fleet keeps checkpoints because the training host is rented and the fleet
// is not, and the question a retention budget answers is how far back a run can
// be rewound after somebody notices something went wrong three hours ago.
type Retention struct {
	Checkpoint Checkpoint
	Free       int64
	Interval   time.Duration
}

// Keeps is how many checkpoints fit.
func (r Retention) Keeps() int {
	if size := r.Checkpoint.Size(); size > 0 {
		return int(r.Free / size)
	}
	return 0
}

// Reach is how far back the retained checkpoints go.
func (r Retention) Reach() time.Duration { return time.Duration(r.Keeps()) * r.Interval }

// Verdict is the retention budget in one sentence.
func (r Retention) Verdict() string {
	switch n := r.Keeps(); n {
	case 0:
		return fmt.Sprintf("a checkpoint is %s and there is %s free, so not one of them fits and there is no retention budget to write down",
			gb(r.Checkpoint.Size()), gb(r.Free))
	case 1:
		return fmt.Sprintf("a checkpoint is %s and there is %s free, so the fleet holds exactly one and a run can only be rewound to its last save",
			gb(r.Checkpoint.Size()), gb(r.Free))
	default:
		return fmt.Sprintf("a checkpoint is %s and there is %s free, so the fleet holds %d of them, which reaches %s back",
			gb(r.Checkpoint.Size()), gb(r.Free), n, span(r.Reach()))
	}
}

func gb(b int64) string { return fmt.Sprintf("%.0f GB", float64(b)/1e9) }

// span prints a duration the way a person says it, which is what these verdicts
// get read as.
func span(d time.Duration) string {
	switch {
	case d <= 0:
		return "no time"
	case d < time.Minute:
		return fmt.Sprintf("%.0f seconds", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	default:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	}
}
