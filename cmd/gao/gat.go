package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/may"
)

func runGat(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		gatUsage(stderr)
		return 2
	}
	switch args[0] {
	case "pins":
		return runGatPins(stdout, stderr, args[1:])
	case "drift":
		return runGatDrift(stdout, stderr, args[1:])
	case "hf":
		return runGatHF(stdout, stderr, args[1:])
	case "ledger":
		return runGatLedger(stdout, stderr, args[1:])
	case "agent":
		return runGatAgent(stdout, stderr, args[1:])
	case "fetch":
		return runGatFetch(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		gatUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao gat: unknown subcommand %q\n", args[0])
		gatUsage(stderr)
		return 2
	}
}

func gatUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao gat <subcommand> [flags]

subcommands:
  pins   print the ingest manifest: what gets downloaded and at which revision
  drift  ask every host whether it still serves the revision we pinned
  hf     fetch the pinned files, resuming from whatever a previous run finished
  ledger print what an ingest has already finished
  agent  print the User-Agent the crawler sends, and where it points
  fetch  fetch one page the way the crawler would, and print what happened

run 'gao gat <subcommand> -h' for the flags of a single subcommand.
`)
}

// runGatAgent prints what a webmaster is going to see in a log.
//
// It exists so that the answer to "what does your crawler call itself" is a
// command rather than a grep. A published identity that has to be read out of
// the source is not published.
func runGatAgent(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("gat agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao gat agent

Prints the User-Agent header this build sends, the product token a site writes
in robots.txt to address it, and the contact page every request points at.

The two strings are different on purpose. A robots.txt rule is matched against
the token and never against the header, so a site that writes

  User-agent: gaobot
  Disallow: /

has addressed this crawler, and a crawler comparing that line against its whole
header would find no match and carry on. That is a real bug in real crawlers and
this is the command that shows the two are not the same thing.
`)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	fmt.Fprintf(stdout, "user agent   %s\n", gat.Agent(version))
	fmt.Fprintf(stdout, "robots token %s\n", gat.Bot)
	fmt.Fprintf(stdout, "contact      %s\n", gat.Contact)
	fmt.Fprint(stdout, "\nA site blocks this crawler by writing, in robots.txt:\n\n")
	fmt.Fprintf(stdout, "  User-agent: %s\n  Disallow: /\n", gat.Bot)
	return 0
}

// runGatFetch is the crawler doing one page, with everything it would do on a
// real run and nothing it would do for a second page.
//
// It exists because the parts of a crawl that are worth checking before starting
// one are the parts a person cannot see from a log line: whether robots.txt was
// read, which rule allowed the page, what the site said about mining, and how
// long the next request would have to wait. All of that is invisible in a fetch
// that worked, and all of it is what makes this a crawler rather than a loop
// around curl.
func runGatFetch(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("gat fetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	delay := fs.Duration("delay", gat.DefaultDelay, "the shortest gap between two requests to one host, before the site's own Crawl-delay")
	timeout := fs.Duration("timeout", 30*time.Second, "how long to wait for one request")
	max := fs.Int64("max", gat.MaxBody, "the largest body to read, in bytes")
	body := fs.Bool("body", false, "write the body to stdout instead of the summary")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao gat fetch [flags] URL [URL ...]

Fetches each URL the way the crawler does: robots.txt first and once per host,
the published User-Agent, a gap between requests that is the longer of ours and
the site's, and a body cap. Redirects are reported rather than followed, because
a redirect can cross to another host where a different robots.txt applies.

It prints what happened rather than the page, so that the parts of the decision
that do not show up in a body are visible: the rule that allowed the fetch, what
the response said about text and data mining, and the wait the next request to
that host would take. Use -body for the bytes.

Exits 0 when every URL was fetched and 1 when any was refused, by robots.txt, by
the host, or by the size cap.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	c := gat.NewCrawler(gat.CrawlOptions{
		Polite:  gat.NewPolite(gat.PoliteOptions{Delay: *delay}),
		Version: version,
		MaxBody: *max,
		Client:  &http.Client{Timeout: *timeout, CheckRedirect: noRedirect},
	})

	refused := 0
	for i, target := range fs.Args() {
		if i > 0 && !*body {
			fmt.Fprintln(stdout)
		}
		v, err := c.Get(ctx, target)
		if err != nil {
			refused++
			reason, why, _ := gat.Reject(err)
			fmt.Fprintf(stderr, "%s\n  refused: %s\n  reason:  %s\n", target, why, reason)
			continue
		}
		if *body {
			_, _ = stdout.Write(v.Body)
			continue
		}
		printVisit(stdout, c, v)
	}
	if refused > 0 {
		fmt.Fprintf(stderr, "\n%d of %d URLs were not fetched\n", refused, fs.NArg())
		return 1
	}
	return 0
}

// noRedirect is the only redirect policy this project uses. See [gat.Crawler.Get].
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func printVisit(stdout io.Writer, c *gat.Crawler, v *gat.Visit) {
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "%s\n", v.URL)
	fmt.Fprintf(tw, "  robots\t%s\n", describeDecision(v.Robots))
	fmt.Fprintf(tw, "  status\t%d\n", v.Status)
	fmt.Fprintf(tw, "  bytes\t%d\n", len(v.Body))
	if ct := v.Header.Get("Content-Type"); ct != "" {
		fmt.Fprintf(tw, "  type\t%s\n", ct)
	}
	if v.Redirect != "" {
		fmt.Fprintf(tw, "  redirect\t%s, not followed\n", v.Redirect)
	}
	fmt.Fprintf(tw, "  mining\t%s\n", describeReservation(v.Reserve))
	fmt.Fprintf(tw, "  next\t%v from now, to %s\n", c.Delay(v.Host), v.Host)
	_ = tw.Flush()
}

