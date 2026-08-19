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

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/frontier"
)

// DefaultBuckets is how many queue files the frontier writes.
//
// A URL goes to the bucket its host hashes to, and a batch is taken from the
// buckets in rotation, so a page that links to forty pages on one site does not
// get fetched as forty requests in a row to that site. Sixty four is enough
// mixing for that and few enough files to hold every one of them open, which
// matters more than it sounds: the alternative is an open file cache, and a
// cache in the queue is a cache in the one structure that cannot be rebuilt.
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

	mu sync.Mutex

	// filter is the resident approximate set, bits is its length in bits.
	filter []uint64
	bits   uint64

	// pending is the hashes offered since the last run was written, held both
	// as a map for the lookup and as a slice so the run can be written in the
	// order they arrived, sorted once.
	pending map[uint64]struct{}
	log     *os.File
	logw    *bufio.Writer

	runs []*run
	gen  int

	queue []*bucket
	turn  int

	stats Stats

	closed bool
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

	w  *os.File
	bw *bufio.Writer

	r  *os.File
	br *bufio.Reader

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
func (s Stats) Queued() int64 { return s.Admitted + s.Requeued + s.Deferred - s.Handed }

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
		f.gen, f.stats = m.Gen, m.Stats
		// A resume with a smaller filter than the one that filled it would be
		// wrong about everything. The plan is allowed to grow.
		o.Expect = max(o.Expect, m.Expect)
		f.o.Expect = o.Expect
	}

	f.bits = uint64(o.Expect) * uint64(o.Bits)
	if f.bits < 64 {
		f.bits = 64
	}
	f.filter = make([]uint64, (f.bits+63)/64)

	if err := f.openRuns(m); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.openLog(); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.openQueue(m); err != nil {
		f.Close()
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
			fh.Close()
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
func (f *Frontier) openLog() error {
	path := filepath.Join(f.dir, "pending.hashes")
	fh, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("crawl: opening the pending hashes: %w", err)
	}
	br := bufio.NewReaderSize(fh, 1<<20)
	var buf [8]byte
	var n int64
	for {
		if _, err := io.ReadFull(br, buf[:]); err != nil {
			break
		}
		h := binary.BigEndian.Uint64(buf[:])
		f.pending[h] = struct{}{}
		f.add(h)
		n++
	}
	// Truncate to whole hashes, for the same reason a run is.
	if err := fh.Truncate(n * 8); err != nil {
		fh.Close()
		return fmt.Errorf("crawl: opening the pending hashes: %w", err)
	}
	if _, err := fh.Seek(0, io.SeekEnd); err != nil {
		fh.Close()
		return fmt.Errorf("crawl: opening the pending hashes: %w", err)
	}
	f.log, f.logw = fh, bufio.NewWriterSize(fh, 1<<16)
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
			w.Close()
			return fmt.Errorf("crawl: opening the queue: %w", err)
		}
		r, err := os.Open(b.path)
		if err != nil {
			w.Close()
			return fmt.Errorf("crawl: opening the queue: %w", err)
		}
		b.w, b.bw, b.r, b.end = w, bufio.NewWriterSize(w, 1<<16), r, info.Size()
		if m != nil && i < len(m.Heads) {
			b.head = min(m.Heads[i], b.end)
		}
		if _, err := b.r.Seek(b.head, io.SeekStart); err != nil {
			w.Close()
			r.Close()
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
	canonical, err := frontier.Canon(rawurl)
	if err != nil {
		f.mu.Lock()
		f.stats.Offered++
		f.stats.Malformed++
		f.mu.Unlock()
		return false, err.Error(), nil
	}
	host, err := hostOf(canonical)
	if err != nil {
		f.mu.Lock()
		f.stats.Offered++
		f.stats.Malformed++
		f.mu.Unlock()
		return false, err.Error(), nil
	}

	// The fleet split comes before anything is remembered. A URL another box
	// owns is not this box's business at all, and recording it here would put a
	// hash in this frontier's set for a page this frontier will never fetch.
	if f.o.Fleet > 1 && int(hashOf(host)%uint64(f.o.Fleet)) != f.o.Shard {
		f.mu.Lock()
		f.stats.Offered++
		f.stats.Foreign++
		f.mu.Unlock()
		return false, fmt.Sprintf("%s belongs to box %d of %d", host, hashOf(host)%uint64(f.o.Fleet), f.o.Fleet), nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats.Offered++

	h := hashOf(canonical)
	seen, err := f.seen(h)
	if err != nil {
		return false, "", err
	}
	if seen {
		f.stats.Duplicate++
		return false, "already offered", nil
	}

	if f.o.Budget != nil {
		if ok, why := f.o.Budget.Offer(canonical); !ok {
			// The refusal is remembered too. A trap generates the same URL from
			// twenty pages and refusing it twenty times is twenty shape lookups
			// for an answer that will not change.
			if err := f.record(h); err != nil {
				return false, "", err
			}
			f.stats.Refused++
			return false, why, nil
		}
	}

	if err := f.record(h); err != nil {
		return false, "", err
	}
	if err := f.push(host, canonical); err != nil {
		return false, "", err
	}
	f.stats.Admitted++
	return true, "", nil
}

// Requeue puts a URL back without asking whether it has been seen, for a fetch
// that did not happen: a host that asked for time, a connection that broke. It
// is deliberately separate from Offer, because a URL coming back from a failed
// fetch is already in the seen set and Offer would refuse it.
func (f *Frontier) Requeue(canonical string) error {
	host, err := hostOf(canonical)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.push(host, canonical); err != nil {
		return err
	}
	f.stats.Requeued++
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
	f.mu.Lock()
	defer f.mu.Unlock()

	// The readers cannot see what is sitting in a write buffer, and on a small
	// queue that is most of it.
	for _, b := range f.queue {
		if err := b.bw.Flush(); err != nil {
			return nil, fmt.Errorf("crawl: reading the queue: %w", err)
		}
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
		b := f.queue[f.turn%f.o.Buckets]
		f.turn++
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
				f.stats.Malformed++
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
		host, err := hostOf(line)
		if err != nil {
			continue
		}
		if err := f.push(host, line); err != nil {
			return nil, err
		}
		f.stats.Deferred++
	}
	f.stats.Handed += int64(len(out))
	return out, nil
}

// Fetched reports what a URL turned into, which is what the budget earns on and
// what tells a template that produces nothing from one that produces articles.
func (f *Frontier) Fetched(canonical string, r frontier.Result) {
	f.mu.Lock()
	f.stats.Fetched++
	switch r {
	case frontier.New:
		f.stats.New++
	case frontier.Repeat:
		f.stats.Repeat++
	default:
		f.stats.Empty++
	}
	f.mu.Unlock()
	if f.o.Budget != nil {
		f.o.Budget.Fetched(canonical, r)
	}
}

// Stats is a snapshot of the counters.
func (f *Frontier) Stats() Stats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stats
}

