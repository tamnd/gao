package count

// The ten gates a tokenizer passes before anything else about it is discussed.

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/normalize"
)

// An Encoder is everything the gates need from a tokenizer.
//
// It is an interface rather than a *[Tokenizer] for one reason: a gate that has
// never been seen to fail has not been tested. The pinned tokenizer passes all
// ten, so the only way to find out whether a gate can catch anything is to hand
// the suite a tokenizer that breaks the rule on purpose, and that is a test
// fixture rather than a 4.7 MB protobuf.
type Encoder interface {
	// Name is what a report calls this tokenizer.
	Name() string

	// Vocab is how many identities the vocabulary holds, which is the range the
	// audit in T10 walks.
	Vocab() int

	// Encode splits text into pieces, adding no sequence markers.
	Encode(text string) []Piece

	// Decode turns identities back into text.
	Decode(ids []int) string
}

// ThroughputFloor is the T9 requirement in megabytes per second per core.
//
// It comes from the pipeline rather than from the tokenizer: doc 06 sizes the
// ingest around counting tokens in the pass that already reads the bytes, and a
// tokenizer slower than this turns that pass into a tokenizing pass that also
// reads bytes.
const ThroughputFloor = 20.0

// The gates, in the order doc 07 §8 lists them.
const (
	roundTrip = iota
	roundTripBare
	roundTripMixed
	wholeCharacters
	diacriticAtomicity
	nfcStable
	digitGrouping
	leadingSpace
	throughput
	vocabularyAudit
	numGates
)

// A Gate is one of the ten checks, with what it found.
//
// Checked and Failed are counted in Unit, which is not always documents. T4 and
// T5 are counted in token boundaries because one document can hold a thousand
// of them and reporting a document as one failure hides how bad it is. T7 is
// counted in digit runs and T8 in syllables for the same reason.
type Gate struct {
	Name string
	What string
	Unit string

	Checked int64
	Failed  int64

	// Skipped is work the gate could not do. A boundary gate cannot say
	// anything about a document whose pieces do not add up to the document, so
	// it declines rather than guesses, and says how often.
	Skipped int64

	// Ran is false when the sample held nothing the gate applies to. A gate that
	// did not run is not a gate that passed, and the difference is the whole
	// reason this field exists.
	Ran bool

	// Why is set when Ran is false, and Note carries whatever a reader needs to
	// judge the number: a throughput, a distribution, the share of the sample
	// the gate was vacuous on.
	Why  string
	Note string

	// Audit marks T10, which is a list for a person to read rather than a
	// threshold. It never fails and it is never a pass either.
	Audit bool

	// Sample names the first few things that failed, so that the next step is
	// opening one of them rather than looking for one.
	Sample []string
}

// Passed reports whether this gate is satisfied. An audit is never a pass, and
// neither is a gate with nothing to run on.
func (g Gate) Passed() bool { return !g.Audit && g.Ran && g.Failed == 0 }

// Status is the one word a report prints.
func (g Gate) Status() string {
	switch {
	case g.Audit:
		return "audited"
	case !g.Ran:
		return "not run"
	case g.Failed > 0:
		return "failed"
	default:
		return "passed"
	}
}

// GateOptions is how a run is selected and reported.
type GateOptions struct {
	// OneIn keeps one document in this many, chosen by document identity rather
	// than by a random number, so a run is reproducible without a seed and two
	// boxes given the same corpus check the same documents. Zero and one both
	// mean keep everything.
	OneIn int

	// Limit stops after this many documents. Zero means no limit.
	Limit int

	// Sample is how many failing examples each gate names. Zero means five.
	Sample int
}

