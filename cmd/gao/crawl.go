package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tamnd/gao/crawl"
	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/frontier"
	"github.com/tamnd/gao/harvest"
	"github.com/tamnd/gao/store"
)

// pushGrace is how long a part still gets to reach the store after the run has
// been told to stop. It is long enough for a full part on a slow uplink and
// short enough that a stopped crawl is a stopped crawl.
const pushGrace = 10 * time.Minute

func runCrawl(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("crawl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "where the frontier, the WARC volumes and the parts being written live")
	seeds := fs.String("seed", "", "a file of URLs or hosts to start from, one per line, or - for standard input")
	snapshot := fs.String("snapshot", "", "the snapshot the parts are written under, name-revision (default web-YYYYMMDD)")
	shard := fs.Int("shard", 0, "this box's index in the fleet, which decides which hosts it owns and names its parts")
	boxes := fs.Int("fleet", 1, "how many boxes are crawling, so that every host is fetched by exactly one of them")
	workers := fs.Int("workers", crawl.DefaultWorkers, "how many fetches are in flight at once")
	batch := fs.Int("batch", 0, "how many URLs are taken from the frontier at a time (default eight per worker)")
	feeders := fs.Int("feeders", crawl.DefaultFeeders, "how many goroutines take batches from the frontier")
	pages := fs.Int64("pages", 0, "stop after this many fetches, which is how a first run is kept to a size somebody can read")
	delay := fs.Duration("delay", harvest.DefaultDelay, "the gap between two requests to one host, before the site's own Crawl-delay")
	header := fs.Duration("header", harvest.DefaultHeaderTimeout, "how long a server has to begin answering before the request is given up on")
	timeout := fs.Duration("timeout", harvest.DefaultFetchTimeout, "how long the whole exchange gets, header and body together")
	shards := fs.Int("shards", harvest.DefaultShards, "how many keep-alive pools the hosts are spread over")
	strikes := fs.Int("strikes", harvest.DefaultStrikes, "how many failures a host that has never answered gets, or -1 to keep asking")
	verify := fs.Bool("verify", false, "check TLS certificates, which drops the sites whose certificates have expired")
	expect := fs.Int64("expect", crawl.DefaultExpect, "how many URLs the frontier's resident filter is sized for")
	warc := fs.Bool("warc", false, "keep a WARC recording of every fetch, which costs about a sixth of the crawl's CPU")
	volume := fs.Int64("volume", crawl.DefaultVolume, "how large a WARC volume grows before the next one opens, with -warc")
	keep := fs.Int("keep", 0, "how many finished WARC volumes stay on the disk, zero for all of them")
	part := fs.Duration("part", crawl.DefaultPartEvery, "how long a part stays open before it is closed and pushed although it is not full")
	push := fs.Bool("push", false, "push each part as it closes and delete the local copy")
	every := fs.Duration("every", time.Minute, "how often the frontier is flushed and a progress line printed")
	index := fs.Duration("index", 0, "how often the parts index and the card are rebuilt from the repo, off by default, and for exactly one box in a fleet")
	report := fs.String("report", "", "write the run report to this file as JSON")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	profile := fs.String("pprof", "", "serve profiles on this address, which turns on the block and mutex profilers")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao crawl -dir DIR [-seed FILE] [-shard N -fleet N] [-pages N] [-push] [flags]

Runs gao's own crawler: a frontier of URLs, a pool of polite fetchers, and two
published datasets.

What comes out is the pages. `+store.Org+`/vitweb holds one row per page that was
kept, carrying the article as plain text, the same article as markdown, and the
whole page as markdown, alongside the URL, the host, the fetch time, the robots
rule that allowed it, and every score the page was judged on.
`+store.Org+`/vitweb-rejects holds one row per page that was turned away, with the
stage that turned it away and the reason, because a threshold that turns out to
be wrong is only recoverable if what it removed can be found.

-warc keeps a byte for byte recording of every fetch beside those two, and it is
off. The case for keeping one is that an extractor is a program we will change
and a page it got wrong is only worth having if the bytes are still here when the
next version runs. The case against it is what it costs: writing the WARC was
15.6% of a live crawl's CPU and gzipping it 13.3%, on a crawler whose ceiling is
CPU rather than bandwidth. It also does not buy what it appears to, because -keep
ages the volumes out after a few gigabytes, so by the time an extractor changes
the bytes are gone. What the published rows carry instead is the address and the
time of the fetch, and going back to a page is a thing anybody with those two can
do. Turn it on for a run whose extraction is the thing under test.

The pages ship under the posture in law/posture.go, which is the one Common
Crawl fetches and publishes under: publicly reachable, robots.txt honored, text
and data mining reservations honored, published as fetched with the address on
every row, takedowns acted on. A page that reserved itself is fetched and
measured and never published. Run `+"`gao law`"+` to print the table.

Both repos are written incrementally. A part is filled, closed, pushed and
deleted before the next one opens, so what the box holds is one part per repo
whatever the crawl weighs. The parts are partitioned by snapshot and sharded by
box, so three boxes writing at once write into one dataset without ever writing
the same path. The WARC volumes, if there are any, are aged out by -keep, which
is what makes a crawl bigger than the disk under it possible at all.

-part is the other bound on a part, alongside its size. A part that has been
open that long is closed and pushed at the next row whether or not it is full,
which is what makes the published dataset track a crawl that has not finished
rather than appear all at once when it does.

-index rebuilds `+"`"+store.IndexName+"`"+` and the card from the repo on a timer, and it is
off. The index is the one file in a working repo that says what is in it, and
without this nothing writes it while a crawl is running, so a fleet that pushes
for two days leaves an index describing the first afternoon. Give it to exactly
one box. Three crawlers each reading the index, adding their own parts and
writing it back is a lost update. The box that has it lists the repo and reads
every part's footer rather than remembering its own, so it indexes the whole
fleet's parts without hearing from the other boxes. A pass is a few kilobytes
per part, which is 16.6MB of footers over a repo holding 474.3MB, and it grows
with the repo rather than with the crawl, so an hour is a reasonable timer and a
minute is not.

-pprof turns on the block and mutex profilers and serves them, which is how a
claim about this crawler's ceiling gets checked. A goroutine dump says where
goroutines are parked, the mutex profile says which lock they waited on and for
how long in total, and only the second one can tell you whether a change helped.
A run with -pprof is being measured rather than counted, so do not quote its
pages a second as the box's rate.

A fleet splits on the host and not on the URL. Box two offering a link to a site
box one owns writes it down as another box's and does not queue it, so every
site is fetched by exactly one machine and that machine's politeness schedule is
the whole story for it. Three boxes each waiting a second between requests would
be three requests a second to a site that asked for one. The split is written
into the frontier at its first open and a resume under a different shard is
refused.

A run is stopped by its context or by -pages, and stopping it is how it ends.
The frontier is flushed on the way out, so the next run starts from the URLs
this one had queued rather than from the seed list.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" {
		fmt.Fprint(stderr, "gao crawl: -dir is required, because a crawl that picks its own directory writes a frontier somewhere nobody looks\n")
		return 2
	}
	if *boxes < 1 || *shard < 0 || *shard >= *boxes {
		fmt.Fprintf(stderr, "gao crawl: shard %d of %d is not a box in the fleet\n", *shard, *boxes)
		return 2
	}
	if *snapshot == "" {
		*snapshot = "web-" + time.Now().UTC().Format("20060102")
	}
	if *profile != "" {
		// Before the crawl starts rather than alongside it. The profilers have
		// to be on before the first lock is taken or the profile is missing the
		// opening of the run, which is where a frontier that rebuilds its filter
		// at open does its worst work.
		if err := serveProfiles(stdout, *profile); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	kept, _ := store.Lookup(crawl.KeptRepo)
	drops, _ := store.Lookup(crawl.RejectRepo)

	var token string
	if *push {
		token = fleet.Token()
		if token == "" {
			fmt.Fprintf(stderr, "gao crawl: %s is not set, and both repos need it\n", fleet.TokenEnv)
			return 2
		}
	}
	// The index describes what is on the repo, so a run that puts nothing there
	// has nothing to describe, and asking for one is a flag that was meant for
	// another box.
	if *index > 0 && !*push {
		fmt.Fprintln(stderr, "gao crawl: -index rebuilds the index of the published repo, so it needs -push")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Before the first request, so that a wrong token costs a second rather than
	// the hours it takes to fill the first part.
	pushers := map[string]*store.Pusher{}
	if *push {
		for _, d := range []store.Dataset{kept, drops} {
			p := &store.Pusher{Repo: d.Repo(), Token: token}
			if err := p.EnsureRepo(ctx, d); err != nil {
				fmt.Fprintf(stderr, "gao crawl: %v\n", err)
				return 1
			}
			pushers[d.Name] = p
		}
	}

	f, err := crawl.OpenFrontier(crawl.FrontierOptions{
		Dir:    *dir + "/frontier",
		Budget: frontier.NewBudget(frontier.Options{}),
		Shard:  *shard,
		Fleet:  *boxes,
		Expect: *expect,
	})
	if err != nil {
		fmt.Fprintf(stderr, "gao crawl: %v\n", err)
		return 1
	}
	defer func() { _ = f.Close() }()

	queued, refused, err := crawlSeeds(*seeds, fs.Args(), f)
	if err != nil {
		fmt.Fprintf(stderr, "gao crawl: %v\n", err)
		return 1
	}

	sinkOpts := crawl.SinkOptions{
		Dir:       *dir,
		Snapshot:  *snapshot,
		Shard:     *shard,
		Box:       fleet.Label(),
		Version:   version,
		Record:    *warc,
		Volume:    *volume,
		Keep:      *keep,
		PartEvery: *part,
		Out:       stdout,
	}
	// A nil Push leaves the parts on the disk, which is what a run without
	// -push means. Setting it to a function that fails would turn a local run
	// into an error at the first part that closes.
	if *push {
		sinkOpts.Push = func(d store.Dataset, local, path string) error {
			p := pushers[d.Name]
			if p == nil {
				return fmt.Errorf("no pusher for %s", d.Repo())
			}
			// Not the run's context. A crawl is stopped by a signal and the last
			// thing it does is close both rolls, which is where the part holding
			// everything since the last one gets pushed. Under the run's context
			// that push is already canceled before it starts, so every stop
			// leaves its final part on the disk and out of the dataset. The
			// timeout is what keeps a stop from hanging on a store that is not
			// answering.
			up, cancel := context.WithTimeout(context.WithoutCancel(ctx), pushGrace)
			defer cancel()
			_, err := p.Push(up, local, path)
			return err
		}
	}
	sink, err := crawl.OpenSink(sinkOpts)
	if err != nil {
		fmt.Fprintf(stderr, "gao crawl: %v\n", err)
		return 1
	}

	c := harvest.NewCrawler(harvest.CrawlOptions{
		Polite:  harvest.NewPolite(harvest.PoliteOptions{Delay: *delay}),
		Version: version,
		Timeout: *timeout,
		Strikes: *strikes,
		Transport: harvest.TransportOptions{
			Shards: *shards,
			Header: *header,
			Verify: *verify,
		},
	})

	fmt.Fprintf(stdout, "%s and %s\n", kept.Repo(), drops.Repo())
	fmt.Fprintf(stdout, "snapshot %s, box %d of %d, %d workers, %s between requests to one host\n",
		*snapshot, *shard+1, *boxes, *workers, *delay)
	fmt.Fprintf(stdout, "%s seeds queued, %s refused or another box's, %s already in the frontier\n",
		thousands(int64(queued)), thousands(int64(refused)), thousands(f.Stats().Duplicate))
	if !*push {
		fmt.Fprint(stdout, "nothing is being pushed, so the parts stay on this disk\n")
	}

	// The index runs alongside the crawl rather than after it, because a crawl
	// that is stopped and started every few hours would otherwise never write
	// one, and a crawl that is not stopped would never write one either.
	if *index > 0 {
		indexing, done := context.WithCancel(ctx)
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			indexEvery(indexing, stdout, *index, token, []store.Dataset{kept, drops})
		}()
		defer func() {
			done()
			<-stopped
		}()
	}

	p, runErr := crawl.Run(ctx, crawl.RunOptions{
		Frontier:   f,
		Sink:       sink,
		Crawler:    c,
		Workers:    *workers,
		Batch:      *batch,
		Feeders:    *feeders,
		Pages:      *pages,
		Checkpoint: *every,
		Out:        stdout,
	})
	// The sink is closed whatever happened, because a part left open is a part
	// whose rows are in memory, and its close is where the last one is pushed.
	if err := sink.Close(); err != nil && runErr == nil {
		runErr = err
	}
	// The last part of each repo is written by that close, so the sink's own
	// counters are read again afterwards. Reporting the ones Run came back with
	// would report a run that pushed nothing on a crawl short enough to fit in
	// one part, which is every trial run.
	p.Sink = sink.Stats()

	if *asJSON || *report != "" {
		b, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "gao crawl: %v\n", err)
			return 1
		}
		if *report != "" {
			if err := os.WriteFile(*report, append(b, '\n'), 0o644); err != nil {
				fmt.Fprintf(stderr, "gao crawl: %v\n", err)
				return 1
			}
		}
		if *asJSON {
			fmt.Fprintf(stdout, "%s\n", b)
		}
	} else {
		crawlSummary(stdout, p)
	}

	if runErr != nil {
		fmt.Fprintf(stderr, "gao crawl: %v\n", runErr)
		return 1
	}
	return 0
}

