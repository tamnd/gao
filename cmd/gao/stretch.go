package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/stretch"
)

func runStretch(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		stretchUsage(stderr)
		return 2
	}
	switch args[0] {
	case "ladder":
		return runStretchLadder(stdout, stderr, args[1:])
	case "pool":
		return runStretchPool(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		stretchUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao stretch: no subcommand named %s\n", args[0])
		stretchUsage(stderr)
		return 2
	}
}

func stretchUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao stretch ladder [-json]
       gao stretch pool [-name NAME] [-json] parts/*.parquet

The context extension ladder, and whether the corpus can climb it.

A long context is trained in stages, and the stages are in the curriculum. What
is not in the curriculum is whether the data exists to fill them. Extension is
usually done on concatenated short documents, which teaches a model to handle
positions and not to carry anything across them, and a model trained that way
passes a synthetic retrieval test at the top window and cannot answer a question
about a statute.

ladder prints the three rungs with the method, the data rule, and what each one
is evaluated by. pool measures a body of parts against them: how many documents
are naturally long enough to teach each window, how many passes over them the
stage would take, how far into the window they reach, and which source the pool
leans on.

The lengths are read out of two columns rather than off the documents, so a box
can measure a release it could not hold. What that read cost is printed with the
reading.

Exits 1 when what was read is not a length distribution, and 2 when it is one the
ladder cannot be climbed with.

run 'gao stretch <command> -h' for the flags of one of them.
`)
}

type stretchRungReport struct {
	stretch.Rung
	Demanded string `json:"demanded"`
}

type stretchLadderReport struct {
	Rungs    []stretchRungReport `json:"rungs"`
	Blocking []string            `json:"blocking,omitempty"`
}

func runStretchLadder(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("stretch ladder", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao stretch ladder [-json]\n\nThe three rungs of the context extension, read off the curriculum.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	report := stretchLadderReport{Blocking: stretch.CheckLadder()}
	for _, r := range stretch.Ladder() {
		report.Rungs = append(report.Rungs, stretchRungReport{Rung: r, Demanded: share(r.Demand, r.Tokens)})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprint(tw, "stage\twindow\tfrom documents over\tspends\ton long slices\tmethod\n")
		for _, r := range report.Rungs {
			fmt.Fprintf(tw, "%d %s\t%d\t%s\t%s\t%s (%s)\t%s\n",
				r.Stage, r.Name, r.Window, stretchFloor(r.Floor), stretchTokens(r.Tokens), stretchTokens(r.Demand), r.Demanded, r.Method)
		}
		_ = tw.Flush()

		fmt.Fprintln(stdout)
		for _, r := range report.Rungs {
			fmt.Fprintf(stdout, "%s: %s. Read against %s.\n", r.Name, r.Data, r.Eval)
		}

		if len(report.Blocking) > 0 {
			fmt.Fprint(stdout, "\nThis is not a ladder:\n")
			for _, w := range report.Blocking {
				fmt.Fprintf(stdout, "  %s\n", w)
			}
		}
	}

	if len(report.Blocking) > 0 {
		return 2
	}
	return 0
}

type stretchBandReport struct {
	Window    int     `json:"window"`
	Floor     int     `json:"floor"`
	Documents int64   `json:"documents"`
	Tokens    int64   `json:"tokens"`
	Mean      int64   `json:"mean"`
	Reach     float64 `json:"reach"`
	Demand    int64   `json:"demand"`
	Passes    float64 `json:"passes"`
	Source    string  `json:"largest_source,omitempty"`
	Share     float64 `json:"largest_share"`
}

type stretchPoolReport struct {
	stretch.Pool
	Box      string              `json:"box"`
	Bands    []stretchBandReport `json:"bands"`
	Faults   []string            `json:"faults,omitempty"`
	Blocking []string            `json:"blocking,omitempty"`
	Holds    bool                `json:"holds"`
	Verdict  string              `json:"verdict"`
}

func runStretchPool(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("stretch pool", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	name := fs.String("name", "the parts read", "what the measurement is of, which is a snapshot or a slice")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: gao stretch pool [-name NAME] [-json] parts/*.parquet\n\nWhat a body of parts holds at each rung of the ladder.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	pool, err := stretch.Measure(*name, fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "gao stretch: %v\n", err)
		return 1
	}

	report := stretchPoolReport{
		Pool:     pool,
		Box:      fleet.Label(),
		Faults:   pool.Faults(),
		Blocking: pool.Blocking(),
		Holds:    pool.Holds(),
		Verdict:  pool.Verdict(),
	}
	for _, b := range pool.Extending() {
		largest := b.Largest()
		report.Bands = append(report.Bands, stretchBandReport{
			Window:    b.Rung.Window,
			Floor:     b.Rung.Floor,
			Documents: b.Documents,
			Tokens:    b.Tokens,
			Mean:      int64(b.Mean()),
			Reach:     b.Reach(),
			Demand:    b.Rung.Demand,
			Passes:    b.Passes(),
			Source:    largest.Source,
			Share:     largest.Share,
		})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printStretch(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

func printStretch(w io.Writer, r stretchPoolReport) {
	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "This is not a length distribution, so nothing was read off it:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "window\tdocuments over the floor\ttokens\tmean\treach\tpasses\tleans on\n")
	for _, b := range r.Bands {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%.1f\t%s %s\n",
			b.Window, thousands(b.Documents), stretchTokens(b.Tokens), thousands(b.Mean), percent(b.Reach), b.Passes, b.Source, percent(b.Share))
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s over %s of Parquet, read on %s.\n", plural(r.Parts, "part"), disk(r.Bytes), r.Box)
	if r.Box == "unmeasured" {
		fmt.Fprint(w, "That box is not on the fleet, so this is a check rather than the corpus reading.\n")
	}
	fmt.Fprintf(w, "Taking the lengths read %s, which is %s of the parts, so the box doing the reading does not have to be the box holding them.\n",
		disk(r.Read), share(r.Read, r.Bytes))
	fmt.Fprintf(w, "The longest document is %s tokens and %s of them are longer than the %d window, which the last rung reads in pieces.\n",
		thousands(int64(r.Longest)), thousands(r.Over), stretch.Top())

	if len(r.Faults) > 0 {
		fmt.Fprintf(w, "\n%s the ladder cannot be climbed with:\n", plural(len(r.Faults), "reading"))
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// stretchFloor writes the length a document has to reach to be in a band, and says
// so in words for the rung that has no floor under it.
func stretchFloor(floor int) string {
	if floor == 0 {
		return "any length"
	}
	return fmt.Sprintf("%d tokens", floor)
}

// stretchTokens writes a token count the way the training plan quotes one.
func stretchTokens(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// thousands writes a count with separators, since a pool of 812394 documents and
// one of 8123940 look alike at a glance and differ by an order of magnitude.
func thousands(n int64) string {
	if n < 0 {
		return "-" + thousands(-n)
	}
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
