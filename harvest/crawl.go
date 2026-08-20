package harvest

// Fetching one page, with everything the other files in this package decided
// actually applied to it.
//
// The pieces existed and none of them were wired to anything that opens a
// socket: a robots parser nothing called, an identity nothing sent, a schedule
// nothing waited on, and a reservation reader with no headers to read. This is
// where they meet, and it is deliberately the only place in the project that
// makes an outbound request to a site we do not own.
//
// One request per call, no retry loop, no redirect following, no queue. A
// crawler's frontier decides what to ask for next and this decides whether the
// asking is allowed, which are different jobs and get confused when they are the
// same object.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamnd/gao/reject"
)

// MaxBody is the largest page this will read.
//
// Eight megabytes is far past any Vietnamese article or forum thread and short
// of the things that are not pages: a database dump served with an HTML content
// type, a video behind a misconfigured route, a log file. A body over the cap is
// refused rather than truncated, because half a document is a document nobody
// can tell is half.
const MaxBody = 8 << 20

// DefaultBackoff is how long a host gets left alone when it said it was busy and
// did not say for how long.
const DefaultBackoff = 5 * time.Minute

// The reasons a fetch did not produce a page. They are errors rather than a
// status field because every one of them means the caller must not read a body,
// and a field can be ignored.
var (
	// ErrDeclined is robots.txt saying no to this path. The host is fine and
	// other paths on it may still be fetched.
	ErrDeclined = errors.New("harvest: robots.txt declined this path")

	// ErrBlocked is the host saying no to us. It is remembered, and every later
	// request to that host fails with it without a packet being sent.
	ErrBlocked = errors.New("harvest: the host has blocked this crawler")

	// ErrBusy is the host saying it cannot take this now. It is not a failure
	// and it is not a reason to try again sooner.
	ErrBusy = errors.New("harvest: the host asked for time")

	// ErrTooLarge is a body over [MaxBody].
	ErrTooLarge = errors.New("harvest: the body is larger than a page")
)

// A Visit is one fetch and everything that came back with it.
type Visit struct {
	// URL is what was asked for, and Host is the authority the politeness and
	// the robots decision were keyed on.
	URL  string
	Host string

	Status int
	Header http.Header
	Body   []byte

	// Redirect is the Location of a 3xx, absolute, and empty otherwise.
	// Redirects are not followed: see [Crawler.Get].
	Redirect string

	// Reserve is what the response said about text and data mining, read from
	// the headers. The caller merges it with whatever the document itself says
	// once the HTML has been parsed.
	Reserve Reservation

	// Robots is the decision that let this fetch happen, kept so that a record
	// of the fetch can say which rule allowed it rather than only that nothing
	// stopped it.
	Robots Decision

	// At is when the request went out, taken after the politeness schedule let
	// it go rather than when the crawler picked the URL up.
	//
	// The difference is the whole of the wait, which on a host that asked for
	// thirty seconds is thirty seconds, and it is what a reader measures the
	// crawl's manners with. A time taken before the wait says two requests to
	// one host went out together when what happened is that two workers reached
	// for the same host together and one of them then stood in line.
	At time.Time
}

// Reject turns a visit that did not produce a page into a rejection, so that a
// skipped URL is written down in the same place as every other document the
// pipeline dropped.
func Reject(err error) (reject.Reason, string, bool) {
	switch {
	case errors.Is(err, ErrDeclined):
		return reject.ReasonRobots, err.Error(), true
	case errors.Is(err, ErrBlocked):
		// A 403 is a non-200 status and that is what it is filed as. It reads
		// like a robots decision and it is not one: robots means a publisher
		// stated a rule in a file or a meta tag, and counting a bot wall under
		// it inflates the number of Vietnamese sites that asked to be left
		// alone. On the first fleet run that was 832 refusals against 999 real
		// disallows, so the reason column would have overstated it by four
		// fifths. The error keeps the host and the status, so a reader who
		// wants the walls back can have them from the detail.
		return reject.ReasonFetch, err.Error(), true
	case errors.Is(err, ErrBusy):
		return reject.ReasonFetch, err.Error(), true
	case errors.Is(err, ErrTooLarge):
		return reject.ReasonFetch, err.Error(), true
	case err != nil:
		return reject.ReasonFetch, err.Error(), true
	default:
		return "", "", false
	}
}