// A Gates runs the suite over documents as they stream past.
//
// It streams because the sample doc 07 asks for is ten million documents, which
// is a few hundred gigabytes, and a suite that wanted them in memory could not
// be pointed at the corpus it is supposed to check.
type Gates struct {
	enc  Encoder
	opts GateOptions

	gates [numGates]Gate

	docs      int64
	chars     int64
	syllables int64
	tokens    int64

	// encoded and bytes are the T9 measurement: the time spent inside Encode
	// and nothing else, over the text handed to it.
	encoded time.Duration
	bytes   int64

	// notNFC is how many documents in the sample were not already NFC. When it
	// is zero, T6 compared every document against itself, which is a fact about
	// the sample rather than about the tokenizer, and the report says so.
	notNFC int64

	// digits remembers how each digit run tokenized the first time it was seen,
	// which is what makes T7 a check on context dependence rather than on
	// grouping.
	digits map[string]string

	// inventory is the distinct Vietnamese syllables seen, for T8. It is capped
	// because a corpus holds about six thousand syllable types and a cap that
	// is never reached costs nothing, while an uncapped map on a bad detector
	// costs the run.
	inventory map[string]bool
}

// inventoryCap is where the syllable inventory stops growing. Vietnamese has
// about 6,200 syllable types in use, so this is roughly twice the language.
const inventoryCap = 16384

// NewGates starts a run.
func NewGates(enc Encoder, opts GateOptions) *Gates {
	if opts.OneIn < 1 {
		opts.OneIn = 1
	}
	if opts.Sample < 1 {
		opts.Sample = 5
	}
	g := &Gates{
		enc:       enc,
		opts:      opts,
		digits:    map[string]string{},
		inventory: map[string]bool{},
	}
	g.gates = [numGates]Gate{
		roundTrip:          {Name: "T1", What: "decode(encode(x)) is x", Unit: "documents"},
		roundTripBare:      {Name: "T2", What: "and on the same text with its marks taken off", Unit: "documents"},
		roundTripMixed:     {Name: "T3", What: "and on documents mixing Vietnamese, English and code", Unit: "documents"},
		wholeCharacters:    {Name: "T4", What: "no token boundary lands inside a character", Unit: "boundaries"},
		diacriticAtomicity: {Name: "T5", What: "no token boundary separates a letter from its marks", Unit: "boundaries"},
		nfcStable:          {Name: "T6", What: "encode(NFC(x)) is encode(x)", Unit: "documents"},
		digitGrouping:      {Name: "T7", What: "a run of digits tokenizes the same way wherever it appears", Unit: "digit runs"},
		leadingSpace:       {Name: "T8", What: "a leading space is handled the same way for every syllable", Unit: "syllables"},
		throughput:         {Name: "T9", What: fmt.Sprintf("at least %.0f MB/s on one core", ThroughputFloor), Unit: ""},
		vocabularyAudit:    {Name: "T10", What: "no piece is reachable only from text gao would reject", Unit: "pieces", Audit: true},
	}
	return g
}

// Full reports whether the limit has been reached, so a caller can stop reading
// instead of handing over documents that will be dropped.
func (g *Gates) Full() bool {
	return g.opts.Limit > 0 && g.docs >= int64(g.opts.Limit)
}

// Add runs the per-document gates over one document and reports whether it was
// part of the sample.
func (g *Gates) Add(id doc.Hash, text string) bool {
	if g.Full() {
		return false
	}
	if g.opts.OneIn > 1 && doc.Shard(id, g.opts.OneIn) != 0 {
		return false
	}
	g.docs++

	start := time.Now()
	ps := g.enc.Encode(text)
	g.encoded += time.Since(start)
	g.bytes += int64(len(text))

	g.chars += int64(utf8.RuneCountInString(text))
	g.syllables += int64(doc.Syllables(text))
	g.tokens += int64(len(ps))

	back := g.enc.Decode(identities(ps))
	g.record(roundTrip, id.String(), intact(back, text))

	bare := normalize.Bare(text)
	g.record(roundTripBare, id.String(), g.roundTrips(bare))

	if mixed(text) {
		g.record(roundTripMixed, id.String(), intact(back, text))
	}

	if nfc := norm.NFC.String(text); nfc != text {
		g.notNFC++
		g.record(nfcStable, id.String(), slices.Equal(identities(g.enc.Encode(nfc)), identities(ps)))
	} else {
		g.record(nfcStable, id.String(), true)
	}

	// The boundary gates need the pieces to account for the text exactly. When
	// they do not, the offsets are offsets into something else and any answer
	// they give is about the wrong string, so they decline and say how often.
	joined, ends := spans(ps)
	if joined != text {
		g.gates[wholeCharacters].Skipped++
		g.gates[diacriticAtomicity].Skipped++
		g.gates[digitGrouping].Skipped++
		return true
	}

	g.boundaries(id, text, ends)
	g.digitRuns(id, text, ends)
	g.collect(text)
	return true
}

