package crawl

// Rendering a page as markdown.
//
// The text renderer next door throws the document's shape away on purpose: it
// wants paragraphs and nothing else, because that is what a language model reads
// and what every quality measure in this pipeline counts. That is the right
// answer for the text column and it is a lossy one. A recipe becomes a wall of
// prose, a table of exam results becomes a run of numbers with nothing saying
// which column they were in, and a heading is indistinguishable from a sentence.
//
// So the page is rendered twice from the same tree. Text for the readers who
// want prose, markdown for the readers who want the document, and both from the
// same container so the two columns of one row are the same piece of the page
// rather than two different guesses at it.
//
// What is here is deliberately a subset. Headings, paragraphs, lists, links,
// images, emphasis, code, quotes, rules and tables cover what is actually on a
// Vietnamese news site, forum or government page. There is no HTML passthrough,
// because markdown that carries HTML through is a document a reader has to parse
// twice, and the one thing a corpus column has to be is uniform.

import (
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// markdown renders a subtree as markdown, resolving links and images against
// base.
//
// The skip function decides what is furniture. The content column passes
// [skipped], which is the same judgment the text renderer makes, and the body
// column passes [nonContent], which throws away only what is not writing at all.
func markdown(n *html.Node, base *url.URL, skip func(*html.Node) bool) string {
	m := &marker{base: base, skip: skip}
	m.blocks(n)
	m.flush()
	return strings.Join(m.out, "\n\n")
}

// nonContent is the smaller of the two skip rules: the elements whose contents
// are not writing in any rendering of the page.
//
// [skipped] is the other one, and the difference between them is the difference
// between the two columns. A nav bar is furniture in the content column and it
// is part of the body, so the body column keeps it.
func nonContent(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg,
		atom.Iframe, atom.Object, atom.Canvas, atom.Map, atom.Area:
		return true
	}
	return false
}

// A marker is one render in progress: the blocks finished, the inline run being
// built, and how deep in lists and quotes it currently is.
type marker struct {
	base *url.URL
	skip func(*html.Node) bool

	out  []string
	line strings.Builder

	// quote is how many blockquotes deep this is, and list is one entry per
	// enclosing list, so an ordered list inside a bulleted one numbers its own
	// items and indents by the right amount.
	quote int
	list  []*listing
}

// A listing is one open list: whether it counts and what it has counted to.
type listing struct {
	ordered bool
	n       int
}

// flush ends the inline run and files it as a block.
func (m *marker) flush() {
	s := strings.TrimRight(squeeze(m.line.String()), " ")
	m.line.Reset()
	if s == "" {
		return
	}
	m.emit(s)
}

// emit files a finished block, wearing whatever quote markers are open.
func (m *marker) emit(s string) {
	if m.quote > 0 {
		prefix := strings.Repeat("> ", m.quote)
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(prefix+l, " ")
		}
		s = strings.Join(lines, "\n")
	}
	m.out = append(m.out, s)
}

// write adds inline text to the run, putting a space between it and what is
// already there when both sides want one.
func (m *marker) write(s string) {
	if s == "" {
		return
	}
	if m.line.Len() > 0 && !strings.HasSuffix(m.line.String(), " ") && !strings.HasPrefix(s, " ") {
		m.line.WriteByte(' ')
	}
	m.line.WriteString(s)
}

// blocks walks the children of a node, filing block elements as they arrive and
// gathering everything else into the inline run.
//
// It is shaped like [render] next door for a reason. The two renderers agree on
// where a paragraph ends, so the text and the markdown of one page break in the
// same places, and a reader comparing the two columns is comparing renderings
// rather than two different opinions about the document.
func (m *marker) blocks(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			m.write(escape(squeeze(c.Data)))
		case html.ElementNode:
			if m.skip(c) {
				continue
			}
			m.element(c)
		}
	}
}

