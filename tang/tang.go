// Package tang reads an estimate that was taken layer by layer and says what
// the layers nobody opened are worth.
//
// Tầng is a layer. HPLT v3 ships its Vietnamese in quality buckets, and the
// 176B this project used to quote was taken by sampling five of them at 40 MB
// each and weighting the readings by the size of every bucket on disk. That is a
// stratified estimate, and it is a reasonable way to read a corpus nobody has
// time to read all of. What it is not is the thing uoc computes, and the
// difference is the reason this package exists.
//
// The five were five of a supposed ten. vie_Latn ships six, numbered 5 through
// 10, and all six have been read now, which is where the 143.7B that replaced
// the 176B comes from.
//
// # Why an interval over the part that was read says nothing about the part that was not
//
// uoc draws parts at random from one population and reports how much the answer
// would move if a different draw had come back. That interval narrows as the
// sample grows, and it is the right interval for the question it answers. It is
// the wrong one here. Reading a sixth bucket does not narrow it, reading the
// same five buckets harder narrows it while leaving the estimate exactly as
// wrong as it was, and no amount of reading inside the five says anything at all
// about the five that were skipped.
//
// So the range this package computes is not a sampling interval. It is the
// bound on what was never opened: every unread byte at the lowest rate any layer
// read at, and every unread byte at the highest. That range is honest, it is
// usually wide, and it does not move when somebody reads more of what they had
// already read. The two are different quantities and they add. A published
// estimate needs both.
//
// # Why the layers people skip are the ones that flatter the number
//
// An earlier reading of the same corpus sampled only the top quality bucket and
// came back with 194B. The 176B figure came from a broader sample and the gap
// between them is 10% in the flattering direction, which is what you would
// expect: clean prose spends fewer bytes on markup and boilerplate, so it reads
// at a higher rate per byte, so scaling the rate of the cleanest text over all
// of the text buys tokens that are not there. Nobody chose the top bucket to
// inflate the number. It is the bucket you reach for when you want a rate to
// settle quickly, and the bias arrives on its own.
//
// That is what the ordering on a layer is for. When the mass nobody read sits
// below every layer that was read, the report says so in those words, because a
// range is easy to skim past and a sentence naming which end of the corpus was
// left out is not.
//
// # Why weighting by what a layer costs on disk is a third assumption
//
// The weights come off the manifest, which knows what each layer takes on disk
// and not what it holds in text. Weighting by stored size is assuming a byte on
// disk carries the same amount of text everywhere, and repetitive text
// compresses better than prose does, so the assumption fails in the same
// direction as everything else here. Every layer that was read gives a
// measurement of its own packing, and when those disagree by more than a
// quarter the report says the weights on the unread layers carry that much of
// their own error.
package tang

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tamnd/gao/uoc"
)

// MaxDark is the share of a source that may sit in layers nobody read before
// the estimate stops being an estimate of the source and starts being an
// estimate of the part somebody had time for. Ten percent is chosen so that a
// layer left out for a good reason is reportable rather than fatal, and so that
// leaving out half of a corpus is neither.
const MaxDark = 0.10

// MaxYieldSpread is how far apart the layers that were read may be, as a ratio
// of the richest to the thinnest, before one pooled rate over the unread layers
// is a choice rather than a reading. Under it the layers are close enough that
// which one an unread byte resembles does not matter much. Over it, it is the
// only thing that matters.
const MaxYieldSpread = 1.15

// MaxPackSpread is the same question asked about text per stored byte, which is
// the assumption the weights rest on rather than the assumption the rate rests
// on. A quarter is wider because compression varies more than tokenization does
// and a narrower line here would fire on every real corpus.
const MaxPackSpread = 1.25

// MinRead is how much of a layer has to be read before its rate is about the
// layer. Eight megabytes is a few thousand Vietnamese documents, which is
// enough that one long block of boilerplate cannot move the rate and few enough
// that reading every layer stays affordable.
const MinRead = 8_000_000

// MinShare is the same question asked as a fraction of the layer rather than as
// an amount, because the two catch different mistakes. MinRead catches a rate
// that one long page can move. This catches a rate that is perfectly steady and
// belongs to a fortieth of a percent of the layer it is about to be multiplied
// across.
//
// One percent is little to ask of a small layer and a great deal to ask of a
// large one, which is the point of asking it this way. The reading of HPLT v3
// vie_Latn takes 40 MB off a 94.9 GB bucket, which is 0.04%, and the number that
// comes out of it is worth quoting. It is not worth quoting without that beside
// it.
const MinShare = 0.01