// roundTrips is T1 on a string other than the document, which T2 needs.
func (g *Gates) roundTrips(text string) bool {
	return intact(g.enc.Decode(identities(g.enc.Encode(text))), text)
}

// intact reports whether a round trip came back with the text.
//
// The normalized form counts as coming back with it, and only for a document
// that did not arrive normalized. That is not a loosening of T1, it is what
// makes T1 and T6 possible to satisfy at the same time. T6 asks that a
// document and its normalized form encode identically, and a tokenizer that
// satisfies it on a document written with its marks separate has only one
// sequence of identities to decode back, so one of the two forms is what it
// returns and the other is what T1 would call a failure. No tokenizer can pass
// both readings, so the strict one has to give somewhere, and this is the
// place: the corpus is NFC by the time it reaches a tokenizer, because
// normalize normalizes at ingest, and a round trip that comes back with the
// form the pipeline would have produced has lost nothing anybody was going to
// keep.
//
// For a document that is already NFC, which is every document in the store, this
// is a byte comparison and nothing else.
func intact(back, text string) bool {
	return back == text || back == norm.NFC.String(text)
}

// boundaries runs T4 and T5 over one document.
//
// Both look at the same places, which is why they are one pass: the interior
// token boundaries, as byte offsets into the text. T4 asks whether the boundary
// is in the middle of a character at all. T5 asks the narrower question that
// matters for Vietnamese, which is whether a base letter has been parted from
// its marks, either by splitting a precomposed character or by cutting between
// a letter and a combining mark that belongs to it.
func (g *Gates) boundaries(id doc.Hash, text string, ends []int) {
	for _, off := range ends {
		if off <= 0 || off >= len(text) {
			continue
		}
		g.gates[wholeCharacters].Checked++
		g.gates[wholeCharacters].Ran = true
		g.gates[diacriticAtomicity].Checked++
		g.gates[diacriticAtomicity].Ran = true

		if !utf8.RuneStart(text[off]) {
			g.fail(wholeCharacters, fmt.Sprintf("%s at byte %d", id, off))
			// A character split down the middle is a letter parted from its
			// marks whenever the character was a marked letter, and on
			// Vietnamese it usually was.
			if marked(runeAround(text, off)) {
				g.fail(diacriticAtomicity, fmt.Sprintf("%s at byte %d, inside %q", id, off, string(runeAround(text, off))))
			}
			continue
		}
		next, _ := utf8.DecodeRuneInString(text[off:])
		if unicode.In(next, unicode.Mn) {
			prev, _ := utf8.DecodeLastRuneInString(text[:off])
			if unicode.IsLetter(prev) {
				g.fail(diacriticAtomicity, fmt.Sprintf("%s at byte %d, between %q and its mark", id, off, string(prev)))
			}
		}
	}
}

