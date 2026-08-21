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
// It moves when rows stop being comparable to the rows before them, which is
// either because a published column changed meaning or because the crawl
// changed its mind about what to fetch. Every move so far came out of the first
// fleet run rather than out of the code.
//
// 0.2.0 is where fetched_at became the time the request went out. Under 0.1.0 it
// was taken when a worker picked the URL up, before the fetch waited for the
// host's turn, so a pair of 0.1.0 rows can say two requests went to one site in
// the same millisecond when the requests were a second apart. Anybody measuring
// this crawl's manners should measure them on 0.2.0 and later.
//
// 0.3.0 is where a 403 or a 401 became a fetch rejection instead of a robots
// one. Under 0.2.0 and earlier, reject_reason adds a publisher who stated a rule
// in robots.txt to a server that put up a bot wall, and on the first run that
// was 999 real disallows against 832 walls. Both versions keep the host and the
// status in reject_detail, so a wall is countable in every version and only the
// reason column changed.
//
// 0.4.0 is where the crawl stopped spending a full host allowance on hosts that
// had never produced a Vietnamese page. Nothing about the columns changed, and
// that is exactly why the version has to move: a frontier refusal never becomes
// a row, so the only trace of the old behavior is which hosts are in the
// corpus at all. Under 0.3.0 and earlier the fleet followed the seeds outwards
// until 19,457 of one hour's 22,022 requests were going to hosts outside .vn,
// so a reader counting what share of this crawl is Vietnamese gets a different
// answer either side of the boundary and deserves to know where it is.
//
// 0.5.0 is the other half of the same problem, and it needed the 0.4.0 rows to
// find. Reach bounds what one host that keeps nothing can cost and says nothing
// about how many such hosts arrive, and under 0.4.0, 5,818 hosts outside .vn
// showed up in half an hour and took 3.6 requests each. They arrive because the
// links on a page were queued before the page was judged, so a page in another
// language handed the frontier a whole subgraph of that language. Links are now
// followed after the verdict and a page that failed the language test is a dead
// end. Only that one rejection stops a page from being followed: a listing
// refused as boilerplate is still a Vietnamese page and its links are the most
// valuable thing on the site.
//
// 0.6.0 is where a worker stopped waiting for a host. Under 0.5.0 and earlier a
// worker that drew a URL for a host with a request already in flight got in line
// behind it, and one that drew a host the schedule had pushed hours out slept
// until then. Both were the politeness rules doing what they were written to do
// and both were paid for out of the crawl: on the third shard of the fleet run
// that found this, all twenty workers ended up on one host, nineteen queued
// behind the twentieth and the twentieth asleep on a twenty seven minute gap,
// and between two and a half and four and a half hours in the shard fetched one
// page while the other two held three pages a second. A host that is not ready
// now hands the URL back to the queue. The host is not treated any faster for
// it, so what changed is not what the sites see but which of the queued million
// this run got to, and the corpus either side of the boundary is drawn from a
// different set of hosts.
//
// 0.7.0 is where the crawl started going as fast as the box it is on. Under
// 0.6.0 the default was twenty workers, all of them sharing one connection pool
// behind one mutex, and every fetched page took the same sink lock to be gzipped
// at the highest deflate level and then took it again per link to be offered to
// the frontier. On server3 that was 6.4 pages a second while ami on the same box
// at the same loaded moment held 130. The workers now number in the hundreds,
// the connection pools are sharded by host, a host that has never answered is
// given up on after three tries, a record is compressed before the sink lock is
// taken rather than inside it, and a page's links go to the frontier in one
// turn at its lock instead of sixty.
//
// None of that changes a column and all of it changes which pages are in the
// corpus. A crawl that fetches ten times as many pages an hour reaches deeper
// into each site before a part closes, and the dead host rule means the hosts
// that only ever timed out are absent rather than represented by their failures.
//
// Older rows stay published rather than being deleted, since a version column
// nobody has to trust is worth more than a corpus with holes in it.
const PipelineVersion = "0.7.0"

