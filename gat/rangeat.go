package gat

// Random access over a file nobody can afford to download.
//
// Four of the six sources ship Parquet, and Parquet keeps its schema and its row
// group index in a footer at the end of the file. A reader has to know where the
// end is before it can read the beginning, so the format cannot be decoded from a
// stream that only goes forwards, and the files are 1.6 to 4.8 GB against a box
// that peaks at 4.1 GB for everything it is doing at once. Downloading one to read
// it is not available and neither is buffering it.
//
// What is left is to read the parts that are wanted, over the network, by Range
// request. [RangeAt] is an [io.ReaderAt] that does that. It reads in windows
// rather than in whatever size the caller asked for, because a Parquet reader
// asks for a page header, then a page, then the next page header, and answering
// each of those with its own HTTP request would be tens of thousands of round
// trips for one file.
//
// It keeps several windows rather than one. A Parquet row group is read one
// column at a time and the columns are interleaved as rows are assembled, so
// there are as many live read positions as there are columns in the schema, each
// moving forwards through its own part of the file. One window would be evicted
// by every column in turn and hit nothing. GlotCC is the case that settles the
// number: its 2.1 GB file is a single row group of half a million rows across
// thirteen columns.
//
// The cost of reading this way is the digest. A streamed file is hashed as it
// goes and checked at the end against what was pinned, and a file read in pieces
// never has all of its bytes in one place, so there is nothing to hash. That is
// recorded rather than papered over: the ledger marks how a file was read, and a
// randomly read file carries no computed digest.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/tamnd/gao/may"
)

// DefaultWindow is how much a [RangeAt] fetches when it has to go to the host.
//
// Four megabytes is a compromise between round trips and waste. A Parquet page is
// tens of kilobytes and a column chunk is tens of megabytes, so a window this
// size covers many pages of one column and is thrown away without being fully
// read only at the end of a chunk.
const DefaultWindow = 4 << 20

// DefaultWindows is how many windows a [RangeAt] keeps.
//
// One per column of the widest schema gao reads, with room over. FinePDFs has 23
// columns and is the widest, but its files are hundreds of small row groups, so
// its live positions are close together and share windows. GlotCC has thirteen
// columns spread across a single 2.1 GB row group and is the one that needs them
// all at once.
const DefaultWindows = 24

// ErrShortRange is returned when a host answers a Range request with fewer bytes
// than were asked for, without the file having ended.
var ErrShortRange = errors.New("gat: the host answered a range request with less than it was asked for")

// ErrNoRange is returned when a host ignores a Range header and sends the whole
// file. Taking that would mean downloading several gigabytes to satisfy a read of
// four megabytes, once per window, so it is a failure rather than a slow path.
var ErrNoRange = errors.New("gat: the host does not answer range requests")

// OpenAt returns a reader over a pinned file that can be read out of order.
//
// The size comes from the manifest rather than from a HEAD request, because the
// manifest is what the file is pinned to and a host that now serves a different
// length is drift rather than a fact to adopt. `gao gat drift` is where that is
// asked about.
func (f *Fetcher) OpenAt(ctx context.Context, p Pinned, file File) (*RangeAt, error) {
	if file.Bytes <= 0 {
		return nil, fmt.Errorf("gat: %s from %s has no pinned size, so it cannot be read out of order", file.Path, p.Source)
	}
	return &RangeAt{
		fetcher: f,
		ctx:     ctx,
		pin:     p,
		file:    file,
		url:     p.URL(file),
		size:    file.Bytes,
		window:  DefaultWindow,
		keep:    DefaultWindows,
	}, nil
}

// RangeAt reads a pinned file out of order over HTTP. It is safe for concurrent
// use, which it has to be, because a Parquet reader reads its columns from
// several goroutines at once.
type RangeAt struct {
	fetcher *Fetcher
	ctx     context.Context
	pin     Pinned
	file    File
	url     string
	size    int64

	window int
	keep   int

	mu      sync.Mutex
	windows []*pane
	reqs    int64
	got     int64
}

// pane is one cached window of the file.
type pane struct {
	off int64
	buf []byte
}

// Size returns the pinned length of the file.
func (r *RangeAt) Size() int64 { return r.size }

// Requests returns how many HTTP requests the reads so far have cost, and Bytes
// how many bytes they moved. Both are reported after a file, because the whole
// question about reading this way is whether it moved less than the file.
func (r *RangeAt) Requests() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reqs
}

// Bytes returns how many bytes the reads so far moved.
func (r *RangeAt) Bytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got
}

