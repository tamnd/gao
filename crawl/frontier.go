// Package crawl runs gao's own crawler: the loop that turns a list of hosts
// into fetched pages, and fetched pages into documents and into more URLs.
//
// The parts it is built from already existed and were not wired to anything.
// [github.com/tamnd/gao/frontier] decides what a URL is worth asking for,
// [github.com/tamnd/gao/harvest] knows how to ask for one politely and how to
// write what came back into a WARC, and [github.com/tamnd/gao/store] knows how
// to push a finished part to the hub and delete the local copy. What was missing
// was the thing in the middle: somewhere to keep two hundred and eighty million
// URLs that is not memory, a scheduler that hands them out without asking one
// host for everything at once, and a run that can be killed at any point and
// picked up where it stopped.
//
// The crawl is sized for ten million sites and a billion pages, spread over
// three machines with five gigabytes of memory each. Both halves of that matter:
// the billion is why the frontier is on disk, and the five gigabytes is why the
// resident part of it is a filter and an active set rather than the whole thing.
package crawl

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/frontier"
)

// DefaultBuckets is how many queue files the frontier writes.
//
// A URL goes to the bucket it hashes to and a batch is taken from the buckets in
// rotation, so a page that links to forty pages on one site does not get fetched
// as forty requests in a row to that site. Sixty four is enough mixing for that
// and few enough files to hold every one of them open, which matters more than
// it sounds: the alternative is an open file cache, and a cache in the queue is a
// cache in the one structure that cannot be rebuilt.
const DefaultBuckets = 64

// DefaultBits is bits per URL in the resident filter, matching
// [frontier.Frontier]. At ten bits the filter is wrong about one time in a
// hundred, and being wrong costs a disk read rather than a lost URL.
const DefaultBits = 10

// DefaultExpect is how many URLs the filter is sized for when the caller does
// not say. It is deliberately not the billion: a filter sized for the whole
// crawl on a box doing a ten thousand URL trial run is a gigabyte of untouched
// memory.
const DefaultExpect = 10_000_000

// DefaultPending is how many new hashes are held in memory before they are
// written out as a sorted run. Two million of them is around fifty megabytes of
// map, and it is the number that decides how often the runs are merged.
const DefaultPending = 2_000_000

// DefaultPerHost is how many URLs one host may contribute to one batch. Two,
// because a batch is worked on concurrently and a host is fetched one request at
// a time: a batch that is half one host is a batch that finishes at that host's
// crawl delay times its share, however many workers are watching.
const DefaultPerHost = 2

// DefaultCompact is how many consumed bytes a queue file carries before its head
// is cut off. Without this a queue file is an append only record of every URL
// the crawl ever handed out, which for a billion of them is eighty gigabytes of
// disk holding nothing that will be read again.
const DefaultCompact = 64 << 20

// fanout is how many hashes one fence entry covers in a sorted run. Five hundred
// and twelve of them is a four kilobyte block, which is one read, and the fence
// for a billion hashes is sixteen megabytes.
const fanout = 512

// FrontierOptions configures an [OpenFrontier]. The zero value works and uses
// the defaults named on each field.
type FrontierOptions struct {
	// Dir is where the queue files, the sorted runs and the manifest live. It is
	// created if it is not there. A crawl points this at scratch disk: the
	// frontier is working state, and what gets published is documents.
	Dir string

	// Budget decides what a URL is worth asking for and charges it. Zero means
	// nothing is refused on shape, which is only right for a test or for a seed
	// list somebody else has already filtered.
	Budget *frontier.Budget

	// Shard and Fleet are this box's place in a fleet. A URL whose host belongs
	// to another box is turned away here, unqueued and unremembered, so that
	// every host is crawled by exactly one machine and the politeness schedule
	// on that machine is the whole story for that host. Three boxes each
	// deciding on their own to wait a second between requests is three requests
	// a second to a site that asked for one.
	//
	// The split is on the host and not on the URL, for the same reason. Fleet
	// zero or one turns the split off, which is what a single box wants.
	Shard int
	Fleet int

	// Buckets, Bits, Expect, Pending, PerHost and Compact override the defaults
	// above. Buckets and Bits are fixed at the first open and a later open with
	// different ones is refused, because both of them decide where a URL went.
	Buckets int
	Bits    int
	Expect  int64
	Pending int
	PerHost int
	Compact int64
}

// A Frontier is the crawl's queue and its memory of what it has already asked
// for.
//
// The memory is the part that is hard. Every link on every page has to be
// checked against every URL the crawl has ever seen, a billion of them, on a box
// with five gigabytes. So the exact set is on disk as sorted runs of eight byte
// hashes, with a resident filter in front of it to answer for the URLs that are
// new, which most of them are not. A hit in the filter costs one four kilobyte
// read per run, found through a fence index that holds every five hundred and
// twelfth hash.
//
// The hashes are the first eight bytes of the blake3 of the canonical URL. Two
// URLs out of a billion sharing those eight bytes is about a one in forty
// chance, and what it costs when it happens is one page never fetched. That is
// the right thing to trade for keeping the set at eight gigabytes.
//
// A Frontier is safe for concurrent use. Every worker offers the links it found
// and takes its next batch from the same one, because a per worker idea of what
// has been seen is most of a crawl's duplicate fetches.
type Frontier struct {
	o   FrontierOptions
	dir string

	// mu guards the resident half of the seen set. It is the outermost of the
	// three locks here: a caller holding it may take a bucket's or the runs',
	// and neither of those is ever held while taking this.
	//
	// [Frontier.Next] does not take it. Filling a batch touches the queue files,
	// which have their own locks, the rotation counter, which is an atomic, and
	// the counters, which are atomics too. It used to hold this lock for the
	// whole of a batch, and since a batch is the supply of URLs for every worker
	// on the box, that put the thing offering links behind the thing handing them
	// out.
	//
	// What it does not guard is the runs on disk, which is the point. Everything
	// under it is memory, so the time it is held for is bounded by the machine
	// rather than by a read.
	mu sync.Mutex

	// filter is the resident approximate set, bits is its length in bits.
	//
	// The words are atomics and neither reading nor setting one takes mu. Bits in
	// this thing are only ever set, never cleared, so a reader that misses a set
	// another worker has just made reads the state of a moment slightly before its
	// own, which is the same answer it would have got by arriving slightly
	// earlier. What that buys is the three probes and the two hashes in front of
	// them happening outside the lock, and on a page of sixty links that was most
	// of what the lock was being held for.
	filter []atomic.Uint64
	bits   uint64

	// pending is the hashes offered since the last run was written, held both
	// as a map for the lookup and as a slice so the run can be written in the
	// order they arrived, sorted once.
	pending map[uint64]struct{}
	log     *os.File
	logw    *bufio.Writer

	// spilling is the map a spill has taken away from the crawl and is writing
	// out, and nil when no spill is running.
	//
	// It is here because a hash has to stay findable for the whole of the write.
	// A hash that has left pending and is not in a run yet is a hash the frontier
	// has forgotten, and the URL behind it gets fetched a second time. So the map
	// is set aside rather than cleared, every lookup asks both of them through
	// [Frontier.held], and it is dropped in the same breath as the run being
	// added to the slice.
	//
	// What it costs is that the resident half can hold twice Pending for as long
	// as a write takes, since the crawl goes on offering into a fresh map
	// meanwhile. That is the trade: memory for the two million hashes twice over,
	// against every worker on the box stopping for the length of a merge.
	spilling map[uint64]struct{}

	// spillMu is held for the whole of a spill. Only one runs at a time by
	// construction, since the map is set aside under mu and a second freeze finds
	// spilling already set, so what this is actually for is [Frontier.Close]
	// waiting for a spill to finish rather than closing the files under it.
	//
	// It is outside mu. A spill takes mu twice while holding this, and nothing
	// takes this while holding mu.
	spillMu sync.Mutex

	// beforeRun, when set, is called at the top of a spill with the frontier's
	// lock let go. It is the seam a test parks in to show that the crawl goes on
	// offering while a run is being written, which is the whole claim this is
	// being changed for and is not otherwise observable from outside.
	beforeRun func()

	// runsMu guards the runs slice and the files it names. It is separate from
	// mu because looking a hash up in a run is a disk read, and a disk read is
	// the one thing that must not happen with the frontier's lock held. It is
	// taken for reading by any number of workers at once, and for writing only
	// when a spill adds a run or a merge replaces two, which is where the files
	// a reader is holding get closed.
	//
	// A caller holding mu may take it. Nothing takes mu while holding it.
	runsMu sync.RWMutex
	runs   []*run
	gen    int

	queue []*bucket

	// turn is which bucket the next batch starts from, and it is an atomic
	// because [Frontier.Next] takes no lock. Two batches being filled at once
	// step through the rotation together rather than each from the same place,
	// which is all the coordination they need: the lines themselves are handed
	// out under the bucket's own lock, so no line goes to both of them.
	turn atomic.Uint64

	stats counters

	closed bool
}

