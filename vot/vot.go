// Package vot reads a training curve back and says whether it spiked, what a
// spike cost, and whether the protocol that was supposed to catch it could have.
//
// Vọt is to shoot up. Every long pretraining run has one, and the reason a loss
// spike is worth a package rather than a paragraph is that it is the one failure
// on this project that is cheap to see and expensive to see late. A spike that
// recovers on its own is a curiosity. A spike that does not is a run that has
// been writing garbage into the weights since it happened, and the bill for
// noticing it a day later is the day.
//
// # Why the protocol is written before the curve exists
//
// The response to a spike is to rewind to the last checkpoint before it, skip
// the span of data that produced it, and resume at a lower learning rate. Every
// part of that is a decision somebody makes at three in the morning while the
// run is burning money, which is the worst possible time to decide what counts
// as a spike. So it is decided here, in numbers, before there is a curve to be
// tempted by: how far over the trailing band a step has to sit, how long it has
// to stay there, and how much of the run a single rewind is allowed to cost.
//
// The threshold is two tests rather than one, and a step has to clear both. It
// has to be a set fraction over the trailing median, so that a run whose loss is
// perfectly flat does not report a spike every time the third decimal moves, and
// it has to be several times the run's own scatter, so that a run which is noisy
// by nature does not report one on every step. One test alone is wrong in a
// different direction on each kind of run, and the two together are wrong on
// neither.
//
// # Why the checkpoint cadence is part of this and not an operational detail
//
// A rewind costs the steps between the last checkpoint and the spike, so the
// cadence decides the price of the protocol before the protocol runs. A run that
// checkpoints every four hours has agreed, without writing it down anywhere, to
// throw away up to four hours every time the loss jumps. That is a number, this
// package makes it one, and it is a fault when it goes over what the run can
// afford rather than a discovery made after the second spike.
//
// # What this does not do
//
// It does not say why the loss spiked. A bad batch, a numerical overflow in a
// low precision cast, a learning rate that was always too high, and a corrupt
// shard all draw the same shape on the curve, and telling them apart needs the
// gradient norm, the data span, and somebody looking. What this does is make
// sure the shape is on the report rather than inside an average, name what the
// response would have cost, and refuse to answer at all off a log too coarse to
// have held the answer.
package vot

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Window is how many logged rows the band is taken over. A hundred is long
// enough that one bad batch does not move the median it is being judged against
// and short enough that the band follows a loss curve that is still coming down.
const Window = 100

// Rise is how far over the trailing median a step has to sit. Loss curves at
// this scale move in the third decimal from step to step, so a tenth over the
// median is far outside anything the schedule produces on its own.
const Rise = 0.10

// Scatter is how many times the run's own spread a step has to clear, measured
// as median absolute deviation because one spike would move a standard deviation
// enough to hide the next one.
//
// This number was not picked. It was swept against the three real runs in
// testdata, which are a clean run, a run that was handed a learning rate twenty
// five times too high for thirty steps, and a run that was handed one four
// hundred times too high for sixty. Under 3 the clean run starts reporting its
// own noise, six times in a four thousand step log. Over 4.5 the recoverable
// blowup stops being reported at all, which was the first thing real data said
// that a formula would not have: the six this started at read a real spike, one
// anybody would see by eye, as an ordinary step.
const Scatter = 3.5

// Recover is how many logged rows a spike has to come back inside the band
// within before it stops being a spike and starts being a diverged run.
const Recover = 200

// MaxGap is the coarsest logging this protocol can be run against, in steps. A
// spike that lasts fifty steps and is logged every hundred leaves nothing in the
// log at all, and a report taken off that log says the run was clean.
const MaxGap = 10

// MaxRewind is the share of a run a single rewind may cost. Past this the
// protocol is more expensive than the problem, which means the checkpoint
// cadence is wrong rather than the protocol.
const MaxRewind = 0.02

// MaxSpikes is how many spikes a run may have and still be a run somebody is
// reading a curve off. Past that the curve is the finding.
const MaxSpikes = 3

// MinRows is the least log this will take a band off. Below a window and a bit
// there is no trailing median for the first row to be judged against, so every
// answer is a property of where the log happens to start.
const MinRows = Window * 3

