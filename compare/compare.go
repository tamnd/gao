// Package compare reads a human evaluation back and says whether the raters were
// reading the answers or reading the layout.
//
// So sánh is to compare. Two systems answer the same prompts, a person is shown
// both answers and picks one, and the share of picks one system got is the
// headline. That number is the easiest one in this project to produce and the
// easiest to produce wrongly, because every way it goes wrong produces a number
// that looks exactly like a result.
//
// # Why the side an answer was shown on is measured before the answer is
//
// A rater shown two answers side by side picks the left one more often than the
// right one, and does it whether or not the left one is better. The effect is
// large enough to carry a protocol on its own: at a genuine tie, a left hand
// preference of 58% reports the left hand system winning 58 to 42, and every
// sentence written about that result afterward is a sentence about the layout.
// The defense is to show each pair in both orders and then check that it
// happened, because a harness that was supposed to alternate and did not is
// invisible in the finished file. So two numbers come out before the win rate:
// the share of picks that went to whichever answer was shown first, and the
// share of pairs a given system was shown first in. Both are supposed to sit at
// a half and neither is assumed to.
//
// # Why length is measured next
//
// The other thing a rater picks without meaning to is the longer answer. It is
// the single strongest confound in preference evaluation and it does not need a
// careless rater, since a longer answer really does look more thorough in the
// two minutes somebody spends on it. A system tuned to write more will win a
// preference evaluation against a system tuned to write better, so the share of
// decided pairs where the longer answer won is reported next to the win rate
// and not underneath it.
//
// # Why the interval is here and what it does not cover
//
// A win rate of 54% over 200 pairs is a tie. Reporting it as a win is how a
// project ends up with a headline that the next run cannot reproduce, so the
// win rate is printed with an interval around it and the report says in words
// when that interval covers a half. The interval is the ordinary normal
// approximation and it treats the pairs as independent, which they are not: two
// raters reading the same pair are two correlated readings, so a set with a lot
// of second opinions in it has a slightly wider true interval than the one
// printed. That is stated here rather than corrected because the correction
// needs an assumption about the correlation and the honest fix is the number
// right beside it, which is how much of the set was read twice at all.
//
// # Why agreement is the last thing and not the first
//
// Agreement between raters is what says the choice was about the answers rather
// than about the rater, and it is chance corrected for the same reason xep
// corrects it: three choices with ties rare means two people who never read
// anything would agree most of the time. It is Scott's pi rather than Cohen's
// kappa because there is no first rater and second rater, a pair gets read by
// whoever picks it up, and the marginals are pooled so the statistic does not
// depend on who was written to the file first.
package compare

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// MinPairs is the smallest evaluation this package will read. Two hundred is
// where the interval on a win rate stops being wider than the effects anybody
// is looking for.
const MinPairs = 200

// MinDouble is the share of items that have to be read by more than one rater.
// A fifth is enough to measure agreement on and small enough that a protocol
// can afford it.
const MinDouble = 0.20

// MinPi is the floor on chance corrected agreement between raters. Forty is
// lower than the line xep holds its rubric to, deliberately: a preference
// between two good answers has real ties in it, and a floor set where a
// classification task sets it would fail protocols that are working.
const MinPi = 0.40

// MaxFirst is the share of picks that may go to whichever answer was shown
// first before the result is a reading of the layout.
const MaxFirst = 0.55

// MaxLonger is the share of decided pairs the longer answer may win before the
// result is a reading of length.
const MaxLonger = 0.65

// MaxOrder is the share of pairs one system may be shown first in. A harness
// that alternates lands at a half, and one that does not lands wherever the
// input file happened to be ordered.
const MaxOrder = 0.55

// MaxRater is the share of the evaluation one person may carry. Past this the
// result is that person's preference with a sample size written next to it.
const MaxRater = 0.25

