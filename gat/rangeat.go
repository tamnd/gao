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
// Four megabytes is a compromise between round trips and waste, sized for the
// text column, where a chunk is tens of megabytes and a window is thrown away
// without being fully read only at the end of one.
//
// It is the wrong size for a fixed width column, and being wrong there is
// expensive rather than untidy. See [ColumnWindow].
const DefaultWindow = 4 << 20

// ColumnWindow is the window for a pass that reads one fixed width column.
//
// A shape column holds four bytes per document, so a chunk of it is a couple of
// hundred kilobytes where the text chunk beside it is tens of megabytes, and a
// four megabyte window fetches the whole neighborhood to read a page of it.
// Measured on one real part of glotcc-9ad140b6be3a, 511.6 MB holding 126,853
// documents, summing the three shape columns moved:
//
//	window     bytes moved   requests
//	4 MB        58.1 MB        14
//	1 MB        15.5 MB        15
//	256 KB       4.6 MB        18
//	64 KB        1.8 MB        24
//	16 KB        1.0 MB        23
//
// The floor is 1.5 MB, which is twelve bytes a document, and the columns encode
// to less than that. So the default was moving thirty eight times what the pass
// was reading, and ten more round trips buys all of it back.
//
// Sixty four kilobytes rather than sixteen because sixteen is smaller than a
// Parquet page, which costs two requests for one page on any schema whose pages
// are larger, and the measured difference between the two here is 0.7 MB on a
// part of half a gigabyte.
const ColumnWindow = 64 << 10

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

// Remote is a file somewhere else, described well enough to read it out of
// order and to say what went wrong when that fails.
//
// The length is the caller's rather than a HEAD request's, because a length that
// has to be asked for is a length that can change between the ask and the read,
// and upstream the pinned size is the one the file is pinned to. A host now
// serving a different one is drift rather than a fact to adopt, and
// `gao harvest drift` is where that is asked about.
type Remote struct {
	// Name is the path of the file and From is who is serving it. Neither is
	// used to find anything. They are what turns a failed read into a sentence
	// naming the file and the source rather than a byte offset.
	Name string
	From string

	URL   string
	Bytes int64

	// Auth sends the fetcher's token with every request.
	Auth bool

	// Gated marks a source whose terms have to be accepted, and Page is where to
	// accept them. A 403 on a gated source is somebody missing a grant rather
	// than a broken URL, and it is worth saying so.
	Gated bool
	Page  string

	// Window is how much is fetched at a time and Windows is how many of those
	// are kept. Zero for either is [DefaultWindow] and [DefaultWindows], which
	// are sized for a reader assembling whole rows out of an interleaved
	// schema. A caller reading one column has one live read position rather
	// than thirteen and has no use for the other twenty three windows.
	Window  int
	Windows int
}

// OpenAt returns a reader over a pinned file that can be read out of order.
func (f *Fetcher) OpenAt(ctx context.Context, p Pinned, file File) (*RangeAt, error) {
	if file.Bytes <= 0 {
		return nil, fmt.Errorf("gat: %s from %s has no pinned size, so it cannot be read out of order", file.Path, p.Source)
	}
	return f.OpenRemote(ctx, Remote{
		Name:  file.Path,
		From:  string(p.Source),
		URL:   p.URL(file),
		Bytes: file.Bytes,
		Auth:  p.Origin == Hub && f.Token != "",
		Gated: p.Gated,
		Page:  p.Page(),
	})
}

// Open returns a reader over any file of a known length that can be read out of
// order.
//
// It is exported because the same problem comes back on the way out. A part in
// the store is Parquet as well, and a pass that wants one column of a 2 GB part
// has the same reason not to move the other twenty two columns that an ingest
// has not to download a source to read it.
func (f *Fetcher) OpenRemote(ctx context.Context, r Remote) (*RangeAt, error) {
	if r.Bytes <= 0 {
		return nil, fmt.Errorf("gat: %s from %s has no known size, so it cannot be read out of order", r.Name, r.From)
	}
	window, keep := r.Window, r.Windows
	if window <= 0 {
		window = DefaultWindow
	}
	if keep <= 0 {
		keep = DefaultWindows
	}
	return &RangeAt{
		fetcher: f,
		ctx:     ctx,
		remote:  r,
		size:    r.Bytes,
		window:  window,
		keep:    keep,
	}, nil
}

// RangeAt reads a remote file out of order over HTTP. It is safe for concurrent
// use, which it has to be, because a Parquet reader reads its columns from
// several goroutines at once.
type RangeAt struct {
	fetcher *Fetcher
	ctx     context.Context
	remote  Remote
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

// Window returns how much this reader fetches when it has to go to the host.
//
// It is the difference between a pass that moves what it reads and one that
// moves thirty eight times it, and nothing at the call site shows which of the
// two a reader is, so a caller that picked a window deliberately can assert it.
func (r *RangeAt) Window() int { return r.window }

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
		return 0, fmt.Errorf("gat: reading %s at %d: negative offset", r.remote.Name, off)
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
				r.remote.Name, r.remote.From, off, try+1, last)
		}
		if waitErr := sleep(r.ctx, time.Duration(try+1)*r.fetcher.retryWait()); waitErr != nil {
			return 0, waitErr
		}
	}
}

// once issues one Range request and reads the whole answer.
func (r *RangeAt) once(p []byte, off int64) (int, error) {
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.remote.URL, nil)
	if err != nil {
		return 0, fmt.Errorf("gat: reading %s from %s: %w", r.remote.Name, r.remote.From, err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+int64(len(p))-1))
	if r.remote.Auth {
		req.Header.Set("Authorization", "Bearer "+r.fetcher.Token)
	}

	resp, err := r.fetcher.client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("gat: reading %s from %s at byte %d: %w", r.remote.Name, r.remote.From, off, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		drain(resp)
		if r.remote.Gated {
			return 0, fmt.Errorf("%w: %s answered %s for %s, so accept the terms at %s and set %s",
				ErrGated, r.remote.From, resp.Status, r.remote.Name, r.remote.Page, may.TokenEnv)
		}
		return 0, fmt.Errorf("gat: reading %s from %s: %s", r.remote.Name, r.remote.From, resp.Status)

	case resp.StatusCode == http.StatusOK:
		drain(resp)
		return 0, fmt.Errorf("%w: %s answered a request for %d bytes of %s with the whole file",
			ErrNoRange, r.remote.From, len(p), r.remote.Name)

	case resp.StatusCode != http.StatusPartialContent:
		drain(resp)
		return 0, fmt.Errorf("gat: reading %s from %s at byte %d: %s", r.remote.Name, r.remote.From, off, resp.Status)
	}

	n, err := io.ReadFull(resp.Body, p)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF):
		// Inside the pinned length, so this is a truncated answer rather than
		// the end of the file, and it is worth retrying.
		return n, fmt.Errorf("%w: %s at byte %d, %d of %d bytes", ErrShortRange, r.remote.Name, off, n, len(p))
	default:
		return n, fmt.Errorf("gat: reading %s from %s at byte %d: %w", r.remote.Name, r.remote.From, off, err)
	}
}