// A Step is one logged row of a training run.
type Step struct {
	Step int     `json:"step"`
	Loss float64 `json:"loss"`

	// LR and Grad are what separates the kinds of spike from each other. They
	// are not part of the detection, which is the point: the detection has to
	// work on a log that carries neither, and the report has to say so.
	LR   float64 `json:"lr,omitempty"`
	Grad float64 `json:"grad_norm,omitempty"`
}

// A Spike is one excursion above the band, and what the protocol would have paid
// to undo it.
type Spike struct {
	Step int     `json:"step"`
	Loss float64 `json:"loss"`

	// Base is the trailing median the step was judged against and Band is the
	// threshold it cleared, so a reader can check the call rather than take it.
	Base float64 `json:"base"`
	Band float64 `json:"band"`
	Over float64 `json:"over"`

	Grad float64 `json:"grad_norm,omitempty"`

	// Back is the step the loss came back to the band at, and Rows is how many
	// logged rows it spent outside. Back is zero when it never came back.
	Back int `json:"back,omitempty"`
	Rows int `json:"rows"`

	// From is the checkpoint the protocol rewinds to, Rewind is the steps that
	// costs, and Lost is those steps against the run.
	From   int     `json:"from"`
	Rewind int     `json:"rewind"`
	Lost   float64 `json:"lost"`
}

// Recovered reports whether the loss came back on its own.
func (s Spike) Recovered() bool { return s.Back > 0 }

// A Curve is one training log read against the protocol.
type Curve struct {
	Run   string `json:"run"`
	Total int    `json:"total"`

	// Checkpoint is how often the run takes one, in steps, which is what decides
	// the price of every rewind below.
	Checkpoint int `json:"checkpoint"`

	Rows  int `json:"rows"`
	Every int `json:"every"`
	First int `json:"first"`
	Last  int `json:"last"`

	Median float64 `json:"median"`
	MAD    float64 `json:"mad"`

	Spikes   []Spike `json:"spikes,omitempty"`
	Diverged int     `json:"diverged"`

	// Rewind is what the protocol would have cost over the whole run, in steps,
	// and Cost is that against the run's length.
	Rewind int     `json:"rewind"`
	Cost   float64 `json:"cost"`

	Grad bool `json:"grad_norm"`
	LR   bool `json:"lr"`

	gaps  []int
	steps []Step
}

// ReadCurve runs the protocol over a log.
func ReadCurve(run string, total, checkpoint int, steps []Step) Curve {
	c := Curve{Run: run, Total: total, Checkpoint: checkpoint, Rows: len(steps), steps: steps}
	if len(steps) == 0 {
		return c
	}
	c.First, c.Last = steps[0].Step, steps[len(steps)-1].Step

	loss := make([]float64, 0, len(steps))
	gaps := make([]int, 0, len(steps))
	c.Grad, c.LR = true, true
	for i, s := range steps {
		loss = append(loss, s.Loss)
		if i > 0 {
			gaps = append(gaps, s.Step-steps[i-1].Step)
		}
		if s.Grad <= 0 {
			c.Grad = false
		}
		if s.LR <= 0 {
			c.LR = false
		}
	}
	c.Median, c.MAD = spread(loss)
	if len(gaps) > 0 {
		sorted := slices.Clone(gaps)
		slices.Sort(sorted)
		c.Every = sorted[len(sorted)/2]
	}
	for i, g := range gaps {
		if c.Every > 0 && g > c.Every {
			c.gaps = append(c.gaps, steps[i+1].Step)
		}
	}

	if len(steps) < MinRows || checkpoint <= 0 {
		return c
	}
	c.Spikes = find(steps, loss, checkpoint, total)
	for _, s := range c.Spikes {
		c.Rewind += s.Rewind
		if !s.Recovered() {
			c.Diverged++
		}
	}
	if total > 0 {
		c.Cost = float64(c.Rewind) / float64(total)
	}
	return c
}

