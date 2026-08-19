// Package total adds up a release without letting the addition decide anything.
//
// Cộng is to add. The arithmetic is trivial and every hard part of this is
// about what may be added to what. A corpus assembled from five ingested
// sources, a crawl, a recovery pass, three extraction routes and a generator
// does not have one number, it has several, and the way that gets published
// wrong is not a bad sum. It is a good sum over rows that had no business being
// in the same column.
//
// Three separations are load bearing. Natural text and generated text are never
// summed, and the headline is the natural one, because a reader who downloads a
// corpus of Vietnamese believes a person wrote it and nothing further down the
// card undoes that. The publishable subset is stated apart from the total,
// since license class is a per document column and a corpus whose publishable
// subset is unstated is one nobody can safely use. And per source contribution
// is a table rather than a total, because where the tokens came from is the
// question anybody checking the headline asks second.
//
// Two counts are refused rather than added. Counts from two tokenizers, since
// two tokenizers are two units and their sum is a number in neither. And counts
// from two snapshots, since a release is a snapshot and adding across them
// publishes a corpus that never existed at one moment.
//
// The comparison ratios in the headline claim are computed from the number that
// came back rather than written down next to it. The kill criterion for this
// slice says that if the corpus lands short the real number gets published and
// the ratios get restated, and a ratio that is restated by hand is a ratio that
// gets restated once.
package total

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
)

// Natural and Made are the two origins, and the whole of the rule is that they
// are never added together.
const (
	Natural = "natural"
	Made    = "synthetic"
)

// Target is the claim, in natural tokens.
const Target = 300e9

// Floor is the kill criterion. Under it the project publishes the real number
// and restates the ratios rather than restating the project.
const Floor = 250e9

// MinBytesPerChar and MaxBytesPerChar bound the ratio of stored bytes to
// characters. Vietnamese in UTF-8 costs more than a byte a character, since
// every vowel carrying a tone mark is two, so a row where bytes and characters
// agree counted the bytes of something that was not Vietnamese.
const (
	MinBytesPerChar = 1.05
	MaxBytesPerChar = 2.0
)

// MinFertility and MaxFertility bound tokens per syllable. Vietnamese writes
// spaces between syllables rather than words, and every tokenizer measured for
// this project spends between one and a half and two and a half tokens on one,
// so a row outside these was counted with a tokenizer that is not the pinned
// one whatever the row says.
const (
	MinFertility = 1.0
	MaxFertility = 3.0
)

// An Incumbent is a corpus the headline is stated against.
type Incumbent struct {
	Name   string
	Tokens int64

	// Note is where the number came from, since two of these are published by
	// their own projects and one is this project's measurement of somebody
	// else's corpus.
	Note string
}

// Incumbents are the three corpora the claim is written against, in the order
// the claim states them.
var Incumbents = []Incumbent{
	{"HPLT v3 vie_Latn", 143_700_000_000, "gao's own reading of 40 MB off every one of its six buckets, standing in until the exact count lands"},
	{"PhoGPT", 102_000_000_000, "as published by its authors"},
	{"CulturaX", 55_400_000_000, "as published"},
}

// Ratio is what a headline of n tokens is worth against this corpus.
func (i Incumbent) Ratio(n int64) float64 {
	if i.Tokens <= 0 {
		return 0
	}
	return float64(n) / float64(i.Tokens)
}

// A Group is one source, one origin and one license class, counted once.
type Group struct {
	Source   string `json:"source"`
	Snapshot string `json:"snapshot"`

	// Origin is natural or synthetic.
	Origin string `json:"origin"`

	// Class is the license class every document in this group carries, which is
	// what decides whether these tokens ship.
	Class doc.LicenseClass `json:"class"`

	Documents int64 `json:"documents"`
	Bytes     int64 `json:"bytes"`
	Chars     int64 `json:"chars"`
	Syllables int64 `json:"syllables"`
	Tokens    int64 `json:"tokens"`

	// Tokenizer is what produced the token count, named because a token count
	// without one is a number in an unstated unit.
	Tokenizer string `json:"tokenizer"`
}