// digitRuns runs T7 over one document.
//
// The question is not how a tokenizer groups digits. Single digits, pairs and
// threes are all defensible and the choice shows up in arithmetic ability
// rather than in correctness. The question is whether it groups the same run
// the same way in different company, because a tokenizer that splits 2024 one
// way in a date and another way in a price has made every number two things.
func (g *Gates) digitRuns(id doc.Hash, text string, ends []int) {
	for a := 0; a < len(text); {
		if !isDigit(text[a]) {
			a++
			continue
		}
		b := a
		for b < len(text) && isDigit(text[b]) {
			b++
		}
		run := text[a:b]
		sig := grouping(ends, a, b)
		g.gates[digitGrouping].Checked++
		g.gates[digitGrouping].Ran = true
		if was, seen := g.digits[run]; seen {
			if was != sig {
				g.fail(digitGrouping, fmt.Sprintf("%s: %s is %s here and %s elsewhere", id, run, sig, was))
			}
		} else if len(g.digits) < inventoryCap {
			g.digits[run] = sig
		}
		a = b
	}
}

// collect adds the document's Vietnamese syllables to the T8 inventory.
//
// A word counts as a Vietnamese syllable when it is letters throughout and at
// least one of them is not ASCII. That is deliberately narrow. It admits no
// English, which would answer a different question, and it misses the
// unmarked syllables like tam and con, which is a loss the gate can afford
// because it is asking about uniformity across a set rather than measuring the
// set.
func (g *Gates) collect(text string) {
	for word := range strings.FieldsSeq(text) {
		if len(g.inventory) >= inventoryCap {
			return
		}
		if syllable(word) {
			g.inventory[strings.ToLower(word)] = true
		}
	}
}

// record folds one document sized result into a gate.
func (g *Gates) record(i int, name string, ok bool) {
	g.gates[i].Checked++
	g.gates[i].Ran = true
	if !ok {
		g.fail(i, name)
	}
}

func (g *Gates) fail(i int, name string) {
	g.gates[i].Ran = true
	g.gates[i].Failed++
	if len(g.gates[i].Sample) < g.opts.Sample {
		g.gates[i].Sample = append(g.gates[i].Sample, name)
	}
}

// A GateReport is what a run of the suite found.
type GateReport struct {
	Tokenizer string
	Vocab     int

	Documents int64
	Chars     int64
	Syllables int64
	Tokens    int64

	// MBPerSecond is the T9 measurement, over one goroutine, counting only the
	// time inside Encode.
	MBPerSecond float64

	Gates []Gate
}

// CharsPerToken is the measured fertility, which P07-5 predicts at 3.0 for
// Vietnamese under the pinned tokenizer.
func (r GateReport) CharsPerToken() float64 {
	if r.Tokens == 0 {
		return 0
	}
	return float64(r.Chars) / float64(r.Tokens)
}

// TokensPerSyllable is the same fertility in the unit that matters for a budget:
// the multiplier on every training run and the divisor on every context window.
func (r GateReport) TokensPerSyllable() float64 {
	if r.Syllables == 0 || r.Tokens == 0 {
		return 0
	}
	return float64(r.Tokens) / float64(r.Syllables)
}

// Eligible reports whether this tokenizer may be used, which needs every gate
// except the audit to have run and passed. A gate that found nothing to run on
// leaves the suite incomplete, and an incomplete suite is not an answer.
func (r GateReport) Eligible() bool {
	for _, g := range r.Gates {
		if !g.Audit && !g.Passed() {
			return false
		}
	}
	return true
}

// Correct reports whether every gate about what a tokenizer does to text ran and
// passed. It is the question the coverage set can answer, and it is a weaker
// question than [GateReport.Eligible] by exactly one gate.
//
// T9 is the one left out. It is a measurement of the machine as much as of the
// tokenizer, it needs more text than the coverage set holds before its number
// means anything, and a suite that reported a four kilobyte run as an
// eligibility decision would be handing somebody a claim they could not defend.
func (r GateReport) Correct() bool {
	for _, g := range r.Gates {
		if g.Audit || g.Name == ThroughputGate {
			continue
		}
		if !g.Passed() {
			return false
		}
	}
	return true
}

// ThroughputGate names T9. A report is a value and is judged by what is in it
// rather than by the order it happens to be in, so the one gate that is treated
// differently is found by name.
const ThroughputGate = "T9"

