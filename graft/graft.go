// Package graft grafts new tokens onto a vocabulary that was not trained with
// them, and says what the graft cost.
//
// Ghép is to graft. The continued pretraining path does not get to choose its
// tokenizer, because the base model arrives with one and every weight in it was
// trained against those ids. Expansion is the only move available: keep the
// vocabulary, add the Vietnamese the base tokenizer spells out three pieces at
// a time, and pay for the new rows in the embedding matrix out of the run's own
// budget. The fertility win from that is real and it is the easy half. It costs
// nothing to measure, it is a property of the tokenizer alone, and it is
// available before a single step is trained.
//
// The other half is that the new rows start out meaning nothing. Every existing
// row is a direction the body has been reading for trillions of tokens, and a
// row added this morning is noise sitting in the same space. So the loss goes
// up when the expanded tokenizer is switched on and comes back down over some
// number of tokens, and that number comes out of the continued pretraining
// budget rather than out of nowhere. An expansion that improves fertility by a
// fifth and spends a third of the run recovering has made the run worse and
// every number on the tokenizer side of it looks like a win.
//
// So the checks here are about the rows rather than about the vocabulary. How
// they were initialized, because the mean of a quarter of a million vectors is
// a vector with almost no norm and a token the output head cannot reach. What
// happened to the output side when the embeddings are untied, since a model can
// end up able to read a token and unable to write it. Whether the body was held
// still while the new rows found their place. Whether the added tokens are
// strings the base tokenizer already had, which leaves two ids for one string
// and lets merge order decide which one the model is shown. And whether the
// loss ever came back, because if it did not then the fertility figure is the
// only thing that improved.
package graft

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// MinGain is the fertility improvement an expansion has to buy to be worth the
// parameters. It is the line P07-2 is written against.
const MinGain = 0.15

// MinCovered is the least share of corpus tokens the added ids have to carry.
// Below it the run bought a vocabulary the text does not use.
const MinCovered = 0.05

// NormLow and NormHigh bound the new rows' mean norm against the existing rows'.
// A row an order of magnitude smaller than its neighbors is a token the output
// head produces a flat logit for however good its direction is.
const (
	NormLow  = 0.5
	NormHigh = 1.5
)

// MinFrozen is the fewest steps the body may be held still for while the new
// rows move. Zero means the first gradient the model saw came from rows
// initialized minutes earlier and went into every layer of it.
const MinFrozen = 500

// MaxSpike is the most the loss may reach, as a multiple of where it was before
// the expansion. Above it the switch was thrown on a model that had no way to
// read half of what it was being shown.
const MaxSpike = 1.5

// MaxShare is the most of the continued pretraining budget an expansion may
// spend getting back to the loss it started at.
const MaxShare = 0.10

// Bytes is what one parameter of an embedding row weighs at training precision.
const Bytes = 2

// An Expansion is one way of grafting tokens onto a base vocabulary, with what
// it bought and what it cost.
type Expansion struct {
	// Base is the model whose vocabulary is being expanded, and Method is how
	// the new rows were initialized: random, mean, or pieces.
	Base   string `json:"base"`
	Method string `json:"method"`

	// Tied says the input and output embeddings are one matrix. When they are
	// not, OutInit is how the output rows were initialized, which is a separate
	// decision and the one that gets forgotten.
	Tied    bool   `json:"tied"`
	OutInit string `json:"out_init,omitempty"`

	Vocab int `json:"vocab"`
	New   int `json:"new"`
	Dim   int `json:"dim"`

	// Duplicate is how many added tokens are strings the base tokenizer could
	// already produce.
	Duplicate int `json:"duplicate"`

	// Covered is the share of corpus tokens the added ids carry once the
	// expanded tokenizer is used.
	Covered float64 `json:"covered"`

	// Before and After are tokens per syllable on gao text.
	Before float64 `json:"before"`
	After  float64 `json:"after"`

	// BaseNorm and NewNorm are the mean L2 norm of the existing rows and of the
	// grafted ones.
	BaseNorm float64 `json:"base_norm"`
	NewNorm  float64 `json:"new_norm"`

	// Frozen is how many steps the body was held still for.
	Frozen int `json:"frozen"`

	// LossBefore is where the run was before the switch, Spike is the highest
	// it reached after it, and Recovered is the tokens spent getting back to
	// LossBefore. Zero means it never did.
	LossBefore float64 `json:"loss_before"`
	Spike      float64 `json:"spike"`
	Recovered  int64   `json:"recovered"`

	// Box is where this was measured, because a fertility number without one is
	// not reproducible and reproducing it across the fleet is its own item.
	Box string `json:"box"`
}

