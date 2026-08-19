// Package ask is vi-longdoc-qa: whether a question about a long document needs
// the document.
//
// Hỏi is to ask. The benchmark is easy to describe and easy to build wrong.
// Take a long Vietnamese document, write questions about it, score the answers.
// The trouble is that almost every long document question set that gets built
// this way measures something other than long context, and the three ways it
// goes wrong are all invisible in the finished set.
//
// The first is that the question can be answered without the document at all. A
// question about a well known decree is a question a model has read the answer
// to somewhere, and a set full of those measures what the pretraining corpus
// held. It is not a hard failure to catch and almost nobody catches it, because
// catching it means running the questions with no document attached and
// throwing away the ones that were answered anyway. That run is recorded here
// per question rather than described in a paper, and a question that survived
// it without being asked closed book is not admitted.
//
// The second is that the answer sits in one span. A question whose answer is a
// contiguous stretch of the document is a retrieval question, and retrieval in a
// long context is what gao needle already measures with a needle. A long document
// question has to need more than one place in the document, which means the
// spans are part of the record and the count of them is checked. Two spans a
// paragraph apart are still one span for this purpose, so the spread between the
// first and the last is checked too: a question whose evidence all sits inside
// the opening pages is answered by a model that reads the opening pages.
//
// The third is the ladder. S8 extends context in three steps, 4k to 32k to
// 131k, and the only way to know whether a step worked is to have questions that
// live above it. A set whose documents are all forty thousand tokens long says
// nothing about the last rung, so the rungs are declared, every question is
// placed on one, and a set that leaves a rung nearly empty is reported as a set
// that cannot answer the question it was built for.
//
// One more thing is checked and it is the dull one. A benchmark where a tenth
// of the questions come off the same document is a benchmark about that
// document. Vietnamese long documents that are freely redistributable are
// mostly legal and administrative, so this failure is the natural state of the
// set rather than a mistake somebody has to make.
package ask

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// Rungs are the context lengths S8 extends to, in tokens. Every question sits
// on the highest rung its document clears, and a set that cannot fill a rung
// cannot say whether the extension to it worked.
var Rungs = []int{32_000, 65_536, 131_072}

// MinRung is the share of the set each rung has to hold. Three rungs at a fifth
// each leaves room for the shape of what is available to decide the rest,
// without letting the cheapest rung become the whole set.
const MinRung = 0.20

// MinTokens is the shortest document a long document question can be about. It
// is the first rung, because a question over a document that fits in the base
// context is a reading comprehension question, which gao already has in the
// finetuning slate.
const MinTokens = 32_000

// MinSpans is how many places in the document the answer has to need. One is
// retrieval and gao needle measures retrieval.
const MinSpans = 2

// MinReach is how much of the document has to sit between the first span the
// answer needs and the last. Evidence bunched into one part of a long document
// is a short document question with padding after it.
const MinReach = 0.35

// MinGraders is how many people have to have read a question and agreed on its
// answer. Two is the fewest that can disagree, and disagreement is the signal
// worth having: a question two Vietnamese speakers answer differently is a
// question, not a test item.
const MinGraders = 2

// MaxPerDocument is the share of the set one document may supply. Long
// Vietnamese documents that can be redistributed are mostly legal, so a set
// built without this ceiling becomes a legal reading benchmark by accident.
const MaxPerDocument = 0.05

// Target is the size the set is composed to.
const Target = 600

// The kinds of question in the set, which are the kinds that need more than one
// place in a document by construction rather than by hope.
const (
	Synthesis  = "tong-hop" // two statements combined into one answer
	Comparison = "so-sanh"  // two things in the document held against each other
	Sequence   = "trinh-tu" // what happened in what order, across the document
	Amendment  = "sua-doi"  // a clause and the later clause that changed it
	Counting   = "dem-so"   // how many of a thing the document holds
)

// Kinds is the roster, in the order the report prints them.
var Kinds = []string{Synthesis, Comparison, Sequence, Amendment, Counting}

// A Span is where in the document, in tokens, a piece of the answer lives.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Len is the span's length in tokens.
func (s Span) Len() int { return s.End - s.Start }

// A Question is one item of the set, and the evidence that it is one.
type Question struct {
	ID       string `json:"id"`
	Document string `json:"document"`
	Kind     string `json:"kind"`

	// Tokens is the length of the document the question is about, counted with
	// the gao tokenizer, since a length in characters is a different number in
	// Vietnamese than in English and the rungs are in tokens.
	Tokens int `json:"tokens"`

	// Spans are the places the answer needs, in the order they appear.
	Spans []Span `json:"spans"`

	// ClosedBook records that the question was put to a model with no document
	// attached, and Recalled records that the model answered it anyway. A
	// question that was never asked closed book is not admitted, because the
	// check is cheap and the failure it catches is the one that invalidates the
	// whole set.
	ClosedBook bool `json:"closed_book"`
	Recalled   bool `json:"recalled"`

	// Graders is how many people read the question and Agreed is how many of
	// them gave the same answer.
	Graders int `json:"graders"`
	Agreed  int `json:"agreed"`
}