// counters is [Stats] as the frontier keeps it while a crawl is running.
//
// They are atomics rather than plain fields under the frontier's mutex because
// of what one of them was costing. Fetched is called once per URL that comes
// back and did nothing but add one to four numbers, and taking the frontier's
// lock to do that put it behind every offer, every batch and every queue write
// on the box: a goroutine dump of a 2,500 worker run on server3 had 765 workers
// standing in that lock inside Fetched, against 3,876 on the network. Counting
// is not a reason to queue.
//
// Reading them one at a time means a snapshot can catch a counter mid update
// and show a total that no single instant had. That is the right trade for a
// progress line and it is why [Stats] is documented as a snapshot rather than
// as a transaction.
type counters struct {
	Offered   atomic.Int64
	Admitted  atomic.Int64
	Duplicate atomic.Int64
	Refused   atomic.Int64
	Malformed atomic.Int64
	Foreign   atomic.Int64
	Handed    atomic.Int64
	Deferred  atomic.Int64
	Exhausted atomic.Int64
	Requeued  atomic.Int64
	Fetched   atomic.Int64
	New       atomic.Int64
	Repeat    atomic.Int64
	Empty     atomic.Int64
}

// load is the counters as a value, for a caller or for the manifest.
func (c *counters) load() Stats {
	return Stats{
		Offered:   c.Offered.Load(),
		Admitted:  c.Admitted.Load(),
		Duplicate: c.Duplicate.Load(),
		Refused:   c.Refused.Load(),
		Malformed: c.Malformed.Load(),
		Foreign:   c.Foreign.Load(),
		Handed:    c.Handed.Load(),
		Deferred:  c.Deferred.Load(),
		Exhausted: c.Exhausted.Load(),
		Requeued:  c.Requeued.Load(),
		Fetched:   c.Fetched.Load(),
		New:       c.New.Load(),
		Repeat:    c.Repeat.Load(),
		Empty:     c.Empty.Load(),
	}
}

// store puts a value back, which happens once, when a frontier is opened on a
// directory a previous run left counters in.
func (c *counters) store(s Stats) {
	c.Offered.Store(s.Offered)
	c.Admitted.Store(s.Admitted)
	c.Duplicate.Store(s.Duplicate)
	c.Refused.Store(s.Refused)
	c.Malformed.Store(s.Malformed)
	c.Foreign.Store(s.Foreign)
	c.Handed.Store(s.Handed)
	c.Deferred.Store(s.Deferred)
	c.Exhausted.Store(s.Exhausted)
	c.Requeued.Store(s.Requeued)
	c.Fetched.Store(s.Fetched)
	c.New.Store(s.New)
	c.Repeat.Store(s.Repeat)
	c.Empty.Store(s.Empty)
}

// A run is one sorted file of hashes, with a fence index in memory.
type run struct {
	name  string
	count int64
	f     *os.File
	fence []uint64
}

// A bucket is one queue file, open for appending at the end and for reading at
// the head. Both at once, which is the normal state of a crawl: the pages being
// fetched from the front of the file are what is being appended to the back.
type bucket struct {
	path string

	// mu guards this bucket and nothing else.
	//
	// The frontier's own lock used to guard every bucket, which meant a URL going
	// onto one queue file waited for a URL going onto another one. A goroutine
	// dump of a 2,500 worker run on server3 had 2,007 workers standing in the
	// frontier's lock and 856 of those were inside [Frontier.Requeue], holding up
	// the whole box to append one line to one file. The files are independent and
	// so are these.
	//
	// It is the inner lock. A caller that holds the frontier's lock may take it,
	// and nothing takes the frontier's lock while holding this.
	mu sync.Mutex

	w  *os.File
	bw *bufio.Writer

	r  *os.File
	br *bufio.Reader

	// part is the front of a line that was on the disk without its newline yet.
	// See [bucket.takeLocked] for why a bucket has one.
	part string

	head int64 // bytes consumed
	end  int64 // bytes written
}

// Stats is what the frontier has done, and it is all counters rather than rates
// because the thing reading it is a status line on a run that has been going for
// a week.
type Stats struct {
	Offered   int64 `json:"offered"`
	Admitted  int64 `json:"admitted"`
	Duplicate int64 `json:"duplicate"`
	Refused   int64 `json:"refused"`
	Malformed int64 `json:"malformed"`

	// Foreign is URLs on hosts another box in the fleet owns. It is worth its
	// own counter rather than being folded into Refused: a box whose offers are
	// almost all foreign is a box doing the fleet's link extraction and none of
	// its fetching, which is what a bad split looks like from the inside.
	Foreign int64 `json:"foreign"`

	Handed   int64 `json:"handed"`
	Deferred int64 `json:"deferred"`
	Requeued int64 `json:"requeued"`

	// Exhausted is how many batches stopped early because the queue had no host
	// left under its share of the batch, rather than because the batch was full.
	// It is the frontier saying it has run out of breadth rather than out of
	// URLs, and it is the number to watch for throughput: a crawl whose batches
	// are mostly exhausted is a crawl whose ceiling is how many hosts it knows,
	// and no amount of extra workers will move it.
	Exhausted int64 `json:"exhausted"`

	// Fetched, New, Repeat and Empty are what came back, reported to
	// [Frontier.Fetched] and kept here so a status line does not have to add up
	// a log. The three of them are the crawl's yield, and a run where Empty is
	// most of Fetched is a run fetching the wrong things.
	Fetched int64 `json:"fetched"`
	New     int64 `json:"new"`
	Repeat  int64 `json:"repeat"`
	Empty   int64 `json:"empty"`
}

// Queued is how many URLs have been admitted and not yet handed out.
//
// Requeued is in the sum and Deferred is not, and the difference between the two
// is the whole of why this is worth a comment. A requeue is a URL that was handed
// out and came back, so it pairs with a Handed that already ran and has to be
// added back. A deferral never left: [Frontier.Next] took the line out of a
// bucket, saw the host was already at its share of the batch, and put the same
// line back. Nothing was handed out and nothing needs adding back.
//
// Counting deferrals here made the queue look far deeper than it was, and by a
// margin that grew with the crawl rather than staying still. A shard that had
// fetched 4,097 pages reported 768,816 URLs waiting when it was holding about
// 22,669, because 746,147 of that total was the same small set of URLs being
// taken out and put back. That is not a cosmetic wrong number. A frontier that
// looks like it is holding three quarters of a million URLs is a frontier nobody
// suspects of having run short of hosts, which is exactly what it had done.
func (s Stats) Queued() int64 { return s.Admitted + s.Requeued - s.Handed }

// manifest is what is written to frontier.json, and it is everything an open
// needs that cannot be worked out from the files.
type manifest struct {
	Buckets int      `json:"buckets"`
	Bits    int      `json:"bits"`
	Expect  int64    `json:"expect"`
	Shard   int      `json:"shard"`
	Fleet   int      `json:"fleet"`
	Gen     int      `json:"gen"`
	Runs    []runMan `json:"runs"`
	Heads   []int64  `json:"heads"`
	Stats   Stats    `json:"stats"`
}

