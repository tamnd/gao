// Package route divides a pile of PDFs three ways before any of them is
// extracted, because the three ways cost very different amounts of money.
//
// A born digital PDF with a working text layer costs milliseconds. The same
// page typeset in 2003 with a one byte Vietnamese font costs the same
// milliseconds and then has to be transcoded and checked, because its text
// layer extracts as Coäng hoøa xaõ hoäi chuû nghóa Vieät Nam and every stage
// downstream will take that for Vietnamese. A scanned page costs a GPU second
// and produces text with an error rate. There is one GPU on the fleet, so the
// only number that decides what this slice costs is how much of the pile lands
// on the third route.
//
// Nothing here parses PDF properly. It is a linear scan over the objects with
// FlateDecode and object streams handled, which is enough to answer one
// question per document and cheap enough to answer it for millions of them. A
// real parser would be slower, would pull in a dependency that has to be
// trusted with hostile input, and would answer a question nobody asked. What it
// gives up is documents built in ways the scan does not follow, and those come
// back [Unroutable] with the reason rather than guessed at, because a scanned
// page routed to direct extraction is an empty document and a born digital page
// routed to OCR is a GPU second spent to make good text worse.
package route

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/tamnd/gao/normalize"
)

// A Route is what happens to the document next.
type Route int

const (
	// Unroutable is a document this stage will not guess about.
	Unroutable Route = iota
	// Text is born digital with a text layer worth reading, route T.
	Text
	// Legacy is a text layer written in a one byte Vietnamese font encoding,
	// route L, which is transcoded and validated before it is admitted.
	Legacy
	// Scan is a page image with no text on it, route O, which goes to OCR.
	Scan
)

func (r Route) String() string {
	switch r {
	case Text:
		return "text"
	case Legacy:
		return "legacy"
	case Scan:
		return "scan"
	case Unroutable:
		return "unroutable"
	default:
		return fmt.Sprintf("route(%d)", int(r))
	}
}

// Letter is the route's one character label, which is what the spec and the
// cost tables call these and what a manifest column holds.
func (r Route) Letter() string {
	switch r {
	case Text:
		return "T"
	case Legacy:
		return "L"
	case Scan:
		return "O"
	default:
		return "-"
	}
}

// MinGlyphsPerPage is how many shown characters a page has to carry for its
// text layer to be worth reading.
//
// A page of Vietnamese prose is around two thousand characters. A scanned page
// is zero, except that a lot of scans are not purely scans: they carry a
// letterhead, a page number, a watermark, or a stamp from whatever put the
// scan together, and a threshold of one would send every one of those down the
// route that produces a document holding the word "Scanned" and nothing else.
// A hundred is well above what furniture produces and an order of magnitude
// below what a page of text produces, which is the gap this number sits in.
const MinGlyphsPerPage = 100

// MaxShown is how much of the text layer the decision is taken over.
//
// The legacy detector needs a few hundred function words and this is tens of
// thousands, so the cap costs nothing in accuracy and bounds the work on the
// documents that are pathological rather than long.
const MaxShown = 1 << 16

// MaxScan is how much of a file this stage will read.
//
// A PDF is a container and there is no upper bound on what somebody puts in
// one. The routing decision is made from the first objects either way, and a
// document larger than this is answered from its first sixteen megabytes with
// the truncation recorded rather than by reading a gigabyte to reach the same
// answer.
const MaxScan = 16 << 20

// A Reading is what the scan found and what it decided.
type Reading struct {
	// Route is the decision.
	Route Route
	// Why is the one sentence that explains it, written for somebody looking at
	// a routing distribution and asking why a document is where it is.
	Why string

	// Pages is how many pages the scan found.
	Pages int
	// Glyphs is how many characters the text layer shows across the document.
	Glyphs int
	// ImageShare is the fraction of the document's stream bytes that are image
	// data.
	//
	// It is bytes rather than page area on purpose. Working out how much of a
	// page an image covers means tracking the current transformation matrix
	// through the content stream, which is most of a renderer. Bytes need no
	// interpretation and separate the cases just as well, because a scan is
	// almost entirely image and a born digital page is almost entirely not.
	ImageShare float64
	// Charset is the legacy encoding the text layer is in, and is empty when it
	// is not in one.
	Charset string
	// Fonts is every base font the document names, sorted and deduplicated. The
	// legacy encodings travel with font names like VNI-Times and .VnTime, so
	// this is corroboration for a route L decision rather than the basis of it.
	Fonts []string
	// Truncated records that the file was longer than [MaxScan].
	Truncated bool

	// shown is the text layer sample the decision was taken over.
	shown string
}

