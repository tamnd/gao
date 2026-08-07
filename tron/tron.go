// Package tron composes the supervised finetuning set without letting the
// mixing hide where the examples came from.
//
// Trộn is to mix. Vietnamese has an instruction data problem English does not.
// Most Vietnamese instruction sets are translations of English ones, and a
// model finetuned on translated instructions learns to write Vietnamese that
// reads like translated English: grammatical, fluent, and wrong in a way native
// speakers notice in a sentence and benchmarks do not notice at all. The
// failure is not that translated data is used. It is that translated data gets
// poured into the same pot as native data, the pot gets a size and a name, and
// after that nobody can say which half the model learned its register from.
//
// So origin is a column rather than a note. Every slice declares whether a
// Vietnamese speaker wrote it, whether it came out of a translator, or whether
// a model made it, and the set is reported as three numbers rather than one.
// All three are trained on. Only the reporting is separated, because the claim
// this milestone makes is about the native half and a claim about a half needs
// the half to still be identifiable after the mixing.
//
// A native label is a claim about provenance, and provenance metadata on
// instruction data is wrong often enough that it gets audited. A slice claiming
// native origin carries a count of examples a Vietnamese speaker actually read
// and a count of those whose label held. Under two hundred read, or under 95%
// holding, the label is not established and the slice is reported as unproven
// rather than counted as native.
//
// The comparison that P09-3 and P10-5 rest on needs two arms, and two arms need
// to differ in one thing. A native arm of six hundred thousand examples against
// a translated arm of forty thousand measures the training set size. A native
// arm heavy on writing against a translated arm heavy on question answering
// measures the capability mix. So the arm size is the smaller of the two, the
// per capability shares have to agree within three points, and any capability
// one origin has nothing of is named as excluded rather than quietly dropped,
// since a comparison that drops a capability is a comparison of a different set.
package tron

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
)

// The three origins. All of them are trained on and none of them are added
// together in a report.
const (
	Native     = "native"
	Translated = "translated"
	Made       = "synthetic"
)

// Target is the size the set is composed to.
const Target = 800_000

// Tolerance is how far off that size the set may land and still be the set that
// was planned, since the last shard of any source comes in short.
const Tolerance = 0.05

// MinArm is the smallest arm that is a training run rather than a demonstration.
// Below it both arms are undertrained and the difference between them is a
// measurement of how little data each one got.
const MinArm = 100_000

// MaxDrift is how far the two arms' capability mixes may differ, per capability,
// before the comparison is measuring the mix instead of the origin.
const MaxDrift = 0.03

// MaxShareDrift is how far a capability may land from the share it was composed
// to. A composition target nothing checks is a comment, and the way a set drifts
// is never deliberate: one source turns out to be four times the size anybody
// expected and the mixture follows it.
const MaxShareDrift = 0.05

// MinAudited and MinPass are what turns an origin label into a measurement. Two
// hundred examples read by somebody who speaks the language, and the label
// holding on 95% of them.
const (
	MinAudited = 200
	MinPass    = 0.95
)

// A Capability is one thing the finetuned model has to do, with the share of
// the set it gets and the share of that which has to be native.
type Capability struct {
	Name      string
	Share     float64
	MinNative float64
	Why       string
}

// Slate is what the set is composed against, fixed here so that a set which
// came in without a capability reads as a hole rather than as a shorter set.
var Slate = []Capability{
	{"hoi-dap", 0.22, 0.80,
		"open question answering, where a translated answer is usually right and usually phrased like a search result"},
	{"viet", 0.18, 0.95,
		"writing and rewriting, which is the capability the native origin claim is about, since register is the first thing translation flattens"},
	{"doc-hieu", 0.14, 0.85,
		"reading comprehension over a Vietnamese document, where the document has to be one somebody in Vietnam would actually read"},
	{"tom-tat", 0.12, 0.85,
		"summarization, where translated data teaches the length and the shape of an English summary"},
	{"dau-cau", 0.10, 0.98,
		"diacritic restoration, which comes out of the corpus with its answers already known and has no translated form"},
	{"ma-nguon", 0.10, 0.30,
		"code and technical instruction following, where the code is language neutral and only the prose around it is not"},
	{"phap-ly", 0.08, 0.95,
		"legal question answering, where a translated example is a confident answer about another country's law"},
	{"dich", 0.06, 0.20,
		"translation, the one capability where a translated example is the task rather than a defect, so the floor is only high enough to keep the Vietnamese side written by somebody who speaks it"},
}

