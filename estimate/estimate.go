// Package estimate publishes an estimate as an interval and says when to stop
// sampling.
//
// Ước lượng is to estimate. This project quotes one estimate in its own claim:
// HPLT v3 vie_Latn at 176B tokens, which is gao's sampled reading of somebody
// else's corpus and stands in until the exact count lands. Every ratio in the
// README is written against it. A single number carrying that much weight and no
// interval is a number people quote as exact, including the people who wrote it,
// and by the third repetition nobody remembers a sample was involved.
//
// The estimator is a ratio rather than a mean, because the auxiliary total is
// already known. A pinned manifest gives the exact byte count of every part in
// the source without reading one of them, so the sample only has to establish
// tokens per byte, and the estimate is that rate against a total nobody has to
// guess at. The naive alternative, mean tokens per part times the number of
// parts, throws that away and inherits the variance of the part sizes, which on
// a corpus whose shards run from 200 MB to 2 GB is most of the variance there
// is. Both are computed here, since the gap between them is the argument for
// using the first one.
//
// Two rules keep an interval from being decoration. Under 30 parts the normal
// approximation behind it is not one, so the interval is refused rather than
// widened, and an estimate that cannot say how wrong it might be is not
// published at all. And the sample has to name the seed it was drawn with, since
// a sample somebody assembled by opening files until the number looked right has
// no sampling distribution and no interval, whatever arithmetic is done to it.
//
// When the exact count lands, Against says whether it fell inside the interval
// that was published. That is the only way an estimator earns anything: a
// project that publishes an interval and never checks it against the answer has
// published a decoration with error bars on it.
package estimate

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
)

// MinSample is the smallest sample this package will put an interval on. Below
// it the normal approximation is not one, and a narrow interval computed off
// eight parts is worse than no interval, since it reads as precision.
const MinSample = 30

// MaxWidth is the relative half width an estimate has to reach before it is
// worth publishing, which is five percent. At 176B that is plus or minus 8.8B,
// which is wide enough to matter and narrow enough that the comparison the
// number exists for still holds at both ends.
const MaxWidth = 0.05

// Z is the multiplier for a 95% interval. Two figures, because pretending to
// more is pretending the sample was drawn more carefully than it was.
const Z = 1.96

// MinRate and MaxRate bound tokens per byte. The one exact reading this project
// has taken is GlotCC vie-Latn_0, which counted 983022920 tokens over 3228869043
// characters in 4.2 GB of text, and that is 0.234 tokens a byte. Vietnamese in
// UTF-8 spends about 1.3 bytes a character and every tokenizer measured here
// spends more than a character on a token, so a part outside this band is not
// Vietnamese text read with the tokenizer it names.
const (
	MinRate = 0.15
	MaxRate = 0.45
)

// A Part is one shard of the source, read exactly.
type Part struct {
	Path string `json:"path"`

	// Bytes is what this part actually held when it was read.
	Bytes int64 `json:"bytes"`

	// Manifest is what the pin says it holds. The two disagreeing means the
	// sample was taken off something other than the corpus that was pinned, and
	// an estimate scaled by a total from one corpus off a rate measured on
	// another is not an estimate of either.
	Manifest int64 `json:"manifest"`

	Tokens    int64  `json:"tokens"`
	Tokenizer string `json:"tokenizer"`
}

// Rate is tokens per byte in this part.
func (p Part) Rate() float64 {
	if p.Bytes <= 0 {
		return 0
	}
	return float64(p.Tokens) / float64(p.Bytes)
}

// Matches reports whether the part read is the part the manifest pinned.
func (p Part) Matches() bool { return p.Bytes == p.Manifest }

// Blocking is every reason this part is not a reading.
func (p Part) Blocking() []string {
	if p.Path == "" {
		return []string{"a reading with no part on it cannot be checked against the manifest or against itself"}
	}
	var why []string
	if p.Bytes <= 0 {
		why = append(why, fmt.Sprintf("%s counts no bytes, and the rate this sample exists to measure is a rate per byte", p.Path))
	}
	if p.Tokens <= 0 {
		why = append(why, fmt.Sprintf("%s counts no tokens", p.Path))
	}
	if p.Tokenizer == "" {
		why = append(why, fmt.Sprintf("%s names no tokenizer, and a token count in an unstated unit is not a count", p.Path))
	}
	if p.Manifest > 0 && !p.Matches() {
		why = append(why, fmt.Sprintf(
			"%s read %d bytes against the %d the manifest pins, so this sample came off something other than the corpus the total is taken from",
			p.Path, p.Bytes, p.Manifest))
	}
	if r := p.Rate(); p.Bytes > 0 && p.Tokens > 0 && (r < MinRate || r > MaxRate) {
		why = append(why, fmt.Sprintf(
			"%s reads %.2f tokens a byte, outside %.2f to %.2f, so it is not Vietnamese text tokenized with %s",
			p.Path, r, MinRate, MaxRate, p.Tokenizer))
	}
	return why
}

