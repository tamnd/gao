package crawl

// The crawl itself: a pool of workers over a frontier and a sink.
//
// Everything the loop does is somewhere else. The frontier decides what is
// asked for and in what order, the crawler decides whether asking is allowed
// and how long to wait first, the extractor decides what is on the page, the
// document gate decides whether it is a document, and the sink decides where it
// lands. What is left here is the order of those calls and the accounting, which
// is why this file is short and why the parts it calls are separately testable.
//
// The worker count is a fetch count and not a host count. Politeness is owned by
// the schedule inside the crawler, so twenty workers on one host are twenty
// workers taking turns at one request a second rather than twenty requests a
// second. The frontier spreads a batch over hosts for the same reason from the
// other end: a batch that is one host is a batch nineteen workers wait through.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tamnd/gao/frontier"
	"github.com/tamnd/gao/harvest"
	"github.com/tamnd/gao/reject"
	"github.com/tamnd/gao/sift"
)

// DefaultWorkers is how many fetches are in flight at once.
//
// Twenty was the first number and it was wrong by a factor of twenty five. The
// reasoning behind it was about the host, that a crawl spread over thousands of
// hosts almost never has two workers on one, and that part was right. What it
// missed is that the host is not what a worker spends its time on. A worker
// spends it on a socket, and a socket is idle almost the whole time it is open.
//
// The fleet measured it on one box, on one afternoon, on the same seed. At
// twenty workers the crawl did 6.4 pages a second. At five hundred, with
// nothing else changed, it did 60 to 67. The box was not busy either time: the
// limit was never the machine, it was that twenty sockets cannot hold more than
// twenty pages in the air at once, and a page takes about three seconds to
// arrive. Five hundred is chosen against the same measurement and against the
// open file limit, which is what actually breaks first on a small box.
//
// A crawl still owes each host a second between requests and still sends one
// request to a host at a time. Those are enforced in [harvest.Polite] and this
// number does not touch them. Five hundred workers over a frontier of a hundred
// thousand hosts is five hundred different hosts being asked once.
const DefaultWorkers = 500

// DefaultBatch is how many URLs are taken from the frontier at a time. It is
// larger than the worker count so that the pool is never waiting on a read, and
// small enough that a run stopped in the middle loses a batch and not a shift.
//
// It is eight times the workers rather than a fixed number because the frontier
// hands out at most [DefaultPerHost] URLs per host per batch. A batch of 200 is
// therefore at least 100 hosts, which was plenty for twenty workers and is a
// fifth of what five hundred need: the pool would drain the batch, find most of
// its hosts already in flight, and hand everything back.
func DefaultBatch(workers int) int {
	if workers <= 0 {
		workers = DefaultWorkers
	}
	return 8 * workers
}

// idle is how long the feeder waits before asking an empty frontier again while
// fetches are still in flight. A crawl's queue empties whenever the pool has
// taken everything and the links that will refill it are still on the wire, and
// a feeder that read that as the end would stop a crawl at its seeds.
const idle = 50 * time.Millisecond

// RunOptions is one crawl.
type RunOptions struct {
	// Frontier is what to fetch, and Sink is where it goes. Both are required.
	Frontier *Frontier
	Sink     *Sink

	// Crawler is the fetcher. It is required, because the politeness schedule
	// and the robots files it holds are facts about the crawl and building one
	// here would make them facts about this function.
	Crawler *harvest.Crawler

	// Workers is how many fetches run at once. Zero is [DefaultWorkers].
	Workers int

	// Batch is how many URLs are taken from the frontier at a time. Zero is
	// [DefaultBatch].
	Batch int

	// Pages stops the run after this many fetches. Zero runs until the frontier
	// has nothing left, which on the open web is never, so a real run sets one
	// or stops the run with its context.
	Pages int64

	// Limits are the sift thresholds a page has to clear. The zero value takes
	// [sift.Default].
	Limits sift.Limits

	// Checkpoint is how often the frontier is flushed to the disk. Zero is a
	// minute. A crash loses the URLs offered since the last one, which come
	// back as URLs offered twice.
	Checkpoint time.Duration

	// Report, when set, is called at each checkpoint with what the run has done
	// so far.
	Report func(Progress)

	// Out, when set, gets a line per checkpoint.
	Out io.Writer
}

