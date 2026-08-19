// Package assign is to hand over: which box fetches which file of the ingest, and
// what the whole ingest costs in wall clock once it is split across the fleet.
//
// The manifest pins 122 files and 513.6 GB, and what decides how long that takes
// is usually not the link. A decoding ingest fetches a record, puts it to the
// ingest contract, tokenizes it and writes Parquet, and the fastest reading on
// this fleet is 4.2 GB of Vietnamese in 40m09s. So the number a schedule needs
// is what a box got through end to end rather than what its link can do, and
// every reading here is that number, taken off a run that happened. A schedule
// built on a link speed is a schedule that finishes on paper.
//
// A reading is a box and a source together and not a property of the box. The
// three taken on 2026-08-18 were taken on three different sources, because that
// is how the ingest was split, and FinePDFs compresses at 1.07 where GlotCC
// compresses at 2.07, so part of the gap between two boxes here is the shape of
// what each was fetching. The schedule uses them anyway, because a reading off
// the wrong source is still a run that happened and the alternative is a guess,
// but it is why 'gao assign plan' prints how each one was taken rather than only
// the rate it works out to.
//
// Two things then make the split easy to get wrong. The first is that the files
// are not the same size. The largest pinned file is 26.6 GB and the median is
// 2.1 GB, a thirteenth of it, so dividing 122 files into equal piles leaves one
// box working for a day after the others have gone quiet, and a file cannot be
// cut in half because it is streamed and hashed as one unit. The second is that
// the sources cannot all be fetched at once. HPLT v3 is pinned at order zero and
// ingests alone, because every later source dedups against a store that already
// holds it and the retention numbers are only reproducible against a fixed
// reference. So the schedule is a sequence of groups rather than one pile, every
// box waits at the end of each group, and the idle time that produces is a cost
// of the ingest order rather than a mistake in the arithmetic.
//
// This prices both. It divides each order group across the boxes that may hold
// corpus bytes, heaviest file first onto whichever box would finish it soonest,
// and reports the wall clock against the split that would be possible if files
// were divisible and the order did not bind. Nothing can reach that floor. It is
// there so that the gap between it and the schedule can be read as what it is,
// which is the shape of the work rather than a shortage of machines.
//
// Boxes that may hold corpus bytes is [fleet.HoldsCorpus], and for one inventory
// it excluded the box carrying the fastest reading. server3 had 17.7 GB free
// against a 20.0 GB reserve, so it had no scratch at all and drew nothing here,
// and it was also the box that fetched, decoded and published the whole of the
// GlotCC ingest on 2026-08-18, 1,500,000 documents and 12.6 GB of text into
// 6.1 GB of Parquet. Both of those were true, because [fleet.HoldsCorpus] asks
// whether a box can hold a stage's working set, which is four shards, and a
// fetch holds [InFlight], which is two, so the schedule answers a larger
// question than the one it is asking about.
//
// The reserve was not adjusted to let that box back in. A safety number that
// moves the first time it excludes a machine somebody wanted is not a safety
// number. What happened instead is that 'gao fleet check' was run on all four
// boxes on 2026-08-19 and said server3 had 43.7 GB free, 26.0 GB more than the
// record, in the sentence the drift check exists to print: the fleet is larger
// than the plan thinks. The inventory was retaken and server3 draws 48% of the
// ingest here on the same arithmetic that had been giving it nothing.
//
// That is the argument for keeping the two numbers apart. A gate made of
// arithmetic over a dated measurement lets a box out and back in without
// anybody editing a rule, and the only thing needed to notice was running the
// check. 'gao assign plan' still prints the free disk, the scratch after the
// reserve, what a stage needs and what a fetch holds for every box it drops,
// because a box dropped without its numbers looks like a bug. server2 is the
// one it drops now, and it has been under the reserve at all three inventories.
package assign

import (
	"fmt"
	"sort"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/harvest"
)

