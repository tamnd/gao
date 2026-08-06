package main

// Building, scoring and validating the fast proxy benchmark.

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tamnd/gao/dien"
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/may"
)

func runDien(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		dienUsage(stderr)
		return 2
	}
	switch args[0] {
	case "build":
		return runDienBuild(stdout, stderr, args[1:])
	case "baseline":
		return runDienBaseline(stdout, stderr, args[1:])
	case "grade":
		return runDienGrade(stdout, stderr, args[1:])
	case "validate":
		return runDienValidate(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		dienUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao dien: unknown subcommand %q\n", args[0])
		dienUsage(stderr)
		return 2
	}
}

func dienUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: gao dien <subcommand> [flags] [files...]

Builds and scores %s, the proxy the ablation slate is run against.

The slate is forty training runs and each one has to be scored, so the inner
loop of the whole ablation program is this benchmark. A generative evaluation
with a judge behind it would put an hour and an API bill between every run and
its result. This is four candidate continuations scored by likelihood and an
argmax over them, which is minutes on one card, and the answer key is the page
the passage came off, so it costs nothing to build.

A proxy is only worth running if it agrees with the thing it stands in for.
'gao dien validate' measures that agreement and reports whether the slate can be
presented as having chosen anything.

subcommands:
  build     turn documents into questions and their answers
  baseline  score the answer a model has to beat before its number means anything
  grade     score a file of answers against the task set
  validate  measure whether the proxy agrees with full scale results

Files are Parquet parts or text files, and a text file is one document.

