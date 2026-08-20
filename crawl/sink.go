package crawl

// Where a crawl puts what it fetched, while it is still fetching.
//
// Three streams come out of a crawl and they have different lifetimes. The WARC
// is the raw exchange, request and response, and it is the only copy of the
// bytes a document was extracted from, so it stays on the box that fetched it
// and is aged out on a rule the operator sets. The kept documents go to a repo
// as one row per page with the text withheld, because a crawled page carries no
// grant to pass its text on and the row without the text is what lets somebody
// else fetch the same pages under their own access. The rejections go to a repo
// of their own with the stage and the reason, because a threshold that turns out
// to be wrong is only recoverable if what it removed can be found.
//
// Both repos are written the way the ingest writes: a part is filled, closed,
// pushed, and deleted here before the next one opens. What the box holds is one
// part being written rather than the corpus, which is what lets a crawl of a
// billion pages run on a disk that could not hold a hundredth of it.
//
// A crawl is not replayable, which is the one thing that makes this different
// from the ingest sink. An ingest that dies reads its input file again from the
// start and writes over the parts it left behind. A crawl that dies is started
// again and carries on with URLs the first run never saw, so a part number it
// already used names a file holding other pages. The counters are therefore on
// the disk beside the frontier and are read back at open.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/harvest"
	"github.com/tamnd/gao/store"
)

// The repos a crawl writes. Both are working tier and both are public: the
// crawl produces no text anybody may republish, so what ships is the addresses
// and the measurements.
const (
	KeptRepo   = "vitweb"
	RejectRepo = "vitweb-rejects"
)

// Stage is what a part written by this says wrote it.
const Stage = "gao-crawl@" + PipelineVersion

// DefaultVolume is how much a WARC volume holds before the writer opens the
// next one. A gigabyte is small enough to age out one at a time and large
// enough that a long run is not a directory of ten thousand files.
const DefaultVolume = 1 << 30

// DefaultPartEvery is how long a part stays open before the sink closes it
// although it is not full.
//
// Six hours is a compromise between two costs that pull opposite ways. Cutting
// often gives the reader a fresh dataset and the box its disk back, and pays
// for it in files: a crawl that cut every minute would leave a repo of tiny
// parts nobody can query quickly. Cutting rarely gives whole parts and a repo
// that is hours behind the crawl. Six hours on one box at the rate a polite
// crawler runs at is a part of tens of thousands of rows, which is a file worth
// opening, and it means the disk never holds more than a morning's work.
const DefaultPartEvery = 6 * time.Hour

// SinkOptions are what a sink needs. Dir and Snapshot are required.
type SinkOptions struct {
	// Dir is the working directory. Everything under it is cache: the WARC
	// volumes are aged out on the rule below and the parts are deleted as they
	// are pushed.
	Dir string

	// Snapshot is the crawl this run belongs to, which is the partition its
	// parts sit under. It reads name-revision, so gao-crawl-20260819 puts its
	// parts under data/gao/ and names the revision in every file.
	Snapshot string

	// Shard is this box's index in the fleet, which is the other half of a part
	// path. Two boxes writing the same shard write over each other, which is why
	// this is required to be distinct rather than convenient.
	Shard int

	// Box is the machine label that goes in every part's own metadata.
	Box string

	// Version is the crawler version, which goes in the warcinfo record beside
	// the User-Agent so that a file found later says which program made it.
	Version string

	// Push, when set, is where a finished part goes. The local copy is deleted
	// once the store has it. A nil Push keeps everything on the disk, which is
	// what a first run against real sites wants.
	//
	// It takes no context because a roll calls it from wherever a part happened
	// to close. The run's context belongs to whoever builds this function, and
	// closing over it is how a stopped run stops its uploads too.
	Push func(d store.Dataset, local, path string) error

	// Volume is how large a WARC volume grows before the next one opens. Zero
	// is [DefaultVolume].
	Volume int64

	// Keep is how many finished WARC volumes stay on the disk. Zero keeps every
	// one of them, which is right for a run whose output somebody is going to
	// look at and wrong for a run that has to fit on a disk.
	Keep int

	// TextPerPart and BytesPerPart override the roll's part size. They are here
	// for tests, which cannot afford to write half a gigabyte to watch a part
	// close.
	TextPerPart, BytesPerPart int64

	// PartEvery is how long a part may stay open before it is closed and pushed
	// although it is not full. Zero is [DefaultPartEvery].
	//
	// A crawl's rows are metadata, so a part closing on size closes after a
	// million and a half pages, which on one box is weeks. Everything written in
	// those weeks would sit on the disk that is meant to be cache and out of
	// reach of anybody reading the dataset. The clock is what makes the crawl
	// publish while it runs.
	PartEvery time.Duration

	// Out, when set, gets one line per part as it lands.
	Out io.Writer
}

