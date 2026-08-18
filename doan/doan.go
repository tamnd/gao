// Package doan holds the predictions register: what this project said would
// happen, written down before any of it happened.
//
// Đoán is to guess. A specification full of decisions and no predictions cannot
// be wrong about anything, because every claim in it is a plan rather than a
// forecast, and a plan that does not survive contact gets edited into one that
// did. So fifty eight predictions were written across the specification before a
// byte was ingested, each one a number or a comparison that some later
// measurement either lands inside or does not, and this package is where they
// live in code.
//
// Being wrong is not the failure this package guards against. Two thirds right
// is the honest target, and a register that comes back entirely right means the
// predictions were set too safe to be worth making. What it guards against is
// the three ways a register launders a bad forecast, all of which are invisible
// in the published table and none of which need anybody to lie.
//
// The first is editing the claim after the number lands, which turns a miss into
// a hit with a one word diff. So the claims are hashed together and the digest
// is printed with the register, and a result carries the claim it was scored
// against: a result whose claim is not the one the register holds is refused
// rather than applied, and the refusal names the identifier rather than the
// difference, because the difference is the thing under argument.
//
// The second is dropping the prediction that missed. A prediction leaves the
// register only as a withdrawal carrying a reason, the withdrawals stay on the
// published table, and they are capped at a tenth of the register, because past
// that the rate is a fact about what was pulled rather than about what was
// predicted. The register also refuses to change size, so a deletion fails a
// build rather than a review.
//
// The third is scoring the register early, while the easy predictions have
// landed and the expensive ones have not. The rate is not reported as a reading
// on the specification until half the register has resolved.
//
// Every prediction names the slice whose work produces its measurement, and a
// prediction named in a slice's gate has to be on the register, so a gate cannot
// stand on a forecast nobody wrote down. S0 carries no predictions at all. Its
// gate is a set of questions for counsel rather than a set of measurements, and
// the register prints that rather than treating it as a hole.
package doan

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/tamnd/gao/doc"
)

// The four states a prediction can be in. A prediction is open until something
// measures it, and after that it either held or it did not. Withdrawn is the
// fourth because a measurement occasionally becomes impossible, and the honest
// record of that is a row saying so rather than a missing row.
const (
	Open      = "mo"    // nothing has measured it yet
	Landed    = "dung"  // the measurement fell inside the claim
	Missed    = "truot" // it fell outside
	Withdrawn = "rut"   // the measurement can no longer be made, and the reason is on the row
)

// Declared is the number of predictions the register was published with. It is
// a constant rather than a length so that removing a row fails a test, which is
// the only enforcement a register of this kind can have.
const Declared = 58

// Quorum is the share of the register that has to resolve before the rate is
// worth quoting. Below it the rate describes which predictions were cheap to
// measure.
const Quorum = 0.5

// MinRate and MaxRate bound what a rate says about the specification rather than
// about the project. Under half means it was written from hope. Over ninety five
// percent means the predictions were written to be met.
const (
	MinRate = 0.50
	MaxRate = 0.95
)

// MaxWithdrawn caps withdrawals at a tenth of the register, since withdrawing
// the predictions that were going to miss is the cheapest way to a good rate.
const MaxWithdrawn = 0.10

// A Prediction is one claim from the specification, with whatever has come back
// against it.
type Prediction struct {
	// ID is the stable identifier, P03-1 through P11-5, numbered by the
	// specification document the prediction was written in rather than by the
	// slice that measures it.
	ID string `json:"id"`
	// Slice is the slice whose work produces the measurement.
	Slice string `json:"slice"`
	// Claim is the prediction as written, on one line, because the register
	// publishes as a table.
	Claim string `json:"claim"`
	// State is one of the four above.
	State string `json:"state"`
	// Reading is what came back, in the units of the claim.
	Reading string `json:"reading,omitempty"`
	// By is the command that produced the reading and Box is the machine it ran
	// on. A resolved prediction carries both, since a result nobody can rerun is
	// an assertion with a number in it.
	By  string `json:"by,omitempty"`
	Box string `json:"box,omitempty"`
	// Why is the reason a prediction was withdrawn, or the reason a reading that
	// looks decisive is not being treated as one.
	Why string `json:"why,omitempty"`
}