// CrawlOptions configures a [Crawler]. The zero value works and uses the
// defaults named on each field.
type CrawlOptions struct {
	// Client is the HTTP client. Zero means [NewClient] with the defaults,
	// which is a sharded keep-alive pool, a five second deadline on the
	// response header, twenty seconds on the whole exchange, and redirects
	// returned rather than followed. See [Crawler.Get] on the redirects and
	// transport.go on the rest.
	Client *http.Client

	// Transport configures the client built when Client is nil. It is ignored
	// when a caller brings its own.
	Transport TransportOptions

	// Timeout bounds the whole exchange on the client built when Client is
	// nil. Zero means [DefaultFetchTimeout].
	Timeout time.Duration

	// Strikes is how many failures a host that has never answered gets before
	// the crawl stops sending to it. Zero means [DefaultStrikes], and a
	// negative number turns the breaker off, which is what a test that wants
	// every request to go out asks for.
	Strikes int

	// Polite is the schedule. Zero means a new one with the defaults, which is
	// wrong for a real crawl, because the schedule has to be shared by every
	// worker to mean anything.
	Polite *Polite

	// Version is stamped into the User-Agent. Zero means "dev".
	Version string

	// MaxBody overrides [MaxBody].
	MaxBody int64
}

// A Crawler fetches pages from sites that did not ask to be fetched.
//
// It is safe for concurrent use. One instance per crawl rather than one per
// worker, because the robots files it has read and the hosts that have blocked
// it are facts about the crawl and not about a goroutine.
type Crawler struct {
	client  *http.Client
	polite  *Polite
	dead    *breaker
	agent   string
	maxBody int64

	mu      sync.Mutex
	sites   map[string]*published
	blocked map[string]string

	// reading is one gate per host, held while its published files are being
	// fetched. Twenty workers starting on one host is the ordinary shape of a
	// crawl, and without this they fetch the same two files twenty times:
	// politely, one at a time, and twenty times.
	reading map[string]chan struct{}
}

// A published is the two files a site puts up for crawlers to read, fetched once
// per host and kept for the rest of the run.
//
// They answer different questions and are treated differently when they cannot
// be read. robots.txt decides whether a page may be fetched, so a file we could
// not read stops the fetch. tdmrep.json decides what may be done with a page
// that was fetched, and there is a second gate on that at the write into the
// store, so a file we could not read is written into the record and the crawl
// carries on. Stopping instead would hand any site a way to end its own crawl by
// misconfiguring a file most sites do not have.
type published struct {
	robots *Robots

	// tdm is the well known file, nil when the site does not publish one, which
	// is almost every site.
	tdm *TDMRep

	// tdmNote is set when the file was there and could not be read. It goes
	// into the record of every page fetched from the host, because a
	// reservation we could not read is a fact about the fetch.
	tdmNote string
}

// NewCrawler returns a crawler with these options.
func NewCrawler(o CrawlOptions) *Crawler {
	c := &Crawler{
		client:  o.Client,
		polite:  o.Polite,
		maxBody: o.MaxBody,
		sites:   map[string]*published{},
		blocked: map[string]string{},
		reading: map[string]chan struct{}{},
	}
	if c.client == nil {
		c.client = NewClient(o.Transport, o.Timeout)
	}
	if c.polite == nil {
		c.polite = NewPolite(PoliteOptions{})
	}
	if o.Strikes >= 0 {
		c.dead = newBreaker(o.Strikes)
	}
	if c.maxBody <= 0 {
		c.maxBody = MaxBody
	}
	version := o.Version
	if version == "" {
		version = "dev"
	}
	c.agent = Agent(version)
	return c
}

