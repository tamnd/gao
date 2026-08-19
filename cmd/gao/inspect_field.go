package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/inspect"
)

func runInspectField(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("inspect field", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	pages := fs.Int64("pages", inspect.Slice, "how many pages the plan expects to reach OCR, which is what turns a rate into a cost")
	box := fs.String("box", "gamingpc", "the box whose accelerator the field was evaluated on")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao inspect field [-pages N] [-box name] [-json] engines.jsonl

Read a field of candidate OCR engines, losers included.

A gate on one engine says whether that engine works. It does not say the field
was searched, and the milestone asks for the field, since a table with one row
in it cannot be argued with.

Three things make a published comparison reproducible and all three are usually
missing. The engines have to have read the same pages, since a diacritic error
rate off one set and one off another are two numbers rather than a difference.
The gap between the top two has to be larger than what the set can resolve, and
two hundred pages hold about a hundred thousand marks, which places a rate to
within a tenth of a point. And the batch size and the memory it held have to be
recorded against the card, since a result at batch 64 holding 23.6 GB of a 24 GB
card is a result that fails the first time anything else touches the GPU.

The cost line is the other half of the S4 gate, which asks the winning path to
sustain its throughput across a full batch at a rate that finishes the slice in
the time the plan allows. That is arithmetic over pages a second, and the page
count is a plan estimate until the routing distribution is measured, so -pages
is where a measured one goes.

Exits 1 if the field is not a comparison, or 2 if no engine clears the gate,
reads the slice inside the budget, and does it with no Vietnamese finetune.

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

	b, ok := fleet.Lookup(*box)
	if !ok || !b.HasGPU() {
		fmt.Fprintf(stderr, "gao inspect: %s is not a box on the fleet with an accelerator in it, and the one that has one is gamingpc\n", *box)
		return 2
	}

	f, err := inspect.ReadField(b.GPUMemory, *pages, inspect.S4, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao inspect: %v\n", err)
		return 1
	}

	report := inspectFieldReport{
		Card: b.GPU, CardBytes: b.GPUMemory, Pages: f.Pages, Gate: f.Gate,
		Engines: len(f.Candidates), Losers: len(f.Losers()),
		Separated: f.Separated(), Passed: f.Passed(),
		Affordable: f.Affordable(), Holds: f.Holds(),
		Budget: inspect.Budget(), Blocking: f.Blocking(), Verdict: f.Verdict(),
	}
	if l, ok := f.Leads(); ok {
		report.Leader = l.Engine
	}
	if w, ok := f.Winner(); ok {
		report.Winner = w.Engine
	}
	for _, c := range f.Ranked() {
		report.Results = append(report.Results, inspectResult{
			Engine: c.Engine, Version: c.Version, Finetuned: c.Finetuned,
			DER: c.Score.DER(), CER: c.Score.CER(),
			ToneDeletion: c.Score.ToneDeletionRate(), StdErr: c.StdErr(),
			Batch: c.Batch, VRAM: c.VRAM, Headroom: c.Headroom(b.GPUMemory),
			Rate: c.Rate, GPUHours: c.Cost(f.Pages),
			Fails: f.Gate.Check(c.Score),
		})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printInspectField(stdout, f, b)
	}
	if len(f.Blocking()) > 0 {
		return 1
	}
	if !f.Passed() || !f.Affordable() || !f.Holds() {
		return 2
	}
	return 0
}

// inspectResult is one engine as the table carries it, which is rates and the card
// they were measured on rather than the counts underneath.
type inspectResult struct {
	Engine    string `json:"engine"`
	Version   string `json:"version"`
	Finetuned bool   `json:"finetuned"`

	DER          float64 `json:"der"`
	CER          float64 `json:"cer"`
	ToneDeletion float64 `json:"tone_deletion"`

	// StdErr is how precisely this set places this engine, which is what says
	// whether the row above it is a better engine or a better draw.
	StdErr float64 `json:"stderr"`

	Batch    int     `json:"batch"`
	VRAM     int64   `json:"vram"`
	Headroom float64 `json:"headroom"`
	Rate     float64 `json:"rate"`
	GPUHours float64 `json:"gpu_hours"`

	Fails []string `json:"fails,omitempty"`
}

type inspectFieldReport struct {
	Card      string       `json:"card"`
	CardBytes int64        `json:"card_bytes"`
	Pages     int64        `json:"pages"`
	Gate      inspect.Gate `json:"gate"`

	Engines int             `json:"engines"`
	Losers  int             `json:"losers"`
	Results []inspectResult `json:"results"`

	// Leader reads best and Winner is the path that ships, which are the same
	// engine only when the best reading also fits the hours OCR has.
	Leader    string `json:"leader,omitempty"`
	Winner    string `json:"winner,omitempty"`
	Separated bool   `json:"separated"`

	Passed     bool    `json:"passed"`
	Affordable bool    `json:"affordable"`
	Holds      bool    `json:"holds"`
	Budget     float64 `json:"budget"`

	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printInspectField(w io.Writer, f inspect.Field, b fleet.Box) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "engine\tder\tcer\ttone\tbatch\tvram\tfree\trate\thours\tgate\n")
	for _, c := range f.Ranked() {
		verdict := "pass"
		if fails := f.Gate.Check(c.Score); len(fails) > 0 {
			verdict = fmt.Sprintf("fails %s", plural(len(fails), "line"))
		}
		name := c.Engine
		if c.Finetuned {
			name += " (finetuned)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%.1f GB\t%.0f%%\t%.1f/s\t%.0f\t%s\n",
			name, percent(c.Score.DER()), percent(c.Score.CER()), percent(c.Score.ToneDeletionRate()),
			c.Batch, float64(c.VRAM)/(1<<30), 100*c.Headroom(f.Card), c.Rate, c.Cost(f.Pages), verdict)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s on %s, %s, against a %s diacritic gate.\n",
		plural(len(f.Candidates), "candidate engine"), b.Name, b.GPU, percent(f.Gate.DER))
	fmt.Fprintf(w, "%s did not clear it, and they are in the table because a comparison without them is an announcement.\n",
		plural(len(f.Losers()), "engine"))
	fmt.Fprintf(w, "Hours are for the %.1fM pages the plan routes to OCR, against the %.0f OCR has of the extraction stage's %.0f.\n",
		float64(f.Pages)/1e6, inspect.Budget(), inspect.Hours)

	if why := f.Blocking(); len(why) > 0 {
		fmt.Fprintf(w, "\n%s:\n", plural(len(why), "fault"))
		for _, one := range why {
			fmt.Fprintf(w, "  %s\n", one)
		}
		return
	}
	fmt.Fprintf(w, "\n%s.\n", f.Verdict())
}