// Natural reports whether a person wrote this text.
func (g Group) Natural() bool { return g.Origin == Natural }

// Made reports whether a model wrote it.
func (g Group) Made() bool { return g.Origin == Made }

// Ships reports whether this group's license class lets it into the published
// artifact.
func (g Group) Ships() bool { return g.Class.Publishable() }

// Weight is stored bytes per character, which says whether the bytes were
// counted on Vietnamese.
func (g Group) Weight() float64 {
	if g.Chars <= 0 {
		return 0
	}
	return float64(g.Bytes) / float64(g.Chars)
}

// Fertility is tokens per syllable, which says whether the tokenizer named is
// the tokenizer used.
func (g Group) Fertility() float64 {
	if g.Syllables <= 0 {
		return 0
	}
	return float64(g.Tokens) / float64(g.Syllables)
}

// Blocking is every reason this group is not a count anybody can add.
func (g Group) Blocking() []string {
	var why []string
	if g.Source == "" {
		return []string{"a count with no source on it is a total, and the table this builds is the answer to where the tokens came from"}
	}
	if !g.Natural() && !g.Made() {
		why = append(why, fmt.Sprintf(
			"%s does not say whether a person or a model wrote it, and those two numbers are never added together",
			g.Source))
	}
	if !g.Class.Valid() || g.Class == doc.LicenseUnknown {
		why = append(why, fmt.Sprintf("%s carries no license determination, so whether it ships is undecided and the publishable subset cannot be stated", g.Source))
	}
	if g.Snapshot == "" {
		why = append(why, fmt.Sprintf("%s does not name the snapshot it was counted off, and a release is a snapshot rather than a date", g.Source))
	}
	if g.Tokenizer == "" {
		why = append(why, fmt.Sprintf("%s names no tokenizer, and a token count in an unstated unit is not a count", g.Source))
	}
	for _, empty := range []struct {
		n    int64
		what string
	}{
		{g.Documents, "documents"},
		{g.Bytes, "bytes"},
		{g.Chars, "characters"},
		{g.Syllables, "syllables"},
		{g.Tokens, "tokens"},
	} {
		if empty.n <= 0 {
			why = append(why, fmt.Sprintf("%s counts no %s, and a release states all four units rather than the one that was easy", g.Source, empty.what))
		}
	}
	if w := g.Weight(); g.Chars > 0 && (w < MinBytesPerChar || w > MaxBytesPerChar) {
		why = append(why, fmt.Sprintf(
			"%s stores %.2f bytes a character, outside %.2f to %.2f, and Vietnamese costs two bytes on every vowel that carries a tone mark, so one of those two columns is counting something else",
			g.Source, w, MinBytesPerChar, MaxBytesPerChar))
	}
	if f := g.Fertility(); g.Syllables > 0 && (f < MinFertility || f > MaxFertility) {
		why = append(why, fmt.Sprintf(
			"%s spends %.2f tokens a syllable, outside %.1f to %.1f, so the count did not come from the tokenizer the row names",
			g.Source, f, MinFertility, MaxFertility))
	}
	return why
}

// A Total is what a set of groups adds up to.
type Total struct {
	Source string `json:"source"`

	Groups    int   `json:"groups"`
	Documents int64 `json:"documents"`
	Bytes     int64 `json:"bytes"`
	Chars     int64 `json:"chars"`
	Syllables int64 `json:"syllables"`
	Tokens    int64 `json:"tokens"`
}

func (t Total) add(g Group) Total {
	t.Groups++
	t.Documents += g.Documents
	t.Bytes += g.Bytes
	t.Chars += g.Chars
	t.Syllables += g.Syllables
	t.Tokens += g.Tokens
	return t
}