// A Layer is one stratum of a source: what it holds, where it sits in the
// quality ordering, and what was read out of it, which for most layers of most
// samples is nothing.
type Layer struct {
	Name string `json:"name"`

	// Rank is where the layer sits in the ordering the source publishes, 1
	// lowest. It is what lets the report say which end of the corpus went
	// unread rather than only how much of it did.
	Rank int `json:"rank"`

	// Stored is what the layer takes on disk, off the manifest, known without
	// reading a byte of it. This is the weight.
	Stored int64 `json:"stored"`

	// Read is how much of that was actually fetched, and Text is how much text
	// came out of it once decompressed. The two together are the only
	// measurement anybody has of how much text a stored byte of this layer
	// holds.
	Read int64 `json:"read"`
	Text int64 `json:"text"`

	Tokens    int64  `json:"tokens"`
	Tokenizer string `json:"tokenizer"`
}

// Sampled reports whether anything was read out of this layer.
func (l Layer) Sampled() bool { return l.Read > 0 }

// Yield is tokens per stored byte, which is the quantity the weights are
// multiplied by and the one the whole estimate turns on.
func (l Layer) Yield() float64 {
	if l.Read <= 0 {
		return 0
	}
	return float64(l.Tokens) / float64(l.Read)
}

// Pack is text bytes per stored byte in this layer.
func (l Layer) Pack() float64 {
	if l.Read <= 0 {
		return 0
	}
	return float64(l.Text) / float64(l.Read)
}

// Rate is tokens per byte of text, which is the number that can be compared
// against a reading taken anywhere else.
func (l Layer) Rate() float64 {
	if l.Text <= 0 {
		return 0
	}
	return float64(l.Tokens) / float64(l.Text)
}

// Share is how much of the layer was read.
func (l Layer) Share() float64 {
	if l.Stored <= 0 {
		return 0
	}
	return float64(l.Read) / float64(l.Stored)
}

// Estimate is what this layer contributes, which is its own reading scaled to
// its own size. A layer nobody read contributes nothing here and is carried by
// the source instead, since a layer with no reading has no rate of its own and
// pretending otherwise is the mistake this package is about.
func (l Layer) Estimate() int64 { return int64(l.Yield() * float64(l.Stored)) }

// Blocking is every reason this layer is not a row anybody can estimate from.
func (l Layer) Blocking() []string {
	if l.Name == "" {
		return []string{"a layer with no name cannot be placed in the ordering or reported against"}
	}
	var why []string
	if l.Rank <= 0 {
		why = append(why, fmt.Sprintf("%s has no place in the ordering, and a layer that cannot be ranked cannot be said to sit above or below the ones that went unread", l.Name))
	}
	if l.Stored <= 0 {
		why = append(why, fmt.Sprintf("%s says it holds nothing on disk, and the weight this estimate scales by is that number", l.Name))
	}
	if l.Read > l.Stored {
		why = append(why, fmt.Sprintf("%s read %d bytes out of the %d it holds", l.Name, l.Read, l.Stored))
	}
	if l.Read <= 0 {
		if l.Tokens > 0 || l.Text > 0 {
			why = append(why, fmt.Sprintf("%s counted tokens without reading anything", l.Name))
		}
		return why
	}
	if l.Text <= 0 {
		why = append(why, fmt.Sprintf("%s read %d bytes and got no text out of them", l.Name, l.Read))
	}
	if l.Tokens <= 0 {
		why = append(why, fmt.Sprintf("%s counted no tokens", l.Name))
	}
	if l.Tokenizer == "" {
		why = append(why, fmt.Sprintf("%s names no tokenizer, and a token count in an unstated unit is not a count", l.Name))
	}
	// The band is the one uoc gates on, since it is a fact about Vietnamese
	// text and this package should not have a second opinion about it.
	if r := l.Rate(); l.Text > 0 && l.Tokens > 0 && (r < uoc.MinRate || r > uoc.MaxRate) {
		why = append(why, fmt.Sprintf("%s reads %.2f tokens a byte of text, outside %.2f to %.2f, so it is not Vietnamese read with %s",
			l.Name, r, uoc.MinRate, uoc.MaxRate, l.Tokenizer))
	}
	return why
}