// Prevalent is the share of one outcome above which the agreement figure has
// stopped measuring the raters. Past this, two people agreeing mostly means two
// people have noticed which system is better, which is a fact about the systems
// and not evidence about the protocol. It sits above the line xep holds its
// rubric to because a preference where one system genuinely wins three pairs in
// four is an ordinary result rather than a degenerate draw.
const Prevalent = 0.75

// Z is the 95% two sided normal quantile.
const Z = 1.96

// A Choice is what a rater picked. Left and Right are positions rather than
// systems, because the position is the thing being checked.
type Choice string

const (
	Left  Choice = "left"
	Right Choice = "right"
	Tie   Choice = "tie"
)

// A Pair is one rater's read of one item.
type Pair struct {
	// Item is the prompt both answers came from. Two raters reading the same
	// item is what agreement is measured over.
	Item  string `json:"item"`
	Rater string `json:"rater"`

	// Left and Right are the systems, in the order they were shown.
	Left  string `json:"left"`
	Right string `json:"right"`

	// LeftSyllables and RightSyllables are the lengths of the two answers as
	// sang counts them. They are recorded rather than derived so that the
	// protocol file does not carry every answer once per rater.
	LeftSyllables  int `json:"left_syllables"`
	RightSyllables int `json:"right_syllables"`

	Choice Choice `json:"choice"`
}

// won is the system a pair went to, and whether it went to one at all.
func (p Pair) won() (string, bool) {
	switch p.Choice {
	case Left:
		return p.Left, true
	case Right:
		return p.Right, true
	}
	return "", false
}

// A Rater is one person and what they did.
type Rater struct {
	Rater string  `json:"rater"`
	Pairs int     `json:"pairs"`
	Share float64 `json:"share"`

	First int `json:"first"`
	Ties  int `json:"ties"`
}

// A Reading is a human evaluation, read back.
type Reading struct {
	// A and B are the two systems, A being the one the win rate is quoted for.
	A string `json:"a"`
	B string `json:"b"`

	Pairs int `json:"pairs"`
	Items int `json:"items"`

	WinsA int `json:"wins_a"`
	WinsB int `json:"wins_b"`
	Ties  int `json:"ties"`

	// Rate is A's share of the pairs that went to somebody, with Low and High
	// the interval around it. Decided is what all three are computed over.
	Decided int     `json:"decided"`
	Rate    float64 `json:"rate"`
	Low     float64 `json:"low"`
	High    float64 `json:"high"`

	// First is the share of picks that went to the answer shown first, and
	// Order the share of pairs A was shown first in.
	First float64 `json:"first"`
	Order float64 `json:"order"`

	// Longer is the share of the decided pairs whose two answers differed in
	// length that the longer answer won, over Compared of them.
	Longer   float64 `json:"longer"`
	Compared int     `json:"compared"`

	// Doubled is how many items more than one rater read, and Exact, Chance and
	// Pi are the agreement over them.
	Doubled     int     `json:"doubled"`
	DoubleShare float64 `json:"double_share"`
	Comparisons int     `json:"comparisons"`
	Exact       float64 `json:"exact"`
	Chance      float64 `json:"chance"`
	Pi          float64 `json:"pi"`

	// Common is what the second opinions mostly came out as and Prevalence its
	// share, which is what decides whether Exact was ever going to mean
	// anything.
	Common     string  `json:"common"`
	Prevalence float64 `json:"prevalence"`

	Raters []Rater `json:"raters"`

	pairs []Pair
}