// ReadAt implements [io.ReaderAt].
func (r *RangeAt) ReadAt(p []byte, off int64) (int, error) {
	switch {
	case off < 0:
		return 0, fmt.Errorf("gat: reading %s at %d: negative offset", r.file.Path, off)
	case off >= r.size:
		return 0, io.EOF
	case len(p) == 0:
		return 0, nil
	}

	// A read that runs off the end is served short with io.EOF, which is what
	// [io.ReaderAt] asks for and what a Parquet reader probing the tail expects.
	want := p
	var short error
	if room := r.size - off; int64(len(want)) > room {
		want, short = want[:room], io.EOF
	}

	// A read larger than a window would evict everything to cache something
	// nothing will ask for again, so it goes straight to the host.
	if len(want) > r.window {
		n, err := r.fetch(want, off)
		if err != nil {
			return n, err
		}
		return n, short
	}

	var n int
	for n < len(want) {
		at := off + int64(n)
		buf, base, err := r.pane(at)
		if err != nil {
			return n, err
		}
		n += copy(want[n:], buf[at-base:])
	}
	return n, short
}

// pane returns the cached window containing off, fetching it if it is not held.
func (r *RangeAt) pane(off int64) (buf []byte, base int64, err error) {
	// Windows are aligned, so a read position moving forwards through a column
	// asks for the same window until it crosses a boundary. Unaligned windows
	// would mean a fresh request for every read.
	base = off - off%int64(r.window)

	r.mu.Lock()
	for i, w := range r.windows {
		if w.off != base {
			continue
		}
		// Most recently used to the front, since the columns being read are the
		// windows worth keeping and the ones being passed over are not.
		copy(r.windows[1:i+1], r.windows[:i])
		r.windows[0] = w
		r.mu.Unlock()
		return w.buf, base, nil
	}
	r.mu.Unlock()

	size := int64(r.window)
	if room := r.size - base; size > room {
		size = room
	}
	fresh := make([]byte, size)
	if _, err := r.fetch(fresh, base); err != nil {
		return nil, 0, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Another goroutine may have fetched the same window while this one was
	// waiting on the network. Keeping both would be correct and wasteful, so the
	// one already there wins.
	for _, w := range r.windows {
		if w.off == base {
			return w.buf, base, nil
		}
	}
	if len(r.windows) < r.keep {
		r.windows = append(r.windows, nil)
	}
	copy(r.windows[1:], r.windows[:len(r.windows)-1])
	r.windows[0] = &pane{off: base, buf: fresh}
	return fresh, base, nil
}

// fetch fills p from the host, retrying a dropped request the same number of
// times a streamed transfer retries a dropped connection.
func (r *RangeAt) fetch(p []byte, off int64) (int, error) {
	var last error
	for try := 0; ; try++ {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		n, err := r.once(p, off)
		if err == nil {
			r.mu.Lock()
			r.reqs++
			r.got += int64(n)
			r.mu.Unlock()
			return n, nil
		}
		// A host that will not answer a range request at all, or will not let
		// this caller in, answers the same way on every retry.
		if errors.Is(err, ErrNoRange) || errors.Is(err, ErrGated) {
			return 0, err
		}
		last = err
		if try >= r.fetcher.retries() {
			return 0, fmt.Errorf("gat: reading %s from %s at byte %d: gave up after %d attempts: %w",
				r.file.Path, r.pin.Source, off, try+1, last)
		}
		if waitErr := sleep(r.ctx, time.Duration(try+1)*r.fetcher.retryWait()); waitErr != nil {
			return 0, waitErr
		}
	}
}

// once issues one Range request and reads the whole answer.
func (r *RangeAt) once(p []byte, off int64) (int, error) {
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return 0, fmt.Errorf("gat: reading %s from %s: %w", r.file.Path, r.pin.Source, err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+int64(len(p))-1))
	if r.pin.Origin == Hub && r.fetcher.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.fetcher.Token)
	}

	resp, err := r.fetcher.client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("gat: reading %s from %s at byte %d: %w", r.file.Path, r.pin.Source, off, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		drain(resp)
		if r.pin.Gated {
			return 0, fmt.Errorf("%w: %s answered %s for %s, so accept the terms at %s and set %s",
				ErrGated, r.pin.Repo, resp.Status, r.file.Path, r.pin.Page(), may.TokenEnv)
		}
		return 0, fmt.Errorf("gat: reading %s from %s: %s", r.file.Path, r.pin.Source, resp.Status)

	case resp.StatusCode == http.StatusOK:
		drain(resp)
		return 0, fmt.Errorf("%w: %s answered a request for %d bytes of %s with the whole file",
			ErrNoRange, r.pin.Repo, len(p), r.file.Path)

	case resp.StatusCode != http.StatusPartialContent:
		drain(resp)
		return 0, fmt.Errorf("gat: reading %s from %s at byte %d: %s", r.file.Path, r.pin.Source, off, resp.Status)
	}

	n, err := io.ReadFull(resp.Body, p)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF):
		// Inside the pinned length, so this is a truncated answer rather than
		// the end of the file, and it is worth retrying.
		return n, fmt.Errorf("%w: %s at byte %d, %d of %d bytes", ErrShortRange, r.file.Path, off, n, len(p))
	default:
		return n, fmt.Errorf("gat: reading %s from %s at byte %d: %w", r.file.Path, r.pin.Source, off, err)
	}
}
