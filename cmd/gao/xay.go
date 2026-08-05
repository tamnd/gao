package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/tamnd/gao/kho"
	"github.com/tamnd/gao/xay"
)

func runXay(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("xay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	threshold := fs.Float64("threshold", xay.DefaultThreshold, "the similarity at which two documents are copies of each other")
	curve := fs.Bool("curve", false, "print what every threshold would retain instead of what one of them does")
	boiler := fs.Bool("boiler", false, "report the boilerplate each host repeats on its pages instead of deduplicating documents")
	hosts := fs.Int("hosts", 20, "with -boiler, how many hosts to print")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao xay [-threshold t] [-curve] [-json] file...
       gao xay -boiler [-hosts n] [-json] part...

Find the documents a corpus holds more than one copy of. A file is either a
parquet part written by the ingest, in which case every row in it is a document,
or a text file, in which case the file is one document.

Exact copies are found by identity and near copies by minhash over character
five-grams, so a syndicated article that a second site gave its own headline is
one document rather than two.

With -curve it prints what each threshold would retain rather than what one of
them does, which is the measurement the deduplication threshold is chosen from.
The curve is built at a wider banding than the pipeline runs at, because a pair
that was never proposed as a candidate cannot be scored at any threshold.

With -boiler it does the other half of the job, the half document identity cannot
see: the nav column, the share prompt and the copyright notice that every page of
a site carries. None of those pages is a copy of another one, so deduplication
keeps all of them and the notice arrives once per page. It reads the parts twice,
once to count what each host repeats and once to take it out, and it takes parts
rather than text files because a text file carries no host to be aware of.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *threshold < 0 || *threshold > 1 {
		fmt.Fprintf(stderr, "gao xay: a threshold is a similarity between 0 and 1, not %v\n", *threshold)
		return 2
	}
	if *boiler && *curve {
		fmt.Fprintln(stderr, "gao xay: -boiler and -curve are two different measurements. Run one of them")
		return 2
	}
	if *hosts < 1 {
		fmt.Fprintf(stderr, "gao xay: -hosts is how many hosts to print, so it is at least 1, not %d\n", *hosts)
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(stderr, "gao xay: nothing to read. Give it parquet parts or text files")
		return 2
	}
	if *boiler {
		return runBoiler(stdout, stderr, files, *hosts, *asJSON)
	}

	banding := xay.Default()
	if *curve {
		banding = xay.Wide()
	}
	index, err := xay.New(banding)
	if err != nil {
		fmt.Fprintf(stderr, "gao xay: %v\n", err)
		return 1
	}
	for _, name := range files {
		if err := readInto(index, name); err != nil {
			fmt.Fprintf(stderr, "gao xay: %v\n", err)
			return 1
		}
	}

	if *curve {
		reports := index.Curve(xay.CurveThresholds...)
		if *asJSON {
			return printJSON(stdout, stderr, xayCurve{Banding: bandingOf(banding), Curve: reports})
		}
		printCurve(stdout, reports)
		return 0
	}

	report := index.Cluster(*threshold)
	if *asJSON {
		return printJSON(stdout, stderr, xayRun{Banding: bandingOf(banding), Report: report})
	}
	printDedup(stdout, banding, report)
	return 0
}

// runBoiler counts what each host repeats and then reports what taking it out
// would cost.
//
// It reports rather than writes. Every threshold in this pipeline is a default
// until an ablation moves it, and the three that decide what furniture is are no
// different, so the first thing this has to be able to do is show a person what
// the current three would remove from a real host.
func runBoiler(stdout, stderr io.Writer, files []string, hosts int, asJSON bool) int {
	for _, name := range files {
		if filepath.Ext(name) != ".parquet" {
			fmt.Fprintf(stderr, "gao xay: %s is not a part, and boilerplate is found per host, which a text file does not carry\n", name)
			return 2
		}
	}

	b := xay.NewBoiler(xay.DefaultFurniture())
	for _, name := range files {
		if err := kho.ScanPart(name, func(r kho.Row) error {
			b.Count(r.Host, r.Text)
			return nil
		}); err != nil {
			fmt.Fprintf(stderr, "gao xay: %v\n", err)
			return 1
		}
	}

	var run boilerRun
	for _, name := range files {
		if err := kho.ScanPart(name, func(r kho.Row) error {
			s := b.Strip(r.Host, r.Text)
			run.Documents++
			run.Lines += s.Lines
			run.Removed += s.Removed
			if s.Emptied {
				run.Emptied++
			}
			return nil
		}); err != nil {
			fmt.Fprintf(stderr, "gao xay: %v\n", err)
			return 1
		}
	}
	run.Hosts = b.Hosts()
	run.Furniture = xay.DefaultFurniture()

	reports := b.Reports()
	if asJSON {
		run.Sites = reports
		return printJSON(stdout, stderr, run)
	}
	printBoiler(stdout, run, reports, hosts)
	return 0
}

// boilerRun is what one pass over a set of parts did, over all the hosts in
// them.
type boilerRun struct {
	Furniture xay.Furniture    `json:"furniture"`
	Hosts     int              `json:"hosts"`
	Documents int              `json:"documents"`
	Lines     int              `json:"lines"`
	Removed   int              `json:"removed"`
	Emptied   int              `json:"emptied"`
	Sites     []xay.HostReport `json:"sites,omitempty"`
}

// LineShare is the share of the lines that came out.
func (r boilerRun) LineShare() float64 {
	if r.Lines == 0 {
		return 0
	}
	return float64(r.Removed) / float64(r.Lines)
}

func printBoiler(w io.Writer, run boilerRun, reports []xay.HostReport, top int) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "host\tdocuments\tdistinct lines\tfurniture\tremoved\texample\n")
	for i, r := range reports {
		if i == top {
			break
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\n",
			r.Host, r.Documents, r.Lines, r.Furniture, r.Removed, example(r.Samples))
	}
	_ = tw.Flush()

	if len(reports) > top {
		fmt.Fprintf(w, "\n%d more hosts are not shown. Use -hosts to see them or -json to get all of them.\n", len(reports)-top)
	}
	fmt.Fprintf(w, "\nAcross %d hosts and %d documents, %d of %d lines were furniture, which is %s of them.\n",
		run.Hosts, run.Documents, run.Removed, run.Lines, percent(run.LineShare()))
	fmt.Fprintf(w, "A line is furniture on a host with %d documents or more when it appears in %d of them or in %s, whichever is more.\n",
		run.Furniture.MinDocuments, run.Furniture.MinCopies, percent(run.Furniture.MinShare))
	if run.Emptied > 0 {
		fmt.Fprintf(w, "%d documents were nothing but furniture and have nothing left. They go to the reject store rather than out of the corpus quietly.\n", run.Emptied)
	}
}