// SinkStats is what a sink has done, which is the half of a crawl's progress
// that is about output rather than about the frontier.
type SinkStats struct {
	Archived int64 `json:"archived"`
	Kept     int64 `json:"kept"`
	Dropped  int64 `json:"dropped"`

	Volumes   int   `json:"volumes"`
	WARCBytes int64 `json:"warc_bytes"`
	Aged      int   `json:"aged"`

	Parts     int   `json:"parts"`
	PartBytes int64 `json:"part_bytes"`
	Pushed    int   `json:"pushed"`
	Freed     int64 `json:"freed"`
}

// sinkState is the counters that survive a restart.
type sinkState struct {
	Volume int `json:"volume"`
	Kept   int `json:"kept_part"`
	Reject int `json:"reject_part"`
}

// warcGzip is whether the volumes are compressed. It is not an option because a
// run whose archive does not fit on the disk is not a run, and it is a named
// constant because the sink now compresses a record in one place and writes it
// in another and the two have to agree.
const warcGzip = true

// A stream is one repo's part writer with the lock that serializes it.
//
// The lock is per repo rather than one for the pair because the two are written
// at different moments by different workers, and because closing a part sends it
// to the store: a kept part being uploaded should not stop the rejects being
// written for the minute that takes.
type stream struct {
	mu   sync.Mutex
	roll *store.Roll
	// opened is when the part being written was first written to. The zero time
	// means no part is open, which is the state a fresh sink and a sink that has
	// just pushed a part are both in.
	opened time.Time
}

// sinkCount is the running totals. They are atomics rather than fields under a
// lock because every worker adds to them and reading them for a progress line
// should not have to wait behind a part upload.
type sinkCount struct {
	archived, kept, dropped         atomic.Int64
	volumes, warcBytes, aged        atomic.Int64
	parts, partBytes, pushed, freed atomic.Int64
}

// A Sink is the output side of one crawl on one box.
//
// It is safe for concurrent use, and it holds three locks rather than one. That
// was one lock, on the reasoning that a crawl spends its time waiting for hosts
// rather than waiting for a file. Measurement said otherwise. A goroutine dump
// of a two thousand worker run on server3 had 1,841 workers blocked on that one
// lock and 75 of them on the network, 1,173 waiting to append a Parquet row and
// 668 waiting to write a WARC record, because the record was being gzipped at
// the highest deflate level with the lock held and every other worker on the box
// was queueing behind that one compression.
//
// So the compression moved out of the lock, which [harvest.EncodeRecord] is
// for, and the lock became three: one for the WARC stream, and one for each of
// the two repos. Two workers writing WARC records at once would still interleave
// them, so the archive is still serialized, but it is serialized around a file
// write rather than around a compressor.
type Sink struct {
	o     SinkOptions
	kept  store.Dataset
	drops store.Dataset

	// streams is fixed at open and never written to afterwards, so a worker
	// reads it without a lock and takes the lock inside the stream it found.
	streams map[string]*stream

	warcMu sync.Mutex
	warc   *harvest.WARCWriter
	file   *os.File
	name   string
	done   []string

	// stateMu is the innermost lock. It is taken while one of the others is
	// held and never the other way round, which is what keeps the order safe.
	stateMu sync.Mutex
	state   sinkState

	// outMu keeps the two repos from interleaving their progress lines, which
	// they can now do because they no longer share a lock.
	outMu sync.Mutex

	closed atomic.Bool
	stats  sinkCount
}

// VolumePath is where one WARC volume lives under the working directory.
//
// The volumes are outside the data directory the parts are written under,
// because that directory is a mirror of the repo tree and a WARC is not
// something the repo takes.
func VolumePath(snapshot string, shard, volume int) string {
	return fmt.Sprintf("warc/%s-%05d-%05d.warc.gz", snapshot, shard, volume)
}