type runMan struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// OpenFrontier opens the frontier in a directory, creating it if it is not
// there and picking up where the last run left off if it is.
//
// A resume rebuilds the resident filter by reading the sorted runs, because the
// filter is derived from the exact set and a derived thing written to disk is a
// derived thing that can disagree with what it was derived from. The read is
// sequential and it is eight bytes per URL, so a frontier holding a hundred
// million of them costs an eight hundred megabyte read at startup, once.
func OpenFrontier(o FrontierOptions) (*Frontier, error) {
	if o.Dir == "" {
		return nil, errors.New("crawl: a frontier needs a directory")
	}
	if o.Buckets <= 0 {
		o.Buckets = DefaultBuckets
	}
	if o.Bits <= 0 {
		o.Bits = DefaultBits
	}
	if o.Expect <= 0 {
		o.Expect = DefaultExpect
	}
	if o.Pending <= 0 {
		o.Pending = DefaultPending
	}
	if o.PerHost <= 0 {
		o.PerHost = DefaultPerHost
	}
	if o.Compact <= 0 {
		o.Compact = DefaultCompact
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("crawl: opening the frontier: %w", err)
	}

	f := &Frontier{
		o:       o,
		dir:     o.Dir,
		pending: make(map[uint64]struct{}),
	}

	m, err := f.readManifest()
	if err != nil {
		return nil, err
	}
	if m != nil {
		if m.Buckets != o.Buckets {
			return nil, fmt.Errorf("crawl: the frontier in %s was written with %d buckets and this one has %d, which puts every URL in a different file", o.Dir, m.Buckets, o.Buckets)
		}
		// The split is part of what the frontier holds and not a flag on the
		// run. A box resumed under another shard would be crawling hosts it has
		// queued and refusing the hosts it is now responsible for, and the two
		// boxes that swapped would each be crawling the other's sites.
		if m.Fleet != o.Fleet || m.Shard != o.Shard {
			return nil, fmt.Errorf("crawl: the frontier in %s is shard %d of %d and this run is shard %d of %d",
				o.Dir, m.Shard, m.Fleet, o.Shard, o.Fleet)
		}
		f.gen = m.Gen
		f.stats.store(m.Stats)
		// A resume with a smaller filter than the one that filled it would be
		// wrong about everything. The plan is allowed to grow.
		o.Expect = max(o.Expect, m.Expect)
		f.o.Expect = o.Expect
	}

	f.bits = uint64(o.Expect) * uint64(o.Bits)
	if f.bits < 64 {
		f.bits = 64
	}
	f.filter = make([]atomic.Uint64, (f.bits+63)/64)

	if err := f.openRuns(m); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.dropStrayRuns(m); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.openLog(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.openQueue(m); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func (f *Frontier) readManifest() (*manifest, error) {
	b, err := os.ReadFile(filepath.Join(f.dir, "frontier.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("crawl: reading the frontier manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("crawl: reading the frontier manifest: %w", err)
	}
	return &m, nil
}

// openRuns opens the sorted runs named in the manifest and loads the filter and
// the fences from them.
func (f *Frontier) openRuns(m *manifest) error {
	if m == nil {
		return nil
	}
	for _, rm := range m.Runs {
		r, err := f.loadRun(rm.Name)
		if err != nil {
			return err
		}
		f.runs = append(f.runs, r)
	}
	return nil
}

// loadRun opens one run file, filling the filter and building the fence as it
// reads. The count is taken from the file rather than the manifest so that a run
// cut short by a crash mid write is read as what is actually in it.
func (f *Frontier) loadRun(name string) (*run, error) {
	path := filepath.Join(f.dir, name)
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("crawl: opening a frontier run: %w", err)
	}
	r := &run{name: name, f: fh}
	br := bufio.NewReaderSize(fh, 1<<20)
	var buf [8]byte
	for {
		if _, err := io.ReadFull(br, buf[:]); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				// A partial hash at the end is a crash during a write. The run
				// is truncated to whole hashes and the lost ones come back as
				// URLs offered a second time, which is what the frontier is for.
				break
			}
			_ = fh.Close()
			return nil, fmt.Errorf("crawl: reading %s: %w", name, err)
		}
		h := binary.BigEndian.Uint64(buf[:])
		if r.count%fanout == 0 {
			r.fence = append(r.fence, h)
		}
		f.add(h)
		r.count++
	}
	return r, nil
}

// openLog opens the append log of hashes that have not been written into a run
// yet, and loads what is in it. This is the file that makes a kill safe: the
// hashes in memory are also on disk within a flush of being offered.
//
// It also picks up the logs a spill rotated away and did not live to delete. A
// crawl killed while it was writing a run leaves the hashes for that run in a
// pending-NNNNNN.hashes and the run itself unfinished and unnamed by any
// manifest, so those hashes are read back into memory, appended to the live log
// and the rotated file removed. What comes out is one log again, holding exactly
// what the resident half of the seen set holds, which is the state the frontier
// expects to open on.
func (f *Frontier) openLog() error {
	path := filepath.Join(f.dir, "pending.hashes")
	fh, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("crawl: opening the pending hashes: %w", err)
	}
	n, err := f.readLog(fh)
	if err != nil {
		_ = fh.Close()
		return err
	}
	// Truncate to whole hashes, for the same reason a run is.
	if err := fh.Truncate(n * 8); err != nil {
		_ = fh.Close()
		return fmt.Errorf("crawl: opening the pending hashes: %w", err)
	}
	if _, err := fh.Seek(0, io.SeekEnd); err != nil {
		_ = fh.Close()
		return fmt.Errorf("crawl: opening the pending hashes: %w", err)
	}
	f.log, f.logw = fh, bufio.NewWriterSize(fh, 1<<16)

	if err := f.recoverLogs(); err != nil {
		return err
	}
	return nil
}

// readLog loads whole hashes from an open log into the pending map and the
// filter, and reports how many it read.
//
// The end of the file and a hash cut in half by a kill both end the read without
// an error, because both are what a log written by a process that was killed
// looks like and neither costs anything but the last URL. Anything else is a
// disk that cannot be read, which is worth saying out loud rather than reporting
// as a short frontier.
func (f *Frontier) readLog(fh *os.File) (int64, error) {
	br := bufio.NewReaderSize(fh, 1<<20)
	var buf [8]byte
	var n int64
	for {
		if _, err := io.ReadFull(br, buf[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return n, nil
			}
			return n, fmt.Errorf("crawl: reading the pending hashes: %w", err)
		}
		h := binary.BigEndian.Uint64(buf[:])
		f.pending[h] = struct{}{}
		f.add(h)
		n++
	}
}

// recoverLogs folds the logs of spills that did not finish back into the live
// one. See [Frontier.openLog] for when there are any.
func (f *Frontier) recoverLogs() error {
	names, err := filepath.Glob(filepath.Join(f.dir, "pending-*.hashes"))
	if err != nil {
		return fmt.Errorf("crawl: looking for rotated pending hashes: %w", err)
	}
	slices.Sort(names)
	for _, name := range names {
		fh, err := os.Open(name)
		if err != nil {
			return fmt.Errorf("crawl: opening %s: %w", filepath.Base(name), err)
		}
		n, err := f.readLog(fh)
		_ = fh.Close()
		if err != nil {
			return err
		}
		// Written to the live log rather than left where they are, because the
		// next spill writes its run from the map and deletes only its own rotated
		// log. Anything left in an older one would be read again at every open
		// from here on.
		if n > 0 {
			if err := f.appendLog(name); err != nil {
				return err
			}
		}
		if err := os.Remove(name); err != nil {
			return fmt.Errorf("crawl: removing %s: %w", filepath.Base(name), err)
		}
	}
	return nil
}

// appendLog copies a rotated log onto the end of the live one, whole hashes
// only.
func (f *Frontier) appendLog(name string) error {
	b, err := os.ReadFile(name)
	if err != nil {
		return fmt.Errorf("crawl: reading %s: %w", filepath.Base(name), err)
	}
	if _, err := f.logw.Write(b[:len(b)-len(b)%8]); err != nil {
		return fmt.Errorf("crawl: recovering %s: %w", filepath.Base(name), err)
	}
	return f.logw.Flush()
}

// dropStrayRuns removes run files no manifest names.
//
// A crawl killed inside a spill or a merge leaves a run part written, and the
// manifest, which is the only thing that says which runs exist, does not name
// it. Nothing reads those files again and they are the size of the run that was
// being written, so on a box whose disk is the thing that runs out first they
// are worth removing rather than leaving.
//
// Only done when a manifest was actually read. A frontier whose manifest is
// missing is a frontier nobody can say anything about, and deleting its runs on
// a guess is the one mistake here that cannot be undone by crawling again.
func (f *Frontier) dropStrayRuns(m *manifest) error {
	if m == nil {
		return nil
	}
	named := make(map[string]struct{}, len(m.Runs))
	for _, rm := range m.Runs {
		named[rm.Name] = struct{}{}
	}
	names, err := filepath.Glob(filepath.Join(f.dir, "seen-*.hashes"))
	if err != nil {
		return fmt.Errorf("crawl: looking for stray runs: %w", err)
	}
	for _, name := range names {
		if _, ok := named[filepath.Base(name)]; ok {
			continue
		}
		if err := os.Remove(name); err != nil {
			return fmt.Errorf("crawl: removing %s: %w", filepath.Base(name), err)
		}
	}
	return nil
}