// Find returns the capability of that name.
func Find(name string) (Capability, bool) {
	for _, c := range Slate {
		if c.Name == name {
			return c, true
		}
	}
	return Capability{}, false
}

// A Slice is one source, one capability and one origin, counted once.
type Slice struct {
	Source     string `json:"source"`
	Capability string `json:"capability"`

	// Origin is native, translated or synthetic, and it is the column this whole
	// package exists to keep.
	Origin string `json:"origin"`

	Examples int64 `json:"examples"`

	// Turns is conversation turns. A multi turn example is one example and more
	// than one turn, and a slice reporting fewer turns than examples counted
	// something other than what it says.
	Turns int64 `json:"turns"`

	// Audited is examples a Vietnamese speaker read, and Passed is those whose
	// origin label held when they read it.
	Audited int64 `json:"audited"`
	Passed  int64 `json:"passed"`

	// Held marks a slice kept out of the mixture and aside for the comparison
	// arm. This is the whole of what keeping translated data separate means: it
	// is composed, it is counted, it gets trained on in its own run, and it does
	// not go into the pot the headline set is poured from.
	Held bool `json:"held,omitempty"`

	// License decides whether a third party can rebuild this finetune, which is
	// a different question from whether we can run it.
	License doc.LicenseClass `json:"license"`
}

// Pass is the share of the audit on which the origin label held.
func (s Slice) Pass() float64 {
	if s.Audited <= 0 {
		return 0
	}
	return float64(s.Passed) / float64(s.Audited)
}

// Proven reports whether the origin label on this slice is a measurement.
// Translated and synthetic origin are proven by construction, since a
// translator and a generator both know what they produced. Native origin is a
// claim about a person and gets audited.
func (s Slice) Proven() bool {
	if s.Origin != Native {
		return s.Origin == Translated || s.Origin == Made
	}
	return s.Audited >= MinAudited && s.Pass() >= MinPass
}

// Reproducible reports whether somebody outside this project could rebuild the
// finetune from this slice.
func (s Slice) Reproducible() bool { return s.License.Publishable() }

// Blocking is every reason this slice cannot go into a set.
func (s Slice) Blocking() []string {
	if s.Source == "" {
		return []string{"a slice with no source on it cannot be audited, and the origin column is only worth what the audit behind it is worth"}
	}
	var why []string
	switch s.Origin {
	case Native, Translated, Made:
	case "":
		why = append(why, fmt.Sprintf(
			"%s does not say what wrote its examples, and in Vietnamese instruction data an unstated origin is a translation more often than not",
			s.Source))
	default:
		why = append(why, fmt.Sprintf("%s declares origin %q, which is not one of the three this set is reported in", s.Source, s.Origin))
	}
	if _, ok := Find(s.Capability); !ok {
		why = append(why, fmt.Sprintf(
			"%s is filed under %q, which the slate does not name, so it would be trained on and reported under nothing",
			s.Source, s.Capability))
	}
	if s.Examples <= 0 {
		why = append(why, fmt.Sprintf("%s holds no examples", s.Source))
	}
	if s.Turns < s.Examples {
		why = append(why, fmt.Sprintf(
			"%s reports %s over %s, and an example is at least one turn, so one of those two columns counted something else",
			s.Source, count(s.Turns, "turn"), count(s.Examples, "example")))
	}
	if !s.License.Valid() || s.License == doc.LicenseUnknown {
		why = append(why, fmt.Sprintf(
			"%s carries no license determination, so whether the finetune can be rebuilt by anybody else is undecided",
			s.Source))
	}
	if s.Held && s.Origin != Translated && s.Origin != "" {
		why = append(why, fmt.Sprintf(
			"%s is held aside for the comparison arm and its origin is %s, and the only thing there is to hold aside is the translated counterpart the mixture gets measured against",
			s.Source, s.Origin))
	}
	if s.Audited > s.Examples {
		why = append(why, fmt.Sprintf("%s audited %s out of %s", s.Source, count(s.Audited, "example"), count(s.Examples, "example")))
	}
	if s.Passed > s.Audited {
		why = append(why, fmt.Sprintf("%s passed more examples than it read", s.Source))
	}
	if s.Origin == Native && s.Audited > 0 && s.Audited < MinAudited {
		why = append(why, fmt.Sprintf(
			"%s claims native origin off %s read, under the %d this set treats as an audit, and a native label nobody checked is a metadata field",
			s.Source, count(s.Audited, "example"), MinAudited))
	}
	if s.Origin == Native && s.Audited == 0 {
		why = append(why, fmt.Sprintf(
			"%s claims native origin and nobody read any of it, so %s enter the set on the word of whoever uploaded them",
			s.Source, count(s.Examples, "example")))
	}
	if s.Origin == Native && s.Audited >= MinAudited && s.Pass() < MinPass {
		why = append(why, fmt.Sprintf(
			"%s's audit held on %.0f%% of what was read, under %.0f%%, so the native label on %s is not established and the slice reads as unproven rather than as native",
			s.Source, s.Pass()*100, MinPass*100, count(s.Examples, "example")))
	}
	return why
}