// Progress is a crawl in flight.
type Progress struct {
	Elapsed time.Duration `json:"elapsed"`

	Fetched   int64 `json:"fetched"`
	Kept      int64 `json:"kept"`
	Dropped   int64 `json:"dropped"`
	Failed    int64 `json:"failed"`
	Redirects int64 `json:"redirects"`
	Offered   int64 `json:"offered"`

	Frontier Stats     `json:"frontier"`
	Sink     SinkStats `json:"sink"`
}

// Rate is pages fetched per second, which is the number a fleet is sized by.
func (p Progress) Rate() float64 {
	if p.Elapsed <= 0 {
		return 0
	}
	return float64(p.Fetched) / p.Elapsed.Seconds()
}

// Run crawls until the frontier is empty, the page limit is reached, or the
// context is canceled.
//
// A canceled context is not an error. Stopping a crawl is how a crawl ends, and
// what matters is that the frontier and the sink are left in a state the next
// run picks up from, which is what the deferred flush is for.
func Run(ctx context.Context, o RunOptions) (Progress, error) {
	if o.Frontier == nil || o.Sink == nil || o.Crawler == nil {
		return Progress{}, errors.New("crawl: a run needs a frontier, a sink, and a crawler")
	}
	if o.Workers <= 0 {
		o.Workers = DefaultWorkers
	}
	if o.Batch <= 0 {
		o.Batch = DefaultBatch(o.Workers)
	}
	if o.Checkpoint <= 0 {
		o.Checkpoint = time.Minute
	}
	if o.Limits.MinSyllables == 0 {
		o.Limits = sift.Default()
	}

	r := &loop{o: o, start: time.Now()}
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	urls := make(chan string, o.Batch)
	var wg sync.WaitGroup
	for range o.Workers {
		wg.Go(func() {
			for u := range urls {
				if ctx.Err() != nil {
					r.busy.Add(-1)
					return
				}
				err := r.one(ctx, u)
				r.busy.Add(-1)
				if err != nil {
					r.fail(err)
					stop()
					return
				}
			}
		})
	}

	done := make(chan struct{})
	go r.watch(ctx, done)

	err := r.feed(ctx, urls)
	close(urls)
	wg.Wait()
	close(done)

	// The frontier is flushed whatever happened. A run that stopped because the
	// disk filled still knows which URLs it had taken, and the next one starts
	// from there rather than from the last checkpoint.
	if ferr := o.Frontier.Flush(); ferr != nil && err == nil {
		err = ferr
	}
	if serr := r.err(); serr != nil && err == nil {
		err = serr
	}
	return r.progress(), err
}

// loop is the state one crawl keeps, which is counters and one error.
type loop struct {
	o     RunOptions
	start time.Time

	// busy is how many URLs are between the feeder and a worker's return. It is
	// what tells an empty frontier apart from a finished crawl.
	busy atomic.Int64

	fetched   atomic.Int64
	kept      atomic.Int64
	dropped   atomic.Int64
	failed    atomic.Int64
	redirects atomic.Int64
	offered   atomic.Int64

	mu    sync.Mutex
	first error
}

// feed takes batches from the frontier and hands them to the workers, stopping
// when the frontier is empty or the run has fetched what it was asked for.
func (r *loop) feed(ctx context.Context, urls chan<- string) error {
	for {
		// A stopped crawl is a finished crawl rather than a failed one, so the
		// cancellation ends the feed and is not passed on as the run's error.
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if r.o.Pages > 0 && r.fetched.Load() >= r.o.Pages {
			return nil
		}
		batch, err := r.o.Frontier.Next(r.o.Batch)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			// An empty frontier ends the run only once nothing is in flight.
			// Until then the queue is empty because the pool has taken
			// everything, and the links that refill it are on pages still being
			// fetched. Stopping here would end a crawl at its seeds.
			if r.busy.Load() == 0 {
				return nil
			}
			select {
			case <-time.After(idle):
			case <-ctx.Done():
				return nil
			}
			continue
		}
		for _, u := range batch {
			r.busy.Add(1)
			select {
			case urls <- u:
			case <-ctx.Done():
				r.busy.Add(-1)
				return nil
			}
		}
	}
}