// Gain is the fertility improvement, as a share of where it started.
func (e Expansion) Gain() float64 {
	if e.Before <= 0 {
		return 0
	}
	return (e.Before - e.After) / e.Before
}

// Params is the parameters the graft added, which is two matrices when the
// embeddings are untied and one when they are tied.
func (e Expansion) Params() int64 {
	n := int64(e.New) * int64(e.Dim)
	if !e.Tied {
		n *= 2
	}
	return n
}

// Weight is what those parameters cost at training precision.
func (e Expansion) Weight() int64 { return e.Params() * Bytes }

// Ratio is the new rows' norm against the existing rows'.
func (e Expansion) Ratio() float64 {
	if e.BaseNorm <= 0 {
		return 0
	}
	return e.NewNorm / e.BaseNorm
}

// Share is what fraction of a budget the recovery spent.
func (e Expansion) Share(budget int64) float64 {
	if budget <= 0 || e.Recovered <= 0 {
		return 0
	}
	return float64(e.Recovered) / float64(budget)
}

// Net is the fertility bought less the run spent buying it, which is the one
// figure the four methods can be ordered by.
func (e Expansion) Net(budget int64) float64 {
	if e.Recovered <= 0 {
		return -1
	}
	return e.Gain() - e.Share(budget)
}

// Paid reports whether the graft was worth making at this budget.
func (e Expansion) Paid(budget int64) bool {
	return e.Gain() >= MinGain && e.Recovered > 0 && e.Share(budget) <= MaxShare
}

