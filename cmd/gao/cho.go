package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/cho"
	"github.com/tamnd/gao/may"
)

func runCho(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	crawl := fs.String("crawl", "gao-crawl-2026-09", "the crawl the readings were taken off")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao wait [-crawl NAME] [-json] hosts.jsonl

To wait: what the crawl actually left between requests to one host.

A crawl delay is configured once, in a file, in seconds. Between that number and
the wire there is a scheduler, a connection pool, a retry path, a redirect that
lands on the same site under a different name, and a DNS answer with two
addresses in it. Any of them can put two requests a hundred milliseconds apart
while the configuration still reads four seconds, and nothing in the crawl
notices, because the crawl is watching throughput and what went wrong is a gap.

The item asks for this verified on the real box under real load rather than in a
simulator, and those are two requirements. A delay kept by a scheduler with one
fetch in flight is not evidence about a scheduler with several hundred competing
for four cores, so a reading taken off an idle box is refused here rather than
reported with a note.

Four things fail differently. The shortest gap against the delay that binds. The
delay we configured against the one robots.txt asked for, since the larger is
not ours to choose. The peak requests in flight to one host against the cap,
because every gap can be held on every connection while six are open. And the
429 and 503 answers, which are the site's own opinion of our crawl delay.

Exits 1 if this is not a verification, or 2 if it is one that says the crawl was
not polite.

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

	r, err := cho.ReadRun(*crawl, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao wait: %v\n", err)
		return 1
	}

	// The crawl runs from server1 and a politeness claim is a claim about a
	// machine, so a reading off a box nobody has is not one.
	var claims []string
	for _, h := range r.Hosts {
		if _, ok := may.Lookup(h.Box); !ok && h.Box != "" {
			claims = append(claims, fmt.Sprintf(
				"%s was fetched from %s, which is not a box on this fleet, so the load it was measured under is nobody's to reproduce",
				h.Host, h.Box))
		}
	}

	report := choReport{
		Crawl: r.Crawl, Hosts: len(r.Hosts), Load: r.Load(),
		Broken: len(r.Broken()), Overrun: len(r.Overrun()), Refused: len(r.Refused()),
		Asked:   len(r.Asked()),
		MinLoad: cho.MinLoad, MaxErrors: cho.MaxErrors,
		Holds:    r.Holds() && len(claims) == 0,
		Blocking: append(r.Blocking(), claims...), Verdict: r.Verdict(),
	}
	if h, ok := r.Closest(); ok {
		report.Closest = h.Host
		report.Margin = h.Margin()
	}
	for _, h := range r.Ranked() {
		report.Readings = append(report.Readings, choReading{
			Host: h.Host, Box: h.Box, Fetches: h.Fetches, Seconds: h.Seconds,
			Delay: h.Delay, Robots: h.Robots, Required: h.Required(),
			MinGap: h.MinGap, MeanGap: h.MeanGap, Margin: h.Margin(),
			Cap: h.Cap, Peak: h.Peak, Load: h.Load, Errors: h.Errors(),
			Kept: h.Kept(), Overrun: h.Overrun(), Refused: h.Refused(),
		})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printCho(stdout, r, claims)
	}
	if len(r.Blocking()) > 0 || len(claims) > 0 {
		return 1
	}
	if !r.Holds() {
		return 2
	}
	return 0
}

// choReading is one host as the table carries it.
type choReading struct {
	Host string `json:"host"`
	Box  string `json:"box"`

	Fetches int     `json:"fetches"`
	Seconds float64 `json:"seconds"`

	Delay  float64 `json:"delay"`
	Robots float64 `json:"robots"`

	// Required is the delay that binds, which is the larger of the two above.
	Required float64 `json:"required"`

	MinGap  float64 `json:"min_gap"`
	MeanGap float64 `json:"mean_gap"`
	Margin  float64 `json:"margin"`

	Cap  int `json:"cap"`
	Peak int `json:"peak"`
	Load int `json:"load"`

	Errors float64 `json:"errors"`

	Kept    bool `json:"kept"`
	Overrun bool `json:"overrun"`
	Refused bool `json:"refused"`
}

type choReport struct {
	Crawl string `json:"crawl"`
	Hosts int    `json:"hosts"`

	// Load is the lowest box wide load any reading was taken under, which is
	// what the weakest claim in the set rests on.
	Load int `json:"load"`

	Readings []choReading `json:"readings"`

	Asked   int `json:"asked"`
	Broken  int `json:"broken"`
	Overrun int `json:"overrun"`
	Refused int `json:"refused"`

	Closest string  `json:"closest"`
	Margin  float64 `json:"margin"`

	MinLoad   int     `json:"min_load"`
	MaxErrors float64 `json:"max_errors"`

	Holds bool `json:"holds"`

	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printCho(w io.Writer, r cho.Run, claims []string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "host\tbox\tfetches\twatched\tdelay\trobots\tshortest gap\tmean gap\tof required\tin flight\t429 and 503\n")
	for _, h := range r.Ranked() {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%.0f%%\t%d of %d\t%s\n",
			h.Host, h.Box, h.Fetches, watched(h.Seconds), gap(h.Delay), robots(h.Robots),
			gap(h.MinGap), gap(h.MeanGap), 100*h.Margin(), h.Peak, h.Cap, percent(h.Errors()))
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s, %s watched under %d fetches in flight at the lowest.\n",
		r.Crawl, plural(len(r.Hosts), "host"), r.Load())
	fmt.Fprintf(w, "A reading needs %d fetches in flight box wide to have been taken under load, since a delay held by a scheduler with nothing else to do is not the delay it will hold.\n",
		cho.MinLoad)
	fmt.Fprint(w, "The delay that binds is the larger of ours and the one robots.txt asks for, and the shortest gap is what it is measured against rather than the mean.\n")
	if asked := r.Asked(); len(asked) > 0 {
		fmt.Fprintf(w, "%s asked for a longer gap than the crawl's own delay, and what they got is the number they asked for.\n", count(len(asked), "host"))
	}

	why := append(r.Blocking(), claims...)
	if len(why) > 0 {
		fmt.Fprintf(w, "\n%s:\n", count(len(why), "fault"))
		for _, one := range why {
			fmt.Fprintf(w, "  %s\n", one)
		}
		return
	}
	fmt.Fprintf(w, "\n%s.\n", r.Verdict())
}

// gap renders a delay to the tenth of a second, which is the resolution the
// failure shows up at.
func gap(f float64) string { return fmt.Sprintf("%.1fs", f) }

// robots renders what a host asked for, where asking for nothing is not asking
// for zero seconds.
func robots(f float64) string {
	if f <= 0 {
		return "none"
	}
	return gap(f)
}

// watched renders how long a host was observed for, in the minutes a window
// like this is actually described in.
func watched(f float64) string { return fmt.Sprintf("%.0fm", f/60) }
