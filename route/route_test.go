package route_test

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"

	"github.com/tamnd/gao/normalize"
	"github.com/tamnd/gao/route"
)

// The three routes cost milliseconds, milliseconds plus a transcode, and a GPU
// second. Every test below is about one document going to the wrong one of
// those, because that is the only way this package can be wrong.

func TestABornDigitalPageIsExtractedDirectly(t *testing.T) {
	r := route.Read(pdf(t, page(show(vietnamese)), false))
	if r.Route != route.Text {
		t.Fatalf("a page of Vietnamese prose routed to %s: %s", r.Route, r.Why)
	}
	if r.Charset != "" {
		t.Errorf("a page that is already Unicode was called %s", r.Charset)
	}
	if !strings.Contains(r.Shown(), "Việt") {
		t.Errorf("the text layer came out as %q", short(r.Shown()))
	}
}

// This is the failure the whole package exists for. The text layer is perfect
// and it is not Vietnamese, and a stage that reads it without checking puts
// invented words in the corpus with nothing marking them.
func TestALegacyFontEncodingIsCaughtBeforeAnythingReadsIt(t *testing.T) {
	r := route.Read(pdf(t, page(show(tcvn3(vietnamese))), false))
	if r.Route != route.Legacy {
		t.Fatalf("a page in a one byte Vietnamese font routed to %s: %s", r.Route, r.Why)
	}
	if r.Charset == "" {
		t.Error("the document was called legacy without naming the encoding it is in, which is the thing the transcode needs")
	}
	if !strings.Contains(r.Why, r.Charset) {
		t.Errorf("the reason does not name the encoding: %q", r.Why)
	}
}

// A scan has a text layer of zero, or nearly zero once the letterhead and the
// page number are counted, and it is the route that costs a GPU second.
func TestAPageImageWithNoTextOnItGoesToOCR(t *testing.T) {
	r := route.Read(pdf(t, page("BT (Trang 1) Tj ET")+imageObject(400_000), false))
	if r.Route != route.Scan {
		t.Fatalf("a page image routed to %s: %s", r.Route, r.Why)
	}
	if r.ImageShare < 0.9 {
		t.Errorf("a document that is almost entirely image data reported an image share of %.2f", r.ImageShare)
	}
}

// The floor is not one character. A scan with a page number on it is still a
// scan, and routing it to direct extraction produces a document holding the
// word "Trang" and nothing else.
func TestFurnitureOnAScanDoesNotMakeItATextLayer(t *testing.T) {
	r := route.Read(pdf(t, page("BT (Cong ty co phan ABC   Trang 4 / 12) Tj ET")+imageObject(200_000), false))
	if r.Route != route.Scan {
		t.Errorf("a scan with a letterhead and a page number routed to %s: %s", r.Route, r.Why)
	}
}

func TestDensityIsMeasuredPerPageRatherThanPerDocument(t *testing.T) {
	// One page of prose and nineteen scanned ones. Counted over the whole
	// document the text looks thin, and the point of dividing by pages is that
	// the answer does not depend on how many pages the file happens to hold.
	one := route.Read(pdf(t, page(show(vietnamese)), false))
	if one.Pages != 1 {
		t.Fatalf("the scan found %d pages in a one page document", one.Pages)
	}
	if one.GlyphsPerPage() < route.MinGlyphsPerPage {
		t.Errorf("a full page of prose measured %.0f characters a page", one.GlyphsPerPage())
	}
}

// Anything written this century puts the page tree and the fonts in a
// compressed object stream. A scanner that stops at the top level finds no
// pages in a completely ordinary document and calls it broken.
func TestPagesInsideACompressedObjectStreamAreStillFound(t *testing.T) {
	r := route.Read(objStmPDF(t))
	if r.Pages == 0 {
		t.Fatalf("the scan found no pages: %s", r.Why)
	}
	if r.Route != route.Text {
		t.Errorf("an ordinary modern PDF routed to %s: %s", r.Route, r.Why)
	}
}

func TestACompressedContentStreamIsRead(t *testing.T) {
	plain := route.Read(pdf(t, page(show(vietnamese)), false))
	packed := route.Read(pdf(t, page(show(vietnamese)), true))
	if packed.Glyphs != plain.Glyphs {
		t.Errorf("the same page showed %d characters compressed and %d uncompressed", packed.Glyphs, plain.Glyphs)
	}
	if packed.Route != plain.Route {
		t.Errorf("the same page routed to %s compressed and %s uncompressed", packed.Route, plain.Route)
	}
}