// Rung is the context length this question's document needs, which is the
// highest declared rung it clears. A document longer than the top rung sits on
// the top rung, since that is the largest context anything will be run at.
func (q Question) Rung() int {
	rung := 0
	for _, r := range Rungs {
		if q.Tokens >= r {
			rung = r
		}
	}
	return rung
}

// Reach is the share of the document lying between the start of the first span
// the answer needs and the end of the last.
func (q Question) Reach() float64 {
	if len(q.Spans) < 2 || q.Tokens <= 0 {
		return 0
	}
	first, last := q.Spans[0].Start, q.Spans[0].End
	for _, s := range q.Spans {
		first = min(first, s.Start)
		last = max(last, s.End)
	}
	return float64(last-first) / float64(q.Tokens)
}

// Settled reports whether the graders came back with one answer.
func (q Question) Settled() bool { return q.Graders >= MinGraders && q.Agreed == q.Graders }

// Admitted reports whether the question is a long document question that two
// people agree on.
func (q Question) Admitted() bool { return len(q.Blocking()) == 0 }

// Blocking is every reason this question is not one, written as sentences.
func (q Question) Blocking() []string {
	var why []string
	where := q.ID
	if where == "" {
		where = "a question with no identifier"
	}
	if q.Document == "" {
		why = append(why, fmt.Sprintf("%s does not say which document it is about", where))
	}
	if !contains(Kinds, q.Kind) {
		why = append(why, fmt.Sprintf("%s is of kind %q, which is not one of the five the set is composed from", where, q.Kind))
	}
	if q.Tokens < MinTokens {
		why = append(why, fmt.Sprintf(
			"%s is about a document of %s tokens, under the %s that is the first rung, so it is a reading comprehension question",
			where, thousands(int64(q.Tokens)), thousands(MinTokens)))
	}
	if len(q.Spans) < MinSpans {
		why = append(why, fmt.Sprintf(
			"%s needs %s of the document, and one span is retrieval, which vi-needle already measures",
			where, count(len(q.Spans), "span")))
	}
	for _, s := range q.Spans {
		if s.Len() <= 0 || s.Start < 0 || s.End > q.Tokens {
			why = append(why, fmt.Sprintf("%s cites tokens %d to %d of a document that is %d long", where, s.Start, s.End, q.Tokens))
			break
		}
	}
	if len(q.Spans) >= MinSpans && q.Reach() < MinReach {
		why = append(why, fmt.Sprintf(
			"%s has all its evidence inside %.0f%% of the document against a %.0f%% line, so a model that reads the opening answers it",
			where, q.Reach()*100, MinReach*100))
	}
	if !q.ClosedBook {
		why = append(why, fmt.Sprintf(
			"%s was never put to a model without the document, so nobody knows whether it needs one", where))
	}
	if q.Recalled {
		why = append(why, fmt.Sprintf(
			"%s was answered with no document attached, which makes it a memory question rather than a reading one", where))
	}
	if q.Graders < MinGraders {
		why = append(why, fmt.Sprintf("%s was read by %s, and two is the fewest that can disagree", where, count(q.Graders, "grader")))
	} else if q.Agreed < q.Graders {
		why = append(why, fmt.Sprintf(
			"%s got %d answers from %d readers, so it is a question rather than a test item", where, q.Graders-q.Agreed+1, q.Graders))
	}
	return why
}

// A Row is one rung of the ladder, or one kind, with what the set holds of it.
type Row struct {
	Name      string  `json:"name"`
	Questions int     `json:"questions"`
	Share     float64 `json:"share"`
	Floor     float64 `json:"floor,omitempty"`
	Reach     float64 `json:"reach"`
	Spans     float64 `json:"spans"`
	Holds     bool    `json:"holds"`
}

// A Set is the benchmark.
type Set struct {
	Name      string
	Questions []Question
}

// In returns the questions that are admitted.
func (s Set) In() []Question {
	out := make([]Question, 0, len(s.Questions))
	for _, q := range s.Questions {
		if q.Admitted() {
			out = append(out, q)
		}
	}
	return out
}

// Out returns the questions that are not, which are reported rather than
// deleted, since the count of them is the honest description of how expensive
// this set is to build.
func (s Set) Out() []Question {
	out := make([]Question, 0, len(s.Questions))
	for _, q := range s.Questions {
		if !q.Admitted() {
			out = append(out, q)
		}
	}
	return out
}

// Recalled counts the questions a model answered with no document attached,
// which is the number worth publishing on its own.
func (s Set) Recalled() int {
	n := 0
	for _, q := range s.Questions {
		if q.Recalled {
			n++
		}
	}
	return n
}

// Ladder is the set broken out by rung.
func (s Set) Ladder() []Row {
	in := s.In()
	out := make([]Row, 0, len(Rungs))
	for _, r := range Rungs {
		row := Row{Name: thousands(int64(r)), Floor: MinRung}
		var reach, spans float64
		for _, q := range in {
			if q.Rung() != r {
				continue
			}
			row.Questions++
			reach += q.Reach()
			spans += float64(len(q.Spans))
		}
		row.Share = divide(row.Questions, len(in))
		row.Reach = divide64(reach, row.Questions)
		row.Spans = divide64(spans, row.Questions)
		row.Holds = row.Share >= MinRung
		out = append(out, row)
	}
	return out
}

