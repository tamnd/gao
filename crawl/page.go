package crawl

// Reading a page.
//
// A crawl needs two things out of the HTML it fetched: the links, so it has
// somewhere to go next, and the text, so the fetch was worth making. Neither is
// hard on a page written by hand and both are hard on the web, where the article
// is nine hundred words in the middle of eleven thousand words of navigation,
// cookie notices, related stories, share buttons and a footer with every
// province in the country in it.
//
// The forum extractor in husk finds a conversation by looking for the repeated
// element that a thread page has and an article page does not. This is the other
// case, and the method is the one that has worked since readability: a container
// holding a lot of text and few links is the article, a container holding a lot
// of links and little text is the navigation, and which is which can be decided
// without knowing what site it is.
//
// What is deliberately not here is a rule about any particular site. A crawl of
// ten million Vietnamese sites cannot carry a rule per site, and a rule for the
// twenty biggest is twenty rules that rot.

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/tamnd/gao/harvest"
)

// ExtractorVersion is the version of the rules below, and it moves when the
// rules change rather than when the binary does. Two documents carrying the same
// extractor string were built the same way, which is the only question that
// column answers.
const ExtractorVersion = "1.0.0"

// Extractor is the value of the extractor column for a document this package
// produced.
const Extractor = "gao-crawl@" + ExtractorVersion

// PipelineVersion is the value of the pipeline_version column for a crawled
// document that has been extracted and nothing else. The leading zero says that
// no cleaning stage has touched it, the same as it does for an ingest.
//
// It moved to 0.2.0 when fetched_at changed meaning. Rows written by 0.1.0 were
// stamped when a worker picked the URL up, before the fetch waited for the
// host's turn, so a pair of them can say two requests went to one site in the
// same millisecond when the requests were a second apart. Rows written by 0.2.0
// are stamped after the wait, when the request goes out. Both are published and
// this column is how a reader tells them apart, which is better than a sentence
// in the card that a query cannot see.
const PipelineVersion = "0.2.0"

// A Page is what one HTML document had in it.
type Page struct {
	// Title is the document title with the site name trimmed off the end when
	// the separator makes that safe to do.
	Title string

	// Text is the main content, paragraphs separated by a blank line. It is
	// empty when nothing on the page looked like content, which is the ordinary
	// answer for a category listing or a photo gallery and is not an error.
	Text string

	// Links are every URL the page pointed at, absolute, in the order they
	// appeared. They are not canonicalized or filtered here: that is the
	// frontier's job and it has the seen set and the budget to do it with.
	Links []string

	// Canonical is what the page said its own address is, absolute, when it said
	// so. A crawl that ignores this fetches the same article once per tracking
	// parameter somebody linked it with.
	Canonical string

	// Lang is the lang attribute on the html element, lowercased, when there is
	// one. It is a hint and nothing more: the language of the text is decided by
	// measuring the text.
	Lang string

	// Reserve is what the document said about text and data mining in its meta
	// tags. The caller merges it with what the response headers said, because a
	// site can reserve its rights in either place and meaning it in one is
	// meaning it.
	Reserve harvest.Reservation

	// NoFollow is set when a meta robots tag asked that the links on this page
	// not be followed. The links are still returned, because the record of what
	// a page pointed at is a fact about the page, and the caller drops them.
	NoFollow bool
}

// Read parses an HTML document fetched from base.
//
// The base URL is required, because a page is mostly relative links and a
// relative link with nothing to resolve against is not a URL. An error means the
// HTML could not be parsed at all, which is rare: the parser recovers from
// almost anything, and what it recovers into is what the browsers do.
func Read(base *url.URL, r io.Reader) (*Page, error) {
	if base == nil {
		return nil, fmt.Errorf("crawl: reading a page needs the URL it came from")
	}
	root, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("crawl: parsing %s: %w", base, err)
	}
	p := &Page{}

	// The head is read first, because a base element changes what every link on
	// the page resolves to and a meta robots tag changes whether they are
	// followed at all.
	resolve := base
	p.readHead(root, &resolve)
	p.readLinks(root, resolve)
	p.Text = mainText(root)
	return p, nil
}

