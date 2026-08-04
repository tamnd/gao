package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/tamnd/gao/phoi"
)

func runPhoi(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("phoi", flag.ContinueOnError)
	fs.SetOutput(stderr)
	report := fs.Bool("report", false, "print what normalization did instead of the normalized text")
	asJSON := fs.Bool("json", false, "with -report, print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao phoi [-report [-json]] [file...]

Normalize Vietnamese text, which is the first thing done to a document and the
thing every stage after it assumes has been done. With no file it reads standard
input. The normalized text goes to standard output, so this is a filter and you
can pipe a document through it.

With -report it prints what it had to do instead: the homoglyphs it repaired, the
invisible characters it took out, the syllables whose tone mark moved, and the
syllables that look like input method keystrokes, which it counts and leaves
alone. The last column says whether the document goes on to the next stage, and
names the reason when it does not. Several files are reported one to a line with
a total.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *asJSON && !*report {
		fmt.Fprintln(stderr, "gao phoi: -json only means something with -report")
		return 2
	}

	files := fs.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	var tally phoi.Tally
	lines := make([]phoiLine, 0, len(files))
	for _, name := range files {
		text, err := readDocument(name)
		if err != nil {
			fmt.Fprintf(stderr, "gao phoi: %v\n", err)
			return 1
		}
		r := phoi.Normalize(string(text))
		tally.Add(r)
		if !*report {
			if _, err := io.WriteString(stdout, r.Text); err != nil {
				fmt.Fprintf(stderr, "gao phoi: %v\n", err)
				return 1
			}
			continue
		}
		lines = append(lines, phoiLine{Name: name, Result: r})
	}
	if !*report {
		return 0
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(phoiReport{Documents: lines, Total: tally}); err != nil {
			fmt.Fprintf(stderr, "gao phoi: %v\n", err)
			return 1
		}
		return 0
	}
	printPhoi(stdout, lines, tally)
	return 0
}

// phoiLine is one document's report. The result is flattened into the JSON so
// that a reader does not have to reach through a nested object to get a count.
type phoiLine struct {
	Name string `json:"name"`
	phoi.Result
}

type phoiReport struct {
	Documents []phoiLine `json:"documents"`
	Total     phoi.Tally `json:"total"`
}

func readDocument(name string) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(name)
}

func printPhoi(w io.Writer, lines []phoiLine, total phoi.Tally) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "document\tchanged\thomoglyphs\tinvisible\tcontrols\tcomposed\ttones\tresidue\tsyllables\tkept\n")
	for _, l := range lines {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			l.Name, yesNo(l.Changed), l.Homoglyphs, l.Invisible, l.Controls,
			l.Composed, l.Tones, l.Residue, l.Syllables, kept(l.Result))
	}
	if len(lines) > 1 {
		fmt.Fprintf(tw, "%d documents\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d rejected\n",
			total.Documents, total.Changed, total.Homoglyphs, total.Invisible,
			total.Controls, total.Composed, total.Tones, total.Residue,
			total.Syllables, total.Rejected)
	}
	_ = tw.Flush()

	if len(lines) == 1 {
		r := lines[0].Result
		if r.Residue > 0 {
			fmt.Fprintf(w, "\n%s of the syllables look like input method keystrokes, and the limit is %s.\n",
				percent(r.ResidueRate()), percent(phoi.ResidueLimit))
		}
		if r.ControlRate() > phoi.ControlLimit {
			fmt.Fprintf(w, "\n%s of the runes were control characters, which is over the limit of %s and usually means the file is not text.\n",
				percent(r.ControlRate()), percent(phoi.ControlLimit))
		}
		if reason, ok := phoi.Reject(r); ok {
			fmt.Fprintf(w, "The document does not go on to the next stage. The reject store records it as %q, meaning %s.\n",
				string(reason), reason.Describe())
		}
	}
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
func kept(r phoi.Result) string {
	if reason, ok := phoi.Reject(r); ok {
		return "no, " + string(reason)
	}
	return "yes"
}
