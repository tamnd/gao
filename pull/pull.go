// Package pull prices getting back into a training run after the host is gone.
//
// Kéo is to pull. The milestone item is one line: resume tested from a
// checkpoint pulled back from the fleet, not only from one sitting on the
// training host. The distinction is the whole item. A resume tested on the
// machine that wrote the checkpoint reads it out of the page cache, never
// crosses a network, never checks it against its digest, and reads it back at
// exactly the rank count that wrote it. Every one of those is a path that will
// not run on the day it matters, because the day it matters the host has been
// reclaimed and the only copy left is the one that streamed off it.
//
// So a resume is recorded as three separate claims and they fail differently.
// The first is that the bytes came back: a digest computed after the pull,
// against the one written with the checkpoint, since the copy it could have
// been compared against was on the machine that is gone. The second is that the
// state came back: the loss at the first step after the resume against the loss
// at the step the checkpoint was written, because a loader that restores the
// weights and quietly drops the optimizer moments produces a run that trains
// fine, recovers over a few hundred steps, and has thrown away whatever the
// moments were worth. The third is that it came back onto different hardware,
// since a reclaimed host is replaced by whatever capacity was free and a
// checkpoint written by sixty four ranks and read by thirty two is a reshard
// rather than a load.
//
// Cost is the fourth thing and it is kept apart from the other three. A resume
// can be perfectly correct and still be a restart nobody can afford, and those
// are different answers. Provisioning, pull and load are what a restart costs
// before a single step is recomputed, and the number worth carrying is that
// against the checkpoint interval, because a run whose restart costs more than
// the interval it checkpoints on spends its capacity restarting.
package pull

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
)

// Noise is the most the loss may move across a resume. Above it the optimizer
// state did not come back, whatever the weights did.
const Noise = 0.01

// Budget is the most of a checkpoint interval a restart may cost in
// provisioning, pull and load. A quarter, because the recomputed steps come out
// of the same interval and the two together are what the run actually loses.
const Budget = 0.25

// StateBytes is what one parameter of a full training state weighs: two bytes
// of weights, four of master weights, and four each of the two Adam moments.
const StateBytes = 14

// Weights is what one parameter weighs on its own, which is what an inference
// export holds and what a resume cannot be started from.
const Weights = 2

// Local is the rate above which a pull did not cross a network. Two gigabytes a
// second is a local disk read or a page cache hit wearing a pull's name.
const Local int64 = 2_147_483_648

// A Resume is one recorded attempt at getting back into a run.
type Resume struct {
	// Step is the step the checkpoint was written at, which is also the step
	// the loss either side of the resume is compared at.
	Step int `json:"step"`

	// From is host, fleet, or store. It is the field the milestone item is
	// about, since a resume from the host skips the pull, the digest and the
	// reshard all three.
	From string `json:"from"`

	// Source is the box or the repository the bytes came from.
	Source string `json:"source"`

	// Instance is what the resume ran on, which is not what the checkpoint was
	// written on and is the reason a reshard happens at all.
	Instance string `json:"instance"`

	Bytes int64 `json:"bytes"`

	// Digest is what was recorded when the checkpoint was written and Verified
	// is what was computed after it came back. There is no third copy.
	Digest   string `json:"digest"`
	Verified string `json:"verified"`

	WroteRanks int `json:"wrote_ranks"`
	ReadRanks  int `json:"read_ranks"`

	// Provision is the wait for a replacement host, Pull is the bytes moving,
	// and Load is reading and resharding them. Lost is the training that has to
	// be recomputed, which is a property of the interval rather than of the
	// resume and is kept out of the restart cost for that reason.
	Provision float64 `json:"provision"`
	Pull      float64 `json:"pull"`
	Load      float64 `json:"load"`
	Lost      float64 `json:"lost"`

	Interval float64 `json:"interval"`

	LossAt    float64 `json:"loss_at"`
	LossAfter float64 `json:"loss_after"`
}

// Rate is the pull in bytes a second.
func (r Resume) Rate() float64 {
	if r.Pull <= 0 {
		return 0
	}
	return float64(r.Bytes) / r.Pull
}

// Restart is what getting back to the last checkpoint costs before a single
// step is recomputed.
func (r Resume) Restart() float64 { return r.Provision + r.Pull + r.Load }

// Cost is the restart and the training that has to be done again.
func (r Resume) Cost() float64 { return r.Restart() + r.Lost }