const (
	// MinSample is the least a reading may be taken over. A rate measured across
	// a hundred megabytes is a measurement of the first minute of a run, which is
	// the minute the congestion window spends growing, the page cache spends
	// filling, and the host spends deciding how much it likes you.
	MinSample int64 = 1_000_000_000

	// Margin is how far past its own floor a group has to run before it is
	// reported as ending late. A group a hair over costs minutes and reads as a
	// rounding difference, which is not worth a sentence in a run book.
	Margin = 1.10
)

// A Reading is what one box actually got through, measured rather than assumed.
//
// It is pinned input bytes against wall clock, so a box held up by its link and
// a box held up by its tokenizer produce the same shape of number, which is the
// only shape a schedule can use. Reading it off the ledger of a real run is what
// makes it that: the ledger records a finished file and when it finished, and
// the manifest already says how big the file was.
type Reading struct {
	// Box is the fleet label the reading was taken on.
	Box string `json:"box"`

	// Bytes and Seconds are what it was measured across. They are kept as the
	// two numbers rather than as their quotient, since a rate off eleven
	// gigabytes and a rate off eleven megabytes are the same quotient and not
	// the same claim.
	Bytes   int64   `json:"bytes"`
	Seconds float64 `json:"seconds"`

	// On is when, in ISO 8601. A throughput figure without a date is one nobody
	// should book weeks against.
	On string `json:"measured_on"`

	// How is what produced it, which is the difference between a reading off a
	// real ingest and a reading off a speed test to a nearby city.
	How string `json:"how"`
}

// Rate is the reading in bytes of pinned input per second.
func (l Reading) Rate() float64 {
	if l.Seconds <= 0 {
		return 0
	}
	return float64(l.Bytes) / l.Seconds
}

// A Job is one pinned file to fetch.
type Job struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`

	// Order is the ingest order of the source it belongs to. Jobs of different
	// orders are never in flight at the same time.
	Order int `json:"order"`
}

// Jobs is every file the manifest pins and does not drop, heaviest first inside
// each order group.
//
// Heaviest first is the assignment order and not a presentation choice. Handing
// out the big files while every box is still empty is what keeps the last file
// from being the one nobody has room in the schedule for.
func Jobs() []Job {
	var out []Job
	for _, p := range harvest.Sources() {
		for _, f := range p.Files {
			out = append(out, Job{Source: string(p.Source), Path: f.Path, Bytes: f.Bytes, Order: p.Order})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// A Hand is one box's share of one order group.
type Hand struct {
	Box     string  `json:"box"`
	Jobs    []Job   `json:"jobs"`
	Bytes   int64   `json:"bytes"`
	Seconds float64 `json:"seconds"`
}

// A Group is one ingest order fetched across the fleet, which every box waits
// at the end of.
type Group struct {
	Order   int      `json:"order"`
	Sources []string `json:"sources"`
	Files   int      `json:"files"`
	Bytes   int64    `json:"bytes"`
	Hands   []Hand   `json:"hands"`
}

// Makespan is how long the group takes, which is how long its slowest box
// takes, since the next group cannot start until this one is in the store.
func (g Group) Makespan() float64 {
	var most float64
	for _, h := range g.Hands {
		if h.Seconds > most {
			most = h.Seconds
		}
	}
	return most
}

// Idle is the time a box spends waiting at the end of the group, summed over
// the boxes, which is what the ingest order and the file sizes cost together.
func (g Group) Idle() float64 {
	var idle float64
	for _, h := range g.Hands {
		idle += g.Makespan() - h.Seconds
	}
	return idle
}

// Last is the hand every other box waits on, which is the one whose finish is
// the group's finish.
func (g Group) Last() Hand {
	var last Hand
	for _, h := range g.Hands {
		if h.Seconds >= last.Seconds {
			last = h
		}
	}
	return last
}

// A Split is the whole ingest divided across the fleet.
type Split struct {
	Readings []Reading `json:"readings"`
	Group    []Group   `json:"groups"`

	// Files and Bytes are what the manifest asks for, which is what the split
	// has to add back up to.
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`

	// Idle is every box with a reading that may not hold corpus bytes, so that a
	// measurement taken on one is reported as unusable rather than quietly
	// dropped.
	Idle []string `json:"idle,omitempty"`

	// Unplaced is every pinned file no box has the scratch to fetch. It is a
	// list rather than a count because the answer to it is either a bigger disk
	// or a smaller shard, and which one depends on the file.
	Unplaced []Job `json:"unplaced,omitempty"`

	rates map[string]float64
}

