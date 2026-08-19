package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/dem"
)

func runDemFertility(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("count fertility", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	roster := fs.Bool("roster", false, "print the candidates and their pins rather than a measurement")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao count fertility [-roster] [-json]
       gao count fertility [-json] fertility.jsonl

Fertility is the multiplier on everything downstream. A tokenizer that spends
1.99 tokens per Vietnamese syllable where another spends 1.50 makes every
training run 33% more expensive and every context window 33% shorter, for the
life of the model, and nothing later undoes it.

With no argument this prints the roster: every candidate, which training path it
belongs to, and whether anybody has pinned the file yet. A candidate nobody has
pinned is a candidate nobody has measured, and the list says so rather than
quietly leaving it out.

Given a log of measurements it folds them onto the roster, ranks what has been
measured by the figure the budget is a function of, prices the spread, and names
what is missing. Two readings of one tokenizer taken on different boxes over the
same text have to agree exactly. That is the cheapest reproducibility check in
this project, and a disagreement is a locale, a normalization, or a tokenizer
file that is not the one that was pinned.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return 2
	}
	if fs.NArg() == 0 || *roster {
		return printCandidates(stdout, stderr, *asJSON)
	}

	readings, err := dem.ReadFertility(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao count: %v\n", err)
		return 1
	}
	s := dem.Fold(readings)

	report := fertilitySlateReport{
		Candidates: len(dem.Candidates()),
		Missing:    s.Missing,
		Spread:     s.Spread(),
		Complete:   s.Complete(),
		Reproduced: s.Reproduced(),
		Faults:     s.Faults,
		Verdict:    s.Verdict(),
	}
	for _, m := range s.Ranked() {
		f, _ := m.Reading()
		row := fertilityRow{
			Tokenizer: m.Candidate.Model.Name, Vocab: m.Candidate.Model.Vocab,
			Path: string(m.Candidate.Path), Boxes: m.Boxes,
			PerToken: f.PerToken(), PerSyllable: f.PerSyllable(),
		}
		if holds, applies := m.Candidate.Predicts.Holds(f); applies {
			row.Predicts, row.Held = m.Candidate.Predicts.ID, holds
		}
		report.Measured = append(report.Measured, row)
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printFertility(stdout, s)
	}
	if s.Complete() && s.Reproduced() {
		return 0
	}
	return 1
}

type fertilityRow struct {
	Tokenizer   string   `json:"tokenizer"`
	Vocab       int      `json:"vocab"`
	Path        string   `json:"path"`
	Boxes       []string `json:"boxes"`
	PerToken    float64  `json:"chars_per_token"`
	PerSyllable float64  `json:"tokens_per_syllable"`

	// Predicts names the prediction standing against this candidate and Held
	// says whether the measurement fell inside it, which is a different fact
	// from the measurement and is the reason the register exists.
	Predicts string `json:"predicts,omitempty"`
	Held     bool   `json:"held,omitempty"`
}

type fertilitySlateReport struct {
	Candidates int            `json:"candidates"`
	Measured   []fertilityRow `json:"measured,omitempty"`
	Missing    []string       `json:"missing,omitempty"`

	// Spread is what the worst measured candidate costs against the best, as a
	// multiplier on every training run this project will ever pay for.
	Spread float64 `json:"spread"`

	Complete   bool     `json:"complete"`
	Reproduced bool     `json:"reproduced"`
	Faults     []string `json:"faults,omitempty"`
	Verdict    string   `json:"verdict"`
}

func printCandidates(stdout, stderr io.Writer, asJSON bool) int {
	got := dem.Candidates()
	if asJSON {
		report := make([]fertilityCandidate, 0, len(got))
		for _, c := range got {
			report = append(report, fertilityCandidate{
				Tokenizer: c.Model.Name, Vocab: c.Model.Vocab, Path: string(c.Path),
				Pinned: c.Pinned(), Digest: c.Model.Digest, Origin: c.Model.Origin,
				Reported: c.Reported, Predicts: c.Predicts.ID, Why: c.Why,
			})
		}
		return printJSON(stdout, stderr, report)
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "tokenizer\tvocab\tpath\tpinned\treported\n")
	for _, c := range got {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", c.Model.Name, c.Model.Vocab, c.Path, pinnedYet(c.Pinned()), reported(c.Reported))
	}
	_ = tw.Flush()

	var pinned int
	for _, c := range got {
		if c.Pinned() {
			pinned++
		}
	}
	is := "is"
	if pinned != 1 {
		is = "are"
	}
	fmt.Fprintf(stdout, "\n%d of %d candidates %s pinned, and only a pinned file can be measured, because a fertility number taken on whatever was installed on the box is a number nobody can reproduce.\n",
		pinned, len(got), is)
	fmt.Fprint(stdout, "The reported column is what somebody else has published on Vietnamese. It is where to start rather than an answer, and replacing it with a figure taken on gao text is the whole of the item.\n")
	return 0
}

type fertilityCandidate struct {
	Tokenizer string  `json:"tokenizer"`
	Vocab     int     `json:"vocab"`
	Path      string  `json:"path"`
	Pinned    bool    `json:"pinned"`
	Digest    string  `json:"digest,omitempty"`
	Origin    string  `json:"origin,omitempty"`
	Reported  float64 `json:"reported_chars_per_token,omitempty"`
	Predicts  string  `json:"predicts,omitempty"`
	Why       string  `json:"why"`
}

func printFertility(w io.Writer, s dem.Slate) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "tokenizer\tchars/token\ttokens/syllable\tcost\tboxes\n")
	ranked := s.Ranked()
	var best float64
	if len(ranked) > 0 {
		f, _ := ranked[0].Reading()
		best = f.PerSyllable()
	}
	for _, m := range ranked {
		f, _ := m.Reading()
		cost := "the floor"
		if best > 0 && f.PerSyllable() > best {
			cost = fmt.Sprintf("%+.0f%%", (f.PerSyllable()/best-1)*100)
		}
		fmt.Fprintf(tw, "%s\t%.2f\t%.2f\t%s\t%s\n", m.Candidate.Model.Name, f.PerToken(), f.PerSyllable(), cost, strings.Join(m.Boxes, " "))
	}
	_ = tw.Flush()

	var judged int
	for _, m := range ranked {
		f, _ := m.Reading()
		holds, applies := m.Candidate.Predicts.Holds(f)
		if !applies {
			continue
		}
		if judged == 0 {
			fmt.Fprint(w, "\nAgainst what was written down before any of this was measured:\n")
		}
		judged++
		held := "did not hold"
		if holds {
			held = "held"
		}
		fmt.Fprintf(w, "  %s %s on %s\n", m.Candidate.Predicts.ID, held, m.Candidate.Model.Name)
	}

	if len(s.Missing) > 0 {
		fmt.Fprintf(w, "\nNot measured: %s.\n", strings.Join(s.Missing, ", "))
	}
	fmt.Fprintf(w, "\n%s\n", s.Verdict())
	if len(s.Faults) > 1 {
		for _, fault := range s.Faults[1:] {
			fmt.Fprintf(w, "  and %s\n", fault)
		}
	}
}

func pinnedYet(b bool) string {
	if b {
		return "yes"
	}
	return "not yet"
}

func reported(f float64) string {
	if f == 0 {
		return "nobody has"
	}
	return fmt.Sprintf("%.2f", f)
}