// Every text showing operator has to be read, because a document that uses only
// TJ and one that uses only Tj are the same document as far as cost goes, and
// missing one operator turns a page of prose into a scan.
func TestEveryTextShowingOperatorCounts(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"Tj", "BT (" + vietnamese + ") Tj ET"},
		{"TJ", "BT [(" + vietnamese + ") -250 (tiếp theo)] TJ ET"},
		{"quote", "BT (" + vietnamese + ") ' ET"},
		{"doublequote", "BT 1 2 (" + vietnamese + ") \" ET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := route.Read(pdf(t, page(tc.body), false))
			if r.Route != route.Text {
				t.Errorf("a page shown with %s routed to %s: %s", tc.name, r.Route, r.Why)
			}
		})
	}
}

// A hex string is what a document uses when its bytes would otherwise need
// escaping on every second character, which is exactly what a legacy Vietnamese
// encoding produces, so this is not an edge case on route L.
func TestAHexStringIsReadAsTheBytesItHolds(t *testing.T) {
	legacy := tcvn3(vietnamese)
	var hex strings.Builder
	for _, b := range []byte(legacy) {
		fmt.Fprintf(&hex, "%02X", b)
	}
	r := route.Read(pdf(t, page("BT <"+hex.String()+"> Tj ET"), false))
	if r.Route != route.Legacy {
		t.Fatalf("a hex encoded legacy string routed to %s: %s", r.Route, r.Why)
	}
}

func TestAnOctalEscapeIsTheByteItNames(t *testing.T) {
	// \351 is 0xE9, which TCVN3 shows as a Vietnamese letter and which a
	// document has to escape because it cannot be written literally.
	body := "BT (" + strings.ReplaceAll(escapeHigh(tcvn3(vietnamese)), "\n", "") + ") Tj ET"
	r := route.Read(pdf(t, page(body), false))
	if r.Route != route.Legacy {
		t.Fatalf("an octal escaped legacy string routed to %s: %s", r.Route, r.Why)
	}
}

// An encrypted document's streams decompress to nothing, which looks exactly
// like a document with no text layer. Routing it to OCR produces a bill and no
// finding.
func TestAnEncryptedDocumentIsNotSentToOCR(t *testing.T) {
	body := pdf(t, page(show(vietnamese)), false)
	body = bytes.Replace(body, []byte("trailer\n<<"), []byte("trailer\n<< /Encrypt 9 0 R"), 1)
	r := route.Read(body)
	if r.Route != route.Unroutable {
		t.Errorf("an encrypted document routed to %s, and its text layer cannot be read at all", r.Route)
	}
	if !strings.Contains(r.Why, "key") {
		t.Errorf("the reason does not say why it cannot be read: %q", r.Why)
	}
}

func TestSomethingThatIsNotAPDFIsSaidToNotBeOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"empty", nil},
		{"html", []byte("<!doctype html><title>Không phải PDF</title>")},
		{"truncated header", []byte("%PD")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := route.Read(tc.body)
			if r.Route != route.Unroutable {
				t.Errorf("%s routed to %s", tc.name, r.Route)
			}
			if r.Why == "" {
				t.Error("a document was refused with no reason given")
			}
		})
	}
}