// Read measures an evaluation. The order of pairs is the order they were
// collected in and nothing here depends on it.
func Read(pairs []Pair) Reading {
	r := Reading{Pairs: len(pairs), pairs: pairs}

	systems := map[string]bool{}
	for _, p := range pairs {
		systems[p.Left] = true
		systems[p.Right] = true
	}
	named := make([]string, 0, len(systems))
	for s := range systems {
		named = append(named, s)
	}
	sort.Strings(named)
	if len(named) > 0 {
		r.A = named[0]
	}
	if len(named) > 1 {
		r.B = named[1]
	}
	if len(named) != 2 {
		return r
	}

	byItem := map[string][]Pair{}
	byRater := map[string]*Rater{}
	var first, order, longer, compared int

	for _, p := range pairs {
		byItem[p.Item] = append(byItem[p.Item], p)
		if byRater[p.Rater] == nil {
			byRater[p.Rater] = &Rater{Rater: p.Rater}
		}
		who := byRater[p.Rater]
		who.Pairs++

		if p.Left == r.A {
			order++
		}
		switch p.Choice {
		case Left:
			first++
			who.First++
		case Tie:
			r.Ties++
			who.Ties++
		}

		winner, decided := p.won()
		if !decided {
			continue
		}
		r.Decided++
		if winner == r.A {
			r.WinsA++
		} else {
			r.WinsB++
		}
		if p.LeftSyllables != p.RightSyllables {
			compared++
			if (p.Choice == Left) == (p.LeftSyllables > p.RightSyllables) {
				longer++
			}
		}
	}

	r.Items = len(byItem)
	if len(pairs) > 0 {
		r.First = float64(first) / float64(len(pairs))
		r.Order = float64(order) / float64(len(pairs))
	}
	if r.Decided > 0 {
		r.Rate = float64(r.WinsA) / float64(r.Decided)
		half := Z * math.Sqrt(r.Rate*(1-r.Rate)/float64(r.Decided))
		r.Low = math.Max(0, r.Rate-half)
		r.High = math.Min(1, r.Rate+half)
	}
	r.Compared = compared
	if compared > 0 {
		r.Longer = float64(longer) / float64(compared)
	}

	r.agree(byItem)
	r.raters(byRater)
	return r
}

// agree measures Scott's pi over the items more than one rater read, pooling the
// marginals across raters because there is no first rater here.
func (r *Reading) agree(byItem map[string][]Pair) {
	// A pair shown to two raters in two orders is the same judgement about the
	// systems and a different one about the positions, so both the agreement and
	// the marginals it is corrected against are read over the system that won
	// rather than over the side that was picked.
	picked := func(p Pair) string {
		if won, ok := p.won(); ok {
			return won
		}
		return string(Tie)
	}

	seen := map[string]int{}
	var comparisons, exact int
	for _, read := range byItem {
		if len(read) < 2 {
			continue
		}
		r.Doubled++
		for i := range read {
			for j := i + 1; j < len(read); j++ {
				comparisons++
				if picked(read[i]) == picked(read[j]) {
					exact++
				}
				seen[picked(read[i])]++
				seen[picked(read[j])]++
			}
		}
	}

	r.Comparisons = comparisons
	if r.Items > 0 {
		r.DoubleShare = float64(r.Doubled) / float64(r.Items)
	}
	if comparisons == 0 {
		return
	}

	r.Exact = float64(exact) / float64(comparisons)
	total := float64(2 * comparisons)
	for _, outcome := range names(seen) {
		p := float64(seen[outcome]) / total
		r.Chance += p * p
		if p > r.Prevalence {
			r.Common, r.Prevalence = outcome, p
		}
	}

	// Everybody choosing the same way every time is perfect agreement and it is
	// also the case Scott's pi is not defined on, since there is no variance to
	// correct against. It is reported as the agreement it is and Prevalence is
	// what says how much that figure is worth.
	if r.Chance >= 1 {
		r.Pi = r.Exact
		return
	}
	r.Pi = (r.Exact - r.Chance) / (1 - r.Chance)
}

// raters is every person and what they did, busiest first.
func (r *Reading) raters(byRater map[string]*Rater) {
	out := make([]Rater, 0, len(byRater))
	for _, who := range byRater {
		if r.Pairs > 0 {
			who.Share = float64(who.Pairs) / float64(r.Pairs)
		}
		out = append(out, *who)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pairs != out[j].Pairs {
			return out[i].Pairs > out[j].Pairs
		}
		return out[i].Rater < out[j].Rater
	})
	r.Raters = out
}

