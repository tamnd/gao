package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/suat"
)

func runSuat(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("suat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	crawl := fs.String("crawl", "gao-crawl-2026-09", "the crawl these measurements came from")
	next := fs.Int64("next", 0, "divide this many further fetches between the target classes")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao suat [-crawl name] [-next fetches] [-json] yield.jsonl

Read the crawl's net yield while it is still running.

Net yield is unique documents kept per fetch made, and it is the number that
decides whether this crawl was worth doing. The plan says 0.15 or better. The
kill criterion says stop below 0.08 once a hundred million fetches are behind
it, which is only actionable if the meter exists first, so this reads a file the
crawl appends a cumulative point to every few million fetches.

Net rather than gross. A fetch that returned 200 with a full page under it has
produced nothing if the page is a duplicate, or is boilerplate, or is a calendar
entry for 2031, so every fetch is accounted for by outcome and a point whose
outcomes do not sum to its fetches is refused.

The per class breakdown is the part that changes what happens next. Forums are
the class Common Crawl's per host cap hurts most and the class this crawl exists
for, and a per class yield that only arrives at the end cannot move a budget.

Give -next a fetch count and it divides that many further fetches between the
classes. The division is made on tokens per fetch in the last window rather than
over the whole crawl, because a class whose hosts have already been read goes on
looking good in a cumulative number for a long time. A class whose operators are
objecting is halted regardless of what it pays, and no class is ever cut to
nothing, since a class nobody measures cannot be found to have recovered.

Exits 1 if the run is not a continuous measurement, or 2 if the crawl should
stop.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	r, err := suat.ReadRun(*crawl, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao suat: %v\n", err)
		return 1
	}
	p, _ := r.Latest()
	report := suatReport{
		Crawl:   r.Crawl,
		At:      p.At,
		Box:     p.Box,
		Points:  len(r.Points),
		Verdict: r.Read(),
		Faults:  r.Faults(),
	}
	if w, ok := r.Window(); ok {
		report.Window = w.Yield()
		report.Windowed = true
	}
	if d, ok := r.Trend(); ok {
		report.Trend = d
		report.Trended = true
	}
	if *next > 0 {
		b := r.Budget(*next)
		report.Budget = &b
		report.Faults = b.Faults
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printSuat(stdout, p, report)
	}
	if len(report.Faults) > 0 {
		return 1
	}
	if report.Verdict.Call == suat.Stop {
		return 2
	}
	return 0
}

type suatReport struct {
	Crawl   string       `json:"crawl"`
	At      int64        `json:"at"`
	Box     string       `json:"box"`
	Points  int          `json:"points"`
	Verdict suat.Verdict `json:"verdict"`

	// Window is the yield of the last stretch alone, which is what moves when
	// something changes. Windowed says whether there was a stretch to measure.
	Window   float64 `json:"window,omitempty"`
	Windowed bool    `json:"windowed"`

	Trend   float64 `json:"trend,omitempty"`
	Trended bool    `json:"trended"`

	// Budget is the next stretch of fetches divided between the classes, and nil
	// unless somebody asked for one.
	Budget *suat.Budget `json:"budget,omitempty"`

	Faults []string `json:"faults,omitempty"`
}

func printSuat(w io.Writer, p suat.Point, r suatReport) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "class\tfetches\tdocuments\tyield\ttokens\thosts\tobjected\n")
	for _, c := range p.Ranked() {
		t := p.By[c]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%.3f\t%s\t%s\t%s\n",
			c, fetchCount(t.Fetches), fetchCount(t.Documents), t.Yield(),
			fetchCount(t.Tokens), fetchCount(t.Hosts), rate(t.Objection()))
	}
	total := p.Total()
	fmt.Fprintf(tw, "\t\t\t\t\t\t\n")
	fmt.Fprintf(tw, "all\t%s\t%s\t%.3f\t%s\t%s\t%s\n",
		fetchCount(total.Fetches), fetchCount(total.Documents), total.Yield(),
		fetchCount(total.Tokens), fetchCount(total.Hosts), rate(total.Objection()))
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s at %s on %s, measured at %s.\n",
		r.Crawl, fetchCount(p.At), r.Box, plural(r.Points, "checkpoint"))
	if r.Windowed {
		fmt.Fprintf(w, "The last stretch alone yielded %.3f, which is the number that moves before the cumulative one does.\n", r.Window)
	}
	if holding, forum, news := p.Holding(); holding {
		fmt.Fprintf(w, "P03-5 is holding: forums have produced %s tokens against %s from news archives.\n", fetchCount(forum), fetchCount(news))
	} else {
		forum, news := p.By[suat.Forum].Tokens, p.By[suat.News].Tokens
		fmt.Fprintf(w, "P03-5 is not holding: forums have produced %s tokens against %s from news archives.\n", fetchCount(forum), fetchCount(news))
	}
	fmt.Fprintf(w, "The classifier placed %.1f%% of fetches into one of the five target classes.\n", 100*p.Classified())

	if r.Budget != nil {
		printSuatBudget(w, *r.Budget)
	}

	if len(r.Faults) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(r.Faults), "fault"))
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
		return
	}
	fmt.Fprintf(w, "\n%s: %s.\n", r.Verdict.Call, r.Verdict.Why)
}

func printSuatBudget(w io.Writer, b suat.Budget) {
	fmt.Fprintf(w, "\nthe next %s, divided on the last %s:\n", fetchCount(b.Stretch), fetchCount(b.Window))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "  class\tmove\tshare\tfetches\tnow\tbefore\n")
	for _, s := range b.Slices {
		fmt.Fprintf(tw, "  %s\t%s\t%.0f%%\t%s\t%.1f\t%.1f\n",
			s.Class, s.Move, 100*s.Share, fetchCount(s.Gets(b.Stretch)), s.PerFetch, s.Average)
	}
	_ = tw.Flush()
	for _, s := range b.Moving() {
		fmt.Fprintf(w, "  %s\n", s.Why)
	}
	if b.Settled() {
		fmt.Fprintf(w, "\n%s.\n", b.Verdict())
	}
}

// rate prints a share the way an objection rate is discussed, which is in
// hundredths of a percent because the ceiling is 2% and the prediction is 0.5%.
func rate(f float64) string { return fmt.Sprintf("%.2f%%", 100*f) }

// fetchCount prints a crawl scale number, which runs to hundreds of millions of
// fetches and tens of billions of tokens.
func fetchCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprint(n)
	}
}