func (f *Frontier) openQueue(m *manifest) error {
	if err := os.MkdirAll(filepath.Join(f.dir, "queue"), 0o755); err != nil {
		return fmt.Errorf("crawl: opening the queue: %w", err)
	}
	for i := range f.o.Buckets {
		b := &bucket{path: filepath.Join(f.dir, "queue", fmt.Sprintf("%04d.urls", i))}
		w, err := os.OpenFile(b.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("crawl: opening the queue: %w", err)
		}
		info, err := w.Stat()
		if err != nil {
			_ = w.Close()
			return fmt.Errorf("crawl: opening the queue: %w", err)
		}
		r, err := os.Open(b.path)
		if err != nil {
			_ = w.Close()
			return fmt.Errorf("crawl: opening the queue: %w", err)
		}
		b.w, b.bw, b.r, b.end = w, bufio.NewWriterSize(w, 1<<16), r, info.Size()
		if m != nil && i < len(m.Heads) {
			b.head = min(m.Heads[i], b.end)
		}
		if _, err := b.r.Seek(b.head, io.SeekStart); err != nil {
			_ = w.Close()
			_ = r.Close()
			return fmt.Errorf("crawl: opening the queue: %w", err)
		}
		b.br = bufio.NewReaderSize(b.r, 1<<16)
		f.queue = append(f.queue, b)
	}
	return nil
}

// Offer puts a URL to the frontier, and reports whether it was queued and, when
// it was not, why not in a sentence.
//
// The order of the checks is the order of what they cost. A URL that will not
// parse is refused without touching anything, a URL already seen is refused by
// the filter without a disk read most of the time, and only what is left is
// charged to the budget, because charging a host for a URL it has already been
// asked for is how a host's allowance gets spent on one page.
func (f *Frontier) Offer(rawurl string) (bool, string, error) {
	ps := []pending{parseOffer(rawurl)}
	if _, err := f.offer(ps); err != nil {
		return false, "", err
	}
	return ps[0].queued, ps[0].why, nil
}

// OfferAll puts a page's links to the frontier in one turn at the lock.
//
// It exists because of the ratio. A crawl offers around sixty links for every
// page it fetches, 909,695 URLs from 15,669 pages on the bench that measured it,
// so the frontier is asked sixty times as often as anything else on the box and
// one call per link makes it the queue every worker stands in. A goroutine dump
// of a two thousand worker run had 1,584 workers waiting on this lock, against
// 46 in the sink and 48 on the network.
//
// The parsing, the canonicalization and the fleet split all happen before the
// lock is taken, because none of them touch the frontier, and doing the whole
// page's worth of that work up front is what makes one turn at the lock enough.
func (f *Frontier) OfferAll(rawurls []string) (int, error) {
	if len(rawurls) == 0 {
		return 0, nil
	}
	ps := make([]pending, len(rawurls))
	for i, u := range rawurls {
		ps[i] = parseOffer(u)
	}
	return f.offer(ps)
}

// offer is the three passes an offer goes through, and the split between them
// is where the lock is held and where it is not.
//
// The first pass answers everything memory can answer: whether the URL parsed,
// whether it is this box's, and whether the filter or the hashes not yet written
// out have seen it. The second pass is the sorted runs on disk, and it runs with
// the frontier's lock let go, because a goroutine dump of a 2,500 worker run
// caught the one worker holding that lock inside a read of a run file with 1,151
// others waiting behind it. The third pass records what survived and queues it.
//
// Letting the lock go in the middle means two workers can be on disk for the
// same URL at once and both find it new. The third pass looks again at the
// hashes in memory, which closes that for every case but an exact overlap of the
// two reads, and what an exact overlap costs is one page fetched twice.
func (f *Frontier) offer(ps []pending) (int, error) {
	for i := range ps {
		f.sift(&ps[i])
	}

	f.mu.Lock()
	for i := range ps {
		if ps[i].done || !ps[i].onDisk {
			continue
		}
		if f.held(ps[i].hash) {
			f.stats.Duplicate.Add(1)
			ps[i].done, ps[i].why, ps[i].onDisk = true, "already offered", false
		}
	}
	f.mu.Unlock()

	f.runsMu.RLock()
	var err error
	for i := range ps {
		if ps[i].done || !ps[i].onDisk {
			continue
		}
		var seen bool
		if seen, err = f.runsHave(ps[i].hash); err != nil {
			break
		}
		if seen {
			f.stats.Duplicate.Add(1)
			ps[i].done, ps[i].why = true, "already offered"
		}
	}
	f.runsMu.RUnlock()
	if err != nil {
		return 0, err
	}

	// take is the URLs this batch admitted, collected under the lock and written
	// to the queue after it. A queue write is a line into one bucket's own
	// buffer, and the bucket has its own lock for exactly that, so doing it here
	// put a file write behind the one mutex every worker on the box needs for
	// every one of the sixty links on every page it fetches.
	//
	// It is the same change [Frontier.Requeue] already made for the hand backs,
	// for the same reason and on the evidence of the same kind of dump: with the
	// breaker sharded in #197, offer went from six goroutines waiting on it to
	// 421, second only to the sink.
	//
	// A URL is recorded as seen before it is queued, and a crash between the two
	// loses it. That window is not new. record writes into a buffered log that a
	// crash loses the tail of anyway, so the URL was already only as durable as
	// the next flush, and the frontier is built to be told about a URL twice.
	take := make([]string, 0, len(ps))

	f.mu.Lock()
	queued := 0
	var admitErr error
	for i := range ps {
		ok, err := f.admit(&ps[i])
		if err != nil {
			admitErr = err
			break
		}
		if ok {
			queued++
			take = append(take, ps[i].canonical)
		}
	}
	s, spilling, err := f.freeze()
	f.mu.Unlock()

	for _, canonical := range take {
		if perr := f.push(canonical); perr != nil {
			return queued, perr
		}
	}

	if admitErr != nil {
		return queued, admitErr
	}
	if err != nil {
		return queued, err
	}
	// The spill happens here rather than inside the third pass, with the
	// frontier's lock let go, which is the whole of [Frontier.freeze]'s reason to
	// exist. See it for what that was costing.
	if spilling {
		if err := f.spill(s); err != nil {
			return queued, err
		}
	}
	return queued, nil
}

// A pending is one URL on its way through [Frontier.offer], carrying both the
// part of the answer worked out without the frontier and the part worked out
// with it.
type pending struct {
	canonical string
	host      string
	hash      uint64
	hostHash  uint64
	why       string // why it was refused, or empty
	bad       bool   // refused for not parsing rather than for the fleet split
	done      bool   // answered, and why says how
	queued    bool   // it went on the queue
	onDisk    bool   // the runs still have to be asked about it
}

// parseOffer does the part of an offer that needs nothing from the frontier at
// all. It is the expensive part, a URL parse and two blake3 hashes, and the
// whole reason it is separate.
func parseOffer(rawurl string) pending {
	canonical, err := frontier.Canon(rawurl)
	if err != nil {
		return pending{why: err.Error(), bad: true}
	}
	host, err := hostOf(canonical)
	if err != nil {
		return pending{why: err.Error(), bad: true}
	}
	return pending{
		canonical: canonical,
		host:      host,
		hash:      hashOf(canonical),
		hostHash:  hashOf(host),
	}
}