// crawlSummary is what a run leaves on the terminal, which is the yield and the
// disk. Both are what a fleet is sized by: pages a second says how long ten
// million sites take, and freed says whether the box survives it.
func crawlSummary(w io.Writer, p crawl.Progress) {
	fmt.Fprintf(w, "\n%s: %s fetched, %s kept, %s dropped, %s failed, %.1f pages a second\n",
		round(p.Elapsed), thousands(p.Fetched), thousands(p.Kept), thousands(p.Dropped),
		thousands(p.Failed), p.Rate())
	fmt.Fprintf(w, "schedule: %s handed back because the host was not due, %s put back because the batch already had two of that host, %s batches short of hosts\n",
		thousands(p.Waited), thousands(p.Frontier.Deferred), thousands(p.Frontier.Exhausted))
	fmt.Fprintf(w, "frontier: %s offered, %s queued, %s already seen, %s refused, %s another box's\n",
		thousands(p.Frontier.Offered), thousands(p.Frontier.Queued()),
		thousands(p.Frontier.Duplicate), thousands(p.Frontier.Refused), thousands(p.Frontier.Foreign))
	if p.Sink.Volumes > 0 || p.Sink.WARCBytes > 0 {
		fmt.Fprintf(w, "archive: %s records in %d volumes, %s written, %d aged out\n",
			thousands(p.Sink.Archived), p.Sink.Volumes, fleet.GB(p.Sink.WARCBytes), p.Sink.Aged)
	} else {
		fmt.Fprintf(w, "archive: %s pages fetched and not recorded, which is -warc off\n", thousands(p.Sink.Archived))
	}
	fmt.Fprintf(w, "parts: %d written, %d pushed, %s given back to the disk\n",
		p.Sink.Parts, p.Sink.Pushed, fleet.GB(p.Sink.Freed))
	fmt.Fprintf(w, "names: %s asked of the resolver, %s answered from the cache, %s waited on a query already running, %s did not resolve, %s held\n",
		thousands(p.Names.Misses), thousands(p.Names.Hits), thousands(p.Names.Joined),
		thousands(p.Names.Failures), thousands(int64(p.Names.Held)))
}

