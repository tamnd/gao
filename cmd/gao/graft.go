package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/graft"
)

func runGraft(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("graft", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	budget := fs.Int64("budget", 40_000_000_000, "the continued pretraining budget the recovery is a share of, in tokens")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao graft [-budget N] [-json] expansions.jsonl

To graft: what adding Vietnamese tokens to a base vocabulary bought and cost.

The continued pretraining path does not get to pick its tokenizer. The base
model arrives with one, every weight in it was trained against those ids, and
the only move left is to keep the vocabulary and graft onto it. The fertility
that buys is real, it costs nothing to measure, and it is available before a
single step is trained. It is also the easy half.

The other half is that the grafted rows start out meaning nothing while every
row around them is a direction the body has been reading for trillions of
tokens. So the loss goes up when the expanded tokenizer is switched on and comes
back down over some number of tokens, and those tokens come out of this run's
budget. An expansion that buys a fifth of the fertility and spends a third of
the run recovering has made the run worse, and everything on the tokenizer side
of it reads like a win.

So the methods are ordered by what they net, which is the fertility bought less
the share of the run spent buying it, and the checks are about the rows rather
than about the vocabulary.

Exits 1 if this is not a measurement of the mechanics, or 2 if it is one that
says the graft is not worth making.

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

	t, err := graft.ReadTrial(*budget, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao graft: %v\n", err)
		return 1
	}

	// A fertility number carries the box it was measured on, and the inventory
	// is the only thing that can say whether that box is one of ours.
	var claims []string
	for _, e := range t.Runs {
		if _, ok := fleet.Lookup(e.Box); !ok && e.Box != "" {
			claims = append(claims, fmt.Sprintf(
				"%s by %s was measured on %s, which is not a box on this fleet, so the fertility it reports is nobody's to reproduce",
				e.Base, e.Method, e.Box))
		}
	}

	report := graftReport{
		Budget: t.Budget, Methods: len(t.Runs), Stranded: len(t.Stranded()),
		MinGain: graft.MinGain, MaxShare: graft.MaxShare,
		Holds:    t.Holds() && len(claims) == 0,
		Blocking: append(t.Blocking(), claims...), Verdict: t.Verdict(),
	}
	if b, ok := t.Best(); ok {
		report.Base = b.Base
		report.Best = b.Method
		report.Gain = b.Gain()
		report.Net = b.Net(t.Budget)
	}
	for _, e := range t.Ranked() {
		report.Readings = append(report.Readings, graftReading{
			Method: e.Method, Tied: e.Tied, New: e.New, Duplicate: e.Duplicate,
			Params: e.Params(), Weight: e.Weight(), Covered: e.Covered,
			Before: e.Before, After: e.After, Gain: e.Gain(), Ratio: e.Ratio(),
			Frozen: e.Frozen, Spike: e.Spike / e.LossBefore, Recovered: e.Recovered,
			Share: e.Share(t.Budget), Net: e.Net(t.Budget), Paid: e.Paid(t.Budget),
		})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printGraft(stdout, t, claims)
	}
	if len(t.Blocking()) > 0 || len(claims) > 0 {
		return 1
	}
	if !t.Holds() {
		return 2
	}
	return 0
}

// graftReading is one initialization method as the table carries it.
type graftReading struct {
	Method string `json:"method"`
	Tied   bool   `json:"tied"`

	New       int `json:"new"`
	Duplicate int `json:"duplicate"`

	Params int64 `json:"params"`
	Weight int64 `json:"weight"`

	Covered float64 `json:"covered"`

	// Before and After are tokens per syllable, and Gain is what the graft
	// bought before anything about the rows is taken into account.
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	Gain   float64 `json:"gain"`

	// Ratio is the grafted rows' norm against the existing rows'.
	Ratio  float64 `json:"ratio"`
	Frozen int     `json:"frozen"`

	// Spike is the loss after the switch as a multiple of the loss before it.
	Spike float64 `json:"spike"`

	Recovered int64   `json:"recovered"`
	Share     float64 `json:"share"`

	// Net is Gain less Share, which is the one figure the methods are ordered
	// by, and negative when the loss never came back at all.
	Net  float64 `json:"net"`
	Paid bool    `json:"paid"`
}

type graftReport struct {
	Base   string `json:"base"`
	Budget int64  `json:"budget"`

	Methods  int `json:"methods"`
	Stranded int `json:"stranded"`

	Readings []graftReading `json:"readings"`

	Best string  `json:"best"`
	Gain float64 `json:"gain"`
	Net  float64 `json:"net"`

	MinGain  float64 `json:"min_gain"`
	MaxShare float64 `json:"max_share"`

	Holds bool `json:"holds"`

	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printGraft(w io.Writer, t graft.Trial, claims []string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "method\trows\tadded\ttokens/syllable\tgain\tnorm\tfrozen\tspike\trecovered\tof budget\tnet\n")
	for _, e := range t.Ranked() {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%.2f to %.2f\t%s\t%.2f\t%d\t%.2fx\t%s\t%s\t%s\n",
			e.Method, e.New, weight(e.Weight()), e.Before, e.After, percent(e.Gain()),
			e.Ratio(), e.Frozen, e.Spike/e.LossBefore, recovery(e.Recovered),
			percent(e.Share(t.Budget)), net(e.Net(t.Budget)))
	}
	_ = tw.Flush()

	if b, ok := t.Best(); ok {
		fmt.Fprintf(w, "\n%s, %d tokens at %d wide, measured on %s.\n", b.Base, b.Vocab, b.Dim, b.Box)
	}
	fmt.Fprintf(w, "A graft has to buy %s of fertility to be worth the parameters, and may spend %s of the run getting back to the loss it started at.\n",
		percent(graft.MinGain), percent(graft.MaxShare))
	fmt.Fprint(w, "The fertility columns are free and the recovery columns are what the run pays, which is why the methods are ordered by the difference rather than by the gain.\n")

	why := append(t.Blocking(), claims...)
	if len(why) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(why), "fault"))
		for _, one := range why {
			fmt.Fprintf(w, "  %s\n", one)
		}
		return
	}
	if s := t.Stranded(); len(s) > 0 {
		fmt.Fprintf(w, "%s never came back to the loss it started at, and an expansion that has not recovered has bought fertility and nothing else.\n",
			plural(len(s), "method"))
	}
	fmt.Fprintf(w, "\n%s.\n", t.Verdict())
}

// weight renders what the grafted rows cost in memory, which lands in megabytes
// for one matrix and in gigabytes once the embeddings are untied and large.
func weight(n int64) string {
	if n < 1<<30 {
		return megabytes(n)
	}
	return gigabytes(n)
}

// recovery renders the tokens a method spent getting back, where zero is not a
// small number but a run that never got there.
func recovery(n int64) string {
	if n <= 0 {
		return "never"
	}
	return billions(n)
}

// net renders the figure the methods are ordered by, and a method that never
// recovered has no net rather than a negative one.
func net(f float64) string {
	if f < 0 {
		return "none"
	}
	return percent(f)
}