func describeDecision(d gat.Decision) string {
	switch d.Why {
	case gat.RobotsAllowDefault:
		return "allowed, no rule addressed to us"
	case gat.RobotsAllow:
		return "allowed by " + d.Rule
	default:
		return d.Why
	}
}

func describeReservation(r gat.Reservation) string {
	if !r.Reserved() {
		return "no reservation, the response asked for nothing"
	}
	var said []string
	for name, value := range r.Signals() {
		said = append(said, name+": "+value)
	}
	sort.Strings(said)
	return "reserved, " + strings.Join(said, "; ")
}

func runGatPins(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("gat pins", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "", "print one source in full, by name")
	files := fs.Bool("files", false, "list every pinned file with its size and digest")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao gat pins [-source NAME] [-files]

Prints the ingest manifest, which is every file gao downloads and the exact
revision it downloads it at. Hub sources are pinned to a commit SHA and direct
sources to the digest of the file that fixes their file list, because a corpus
pinned to a branch cannot be rebuilt from its own manifest.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	if *source != "" {
		p, ok := gat.Pin(doc.Source(*source))
		if !ok {
			fmt.Fprintf(stderr, "gao gat pins: %q is not a pinned source\n", *source)
			return 1
		}
		printPin(stdout, p, true)
		return 0
	}

	fmt.Fprintf(stdout, "ingest manifest, pinned %s\n", gat.PinnedOn())
	fmt.Fprintf(stdout, "%d sources, %d files, %s to download\n", len(gat.Sources()), gat.Files(), may.GB(gat.TotalBytes()))
	// Printed rather than subtracted quietly. A plan that got smaller and does
	// not say so reads as a plan that was always this size.
	if n := gat.DroppedBytes(); n > 0 {
		fmt.Fprintf(stdout, "%s more is pinned and dropped, listed below with the reason\n", may.GB(n))
	}
	fmt.Fprintln(stdout)

	if *files {
		for i, p := range gat.AllSources() {
			if i > 0 {
				fmt.Fprintln(stdout)
			}
			printPin(stdout, p, true)
		}
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "order\tsource\trepo\trevision\tconfig\tfiles\tsize\tclass\n")
	for _, p := range gat.AllSources() {
		repo := p.Repo
		if p.Gated {
			repo += " (gated)"
		}
		size := may.GB(p.Bytes())
		if p.Dropped {
			size += ", dropped"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			p.Order, p.Source, repo, shortRevision(p.Revision), p.Config, len(p.Files), size, p.Class)
	}
	_ = tw.Flush()
	fmt.Fprint(stdout, "\nrun 'gao gat pins -source NAME' for one source in full.\n")
	return 0
}