// Resolved reports whether something measured this prediction.
func (p Prediction) Resolved() bool { return p.State == Landed || p.State == Missed }

var identifier = regexp.MustCompile(`^P[0-9]{2}-[0-9]+$`)

// Blocking returns the reasons this row cannot be published, as sentences.
func (p Prediction) Blocking() []string {
	var why []string
	switch {
	case p.ID == "":
		why = append(why, "a prediction on the register carries no identifier")
	case !identifier.MatchString(p.ID):
		why = append(why, fmt.Sprintf("%q is not shaped like a prediction identifier", p.ID))
	}
	if _, ok := doc.SliceByID(p.Slice); !ok {
		why = append(why, fmt.Sprintf("%s is filed under %q, which is not a slice of the build plan", p.ID, p.Slice))
	}
	switch {
	case strings.TrimSpace(p.Claim) == "":
		why = append(why, fmt.Sprintf("%s carries no claim, so nothing about it could come back wrong", p.ID))
	case strings.Contains(p.Claim, "\n"):
		why = append(why, fmt.Sprintf("%s carries a claim broken across lines, and the register publishes as a table", p.ID))
	}
	switch p.State {
	case Open:
		if p.Reading != "" {
			why = append(why, fmt.Sprintf("%s is open and carries a reading, so either it resolved or the reading belongs to another row", p.ID))
		}
	case Landed, Missed:
		if p.Reading == "" {
			why = append(why, fmt.Sprintf("%s is settled and does not say what came back", p.ID))
		}
		if p.By == "" {
			why = append(why, fmt.Sprintf("%s is settled and does not name what measured it, which makes it an assertion rather than a result", p.ID))
		}
	case Withdrawn:
		if p.Why == "" {
			why = append(why, fmt.Sprintf("%s was withdrawn with no reason given, and a withdrawal with no reason is a deletion", p.ID))
		}
	default:
		why = append(why, fmt.Sprintf("%s is in state %q, which is not one of the four", p.ID, p.State))
	}
	return why
}

// A Row is one slice's line in the register summary.
type Row struct {
	Slice     string  `json:"slice"`
	Title     string  `json:"title"`
	Count     int     `json:"predictions"`
	Open      int     `json:"open"`
	Landed    int     `json:"landed"`
	Missed    int     `json:"missed"`
	Withdrawn int     `json:"withdrawn"`
	Rate      float64 `json:"rate"`
}

// Resolved is how many of the slice's predictions something measured.
func (r Row) Resolved() int { return r.Landed + r.Missed }

// A Register is the whole table.
type Register struct {
	Name        string
	Predictions []Prediction
}

// Published returns the register as it was written, with no results in it. The
// results are applied on top with Apply rather than edited into this table,
// which is what keeps the claims and the numbers in different files.
func Published() Register {
	out := make([]Prediction, len(register))
	copy(out, register)
	return Register{Name: "gao-predictions", Predictions: out}
}

// Digest is the blake3 digest of the identifiers and claims, in register order.
// It is published with the table so that a claim edited after a number landed
// changes a value somebody already wrote down.
func (r Register) Digest() doc.Hash {
	var b strings.Builder
	for _, p := range r.Predictions {
		b.WriteString(p.ID)
		b.WriteByte('\t')
		b.WriteString(p.Claim)
		b.WriteByte('\n')
	}
	return doc.SumString(b.String())
}

func (r Register) in(state string) []Prediction {
	var out []Prediction
	for _, p := range r.Predictions {
		if p.State == state {
			out = append(out, p)
		}
	}
	return out
}

// Waiting, Hits, Misses and Pulled are the four states as lists, since the
// published table prints all four and the misses are the ones the register
// exists for.
func (r Register) Waiting() []Prediction { return r.in(Open) }
func (r Register) Hits() []Prediction    { return r.in(Landed) }
func (r Register) Misses() []Prediction  { return r.in(Missed) }
func (r Register) Pulled() []Prediction  { return r.in(Withdrawn) }

// Resolved is how many predictions something has measured.
func (r Register) Resolved() int { return len(r.Hits()) + len(r.Misses()) }