// one is the whole of what happens to a URL.
func (r *loop) one(ctx context.Context, rawurl string) error {
	// The limit is checked here as well as in the feeder, because the feeder
	// runs a batch ahead and a run asked for two pages should fetch two pages
	// rather than the batch that was already in the channel. Workers can still
	// pass this together, so the overshoot is bounded by the worker count, and
	// the URL that is not fetched goes back in the queue for the next run.
	if r.o.Pages > 0 && r.fetched.Load() >= r.o.Pages {
		return r.o.Frontier.Requeue(rawurl)
	}

	at := time.Now()
	v, err := r.o.Crawler.Get(ctx, rawurl)
	if err != nil {
		// Nothing went out, so the only time there is is the time this worker
		// picked the URL up.
		return r.missed(ctx, rawurl, at, err)
	}
	r.fetched.Add(1)
	// The fetch waited for the host's turn before it asked, and the time worth
	// recording is the time the site saw rather than the time this worker
	// started queueing for it. Without this, two workers reaching for one host
	// at the same instant produce two rows a millisecond apart describing two
	// requests a second apart, and the column that is supposed to show the
	// crawl's manners shows the opposite.
	if !v.At.IsZero() {
		at = v.At
	}

	locator, err := r.o.Sink.Archive(v, at)
	if err != nil {
		return err
	}

	// A redirect is not a page and its Location is a URL like any other. It goes
	// back to the frontier, where the decision about whether to ask for it
	// belongs, and the redirect itself is written down as a row so that the
	// chain is in the record rather than only in its result.
	if v.Redirect != "" {
		r.redirects.Add(1)
		r.offer(v.Redirect)
	}

	var page *Page
	if v.Status == 200 && readable(v) {
		base, err := url.Parse(v.URL)
		if err != nil {
			return fmt.Errorf("crawl: %s: %w", v.URL, err)
		}
		page, err = Read(base, bytes.NewReader(v.Body))
		if err != nil {
			// A page that will not parse is a rejection and not a failure. The
			// bytes are in the archive and a later extractor can have them.
			return r.write(rawurl, Refused(rawurl, at, reject.ReasonExtract, err.Error()))
		}
	}

	verdict := Build(v, page, BuildOptions{
		Locator:   locator,
		FetchedAt: at,
		Limits:    r.o.Limits,
	})

	// The links are followed after the page has been judged rather than before,
	// and a page that failed the language test is not followed.
	//
	// This is the frontier's only defense against fanning out through a language
	// it did not come for. A page in Chinese links to more pages in Chinese, and
	// offering its links queues hosts whose entire subgraph is somebody else's
	// web. The reach rule bounds what each of those hosts costs and does nothing
	// about how many of them arrive: on the run that added it, 5,818 hosts
	// outside .vn showed up in half an hour, took 3.6 requests each, and 19,880
	// of those 20,691 requests went to a host that never kept a page.
	//
	// Only the language rejection stops a page from being followed. A listing
	// page rejected as boilerplate, an article rejected as too short, a page
	// rejected for repeating itself are all still Vietnamese pages, and their
	// links are how the crawl reaches the articles. A category index is the
	// clearest case: it is refused on every content test there is and it is the
	// most valuable page on the site to follow.
	if page != nil && !page.NoFollow && verdict.Reason != reject.ReasonLanguage {
		r.offerAll(page.Links)
	}

	return r.write(rawurl, verdict)
}

