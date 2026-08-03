package may

import (
	"strings"
	"testing"
)

// The rule that no corpus bytes land on server2 is the reason this file exists.
// It has eight gigabytes of free disk, which is less than the reserve, so the
// arithmetic says no without anybody having to remember to say it.
func TestServer2DoesNoCorpusWork(t *testing.T) {
	b, ok := Lookup("server2")
	if !ok {
		t.Fatal("server2 is not in the inventory")
	}
	if HoldsCorpus(b) {
		t.Errorf("server2 reads as able to hold corpus bytes with %s free", GB(b.FreeDisk))
	}
	if got := Scratch(b); got != 0 {
		t.Errorf("scratch on server2 = %s, want none", GB(got))
	}
	if got := Workers(b); got != 0 {
		t.Errorf("workers on server2 = %d, want 0", got)
	}
}

func TestEveryOtherBoxDoesCorpusWork(t *testing.T) {
	for _, p := range Placements() {
		if p.Box.Name == "server2" {
			continue
		}
		if !p.Holds {
			t.Errorf("%s cannot hold corpus bytes with %s free", p.Box.Name, GB(p.Box.FreeDisk))
		}
		if p.Workers < 1 {
			t.Errorf("%s runs %d workers", p.Box.Name, p.Workers)
		}
		if p.Shards < 2*p.Workers {
			t.Errorf("%s runs %d workers on %d shards of scratch, which is not room to read and write",
				p.Box.Name, p.Workers, p.Shards)
		}
	}
}

// A worker needs room for the shard it reads and the shard it writes, so the
// thread count is only usable if the disk agrees.
func TestWorkersRespectBothThreadsAndDisk(t *testing.T) {
	for _, p := range Placements() {
		if p.Workers > p.Box.Threads {
			t.Errorf("%s runs %d workers on %d threads", p.Box.Name, p.Workers, p.Box.Threads)
		}
		if int64(p.Workers)*2*ShardBytes > p.Scratch {
			t.Errorf("%s runs %d workers with only %s of scratch", p.Box.Name, p.Workers, GB(p.Scratch))
		}
	}
}

func TestScratchLeavesTheReserveAlone(t *testing.T) {
	for _, p := range Placements() {
		if p.Scratch > p.Box.FreeDisk-ReserveBytes && p.Scratch != 0 {
			t.Errorf("%s offers %s of scratch out of %s free, which spends the reserve",
				p.Box.Name, GB(p.Scratch), GB(p.Box.FreeDisk))
		}
	}
}

// The fleet-wide worker count is what sets how long a pass over the corpus
// takes, so it is asserted rather than assumed. Forty-four is the fleet as
// measured, and this test failing means the fleet changed and the pass time
// estimates in the plan changed with it.
func TestFleetWorkersMatchesTheBoxes(t *testing.T) {
	var want int
	for _, b := range Boxes {
		want += Workers(b)
	}
	if got := FleetWorkers(); got != want {
		t.Fatalf("FleetWorkers = %d, the boxes add up to %d", got, want)
	}
	if got := FleetWorkers(); got < 40 || got > 48 {
		t.Errorf("the fleet runs %d workers, which is outside the range the pass time estimates assume", got)
	}
}

// A working set is a working set, and this is the test that fails on the day
// that stops being true and the off-box decision is worth revisiting.
//
// Two claims, because the encouraging number and the useful number are
// different. No single box has scratch for the corpus, which is what rules out
// putting the store of record on gamingpc. And the fleet does not have twice the
// corpus in scratch, which is what rules out spreading it across all four and
// processing it in place, since a stage reads a shard and writes a shard.
func TestTheFleetHoldsAWorkingSetAndNotTheCorpus(t *testing.T) {
	p := Plan(TargetTokens)

	var fleet, largest int64
	for _, pl := range Placements() {
		fleet += pl.Scratch
		if pl.Scratch > largest {
			largest = pl.Scratch
		}
	}
	if largest >= p.Compressed {
		t.Errorf("one box has %s of scratch against a %s corpus, so the store of record could live on it",
			GB(largest), GB(p.Compressed))
	}
	if fleet >= 2*p.Compressed {
		t.Errorf("the fleet has %s of scratch against a %s corpus, so it could hold the corpus and process it in place",
			GB(fleet), GB(p.Compressed))
	}
}

func TestTheStoreOfRecordHasNoDefault(t *testing.T) {
	t.Setenv(StoreEnv, "")
	if _, ok := Store(); ok {
		t.Error("an empty store setting reads as configured")
	}

	t.Setenv(StoreEnv, "s3://gao-store")
	got, ok := Store()
	if !ok {
		t.Fatal("a set store does not read as configured")
	}
	if got != "s3://gao-store" {
		t.Errorf("Store = %q", got)
	}
}

func TestTheMissingStoreErrorSaysWhatToSet(t *testing.T) {
	if !strings.Contains(ErrNoStore.Error(), StoreEnv) {
		t.Errorf("the error does not name the variable to set: %v", ErrNoStore)
	}
}