// A Source is a corpus being estimated: what the pin says about all of it, and
// what was read off some of it.
type Source struct {
	Source   string `json:"source"`
	Snapshot string `json:"snapshot"`

	// Parts and Bytes are the whole source, off the pinned manifest rather than
	// off a pass over the data. This is the auxiliary total the estimator leans
	// on, and it is exact without a byte being fetched.
	Parts int64 `json:"parts"`
	Bytes int64 `json:"bytes"`

	// Seed is what the sample was drawn with, so a third party draws the same
	// parts and reads the same numbers.
	Seed string `json:"seed"`

	Sample []Part `json:"sample"`
}

// Read is how many parts were read.
func (s Source) Read() int { return len(s.Sample) }

// Fraction is the share of the source that was read.
func (s Source) Fraction() float64 {
	if s.Parts <= 0 {
		return 0
	}
	return float64(len(s.Sample)) / float64(s.Parts)
}

// SampledBytes and SampledTokens are what the sample actually held.
func (s Source) SampledBytes() int64  { return total(s.Sample, func(p Part) int64 { return p.Bytes }) }
func (s Source) SampledTokens() int64 { return total(s.Sample, func(p Part) int64 { return p.Tokens }) }

// Ratio is tokens per byte over the sample as a whole, which is the quantity
// being estimated. It is not the mean of the per part rates, since a part is not
// a unit of anything and a 2 GB shard says more about the rate than a 200 MB
// one.
func (s Source) Ratio() float64 {
	b := s.SampledBytes()
	if b <= 0 {
		return 0
	}
	return float64(s.SampledTokens()) / float64(b)
}

// Estimate is the ratio against the known total.
func (s Source) Estimate() int64 { return int64(s.Ratio() * float64(s.Bytes)) }

// Mean is the estimator that ignores the manifest: mean tokens per part times
// the number of parts. It is here to be compared against, since the whole
// argument for the ratio estimator is the gap between the two on a corpus whose
// shards are not the same size.
func (s Source) Mean() int64 {
	if len(s.Sample) == 0 {
		return 0
	}
	return int64(float64(s.SampledTokens()) / float64(len(s.Sample)) * float64(s.Parts))
}

// StdErr is the standard error of the estimate, with the finite population
// correction, since reading a fifth of the parts is a fifth of the way to
// reading all of them and an interval that ignores that is wider than the truth.
func (s Source) StdErr() float64 {
	k := len(s.Sample)
	if k < 2 || s.Parts <= 0 || s.Bytes <= 0 {
		return 0
	}
	r := s.Ratio()
	mean := float64(s.SampledBytes()) / float64(k)
	if mean <= 0 {
		return 0
	}
	// The residual is tokens against what the fitted rate says the part should
	// have held, so a part that is big and ordinary contributes nothing and a
	// part that reads at a different rate contributes whatever that difference
	// is worth.
	var ss float64
	for _, p := range s.Sample {
		d := float64(p.Tokens) - r*float64(p.Bytes)
		ss += d * d
	}
	v := ss / float64(k-1)
	fpc := 1 - float64(k)/float64(s.Parts)
	if fpc < 0 {
		fpc = 0
	}
	// Var of the ratio estimate of the total, scaled from the per part variance
	// through the number of parts.
	varTotal := float64(s.Parts) * float64(s.Parts) * fpc * v / float64(k)
	return math.Sqrt(varTotal)
}

// Half is the half width of the interval.
func (s Source) Half() float64 { return Z * s.StdErr() }

// Low and High are the interval, which is what gets published instead of the
// estimate on its own.
func (s Source) Low() int64  { return int64(float64(s.Estimate()) - s.Half()) }
func (s Source) High() int64 { return int64(float64(s.Estimate()) + s.Half()) }

// Width is the half width as a share of the estimate, which is the number worth
// gating on since an interval means nothing without the number it sits on.
func (s Source) Width() float64 {
	e := s.Estimate()
	if e <= 0 {
		return 0
	}
	return s.Half() / float64(e)
}

// Needed is how many more parts to read to bring the relative half width down to
// want. It answers the question a sampling plan is actually asked, which is not
// how wrong the estimate is but how much more reading it would take to stop
// arguing about it. The fourfold cost of halving an interval is the part people
// guess wrong.
func (s Source) Needed(want float64) int64 {
	k := int64(len(s.Sample))
	if want <= 0 || k < 2 || s.Width() <= want {
		return 0
	}
	ratio := s.Width() / want
	need := int64(math.Ceil(float64(k) * ratio * ratio))
	if need > s.Parts {
		need = s.Parts
	}
	if need <= k {
		return 0
	}
	return need - k
}

// Tokenizer is the unit the estimate is in.
func (s Source) Tokenizer() string {
	if len(s.Sample) == 0 {
		return ""
	}
	return s.Sample[0].Tokenizer
}

