package tach

// Reading a forum page as the thread it is.

import (
	"bytes"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Forum reads a page as a thread, and reports whether it is one.
//
// False means the page holds no run of sibling containers each carrying real
// text, which is what a thread looks like from the outside no matter which
// software served it. An article, a product page, and a forum index all come
// back false, and that is the routing decision rather than a failure.
func Forum(page []byte, o Options) (*Thread, bool) {
	o = o.or(Default())

	root, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		// x/net/html does not reject markup, so this is an unreadable reader
		// rather than an unreadable page, and either way there is no thread.
		return nil, false
	}

	// The page's text before anything structural is thrown away. Scripts and
	// styles are not text in any sense, so they are out of the denominator
	// rather than counted as something the extractor dropped.
	strip(root, func(n *html.Node) bool {
		switch n.DataAtom {
		case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg, atom.Iframe:
			return true
		}
		return false
	})
	total := textLen(root)

	title := titleOf(root)

	// The chrome goes next. These elements are what they are by the standard
	// rather than by convention, which is the only reason a tag list is
	// acceptable here when a class list would not be.
	strip(root, func(n *html.Node) bool {
		switch n.DataAtom {
		case atom.Nav, atom.Header, atom.Footer, atom.Aside, atom.Form, atom.Button, atom.Select, atom.Label:
			return true
		}
		return n.Type == html.ElementNode && attr(n, "role") == "navigation"
	})

	run := findRun(root, o)
	if run == nil {
		return nil, false
	}

	posts := make([]Post, 0, len(run))
	lines := make([][]string, 0, len(run))
	for _, n := range run {
		p := readPost(n)
		if len(p.lines) == 0 || mostlyLinks(p.chars, p.links) {
			continue
		}
		posts = append(posts, Post{Author: p.author, Quoted: p.quoted})
		lines = append(lines, p.lines)
	}
	if len(posts) < o.MinPosts {
		return nil, false
	}

	repeated := 0
	seen := map[string]int{}
	for _, ls := range lines {
		for _, l := range unique(ls) {
			seen[l]++
		}
	}

	thread := &Thread{Title: title}
	for i, ls := range lines {
		kept := make([]string, 0, len(ls))
		for _, l := range ls {
			if seen[l] > 1 {
				repeated++
				continue
			}
			kept = append(kept, l)
		}
		if len(kept) == 0 {
			// Everything this post said, another post said too. That is a
			// signature, or a greeting, and either way it is not the post.
			continue
		}
		p := posts[i]
		p.Index = len(thread.Posts) + 1
		p.Text = strings.Join(kept, "\n")
		thread.Posts = append(thread.Posts, p)
	}
	if len(thread.Posts) < o.MinPosts {
		return nil, false
	}

	thread.Repeated = repeated
	if d := total - thread.Chars(); d > 0 {
		thread.Dropped = d
	}
	return thread, true
}

// findRun returns the sibling elements that look like posts, or nil.
//
// The run with the most text wins, and a tie goes to the longer run, because a
// page whose sidebar happens to hold as many characters as its thread is a page
// where the thread is the one with more pieces in it.
func findRun(root *html.Node, o Options) []*html.Node {
	var best []*html.Node
	var bestChars int

	var walk func(n *html.Node, depth int)
	walk = func(n *html.Node, depth int) {
		if depth > o.MaxDepth {
			return
		}
		for _, group := range groups(n) {
			chars := 0
			qualifying := 0
			for _, m := range group {
				p := readPost(m)
				if p.chars >= o.MinChars && !mostlyLinks(p.chars, p.links) {
					qualifying++
					chars += p.chars
				}
			}
			if qualifying < o.MinPosts {
				continue
			}
			if chars > bestChars || (chars == bestChars && len(group) > len(best)) {
				best, bestChars = group, chars
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				walk(c, depth+1)
			}
		}
	}
	walk(root, 0)
	return best
}

