package inspect

// The field of candidate engines, and what separates one of them from the rest.
//
// A gate on one engine says whether that engine works. It does not say the field
// was searched, and the milestone asks for the field: candidate engines evaluated
// against the set, results published including the losers. The losers are the
// part that gets dropped, and dropping them turns a comparison into an
// announcement, because a table with one row in it cannot be argued with.
//
// Three things make a published OCR comparison reproducible, and all three are
// usually missing. The engines have to have read the same pages, since a DER off
// one set and a DER off another are two numbers rather than a difference. The
// gap between the top two has to be larger than what the set can resolve, since
// two engines a tenth of a point apart on two hundred pages are one engine and
// the luckier draw. And the batch size and the memory it held have to be written
// down against the card, since a result at batch 64 on 23.6 GB of a 24 GB card
// is a result that fails the first time anything else touches the GPU.
//
// The cost line is the other half of the S4 gate, which asks the winning path to
// sustain its throughput across a full batch of real documents at a rate that
// finishes the slice in the time the plan allows. That is arithmetic over one
// measured number, pages a second, and it is worth doing early because an engine
// that clears the diacritic gate and reads at a tenth of the rate is not the
// engine this slice ships.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
)

// Reserve is the share of the card a published batch size has to leave free. An
// engine sized to fill the GPU reproduces on an idle machine and nowhere else,
// and gamingpc is also where the classifiers, the tokenizer and every evaluation
// run.
const Reserve = 0.15

// MinField is the smallest number of engines that is a comparison. Below it the
// result is the engine somebody picked, which may well be the right one and is
// not a finding.
const MinField = 3

// Slice is how many pages the plan expects to reach OCR. It is P04-2's ceiling
// over the institutional PDF estimate in S4, which makes it a plan number rather
// than a count, and every cost below moves when the routing distribution is
// measured on real documents.
const Slice int64 = 12_000_000

// Hours is the GPU-hour budget P04-6 sets for the whole extraction stage.
const Hours = 6000.0

// Share is how much of that budget OCR may take. OCR is the bulk of the stage
// and it is not the whole of it, since the router, the legacy transcoder and the
// ASR pass are in the same number, so an engine that spends the stage budget on
// its own has not left room for the rest of the stage.
const Share = 0.75

// Budget is the GPU hours OCR itself has.
func Budget() float64 { return Hours * Share }

