// Package syllable measures what a syllable-atomic tokenizer would govern, and what
// it gives up, over real text and before anything is trained.
//
// Tiếng is a syllable. Vietnamese writes a space between them, and every few
// months somebody proposes the obvious thing: forbid the tokenizer from merging
// across that space, so that a token is a syllable or part of one and never a
// piece of two. It is a tidy rule. It is also a rule about a language rather than
// about a corpus, and this package exists because the two are not the same and
// the difference is measurable.
//
// # What the rule costs, which is arithmetic
//
// Under the rule a syllable never costs less than one token, so the whole corpus
// costs at least one token per syllable and there is no vocabulary size that
// moves that. A tokenizer without the rule has one extra freedom and one only:
// it may spend a slot on a run of syllables that keeps turning up, and pay one
// token for it instead of two or three. Việt Nam, chúng tôi, có thể, thành phố.
// Every other difference between the two arms can be held equal, which means the
// cost of the rule is exactly the tokens those merges would have saved, and that
// number can be counted off text with nothing trained and nothing fetched.
//
// So the reading below is the same vocabulary twice. Once with syllables atomic,
// where the cost is 1.00 tokens per syllable by construction, and once with the
// slots that pay best spent on runs, where it is whatever the text says. The gap
// is what the rule gives up. It is a floor on the cost rather than an estimate
// of it, because a real tokenizer without the rule also merges inside syllables
// and across the units this counts as ungoverned, and every one of those is a
// further saving the rule forbids and this arithmetic does not count.
//
// # What the rule buys, which is not in here
//
// The case for the rule is that a boundary the language already draws is a
// better boundary than one a merge table found, that it keeps a token from
// spanning two words, and that it makes the unmarked register behave. Those are
// claims about what a model learns, and no amount of counting settles them. They
// are P07-3 and they need the slate. What this package refuses to do is let the
// question be decided by whichever side quoted a number first, which is why it
// prints the cost with the buying side left explicitly empty.
//
// # Why the governed share is on the report
//
// The rule is stated about Vietnamese syllables and real text is not made only
// of those. Numbers, English terms in a technical page, code, URLs, and
// punctuation all fall through to whatever the tokenizer would have done anyway,
// and the share of the text that falls through is the share the rule does not
// govern. A proposal that reads as a rule about the corpus and turns out to be a
// rule about eight tenths of it, with the rest handed to an escape hatch nobody
// specified, is a different proposal, so the share is measured rather than
// assumed.
//
// The syllable test is the inventory in sift, and it is used here in both
// registers, which admits a little English: the and man and con are spellings
// a Vietnamese syllable also has once the marks come off. sift says the same
// thing about the same test. It costs a fraction of the governed share on
// marked text and it is the safe direction, since counting an English word as
// governed makes the rule look broader than it is rather than narrower.
package syllable

import (
	"container/heap"
	"fmt"
	"strings"
	"unicode"

	"github.com/tamnd/gao/sift"
)

// Atomic is what a syllable costs in tokens under the rule. It is one, it is one
// on every corpus, and it is on the report as a number rather than as a remark
// because the whole comparison is against it.
const Atomic = 1.0

// Slots is how many vocabulary entries the arm without the rule is allowed to
// spend on cross-syllable runs. It is not a vocabulary size. It is the part of a
// vocabulary the rule is an argument about, and 8192 is chosen as the largest
// number that is still small next to the 192k this project is aiming at, so that
// the cost it measures is one a real vocabulary could afford to pay.
const Slots = 8192

// MinCount is how often a run has to turn up in the sample before a slot spent
// on it is spent rather than wasted. Under fifty appearances the run is a fact
// about the sample and the saving is inside the noise of having drawn it.
const MinCount = 50

// MaxRun is the longest run of syllables this counts as one candidate merge.
// Vietnamese compounds run to four syllables and stop, and past that a run that
// repeats is a quoted phrase rather than a unit of the language.
const MaxRun = 4

// MinSyllables is the least text this will read a collocation table off. A run
// needs MinCount appearances to enter the table, and a sample that holds a few
// thousand syllables can only produce that for the handful of function words
// every Vietnamese page has, which is a table about Vietnamese grammar rather
// than about the corpus.
const MinSyllables = 100_000