// A Mix is what a group of slices holds.
type Mix struct {
	Name string `json:"name"`

	Slices   int   `json:"slices"`
	Examples int64 `json:"examples"`
	Turns    int64 `json:"turns"`
	Audited  int64 `json:"audited"`
	Passed   int64 `json:"passed"`
}

func (m Mix) add(s Slice) Mix {
	m.Slices++
	m.Examples += s.Examples
	m.Turns += s.Turns
	m.Audited += s.Audited
	m.Passed += s.Passed
	return m
}

// Share is this mix against a whole.
func (m Mix) Share(whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(m.Examples) / float64(whole)
}

// A Row is one capability of the slate against what the set holds for it.
type Row struct {
	Capability string  `json:"capability"`
	Target     float64 `json:"target"`

	Examples int64   `json:"examples"`
	Share    float64 `json:"share"`

	NativeExamples int64   `json:"native_examples"`
	NativeShare    float64 `json:"native_share"`
	MinNative      float64 `json:"min_native"`

	Holds bool `json:"holds"`
}

// A Set is the composed finetuning data.
type Set struct {
	Name   string  `json:"name"`
	Slices []Slice `json:"slices"`
}

// Proven is every slice whose origin label is a measurement, which is the set
// anything downstream is allowed to reason about origin with.
func (s Set) Proven() []Slice { return s.filter(Slice.Proven) }

// Unproven is every slice whose origin label is not, named because a slice that
// silently fell out of the native count is the failure this package is about
// wearing different clothes.
func (s Set) Unproven() []Slice {
	return s.filter(func(sl Slice) bool { return !sl.Proven() })
}

// In is every slice that goes into the mixture, which is the set the model is
// finetuned on and the set the headline number counts.
func (s Set) In() []Slice {
	return s.filter(func(sl Slice) bool { return sl.Proven() && !sl.Held })
}

// Aside is the translated data kept out of the mixture for the comparison arm.
func (s Set) Aside() []Slice {
	return s.filter(func(sl Slice) bool { return sl.Proven() && sl.Held })
}

// Examples is the mixture, all three origins, since all three are trained on.
func (s Set) Examples() int64 { return s.Origin("").Examples }

// Origin is what one origin holds in the mixture, or the whole mixture when the
// origin is empty. It counts proven slices only, because an unproven native
// label counted as native is exactly the blending this exists to prevent.
func (s Set) Origin(origin string) Mix {
	m := Mix{Name: origin}
	for _, sl := range s.In() {
		if origin == "" || sl.Origin == origin {
			m = m.add(sl)
		}
	}
	return m
}

// Composition is the slate against what the set holds, in slate order, so a
// capability with nothing in it is a row rather than an absence.
func (s Set) Composition() []Row {
	whole := s.Examples()
	out := make([]Row, 0, len(Slate))
	for _, c := range Slate {
		var held, native int64
		for _, sl := range s.In() {
			if sl.Capability != c.Name {
				continue
			}
			held += sl.Examples
			if sl.Origin == Native {
				native += sl.Examples
			}
		}
		r := Row{
			Capability:     c.Name,
			Target:         c.Share,
			Examples:       held,
			NativeExamples: native,
			MinNative:      c.MinNative,
		}
		if whole > 0 {
			r.Share = float64(held) / float64(whole)
		}
		if held > 0 {
			r.NativeShare = float64(native) / float64(held)
		}
		r.Holds = held > 0 && r.NativeShare >= c.MinNative
		out = append(out, r)
	}
	return out
}

