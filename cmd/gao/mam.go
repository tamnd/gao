package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/mam"
)

func runMam(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		mamUsage(stderr)
		return 2
	}
	switch args[0] {
	case "ct":
		return runMamCT(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		mamUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao mam: unknown subcommand %q\n", args[0])
		mamUsage(stderr)
		return 2
	}
}

func mamUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao mam <subcommand> [flags]

subcommands:
  ct  read Certificate Transparency and print the hosts it names

run 'gao mam <subcommand> -h' for the flags of a single subcommand.
`)
}

// runMamCT turns a Certificate Transparency dump into a host list.
//
// It reads a file or standard input by default rather than the network, because
// a `.vn` search is one very large body and the useful thing is to pull it once
// and read it many times. -search asks for it.
func runMamCT(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("mam ct", flag.ContinueOnError)
	fs.SetOutput(stderr)
	suffix := fs.String("suffix", "vn", "keep only hosts under this suffix, on a label boundary")
	seed := fs.String("seed", "", "a file of hosts we already have, one per line: print only what is new")
	direct := fs.Bool("direct", false, "keep only hosts some certificate named outright, rather than through a wildcard")
	counts := fs.Bool("counts", false, "print how many certificates named each host and when the first one was issued")
	search := fs.String("search", "", "ask this Certificate Transparency search front end instead of reading a file")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for the search")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao mam ct [flags] [FILE]

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

	var found []mam.Host
	var err error
	switch {
	case *search != "":
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		found, err = mam.Search(ctx, &http.Client{Timeout: *timeout}, *search, *suffix)
	case fs.NArg() == 1:
		var f *os.File
		if f, err = os.Open(fs.Arg(0)); err == nil {
			found, err = mam.Hosts(f, *suffix)
			_ = f.Close()
		}
	default:
		found, err = mam.Hosts(stdin, *suffix)
	}
	if err != nil {
		fmt.Fprintf(stderr, "gao mam ct: %v\n", err)
		return 1
	}

	total := len(found)
	if *direct {
		kept := make([]mam.Host, 0, len(found))
		for _, h := range found {
			if h.Direct > 0 {
				kept = append(kept, h)
			}
		}
		found = kept
	}

	subtracted := 0
	if *seed != "" {
		have, err := readHosts(*seed)
		if err != nil {
			fmt.Fprintf(stderr, "gao mam ct: %v\n", err)
			return 1
		}
		before := len(found)
		found = mam.New(found, have)
		subtracted = before - len(found)
	}

	if *counts {
		byWeight := make([]mam.Host, len(found))
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
	if *seed != "" {
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