// example is the one removed line the table has room for. It is truncated
// because furniture is often a paragraph and the table is read across.
func example(samples []string) string {
	if len(samples) == 0 {
		return ""
	}
	s := samples[0]
	r := []rune(s)
	if len(r) > 48 {
		return string(r[:47]) + "…"
	}
	return s
}

type xayBanding struct {
	Bands int     `json:"bands"`
	Rows  int     `json:"rows"`
	Knee  float64 `json:"knee"`
}

func bandingOf(b xay.Banding) xayBanding {
	return xayBanding{Bands: b.Bands, Rows: b.Rows, Knee: b.Knee()}
}

type xayRun struct {
	Banding xayBanding `json:"banding"`
	Report  xay.Report `json:"report"`
}

type xayCurve struct {
	Banding xayBanding   `json:"banding"`
	Curve   []xay.Report `json:"curve"`
}

// readInto adds every document in one file to the index. A parquet part holds
// many and a text file is one, and the extension is what says which, because a
// part is a file the pipeline wrote and a text file is a file somebody has.
func readInto(x *xay.Index, name string) error {
	if filepath.Ext(name) != ".parquet" {
		text, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		x.AddText(string(text))
		return nil
	}
	rows, err := kho.ReadPart(name)
	if err != nil {
		return err
	}
	for _, r := range rows {
		x.Add(r.DocID, len([]rune(r.Text)), xay.Sign(r.Text))
	}
	return nil
}

func printDedup(w io.Writer, b xay.Banding, r xay.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "documents\texact\tnear\tkept\tretention\tclusters\tlargest\n")
	fmt.Fprintf(tw, "%d\t%d\t%d\t%d\t%s\t%d\t%d\n",
		r.Documents, r.Exact, r.Near, r.Kept, percent(r.Retention()), r.Clusters, r.Largest)
	_ = tw.Flush()

	fmt.Fprintf(w, "\nTwo documents are copies of each other at %s similarity or more, over %d bands of %d rows.\n",
		fmt.Sprintf("%.2f", r.Threshold), b.Bands, b.Rows)
	fmt.Fprintf(w, "A pair at %s is found %s of the time and a pair at 0.5 is found %s of the time.\n",
		fmt.Sprintf("%.2f", r.Threshold), percent(b.Detection(r.Threshold)), percent(b.Detection(0.5)))
}

func printCurve(w io.Writer, reports []xay.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "threshold\tkept\tretention\tclusters\tlargest\n")
	for _, r := range reports {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%d\n",
			fmt.Sprintf("%.2f", r.Threshold), r.Kept, percent(r.Retention()), r.Clusters, r.Largest)
	}
	_ = tw.Flush()
	fmt.Fprint(w, "\nThe threshold is not chosen from this table. It is chosen by training on either side of it,\nand the table says what each choice would cost in documents.\n")
}

func printJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(stderr, "gao: %v\n", err)
		return 1
	}
	return 0
}