run 'gao dien <subcommand> -h' for the flags of a single subcommand.
`, dien.Name)
}

func runDienBuild(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dien build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "write the task set here as JSON lines, one item per line, instead of to stdout")
	count := fs.String("count", "", "comma separated files to count the frequency ranking over, which must not be the files the items are built from")
	sample := fs.Float64("sample", 0, "the share of documents to consider, chosen by document identity so the build is reproducible")
	limit := fs.Int("n", 0, "stop after this many items")
	minChars := fs.Int("min-chars", 0, "the shortest passage to accept")
	maxChars := fs.Int("max-chars", 0, "the longest passage to accept, cut at a boundary")
	function := fs.Int("function", 0, "how many of the commonest syllables are never blanked")
	band := fs.Int("band", 0, "how far up and down the frequency ranking a wrong answer may be drawn from")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: gao dien build -count FILES [flags] <file...>

Turns documents into questions. Each item is a passage with one syllable taken
out, four candidates for what it was, and the identity of the document it came
from.

The frequency ranking is counted over -count and the items are built out of the
files given as arguments, and those two sets have to be different. A ranking
that saw the passage is a ranking that chose the wrong answers with the right
one in view.

Three things make this benchmark measure something other than what it says, and
each is refused rather than warned about. A blank over one of the commonest
syllables is answered by grammar, so the top %d are never taken out. Wrong
answers picked at random let a model win by always choosing the commonest
candidate, so they are drawn from the ranks nearest the answer and the answer's
own rank among them is spread evenly across the set. A candidate that is the
answer with different marks turns the item into diacritic restoration, which is
what 'gao dau' measures, so it is refused.

The rejections are printed per reason, because a builder that quietly drops nine
documents in ten is a builder nobody can debug.

flags:
`, dien.Default().Function)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 || *count == "" {
		fs.Usage()
		return 2
	}

	counted := strings.Split(*count, ",")
	for _, name := range counted {
		for _, from := range fs.Args() {
			if name == from {
				fmt.Fprintf(stderr, "gao dien build: %s is both counted and built from, and a ranking that saw the passage chose the wrong answers with the right one in view\n", name)
				return 1
			}
		}
	}

	v := dien.NewVocabulary()
	pages, bare := 0, 0
	for _, name := range counted {
		err := eachIdentifiedDocument(name, func(_ doc.Hash, text string) error {
			if v.Add(text) {
				pages++
			} else {
				bare++
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gao dien build: %s: %v\n", name, err)
			return 1
		}
	}
	if v.Size() == 0 {
		fmt.Fprintln(stderr, "gao dien build: nothing was counted, so there is no ranking to draw wrong answers from")
		return 1
	}

	b := dien.NewBuilder(dien.Options{
		Sample:   *sample,
		MinChars: *minChars,
		MaxChars: *maxChars,
		Function: *function,
		Band:     *band,
	}, v)

	w := stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(stderr, "gao dien build: %v\n", err)
			return 1
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	enc := json.NewEncoder(w)

	// The limit has to stop the scan rather than be checked after it, because a
	// part is millions of documents and -n 4000 should read four thousand of
	// them and not the part.
	errEnough := errors.New("enough")
	for _, name := range fs.Args() {
		err := eachIdentifiedDocument(name, func(id doc.Hash, text string) error {
			it, _, ok := b.Add(id, text)
			if !ok {
				return nil
			}
			if err := enc.Encode(it); err != nil {
				return err
			}
			if *limit > 0 && len(b.Items()) >= *limit {
				return errEnough
			}
			return nil
		})
		if errors.Is(err, errEnough) {
			break
		}
		if err != nil {
			fmt.Fprintf(stderr, "gao dien build: %s: %v\n", name, err)
			return 1
		}
	}

	report := stdout
	if *out == "" {
		// The items went to stdout, so the accounting cannot follow them there.
		report = stderr
	}
	fmt.Fprintf(report, "the ranking holds %d syllables, counted over %d documents, with %d refused for being typed without marks\n",
		v.Size(), pages, bare)
	fmt.Fprintf(report, "%s: %d items from %d documents\n", dien.Name, len(b.Items()), b.Read())
	for _, r := range dien.Reasons() {
		fmt.Fprintf(report, "  %-20s %d\n", r, b.Rejected(r))
	}
	if len(b.Items()) == 0 {
		fmt.Fprintln(stderr, "\nno item was built, so there is nothing to hold out and nothing to score")
		return 1
	}
	fmt.Fprintln(report, "\nhold these document identities out of the corpus before training against this set")
	return 0
}

func runDienBaseline(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dien baseline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	items := fs.String("items", "", "the task set to score against, as written by 'gao dien build'")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: gao dien baseline -items SET <file...>

Scores the answer a model has to beat before its own number means anything:
pick the most common of the four candidates and never read the passage.

By construction that scores chance, because the answer sits at every frequency
position among its candidates equally often. Running it is the point. A build
that broke the spread shows up here as the baseline scoring well, and a
benchmark the unigram distribution can win looks exactly like a benchmark a
model is winning.

The files counted here are the ones the ranking was counted over at build time.
Reading nothing scores %.1f%%.

flags:
`, 100*dien.Chance)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 || *items == "" {
		fs.Usage()
		return 2
	}

	set, err := readDienItems(*items)
	if err != nil {
		fmt.Fprintf(stderr, "gao dien baseline: %v\n", err)
		return 1
	}

	v := dien.NewVocabulary()
	for _, name := range fs.Args() {
		err := eachIdentifiedDocument(name, func(_ doc.Hash, text string) error {
			v.Add(text)
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gao dien baseline: %s: %v\n", name, err)
			return 1
		}
	}
	if v.Size() == 0 {
		fmt.Fprintln(stderr, "gao dien baseline: nothing was counted, so there is no ranking to pick the commonest candidate from")
		return 1
	}

	r := dien.NewReport(may.Label())
	for _, it := range set {
		r.Add(dien.Grade(it, dien.Frequent(v, it)))
	}
	fmt.Fprint(stdout, r.String())
	fmt.Fprintln(stdout, "a model that does not clear this line has learned which syllables are common and not the language")
	return 0
}

func runDienGrade(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dien grade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	items := fs.String("items", "", "the task set the answers are against")
	box := fs.String("box", may.Label(), "the box the run happened on, which is published with the score")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: gao dien grade -items SET <answers.jsonl>

Scores a file of answers. Each line is a JSON object with a doc_id and either a
choice, which is the index of the candidate the model picked, or an answer,
which is the candidate itself. An item with no answer is scored wrong, because a
model that declined has not earned the item.

The report carries the box it was produced on. This benchmark exists to be run
forty times and compared, and a score with no hardware behind it cannot be
compared with anything.

It also carries how the run did at each frequency position of the answer. A run
that only wins the items where the answer was the commonest candidate has
learned the unigram distribution, and that shows in the breakdown rather than in
the headline number.

Reading nothing scores %.1f%%.

flags:
`, 100*dien.Chance)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *items == "" {
		fs.Usage()
		return 2
	}

	set, err := readDienItems(*items)
	if err != nil {
		fmt.Fprintf(stderr, "gao dien grade: %v\n", err)
		return 1
	}
	answers, err := readDienAnswers(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao dien grade: %v\n", err)
		return 1
	}

	r := dien.NewReport(*box)
	var missing int
	for _, it := range set {
		a, ok := answers[it.DocID]
		if !ok {
			missing++
		}
		r.Add(dien.Grade(it, a.pick(it)))
	}

	if *asJSON {
		return printJSON(stdout, stderr, r)
	}
	fmt.Fprint(stdout, r.String())
	if missing > 0 {
		fmt.Fprintf(stdout, "%d items had no answer and are scored wrong.\n", missing)
	}
	if extra := len(answers) - (len(set) - missing); extra > 0 {
		fmt.Fprintf(stdout, "%d answers do not match any item and were ignored.\n", extra)
	}
	return 0
}

