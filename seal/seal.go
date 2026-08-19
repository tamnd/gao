// Package seal fixes the evaluation harness before any result exists.
//
// Chốt sổ is to close the ledger: what is written is written, and nothing goes
// in after. That is the whole idea here. The continued pretraining slice
// compares three arms, one of which is gao's own corpus, and the person running
// that comparison is the person who wants gao to win. Everything that decides
// what the comparison says is written down and hashed before a single number
// exists, so that a benchmark added after the results are in is visible as a
// changed digest rather than invisible as a better table.
//
// The digest is the mechanism. A published result carries the digest of the
// harness it was scored under. Two result sets whose digests differ were not
// measuring the same thing, whatever their columns are called, and they say so
// themselves without anybody having to remember.
//
// What goes in the digest is chosen carefully. The prompt is in it, verbatim,
// because the same benchmark asked two ways is two measurements and the gap
// between them is often larger than the gap between two models. The few shot
// count is in it and so is the seed the examples are drawn with, because a
// different draw is a different benchmark. The metric is in it, and the rule for
// getting an answer out of the output, which is where a surprising amount of a
// score actually comes from.
//
// The note is not in the digest. Writing a clearer sentence about why a task is
// on the harness does not change the task, and a rule that punishes somebody for
// improving an explanation teaches them to stop writing explanations.
//
// The arms are in the harness for the same reason the tasks are. An arm dropped
// after the numbers exist is the same offense as a benchmark added after them,
// and it is the easier of the two to commit by accident.
package seal

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	_ "embed"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/pick"
)

// Metrics a task can be scored by. Every one of them is a rate between zero and
// one, chrF included, which is conventionally quoted out of a hundred and is
// stored here as a fraction so that no table has to carry two scales.
const (
	// Accuracy is the share of items answered correctly, which is what every
	// multiple choice benchmark reports.
	Accuracy = "accuracy"

	// ExactMatch is the share of items whose answer matched the reference
	// exactly after normalization.
	ExactMatch = "exact-match"

	// F1 is the token overlap score extractive reading comprehension is scored
	// by, where a partially right span is worth something and exact match says
	// it is worth nothing.
	F1 = "f1"

	// PassRate is the share of programs that pass their tests on the first
	// sample, which is pass@1 under its plainer name.
	PassRate = "pass-rate"

	// ChrF is the character n-gram translation score, which is used here rather
	// than BLEU because BLEU counts word n-grams and Vietnamese writes spaces
	// between syllables, so every word based translation metric measures
	// something slightly different on Vietnamese than it does on English.
	ChrF = "chrf"

	// DER is diacritic error rate, and it is the one metric here where a
	// smaller number is a better model.
	DER = "der"
)

// Extraction rules, which say how an answer is taken out of what the model
// wrote. This is not a detail. On a multiple choice benchmark the difference
// between reading the first letter and scoring the options by likelihood can
// move a result several points, and a harness that does not pin it has not
// pinned the measurement.
const (
	// FirstOption takes the first option letter that appears in the output.
	FirstOption = "first-option"

	// Likelihood does not read the output at all. It scores each option under
	// the model and takes the argmax, which is what a base model can do and
	// what an instruction tuned model is not being asked to do here.
	Likelihood = "likelihood"

	// FirstLine takes the first line of the output.
	FirstLine = "first-line"

	// Whole takes everything the model wrote.
	Whole = "whole"

	// CodeBlock takes the first fenced code block, and the whole output when
	// there is no fence.
	CodeBlock = "code-block"
)

// A Task is one benchmark exactly as it will be run.
type Task struct {
	// Benchmark is the name on the nhat roster. The harness does not carry the
	// benchmark's revision or its origin, because the roster carries both and a
	// second copy is a second thing to keep in step.
	Benchmark string `json:"benchmark"`

	Metric string `json:"metric"`

	// Shots is how many examples go in the prompt before the item.
	Shots int `json:"shots"`

	// Seed is what the examples are drawn with, which matters exactly as much
	// as how many of them there are.
	Seed int64 `json:"seed"`

	// Prompt is the template, verbatim, with {{item}} where the item goes and
	// {{shots}} where the examples go.
	Prompt string `json:"prompt"`

	Extract string `json:"extract"`

	// Note is why this task is on the harness and how it is meant to be read.
	// It is deliberately outside the digest.
	Note string `json:"note,omitempty"`
}

