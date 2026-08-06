package hieu

import (
	"strings"
	"testing"
	"time"
)

// spot is the from scratch run on capacity that gets taken back every four
// hours, writing a resumable checkpoint at 4 GB/s.
func spot() Spot {
	return Spot{
		Checkpoint: Checkpoint{Params: Com().Params(), Bytes: Resumable, Rate: 4e9},
		Mean:       4 * time.Hour,
		Restart:    12 * time.Minute,
		Confirm:    2 * time.Minute,
	}
}

// The two checkpoints get confused constantly, and the difference is a factor of
// seven on every disk this touches.
func TestAPublishableCheckpointCannotResumeARun(t *testing.T) {
	m := Com()
	weights := Checkpoint{Params: m.Params(), Bytes: WeightsOnly, Rate: 4e9}
	resumable := Checkpoint{Params: m.Params(), Bytes: Resumable, Rate: 4e9}
	if got := float64(weights.Size()) / 1e9; got < 60 || got > 62 {
		t.Errorf("bf16 weights of a 30.5B model are %.0f GB", got)
	}
	if got := float64(resumable.Size()) / 1e9; got < 424 || got > 428 {
		t.Errorf("a resumable checkpoint of the same model is %.0f GB", got)
	}
	if resumable.Cost() <= weights.Cost() {
		t.Error("the bigger checkpoint did not take longer to write")
	}
	if (Checkpoint{Params: m.Params(), Bytes: Resumable}).Cost() != 0 {
		t.Error("a checkpoint with no write rate on it reported a time to write")
	}
}

// Daly's optimum, checked the way it is worth checking: against the two ways of
// being wrong on either side of it.
func TestTheIntervalIsBetterThanCheckpointingMoreOrLess(t *testing.T) {
	s := spot()
	best := s.Interval()
	if best < 25*time.Minute || best > 35*time.Minute {
		t.Fatalf("the interval is %s, and a 107 second write against four hours between preemptions puts it near half an hour", best)
	}
	for _, other := range []time.Duration{best / 4, best / 2, 2 * best, 4 * best} {
		if s.Overhead(other) < s.Overhead(best) {
			t.Errorf("checkpointing every %s costs %.3f against %.3f at the recommended %s",
				other, s.Overhead(other), s.Overhead(best), best)
		}
	}
}

// Checkpointing more often costs writes and checkpointing less often costs
// gradient, and the point of the interval is that both terms are in it.
func TestBothWaysOfGettingTheIntervalWrongAreCounted(t *testing.T) {
	s := spot()

	// Every minute: the write is 107 seconds, so the run never stops writing.
	if got := s.Overhead(time.Minute); got < 1 {
		t.Errorf("checkpointing every minute with a 107 second write costs %.2f of wall clock", got)
	}

	// Once a day: nothing is spent writing and a preemption throws away half a
	// day, on capacity that goes away every four hours.
	if got := s.Overhead(24 * time.Hour); got < 1 {
		t.Errorf("checkpointing once a day on four hour capacity costs %.2f of wall clock", got)
	}
	if s.Lost(24*time.Hour) <= s.Lost(time.Hour) {
		t.Error("a longer interval did not put more work at risk")
	}
	if s.Overhead(0) != 0 || s.Lost(0) != 0 {
		t.Error("an interval of nothing produced a number")
	}
}

// Restart and confirmation are not rounding. Twelve minutes to reacquire
// capacity and two for the store to confirm are lost on every preemption
// whatever the interval is, and leaving them out understates the overhead by a
// third here.
func TestWhatIsLostIsMoreThanHalfAnInterval(t *testing.T) {
	s := spot()
	i := s.Interval()
	if s.Lost(i) <= i/2 {
		t.Errorf("the loss per preemption is %s, which is no more than half the %s interval", s.Lost(i), i)
	}
	bare := s
	bare.Restart, bare.Confirm = 0, 0
	if bare.Best() >= s.Best() {
		t.Errorf("a run that restarts instantly and confirms instantly costs %.3f against %.3f", bare.Best(), s.Best())
	}
}