// missed handles a URL that produced no response at all.
func (r *loop) missed(ctx context.Context, rawurl string, at time.Time, err error) error {
	if ctx.Err() != nil {
		// The run is stopping, so the URL is one nobody asked about rather than
		// one that failed. It goes back in the queue for the next run.
		return r.o.Frontier.Requeue(rawurl)
	}
	if errors.Is(err, harvest.ErrBusy) {
		// A host that asked for time has not refused. The URL goes back and the
		// wait is the schedule's business rather than this loop's.
		return r.o.Frontier.Requeue(rawurl)
	}
	r.failed.Add(1)
	reason, detail, _ := harvest.Reject(err)
	return r.write(rawurl, Refused(rawurl, at, reason, detail))
}

// write puts a verdict in the sink and tells the frontier what the fetch was
// worth, which is what keeps a host that produces nothing from being crawled
// forever.
func (r *loop) write(rawurl string, v Verdict) error {
	if err := r.o.Sink.Write(v); err != nil {
		return err
	}
	if v.Kept {
		r.kept.Add(1)
		r.o.Frontier.Fetched(rawurl, frontier.New)
		return nil
	}
	r.dropped.Add(1)
	r.o.Frontier.Fetched(rawurl, frontier.Empty)
	return nil
}

// offer puts a link in the frontier. What comes back is a count and not an
// error: a link that is malformed, already seen, or over its host's budget is
// the ordinary case and the frontier counts each of them.
func (r *loop) offer(rawurl string) {
	ok, _, err := r.o.Frontier.Offer(rawurl)
	if err != nil {
		r.fail(err)
		return
	}
	if ok {
		r.offered.Add(1)
	}
}

// offerAll puts a whole page's links in the frontier in one go, which is one
// turn at the frontier's lock rather than sixty of them. See
// [Frontier.OfferAll]: the links of one page are where a crawl's frontier
// traffic comes from and offering them one at a time was what every worker on
// the box was queueing for.
func (r *loop) offerAll(links []string) {
	n, err := r.o.Frontier.OfferAll(links)
	r.offered.Add(int64(n))
	if err != nil {
		r.fail(err)
	}
}

// watch flushes the frontier on a timer and reports what the run has done.
func (r *loop) watch(ctx context.Context, done <-chan struct{}) {
	t := time.NewTicker(r.o.Checkpoint)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			if err := r.o.Frontier.Flush(); err != nil {
				r.fail(err)
				return
			}
			p := r.progress()
			if r.o.Report != nil {
				r.o.Report(p)
			}
			if r.o.Out != nil {
				fmt.Fprintf(r.o.Out, "%s  %d fetched, %d kept, %d dropped, %d failed, %d queued, %.1f pages a second\n",
					p.Elapsed.Round(time.Second), p.Fetched, p.Kept, p.Dropped, p.Failed,
					p.Frontier.Queued(), p.Rate())
			}
		}
	}
}

func (r *loop) progress() Progress {
	return Progress{
		Elapsed:   time.Since(r.start),
		Fetched:   r.fetched.Load(),
		Kept:      r.kept.Load(),
		Dropped:   r.dropped.Load(),
		Failed:    r.failed.Load(),
		Redirects: r.redirects.Load(),
		Offered:   r.offered.Load(),
		Frontier:  r.o.Frontier.Stats(),
		Sink:      r.o.Sink.Stats(),
	}
}

// fail keeps the first error and lets the rest go. A run that fills a disk
// fails in every worker at once, and the twentieth report of it says nothing
// the first did not.
func (r *loop) fail(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.first == nil {
		r.first = err
	}
}

func (r *loop) err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.first
}

// html reports whether the response is something the extractor reads. A crawl
// of the open web is handed PDFs, images and zip files by pages that link to
// them, and parsing one as HTML produces a document made of nothing.
func readable(v *harvest.Visit) bool {
	t := mediaType(v.Header.Get("Content-Type"))
	return t == "text/html" || t == "application/xhtml+xml"
}