// find walks the curve once and reports each excursion above the band.
//
// The walk does not start a second spike while the first one is still out,
// because a loss that jumps and stays up is one event and reporting it as eighty
// is how a report gets skimmed instead of read.
func find(steps []Step, loss []float64, checkpoint, total int) []Spike {
	var out []Spike
	for i := Window; i < len(steps); i++ {
		base, mad := spread(loss[i-Window : i])
		band := math.Max(base*(1+Rise), base+Scatter*mad)
		if loss[i] <= band {
			continue
		}

		s := Spike{
			Step: steps[i].Step, Loss: loss[i], Base: base, Band: band,
			Grad: steps[i].Grad,
		}
		if base > 0 {
			s.Over = loss[i]/base - 1
		}
		s.From = steps[i].Step / checkpoint * checkpoint
		s.Rewind = steps[i].Step - s.From
		if total > 0 {
			s.Lost = float64(s.Rewind) / float64(total)
		}

		j := i + 1
		for ; j < len(steps) && j-i <= Recover; j++ {
			if loss[j] <= base {
				s.Back = steps[j].Step
				break
			}
		}
		s.Rows = min(j, len(steps)-1) - i
		out = append(out, s)
		i = j
	}
	return out
}

// spread is the median and the median absolute deviation of a window, which is
// the pair a spike is judged against. Both are medians because the thing being
// measured is exactly the kind of outlier a mean absorbs.
func spread(in []float64) (float64, float64) {
	if len(in) == 0 {
		return 0, 0
	}
	sorted := slices.Clone(in)
	slices.Sort(sorted)
	med := sorted[len(sorted)/2]

	off := make([]float64, 0, len(in))
	for _, v := range in {
		off = append(off, math.Abs(v-med))
	}
	slices.Sort(off)
	return med, off[len(off)/2]
}

// Blocking is every reason the protocol cannot be run against this log.
func (c Curve) Blocking() []string {
	var why []string
	if c.Run == "" {
		why = append(why, "the log does not say what run it came off, and a curve nobody can attribute is a curve nobody can compare")
	}
	if c.Checkpoint <= 0 {
		why = append(why, "the run does not say how often it checkpoints, and the response to a spike is a rewind to a checkpoint, so without a cadence there is a detector here and no protocol")
	}
	if c.Total <= 0 {
		why = append(why, "the run does not say how long it is, so what a rewind costs cannot be put against anything")
	}
	if len(c.steps) == 0 {
		return append(why, "the log holds no steps")
	}

	var back, twice, noLoss tally
	seen := map[int]bool{}
	for i, s := range c.steps {
		switch {
		case seen[s.Step]:
			twice.add(fmt.Sprintf("step %d", s.Step))
		case i > 0 && s.Step <= c.steps[i-1].Step:
			back.add(fmt.Sprintf("step %d after step %d", s.Step, c.steps[i-1].Step))
		}
		seen[s.Step] = true
		if math.IsNaN(s.Loss) || math.IsInf(s.Loss, 0) || s.Loss <= 0 {
			noLoss.add(fmt.Sprintf("step %d", s.Step))
		}
	}
	why = append(why,
		twice.say(
			"%[2]s is logged twice, and a step counted twice moves the band it is judged against",
			"%[1]d steps are logged twice, the first of them %[2]s"),
		back.say(
			"the log goes backwards at %[2]s, so it is two runs concatenated rather than one curve",
			"%[1]d rows go backwards, the first of them %[2]s"),
		noLoss.say(
			"%[2]s carries a loss that is not a number, which is past a spike and into a run that has already stopped meaning anything",
			"%[1]d steps carry a loss that is not a number, the first of them %[2]s"),
	)

	if c.Last > c.Total && c.Total > 0 {
		why = append(why, fmt.Sprintf("the log runs to step %d and the run is %d steps long, so one of the two numbers is from a different run", c.Last, c.Total))
	}
	if len(c.steps) < MinRows {
		why = append(why, fmt.Sprintf(
			"the log holds %s and the band is taken over %d, so the first %d rows have nothing to be judged against and what is left is not a curve",
			plural(len(c.steps), "row"), Window, Window))
	}
	return said(why)
}