// groups partitions a node's element children by shape, in document order.
//
// Only containers are considered. A run of sibling paragraphs is an article and
// a run of sibling list items in a menu is a menu, and neither is a thread, so
// the shape a post has to have is the shape a post has: a box with things in it.
func groups(n *html.Node) [][]*html.Node {
	var order []string
	by := map[string][]*html.Node{}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || !container(c) {
			continue
		}
		k := shape(c)
		if _, ok := by[k]; !ok {
			order = append(order, k)
		}
		by[k] = append(by[k], c)
	}
	out := make([][]*html.Node, 0, len(order))
	for _, k := range order {
		if len(by[k]) > 1 {
			out = append(out, by[k])
		}
	}
	return out
}

func container(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Div, atom.Li, atom.Article, atom.Section, atom.Tr, atom.Dd:
		return true
	}
	return false
}

// shape is the tag and the class list, which is what "the same kind of box"
// means in every forum template anybody has written.
func shape(n *html.Node) string {
	cls := strings.Fields(strings.ToLower(attr(n, "class")))
	sort.Strings(cls)
	return n.Data + "|" + strings.Join(cls, " ")
}

// A reading is one post container taken apart.
type reading struct {
	author string
	lines  []string
	chars  int

	// quoted is the characters of somebody else's post that were removed.
	quoted int

	// links is how much of what is left sits inside an anchor. It is the one
	// number that tells a post apart from a row of a forum index, which has the
	// same shape and repeats down the page exactly the way posts do. A post is
	// prose with the occasional link in it and an index row is a link with a
	// reply count next to it, and nothing else about the markup says so.
	links int
}

// readPost takes one candidate container apart.
//
// The byline is dropped from the text rather than left in it, because it sits
// inside the post container on every forum template and it is not something the
// poster wrote.
func readPost(n *html.Node) reading {
	r := reading{lines: []string{}}
	skip := authorNode(n)
	if skip != nil {
		r.author = firstLine(skip)
		if b := bylineBlock(n, skip); b != nil {
			skip = b
		}
	}

	var l liner
	var walk func(n *html.Node, inLink bool)
	walk = func(n *html.Node, inLink bool) {
		switch n.Type {
		case html.TextNode:
			if inLink {
				r.links += runeLen(strings.Join(strings.Fields(n.Data), " "))
			}
			l.write(n.Data)
			return
		case html.ElementNode:
			if n == skip {
				return
			}
			if quotation(n) {
				r.quoted += textLen(n)
				return
			}
			if n.DataAtom == atom.A {
				inLink = true
			}
			if breaks(n) {
				l.brk()
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inLink)
		}
		if n.Type == html.ElementNode && breaks(n) {
			l.brk()
		}
	}
	walk(n, false)

	r.lines = l.done()
	r.chars = runeLen(strings.Join(r.lines, "\n"))
	return r
}

// mostlyLinks reports whether a candidate is a navigation row wearing a post's
// shape. Half is the line because a post that is more anchor than sentence is
// not text worth training on even when it is genuinely a post.
func mostlyLinks(chars, links int) bool {
	return chars > 0 && links*2 > chars
}

// quotation reports whether an element is somebody else's words inside this
// post. The tag is the standard's answer and the class is every forum's answer,
// and this consults both because threads are full of each.
func quotation(n *html.Node) bool {
	if n.DataAtom == atom.Blockquote {
		return true
	}
	for c := range strings.FieldsSeq(strings.ToLower(attr(n, "class"))) {
		if strings.Contains(c, "quote") {
			return true
		}
	}
	return false
}

func breaks(n *html.Node) bool {
	switch n.DataAtom {
	case atom.P, atom.Div, atom.Li, atom.Ul, atom.Ol, atom.Br, atom.Tr, atom.Td, atom.Th,
		atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
		atom.Section, atom.Article, atom.Pre, atom.Hr, atom.Dd, atom.Dt, atom.Table:
		return true
	}
	return false
}

