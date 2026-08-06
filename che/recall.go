package che

// The recall measurement.
//
// Every detector in this package is a trade between two mistakes, and the
// documentation says which way each one leans. That is worth nothing without a
// number. A detector nobody measured is a claim, and a privacy claim that has
// never been checked against text somebody read by hand is the kind of claim
// that holds until the day it matters.
//
// So the personal data in the documents under testdata/recall is marked by
// hand, in the text, and this file is what compares what a person marked with
// what the detectors found. The measurement runs in the test suite and it runs
// from the command line, because a number that only exists in a test log is a
// number nobody outside the repository can check.
//
// # Why the marks are in the text
//
// A labeled set held as offsets in a second file rots the first time somebody
// fixes a typo in the first one. Marking a span where it sits keeps the label
// attached to the thing it labels, keeps the fixture readable as a document,
// and makes a review of the set a review of ordinary Vietnamese pages rather
// than of a table of numbers.
//
// # What is marked
//
// What the policy says must be covered, and not everything a person could point
// at. Names are the case that matters: a name beside an identifier is marked,
// because L2 covers it, and a name in an article about a minister is not,
// because covering that is what this package refuses to do. So the recall here
// is recall against the policy in che.go rather than against every proper noun
// in the corpus, and reading it any other way makes it say something it does
// not say.

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// The marker a labeled document writes its spans with. A span is written
// {{kind:text}} and nothing else in the file is touched, so the fixture reads
// as the page it came from.
const (
	markOpen  = "{{"
	markClose = "}}"
)

// Marked is one document of the labeled set: the text as a detector sees it,
// and the spans a person said are in it.
type Marked struct {
	// Name is the file the document came from, which is what a report prints
	// when a detector misses something.
	Name string

	// Text is the document with the markers taken out. Every offset in Gold is
	// into this string, which is what a detector is given.
	Text string

	// Gold is what must be found, in the order it appears.
	Gold []Span
}

// ParseMarked reads a labeled document.
//
// It is strict about everything it can be strict about. An unknown kind, an
// unclosed marker or a marker with no text in it is an error rather than a span
// quietly dropped, because a labeled set that silently loses a label reports a
// recall of one on a detector that found nothing.
func ParseMarked(name, annotated string) (Marked, error) {
	m := Marked{Name: name}
	var b strings.Builder
	b.Grow(len(annotated))

	for at := 0; at < len(annotated); {
		open := strings.Index(annotated[at:], markOpen)
		if open < 0 {
			b.WriteString(annotated[at:])
			break
		}
		open += at
		b.WriteString(annotated[at:open])

		close := strings.Index(annotated[open:], markClose)
		if close < 0 {
			return Marked{}, fmt.Errorf("che: %s: a mark opens at byte %d and never closes", name, open)
		}
		body := annotated[open+len(markOpen) : open+close]
		colon := strings.Index(body, ":")
		if colon < 0 {
			return Marked{}, fmt.Errorf("che: %s: the mark %q does not say what kind it is, which is written {{kind:text}}", name, body)
		}
		kind, text := Kind(strings.TrimSpace(body[:colon])), body[colon+1:]
		if !kind.Valid() {
			return Marked{}, fmt.Errorf("che: %s: %q is not a kind this package finds", name, kind)
		}
		if text == "" {
			return Marked{}, fmt.Errorf("che: %s: the %s mark at byte %d has no text in it", name, kind, open)
		}
		start := b.Len()
		b.WriteString(text)
		m.Gold = append(m.Gold, Span{Start: start, End: b.Len(), Kind: kind, Text: text})
		at = open + close + len(markClose)
	}

	m.Text = b.String()
	return m, nil
}

//go:embed testdata/recall/*.txt
var labeledFS embed.FS