// A Candidate is one engine, evaluated once, on one card.
type Candidate struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`

	// Finetuned says whether the engine was tuned on Vietnamese before it was
	// evaluated. P04-4 is specifically about an engine that was not, so a field
	// where only the finetuned entries clear the gate meets the gate and refutes
	// the prediction, and those are two separate answers.
	Finetuned bool `json:"finetuned"`

	// Set and Pages are the evaluation set this reading came off. They are on
	// every candidate rather than on the field so that a comparison across two
	// sets is caught here instead of being averaged.
	Set   string `json:"set"`
	Pages int    `json:"pages"`

	Box string `json:"box"`

	// Batch is how many pages went to the card at once and VRAM is the peak the
	// card held at that batch. Both are the milestone item: a published result
	// reproduces on the same card or it is not a published result.
	Batch int   `json:"batch"`
	VRAM  int64 `json:"vram"`

	// Rate is pages a second, sustained across a full batch rather than measured
	// on the first one, which is the wording the gate uses and the difference
	// between a benchmark and a throughput.
	Rate float64 `json:"rate"`

	Score Score `json:"score"`
}

// Headroom is the share of the card the batch left free.
func (c Candidate) Headroom(card int64) float64 {
	if card <= 0 {
		return 0
	}
	return 1 - float64(c.VRAM)/float64(card)
}

// Cost is the GPU hours this engine needs to read the pages the plan expects to
// reach OCR.
func (c Candidate) Cost(pages int64) float64 {
	if c.Rate <= 0 {
		return 0
	}
	return float64(pages) / c.Rate / 3600
}

// StdErr is how precisely this set can place this engine's diacritic error rate.
//
// DER is a share of the page's marked characters, so its uncertainty is the
// ordinary one for a share: the rate, its complement, and the number of marks it
// was measured over. It is here because the difference between two engines is
// only a difference when it is bigger than this.
func (c Candidate) StdErr() float64 {
	p, n := c.Score.DER(), float64(c.Score.Marked)
	if n <= 0 {
		return 0
	}
	return math.Sqrt(p * (1 - p) / n)
}

// Blocking is every reason this reading cannot go in a published table.
func (c Candidate) Blocking(card int64) []string {
	var why []string
	if c.Engine == "" {
		why = append(why, "a reading with no engine on it cannot be published as a result for one")
		return why
	}
	if c.Version == "" {
		why = append(why, fmt.Sprintf("%s does not say what version was run, and an engine is a different engine two releases later", c.Engine))
	}
	if c.Set == "" || c.Pages <= 0 {
		why = append(why, fmt.Sprintf("%s does not say what it was read on, so there is nothing to compare it against", c.Engine))
	}
	if c.Box == "" {
		why = append(why, fmt.Sprintf("%s does not say what it ran on, and a throughput without a card under it is a number without units", c.Engine))
	}
	switch {
	case c.Batch <= 0:
		why = append(why, fmt.Sprintf(
			"%s does not record its batch size, so nobody can reproduce the result on the same card, which is the whole of what publishing it is for",
			c.Engine))
	case c.VRAM <= 0:
		why = append(why, fmt.Sprintf(
			"%s ran at batch %d and does not record what the card held, so the batch size is a setting rather than a result anybody can repeat",
			c.Engine, c.Batch))
	case card > 0 && c.Headroom(card) < Reserve:
		why = append(why, fmt.Sprintf(
			"%s held %s of a %s card at batch %d, leaving %.0f%% against a reserve of %.0f%%, and a batch sized to fill the card fails the first time anything else is on it",
			c.Engine, gigabytes(c.VRAM), gigabytes(card), c.Batch, 100*c.Headroom(card), 100*Reserve))
	}
	if c.Rate <= 0 {
		why = append(why, fmt.Sprintf(
			"%s does not report a sustained rate, and the gate asks the winning path to hold its throughput across a full batch rather than to clear an error rate",
			c.Engine))
	}
	if c.Score.Chars == 0 {
		why = append(why, fmt.Sprintf("%s has no characters behind its score", c.Engine))
	} else if c.Score.Marked == 0 {
		why = append(why, fmt.Sprintf(
			"%s was scored on %d characters carrying no marks, and a diacritic error rate over nothing is zero for the wrong reason",
			c.Engine, c.Score.Chars))
	}
	return why
}

// A Field is every engine that was tried, including the ones that lost.
type Field struct {
	// Card is the accelerator every candidate has to have run on, in bytes. It is
	// passed in rather than written here, for the same reason [Gate] is: a metric
	// that carries the hardware it assumes is a metric that moves when the
	// hardware does.
	Card int64 `json:"card"`

	// Pages is how many the plan expects to reach OCR, which is what turns a rate
	// into a cost.
	Pages int64 `json:"pages"`

	Gate       Gate        `json:"gate"`
	Candidates []Candidate `json:"candidates"`
}

// Ranked is the field by diacritic error rate, best first, which is the order the
// gate is written in and the order a reader wants.
func (f Field) Ranked() []Candidate {
	out := slices.Clone(f.Candidates)
	slices.SortStableFunc(out, func(a, b Candidate) int {
		switch {
		case a.Score.DER() < b.Score.DER():
			return -1
		case a.Score.DER() > b.Score.DER():
			return 1
		default:
			return strings.Compare(a.Engine, b.Engine)
		}
	})
	return out
}

// Leads is the best reading in the field, and false if the field is empty. It is
// the best reading whether or not it cleared anything, which is the row a reader
// looks at first and is not always the engine that ships.
func (f Field) Leads() (Candidate, bool) {
	if len(f.Candidates) == 0 {
		return Candidate{}, false
	}
	return f.Ranked()[0], true
}

// Winner is the path this slice would actually ship: the best diacritic error
// rate among the engines that clear the gate and read the slice inside the hours
// OCR has. The S4 gate asks for both, so an engine that leads on accuracy and
// takes the whole extraction budget is not the winner, it is the reason there is
// a second column.
func (f Field) Winner() (Candidate, bool) {
	for _, c := range f.Ranked() {
		if len(f.Gate.Check(c.Score)) == 0 && c.Cost(f.Pages) > 0 && c.Cost(f.Pages) <= Budget() {
			return c, true
		}
	}
	return Candidate{}, false
}

// Losers is everything that did not clear the gate, which is the part of the
// table the milestone asks for by name.
func (f Field) Losers() []Candidate {
	var out []Candidate
	for _, c := range f.Ranked() {
		if len(f.Gate.Check(c.Score)) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// Separated reports whether the top two are further apart than the set can
// resolve. A field of one is separated from nothing and reports true, since there
// is no second reading for it to be confused with.
func (f Field) Separated() bool {
	_, _, ok := f.gap()
	return ok
}

// gap is the distance between the top two and the distance the set can resolve,
// and false when the first does not exceed the second.
func (f Field) gap() (got, need float64, ok bool) {
	r := f.Ranked()
	if len(r) < 2 {
		return 0, 0, true
	}
	a, b := r[0], r[1]
	got = b.Score.DER() - a.Score.DER()
	// Two standard errors on the difference of two independent shares, which is
	// the ordinary line for calling one of them smaller than the other.
	need = 2 * math.Sqrt(a.StdErr()*a.StdErr()+b.StdErr()*b.StdErr())
	return got, need, got > need
}

// Blocking is every reason this field is not a comparison.
func (f Field) Blocking() []string {
	if len(f.Candidates) == 0 {
		return []string{"no engine was evaluated, so there is no field here and the gate is still a number in a milestone"}
	}
	var why []string
	seen := map[string]bool{}
	sets, boxes := map[string]bool{}, map[string]bool{}
	pages := map[int]bool{}
	for _, c := range f.Candidates {
		key := c.Engine + " " + c.Version
		if seen[key] {
			why = append(why, fmt.Sprintf("%s appears twice, and two readings by one engine are not two engines", c.Engine))
		}
		seen[key] = true
		if c.Set != "" {
			sets[c.Set] = true
			pages[c.Pages] = true
		}
		if c.Box != "" {
			boxes[c.Box] = true
		}
		why = append(why, c.Blocking(f.Card)...)
	}
	if len(sets) > 1 {
		why = append(why, fmt.Sprintf(
			"the field was read on %s, and two engines scored on different pages are two numbers rather than a difference",
			names(sets)))
	} else if len(pages) > 1 {
		why = append(why, fmt.Sprintf(
			"the field names one set and the readings cover %s of it, so somebody scored a subset and the table compares a whole engine against part of a run",
			counts(pages)))
	}
	if len(boxes) > 1 {
		why = append(why, fmt.Sprintf(
			"the field ran on %s, and a throughput is a fact about a card, so a comparison spread across boxes is a comparison of the boxes",
			names(boxes)))
	}
	if n := len(f.Candidates); n < MinField {
		why = append(why, fmt.Sprintf(
			"%s was evaluated, and a field that small is the engine somebody picked rather than a search for the best one",
			plural(n, "engine")))
	}
	if got, need, ok := f.gap(); !ok {
		r := f.Ranked()
		why = append(why, fmt.Sprintf(
			"%s at %.2f%% and %s at %.2f%% are %.2f points apart on a set that resolves %.2f, so naming one of them the winner is naming which engine drew the easier pages",
			r[0].Engine, 100*r[0].Score.DER(), r[1].Engine, 100*r[1].Score.DER(), 100*got, 100*need))
	}
	return why
}

// Settled reports whether the field is a comparison worth publishing, whatever
// it says.
func (f Field) Settled() bool { return len(f.Blocking()) == 0 }

// Passed reports whether anything in the field cleared the gate, which is E1.
func (f Field) Passed() bool {
	l, ok := f.Leads()
	return ok && len(f.Gate.Check(l.Score)) == 0
}

// Affordable reports whether one of the engines that cleared the gate also reads
// the slice inside the hours OCR has.
func (f Field) Affordable() bool {
	_, ok := f.Winner()
	return ok
}

// Holds reports whether P04-4 holds, which is an engine clearing the gate with no
// Vietnamese finetune on it.
func (f Field) Holds() bool {
	if !f.Settled() {
		return false
	}
	for _, c := range f.Ranked() {
		if !c.Finetuned && len(f.Gate.Check(c.Score)) == 0 {
			return true
		}
	}
	return false
}

// Verdict is the field in one sentence.
func (f Field) Verdict() string {
	l, ok := f.Leads()
	if !ok {
		return "nothing was evaluated, so the OCR path is whatever somebody picks"
	}
	if why := f.Blocking(); len(why) > 0 {
		return why[0]
	}
	if !f.Passed() {
		return fmt.Sprintf(
			"%s is the best of %s and %s, so the slice ships at the born-digital subset unless a Vietnamese finetune closes it",
			l.Engine, plural(len(f.Candidates), "engine"), strings.Join(f.Gate.Check(l.Score), ", "))
	}
	w, ok := f.Winner()
	if !ok {
		return fmt.Sprintf(
			"%s clears the gate at %.2f%% and reads %.1f pages a second, which is %.0f GPU hours for the %s pages the plan routes to OCR against the %.0f OCR has, and no engine that clears the gate is faster",
			l.Engine, 100*l.Score.DER(), l.Rate, l.Cost(f.Pages), millions(f.Pages), Budget())
	}
	if w.Engine != l.Engine {
		return fmt.Sprintf(
			"%s reads best at %.2f%% and costs %.0f GPU hours, which is more than OCR's %.0f, so the path that ships is %s at %.2f%% and %.0f hours",
			l.Engine, 100*l.Score.DER(), l.Cost(f.Pages), Budget(), w.Engine, 100*w.Score.DER(), w.Cost(f.Pages))
	}
	if !f.Holds() {
		return fmt.Sprintf(
			"%s clears the gate at %.2f%% and it is finetuned on Vietnamese, so P04-4 does not hold and the path this slice ships is one somebody has to keep training",
			w.Engine, 100*w.Score.DER())
	}
	return fmt.Sprintf(
		"%s wins the field of %s at %.2f%% against a %.2f%% gate, batch %d holding %s of a %s card, %.0f GPU hours for the slice",
		w.Engine, plural(len(f.Candidates), "engine"), 100*w.Score.DER(), 100*f.Gate.DER,
		w.Batch, gigabytes(w.VRAM), gigabytes(f.Card), w.Cost(f.Pages))
}

// ReadField loads a field from a file of one JSON candidate per line, which is
// what an evaluation harness appends to as each engine finishes.
func ReadField(card int64, pages int64, g Gate, path string) (Field, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Field{}, fmt.Errorf("inspect: %w", err)
	}
	f := Field{Card: card, Pages: pages, Gate: g}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var c Candidate
		if err := dec.Decode(&c); err != nil {
			return Field{}, fmt.Errorf("inspect: %s line %d: %w", path, i+1, err)
		}
		f.Candidates = append(f.Candidates, c)
	}
	if len(f.Candidates) == 0 {
		return Field{}, fmt.Errorf("inspect: %s holds no engines", path)
	}
	return f, nil
}

// names writes a set of them the way somebody says them out loud.
func names(in map[string]bool) string {
	out := make([]string, 0, len(in))
	for n := range in {
		out = append(out, n)
	}
	slices.Sort(out)
	return join(out)
}

// counts writes a set of page counts the same way.
func counts(in map[int]bool) string {
	out := make([]string, 0, len(in))
	for n := range in {
		out = append(out, fmt.Sprintf("%d pages", n))
	}
	slices.Sort(out)
	return join(out)
}

func join(in []string) string {
	switch len(in) {
	case 0:
		return ""
	case 1:
		return in[0]
	}
	return strings.Join(in[:len(in)-1], ", ") + " and " + in[len(in)-1]
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func gigabytes(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/(1<<30)) }

func millions(n int64) string { return fmt.Sprintf("%.1fM", float64(n)/1e6) }
