package main

// Building and scoring the diacritic restoration task set.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tamnd/gao/dau"
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

func runDau(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		dauUsage(stderr)
		return 2
	}
	switch args[0] {
	case "build":
		return runDauBuild(stdout, stderr, args[1:])
	case "baseline":
		return runDauBaseline(stdout, stderr, args[1:])
	case "grade":
		return runDauGrade(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		dauUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao dau: unknown subcommand %q\n", args[0])
		dauUsage(stderr)
		return 2
	}
}

func dauUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: gao dau <subcommand> [flags] [files...]

Builds and scores %s, the diacritic restoration task set. Taking the
marks off a page of Vietnamese is a function and putting them back is not, so
every page in the corpus is a question whose answer is exact and free. It is the
one thing here that does not cost an annotator.

subcommands:
  build     turn documents into questions and their answers
  baseline  score the two answers a model has to beat before its number means anything
  grade     score a file of answers against the task set

Files are Parquet parts or text files, and a text file is one document.

run 'gao dau <subcommand> -h' for the flags of a single subcommand.
`, dau.Name)
}

func runDauBuild(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dau build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "write the task set here as JSON lines, one item per line, instead of to stdout")
	oneIn := fs.Int("one-in", 1, "keep one document in this many, chosen by document identity so the build is reproducible")
	limit := fs.Int("n", 0, "stop after this many items")
	minChars := fs.Int("min-chars", 0, "the shortest answer to accept")
	maxChars := fs.Int("max-chars", 0, "the longest answer to accept, cut at a boundary")
	minMarked := fs.Float64("min-marked", 0, "the floor on the share of characters carrying a mark")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: gao dau build [flags] <file...>

Turns documents into questions. Each item is a page of Vietnamese with its marks
taken off, the page itself as the answer, and the identity of the document it
came from.

The identity is the point of the exercise as much as the text is. The answers
are in the training corpus, so a model trained on gao has read every one of
these pages with its marks on, and the set is worth nothing unless its items are
held out first. The identity is what makes holding them out possible and what
lets nhat check afterwards that it happened.

A document typed without its marks is refused. Roughly half the Vietnamese
online is written that way, and such a document is not an answer key, it is a
second copy of the question. The rejections are printed per reason, because a
builder that quietly drops nine documents in ten is a builder nobody can debug.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	b := dau.NewBuilder(dau.Options{
		MinChars:  *minChars,
		MaxChars:  *maxChars,
		MinMarked: *minMarked,
		OneIn:     *oneIn,
		Limit:     *limit,
	})

	w := stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(stderr, "gao dau build: %v\n", err)
			return 1
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	enc := json.NewEncoder(w)

	for _, name := range fs.Args() {
		err := eachIdentifiedDocument(name, func(id doc.Hash, text string) error {
			it, _, ok := b.Add(id, text)
			if !ok {
				return nil
			}
			return enc.Encode(it)
		})
		if err != nil {
			fmt.Fprintf(stderr, "gao dau build: %s: %v\n", name, err)
			return 1
		}
	}

	report := stdout
	if *out == "" {
		// The items went to stdout, so the accounting cannot follow them there.
		report = stderr
	}
	fmt.Fprintf(report, "%s: %d items from %d documents\n", dau.Name, len(b.Items()), b.Read())
	for _, r := range dau.Reasons() {
		fmt.Fprintf(report, "  %-20s %d\n", r, b.Rejected(r))
	}
	if len(b.Items()) == 0 {
		fmt.Fprintln(stderr, "\nno item was built, so there is nothing to hold out and nothing to score")
		return 1
	}
	fmt.Fprintln(report, "\nhold these document identities out of the corpus before training against this set")
	return 0
}

func runDauBaseline(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dau baseline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	items := fs.String("items", "", "the task set to score against, as written by 'gao dau build'")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao dau baseline -items SET <file...>

Scores the two answers a model has to beat before its own number means anything.

The first is answering with the question. A model that hands the bare text back
restores nothing and still scores around 76% character accuracy, because only
about a quarter of Vietnamese characters carry a mark, and it gets about one
syllable in nine exactly right because that many are written with no mark at
all. Any restoration result quoted as character accuracy is quoting a number
that starts in the seventies.

The second is a table: answer every bare syllable with the marked spelling it
most often has, counted off the files given here, with no context at all. That
is the whole task minus the only interesting part of it, and it is strong. A
model that does not beat it has learned the dictionary rather than the language.

The files counted must not be the files the task set was built from. A table is
trivially perfect on the pages it counted, and a baseline measured on its own
training data is the same mistake as a benchmark measured on the model's.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 || *items == "" {
		fs.Usage()
		return 2
	}

	set, err := readItems(*items)
	if err != nil {
		fmt.Fprintf(stderr, "gao dau baseline: %v\n", err)
		return 1
	}

	lex := dau.NewLexicon()
	counted, refused := 0, 0
	for _, name := range fs.Args() {
		err := eachIdentifiedDocument(name, func(_ doc.Hash, text string) error {
			if lex.Add(text) {
				counted++
			} else {
				refused++
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gao dau baseline: %s: %v\n", name, err)
			return 1
		}
	}

	var nothing, table dau.Report
	for _, it := range set {
		nothing.Add(dau.Grade(it, it.Prompt))
		table.Add(dau.Grade(it, lex.Restore(it.Prompt)))
	}

	fmt.Fprintf(stdout, "%s: %d items\n", dau.Name, len(set))
	fmt.Fprintf(stdout, "the table counted %d documents and refused %d for being typed without marks\n", counted, refused)
	fmt.Fprintf(stdout, "it holds %d bare spellings\n\n", lex.Size())
	printDauReport(stdout, "answer with the question", nothing)
	printDauReport(stdout, "answer from a table, no context", table)
	fmt.Fprintln(stdout, "a model that does not clear the second line has learned the dictionary and not the language")
	return 0
}

func runDauGrade(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dau grade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	items := fs.String("items", "", "the task set the answers are against")
	perItem := fs.Bool("v", false, "print a line per item as well as the total")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao dau grade -items SET <answers.jsonl>

Scores a file of answers. Each line is a JSON object with a doc_id and an
answer, matching an item in the task set. An item with no answer is scored as
having restored nothing, because a model that declined to answer has not earned
the item.

The headline number is the share of the page's marks that came back, rather than
character accuracy, which on this task starts in the seventies for a model that
does nothing at all.

An answer that changed the letters rather than only the marks is counted as
unfaithful. It is still scored, because a model that paraphrases half the time
is a fact about the model, but its syllables are not counted right, since the
two texts no longer line up and an alignment would put a judgment inside a
count.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *items == "" {
		fs.Usage()
		return 2
	}

	set, err := readItems(*items)
	if err != nil {
		fmt.Fprintf(stderr, "gao dau grade: %v\n", err)
		return 1
	}
	answers, err := readAnswers(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao dau grade: %v\n", err)
		return 1
	}

	var report dau.Report
	var missing int
	for _, it := range set {
		answer, ok := answers[it.DocID]
		if !ok {
			missing++
			answer = it.Prompt
		}
		r := dau.Grade(it, answer)
		report.Add(r)
		if *perItem {
			fmt.Fprintf(stdout, "%s  restored %.3f  syllables %d/%d  faithful %t\n",
				it.DocID, 1-r.Score.DER(), r.Right, r.Syllables, r.Faithful)
		}
	}

	if *perItem {
		fmt.Fprintln(stdout)
	}
	fmt.Fprintf(stdout, "%s: %d items\n", dau.Name, report.Items)
	if missing > 0 {
		fmt.Fprintf(stdout, "%d items had no answer and are scored as having restored nothing\n", missing)
	}
	if extra := len(answers) - (len(set) - missing); extra > 0 {
		fmt.Fprintf(stdout, "%d answers do not match any item and were ignored\n", extra)
	}
	fmt.Fprintln(stdout)
	printDauReport(stdout, "answers", report)
	return 0
}

func printDauReport(w io.Writer, label string, r dau.Report) {
	fmt.Fprintf(w, "%s\n", label)
	fmt.Fprintf(w, "  marks restored     %.3f\n", r.Restored())
	fmt.Fprintf(w, "  syllables exact    %.3f\n", r.SyllableAccuracy())
	fmt.Fprintf(w, "  character accuracy %.3f\n", r.Score.Accuracy())
	if r.Unfaithful > 0 {
		fmt.Fprintf(w, "  unfaithful         %d of %d answers changed the letters and not only the marks\n", r.Unfaithful, r.Items)
	}
	fmt.Fprintln(w)
}

// readItems reads a task set back.
func readItems(path string) ([]dau.Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []dau.Item
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		if len(s.Bytes()) == 0 {
			continue
		}
		var it dau.Item
		if err := json.Unmarshal(s.Bytes(), &it); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, it)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no items", path)
	}
	return out, nil
}

// readAnswers reads what a model produced, keyed by the item it answers.
func readAnswers(path string) (map[doc.Hash]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := map[doc.Hash]string{}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		if len(s.Bytes()) == 0 {
			continue
		}
		var a struct {
			DocID  doc.Hash `json:"doc_id"`
			Answer string   `json:"answer"`
		}
		if err := json.Unmarshal(s.Bytes(), &a); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out[a.DocID] = a.Answer
	}
	return out, s.Err()
}

// eachIdentifiedDocument is [eachDocument] with the document identity, which a
// task set needs and a measurement does not. A text file has no recorded
// identity, so it gets the one its text implies, which is the same rule the
// ingest uses.
func eachIdentifiedDocument(name string, fn func(id doc.Hash, text string) error) error {
	if filepath.Ext(name) != ".parquet" {
		text, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return fn(doc.SumString(string(text)), string(text))
	}
	return kho.ScanPart(name, func(r kho.Row) error { return fn(r.DocID, r.Text) })
}