// OpenSink opens the sink, picking up the part and volume numbers a previous
// run left behind.
func OpenSink(o SinkOptions) (*Sink, error) {
	if o.Dir == "" {
		return nil, fmt.Errorf("crawl: a sink needs a directory to write under")
	}
	if o.Snapshot == "" {
		return nil, fmt.Errorf("crawl: a sink needs a snapshot, which is the partition its parts sit under")
	}
	if o.Volume <= 0 {
		o.Volume = DefaultVolume
	}
	if o.PartEvery <= 0 {
		o.PartEvery = DefaultPartEvery
	}
	kept, ok := store.Lookup(KeptRepo)
	if !ok {
		return nil, fmt.Errorf("crawl: %s is not in the dataset table", KeptRepo)
	}
	drops, ok := store.Lookup(RejectRepo)
	if !ok {
		return nil, fmt.Errorf("crawl: %s is not in the dataset table", RejectRepo)
	}
	if err := os.MkdirAll(filepath.Join(o.Dir, "warc"), 0o755); err != nil {
		return nil, fmt.Errorf("crawl: making the volume directory: %w", err)
	}
	s := &Sink{o: o, kept: kept, drops: drops, streams: map[string]*stream{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	if err := s.found(); err != nil {
		return nil, err
	}
	s.streams[KeptRepo] = &stream{roll: s.roll(kept, s.state.Kept)}
	s.streams[RejectRepo] = &stream{roll: s.roll(drops, s.state.Reject)}
	return s, nil
}

// found picks up the volumes an earlier run left on the disk, so that Keep
// counts what is there rather than what this run wrote.
//
// A crawl is restarted. It is restarted to take a new binary, and it is
// restarted by its own supervisor after a box loses its network, so the run that
// is going now is rarely the run that wrote the volumes under it. Aging only
// what this process wrote means a box set to keep two volumes keeps two per run
// and every restart resets the count, which is how a disk that is supposed to
// hold a gigabyte ends up holding all of it. server2 had four volumes and 7 GB
// of headroom when this was found.
//
// Every snapshot's volumes are collected and not only this one's. Keep means how
// many volumes may sit on the disk, and counting per snapshot means a box set to
// keep two keeps two of each snapshot it has ever run, which is unbounded in the
// number of snapshots rather than in anything about the disk. That is not
// theoretical: a run started under a new snapshot to measure a frontier change
// left every box over its limit within a day, server1 holding eight volumes
// against a keep of four and server2 five against two.
//
// Only this shard's volumes are collected. A box crawling shard 1 that deleted a
// file named for shard 2 would be deleting another box's archive, and one
// directory shared by two shards is a mistake worth surviving rather than
// compounding. So the shard is matched and the snapshot is not, which is why
// this reads the name apart rather than taking a prefix: the snapshot sits in
// front of the shard and a prefix cannot skip it.
func (s *Sink) found() error {
	dir := filepath.Join(s.o.Dir, "warc")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("crawl: reading the volume directory: %w", err)
	}
	shard := fmt.Sprintf("%05d", s.o.Shard)
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".warc.gz") {
			continue
		}
		// snapshot-shard-volume.warc.gz, and the snapshot may hold dashes of its
		// own, so the shard is counted back from the end rather than forward
		// from the start.
		f := strings.Split(strings.TrimSuffix(n, ".warc.gz"), "-")
		if len(f) < 3 || f[len(f)-2] != shard {
			continue
		}
		names = append(names, "warc/"+n)
	}
	// Sorted on the volume number alone, which is a counter kept on the box and
	// carries on across snapshots, so it is the order the volumes were written
	// and the oldest goes first. Sorting the whole name would sort on the
	// snapshot first, and a snapshot named for anything other than a date would
	// then put an old volume after a new one and delete the wrong file.
	sort.Slice(names, func(i, j int) bool { return volumeOf(names[i]) < volumeOf(names[j]) })
	s.done = names
	return s.age()
}

// roll builds the roll for one dataset, starting at the part number this box
// has already reached.
func (s *Sink) roll(d store.Dataset, first int) *store.Roll {
	r := &store.Roll{
		// One directory per repo. Both rolls number their parts the same way,
		// so a shared directory would have the kept part and the reject part of
		// the same number writing over each other, and the second one to close
		// would push a file holding the other one's rows.
		Dir:     filepath.Join(s.o.Dir, d.Name),
		Dataset: d,
		Stamp: store.Stamp{
			Snapshot: s.o.Snapshot,
			Stage:    Stage,
			Box:      s.o.Box,
		},
		File:         s.o.Shard,
		First:        first,
		TextPerPart:  s.o.TextPerPart,
		BytesPerPart: s.o.BytesPerPart,
	}
	r.Finished = func(f store.PartFile) error { return s.finished(d, f) }
	return r
}