// Report closes the run and returns what it found.
//
// The three gates that are not per-document are settled here: T8 over the
// syllable inventory the run collected, T9 from the time spent encoding, and
// the T10 audit over the vocabulary. Report can be called more than once and
// says the same thing each time.
func (g *Gates) Report() GateReport {
	gates := g.gates

	if g.gates[nfcStable].Ran {
		gates[nfcStable].Note = fmt.Sprintf("%d of %d documents were not NFC as given", g.notNFC, g.docs)
		if g.notNFC == 0 {
			gates[nfcStable].Note += ", so this compared every document against itself"
		}
	}

	gates[leadingSpace] = g.leadingSpace(gates[leadingSpace])
	gates[throughput] = g.throughput(gates[throughput])
	gates[vocabularyAudit] = g.audit(gates[vocabularyAudit])

	for i, gate := range gates {
		if gate.Ran || gate.Why != "" {
			continue
		}
		gates[i].Why = "nothing in the sample to run it on"
	}
	if !gates[roundTripMixed].Ran {
		gates[roundTripMixed].Why = "no document in the sample mixed Vietnamese, English and code"
	}

	return GateReport{
		Tokenizer:   g.enc.Name(),
		Vocab:       g.enc.Vocab(),
		Documents:   g.docs,
		Chars:       g.chars,
		Syllables:   g.syllables,
		Tokens:      g.tokens,
		MBPerSecond: g.rate(),
		Gates:       gates[:],
	}
}

// leadingSpace settles T8.
//
// A tokenizer either folds the space before a syllable into the syllable's
// first piece or leaves it as a piece of its own. Either is workable. Doing one
// for some syllables and the other for the rest is not, because then the same
// word is two different token sequences depending on whether it opened a line,
// and the model has to learn both.
func (g *Gates) leadingSpace(gate Gate) Gate {
	if len(g.inventory) == 0 {
		gate.Why = "no Vietnamese syllable in the sample"
		return gate
	}
	words := make([]string, 0, len(g.inventory))
	for w := range g.inventory {
		words = append(words, w)
	}
	sort.Strings(words)

	counts := map[string]int{}
	var first string
	for _, w := range words {
		c := g.spacing(w)
		counts[c]++
		gate.Checked++
		gate.Ran = true
		if first == "" {
			first = c
			continue
		}
		if c != first {
			gate.Failed++
			if len(gate.Sample) < g.opts.Sample {
				gate.Sample = append(gate.Sample, fmt.Sprintf("%q is %s where the rest are %s", w, c, first))
			}
		}
	}
	kinds := make([]string, 0, len(counts))
	for k, n := range counts {
		kinds = append(kinds, fmt.Sprintf("%s %d", k, n))
	}
	sort.Strings(kinds)
	gate.Note = strings.Join(kinds, ", ")
	return gate
}

// spacing says what a tokenizer does with the space in front of a syllable.
//
// The question is only about the space. What happens to the rest of the
// syllable is not asked, because a leading space changes which merges are
// available and a tokenizer is entitled to cut the word differently because of
// it. What it is not entitled to do is put the space in a token of its own here
// and inside the first token of the word there.
func (g *Gates) spacing(word string) string {
	lead := g.enc.Encode(" " + word)
	switch {
	case len(lead) == 0:
		return "nothing at all"
	case lead[0].Text == " ":
		return "its own token"
	case strings.HasPrefix(lead[0].Text, " "):
		return "folded in"
	default:
		return "dropped or moved"
	}
}

// MinEncodeTime is how long a run has to have spent inside Encode before the
// rate it computed is a measurement rather than a reading of the clock.
//
// It is a time and not a size on purpose. Below a few milliseconds the number is
// dominated by the first call warming a cache and by whatever else the machine
// was doing, and it swings by a factor of two between runs on an idle laptop. A
// tokenizer slow enough to matter spends longer than this on any sample worth
// timing, so the threshold costs nothing in the direction the gate is for.
const MinEncodeTime = 10 * time.Millisecond