// Blocking is every reason this expansion is not a measurement of one.
func (e Expansion) Blocking(budget int64) []string {
	var why []string
	if e.Base == "" {
		return []string{"an expansion with no base model under it is a vocabulary, and every cost here is a cost against weights that already exist"}
	}
	if e.Box == "" {
		why = append(why, fmt.Sprintf(
			"%s does not say where it was measured, and a fertility number that four boxes have not agreed on is one box's locale",
			e.Base))
	}
	switch e.Method {
	case "":
		why = append(why, fmt.Sprintf("%s does not say how the new rows were initialized, which is the whole of the mechanics this item asks for", e.Base))
	case "random":
		why = append(why, fmt.Sprintf(
			"%s drew the new rows from a normal, so the first thing the model does with a token it has never seen is produce a logit with nothing to do with the string, and the gradient that corrects it goes through every layer of a body that was already right",
			e.Base))
	case "mean", "pieces":
	default:
		why = append(why, fmt.Sprintf("%s initialized the new rows by %q, which is not one of random, mean, or pieces", e.Base, e.Method))
	}
	if e.Vocab <= 0 || e.New <= 0 || e.Dim <= 0 {
		why = append(why, fmt.Sprintf(
			"%s does not say how many tokens were added to how large a vocabulary at what width, so the parameters the graft costs cannot be counted",
			e.Base))
	}
	if !e.Tied && e.OutInit == "" {
		why = append(why, fmt.Sprintf(
			"%s has untied embeddings and only says how the input rows were initialized, so the model can read the new tokens and there is nothing said about writing them",
			e.Base))
	}
	if e.BaseNorm <= 0 || e.NewNorm <= 0 {
		why = append(why, fmt.Sprintf(
			"%s records no norm either side of the graft, and the row that averages a quarter of a million vectors is small in a way nothing about the method's name says",
			e.Base))
	} else if r := e.Ratio(); r < NormLow || r > NormHigh {
		why = append(why, fmt.Sprintf(
			"%s put the new rows at %.2f of the existing rows' norm, outside %.1f to %.1f, so the graft is %s and the direction it points is not the problem",
			e.Base, r, NormLow, NormHigh, reach(r)))
	}
	if e.Duplicate > 0 {
		why = append(why, fmt.Sprintf(
			"%d of the added tokens are strings the base tokenizer could already produce, so there are two ids for one piece of text and merge order decides which the model is shown, having trained on the other",
			e.Duplicate))
	}
	if e.Covered <= 0 {
		why = append(why, fmt.Sprintf("%s does not say what share of the corpus the added ids carry, so the parameters bought something unmeasured", e.Base))
	} else if e.Covered < MinCovered {
		why = append(why, fmt.Sprintf(
			"the added ids carry %s of the corpus, under %s, so %s of new parameters bought a vocabulary this text does not use",
			share(e.Covered), share(MinCovered), gigabytes(e.Weight())))
	}
	if e.Before <= 0 || e.After <= 0 {
		why = append(why, fmt.Sprintf("%s records fertility on only one side of the graft, and the improvement is the difference", e.Base))
	} else if e.After >= e.Before {
		why = append(why, fmt.Sprintf(
			"%s went from %.2f tokens a syllable to %.2f, so the expanded tokenizer spells Vietnamese out no more cheaply than the one it replaced",
			e.Base, e.Before, e.After))
	}
	if e.Frozen < MinFrozen {
		why = append(why, fmt.Sprintf(
			"the body was held still for %d steps, under %d, so most of the gradient from rows that meant nothing yet went into weights that were already trained",
			e.Frozen, MinFrozen))
	}
	if e.LossBefore <= 0 {
		why = append(why, fmt.Sprintf("%s records no loss before the switch, so there is nothing for the recovery to have recovered to", e.Base))
	} else if e.Spike <= 0 {
		why = append(why, fmt.Sprintf("%s records no loss after the switch, and an expansion that did not move the loss did not take effect", e.Base))
	} else if e.Spike > e.LossBefore*MaxSpike {
		why = append(why, fmt.Sprintf(
			"the loss reached %.4f against the %.4f it was at, which is %.1f times and is a switch thrown on a model with no way to read what it was being shown",
			e.Spike, e.LossBefore, e.Spike/e.LossBefore))
	}
	if budget <= 0 {
		why = append(why, "the run has no token budget, so the recovery is a number of tokens with nothing to be a share of")
	} else if e.Recovered > budget {
		why = append(why, fmt.Sprintf(
			"%s spent %s recovering out of a budget of %s, which is not a recovery inside this run",
			e.Base, billions(e.Recovered), billions(budget)))
	}
	return why
}

// A Trial is the expansions being compared, which is how the answer gets
// published either way rather than as whichever one was tried last.
type Trial struct {
	// Budget is the continued pretraining run's token budget, which every
	// recovery is a share of.
	Budget int64 `json:"budget"`

	Runs []Expansion `json:"runs"`
}

// Ranked is the expansions by what they netted, best first.
func (t Trial) Ranked() []Expansion {
	out := slices.Clone(t.Runs)
	slices.SortStableFunc(out, func(a, b Expansion) int {
		switch {
		case a.Net(t.Budget) > b.Net(t.Budget):
			return -1
		case a.Net(t.Budget) < b.Net(t.Budget):
			return 1
		default:
			return strings.Compare(a.Method, b.Method)
		}
	})
	return out
}

// Best is the expansion that netted most, and false when nothing was tried.
func (t Trial) Best() (Expansion, bool) {
	if len(t.Runs) == 0 {
		return Expansion{}, false
	}
	// Taken off the ranking rather than found again, so two methods that netted
	// the same name the same one in the table and in the sentence.
	return t.Ranked()[0], true
}

// Stranded is every expansion whose loss never came back.
func (t Trial) Stranded() []Expansion {
	var out []Expansion
	for _, e := range t.Ranked() {
		if e.Recovered <= 0 {
			out = append(out, e)
		}
	}
	return out
}