// Blocking is every reason this sample is not an estimate anybody can publish.
func (s Source) Blocking() []string {
	if len(s.Sample) == 0 {
		return []string{"no parts were read, so what there is here is a total from a manifest and no rate to scale it by"}
	}
	var why []string
	if s.Source == "" {
		why = append(why, "the sample does not say what it is a sample of")
	}
	if s.Seed == "" {
		why = append(why, "the sample names no seed, and parts somebody opened until the number looked right have no sampling distribution and cannot carry an interval")
	}
	if s.Parts <= 0 || s.Bytes <= 0 {
		why = append(why, "the source names no part count and no byte total, and a ratio estimator with nothing to scale by is a rate")
	}
	if int64(len(s.Sample)) > s.Parts && s.Parts > 0 {
		why = append(why, fmt.Sprintf("%d parts were read off a source the manifest says holds %d", len(s.Sample), s.Parts))
	}
	seen := map[string]bool{}
	units := map[string]bool{}
	for _, p := range s.Sample {
		if seen[p.Path] {
			why = append(why, fmt.Sprintf("%s was read twice, and a part counted twice is a part weighted twice", p.Path))
		}
		seen[p.Path] = true
		if p.Tokenizer != "" {
			units[p.Tokenizer] = true
		}
		why = append(why, p.Blocking()...)
	}
	if len(units) > 1 {
		why = append(why, "the sample was read with more than one tokenizer, and two tokenizers are two units, so the rate between them is a number in neither")
	}
	if len(s.Sample) < MinSample {
		why = append(why, fmt.Sprintf(
			"%d parts is under the %d this interval's arithmetic assumes, and a narrow interval off a small sample reads as precision rather than as the guess it is",
			len(s.Sample), MinSample))
	}
	if s.SampledBytes() > s.Bytes && s.Bytes > 0 {
		why = append(why, "the sample holds more bytes than the manifest says the whole source does")
	}
	return why
}

// Settled reports whether the sample is a measurement.
func (s Source) Settled() bool { return len(s.Blocking()) == 0 }

// Holds reports whether the estimate is tight enough to publish.
func (s Source) Holds() bool { return s.Settled() && s.Width() <= MaxWidth }

// Covers reports whether an exact count landed inside the interval.
func (s Source) Covers(exact int64) bool {
	return s.Settled() && exact >= s.Low() && exact <= s.High()
}

// Against is what to say once the exact count exists. It is the only line in
// this package that costs the estimator anything, which is why it is here.
func (s Source) Against(exact int64) string {
	if !s.Settled() {
		return s.Blocking()[0]
	}
	off := math.Abs(float64(exact-s.Estimate())) / float64(exact) * 100
	if s.Covers(exact) {
		return fmt.Sprintf(
			"%s counted exactly %s, inside the %s to %s that was published, and %.1f%% off the estimate itself, so the sampled number stood",
			s.Source, tokens(exact), tokens(s.Low()), tokens(s.High()), off)
	}
	return fmt.Sprintf(
		"%s counted exactly %s, outside the %s to %s that was published and %.1f%% off the estimate, so the sample missed and every ratio quoted against the estimate was quoted against a number that was wrong",
		s.Source, tokens(exact), tokens(s.Low()), tokens(s.High()), off)
}

// Verdict is the estimate in one sentence.
func (s Source) Verdict() string {
	if why := s.Blocking(); len(why) > 0 {
		return why[0]
	}
	if !s.Holds() {
		return fmt.Sprintf(
			"%s estimates %s tokens, %s to %s, which is %s wide against a %s line, and closing that takes %d more parts rather than another argument",
			s.Source, tokens(s.Estimate()), tokens(s.Low()), tokens(s.High()),
			share(s.Width()), share(MaxWidth), s.Needed(MaxWidth))
	}
	return fmt.Sprintf(
		"%s estimates %s tokens, %s to %s at 95%%, off %d of %d parts, and what gets published is the interval rather than its middle",
		s.Source, tokens(s.Estimate()), tokens(s.Low()), tokens(s.High()), len(s.Sample), s.Parts)
}

func total(parts []Part, of func(Part) int64) int64 {
	var n int64
	for _, p := range parts {
		n += of(p)
	}
	return n
}

// ReadSource loads a sample from a file of one JSON part per line, with the
// source totals given by the caller since they come off the manifest rather than
// off the reading.
func ReadSource(source, snapshot, seed string, parts, bytes int64, path string) (Source, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Source{}, fmt.Errorf("estimate: %w", err)
	}
	s := Source{Source: source, Snapshot: snapshot, Seed: seed, Parts: parts, Bytes: bytes}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var p Part
		if err := dec.Decode(&p); err != nil {
			return Source{}, fmt.Errorf("estimate: %s line %d: %w", path, i+1, err)
		}
		s.Sample = append(s.Sample, p)
	}
	if len(s.Sample) == 0 {
		return Source{}, fmt.Errorf("estimate: %s holds no readings", path)
	}
	return s, nil
}

// tokens renders a count in the unit the claim is written in.
func tokens(n int64) string { return fmt.Sprintf("%.1fB", float64(n)/1e9) }

func share(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }
