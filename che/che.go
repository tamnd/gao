// Package che finds and removes personal data from Vietnamese text.
//
// Che means to cover. The stage does not delete what it finds, it covers it
// with a tag that says what was there, because the two are not the same thing
// for a model. A phone number replaced by zeros teaches a model that phone
// numbers are zeros. A phone number replaced by nothing teaches it that
// sentences about calling somebody end in the middle. A phone number replaced
// by [SODIENTHOAI] teaches it that a phone number goes there, which is the only
// one of the three that is true.
//
// # Why this is a Vietnamese problem rather than a general one
//
// Every identifier a person in Vietnam carries is a run of digits, and so is
// every price, date, article number and lottery result on the same page. A
// detector that matches digit runs has enormous recall and no precision, and
// the corpus it produces has holes in the middle of ordinary sentences. So each
// detector here validates the structure of what it matched: a national ID has a
// province code in it and the list of province codes is finite, a phone number
// has a carrier prefix and the list of carrier prefixes is finite, a tax code
// has a check digit. What cannot be validated is required to carry a cue near
// it in the text.
//
// The polarity of every one of those rules is chosen the same way. A rule that
// is wrong should cost precision, which means a number that was not personal
// gets covered and a sentence reads slightly worse. A rule that is wrong in the
// other direction costs recall, which means a real national ID stays in a
// published corpus. So a validator is never the only thing standing between an
// identifier and publication: a cue in the text is enough on its own, and the
// validator only decides whether a bare number with no cue around it is covered
// as well.
//
// # Names
//
// Names are not redacted by default and this is a decision rather than an
// omission. A Vietnamese corpus with no Vietnamese names in it is not a
// Vietnamese corpus. It cannot answer who wrote a poem, it has never seen the
// name of a president, and the names it does not know are exactly the names a
// model needs to have seen to write Vietnamese about Vietnamese people. Half of
// the country is named Nguyễn and redacting Nguyễn is not privacy work.
//
// What is actually private is a name next to a way of reaching the person it
// belongs to. So a name is covered when the paragraph it sits in also holds an
// identifier that was covered, and left alone otherwise. That is the
// co-occurrence policy, and it means a news article about a minister keeps the
// minister and a classified advertisement loses the seller.
//
// # Levels
//
// L0 finds and records and removes nothing. It is not a null option: it is what
// the recall measurement runs against, and it is what says how much personal
// data a source holds before anybody decides what to do about it.
//
// L1 covers identifiers and leaves names and addresses. It is the level for
// text whose upstream is already published under its own terms.
//
// L2 covers identifiers, names in paragraphs that hold one, and street level
// addresses. It is the level for anything derived from our own crawl, where
// nobody upstream made the decision for us.
//
// The level is recorded per document rather than per run, because a corpus
// assembled from sources at different levels is a corpus where the question
// "what was removed from this document" has a per document answer.
package che

import (
	"sort"
	"strings"
)

// Kind is what a span was found to be. The values are the tags that go into the
// text, lowercased, so that a reader of the corpus and a reader of the tally are
// looking at the same vocabulary.
type Kind string

const (
	KindEmail   Kind = "email"
	KindPhone   Kind = "phone"
	KindCCCD    Kind = "cccd"
	KindCMND    Kind = "cmnd"
	KindTax     Kind = "tax"
	KindPlate   Kind = "plate"
	KindName    Kind = "name"
	KindAddress Kind = "address"
)

// tags are what replaces a span in the text. They are written in Vietnamese
// without tone marks, uppercase, in brackets, so that they survive
// normalization unchanged, cannot be produced by ordinary text, and are one
// token in most vocabularies.
var tags = map[Kind]string{
	KindEmail:   "[EMAIL]",
	KindPhone:   "[SODIENTHOAI]",
	KindCCCD:    "[CCCD]",
	KindCMND:    "[CMND]",
	KindTax:     "[MST]",
	KindPlate:   "[BIENSO]",
	KindName:    "[HOTEN]",
	KindAddress: "[DIACHI]",
}

// Tag is what covers a span of this kind.
func (k Kind) Tag() string { return tags[k] }

// Valid reports whether k is a kind this package produces.
func (k Kind) Valid() bool { _, ok := tags[k]; return ok }

// Kinds returns every kind, in the order the report lists them.
func Kinds() []Kind {
	return []Kind{
		KindEmail, KindPhone, KindCCCD, KindCMND, KindTax, KindPlate,
		KindName, KindAddress,
	}
}

// Level is how much of what was found gets covered.
type Level int

const (
	// L0 records and covers nothing.
	L0 Level = iota
	// L1 covers identifiers.
	L1
	// L2 covers identifiers, names beside them, and street addresses.
	L2
)