// MinEncodeBytes is how much text a run has to have put through Encode before
// the rate it computed is about the tokenizer at all.
//
// The time floor alone is not enough, and which way it fails depends on the
// machine. A clock that ticks every 15.6 milliseconds, which is the ordinary
// Windows case, reports one tick for four kilobytes of encoding and one tick is
// over the floor, so the gate would run and print a rate computed from the
// granularity of the clock rather than from anything the tokenizer did. The
// size floor is the same judgement stated in the units the sample is actually
// too small in, and it says the same thing on every machine.
const MinEncodeBytes = 1_000_000

// throughput settles T9.
//
// A sample too small to time and a run with nothing in it are different
// findings and are reported as different findings. A clock that did not tick is
// the first of the two, not the second, and on Windows it is the ordinary case
// for a sample this size rather than a curiosity.
func (g *Gates) throughput(gate Gate) Gate {
	if g.bytes == 0 {
		gate.Why = "nothing was encoded"
		return gate
	}
	if g.bytes < MinEncodeBytes {
		gate.Why = fmt.Sprintf("%d bytes went through Encode, and a rate over less than %d is a reading of the clock rather than a measurement of the tokenizer", g.bytes, MinEncodeBytes)
		return gate
	}
	if g.encoded < MinEncodeTime {
		gate.Why = fmt.Sprintf("encoding %d bytes took %v, and a rate computed from that is a reading of the clock", g.bytes, g.encoded.Round(time.Microsecond))
		return gate
	}
	rate := g.rate()
	gate.Ran = true
	gate.Checked = g.bytes
	gate.Unit = "bytes"
	gate.Note = fmt.Sprintf("%.1f MB/s on one core over %.1f MB", rate, float64(g.bytes)/1e6)
	if rate < ThroughputFloor {
		gate.Failed = 1
		gate.Sample = append(gate.Sample, fmt.Sprintf("%.1f MB/s is under the %.0f MB/s the pipeline is sized for", rate, ThroughputFloor))
	}
	return gate
}

func (g *Gates) rate() float64 {
	if g.bytes == 0 || g.encoded == 0 {
		return 0
	}
	return float64(g.bytes) / 1e6 / g.encoded.Seconds()
}

// audit settles T10, which is a list rather than a threshold.
//
// The gate asks whether the vocabulary holds pieces that are only reachable
// from text gao would reject, because such a piece is a fact about the
// tokenizer's training corpus: spam boilerplate, adult site furniture, and
// mojibake all leave tokens behind. There is no threshold on this and there
// should not be, since one such piece is a hint and a thousand is a different
// tokenizer. What the audit can do without a person is find the pieces whose
// characters gao's own cleaning would have removed, and print them.
func (g *Gates) audit(gate Gate) Gate {
	var special int64
	for id := range g.enc.Vocab() {
		s := g.enc.Decode([]int{id})
		if s == "" || s == string(utf8.RuneError) {
			// A control piece decodes to nothing and a byte fallback piece to
			// the replacement character, since one byte of a character is not a
			// character. Neither is a finding.
			special++
			continue
		}
		gate.Checked++
		why, bad := unusable(s)
		if !bad {
			continue
		}
		gate.Failed++
		if len(gate.Sample) < g.opts.Sample {
			gate.Sample = append(gate.Sample, fmt.Sprintf("%d %q: %s", id, s, why))
		}
	}
	gate.Ran = gate.Checked > 0
	gate.Note = fmt.Sprintf("%d pieces read, %d control or byte fallback, %d for a person to look at", gate.Checked, special, gate.Failed)
	return gate
}

// unusable reports whether a vocabulary piece is made of characters gao strips
// or refuses, and which one it is.
func unusable(piece string) (string, bool) {
	for _, r := range piece {
		switch {
		case r == utf8.RuneError:
			return "the replacement character, which is what mojibake decodes to", true
		case unicode.IsControl(r) && r != '\n' && r != '\t':
			return "a control character", true
		case unicode.In(r, unicode.Co):
			return "a private use character", true
		case unicode.In(r, unicode.Cf):
			return "an invisible formatting character", true
		}
	}
	if norm.NFC.String(piece) != piece {
		return "not NFC, so it is reachable only from text phoi would have rewritten", true
	}
	return "", false
}