// Faults are the reasons a curve that was read is not the clean run it looks
// like, or is a curve the protocol could not have caught anything in.
func (c Curve) Faults() []string {
	if len(c.Blocking()) > 0 {
		return nil
	}
	var out []string

	if c.Every > MaxGap {
		out = append(out, fmt.Sprintf(
			"the loss is logged every %d steps, over the %d this protocol needs, so a spike shorter than that leaves nothing in the log and this reading cannot tell a clean run from an unlogged one",
			c.Every, MaxGap))
	}
	if n := len(c.gaps); n > 0 {
		out = append(out, fmt.Sprintf(
			"the log jumps at %s, starting at step %d, which is where a resume came back without its logging and is exactly where a spike would not have been recorded",
			plural(n, "place"), c.gaps[0]))
	}
	if !c.Grad {
		out = append(out, "the log carries no gradient norm, so a spike in it cannot be told from a bad batch, a bad cast, or a learning rate that was always too high, and the response to those three is not the same response")
	}
	if !c.LR {
		out = append(out, "the log carries no learning rate, so a curve that lifted because the schedule warmed back up reads here as a run that spiked")
	}

	if c.Diverged > 0 {
		worst, ok := c.worst()
		if ok {
			out = append(out, fmt.Sprintf(
				"%s never came back inside the band, the first at step %d where the loss went to %.4f against a trailing %.4f, so the run was writing into the weights off a curve that had already left",
				plural(c.Diverged, "spike"), worst.Step, worst.Loss, worst.Base))
		}
	}
	if n := len(c.Spikes); n > MaxSpikes {
		out = append(out, fmt.Sprintf(
			"the curve holds %d spikes, over the %d a run may have before the curve is the finding rather than a thing the protocol handles",
			n, MaxSpikes))
	}
	if over := c.expensive(); len(over) > 0 {
		out = append(out, fmt.Sprintf(
			"a rewind at step %d throws away %s, which is %s of the run and over the %s one rewind may cost, so the checkpoint cadence of %s is the thing to change rather than the protocol",
			over[0].Step, plural(over[0].Rewind, "step"), share(over[0].Lost), share(MaxRewind), plural(c.Checkpoint, "step")))
	}
	return out
}

// worst is the first spike that never came back, which is the one to name,
// because everything after it happened to a run that was already gone.
func (c Curve) worst() (Spike, bool) {
	for _, s := range c.Spikes {
		if !s.Recovered() {
			return s, true
		}
	}
	return Spike{}, false
}

// expensive is the spikes whose rewind costs more of the run than one rewind is
// allowed to.
func (c Curve) expensive() []Spike {
	var out []Spike
	for _, s := range c.Spikes {
		if s.Lost > MaxRewind {
			out = append(out, s)
		}
	}
	return out
}

// Holds reports whether this is a run whose stability the protocol settled.
func (c Curve) Holds() bool { return len(c.Blocking()) == 0 && len(c.Faults()) == 0 }

// Verdict is the curve in one paragraph.
func (c Curve) Verdict() string {
	if why := c.Blocking(); len(why) > 0 {
		return why[0]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s logged %s from step %d to step %d, every %s, at a median loss of %.4f and a scatter of %.4f.",
		c.Run, plural(c.Rows, "row"), c.First, c.Last, plural(c.Every, "step"), c.Median, c.MAD)
	switch n := len(c.Spikes); n {
	case 0:
		fmt.Fprintf(&b, " Nothing in it cleared %.0f%% over the trailing median and %.1f times the scatter at once, so the protocol had nothing to do and the checkpoint cadence of %s was never tested by this run.",
			Rise*100, Scatter, plural(c.Checkpoint, "step"))
	default:
		fmt.Fprintf(&b, " %s cleared the band, %d of them came back on their own, and rewinding to the checkpoint before each would have cost %s, which is %s of the run.",
			plural(n, "spike"), n-c.Diverged, plural(c.Rewind, "step"), share(c.Cost))
	}

	faults := c.Faults()
	switch n := len(faults); n {
	case 0:
	case 1:
		fmt.Fprintf(&b, " One reading says this is not the run it looks like: %s.", faults[0])
	default:
		fmt.Fprintf(&b, " %d readings say this is not the run it looks like: %s.", n, strings.Join(faults, "; and "))
	}
	return b.String()
}

// A tally counts one kind of bad row and remembers the first, since one broken
// exporter writes the same fault onto every line it produced.
type tally struct {
	n     int
	first string
}

func (t *tally) add(what string) {
	if t.n == 0 {
		t.first = what
	}
	t.n++
}

func (t tally) say(one, many string) string {
	f := one
	if t.n > 1 {
		f = many
	}
	switch {
	case t.n == 0:
		return ""
	case !strings.Contains(f, "%"):
		return f
	}
	return fmt.Sprintf(f, t.n, t.first)
}

// said drops the empty sentences a tally hands back when its kind never fired.
func said(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func share(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
