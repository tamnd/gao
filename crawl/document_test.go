package crawl

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/harvest"
)

// build runs a document through the gate the way [Run] does: the bytes are what
// the server sent, the page is what the extractor made of them, and the visit
// carries what the fetch knew.
func build(t *testing.T, rawurl, html string) Verdict {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatalf("parsing the URL: %v", err)
	}
	p, err := Read(u, strings.NewReader(html))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return Build(&harvest.Visit{
		URL:    rawurl,
		Body:   []byte(html),
		Status: http.StatusOK,
		Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
	}, p, BuildOptions{Locator: "web-00000-00001.warc.gz:0:1024"})
}

// The three text columns come off one page and say three different things about
// it: what the extractor thought the content was, the same thing with its shape,
// and the whole page for anybody who disagrees with the extractor.
func TestADocumentCarriesTheTextTheMarkdownAndTheBody(t *testing.T) {
	v := build(t, "https://vnexpress.example/thoi-su/ap-thap-nhiet-doi-123.html", articleInASidebar)

	if !v.Kept {
		t.Fatalf("the page was turned away at %s for %s: %s", v.Stage, v.Reason, v.Detail)
	}
	d := v.Doc
	if d.Text == "" || d.Markdown == "" || d.Body == "" {
		t.Fatalf("text %d, markdown %d, body %d, and all three should carry the page",
			len(d.Text), len(d.Markdown), len(d.Body))
	}
	if !strings.HasPrefix(d.Markdown, "# Ap thap nhiet doi") {
		t.Errorf("the markdown does not open on the headline:\n%.80s", d.Markdown)
	}

	// The body is the whole page, so it holds the footer the extractor dropped.
	// That is the point of it: an extractor that turns out to be wrong costs the
	// corpus nothing as long as what it removed is still in the row.
	if strings.Contains(d.Text, "Giay phep so") {
		t.Errorf("the footer is in the text:\n%s", d.Text)
	}
	if !strings.Contains(d.Body, "Giay phep so") {
		t.Error("the body dropped the footer, so it is not the whole page")
	}

	// The identity and the shape columns are the text's, not the body's. A
	// document is the content somebody would read, and two pages carrying the
	// same article under different furniture are the same document.
	if d.NChars == 0 {
		t.Error("n_chars is zero on a document with text in it")
	}
	if d.DocID != doc.SumString(d.Text) {
		t.Error("the identity is not the hash of the text")
	}
	if d.SchemaVersion != doc.SchemaVersion {
		t.Errorf("the row says schema version %d, want %d", d.SchemaVersion, doc.SchemaVersion)
	}
}

// A page of links is refused, and the refusal names the stage that refused it
// and keeps the address, because a host that is absent from the corpus is a
// question somebody asks eventually.
func TestAPageWithNoArticleIsRefusedAtTheExtractor(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html lang="vi"><body><div class="list">`)
	for i := range 40 {
		b.WriteString(`<div><a href="/bai-`)
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(`.html">Mot tieu de bai bao kha dai de trong giong that</a></div>`)
	}
	b.WriteString(`</div></body></html>`)

	v := build(t, "https://tin.example/thoi-su", b.String())

	if v.Kept {
		t.Fatal("a page of links was kept as a document")
	}
	if v.Stage != StageExtract {
		t.Errorf("the page was turned away at %s, want %s", v.Stage, StageExtract)
	}
	if v.Doc.Text != "" {
		t.Errorf("the extractor found text on a page of links:\n%s", v.Doc.Text)
	}
	// The refusal still carries the provenance, which is what makes it something
	// anybody can act on. A row that says "boilerplate" and nothing else is a row
	// nobody can argue with.
	if v.Doc.URL == "" || v.Doc.Host == "" || v.Doc.SourceLocator == "" {
		t.Errorf("the rejection lost its provenance: %+v", v.Doc.Provenance)
	}
}
