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
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao xay [-threshold t] [-curve] [-json] file...

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
	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(stderr, "gao xay: nothing to read. Give it parquet parts or text files")
		return 2
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