func (m *marker) element(n *html.Node) {
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		m.flush()
		level := int(n.Data[1] - '0')
		if s := m.inline(n); s != "" {
			m.emit(strings.Repeat("#", level) + " " + s)
		}
	case atom.P:
		m.flush()
		m.blocks(n)
		m.flush()
	case atom.Br:
		// A hard break inside a paragraph, which markdown spells as two spaces
		// at the end of the line.
		if m.line.Len() > 0 {
			m.line.WriteString("  \n")
		}
	case atom.Hr:
		m.flush()
		m.emit("---")
	case atom.Pre:
		m.flush()
		m.emit(fence(text(n)))
	case atom.Blockquote:
		m.flush()
		m.quote++
		m.blocks(n)
		m.flush()
		m.quote--
	case atom.Ul, atom.Ol:
		m.flush()
		m.list = append(m.list, &listing{ordered: n.DataAtom == atom.Ol})
		m.blocks(n)
		m.flush()
		m.list = m.list[:len(m.list)-1]
	case atom.Li:
		m.flush()
		m.item(n)
	case atom.Table:
		m.flush()
		if s := m.table(n); s != "" {
			m.emit(s)
		}
	case atom.Tr, atom.Td, atom.Th, atom.Thead, atom.Tbody, atom.Tfoot:
		// Reached outside a table, which happens on pages the parser had to
		// repair. Treated as ordinary containers so the text is not lost.
		m.flush()
		m.blocks(n)
		m.flush()
	case atom.A, atom.Img, atom.Strong, atom.B, atom.Em, atom.I, atom.Code,
		atom.Del, atom.S, atom.Strike, atom.Sup, atom.Sub, atom.Span, atom.Abbr,
		atom.Small, atom.Time, atom.Label, atom.Cite, atom.Q, atom.Mark, atom.U:
		m.write(m.inlineElement(n))
	default:
		if container(n) || paragraph(n) {
			m.flush()
			m.blocks(n)
			m.flush()
			return
		}
		m.blocks(n)
	}
}

// item renders one list item, indented by how many lists are open and numbered
// by the innermost one.
func (m *marker) item(n *html.Node) {
	var l *listing
	if len(m.list) > 0 {
		l = m.list[len(m.list)-1]
	} else {
		l = &listing{}
	}
	l.n++

	bullet := "- "
	if l.ordered {
		bullet = fmt.Sprintf("%d. ", l.n)
	}
	indent := strings.Repeat("  ", max(len(m.list)-1, 0))

	// The item's own blocks are rendered on their own, so a paragraph or a
	// nested list inside an item comes out as a paragraph or a nested list
	// rather than being flattened into the bullet.
	inner := &marker{base: m.base, skip: m.skip, list: m.list}
	inner.blocks(n)
	inner.flush()
	if len(inner.out) == 0 {
		return
	}
	for i, b := range inner.out {
		lines := strings.Split(b, "\n")
		for j, l := range lines {
			switch {
			case i == 0 && j == 0:
				lines[j] = indent + bullet + l
			default:
				lines[j] = indent + "  " + l
			}
		}
		inner.out[i] = strings.Join(lines, "\n")
	}
	m.emit(strings.Join(inner.out, "\n\n"))
}

// table renders a table as a pipe table.
//
// Markdown has no way to say that a table has no header, so the first row
// becomes the header whether or not it was one. That is a lie about a handful of
// tables and the alternative is either dropping the first row or emitting
// something no markdown reader understands.
//
// A table of fewer than two rows, or one whose cells are all empty, renders as
// nothing. Layout tables were how a whole generation of pages was built, and a
// corpus full of one row tables holding a logo and a menu is worse than one that
// quietly leaves them out. Two rows is also what the syntax needs, since a
// markdown table is a header and a body and a table with only a header is a
// header with nothing under it.
func (m *marker) table(n *html.Node) string {
	var rows [][]string
	width := 0
	var walkRows func(*html.Node)
	walkRows = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || m.skip(c) {
				continue
			}
			if c.DataAtom == atom.Tr {
				var row []string
				for d := c.FirstChild; d != nil; d = d.NextSibling {
					if d.Type != html.ElementNode || m.skip(d) {
						continue
					}
					if d.DataAtom != atom.Td && d.DataAtom != atom.Th {
						continue
					}
					row = append(row, strings.ReplaceAll(m.inline(d), "|", "\\|"))
				}
				if len(row) > 0 {
					rows = append(rows, row)
					width = max(width, len(row))
				}
				continue
			}
			walkRows(c)
		}
	}
	walkRows(n)

	if len(rows) < 2 || width == 0 {
		return ""
	}
	filled := false
	for _, row := range rows {
		for _, cell := range row {
			if cell != "" {
				filled = true
			}
		}
	}
	if !filled {
		return ""
	}

	var b strings.Builder
	line := func(cells []string) {
		b.WriteString("|")
		for i := range width {
			b.WriteString(" ")
			if i < len(cells) {
				b.WriteString(cells[i])
			}
			b.WriteString(" |")
		}
		b.WriteString("\n")
	}
	line(rows[0])
	b.WriteString("|")
	for range width {
		b.WriteString(" --- |")
	}
	for _, row := range rows[1:] {
		b.WriteString("\n")
		line(row)
	}
	return strings.TrimRight(b.String(), "\n")
}

