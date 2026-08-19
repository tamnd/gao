package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/tamnd/gao/kho"
	"github.com/tamnd/gao/sang"
	"github.com/tamnd/gao/vo"
)

func runSang(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("sift", flag.ContinueOnError)
	fs.SetOutput(stderr)
	minSyllables := fs.Int("min-syllables", sang.Default().MinSyllables, "the shortest thing that counts as a document")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao sift [-min-syllables n] [-json] file...

Measure documents and say which of them are Vietnamese prose. A file is either
a parquet part written by the ingest, in which case every row in it is a
document, or a text file, in which case the file is one document.

The measurements are the ones every published pipeline uses and none of the
thresholds are, because those pipelines count space separated tokens and call
them words. Vietnamese writes a space between syllables. The report prints what
each document measured beside what it was measured against, so a threshold that
is removing good documents can be seen doing it.

The language is decided by the Vietnamese syllable inventory rather than by a
model, and the vietnamese column is the share of a document's tokens that are
Vietnamese syllables. For a document typed without tone marks it is the share
with the marks taken off both sides, which is a looser test and is held to a
higher bar.

Nothing here judges quality. A document that goes through has been found to be
Vietnamese prose of some length, which is the floor rather than the bar.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(stderr, "gao sift: nothing to read. Give it parquet parts or text files")
		return 2
	}

	limits := sang.Default()
	if *minSyllables < 0 {
		fmt.Fprintf(stderr, "gao sift: a document cannot be shorter than no syllables, and %d is\n", *minSyllables)
		return 2
	}
	limits.MinSyllables = *minSyllables

	var (
		tally sang.Tally
		rows  []sangRow
	)
	for _, name := range files {
		texts, err := readDocuments(name)
		if err != nil {
			fmt.Fprintf(stderr, "gao sift: %v\n", err)
			return 1
		}
		for i, text := range texts {
			r := sang.Measure(text)
			tally.Add(limits, r)
			rows = append(rows, sangRowOf(limits, label(name, i, len(texts)), r))
		}
	}

	if *asJSON {
		return printJSON(stdout, stderr, sangReport{Documents: rows, Total: sangTotalOf(tally)})
	}
	printSang(stdout, rows, tally)
	return 0
}

// label names a document in the report. A part holds many, so a row from one is
// named by the file and the row inside it, and a text file is named by itself.
func label(name string, i, n int) string {
	if n == 1 {
		return name
	}
	return fmt.Sprintf("%s#%d", name, i)
}

// eachDocument hands every document in one file to fn, in the order the file
// holds them. A parquet part holds many and a text file is one, and the
// extension is what says which.
//
// It streams rather than returning a slice, because a part off the fleet is
// around 700 MB compressed and several gigabytes of text once it is read, and a
// command that has to hold a part to measure one is a command that cannot be
// pointed at the corpus.
func eachDocument(name string, fn func(text string) error) error {
	if filepath.Ext(name) != ".parquet" {
		text, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return fn(string(text))
	}
	return kho.ScanPart(name, func(r kho.Row) error { return fn(r.Text) })
}

// readDocuments returns every document in one file, for the commands that build
// a row per document anyway and so gain nothing from streaming.
func readDocuments(name string) ([]string, error) {
	var texts []string
	err := eachDocument(name, func(text string) error {
		texts = append(texts, text)
		return nil
	})
	return texts, err
}

type sangRow struct {
	Document   string  `json:"document"`
	Syllables  int     `json:"syllables"`
	Mean       float64 `json:"mean_syllable"`
	StopWords  int     `json:"stop_words"`
	Vietnamese float64 `json:"syllable_rate"`
	BareRate   float64 `json:"bare_rate"`
	MarkRate   float64 `json:"mark_rate"`
	Alpha      float64 `json:"alpha_rate"`
	Bullets    float64 `json:"bullet_rate"`
	Ellipses   float64 `json:"ellipsis_rate"`
	Duplicate  float64 `json:"dup_line_rate"`
	Repeat     float64 `json:"repeat_gram_max"`
	Diacritics string  `json:"diacritics"`
	Kept       bool    `json:"kept"`
	Reason     string  `json:"reject_reason,omitempty"`
	Detail     string  `json:"reject_detail,omitempty"`
}