// Composition is the set broken out by kind.
func (s Set) Composition() []Row {
	in := s.In()
	out := make([]Row, 0, len(Kinds))
	for _, kind := range Kinds {
		row := Row{Name: kind}
		var reach, spans float64
		for _, q := range in {
			if q.Kind != kind {
				continue
			}
			row.Questions++
			reach += q.Reach()
			spans += float64(len(q.Spans))
		}
		row.Share = divide(row.Questions, len(in))
		row.Reach = divide64(reach, row.Questions)
		row.Spans = divide64(spans, row.Questions)
		row.Holds = row.Questions > 0
		out = append(out, row)
	}
	return out
}

// Thin names the rungs the set cannot fill.
func (s Set) Thin() []string {
	var out []string
	for _, row := range s.Ladder() {
		if !row.Holds {
			out = append(out, fmt.Sprintf("%s tokens holds %s of the set against a %.0f%% floor", row.Name, percent(row.Share), MinRung*100))
		}
	}
	return out
}

// Heaviest names the document the set leans on hardest and what share of it that
// document supplies.
func (s Set) Heaviest() (string, float64) {
	counts := map[string]int{}
	for _, q := range s.In() {
		counts[q.Document]++
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	var worst string
	var most int
	for _, name := range names {
		if counts[name] > most {
			worst, most = name, counts[name]
		}
	}
	return worst, divide(most, len(s.In()))
}

// Documents counts the distinct documents the admitted questions come off.
func (s Set) Documents() int {
	seen := map[string]bool{}
	for _, q := range s.In() {
		seen[q.Document] = true
	}
	return len(seen)
}

// Reach is the mean share of a document an admitted question spans.
func (s Set) Reach() float64 {
	var total float64
	for _, q := range s.In() {
		total += q.Reach()
	}
	return divide64(total, len(s.In()))
}

// Blocking is every reason this is not a benchmark yet.
func (s Set) Blocking() []string {
	if len(s.Questions) == 0 {
		return []string{"no questions were read, so there is nothing to check"}
	}
	var why []string
	if n := len(s.In()); n == 0 {
		why = append(why, "no question in the file survived its own checks, so the set is a list of drafts")
		return why
	}
	if doc, share := s.Heaviest(); share > MaxPerDocument {
		why = append(why, fmt.Sprintf(
			"%s supplies %s of the set against a %.0f%% ceiling, so what this measures is that document",
			doc, percent(share), MaxPerDocument*100))
	}
	if n := len(s.In()); math.Abs(float64(n)-Target)/Target > 0.1 {
		why = append(why, fmt.Sprintf(
			"the set admits %s against the %s it was composed to", count(n, "question"), count(Target, "question")))
	}
	return why
}

// Settled reports whether the set is composed rather than collected.
func (s Set) Settled() bool { return len(s.Blocking()) == 0 }

// Holds reports whether the set can answer what it was built to answer, which
// needs it to be composed and to reach every rung.
func (s Set) Holds() bool { return s.Settled() && len(s.Thin()) == 0 }

// Verdict is the set in one sentence.
func (s Set) Verdict() string {
	if why := s.Blocking(); len(why) > 0 {
		return why[0]
	}
	in := s.In()
	head := fmt.Sprintf(
		"%s admits %s of the %s read, over %s, each needing at least two places in its document and %.0f%% of it on average.",
		s.Name, thousands(int64(len(in))), count(len(s.Questions), "question"),
		count(s.Documents(), "document"), s.Reach()*100)
	if n := s.Recalled(); n > 0 {
		head += fmt.Sprintf(
			" %s were answered with no document attached and are out, which is the check most sets of this kind skip.", count(n, "question"))
	}
	if thin := s.Thin(); len(thin) > 0 {
		return head + fmt.Sprintf(
			" The ladder has a hole in it, since %s, so a result here cannot say whether the extension to that length worked.", thin[0])
	}
	return head + " Every rung of the context ladder is filled, so a score on this set separates a model that reads a long document from one that reads the start of it."
}

// ReadSet loads a set from a file of one JSON question per line.
func ReadSet(name, path string) (Set, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Set{}, fmt.Errorf("hoi: %w", err)
	}
	s := Set{Name: name}
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var q Question
		if err := dec.Decode(&q); err != nil {
			return Set{}, fmt.Errorf("hoi: %s line %d: %w", path, i+1, err)
		}
		s.Questions = append(s.Questions, q)
	}
	if len(s.Questions) == 0 {
		return Set{}, fmt.Errorf("hoi: %s holds no questions", path)
	}
	return s, nil
}

func contains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}

func divide(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func divide64(total float64, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return total / float64(whole)
}

func percent(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%s %ss", thousands(int64(n)), noun)
}

func thousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
