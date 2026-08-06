package theo

// Reading an answer and deciding what language it is in, which is the half of
// this package that can be wrong.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/tamnd/gao/doc"
)

// Words that carry an English sentence. They are function words rather than
// content words, because content words are exactly what a Vietnamese answer
// legitimately borrows and function words are what it does not.
//
// A few obvious ones are missing on purpose. Vietnamese without tone marks
// spells cần as can, máy as may, và as va, and a detector that counts those as
// English reports drift in answers that never drifted.
var englishWords = map[string]bool{
	"the": true, "and": true, "of": true, "is": true, "are": true, "was": true,
	"were": true, "with": true, "for": true, "that": true, "this": true,
	"these": true, "those": true, "you": true, "your": true, "they": true,
	"their": true, "there": true, "have": true, "has": true, "from": true,
	"which": true, "will": true, "would": true, "should": true, "could": true,
	"about": true, "because": true, "when": true, "what": true, "here": true,
	"not": true, "but": true, "we": true, "our": true, "it": true, "its": true,
	"if": true, "then": true, "into": true, "over": true, "than": true,
	"first": true, "also": true, "make": true, "makes": true, "means": true,
}

// Words that carry a Vietnamese sentence written without its tone marks. Same
// rule in the other direction: these are the function words, and the ones that
// collide with English are left out rather than argued about.
var bareWords = map[string]bool{
	"cua": true, "nhung": true, "duoc": true, "khong": true, "nguoi": true,
	"viec": true, "nay": true, "voi": true, "hoac": true, "neu": true,
	"tren": true, "duoi": true, "cung": true, "hon": true, "rat": true,
	"phai": true, "boi": true, "trong": true, "theo": true, "nhieu": true,
	"chung": true, "minh": true, "mot": true, "ban": true, "se": true,
	"dang": true, "thi": true, "moi": true, "giup": true, "lam": true,
	"chinh": true, "cac": true, "nhu": true, "vay": true, "den": true,
	"khi": true, "sau": true, "truoc": true, "ngay": true, "thang": true,
	"nhat": true, "tuc": true, "biet": true, "hieu": true, "them": true,
}

// Classify says what language a stretch of text is written in.
//
// It counts words rather than characters, because a Vietnamese sentence with an
// English term in it is Vietnamese and a character count says otherwise. A
// stretch with a tone mark on it is Vietnamese unless the English function words
// outnumber the marked ones, which is what a quotation inside an English
// sentence looks like. A stretch with no marks at all is decided between English
// and unmarked Vietnamese by which set of function words shows up, and if
// neither does it is [Other] and counts for nobody.
func Classify(s string) Lang {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '\''
	})
	var markedWords, en, bare int
	for _, w := range words {
		switch {
		case marked(w):
			markedWords++
		case englishWords[w]:
			en++
		case bareWords[w]:
			bare++
		}
	}
	switch {
	case markedWords >= 2 && markedWords >= en:
		return Viet
	case en >= 2 && en > bare:
		return Eng
	case bare >= 2 && bare > en:
		return Bare
	case markedWords >= 1 && en == 0:
		return Viet
	default:
		return Other
	}
}

// A Reading is one answer taken apart.
type Reading struct {
	// Wrote is the language of the answer as a whole, which is the language most
	// of its decidable sentences are in.
	Wrote Lang `json:"wrote"`

	// Opened is the language of the first decidable sentence, and it is separate
	// from Wrote because the difference between them is the failure this package
	// is built around.
	Opened Lang `json:"opened"`

	// Segments is how many sentences could be decided at all, and Drifted says
	// whether they were not all the same language.
	Segments int  `json:"segments"`
	Drifted  bool `json:"drifted"`

	// At is how far into the answer the language first changed, as a share of the
	// decidable sentences. A model that drifts at 0.9 and one that answers in
	// English outright are the same failure rate and different bugs.
	At float64 `json:"at,omitempty"`
}

// Read takes an answer apart sentence by sentence.
//
// Code fences and inline code come out first. A model that writes a shell
// command in an answer has not switched language, and a measure that says it did
// teaches the model to translate identifiers, which is worse than the thing
// being measured.
func Read(text string) Reading {
	var r Reading
	langs := make([]Lang, 0, 8)
	for _, seg := range sentences(strip(text)) {
		l := Classify(seg)
		if l == Other {
			continue
		}
		langs = append(langs, l)
	}
	r.Segments = len(langs)
	if len(langs) == 0 {
		r.Wrote, r.Opened = Other, Other
		return r
	}

	r.Opened = langs[0]
	counts := map[Lang]int{}
	for _, l := range langs {
		counts[l]++
	}
	best := 0
	for _, l := range []Lang{Viet, Eng, Bare} {
		if counts[l] > best {
			best, r.Wrote = counts[l], l
		}
	}
	for i, l := range langs {
		if l != r.Opened {
			r.Drifted = true
			r.At = float64(i) / float64(len(langs))
			break
		}
	}
	return r
}