// spans joins the pieces and returns the byte offset each one ends at.
func spans(ps []Piece) (string, []int) {
	var b strings.Builder
	ends := make([]int, len(ps))
	n := 0
	for i, p := range ps {
		b.WriteString(p.Text)
		n += len(p.Text)
		ends[i] = n
	}
	return b.String(), ends
}

// grouping describes how the byte range [a,b) was cut up, as the sizes of the
// pieces covering it. A piece that reaches outside the range is marked, since a
// digit swallowed into a word is a different thing from a digit in a group.
func grouping(ends []int, a, b int) string {
	i := sort.SearchInts(ends, a+1)
	parts := make([]string, 0, 4)
	for ; i < len(ends) && start(ends, i) < b; i++ {
		lo, hi := max(start(ends, i), a), min(ends[i], b)
		part := strconv.Itoa(hi - lo)
		if start(ends, i) < a || ends[i] > b {
			part += "+"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "-")
}

func start(ends []int, i int) int {
	if i == 0 {
		return 0
	}
	return ends[i-1]
}

func identities(ps []Piece) []int {
	ids := make([]int, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	return ids
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// runeAround decodes the character that byte offset off falls inside.
func runeAround(text string, off int) rune {
	for i := off; i > 0 && off-i < utf8.UTFMax; i-- {
		if utf8.RuneStart(text[i]) {
			r, _ := utf8.DecodeRuneInString(text[i:])
			return r
		}
	}
	r, _ := utf8.DecodeRuneInString(text)
	return r
}

// marked reports whether a character is a letter carrying diacritics: ế yes, e
// no, 語 no. Đ counts, because the stroke is part of the letter and a tokenizer
// that cuts it in half has done to it what cutting the hat off an â does.
func marked(r rune) bool {
	if r == 'đ' || r == 'Đ' {
		return true
	}
	if r < utf8.RuneSelf || !unicode.IsLetter(r) {
		return false
	}
	d := []rune(norm.NFD.String(string(r)))
	return len(d) > 1 && d[0] < utf8.RuneSelf && unicode.IsLetter(d[0])
}

// syllable reports whether a word is a Vietnamese syllable for T8's purposes.
func syllable(word string) bool {
	word = strings.Trim(word, ".,;:!?()[]{}\"'“”‘’…")
	if word == "" {
		return false
	}
	nonASCII := false
	for _, r := range word {
		if !unicode.IsLetter(r) {
			return false
		}
		if r >= utf8.RuneSelf {
			nonASCII = true
		}
	}
	return nonASCII
}

// mixed reports whether a document holds Vietnamese, English and code at once,
// which is what T3 is about. The combination is common in the corpus, it is
// where the tokenizer has to switch behavior most often, and it is where a
// round trip breaks if it is going to.
//
// The foreign test is the one worth explaining. An ASCII word is not English by
// itself, since Vietnamese typed without its marks is ASCII too. What no
// Vietnamese syllable has is f, j, w or z, and no Vietnamese syllable is longer
// than seven letters, so a word that breaks either rule came from somewhere
// else.
func mixed(text string) bool {
	var vietnamese, foreign, code bool
	run, odd := 0, false
	for _, r := range text {
		switch {
		case r >= utf8.RuneSelf && unicode.IsLetter(r):
			vietnamese = true
			run, odd = 0, false
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			run++
			if strings.ContainsRune("fjwzFJWZ", r) {
				odd = true
			}
			if run > 7 || (odd && run > 1) {
				foreign = true
			}
		default:
			run, odd = 0, false
			if strings.ContainsRune("{}<>/\\|=;$#`~*_", r) {
				code = true
			}
		}
	}
	return vietnamese && foreign && code
}
