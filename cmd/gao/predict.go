package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/predict"
)

func runPredict(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("predict", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	results := fs.String("results", "", "a JSONL file of measurements to put on the register")
	only := fs.String("slice", "", "print the predictions of one slice, S0 through S9")
	all := fs.Bool("all", false, "print every prediction rather than the summary")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao predict [-slice S1] [-all] [-results results.jsonl] [-json]

Print the predictions register: what this project said would happen, written
down before any of it happened.

With -results, each line is one measurement: the identifier, the claim it was
scored against, whether it landed inside that claim, what came back, and what
measured it on which box. A result whose claim is not the one the register holds
is refused rather than applied, because the alternative is a register where a
one word edit turns a miss into a hit.

Exits 1 when the register cannot be published as one, and 2 when the rate says
more about how the predictions were written than about how the work went.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	r := predict.Published()
	var refused []string
	if *results != "" {
		got, err := predict.ReadResults(*results)
		if err != nil {
			fmt.Fprintf(stderr, "gao predict: %v\n", err)
			return 1
		}
		r, refused = r.Apply(got)
	}
	for _, p := range r.Predictions {
		if p.Box == "" {
			continue
		}
		if _, ok := fleet.Lookup(p.Box); !ok {
			refused = append(refused, fmt.Sprintf("%s was measured on %q, which is not a box in the fleet", p.ID, p.Box))
		}
	}
	sort.Strings(refused)

	report := predictReport{
		Name:         r.Name,
		Digest:       r.Digest().String(),
		Predictions:  len(r.Predictions),
		Open:         len(r.Waiting()),
		Landed:       len(r.Hits()),
		Missed:       len(r.Misses()),
		Withdrawn:    len(r.Pulled()),
		Resolved:     r.Resolved(),
		Rate:         r.Rate(),
		Quorum:       predict.Quorum,
		MinRate:      predict.MinRate,
		MaxRate:      predict.MaxRate,
		MaxWithdrawn: predict.MaxWithdrawn,
		Settled:      r.Settled(),
		Slices:       r.Slices(),
		Refused:      refused,
		Holds:        r.Holds(),
		Blocking:     r.Blocking(),
		Verdict:      r.Verdict(),
	}
	for _, p := range r.Predictions {
		switch {
		case *only != "" && !strings.EqualFold(p.Slice, *only):
		case *only != "" || *all:
			report.Register = append(report.Register, p)
		}
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printPredict(stdout, report, r)
	}

	switch {
	case len(report.Blocking) > 0 || len(refused) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

type predictReport struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`

	Predictions int `json:"predictions"`
	Open        int `json:"open"`
	Landed      int `json:"landed"`
	Missed      int `json:"missed"`
	Withdrawn   int `json:"withdrawn"`
	Resolved    int `json:"resolved"`

	// Rate is hits over resolved, and Settled says whether enough of the
	// register has resolved for it to mean anything. The two are published
	// together because the rate on its own is quotable in either direction.
	Rate    float64 `json:"rate"`
	Settled bool    `json:"settled"`

	Quorum       float64 `json:"quorum"`
	MinRate      float64 `json:"min_rate"`
	MaxRate      float64 `json:"max_rate"`
	MaxWithdrawn float64 `json:"max_withdrawn"`

	Slices   []predict.Row        `json:"slices"`
	Register []predict.Prediction `json:"register,omitempty"`

	// Refused is the measurements the register would not take, which is a
	// different list from Blocking: the register is fine and the results are
	// not.
	Refused []string `json:"refused,omitempty"`

	Holds    bool     `json:"holds"`
	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printPredict(w io.Writer, r predictReport, reg predict.Register) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "slice\ttitle\tpredictions\topen\tright\twrong\tpulled\trate\n")
	for _, row := range r.Slices {
		rate := "."
		if row.Resolved() > 0 {
			rate = percent(row.Rate)
		}
		predictions := "."
		if row.Count > 0 {
			predictions = fmt.Sprintf("%d", row.Count)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n",
			row.Slice, row.Title, predictions, row.Open, row.Landed, row.Missed, row.Withdrawn, rate)
	}
	_ = tw.Flush()

	if len(r.Register) > 0 {
		fmt.Fprint(w, "\n")
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprint(tw, "id\tslice\tstate\tclaim\treading\n")
		for _, p := range r.Register {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.ID, p.Slice, p.State, p.Claim, blank(p.Reading))
		}
		_ = tw.Flush()
	}

	// The misses print in full whatever else was asked for, since a register
	// that reports its hits and counts its misses is a scoreboard.
	if misses := reg.Misses(); len(misses) > 0 {
		fmt.Fprintf(w, "\n%s came back wrong:\n", plural(len(misses), "prediction"))
		for _, p := range misses {
			fmt.Fprintf(w, "  %s: %s\n    %s, measured by %s on %s\n", p.ID, p.Claim, p.Reading, p.By, p.Box)
		}
	}
	if pulled := reg.Pulled(); len(pulled) > 0 {
		fmt.Fprintf(w, "\n%s was withdrawn:\n", plural(len(pulled), "prediction"))
		for _, p := range pulled {
			fmt.Fprintf(w, "  %s: %s\n    %s\n", p.ID, p.Claim, p.Why)
		}
	}

	if len(r.Refused) > 0 {
		fmt.Fprint(w, "\nThese measurements did not go on the register:\n")
		for _, why := range r.Refused {
			fmt.Fprintf(w, "  %s\n", why)
		}
	}

	if len(r.Blocking) > 0 {
		fmt.Fprint(w, "\nThis is not a register:\n")
		for _, why := range r.Blocking {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}
	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// blank prints a dot where a column has nothing in it, so that an empty cell
// reads as nothing rather than as something that failed to print.
func blank(s string) string {
	if s == "" {
		return "."
	}
	return s
}