// Overhead is the restart against the interval it happened inside.
func (r Resume) Overhead() float64 {
	if r.Interval <= 0 {
		return 0
	}
	return r.Restart() / r.Interval
}

// Fits reports whether the restart is affordable at this checkpoint interval.
func (r Resume) Fits() bool { return r.Interval > 0 && r.Overhead() <= Budget }

// Offhost reports whether this resume read a copy the reclaim would have taken.
func (r Resume) Offhost() bool { return r.From != "" && r.From != "host" }

// Matched reports whether the bytes that came back are the bytes that went out.
func (r Resume) Matched() bool { return r.Digest != "" && r.Digest == r.Verified }

// Reshards reports whether the checkpoint was read back at a different rank
// count than it was written at, which is the case a reclaim actually causes.
func (r Resume) Reshards() bool {
	return r.WroteRanks > 0 && r.ReadRanks > 0 && r.WroteRanks != r.ReadRanks
}

// Drift is what the loss did across the resume.
func (r Resume) Drift() float64 { return r.LossAfter - r.LossAt }

// Dropped reports the failure this package is mostly written for: the bytes
// verified and the loss came back higher anyway, which is the optimizer state
// not being restored and is invisible a thousand steps later.
func (r Resume) Dropped() bool {
	return r.Matched() && r.LossAt > 0 && r.LossAfter > 0 && r.Drift() > Noise
}

// Intact reports whether both halves of correctness held.
func (r Resume) Intact() bool {
	return r.Matched() && r.LossAt > 0 && r.LossAfter > 0 && math.Abs(r.Drift()) <= Noise
}

// Blocking is every reason this resume is not evidence that a run can be
// restarted. Params is the model's parameter count, which is what says whether
// the thing that came back was a training state or an inference export.
func (r Resume) Blocking(params int64) []string {
	var why []string
	switch r.From {
	case "":
		why = append(why, fmt.Sprintf(
			"the resume at step %d does not say where it read the checkpoint from, and a checkpoint read off the host that wrote it is the case this item exists to rule out",
			r.Step))
	case "host", "fleet", "store":
	default:
		why = append(why, fmt.Sprintf(
			"step %d was read from %q, which is not somewhere a checkpoint survives a host being reclaimed",
			r.Step, r.From))
	}
	if r.Offhost() && r.Source == "" {
		why = append(why, fmt.Sprintf(
			"step %d was pulled from the %s and does not say which one, and a pull rate with no source on it cannot be read against the link it crossed",
			r.Step, r.From))
	}
	if r.Instance == "" {
		why = append(why, fmt.Sprintf(
			"step %d does not say what it resumed onto, and the rank count the replacement host came back with is the whole of what makes this a reshard",
			r.Step))
	}
	if r.Bytes <= 0 {
		why = append(why, fmt.Sprintf(
			"step %d does not say how large the checkpoint was, so the pull is a duration with nothing to divide into it",
			r.Step))
	} else if params > 0 && r.Bytes*2 < params*StateBytes {
		why = append(why, fmt.Sprintf(
			"step %d pulled back %s against the %s a training state of this model weighs, so what came back is closer to the weights on their own and a resume cannot be started from those",
			r.Step, gigabytes(r.Bytes), gigabytes(params*StateBytes)))
	}
	if r.Digest == "" {
		why = append(why, fmt.Sprintf(
			"step %d recorded no digest when it was written, and the copy it could have been compared against afterwards was on the host that was reclaimed",
			r.Step))
	} else if r.Verified == "" {
		why = append(why, fmt.Sprintf(
			"step %d was not checked against its digest after the pull, which is the one check that a network transfer of %s makes necessary",
			r.Step, gigabytes(r.Bytes)))
	} else if r.Digest != r.Verified {
		why = append(why, fmt.Sprintf(
			"step %d came back as %s against the %s it was written as, so the bytes that arrived are not the bytes that left and there is no third copy to arbitrate",
			r.Step, short(r.Verified), short(r.Digest)))
	}
	if r.Pull <= 0 && r.Offhost() {
		why = append(why, fmt.Sprintf(
			"step %d records no time spent pulling, and a checkpoint that arrived instantly arrived from the page cache",
			r.Step))
	} else if r.Rate() > float64(Local) {
		why = append(why, fmt.Sprintf(
			"step %d moved %s in %.0f seconds, which is %s a second and is a local read rather than a pull",
			r.Step, gigabytes(r.Bytes), r.Pull, gigabytes(int64(r.Rate()))))
	}
	if r.Provision <= 0 && r.Offhost() {
		why = append(why, fmt.Sprintf(
			"step %d prices the pull and not the wait for a host, and an instance that was reclaimed is not replaced the moment it is asked for",
			r.Step))
	}
	if r.Load <= 0 {
		why = append(why, fmt.Sprintf(
			"step %d records no load time, and reading %s back and resharding it across a different rank count is not free",
			r.Step, gigabytes(r.Bytes)))
	}
	if r.Interval <= 0 {
		why = append(why, fmt.Sprintf(
			"step %d does not say what the run checkpoints at, so its restart cost is a number with nothing to be large against",
			r.Step))
	}
	if r.LossAt <= 0 || r.LossAfter <= 0 {
		why = append(why, fmt.Sprintf(
			"step %d records the loss on only one side of the resume, and the whole of the state check is that the two agree",
			r.Step))
	} else if r.Dropped() {
		why = append(why, fmt.Sprintf(
			"step %d came back at %.4f against the %.4f it was written at, and the digest matched, so the bytes are right and something in them was not restored, which is what a loader that keeps the weights and drops the optimizer moments looks like on the way to recovering over a few hundred steps",
			r.Step, r.LossAfter, r.LossAt))
	} else if r.Drift() < -Noise {
		why = append(why, fmt.Sprintf(
			"step %d came back at %.4f, below the %.4f it was written at, which is a resume onto a later checkpoint than the one it says it read",
			r.Step, r.LossAfter, r.LossAt))
	}
	return why
}