// Get fetches one URL.
//
// It reads the host's robots.txt first, once per host, and refuses a path the
// file declines. It waits for the host's turn. It sends one request with one
// User-Agent and it does not retry: a crawler that retries is a crawler that
// asks twice, and the schedule it was waiting on was for asking once.
//
// Redirects are returned rather than followed. A redirect can cross to another
// host, where a different robots.txt applies and a different schedule is owed,
// and a client that follows one has made a request nothing checked. The Location
// goes back to the frontier as a URL like any other, which is where a decision
// about whether to ask for it belongs.
//
// A 401 or a 403 is a stop. The host is remembered and every later request to it
// fails without a packet being sent, because a site that has said no does not
// have to keep saying it.
func (c *Crawler) Get(ctx context.Context, rawurl string) (*Visit, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil, fmt.Errorf("harvest: %s: %w", rawurl, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("harvest: %s: %q is not a scheme this fetches", rawurl, u.Scheme)
	}
	host := u.Host
	if host == "" {
		return nil, fmt.Errorf("harvest: %s: no host", rawurl)
	}

	if why, ok := c.isBlocked(host); ok {
		return nil, fmt.Errorf("%w: %s said %s", ErrBlocked, host, why)
	}
	// Before the robots fetch rather than after it. A host that does not
	// resolve does not resolve for its robots.txt either, and checking after
	// would pay the dead host's deadline to find out that we already knew.
	if c.dead != nil && c.dead.dead(host) {
		return nil, fmt.Errorf("%w: %s failed %d times without answering", ErrDead, host, c.dead.strikes)
	}

	site, err := c.published(ctx, u)
	if err != nil {
		return nil, err
	}
	decision := site.robots.Check(Bot, u.RequestURI())
	if !decision.Allowed {
		return nil, fmt.Errorf("%w: %s, by %q", ErrDeclined, rawurl, decision.Rule)
	}

	got, err := c.fetch(ctx, host, u.String())
	if err != nil {
		return nil, err
	}

	v := &Visit{
		URL:     rawurl,
		Host:    host,
		Status:  got.status,
		Header:  got.header,
		Body:    got.body,
		Reserve: site.reserve(u.EscapedPath()).Merge(ReadHeaders(got.header, Bot)),
		Robots:  decision,
		At:      got.at,
	}
	if loc := got.header.Get("Location"); loc != "" && got.status >= 300 && got.status < 400 {
		if abs, err := u.Parse(loc); err == nil {
			v.Redirect = abs.String()
		}
	}
	return v, nil
}

// Robots returns the host's robots.txt, fetching it the first time and keeping
// it for the rest of the crawl.
//
// The fetch of the file is itself polite, because a file about how often we may
// ask is not a file we may ask for at any rate we like. It is not itself checked
// against robots.txt, for the obvious reason.
func (c *Crawler) Robots(ctx context.Context, u *url.URL) (*Robots, error) {
	site, err := c.published(ctx, u)
	if err != nil {
		return nil, err
	}
	return site.robots, nil
}

// published returns what the host has put up for crawlers, fetching both files
// the first time and keeping them for the rest of the run.
func (c *Crawler) published(ctx context.Context, u *url.URL) (*published, error) {
	host := u.Host
	site, err := c.readPublished(ctx, u)
	if err != nil {
		return nil, err
	}

	// The delay is checked here rather than only where the file is read, so a
	// host asking for longer than a crawl waits answers the same way for every
	// URL on it, without its robots.txt being fetched again for each one.
	if d, ok := c.polite.Learn(host, site.robots); !ok {
		return nil, fmt.Errorf("%w: %s asked for %v between requests, which is longer than a crawl waits", ErrBusy, host, d)
	}
	return site, nil
}