// Labeled is the labeled set that ships with the package.
//
// It is embedded rather than read off disk so that the measurement runs from an
// installed binary. A recall figure anybody can reproduce with one command is
// worth more than one that needs a checkout.
func Labeled() ([]Marked, error) {
	names, err := fs.Glob(labeledFS, "testdata/recall/*.txt")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	out := make([]Marked, 0, len(names))
	for _, name := range names {
		b, err := labeledFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		m, err := ParseMarked(path.Base(name), string(b))
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("che: the labeled set is empty, and a recall of nothing reads as a recall of one")
	}
	return out, nil
}

// Count is one detector's score.
type Count struct {
	// Gold is how many spans of this kind a person marked, Hit is how many of
	// those a detector covered, and Found is how many spans of this kind the
	// detectors produced in total.
	Gold  int `json:"gold"`
	Hit   int `json:"hit"`
	Found int `json:"found"`

	// Spurious is how many found spans no marked span accounts for. On a
	// detector with no marked spans at all it is the whole story, which is what
	// the documents with nothing in them are for.
	Spurious int `json:"spurious"`
}

// Recall is the share of marked spans that were covered. A detector with
// nothing marked has no recall rather than a recall of one, and the report
// prints it as such.
func (c Count) Recall() float64 {
	if c.Gold == 0 {
		return 0
	}
	return float64(c.Hit) / float64(c.Gold)
}

// Precision is the share of found spans a person had marked.
func (c Count) Precision() float64 {
	if c.Found == 0 {
		return 0
	}
	return float64(c.Found-c.Spurious) / float64(c.Found)
}

// Miss is one marked span nothing covered, with the document it is in, so that
// a failing measurement names the page to go and read.
type Miss struct {
	Document string `json:"document"`
	Span     Span   `json:"span"`

	// Partial says a detector found something overlapping this span but did not
	// cover all of it. It is worth separating: a phone number covered except for
	// its last two digits is a different failure from a phone number nothing saw,
	// and it usually means a boundary is wrong rather than a validator.
	Partial bool `json:"partial,omitempty"`
}

// Score is what a run of the measurement reports.
type Score struct {
	Documents int             `json:"documents"`
	ByKind    map[Kind]Count  `json:"by_kind"`
	Total     Count           `json:"total"`
	Missed    []Miss          `json:"missed,omitempty"`
	Spurious  map[Kind][]Miss `json:"spurious,omitempty"`
}

// Measure compares what the detectors find against what a person marked.
//
// A marked span counts as covered when a found span of the same kind holds all
// of it. Covering part of it does not count, and that is deliberate: the point
// of a span is that the text inside it does not reach the corpus, and half of a
// national ID with the province code still attached has reached the corpus.
func Measure(set []Marked) Score {
	s := Score{Documents: len(set), ByKind: map[Kind]Count{}}
	for _, m := range set {
		found := Find(m.Text)
		accountedFor := make([]bool, len(found))

		for _, gold := range m.Gold {
			c := s.ByKind[gold.Kind]
			c.Gold++
			hit, partial := false, false
			for i, f := range found {
				if f.Kind != gold.Kind || f.End <= gold.Start || f.Start >= gold.End {
					continue
				}
				accountedFor[i] = true
				if f.Start <= gold.Start && f.End >= gold.End {
					hit = true
				} else {
					partial = true
				}
			}
			if hit {
				c.Hit++
			} else {
				s.Missed = append(s.Missed, Miss{Document: m.Name, Span: gold, Partial: partial})
			}
			s.ByKind[gold.Kind] = c
		}

		for i, f := range found {
			c := s.ByKind[f.Kind]
			c.Found++
			if !accountedFor[i] {
				c.Spurious++
				if s.Spurious == nil {
					s.Spurious = map[Kind][]Miss{}
				}
				s.Spurious[f.Kind] = append(s.Spurious[f.Kind], Miss{Document: m.Name, Span: f})
			}
			s.ByKind[f.Kind] = c
		}
	}

	for _, c := range s.ByKind {
		s.Total.Gold += c.Gold
		s.Total.Hit += c.Hit
		s.Total.Found += c.Found
		s.Total.Spurious += c.Spurious
	}
	return s
}