// A Drill is every resume recorded for one run.
type Drill struct {
	Run string `json:"run"`

	// Params is the model's parameter count, which is what a checkpoint size is
	// read against.
	Params int64 `json:"params"`

	Resumes []Resume `json:"resumes"`
}

// State is what a full training state of this model weighs.
func (d Drill) State() int64 { return d.Params * StateBytes }

// Offhost is every resume that read a copy the reclaim would have taken.
func (d Drill) Offhost() []Resume { return filter(d.Resumes, Resume.Offhost) }

// Fleet is every resume that read the copy sitting on our own machines, which
// is the one the milestone item names.
func (d Drill) Fleet() []Resume {
	return filter(d.Resumes, func(r Resume) bool { return r.From == "fleet" })
}

// Resharded is every resume that came back onto a different rank count.
func (d Drill) Resharded() []Resume { return filter(d.Resumes, Resume.Reshards) }

// Unaffordable is every off host resume whose restart costs more of the
// checkpoint interval than a restart may.
func (d Drill) Unaffordable() []Resume {
	return filter(d.Offhost(), func(r Resume) bool { return !r.Fits() })
}

// Ranked is the resumes most expensive restart first, since the expensive one
// is what a plan has to be written around.
func (d Drill) Ranked() []Resume {
	out := slices.Clone(d.Resumes)
	slices.SortStableFunc(out, func(a, b Resume) int {
		switch {
		case a.Restart() > b.Restart():
			return -1
		case a.Restart() < b.Restart():
			return 1
		default:
			return a.Step - b.Step
		}
	})
	return out
}

// Worst is the most expensive restart on the run.
func (d Drill) Worst() (Resume, bool) {
	if len(d.Resumes) == 0 {
		return Resume{}, false
	}
	// Taken off the ranking rather than found again, so two resumes at the same
	// cost name the same one in the table and in the verdict.
	return d.Ranked()[0], true
}

// Fastest is the cheapest way back into the run from a copy that survives the
// host, which is the one a live restart would actually read.
func (d Drill) Fastest() (Resume, bool) {
	off := d.Offhost()
	if len(off) == 0 {
		return Resume{}, false
	}
	best := off[0]
	for _, r := range off[1:] {
		if r.Restart() < best.Restart() {
			best = r
		}
	}
	return best, true
}