// inline renders a subtree as one run of inline markdown.
func (m *marker) inline(n *html.Node) string {
	var b strings.Builder
	add := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), " ") && !strings.HasPrefix(s, " ") {
			b.WriteByte(' ')
		}
		b.WriteString(s)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			add(escape(squeeze(c.Data)))
		case html.ElementNode:
			if m.skip(c) {
				continue
			}
			add(m.inlineElement(c))
		}
	}
	return strings.TrimSpace(b.String())
}

func (m *marker) inlineElement(n *html.Node) string {
	switch n.DataAtom {
	case atom.Br:
		return " "
	case atom.Img:
		return m.image(n)
	case atom.A:
		inner := m.inline(n)
		href := m.resolve(attr(n, "href"))
		// A link with no text is a link to nothing a reader can click, and a
		// link to nothing resolvable is just its text.
		if href == "" || inner == "" {
			return inner
		}
		return "[" + inner + "](" + href + ")"
	case atom.Strong, atom.B:
		return wrap(m.inline(n), "**")
	case atom.Em, atom.I, atom.Cite:
		return wrap(m.inline(n), "*")
	case atom.Del, atom.S, atom.Strike:
		return wrap(m.inline(n), "~~")
	case atom.Code, atom.Kbd, atom.Samp:
		if s := squeeze(text(n)); s != "" {
			return "`" + strings.ReplaceAll(s, "`", "") + "`"
		}
		return ""
	}
	return m.inline(n)
}

// image renders an img, taking the alt text when there is one and saying
// nothing about the picture when there is not.
func (m *marker) image(n *html.Node) string {
	src := m.resolve(attr(n, "src"))
	if src == "" {
		// Lazy loading puts the real address somewhere else on a large share of
		// the sites this crawl meets, and these two attributes are where.
		for _, k := range []string{"data-src", "data-original"} {
			if src = m.resolve(attr(n, k)); src != "" {
				break
			}
		}
	}
	if src == "" {
		return ""
	}
	return "![" + escape(squeeze(attr(n, "alt"))) + "](" + src + ")"
}

// resolve makes a URL absolute, and returns nothing for the ones that are not
// addresses a reader can follow.
func (m *marker) resolve(href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	if m.base == nil {
		return href
	}
	u, err := m.base.Parse(href)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "http", "https", "mailto", "tel":
	default:
		return ""
	}
	// A URL with a bracket or a space in it breaks the link syntax around it,
	// and percent encoding is what those characters are supposed to be anyway.
	s := u.String()
	for _, bad := range []string{" ", "(", ")", "[", "]"} {
		if strings.Contains(s, bad) {
			return url.QueryEscape(s)
		}
	}
	return s
}

// wrap puts emphasis markers around text, and leaves empty text alone rather
// than emitting the markers with nothing between them.
func wrap(s, with string) string {
	if s == "" {
		return ""
	}
	return with + s + with
}

// fence puts preformatted text in a code block, choosing a fence the text does
// not already contain.
func fence(s string) string {
	s = strings.Trim(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if s == "" {
		return ""
	}
	f := "```"
	for strings.Contains(s, f) {
		f += "`"
	}
	return f + "\n" + s + "\n" + f
}

// escape puts a backslash in front of the characters that would otherwise be
// markup.
//
// The list is short on purpose. Escaping everything the specification allows to
// be escaped turns ordinary Vietnamese prose into a thicket of backslashes,
// since a dash and a full stop are both markup in the right position and neither
// is markup in the middle of a sentence. What is escaped here is the characters
// that change the meaning of a line wherever they appear in it.
func escape(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', '`', '*', '_', '[', ']', '<', '>':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	out := b.String()
	// And at the start of a line, the characters that make a block out of it.
	switch {
	case strings.HasPrefix(out, "#"), strings.HasPrefix(out, "|"),
		strings.HasPrefix(out, "-"), strings.HasPrefix(out, "+"),
		strings.HasPrefix(out, "="):
		out = "\\" + out
	}
	return out
}