// Blocking is every reason this trial does not answer the mechanics question.
func (t Trial) Blocking() []string {
	if len(t.Runs) == 0 {
		return []string{"no expansion was run, and the mechanics of one are a paragraph until somebody switches the tokenizer over"}
	}
	var why []string
	seen := map[string]bool{}
	for _, e := range t.Runs {
		key := e.Base + "/" + e.Method
		if seen[key] {
			why = append(why, fmt.Sprintf("%s by %s appears twice, and two readings of one graft are not two grafts", e.Base, e.Method))
		}
		seen[key] = true
		why = append(why, e.Blocking(t.Budget)...)
	}
	bases := map[string]bool{}
	for _, e := range t.Runs {
		bases[e.Base] = true
	}
	if len(bases) > 1 {
		why = append(why, "the expansions are grafted onto different base models, and a comparison of initialization methods across two bodies is a comparison of the bodies")
	}
	if len(t.Runs) < 2 {
		why = append(why, "one method was tried, so what is published is the method somebody picked rather than the answer to which one is better")
	}
	return why
}

// Settled reports whether this is a measurement of the mechanics.
func (t Trial) Settled() bool { return len(t.Blocking()) == 0 }

// Holds reports whether the best expansion is one worth making.
func (t Trial) Holds() bool {
	if !t.Settled() {
		return false
	}
	b, ok := t.Best()
	return ok && b.Paid(t.Budget)
}

// Verdict is the trial in one sentence.
func (t Trial) Verdict() string {
	b, ok := t.Best()
	if !ok {
		return "no expansion was run, so the vocabulary is whatever the base model arrived with"
	}
	if why := t.Blocking(); len(why) > 0 {
		return why[0]
	}
	if b.Gain() < MinGain {
		return fmt.Sprintf(
			"the best of %s bought %s of fertility, under the %s this path is worth taking for, so %s stays on the vocabulary it came with",
			plural(len(t.Runs), "method"), share(b.Gain()), share(MinGain), b.Base)
	}
	if b.Recovered <= 0 {
		return fmt.Sprintf(
			"%s by %s bought %s of fertility and never came back to the loss it started at inside %s, so the fertility figure is the only thing that improved",
			b.Base, b.Method, share(b.Gain()), billions(t.Budget))
	}
	if s := b.Share(t.Budget); s > MaxShare {
		return fmt.Sprintf(
			"%s by %s bought %s of fertility and spent %s of the run getting back to the loss it started at, over the %s an expansion may cost, so the sequences are shorter and the run is worse",
			b.Base, b.Method, share(b.Gain()), share(s), share(MaxShare))
	}
	return fmt.Sprintf(
		"%s by %s is the best of %s, buying %s of fertility for %s of new parameters and %s of the run spent recovering, which nets %s",
		b.Base, b.Method, plural(len(t.Runs), "method"), share(b.Gain()),
		gigabytes(b.Weight()), share(b.Share(t.Budget)), share(b.Net(t.Budget)))
}

// ReadTrial loads a trial from a file of one JSON expansion per line.
func ReadTrial(budget int64, path string) (Trial, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Trial{}, fmt.Errorf("graft: %w", err)
	}
	t := Trial{Budget: budget}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var e Expansion
		if err := dec.Decode(&e); err != nil {
			return Trial{}, fmt.Errorf("graft: %s line %d: %w", path, i+1, err)
		}
		t.Runs = append(t.Runs, e)
	}
	if len(t.Runs) == 0 {
		return Trial{}, fmt.Errorf("graft: %s holds no expansions", path)
	}
	return t, nil
}

// reach says which way a norm went wrong, since the two failures are not the
// same failure and the fix for one makes the other worse.
func reach(r float64) string {
	if r < NormLow {
		return "a set of tokens the output head produces a flat logit for"
	}
	return "a set of tokens the output head reaches for before it has any reason to"
}

func share(f float64) string { return fmt.Sprintf("%.1f%%", 100*f) }

func gigabytes(n int64) string {
	if n < 1<<30 {
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}

func billions(n int64) string { return fmt.Sprintf("%.1fB tokens", float64(n)/1e9) }

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