// readHead walks the document for the elements that say something about the
// whole page: the title, the language, the canonical address, the base, and the
// meta tags that reserve rights.
func (p *Page) readHead(root *html.Node, resolve **url.URL) {
	walk(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		switch n.DataAtom {
		case atom.Html:
			if v := attr(n, "lang"); v != "" && p.Lang == "" {
				p.Lang = strings.ToLower(strings.TrimSpace(v))
			}
		case atom.Title:
			if p.Title == "" {
				p.Title = trimTitle(text(n))
			}
		case atom.Base:
			if u, err := (*resolve).Parse(attr(n, "href")); err == nil && u.Host != "" {
				*resolve = u
			}
		case atom.Link:
			if strings.Contains(strings.ToLower(attr(n, "rel")), "canonical") && p.Canonical == "" {
				if u, err := (*resolve).Parse(strings.TrimSpace(attr(n, "href"))); err == nil {
					p.Canonical = u.String()
				}
			}
		case atom.Meta:
			p.readMeta(n)
		case atom.Body:
			// Nothing in the body says anything about the whole document, and a
			// meta tag down there is a meta tag a browser ignores.
			return false
		}
		return true
	})
}

func (p *Page) readMeta(n *html.Node) {
	name := strings.ToLower(strings.TrimSpace(attr(n, "name")))
	content := attr(n, "content")
	if name == "" || content == "" {
		return
	}
	p.Reserve.Meta(name, content)
	// A page addresses this crawler by its product token, the same name a
	// robots.txt rule uses, and a rule addressed to us counts alongside the one
	// addressed to everybody.
	switch name {
	case "robots", harvest.Bot:
		if strings.Contains(strings.ToLower(content), "nofollow") {
			p.NoFollow = true
		}
	}
	if p.Lang == "" && (name == "language" || name == "content-language") {
		p.Lang = strings.ToLower(strings.TrimSpace(content))
	}
}

// readLinks collects every href on the page, resolved and absolute.
//
// A link with rel=nofollow is left out. It is the one link level instruction
// that is nearly universally understood to mean do not go there, and honouring
// it costs a crawl almost nothing: it is on comment links and on paid links,
// which are the two kinds of link least likely to lead to a Vietnamese article.
func (p *Page) readLinks(root *html.Node, base *url.URL) {
	seen := map[string]bool{}
	walk(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.DataAtom != atom.A {
			return true
		}
		href := strings.TrimSpace(attr(n, "href"))
		if href == "" || strings.HasPrefix(href, "#") {
			return true
		}
		if strings.Contains(strings.ToLower(attr(n, "rel")), "nofollow") {
			return true
		}
		u, err := base.Parse(href)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return true
		}
		u.Fragment, u.RawFragment = "", ""
		s := u.String()
		if seen[s] {
			return true
		}
		seen[s] = true
		p.Links = append(p.Links, s)
		return true
	})
}

// boilerplate is the words that appear in the class or id of a container that
// holds something other than the page's content.
//
// A word list is a blunt instrument and this one is short on purpose. It is here
// for the case the density test gets wrong: a sidebar of forty article teasers
// has real sentences in it and low enough link density to look like content, and
// no amount of arithmetic tells it from the article. Everything else is left to
// the arithmetic, because a list long enough to cover the web is a list that
// throws away an article whose author called their content div "menu-content".
var boilerplate = []string{
	"advert", "banner", "breadcrumb", "cookie", "comment", "disqus", "footer",
	"header", "menu", "nav", "newsletter", "pagination", "popup", "related",
	"share", "sidebar", "social", "subscribe", "tag-list", "widget",
}

// skipped are the elements whose text is never part of the content, whatever it
// says. Script and style are the obvious ones and the rest are the parts of the
// page that are furniture in every design.
func skipped(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg,
		atom.Iframe, atom.Form, atom.Button, atom.Select, atom.Textarea,
		atom.Nav, atom.Aside, atom.Footer:
		return true
	}
	if n.DataAtom == atom.Header {
		// A header inside an article is the headline. A header at the top of the
		// document is the site's masthead.
		return !inside(n, atom.Article)
	}
	class := strings.ToLower(attr(n, "class") + " " + attr(n, "id"))
	if class == " " {
		return false
	}
	for _, w := range boilerplate {
		if strings.Contains(class, w) {
			return true
		}
	}
	return false
}

// A block is one candidate container with the numbers the decision is made on.
type block struct {
	node  *html.Node
	chars int // characters of text under it, outside anchors
	links int // characters of text under it, inside anchors
	paras int // paragraph level elements under it
}

// score is what decides which container is the content.
//
// It is the text a container holds, less what is inside its links, plus a bonus
// for holding paragraphs rather than one wall of text. The link subtraction is
// the whole method: a navigation column and an article can hold the same number
// of characters, and the difference between them is that nearly every character
// of the navigation is inside an anchor.
func (b block) score() int {
	if b.chars == 0 {
		return 0
	}
	s := b.chars - b.links
	if b.paras > 1 {
		s += b.paras * 25
	}
	return s
}