// sift is the part of an offer that needs no lock at all: the counters, which
// are atomics, the fleet split, which is a hash and a remainder, and the filter,
// whose words are atomics too.
//
// Everything here used to be under the frontier's lock, and on a page of sixty
// links it was two blake3 hashes and three probes into a twelve megabyte bitmap
// per link, sixty times over, with every other worker on the box waiting.
func (f *Frontier) sift(p *pending) {
	f.stats.Offered.Add(1)
	if p.bad {
		f.stats.Malformed.Add(1)
		p.done = true
		return
	}

	// The fleet split comes before anything is remembered. A URL another box
	// owns is not this box's business at all, and recording it here would put a
	// hash in this frontier's set for a page this frontier will never fetch.
	if f.o.Fleet > 1 {
		if box := int(p.hostHash % uint64(f.o.Fleet)); box != f.o.Shard {
			f.stats.Foreign.Add(1)
			p.done, p.why = true, fmt.Sprintf("%s belongs to box %d of %d", p.host, box, f.o.Fleet)
			return
		}
	}

	// The filter is never wrong about a URL it has not got, so a no here is the
	// whole answer and neither the hashes in memory nor the runs on disk are
	// asked about it.
	p.onDisk = f.maybe(p.hash)
}

// admit is the last pass: what is left is new as far as anything knows, so it is
// charged to the budget, remembered and queued. It is called with the frontier's
// lock held.
func (f *Frontier) admit(p *pending) (bool, error) {
	if p.done {
		return false, nil
	}

	// Looked at again because the runs were read with the lock let go, and
	// another worker offering the same URL in that window has recorded it here.
	if f.held(p.hash) {
		f.stats.Duplicate.Add(1)
		p.done, p.why = true, "already offered"
		return false, nil
	}

	// The filter said no when this URL was sifted and says yes now, so somebody
	// set those bits in between. Usually that somebody is in the hashes above and
	// the check has already caught it, and what is left is the case where a spill
	// moved the hash out of memory and into a run between the two. The runs did
	// not get asked, because at sift time there was no reason to ask them.
	//
	// This is a disk read with the lock held, which is the thing the three passes
	// exist to avoid, and it is here because it happens when one worker records a
	// hash inside the few microseconds another is sifting the same one and a
	// spill lands in the same window. What it costs when it is skipped is a page
	// fetched twice.
	if !p.onDisk && f.maybe(p.hash) {
		f.runsMu.RLock()
		seen, err := f.runsHave(p.hash)
		f.runsMu.RUnlock()
		if err != nil {
			return false, err
		}
		if seen {
			f.stats.Duplicate.Add(1)
			p.done, p.why = true, "already offered"
			return false, nil
		}
	}

	if f.o.Budget != nil {
		if ok, why := f.o.Budget.Offer(p.canonical); !ok {
			// The refusal is remembered too. A trap generates the same URL from
			// twenty pages and refusing it twenty times is twenty shape lookups
			// for an answer that will not change.
			if err := f.record(p.hash); err != nil {
				return false, err
			}
			f.stats.Refused.Add(1)
			p.done, p.why = true, why
			return false, nil
		}
	}

	if err := f.record(p.hash); err != nil {
		return false, err
	}
	// The queue write is not done here. It is the one thing this function used
	// to do that does not need the frontier's lock, and [Frontier.offer] does it
	// once the lock is let go. See the loop there.
	f.stats.Admitted.Add(1)
	p.done, p.queued = true, true
	return true, nil
}

// Requeue puts a URL back without asking whether it has been seen, for a fetch
// that did not happen: a host that asked for time, a connection that broke. It
// is deliberately separate from Offer, because a URL coming back from a failed
// fetch is already in the seen set and Offer would refuse it.
//
// It takes one bucket's lock rather than the frontier's. Every hand back from a
// host that is not due yet comes through here, which on a real shard is one call
// for every two pages fetched, and all any of them does is append a line to a
// file. Making them queue behind the offers and the batches was most of what a
// goroutine dump found the workers waiting for.
func (f *Frontier) Requeue(canonical string) error {
	if err := f.push(canonical); err != nil {
		return err
	}
	f.stats.Requeued.Add(1)
	return nil
}

// Next hands out up to n URLs to fetch.
//
// It takes them from the buckets in rotation and from at most [PerHost] per host
// per batch, and a host over its share has its extra URLs put back at the end of
// its bucket. That is the whole scheduler: a batch that is spread over hundreds
// of hosts can be fetched concurrently at one request per host at a time, and a
// batch that is one host cannot.
//
// An empty return means the queue is empty, which on a crawl in progress means
// the workers are holding everything there was.
func (f *Frontier) Next(n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}

	out := make([]string, 0, n)
	per := map[string]int{}
	var over []string

	// Every bucket gets a look, and a bucket that only had URLs for hosts
	// already at their share is not a reason to stop. The read cap is what
	// stops a bucket holding a million URLs for one host from being pulled into
	// memory in one batch: past it the bucket is left alone until next time.
	most := 4 * n

	for range f.o.Buckets {
		b := f.queue[(f.turn.Add(1)-1)%uint64(f.o.Buckets)]
		if err := b.arm(); err != nil {
			return nil, err
		}
		for reads := 0; len(out) < n && reads < most; reads++ {
			line, err := b.take()
			if err != nil {
				return nil, err
			}
			if line == "" {
				break
			}
			host, err := hostOf(line)
			if err != nil {
				f.stats.Malformed.Add(1)
				continue
			}
			if per[host] >= f.o.PerHost {
				over = append(over, line)
				continue
			}
			per[host]++
			out = append(out, line)
		}
		if len(out) >= n {
			break
		}
	}

	for _, line := range over {
		if err := f.push(line); err != nil {
			return nil, err
		}
		f.stats.Deferred.Add(1)
	}

	// A batch that came back short after every bucket was looked at did not run
	// out of URLs, it ran out of hosts. There were more URLs down there and every
	// one of them belonged to a host already holding its share of this batch.
	//
	// This is a note taken rather than a decision made. It changes nothing about
	// what was handed out, and it is here because it is the number that says
	// whether adding workers can help. A batch that is short is a batch where the
	// fetchers are already able to take everything the queue can safely give, so
	// the ceiling is how many hosts the crawl knows and the only thing that lifts
	// it is finding more of them.
	if len(out) < n {
		f.stats.Exhausted.Add(1)
	}

	f.stats.Handed.Add(int64(len(out)))
	return out, nil
}

// Fetched reports what a URL turned into, which is what the budget earns on and
// what tells a template that produces nothing from one that produces articles.
//
// It takes no lock. The counters are atomics and the budget keeps its own, so
// the one call a worker makes for every URL it finishes no longer queues behind
// the offers and the batches.
func (f *Frontier) Fetched(canonical string, r frontier.Result) {
	f.stats.Fetched.Add(1)
	switch r {
	case frontier.New:
		f.stats.New.Add(1)
	case frontier.Repeat:
		f.stats.Repeat.Add(1)
	default:
		f.stats.Empty.Add(1)
	}
	if f.o.Budget != nil {
		f.o.Budget.Fetched(canonical, r)
	}
}

// Stats is a snapshot of the counters, read without the lock and therefore
// without stopping the crawl to print a progress line.
func (f *Frontier) Stats() Stats {
	return f.stats.load()
}

// Flush writes everything that is only in memory: the queue buffers, the pending
// hashes, the queue heads and the counters. A crawl calls this on a timer, and
// what it costs to lose is what happened since the last one.
func (f *Frontier) Flush() error {
	f.mu.Lock()
	err := f.flush()
	s, spilling, ferr := f.freeze()
	f.mu.Unlock()

	if err != nil {
		return err
	}
	if ferr != nil {
		return ferr
	}
	if spilling {
		return f.spill(s)
	}
	return nil
}

func (f *Frontier) flush() error {
	for _, b := range f.queue {
		if err := b.trim(f.o.Compact); err != nil {
			return err
		}
	}
	if f.logw != nil {
		if err := f.logw.Flush(); err != nil {
			return fmt.Errorf("crawl: flushing the pending hashes: %w", err)
		}
	}
	return f.writeManifest()
}

// Close flushes and closes everything. A frontier that was closed cleanly opens
// again with nothing lost.
func (f *Frontier) Close() error {
	// Taken before the frontier's own lock, and taken at all so that a spill
	// still writing its run finishes before the files under it are closed. A
	// close that did not wait would be a run half on the disk and a rotated log
	// already deleted.
	f.spillMu.Lock()
	defer f.spillMu.Unlock()

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true

	var errs []error
	if f.logw != nil {
		errs = append(errs, f.logw.Flush())
	}
	// Each bucket's own lock is taken here as well as the frontier's, because a
	// Requeue arriving now holds nothing but the bucket and would otherwise be
	// writing into a file this is closing.
	for _, b := range f.queue {
		b.mu.Lock()
		if b.bw != nil {
			errs = append(errs, b.bw.Flush())
		}
		b.mu.Unlock()
	}
	errs = append(errs, f.writeManifest())
	for _, b := range f.queue {
		b.mu.Lock()
		if b.w != nil {
			errs = append(errs, b.w.Close())
		}
		if b.r != nil {
			errs = append(errs, b.r.Close())
		}
		b.mu.Unlock()
	}
	if f.log != nil {
		errs = append(errs, f.log.Close())
	}
	f.runsMu.Lock()
	for _, r := range f.runs {
		errs = append(errs, r.f.Close())
	}
	f.runsMu.Unlock()
	return errors.Join(errs...)
}

