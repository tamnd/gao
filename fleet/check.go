package fleet

// Checking the recorded inventory against the box the process is running on.
//
// The inventory is data with a date on it, and the date is there because the
// numbers move. They moved: the fleet was recorded on 2026-08-03, and two weeks
// later one box had lost 26 GB of free disk and another had gained 70. That is
// not a bookkeeping problem. A plan reads the recorded number, and 'gao fleet
// peak' printed a fault sentence saying a box had 44.3 GB free while the box
// had 18.2, which is a sentence somebody would have acted on.
//
// So a box can be asked what it actually has. Only the numbers that move are
// measured here: free disk, which moves hourly, and hardware threads, which
// move when a machine is resized. Memory and the card are read off the box the
// same way they were read the first time, by hand, because they change when
// somebody changes them and a wrong answer from a portable guess is worse than
// an old answer with a date on it.

import (
	"fmt"
	"runtime"
)

// DriftBytes is how far free disk may move before the record is worth retaking.
//
// It is not a tolerance on the measurement, which is exact. It is the point
// where the recorded number would change a decision: the reserve is 20 GB and a
// shard is a couple of gigabytes, so ten is the size of a mistake that matters.
const DriftBytes int64 = 10_000_000_000

// Live is what a box says about itself right now.
type Live struct {
	// Box is the fleet label this was taken on, and it is "unmeasured" when the
	// process is running somewhere that is not on the inventory, which is the
	// same word every other unlabeled number in gao carries.
	Box string `json:"box"`

	// Path is the directory the free disk was measured on, since a box has more
	// than one filesystem and the answer is a property of the one being used.
	Path string `json:"path"`

	Free    int64 `json:"free"`
	Threads int   `json:"threads"`
}

// Now reads what this box has, at the path the work would happen in.
func Now(path string) (Live, error) {
	free, err := freeBytes(path)
	if err != nil {
		return Live{}, fmt.Errorf("may: measuring free disk on %s: %w", path, err)
	}
	return Live{Box: Label(), Path: path, Free: free, Threads: runtime.NumCPU()}, nil
}

// Drift is every way the record disagrees with the box, worst first.
//
// An empty result means the inventory can still be planned against. The
// sentences are written to be read on their own, since the one that matters is
// the one that ends up in a run book.
func (l Live) Drift() []string {
	b, ok := Lookup(l.Box)
	if !ok {
		return []string{"this machine is not on the fleet inventory, so nothing here was planned against it"}
	}

	var out []string
	moved := l.Free - b.FreeDisk
	if moved <= -DriftBytes || moved >= DriftBytes {
		out = append(out, fmt.Sprintf("%s has %s free and the inventory recorded %s on %s, so the record is %s %s",
			b.Name, GB(l.Free), GB(b.FreeDisk), MeasuredOn, GB(abs(moved)), direction(moved)))
	}

	// The reserve is the line that decides whether a box may hold corpus bytes
	// at all, so crossing it is worth its own sentence. A box that has quietly
	// stopped qualifying still appears in the plan with a share of the work.
	was, now := Scratch(b) >= MinScratchBytes, Scratch(Box{FreeDisk: l.Free}) >= MinScratchBytes
	switch {
	case was && !now:
		out = append(out, fmt.Sprintf("%s is recorded as a box that holds corpus bytes and has %s free today, which is under the %s a stage needs on top of the %s reserve, so the plan is handing work to a box that cannot take it",
			b.Name, GB(l.Free), GB(MinScratchBytes), GB(ReserveBytes)))
	case !was && now:
		out = append(out, fmt.Sprintf("%s is recorded as holding no corpus bytes and has %s free today, which is enough to run a stage, so the fleet is larger than the plan thinks",
			b.Name, GB(l.Free)))
	}

	if l.Threads != b.Threads {
		out = append(out, fmt.Sprintf("%s runs %d hardware threads and the inventory recorded %d, so every rate quoted per core against this box is off",
			b.Name, l.Threads, b.Threads))
	}
	return out
}

// Holds reports whether the box can be planned against as recorded.
func (l Live) Holds() bool { return len(l.Drift()) == 0 }

// Verdict is the one line somebody reads.
func (l Live) Verdict() string {
	if _, ok := Lookup(l.Box); !ok {
		return fmt.Sprintf("this machine is not on the fleet inventory, so its %s of free disk is a fact about a laptop rather than about the plan", GB(l.Free))
	}
	why := l.Drift()
	if len(why) == 0 {
		return fmt.Sprintf("%s matches the inventory taken on %s, %s free against %s recorded",
			l.Box, MeasuredOn, GB(l.Free), GB(recorded(l.Box)))
	}
	return fmt.Sprintf("%s has moved since %s, so retake the inventory before planning against it: %s",
		name(l.Box), MeasuredOn, why[0])
}

func recorded(box string) int64 {
	b, _ := Lookup(box)
	return b.FreeDisk
}

func name(box string) string {
	if box == "" || box == "unmeasured" {
		return "this machine"
	}
	return box
}

func direction(moved int64) string {
	if moved < 0 {
		return "high"
	}
	return "low"
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