// crawlSeeds reads the seed list and offers each URL to the frontier as it is
// read, returning how many were queued and how many were turned away.
//
// The seed list is URLs on the command line, a file, or standard input. A file
// whose name ends in .gz is read through gzip, since the seed is now an extract
// from a Common Crawl index and those are kept compressed.
//
// It offers as it reads rather than collecting first. The seed used to be a few
// thousand hosts and a slice was the obvious thing. It is now the Vietnamese
// side of a whole Common Crawl index, 6.6 million URLs over 404,186 hosts, and
// as a slice of strings that is most of server1's memory spent before the crawl
// has fetched a page.
//
// A bare host is taken as its home page over https, because a seed list from
// Certificate Transparency is hosts and typing the scheme onto ten million of
// them is work for the program rather than for the person.
//
// It offers in batches because of what one URL per turn costs at this size. The
// whole seed goes in through the frontier's lock, and taking it once per line
// loaded server1 at around five thousand URLs a second, which is twenty three
// minutes of a machine holding open connections to nothing before it fetches its
// first page.
func crawlSeeds(path string, args []string, f *crawl.Frontier) (queued, refused int, err error) {
	batch := make([]string, 0, seedBatch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n, err := f.OfferAll(batch)
		queued += n
		if err != nil {
			return err
		}
		refused += len(batch) - n
		batch = batch[:0]
		return nil
	}
	add := func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			return nil
		}
		if !strings.Contains(line, "://") {
			line = "https://" + line + "/"
		}
		batch = append(batch, line)
		if len(batch) < seedBatch {
			return nil
		}
		return flush()
	}
	for _, a := range args {
		if err := add(a); err != nil {
			return queued, refused, err
		}
	}
	if path == "" {
		return queued, refused, flush()
	}

	r := io.Reader(os.Stdin)
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return queued, refused, err
		}
		defer func() { _ = file.Close() }()
		r = file
	}
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return queued, refused, fmt.Errorf("%s: %w", path, err)
		}
		defer func() { _ = zr.Close() }()
		r = zr
	}

	s := bufio.NewScanner(bufio.NewReaderSize(r, 1<<20))
	s.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for s.Scan() {
		if err := add(s.Text()); err != nil {
			return queued, refused, err
		}
	}
	if err := s.Err(); err != nil {
		return queued, refused, err
	}
	return queued, refused, flush()
}

// seedBatch is how many seed URLs are handed to the frontier in one turn at its
// lock. Large enough that the lock stops being the cost, small enough that the
// batch is a rounding error against the frontier itself.
const seedBatch = 10000