func runDienValidate(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("dien validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the verdict as JSON")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: gao dien validate [-json] <recipes.json>

Measures whether the proxy agrees with the benchmark it stands in for.

The file is a JSON array of recipes, each with a name, the score %s gave it at
ablation scale, the score the full scale evaluation gave it, and the box each of
those two came off.

The number that comes out is a rank correlation, and the number beside it is how
often the two rankings pick the same winner out of a pair, which is the question
anybody running a slate actually has.

Below %.2f the slate is reported as exploratory rather than decisive, every
threshold it set falls back to a published default, and every one of those ships
flagged as unvalidated. That is the kill criterion, and it is measured here so
that the run which settles it is the run that reports it.

flags:
`, dien.Name, dien.KillCorrelation)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	b, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao dien validate: %v\n", err)
		return 1
	}
	var recipes []dien.Recipe
	if err := json.Unmarshal(b, &recipes); err != nil {
		fmt.Fprintf(stderr, "gao dien validate: %s: %v\n", fs.Arg(0), err)
		return 1
	}

	v, err := dien.Validate(recipes)
	if err != nil {
		fmt.Fprintf(stderr, "gao dien validate: %v\n", err)
		for _, p := range dien.CheckRecipes(recipes) {
			fmt.Fprintf(stderr, "  %s\n", p)
		}
		return 1
	}

	if *asJSON {
		return printJSON(stdout, stderr, v)
	}
	fmt.Fprint(stdout, v.String())
	return 0
}

// A dienAnswer is one line of what a model produced. It carries either the index
// of the candidate or the candidate itself, because a scoring harness that
// argmaxes over four continuations has the index and one that writes out the
// text has the text, and neither should have to convert.
type dienAnswer struct {
	DocID  doc.Hash `json:"doc_id"`
	Choice *int     `json:"choice"`
	Answer string   `json:"answer"`
}

// pick is the index this answer names, or one that matches nothing when the
// answer is absent or names a candidate the item does not offer.
func (a dienAnswer) pick(it dien.Item) int {
	if a.Choice != nil {
		return *a.Choice
	}
	for i, s := range it.Choices {
		if s == a.Answer {
			return i
		}
	}
	return -1
}

func readDienItems(path string) ([]dien.Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []dien.Item
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		if len(s.Bytes()) == 0 {
			continue
		}
		var it dien.Item
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

func readDienAnswers(path string) (map[doc.Hash]dienAnswer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := map[doc.Hash]dienAnswer{}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		if len(s.Bytes()) == 0 {
			continue
		}
		var a dienAnswer
		if err := json.Unmarshal(s.Bytes(), &a); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out[a.DocID] = a
	}
	return out, s.Err()
}
