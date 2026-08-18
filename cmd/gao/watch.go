package main

// What the box was actually holding while the ingest ran.
//
// The disk budget in may is arithmetic: two shards per worker, and it does not
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
// that produced the reading, and 'gao box peak' reads the file back.
//
// Ten seconds between samples, against the thirty second resolution a peak is
// read at, because a part written and pushed inside a sampling gap is a part the
// watcher never saw and the gap is what the reading is graded on.

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

	"github.com/tamnd/gao/may"
)

// watchEvery is the gap between samples.
var watchEvery = may.Resolution / 3

// watcher samples the disk a run is holding and writes one JSON line per
// sample.
type watcher struct {
	f     *os.File
	enc   *json.Encoder
	dirs  []string
	box   string
	stage func() string

	start time.Time
	done  chan struct{}
	wg    sync.WaitGroup

	mu  sync.Mutex
	err error
}

// watch starts sampling. The trace is opened before the run rather than at the
// first sample, so a path nobody can write to costs a second instead of the
// hours the run takes.
func watch(path string, dirs []string, box string, stage func() string) (*watcher, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("gao gat hf: opening the disk trace: %w", err)
	}
	w := &watcher{
		f:     f,
		enc:   json.NewEncoder(f),
		dirs:  dirs,
		box:   box,
		stage: stage,
		start: time.Now(),
		done:  make(chan struct{}),
	}
	w.sample(w.start)

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		t := time.NewTicker(watchEvery)
		defer t.Stop()
		for {
			select {
			case <-w.done:
				return
			case at := <-t.C:
				w.sample(at)
			}
		}
	}()
	return w, nil
}

// sample writes one reading. A failure is kept and reported at the close rather
// than stopping the run, because the run is the thing worth having and a trace
// with a hole in it still says where the peak was.
func (w *watcher) sample(at time.Time) {
	held, err := heldBytes(w.dirs)
	if err != nil {
		w.fail(err)
		return
	}
	s := may.Sample{
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
func (w *watcher) Close() error {
	close(w.done)
	w.wg.Wait()
	w.sample(time.Now())

	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.err
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("gao gat hf: the disk trace: %w", err)
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
