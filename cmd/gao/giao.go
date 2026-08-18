package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/giao"
)

func runGiao(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		giaoUsage(stderr)
		return 2
	}
	switch args[0] {
	case "plan":
		return runGiaoPlan(stdout, stderr, args[1:], false)
	case "files":
		return runGiaoPlan(stdout, stderr, args[1:], true)
	case "read":
		return runGiaoRead(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		giaoUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao giao: no subcommand named %s\n", args[0])
		giaoUsage(stderr)
		return 2
	}
}

func giaoUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao giao plan  [-json] readings.jsonl
       gao giao files [-json] readings.jsonl
       gao giao read  -dir DIR

To hand over: which box fetches which file of the ingest, and what the whole
thing costs once it is split.

The readings are what each box got through end to end on a run that happened,
one JSON object per box, with the bytes, the seconds, the date and how it was
taken. Not a link speed. An ingest that decodes tokenizes what it fetches, and
on this fleet that is the slower half by an order of magnitude.

plan divides every pinned file across the boxes that may hold corpus bytes, one
ingest order at a time, and prints the wall clock against the split that would
be possible if a file could be cut in half and the order did not bind. files
prints the assignment itself, which is what somebody reads before starting a
box.

read derives this box's reading from the ledger of an ingest that ran here and
prints it as the one line to append to the readings file. That is where a
reading is supposed to come from. Four numbers are easy to type from memory and
a typed one looks exactly like a measured one on the page.

Exits 1 when the readings are not a schedule, and 2 when they are one that
should not be run as written.