// A Source is a corpus published in layers, with whatever was read off some of
// them.
type Source struct {
	Source string `json:"source"`

	// Quoted is the number the project puts in front of people, carried here so
	// the reading can be checked against what got published rather than only
	// against itself.
	Quoted int64 `json:"quoted,omitempty"`

	Layers []Layer `json:"layers"`
}

// Stored is the whole source on disk.
func (s Source) Stored() int64 {
	var n int64
	for _, l := range s.Layers {
		n += l.Stored
	}
	return n
}

// Lit is the layers something was read out of, and Dark is the rest.
func (s Source) Lit() []Layer  { return pick(s.Layers, Layer.Sampled) }
func (s Source) Dark() []Layer { return pick(s.Layers, func(l Layer) bool { return !l.Sampled() }) }

// DarkBytes is what the unread layers hold, and DarkShare is that against the
// source. It is the number the whole report is arguing about.
func (s Source) DarkBytes() int64 {
	var n int64
	for _, l := range s.Dark() {
		n += l.Stored
	}
	return n
}

func (s Source) DarkShare() float64 {
	if s.Stored() <= 0 {
		return 0
	}
	return float64(s.DarkBytes()) / float64(s.Stored())
}

// Pooled is tokens per stored byte over everything that was read, taken as one
// pile rather than as the mean of the layer rates, since a layer is not a unit
// of anything and 40 MB off a small layer says as much about the pile as 40 MB
// off a large one.
func (s Source) Pooled() float64 {
	var read, tokens int64
	for _, l := range s.Lit() {
		read += l.Read
		tokens += l.Tokens
	}
	if read <= 0 {
		return 0
	}
	return float64(tokens) / float64(read)
}

// Estimate is what a person doing this by hand computes: every layer that was
// read scaled to its own size, and every layer that was not scaled by the rate
// of the ones that were. It is reported because it is the number that gets
// quoted, not because it is the number that should be.
func (s Source) Estimate() int64 { return s.dark(s.Pooled()) }

// Low and High are the same arithmetic with the unread layers held at the
// thinnest and at the richest rate any layer actually read at. The gap between
// them is what skipping a layer costs, and unlike a sampling interval it does
// not close by reading more of the layers already read.
func (s Source) Low() int64  { lo, _ := s.bounds(); return s.dark(lo) }
func (s Source) High() int64 { _, hi := s.bounds(); return s.dark(hi) }

// dark is the estimate with the unread layers taken at rate.
func (s Source) dark(rate float64) int64 {
	var n int64
	for _, l := range s.Layers {
		if l.Sampled() {
			n += l.Estimate()
			continue
		}
		n += int64(rate * float64(l.Stored))
	}
	return n
}

// bounds is the thinnest and richest yield any layer read at.
func (s Source) bounds() (float64, float64) {
	lit := s.Lit()
	if len(lit) == 0 {
		return 0, 0
	}
	lo, hi := lit[0].Yield(), lit[0].Yield()
	for _, l := range lit[1:] {
		if y := l.Yield(); y < lo {
			lo = y
		} else if y > hi {
			hi = y
		}
	}
	return lo, hi
}

// Spread is the range as a share of the estimate, which is the one figure worth
// gating on since the range means nothing without the number it sits under.
func (s Source) Spread() float64 {
	e := s.Estimate()
	if e <= 0 {
		return 0
	}
	return float64(s.High()-s.Low()) / float64(e)
}

// Under is the unread layers sitting below every layer that was read. These are
// the ones that make an estimate lean rather than merely widen, because the
// rate being scaled over them was measured on text that is cleaner than they
// are by the source's own ordering.
func (s Source) Under() []Layer {
	lit := s.Lit()
	if len(lit) == 0 {
		return nil
	}
	floor := lit[0].Rank
	for _, l := range lit[1:] {
		if l.Rank < floor {
			floor = l.Rank
		}
	}
	return pick(s.Dark(), func(l Layer) bool { return l.Rank < floor })
}

// Partial is the layers whose rate was measured over less of themselves than
// MinShare, thinnest share first.
//
// A layer here is not unread and its rate is not pooled from anywhere else. It
// is the layer's own reading, scaled across the rest of the layer on the
// assumption that the rest reads like the part that was opened, and that
// assumption is the last one standing once every layer has been read.
func (s Source) Partial() []Layer {
	out := pick(s.Lit(), func(l Layer) bool { return l.Stored > 0 && l.Share() < MinShare })
	sort.Slice(out, func(i, j int) bool { return out[i].Share() < out[j].Share() })
	return out
}

