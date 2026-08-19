package main

// What the box was actually holding while the ingest ran.
//
// The disk budget in fleet is arithmetic: two shards per worker, and it does not
// read the size of the corpus because a worker pushes a finished part and
// deletes it before opening the next one. The milestone does not gate on that.
// It gates on a measurement taken during a run, because the arithmetic knows
// about parts in flight and knows nothing about a Parquet writer's row group
// buffer, a part waiting out an upload retry, or whatever the operating system
// left in a temporary directory.
//
// So the run writes its own trace. A shell loop around du would do the same
// arithmetic and would not be part of the thing being measured, which matters
// on the day somebody has to reproduce the number: the flag is in the command
// that produced the reading, and 'gao fleet peak' reads the file back.
//
// Ten seconds between samples, against the thirty second resolution a peak is
// read at, because a part written and pushed inside a sampling gap is a part the
// watcher never saw and the gap is what the reading is graded on.
//
// A sample is a walk of the directories the run writes to, and it used to happen
// on the goroutine holding the ticker. A time.Ticker drops a tick nobody is
// waiting on, so on a box busy enough for that walk to take longer than ten
// seconds the walk was eating the next sample. server1's FineWeb2 trace came
// back with 2168 gaps of exactly 10s and 26 longer ones, every one a multiple of
// ten, up to 1m10s. That is a dropped tick and not a slow disk, and 'gao fleet
// peak' refused six hours of real ingest over it, which is the correct answer to
// the trace and the wrong answer about the run.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tamnd/gao/fleet"
)

// watchEvery is the gap between samples.
var watchEvery = fleet.Resolution / 3

// watchHeld is how a sample measures what the run is holding. A test replaces
// it before the watcher starts, to make a walk slow without a slow filesystem.
var watchHeld = heldBytes

// watchSlots is how many samples may be walking at once.
//
// There is a cap because a filesystem that has stopped answering would otherwise
// get a new goroutine every ten seconds for the length of the run, and a watcher
// that takes a box down is worse than a watcher that misses a reading. When
// every slot is busy the tick is dropped and the hole it leaves in the trace is
// what 'gao fleet peak' grades the trace on, which is where a filesystem that
// slow belongs in the report.
const watchSlots = 4

// watcher samples the disk a run is holding and writes one JSON line per
// sample.
type watcher struct {
	f     *os.File
	enc   *json.Encoder
	box   string
	stage func() string

	// held is what the run is holding right now, which is a walk of its
	// directories. It is a field rather than a call so a test can make a sample
	// slow without making a filesystem slow, and it is set before the ticker
	// starts because a watcher whose measurement can be swapped mid run is a
	// data race that only shows up under load.
	held func() (int64, error)

	start time.Time
	done  chan struct{}
	tick  sync.WaitGroup
	takes sync.WaitGroup
	slots chan struct{}

	mu  sync.Mutex
	err error
}

// watch starts sampling. The trace is opened before the run rather than at the
// first sample, so a path nobody can write to costs a second instead of the
// hours the run takes.
func watch(path string, dirs []string, box string, stage func() string) (*watcher, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("gao harvest hf: opening the disk trace: %w", err)
	}
	w := &watcher{
		f:     f,
		enc:   json.NewEncoder(f),
		box:   box,
		stage: stage,
		held:  func() (int64, error) { return watchHeld(dirs) },
		start: time.Now(),
		done:  make(chan struct{}),
		slots: make(chan struct{}, watchSlots),
	}
	w.sample(w.start)

	w.tick.Add(1)
	go func() {
		defer w.tick.Done()
		t := time.NewTicker(watchEvery)
		defer t.Stop()
		for {
			select {
			case <-w.done:
				return
			case at := <-t.C:
				w.take(at)
			}
		}
	}()
	return w, nil
}

// take samples on its own goroutine, so a walk that runs long costs its own
// reading rather than the next one.
//
// The sample carries the tick it belongs to rather than the time it finished, so
// the readings stay on the ten second grid even when one of them lands late, and
// a late one can be written after an early one. gao fleet peak sorts the trace
// before it reads it, for this reason.
func (w *watcher) take(at time.Time) {
	select {
	case w.slots <- struct{}{}:
	default:
		return
	}
	w.takes.Add(1)
	go func() {
		defer w.takes.Done()
		defer func() { <-w.slots }()
		w.sample(at)
	}()
}

// sample writes one reading. A failure is kept and reported at the close rather
// than stopping the run, because the run is the thing worth having and a trace
// with a hole in it still says where the peak was.
func (w *watcher) sample(at time.Time) {
	held, err := w.held()
	if err != nil {
		w.fail(err)
		return
	}
	s := fleet.Sample{
		Second:  int64(at.Sub(w.start) / time.Second),
		Bytes:   held,
		Box:     w.box,
		Stage:   w.stage(),
		Workers: 1,
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.enc.Encode(s); err != nil && w.err == nil {
		w.err = err
	}
}

func (w *watcher) fail(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil {
		w.err = err
	}
}

// Close takes a last sample, stops the ticker and closes the file. The last
// sample is what says how long the trace covers, and a trace that stops a minute
// before the run does reads as a run that was watched for less than it ran.
//
// The ticker goroutine is waited on first and the samples second, because the
// ticker is the only thing that starts a sample and a WaitGroup cannot be added
// to while it is being waited on.
func (w *watcher) Close() error {
	close(w.done)
	w.tick.Wait()
	w.takes.Wait()
	w.sample(time.Now())

	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.err
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("gao harvest hf: the disk trace: %w", err)
	}
	return nil
}

// heldBytes is what the run is holding across the directories it writes to.
//
// A file that goes away between the listing and the stat is not an error. That
// is a part being deleted after its push, which is the whole behavior being
// measured, and a watcher that failed on it would fail on every successful run.
func heldBytes(dirs []string) (int64, error) {
	var total int64
	for _, dir := range roots(dirs) {
		err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			total += info.Size()
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, err
		}
	}
	return total, nil
}

// roots is the directories to walk, with the ones contained in another dropped.
//
// The usual way to run this is -dir ingest -out ingest/parts, and a walk of both
// counts every part twice. That is not a rounding error on the reading the disk
// budget is graded against, it is the difference between 45 GB and a run that
// looks like it went over.
func roots(dirs []string) []string {
	abs := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		p, err := filepath.Abs(dir)
		if err != nil {
			// Keep it as given. A path the working directory cannot resolve is
			// still a path the walk can try, and a sample is worth more than a
			// tidy error.
			p = filepath.Clean(dir)
		}
		abs = append(abs, p)
	}
	// Shortest first, so a parent is always considered before what is under it.
	slices.SortFunc(abs, func(a, b string) int { return len(a) - len(b) })

	var out []string
	for _, p := range abs {
		if slices.ContainsFunc(out, func(root string) bool { return under(p, root) }) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// under reports whether p is root or sits inside it.
func under(p, root string) bool {
	if p == root {
		return true
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