func (f *Frontier) writeManifest() error {
	m := manifest{
		Buckets: f.o.Buckets,
		Bits:    f.o.Bits,
		Expect:  f.o.Expect,
		Shard:   f.o.Shard,
		Fleet:   f.o.Fleet,
		Gen:     f.gen,
		Stats:   f.stats.load(),
	}
	f.runsMu.RLock()
	for _, r := range f.runs {
		m.Runs = append(m.Runs, runMan{Name: r.name, Count: r.count})
	}
	f.runsMu.RUnlock()
	for _, b := range f.queue {
		m.Heads = append(m.Heads, b.at())
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("crawl: writing the frontier manifest: %w", err)
	}
	tmp := filepath.Join(f.dir, "frontier.json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("crawl: writing the frontier manifest: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(f.dir, "frontier.json")); err != nil {
		return fmt.Errorf("crawl: writing the frontier manifest: %w", err)
	}
	return nil
}

// record marks a hash as seen, in the filter, in the pending map and in the log.
// The caller holds mu.
func (f *Frontier) record(h uint64) error {
	f.add(h)
	f.pending[h] = struct{}{}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], h)
	if _, err := f.logw.Write(buf[:]); err != nil {
		return fmt.Errorf("crawl: recording a URL: %w", err)
	}
	return nil
}

// held reports whether a hash is in the resident half of the seen set, which is
// the map being filled now and, while a spill is running, the one being written
// out. The caller holds mu.
func (f *Frontier) held(h uint64) bool {
	if _, ok := f.pending[h]; ok {
		return true
	}
	if f.spilling == nil {
		return false
	}
	_, ok := f.spilling[h]
	return ok
}

// push appends a canonical URL to its bucket. It takes that bucket's lock and no
// other, so a caller that holds the frontier's lock may call it and a caller
// that holds nothing may too.
func (f *Frontier) push(canonical string) error {
	return f.bucket(canonical).push(canonical)
}

// bucket is the queue file a URL goes in, chosen by the whole URL rather than by
// its host.
//
// It was the host, on the reasoning that a batch read a bucket at a time would
// then be spread over hosts. It does the opposite. A page links to its own site
// sixty times over, all sixty go in together, and the bucket ends up a run of
// one host followed by a run of another. A batch takes two URLs per host, so
// reading that run means reading fifty eight it has to put straight back:
// server2 was deferring twenty nine URLs for every one it handed out, and doing
// it in the one goroutine that fills the batch, with a URL parse each and the
// frontier's lock held. The workers waiting for that were the largest group in a
// goroutine dump of the box.
//
// On the whole URL those sixty links land in sixty different files and every
// bucket ends up interleaved, which is what makes two per host per batch
// something a read runs into rarely rather than constantly.
//
// Nothing looks a URL up by bucket, so changing what decides it costs nothing on
// a queue already written: those URLs come out of wherever they went in.
func (f *Frontier) bucket(canonical string) *bucket {
	return f.queue[int(hashOf(canonical)%uint64(f.o.Buckets))]
}

// runsHave reports whether a hash is in one of the sorted runs on disk. It is
// called with the runs held for reading and the frontier's lock let go.
//
// Newest first, because a URL offered twice tends to be offered twice close
// together.
func (f *Frontier) runsHave(h uint64) (bool, error) {
	if len(f.runs) == 0 {
		return false, nil
	}
	b := blocks.Get().(*[]byte)
	defer blocks.Put(b)
	for i := len(f.runs) - 1; i >= 0; i-- {
		ok, err := f.runs[i].has(h, *b)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// blocks holds the four kilobyte block a lookup reads into.
//
// A lookup used to allocate one, and a lookup that misses walks every run on
// disk and allocated one per run. That is four kilobytes thrown away per run per
// URL offered, on a crawl where a page offers sixty links and the frontier
// already holds nine hundred million URLs, so nearly every offer is a lookup. On
// server2 it was the largest single allocator in the crawl's own code: a heap
// sample nine minutes apart had it holding 106 MB and growing by 83 MB between
// the two, which is not a leak but garbage, and garbage is what the collector
// sizes the heap against. The process was living in 2.4 GB against a live set of
// 1.1 GB and being killed by the kernel on a box it shares.
//
// The block is a fixed size, which is what makes a pool the whole of the fix
// rather than the beginning of one.
var blocks = sync.Pool{New: func() any {
	b := make([]byte, fanout*8)
	return &b
}}

// The filter is a plain bloom filter with three probes taken from the one hash,
// which is enough at ten bits per URL and one less thing to get wrong than a
// second hash function would be.
func (f *Frontier) probes(h uint64) (uint64, uint64, uint64) {
	a := h % f.bits
	b := (h>>21 | 1) % f.bits
	c := (h>>42 | 1) % f.bits
	return a, (a + b) % f.bits, (a + b + c) % f.bits
}

func (f *Frontier) add(h uint64) {
	a, b, c := f.probes(h)
	f.filter[a/64].Or(1 << (a % 64))
	f.filter[b/64].Or(1 << (b % 64))
	f.filter[c/64].Or(1 << (c % 64))
}

func (f *Frontier) maybe(h uint64) bool {
	a, b, c := f.probes(h)
	return f.filter[a/64].Load()&(1<<(a%64)) != 0 &&
		f.filter[b/64].Load()&(1<<(b%64)) != 0 &&
		f.filter[c/64].Load()&(1<<(c%64)) != 0
}

// A spill is one set of hashes on its way out of memory and onto the disk: the
// map that was taken away from the crawl, the number the run and the rotated log
// are both named with, and nothing else. It is what [Frontier.freeze] hands to
// [Frontier.spill].
type spill struct {
	hashes map[uint64]struct{}
	gen    int
}

func (s spill) run() string { return fmt.Sprintf("seen-%06d.hashes", s.gen) }
func (s spill) log() string { return fmt.Sprintf("pending-%06d.hashes", s.gen) }

// freeze takes the pending hashes away from the crawl when there are enough of
// them to be worth a run, and hands back the work of writing them. The caller
// holds mu and must call [Frontier.spill] with what it gets back, after letting
// mu go.
//
// This split is the point of the whole arrangement. The writing used to happen
// where the counting happens, which was inside record, inside admit, inside the
// third pass of offer, which holds the frontier's lock across all of it. So once
// every two million new hashes one unlucky worker sorted two million of them,
// wrote a sixteen megabyte run, and then merged runs, which on a frontier the
// size of the fleet's streams hundreds of megabytes and writes them back. Every
// second of that was a second in which no other worker on the box could offer a
// link. A goroutine dump of server3 at 130 pages a second had 1,098 of them
// waiting on this lock, the largest group in the dump after the fetches
// themselves, and the rate inside a single run decayed from 118 to 114 to lower
// as the runs got bigger and the merges got longer.
//
// What is left here is a map swap, a file rename and an open, which is
// microseconds, and the write happens with the lock let go while the rest of the
// box goes on crawling.
//
// The log is rotated rather than truncated for the same reason the map is set
// aside rather than cleared. Those hashes are only on the disk in that file
// until the run lands, so a crash during the write has to find them there.
func (f *Frontier) freeze() (spill, bool, error) {
	if len(f.pending) < f.o.Pending || f.spilling != nil {
		return spill{}, false, nil
	}
	f.gen++
	s := spill{hashes: f.pending, gen: f.gen}

	if err := f.rotateLog(s.log()); err != nil {
		// Reported rather than swallowed. A log that will not rotate is a disk
		// that will not take another file, and both boxes running this crawl are
		// above 97% on the filesystem the frontier lives on, so this is the case
		// that actually happens. A crawl that quietly went on offering here would
		// grow the pending map without bound and lose the lot on the next kill.
		//
		// Nothing has been given away at this point: rotateLog puts a working
		// log back on every path out of it, so the hashes are still in the map
		// and still in a file called pending.hashes.
		f.gen--
		return spill{}, false, err
	}
	f.spilling = f.pending
	f.pending = make(map[uint64]struct{}, f.o.Pending)
	return s, true, nil
}

// rotateLog flushes the pending log, closes it, renames it out of the way under
// the name the spill will delete it by, and opens a fresh one. The caller holds
// mu.
//
// The close comes before the rename because Windows will not rename a file that
// anybody has open, and every test in this file that spills said so at once on
// the Windows runner. Unix does not care, and the version that renamed first was
// nicer: a failure left the frontier writing to a descriptor that was still
// good. Without that, every path out of here has to put a working log back, and
// [Frontier.reopenLog] is what does it.
func (f *Frontier) rotateLog(name string) error {
	if err := f.logw.Flush(); err != nil {
		return fmt.Errorf("crawl: rotating the pending hashes: %w", err)
	}
	if err := f.log.Close(); err != nil {
		return fmt.Errorf("crawl: rotating the pending hashes: %w", err)
	}
	path := filepath.Join(f.dir, "pending.hashes")
	rotated := filepath.Join(f.dir, name)
	if err := os.Rename(path, rotated); err != nil {
		return errors.Join(fmt.Errorf("crawl: rotating the pending hashes: %w", err), f.reopenLog())
	}
	fh, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		// The rotated file goes back under the live name, so the hashes written
		// since the last spill are still where a recovery looks for them, and
		// then it is reopened for appending. Losing them here would be losing
		// every URL this crawl has seen since it started.
		if back := os.Rename(rotated, path); back != nil {
			return errors.Join(fmt.Errorf("crawl: rotating the pending hashes: %w", err), back)
		}
		return errors.Join(fmt.Errorf("crawl: rotating the pending hashes: %w", err), f.reopenLog())
	}
	f.log, f.logw = fh, bufio.NewWriterSize(fh, 1<<16)
	return nil
}

// reopenLog opens the pending log for appending and puts it back on the
// frontier. It is the recovery path out of [Frontier.rotateLog], which has to
// close the log before it can rename it and therefore has to be able to undo
// that. The caller holds mu.
//
// A failure here is not recoverable and is not swallowed. The frontier would go
// on offering with nothing writing the hashes down, and the crawl would find out
// on the next restart, when a billion URLs it had already fetched came back as
// unseen.
func (f *Frontier) reopenLog() error {
	fh, err := os.OpenFile(filepath.Join(f.dir, "pending.hashes"), os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("crawl: reopening the pending hashes: %w", err)
	}
	f.log, f.logw = fh, bufio.NewWriterSize(fh, 1<<16)
	return nil
}

// spill writes a frozen set of hashes out as a sorted run and then merges runs
// that have grown to the same size. It is called with no lock held.
//
// The merging is what keeps a lookup cheap. Without it a crawl that spills five
// hundred times has five hundred runs to search, and with it the runs double in
// size as they merge, so a billion URLs is around nine of them and every hash is
// rewritten around nine times rather than five hundred.
//
// The order of the last three steps is what a crash lands on. The run goes in
// the slice, then the frozen map is dropped, so a hash is never out of both at
// once. Then the manifest names the run, and only then is the rotated log
// deleted, so a crash anywhere in here leaves the hashes either in a log that
// gets read at open or in a run the manifest knows about, and never in a file
// nobody looks at.
func (f *Frontier) spill(s spill) error {
	f.spillMu.Lock()
	defer f.spillMu.Unlock()
	if f.beforeRun != nil {
		f.beforeRun()
	}

	hashes := make([]uint64, 0, len(s.hashes))
	for h := range s.hashes {
		hashes = append(hashes, h)
	}
	slices.Sort(hashes)

	r, err := f.writeRun(s.run(), hashes)
	if err != nil {
		return err
	}
	f.runsMu.Lock()
	f.runs = append(f.runs, r)
	f.runsMu.Unlock()

	f.mu.Lock()
	f.spilling = nil
	err = f.writeManifest()
	f.mu.Unlock()
	if err != nil {
		return err
	}

	if err := os.Remove(filepath.Join(f.dir, s.log())); err != nil {
		return fmt.Errorf("crawl: removing a spilled log: %w", err)
	}
	return f.compactRuns()
}

// nextGen hands out the next number for a run file. Runs are named from it and a
// name used twice is a file overwritten, so it is taken under mu even though
// compaction is the only thing that asks for it off the offer path.
func (f *Frontier) nextGen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	return f.gen
}

