// Package may is the fleet: the four boxes gao actually runs on, recorded as
// data rather than as folklore.
//
// A plan checked against imaginary hardware is a plan that discovers its
// constraints at the worst possible moment, usually a hundred million fetches
// into a crawl. So the inventory lives here, in code, with a measurement date on
// it, and the tests assert the consequences that follow from it. The most
// important of those consequences is that the corpus does not fit: 300 billion
// natural tokens is about 1.2 TB of extracted text and the fleet has half a
// terabyte of free disk spread across four machines. Every stage therefore
// streams and works a shard at a time, and the store of record lives off-box.
// That is not an operational detail of one slice, it is a design constraint on
// all of them.
//
// The numbers are measured, not specified. They will drift as disks fill and
// boxes change, and the test that asserts the corpus does not fit is written so
// that it fails the day the fleet grows enough for that to stop being true,
// which is exactly when the plan should be revisited.
package may

import (
	"fmt"
	"os"
	"strings"
)

// MeasuredOn is when the inventory below was taken, in ISO 8601. A capacity
// number without a date is a capacity number nobody should act on.
const MeasuredOn = "2026-08-03"

// Box is one machine on the fleet.
type Box struct {
	// Name is the ssh host alias, which is also the box label used to stamp a
	// measurement.
	Name string

	OS   string
	Arch string
	CPU  string

	// Cores is physical cores and Threads is hardware threads. Both are here
	// because a throughput number quoted against the wrong one is off by two.
	Cores   int
	Threads int

	// Memory is total physical memory in bytes.
	Memory int64

	// Disk and FreeDisk are the working filesystem, in bytes.
	Disk     int64
	FreeDisk int64

	// GPU is empty when there is none. GPUMemory is the card's memory in bytes.
	GPU       string
	GPUMemory int64

	// Role is what this box is for. It follows from the numbers above rather
	// than from preference, and writing it down is what stops somebody planning
	// a training run onto a machine with 7 GB of free disk.
	Role string
}

// HasGPU reports whether the box has a usable accelerator.
func (b Box) HasGPU() bool { return b.GPU != "" }

// Boxes is the fleet, ordered from most capable to least. The order is the one
// the roles are assigned in, so reading the list top to bottom reads as the plan.
var Boxes = []Box{
	{
		Name: "gamingpc", OS: "windows", Arch: "amd64",
		CPU: "13th Gen Intel Core i9-13900K", Cores: 24, Threads: 32,
		Memory: 68463005696, Disk: 1023249739776, FreeDisk: 329700347904,
		GPU: "NVIDIA GeForce RTX 4090", GPUMemory: 25769803776,
		Role: "the only GPU on the fleet: classifiers, tokenizer, OCR, ASR, embeddings, and every evaluation. Also the Windows box of record, which is why Windows is in the CI matrix rather than a courtesy",
	},
	{
		Name: "server3", OS: "linux", Arch: "amd64",
		CPU: "AMD EPYC", Cores: 8, Threads: 8,
		Memory: 25199222784, Disk: 414921494528, FreeDisk: 44280352768,
		Role: "box of record for pipeline throughput and memory: the most Linux memory on the fleet",
	},
	{
		Name: "server2", OS: "linux", Arch: "amd64",
		CPU: "AMD EPYC", Cores: 6, Threads: 6,
		Memory: 12541526016, Disk: 206900281344, FreeDisk: 7972212736,
		Role: "control plane only. Eight gigabytes of free disk means no corpus bytes land here, and that is a rule rather than an accident",
	},
	{
		Name: "server1", OS: "linux", Arch: "amd64",
		CPU: "AMD EPYC", Cores: 4, Threads: 4,
		Memory: 6213033984, Disk: 419491782656, FreeDisk: 118498254848,
		Role: "fetch and publish: the most free disk of the Linux boxes and a public route, and crawling is network bound rather than memory bound",
	},
}

// Lookup returns the box with the given name.
func Lookup(name string) (Box, bool) {
	for _, b := range Boxes {
		if b.Name == name {
			return b, true
		}
	}
	return Box{}, false
}

// Totals is the fleet summed up.
type Totals struct {
	Boxes    int
	Cores    int
	Threads  int
	Memory   int64
	Disk     int64
	FreeDisk int64
	GPUs     int
}

// Total sums the inventory.
func Total() Totals {
	var t Totals
	for _, b := range Boxes {
		t.Boxes++
		t.Cores += b.Cores
		t.Threads += b.Threads
		t.Memory += b.Memory
		t.Disk += b.Disk
		t.FreeDisk += b.FreeDisk
		if b.HasGPU() {
			t.GPUs++
		}
	}
	return t
}

// Largest returns the box with the most free disk. It is the answer to "where
// does this land", and it is deliberately a single box rather than the fleet
// total, because a working set spread across four machines is four working sets.
func Largest() Box {
	best := Boxes[0]
	for _, b := range Boxes[1:] {
		if b.FreeDisk > best.FreeDisk {
			best = b
		}
	}
	return best
}

// Holds reports whether any single box has room for n bytes, and which one. A
// stage that needs more than this has to stream, and finding that out here is
// cheaper than finding it out at 80% of the way through a run.
func Holds(n int64) (Box, bool) {
	b := Largest()
	if b.FreeDisk >= n {
		return b, true
	}
	return Box{}, false
}

// BoxEnv is the environment variable that names the box a run is executing on.
// It exists because a hostname is not provenance: a container, a rented
// instance, and a laptop all have hostnames, and only one of them is a box of
// record.
const BoxEnv = "GAO_BOX"

// Current returns the box this process is running on, from BoxEnv if it is set
// and from the hostname otherwise. The second return is false when the label
// does not name a box on the fleet, which is the signal that any number produced
// here is unmeasured rather than measured.
func Current() (Box, bool) {
	label := strings.TrimSpace(os.Getenv(BoxEnv))
	if label == "" {
		h, err := os.Hostname()
		if err != nil {
			return Box{}, false
		}
		// A fully qualified name still names the box, so take the first label.
		label, _, _ = strings.Cut(h, ".")
	}
	return Lookup(label)
}

// Label returns the provenance label for a number produced by this process. It
// is the box name when the box is on the fleet and "unmeasured" otherwise, which
// is how an unlabeled number stays visibly unlabeled instead of quietly passing
// for a measurement.
func Label() string {
	if b, ok := Current(); ok {
		return b.Name
	}
	return "unmeasured"
}

// GB formats a byte count in decimal gigabytes, the unit storage vendors and
// dataset cards both use, and the one doc/units.go converts against.
func GB(n int64) string {
	return fmt.Sprintf("%.1f GB", float64(n)/1e9)
}