// Divide hands every pinned file to a box, one order group at a time.
//
// Inside a group the heaviest file goes to whichever box would have it soonest,
// which is the box's rate against the work it has already been given. That is
// the longest processing time rule, and on machines that differ in speed it is
// the thing that keeps the slow box from drawing a file it will still be
// fetching after the fast one has finished the source.
//
// A box is only offered a file it has the disk to fetch, which is [InFlight]
// and not the size of the file. That is not a tie break, it is the difference
// between a plan and a plan that runs.
func Divide(readings []Reading) Split {
	s := Split{Readings: readings, rates: map[string]float64{}}

	var usable []Reading
	for _, l := range readings {
		b, ok := fleet.Lookup(l.Box)
		switch {
		case !ok:
			continue
		case !fleet.HoldsCorpus(b):
			s.Idle = append(s.Idle, b.Name)
		default:
			l.Box = b.Name
			usable = append(usable, l)
			s.rates[b.Name] = l.Rate()
		}
	}
	sort.Slice(usable, func(i, j int) bool {
		if usable[i].Rate() != usable[j].Rate() {
			return usable[i].Rate() > usable[j].Rate()
		}
		return usable[i].Box < usable[j].Box
	})

	byOrder := map[int][]Job{}
	var orders []int
	for _, j := range Jobs() {
		if _, seen := byOrder[j.Order]; !seen {
			orders = append(orders, j.Order)
		}
		byOrder[j.Order] = append(byOrder[j.Order], j)
		s.Files++
		s.Bytes += j.Bytes
	}
	sort.Ints(orders)

	for _, order := range orders {
		g := Group{Order: order}
		seen := map[string]bool{}
		for _, j := range byOrder[order] {
			g.Files++
			g.Bytes += j.Bytes
			if !seen[j.Source] {
				seen[j.Source] = true
				g.Sources = append(g.Sources, j.Source)
			}
		}
		for _, l := range usable {
			g.Hands = append(g.Hands, Hand{Box: l.Box})
		}
		for _, j := range byOrder[order] {
			pick, soonest := -1, 0.0
			for i, h := range g.Hands {
				rate := s.rates[h.Box]
				if rate <= 0 || Room(h.Box) < InFlight {
					continue
				}
				done := h.Seconds + float64(j.Bytes)/rate
				if pick < 0 || done < soonest {
					pick, soonest = i, done
				}
			}
			if pick < 0 {
				s.Unplaced = append(s.Unplaced, j)
				continue
			}
			g.Hands[pick].Jobs = append(g.Hands[pick].Jobs, j)
			g.Hands[pick].Bytes += j.Bytes
			g.Hands[pick].Seconds = soonest
		}
		s.Group = append(s.Group, g)
	}
	return s
}

// Rate is what the split has the given box moving at, in bytes per second.
func (s Split) Rate(box string) float64 { return s.rates[box] }

// Seconds is the whole ingest, which is the groups end to end because the order
// forbids running them together.
func (s Split) Seconds() float64 {
	var total float64
	for _, g := range s.Group {
		total += g.Makespan()
	}
	return total
}