// Shown is the text the document's own text layer produced, up to [MaxShown].
// It is what the legacy detector read and what a person checking a routing
// decision should look at first.
func (r Reading) Shown() string { return r.shown }

// GlyphsPerPage is the density the route O decision is taken on.
func (r Reading) GlyphsPerPage() float64 {
	if r.Pages == 0 {
		return 0
	}
	return float64(r.Glyphs) / float64(r.Pages)
}

// Read routes one document.
func Read(b []byte) Reading {
	if len(b) > MaxScan {
		b = b[:MaxScan]
	}
	if !bytes.HasPrefix(bytes.TrimLeft(b, "\x00\r\n \t"), []byte("%PDF-")) {
		return Reading{Why: "the file does not start with a PDF header, so it is not a PDF or it is one with something in front of it"}
	}

	r := Reading{Truncated: len(b) == MaxScan}
	objs := scan(b)

	// Encryption is checked before anything else is believed. An encrypted
	// document's streams decompress to nothing useful and its text layer looks
	// exactly like an absent one, which would route a perfectly good page to
	// OCR and produce a bill rather than an error.
	for _, o := range objs {
		if bytes.Contains(o.dict, []byte("/Encrypt")) {
			r.Why = "the document is encrypted, and its text layer cannot be read without the key"
			return r
		}
	}
	if bytes.Contains(trailer(b), []byte("/Encrypt")) {
		r.Why = "the trailer names an encryption dictionary, and the text layer cannot be read without the key"
		return r
	}

	var shown strings.Builder
	var streamBytes, imageBytes int
	fonts := map[string]bool{}
	for _, o := range expand(objs) {
		switch {
		case bytes.Contains(o.dict, []byte("/Type/Page")), bytes.Contains(o.dict, []byte("/Type /Page")):
			if !bytes.Contains(o.dict, []byte("/Type/Pages")) && !bytes.Contains(o.dict, []byte("/Type /Pages")) {
				r.Pages++
			}
		}
		for _, f := range baseFonts(o.dict) {
			fonts[f] = true
		}
		if len(o.stream) == 0 {
			continue
		}
		streamBytes += len(o.stream)
		if isImage(o.dict) {
			imageBytes += len(o.stream)
			continue
		}
		if shown.Len() < MaxShown {
			shown.WriteString(showText(inflate(o.dict, o.stream)))
		}
	}

	r.shown = shown.String()
	if len(r.shown) > MaxShown {
		r.shown = r.shown[:MaxShown]
	}
	r.Glyphs = len([]rune(r.shown))
	r.Fonts = sortedKeys(fonts)
	if streamBytes > 0 {
		r.ImageShare = float64(imageBytes) / float64(streamBytes)
	}
	if r.Pages == 0 {
		r.Pages = pageCount(objs)
	}

	return decide(r)
}

// decide turns the measurements into a route. It is separate from the scan so
// that the rule can be read without reading the parser, since the rule is the
// part anybody looking at a routing distribution will want to argue with.
func decide(r Reading) Reading {
	switch {
	case r.Pages == 0:
		r.Why = "the scan found no pages, so there is nothing to say about this document that would survive being acted on"
		return r
	case r.GlyphsPerPage() < MinGlyphsPerPage:
		r.Route = Scan
		r.Why = fmt.Sprintf("the text layer shows %.0f characters a page against a floor of %d, so there is a page image here and no text on it", r.GlyphsPerPage(), MinGlyphsPerPage)
		return r
	}
	if c := normalize.Detect(r.shown); c != nil {
		r.Route, r.Charset = Legacy, c.Name()
		r.Why = fmt.Sprintf("the text layer extracts as %s rather than as Vietnamese, so it is transcoded and validated before anything downstream reads it", c.Name())
		return r
	}
	r.Route = Text
	r.Why = fmt.Sprintf("the text layer shows %.0f characters a page and reads as Vietnamese already, so it is extracted directly", r.GlyphsPerPage())
	return r
}