// MinDocs is how many documents the reading is taken over. The cost of the rule
// is a claim about a corpus, and a corpus of ten documents is ten documents.
const MinDocs = 200

// MaxDoc is the most of the syllables one document may supply before the table
// is a reading of that document.
const MaxDoc = 0.10

// MinGoverned is the share of the text that has to be Vietnamese syllables
// before the rule is a rule about the text rather than about part of it.
const MinGoverned = 0.85

// A Doc is one document as the pipeline hands it over, normalized and already
// judged to be Vietnamese by whatever ran before this.
type Doc struct {
	Name string
	Text string
}

// A Run is a sequence of syllables that turns up often enough for a vocabulary
// without the rule to spend a slot on it.
type Run struct {
	Run   string `json:"run"`
	Words int    `json:"words"`
	Count int    `json:"count"`

	// Saves is the tokens the merge takes off this text, which is what the slot
	// buys and what the ordering is by. A three syllable run seen once is worth
	// less than a two syllable run seen a thousand times, and the slot goes to
	// the second one.
	Saves int `json:"saves"`
}

// A Reading is what one sample of text says about the rule.
type Reading struct {
	Source string `json:"source"`
	Slots  int    `json:"slots"`

	Docs    int   `json:"docs"`
	Refused int   `json:"refused"`
	Bytes   int64 `json:"bytes"`
	Units   int64 `json:"units"`

	// The units, by what the rule can say about them. Marked and Bare are the
	// two registers of Vietnamese and together they are what the rule governs.
	// Foreign is letters that are not a syllable in either register, Number is
	// digits, Mixed is the two stuck together, and Symbol is everything left.
	Marked  int64 `json:"marked"`
	Bare    int64 `json:"bare"`
	Foreign int64 `json:"foreign"`
	Number  int64 `json:"number"`
	Mixed   int64 `json:"mixed"`
	Symbol  int64 `json:"symbol"`

	Syllables int64   `json:"syllables"`
	Governed  float64 `json:"governed"`

	// Runs is the table the slots went to, and Candidates is how many runs
	// cleared MinCount before the slots ran out.
	Runs       []Run `json:"runs"`
	Candidates int   `json:"candidates"`

	// Inside is the syllable occurrences a merge swallowed, which is the
	// material the rule takes off the table.
	Inside      int64   `json:"inside"`
	InsideShare float64 `json:"inside_share"`

	// Crossing is tokens per syllable once the merges are allowed, against the
	// 1.00 the rule fixes, and Cost is the difference as a share.
	Crossing float64 `json:"crossing"`
	Cost     float64 `json:"cost"`

	Widest      string  `json:"widest"`
	WidestShare float64 `json:"widest_share"`

	// Inventory is how many spellings the syllable inventory in sift forms before
	// the tone marks go on, which times the six tones is the ceiling on what the
	// floor costs a vocabulary. It is on the reading because it is what makes the
	// 1.00 reachable rather than theoretical: a vocabulary that cannot hold every
	// syllable does not get 1.00 tokens per syllable, it gets 1.00 for the
	// syllables it holds and worse for the rest, and every candidate here is
	// several times larger than this number.
	Inventory int `json:"inventory"`

	docs    []Doc
	refused []string
}

// Read measures a sample.
//
// Documents the identifier in sift does not call Vietnamese are counted and
// left out of everything else, because a run table built over an English page
// is a table about English and the rule is not being proposed for English.
func Read(source string, slots int, docs []Doc) Reading {
	r := Reading{Source: source, Slots: slots, docs: docs, Inventory: len(sift.Syllables())}

	counts := map[string]int{}
	perDoc := map[string]int64{}
	var segments [][]string
	for _, d := range docs {
		if d.Name == "" || d.Text == "" {
			continue
		}
		if !sift.Identify(d.Text).Vietnamese() {
			r.Refused++
			r.refused = append(r.refused, d.Name)
			continue
		}
		r.Docs++
		r.Bytes += int64(len(d.Text))
		for _, seg := range measure(&r, d.Name, d.Text, perDoc) {
			segments = append(segments, seg)
			for n := 2; n <= MaxRun; n++ {
				for i := 0; i+n <= len(seg); i++ {
					counts[strings.Join(seg[i:i+n], " ")]++
				}
			}
		}
	}

	r.Syllables = r.Marked + r.Bare
	if r.Units > 0 {
		r.Governed = float64(r.Syllables) / float64(r.Units)
	}
	for _, d := range docs {
		if perDoc[d.Name] > perDoc[r.Widest] {
			r.Widest = d.Name
		}
	}
	if r.Syllables > 0 && r.Widest != "" {
		r.WidestShare = float64(perDoc[r.Widest]) / float64(r.Syllables)
	}

	r.Runs, r.Candidates = table(counts, slots)
	r.Inside, r.Crossing = spend(segments, r.Runs, r.Syllables)
	if r.Syllables > 0 {
		r.InsideShare = float64(r.Inside) / float64(r.Syllables)
	}
	r.Cost = Atomic - r.Crossing
	return r
}