// compactRuns merges the last two runs while the newer one is at least half the
// size of the one before it, which is the binary counter that keeps the number
// of runs logarithmic in the number of URLs.
func (f *Frontier) compactRuns() error {
	for {
		f.runsMu.RLock()
		enough := len(f.runs) >= 2
		var a, b *run
		if enough {
			a, b = f.runs[len(f.runs)-2], f.runs[len(f.runs)-1]
		}
		f.runsMu.RUnlock()
		if !enough || b.count*2 < a.count {
			return nil
		}
		name := fmt.Sprintf("seen-%06d.hashes", f.nextGen())
		merged, err := f.mergeRuns(name, a, b)
		if err != nil {
			return err
		}
		// The swap and the two closes go together under the write lock. A reader
		// that got through with the old slice would be part way into a file this
		// is about to close, and the error it came back with would be reported as
		// a URL nobody had seen.
		f.runsMu.Lock()
		f.runs = append(f.runs[:len(f.runs)-2], merged)
		for _, old := range []*run{a, b} {
			_ = old.f.Close()
		}
		f.runsMu.Unlock()
		for _, old := range []*run{a, b} {
			if err := os.Remove(filepath.Join(f.dir, old.name)); err != nil {
				return fmt.Errorf("crawl: removing a merged run: %w", err)
			}
		}
	}
}

func (f *Frontier) writeRun(name string, hashes []uint64) (*run, error) {
	path := filepath.Join(f.dir, name)
	fh, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("crawl: writing a frontier run: %w", err)
	}
	bw := bufio.NewWriterSize(fh, 1<<20)
	r := &run{name: name}
	var buf [8]byte
	for _, h := range hashes {
		binary.BigEndian.PutUint64(buf[:], h)
		if _, err := bw.Write(buf[:]); err != nil {
			_ = fh.Close()
			return nil, fmt.Errorf("crawl: writing a frontier run: %w", err)
		}
		if r.count%fanout == 0 {
			r.fence = append(r.fence, h)
		}
		r.count++
	}
	if err := bw.Flush(); err != nil {
		_ = fh.Close()
		return nil, fmt.Errorf("crawl: writing a frontier run: %w", err)
	}
	if err := fh.Close(); err != nil {
		return nil, fmt.Errorf("crawl: writing a frontier run: %w", err)
	}
	if r.f, err = os.Open(path); err != nil {
		return nil, fmt.Errorf("crawl: writing a frontier run: %w", err)
	}
	return r, nil
}

// mergeRuns streams two sorted runs into one, dropping the hashes they have in
// common, which they do have: a run is written from what was offered and the
// same URL can be offered again after the filter was rebuilt.
func (f *Frontier) mergeRuns(name string, a, b *run) (*run, error) {
	path := filepath.Join(f.dir, name)
	fh, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("crawl: merging frontier runs: %w", err)
	}
	bw := bufio.NewWriterSize(fh, 1<<20)
	ra, rb := a.scan(), b.scan()
	out := &run{name: name}

	write := func(h uint64) error {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], h)
		if _, err := bw.Write(buf[:]); err != nil {
			return err
		}
		if out.count%fanout == 0 {
			out.fence = append(out.fence, h)
		}
		out.count++
		return nil
	}

	ha, oka, err := ra.next()
	if err != nil {
		_ = fh.Close()
		return nil, err
	}
	hb, okb, err := rb.next()
	if err != nil {
		_ = fh.Close()
		return nil, err
	}
	for oka || okb {
		var h uint64
		switch {
		case oka && okb && ha == hb:
			h = ha
			ha, oka, err = ra.next()
			if err == nil {
				hb, okb, err = rb.next()
			}
		case okb && (!oka || hb < ha):
			h = hb
			hb, okb, err = rb.next()
		default:
			h = ha
			ha, oka, err = ra.next()
		}
		if err != nil {
			_ = fh.Close()
			return nil, err
		}
		if err := write(h); err != nil {
			_ = fh.Close()
			return nil, fmt.Errorf("crawl: merging frontier runs: %w", err)
		}
	}
	if err := bw.Flush(); err != nil {
		_ = fh.Close()
		return nil, fmt.Errorf("crawl: merging frontier runs: %w", err)
	}
	if err := fh.Close(); err != nil {
		return nil, fmt.Errorf("crawl: merging frontier runs: %w", err)
	}
	if out.f, err = os.Open(path); err != nil {
		return nil, fmt.Errorf("crawl: merging frontier runs: %w", err)
	}
	return out, nil
}