// Flush writes everything that is only in memory: the queue buffers, the pending
// hashes, the queue heads and the counters. A crawl calls this on a timer, and
// what it costs to lose is what happened since the last one.
func (f *Frontier) Flush() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flush()
}

func (f *Frontier) flush() error {
	for _, b := range f.queue {
		if err := b.bw.Flush(); err != nil {
			return fmt.Errorf("crawl: flushing the queue: %w", err)
		}
		if b.head >= f.o.Compact {
			if err := b.compact(); err != nil {
				return err
			}
		}
	}
	if f.logw != nil {
		if err := f.logw.Flush(); err != nil {
			return fmt.Errorf("crawl: flushing the pending hashes: %w", err)
		}
	}
	if len(f.pending) >= f.o.Pending {
		if err := f.spill(); err != nil {
			return err
		}
	}
	return f.writeManifest()
}

// Close flushes and closes everything. A frontier that was closed cleanly opens
// again with nothing lost.
func (f *Frontier) Close() error {
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
	for _, b := range f.queue {
		if b.bw != nil {
			errs = append(errs, b.bw.Flush())
		}
	}
	errs = append(errs, f.writeManifest())
	for _, b := range f.queue {
		if b.w != nil {
			errs = append(errs, b.w.Close())
		}
		if b.r != nil {
			errs = append(errs, b.r.Close())
		}
	}
	if f.log != nil {
		errs = append(errs, f.log.Close())
	}
	for _, r := range f.runs {
		errs = append(errs, r.f.Close())
	}
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
		Stats:   f.stats,
	}
	for _, r := range f.runs {
		m.Runs = append(m.Runs, runMan{Name: r.name, Count: r.count})
	}
	for _, b := range f.queue {
		m.Heads = append(m.Heads, b.head)
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
func (f *Frontier) record(h uint64) error {
	f.add(h)
	f.pending[h] = struct{}{}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], h)
	if _, err := f.logw.Write(buf[:]); err != nil {
		return fmt.Errorf("crawl: recording a URL: %w", err)
	}
	if len(f.pending) >= f.o.Pending {
		if err := f.logw.Flush(); err != nil {
			return fmt.Errorf("crawl: recording a URL: %w", err)
		}
		return f.spill()
	}
	return nil
}

// push appends a canonical URL to its host's bucket.
func (f *Frontier) push(host, canonical string) error {
	b := f.queue[int(hashOf(host)%uint64(f.o.Buckets))]
	n, err := b.bw.WriteString(canonical + "\n")
	if err != nil {
		return fmt.Errorf("crawl: queueing a URL: %w", err)
	}
	b.end += int64(n)
	return nil
}

