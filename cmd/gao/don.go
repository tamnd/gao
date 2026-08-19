package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/tamnd/gao/don"
	"github.com/tamnd/gao/may"
)

func runDon(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		donUsage(stderr)
		return 2
	}
	switch args[0] {
	case "fit":
		return runDonFit(stdout, stderr, args[1:])
	case "read":
		return runDonRead(stdout, stderr, args[1:])
	case "help":
		donUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao clear: no subcommand named %s\n\n", args[0])
		donUsage(stderr)
		return 2
	}
}

func donUsage(w io.Writer) {
	fmt.Fprint(w, `usage: gao clear fit  [-uplink BYTES] [-fetches N] [-confirm DURATION] [-json]
       gao clear read rotation.jsonl [-json]

Clear away: whether the bytes leave the box faster than they arrive on it.

The corpus does not fit on the fleet, so every stage writes a file, pushes it
off-box, and deletes it. That is settled. What saying so does not settle is
whether the pushing keeps up with the writing, and if it does not then every
other decision in this project is downstream of a disk that filled at three in
the morning with nobody watching.

fit is the arithmetic. It answers three questions that get confused with each
other: whether the uplink moves bytes faster than the crawl writes them, how
much sits on disk in steady state, and how long the store can be unreachable
before fetching has to stop. The second one is where capacity plans go wrong,
because a file that has been uploaded is not a file that may be deleted. Between
the upload finishing and the store confirming it holds those exact bytes there
is a window, and everything written during that window is on the disk and cannot
be reclaimed.

read is the evidence. A crawl either deleted only what the store had confirmed
or it did not, and afterwards the two are indistinguishable from the disk,
because in both cases the file is gone. The log is the only place the difference
survives, so read folds it up and names every file that skipped a step.

flags:
`)
}