// Blocking is every reason this drill is not a test of resuming a run.
func (d Drill) Blocking() []string {
	if len(d.Resumes) == 0 {
		return []string{"no resume was recorded, and a resume nobody has run is a code path with a comment above it"}
	}
	var why []string
	if d.Params <= 0 {
		why = append(why, "the run does not say how large the model is, so nothing here can say whether what came back was a training state or an export of the weights")
	}
	seen := map[int]bool{}
	for _, r := range d.Resumes {
		if seen[r.Step] {
			why = append(why, fmt.Sprintf("step %d appears twice, and two readings of one resume are not two resumes", r.Step))
		}
		seen[r.Step] = true
		why = append(why, r.Blocking(d.Params)...)
	}
	if len(d.Offhost()) == 0 {
		why = append(why, "every resume on this run read a checkpoint that was already on the training host, where the pull, the digest and the reshard are all skipped, so the path this item is about has not been run once")
	} else if len(d.Fleet()) == 0 {
		why = append(why, "no resume read the fleet copy, and the fleet copy is the one that is still there after the host is taken back, so it is the copy whose resume has to be the tested one")
	}
	if len(d.Resharded()) == 0 {
		why = append(why, "every resume read the checkpoint back at the rank count that wrote it, which is the layout that already worked, and a reclaimed host is replaced by whatever capacity was free that hour")
	}
	return why
}

// Settled reports whether this is a test of a resume at all.
func (d Drill) Settled() bool { return len(d.Blocking()) == 0 }

// Holds reports whether the run can be restarted and whether the restart is
// affordable, which are two questions and are answered together here because a
// plan needs both.
func (d Drill) Holds() bool {
	if !d.Settled() {
		return false
	}
	for _, r := range d.Offhost() {
		if !r.Intact() {
			return false
		}
	}
	f, ok := d.Fastest()
	return ok && f.Fits()
}

// Verdict is the drill in one sentence.
func (d Drill) Verdict() string {
	if len(d.Resumes) == 0 {
		return "no resume was recorded, so whether this run can be restarted is a thing somebody believes"
	}
	if why := d.Blocking(); len(why) > 0 {
		return why[0]
	}
	f, _ := d.Fastest()
	fleet := d.Fleet()[0]
	if !f.Fits() {
		return fmt.Sprintf(
			"the cheapest way back into %s is %s from the %s, and %.0f%% of a %s checkpoint interval spent provisioning, pulling and loading is a run that restarts more than it trains",
			d.Run, Duration(f.Restart()), f.From, 100*f.Overhead(), Duration(f.Interval))
	}
	return fmt.Sprintf(
		"%s came back from the fleet copy at step %d intact, %d ranks reading what %d wrote, and the cheapest way back in is %s from %s at %.0f%% of a %s checkpoint interval",
		d.Run, fleet.Step, fleet.ReadRanks, fleet.WroteRanks,
		Duration(f.Restart()), f.Source, 100*f.Overhead(), Duration(f.Interval))
}

// ReadDrill loads a drill from a file of one JSON resume per line.
func ReadDrill(run string, params int64, path string) (Drill, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Drill{}, fmt.Errorf("keo: %w", err)
	}
	d := Drill{Run: run, Params: params}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var r Resume
		if err := dec.Decode(&r); err != nil {
			return Drill{}, fmt.Errorf("keo: %s line %d: %w", path, i+1, err)
		}
		d.Resumes = append(d.Resumes, r)
	}
	if len(d.Resumes) == 0 {
		return Drill{}, fmt.Errorf("keo: %s holds no resumes", path)
	}
	return d, nil
}

func filter(in []Resume, keep func(Resume) bool) []Resume {
	var out []Resume
	for _, r := range in {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

func gigabytes(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/(1<<30)) }

// Duration is the restart clock. It is exported because the table and the
// sentence underneath it have to render the same number the same way, and it
// stops at the minute above an hour because a restart measured to the second
// reads as more precise than the thing being measured.
func Duration(seconds float64) string {
	s := int(seconds)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		if s%60 == 0 {
			return fmt.Sprintf("%dm", s/60)
		}
		return fmt.Sprintf("%dm%ds", s/60, s%60)
	case (s/60)%60 == 0:
		return fmt.Sprintf("%dh", s/3600)
	default:
		return fmt.Sprintf("%dh%dm", s/3600, (s/60)%60)
	}
}

// short is the first eight characters of a digest, which is what a person
// reading two of them side by side actually compares.
func short(digest string) string {
	if i := strings.IndexByte(digest, ':'); i >= 0 && i+9 <= len(digest) {
		return digest[:i+9]
	}
	if len(digest) > 8 {
		return digest[:8]
	}
	return digest
}