// UnderBytes is what those layers hold.
func (s Source) UnderBytes() int64 {
	var n int64
	for _, l := range s.Under() {
		n += l.Stored
	}
	return n
}

// Packing is the thinnest and richest text per stored byte measured across the
// layers that were read, which is the spread the weights carry.
func (s Source) Packing() (float64, float64) {
	lit := s.Lit()
	if len(lit) == 0 {
		return 0, 0
	}
	lo, hi := lit[0].Pack(), lit[0].Pack()
	for _, l := range lit[1:] {
		if p := l.Pack(); p < lo {
			lo = p
		} else if p > hi {
			hi = p
		}
	}
	return lo, hi
}

// Blocking is every reason this is not a stratified reading at all.
func (s Source) Blocking() []string {
	if len(s.Layers) == 0 {
		return []string{"the source is published in layers and none of them are here"}
	}
	var why []string
	if s.Source == "" {
		why = append(why, "the reading does not say what it is a reading of")
	}
	names := map[string]bool{}
	ranks := map[int]string{}
	units := map[string]bool{}
	for _, l := range s.Layers {
		if names[l.Name] {
			why = append(why, fmt.Sprintf("%s appears twice, and a layer weighted twice is a layer counted twice", l.Name))
		}
		names[l.Name] = true
		if first, ok := ranks[l.Rank]; ok && l.Rank > 0 {
			why = append(why, fmt.Sprintf("%s and %s both sit at rank %d, so the ordering does not order them", first, l.Name, l.Rank))
		} else if l.Rank > 0 {
			ranks[l.Rank] = l.Name
		}
		if l.Tokenizer != "" {
			units[l.Tokenizer] = true
		}
		why = append(why, l.Blocking()...)
	}
	if len(units) > 1 {
		why = append(why, "the layers were read with more than one tokenizer, and two tokenizers are two units, so the layers cannot be added")
	}
	if len(s.Lit()) == 0 {
		why = append(why, "no layer was read, so what there is here is a list of sizes and no rate to scale any of them by")
	}
	return why
}

// Faults are the reasons an estimate that computes carries more than a sample's
// worth of guess. None of them stop the number being printed, because the
// number is the one being quoted either way and the argument is about what
// belongs next to it.
func (s Source) Faults() []string {
	if len(s.Blocking()) > 0 {
		return nil
	}
	var out []string

	if dark := s.Dark(); s.DarkShare() > MaxDark {
		if len(dark) == 1 {
			out = append(out, fmt.Sprintf(
				"%s holds %s of the source and was never read, so the estimate over it is the rate of the layers that were",
				dark[0].Name, share(s.DarkShare())))
		} else {
			out = append(out, fmt.Sprintf(
				"%d layers holding %s of the source were never read, starting with %s, so the estimate over all of them is the rate of the layers that were",
				len(dark), share(s.DarkShare()), dark[0].Name))
		}
	}

	if under := s.Under(); len(under) > 0 {
		out = append(out, fmt.Sprintf(
			"%s of the source sits in %s ranked below every layer that was read, so what is being scaled over the gap is the rate of the cleaner end of the corpus",
			share(float64(s.UnderBytes())/float64(s.Stored())), plural(len(under), "layer")))
	}

	// Both of the spreads are statements about scaling one layer's reading over
	// a layer nobody read, so both are silent when there is no such layer. The
	// first reading that opened every bucket of a source printed them anyway,
	// and what they said was that a pooled rate over the layers nobody read is a
	// choice and that every unread layer is weighted by a number off by 38.8%,
	// with nothing unread and the range 143.7B to 143.7B. A complete reading
	// does not get to exit 2 over an error applied to zero bytes.
	if len(s.Dark()) > 0 {
		if lo, hi := s.bounds(); lo > 0 && hi/lo > MaxYieldSpread {
			thin, rich := s.ends()
			out = append(out, fmt.Sprintf(
				"the layers that were read do not read at one rate, since %s gives %.3f tokens a stored byte and %s gives %.3f, so a single pooled rate over the layers nobody read is a choice rather than a measurement",
				thin.Name, lo, rich.Name, hi))
		}

		if lo, hi := s.Packing(); lo > 0 && hi/lo > MaxPackSpread {
			out = append(out, fmt.Sprintf(
				"the weights are stored bytes and a stored byte holds between %.2f and %.2f bytes of text across the layers that were read, so every unread layer is weighted by a number that is off by as much as %s",
				lo, hi, share(hi/lo-1)))
		}
	}

	var thin []Layer
	for _, l := range s.Lit() {
		if l.Read < MinRead {
			thin = append(thin, l)
		}
	}
	switch n := len(thin); {
	case n == 1:
		out = append(out, fmt.Sprintf(
			"%s was read over %s, under the %s a layer's rate needs before one long page stops moving it",
			thin[0].Name, size(thin[0].Read), size(MinRead)))
	case n > 1:
		out = append(out, fmt.Sprintf(
			"%d layers were read over less than %s each, starting with %s, and a rate off that much text moves with the pages that happened to be in it",
			n, size(MinRead), thin[0].Name))
	}

	// The one that survives a complete reading. Every layer having a rate of its
	// own says nothing about how much of the layer that rate was measured over,
	// and on a corpus this size it is measured over a fraction of a percent.
	if part := s.Partial(); len(part) > 0 {
		worst := part[0]
		switch n := len(part); n {
		case 1:
			out = append(out, fmt.Sprintf(
				"%s was read over %s of the %s it holds, %s of it, so the rate scaled across the layer is the rate of the part that was read",
				worst.Name, size(worst.Read), size(worst.Stored), share(worst.Share())))
		default:
			out = append(out, fmt.Sprintf(
				"%d layers were read over under %s of themselves each, thinnest %s at %s of %s, so the rate scaled across each of them is the rate of the part that was read",
				n, share(MinShare), worst.Name, size(worst.Read), size(worst.Stored)))
		}
	}

	if s.Quoted > 0 && (s.Quoted < s.Low() || s.Quoted > s.High()) {
		out = append(out, fmt.Sprintf(
			"the number this project publishes is %s and this reading covers %s to %s, so the published number is not what this sample says",
			tokens(s.Quoted), tokens(s.Low()), tokens(s.High())))
	}
	return out
}

