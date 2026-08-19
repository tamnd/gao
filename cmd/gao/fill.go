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

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fill"
	"github.com/tamnd/gao/fleet"
)

func runFill(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		fillUsage(stderr)
		return 2
	}
	switch args[0] {
	case "build":
		return runFillBuild(stdout, stderr, args[1:])
	case "baseline":
		return runFillBaseline(stdout, stderr, args[1:])
	case "grade":
		return runFillGrade(stdout, stderr, args[1:])
	case "validate":
		return runFillValidate(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		fillUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gao fill: unknown subcommand %q\n", args[0])
		fillUsage(stderr)
		return 2
	}
}

func fillUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: gao fill <subcommand> [flags] [files...]

Builds and scores %s, the proxy the ablation slate is run against.

The slate is forty training runs and each one has to be scored, so the inner
loop of the whole ablation program is this benchmark. A generative evaluation
with a judge behind it would put an hour and an API bill between every run and
its result. This is four candidate continuations scored by likelihood and an
argmax over them, which is minutes on one card, and the answer key is the page
the passage came off, so it costs nothing to build.

A proxy is only worth running if it agrees with the thing it stands in for.
'gao fill validate' measures that agreement and reports whether the slate can be
presented as having chosen anything.

subcommands:
  build     turn documents into questions and their answers
  baseline  score the answer a model has to beat before its number means anything
  grade     score a file of answers against the task set
  validate  measure whether the proxy agrees with full scale results

Files are Parquet parts or text files, and a text file is one document.

run 'gao fill <subcommand> -h' for the flags of a single subcommand.
`, fill.Name)
}

func runFillBuild(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("fill build", flag.ContinueOnError)
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
		fmt.Fprintf(stderr, `usage: gao fill build -count FILES [flags] <file...>

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
what 'gao mark' measures, so it is refused.

The rejections are printed per reason, because a builder that quietly drops nine
documents in ten is a builder nobody can debug.

flags:
`, fill.Default().Function)
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
				fmt.Fprintf(stderr, "gao fill build: %s is both counted and built from, and a ranking that saw the passage chose the wrong answers with the right one in view\n", name)
				return 1
			}
		}
	}

	v := fill.NewVocabulary()
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
			fmt.Fprintf(stderr, "gao fill build: %s: %v\n", name, err)
			return 1
		}
	}
	if v.Size() == 0 {
		fmt.Fprintln(stderr, "gao fill build: nothing was counted, so there is no ranking to draw wrong answers from")
		return 1
	}

	b := fill.NewBuilder(fill.Options{
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
			fmt.Fprintf(stderr, "gao fill build: %v\n", err)
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
			fmt.Fprintf(stderr, "gao fill build: %s: %v\n", name, err)
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
	fmt.Fprintf(report, "%s: %d items from %d documents\n", fill.Name, len(b.Items()), b.Read())
	for _, r := range fill.Reasons() {
		fmt.Fprintf(report, "  %-20s %d\n", r, b.Rejected(r))
	}
	if len(b.Items()) == 0 {
		fmt.Fprintln(stderr, "\nno item was built, so there is nothing to hold out and nothing to score")
		return 1
	}
	fmt.Fprintln(report, "\nhold these document identities out of the corpus before training against this set")
	return 0
}

func runFillBaseline(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("fill baseline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	items := fs.String("items", "", "the task set to score against, as written by 'gao fill build'")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: gao fill baseline -items SET <file...>

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
`, 100*fill.Chance)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 || *items == "" {
		fs.Usage()
		return 2
	}

	set, err := readFillItems(*items)
	if err != nil {
		fmt.Fprintf(stderr, "gao fill baseline: %v\n", err)
		return 1
	}

	v := fill.NewVocabulary()
	for _, name := range fs.Args() {
		err := eachIdentifiedDocument(name, func(_ doc.Hash, text string) error {
			v.Add(text)
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gao fill baseline: %s: %v\n", name, err)
			return 1
		}
	}
	if v.Size() == 0 {
		fmt.Fprintln(stderr, "gao fill baseline: nothing was counted, so there is no ranking to pick the commonest candidate from")
		return 1
	}

	r := fill.NewReport(fleet.Label())
	for _, it := range set {
		r.Add(fill.Grade(it, fill.Frequent(v, it)))
	}
	fmt.Fprint(stdout, r.String())
	fmt.Fprintln(stdout, "a model that does not clear this line has learned which syllables are common and not the language")
	return 0
}

func runFillGrade(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("fill grade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	items := fs.String("items", "", "the task set the answers are against")
	box := fs.String("box", fleet.Label(), "the box the run happened on, which is published with the score")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: gao fill grade -items SET <answers.jsonl>

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
`, 100*fill.Chance)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *items == "" {
		fs.Usage()
		return 2
	}

	set, err := readFillItems(*items)
	if err != nil {
		fmt.Fprintf(stderr, "gao fill grade: %v\n", err)
		return 1
	}
	answers, err := readFillAnswers(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "gao fill grade: %v\n", err)
		return 1
	}

	r := fill.NewReport(*box)
	var missing int
	for _, it := range set {
		a, ok := answers[it.DocID]
		if !ok {
			missing++
		}
		r.Add(fill.Grade(it, a.pick(it)))
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

func runFillValidate(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("fill validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the verdict as JSON")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: gao fill validate [-json] <recipes.json>

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
`, fill.Name, fill.KillCorrelation)
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
		fmt.Fprintf(stderr, "gao fill validate: %v\n", err)
		return 1
	}
	var recipes []fill.Recipe
	if err := json.Unmarshal(b, &recipes); err != nil {
		fmt.Fprintf(stderr, "gao fill validate: %s: %v\n", fs.Arg(0), err)
		return 1
	}

	v, err := fill.Validate(recipes)
	if err != nil {
		fmt.Fprintf(stderr, "gao fill validate: %v\n", err)
		for _, p := range fill.CheckRecipes(recipes) {
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

// A fillAnswer is one line of what a model produced. It carries either the index
// of the candidate or the candidate itself, because a scoring harness that
// argmaxes over four continuations has the index and one that writes out the
// text has the text, and neither should have to convert.
type fillAnswer struct {
	DocID  doc.Hash `json:"doc_id"`
	Choice *int     `json:"choice"`
	Answer string   `json:"answer"`
}

// pick is the index this answer names, or one that matches nothing when the
// answer is absent or names a candidate the item does not offer.
func (a fillAnswer) pick(it fill.Item) int {
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

func readFillItems(path string) ([]fill.Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []fill.Item
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		if len(s.Bytes()) == 0 {
			continue
		}
		var it fill.Item
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

func readFillAnswers(path string) (map[doc.Hash]fillAnswer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := map[doc.Hash]fillAnswer{}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		if len(s.Bytes()) == 0 {
			continue
		}
		var a fillAnswer
		if err := json.Unmarshal(s.Bytes(), &a); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out[a.DocID] = a
	}
	return out, s.Err()
}
