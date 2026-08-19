package frontier

// Recognizing when a thousand links are one page wearing different numbers.
//
// A crawl budget is spent per URL and earned per host, and between those two
// there has to be something that says a hundred thousand URLs off one forum are
// a hundred thousand pages while a hundred thousand URLs off one calendar are
// one page and a date field. The shape is that something. It is the URL with its
// varying parts replaced by what kind of thing they were, so that URLs generated
// from one template collapse onto one string and can be counted.
//
// This is the measure the whole budget rests on, which is why it is a separate
// file with its own tests. Get it too coarse and every article on a news site
// looks like one page. Get it too fine and a calendar with a date in the path
// gets a fresh budget for every day of every year forever.

import (
	"strconv"
	"strings"
	"time"
)

// A Shape is a URL with its values replaced by their kind.
//
// Two URLs with the same shape were produced by the same piece of a site: the
// same template, the same handler, the same query form. That is what a budget
// counts and what a trap is detected in.
type Shape struct {
	// Host is the host, unchanged. A shape never crosses hosts, because two
	// sites running the same forum software are two sites.
	Host string

	// Path is the path with each varying segment replaced.
	Path string

	// Keys is the query parameter names, sorted, with the values gone. The
	// names are the form and the values are what was typed into it.
	Keys []string
}

// The placeholders. They are spelled with angle brackets because a path segment
// cannot contain one without escaping it, so a shape can never be mistaken for a
// URL that happened to look like one.
const (
	// KindNumber is a segment that is only digits: a page number, an article
	// id, a year, a thread id.
	KindNumber = "<n>"

	// KindDate is a segment that is a date, in any of the spellings sites use.
	// Told apart from a number because a date is the shape of a trap and a
	// number is the shape of an article.
	KindDate = "<date>"

	// KindID is a segment that is a long opaque token: a hash, a uuid, a
	// session, a slug that is only hex.
	KindID = "<id>"
)

// Of returns the shape of a canonical URL.
//
// It takes the string rather than a parsed URL so that the shape is always taken
// of the canonical form. Two URLs that canonicalize together must shape
// together, and the way to be sure of that is to have one entry point.
func Of(canonical string) (Shape, error) {
	u, err := Parse(canonical)
	if err != nil {
		return Shape{}, err
	}

	s := Shape{Host: u.Host}

	var out []string
	for seg := range strings.SplitSeq(strings.Trim(u.Path, "/"), "/") {
		if seg == "" {
			continue
		}
		out = append(out, kindOf(seg))
	}
	s.Path = "/" + strings.Join(out, "/")
	if strings.HasSuffix(u.Path, "/") && len(out) > 0 {
		s.Path += "/"
	}

	if u.RawQuery != "" {
		// The query is already sorted by canonQuery, so the keys come out
		// sorted without sorting them again.
		for pair := range strings.SplitSeq(u.RawQuery, "&") {
			name, _, _ := strings.Cut(pair, "=")
			if name != "" {
				s.Keys = append(s.Keys, name)
			}
		}
	}
	return s, nil
}

// String is the shape as one comparable string, which is the form a counter
// keys on.
func (s Shape) String() string {
	out := s.Host + s.Path
	if len(s.Keys) > 0 {
		out += "?" + strings.Join(s.Keys, "&")
	}
	return out
}

// Dated reports whether any part of this shape is a date.
//
// This is the question a trap detector asks first. A date in a URL is not by
// itself a problem, since news sites put the publication date in the path of
// every article they have. A date is a problem when the pages behind it are
// empty, and it is the pair of those two facts that names a calendar.
func (s Shape) Dated() bool {
	if strings.Contains(s.Path, KindDate) {
		return true
	}
	for _, k := range s.Keys {
		if dateKeys[strings.ToLower(k)] {
			return true
		}
	}
	return false
}

// dateKeys are the query parameter names that carry a date or move one.
//
// Both spellings are here because half the Vietnamese web runs software written
// in Vietnamese and half runs WordPress. `thang` is month and `nam` is year, and
// they appear in the query of every locally written event calendar.
var dateKeys = map[string]bool{
	"date": true, "day": true, "month": true, "year": true, "week": true,
	"from": true, "to": true, "start": true, "end": true, "until": true, "since": true,
	"mon": true, "yr": true, "m": true, "y": true, "d": true,
	"ngay": true, "thang": true, "nam": true, "tu-ngay": true, "den-ngay": true,
	"tuan": true, "lich": true, "thoi-gian": true,
}

