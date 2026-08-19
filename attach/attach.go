// Package attach keeps a page image attached to the text that came off it.
//
// Đính is to attach. The milestone item reads like a freebie: page images
// retained aligned with extracted text, since the vision training data is a
// by-product of work this slice does anyway. The by-product half is true. A
// scanned PDF has to be rendered to an image before OCR can read it, so the
// image exists whether or not anybody keeps it, and throwing it away and
// rendering it again later costs the same GPU hours a second time.
//
// The aligned half is the part that is not free. A page image is worth
// something only as a pair: this picture, this text. A pair that is off by one
// page is worse than no pair at all, because it is training data that teaches
// the wrong association, it looks exactly like a correct pair from the outside,
// and nothing downstream can find it. A corpus with two percent of its pairs
// shifted does not fail loudly. It produces a model that reads pages slightly
// wrong forever.
//
// So the join is checked here rather than assumed. The key is the document and
// the page number inside it, both sides carry it, and a document whose pages
// arrive as 1, 2 and 4 is reported rather than renumbered, since renumbering is
// the operation that turns a missing page into a silently shifted set. Ink is
// carried for the same reason: a page with no marks on it that produced four
// thousand characters of text is a pair nobody should keep, and the arithmetic
// that catches it is a comparison rather than a model.
//
// The other half of the item is disk. Rendering is what makes this slice
// expensive to store rather than to run: gamingpc has 307 GB free and a page at
// 300 dpi is most of a megabyte, so a million pages is more than the box holds
// and the run does not fit on the machine that produces it. The images stream
// to the store and the box keeps a window. This package reports what is still
// resident and refuses a batch that leaves too much of itself behind. Whether
// the drain keeps up with the write is a rate, and rates are what gao clear
// measures. What is asked here is the smaller question that has to be true
// first, which is whether anything is being left behind at all.
package attach

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

// The three routes a document takes through S4, named the way P04 names them.
// A page image is a by-product on the OCR route and a render on the other two,
// which is why the report keeps them apart rather than averaging a cost over
// them.
const (
	Direct = "T" // born digital, a usable text layer, no render needed to read it
	Legacy = "L" // a legacy font encoding, transcoded, the render kept as evidence
	OCR    = "O" // scanned, rendered, read by an engine
)

// MinDPI is the resolution below which a page image is not training data. 200
// is where Vietnamese diacritics stop surviving a downscale: the marks sit above
// and below letters that are already small, and a tone mark that has become two
// pixels is a mark a model has to guess at the same way the OCR engine did.
const MinDPI = 200

// MinInk is the share of a page that has to be marked for the page to be
// carrying anything. Below it the page is blank, which is a normal thing for a
// page to be and not a failure.
const MinInk = 0.004

// MinChars is what a page with ink on it has to have produced. A page carrying
// marks that came back with a dozen characters is a page the extraction lost,
// whichever route it took.
const MinChars = 40

// MinAttached is the share of pages that have to carry both halves of the pair.
// It is high on purpose. The pairs are the deliverable and a batch that is 90%
// paired is a batch somebody has to sort before it is one.
const MinAttached = 0.99

// MaxLost is how much of a batch the extraction may drop before the batch is a
// report about the extraction rather than a set of pages.
const MaxLost = 0.02

// MaxResident is what may sit on the box that made it. gamingpc has 307 GB free
// and this is two thirds of it, which leaves room for the working set of the
// engine that is still running while the last batch uploads.
const MaxResident = 200_000_000_000

// Page is one rendered page and the text that came off it.
type Page struct {
	// Document and Page are the join. Both halves of the pair carry them and
	// neither half is identified by anything else, since a path is a thing
	// somebody renames.
	Document string `json:"document"`
	Page     int    `json:"page"`

	Route string `json:"route"`

	// Image is where the render lives in the store, and Hash is what it hashed
	// to, so a pair is checked rather than trusted.
	Image string `json:"image"`
	Bytes int64  `json:"bytes"`
	Hash  string `json:"hash"`
	DPI   int    `json:"dpi"`

	// Chars is the text this page produced and Ink is the share of the render
	// that is not background. The two together are what says the pair belongs
	// together.
	Chars int     `json:"chars"`
	Ink   float64 `json:"ink"`

	// Stored says the image reached the store, which is what lets the copy on
	// the box go.
	Stored bool `json:"stored"`
}

// Blank reports whether the page has enough on it to have said anything.
func (p Page) Blank() bool { return p.Ink < MinInk }

// Lost reports a page that carries marks and produced no text, which is the
// extraction failing on this page rather than the page being empty.
func (p Page) Lost() bool { return !p.Blank() && p.Chars < MinChars }

// Rendered reports whether an image of this page exists. A born digital page is
// read out of its text layer and never rendered, so it has none, and that is a
// fact about the route rather than a fault. The by-product argument only covers
// the pages something had to render in order to read.
func (p Page) Rendered() bool { return p.Image != "" }

// Paired reports whether the page is training data: an image that can be
// checked, and text that came off that image.
func (p Page) Paired() bool {
	return p.Rendered() && p.Hash != "" && p.DPI >= MinDPI && !p.Lost()
}

// Blocking is every reason this page is not a pair anybody should keep, written
// as sentences because the reader is deciding whether to rerun a document.
func (p Page) Blocking() []string {
	var why []string
	where := fmt.Sprintf("%s page %d", p.Document, p.Page)
	if p.Document == "" {
		where = fmt.Sprintf("a page numbered %d", p.Page)
		why = append(why, "a page that does not say which document it came off cannot be joined to anything")
	}
	if p.Page < 1 {
		why = append(why, fmt.Sprintf("%s is numbered from zero or below, and the page number is half the join", where))
	}
	if !slices.Contains([]string{Direct, Legacy, OCR}, p.Route) {
		why = append(why, fmt.Sprintf("%s took route %q, which is not one of the three P04 routes", where, p.Route))
	}
	if p.Image != "" && p.Hash == "" {
		why = append(why, fmt.Sprintf("%s carries an image nobody hashed, so the pair cannot be checked after it moves", where))
	}
	if p.Image != "" && p.Bytes <= 0 {
		why = append(why, fmt.Sprintf("%s has an image path and no bytes behind it", where))
	}
	// A scanned page is unreadable without a render, so one that has no image is
	// a page whose text came from somewhere nobody can point at.
	if p.Route == OCR && !p.Rendered() {
		why = append(why, fmt.Sprintf("%s took the OCR route and carries no image, so nothing can be checked against what was read off it", where))
	}
	if p.Image != "" && p.DPI < MinDPI {
		why = append(why, fmt.Sprintf("%s was rendered at %d dpi, under the %d where Vietnamese tone marks survive", where, p.DPI, MinDPI))
	}
	// The pair being wrong is the failure this package exists for, so it is a
	// refusal rather than a number in a table.
	if p.Blank() && p.Chars >= MinChars {
		why = append(why, fmt.Sprintf(
			"%s has %.1f%% ink on it and %d characters of text, so the text came off some other page", where, p.Ink*100, p.Chars))
	}
	return why
}

// Route is what one route cost and what it left behind.
type Route struct {
	Key      string  `json:"key"`
	Pages    int     `json:"pages"`
	Share    float64 `json:"share"`
	Rendered int     `json:"rendered"`
	Paired   int     `json:"paired"`
	Lost     int     `json:"lost"`
	Bytes    int64   `json:"bytes"`
	Chars    int64   `json:"chars"`
}

// Batch is a run of pages, which is what comes off one pass over a set of
// documents on one box.
type Batch struct {
	Name  string
	Pages []Page
}

// Documents names what the batch covers, in order, since the report is read
// next to a list of documents somebody queued.
func (b Batch) Documents() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range b.Pages {
		if p.Document != "" && !seen[p.Document] {
			seen[p.Document] = true
			out = append(out, p.Document)
		}
	}
	sort.Strings(out)
	return out
}