// A Page is what one HTML document had in it.
type Page struct {
	// Title is the document title with the site name trimmed off the end when
	// the separator makes that safe to do.
	Title string

	// Text is the main content, paragraphs separated by a blank line. It is
	// empty when nothing on the page looked like content, which is the ordinary
	// answer for a category listing or a photo gallery and is not an error.
	Text string

	// Markdown is the same content as Text, rendered with the document's shape
	// left in: headings, lists, tables, links and emphasis. It comes from the
	// same container Text does, so the two are two renderings of one piece of
	// the page rather than two guesses at which piece it was.
	Markdown string

	// Body is the whole document as markdown, with only the elements that are
	// not writing at all taken out.
	//
	// It is here because Text and Markdown are an extractor's opinion, and an
	// extractor is the part of a pipeline most likely to be wrong and least
	// likely to be noticed being wrong. A reader who thinks the container was
	// picked badly, or who wants the nav bar because they are studying nav bars,
	// has the page. Nobody has to re-fetch ten million pages to disagree with
	// this package.
	Body string

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
	shape := measure(root)
	if n := shape.mainBlock(root); n != nil {
		p.Text = shape.render(n)
		p.Markdown = markdown(n, resolve, shape.skipped)
	}
	p.Body = markdown(root, resolve, nonContent)
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
// that is nearly universally understood to mean do not go there, and honoring
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

// furniture is the words that appear in the class or id of a container that is
// never the page's content, however much text is in it.
//
// Every one of them names a thing rather than a position: an advertisement, a
// cookie notice, a share bar. A container called any of these is that thing.
var furniture = []string{
	"advert", "banner", "breadcrumb", "cookie", "comment", "disqus", "footer",
	"newsletter", "pagination", "popup", "share", "social", "subscribe",
}

// position is the words that name where something sits on the page rather than
// what it is, and those are the ones that cost a corpus an article.
//
// They used to be in the list above, and refusing a container for holding one of
// them is how this package returned two bytes from a vnexpress article for as
// long as it has existed. vnexpress wraps its stories in a div called sidebar-1
// inside one called header-content, and both of those words were a ban. Three
// articles taken off the front page on 21 August each came back as the
// multiplication sign from a close button while the prose sat in the HTML.
//
// A word in this list is a reason to look rather than a verdict. What decides is
// the same arithmetic the rest of the extractor runs on: a container holding
// more link text than prose is navigation whatever it is called, and a container
// holding two paragraphs of prose is content whatever it is called. That keeps
// the case the list was written for, a sidebar of forty article teasers with
// real sentences in it, because forty teasers are forty links and the links win.
var position = []string{
	"header", "menu", "nav", "related", "sidebar", "tag-list", "widget",
}

// prose is how much text outside links a container named for its position has to
// hold before it is read as content rather than as furniture.
//
// Two paragraphs, roughly. Below it are the mastheads and the footers, which are
// a line of text and a phone number and would otherwise be read as content on
// any page with few enough links in them.
const prose = 400

// A shape is what the containers of one document hold: characters of text
// outside links, and characters of text inside them.
//
// It is measured once per document, in one pass, and only for the containers
// whose name puts the question. Measuring every element would be a map of a
// hundred thousand entries on a page like the Vietnamese Wikipedia, and asking
// per candidate would walk the subtree again for every one of them.
type shape map[*html.Node]counts

type counts struct{ chars, links int }

// measure walks a document once and records what the named containers hold.
func measure(root *html.Node) shape {
	s := shape{}
	var walk func(n *html.Node, inLink bool) counts
	walk = func(n *html.Node, inLink bool) counts {
		var c counts
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			switch ch.Type {
			case html.TextNode:
				k := len(strings.TrimSpace(ch.Data))
				if inLink {
					c.links += k
				} else {
					c.chars += k
				}
			case html.ElementNode:
				sub := walk(ch, inLink || ch.DataAtom == atom.A)
				c.chars += sub.chars
				c.links += sub.links
			}
		}
		if named(n, position) {
			s[n] = c
		}
		return c
	}
	walk(root, false)
	return s
}

// named reports whether a node's class or id holds one of the words.
func named(n *html.Node, words []string) bool {
	if n.Type != html.ElementNode {
		return false
	}
	class := strings.ToLower(attr(n, "class") + " " + attr(n, "id"))
	if class == " " {
		return false
	}
	for _, w := range words {
		if strings.Contains(class, w) {
			return true
		}
	}
	return false
}

// skipped are the elements whose text is not part of the content. Script and
// style are the obvious ones and the rest are the parts of the page that are
// furniture in every design.
func (s shape) skipped(n *html.Node) bool {
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
	if named(n, furniture) {
		return true
	}
	if !named(n, position) {
		return false
	}
	c := s[n]
	return c.links >= c.chars || c.chars < prose
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

// mainBlock picks the container that holds the page's content.
//
// Containers are scored from the deepest up, and the best one wins outright
// rather than being merged with its neighbors. An article split across two divs
// loses the second one, and that is the right trade: merging containers by score
// is how an extractor ends up with the article followed by the list of related
// stories, and a document with a nav column glued to the end of it is worse than
// a document that is short.
func (s shape) mainBlock(root *html.Node) *html.Node {
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
				if s.skipped(c) {
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
		return nil
	}
	return best.node
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
func (s shape) render(n *html.Node) string {
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
				if s.skipped(c) {
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