// readPublished returns the cached files or fetches them, with one fetch per
// host even when twenty workers arrive at once.
func (c *Crawler) readPublished(ctx context.Context, u *url.URL) (*published, error) {
	host := u.Host
	if site, ok := c.cached(host); ok {
		return site, nil
	}

	gate := c.gate(host)
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-gate }()

	// Whoever held the gate has finished by now, and the usual outcome for
	// everybody but the first worker is that the answer is already here.
	if site, ok := c.cached(host); ok {
		return site, nil
	}

	site := &published{}
	file := &url.URL{Scheme: u.Scheme, Host: host, Path: "/robots.txt"}
	got, err := c.fetch(ctx, host, file.String())
	switch {
	case err != nil:
		// A timeout, a refused connection, a 403 on the file itself, a 429. All
		// of those are things that happened to this request rather than facts
		// about the site, so they are reported and not remembered. A host is not
		// closed for the rest of a run by one bad minute, and the only thing
		// that does close it is the host saying so, which [Crawler.Get] has
		// already recorded by the time this returns.
		return nil, err
	case got.status == http.StatusOK:
		site.robots = ReadRobots(got.body)
	default:
		// A 404 is a site with no file and therefore no objection. Anything
		// else the server answered with is an answer, and RobotsUnavailable
		// decides which way each one falls.
		site.robots = RobotsUnavailable(got.status)
	}
	// The delay the file asked for applies to the very next request, and the
	// very next request is ours. A host asking for longer than a crawl waits is
	// not asked for its second file at all, since nothing on it is going to be
	// fetched: published reports that to the caller.
	if _, ok := c.polite.Learn(host, site.robots); ok {
		c.readTDMRep(ctx, u, site)
	}

	c.mu.Lock()
	c.sites[host] = site
	c.mu.Unlock()
	return site, nil
}

// TDMRepPath is the well known location, fixed by the specification.
const TDMRepPath = "/.well-known/tdmrep.json"

// readTDMRep asks the host for its well known file, once, on the way in.
//
// This costs one request per host and it is the request worth making. TDMRep is
// the only mechanism that states a reservation for a whole site rather than on
// every response, so a site that has bothered to publish one has said something
// deliberate, and a crawler that only read response headers would miss it on
// every page and record a consent state of open for all of them.
//
// It is checked against robots.txt like anything else. A site that disallowed
// the path has not made an exception for us, and the note that goes in the
// record says the file was not read rather than that it was not there.
func (c *Crawler) readTDMRep(ctx context.Context, u *url.URL, site *published) {
	if d := site.robots.Check(Bot, TDMRepPath); !d.Allowed {
		site.tdmNote = "tdmrep " + TDMRepPath + ": not read, robots.txt disallows it"
		return
	}

	file := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: TDMRepPath}
	got, err := c.fetch(ctx, u.Host, file.String())
	switch {
	case err != nil:
		site.tdmNote = "tdmrep " + TDMRepPath + ": not read, " + err.Error()
	case got.status == http.StatusNotFound || got.status == http.StatusGone:
		// The ordinary case, and not worth a note. A site with no file has not
		// reserved anything, which is exactly what an empty reservation says.
	case got.status != http.StatusOK:
		site.tdmNote = fmt.Sprintf("tdmrep %s: not read, the server answered %d", TDMRepPath, got.status)
	default:
		rep, err := ReadTDMRep(got.body)
		if err != nil {
			site.tdmNote = "tdmrep " + TDMRepPath + ": published and unreadable, " + err.Error()
			return
		}
		site.tdm = rep
	}
}

// reserve is what this site published about one path, before anything the
// response itself said.
func (p *published) reserve(path string) Reservation {
	r := p.tdm.For(path)
	if p.tdmNote != "" {
		r.Said = append(r.Said, p.tdmNote)
	}
	return r
}

func (c *Crawler) cached(host string) (*published, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	site, ok := c.sites[host]
	return site, ok
}

func (c *Crawler) gate(host string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	g, ok := c.reading[host]
	if !ok {
		g = make(chan struct{}, 1)
		c.reading[host] = g
	}
	return g
}

// A fetched is the part of a response that outlives the connection. Returning
// this rather than the response is what makes the body's lifetime local to the
// one function that reads it, and it is why nothing else in this file has to
// remember to close anything.
type fetched struct {
	status int
	header http.Header
	body   []byte
	at     time.Time
}