// Compared is every capability the native mixture and the translated arm both
// hold, in slate order. It is what the comparison can actually run over.
func (s Set) Compared() []string {
	var out []string
	for _, c := range Slate {
		if s.arm(c.Name, Native) > 0 && s.arm(c.Name, Translated) > 0 {
			out = append(out, c.Name)
		}
	}
	return out
}

// Excluded is every capability it cannot, named rather than dropped, since a
// comparison that quietly leaves out a capability is a comparison of a
// different set from the one that was published.
func (s Set) Excluded() []string {
	compared := s.Compared()
	var out []string
	for _, c := range Slate {
		if !slices.Contains(compared, c.Name) {
			out = append(out, c.Name)
		}
	}
	return out
}

// Arm is what one origin brings to the comparison, over the compared
// capabilities only. The native arm comes out of the mixture and the translated
// arm is the data that was held aside from it.
func (s Set) Arm(origin string) int64 {
	var n int64
	for _, name := range s.Compared() {
		n += s.arm(name, origin)
	}
	return n
}

// ArmSize is the size both arms get trained at, which is the smaller of the
// two, because the larger one gets subsampled down rather than the smaller one
// getting an excuse.
func (s Set) ArmSize() int64 { return min(s.Arm(Native), s.Arm(Translated)) }

// Drift is the widest per capability share disagreement between the two arms.
func (s Set) Drift() float64 {
	native, translated := s.Arm(Native), s.Arm(Translated)
	if native <= 0 || translated <= 0 {
		return 0
	}
	var worst float64
	for _, name := range s.Compared() {
		a := float64(s.arm(name, Native)) / float64(native)
		b := float64(s.arm(name, Translated)) / float64(translated)
		worst = math.Max(worst, math.Abs(a-b))
	}
	return worst
}

// Drifted is the capability the drift was measured on, so a report says where
// the arms disagree rather than by how much.
func (s Set) Drifted() string {
	native, translated := s.Arm(Native), s.Arm(Translated)
	if native <= 0 || translated <= 0 {
		return ""
	}
	var worst float64
	var at string
	for _, name := range s.Compared() {
		a := float64(s.arm(name, Native)) / float64(native)
		b := float64(s.arm(name, Translated)) / float64(translated)
		if d := math.Abs(a - b); d > worst {
			worst, at = d, name
		}
	}
	return at
}

// Matched reports whether the two arms differ in origin and in nothing else
// large enough to explain a result.
func (s Set) Matched() bool {
	return s.ArmSize() >= MinArm && s.Drift() <= MaxDrift
}

// Reproducible is the share of the set a third party could rebuild the finetune
// from.
func (s Set) Reproducible() float64 {
	whole := s.Examples()
	if whole <= 0 {
		return 0
	}
	var n int64
	for _, sl := range s.In() {
		if sl.Reproducible() {
			n += sl.Examples
		}
	}
	return float64(n) / float64(whole)
}

// Short is every capability the set does not hold enough native examples for,
// which is where the writing quality claim cannot be made.
func (s Set) Short() []Row {
	var out []Row
	for _, r := range s.Composition() {
		if !r.Holds {
			out = append(out, r)
		}
	}
	return out
}