// The regime where no interval works, which is the thing worth detecting rather
// than optimizing inside. The formula still returns a number here, and the
// number describes a run that makes no progress.
func TestAWriteThatOutlastsTheCapacityIsNotAnIntervalProblem(t *testing.T) {
	s := spot()
	s.Mean = 90 * time.Second
	if s.Survives() {
		t.Fatal("a run preempted more often than it can finish a save read as survivable")
	}
	if !strings.Contains(s.Verdict(), "no interval fixes that") {
		t.Errorf("the verdict suggests tuning the interval: %s", s.Verdict())
	}

	// And the regime next to it, where a checkpoint does land but the overhead
	// has eaten what spot capacity was cheaper by.
	tight := spot()
	tight.Mean = 20 * time.Minute
	if tight.Survives() {
		t.Fatal("a run losing most of its wall clock to interruption read as survivable")
	}
	if !strings.Contains(tight.Verdict(), "spot reliability at on demand prices") {
		t.Errorf("the verdict does not say what the overhead costs: %s", tight.Verdict())
	}
}

// A checkpoint the store has not confirmed is not a checkpoint, which is the
// same distinction the rotation is built around and fails the same way.
func TestAnUnconfirmedCheckpointIsNotACheckpoint(t *testing.T) {
	s := spot()
	s.Confirm = 2 * time.Hour
	if s.Survives() {
		t.Fatal("a run whose store confirms slower than it checkpoints read as survivable")
	}
	var found bool
	for _, why := range s.Blocking() {
		if strings.Contains(why, "never more than one unconfirmed save from having no checkpoint at all") {
			found = true
		}
	}
	if !found {
		t.Errorf("the confirmation window is not named: %v", s.Blocking())
	}
}

func TestARunThatDoesNotSayWhatItCostsIsRefused(t *testing.T) {
	var empty Spot
	if empty.Survives() {
		t.Fatal("a run with nothing in it read as survivable")
	}
	if len(empty.Blocking()) != 2 {
		t.Errorf("%d reasons came back: %v", len(empty.Blocking()), empty.Blocking())
	}
	if empty.Interval() != 0 || empty.Best() != 0 {
		t.Error("an interval was computed out of nothing")
	}
}

// The retention budget, against the disk this project actually owns.
func TestTheFleetHoldsOneResumableCheckpointAndSevenPublishableOnes(t *testing.T) {
	const free = 467e9
	s := spot()
	resumable := Retention{Checkpoint: s.Checkpoint, Free: free, Interval: s.Interval()}
	if resumable.Keeps() != 1 {
		t.Errorf("the fleet holds %d resumable checkpoints of 427 GB in 467 GB", resumable.Keeps())
	}
	if !strings.Contains(resumable.Verdict(), "rewound to its last save") {
		t.Errorf("the verdict does not say what one checkpoint of retention means: %s", resumable.Verdict())
	}

	weights := Retention{Checkpoint: Checkpoint{Params: Com().Params(), Bytes: WeightsOnly, Rate: 4e9}, Free: free, Interval: s.Interval()}
	if weights.Keeps() != 7 {
		t.Errorf("the fleet holds %d weight only checkpoints", weights.Keeps())
	}
	if weights.Reach() <= resumable.Reach() {
		t.Error("keeping seven checkpoints did not reach further back than keeping one")
	}

	none := Retention{Checkpoint: Checkpoint{Params: Com().Params(), Bytes: Resumable}, Free: 100e9}
	if none.Keeps() != 0 || !strings.Contains(none.Verdict(), "not one of them fits") {
		t.Errorf("a disk too small for a single checkpoint reported %d: %s", none.Keeps(), none.Verdict())
	}
	if (Retention{Free: free}).Keeps() != 0 {
		t.Error("a retention budget was computed against a checkpoint of no size")
	}
}
