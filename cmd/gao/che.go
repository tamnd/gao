package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"

	"github.com/tamnd/gao/che"
)

func runChe(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("che", flag.ContinueOnError)
	fs.SetOutput(stderr)
	level := fs.String("level", "L1", "how much to cover: L0 records only, L1 covers identifiers, L2 also covers names and addresses")
	report := fs.Bool("report", false, "print what was found instead of the covered text")
	spans := fs.Bool("spans", false, "with -report, list every span, including the text it matched")
	asJSON := fs.Bool("json", false, "with -report or -recall, print JSON")
	recall := fs.Bool("recall", false, "measure the detectors against the labeled set built into this binary")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao che [-level L0|L1|L2] [-report [-spans] [-json]] [file...]
       gao che -recall [-json]

Cover the personal data in a document. With no file it reads standard input,
and the covered text goes to standard output, so this is a filter in the same
way gao phoi is.

What is covered is a level. L0 finds everything and covers nothing, which is
what a source is measured with before anybody decides what to do about it. L1
covers identifiers: email, phone, national ID under both numberings, tax code
and registration plate. L2 covers those plus street addresses and the names
that sit in a paragraph beside an identifier.

Names are not covered wholesale at any level, and that is a decision. A
Vietnamese corpus with no Vietnamese names in it cannot say who wrote a poem.
What is private is a name next to a way of reaching the person, so that is what
L2 looks for.

With -report it prints what it found instead of the text, one document to a
line, with a count for each kind. The cued column is how many spans were found
because the text named what they were rather than because their own structure
gave them away, which is the share of the recall that depends on people
announcing their own identifiers. A parquet part reports every row in it as a
document.

With -spans it also lists each span with the text it matched. That prints
personal data to the terminal on purpose, because reading the matches is the
only way to see a detector firing on something it should not.

With -recall it reads no files at all. It runs the detectors over a set of
documents that carry their own answers, built into this binary, and prints what
each detector found of what was marked. That is the number to quote when
somebody asks how much personal data the corpus still holds, and the misses are
printed with it because a recall figure with no list of what it lost is not
worth much.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *recall {
		if *report || *spans {
			fmt.Fprintln(stderr, "gao che: -recall measures the labeled set built into this binary, so it has nothing to report on")
			return 2
		}
		if len(fs.Args()) > 0 {
			fmt.Fprintln(stderr, "gao che: -recall reads the labeled set built into this binary rather than a file")
			return 2
		}
		return runCheRecall(stdout, stderr, *asJSON)
	}
	lv, ok := che.ParseLevel(*level)
	if !ok {
		fmt.Fprintf(stderr, "gao che: %q is not a level. The levels are L0, L1 and L2\n", *level)
		return 2
	}
	if !*report && (*asJSON || *spans) {
		fmt.Fprintln(stderr, "gao che: -json and -spans only mean something with -report")
		return 2
	}

	files := fs.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	var (
		tally che.Tally
		rows  []cheRow
	)
	for _, name := range files {
		if !*report {
			if filepath.Ext(name) == ".parquet" {
				fmt.Fprintf(stderr, "gao che: %s holds many documents, and covering them onto one stream loses where each one ended. Use -report\n", name)
				return 2
			}
			text, err := readDocument(name)
			if err != nil {
				fmt.Fprintf(stderr, "gao che: %v\n", err)
				return 1
			}
			covered, found := che.Redact(string(text), lv)
			tally.Add(lv, found)
			if _, err := io.WriteString(stdout, covered); err != nil {
				fmt.Fprintf(stderr, "gao che: %v\n", err)
				return 1
			}
			continue
		}
		texts, err := readDocuments(name)
		if err != nil {
			fmt.Fprintf(stderr, "gao che: %v\n", err)
			return 1
		}
		for i, text := range texts {
			found := che.Find(text)
			tally.Add(lv, found)
			rows = append(rows, cheRowOf(lv, label(name, i, len(texts)), found, *spans))
		}
	}
	if !*report {
		return 0
	}

	if *asJSON {
		return printJSON(stdout, stderr, cheReport{Level: lv.String(), Documents: rows, Total: cheTotalOf(tally)})
	}
	printChe(stdout, lv, rows, tally)
	return 0
}

// runCheRecall prints how much of a set of hand marked documents the detectors
// find. It takes no files, because the set it measures is the point: a recall
// figure measured on whatever the caller happened to pass in is a figure nobody
// can compare against anybody else's.
func runCheRecall(stdout, stderr io.Writer, asJSON bool) int {
	set, err := che.Labeled()
	if err != nil {
		fmt.Fprintf(stderr, "gao che: %v\n", err)
		return 1
	}
	s := che.Measure(set)
	if asJSON {
		return printJSON(stdout, stderr, s)
	}
	printCheRecall(stdout, s)
	return 0
}