// Blocking is every reason this is not a set anybody can train an arm on.
func (s Set) Blocking() []string {
	if len(s.Slices) == 0 {
		return []string{"no slices were read, so what there is here is a composition plan and not a set"}
	}
	var why []string
	if s.Name == "" {
		why = append(why, "the set does not name itself, and a finetune is named after the data it read rather than after the day it ran")
	}
	seen := map[string]bool{}
	for _, sl := range s.Slices {
		key := sl.Source + "\x00" + sl.Capability + "\x00" + sl.Origin
		if seen[key] {
			why = append(why, fmt.Sprintf(
				"%s appears twice for %s at the same origin, and a slice counted twice is a slice the model sees twice",
				sl.Source, sl.Capability))
		}
		seen[key] = true
		why = append(why, sl.Blocking()...)
	}
	for _, r := range s.Composition() {
		switch {
		case r.Examples == 0:
			c, _ := Find(r.Capability)
			why = append(why, fmt.Sprintf(
				"%s holds nothing, and it is on the slate because it is %s, so a set without it is a hole rather than a shorter set",
				r.Capability, c.Why))
		case !r.Holds:
			why = append(why, fmt.Sprintf(
				"%s is %.0f%% native against a %.0f%% floor, and a Vietnamese writing claim made on a capability that is mostly translated is a claim about the translator",
				r.Capability, r.NativeShare*100, r.MinNative*100))
		}
		if off := math.Abs(r.Share - r.Target); r.Examples > 0 && off > MaxShareDrift {
			why = append(why, fmt.Sprintf(
				"%s came out at %.0f%% of the set against the %.0f%% it was composed to, and a mixture that followed whichever source turned out to be large is a mixture nobody chose",
				r.Capability, r.Share*100, r.Target*100))
		}
	}
	if n := s.Examples(); n > 0 {
		if off := math.Abs(float64(n)-Target) / Target; off > Tolerance {
			why = append(why, fmt.Sprintf(
				"the set holds %s against the %s it was composed to, which is %.0f%% off and past the %.0f%% the last shard of a source explains",
				count(n, "example"), count(Target, "example"), off*100, Tolerance*100))
		}
	}
	return why
}

// Settled reports whether the set is composed rather than collected.
func (s Set) Settled() bool { return len(s.Blocking()) == 0 }

// Holds reports whether the set supports the claim it was composed to support,
// which needs the composition to stand and the arms to be comparable.
func (s Set) Holds() bool { return s.Settled() && s.Matched() }

// Verdict is the set in one sentence.
func (s Set) Verdict() string {
	if why := s.Blocking(); len(why) > 0 {
		return why[0]
	}
	native, translated := s.Origin(Native), s.Origin(Translated)
	head := fmt.Sprintf(
		"%s holds %s over %d capabilities, %s of them native and %s translated, and the two are reported apart because a set that adds them cannot answer the question it was built for.",
		s.Name, count(s.Examples(), "example"), len(Slate),
		thousands(native.Examples), thousands(translated.Examples))
	switch {
	case s.ArmSize() < MinArm:
		return head + fmt.Sprintf(
			" The arms come out at %s each, under the %s that is a training run rather than a demonstration, so a difference between them would be a measurement of how little each one read.",
			thousands(s.ArmSize()), count(MinArm, "example"))
	case s.Drift() > MaxDrift:
		return head + fmt.Sprintf(
			" The two arms differ by %.1f points on %s against a %.1f point line, so a result would be a measurement of the capability mix rather than of the origin.",
			s.Drift()*100, s.Drifted(), MaxDrift*100)
	}
	return head + fmt.Sprintf(
		" The arms run at %s each and their mixes agree within %.1f points, so what a comparison of them measures is origin.",
		thousands(s.ArmSize()), s.Drift()*100)
}

// arm counts one capability on one side of the comparison. The native side is
// the mixture and the translated side is what was held out of it.
func (s Set) arm(capability, origin string) int64 {
	from := s.In()
	if origin == Translated {
		from = s.Aside()
	}
	var n int64
	for _, sl := range from {
		if sl.Capability == capability && sl.Origin == origin {
			n += sl.Examples
		}
	}
	return n
}

func (s Set) filter(want func(Slice) bool) []Slice {
	var out []Slice
	for _, sl := range s.Slices {
		if want(sl) {
			out = append(out, sl)
		}
	}
	return out
}

// ReadSet loads a set from a file of one JSON slice per line.
func ReadSet(name, path string) (Set, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Set{}, fmt.Errorf("tron: %w", err)
	}
	s := Set{Name: name}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var sl Slice
		if err := dec.Decode(&sl); err != nil {
			return Set{}, fmt.Errorf("tron: %s line %d: %w", path, i+1, err)
		}
		s.Slices = append(s.Slices, sl)
	}
	if len(s.Slices) == 0 {
		return Set{}, fmt.Errorf("tron: %s holds no slices", path)
	}
	return s, nil
}

func count(n int64, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%s %ss", thousands(n), noun)
}

// thousands writes a count the way somebody reads it out loud.
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