func runDonFit(stdout, stderr io.Writer, args []string) int {
	r := don.Target()

	fs := flag.NewFlagSet("clear fit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	box := fs.String("box", r.Box.Name, "the box the rotation runs on")
	uplink := fs.Int64("uplink", r.Uplink, "bytes per second off the box")
	fetches := fs.Float64("fetches", r.Fetches, "fetches per second")
	record := fs.Int64("record", r.Record, "mean bytes one fetch adds to the WARC")
	volume := fs.Int64("volume", r.Volume, "bytes a WARC takes before it is closed and can be pushed")
	confirm := fs.Duration("confirm", r.Confirm, "how long after an upload the store confirms it holds the bytes")
	fs.Usage = func() { donUsage(stderr); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	b, ok := may.Lookup(*box)
	if !ok {
		fmt.Fprintf(stderr, "gao clear: %s is not on the fleet inventory\n", *box)
		return 2
	}
	r.Box, r.Uplink, r.Fetches, r.Record, r.Volume, r.Confirm = b, *uplink, *fetches, *record, *volume, *confirm

	report := donFitReport{
		Box:      r.Box.Name,
		Fill:     int64(r.Fill()),
		Uplink:   r.Uplink,
		Scratch:  r.Scratch(),
		Mark:     r.Mark(),
		Held:     r.Held(),
		Flight:   r.Flight(),
		Rotate:   seconds(r.Rotate()),
		Push:     seconds(r.Push()),
		Outage:   seconds(r.Outage()),
		Fits:     r.Fits(),
		Blocking: r.Blocking(),
		Verdict:  r.Verdict(),
	}
	if full, ok := r.Full(); ok {
		report.Full = seconds(full)
		report.Fills = true
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printDonFit(stdout, r)
	}
	if r.Fits() {
		return 0
	}
	return 1
}

// donFitReport is the arithmetic in a form something else can read. Durations
// are seconds rather than Go duration strings, because the reader on the other
// end is as likely to be a dashboard as a person.
type donFitReport struct {
	Box     string `json:"box"`
	Fill    int64  `json:"fill_bytes_per_second"`
	Uplink  int64  `json:"uplink_bytes_per_second"`
	Scratch int64  `json:"scratch_bytes"`
	Mark    int64  `json:"mark_bytes"`
	Held    int64  `json:"held_bytes"`
	Flight  int    `json:"volumes_in_flight"`
	Rotate  int64  `json:"rotate_seconds"`
	Push    int64  `json:"push_seconds"`
	Outage  int64  `json:"outage_seconds"`

	// Full is how long until the disk reaches the mark, and Fills says whether
	// there is such a time. They are two fields because a zero here would
	// otherwise read as immediately rather than as never.
	Full  int64 `json:"full_seconds,omitempty"`
	Fills bool  `json:"fills"`

	Fits     bool     `json:"fits"`
	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printDonFit(w io.Writer, r don.Rotation) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "box\t%s, %s free, %s reserved\n", r.Box.Name, may.GB(r.Box.FreeDisk), may.GB(may.ReserveBytes))
	fmt.Fprintf(tw, "scratch\t%s, and the crawl stops fetching at %s\n", may.Size(r.Scratch()), may.Size(r.Mark()))
	fmt.Fprintf(tw, "fill\t%s per second, at %.0f fetches of %s\n", may.Size(int64(r.Fill())), r.Fetches, may.Size(r.Record))
	fmt.Fprintf(tw, "uplink\t%s per second\n", may.Size(r.Uplink))
	fmt.Fprintf(tw, "volume\t%s, closing every %s and pushing in %s\n", may.Size(r.Volume), span(r.Rotate()), span(r.Push()))
	fmt.Fprintf(tw, "confirm\t%s, during which nothing may be deleted\n", span(r.Confirm))
	fmt.Fprintf(tw, "held\t%s, which is the open volume and %d in flight\n", may.Size(r.Held()), r.Flight())
	fmt.Fprintf(tw, "outage\t%s of store outage before fetching has to stop\n", span(r.Outage()))
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%s\n", r.Verdict())
	if reasons := r.Blocking(); len(reasons) > 1 {
		for _, reason := range reasons[1:] {
			fmt.Fprintf(w, "  and %s\n", reason)
		}
	}
}

func runDonRead(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("clear read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	files := fs.Bool("files", false, "print every file rather than the totals")
	fs.Usage = func() { donUsage(stderr); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	events, err := don.ReadLog(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao clear: %v\n", err)
		return 1
	}
	l := don.Read(events)

	report := donReadReport{
		Files:     len(l.Files),
		Bytes:     l.Bytes(),
		OnDisk:    l.OnDisk(),
		Unsafe:    l.Unsafe(),
		Reclaimed: l.Reclaimed(),
		Sound:     l.Sound(),
		Faults:    l.Faults,
		Verdict:   l.Verdict(),
	}
	if oldest, ok := l.Oldest(); ok {
		report.Oldest = oldest.Name
		report.OldestHeld = seconds(oldest.Held())
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printDonRead(stdout, l, *files)
	}
	if l.Sound() {
		return 0
	}
	return 1
}

type donReadReport struct {
	Files     int   `json:"files"`
	Bytes     int64 `json:"bytes"`
	OnDisk    int64 `json:"on_disk_bytes"`
	Unsafe    int64 `json:"unsafe_bytes"`
	Reclaimed int64 `json:"reclaimed_bytes"`

	// Oldest is the file that has been on the box longest without being
	// reclaimed, which is where a rotation that has quietly stopped shows up
	// first.
	Oldest     string `json:"oldest,omitempty"`
	OldestHeld int64  `json:"oldest_held_seconds,omitempty"`

	Sound   bool     `json:"sound"`
	Faults  []string `json:"faults,omitempty"`
	Verdict string   `json:"verdict"`
}

func printDonRead(w io.Writer, l don.Ledger, files bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "state\tfiles\tbytes\n")
	for _, s := range []don.State{don.Resident, don.Pushed, don.Verified, don.Reclaimed} {
		fmt.Fprintf(tw, "%s\t%d\t%s\n", s, l.Count(s), may.Size(bytesAt(l, s)))
	}
	fmt.Fprintf(tw, "\nall\t%d\t%s\n", len(l.Files), may.Size(l.Bytes()))
	_ = tw.Flush()

	if files {
		fmt.Fprint(w, "\n")
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprint(tw, "file\tstate\tbytes\theld\tpath\n")
		for _, f := range l.Files {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", f.Name, f.Reached, may.Size(f.Bytes), span(f.Held()), f.Path)
		}
		_ = tw.Flush()
	}

	if oldest, ok := l.Oldest(); ok {
		fmt.Fprintf(w, "\nThe oldest thing still on the box is %s, %s in, at %s.\n",
			oldest.Name, span(oldest.Held()), oldest.Reached)
	}
	fmt.Fprintf(w, "\n%s\n", l.Verdict())
	if len(l.Faults) > 1 {
		for _, fault := range l.Faults[1:] {
			fmt.Fprintf(w, "  and %s\n", fault)
		}
	}
}

func bytesAt(l don.Ledger, s don.State) int64 {
	var n int64
	for _, f := range l.Files {
		if f.Reached == s {
			n += f.Bytes
		}
	}
	return n
}

// span writes a duration for a person reading a capacity plan, which is never
// in nanoseconds.
func span(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	case d >= 90*time.Minute:
		return fmt.Sprintf("%.1f hours", d.Hours())
	case d >= 90*time.Second:
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	default:
		return fmt.Sprintf("%.0f seconds", d.Seconds())
	}
}

func seconds(d time.Duration) int64 { return int64(d / time.Second) }