// Rate is the share of resolved predictions that held. It is zero when nothing
// has resolved, which is a different fact from a rate of zero and is why Quorum
// exists.
func (r Register) Rate() float64 { return divide(len(r.Hits()), r.Resolved()) }

// Settled reports whether enough of the register has resolved for the rate to
// describe the specification rather than the order the work ran in.
func (r Register) Settled() bool {
	return len(r.Predictions) > 0 && divide(r.Resolved(), len(r.Predictions)) >= Quorum
}

// Slices returns one row per slice of the build plan, including the slices that
// carry no predictions, because a slice with nothing to be wrong about is worth
// seeing on the table.
func (r Register) Slices() []Row {
	rows := make([]Row, 0, len(doc.Slices))
	for _, s := range doc.Slices {
		row := Row{Slice: s.ID, Title: s.Title}
		for _, p := range r.Predictions {
			if p.Slice != s.ID {
				continue
			}
			row.Count++
			switch p.State {
			case Landed:
				row.Landed++
			case Missed:
				row.Missed++
			case Withdrawn:
				row.Withdrawn++
			default:
				row.Open++
			}
		}
		row.Rate = divide(row.Landed, row.Resolved())
		rows = append(rows, row)
	}
	return rows
}

// Gated returns the predictions a slice's gate stands on, by identifier. A gate
// naming a prediction that is not on the register is a fault, since the gate is
// then a condition nobody wrote down.
func Gated() map[string][]string {
	out := make(map[string][]string, len(doc.Slices))
	for _, s := range doc.Slices {
		if named := mentioned.FindAllString(s.Gate, -1); len(named) > 0 {
			out[s.ID] = named
		}
	}
	return out
}

var mentioned = regexp.MustCompile(`P[0-9]{2}-[0-9]+`)

// Blocking returns the reasons the register cannot be published as one.
func (r Register) Blocking() []string {
	if len(r.Predictions) == 0 {
		return []string{"the register is empty, and a specification with no predictions in it cannot be wrong about anything"}
	}
	var why []string
	if len(r.Predictions) != Declared {
		why = append(why, fmt.Sprintf("the register holds %d predictions against the %d it was published with, and a register that changes size is not a register",
			len(r.Predictions), Declared))
	}
	seen := make(map[string]string, len(r.Predictions))
	claims := make(map[string]string, len(r.Predictions))
	for _, p := range r.Predictions {
		why = append(why, p.Blocking()...)
		if first, ok := seen[p.ID]; ok {
			why = append(why, fmt.Sprintf("%s is on the register twice, filed under %s and under %s", p.ID, first, p.Slice))
		}
		seen[p.ID] = p.Slice
		key := strings.ToLower(strings.TrimSpace(p.Claim))
		if first, ok := claims[key]; ok && key != "" {
			why = append(why, fmt.Sprintf("%s and %s make the same claim, so neither of them can miss on its own", first, p.ID))
		}
		claims[key] = p.ID
	}
	for id, named := range Gated() {
		for _, want := range named {
			if _, ok := seen[want]; !ok {
				why = append(why, fmt.Sprintf("the %s gate stands on %s, which is not on the register", id, want))
			}
		}
	}
	sort.Strings(why)
	return why
}

// Holds reports whether the register reads as a set of predictions rather than
// as a scoreboard. It is true while the register is unresolved, because there is
// nothing to judge yet and reporting an unmeasured register as failing would say
// the work is going badly rather than that it has not run.
func (r Register) Holds() bool {
	if divide(len(r.Pulled()), len(r.Predictions)) > MaxWithdrawn {
		return false
	}
	if !r.Settled() {
		return true
	}
	return r.Rate() >= MinRate && r.Rate() <= MaxRate
}

