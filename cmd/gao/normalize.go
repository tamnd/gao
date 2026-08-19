package main

import (
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/tamnd/gao/normalize"
)

func runNormalize(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("normalize", flag.ContinueOnError)
	fs.SetOutput(stderr)
	report := fs.Bool("report", false, "print what normalization did instead of the normalized text")
	total := fs.Bool("total", false, "with -report, print the total and not a line per document")
	asJSON := fs.Bool("json", false, "with -report, print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao normalize [-report [-total] [-json]] [file...]

Normalize Vietnamese text, which is the first thing done to a document and the
thing every stage after it assumes has been done. With no file it reads standard
input. The normalized text goes to standard output, so this is a filter and you
can pipe a document through it.

With -report it prints what it had to do instead: the font encoding it read the
document out of, if it was written in one, the homoglyphs it repaired, the
invisible characters it took out, the syllables whose tone mark moved, and the
syllables that look like input method keystrokes, which it counts and leaves
alone. The last column says whether the document goes on to the next stage, and
names the reason when it does not. Several files are reported one to a line with
a total.

A report takes parquet parts as well as text files, and every row in a part is a
document. A part off the fleet holds a few hundred thousand of them, which is
more rows than anybody reads, so -total prints the summary and drops the lines.
That is also what makes the report cost the same memory on a part as on a file,
which is what running it over the whole corpus needs.

The number the report exists for is the last one: the share of documents this
stage changed by at least one byte.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *asJSON && !*report {
		fmt.Fprintln(stderr, "gao normalize: -json only means something with -report")
		return 2
	}
	if *total && !*report {
		fmt.Fprintln(stderr, "gao normalize: -total only means something with -report")
		return 2
	}

	files := fs.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	var tally normalize.Tally
	lines := make([]normalizeLine, 0, len(files))
	for _, name := range files {
		if !*report {
			// A part holds many documents and running them onto one stream
			// loses where each one ended, which is the one thing a normalizer
			// must not do.
			if filepath.Ext(name) == ".parquet" {
				fmt.Fprintf(stderr, "gao normalize: %s holds many documents, and normalizing them onto one stream loses where each one ended. Use -report\n", name)
				return 2
			}
			text, err := readDocument(name)
			if err != nil {
				fmt.Fprintf(stderr, "gao normalize: %v\n", err)
				return 1
			}
			r := normalize.Normalize(string(text))
			tally.Add(r)
			if _, err := io.WriteString(stdout, r.Text); err != nil {
				fmt.Fprintf(stderr, "gao normalize: %v\n", err)
				return 1
			}
			continue
		}

		// A row of a part is named by the part and its number in it. A text file
		// is one document and is named by itself.
		part := filepath.Ext(name) == ".parquet"
		row := 0
		err := eachDocument(name, func(text string) error {
			r := normalize.Normalize(text)
			tally.Add(r)
			if !*total {
				named := name
				if part {
					named = fmt.Sprintf("%s#%d", name, row)
				}
				lines = append(lines, normalizeLine{Name: named, Result: r})
			}
			row++
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gao normalize: %v\n", err)
			return 1
		}
	}
	if !*report {
		return 0
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(normalizeReport{Documents: lines, Total: tally}); err != nil {
			fmt.Fprintf(stderr, "gao normalize: %v\n", err)
			return 1
		}
		return 0
	}
	printNormalize(stdout, lines, tally)
	return 0
}

// normalizeLine is one document's report. The result is flattened into the JSON so
// that a reader does not have to reach through a nested object to get a count.
type normalizeLine struct {
	Name string `json:"name"`
	normalize.Result
}

type normalizeReport struct {
	Documents []normalizeLine `json:"documents"`
	Total     normalize.Tally `json:"total"`
}

func readDocument(name string) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(name)
}

func printNormalize(w io.Writer, lines []normalizeLine, total normalize.Tally) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "document\tchanged\trepaired\tencoding\thomoglyphs\tinvisible\tcontrols\tcomposed\ttones\tresidue\tsyllables\tkept\n")
	for _, l := range lines {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			l.Name, yesNo(l.Changed), yesNo(l.Repaired()), encoding(l.Result),
			l.Homoglyphs, l.Invisible, l.Controls, l.Composed, l.Tones,
			l.Residue, l.Syllables, kept(l.Result))
	}
	if total.Documents > 1 {
		fmt.Fprintf(tw, "%d documents\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d rejected\n",
			total.Documents, total.Changed, total.Repaired, legacyTotal(total),
			total.Homoglyphs, total.Invisible, total.Controls, total.Composed,
			total.Tones, total.Residue, total.Syllables, total.Rejected)
	}
	_ = tw.Flush()

	if total.Documents > 1 {
		fmt.Fprintf(w, "\nNormalization changed %s of the documents by at least one byte, and %s of them by something other than layout.\n",
			percent(total.ChangedShare()), percent(total.RepairedShare()))
	}
	if len(total.Legacy) > 0 {
		fmt.Fprint(w, legacyLine(total))
	}
	if len(lines) == 1 {
		r := lines[0].Result
		if r.Residue > 0 {
			fmt.Fprintf(w, "\n%s of the syllables look like input method keystrokes, and the limit is %s.\n",
				percent(r.ResidueRate()), percent(normalize.ResidueLimit))
		}
		if r.ControlRate() > normalize.ControlLimit {
			fmt.Fprintf(w, "\n%s of the runes were control characters, which is over the limit of %s and usually means the file is not text.\n",
				percent(r.ControlRate()), percent(normalize.ControlLimit))
		}
		if reason, ok := normalize.Reject(r); ok {
			fmt.Fprintf(w, "The document does not go on to the next stage. The reject store records it as %q, meaning %s.\n",
				string(reason), reason.Describe())
		}
	}
}

// encoding is the font encoding column. It is empty for nearly every document,
// which is the right shape for a column that says a document was rewritten from
// end to end: the few that were are the ones worth finding in a listing.
func encoding(r normalize.Result) string { return r.Legacy }

// legacyTotal is how many documents came out of a font encoding, over all of
// them.
func legacyTotal(t normalize.Tally) int64 {
	var n int64
	for _, count := range t.Legacy {
		n += count
	}
	return n
}

// legacyLine is the sentence under the table. It names the encodings, the most
// of them first, so that a run over a source reads as a sentence about when the
// pages in it were written.
func legacyLine(t normalize.Tally) string {
	names := slices.Collect(maps.Keys(t.Legacy))
	slices.SortFunc(names, func(a, b string) int {
		if c := cmp.Compare(t.Legacy[b], t.Legacy[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	if legacyTotal(t) == 1 {
		return fmt.Sprintf("One document was written in a font encoding rather than in Unicode, and it was %s.\n", names[0])
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%d in %s", t.Legacy[name], name))
	}
	return fmt.Sprintf("%d documents were written in a font encoding rather than in Unicode: %s.\n",
		legacyTotal(t), strings.Join(parts, ", "))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// kept is the last column of the report. A document this stage drops is dropped
// for one of two reasons and the column names it, because "no" on its own sends
// whoever is reading looking through the counts for which limit was the one.
func kept(r normalize.Result) string {
	if reason, ok := normalize.Reject(r); ok {
		return "no, " + string(reason)
	}
	return "yes"
}