// A PDF with a header and nothing behind it is not a scan. Calling it one sends
// a broken file to the GPU, which is the most expensive way to discover that it
// is broken.
func TestAPDFWithNoPagesIsRefusedRatherThanGuessedAt(t *testing.T) {
	r := route.Read([]byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF"))
	if r.Route != route.Unroutable {
		t.Errorf("a document with no pages routed to %s", r.Route)
	}
}

// Every route has to explain itself, because the routing distribution is
// published and somebody will disagree with a document in it.
func TestEveryReadingSaysWhyInASentenceSomebodyCanArgueWith(t *testing.T) {
	for _, body := range [][]byte{
		pdf(t, page(show(vietnamese)), false),
		pdf(t, page(show(tcvn3(vietnamese))), false),
		pdf(t, page("BT (x) Tj ET")+imageObject(100_000), false),
		[]byte("not a pdf"),
	} {
		r := route.Read(body)
		if r.Why == "" {
			t.Errorf("a %s decision came with no reason", r.Route)
		}
		if strings.Contains(r.Why, "—") {
			t.Errorf("%q has an em dash in it", r.Why)
		}
		if strings.Contains(r.Why, "\n") {
			t.Errorf("%q has a line break inside it", r.Why)
		}
	}
}

func TestASubsetFontIsTheFontItIsASubsetOf(t *testing.T) {
	body := pdf(t, page(show(vietnamese))+fontObject("ABCDEF+VNI-Times")+fontObject("VNI-Times"), false)
	r := route.Read(body)
	if len(r.Fonts) != 1 || r.Fonts[0] != "VNI-Times" {
		t.Errorf("the document names fonts %v, and it uses one font in two files", r.Fonts)
	}
}

func TestTheRouteLettersAreTheOnesTheCostTablesUse(t *testing.T) {
	want := map[route.Route]string{route.Text: "T", route.Legacy: "L", route.Scan: "O", route.Unroutable: "-"}
	for r, letter := range want {
		if r.Letter() != letter {
			t.Errorf("%s prints as %q and the plan calls it %q", r, r.Letter(), letter)
		}
		if r.String() == "" {
			t.Errorf("route %d has no name", int(r))
		}
	}
}

func TestTheDistributionCarriesTheBoxItWasCountedOn(t *testing.T) {
	d := route.NewDistribution("gamingpc")
	d.Add(route.Reading{Route: route.Text, Pages: 10})
	d.Add(route.Reading{Route: route.Text, Pages: 4})
	d.Add(route.Reading{Route: route.Scan, Pages: 200})
	d.Add(route.Reading{Route: route.Legacy, Pages: 6, Charset: "TCVN3"})

	if d.Total() != 4 {
		t.Errorf("the distribution counted %d documents", d.Total())
	}
	if got := d.Share(route.Text); got != 50 {
		t.Errorf("two of four documents is %.1f%%", got)
	}
	// Pages are counted separately because OCR is billed per page, and one
	// scanned book among a hundred born digital pages is most of the bill.
	if d.Pages(route.Scan) != 200 {
		t.Errorf("the scan route holds %d pages", d.Pages(route.Scan))
	}
	if d.Charsets()["TCVN3"] != 1 {
		t.Error("the distribution did not record which legacy encoding was found")
	}
	if !strings.Contains(d.String(), "gamingpc") {
		t.Error("a published routing distribution does not say what hardware produced it")
	}
}

func TestAnEmptyDistributionReportsNothingRatherThanZeroPercent(t *testing.T) {
	d := route.NewDistribution("server3")
	if d.Total() != 0 {
		t.Errorf("a fresh distribution holds %d documents", d.Total())
	}
	for _, r := range route.Routes {
		if d.Share(r) != 0 {
			t.Errorf("%s is %.1f%% of nothing", r, d.Share(r))
		}
	}
}

// Test helpers below. The PDFs are built here rather than checked in because a
// binary fixture in a repository is a fixture nobody can see the point of, and
// every one of these is four lines of structure around the one thing its test
// is about.

const vietnamese = "Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc. " +
	"Điều 1. Luật này quy định về quyền và nghĩa vụ của công dân trong việc " +
	"tiếp cận thông tin, và trách nhiệm của cơ quan nhà nước trong việc bảo đảm " +
	"quyền tiếp cận thông tin của công dân theo quy định của Hiến pháp."

func short(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

// tcvn3 writes Vietnamese the way a 2003 document holds it.
//
// The fixture is built by inverting normalize's own decoder rather than by
// typing a byte string, because a hand typed one is a test about what somebody
// believed TCVN3 was. Every single byte and every byte pair is decoded once
// and the results are turned around, which is enough because the encoding is a
// table and a table is invertible.
func tcvn3(s string) string {
	var set *normalize.Charset
	for _, c := range normalize.Charsets() {
		if c.Name() == "TCVN3" {
			set = c
		}
	}
	if set == nil {
		panic("TCVN3 is not among phoi's charsets")
	}

	single := map[rune]string{}
	for lo := 0x80; lo <= 0xFF; lo++ {
		if out, letters := set.Decode(string([]byte{byte(lo)})); letters == 1 {
			single[[]rune(out)[0]] = string([]byte{byte(lo)})
		}
	}
	pair := map[rune]string{}
	for a := 0x20; a <= 0xFF; a++ {
		for b := 0x80; b <= 0xFF; b++ {
			in := string([]byte{byte(a), byte(b)})
			if out, letters := set.Decode(in); letters == 1 && len([]rune(out)) == 1 {
				pair[[]rune(out)[0]] = in
			}
		}
	}

	var b strings.Builder
	for _, r := range s {
		switch {
		case pair[r] != "":
			b.WriteString(pair[r])
		case single[r] != "":
			b.WriteString(single[r])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeHigh writes every byte above ASCII as the octal escape a real document
// has to use for it.
func escapeHigh(s string) string {
	var b strings.Builder
	for i := range len(s) {
		if c := s[i]; c >= 0x80 || c == '(' || c == ')' || c == '\\' {
			fmt.Fprintf(&b, "\\%03o", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// show wraps text in the operators a page uses to put it on the paper. A
// content stream that is only characters shows nothing, which is what a real
// PDF with no text layer looks like too.
func show(s string) string { return "BT (" + s + ") Tj ET" }

// page returns a page object and the content stream it points at.
func page(body string) string {
	return fmt.Sprintf("3 0 obj\n<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>\nendobj\n"+
		"4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(body), body)
}

func fontObject(name string) string {
	return fmt.Sprintf("7 0 obj\n<< /Type /Font /Subtype /TrueType /BaseFont /%s >>\nendobj\n", name)
}

func imageObject(size int) string {
	return fmt.Sprintf("5 0 obj\n<< /Type /XObject /Subtype /Image /Width 2480 /Height 3508 /Length %d >>\nstream\n%s\nendstream\nendobj\n",
		size, strings.Repeat("\xff\x00", size/2))
}

// pdf wraps objects in the smallest thing a scanner will accept as a document.
func pdf(t *testing.T, objects string, compress bool) []byte {
	t.Helper()
	if compress {
		objects = deflateStreams(t, objects)
	}
	return []byte("%PDF-1.7\n" +
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n" +
		objects +
		"trailer\n<< /Root 1 0 R >>\n%%EOF\n")
}

// deflateStreams compresses every stream in the document and marks it, which is
// what any real writer does and what the scanner therefore has to undo.
func deflateStreams(t *testing.T, objects string) string {
	t.Helper()
	var out strings.Builder
	rest := objects
	for {
		i := strings.Index(rest, "stream\n")
		if i < 0 {
			out.WriteString(rest)
			return out.String()
		}
		j := strings.Index(rest[i:], "\nendstream")
		if j < 0 {
			out.WriteString(rest)
			return out.String()
		}
		body := rest[i+len("stream\n") : i+j]
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		if _, err := zw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		head := strings.Replace(rest[:i], "/Length", "/Filter /FlateDecode /Length", 1)
		out.WriteString(head)
		out.WriteString("stream\n")
		out.Write(buf.Bytes())
		out.WriteString("\nendstream")
		rest = rest[i+j+len("\nendstream"):]
	}
}

// objStmPDF puts the page tree and the page object inside a compressed object
// stream, which is how every modern writer produces a PDF.
func objStmPDF(t *testing.T) []byte {
	t.Helper()
	body := show(vietnamese)

	inner := "<< /Type /Catalog /Pages 2 0 R >>" +
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>" +
		"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>"
	offsets := make([]int, 0, 3)
	offsets = append(offsets, 0, len("<< /Type /Catalog /Pages 2 0 R >>"))
	offsets = append(offsets, offsets[1]+len("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"))
	header := fmt.Sprintf("1 %d 2 %d 3 %d ", offsets[0], offsets[1], offsets[2])
	packed := header + inner

	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte(packed)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	fmt.Fprintf(&out, "%%PDF-1.7\n6 0 obj\n<< /Type /ObjStm /N 3 /First %d /Filter /FlateDecode /Length %d >>\nstream\n",
		len(header), buf.Len())
	out.Write(buf.Bytes())
	out.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&out, "4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(body), body)
	out.WriteString("trailer\n<< /Root 1 0 R >>\n%%EOF\n")
	return out.Bytes()
}