// Archive writes one visit to the WARC and returns the locator of its response,
// which is what a document points back at.
//
// The record goes in before anything is extracted from it, so a page whose
// extraction fails is still in the archive. That is the point of keeping one: an
// extractor is a program we will change, and a page it got wrong is only worth
// having if the bytes are still here when the next version runs.
func (s *Sink) Archive(v *harvest.Visit, at time.Time) (string, error) {
	if s.closed.Load() {
		return "", fmt.Errorf("crawl: that sink is closed")
	}

	// Built and compressed before the lock is taken, which is the whole of what
	// this costs. Each record is its own gzip member, so a worker can do its own
	// deflating on its own core and take its turn at the file only to hand over
	// bytes that are already finished.
	recs := harvest.VisitRecords(v, at, harvest.Agent(s.o.Version))
	bodies := make([][]byte, len(recs))
	response := -1
	for i, r := range recs {
		b, err := harvest.EncodeRecord(r, warcGzip)
		if err != nil {
			return "", fmt.Errorf("crawl: encoding %s: %w", v.URL, err)
		}
		bodies[i] = b
		if r.Type() == "response" {
			response = i
		}
	}

	s.warcMu.Lock()
	defer s.warcMu.Unlock()
	if s.closed.Load() {
		return "", fmt.Errorf("crawl: that sink is closed")
	}
	if err := s.volume(); err != nil {
		return "", err
	}

	var locator string
	for i, b := range bodies {
		offset, length, err := s.warc.Append(b)
		if err != nil {
			return "", fmt.Errorf("crawl: writing %s to %s: %w", v.URL, s.name, err)
		}
		if i == response {
			locator = fmt.Sprintf("%s@%d+%d", s.name, offset, length)
		}
	}
	s.stats.archived.Add(1)
	if s.warc.Offset() >= s.o.Volume {
		if err := s.rotate(); err != nil {
			return locator, err
		}
	}
	return locator, nil
}

// Write puts one verdict where it belongs: the document in the kept repo, or
// the rejection with its stage and reason in the other one.
func (s *Sink) Write(v Verdict) error {
	if v.Doc == nil {
		return fmt.Errorf("crawl: a verdict arrived with no document, which is a bug in the caller")
	}
	if s.closed.Load() {
		return fmt.Errorf("crawl: that sink is closed")
	}
	name := RejectRepo
	if v.Kept {
		name = KeptRepo
	}
	st := s.streams[name]

	st.mu.Lock()
	defer st.mu.Unlock()
	if s.closed.Load() {
		return fmt.Errorf("crawl: that sink is closed")
	}
	if v.Kept {
		if err := st.roll.Append(v.Doc); err != nil {
			return err
		}
		s.stats.kept.Add(1)
		return s.due(st)
	}
	if err := st.roll.AppendReject(v.Doc, v.Stage, string(v.Reason), v.Detail); err != nil {
		return err
	}
	s.stats.dropped.Add(1)
	return s.due(st)
}

// due closes the part one repo is writing when it has been open longer than the
// run allows. It is called with that repo's lock held.
//
// It is called after the append rather than before it, so the part that is
// timed is the one the row just went into. That also means the clock is only
// read when a row arrives: a repo nothing is being written to holds its part
// open rather than pushing an empty one every interval, and a crawl that stops
// pushes what is open on the way out.
func (s *Sink) due(st *stream) error {
	if st.opened.IsZero() {
		st.opened = time.Now()
		return nil
	}
	if time.Since(st.opened) < s.o.PartEvery {
		return nil
	}
	return st.roll.Cut()
}

// Stats returns what the sink has written.
func (s *Sink) Stats() SinkStats {
	return SinkStats{
		Archived:  s.stats.archived.Load(),
		Kept:      s.stats.kept.Load(),
		Dropped:   s.stats.dropped.Load(),
		Volumes:   int(s.stats.volumes.Load()),
		WARCBytes: s.stats.warcBytes.Load(),
		Aged:      int(s.stats.aged.Load()),
		Parts:     int(s.stats.parts.Load()),
		PartBytes: s.stats.partBytes.Load(),
		Pushed:    int(s.stats.pushed.Load()),
		Freed:     s.stats.freed.Load(),
	}
}