// ends is the layer that read thinnest and the layer that read richest.
func (s Source) ends() (Layer, Layer) {
	lit := s.Lit()
	sorted := make([]Layer, len(lit))
	copy(sorted, lit)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Yield() < sorted[j].Yield() })
	return sorted[0], sorted[len(sorted)-1]
}

// Holds reports whether this estimate can be published as the source's number
// rather than as somebody's reading of part of it.
func (s Source) Holds() bool { return len(s.Blocking()) == 0 && len(s.Faults()) == 0 }

// Verdict is the reading in one paragraph.
func (s Source) Verdict() string {
	if why := s.Blocking(); len(why) > 0 {
		return why[0]
	}

	var b strings.Builder
	if dark := len(s.Dark()); dark > 0 {
		fmt.Fprintf(&b, "%s estimates %s tokens over %s on disk, %s to %s once the layers nobody read are allowed to run as thin as the thinnest layer that was read and as rich as the richest.",
			s.Source, tokens(s.Estimate()), size(s.Stored()), tokens(s.Low()), tokens(s.High()))
		fmt.Fprintf(&b, " %d of %d layers holding %s of it were never opened, and that range does not close by reading more of the %d that were.",
			dark, len(s.Layers), share(s.DarkShare()), len(s.Lit()))
	} else {
		// The range is the bound on what nobody opened, so with nothing unopened
		// there is no range to print and quoting one would be quoting the
		// estimate twice.
		fmt.Fprintf(&b, "%s estimates %s tokens over %s on disk, off a reading of every one of its %s, so there is no unread layer left for the range to be a range over.",
			s.Source, tokens(s.Estimate()), size(s.Stored()), plural(len(s.Layers), "layer"))
	}

	faults := s.Faults()
	switch n := len(faults); n {
	case 0:
		fmt.Fprint(&b, " Every layer has a rate of its own and nothing here is scaled by another layer's, so the number is the source's rather than a reading of the part of it somebody had time for.")
	case 1:
		fmt.Fprintf(&b, " One reading says the estimate carries more than sampling error: %s.", faults[0])
	default:
		fmt.Fprintf(&b, " %d readings say the estimate carries more than sampling error: %s.", n, strings.Join(faults, "; and "))
	}
	return b.String()
}

func pick(layers []Layer, keep func(Layer) bool) []Layer {
	out := make([]Layer, 0, len(layers))
	for _, l := range layers {
		if keep(l) {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