// Gaps names every document whose page numbers are not 1 to n exactly once.
// A gap is reported rather than closed: closing it renumbers the pages after
// it, and a set of pairs shifted by one page is the failure that has no other
// symptom.
func (b Batch) Gaps() []string {
	byDoc := map[string][]int{}
	for _, p := range b.Pages {
		if p.Document != "" {
			byDoc[p.Document] = append(byDoc[p.Document], p.Page)
		}
	}
	var out []string
	for _, doc := range b.Documents() {
		pages := byDoc[doc]
		sort.Ints(pages)
		var missing, twice []int
		for i, n := range pages {
			if i > 0 && n == pages[i-1] {
				twice = append(twice, n)
			}
		}
		for n := 1; n <= pages[len(pages)-1]; n++ {
			if !slices.Contains(pages, n) {
				missing = append(missing, n)
			}
		}
		switch {
		case len(twice) > 0:
			out = append(out, fmt.Sprintf("%s carries %s twice", doc, numbers(twice, "page")))
		case len(missing) > 0:
			out = append(out, fmt.Sprintf("%s runs to page %d and is missing %s", doc, pages[len(pages)-1], numbers(missing, "page")))
		}
	}
	return out
}

// Paired counts the pages that are training data.
func (b Batch) Paired() int { return b.countIf(Page.Paired) }

// Rendered counts the pages an image exists for, which is the denominator the
// attachment share is measured against. Born digital pages are read without one
// and counting them as unattached would make the number a report about the
// routing rather than about the pairing.
func (b Batch) Rendered() int { return b.countIf(Page.Rendered) }

// Lost counts the pages that carry marks and produced no text.
func (b Batch) Lost() int { return b.countIf(Page.Lost) }

// Blank counts the pages that are empty, which is a fact about the documents
// rather than about the pipeline.
func (b Batch) Blank() int { return b.countIf(Page.Blank) }

// Attached is the share of the rendered pages that are usable pairs.
func (b Batch) Attached() float64 { return divide(b.Paired(), b.Rendered()) }

// Dropped is the share of the batch the extraction lost.
func (b Batch) Dropped() float64 { return divide(b.Lost(), len(b.Pages)) }

// Bytes is what the renders weigh in total.
func (b Batch) Bytes() int64 { return b.sum(func(p Page) int64 { return p.Bytes }) }

// Resident is what is still on the box that made it.
func (b Batch) Resident() int64 {
	return b.sum(func(p Page) int64 {
		if p.Stored {
			return 0
		}
		return p.Bytes
	})
}

// Stored is what reached the store and can be deleted locally.
func (b Batch) Stored() int64 { return b.Bytes() - b.Resident() }

// Free is the ceiling on what may stay behind, which is the smaller of the
// project's window and what the box actually has.
func Free(free int64) int64 {
	if free > 0 && free < MaxResident {
		return free
	}
	return MaxResident
}

// Fits reports whether what the batch left on the box is inside the window,
// given what the box has free.
func (b Batch) Fits(free int64) bool { return b.Resident() <= Free(free) }

// Routes is the batch broken out by route, which is where the cost of the slice
// is, since a born digital page needs no render and a scanned one needs one per
// page.
func (b Batch) Routes() []Route {
	out := make([]Route, 0, 3)
	for _, key := range []string{Direct, Legacy, OCR} {
		r := Route{Key: key}
		for _, p := range b.Pages {
			if p.Route != key {
				continue
			}
			r.Pages++
			r.Bytes += p.Bytes
			r.Chars += int64(p.Chars)
			if p.Rendered() {
				r.Rendered++
			}
			if p.Paired() {
				r.Paired++
			}
			if p.Lost() {
				r.Lost++
			}
		}
		r.Share = divide(r.Pages, len(b.Pages))
		out = append(out, r)
	}
	return out
}

// Blocking is every reason this batch is not a set of pairs. Page level faults
// are reported by document rather than one line each, since a document that
// rendered badly renders badly on every page and a hundred identical lines are
// a way of hiding the second problem.
func (b Batch) Blocking() []string {
	var why []string
	if len(b.Pages) == 0 {
		return []string{"no pages were read, so there is nothing to attach anything to"}
	}
	if b.Rendered() == 0 {
		why = append(why, "no page in this batch was rendered, so there are no pairs here to keep")
	}
	seen := map[string]bool{}
	for _, p := range b.Pages {
		for _, w := range p.Blocking() {
			if !seen[w] {
				seen[w] = true
				why = append(why, w)
			}
		}
	}
	if len(why) > 8 {
		rest := len(why) - 8
		why = append(why[:8:8], fmt.Sprintf("and %s like the ones above", plural(rest, "page")))
	}
	why = append(why, b.Gaps()...)
	return why
}