// Share is this total's tokens against a headline.
func (t Total) Share(headline int64) float64 {
	if headline <= 0 {
		return 0
	}
	return float64(t.Tokens) / float64(headline)
}

// A Sheet is the counts a release is published from.
type Sheet struct {
	Release string  `json:"release"`
	Groups  []Group `json:"groups"`
}

// Natural is every group a person wrote.
func (s Sheet) Natural() []Group { return s.filter(Group.Natural) }

// Made is every group a model wrote, kept apart from the number above it.
func (s Sheet) Made() []Group { return s.filter(Group.Made) }

// Ships is the natural groups whose license class lets them into the published
// artifact.
func (s Sheet) Ships() []Group {
	return s.filter(func(g Group) bool { return g.Natural() && g.Ships() })
}

// Held is the natural groups that stay in the store, which is the number a
// reader needs to know is missing from what they downloaded.
func (s Sheet) Held() []Group {
	return s.filter(func(g Group) bool { return g.Natural() && !g.Ships() })
}

// Headline is the number the project publishes, which is natural tokens.
func (s Sheet) Headline() int64 { return sum(s.Natural()).Tokens }

// Corpus is the whole natural side in all four units.
func (s Sheet) Corpus() Total { return sum(s.Natural()) }

// Publishable is the part of that a third party can download.
func (s Sheet) Publishable() Total { return sum(s.Ships()) }

// Generated is the synthetic side, stated on its own line and added to nothing.
func (s Sheet) Generated() Total { return sum(s.Made()) }

// Sources is the per source contribution table, largest first, over natural
// text only.
func (s Sheet) Sources() []Total {
	at := map[string]Total{}
	var order []string
	for _, g := range s.Natural() {
		if _, seen := at[g.Source]; !seen {
			order = append(order, g.Source)
		}
		t := at[g.Source]
		t.Source = g.Source
		at[g.Source] = t.add(g)
	}
	out := make([]Total, 0, len(order))
	for _, name := range order {
		out = append(out, at[name])
	}
	slices.SortStableFunc(out, func(a, b Total) int {
		switch {
		case a.Tokens > b.Tokens:
			return -1
		case a.Tokens < b.Tokens:
			return 1
		default:
			return strings.Compare(a.Source, b.Source)
		}
	})
	return out
}

// Classes is what each license class holds, in the order the classes are
// declared, so the publishable subset reads against the rest of them.
func (s Sheet) Classes() []Total {
	at := map[doc.LicenseClass]Total{}
	for _, g := range s.Natural() {
		t := at[g.Class]
		t.Source = g.Class.String()
		at[g.Class] = t.add(g)
	}
	var out []Total
	for _, c := range []doc.LicenseClass{
		doc.LicenseOpen, doc.LicensePermissiveAttribution,
		doc.LicenseRestricted, doc.LicenseUnredistributable, doc.LicenseUnknown,
	} {
		if t, ok := at[c]; ok {
			out = append(out, t)
		}
	}
	return out
}

// Tokenizer is the unit every token count in the sheet is in.
func (s Sheet) Tokenizer() string {
	if len(s.Groups) == 0 {
		return ""
	}
	return s.Groups[0].Tokenizer
}

// Snapshot is the store snapshot the sheet was counted off.
func (s Sheet) Snapshot() string {
	if len(s.Groups) == 0 {
		return ""
	}
	return s.Groups[0].Snapshot
}

// Restate is the headline against each incumbent, computed from the number that
// came back rather than from the number that was hoped for.
func (s Sheet) Restate() []string {
	out := make([]string, 0, len(Incumbents))
	for _, i := range Incumbents {
		out = append(out, fmt.Sprintf("%.1fx %s", i.Ratio(s.Headline()), i.Name))
	}
	return out
}

