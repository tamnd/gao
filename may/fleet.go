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
//
// This is the second reading. The first was 2026-08-03 and every free disk
// number in it was wrong fifteen days later: server1 was up 70 GB, server3 down
// 26.6, server2 up 11.8, gamingpc down 32. Run 'gao box check' on a box to be
// told whether the record still describes it.
const MeasuredOn = "2026-08-18"

// Box is one machine on the fleet.
type Box struct {
	// Name is the ssh host alias, which is also the box label used to stamp a
	// measurement.
	Name string

	// Hostname is what the machine calls itself, which is not what we call it.
	// Both are recorded so that a run on a real box labels itself correctly with
	// nothing set in the environment, since the number that goes unlabeled is
	// always the one somebody forgot to label.
	Hostname string

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
		Name: "gamingpc", Hostname: "GamingPC", OS: "windows", Arch: "amd64",
		CPU: "13th Gen Intel Core i9-13900K", Cores: 24, Threads: 32,
		Memory: 68463005696, Disk: 1023249739776, FreeDisk: 297656258560,
		// 24564 MiB, which is what the card reports as total rather than the
		// 24 GiB on the box it came in. Batch size is a function of this number,
		// so it is the reported one and not the advertised one.
		GPU: "NVIDIA GeForce RTX 4090", GPUMemory: 25757614080,
		Role: "the only GPU on the fleet: classifiers, tokenizer, OCR, ASR, embeddings, and every evaluation. Also the Windows box of record, which is why Windows is in the CI matrix rather than a courtesy",
	},
	{
		Name: "server3", Hostname: "vmi3391933", OS: "linux", Arch: "amd64",
		CPU: "AMD EPYC", Cores: 8, Threads: 8,
		Memory: 25199222784, Disk: 414921494528, FreeDisk: 17682468864,
		Role: "box of record for pipeline throughput and memory: the most Linux memory on the fleet. It fell under the reserve between the first inventory and this one, so it holds no corpus bytes until somebody clears 20 GB on it, and that is why the S1 ingest ran there streaming rather than landing",
	},
	{
		Name: "server2", Hostname: "vmi3112167", OS: "linux", Arch: "amd64",
		CPU: "AMD EPYC", Cores: 6, Threads: 6,
		Memory: 12541493248, Disk: 206900281344, FreeDisk: 19753852928,
		Role: "control plane only. It gained 12 GB between the two inventories and is still under the reserve, so no corpus bytes land here, and that is a rule rather than an accident",
	},
	{
		Name: "server1", Hostname: "doge-01", OS: "linux", Arch: "amd64",
		CPU: "AMD EPYC", Cores: 4, Threads: 4,
		Memory: 6213033984, Disk: 419491782656, FreeDisk: 188719312896,
		Role: "fetch and publish: the most free disk of the Linux boxes by a wide margin and a public route, and crawling is network bound rather than memory bound",
	},
}

// Lookup returns the box with the given label, which may be either the name we
// use for it or the name it uses for itself. The match is case insensitive
// because Windows reports its hostname with capitals and nobody types those.
func Lookup(label string) (Box, bool) {
	for _, b := range Boxes {
		if strings.EqualFold(b.Name, label) || strings.EqualFold(b.Hostname, label) {
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

// Size formats a byte count at whatever unit keeps it readable, decimal for the
// same reason [GB] is. It is for numbers whose size is not known in advance,
// such as one shard of a snapshot that might hold a hundred documents or half a
// billion.
func Size(n int64) string {
	switch {
	case n < 1_000:
		return fmt.Sprintf("%d B", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1f kB", float64(n)/1e3)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	default:
		return GB(n)
	}
}
