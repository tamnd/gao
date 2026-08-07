package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/tamnd/gao/dinh"
)

func runDinh(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dinh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	name := fs.String("name", "gao-pdf-2026-09", "the artifact these pages are going into")
	free := fs.Int64("free", 0, "bytes free on the box that made the renders, if it has less than the window")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao dinh [-name artifact] [-free bytes] [-json] pages.jsonl

Check that page images are still attached to the text that came off them, and
that the box which made them is not filling up.

One JSON object per line, one line per page: the document and the page number
that join the two halves, the route the document took, where the render is and
what it hashed to, how many characters of text the page produced, how much ink
is on it, and whether the image reached the store.

The images are a by-product. A scanned page has to be rendered before an engine
can read it, so keeping the render costs storage and no compute. The pairing is
not a by-product. A page image joined to text off a different page is training
data that teaches a wrong association, it is indistinguishable from a correct
pair once it is written, and no later stage can find it. So a document whose
pages arrive as 1, 2 and 4 is reported rather than renumbered, and a blank page
carrying two thousand characters is refused.

Disk is the other half. gamingpc has 307 GB free and a page at 300 dpi is most
of a megabyte, so the run does not fit on the machine that produces it. The
images stream to the store and the box keeps a window. Whether the drain keeps
up with the write is a rate and gao don measures rates. What is asked here is
whether anything is being left behind at all.

Exits 1 when the pages are not joined to their documents, and 2 when they are
joined but the batch is not one anybody should keep.

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

	b, err := dinh.ReadBatch(*name, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao dinh: %v\n", err)
		return 1
	}

	report := dinhReport{
		Name:        b.Name,
		Documents:   len(b.Documents()),
		Pages:       len(b.Pages),
		Rendered:    b.Rendered(),
		Paired:      b.Paired(),
		Attached:    b.Attached(),
		MinAttached: dinh.MinAttached,
		Blank:       b.Blank(),
		Lost:        b.Lost(),
		Dropped:     b.Dropped(),
		MaxLost:     dinh.MaxLost,
		Routes:      b.Routes(),
		Bytes:       b.Bytes(),
		Stored:      b.Stored(),
		Resident:    b.Resident(),
		Window:      dinh.Free(*free),
		Fits:        b.Fits(*free),
		Gaps:        b.Gaps(),
		Holds:       b.Holds(*free),
		Blocking:    b.Blocking(),
		Verdict:     b.Verdict(*free),
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printDinh(stdout, report)
	}

	switch {
	case len(report.Blocking) > 0:
		return 1
	case !report.Holds:
		return 2
	}
	return 0
}

type dinhReport struct {
	Name      string `json:"name"`
	Documents int    `json:"documents"`
	Pages     int    `json:"pages"`

	Rendered    int     `json:"rendered"`
	Paired      int     `json:"paired"`
	Attached    float64 `json:"attached"`
	MinAttached float64 `json:"min_attached"`

	Blank   int     `json:"blank"`
	Lost    int     `json:"lost"`
	Dropped float64 `json:"dropped"`
	MaxLost float64 `json:"max_lost"`

	Routes []dinh.Route `json:"routes"`

	Bytes    int64 `json:"bytes"`
	Stored   int64 `json:"stored"`
	Resident int64 `json:"resident"`
	Window   int64 `json:"window"`
	Fits     bool  `json:"fits"`

	// Gaps is every document whose page numbers do not run 1 to n exactly once,
	// named rather than summarized, since the fix is per document.
	Gaps []string `json:"gaps,omitempty"`

	Holds    bool     `json:"holds"`
	Blocking []string `json:"blocking,omitempty"`
	Verdict  string   `json:"verdict"`
}

func printDinh(w io.Writer, r dinhReport) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "route\tpages\tshare\trendered\tpairs\tlost\trenders weigh\tcharacters\n")
	for _, route := range r.Routes {
		if route.Pages == 0 {
			continue
		}
		// A route nothing rendered weighs nothing, and printing 0 MB there reads
		// as a measurement rather than as a route that does not produce images.
		weight := "."
		if route.Rendered > 0 {
			weight = disk(route.Bytes)
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%d\t%d\t%s\t%s\n",
			route.Key, route.Pages, percent(route.Share), route.Rendered, route.Paired, route.Lost,
			weight, millions(route.Chars))
	}
	_ = tw.Flush()

	fmt.Fprint(w, "\n")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "pairs\t%d of %d rendered\t%s against a %s line\n",
		r.Paired, r.Rendered, percent(r.Attached), percent(r.MinAttached))
	fmt.Fprintf(tw, "lost\t%d of %d pages\t%s against a %s line\n",
		r.Lost, r.Pages, percent(r.Dropped), percent(r.MaxLost))
	fmt.Fprintf(tw, "blank\t%d of %d pages\ta fact about the documents rather than the pipeline\n",
		r.Blank, r.Pages)
	fmt.Fprintf(tw, "in the store\t%s of %s\tthe copies on the box can go\n",
		disk(r.Stored), disk(r.Bytes))
	fmt.Fprintf(tw, "still on the box\t%s\tagainst a %s window, %s\n",
		disk(r.Resident), disk(r.Window), fits(r.Fits))
	_ = tw.Flush()

	if len(r.Gaps) > 0 {
		fmt.Fprintf(w, "\n%s did not come back whole, and the missing numbers are printed rather than closed up because closing a gap shifts every pair after it:\n",
			count(len(r.Gaps), "document"))
		for _, gap := range r.Gaps {
			fmt.Fprintf(w, "  %s\n", gap)
		}
	}

	// The gaps were printed above with the reason they are not closed up, and
	// printing them a second time here reads as two problems.
	rest := make([]string, 0, len(r.Blocking))
	for _, why := range r.Blocking {
		if !slices.Contains(r.Gaps, why) {
			rest = append(rest, why)
		}
	}
	if len(rest) > 0 {
		fmt.Fprint(w, "\nThese pages are not joined to their documents:\n")
		for _, why := range rest {
			fmt.Fprintf(w, "  %s\n", why)
		}
		return
	}
	if len(r.Blocking) > 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", r.Verdict)
}

// disk writes a size at the unit somebody would say it in, since a batch that is
// four hundred megabytes reads as 0.4 GB and nobody says that.
func disk(n int64) string {
	if n >= 1<<30 {
		return gigabytes(n)
	}
	return megabytes(n)
}

// fits says what the resident figure means, since a number of gigabytes next to
// another number of gigabytes is arithmetic the reader should not have to do.
func fits(ok bool) string {
	if ok {
		return "which the box has room for"
	}
	return "which it does not have room for"
}