// Flush closes the open WARC volume without closing the sink, so that a
// checkpoint leaves a file a reader can open. The parts are left alone: a part
// closed early is a small part, and a crawl that checkpointed every minute would
// publish a thousand of them.
func (s *Sink) Flush() error {
	s.warcMu.Lock()
	defer s.warcMu.Unlock()
	if s.closed.Load() || s.warc == nil {
		return nil
	}
	return s.rotate()
}

// Close finishes both rolls and the open volume.
func (s *Sink) Close() error {
	if s.closed.Swap(true) {
		return nil
	}

	// The flag is set first and each lock is taken after it, so a worker already
	// inside Write or Archive finishes the row it was writing and the next one
	// to arrive is turned away rather than finding a closed roll.
	var first error
	for _, name := range []string{KeptRepo, RejectRepo} {
		st := s.streams[name]
		st.mu.Lock()
		_, err := st.roll.Close()
		st.mu.Unlock()
		if err != nil && first == nil {
			first = err
		}
	}

	s.warcMu.Lock()
	if s.warc != nil {
		if err := s.rotate(); err != nil && first == nil {
			first = err
		}
	}
	s.warcMu.Unlock()

	if err := s.save(); err != nil && first == nil {
		first = err
	}
	return first
}

// volume opens the current WARC volume if none is open, writing the warcinfo
// record that says who made the file. It is called with the WARC lock held.
func (s *Sink) volume() error {
	if s.warc != nil {
		return nil
	}
	// A volume already at that name is one a run wrote before it was killed. It
	// is left exactly as it is and this one takes the next number, because the
	// documents that run pushed carry offsets into it and appending to it or
	// writing over it would leave them pointing at the wrong bytes.
	var f *os.File
	var rel string
	s.stateMu.Lock()
	for {
		rel = VolumePath(s.o.Snapshot, s.o.Shard, s.state.Volume)
		var err error
		f, err = os.OpenFile(filepath.Join(s.o.Dir, filepath.FromSlash(rel)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			s.stateMu.Unlock()
			return fmt.Errorf("crawl: opening %s: %w", rel, err)
		}
		s.state.Volume++
	}
	s.stateMu.Unlock()
	s.file, s.name, s.warc = f, rel, harvest.NewWARCWriter(f, warcGzip)
	info := harvest.Info(harvest.WARCInfo{
		Filename:  filepath.Base(rel),
		Software:  Stage,
		Agent:     harvest.Agent(s.o.Version),
		Operator:  "gao",
		Contact:   harvest.Contact,
		IsPartOf:  s.o.Snapshot,
		Described: time.Now().UTC(),
	})
	if _, _, err := s.warc.Write(info); err != nil {
		return fmt.Errorf("crawl: writing the warcinfo record of %s: %w", rel, err)
	}
	return nil
}

// rotate closes the open volume and moves the counter on, aging out the oldest
// volume if the run is only keeping a few. It is called with the WARC lock held.
func (s *Sink) rotate() error {
	if s.warc == nil {
		return nil
	}
	n := s.warc.Offset()
	name := s.name
	s.warc, s.name = nil, ""
	f := s.file
	s.file = nil
	if err := f.Close(); err != nil {
		return fmt.Errorf("crawl: closing %s: %w", name, err)
	}
	s.stats.volumes.Add(1)
	s.stats.warcBytes.Add(n)
	s.stateMu.Lock()
	s.state.Volume++
	s.stateMu.Unlock()
	s.done = append(s.done, name)
	if err := s.age(); err != nil {
		return err
	}
	return s.save()
}

// age deletes the oldest finished volumes down to the number the run keeps.
//
// The rule is the same one the parts run under and it is here for the same
// reason: the disk under a crawler is cache. What it costs is that a document
// whose locator names a deleted volume points at bytes nobody has, and that is
// the trade a run makes when it sets the number.
//
// It is called with the WARC lock held, which is what guards s.done.
func (s *Sink) age() error {
	if s.o.Keep <= 0 {
		return nil
	}
	for len(s.done) > s.o.Keep {
		old := s.done[0]
		s.done = s.done[1:]
		if err := os.Remove(filepath.Join(s.o.Dir, filepath.FromSlash(old))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("crawl: aging out %s: %w", old, err)
		}
		s.stats.aged.Add(1)
	}
	return nil
}

// finished takes one closed part to the store and gives the disk back.
//
// It is the roll's own callback, so it runs inside the append that closed the
// part and that repo's lock is already held. That is what makes the write to
// opened below safe and it is why the upload underneath it stops only this repo
// rather than the crawl.
func (s *Sink) finished(d store.Dataset, f store.PartFile) error {
	s.stats.parts.Add(1)
	s.stats.partBytes.Add(f.Bytes)
	s.count(d)
	// This repo has no part open now, whether it was the clock or the size that
	// closed the last one. The next row to arrive starts the next part and the
	// clock on it.
	s.streams[d.Name].opened = time.Time{}

	// The number goes to the disk before the part goes to the hub, and it used
	// to go after.
	//
	// What is between them is an upload, which on a part of a few megabytes is
	// seconds. A run that stops in there comes back holding the number it had
	// before, writes the same path again, and on the hub a path written again
	// replaces what is at it. That is not a gap in a dataset, it is rows that
	// were published and are not any more. Thirteen parts across the two repos
	// have two Add commits at the same path, and the pairs are minutes apart at
	// exactly the times the fleet was restarted.
	//
	// The other order costs a gap: a number spent by a part that never made it
	// up. A dataset with a part number missing is a dataset somebody has to
	// explain, which is a great deal better than a dataset that quietly holds
	// half of what it was told to hold.
	//
	// It narrows the window rather than closing it. A run stopped between this
	// save and the upload leaves the gap, a run stopped during the upload leaves
	// the gap, and a run whose sink is still uploading when the next run opens
	// the file still reads the old number. Not replacing a path that is already
	// on the hub is the thing that actually closes it, and that belongs on the
	// push rather than here.
	if err := s.save(); err != nil {
		return err
	}

	verb := "wrote"
	if s.o.Push != nil {
		local := filepath.Join(s.o.Dir, d.Name, filepath.FromSlash(f.Path))
		if err := s.o.Push(d, local, f.Path); err != nil {
			// Named with the path it is still at, because the part number has
			// already moved on and the next run will not write this file again.
			// Somebody has to push it by hand and this is where they find out
			// which file and where.
			return fmt.Errorf("crawl: %s is written and still at %s: %w", f.Path, local, err)
		}
		if err := os.Remove(local); err != nil {
			return fmt.Errorf("crawl: %s is in the store and still here: %w", f.Path, err)
		}
		// The directory a part sat in is empty once the last part has gone, and
		// this fails while it is not, which is the condition to check.
		_ = os.Remove(filepath.Dir(local))
		s.stats.pushed.Add(1)
		s.stats.freed.Add(f.Bytes)
		verb = "pushed"
	}
	if s.o.Out != nil {
		s.outMu.Lock()
		fmt.Fprintf(s.o.Out, "%-8s %-52s %8s  %d rows\n", verb, d.Repo()+"/"+f.Path, fleet.GB(f.Bytes), f.Documents)
		s.outMu.Unlock()
	}
	return nil
}

// count moves the part number for one dataset on, so that a run started again
// numbers from where this one got to.
func (s *Sink) count(d store.Dataset) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if d.Name == KeptRepo {
		s.state.Kept++
		return
	}
	s.state.Reject++
}

// The sink state file, which is small and rewritten whole.
const stateFile = "sink.json"

func (s *Sink) load() error {
	b, err := os.ReadFile(filepath.Join(s.o.Dir, stateFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("crawl: reading %s: %w", stateFile, err)
	}
	if err := json.Unmarshal(b, &s.state); err != nil {
		return fmt.Errorf("crawl: reading %s: %w", stateFile, err)
	}
	return nil
}

// save rewrites the counter file. It takes the state lock for the whole of it
// rather than only for the marshal, because two repos closing a part at the same
// moment would otherwise be writing the same temporary file.
func (s *Sink) save() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.o.Dir, stateFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("crawl: writing %s: %w", stateFile, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("crawl: writing %s: %w", stateFile, err)
	}
	return nil
}

// volumeOf is the volume number in a WARC file name, or -1 when the name does
// not carry one. It is the last field, zero padded, and the only part of the
// name that orders volumes across snapshots.
func volumeOf(name string) int {
	f := strings.Split(strings.TrimSuffix(filepath.Base(name), ".warc.gz"), "-")
	if len(f) < 3 {
		return -1
	}
	n, err := strconv.Atoi(f[len(f)-1])
	if err != nil {
		return -1
	}
	return n
}