// Perfect is the wall clock the same bytes would take if a file could be split
// down the middle and every source could be fetched at once. Nothing can
// achieve it. It is the denominator that says how much of the schedule is the
// fleet and how much is the shape of the work.
func (s Split) Perfect() float64 {
	var sum float64
	for _, r := range s.rates {
		sum += r
	}
	if sum <= 0 {
		return 0
	}
	return float64(s.Bytes) / sum
}

// Over is the schedule against that floor.
func (s Split) Over() float64 {
	p := s.Perfect()
	if p <= 0 {
		return 0
	}
	return s.Seconds() / p
}

// Alone is how long the whole ingest takes on the single fastest box, which is
// what splitting it is being compared against.
func (s Split) Alone() float64 {
	var best float64
	for _, r := range s.rates {
		if r > best {
			best = r
		}
	}
	if best <= 0 {
		return 0
	}
	return float64(s.Bytes) / best
}

// Boxes is the boxes drawing work, fastest first.
func (s Split) Boxes() []string {
	var out []string
	if len(s.Group) > 0 {
		for _, h := range s.Group[0].Hands {
			out = append(out, h.Box)
		}
	}
	return out
}

// Bytes is what one box fetches across the whole ingest.
func (s Split) BytesFor(box string) int64 {
	var n int64
	for _, g := range s.Group {
		for _, h := range g.Hands {
			if h.Box == box {
				n += h.Bytes
			}
		}
	}
	return n
}

// Blocking is everything that stops this from being a schedule at all. Nothing
// below it is read when there is anything here.
func (s Split) Blocking() []string {
	var why []string
	if len(s.Readings) == 0 {
		why = append(why, "no readings were given, and a schedule built on a rate nobody measured is a wish with hours on it")
	}
	seen := map[string]string{}
	for _, l := range s.Readings {
		b, ok := fleet.Lookup(l.Box)
		if !ok {
			why = append(why, fmt.Sprintf("%s is not on the fleet inventory, so there is nothing to hand files to", l.Box))
			continue
		}
		if prev, dup := seen[b.Name]; dup {
			why = append(why, fmt.Sprintf("%s has two readings, %s and %s, and two rates for one box is a question rather than a measurement", b.Name, prev, l.On))
			continue
		}
		seen[b.Name] = l.On
		switch {
		case l.Seconds <= 0:
			why = append(why, fmt.Sprintf("the reading for %s got through %s in %.0f seconds", b.Name, bytesOf(l.Bytes), l.Seconds))
		case l.Bytes < MinSample:
			why = append(why, fmt.Sprintf("the reading for %s was taken over %s, and a rate measured across less than %s is a measurement of a run starting up rather than of a run", b.Name, bytesOf(l.Bytes), bytesOf(MinSample)))
		case l.How == "":
			why = append(why, fmt.Sprintf("the reading for %s does not say how it was taken, a speed test to a nearby city and an ingest that tokenizes what it fetches are not the same number", b.Name))
		}
	}
	if len(why) == 0 && len(s.Boxes()) == 0 {
		why = append(why, "every box with a reading is one that may not hold corpus bytes, so there is nowhere for the ingest to land")
	}
	return why
}

// Waiting is the groups that end well after they would if the files inside them
// could be cut up.
//
// It is not a fault. A group of three files across three boxes ends when its
// largest file ends however it is divided, and the idle time that leaves is a
// fact about the manifest. The sentence is there to stop somebody reading that
// idle time as a mistake and going looking for a better split that does not
// exist.
func (s Split) Waiting() []string {
	if len(s.Blocking()) > 0 {
		return nil
	}
	var sum float64
	for _, r := range s.rates {
		sum += r
	}
	if sum <= 0 {
		return nil
	}
	var out []string
	for _, g := range s.Group {
		floor := float64(g.Bytes) / sum
		if g.Makespan() <= floor*Margin {
			continue
		}
		last := g.Last()
		on := "nothing"
		if n := len(last.Jobs); n > 0 {
			on = last.Jobs[n-1].Path
		}
		out = append(out, fmt.Sprintf("order %d divides %s across %s and still ends %s after its own floor, because %s finishes last on %s and a file cannot be handed to a second box once it has started",
			g.Order, plural(g.Files, "file"), plural(len(g.Hands), "box"), hours(g.Makespan()-floor), last.Box, on))
	}
	return out
}

