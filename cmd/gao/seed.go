package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/seed"
)

func runSeed(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		seedUsage(stderr)
		return 2
	}
	switch args[0] {
	case "ct":
		return runSeedCT(stdout, stderr, args[1:])
	case "oai":
		return runSeedOAI(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		seedUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao seed: unknown subcommand %q\n", args[0])
		seedUsage(stderr)
		return 2
	}
}

func seedUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao seed <subcommand> [flags]

subcommands:
  ct   read Certificate Transparency and print the hosts it names
  oai  ask university repositories for their catalogs, and say which of them answer

run 'gao seed <subcommand> -h' for the flags of a single subcommand.
`)
}

// runSeedCT turns a Certificate Transparency dump into a host list.
//
// It reads a file or standard input by default rather than the network, because
// a `.vn` search is one very large body and the useful thing is to pull it once
// and read it many times. -search asks for it.
func runSeedCT(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("seed ct", flag.ContinueOnError)
	fs.SetOutput(stderr)
	suffix := fs.String("suffix", "vn", "keep only hosts under this suffix, on a label boundary")
	known := fs.String("seed", "", "a file of hosts we already have, one per line: print only what is new")
	direct := fs.Bool("direct", false, "keep only hosts some certificate named outright, rather than through a wildcard")
	counts := fs.Bool("counts", false, "print how many certificates named each host and when the first one was issued")
	search := fs.String("search", "", "ask this Certificate Transparency search front end instead of reading a file")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for the search")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao seed ct [flags] [FILE]

Reads a Certificate Transparency search result and prints the hosts it names,
one per line, deduplicated and sorted, ready to be a seed list.

Vietnam has no zone file to work from, so there is no list of .vn domains to
start a crawl from. The logs are the closest thing there is: every publicly
trusted certificate since 2018 is logged in public, and a certificate names the
hosts it is valid for. That makes the logs a list of hosts somebody was willing
to prove they controlled, with no opinion at all about whether the host is worth
indexing, which is the reason to prefer it to a search engine export.

The output is leads and not sites. A host in the logs may be gone, may be an
internal service, may be a certificate provisioned and never used. That is the
right trade: a dead lead costs one request that fails fast, and a missing host
costs a site that never enters the corpus.

Use -seed to subtract a list you already have, which is the measurement that
says whether this route was worth running at all.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return 2
	}

	var found []seed.Host
	var err error
	switch {
	case *search != "":
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		found, err = seed.Search(ctx, &http.Client{Timeout: *timeout}, *search, *suffix)
	case fs.NArg() == 1:
		var f *os.File
		if f, err = os.Open(fs.Arg(0)); err == nil {
			found, err = seed.Hosts(f, *suffix)
			_ = f.Close()
		}
	default:
		found, err = seed.Hosts(stdin, *suffix)
	}
	if err != nil {
		fmt.Fprintf(stderr, "gao seed ct: %v\n", err)
		return 1
	}

	total := len(found)
	if *direct {
		kept := make([]seed.Host, 0, len(found))
		for _, h := range found {
			if h.Direct > 0 {
				kept = append(kept, h)
			}
		}
		found = kept
	}

	subtracted := 0
	if *known != "" {
		have, err := readHosts(*known)
		if err != nil {
			fmt.Fprintf(stderr, "gao seed ct: %v\n", err)
			return 1
		}
		before := len(found)
		found = seed.New(found, have)
		subtracted = before - len(found)
	}

	if *counts {
		byWeight := make([]seed.Host, len(found))
		copy(byWeight, found)
		sort.SliceStable(byWeight, func(i, j int) bool { return byWeight[i].Certs > byWeight[j].Certs })
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, h := range byWeight {
			first := "unknown"
			if !h.First.IsZero() {
				first = h.First.Format(time.DateOnly)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", h.Name, plural(h.Certs, "certificate"), plural(h.Direct, "direct"), first)
		}
		_ = tw.Flush()
	} else {
		for _, h := range found {
			fmt.Fprintln(stdout, h.Name)
		}
	}

	fmt.Fprintf(stderr, "\n%s under .%s", plural(total, "host"), strings.TrimPrefix(*suffix, "."))
	if *direct {
		fmt.Fprintf(stderr, ", %d named outright", len(found)+subtracted)
	}
	if *known != "" {
		fmt.Fprintf(stderr, ", %d already in the seed, %d new", subtracted, len(found))
	}
	fmt.Fprintln(stderr)
	return 0
}

// readHosts reads a seed list: one host per line, blank lines and comments
// skipped, because a list somebody has been keeping by hand has both in it.
func readHosts(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// runSeedOAI asks a list of repositories what they hold.
//
// Two questions, and they are not the same one. Without -links it is asking
// whether the repository speaks the protocol at all, which is what P03-6 is a
// prediction about and what decides whether a university's theses are reachable
// without crawling a search form. With -links it is harvesting the catalog into
// URLs the frontier can take.
func runSeedOAI(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("seed oai", flag.ContinueOnError)
	fs.SetOutput(stderr)
	links := fs.Bool("links", false, "harvest the catalog and print the URLs in it, one per line")
	max := fs.Int("max", 0, "stop after this many records per repository, or every record when zero")
	from := fs.String("from", "", "only records changed since this date, as 2006-01-02")
	set := fs.String("set", "", "harvest one set rather than the whole repository")
	timeout := fs.Duration("timeout", 2*time.Minute, "how long to wait for one repository")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao seed oai [flags] BASE [BASE ...]

Asks each OAI-PMH base URL who it is and whether it will hand over its catalog.
Base URLs are read from the arguments, or one per line from standard input.

A university repository is a DSpace or an Eprints install holding theses,
journal issues and conference papers, which is the highest quality Vietnamese
prose per byte in this project and close to invisible to a crawler: the landing
pages sit behind a search form and the identifiers are handles rather than
paths. A repository that speaks OAI-PMH will instead hand over a complete
catalog of what it holds, which is the difference between reaching some of it
and reaching all of it.

With -links it harvests and prints the URLs, ready for the frontier. Without,
it reports what each repository said about itself and whether it works, and
counts how many of them did, which is the measurement P03-6 is about.

Exits 0 when every repository answered and 1 when any did not.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	bases, err := readURLs(fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "gao seed oai: %v\n", err)
		return 2
	}

	var since time.Time
	if *from != "" {
		since, err = time.Parse(time.DateOnly, *from)
		if err != nil {
			fmt.Fprintf(stderr, "gao seed oai: -from wants a date like 2024-03-15: %v\n", err)
			return 2
		}
	}

	c := &http.Client{Timeout: *timeout}
	ctx := context.Background()
	working := 0
	for i, base := range bases {
		r, err := seed.Works(ctx, c, base)
		if err != nil {
			fmt.Fprintf(stderr, "%s\n  %v\n", base, err)
			continue
		}
		working++

		if *links {
			items, err := seed.Records(ctx, c, r, seed.Harvest{Set: *set, From: since, Max: *max})
			if err != nil && !errors.Is(err, seed.ErrNoRecords) {
				// Whatever came back before the failure is still worth having,
				// so this reports the break and keeps the records.
				fmt.Fprintf(stderr, "%s\n  %v\n", base, err)
			}
			for _, it := range items {
				if it.Deleted {
					continue
				}
				for _, link := range it.Links {
					fmt.Fprintln(stdout, link)
				}
			}
			continue
		}

		if i > 0 {
			fmt.Fprintln(stdout)
		}
		printRepository(stdout, r)
	}

	fmt.Fprintf(stderr, "\n%d of %s answered OAI-PMH\n", working, plural(len(bases), "repository"))
	if working < len(bases) {
		return 1
	}
	return 0
}

func printRepository(stdout io.Writer, r seed.Repository) {
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "%s\n", r.Base)
	fmt.Fprintf(tw, "  name\t%s\n", r.Name)
	fmt.Fprintf(tw, "  protocol\t%s\n", r.Protocol)
	fmt.Fprintf(tw, "  granularity\t%s\n", r.Granularity)
	if !r.Earliest.IsZero() {
		fmt.Fprintf(tw, "  earliest\t%s\n", r.Earliest.Format(time.DateOnly))
	}
	fmt.Fprintf(tw, "  formats\t%s\n", strings.Join(r.Formats, ", "))
	if len(r.Admin) > 0 {
		fmt.Fprintf(tw, "  contact\t%s\n", strings.Join(r.Admin, ", "))
	}
	_ = tw.Flush()
}