// Separates reports whether the interval clears a half, which is the only
// condition under which this evaluation says one system beat the other.
func (r Reading) Separates() bool { return r.Low > 0.5 || r.High < 0.5 }

// Blocking is every reason this is not an evaluation anybody can read.
func (r Reading) Blocking() []string {
	if len(r.pairs) == 0 {
		return []string{"the file holds no judgements, so there is no evaluation to read"}
	}
	var why []string

	systems := map[string]bool{}
	for _, p := range r.pairs {
		systems[p.Left] = true
		systems[p.Right] = true
	}
	switch n := len(systems); {
	case n < 2:
		why = append(why, fmt.Sprintf(
			"every judgement compares %s against itself, which measures the raters rather than the systems", r.A))
	case n > 2:
		why = append(why, fmt.Sprintf(
			"%d systems appear in the file and this is a two system protocol, so a win rate over it would be a win rate against whichever system happened to be drawn opposite: %s",
			n, commas(names(systems))))
	}

	// One bad harness writes the same fault onto every line it produced, so
	// each kind is counted rather than repeated and the first one is named,
	// which is the one somebody opening the file will look at.
	var noItem, noRater, twice, itself, notAChoice tally
	seen := map[string]bool{}
	for _, p := range r.pairs {
		switch {
		case p.Item == "":
			noItem.add("")
		case p.Rater == "":
			noRater.add(p.Item)
		case seen[p.Item+"\x00"+p.Rater]:
			twice.add(p.Rater + " on " + p.Item)
		}
		seen[p.Item+"\x00"+p.Rater] = true

		if p.Left != "" && p.Left == p.Right {
			itself.add(p.Left + " on " + p.Item)
		}
		switch p.Choice {
		case Left, Right, Tie:
		default:
			notAChoice.add(fmt.Sprintf("%q on %s", string(p.Choice), p.Item))
		}
	}

	why = append(why,
		noItem.say(
			"a judgement arrived with no item on it, so nothing can be said about how many people read the same pair",
			"%[1]d judgements arrived with no item on them, so nothing can be said about how many people read the same pair"),
		noRater.say(
			"the judgement on %[2]s does not say who made it",
			"%[1]d judgements do not say who made them, the first of them on %[2]s"),
		twice.say(
			"%[2]s is one person reading the same pair twice, and a rater counted twice is a rater weighted by accident",
			"%[1]d judgements are one person reading the same pair twice, the first of them %[2]s, and a rater counted twice is a rater weighted by accident"),
		itself.say(
			"%[2]s was shown on both sides, which measures the raters rather than the systems",
			"%[1]d judgements showed one system on both sides, the first of them %[2]s"),
		notAChoice.say(
			"%[2]s is not left, right or tie",
			"%[1]d judgements recorded something that is not left, right or tie, the first of them %[2]s"),
	)

	if len(r.pairs) < MinPairs {
		why = append(why, fmt.Sprintf(
			"%d judgements is under the %d this reading needs, and the interval on a win rate over that few is wider than any effect worth reporting",
			len(r.pairs), MinPairs))
	}
	// Everything below is read off the measurement, which was not taken when the
	// file is not two systems compared against each other.
	if len(systems) == 2 {
		if r.Doubled == 0 {
			why = append(why, "no item was read by more than one person, so there is nothing to measure agreement over and no evidence the choices were about the answers")
		}
		if r.Decided == 0 {
			why = append(why, "every judgement was a tie, so there is no win rate to report")
		}
	}
	return said(why)
}

// A tally is one kind of bad line, how many of them there were, and the first.
type tally struct {
	n     int
	first string
}

func (t *tally) add(what string) {
	if t.n == 0 {
		t.first = what
	}
	t.n++
}

// say writes the tally as one sentence, or as nothing when there were none.
// Both formats take the count and the first offender, in that order.
func (t tally) say(one, many string) string {
	f := many
	switch t.n {
	case 0:
		return ""
	case 1:
		f = one
	}
	if !strings.Contains(f, "%") {
		return f
	}
	return fmt.Sprintf(f, t.n, t.first)
}

