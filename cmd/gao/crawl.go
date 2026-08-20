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
	pages := fs.Int64("pages", 0, "stop after this many fetches, which is how a first run is kept to a size somebody can read")
	delay := fs.Duration("delay", harvest.DefaultDelay, "the gap between two requests to one host, before the site's own Crawl-delay")
	header := fs.Duration("header", harvest.DefaultHeaderTimeout, "how long a server has to begin answering before the request is given up on")
	timeout := fs.Duration("timeout", harvest.DefaultFetchTimeout, "how long the whole exchange gets, header and body together")
	shards := fs.Int("shards", harvest.DefaultShards, "how many keep-alive pools the hosts are spread over")
	strikes := fs.Int("strikes", harvest.DefaultStrikes, "how many failures a host that has never answered gets, or -1 to keep asking")
	verify := fs.Bool("verify", false, "check TLS certificates, which drops the sites whose certificates have expired")
	expect := fs.Int64("expect", crawl.DefaultExpect, "how many URLs the frontier's resident filter is sized for")
	volume := fs.Int64("volume", crawl.DefaultVolume, "how large a WARC volume grows before the next one opens")
	keep := fs.Int("keep", 0, "how many finished WARC volumes stay on the disk, zero for all of them")
	part := fs.Duration("part", crawl.DefaultPartEvery, "how long a part stays open before it is closed and pushed although it is not full")
	push := fs.Bool("push", false, "push each part as it closes and delete the local copy")
	every := fs.Duration("every", time.Minute, "how often the frontier is flushed and a progress line printed")
	report := fs.String("report", "", "write the run report to this file as JSON")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao crawl -dir DIR [-seed FILE] [-shard N -fleet N] [-pages N] [-push] [flags]

Runs gao's own crawler: a frontier of URLs, a pool of polite fetchers, and two
published datasets.

What comes out is addresses and measurements rather than text. A crawled page
carries no grant to pass its text on, so `+store.Org+`/vitweb holds one row per page
that was kept, with the URL, the host, the fetch time, the robots rule that
allowed it, and every score the page was judged on. `+store.Org+`/vitweb-rejects
holds one row per page that was turned away, with the stage that turned it away
and the reason, because a threshold that turns out to be wrong is only
recoverable if what it removed can be found. The bytes stay here: the WARC is
the only copy of the exchange and it is not something anybody else may have.

Both repos are written incrementally. A part is filled, closed, pushed and
deleted before the next one opens, so what the box holds is one part per repo
whatever the crawl weighs. The parts are partitioned by snapshot and sharded by
box, so three boxes writing at once write into one dataset without ever writing
the same path. The WARC volumes are aged out by -keep, which is what makes a
crawl bigger than the disk under it possible at all.

A crawl's rows are metadata rather than text, so a part left to fill on size
alone would take a million and a half pages to close, and everything written on
the way there would sit on this disk and out of reach of anybody reading the
dataset. -part is the other bound: a part that has been open that long is closed
and pushed at the next row whether or not it is full. That is what makes the
published dataset track a crawl that has not finished.

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

	p, runErr := crawl.Run(ctx, crawl.RunOptions{
		Frontier:   f,
		Sink:       sink,
		Crawler:    c,
		Workers:    *workers,
		Batch:      *batch,
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
	fmt.Fprintf(w, "frontier: %s offered, %s queued, %s already seen, %s refused, %s another box's\n",
		thousands(p.Frontier.Offered), thousands(p.Frontier.Queued()),
		thousands(p.Frontier.Duplicate), thousands(p.Frontier.Refused), thousands(p.Frontier.Foreign))
	fmt.Fprintf(w, "archive: %s records in %d volumes, %s written, %d aged out\n",
		thousands(p.Sink.Archived), p.Sink.Volumes, fleet.GB(p.Sink.WARCBytes), p.Sink.Aged)
	fmt.Fprintf(w, "parts: %d written, %d pushed, %s given back to the disk\n",
		p.Sink.Parts, p.Sink.Pushed, fleet.GB(p.Sink.Freed))
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
func crawlSeeds(path string, args []string, f *crawl.Frontier) (queued, refused int, err error) {
	add := func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			return nil
		}
		if !strings.Contains(line, "://") {
			line = "https://" + line + "/"
		}
		ok, _, err := f.Offer(line)
		if err != nil {
			return err
		}
		if ok {
			queued++
		} else {
			refused++
		}
		return nil
	}
	for _, a := range args {
		if err := add(a); err != nil {
			return queued, refused, err
		}
	}
	if path == "" {
		return queued, refused, nil
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
	return queued, refused, s.Err()
}