// kindOf says what a single path segment is.
//
// The order matters. A date is checked before a number, because 20240315 is both
// and the date reading is the one that names the trap. An id is checked last,
// because the test for it is the loosest and would otherwise swallow segments
// that are really slugs.
func kindOf(seg string) string {
	switch {
	case isDate(seg):
		return KindDate
	case isNumber(seg):
		return KindNumber
	case isOpaque(seg):
		return KindID
	default:
		// A slug stays as it was written. It is the part of a URL that names
		// the page rather than selecting it, so two articles with different
		// slugs are two pages and have to shape apart.
		return seg
	}
}

func isNumber(seg string) bool {
	if seg == "" || len(seg) > 18 {
		return false
	}
	for i := 0; i < len(seg); i++ {
		if seg[i] < '0' || seg[i] > '9' {
			return false
		}
	}
	return true
}

// dateLayouts are the spellings a date takes in a path segment.
//
// The last two are the ones a Vietnamese site is most likely to use, since the
// day comes first in Vietnamese as it does in most of the world, and a crawler
// written against American layouts reads 03-06-2024 as the third of June and
// then does not recognize 15-03-2024 at all.
var dateLayouts = []string{
	"2006-01-02", "2006_01_02", "20060102",
	"02-01-2006", "02_01_2006", "02012006",
}

func isDate(seg string) bool {
	// A year on its own is a number, not a date. Every article id in a certain
	// range would otherwise read as a year, and the segment that says 2019 in
	// /tin-tuc/2019/ is doing the same job as the one that says 47.
	if len(seg) < 8 {
		return false
	}
	for _, layout := range dateLayouts {
		if len(seg) != len(layout) {
			continue
		}
		if t, err := time.Parse(layout, seg); err == nil && plausibleYear(t) {
			return true
		}
	}
	return false
}

// plausibleYear keeps the parse honest. time.Parse will read 99991231 as a date
// in the year 9999, and a URL with that in it is a template that was never
// filled in rather than a page about the far future.
func plausibleYear(t time.Time) bool {
	y := t.Year()
	return y >= 1990 && y <= 2100
}

// isOpaque is the test for a segment that carries no meaning a person put there.
//
// Long and hexadecimal, or shaped like a uuid. Both are machine generated
// identifiers, and a budget that counted them apart would count one page per
// session for as long as the session ids kept coming.
func isOpaque(seg string) bool {
	if isUUID(seg) {
		return true
	}
	if len(seg) < 24 {
		return false
	}
	digits := 0
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	// All letters and no digits at this length is a word in a language whose
	// alphabet happens to overlap hex. `deadbeefcafedecadedbadfaded` is not an
	// identifier, and neither is a Vietnamese slug with the marks stripped.
	return digits > 0
}

func isUUID(seg string) bool {
	if len(seg) != 36 {
		return false
	}
	for i, c := range []byte(seg) {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

// Depth is how many segments deep a URL is, which is the cheapest signal there
// is that a crawl has walked into something that generates itself.
//
// It is not a trap detector on its own. Vietnamese forums nest genuinely deep,
// with a board inside a board inside a thread, and a limit tight enough to stop
// a filter loop would cut the content this crawl exists to find. It is one input
// to a decision made in budget.go.
func (s Shape) Depth() int {
	if s.Path == "/" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s.Path, "/"), "/")
}

// Repeats reports the longest run of one repeated segment in the path, which is
// the signature of a relative link resolved against itself.
//
// A server that serves /a/b/ and answers a relative link to b/ from it produces
// /a/b/b/, and then /a/b/b/b/, and it never stops. Every one of those is a
// different URL, a different shape, and the same page. Counting depth alone will
// not catch it until the crawl is already thousands of pages in, and the run of
// one segment names it on the third.
func (s Shape) Repeats() int {
	segs := strings.Split(strings.Trim(s.Path, "/"), "/")
	most, run := 0, 0
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		if i > 0 && seg == segs[i-1] {
			run++
		} else {
			run = 1
		}
		if run > most {
			most = run
		}
	}
	return most
}

// Describe says what a shape is in a line, for the reports a person reads when
// they are asking why a host stopped being crawled.
func (s Shape) Describe() string {
	var notes []string
	if s.Dated() {
		notes = append(notes, "carries a date")
	}
	if n := s.Repeats(); n > 2 {
		notes = append(notes, "repeats one segment "+strconv.Itoa(n)+" times")
	}
	if n := s.Depth(); n > 6 {
		notes = append(notes, strconv.Itoa(n)+" segments deep")
	}
	if len(notes) == 0 {
		return s.String()
	}
	return s.String() + " (" + strings.Join(notes, ", ") + ")"
}
