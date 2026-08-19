package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/assign"
	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/harvest"
)

func runAssign(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		assignUsage(stderr)
		return 2
	}
	switch args[0] {
	case "plan":
		return runAssignPlan(stdout, stderr, args[1:], false)
	case "files":
		return runAssignPlan(stdout, stderr, args[1:], true)
	case "read":
		return runAssignRead(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		assignUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao assign: no subcommand named %s\n", args[0])
		assignUsage(stderr)
		return 2
	}
}

func assignUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao assign plan  [-json] readings.jsonl
       gao assign files [-json] readings.jsonl
       gao assign read  -dir DIR

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

run 'gao assign <command> -h' for the flags of one of them.
`)
}

// runAssignRead prints the reading a finished ingest earned.
func runAssignRead(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("assign read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "the ingest directory holding the ledger")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao assign read -dir DIR

Derives this box's reading from the ledger an ingest left in DIR and prints it
as one JSON line, which is what 'gao assign plan' reads.

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

	ledger, err := harvest.OpenLedger(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "gao assign read: %v\n", err)
		return 1
	}
	defer func() { _ = ledger.Close() }()

	reading, err := assign.Measure(ledger.Entries())
	if err != nil {
		fmt.Fprintf(stderr, "gao assign read: %v\n", err)
		return 1
	}
	// One line and not indented, because the whole point is that it is appended
	// to a readings file, and the readings file is JSON lines.
	if err := json.NewEncoder(stdout).Encode(reading); err != nil {
		fmt.Fprintf(stderr, "gao assign read: %v\n", err)
		return 1
	}
	return 0
}

// assignJobReport is one file on one box, which is the assignment itself rather
// than a summary of it.
//
// It carries the name in the form the fetcher takes, source and path joined by
// a slash, because a plan somebody has to retype into another command is a plan
// somebody retypes wrong. 'gao harvest hf -only' reads exactly this.
type assignJobReport struct {
	Source  string  `json:"source"`
	Path    string  `json:"path"`
	Name    string  `json:"name"`
	Bytes   int64   `json:"bytes"`
	Seconds float64 `json:"seconds"`
}

type assignHandReport struct {
	Box     string  `json:"box"`
	Rate    float64 `json:"rate"`
	Files   int     `json:"files"`
	Bytes   int64   `json:"bytes"`
	Seconds float64 `json:"seconds"`

	// Jobs is which files, and it is only filled in by 'assign files'. The plan
	// prices the schedule and the file list is the schedule, so printing 122
	// entries under a command somebody ran to read five summary rows would bury
	// the thing they asked for.
	Jobs []assignJobReport `json:"jobs,omitempty"`
}

type assignGroupReport struct {
	Order    int                `json:"order"`
	Sources  []string           `json:"sources"`
	Files    int                `json:"files"`
	Bytes    int64              `json:"bytes"`
	Makespan float64            `json:"makespan"`
	Idle     float64            `json:"idle"`
	Hands    []assignHandReport `json:"hands"`
}

type assignReport struct {
	Files    int                 `json:"files"`
	Bytes    int64               `json:"bytes"`
	Groups   []assignGroupReport `json:"groups"`
	Seconds  float64             `json:"seconds"`
	Perfect  float64             `json:"perfect"`
	Alone    float64             `json:"alone"`
	Over     float64             `json:"over"`
	Unused   []string            `json:"unused,omitempty"`
	Unplaced []assign.Job        `json:"unplaced,omitempty"`
	Waiting  []string            `json:"waiting,omitempty"`
	Faults   []string            `json:"faults,omitempty"`
	Blocking []string            `json:"blocking,omitempty"`
	Holds    bool                `json:"holds"`
	Verdict  string              `json:"verdict"`

	split assign.Split
}

func runAssignPlan(stdout, stderr io.Writer, args []string, byFile bool) int {
	name := "giao plan"
	if byFile {
		name = "giao files"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	var only *string
	usage := "usage: gao " + name + " [-json] readings.jsonl\n\nflags:\n"
	if byFile {
		only = fs.String("box", "", "print only this box's files, one name per line, which is what 'gao harvest hf -only' reads")
		usage = "usage: gao " + name + " [-json] [-box NAME] readings.jsonl\n\nflags:\n"
	}
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	if only != nil && *only != "" && *asJSON {
		fmt.Fprintf(stderr, "gao %s: -box prints a list for another command to read and -json prints the whole schedule, so asking for both is asking for two different things\n", name)
		return 2
	}

	readings, err := assign.ReadReadings(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao assign: %v\n", err)
		return 1
	}

	split := assign.Divide(readings)
	report := assignReport{
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
		gr := assignGroupReport{
			Order: g.Order, Sources: g.Sources, Files: g.Files, Bytes: g.Bytes,
			Makespan: g.Makespan(), Idle: g.Idle(),
		}
		for _, h := range g.Hands {
			hand := assignHandReport{
				Box: h.Box, Rate: split.Rate(h.Box), Files: len(h.Jobs), Bytes: h.Bytes, Seconds: h.Seconds,
			}
			if byFile {
				rate := split.Rate(h.Box)
				for _, j := range h.Jobs {
					secs := 0.0
					if rate > 0 {
						secs = float64(j.Bytes) / rate
					}
					hand.Jobs = append(hand.Jobs, assignJobReport{
						Source: j.Source, Path: j.Path, Name: workName(j.Source, j.Path),
						Bytes: j.Bytes, Seconds: secs,
					})
				}
			}
			gr.Hands = append(gr.Hands, hand)
		}
		report.Groups = append(report.Groups, gr)
	}

	switch {
	case only != nil && *only != "":
		if code := printAssignBox(stdout, stderr, report, *only); code != 0 {
			return code
		}
	case *asJSON:
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	case byFile:
		printAssignFiles(stdout, report)
	default:
		printAssign(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

func printAssign(w io.Writer, r assignReport) {
	if len(r.Blocking) > 0 {
		printAssignRefusal(w, r)
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "order\tsources\tfiles\tbytes\ttakes\twaiting at the end\n")
	for _, g := range r.Groups {
		fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\t%s\n",
			g.Order, commas(g.Sources), g.Files, assignBytes(g.Bytes), assignHours(g.Makespan), assignHours(g.Idle))
	}
	_ = tw.Flush()

	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "box\tgets through\tfetches\tof the ingest\tscratch left\tbusy for\n")
	for _, box := range r.split.Boxes() {
		bytes := r.split.BytesFor(box)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			box, assignRate(r.split.Rate(box)), assignBytes(bytes), share(bytes, r.Bytes),
			assignBytes(assign.Room(box)), assignHours(assignBusy(r, box)))
	}
	_ = tw.Flush()

	for _, box := range r.Unused {
		b, ok := fleet.Lookup(box)
		if !ok {
			continue
		}
		fmt.Fprintf(w, "\n%s has a reading and %s free, which is %s of scratch once the %s reserve is taken off, against the %s a stage needs.\n",
			box, assignBytes(b.FreeDisk), assignBytes(fleet.Scratch(b)), assignBytes(fleet.ReserveBytes), assignBytes(fleet.MinScratchBytes))
		fmt.Fprintf(w, "So it draws nothing, though a fetch holds %s while it runs and that is the smaller question.\n", assignBytes(assign.InFlight))
	}

	fmt.Fprintf(w, "\nThe whole ingest takes %s, against %s if a file could be cut in half and every source fetched at once.\n",
		assignHours(r.Seconds), assignHours(r.Perfect))
	fmt.Fprintf(w, "On the fastest box alone it takes %s, so the fleet buys %.1fx.\n", assignHours(r.Alone), r.Alone/r.Seconds)

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

// printAssignBox writes one box's files, one name per line and nothing else, in
// the order the schedule hands them out.
//
// Nothing else is the point. This is the one output in gao meant to be read by
// another program rather than by a person, so it carries no header, no totals
// and no verdict, and 'gao harvest hf -only' takes it as it stands. The reasoning
// behind the list is a command away and it is 'gao assign files' without the flag.
//
// A box with no files is not the same as a box that is not in the schedule, and
// they exit differently. The first is a box the split gave nothing to, which is
// already a fault the plan reports. The second is a name that is not on the
// fleet at all, or one under the reserve, and printing an empty list for it
// would start a run that fetches nothing and look like a box that is up to date.
func printAssignBox(stdout, stderr io.Writer, r assignReport, box string) int {
	var found bool
	var lines []string
	for _, g := range r.Groups {
		for _, h := range g.Hands {
			if !strings.EqualFold(h.Box, box) {
				continue
			}
			found = true
			for _, j := range h.Jobs {
				lines = append(lines, j.Name)
			}
		}
	}
	if !found {
		for _, idle := range r.Unused {
			if strings.EqualFold(idle, box) {
				fmt.Fprintf(stderr, "gao assign files: %s draws nothing, so there is no list to hand it: %s\n", box, r.Verdict)
				return 2
			}
		}
		fmt.Fprintf(stderr, "gao assign files: %s is not a box this schedule hands work to, and the boxes it does are %s\n",
			box, strings.Join(r.split.Boxes(), ", "))
		return 2
	}
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	return 0
}

func printAssignFiles(w io.Writer, r assignReport) {
	if len(r.Blocking) > 0 {
		printAssignRefusal(w, r)
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
					g.Order, h.Box, assignBytes(j.Bytes), assignHours(secs), j.Source, j.Path)
			}
		}
	}
	for _, j := range r.Unplaced {
		fmt.Fprintf(tw, "%d\t nobody\t%s\t \t%s/%s\n", j.Order, assignBytes(j.Bytes), j.Source, j.Path)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s over %s, %s in all.\n", assignBytes(r.Bytes), plural(r.Files, "file"), assignHours(r.Seconds))
	fmt.Fprintf(w, "%s\n", r.Verdict)
}

func printAssignRefusal(w io.Writer, r assignReport) {
	fmt.Fprint(w, "This is not a schedule, so nothing was handed to anybody:\n")
	for _, why := range r.Blocking {
		fmt.Fprintf(w, "  %s\n", why)
	}
}

// assignBusy is how long one box spends fetching across every group, which is not
// the same as how long it is booked for, since it waits at every barrier.
func assignBusy(r assignReport, box string) float64 {
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

// assignHours writes a duration at the unit somebody would book it in.
func assignHours(seconds float64) string {
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

// assignBytes writes a transfer size in the decimal units the hosts quote their
// files in, since that is where the manifest's byte counts came from.
func assignBytes(n int64) string {
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

// assignRate writes a throughput both ways, because the megabits figure is what
// says how far under the link the box is running.
func assignRate(rate float64) string {
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