// fetch does the waiting, the request and the reading, and turns the statuses
// that mean stop into the errors that mean stop.
func (c *Crawler) fetch(ctx context.Context, host, target string) (fetched, error) {
	done, err := c.polite.Wait(ctx, host)
	if err != nil {
		return fetched{}, err
	}
	defer done()

	// After the wait, because this is the time the site sees.
	at := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fetched{}, fmt.Errorf("harvest: %s: %w", target, err)
	}
	req.Header.Set("User-Agent", c.agent)
	// Accept-Encoding is deliberately not set. Setting it by hand turns off the
	// transport's own compression handling and hands back a body still in gzip,
	// which then gets counted against the size cap and stored as bytes nothing
	// can decode.

	resp, err := c.client.Do(req)
	if err != nil {
		// Only a request that produced no response counts against the host. A
		// canceled crawl is not the host's doing, and counting it would end a
		// run by convincing itself that the web had gone away.
		if c.dead != nil && ctx.Err() == nil {
			c.dead.failed(host)
		}
		return fetched{}, fmt.Errorf("harvest: %s: %w", target, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// A status line is an answer, whatever the status is. A host that 404s is a
	// host, and the next URL on it may well be a page.
	if c.dead != nil {
		c.dead.answered(host)
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		c.block(host, resp.Status)
		return fetched{}, fmt.Errorf("%w: %s said %s", ErrBlocked, host, resp.Status)
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		wait := retryAfter(resp.Header.Get("Retry-After"))
		c.polite.Backoff(host, wait)
		return fetched{}, fmt.Errorf("%w: %s said %s, leaving it for %v", ErrBusy, host, resp.Status, wait)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return fetched{}, fmt.Errorf("harvest: %s: %w", target, err)
	}
	if int64(len(body)) > c.maxBody {
		return fetched{}, fmt.Errorf("%w: %s sent over %d bytes", ErrTooLarge, target, c.maxBody)
	}
	return fetched{status: resp.StatusCode, header: resp.Header, body: body, at: at}, nil
}

// Delay is the gap this crawler is currently leaving between requests to a host,
// which is the longer of ours and whatever that host's robots.txt asked for.
func (c *Crawler) Delay(host string) time.Duration { return c.polite.Delay(host) }

// Dropped is how many hosts the crawl has stopped sending to because they
// never answered.
//
// It is the third number in the pair [Crawler.Hosts] reports and it is watched
// for the same reason: a crawl dropping a rising share of the hosts it meets
// has either followed links into a bad neighborhood of the web or has broken
// its own networking, and the two look identical from the page count alone.
func (c *Crawler) Dropped() int {
	if c.dead == nil {
		return 0
	}
	return c.dead.dropped()
}

// Blocked reports whether a host has told us to stop, and what it said.
func (c *Crawler) Blocked(host string) (string, bool) { return c.isBlocked(host) }

func (c *Crawler) isBlocked(host string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	why, ok := c.blocked[host]
	return why, ok
}

func (c *Crawler) block(host, why string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.blocked[host]; !ok {
		c.blocked[host] = why
	}
}

// Hosts is how many hosts have been seen and how many have blocked us, which is
// the pair a long run watches. The second number climbing is the crawl being
// told something.
func (c *Crawler) Hosts() (seen, blocked int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sites), len(c.blocked)
}

// retryAfter reads the header in both of the forms the specification allows, and
// falls back to [DefaultBackoff] for the third form, which is a server that sent
// the header and meant nothing by it.
//
// A server asking for longer than a day is asking us to go away, and there is no
// difference between waiting a week and not coming back, so the wait is capped
// at what a crawl can actually hold.
func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return DefaultBackoff
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return DefaultBackoff
		}
		return capBackoff(time.Duration(secs) * time.Second)
	}
	if at, err := http.ParseTime(v); err == nil {
		d := time.Until(at)
		if d <= 0 {
			return DefaultBackoff
		}
		return capBackoff(d)
	}
	return DefaultBackoff
}

// MaxBackoff is the longest a host is left alone before the wait stops being a
// plan and the host is simply not crawled this run.
const MaxBackoff = 24 * time.Hour

func capBackoff(d time.Duration) time.Duration {
	if d > MaxBackoff {
		return MaxBackoff
	}
	return d
}