// String returns the level as it is recorded on a document.
func (l Level) String() string {
	switch l {
	case L0:
		return "L0"
	case L1:
		return "L1"
	case L2:
		return "L2"
	}
	return "invalid"
}

// ParseLevel reads a level back from a document or a command line.
func ParseLevel(s string) (Level, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "L0", "0":
		return L0, true
	case "L1", "1":
		return L1, true
	case "L2", "2":
		return L2, true
	}
	return L0, false
}

// Covers reports whether this level covers a span of that kind. It is exported
// because a report has to say what a level would have done to a document
// without redacting the document to find out.
func (l Level) Covers(k Kind) bool {
	switch l {
	case L1:
		return k != KindName && k != KindAddress
	case L2:
		return true
	}
	return false
}

// Span is one piece of personal data in a document. Start and End are byte
// offsets into the text that was searched, which is the normalized text, since
// nothing upstream of normalization has stable offsets.
type Span struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Kind  Kind   `json:"kind"`
	Text  string `json:"text,omitempty"`

	// Cued records that the span was found because the text around it named
	// what it was, rather than because the digits alone validated. It is the
	// number that says how much of the recall depends on people writing "số
	// điện thoại" before their phone number, which they mostly do not.
	Cued bool `json:"cued,omitempty"`
}

// Find returns every piece of personal data in a document, in the order it
// appears, with no two spans overlapping.
func Find(text string) []Span {
	var found []Span
	found = append(found, findEmails(text)...)
	found = append(found, findNumbers(text)...)
	found = append(found, findPlates(text)...)
	found = append(found, findAddresses(text)...)
	found = resolve(found)
	found = append(found, findNames(text, found)...)
	return resolve(found)
}

// resolve sorts the spans and drops the ones that overlap a span already kept.
// The longer match wins, because a national ID that also parses as something
// shorter is a national ID, and a tie goes to the kind that was validated more
// strictly.
func resolve(found []Span) []Span {
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		switch {
		case a.Start != b.Start:
			return a.Start < b.Start
		case a.End != b.End:
			return a.End > b.End
		}
		return strictness(a.Kind) > strictness(b.Kind)
	})
	kept := make([]Span, 0, len(found))
	end := -1
	for _, s := range found {
		if s.Start < end {
			continue
		}
		kept = append(kept, s)
		end = s.End
	}
	return kept
}

// strictness breaks a tie between two kinds that matched the same bytes. A kind
// with more structure behind it is the better explanation of the same digits.
func strictness(k Kind) int {
	switch k {
	case KindCCCD:
		return 5
	case KindTax:
		return 4
	case KindPhone:
		return 3
	case KindCMND:
		return 2
	case KindPlate:
		return 1
	}
	return 0
}

// Redact covers what the level covers and returns the new text along with every
// span that was found, covered or not. The spans are returned at every level,
// including L0, because what a source holds is worth knowing separately from
// what was done about it.
func Redact(text string, level Level) (string, []Span) {
	found := Find(text)
	if level == L0 {
		return text, found
	}
	var b strings.Builder
	b.Grow(len(text))
	at := 0
	for _, s := range found {
		if !level.Covers(s.Kind) {
			continue
		}
		b.WriteString(text[at:s.Start])
		b.WriteString(s.Kind.Tag())
		at = s.End
	}
	b.WriteString(text[at:])
	return b.String(), found
}

// Tally is what a run of this stage reports.
type Tally struct {
	Documents int64 `json:"documents"`

	// Carrying is how many documents held at least one span. It is the number
	// that matters for a publication decision, since a corpus where one
	// document in ten thousand holds a phone number is a different problem from
	// one where one in ten does.
	Carrying int64 `json:"carrying"`

	Spans   int64          `json:"spans"`
	Covered int64          `json:"covered"`
	Cued    int64          `json:"cued"`
	ByKind  map[Kind]int64 `json:"by_kind,omitempty"`
}

// Add folds one document into the tally.
func (t *Tally) Add(level Level, found []Span) {
	t.Documents++
	if len(found) > 0 {
		t.Carrying++
	}
	if t.ByKind == nil {
		t.ByKind = map[Kind]int64{}
	}
	for _, s := range found {
		t.Spans++
		t.ByKind[s.Kind]++
		if s.Cued {
			t.Cued++
		}
		if level.Covers(s.Kind) {
			t.Covered++
		}
	}
}

// Rate is the share of documents that held at least one piece of personal data.
func (t Tally) Rate() float64 {
	if t.Documents == 0 {
		return 0
	}
	return float64(t.Carrying) / float64(t.Documents)
}