// strip removes fenced blocks and inline code, which are neither language.
func strip(text string) string {
	var b strings.Builder
	fenced := false
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for i, part := range strings.Split(line, "`") {
			if i%2 == 0 {
				b.WriteString(part)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// sentences splits on the marks a sentence ends with, plus line breaks, because
// a list is a stack of sentences with no full stops in it.
func sentences(text string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n' || r == ';' || r == ':'
	}) {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// A KindScore is one shape of prompt on its own, since drift is a property of
// the shape of an answer rather than of its subject.
type KindScore struct {
	Kind     string `json:"kind"`
	Items    int    `json:"items"`
	Adherent int    `json:"adherent"`
	Drifted  int    `json:"drifted"`
}

// A Score is the benchmark's whole output.
type Score struct {
	Set     doc.Hash `json:"set"`
	Version string   `json:"version"`

	Items int `json:"items"`

	// Adherence is the share of answers written end to end in the language the
	// item wanted.
	Adherence float64 `json:"adherence"`
	Adherent  int     `json:"adherent"`

	// InOther is answers in a language the item did not ask for, and Bare is
	// answers in Vietnamese with the tone marks left off. They are counted apart
	// because they are different failures with different causes, and a model can
	// have one without the other.
	InOther  int     `json:"in_other"`
	Bare     int     `json:"bare"`
	BareRate float64 `json:"bare_rate"`

	// Drifted is answers that opened in the right language and did not stay
	// there, and DriftedAt is how far in they turned, averaged.
	Drifted   int     `json:"drifted"`
	DriftedAt float64 `json:"drifted_at,omitempty"`

	ByKind []KindScore `json:"by_kind"`

	// Detected is how many verdicts came from the classifier rather than from a
	// person. The classifier is published and it is not right, so the count
	// travels with the score.
	Detected int `json:"detected"`

	Passed bool `json:"passed"`

	Missing   []string `json:"missing,omitempty"`
	Empty     []string `json:"empty,omitempty"`
	Strays    []string `json:"strays,omitempty"`
	Elsewhere []string `json:"elsewhere,omitempty"`
}

// Grade reads a model's replies against the set. Nothing is refused here; every
// disagreement between the replies and the set comes back as a field.
func (s Set) Grade(replies []Reply) Score {
	digest := s.Digest()
	sc := Score{Set: digest, Version: s.Version, Items: len(s.Items)}

	byID := make(map[string]Reply, len(replies))
	for _, r := range replies {
		if _, ok := s.Lookup(r.ID); !ok {
			sc.Strays = append(sc.Strays, r.ID)
			continue
		}
		if !r.Set.IsZero() && r.Set != digest {
			sc.Elsewhere = append(sc.Elsewhere, r.ID)
			continue
		}
		byID[r.ID] = r
	}

	byKind := map[string]*KindScore{}
	for _, k := range s.Kinds() {
		byKind[k] = &KindScore{Kind: k}
	}

	graded, drift := 0, 0.0
	for _, it := range s.Items {
		r, ok := byID[it.ID]
		if !ok {
			sc.Missing = append(sc.Missing, it.ID)
			continue
		}
		reading := Read(r.Text)
		if r.Wrote != "" {
			reading.Wrote = r.Wrote
			reading.Drifted = false
		} else {
			sc.Detected++
		}
		if reading.Wrote == Other {
			sc.Empty = append(sc.Empty, it.ID)
			continue
		}

		graded++
		k := byKind[it.Kind]
		k.Items++
		switch {
		case reading.Wrote == it.Wants && !reading.Drifted:
			sc.Adherent++
			k.Adherent++
		case reading.Drifted && reading.Opened == it.Wants:
			sc.Drifted++
			k.Drifted++
			drift += reading.At
		}
		if reading.Wrote != it.Wants {
			if reading.Wrote == Bare {
				sc.Bare++
			} else {
				sc.InOther++
			}
		}
	}

	if graded > 0 {
		sc.Adherence = float64(sc.Adherent) / float64(graded)
		sc.BareRate = float64(sc.Bare) / float64(graded)
	}
	if sc.Drifted > 0 {
		sc.DriftedAt = drift / float64(sc.Drifted)
	}
	for _, k := range s.Kinds() {
		sc.ByKind = append(sc.ByKind, *byKind[k])
	}
	sc.Passed = len(sc.Missing) == 0 && len(sc.Empty) == 0 &&
		sc.Adherence >= MinAdherence && sc.BareRate <= MaxBare
	return sc
}

// Publishable is every reason this score does not go out as it stands.
func (sc Score) Publishable() []string {
	var out []string
	if len(sc.Missing) > 0 {
		out = append(out, fmt.Sprintf("%s never came back, and an adherence rate over the items that returned is a rate over whatever the harness did not drop: %s",
			plural(len(sc.Missing), "item"), strings.Join(sc.Missing, ", ")))
	}
	if len(sc.Empty) > 0 {
		out = append(out, fmt.Sprintf("%s came back with nothing in it that could be read as a language at all, which is a harness fault rather than a score: %s",
			plural(len(sc.Empty), "answer"), strings.Join(sc.Empty, ", ")))
	}
	if len(sc.Strays) > 0 {
		out = append(out, fmt.Sprintf("%s: %s, which is what a prompt written after seeing a score looks like from here",
			plural(len(sc.Strays), "reply names an item the set does not hold"), strings.Join(sc.Strays, ", ")))
	}
	if len(sc.Elsewhere) > 0 {
		out = append(out, fmt.Sprintf("%s answered a different version of the set: %s, so the wording moved between asking and scoring",
			plural(len(sc.Elsewhere), "reply"), strings.Join(sc.Elsewhere, ", ")))
	}
	return out
}

// ReadSet reads a set from JSON, for checking one that is not the fixed one.
func ReadSet(path string) (Set, error) {
	f, err := os.Open(path)
	if err != nil {
		return Set{}, err
	}
	defer func() { _ = f.Close() }()

	var s Set
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return Set{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// ReadReplies reads one JSON reply per line.
func ReadReplies(path string) ([]Reply, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Reply
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Reply
		d := json.NewDecoder(strings.NewReader(line))
		d.DisallowUnknownFields()
		if err := d.Decode(&r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %w: it holds no replies", path, ErrBadReplies)
	}
	return out, nil
}
