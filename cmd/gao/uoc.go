package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/uoc"
)

func runUoc(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("uoc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	source := fs.String("source", "", "the source being estimated")
	snapshot := fs.String("snapshot", "", "the snapshot the manifest totals come off")
	seed := fs.String("seed", "", "the seed the sample was drawn with, so a third party draws the same parts")
	parts := fs.Int64("parts", 0, "parts in the whole source, off the pinned manifest")
	bytes := fs.Int64("bytes", 0, "bytes in the whole source, off the pinned manifest")
	exact := fs.Int64("exact", 0, "the exact count, once it exists, to check against the interval that was published")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao uoc -source name -parts N -bytes N -seed s [-exact N] [-json] sample.jsonl

Turn a sampled reading into an interval, and say what it would cost to narrow it.

One JSON object per line, one line per part read: the path, the bytes it held,
the bytes the manifest pins for it, the tokens counted, and the tokenizer that
counted them.

The estimator is a ratio rather than a mean. The pinned manifest already gives
the exact byte total of the source without a byte being fetched, so the sample
only has to establish tokens per byte and the total it scales is known. The
alternative, mean tokens per part times the part count, inherits the variance of
the shard sizes, and both are printed because the gap between them is the whole
argument.

Under 30 parts no interval is printed. A narrow interval off eight parts reads as
precision rather than as the guess it is. A sample that names no seed is refused
the same way, since parts somebody opened until the number looked right have no
sampling distribution to build an interval on.

Pass -exact once the real count exists. It says whether the count landed inside
the interval that was published, which is the only thing that makes publishing
an interval cost anything.

Exits 1 when the sample is not a measurement, and 2 when the estimate is too wide
to publish or when an exact count landed outside it.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *source == "" || *parts <= 0 || *bytes <= 0 {
		fs.Usage()
		return 2
	}

	s, err := uoc.ReadSource(*source, *snapshot, *seed, *parts, *bytes, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao uoc: %v\n", err)
		return 1
	}

	report := uocReport{
		Source:    s.Source,
		Snapshot:  s.Snapshot,
		Seed:      s.Seed,
		Tokenizer: s.Tokenizer(),
		Parts:     s.Parts,
		Bytes:     s.Bytes,
		Read:      s.Read(),
		Fraction:  s.Fraction(),
		Rate:      s.Ratio(),
		Estimate:  s.Estimate(),
		Mean:      s.Mean(),
		Low:       s.Low(),
		High:      s.High(),
		Width:     s.Width(),
		Line:      uoc.MaxWidth,
		Floor:     uoc.MinSample,
		Needed:    s.Needed(uoc.MaxWidth),
		Holds:     s.Holds(),
		Blocking:  s.Blocking(),
		Verdict:   s.Verdict(),
	}
	if *exact > 0 {
		report.Exact = *exact
		report.Covers = s.Covers(*exact)
		report.Against = s.Against(*exact)
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printUoc(stdout, s, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	case *exact > 0 && !report.Covers:
		return 2
	}
	return 0
}

type uocReport struct {
	Source    string `json:"source"`
	Snapshot  string `json:"snapshot,omitempty"`
	Seed      string `json:"seed,omitempty"`
	Tokenizer string `json:"tokenizer,omitempty"`

	Parts int64 `json:"parts"`
	Bytes int64 `json:"bytes"`

	Read     int     `json:"read"`
	Fraction float64 `json:"fraction"`
	Rate     float64 `json:"rate"`

	Estimate int64 `json:"estimate"`

	// Mean is the estimator that ignores the manifest, carried so a reader can
	// see what leaning on the known total bought.
	Mean int64 `json:"mean"`

	Low   int64   `json:"low"`
	High  int64   `json:"high"`
	Width float64 `json:"width"`
	Line  float64 `json:"line"`
	Floor int     `json:"floor"`

	// Needed is parts still to read before the estimate is publishable.
	Needed int64 `json:"needed"`

	// Exact and what it did to the interval, once there is one.
	Exact   int64  `json:"exact,omitempty"`
	Covers  bool   `json:"covers,omitempty"`
	Against string `json:"against,omitempty"`

	Holds    bool     `json:"holds"`
	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printUoc(w io.Writer, s uoc.Source, r uocReport) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "estimator\ttokens\tinterval\twidth\tleans on\n")
	fmt.Fprintf(tw, "ratio on bytes\t%s\t%s to %s\t%s\t%s of pinned bytes\n",
		billions(r.Estimate), billions(r.Low), billions(r.High), percent(r.Width), corpusSize(r.Bytes))
	fmt.Fprintf(tw, "mean per part\t%s\t.\t.\t%d parts, sizes unread\n", billions(r.Mean), r.Parts)
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s, %d of %d parts read at seed %s, which is %s of the source and %.3f tokens a byte.\n",
		r.Source, r.Read, r.Parts, r.Seed, percent(r.Fraction), r.Rate)
	fmt.Fprintf(w, "The two estimators differ by %s, which is what the manifest total is worth here rather than what the extra reading would have cost.\n",
		billions(abs64(r.Estimate-r.Mean)))

	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "\nThis sample is not an estimate:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	if r.Needed > 0 {
		fmt.Fprintf(w, "Reaching the %s line takes %s more, which is the fourfold cost of halving an interval rather than a proportional one.\n",
			percent(r.Line), count(int(r.Needed), "part"))
	}
	fmt.Fprintf(w, "\n%s\n", r.Verdict)
	if r.Against != "" {
		fmt.Fprintf(w, "%s\n", r.Against)
	}
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