// measure classifies every whitespace unit of one document and hands back the
// stretches of syllables a merge could run over.
//
// A stretch ends at anything that is not a syllable and at any punctuation, so
// that a comma between two syllables stops a merge the way it would stop one in
// a tokenizer that pre-splits on punctuation, which every candidate here does.
func measure(r *Reading, name, text string, perDoc map[string]int64) [][]string {
	var out [][]string
	seg := make([]string, 0, MaxRun)
	end := func() {
		if len(seg) > 1 {
			out = append(out, seg)
		}
		seg = nil
	}
	for _, raw := range strings.Fields(text) {
		r.Units++
		cut := trim(raw)
		word := strings.ToLower(cut)
		switch {
		case word == "":
			r.Symbol++
			end()
			continue
		case sift.Syllable(word), sift.BareSyllable(word):
			if sift.Syllable(word) {
				r.Marked++
			} else {
				r.Bare++
			}
		case hasLetter(word) && hasDigit(word):
			r.Mixed++
			end()
			continue
		case hasDigit(word):
			r.Number++
			end()
			continue
		default:
			r.Foreign++
			end()
			continue
		}
		perDoc[name]++
		if !strings.HasPrefix(raw, cut) {
			end()
		}
		seg = append(seg, word)
		if !strings.HasSuffix(raw, cut) {
			end()
		}
	}
	end()
	return out
}

// table picks the runs the slots go to.
//
// The ordering is by what a slot buys, which is the tokens the merge takes off
// this text, and ties go to the run that spells first so that the same sample
// produces the same table twice. What makes this more than a sort is that the
// candidates overlap: theo số liệu của and theo số liệu are the same three
// thousand appearances counted twice, and a table that ranked them
// independently would sell the same saving to two slots and print a phrase
// three times in its top ten. So a run that takes a slot pays for it out of
// every shorter run inside it, since a tokenizer that matches longest first
// takes those occurrences for the longer merge and leaves the shorter one only
// what it has elsewhere.
func table(counts map[string]int, slots int) ([]Run, int) {
	left := make(map[string]int, len(counts))
	h := &runs{}
	for run, n := range counts {
		left[run] = n
		if n >= MinCount {
			*h = append(*h, mkRun(run, n))
		}
	}
	candidates := len(*h)
	heap.Init(h)

	out := make([]Run, 0, min(slots, candidates))
	for h.Len() > 0 && len(out) < slots {
		r := heap.Pop(h).(Run)
		switch now := left[r.Run]; {
		case now < MinCount:
			continue
		case now != r.Count:
			heap.Push(h, mkRun(r.Run, now))
			continue
		}
		out = append(out, r)
		for _, sub := range inside(r.Run) {
			left[sub] -= r.Count
		}
	}
	return out, candidates
}

// mkRun scores one candidate. A slot buys one token per boundary the merge
// removes, every time the run appears, which is why a two syllable run that
// turns up ten thousand times beats a four syllable run that turns up two.
func mkRun(run string, n int) Run {
	words := strings.Count(run, " ") + 1
	return Run{Run: run, Words: words, Count: n, Saves: n * (words - 1)}
}

// inside is every shorter run held within this one.
func inside(run string) []string {
	words := strings.Split(run, " ")
	var out []string
	for n := 2; n < len(words); n++ {
		for i := 0; i+n <= len(words); i++ {
			out = append(out, strings.Join(words[i:i+n], " "))
		}
	}
	return out
}

// runs is the candidate table as a heap, so that a run whose count was cut by a
// longer one gets rescored and falls back into the queue rather than keeping a
// place it no longer earns.
type runs []Run

