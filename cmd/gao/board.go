package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/board"
	"github.com/tamnd/gao/pick"
)

func runBoard(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		boardUsage(stderr)
		return 2
	}
	switch args[0] {
	case "board":
		return runBoardTable(stdout, stderr, args[1:], false)
	case "rows":
		return runBoardTable(stdout, stderr, args[1:], true)
	case "help", "-h", "--help":
		boardUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao board: no subcommand named %s\n", args[0])
		boardUsage(stderr)
		return 2
	}
}

func boardUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao board board [-json] [-roster path] scores.jsonl
       gao board rows  [-json] [-roster path] scores.jsonl

The board: the release scores read against pick's roster, with the benchmarks
written in Vietnamese kept apart from the English ones translated into it.

The two arms are never added together, because a model that reads translated
English scores well on the translated arm and that is the register this work
exists to avoid training it to write. The gap between the arms is printed as a
number rather than left for a reader to work out from a table.

board prints the arms, the gap, and what the release note may say. rows prints
a line per benchmark, with the ones gao built itself marked, since a suite where
the builder's own instruments carry the claim has to say so out loud.

Exits 1 when the scores are not a scoreboard, and 2 when they are one that
cannot be published as it stands.

run 'gao board <command> -h' for the flags of one of them.
`)
}

type boardArm struct {
	Origin     string  `json:"origin"`
	Benchmarks int     `json:"benchmarks"`
	Mean       float64 `json:"mean"`
	Margin     float64 `json:"margin"`
	Decided    int     `json:"decided"`
}

type boardReport struct {
	Roster   string     `json:"roster"`
	Arms     []boardArm `json:"arms"`
	Edge     float64    `json:"edge"`
	Own      float64    `json:"own"`
	Others   float64    `json:"others"`
	Missing  []string   `json:"missing,omitempty"`
	Unpinned []string   `json:"unpinned,omitempty"`
	Coin     []string   `json:"coin,omitempty"`
	Faults   []string   `json:"faults,omitempty"`
	Blocking []string   `json:"blocking,omitempty"`
	Holds    bool       `json:"holds"`
	Verdict  string     `json:"verdict"`

	board board.Board
}

func runBoardTable(stdout, stderr io.Writer, args []string, byRow bool) int {
	name := "bang board"
	if byRow {
		name = "bang rows"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	roster := fs.String("roster", "", "read the roster from this file instead of the one in the repository")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: gao %s [-json] [-roster path] scores.jsonl\n\nflags:\n", name)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	ros, err := readRoster(*roster)
	if err != nil {
		fmt.Fprintf(stderr, "gao board: %v\n", err)
		return 1
	}
	scores, err := board.ReadScores(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao board: %v\n", err)
		return 1
	}

	board := board.Read(ros, scores)
	report := boardReport{
		Roster:   board.Roster,
		Edge:     board.Edge(),
		Own:      board.Own,
		Others:   board.Others,
		Missing:  board.Missing,
		Unpinned: board.Unpinned,
		Coin:     board.Coin,
		Faults:   board.Faults(),
		Blocking: board.Blocking(),
		Holds:    board.Holds(),
		Verdict:  board.Verdict(),
		board:    board,
	}
	for _, a := range board.Arms {
		report.Arms = append(report.Arms, boardArm{
			Origin: a.Origin, Benchmarks: len(a.Scores), Mean: a.Mean, Margin: a.Margin, Decided: a.Decided,
		})
	}

	switch {
	case *asJSON:
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	case byRow:
		printBoardRows(stdout, report)
	default:
		printBoard(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

func printBoard(w io.Writer, r boardReport) {
	if len(r.Blocking) > 0 {
		printBoardRefusal(w, r)
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "arm\tbenchmarks\tscores\tagainst the baseline\tdecided\n")
	for _, a := range r.Arms {
		fmt.Fprintf(tw, "%s\t%d\t%.1f\t%s\t%s\n",
			boardArmName(a.Origin), a.Benchmarks, a.Mean, boardMargin(a.Margin), boardDecided(a))
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\nThe gap between the arm written in Vietnamese and the arm translated into it is %.1f points, and the two are not added together.\n", r.Edge)
	fmt.Fprintf(w, "On the benchmarks gao built the model is %s, on everybody else's it is %s.\n",
		boardMargin(r.Own), boardMargin(r.Others))

	if len(r.Unpinned) > 0 {
		fmt.Fprintf(w, "\nScored on a benchmark with no pinned revision, so nobody can take the number again: %s.\n", commas(r.Unpinned))
	}
	if len(r.Coin) > 0 {
		fmt.Fprint(w, "\nInside their own run to run noise:\n")
		for _, row := range r.Coin {
			fmt.Fprintf(w, "  %s\n", row)
		}
	}

	if len(r.Faults) > 0 {
		fmt.Fprint(w, "\nThis board cannot be published as it stands:\n")
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

func printBoardRows(w io.Writer, r boardReport) {
	if len(r.Blocking) > 0 {
		printBoardRefusal(w, r)
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "benchmark\tarm\tbuilt by\tscore\tbaseline\tmargin\truns\tspread\tdecided\n")
	for _, a := range r.board.Arms {
		for _, s := range a.Scores {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%.1f\t%.1f\t%s\t%d\t%.1f\t%s\n",
				s.Benchmark, boardArmName(a.Origin), boardBuiltBy(s.Benchmark), s.Score, s.Baseline,
				boardMargin(s.Margin()), s.Runs, s.Spread, yesno(s.Decided()))
		}
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

func printBoardRefusal(w io.Writer, r boardReport) {
	fmt.Fprint(w, "This is not a scoreboard, so no arm was averaged:\n")
	for _, why := range r.Blocking {
		fmt.Fprintf(w, "  %s\n", why)
	}
}

// boardArmName is what an origin is called in a table, since native and
// translated are the roster's words for it and a reader of a release note
// should not have to learn them.
func boardArmName(origin string) string {
	switch origin {
	case pick.Native:
		return "written in Vietnamese"
	case pick.Translated:
		return "translated into Vietnamese"
	case pick.Neutral:
		return "code, no language"
	}
	return origin
}

// boardBuiltBy says who made the benchmark, which is the column that keeps the
// builder's own instruments visible in the table rather than in a footnote.
func boardBuiltBy(name string) string {
	if board.Built(name) {
		return "gao"
	}
	return "elsewhere"
}

// boardMargin writes a margin the way it is read out loud, because a minus sign
// in a column of small numbers is easy to miss.
func boardMargin(f float64) string {
	switch {
	case f > 0:
		return fmt.Sprintf("%.1f ahead", f)
	case f < 0:
		return fmt.Sprintf("%.1f behind", -f)
	}
	return "level"
}

// boardDecided is how much of an arm sits outside its own noise, which is the
// number that says how much of the arm is evidence.
func boardDecided(a boardArm) string {
	return fmt.Sprintf("%d of %d", a.Decided, a.Benchmarks)
}