// authorNode is the element naming who wrote the post, or nil.
//
// This is the one place a class name is read for meaning, and nothing depends
// on it: an author the page did not name plainly is left empty rather than
// guessed at, because a wrong name attached to a quote is worse than no name.
func authorNode(n *html.Node) *html.Node {
	var found *html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if found != nil || n == nil {
			return
		}
		if n.Type == html.ElementNode && names(n) && firstLine(n) != "" {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

// bylineBlock is the box the byline sits in, when that box holds nothing but
// the byline, and nil otherwise.
//
// Every forum template puts the poster's name in a small block with the join
// date, the post count, and the rank beside it. The repeated line rule takes out
// whatever of that repeats down the page and leaves whatever does not, so "Bài
// viết: 318" survives into the post of everybody who has posted once, which is
// most of a forum. The block goes as a block instead, on the one condition that
// nothing in it runs as long as a sentence. That condition is what tells a
// profile box apart from a post that happens to open with a name, and getting it
// wrong in the safe direction costs a line of furniture rather than a post.
func bylineBlock(post, author *html.Node) *html.Node {
	if author == nil || author == post || author.Parent == nil || author.Parent == post {
		return nil
	}
	for _, l := range lines(author.Parent) {
		if runeLen(l) > bylineLine {
			return nil
		}
	}
	return author.Parent
}

// bylineLine is how long a line in a profile box can be. Names, dates, and
// counters are all well under it, and anything a person wrote is over it.
const bylineLine = 64

// names reports whether an element is a byline, by what it is called or by
// where it points.
func names(n *html.Node) bool {
	for c := range strings.FieldsSeq(strings.ToLower(attr(n, "class"))) {
		switch c {
		case "author", "username", "user-name", "poster", "postername":
			return true
		}
	}
	if n.DataAtom != atom.A {
		return false
	}
	href := strings.ToLower(attr(n, "href"))
	for _, p := range []string{"/user", "/member", "/profile", "/u/", "/thanh-vien"} {
		if strings.Contains(href, p) {
			return true
		}
	}
	return false
}

func firstLine(n *html.Node) string {
	ls := lines(n)
	if len(ls) == 0 {
		return ""
	}
	s := ls[0]
	if r := []rune(s); len(r) > 64 {
		s = string(r[:64])
	}
	return s
}

// titleOf prefers the heading over the document title, because a forum's title
// tag carries the board name, the forum name, and often the page number, and
// none of that is what the thread is about.
func titleOf(root *html.Node) string {
	var h1, title string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.H1:
				if h1 == "" {
					h1 = firstLine(n)
				}
			case atom.Title:
				if title == "" {
					title = firstLine(n)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if h1 != "" {
		return h1
	}
	return title
}

// strip removes every node the predicate accepts, subtree and all.
func strip(n *html.Node, drop func(*html.Node) bool) {
	c := n.FirstChild
	for c != nil {
		next := c.NextSibling
		if c.Type == html.ElementNode && drop(c) {
			n.RemoveChild(c)
		} else {
			strip(c, drop)
		}
		c = next
	}
}

// lines is the text of a subtree, whitespace collapsed and broken where the
// markup breaks it, with nothing taken out.
func lines(n *html.Node) []string {
	var l liner
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			l.write(n.Data)
		}
		if n.Type == html.ElementNode && breaks(n) {
			l.brk()
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && breaks(n) {
			l.brk()
		}
	}
	walk(n)
	return l.done()
}

// textLen is how many characters of text a subtree holds, whitespace collapsed,
// which is the only measure of a page's size that does not move when somebody
// reformats the markup.
func textLen(n *html.Node) int {
	return runeLen(strings.Join(lines(n), "\n"))
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}

func runeLen(s string) int { return len([]rune(s)) }

// unique is the distinct lines of one post, so that a line repeated inside a
// single post counts once toward the across-post tally. A post that says the
// same thing twice is a post, not a signature.
func unique(lines []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

// A liner turns a walk over text nodes into lines, collapsing the whitespace
// that markup is indented with and that no reader ever sees.
type liner struct {
	lines []string
	cur   []string
}

func (l *liner) write(s string) {
	l.cur = append(l.cur, strings.Fields(s)...)
}

func (l *liner) brk() {
	if len(l.cur) == 0 {
		return
	}
	l.lines = append(l.lines, strings.Join(l.cur, " "))
	l.cur = nil
}

func (l *liner) done() []string {
	l.brk()
	return l.lines
}