func (r runs) Len() int      { return len(r) }
func (r runs) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
func (r runs) Less(i, j int) bool {
	if r[i].Saves != r[j].Saves {
		return r[i].Saves > r[j].Saves
	}
	return r[i].Run < r[j].Run
}
func (r *runs) Push(x any) { *r = append(*r, x.(Run)) }
func (r *runs) Pop() any {
	old := *r
	last := old[len(old)-1]
	*r = old[:len(old)-1]
	return last
}

// spend walks the text again with the table in hand and counts what it actually
// costs. The walk is longest match first, which is what a tokenizer with those
// merges in it does, and it is why the count is taken rather than added up out
// of the table: the same syllable cannot be swallowed by two merges, and adding
// the savings up would let it be.
func spend(segments [][]string, runs []Run, syllables int64) (int64, float64) {
	if syllables == 0 {
		return 0, 0
	}
	known := make(map[string]bool, len(runs))
	for _, run := range runs {
		known[run.Run] = true
	}

	var inside, saved int64
	for _, seg := range segments {
		for i := 0; i < len(seg); {
			n := min(MaxRun, len(seg)-i)
			for ; n > 1; n-- {
				if known[strings.Join(seg[i:i+n], " ")] {
					break
				}
			}
			if n > 1 {
				inside += int64(n)
				saved += int64(n - 1)
			}
			i += max(n, 1)
		}
	}
	return inside, float64(syllables-saved) / float64(syllables)
}

// Blocking is every reason this is not a reading of anything.
func (r Reading) Blocking() []string {
	var why []string
	if r.Source == "" {
		why = append(why, "the reading does not say what text it was taken over, and the cost of the rule is a fact about a corpus rather than about the language")
	}
	if r.Slots <= 0 {
		why = append(why, "the arm without the rule is allowed no vocabulary slots to spend, which makes it the arm with the rule under another name")
	}
	if len(r.docs) == 0 {
		return append(why, "no text was read, so there is nothing here the rule is about")
	}

	var noName, noText, twice tally
	seen := map[string]bool{}
	for _, d := range r.docs {
		switch {
		case d.Name == "":
			noName.add("")
		case seen[d.Name]:
			twice.add(d.Name)
		case d.Text == "":
			noText.add(d.Name)
		}
		seen[d.Name] = true
	}
	why = append(why,
		noName.say(
			"a document arrived with no name, and a run table nobody can trace back to the pages that produced it cannot be argued with",
			"%[1]d documents arrived with no name, and a run table nobody can trace back to the pages that produced it cannot be argued with"),
		twice.say(
			"%[2]s was read twice, and a document counted twice puts its own phrasing in the table",
			"%[1]d documents were read twice, the first of them %[2]s, and a document counted twice puts its own phrasing in the table"),
		noText.say(
			"%[2]s holds no text",
			"%[1]d documents hold no text, the first of them %[2]s"),
	)

	if r.Docs == 0 {
		why = append(why, "the identifier called none of these documents Vietnamese, so what is here is a sample of another language")
	} else if r.Syllables == 0 {
		why = append(why, "the text holds no Vietnamese syllables, so there is nothing for the rule to govern")
	}
	return said(why)
}

