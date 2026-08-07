package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/tron"
)

func runTron(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("tron", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	name := fs.String("name", "com-1.0-sft", "the finetuning set these slices compose")
	slate := fs.Bool("slate", false, "print the slate the set is composed against, and what each capability is on it for")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao tron [-name set] [-json] slices.jsonl
       gao tron -slate

Compose the supervised finetuning set without letting the mixing hide where the
examples came from.

One JSON object per line, one line per source per capability per origin: how
many examples and turns, how many a Vietnamese speaker read, how many of those
kept their origin label, whether the slice is held aside for the comparison arm,
and the license class that decides whether anybody else can rebuild the
finetune.

Most Vietnamese instruction data is translated from English, and a model
finetuned on it writes Vietnamese that reads like translated English: fluent,
grammatical, and wrong in a way a native speaker hears in one sentence and a
benchmark does not hear at all. So origin is a column. All three origins are
trained on and the report keeps them apart, because the claim this project makes
is about the native half and a claim about a half needs the half to still be
findable after the mixing.

A native label is a claim about a person, so it gets audited. Under two hundred
examples read, or under 95% of them keeping the label, the slice is reported as
unproven rather than counted as native.

The origin comparison needs two arms that differ in origin and nothing else. The
translated arm is held out of the mixture rather than blended into it, both arms
run at the size of the smaller one, and their capability mixes have to agree
within three points. A capability one side has nothing of is named as excluded,
since a comparison that quietly drops a capability is a comparison of a
different set.

Exits 1 when the slices are not a composed set, and 2 when they are but the two
arms would not measure origin.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *slate {
		if fs.NArg() != 0 {
			fs.Usage()
			return 2
		}
		return printSlate(stdout, stderr, *asJSON)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	s, err := tron.ReadSet(*name, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao tron: %v\n", err)
		return 1
	}

	report := tronReport{
		Name:         s.Name,
		Target:       tron.Target,
		Examples:     s.Examples(),
		Native:       s.Origin(tron.Native),
		Translated:   s.Origin(tron.Translated),
		Made:         s.Origin(tron.Made),
		Composition:  s.Composition(),
		Compared:     s.Compared(),
		Excluded:     s.Excluded(),
		NativeArm:    s.Arm(tron.Native),
		Arm:          s.ArmSize(),
		MinArm:       tron.MinArm,
		Drift:        s.Drift(),
		Drifted:      s.Drifted(),
		MaxDrift:     tron.MaxDrift,
		Matched:      s.Matched(),
		Reproducible: s.Reproducible(),
		Holds:        s.Holds(),
		Blocking:     s.Blocking(),
		Verdict:      s.Verdict(),
	}
	for _, sl := range s.Aside() {
		report.Aside += sl.Examples
	}
	for _, sl := range s.Unproven() {
		report.Unproven = append(report.Unproven, sl.Source)
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printTron(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Matched:
		return 2
	}
	return 0
}

type tronReport struct {
	Name     string `json:"name"`
	Target   int64  `json:"target"`
	Examples int64  `json:"examples"`

	Native     tron.Mix `json:"native"`
	Translated tron.Mix `json:"translated"`
	Made       tron.Mix `json:"synthetic"`

	// Aside is the translated arm, which is composed and trained on and is not
	// in the number above.
	Aside int64 `json:"aside"`

	Composition []tron.Row `json:"composition"`

	Compared []string `json:"compared"`
	Excluded []string `json:"excluded,omitempty"`

	NativeArm int64 `json:"native_arm"`
	Arm       int64 `json:"arm"`
	MinArm    int64 `json:"min_arm"`

	Drift    float64 `json:"drift"`
	Drifted  string  `json:"drifted,omitempty"`
	MaxDrift float64 `json:"max_drift"`
	Matched  bool    `json:"matched"`

	Reproducible float64 `json:"reproducible"`

	// Unproven is every slice whose origin label did not survive its audit, named
	// because a slice that silently stopped counting is the failure this command
	// is about wearing different clothes.
	Unproven []string `json:"unproven,omitempty"`

	Holds    bool     `json:"holds"`
	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printSlate(stdout, stderr io.Writer, asJSON bool) int {
	if asJSON {
		return printJSON(stdout, stderr, tron.Slate)
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "capability\tshare\tnative floor\texamples\ton the slate because it is\n")
	for _, c := range tron.Slate {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			c.Name, percent(c.Share), percent(c.MinNative),
			examples(int64(c.Share*tron.Target)), c.Why)
	}
	_ = tw.Flush()
	fmt.Fprintf(stdout, "\n%s examples over %d capabilities, with the floor on each one set by how much of that capability translation gets wrong rather than by how much of it we have.\n",
		examples(tron.Target), len(tron.Slate))
	return 0
}

func printTron(w io.Writer, r tronReport) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "capability\texamples\tshare\ttarget\tnative\tfloor\tholds\n")
	for _, row := range r.Composition {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Capability, examples(row.Examples), percent(row.Share), percent(row.Target),
			percent(row.NativeShare), percent(row.MinNative), yesno(row.Holds))
	}
	_ = tw.Flush()

	fmt.Fprint(w, "\n")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "origin\tslices\texamples\tturns\tshare of the mixture\n")
	for _, m := range []tron.Mix{r.Native, r.Translated, r.Made} {
		if m.Slices == 0 {
			continue
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n",
			m.Name, m.Slices, examples(m.Examples), examples(m.Turns), percent(m.Share(r.Examples)))
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s translated examples are held aside for the comparison arm rather than poured in, which is the difference between measuring origin later and not being able to.\n",
		examples(r.Aside))
	if len(r.Excluded) > 0 {
		fmt.Fprintf(w, "The comparison runs over %d capabilities and leaves out %s, named here because a comparison that drops a capability quietly is a comparison of a different set.\n",
			len(r.Compared), strings.Join(r.Excluded, ", "))
	}
	if len(r.Unproven) > 0 {
		fmt.Fprintf(w, "%s did not keep their origin label through the audit and stopped counting: %s.\n",
			count(len(r.Unproven), "slice"), strings.Join(r.Unproven, ", "))
	}
	fmt.Fprintf(w, "%s of the mixture could be rebuilt by somebody outside this project, which is what the license classes leave rather than what the model card would like to say.\n",
		percent(r.Reproducible))

	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "\nThis is not a composed set:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}
	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// examples prints a count of examples the way somebody says it out loud.
func examples(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
