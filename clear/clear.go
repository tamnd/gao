// Package clear is the arithmetic of getting bytes off a box before it fills.
//
// Don is dọn, to clear away, which is the chore rather than the achievement and
// is the right register for what this does. Nothing here produces a corpus. It
// decides whether the machine producing one runs out of disk on a Tuesday night
// with nobody watching.
//
// The fleet has 500 GB of free disk against a corpus of 396 GB compressed, and
// server1 fetches the crawl onto 118 GB of it. That is settled elsewhere: the
// store of record is off-box, in Parquet on the Hub, and a worker writes a
// shard, pushes it, and deletes it. What is not settled by saying so is whether
// the pushing keeps up with the writing, and if it does not then every other
// design decision in this project is downstream of a disk that filled.
//
// Three numbers decide it and only one of them is obvious.
//
// The obvious one is the rate. A crawl writing faster than the uplink can move
// has a deadline rather than a policy, and the deadline is scratch divided by
// the difference. Nothing recovers that: not a bigger disk, which buys hours,
// and not a cleanup pass, which cannot delete what has not been uploaded.
//
// The second is that a file cannot be pushed while it is being written. WARCs
// roll at a fixed size, so the box always holds one open file nobody can take,
// and shrinking that volume to make the number look better means more upload
// requests, more objects, and a store listing nobody can read.
//
// The third is the one that gets left out of capacity plans and is where data
// actually goes missing. A pushed file is not a safe file. Between the upload
// finishing and the store confirming it holds those exact bytes there is a
// window, and anything deleted inside that window is gone with nobody yet aware
// it was ever there. So the steady state on disk is not the open file, it is the
// open file plus everything written during one push and one confirmation, and
// that is the number that has to fit.
//
// What follows from all three is a rule rather than a threshold: when the disk
// reaches the mark, the crawl pauses. It does not delete unverified bytes to
// keep fetching. Fetching is repeatable and losing a WARC nobody has a copy of
// is not, and a crawler that resolves that trade the other way is one that
// silently produces a smaller corpus than its own logs claim.
package clear

import (
	"fmt"
	"time"

	"github.com/tamnd/gao/fleet"
)

// The rotation the crawl is planned against.
//
// Every number is a rate or a size, and every one of them is arguable. They are
// constants so that the argument happens against arithmetic rather than against
// a feeling, and so that changing one prints a different answer instead of
// requiring a new estimate.
const (
	// FetchesPerSecond is the crawl's target rate across all hosts. It is what
	// 700M fetches in a season works out to, and it is set by politeness and the
	// per host delay rather than by anything server1 cannot do, since fetching is
	// network bound and the box has four threads doing very little.
	FetchesPerSecond = 200.0

	// RecordBytes is the mean size of one fetch as it lands in the WARC:
	// response body compressed, plus the request, the headers, and the record
	// framing. Vietnamese web pages are not smaller than anybody else's.
	RecordBytes int64 = 26_000

	// VolumeBytes is how much a WARC takes before it is closed and becomes
	// something that can be uploaded. One gigabyte is the convention and it is a
	// tradeoff rather than a preference: smaller volumes mean less disk held
	// open and more objects in the store, and a listing with two million entries
	// in it is a listing nobody reads twice.
	VolumeBytes int64 = 1_000_000_000

	// UplinkBytesPerSecond is what server1 sustains outbound, not what its link
	// is sold as. It is the conservative end of what a shared VPS uplink does
	// over hours rather than the number from a thirty second test.
	UplinkBytesPerSecond int64 = 12_500_000

	// ConfirmLag is how long after an upload finishes before the store is known
	// to hold those bytes: the commit, and then a read back that says the object
	// is there and hashes to what was sent. It is generous on purpose. Being
	// wrong about this number in the optimistic direction is how a cleanup pass
	// deletes something that never arrived.
	ConfirmLag = 5 * time.Minute

	// HighWater is the share of scratch at which the crawl stops fetching. It is
	// not 100% because the pause has to happen while there is still room for the
	// files already in flight to land, and it is not 50% because scratch that is
	// never used is scratch that was not worth having.
	HighWater = 0.80
)

// A Rotation is one box writing bytes and pushing them off, and the question of
// whether those two keep up with each other.
type Rotation struct {
	// Box is the machine, which is where the disk number comes from. It is the
	// whole box rather than a byte count so that a rotation says which machine
	// it is about, since the same arithmetic gives different answers on server1
	// and on server2 and one of those answers is no.
	Box fleet.Box

	// Fetches is requests per second and Record is the mean bytes each one adds
	// to the WARC. They are separate because the two get revised for different
	// reasons: the rate is a politeness decision and the size is a measurement.
	Fetches float64
	Record  int64

	// Volume is the size a WARC reaches before it is closed.
	Volume int64

	// Uplink is bytes per second off the box.
	Uplink int64

	// Confirm is the wait between an upload finishing and the store being known
	// to hold it. Bytes cannot be reclaimed during it, which is the only reason
	// it appears in a disk calculation at all.
	Confirm time.Duration
}

// Target is the rotation the crawl is planned against, on the box that runs it.
func Target() Rotation {
	server1, ok := fleet.Lookup("server1")
	if !ok {
		panic("don: server1 is not on the fleet inventory")
	}
	return Rotation{
		Box:     server1,
		Fetches: FetchesPerSecond,
		Record:  RecordBytes,
		Volume:  VolumeBytes,
		Uplink:  UplinkBytesPerSecond,
		Confirm: ConfirmLag,
	}
}

// Fill is bytes per second arriving on disk.
func (r Rotation) Fill() float64 { return r.Fetches * float64(r.Record) }

