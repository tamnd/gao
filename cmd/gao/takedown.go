package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/takedown"
)

// now is a variable so the tests can ask what the register looked like on a
// given day rather than on the day the tests happen to run.
var now = time.Now

func runTakedown(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		takedownUsage(stderr)
		return 2
	}
	switch args[0] {
	case "status":
		return runTakedownStatus(stdout, stderr, args[1:])
	case "check":
		return runTakedownCheck(stdout, stderr, args[1:])
	case "url":
		return runTakedownURL(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		takedownUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao takedown: unknown subcommand %q\n", args[0])
		takedownUsage(stderr)
		return 2
	}
}

func takedownUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao takedown <subcommand> [flags]

subcommands:
  status  what has been filed, what is still open, and how long each one took
  check   read the register for entries that cannot be true
  url     say what the register does to one URL, at the fetch and at the store

The register is `+takedown.Name+` at the root of the repository, and every
subcommand reads it from there unless -file says otherwise.

run 'gao takedown <subcommand> -h' for the flags of a single subcommand.
`)
}

// load opens the register and reports the failure the same way everywhere,
// because every subcommand starts by doing this and none of them can go on
// without it.
func load(stderr io.Writer, cmd, path string) (*takedown.Register, bool) {
	g, err := takedown.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "gao takedown %s: %v\n", cmd, err)
		return nil, false
	}
	return g, true
}

// runTakedownStatus prints the record LIEN-HE.md says is kept in public.
//
// It exits 1 when something is past the promised response time, so that a
// request nobody has acted on turns into a failing build rather than a line in
// a file nobody is reading.
func runTakedownStatus(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("takedown status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", takedown.Name, "the register to read")
	crawled := fs.Int("crawled", 0, "how many hosts have been crawled, to report the objection rate")
	all := fs.Bool("all", false, "print every request rather than only the ones still open")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao takedown status [flags]

Prints what has been filed, what is still open, and how long each honored
request took to stop.

The number that describes the promise is the worst case rather than the median,
because a median hides exactly the request that broke it. A register with
nothing in it reports that nothing has been measured rather than a perfect
record, since a path nobody has used is a path nobody has tested.

Exits 1 when a request is past the 72 hour response time LIEN-HE.md promises.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	g, ok := load(stderr, "status", *file)
	if !ok {
		return 1
	}
	at := now()

	show := g.Open()
	if *all {
		show = g.Requests
	}
	if len(show) > 0 {
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, r := range show {
			fmt.Fprintf(tw, "issue %d\t%s\t%s\t%s\t%s\n",
				r.Issue, r.Host, r.Scope, r.Asked.Format(time.DateOnly), stateOf(r, at))
		}
		_ = tw.Flush()
		fmt.Fprintln(stdout)
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "filed\t%s\n", plural(len(g.Requests), "request"))
	fmt.Fprintf(tw, "open\t%d\n", len(g.Open()))
	late := g.Late(at)
	fmt.Fprintf(tw, "late\t%d past %s\n", len(late), takedown.Target)
	fmt.Fprintf(tw, "worst\t%s\n", took(g.Worst()))
	fmt.Fprintf(tw, "median\t%s\n", took(g.Median()))
	if *crawled > 0 {
		rate, err := g.Rate(*crawled)
		if err != nil {
			fmt.Fprintf(stderr, "gao takedown status: %v\n", err)
			return 1
		}
		// P03-8 is a gate on this number: under 0.5 percent is the prediction
		// and 2 percent is where the crawl stops and the design gets revisited.
		fmt.Fprintf(tw, "objected\t%.3f%% of %s crawled\n", rate*100, plural(*crawled, "host"))
	}
	_ = tw.Flush()

	if len(late) > 0 {
		fmt.Fprintf(stderr, "\n%s past the response time we promised\n", plural(len(late), "request"))
		return 1
	}
	return 0
}

// stateOf is the one line a reader of the register wants: what happened, and
// how long it took.
func stateOf(r takedown.Request, at time.Time) string {
	d, ok := r.Answered()
	if !ok {
		waited := at.Sub(r.Asked).Round(time.Hour)
		if waited > takedown.Target {
			return fmt.Sprintf("not stopped, %s waiting", waited)
		}
		return fmt.Sprintf("open, %s waiting", waited)
	}
	if r.Scope == takedown.Erase && r.Rebuilt.IsZero() {
		return fmt.Sprintf("stopped in %s, releases not rebuilt", d.Round(time.Minute))
	}
	return fmt.Sprintf("stopped in %s", d.Round(time.Minute))
}

// took formats a duration that might not exist, which is the whole point of
// [takedown.ErrNothingFiled]: an unmeasured promise reads as unmeasured.
func took(d time.Duration, err error) string {
	if errors.Is(err, takedown.ErrNothingFiled) {
		return "not measured, because nothing has been filed"
	}
	if err != nil {
		return err.Error()
	}
	return d.Round(time.Minute).String()
}

// runTakedownCheck is here because the register is edited by hand by somebody who
// has just been asked to take something down, which is the worst moment to be
// getting a date field the right way round.
func runTakedownCheck(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("takedown check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", takedown.Name, "the register to read")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao takedown check [flags]

Reads the register for entries that cannot be true: a request stopped before it
was asked, a scope that is neither stop nor erase, an issue number that appears
twice, a rebuild with no release named.

Exits 1 when anything is wrong, so CI can run it on every change to the file.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	g, ok := load(stderr, "check", *file)
	if !ok {
		return 1
	}

	bad := g.Check(now())
	for _, line := range bad {
		fmt.Fprintln(stderr, line)
	}
	if len(bad) > 0 {
		fmt.Fprintf(stderr, "\n%s in %s\n", plural(len(bad), "problem"), *file)
		return 1
	}
	fmt.Fprintf(stdout, "%s, nothing wrong with any of them\n", plural(len(g.Requests), "request"))
	return 0
}

// runTakedownURL answers the two questions the register exists to answer, which are
// not the same question and do not have the same answer.
func runTakedownURL(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("takedown url", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", takedown.Name, "the register to read")
	fetched := fs.String("fetched", "", "when the document was fetched, as 2006-01-02, for the question about the store")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao takedown url [flags] URL [URL ...]

Says what the register does to each URL. There are two gates and they bind at
different times, so there are two answers.

The gate at the fetch binds from the moment a request was made, including on
requests nobody has acted on yet, because the alternative is a crawler that
keeps fetching from a site that asked it to stop for as long as it takes
somebody to edit a file.

The gate at the store depends on when the document was fetched. A request
scoped to stop leaves what was already published alone, so pass -fetched to ask
the real question. Without it the date is today, which is the answer for a
document the crawl is holding right now.

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

	when := now()
	if *fetched != "" {
		var err error
		when, err = time.Parse(time.DateOnly, *fetched)
		if err != nil {
			fmt.Fprintf(stderr, "gao takedown url: -fetched wants a date like 2026-03-15: %v\n", err)
			return 2
		}
	}

	g, ok := load(stderr, "url", *file)
	if !ok {
		return 1
	}

	covered := 0
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, raw := range fs.Args() {
		block, blocked := g.Blocked(raw)
		erase, erased := g.Erased(raw, when)
		switch {
		case blocked && erased:
			covered++
			fmt.Fprintf(tw, "%s\tdo not fetch, do not publish\tissue %d\n", raw, erase.Issue)
		case blocked:
			covered++
			fmt.Fprintf(tw, "%s\tdo not fetch, what was published stays\tissue %d\n", raw, block.Issue)
		default:
			fmt.Fprintf(tw, "%s\tnobody has asked us to stop\t\n", raw)
		}
	}
	_ = tw.Flush()

	if covered > 0 {
		return 1
	}
	return 0
}