// an object is one indirect object, its dictionary and its stream if it has one.
type object struct {
	dict   []byte
	stream []byte
}

var objStart = regexp.MustCompile(`(?s)\d+\s+\d+\s+obj`)

// scan walks the file for indirect objects.
//
// It does not read the cross reference table. A table that disagrees with the
// file is common enough that every real reader has a repair path for it, and
// the repair path is this: find the objects where they actually are.
func scan(b []byte) []object {
	locs := objStart.FindAllIndex(b, -1)
	out := make([]object, 0, len(locs))
	for _, loc := range locs {
		body := b[loc[1]:]
		if end := bytes.Index(body, []byte("endobj")); end >= 0 {
			body = body[:end]
		}
		o := object{dict: body}
		if i := bytes.Index(body, []byte("stream")); i >= 0 {
			o.dict = body[:i]
			s := body[i+len("stream"):]
			s = bytes.TrimLeft(s, "\r")
			s = bytes.TrimPrefix(s, []byte("\n"))
			if j := bytes.Index(s, []byte("endstream")); j >= 0 {
				s = s[:j]
			}
			o.stream = bytes.TrimRight(s, "\r\n")
		}
		out = append(out, o)
	}
	return out
}

// expand adds the objects that live inside object streams.
//
// Anything written this century puts the page tree and the font dictionaries in
// a compressed object stream, so a scanner that stops at the top level finds no
// pages and no fonts in a completely ordinary document and reports it as
// unroutable. That failure is silent in the sense that matters: the document
// looks broken rather than the scanner looking incomplete.
func expand(objs []object) []object {
	out := slices.Clone(objs)
	for _, o := range objs {
		if len(o.stream) == 0 || !bytes.Contains(o.dict, []byte("/ObjStm")) {
			continue
		}
		body := inflate(o.dict, o.stream)
		first := intAfter(o.dict, "/First")
		if first <= 0 || first > len(body) {
			continue
		}
		// Everything past /First is the objects themselves, run together. They
		// are split on the offsets in the header rather than re-scanned, since
		// there is no "obj" keyword in here to scan for.
		head := strings.Fields(string(body[:first]))
		var offsets []int
		for i := 1; i < len(head); i += 2 {
			if n, err := strconv.Atoi(head[i]); err == nil {
				offsets = append(offsets, n)
			}
		}
		for i, off := range offsets {
			end := len(body) - first
			if i+1 < len(offsets) {
				end = offsets[i+1]
			}
			if off < 0 || end > len(body)-first || off >= end {
				continue
			}
			out = append(out, object{dict: body[first+off : first+end]})
		}
	}
	return out
}

// inflate decompresses a stream when the dictionary says it is compressed.
//
// Both zlib and raw deflate are tried, because the specification says zlib and
// a noticeable share of real files leave the two byte header off. A stream that
// decompresses under neither is returned as it arrived, which for an
// unsupported filter means the text extractor finds no operators in it and the
// document routes on the rest of its pages.
func inflate(dict, stream []byte) []byte {
	if !bytes.Contains(dict, []byte("/FlateDecode")) {
		return stream
	}
	if zr, err := zlib.NewReader(bytes.NewReader(stream)); err == nil {
		if out, err := io.ReadAll(zr); err == nil || len(out) > 0 {
			_ = zr.Close()
			return out
		}
		_ = zr.Close()
	}
	fr := flate.NewReader(bytes.NewReader(stream))
	defer func() { _ = fr.Close() }()
	if out, err := io.ReadAll(fr); err == nil || len(out) > 0 {
		return out
	}
	return nil
}