run 'gao giao <command> -h' for the flags of one of them.
`)
}

// runGiaoRead prints the reading a finished ingest earned.
func runGiaoRead(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("giao read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "the ingest directory holding the ledger")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao giao read -dir DIR

Derives this box's reading from the ledger an ingest left in DIR and prints it
as one JSON line, which is what 'gao giao plan' reads.

The window is between the first finished file and the last, so the first file's
bytes are not counted: the ledger records when a file finished and not when it
started, and the only honest clock in it starts at a finish. A run of one file
carries no reading at all, and neither does one that moved less than a gigabyte
after its first file, since a rate off a hundred megabytes is a measurement of
the minute a link spends deciding how much it likes you.

Exits 1 when the ledger does not carry a reading, with the reason.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	ledger, err := gat.OpenLedger(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "gao giao read: %v\n", err)
		return 1
	}
	defer func() { _ = ledger.Close() }()

	reading, err := giao.Measure(ledger.Entries())
	if err != nil {
		fmt.Fprintf(stderr, "gao giao read: %v\n", err)
		return 1
	}
	// One line and not indented, because the whole point is that it is appended
	// to a readings file, and the readings file is JSON lines.
	if err := json.NewEncoder(stdout).Encode(reading); err != nil {
		fmt.Fprintf(stderr, "gao giao read: %v\n", err)
		return 1
	}
	return 0
}

type giaoHandReport struct {
	Box     string  `json:"box"`
	Rate    float64 `json:"rate"`
	Files   int     `json:"files"`
	Bytes   int64   `json:"bytes"`
	Seconds float64 `json:"seconds"`
}

type giaoGroupReport struct {
	Order    int              `json:"order"`
	Sources  []string         `json:"sources"`
	Files    int              `json:"files"`
	Bytes    int64            `json:"bytes"`
	Makespan float64          `json:"makespan"`
	Idle     float64          `json:"idle"`
	Hands    []giaoHandReport `json:"hands"`
}

type giaoReport struct {
	Files    int               `json:"files"`
	Bytes    int64             `json:"bytes"`
	Groups   []giaoGroupReport `json:"groups"`
	Seconds  float64           `json:"seconds"`
	Perfect  float64           `json:"perfect"`
	Alone    float64           `json:"alone"`
	Over     float64           `json:"over"`
	Unused   []string          `json:"unused,omitempty"`
	Unplaced []giao.Job        `json:"unplaced,omitempty"`
	Waiting  []string          `json:"waiting,omitempty"`
	Faults   []string          `json:"faults,omitempty"`
	Blocking []string          `json:"blocking,omitempty"`
	Holds    bool              `json:"holds"`
	Verdict  string            `json:"verdict"`

	split giao.Split
}

func runGiaoPlan(stdout, stderr io.Writer, args []string, byFile bool) int {
	name := "giao plan"
	if byFile {
		name = "giao files"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: gao %s [-json] readings.jsonl\n\nflags:\n", name)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	readings, err := giao.ReadReadings(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao giao: %v\n", err)
		return 1
	}

	split := giao.Divide(readings)
	report := giaoReport{
		Files:    split.Files,
		Bytes:    split.Bytes,
		Seconds:  split.Seconds(),
		Perfect:  split.Perfect(),
		Alone:    split.Alone(),
		Over:     split.Over(),
		Unused:   split.Idle,
		Unplaced: split.Unplaced,
		Waiting:  split.Waiting(),
		Faults:   split.Faults(),
		Blocking: split.Blocking(),
		Holds:    split.Holds(),
		Verdict:  split.Verdict(),
		split:    split,
	}
	for _, g := range split.Group {
		gr := giaoGroupReport{
			Order: g.Order, Sources: g.Sources, Files: g.Files, Bytes: g.Bytes,
			Makespan: g.Makespan(), Idle: g.Idle(),
		}
		for _, h := range g.Hands {
			gr.Hands = append(gr.Hands, giaoHandReport{
				Box: h.Box, Rate: split.Rate(h.Box), Files: len(h.Jobs), Bytes: h.Bytes, Seconds: h.Seconds,
			})
		}
		report.Groups = append(report.Groups, gr)
	}

	switch {
	case *asJSON:
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	case byFile:
		printGiaoFiles(stdout, report)
	default:
		printGiao(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

func printGiao(w io.Writer, r giaoReport) {
	if len(r.Blocking) > 0 {
		printGiaoRefusal(w, r)
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "order\tsources\tfiles\tbytes\ttakes\twaiting at the end\n")
	for _, g := range r.Groups {
		fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\t%s\n",
			g.Order, commas(g.Sources), g.Files, giaoBytes(g.Bytes), giaoHours(g.Makespan), giaoHours(g.Idle))
	}
	_ = tw.Flush()

	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "box\tgets through\tfetches\tof the ingest\tscratch left\tbusy for\n")
	for _, box := range r.split.Boxes() {
		bytes := r.split.BytesFor(box)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			box, giaoRate(r.split.Rate(box)), giaoBytes(bytes), share(bytes, r.Bytes),
			giaoBytes(giao.Room(box)), giaoHours(giaoBusy(r, box)))
	}
	_ = tw.Flush()

	for _, box := range r.Unused {
		fmt.Fprintf(w, "\n%s has a reading and may not hold corpus bytes, so it draws nothing.\n", box)
	}

	fmt.Fprintf(w, "\nThe whole ingest takes %s, against %s if a file could be cut in half and every source fetched at once.\n",
		giaoHours(r.Seconds), giaoHours(r.Perfect))
	fmt.Fprintf(w, "On the fastest box alone it takes %s, so the fleet buys %.1fx.\n", giaoHours(r.Alone), r.Alone/r.Seconds)

	for _, why := range r.Waiting {
		fmt.Fprintf(w, "%s.\n", upper(why))
	}

	if len(r.Faults) > 0 {
		fmt.Fprint(w, "\nThis is not the schedule to run:\n")
		for _, f := range r.Faults {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

func printGiaoFiles(w io.Writer, r giaoReport) {
	if len(r.Blocking) > 0 {
		printGiaoRefusal(w, r)
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "order\tbox\tbytes\ttakes\tfile\n")
	for _, g := range r.split.Group {
		for _, h := range g.Hands {
			rate := r.split.Rate(h.Box)
			for _, j := range h.Jobs {
				secs := 0.0
				if rate > 0 {
					secs = float64(j.Bytes) / rate
				}
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s/%s\n",
					g.Order, h.Box, giaoBytes(j.Bytes), giaoHours(secs), j.Source, j.Path)
			}
		}
	}
	for _, j := range r.Unplaced {
		fmt.Fprintf(tw, "%d\t nobody\t%s\t \t%s/%s\n", j.Order, giaoBytes(j.Bytes), j.Source, j.Path)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s over %s, %s in all.\n", giaoBytes(r.Bytes), count(r.Files, "file"), giaoHours(r.Seconds))
	fmt.Fprintf(w, "%s\n", r.Verdict)
}

func printGiaoRefusal(w io.Writer, r giaoReport) {
	fmt.Fprint(w, "This is not a schedule, so nothing was handed to anybody:\n")
	for _, why := range r.Blocking {
		fmt.Fprintf(w, "  %s\n", why)
	}
}

// giaoBusy is how long one box spends fetching across every group, which is not
// the same as how long it is booked for, since it waits at every barrier.
func giaoBusy(r giaoReport, box string) float64 {
	var total float64
	for _, g := range r.Groups {
		for _, h := range g.Hands {
			if h.Box == box {
				total += h.Seconds
			}
		}
	}
	return total
}

// giaoHours writes a duration at the unit somebody would book it in.
func giaoHours(seconds float64) string {
	switch {
	case seconds >= 48*3600:
		return fmt.Sprintf("%.1f days", seconds/(24*3600))
	case seconds >= 3600:
		return fmt.Sprintf("%.1f hours", seconds/3600)
	case seconds >= 90:
		return fmt.Sprintf("%.0f minutes", seconds/60)
	case seconds >= 60:
		return "1 minute"
	case seconds >= 2:
		return fmt.Sprintf("%.0f seconds", seconds)
	}
	return fmt.Sprintf("%.1f seconds", seconds)
}

// giaoBytes writes a transfer size in the decimal units the hosts quote their
// files in, since that is where the manifest's byte counts came from.
func giaoBytes(n int64) string {
	switch {
	case n >= 1e12:
		return fmt.Sprintf("%.1f TB", float64(n)/1e12)
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
	return fmt.Sprintf("%d bytes", n)
}

// giaoRate writes a throughput both ways, because the megabits figure is what
// says how far under the link the box is running.
func giaoRate(rate float64) string {
	return fmt.Sprintf("%.1f MB/s (%.0f Mbit)", rate/1e6, rate*8/1e6)
}

// commas joins a list the way a sentence would.
func commas(s []string) string {
	switch len(s) {
	case 0:
		return "nothing"
	case 1:
		return s[0]
	}
	out := s[0]
	for _, x := range s[1 : len(s)-1] {
		out += ", " + x
	}
	return out + " and " + s[len(s)-1]
}

// upper starts a sentence, since the readings are written as clauses so that
// they can also be joined into one.
func upper(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