// Blocking is every reason this sheet is not a release count.
func (s Sheet) Blocking() []string {
	if len(s.Groups) == 0 {
		return []string{"no counts were read, so what a release would publish is the plan's arithmetic rather than the corpus"}
	}
	var why []string
	if s.Release == "" {
		why = append(why, "the counts do not say which release they are for, and a headline belongs to a version rather than to a project")
	}
	seen := map[string]bool{}
	units := map[string]bool{}
	snapshots := map[string]bool{}
	for _, g := range s.Groups {
		key := g.Source + "\x00" + g.Origin + "\x00" + g.Class.String()
		if seen[key] {
			why = append(why, fmt.Sprintf("%s appears twice for the same origin and license class, and counting a group twice is the one mistake a total cannot show", g.Source))
		}
		seen[key] = true
		if g.Tokenizer != "" {
			units[g.Tokenizer] = true
		}
		if g.Snapshot != "" {
			snapshots[g.Snapshot] = true
		}
		why = append(why, g.Blocking()...)
	}
	if len(units) > 1 {
		why = append(why, fmt.Sprintf(
			"the counts come from %s, and two tokenizers are two units, so their sum is a number in neither of them",
			plural(len(units), "tokenizer")))
	}
	if len(snapshots) > 1 {
		why = append(why, "the counts come off more than one snapshot, and adding across snapshots publishes a corpus that did not exist at any one moment")
	}
	if len(s.Natural()) == 0 {
		why = append(why, "no natural text was counted, and the headline number of this project is the natural one")
	}
	return why
}

// Settled reports whether the sheet is a count rather than a collection of
// numbers.
func (s Sheet) Settled() bool { return len(s.Blocking()) == 0 }

// Holds reports whether the corpus met the claim.
func (s Sheet) Holds() bool { return s.Settled() && s.Headline() >= Target }

// Killed reports whether the corpus came in under the kill criterion, which is
// a different thing from missing the target and has a different consequence.
func (s Sheet) Killed() bool { return s.Settled() && s.Headline() < Floor }

// Verdict is the release in one sentence.
func (s Sheet) Verdict() string {
	if why := s.Blocking(); len(why) > 0 {
		return why[0]
	}
	against := strings.Join(s.Restate(), ", ")
	switch {
	case s.Killed():
		return fmt.Sprintf(
			"%s came back at %s of natural tokens, under the %s the kill criterion names, so the number to publish is this one and the claim is restated at %s rather than defended.",
			s.Release, tokens(s.Headline()), tokens(Floor), against)
	case !s.Holds():
		return fmt.Sprintf(
			"%s came back at %s of natural tokens against the %s claimed, which is still %s, and those are the ratios the headline gets restated with.",
			s.Release, tokens(s.Headline()), tokens(Target), against)
	}
	return fmt.Sprintf(
		"%s holds %s of natural tokens, of which %s ship, which is %s.",
		s.Release, tokens(s.Headline()), tokens(s.Publishable().Tokens), against)
}

func (s Sheet) filter(want func(Group) bool) []Group {
	var out []Group
	for _, g := range s.Groups {
		if want(g) {
			out = append(out, g)
		}
	}
	return out
}

func sum(groups []Group) Total {
	var t Total
	for _, g := range groups {
		t = t.add(g)
	}
	return t
}

// ReadSheet loads a sheet from a file of one JSON group per line.
func ReadSheet(release, path string) (Sheet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Sheet{}, fmt.Errorf("total: %w", err)
	}
	s := Sheet{Release: release}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var g Group
		if err := dec.Decode(&g); err != nil {
			return Sheet{}, fmt.Errorf("total: %s line %d: %w", path, i+1, err)
		}
		s.Groups = append(s.Groups, g)
	}
	if len(s.Groups) == 0 {
		return Sheet{}, fmt.Errorf("total: %s holds no counts", path)
	}
	return s, nil
}

// tokens renders a count in the unit the claim is written in.
func tokens(n int64) string { return fmt.Sprintf("%.1fB", float64(n)/1e9) }

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