// InFlight is the disk one fetch holds while it is running.
//
// It was the size of the file until an ingest was run and watched, on the
// reasoning that a pinned file is streamed and hashed as one unit and therefore
// has to land whole. The second half of that does not follow and never did. No
// gao fetch has landed a file: without -out the bytes are counted and thrown
// away, and with -out and -push what the box holds is the part being written
// and then sent, which is one shard. server3 fetched three GlotCC files on
// 2026-08-18 with a trace running, 6.3 GB in and 6.1 GB of Parquet out across
// 56m33s, and 'gao fleet peak' read 0.5 GB off it.
//
// So a box needs room for a part rather than for a file, and the number is the
// same whichever file it draws, which is why it is a constant. Two shards and
// not one, for the same reason [fleet.ShardsPerWorker] is two: the part being
// closed and the next one opening are not required to be the same part forever.
const InFlight = fleet.ShardsPerWorker * fleet.ShardBytes

// Room is the disk a box has for a file it is fetching, which is its scratch
// less the working set the stage behind the fetch is already holding.
//
// The part being written lands next to the shards the tokenizer is working
// through rather than instead of them, which is what the subtraction is for.
func Room(box string) int64 {
	b, ok := fleet.Lookup(box)
	if !ok {
		return 0
	}
	if n := fleet.Scratch(b) - fleet.PeakBytes(b); n > 0 {
		return n
	}
	return 0
}

// Faults are things about this schedule that should be seen before it is run.
//
// Running late is not one of them. The gap between the schedule and its floor
// is the ingest order and the file sizes, both of which are the manifest's
// rather than the fleet's, and the bytes still have to be fetched either way.
// What is a fault is a box that has been handed something it cannot do.
func (s Split) Faults() []string {
	if len(s.Blocking()) > 0 {
		return nil
	}
	var out []string
	for _, box := range s.Boxes() {
		if s.BytesFor(box) == 0 {
			out = append(out, fmt.Sprintf("%s draws no files at all, so its reading was taken for nothing and the fleet is smaller than it looks", box))
		}
	}
	var most int64
	for _, box := range s.Boxes() {
		if r := Room(box); r > most {
			most = r
		}
	}
	for _, j := range s.Unplaced {
		out = append(out, fmt.Sprintf("%s draws nobody: a fetch holds %s while it runs and the most room any box has is %s",
			j.Path, bytesOf(InFlight), bytesOf(most)))
	}
	return out
}

// Holds says whether the schedule is one to run as written.
func (s Split) Holds() bool { return len(s.Blocking()) == 0 && len(s.Faults()) == 0 }

// Verdict is the sentence that goes in the run book.
func (s Split) Verdict() string {
	if why := s.Blocking(); len(why) > 0 {
		return "This is not a schedule: " + why[0] + "."
	}
	head := fmt.Sprintf("%s over %s across %s takes %s, against %s on the fastest box alone.",
		bytesOf(s.Bytes), plural(s.Files, "file"), plural(len(s.Boxes()), "box"), hours(s.Seconds()), hours(s.Alone()))
	if faults := s.Faults(); len(faults) > 0 {
		if len(faults) == 1 {
			return head + " One reading says this is not the schedule to run: " + faults[0] + "."
		}
		return fmt.Sprintf("%s %d readings say this is not the schedule to run, the first of which is that %s.", head, len(faults), faults[0])
	}
	return fmt.Sprintf("%s That is %.0f%% over a split no arrangement can beat, and the gap is the ingest order and the file sizes rather than the fleet.",
		head, (s.Over()-1)*100)
}