// seen reports whether a hash is in the exact set. The filter answers for most
// of what is new without a read, and everything it says yes to is checked
// against the pending map and then against the runs, newest first, because a URL
// offered twice tends to be offered twice close together.
func (f *Frontier) seen(h uint64) (bool, error) {
	if !f.maybe(h) {
		return false, nil
	}
	if _, ok := f.pending[h]; ok {
		return true, nil
	}
	for i := len(f.runs) - 1; i >= 0; i-- {
		ok, err := f.runs[i].has(h)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

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
	f.filter[a/64] |= 1 << (a % 64)
	f.filter[b/64] |= 1 << (b % 64)
	f.filter[c/64] |= 1 << (c % 64)
}

func (f *Frontier) maybe(h uint64) bool {
	a, b, c := f.probes(h)
	return f.filter[a/64]&(1<<(a%64)) != 0 &&
		f.filter[b/64]&(1<<(b%64)) != 0 &&
		f.filter[c/64]&(1<<(c%64)) != 0
}

// spill writes the pending hashes out as a sorted run and then merges runs that
// have grown to the same size.
//
// The merging is what keeps a lookup cheap. Without it a crawl that spills five
// hundred times has five hundred runs to search, and with it the runs double in
// size as they merge, so a billion URLs is around nine of them and every hash is
// rewritten around nine times rather than five hundred.
func (f *Frontier) spill() error {
	if len(f.pending) == 0 {
		return nil
	}
	hashes := make([]uint64, 0, len(f.pending))
	for h := range f.pending {
		hashes = append(hashes, h)
	}
	slices.Sort(hashes)

	f.gen++
	name := fmt.Sprintf("seen-%06d.hashes", f.gen)
	r, err := f.writeRun(name, hashes)
	if err != nil {
		return err
	}
	f.runs = append(f.runs, r)

	clear(f.pending)
	if err := f.log.Truncate(0); err != nil {
		return fmt.Errorf("crawl: clearing the pending hashes: %w", err)
	}
	if _, err := f.log.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("crawl: clearing the pending hashes: %w", err)
	}

	return f.compactRuns()
}

// compactRuns merges the last two runs while the newer one is at least half the
// size of the one before it, which is the binary counter that keeps the number
// of runs logarithmic in the number of URLs.
func (f *Frontier) compactRuns() error {
	for len(f.runs) >= 2 {
		a, b := f.runs[len(f.runs)-2], f.runs[len(f.runs)-1]
		if b.count*2 < a.count {
			return nil
		}
		f.gen++
		name := fmt.Sprintf("seen-%06d.hashes", f.gen)
		merged, err := f.mergeRuns(name, a, b)
		if err != nil {
			return err
		}
		f.runs = f.runs[:len(f.runs)-2]
		f.runs = append(f.runs, merged)
		for _, old := range []*run{a, b} {
			old.f.Close()
			if err := os.Remove(filepath.Join(f.dir, old.name)); err != nil {
				return fmt.Errorf("crawl: removing a merged run: %w", err)
			}
		}
	}
	return nil
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
			fh.Close()
			return nil, fmt.Errorf("crawl: writing a frontier run: %w", err)
		}
		if r.count%fanout == 0 {
			r.fence = append(r.fence, h)
		}
		r.count++
	}
	if err := bw.Flush(); err != nil {
		fh.Close()
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
		fh.Close()
		return nil, err
	}
	hb, okb, err := rb.next()
	if err != nil {
		fh.Close()
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
			fh.Close()
			return nil, err
		}
		if err := write(h); err != nil {
			fh.Close()
			return nil, fmt.Errorf("crawl: merging frontier runs: %w", err)
		}
	}
	if err := bw.Flush(); err != nil {
		fh.Close()
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
func (r *run) has(h uint64) (bool, error) {
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
	buf := make([]byte, n)
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

// take reads the next URL out of a bucket, or returns empty when the bucket has
// nothing left. The head only moves for a line that was returned, so a crawl
// killed between here and the fetch loses the batch it was holding and no more.
func (b *bucket) take() (string, error) {
	if b.head >= b.end {
		return "", nil
	}
	line, err := b.br.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", fmt.Errorf("crawl: reading %s: %w", b.path, err)
	}
	b.head += int64(len(line))
	return strings.TrimSuffix(line, "\n"), nil
}

// compact cuts the consumed head off a queue file. Everything is closed and
// reopened around the rename, because the point is to hand the disk back and a
// file still held open is a file still on the disk.
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
		out.Close()
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
		w.Close()
		return fmt.Errorf("crawl: compacting %s: %w", b.path, err)
	}
	b.w, b.bw, b.r, b.br = w, bufio.NewWriterSize(w, 1<<16), r, bufio.NewReaderSize(r, 1<<16)
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