// said drops the sentences the tallies had nothing to write.
func said(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Faults are the reasons a readable evaluation does not support the sentence
// somebody wants to write about it.
func (r Reading) Faults() []string {
	if len(r.Blocking()) > 0 {
		return nil
	}
	var out []string

	if r.First > MaxFirst {
		out = append(out, fmt.Sprintf(
			"%s of the picks went to whichever answer was shown first, against a %s line, so this much of the result is the layout rather than the answers",
			share(r.First), share(MaxFirst)))
	}
	if math.Abs(r.Order-0.5) > MaxOrder-0.5 {
		out = append(out, fmt.Sprintf(
			"%s was shown first in %s of the pairs rather than about half, so the harness did not alternate the order and the position effect above cannot be taken back out of the result",
			r.A, share(r.Order)))
	}
	if r.Longer > MaxLonger {
		out = append(out, fmt.Sprintf(
			"the longer answer won %s of the %d pairs whose answers differed in length, against a %s line, so this reads as an evaluation of length",
			share(r.Longer), r.Compared, share(MaxLonger)))
	}
	if r.DoubleShare < MinDouble {
		out = append(out, fmt.Sprintf(
			"%s of the items were read by more than one person, against a %s line, so the agreement figure below is measured over too little of the set to say much about the rest of it",
			share(r.DoubleShare), share(MinDouble)))
	}
	switch {
	case r.Prevalence > Prevalent:
		out = append(out, fmt.Sprintf(
			"%s of the second opinions came out as %s, so the %s the raters agreed on each other is mostly two people noticing that and says little about whether the rest of the protocol was read",
			share(r.Prevalence), r.Common, share(r.Exact)))
	case r.Pi < MinPi:
		out = append(out, fmt.Sprintf(
			"raters agreed on %s of the items two of them read, which is %s once chance is taken out, against a %s line, so the choices are more about the rater than about the answers",
			share(r.Exact), fixed(r.Pi), fixed(MinPi)))
	}
	if len(r.Raters) > 0 && r.Raters[0].Share > MaxRater {
		out = append(out, fmt.Sprintf(
			"%s made %s of the judgements, so the result is that person's preference with a sample size next to it",
			r.Raters[0].Rater, share(r.Raters[0].Share)))
	}
	if !r.Separates() {
		out = append(out, fmt.Sprintf(
			"the interval runs from %s to %s and covers a half, so this evaluation does not say either system won",
			share(r.Low), share(r.High)))
	}
	return out
}

// Holds reports whether the evaluation supports a claim that one system beat the
// other.
func (r Reading) Holds() bool { return len(r.Blocking()) == 0 && len(r.Faults()) == 0 }

// Verdict is the evaluation in one paragraph.
func (r Reading) Verdict() string {
	if why := r.Blocking(); len(why) > 0 {
		return why[0]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s read %d pairs over %d items and picked %s over %s %s of the time, %s to %s, with %s called a tie.",
		count(len(r.Raters), "person"), r.Pairs, r.Items, r.A, r.B, share(r.Rate), share(r.Low), share(r.High), share(float64(r.Ties)/float64(r.Pairs)))

	faults := r.Faults()
	switch n := len(faults); n {
	case 0:
		fmt.Fprintf(&b, " Nothing in the protocol explains that result better than the answers do, so %s beat %s here.", r.A, r.B)
	case 1:
		fmt.Fprintf(&b, " One reading says this is not a result about the answers: %s.", faults[0])
	default:
		fmt.Fprintf(&b, " %d readings say this is not a result about the answers: %s.", n, strings.Join(faults, "; and "))
	}
	return b.String()
}

// names is the keys of a set in a fixed order, so the same file reads the same
// way twice.
func names[V any](set map[string]V) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func commas(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if noun == "person" {
		return fmt.Sprintf("%d people", n)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func share(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

func fixed(f float64) string { return fmt.Sprintf("%.2f", f) }