// mainText picks the container that holds the page's content and renders it.
//
// Containers are scored from the deepest up, and the best one wins outright
// rather than being merged with its neighbours. An article split across two divs
// loses the second one, and that is the right trade: merging containers by score
// is how an extractor ends up with the article followed by the list of related
// stories, and a document with a nav column glued to the end of it is worse than
// a document that is short.
func mainText(root *html.Node) string {
	var best block
	var scan func(n *html.Node) block
	scan = func(n *html.Node) block {
		b := block{node: n}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				count := len(strings.TrimSpace(c.Data))
				if inside(c, atom.A) {
					b.links += count
				} else {
					b.chars += count
				}
			case html.ElementNode:
				if skipped(c) {
					continue
				}
				sub := scan(c)
				b.chars += sub.chars
				b.links += sub.links
				b.paras += sub.paras
				if paragraph(c) {
					b.paras++
				}
			}
		}
		// A container is only a candidate when it is a container. Scoring a
		// paragraph against the article it is in means picking the longest
		// paragraph and losing the rest of the page.
		if container(n) && b.score() > best.score() {
			best = b
		}
		return b
	}
	whole := scan(root)
	if best.node == nil {
		best = whole
	}

	// A container that is mostly links is a listing page, and a listing page has
	// no content of its own however much text is in the link titles.
	if best.chars == 0 || best.links > best.chars {
		return ""
	}
	return render(best.node)
}

func container(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Div, atom.Article, atom.Section, atom.Main, atom.Body, atom.Td, atom.Blockquote:
		return true
	}
	return false
}

func paragraph(n *html.Node) bool {
	switch n.DataAtom {
	case atom.P, atom.Br, atom.Li, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		return true
	}
	return false
}

// render turns a subtree into paragraphs.
//
// The block elements decide where the line breaks go, because that is what they
// mean, and everything else is joined with spaces. Two texts separated by a
// block element are two paragraphs and two texts inside one are one sentence
// with a link in the middle of it.
func render(n *html.Node) string {
	var out strings.Builder
	var line strings.Builder

	flush := func() {
		s := squeeze(line.String())
		line.Reset()
		if s == "" {
			return
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(s)
	}

	var walkNode func(n *html.Node)
	walkNode = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				if s := strings.TrimSpace(c.Data); s != "" {
					if line.Len() > 0 {
						line.WriteByte(' ')
					}
					line.WriteString(s)
				}
			case html.ElementNode:
				if skipped(c) {
					continue
				}
				if block := paragraph(c) || container(c); block {
					flush()
				}
				walkNode(c)
				if block := paragraph(c) || container(c); block {
					flush()
				}
			}
		}
	}
	walkNode(n)
	flush()
	return out.String()
}

// squeeze collapses runs of whitespace to one space and trims the ends.
//
// It leaves the characters alone otherwise. Normalizing the text is the
// normalize package's job and it is a different one: this is here so that a
// paragraph broken across forty source lines by a template comes out as a
// paragraph.
func squeeze(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// trimTitle takes the site name off the end of a document title.
//
// Nearly every Vietnamese news site writes the title as the headline, a
// separator, and the masthead, and the masthead is on every page. Leaving it on
// is a phrase repeated across a million documents, which is exactly what the
// deduplication downstream is for, so this is not correctness. It is politeness
// to whoever reads the corpus.
func trimTitle(s string) string {
	s = squeeze(s)
	for _, sep := range []string{" | ", " - ", " – ", " — ", " :: ", " » "} {
		i := strings.LastIndex(s, sep)
		if i <= 0 {
			continue
		}
		head, tail := strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+len(sep):])
		// A masthead is short and it is a name. The length and the word count
		// are both needed: a dash is also how a headline joins two clauses, and
		// the clause after it is short enough to pass a length test on its own.
		if len(head) >= 10 && len(tail) <= 40 && len(strings.Fields(tail)) <= 5 {
			return head
		}
	}
	return s
}

// walk visits the tree until the callback says to stop descending.
func walk(n *html.Node, fn func(*html.Node) bool) {
	if !fn(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}

func inside(n *html.Node, a atom.Atom) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.DataAtom == a {
			return true
		}
	}
	return false
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}

func text(n *html.Node) string {
	var b strings.Builder
	walk(n, func(c *html.Node) bool {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
		return true
	})
	return b.String()
}
