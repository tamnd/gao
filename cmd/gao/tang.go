package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/tang"
)

func runTang(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("layers", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	source := fs.String("source", "", "the source being estimated")
	quoted := fs.Int64("quoted", 0, "the number this project publishes for the source, to check the reading against")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao layers -source name [-quoted N] [-json] layers.jsonl

Read an estimate that was taken layer by layer and say what the layers nobody
opened are worth.

One JSON object per line, one line per layer: its name, where it sits in the
source's quality ordering, what it holds on disk, and what was read out of it,
which for most layers of most samples is nothing.

The range printed here is not a sampling interval. It is the bound on the part
of the corpus that was never read: every unread byte at the thinnest rate any
layer read at, and every unread byte at the richest. Reading more of the layers
already read does not close it, which is the whole reason it is printed
separately from the interval gao estimate computes.

When the layers nobody read sit below every layer that was read, the report says
so in those words. Clean text reads at a higher rate per byte, so scaling the
rate of the cleanest layers over all of the layers buys tokens that are not
there, and that is the mistake that produced the 194B reading of HPLT v3 before
the 176B one.

Exits 1 when the reading is not a stratified sample, and 2 when it is one that
carries more than sampling error.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *source == "" {
		fs.Usage()
		return 2
	}

	layers, err := tang.ReadLayers(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao layers: %v\n", err)
		return 1
	}

	s := tang.Source{Source: *source, Quoted: *quoted, Layers: layers}
	lo, hi := s.Packing()
	report := tangReport{
		Source:    s.Source,
		Quoted:    s.Quoted,
		Layers:    len(s.Layers),
		Read:      len(s.Lit()),
		Stored:    s.Stored(),
		Dark:      s.DarkBytes(),
		DarkShare: s.DarkShare(),
		Under:     s.UnderBytes(),
		Pooled:    s.Pooled(),
		ThinPack:  lo,
		RichPack:  hi,
		Estimate:  s.Estimate(),
		Low:       s.Low(),
		High:      s.High(),
		Spread:    s.Spread(),
		Faults:    s.Faults(),
		Blocking:  s.Blocking(),
		Holds:     s.Holds(),
		Verdict:   s.Verdict(),
		source:    s,
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printTang(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

type tangReport struct {
	Source string `json:"source"`
	Quoted int64  `json:"quoted,omitempty"`

	Layers int `json:"layers"`
	Read   int `json:"read"`

	Stored int64 `json:"stored"`

	// Dark and Under are the corpus nobody opened, and the part of that which
	// sits below every layer somebody did.
	Dark      int64   `json:"dark"`
	DarkShare float64 `json:"dark_share"`
	Under     int64   `json:"under"`

	Pooled   float64 `json:"pooled"`
	ThinPack float64 `json:"thin_pack"`
	RichPack float64 `json:"rich_pack"`

	Estimate int64   `json:"estimate"`
	Low      int64   `json:"low"`
	High     int64   `json:"high"`
	Spread   float64 `json:"spread"`

	Faults   []string `json:"faults,omitempty"`
	Blocking []string `json:"blocking,omitempty"`
	Holds    bool     `json:"holds"`
	Verdict  string   `json:"verdict"`

	source tang.Source
}

func printTang(w io.Writer, r tangReport) {
	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "This is not a reading of the source, so no layer was scaled:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "layer\trank\ton disk\tread\ttokens a stored byte\testimate\n")
	for _, l := range r.source.Layers {
		if !l.Sampled() {
			fmt.Fprintf(tw, "%s\t%d\t%s\t.\t.\t.\n", l.Name, l.Rank, corpusSize(l.Stored))
			continue
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%.3f\t%s\n",
			l.Name, l.Rank, corpusSize(l.Stored), corpusSize(l.Read), l.Yield(), billions(l.Estimate()))
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%d of %s were read, holding %s of the %s the source takes on disk.\n",
		r.Read, plural(r.Layers, "layer"), corpusSize(r.Stored-r.Dark), corpusSize(r.Stored))
	if r.Dark == 0 {
		fmt.Fprint(w, "Every layer has a rate of its own, so nothing here is scaled at another layer's rate and the range over the part nobody read is a range over nothing.\n")
	} else {
		fmt.Fprintf(w, "The %s nobody read is scaled at %.3f tokens a stored byte, which is the pooled rate of the layers that were, and at the thinnest and the richest of them it would be %s to %s instead.\n",
			corpusSize(r.Dark), r.Pooled, billions(r.Low), billions(r.High))
	}
	if r.Under > 0 {
		fmt.Fprintf(w, "Of that, %s sits below every layer that was read, so the range is drawn from rates measured on the cleaner end of the corpus and covers the rest only if the rest reads like it.\n",
			corpusSize(r.Under))
	}

	if len(r.Faults) > 0 {
		fmt.Fprint(w, "\nThis estimate carries more than sampling error:\n")
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}