// has reports whether a sorted run holds a hash. The fence says which four
// kilobyte block it would be in and the block is read and searched, so a lookup
// is one read whatever the run's size.
//
// buf is where the block is read, and it is the caller's so that a caller
// walking several runs pays for one block rather than one per run. A buf too
// small for the block is grown here rather than refused, which is what lets a
// test call this with nil.
func (r *run) has(h uint64, buf []byte) (bool, error) {
	if r.count == 0 || len(r.fence) == 0 || h < r.fence[0] {
		return false, nil
	}
	i := sort.Search(len(r.fence), func(i int) bool { return r.fence[i] > h }) - 1
	if i < 0 {
		return false, nil
	}
	off := int64(i) * fanout * 8
	n := int64(fanout * 8)
	if rest := r.count*8 - off; rest < n {
		n = rest
	}
	if int64(cap(buf)) < n {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	if _, err := r.f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("crawl: reading %s: %w", r.name, err)
	}
	lo, hi := 0, int(n/8)
	for lo < hi {
		mid := (lo + hi) / 2
		v := binary.BigEndian.Uint64(buf[mid*8:])
		switch {
		case v == h:
			return true, nil
		case v < h:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return false, nil
}

// A scanner reads a run in order, for a merge.
type scanner struct {
	r  *run
	br *bufio.Reader
	n  int64
}

func (r *run) scan() *scanner {
	return &scanner{r: r, br: bufio.NewReaderSize(io.NewSectionReader(r.f, 0, r.count*8), 1<<20)}
}

func (s *scanner) next() (uint64, bool, error) {
	if s.n >= s.r.count {
		return 0, false, nil
	}
	var buf [8]byte
	if _, err := io.ReadFull(s.br, buf[:]); err != nil {
		return 0, false, fmt.Errorf("crawl: reading %s: %w", s.r.name, err)
	}
	s.n++
	return binary.BigEndian.Uint64(buf[:]), true, nil
}

// push appends one URL to the bucket.
func (b *bucket) push(canonical string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pushLocked(canonical)
}

func (b *bucket) pushLocked(canonical string) error {
	n, err := b.bw.WriteString(canonical + "\n")
	if err != nil {
		return fmt.Errorf("crawl: queueing a URL: %w", err)
	}
	b.end += int64(n)
	return nil
}

// take reads the next URL out of a bucket, or returns empty when the bucket has
// nothing left.
func (b *bucket) take() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.takeLocked()
}

// takeLocked is take with the bucket's lock already held.
//
// The head only moves for a line that was returned, so a crawl killed between
// here and the fetch loses the batch it was holding and no more.
func (b *bucket) takeLocked() (string, error) {
	if b.head >= b.end {
		return "", nil
	}
	line, err := b.br.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			// End of what has reached the disk, which is not the same as the end
			// of the bucket. The writer's buffer fills in the middle of a URL as
			// often as anywhere else, and when it does the front of that URL is on
			// the disk and the rest is still in memory. Those bytes are out of the
			// reader now whatever this returns, so they are held here and put back
			// in front of the next read. Dropping them would hand the tail of a URL
			// out as though it were a whole one.
			b.part += line
			return "", nil
		}
		return "", fmt.Errorf("crawl: reading %s: %w", b.path, err)
	}
	line, b.part = b.part+line, ""
	b.head += int64(len(line))
	return strings.TrimSuffix(line, "\n"), nil
}

// arm makes sure the bucket has something for [bucket.take] to find, which means
// flushing the write buffer only when the reader has caught up with what has
// already reached the disk.
//
// [Frontier.Next] used to flush all sixty four buckets on every call, because a
// reader cannot see what is sitting in a write buffer and on a small queue that
// is most of it. On a real queue it is almost none of it: server2 carries twenty
// six million URLs and the reader is megabytes behind the disk on every bucket,
// so those flushes were sixty four locks and sixty four small writes per batch,
// taken against two thousand workers appending to the same buckets, to make
// visible something nobody was going to read for another ten minutes.
func (b *bucket) arm() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.readyLocked() > 0 || b.bw.Buffered() == 0 {
		return nil
	}
	if err := b.bw.Flush(); err != nil {
		return fmt.Errorf("crawl: flushing the queue: %w", err)
	}
	return nil
}

// at is how far this bucket has been read, which is the one thing about a bucket
// the manifest has to write down. It takes the lock because [Frontier.Next]
// moves the head without the frontier's.
func (b *bucket) at() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.head
}

// readyLocked is how many bytes are on the disk and not yet read.
//
// end counts everything written including what is still buffered, and head
// counts what has been handed out, so neither on its own says whether a read
// would find anything. part is in there because those bytes came off the disk
// and are being held for the rest of their line, so they are read and not
// counted in head.
func (b *bucket) readyLocked() int64 {
	return b.end - int64(b.bw.Buffered()) - b.head - int64(len(b.part))
}

// trim writes the buffer out and hands the disk back when the consumed head has
// grown past limit.
func (b *bucket) trim(limit int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.bw.Flush(); err != nil {
		return fmt.Errorf("crawl: flushing the queue: %w", err)
	}
	if b.head < limit {
		return nil
	}
	return b.compact()
}

// compact cuts the consumed head off a queue file. Everything is closed and
// reopened around the rename, because the point is to hand the disk back and a
// file still held open is a file still on the disk. It is called with the
// bucket's lock held.
func (b *bucket) compact() error {
	if err := b.bw.Flush(); err != nil {
		return fmt.Errorf("crawl: compacting %s: %w", b.path, err)
	}
	tmp := b.path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("crawl: compacting %s: %w", b.path, err)
	}
	src := io.NewSectionReader(b.r, b.head, b.end-b.head)
	n, err := io.Copy(out, src)
	if err != nil {
		_ = out.Close()
		return fmt.Errorf("crawl: compacting %s: %w", b.path, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("crawl: compacting %s: %w", b.path, err)
	}
	if err := b.w.Close(); err != nil {
		return fmt.Errorf("crawl: compacting %s: %w", b.path, err)
	}
	if err := b.r.Close(); err != nil {
		return fmt.Errorf("crawl: compacting %s: %w", b.path, err)
	}
	if err := os.Rename(tmp, b.path); err != nil {
		return fmt.Errorf("crawl: compacting %s: %w", b.path, err)
	}
	w, err := os.OpenFile(b.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("crawl: compacting %s: %w", b.path, err)
	}
	r, err := os.Open(b.path)
	if err != nil {
		_ = w.Close()
		return fmt.Errorf("crawl: compacting %s: %w", b.path, err)
	}
	b.w, b.bw, b.r, b.br = w, bufio.NewWriterSize(w, 1<<16), r, bufio.NewReaderSize(r, 1<<16)
	// The copy started at the head, so a part line held from an earlier read is
	// back in the file and holding it as well would hand it out twice.
	b.part = ""
	b.head, b.end = 0, n
	return nil
}

// hashOf is the first eight bytes of the blake3 of a string, which is what the
// seen set is keyed on and what decides a URL's bucket.
func hashOf(s string) uint64 {
	h := doc.SumString(s)
	return binary.BigEndian.Uint64(h[:8])
}

// hostOf pulls the host out of a canonical URL without parsing the whole thing
// again. Canon has already normalised it, so what is between the scheme and the
// first slash is the authority and nothing else.
func hostOf(canonical string) (string, error) {
	_, rest, ok := strings.Cut(canonical, "://")
	if !ok {
		return "", fmt.Errorf("crawl: %q is not an absolute URL", canonical)
	}
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		return "", fmt.Errorf("crawl: %q has no host", canonical)
	}
	return rest, nil
}
