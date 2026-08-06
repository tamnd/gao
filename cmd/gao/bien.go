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

	"github.com/tamnd/gao/bien"
)

func runBien(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		bienUsage(stderr)
		return 2
	}
	switch args[0] {
	case "canon":
		return runBienCanon(stdout, stderr, args[1:])
	case "shape":
		return runBienShape(stdout, stderr, args[1:])
	case "budget":
		return runBienBudget(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		bienUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao bien: unknown subcommand %q\n", args[0])
		bienUsage(stderr)
		return 2
	}
}

func bienUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao bien <subcommand> [flags] [url ...]

subcommands:
  canon  print the canonical form of each URL, and what merged with what
  shape  print the template each URL came from, and what is wrong with it
  budget run a list of URLs past the budget and print what it would ask for

with no URLs on the command line, each subcommand reads them one per line from
standard input.

run 'gao bien <subcommand> -h' for the flags of a single subcommand.
`)
}

// runBienCanon prints the canonical form of every URL it is given, which is the
// only way to see two links merge before a crawl has spent the fetch on finding
// out that they did not.
func runBienCanon(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("bien canon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	merged := fs.Bool("merged", false, "print only the URLs that merged with an earlier one")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	urls, err := readURLs(fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "gao bien canon: %v\n", err)
		return 1
	}

	seen := map[string]string{}
	kept, dropped, bad := 0, 0, 0
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, raw := range urls {
		got, err := bien.Canon(raw)
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

// runBienShape prints the template a URL came from, which is what a budget
// counts and what a trap is detected in.
func runBienShape(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("bien shape", flag.ContinueOnError)
	fs.SetOutput(stderr)
	count := fs.Bool("count", false, "print each template once with how many URLs it covered")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	urls, err := readURLs(fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "gao bien shape: %v\n", err)
		return 1
	}

	seen := map[string]int{}
	describe := map[string]string{}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, raw := range urls {
		s, err := bien.Of(raw)
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

// runBienBudget runs a list of URLs past the budget in the order they were
// given and prints what it would have asked for.
//
// It answers the question that otherwise only gets answered halfway through a
// crawl: how much of this frontier is one site's calendar. The results it feeds
// back are optimistic by construction, since it has not fetched anything and
// cannot know what came back, so what this measures is the structural half of
// the decision rather than the earned half.
func runBienBudget(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("bien budget", flag.ContinueOnError)
	fs.SetOutput(stderr)
	refused := fs.Bool("refused", false, "print only the URLs the budget would not ask for")
	shapes := fs.Bool("shapes", false, "print what every template on every host spent")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	urls, err := readURLs(fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "gao bien budget: %v\n", err)
		return 1
	}

	b := bien.NewBudget(bien.Options{})
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
			b.Fetched(raw, bien.New)
			if u, err := bien.Parse(raw); err == nil {
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
	return fmt.Sprintf("%d %ss", n, noun)
}
