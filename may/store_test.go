package may

import (
	"strings"
	"testing"
)

// The rule that no corpus bytes land on server2 is the reason this file exists.
// It has 19.1 GB of free disk, which is under the reserve, so the arithmetic
// says no without anybody having to remember to say it. It gained 11.8 GB
// between the first two inventories and lost 0.7 between the second and the
// third, and the answer did not change either time, which is what a rule made
// of arithmetic buys over a rule made of a sentence.
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

// The reserve decides, and nothing else does. One box is under it as measured
// on 2026-08-19, server2, which has been under it at all three inventories.
// server3 was under it at the second and is over it at the third. Naming any of
// them here would make this test a copy of the inventory, so it asks the
// arithmetic instead: a box over the line works and a box under it does not.
// That is why this test went on passing across a retake that moved a box from
// one side of the line to the other.
func TestTheReserveDecidesWhichBoxesDoCorpusWork(t *testing.T) {
	for _, p := range Placements() {
		if p.Box.FreeDisk-ReserveBytes < MinScratchBytes {
			if p.Holds || p.Workers != 0 || p.Scratch >= MinScratchBytes {
				t.Errorf("%s has %s free, under the reserve plus %s of scratch, and still reads as able to work",
					p.Box.Name, GB(p.Box.FreeDisk), GB(MinScratchBytes))
			}
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

// Peak disk is a constant times the worker count and has nothing to do with how
// large the corpus is. That is the claim offload buys, and it is the reason the
// numbers in this file describe a fleet that can process a corpus fourteen times
// the size of its disk.
func TestPeakDiskDoesNotGrowWithTheCorpus(t *testing.T) {
	for _, p := range Placements() {
		peak := PeakBytes(p.Box)
		if peak > p.Scratch {
			t.Errorf("%s peaks at %s of scratch and has %s", p.Box.Name, GB(peak), GB(p.Scratch))
		}
		if !p.Holds && peak != 0 {
			t.Errorf("%s holds no corpus bytes and peaks at %s", p.Box.Name, GB(peak))
		}
	}

	// The whole fleet at once, against a corpus ten times the size of the one
	// it is built for. The peak is the same number both times, because it is
	// the worker count and not the corpus that sets it.
	var peak int64
	for _, b := range Boxes {
		peak += PeakBytes(b)
	}
	if want := int64(FleetWorkers()) * ShardsPerWorker * ShardBytes; peak != want {
		t.Errorf("the fleet peaks at %s, and %d workers holding %d shards each is %s",
			GB(peak), FleetWorkers(), ShardsPerWorker, GB(want))
	}
	if ten := Plan(10 * TargetTokens); peak >= ten.Compressed/10 {
		t.Errorf("the fleet peaks at %s against a %s corpus, which is not a working set",
			GB(peak), GB(ten.Compressed))
	}
}

// The S1 gate says ingestion on server1 stays under 90 GB. That is a claim about
// a real box and it is checked here before the run rather than during it.
func TestIngestionFitsServer1sBudget(t *testing.T) {
	const budget int64 = 90_000_000_000

	b, ok := Lookup("server1")
	if !ok {
		t.Fatal("server1 is not in the inventory")
	}
	peak := PeakBytes(b)
	if peak == 0 {
		t.Fatal("server1 runs no workers, and it is the box that fetches")
	}
	if peak > budget {
		t.Errorf("ingestion peaks at %s on server1, over the %s the gate allows", GB(peak), GB(budget))
	}
	if peak > Scratch(b) {
		t.Errorf("ingestion peaks at %s on server1, which has %s of scratch", GB(peak), GB(Scratch(b)))
	}
}

// What the S1 ingest actually held, which is the finding this file was wrong
// about in both directions.
//
// server3 fetched three GlotCC files of about 2.1 GB each on 2026-08-18,
// decoded each one to Parquet, pushed the twelve parts and deleted them. The
// trace 'gao harvest hf -watch' wrote peaked at 0.5 GB, which is one part in flight
// and not one file. So the arithmetic here overstates what a streaming stage holds:
// PeakBytes says two 512 MB shards per worker and the run held closer to one.
//
// It also ruled server3 out of the work entirely, because 17.7 GB free was
// under the 20 GB reserve, and the box did the work anyway. Both were correct
// and they were answering different questions. The reserve is headroom for the
// machine, not a working set for the stage: a box with 17.7 GB free can stream
// a corpus through and still be one bad day away from a filesystem nobody can
// log into. What this test said about that was that the fix is disk on server3
// and not a smaller reserve, and that is how it went. server3 read 43.7 GB free
// on 2026-08-19 and came back over the line on the same arithmetic, with
// nothing in the reserve touched to let it in.
//
// What survives the retake is the half about PeakBytes, which is why this test
// no longer asks anything about which boxes are in. It is here so that the next
// person to read PeakBytes knows it is an upper bound that was checked against
// a run rather than a number nobody has weighed.
func TestTheMeasuredIngestHeldLessThanTheArithmeticAllows(t *testing.T) {
	// Off the trace on server3, 341 samples ten seconds apart across 56m37s.
	const measuredPeak int64 = 500_000_000

	// One worker is what the ingest runs, since it is strictly sequential.
	one := PeakBytes(Box{FreeDisk: ReserveBytes + 100*ShardBytes, Threads: 1})
	if one <= measuredPeak {
		t.Errorf("the arithmetic allows %s for one worker and the run held %s, so the budget is no longer an upper bound",
			GB(one), GB(measuredPeak))
	}
	if measuredPeak >= ReserveBytes {
		t.Errorf("the run held %s, which is more than the %s reserve, so streaming does not keep a box out of trouble on its own",
			GB(measuredPeak), GB(ReserveBytes))
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
// takes, so it is asserted rather than assumed. It was 44 on 2026-08-03, 36 on
// 2026-08-18 when server3 crossed the reserve and took its eight workers with
// it, and 44 again on 2026-08-19 when it crossed back. This test failing means
// the fleet changed again and the pass time estimates in the plan changed with
// it, which is a thing to go and read rather than a thing to go and fix.
func TestFleetWorkersMatchesTheBoxes(t *testing.T) {
	var want int
	for _, b := range Boxes {
		want += Workers(b)
	}
	if got := FleetWorkers(); got != want {
		t.Fatalf("FleetWorkers = %d, the boxes add up to %d", got, want)
	}
	if got := FleetWorkers(); got < 32 || got > 48 {
		t.Errorf("the fleet runs %d workers, which is outside the range the pass time estimates assume", got)
	}

	// Where they are matters as much as how many there are. Thirty two of the
	// thirty six are on the one box with a GPU, which is also the box every
	// classifier and every evaluation queues on, so a pass time computed off
	// the fleet total is a pass time that assumes gamingpc is otherwise idle.
	gpu, _ := Lookup("gamingpc")
	if Workers(gpu) < FleetWorkers()/2 {
		t.Errorf("gamingpc runs %d of the fleet's %d workers, and the plan is written around it running most of them",
			Workers(gpu), FleetWorkers())
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