var (
	// The four text showing operators, with their argument in front of them.
	// Tj and ' take one string, " takes two numbers and a string, and TJ takes
	// an array of strings and kerning numbers.
	showOps = regexp.MustCompile(`(?s)(\[[^\]]*\]|\([^)]*\)|<[0-9A-Fa-f\s]*>)\s*(TJ|Tj|'|")`)
	pieces  = regexp.MustCompile(`(?s)\([^)]*\)|<[0-9A-Fa-f\s]*>`)
	baseRe  = regexp.MustCompile(`/BaseFont\s*/([^\s/\[\]<>()]+)`)
)

// showText pulls the shown bytes out of a content stream.
//
// The bytes are taken as they are rather than mapped through the font's
// encoding, which is the whole point: a document in a legacy font encoding is
// one whose shown bytes are Vietnamese under a one byte table and mojibake
// under Unicode, and mapping them through anything first would erase the
// evidence the decision is made on.
func showText(content []byte) string {
	var b strings.Builder
	for _, m := range showOps.FindAllSubmatch(content, -1) {
		for _, p := range pieces.FindAll(m[1], -1) {
			switch p[0] {
			case '(':
				b.WriteString(unescape(string(p[1 : len(p)-1])))
			case '<':
				b.Write(unhex(p[1 : len(p)-1]))
			}
		}
		b.WriteByte(' ')
	}
	return b.String()
}

// unescape resolves the backslash escapes a PDF literal string may hold.
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch c := s[i]; c {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case '\n':
			// A backslash at end of line continues the string and shows nothing.
		case '0', '1', '2', '3', '4', '5', '6', '7':
			n, digits := 0, 0
			for ; digits < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7'; digits++ {
				n = n*8 + int(s[i]-'0')
				i++
			}
			i--
			b.WriteByte(byte(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// unhex reads a hex string, which is what a document uses when its shown bytes
// would otherwise need escaping on every second character.
func unhex(s []byte) []byte {
	var digits []byte
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			digits = append(digits, c)
		}
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0') // the specification pads the last one itself
	}
	out := make([]byte, 0, len(digits)/2)
	for i := 0; i < len(digits); i += 2 {
		n, err := strconv.ParseUint(string(digits[i:i+2]), 16, 8)
		if err != nil {
			continue
		}
		out = append(out, byte(n))
	}
	return out
}

func isImage(dict []byte) bool {
	return bytes.Contains(dict, []byte("/Subtype/Image")) || bytes.Contains(dict, []byte("/Subtype /Image"))
}

func baseFonts(dict []byte) []string {
	matches := baseRe.FindAllSubmatch(dict, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		name := string(m[1])
		// A subset font carries a six letter tag and a plus in front of its real
		// name, and reporting ABCDEF+VNI-Times as a distinct font from
		// VNI-Times would turn one font into as many fonts as there are files.
		if i := strings.IndexByte(name, '+'); i == 6 {
			name = name[i+1:]
		}
		out = append(out, name)
	}
	return out
}

// pageCount falls back to the page tree's own count when no page object was
// found, which happens on a document whose page objects are somewhere the scan
// does not reach.
func pageCount(objs []object) int {
	for _, o := range objs {
		if bytes.Contains(o.dict, []byte("/Type/Pages")) || bytes.Contains(o.dict, []byte("/Type /Pages")) {
			if n := intAfter(o.dict, "/Count"); n > 0 {
				return n
			}
		}
	}
	return 0
}

// intAfter reads the integer that follows a key in a dictionary.
func intAfter(dict []byte, key string) int {
	i := bytes.Index(dict, []byte(key))
	if i < 0 {
		return 0
	}
	rest := strings.TrimLeft(string(dict[i+len(key):]), " \r\n\t")
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return n
}

// trailer is the tail of the file, where the trailer dictionary lives on a
// document that has one.
func trailer(b []byte) []byte {
	const tail = 2048
	if len(b) > tail {
		return b[len(b)-tail:]
	}
	return b
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