func printCheRecall(w io.Writer, s che.Score) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "detector\tmarked\tcovered\trecall\tfound\tprecision\n")
	for _, k := range che.Kinds() {
		c := s.ByKind[k]
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%d\t%s\n",
			string(k), c.Gold, c.Hit, percent(c.Recall()), c.Found, percent(c.Precision()))
	}
	fmt.Fprintf(tw, "all\t%d\t%d\t%s\t%d\t%s\n",
		s.Total.Gold, s.Total.Hit, percent(s.Total.Recall()), s.Total.Found, percent(s.Total.Precision()))
	_ = tw.Flush()

	fmt.Fprintf(w, "\n%d documents, marked by hand, %d spans in them.\n", s.Documents, s.Total.Gold)
	fmt.Fprintln(w, "A span counts as covered only when a found span of the same kind holds all of it.")

	if len(s.Missed) > 0 {
		fmt.Fprintf(w, "\nNot covered:\n")
		for _, m := range s.Missed {
			why := ""
			if m.Partial {
				why = ", covered in part"
			}
			fmt.Fprintf(w, "  %-8s %s: %s%s\n", string(m.Span.Kind), m.Document, m.Span.Text, why)
		}
	}
	for _, k := range che.Kinds() {
		for _, m := range s.Spurious[k] {
			fmt.Fprintf(w, "  %-8s %s: %s, and nothing there is marked\n", string(k), m.Document, m.Span.Text)
		}
	}
}

type cheRow struct {
	Document string           `json:"document"`
	Spans    int64            `json:"spans"`
	Covered  int64            `json:"covered"`
	Cued     int64            `json:"cued"`
	ByKind   map[che.Kind]int `json:"by_kind,omitempty"`
	Found    []che.Span       `json:"found,omitempty"`
}

func cheRowOf(level che.Level, name string, found []che.Span, withSpans bool) cheRow {
	row := cheRow{Document: name, Spans: int64(len(found)), ByKind: map[che.Kind]int{}}
	for _, s := range found {
		row.ByKind[s.Kind]++
		if s.Cued {
			row.Cued++
		}
		if level.Covers(s.Kind) {
			row.Covered++
		}
	}
	if withSpans {
		row.Found = found
	}
	return row
}

type cheTotal struct {
	Documents int64              `json:"documents"`
	Carrying  int64              `json:"carrying"`
	Rate      float64            `json:"rate"`
	Spans     int64              `json:"spans"`
	Covered   int64              `json:"covered"`
	Cued      int64              `json:"cued"`
	ByKind    map[che.Kind]int64 `json:"by_kind,omitempty"`
}

func cheTotalOf(t che.Tally) cheTotal {
	return cheTotal{
		Documents: t.Documents,
		Carrying:  t.Carrying,
		Rate:      t.Rate(),
		Spans:     t.Spans,
		Covered:   t.Covered,
		Cued:      t.Cued,
		ByKind:    t.ByKind,
	}
}

type cheReport struct {
	Level     string   `json:"level"`
	Documents []cheRow `json:"documents"`
	Total     cheTotal `json:"total"`
}

func printChe(w io.Writer, level che.Level, rows []cheRow, t che.Tally) {
	kinds := che.Kinds()
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "document\tspans\tcovered\tcued")
	for _, k := range kinds {
		fmt.Fprintf(tw, "\t%s", string(k))
	}
	fmt.Fprint(tw, "\n")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d", r.Document, r.Spans, r.Covered, r.Cued)
		for _, k := range kinds {
			fmt.Fprintf(tw, "\t%d", r.ByKind[k])
		}
		fmt.Fprint(tw, "\n")
	}
	if len(rows) > 1 {
		fmt.Fprintf(tw, "%d documents\t%d\t%d\t%d", t.Documents, t.Spans, t.Covered, t.Cued)
		for _, k := range kinds {
			fmt.Fprintf(tw, "\t%d", t.ByKind[k])
		}
		fmt.Fprint(tw, "\n")
	}
	_ = tw.Flush()

	if t.Documents == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s of the documents hold personal data of some kind.\n", percent(t.Rate()))
	switch {
	case t.Spans == 0:
	case level == che.L0:
		fmt.Fprintf(w, "L0 covers none of the %d spans. They are recorded so that a source can be measured before anybody decides what to do about it.\n", t.Spans)
	case t.Covered == t.Spans:
		fmt.Fprintf(w, "%s covers all %d of them.\n", level, t.Spans)
	default:
		fmt.Fprintf(w, "%s covers %d of the %d and leaves the rest in the text.\n", level, t.Covered, t.Spans)
	}
	switch {
	case t.Spans == 0:
	case t.Cued == 0:
		fmt.Fprintf(w, "Nothing in the text named any of them, so all %d were found from their own structure.\n", t.Spans)
	default:
		fmt.Fprintf(w, "%d of them were found because the text named what they were, and the other %d from their own structure.\n",
			t.Cued, t.Spans-t.Cued)
	}

	for _, r := range rows {
		if len(r.Found) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s\n", r.Document)
		for _, s := range r.Found {
			cue := ""
			if s.Cued {
				cue = ", named in the text"
			}
			fmt.Fprintf(w, "  %-8s %s%s\n", string(s.Kind), s.Text, cue)
		}
	}
}
