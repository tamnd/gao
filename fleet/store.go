package fleet

import (
	"fmt"
	"os"
)

// The store of record.
//
// The corpus does not fit on the fleet. That is measured, not feared: 300
// billion natural tokens is about 1279 GB of extracted text, 618 GB compressed,
// and the four boxes have 524 GB of free disk between them. Even the compressed
// corpus on the largest single pool, 298 GB on gamingpc, leaves no room to
// process it, because a stage reads a shard and writes a shard.
//
// So the bytes live off-box and the fleet holds a working set. Three parts of
// that are the decision.
//
// Off-box rather than on more disk. Buying disks for four rented machines is
// buying disks that cannot be moved, cannot be shared between the boxes, and
// are gone when the machine is. The corpus outlives the fleet, and the store of
// record is the thing the corpus is, so it does not live on a machine that is a
// monthly invoice away from disappearing.
//
// Object storage rather than a filesystem. Every access pattern in this project
// is a whole shard read or a whole shard written, addressed by name, from
// several machines at once, with no rename, no partial update, and no locking.
// That is object storage exactly. A network filesystem would give us POSIX
// semantics we do not use in exchange for a mount that breaks a crawl at three
// in the morning.
//
// Dataset repos on the Hugging Face Hub rather than a bucket. This is the part
// that was left open, and it turned out to have a better answer than a
// provider. The bytes are already going to the Hub, because that is where a
// published Vietnamese corpus has to be for anybody to use it, and a bucket
// would mean paying to store the same data twice and paying egress to move it
// between the two. Parquet under a snapshot prefix is queryable in place, so a
// question about a column costs one column rather than a download, and the same
// path serves the fleet, the release, and the reader. The layout lives in kho
// and the pattern comes from ccrawl-cli, which already publishes down it.
//
// What lives on the fleet is scratch: the shards a stage is working on right
// now, plus the WARCs server1 has fetched and not yet uploaded. A worker writes
// a shard, pushes it, and deletes it, so peak disk is two shards per worker no
// matter how large the corpus gets, and that is what lets a box with 189 GB free
// take part in processing a terabyte. Nothing on the fleet is authoritative and
// nothing on the fleet is backed up, because everything on it can be refetched
// from the store or, in the crawl's case, is uploaded before it is deleted.

const (
	// StoreEnv names the environment variable holding the store of record, as a
	// URI such as hf://open-index for a real run or file:///mnt/gao for a local
	// one. Nothing in the code has a default, because a stage that silently
	// wrote to the wrong place is worse than a stage that refused to start, and
	// naming the Hub as the answer is a decision rather than a value to fall
	// back on.
	StoreEnv = "GAO_STORE"

	// ReserveBytes is disk left alone on every box, no matter what the numbers
	// say is free. A machine that fills its root filesystem stops being a
	// machine you can log into, and the crawl runs unattended for weeks.
	ReserveBytes int64 = 20_000_000_000

	// MinScratchBytes is the least scratch a box needs to do corpus work at all:
	// two shards in, two shards out. Below this a box can still run the control
	// plane and cannot run a stage.
	MinScratchBytes = 4 * ShardBytes

	// ShardsPerWorker is the disk one worker holds at once, which is the shard
	// it is reading and the shard it is writing. It is a constant rather than a
	// function of the corpus size, and that is the entire consequence of pushing
	// a finished shard off-box before starting the next one.
	ShardsPerWorker = 2
)

// Scratch is the disk a box can spend on corpus work, which is its free disk
// less the reserve.
func Scratch(b Box) int64 {
	if n := b.FreeDisk - ReserveBytes; n > 0 {
		return n
	}
	return 0
}

// HoldsCorpus reports whether a box has enough scratch to run a pipeline stage.
//
// It is false for two of the four boxes as measured on 2026-08-18, and that is
// the point of having the function. server2 has never had the disk. server3
// lost 26.6 GB between the first inventory and the second and crossed the line
// without anybody deciding to take it out of the pipeline. A rule that lives in
// a sentence somewhere gets broken the first time somebody needs a machine in a
// hurry, and a rule nobody remeasures gets broken without anybody noticing.
func HoldsCorpus(b Box) bool {
	return Scratch(b) >= MinScratchBytes
}

// WorkingShards is how many shards a box can hold at once.
func WorkingShards(b Box) int {
	return int(Scratch(b) / ShardBytes)
}

// Workers is how many stage workers a box should run in parallel.
//
// It is the smaller of its hardware threads and half its working shards, since
// a worker reads a shard and writes a shard and needs room for both. On this
// fleet the thread count is the binding constraint on the two boxes that are
// over the reserve, and on the two that are under it the answer is zero, which
// is the correct answer rather than a degenerate one.
func Workers(b Box) int {
	if !HoldsCorpus(b) {
		return 0
	}
	if n := WorkingShards(b) / ShardsPerWorker; n < b.Threads {
		return n
	}
	return b.Threads
}

// PeakBytes is the most disk a box holds at once while a stage is running, which
// is [ShardsPerWorker] shards for every worker it runs.
//
// It does not depend on the size of the corpus, and that is the whole point:
// every stage pushes a finished shard to the store and deletes it before it
// takes the next one, so the number below is what a box needs to process a
// terabyte and it is also what it needs to process ten.
func PeakBytes(b Box) int64 {
	return int64(Workers(b)) * ShardsPerWorker * ShardBytes
}

// Placement is what one box can contribute to a pipeline run.
type Placement struct {
	Box     Box
	Scratch int64
	Shards  int
	Workers int
	Holds   bool
}

// Placements returns the placement for every box, in fleet order.
func Placements() []Placement {
	out := make([]Placement, 0, len(Boxes))
	for _, b := range Boxes {
		out = append(out, Placement{
			Box:     b,
			Scratch: Scratch(b),
			Shards:  WorkingShards(b),
			Workers: Workers(b),
			Holds:   HoldsCorpus(b),
		})
	}
	return out
}

// FleetWorkers is how many stage workers the whole fleet can run at once, which
// is the number that sets how long a pass over the corpus takes.
func FleetWorkers() int {
	var n int
	for _, b := range Boxes {
		n += Workers(b)
	}
	return n
}

// Store returns the store of record from the environment.
//
// There is no default and there will not be one. Every stage writes through the
// store, and a stage that started against an unconfigured store would either
// fill a box's disk or write a snapshot nobody can find later.
func Store() (string, bool) {
	s, ok := os.LookupEnv(StoreEnv)
	return s, ok && s != ""
}

// ErrNoStore is what a command reports when the store of record is not set.
var ErrNoStore = fmt.Errorf("fleet: the store of record is not set, so nothing knows where the corpus lives: set %s to a URI such as hf://open-index", StoreEnv)