func sangRowOf(l sang.Limits, name string, r sang.Result) sangRow {
	row := sangRow{
		Document:   name,
		Syllables:  r.Syllables,
		Mean:       r.MeanSyllable(),
		StopWords:  r.StopWords,
		Vietnamese: r.Language.Rate(),
		BareRate:   r.Language.BareRate(),
		MarkRate:   r.Language.MarkRate(),
		Alpha:      r.AlphaRate(),
		Bullets:    r.BulletRate(),
		Ellipses:   r.EllipsisRate(),
		Duplicate:  r.DuplicateLineRate(),
		Repeat:     largest(r.Repeat),
		Diacritics: r.Diacritic(),
		Kept:       true,
	}
	if reason, detail, ok := l.Reject(r); ok {
		row.Kept, row.Reason, row.Detail = false, string(reason), detail
	}
	return row
}

// vietnameseRate is the share the language verdict was taken on, which is the
// share with the marks off for a document that was typed without them. One
// column, because a report that printed both would be printing the same number
// twice for every document that carries its marks.
func vietnameseRate(r sangRow) float64 {
	if r.MarkRate < sang.MinMarkRate {
		return r.BareRate
	}
	return r.Vietnamese
}

// largest reports the worst of the three gram sizes, since the report has one
// column for them and the threshold that fires is whichever one is worst.
func largest(f [3]float64) float64 {
	worst := f[0]
	for _, v := range f[1:] {
		if v > worst {
			worst = v
		}
	}
	return worst
}

type sangTotal struct {
	Documents  int64            `json:"documents"`
	Kept       int64            `json:"kept"`
	Retention  float64          `json:"retention"`
	Syllables  int64            `json:"syllables"`
	Rejected   map[string]int64 `json:"rejected,omitempty"`
	Diacritics map[string]int64 `json:"diacritics,omitempty"`
}

func sangTotalOf(t sang.Tally) sangTotal {
	total := sangTotal{
		Documents:  t.Documents,
		Kept:       t.Kept,
		Retention:  t.Retention(),
		Syllables:  t.Syllables,
		Diacritics: t.Diacritics,
	}
	if len(t.Rejected) > 0 {
		total.Rejected = make(map[string]int64, len(t.Rejected))
		for reason, n := range t.Rejected {
			total.Rejected[string(reason)] = n
		}
	}
	return total
}

type sangReport struct {
	Documents []sangRow `json:"documents"`
	Total     sangTotal `json:"total"`
}

func printSang(w io.Writer, rows []sangRow, t sang.Tally) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "document\tsyllables\tmean\tstop\tvietnamese\talpha\tbullets\tellipses\tduplicate\trepeat\tdiacritics\tkept\n")
	for _, r := range rows {
		kept := "yes"
		if !r.Kept {
			kept = "no, " + r.Reason
		}
		fmt.Fprintf(tw, "%s\t%d\t%.2f\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Document, r.Syllables, r.Mean, r.StopWords, percent(vietnameseRate(r)),
			percent(r.Alpha), percent(r.Bullets), percent(r.Ellipses),
			percent(r.Duplicate), percent(r.Repeat), r.Diacritics, kept)
	}
	fmt.Fprintf(tw, "%d documents\t%d\t\t\t\t\t\t\t\t\t\t%d kept\n", t.Documents, t.Syllables, t.Kept)
	_ = tw.Flush()

	if t.Documents == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s of the documents go on to the next stage.\n", percent(t.Retention()))
	if len(t.Rejected) > 0 {
		fmt.Fprint(w, "The reject store records the rest as")
		for i, reason := range sortedReasons(t.Rejected) {
			sep := ","
			if i == 0 {
				sep = ""
			}
			fmt.Fprintf(w, "%s %d %s", sep, t.Rejected[reason], string(reason))
		}
		fmt.Fprint(w, ".\n")
	}
	if bare := t.Diacritics["absent"] + t.Diacritics["mixed"]; bare > 0 {
		fmt.Fprintf(w, "%d of them carry few or no tone marks, which is a label on the row rather than a rejection.\n", bare)
	}
}

// checkOrder is the order Reject tries the checks in, which is the order the
// counts are worth reading in and is not the order vo lists its reasons in.
var checkOrder = []vo.Reason{
	vo.ReasonShort, vo.ReasonBoilerplate, vo.ReasonLanguage, vo.ReasonRepetition,
}

// sortedReasons puts the counts in a fixed order, so that two runs of the same
// report list them the same way round.
func sortedReasons(counted map[vo.Reason]int64) []vo.Reason {
	order := map[vo.Reason]int{}
	for i, r := range checkOrder {
		order[r] = i
	}
	out := make([]vo.Reason, 0, len(counted))
	for r := range counted {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if order[out[i]] != order[out[j]] {
			return order[out[i]] < order[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}