// A Harness is the whole comparison, closed.
type Harness struct {
	// Version is the day it was closed.
	Version string `json:"version"`

	// Roster is the nhat roster version the benchmark names were checked
	// against, so that a harness closed today can be read years later against
	// the roster it was closed against rather than against a later one.
	Roster string `json:"roster"`

	Note string `json:"note,omitempty"`

	// Arms are the runs this will judge, named before any of them exists.
	Arms []string `json:"arms"`

	Tasks []Task `json:"tasks"`
}

//go:embed harness.json
var harnessJSON []byte

// Fixed is the harness in the repository, which is the closed one.
func Fixed() (Harness, error) {
	return Decode(strings.NewReader(string(harnessJSON)))
}

// Read reads a harness from a file.
func Read(path string) (Harness, error) {
	f, err := os.Open(path)
	if err != nil {
		return Harness{}, err
	}
	defer func() { _ = f.Close() }()
	return Decode(f)
}

// Decode reads a harness from JSON and checks it.
func Decode(r io.Reader) (Harness, error) {
	var h Harness
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return Harness{}, fmt.Errorf("chot: reading the harness: %w", err)
	}
	if err := h.check(); err != nil {
		return Harness{}, err
	}
	return h, nil
}

func (h Harness) check() error {
	if h.Version == "" {
		return fmt.Errorf("chot: the harness has no version, and an undated harness cannot be shown to predate anything")
	}
	if h.Roster == "" {
		return fmt.Errorf("chot: the harness names no roster version, so its benchmark names are checked against nothing")
	}
	if len(h.Arms) < 2 {
		return fmt.Errorf("chot: %d arms, and a comparison needs at least two", len(h.Arms))
	}
	seen := map[string]bool{}
	for _, a := range h.Arms {
		if a == "" {
			return fmt.Errorf("chot: an arm with no name")
		}
		if seen[a] {
			return fmt.Errorf("chot: %s is named twice in the arms", a)
		}
		seen[a] = true
	}
	if len(h.Tasks) == 0 {
		return fmt.Errorf("chot: the harness holds no tasks")
	}
	seen = map[string]bool{}
	for _, t := range h.Tasks {
		if err := t.check(); err != nil {
			return err
		}
		if seen[t.Benchmark] {
			return fmt.Errorf("chot: %s appears twice, and one benchmark scored two ways is two benchmarks with one name", t.Benchmark)
		}
		seen[t.Benchmark] = true
	}
	return nil
}

func (t Task) check() error {
	if t.Benchmark == "" {
		return fmt.Errorf("chot: a task with no benchmark")
	}
	switch t.Metric {
	case Accuracy, ExactMatch, F1, PassRate, ChrF, DER:
	default:
		return fmt.Errorf("chot: %s is scored by %q, which is not a metric this harness knows how to read", t.Benchmark, t.Metric)
	}
	switch t.Extract {
	case FirstOption, Likelihood, FirstLine, Whole, CodeBlock:
	default:
		return fmt.Errorf("chot: %s takes its answer by %q, which is not a rule this harness knows", t.Benchmark, t.Extract)
	}
	if t.Shots < 0 {
		return fmt.Errorf("chot: %s asks for %d shots", t.Benchmark, t.Shots)
	}
	if t.Shots > 0 && t.Seed == 0 {
		return fmt.Errorf("chot: %s draws %d shots with no seed, and shots drawn differently are a different benchmark", t.Benchmark, t.Shots)
	}
	if strings.TrimSpace(t.Prompt) == "" {
		return fmt.Errorf("chot: %s has no prompt, and a benchmark without its prompt is not pinned", t.Benchmark)
	}
	if !strings.Contains(t.Prompt, "{{item}}") {
		return fmt.Errorf("chot: the prompt for %s has nowhere to put the item", t.Benchmark)
	}
	if t.Shots > 0 && !strings.Contains(t.Prompt, "{{shots}}") {
		return fmt.Errorf("chot: the prompt for %s draws %d shots and has nowhere to put them", t.Benchmark, t.Shots)
	}
	return nil
}