func printPin(w io.Writer, p gat.Pinned, verbose bool) {
	fmt.Fprintf(w, "%s  %s\n", p.Source, p.Source.Describe())
	fmt.Fprintf(w, "  order:     %d\n", p.Order)
	fmt.Fprintf(w, "  origin:    %s\n", p.Origin)
	fmt.Fprintf(w, "  repo:      %s\n", p.Repo)
	fmt.Fprintf(w, "  page:      %s\n", p.Page())
	fmt.Fprintf(w, "  revision:  %s\n", p.Revision)
	fmt.Fprintf(w, "  config:    %s\n", p.Config)
	fmt.Fprintf(w, "  class:     %s\n", p.Class)
	if p.Gated {
		fmt.Fprint(w, "  gated:     yes, and the ingest fails at the first fetch without an accepted agreement\n")
	}
	if p.Dropped {
		fmt.Fprintf(w, "  dropped:   %d files, %s, pinned and not fetched\n", len(p.Files), may.GB(p.Bytes()))
		fmt.Fprintf(w, "             %s\n", p.DroppedBecause)
	} else {
		fmt.Fprintf(w, "  download:  %d files, %s\n", len(p.Files), may.GB(p.Bytes()))
	}
	if len(p.Excluded) > 0 {
		fmt.Fprintf(w, "  held back: %d files, %s\n", len(p.Excluded), may.GB(p.ExcludedBytes()))
		fmt.Fprintf(w, "             %s\n", p.ExcludedBecause)
	}
	fmt.Fprintf(w, "  note:      %s\n", p.Note)

	if !verbose {
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, f := range p.Files {
		if f.Digest == "" {
			fmt.Fprintf(tw, "  %s\t%s\n", f.Path, may.GB(f.Bytes))
			continue
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", f.Path, may.GB(f.Bytes), shortRevision(f.Digest))
	}
	_ = tw.Flush()
}

// shortRevision trims a commit or a digest to something a table can hold,
// keeping the algorithm prefix so a digest does not read as a commit.
func shortRevision(rev string) string {
	const n = 12
	if algo, hash, ok := strings.Cut(rev, ":"); ok {
		if len(hash) > n {
			return algo + ":" + hash[:n]
		}
		return rev
	}
	if len(rev) > n {
		return rev[:n]
	}
	return rev
}

func runGatDrift(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("gat drift", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 30*time.Second, "how long to wait for all the hosts together")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao gat drift [-timeout DURATION]

Asks every host in the ingest manifest what revision it serves now and reports
the ones that have moved since they were pinned. It reads the network and it
never writes the manifest: re-pinning is a commit somebody makes deliberately,
with the new file lists and byte counts read at the same time, because a
manifest that re-pins itself silently changes what a released corpus was built
from.

Exits 0 when nothing has moved and 1 when something has, so it can run on a
schedule.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	results := make([]driftResult, 0, len(gat.AllSources()))
	for _, p := range gat.AllSources() {
		d, err := gat.Check(ctx, nil, p)
		results = append(results, driftResult{Source: p.Source, Drift: d, Err: err})
	}
	return reportDrift(stdout, stderr, results)
}

// driftResult is one host's answer, or its refusal to give one.
type driftResult struct {
	Source doc.Source
	Drift  gat.Drift
	Err    error
}

// reportDrift is separate from the command so that the reporting can be tested
// without a network, which is the half of this that has branches in it.
func reportDrift(stdout, stderr io.Writer, results []driftResult) int {
	var moved, failed int
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, r := range results {
		switch {
		case r.Err != nil:
			failed++
			fmt.Fprintf(tw, "%s\tunreachable\t%v\n", r.Source, r.Err)
		case r.Drift.Moved():
			moved++
			fmt.Fprintf(tw, "%s\tmoved\tpinned %s, now %s\n", r.Source, shortRevision(r.Drift.Pinned), shortRevision(r.Drift.Current))
		default:
			fmt.Fprintf(tw, "%s\tunchanged\t%s\n", r.Source, shortRevision(r.Drift.Pinned))
		}
	}
	_ = tw.Flush()

	switch {
	case moved > 0:
		fmt.Fprintf(stdout, "\n%d of %d sources have moved upstream. The pins are unchanged and that is deliberate:\na released corpus is built from what was there, not from what is there now.\n",
			moved, len(results))
		return 1
	case failed > 0:
		fmt.Fprintf(stderr, "\ngao gat drift: %d of %d sources could not be reached\n", failed, len(results))
		return 1
	}
	fmt.Fprintf(stdout, "\nall %d sources still serve the revision they were pinned at on %s\n", len(results), gat.PinnedOn())
	return 0
}