// Scratch is the disk this rotation may use, which is the box's free disk less
// the reserve that keeps the machine loggable into.
func (r Rotation) Scratch() int64 { return fleet.Scratch(r.Box) }

// Mark is the disk at which the crawl stops fetching.
func (r Rotation) Mark() int64 { return int64(HighWater * float64(r.Scratch())) }

// Rotate is how often a volume closes and becomes something that can be pushed.
func (r Rotation) Rotate() time.Duration {
	if r.Fill() <= 0 {
		return 0
	}
	return seconds(float64(r.Volume) / r.Fill())
}

// Push is how long one volume takes to upload.
func (r Rotation) Push() time.Duration {
	if r.Uplink <= 0 {
		return 0
	}
	return seconds(float64(r.Volume) / float64(r.Uplink))
}

// Flight is how many closed volumes are on disk at once: uploading, or uploaded
// and not yet confirmed.
//
// It is a count of whole files rather than a byte figure because that is what
// disk actually holds. A volume half uploaded still occupies all of itself.
func (r Rotation) Flight() int {
	if r.Rotate() <= 0 {
		return 0
	}
	span := r.Push() + r.Confirm
	n := int(span / r.Rotate())
	if span%r.Rotate() != 0 {
		n++
	}
	return n
}

// Held is what the box holds in steady state: the volume being written, and
// every volume written during one push and one confirmation.
//
// This is the number the plan has to fit, and it is the one people leave out.
// A capacity plan that counts only the open file is a plan that assumes a push
// is instant and a confirmation is free, and it is wrong by however long the
// store takes to answer.
func (r Rotation) Held() int64 {
	if r.Fill() <= 0 {
		return 0
	}
	return int64(1+r.Flight()) * r.Volume
}

// Keeps reports whether the uplink moves bytes at least as fast as the crawl
// writes them. When it does not, nothing else here matters: the disk has a
// deadline and [Rotation.Full] says when.
func (r Rotation) Keeps() bool { return float64(r.Uplink) >= r.Fill() }

// Full is how long the box lasts when the uplink cannot keep up.
//
// It is zero when the rotation keeps up, which is a different thing from a
// short time and is why this returns a duration and a bool rather than a
// duration somebody has to remember to interpret.
func (r Rotation) Full() (time.Duration, bool) {
	net := r.Fill() - float64(r.Uplink)
	if net <= 0 {
		return 0, false
	}
	return seconds(float64(r.Mark()) / net), true
}

// Outage is how long the store can be unreachable before the crawl has to stop
// fetching.
//
// This is the number that decides whether a store outage is an incident or a
// thing that resolved itself overnight, and it is the sentence in the milestone
// about 111 GB being a few hours of fetching, computed rather than asserted.
func (r Rotation) Outage() time.Duration {
	if r.Fill() <= 0 {
		return 0
	}
	return seconds(float64(r.Mark()) / r.Fill())
}

// Fits reports whether the steady state holds within the mark.
func (r Rotation) Fits() bool { return len(r.Blocking()) == 0 }

// Blocking is every reason this rotation does not work, in full sentences, and
// empty when it does.
//
// A list rather than an error, because a rotation can fail two ways at once and
// fixing the first one then discovering the second is how an afternoon goes.
func (r Rotation) Blocking() []string {
	var out []string
	switch {
	case r.Fill() <= 0:
		out = append(out, "this rotation writes nothing, so it is a description of an idle box rather than a plan")
	case r.Uplink <= 0:
		out = append(out, fmt.Sprintf("%s has no route off the box, so %s of scratch is the whole crawl",
			r.Box.Name, fleet.GB(r.Scratch())))
	}
	if r.Volume > r.Mark() {
		out = append(out, fmt.Sprintf("one volume is %s and the mark on %s is %s, so the box fills before the first file is even closed",
			fleet.Size(r.Volume), r.Box.Name, fleet.Size(r.Mark())))
	}
	if full, ok := r.Full(); ok {
		out = append(out, fmt.Sprintf("the crawl writes %s per second and the uplink moves %s per second, so the disk reaches the mark in %s and no cleanup pass recovers that",
			fleet.Size(int64(r.Fill())), fleet.Size(r.Uplink), span(full)))
	}
	if held := r.Held(); held > r.Mark() {
		out = append(out, fmt.Sprintf("steady state holds %s, which is over the %s mark on %s, because a push takes %s and a confirmation takes %s and nothing may be deleted in between",
			fleet.Size(held), fleet.Size(r.Mark()), r.Box.Name, span(r.Push()), span(r.Confirm)))
	}
	return out
}

// Verdict is the answer in one sentence, which is what a person reads and what
// a commit message quotes.
func (r Rotation) Verdict() string {
	if reasons := r.Blocking(); len(reasons) > 0 {
		return "the crawl does not start: " + reasons[0]
	}
	return fmt.Sprintf("%s holds %s in steady state against a %s mark, and the store can be unreachable for %s before fetching has to stop",
		r.Box.Name, fleet.Size(r.Held()), fleet.Size(r.Mark()), span(r.Outage()))
}

// seconds converts a float number of seconds to a duration without the overflow
// a direct multiplication invites when a rate is near zero.
func seconds(f float64) time.Duration {
	const maxSeconds = float64(1 << 40)
	if f <= 0 {
		return 0
	}
	if f > maxSeconds {
		f = maxSeconds
	}
	return time.Duration(f * float64(time.Second))
}

// span writes a duration the way somebody reading a capacity plan wants it,
// which is never in nanoseconds and rarely to three decimal places.
func span(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	case d >= 90*time.Minute:
		return fmt.Sprintf("%.1f hours", d.Hours())
	case d >= 90*time.Second:
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	default:
		return fmt.Sprintf("%.0f seconds", d.Seconds())
	}
}