// Faults are the reasons a reading that ran is not the reading it looks like.
func (r Reading) Faults() []string {
	if len(r.Blocking()) > 0 {
		return nil
	}
	var out []string

	if r.Syllables < MinSyllables {
		out = append(out, fmt.Sprintf(
			"the sample holds %s, under the %s a run has to be counted against before %d appearances stops being a property of the draw",
			thousands(r.Syllables, "syllable"), thousands(MinSyllables, "syllable"), MinCount))
	}
	if r.Docs < MinDocs {
		out = append(out, fmt.Sprintf(
			"the reading is taken over %s, under the %d it takes before a table of phrases is a table about a corpus rather than about the pages that happened to be in it",
			plural(r.Docs, "document"), MinDocs))
	}
	if r.WidestShare > MaxDoc {
		out = append(out, fmt.Sprintf(
			"%s supplies %s of the syllables, over the %s any one document may, so the runs that pay best are the ones that page repeats",
			r.Widest, share(r.WidestShare), share(MaxDoc)))
	}
	if r.Governed < MinGoverned {
		out = append(out, fmt.Sprintf(
			"the rule governs %s of the text, under %s, and the rest of it falls through to whatever the tokenizer would have done anyway, so what is being compared here is two tokenizers that agree about %s of what they read",
			share(r.Governed), share(MinGoverned), share(1-r.Governed)))
	}
	if r.Refused > 0 {
		out = append(out, fmt.Sprintf(
			"%s in the sample %s not Vietnamese to the identifier and %s left out, starting with %s, so the reading covers less of the draw than it was handed",
			plural(r.Refused, "document"), was(r.Refused), were(r.Refused), r.refused[0]))
	}
	if len(r.Runs) >= r.Slots {
		out = append(out, fmt.Sprintf(
			"all %s went to a run that still pays for it, so what is priced here is what the %d best merges buy rather than what the rule gives up, and the number moves again if the arm is given more of the vocabulary",
			plural(r.Slots, "slot"), r.Slots))
	}
	if len(r.Runs) == 0 {
		out = append(out, fmt.Sprintf(
			"no run of syllables turns up %d times in this text, so what the rule gives up could not be measured here, which is not the same reading as it giving up nothing",
			MinCount))
	}
	return out
}

// Holds reports whether this reading settles the half of the question it is for.
func (r Reading) Holds() bool { return len(r.Blocking()) == 0 && len(r.Faults()) == 0 }

// Verdict is the reading in one paragraph.
func (r Reading) Verdict() string {
	if why := r.Blocking(); len(why) > 0 {
		return why[0]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Over %s of %s, a syllable-atomic rule governs %s of what the text is made of. The runs worth a slot cover %s of the syllables, and the %s that went to them %s the same vocabulary from %.2f tokens per syllable to %.2f, so the rule gives up %s of the tokens before a step is trained.",
		thousands(r.Syllables, "syllable"), r.Source, share(r.Governed),
		share(r.InsideShare), thousands(int64(len(r.Runs)), "slot"), take(len(r.Runs)), Atomic, r.Crossing, share(r.Cost))
	if len(r.Runs) > 0 {
		fmt.Fprintf(&b, " The slot that buys most is %s, at %s.", quoted(r.Runs[0].Run), thousands(int64(r.Runs[0].Count), "appearance"))
	}
	fmt.Fprintf(&b, " What the rule buys is not in this reading and is not in any reading taken off text, since it is a claim about what a model learns from the boundary, which is P07-3 and needs the slate.")

	faults := r.Faults()
	switch n := len(faults); n {
	case 0:
	case 1:
		fmt.Fprintf(&b, " One reading says this is not the sample it looks like: %s.", faults[0])
	default:
		fmt.Fprintf(&b, " %d readings say this is not the sample it looks like: %s.", n, strings.Join(faults, "; and "))
	}
	return b.String()
}

// trim cuts the punctuation off both ends of a unit and leaves the inside
// alone, which is what sift does, so that a syllable in quotes and a syllable
// at the end of a sentence are the same syllable.
func trim(tok string) string {
	return strings.TrimFunc(tok, func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsDigit(c) && !unicode.Is(unicode.Mn, c)
	})
}

func hasLetter(s string) bool {
	return strings.ContainsFunc(s, unicode.IsLetter)
}

func hasDigit(s string) bool {
	return strings.ContainsFunc(s, unicode.IsDigit)
}

// A tally counts one kind of bad document and remembers the first, since one bad
// export writes the same fault onto every page it produced.
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

func (t tally) say(one, many string) string {
	f := one
	if t.n > 1 {
		f = many
	}
	switch {
	case t.n == 0:
		return ""
	case !strings.Contains(f, "%"):
		return f
	}
	return fmt.Sprintf(f, t.n, t.first)
}

// said drops the empty sentences a tally hands back when its kind never fired.
func said(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func quoted(s string) string { return fmt.Sprintf("%q", s) }

func share(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// thousands is how this project quotes a count of things it read, which is with
// separators, because a run table is argued about by people reading it off a
// terminal.
func thousands(n int64, noun string) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if n == 1 {
		return s + " " + noun
	}
	return s + " " + noun + "s"
}

func was(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func were(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

func take(n int) string {
	if n == 1 {
		return "takes"
	}
	return "take"
}
