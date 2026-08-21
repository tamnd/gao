package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/frontier"
)

func runFrontier(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		frontierUsage(stderr)
		return 2
	}
	switch args[0] {
	case "canon":
		return runFrontierCanon(stdout, stderr, args[1:])
	case "shape":
		return runFrontierShape(stdout, stderr, args[1:])
	case "budget":
		return runFrontierBudget(stdout, stderr, args[1:])
	case "fit":
		return runFrontierFit(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		frontierUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao frontier: unknown subcommand %q\n", args[0])
		frontierUsage(stderr)
		return 2
	}
}

func frontierUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao frontier <subcommand> [flags] [url ...]

subcommands:
  canon  print the canonical form of each URL, and what merged with what
  shape  print the template each URL came from, and what is wrong with it
  budget run a list of URLs past the budget and print what it would ask for
  fit    work out whether the frontier fits on the box that has to hold it

canon, shape and budget read URLs one per line from standard input when there
are none on the command line. fit reads no URLs, because it is the check that
runs before the first fetch rather than one that runs over a list.

run 'gao frontier <subcommand> -h' for the flags of a single subcommand.
`)
}

// runFrontierCanon prints the canonical form of every URL it is given, which is the
// only way to see two links merge before a crawl has spent the fetch on finding
// out that they did not.
func runFrontierCanon(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("frontier canon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	merged := fs.Bool("merged", false, "print only the URLs that merged with an earlier one")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	urls, err := readURLs(fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "gao frontier canon: %v\n", err)
		return 1
	}

	seen := map[string]string{}
	kept, dropped, bad := 0, 0, 0
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, raw := range urls {
		got, err := frontier.Canon(raw)
		if err != nil {
			bad++
			if !*merged {
				fmt.Fprintf(tw, "skip\t%s\t%s\n", raw, err)
			}
			continue
		}
		if first, ok := seen[got]; ok {
			dropped++
			fmt.Fprintf(tw, "merge\t%s\twith %s\n", raw, first)
			continue
		}
		seen[got] = raw
		kept++
		if !*merged {
			fmt.Fprintf(tw, "keep\t%s\t%s\n", raw, got)
		}
	}
	_ = tw.Flush()

	fmt.Fprintf(stdout, "\n%d urls in, %d pages out, %d merged, %d not followed\n", len(urls), kept, dropped, bad)
	return 0
}

// runFrontierShape prints the template a URL came from, which is what a budget
// counts and what a trap is detected in.
func runFrontierShape(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("frontier shape", flag.ContinueOnError)
	fs.SetOutput(stderr)
	count := fs.Bool("count", false, "print each template once with how many URLs it covered")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	urls, err := readURLs(fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "gao frontier shape: %v\n", err)
		return 1
	}

	seen := map[string]int{}
	describe := map[string]string{}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, raw := range urls {
		s, err := frontier.Of(raw)
		if err != nil {
			if !*count {
				fmt.Fprintf(tw, "%s\t%s\n", raw, err)
			}
			continue
		}
		seen[s.String()]++
		describe[s.String()] = s.Describe()
		if !*count {
			fmt.Fprintf(tw, "%s\t%s\n", raw, s.Describe())
		}
	}

	if *count {
		keys := make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if seen[keys[i]] != seen[keys[j]] {
				return seen[keys[i]] > seen[keys[j]]
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys {
			fmt.Fprintf(tw, "%d\t%s\n", seen[k], describe[k])
		}
	}
	_ = tw.Flush()

	fmt.Fprintf(stdout, "\n%d urls off %s\n", len(urls), plural(len(seen), "template"))
	return 0
}

// runFrontierBudget runs a list of URLs past the budget in the order they were
// given and prints what it would have asked for.
//
// It answers the question that otherwise only gets answered halfway through a
// crawl: how much of this frontier is one site's calendar. The results it feeds
// back are optimistic by construction, since it has not fetched anything and
// cannot know what came back, so what this measures is the structural half of
// the decision rather than the earned half.
func runFrontierBudget(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("frontier budget", flag.ContinueOnError)
	fs.SetOutput(stderr)
	refused := fs.Bool("refused", false, "print only the URLs the budget would not ask for")
	shapes := fs.Bool("shapes", false, "print what every template on every host spent")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	urls, err := readURLs(fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "gao frontier budget: %v\n", err)
		return 1
	}

	b := frontier.NewBudget(frontier.Options{})
	hosts := map[string]bool{}
	asked, skipped := 0, 0

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, raw := range urls {
		ok, why := b.Offer(raw)
		if ok {
			asked++
			// Nothing has been fetched, so nothing has been learned. New is the
			// assumption that makes this an upper bound on what a crawl would
			// ask for rather than a guess at what it would get.
			b.Fetched(raw, frontier.New)
			if u, err := frontier.Parse(raw); err == nil {
				hosts[u.Host] = true
			}
			if !*refused {
				fmt.Fprintf(tw, "ask\t%s\t\n", raw)
			}
			continue
		}
		skipped++
		fmt.Fprintf(tw, "skip\t%s\t%s\n", raw, why)
	}
	_ = tw.Flush()

	if *shapes {
		names := make([]string, 0, len(hosts))
		for h := range hosts {
			names = append(names, h)
		}
		sort.Strings(names)
		for _, h := range names {
			spent, gained, n := b.Spent(h)
			fmt.Fprintf(stdout, "\n%s: %s over %s, %s of new text\n",
				h, plural(spent, "url"), plural(n, "template"), plural(gained, "page"))
			st := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
			for _, line := range b.Lines(h) {
				note := line.Closed
				if note == "" {
					note = fmt.Sprintf("%d of %d spent", line.Spent, line.Allowed)
				}
				fmt.Fprintf(st, "  %s\t%s\n", line.Shape, note)
			}
			_ = st.Flush()
		}
	}

	fmt.Fprintf(stdout, "\n%d urls in, %d asked for, %d skipped, across %s\n",
		len(urls), asked, skipped, plural(len(hosts), "host"))
	return 0
}

// frontierFitReport is what fit prints with -json, so a gate in a script reads the
// same numbers a person does.
type frontierFitReport struct {
	Box       string `json:"box"`
	Memory    int64  `json:"memory"`
	Available int64  `json:"available"`
	Reserve   int64  `json:"reserve"`
	frontier.Cost
	Headroom float64          `json:"headroom"`
	Fits     bool             `json:"fits"`
	Sample   *frontier.Sample `json:"sample,omitempty"`
	Blocking []string         `json:"blocking"`
	Faults   []string         `json:"faults"`
}

// runFrontierFit answers the one question that has to be answered before the crawl
// starts, because it cannot be answered afterwards.
//
// The frontier and the seen set are the only things a crawl holds that cannot be
// rebuilt from what it has already written. A crawler killed for memory at a
// hundred million fetches comes back not knowing what it has already asked for,
// and a crawl that does not know that is a crawl that asks again. So the
// arithmetic runs first, it runs against a named box rather than against a
// number somebody remembers, and it exits non zero when the answer is no.
func runFrontierFit(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("frontier fit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	p := frontier.Frontier()
	name := fs.String("box", "server1", "the box in the fleet the frontier has to fit on")
	asJSON := fs.Bool("json", false, "print the whole answer as JSON")
	measure := fs.Int("measure", 0, "build a real frontier of this many hosts and read the heap, instead of trusting the arithmetic")
	fs.Int64Var(&p.URLs, "urls", p.URLs, "URLs in the seed frontier")
	fs.Int64Var(&p.Hosts, "hosts", p.Hosts, "hosts the frontier spreads across")
	fs.Int64Var(&p.Active, "active", p.Active, "hosts held resident at once, the rest paged out with the frontier")
	fs.IntVar(&p.SeenBits, "bits", p.SeenBits, "bits per URL in the filter in front of the exact seen set")
	fs.IntVar(&p.ShapesPerHost, "shapes", p.ShapesPerHost, "templates a host is tracked at")
	fs.IntVar(&p.ReadyPerHost, "ready", p.ReadyPerHost, "URLs queued in memory per active host")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "gao frontier fit: %s takes no URLs, since it runs before there are any\n", fs.Name())
		frontierUsage(stderr)
		return 2
	}

	box, ok := fleet.Lookup(*name)
	if !ok {
		names := make([]string, 0, len(fleet.Boxes))
		for _, b := range fleet.Boxes {
			names = append(names, b.Name)
		}
		fmt.Fprintf(stderr, "gao frontier fit: no box named %q in the fleet, which is %s\n", *name, strings.Join(names, ", "))
		return 2
	}

	c := p.Cost()
	out := frontierFitReport{
		Box:       box.Name,
		Memory:    box.Memory,
		Available: frontier.Available(box),
		Reserve:   frontier.Reserve,
		Cost:      c,
		Headroom:  c.Headroom(box),
		Fits:      c.Fits(box),
		Faults:    p.Faults(),
	}
	if len(out.Faults) == 0 {
		out.Blocking = c.Blocking(box)
	}
	if *measure > 0 {
		s := frontier.Measure(*measure, p.ShapesPerHost)
		out.Sample = &s
	}

	if *asJSON {
		printJSON(stdout, stderr, out)
	} else {
		printFrontierFit(stdout, p, out)
	}
	if len(out.Faults) > 0 || len(out.Blocking) > 0 {
		return 1
	}
	return 0
}

func printFrontierFit(w io.Writer, p frontier.Plan, out frontierFitReport) {
	fmt.Fprintf(w, "%s\n\n", p.Describe())

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "seen filter\t%s\t%d bits per URL, exact set on disk behind it\n", frontier.Bytes(out.Seen), p.SeenBits)
	fmt.Fprintf(tw, "host ledgers\t%s\t%s resident of %s\n", frontier.Bytes(out.Ledgers), frontier.Count(p.Active, "host"), frontier.Count(p.Hosts, "host"))
	fmt.Fprintf(tw, "template tallies\t%s\t%d templates apiece\n", frontier.Bytes(out.Shapes), p.ShapesPerHost)
	fmt.Fprintf(tw, "facet counters\t%s\t%d paths apiece, %d combinations each\n", frontier.Bytes(out.Facets), p.FacetPathsPerHost, p.FacetsPerPath)
	fmt.Fprintf(tw, "ready queues\t%s\t%d URLs apiece\n", frontier.Bytes(out.Ready), p.ReadyPerHost)
	fmt.Fprintf(tw, "total\t%s\t%d bytes per resident host, queue aside\n", frontier.Bytes(out.Total), out.PerHost)
	fmt.Fprintf(tw, "%s has\t%s\t%s of memory less %s reserved\n", out.Box, frontier.Bytes(out.Available), frontier.Bytes(out.Memory), frontier.Bytes(out.Reserve))
	_ = tw.Flush()

	fmt.Fprintf(w, "\nthe filter errs %.2f%% of the time, which costs %s in the exact set on disk over the whole crawl and no lost URLs\n",
		100*out.FalsePositive, frontier.Count(out.Reads, "lookup"))

	if out.Sample != nil {
		s := out.Sample
		fmt.Fprintf(w, "\nmeasured on this machine: %s over %s offered, %d bytes per host against the %d worked out above\n",
			frontier.Bytes(s.Heap), plural(s.Offered, "URL"), s.PerHost, out.PerHost)
		fmt.Fprintf(w, "which puts the whole plan at %s measured against %s worked out\n",
			frontier.Bytes(s.Scaled(p)), frontier.Bytes(out.Total))
	}

	fmt.Fprintln(w)
	for _, why := range out.Faults {
		fmt.Fprintf(w, "fault: %s\n", why)
	}
	for _, why := range out.Blocking {
		fmt.Fprintf(w, "blocked: %s\n", why)
	}
	if len(out.Faults) == 0 && len(out.Blocking) == 0 {
		fmt.Fprintf(w, "fits: %s of %s on %s, %.0f%% spare. The crawl may start.\n",
			frontier.Bytes(out.Total), frontier.Bytes(out.Available), out.Box, 100*out.Headroom)
	} else {
		fmt.Fprintf(w, "\nthe crawl does not start until this comes back clean\n")
	}
}

// stdin is a variable so the tests can hand these subcommands a list of URLs
// without a file on disk.
var stdin io.Reader = os.Stdin

// readURLs takes the URLs off the command line, or off standard input when there
// are none, because a frontier is a file and a file is what gets piped.
func readURLs(args []string, in io.Reader) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}
	var out []string
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no urls given, on the command line or on standard input")
	}
	return out, nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	if strings.HasSuffix(noun, "y") {
		return fmt.Sprintf("%d %sies", n, strings.TrimSuffix(noun, "y"))
	}
	// address gives addresses rather than addresss, and the same rule covers
	// the rest of the sibilants a report here is likely to count.
	for _, end := range []string{"s", "x", "z", "ch", "sh"} {
		if strings.HasSuffix(noun, end) {
			return fmt.Sprintf("%d %ses", n, noun)
		}
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