// Verdict is the register in one paragraph.
func (r Register) Verdict() string {
	if why := r.Blocking(); len(why) > 0 {
		return why[0]
	}
	slices := 0
	for _, row := range r.Slices() {
		if row.Count > 0 {
			slices++
		}
	}
	head := fmt.Sprintf("%s holds %s across %d of the %d slices in the build plan, and its digest is %s.",
		r.Name, count(len(r.Predictions), "prediction"), slices, len(doc.Slices), r.Digest().String()[:12])

	switch {
	case divide(len(r.Pulled()), len(r.Predictions)) > MaxWithdrawn:
		return fmt.Sprintf("%s %s of them were withdrawn, against a ceiling of %s, so what the rate describes now is which predictions were pulled rather than which ones held.",
			head, count(len(r.Pulled()), "prediction"), percent(MaxWithdrawn))
	case r.Resolved() == 0:
		return fmt.Sprintf("%s None of them has a result, which is the only state a register can be in before the work runs, and it is published in that state so that a claim edited later changes a value somebody already has.", head)
	case !r.Settled():
		return fmt.Sprintf("%s %s resolved so far, %d right and %d wrong, which is under the half the register needs before its rate says anything except which measurements were cheap to make.",
			head, count(r.Resolved(), "prediction"), len(r.Hits()), len(r.Misses()))
	case r.Rate() < MinRate:
		return fmt.Sprintf("%s %d of the %d resolved held, which is %s, and a specification that comes back wrong more often than right was written from hope rather than from evidence.",
			head, len(r.Hits()), r.Resolved(), percent(r.Rate()))
	case r.Rate() > MaxRate:
		return fmt.Sprintf("%s %d of the %d resolved held, which is %s, and a register that comes back almost entirely right means the predictions were written to be met rather than to be tested.",
			head, len(r.Hits()), r.Resolved(), percent(r.Rate()))
	}
	return fmt.Sprintf("%s %d of the %d resolved held and %d did not, which is %s against a band of %s to %s, so the specification was written from evidence and the misses are the part of it worth reading.",
		head, len(r.Hits()), r.Resolved(), len(r.Misses()), percent(r.Rate()), percent(MinRate), percent(MaxRate))
}

// A Result is one measurement, as it comes off a run. It carries the claim it
// was scored against rather than only the identifier, so that the register can
// tell a result about an older claim from a result about this one.
type Result struct {
	ID      string `json:"id"`
	Claim   string `json:"claim"`
	State   string `json:"state"`
	Reading string `json:"reading"`
	By      string `json:"by"`
	Box     string `json:"box"`
	Why     string `json:"why,omitempty"`
}

// Apply puts results on the register and returns the register with them in it,
// along with the results it would not take. A refused result leaves the row
// open, since a register that guesses which of two claims a number was measured
// against is the thing this package exists to prevent.
func (r Register) Apply(results []Result) (Register, []string) {
	out := Register{Name: r.Name, Predictions: make([]Prediction, len(r.Predictions))}
	copy(out.Predictions, r.Predictions)

	at := make(map[string]int, len(out.Predictions))
	for i, p := range out.Predictions {
		at[p.ID] = i
	}

	var why []string
	seen := make(map[string]bool, len(results))
	for _, res := range results {
		i, ok := at[res.ID]
		if !ok {
			why = append(why, fmt.Sprintf("%s carries a result and is not on the register", res.ID))
			continue
		}
		if seen[res.ID] {
			why = append(why, fmt.Sprintf("%s has two results and nothing in the file says which of them is the later one", res.ID))
			continue
		}
		seen[res.ID] = true
		if strings.TrimSpace(res.Claim) != strings.TrimSpace(out.Predictions[i].Claim) {
			why = append(why, fmt.Sprintf("%s was measured against a claim the register does not hold, so either the claim was edited after the number landed or the result belongs to an older register", res.ID))
			continue
		}
		switch res.State {
		case Landed, Missed, Withdrawn:
		default:
			why = append(why, fmt.Sprintf("%s came back in state %q, which is not one a result can be in", res.ID, res.State))
			continue
		}
		out.Predictions[i].State = res.State
		out.Predictions[i].Reading = res.Reading
		out.Predictions[i].By = res.By
		out.Predictions[i].Box = res.Box
		out.Predictions[i].Why = res.Why
	}
	sort.Strings(why)
	return out, why
}

// ReadResults reads one JSON object per line. Unknown fields are refused rather
// than ignored, because a misspelled state field would otherwise read as an open
// prediction.
func ReadResults(path string) ([]Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Result
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var res Result
		if err := dec.Decode(&res); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
		}
		out = append(out, res)
	}
	return out, nil
}

func divide(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func percent(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