// Task returns the task for a benchmark.
func (h Harness) Task(benchmark string) (Task, bool) {
	for _, t := range h.Tasks {
		if t.Benchmark == benchmark {
			return t, true
		}
	}
	return Task{}, false
}

// Has reports whether an arm is one this harness judges.
func (h Harness) Has(arm string) bool {
	for _, a := range h.Arms {
		if a == arm {
			return true
		}
	}
	return false
}

// Digest is the identity of the harness, and it is what a published result
// carries so that the result can say what it was measured under.
func (h Harness) Digest() doc.Hash { return doc.SumString(h.canonical()) }

// canonical is the byte form the digest is taken over.
//
// Every value is written with its length in front of it, so that no value can
// be spelled to look like the end of one field and the start of another. That
// is the entire reason a canonical form exists rather than hashing the JSON: two
// encoders disagree about spacing and key order, and a digest that moves when
// somebody reformats a file is a digest nobody trusts for long.
func (h Harness) canonical() string {
	var b strings.Builder
	write(&b, "harness", h.Version)
	write(&b, "roster", h.Roster)

	arms := append([]string(nil), h.Arms...)
	sort.Strings(arms)
	for _, a := range arms {
		write(&b, "arm", a)
	}

	tasks := append([]Task(nil), h.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Benchmark < tasks[j].Benchmark })
	for _, t := range tasks {
		write(&b, "task", t.Benchmark)
		write(&b, "metric", t.Metric)
		write(&b, "shots", fmt.Sprint(t.Shots))
		write(&b, "seed", fmt.Sprint(t.Seed))
		write(&b, "extract", t.Extract)
		write(&b, "prompt", t.Prompt)
	}
	return b.String()
}

func write(b *strings.Builder, key, value string) {
	fmt.Fprintf(b, "%s %d:%s\n", key, len(value), value)
}

// Against checks the harness's benchmark names against a roster and returns
// what does not line up.
//
// A harness naming a benchmark the roster does not hold is the more interesting
// fault of the two, because the roster only grows, so a name it does not have is
// a name nobody has agreed gao is judged on.
func (h Harness) Against(r pick.Roster) []string {
	var faults []string
	if r.Version != h.Roster {
		faults = append(faults, fmt.Sprintf("the harness was closed against roster %s and this is roster %s",
			h.Roster, r.Version))
	}
	held := map[string]bool{}
	for _, e := range r.Benchmarks {
		held[e.Name] = true
	}
	for _, t := range h.Tasks {
		if !held[t.Benchmark] {
			faults = append(faults, fmt.Sprintf("%s is on the harness and not on the roster", t.Benchmark))
		}
	}
	return faults
}

// Unpinned is the benchmarks this harness runs whose revision the roster has not
// fixed. They are what stands between the harness and a result anybody can
// reproduce, and they are listed rather than counted so that the work is
// nameable.
func (h Harness) Unpinned(r pick.Roster) []string {
	version := map[string]string{}
	for _, e := range r.Benchmarks {
		version[e.Name] = e.Version
	}
	var out []string
	for _, t := range h.Tasks {
		if v, ok := version[t.Benchmark]; ok && v == pick.Unpinned {
			out = append(out, t.Benchmark)
		}
	}
	sort.Strings(out)
	return out
}

// Better reports whether a is a better score than b under a metric. It exists
// because [DER] runs the other way, and a comparison that gets that backwards
// reads as a clean win for whichever arm is worst at Vietnamese diacritics.
func Better(metric string, a, b float64) bool {
	if metric == DER {
		return a < b
	}
	return a > b
}