// Settled reports whether every page in the batch is joined to its document.
func (b Batch) Settled() bool { return len(b.Blocking()) == 0 }

// Holds reports whether the batch is what the milestone item asks for: pairs
// that are aligned, an extraction that did not lose the page, and a box that is
// not filling up.
func (b Batch) Holds(free int64) bool {
	return b.Settled() && b.Attached() >= MinAttached && b.Dropped() <= MaxLost && b.Fits(free)
}

// Verdict is the batch in one sentence.
func (b Batch) Verdict(free int64) string {
	if why := b.Blocking(); len(why) > 0 {
		return why[0]
	}
	head := fmt.Sprintf(
		"%s pairs %s of the %s something had to render, out of %s across %s, and the pairs are what the vision work later reads rather than the pages.",
		b.Name, count(b.Paired(), "page"), count(b.Rendered(), "page"),
		count(len(b.Pages), "page"), count(len(b.Documents()), "document"))
	switch {
	// The lost pages line comes first because it is the diagnosis and the
	// attachment line is the symptom, and a page the extraction lost is not
	// attached for exactly that reason.
	case b.Dropped() > MaxLost:
		return head + fmt.Sprintf(
			" The extraction lost %s of it against a %.0f%% line, which makes this a report about the extraction rather than a batch.",
			percent(b.Dropped()), MaxLost*100)
	case b.Attached() < MinAttached:
		return head + fmt.Sprintf(
			" That is under the %.0f%% line, so the batch is a set of pages somebody has to sort before it is a set of pairs.", MinAttached*100)
	case !b.Fits(free):
		return head + fmt.Sprintf(
			" It left %s on the box against the %s window, so the next batch runs into a disk that is full rather than a queue that is long.",
			bytes(b.Resident()), bytes(Free(free)))
	}
	return head + fmt.Sprintf(
		" %s of %s reached the store and %s is still on the box, which is inside the %s window.",
		bytes(b.Stored()), bytes(b.Bytes()), bytes(b.Resident()), bytes(Free(free)))
}

func (b Batch) countIf(want func(Page) bool) int {
	n := 0
	for _, p := range b.Pages {
		if want(p) {
			n++
		}
	}
	return n
}

func (b Batch) sum(of func(Page) int64) int64 {
	var n int64
	for _, p := range b.Pages {
		n += of(p)
	}
	return n
}

// ReadBatch loads a batch from a file of one JSON page per line.
func ReadBatch(name, path string) (Batch, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Batch{}, fmt.Errorf("dinh: %w", err)
	}
	b := Batch{Name: name}
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var p Page
		if err := dec.Decode(&p); err != nil {
			return Batch{}, fmt.Errorf("dinh: %s line %d: %w", path, i+1, err)
		}
		b.Pages = append(b.Pages, p)
	}
	if len(b.Pages) == 0 {
		return Batch{}, fmt.Errorf("dinh: %s holds no pages", path)
	}
	return b, nil
}

func divide(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%s %ss", thousands(int64(n)), noun)
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func percent(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

// numbers writes a short list the way somebody says it, since "missing pages 3
// and 7" reads and "missing [3 7]" does not.
func numbers(ns []int, noun string) string {
	s := make([]string, 0, len(ns))
	for _, n := range ns {
		s = append(s, fmt.Sprintf("%d", n))
	}
	if len(s) > 6 {
		s = append(s[:6:6], "and more")
	}
	switch len(s) {
	case 1:
		return fmt.Sprintf("%s %s", noun, s[0])
	case 2:
		return fmt.Sprintf("%ss %s and %s", noun, s[0], s[1])
	}
	return fmt.Sprintf("%ss %s and %s", noun, strings.Join(s[:len(s)-1], ", "), s[len(s)-1])
}

// bytes writes a size at the unit somebody would say it in, in the units the
// machine reports free space in rather than the ones a disk is sold in.
func bytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d bytes", n)
}

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
