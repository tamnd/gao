package boc

// Finding the posts.
//
// The whole method is one observation: a thread page has a repeated element on
// it and an article page does not. Forty posts are forty siblings built from the
// same template, so they have the same tag and the same classes and different
// text, and that is a shape a program can see without knowing which forum it is
// looking at. Everything else here is the consequences of that.
//
// The consequences are where the work is. The same repetition test also matches
// the sidebar's list of recent threads, the pagination, and the row of forum
// categories at the top, so a group has to earn the title of post list by
// holding enough text. And the containers hold more than the posts: a signature
// under every message, a Reply button, a Report link, and on the Vietnamese
// forums a quote of the message being replied to, which is text already in the
// corpus and must not enter it a second time.

import (
	"io"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Peel reads a forum page and returns the conversation in it.
//
// A page that is not a thread comes back with Why set rather than as an error,
// because the caller's next move is to hand the page to the generic extractor
// and an error would have it dropping the page instead. An error here means the
// HTML could not be read at all.
func Peel(r io.Reader) (Thread, error) {
	root, err := html.Parse(r)
	if err != nil {
		return Thread{}, err
	}

	title := pageTitle(root)
	found := candidates(root)
	if len(found) == 0 {
		return Thread{
			Title: title,
			Why:   "no element repeats on this page often enough to be a post list, which is what an article looks like",
		}, nil
	}

	// Candidates are tried in order rather than the best one being picked and
	// committed to. A forum page has several repeated elements on it and only
	// one of them is the conversation, and the one with the most text is
	// frequently the sidebar: forty recent thread titles outweigh eight posts on
	// a quiet thread. Trying them in turn means the sidebar losing costs a pass
	// over it rather than the whole page.
	var first Thread
	for i, g := range found {
		t := peelGroup(g)
		t.Title = title
		if t.Ok() {
			return t, nil
		}
		if i == 0 {
			first = t
		}
	}
	return first, nil
}

// peelGroup turns one repeated element into a thread, or into the reason it is
// not one.
func peelGroup(g group) Thread {
	t := Thread{Shape: g.shape}

	raw := make([]post, 0, len(g.nodes))
	for _, n := range g.nodes {
		raw = append(raw, readPost(n))
	}

	t.Furniture = repeated(raw)
	drop := make(map[string]bool, len(t.Furniture))
	for _, f := range t.Furniture {
		drop[f] = true
	}

	for _, p := range raw {
		var kept []string
		for _, line := range p.lines {
			if !drop[line] {
				kept = append(kept, line)
			}
		}
		text := strings.Join(kept, "\n")
		if len([]rune(text)) < MinRunes {
			t.Skipped++
			continue
		}
		t.Posts = append(t.Posts, Post{
			Index:  len(t.Posts),
			Author: p.author,
			At:     p.at,
			Text:   text,
			Quoted: p.quoted,
		})
	}

	// Nothing surviving at all and most of it not surviving are different
	// findings. The first says the repeated element was never a post list, and
	// the second says it might have been one and this handler took it apart
	// wrongly, which is the failure worth being able to tell apart later.
	switch {
	case len(t.Posts) == 0:
		t.Why = "the repeated element on this page holds buttons rather than messages, which is what a category list looks like"
	case float64(len(t.Posts))/float64(len(raw)) < MinKept:
		t.Why = "most of what repeats on this page is furniture, so what was found is not a post list"
	case len(t.Posts) < MinPosts:
		t.Why = "only one message survived here, and one message is a page rather than a conversation"
	}
	if !t.Ok() {
		t.Posts = nil
	}
	return t
}

// post is one container before the furniture is known, since furniture can only
// be recognized by comparing containers against each other.
type post struct {
	author string
	at     string
	lines  []string
	quoted int
}

// group is a run of siblings built from the same template.
type group struct {
	shape string
	nodes []*html.Node
	runes int
}

// candidates is every repeated element on the page that holds enough text to be
// a post list, the most text first.
//
// Most text rather than most members, because the sidebar usually has more
// members than the thread has posts. Twenty thread titles in a sidebar are
// twenty containers of six words each, and eight posts are eight containers of
// eighty, and the second is the conversation. Ordering rather than choosing,
// because that heuristic is right most of the time and not all of it.
func candidates(root *html.Node) []group {
	var out []group
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for _, g := range groups(n) {
			if g.runes >= MinRunes*MinPosts {
				out = append(out, g)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	sort.SliceStable(out, func(i, j int) bool { return out[i].runes > out[j].runes })
	return out
}

// groups buckets a node's element children by shape and keeps the buckets big
// enough to be a list of something.
func groups(parent *html.Node) []group {
	byShape := make(map[string][]*html.Node)
	var order []string
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || skip(c) {
			continue
		}
		s := shape(c)
		if _, seen := byShape[s]; !seen {
			order = append(order, s)
		}
		byShape[s] = append(byShape[s], c)
	}

	out := make([]group, 0, len(order))
	for _, s := range order {
		nodes := byShape[s]
		if len(nodes) < MinPosts {
			continue
		}
		g := group{shape: s, nodes: nodes}
		for _, n := range nodes {
			g.runes += len([]rune(squeeze(textOf(n))))
		}
		out = append(out, g)
	}
	return out
}

// shape is the identity a template gives its instances: the tag and the classes,
// with the digits taken out.
//
// The digits are the point. Forum software writes id="post-118432" and
// class="message message--post js-post-118432", so the classes that carry a post
// number differ on every post and the template is invisible until they are
// folded together. Sorted, because class order is not meaningful and two posts
// written by two different code paths of the same forum can disagree about it.
func shape(n *html.Node) string {
	var classes []string
	for _, a := range n.Attr {
		if a.Key != "class" {
			continue
		}
		for c := range strings.FieldsSeq(a.Val) {
			c = strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return -1
				}
				return r
			}, c)
			if c = strings.Trim(c, "-_"); c != "" {
				classes = append(classes, c)
			}
		}
	}
	sort.Strings(classes)
	classes = dedupe(classes)
	if len(classes) == 0 {
		return n.Data
	}
	return n.Data + "." + strings.Join(classes, ".")
}

// skip reports whether an element is never part of a page's text.
func skip(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg, atom.Head:
		return true
	}
	return false
}

// readPost pulls one container apart: the author and the time from the markup,
// the quotes out of the text, and the rest into lines.
//
// The byline does not go into the lines. A name and a timestamp are what the
// forum knows about the message rather than anything the person wrote, and
// leaving them in the text means every post in the corpus opens with a username
// and the phrase "2 giờ trước", which is a pattern a model will learn and it is
// a pattern about the software.
func readPost(root *html.Node) post {
	var p post
	p.author = attr(root, "data-author")

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			if line := squeeze(n.Data); line != "" {
				p.lines = append(p.lines, line)
			}
			return
		case n.Type != html.ElementNode || skip(n):
			return
		case n.DataAtom == atom.Blockquote:
			// A quote is somebody else's post, which is already in the corpus
			// once. Taking it twice inflates the count and defeats the
			// deduplication, because each copy sits in a different document with
			// different text around it and the shingles do not collide.
			p.quoted += len([]rune(squeeze(textOf(n))))
			return
		case n.DataAtom == atom.Time:
			if p.at == "" {
				p.at = attr(n, "datetime")
			}
			return
		}

		if n != root {
			if name := authorOf(n); name != "" {
				if p.author == "" {
					p.author = name
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return p
}

// authorOf reads a display name off an element, from markup that means it.
//
// The three attribute forms are microdata and they are what a forum emits when
// it wants a search engine to know who wrote something. The class name is the
// concession: XenForo and vBulletin both use it, it is the single most common
// spelling on Vietnamese forums, and without it most pages come back with no
// names at all. Guessing beyond that is where a wrong name gets attached to an
// opinion, so it stops here.
func authorOf(n *html.Node) string {
	for _, a := range n.Attr {
		switch {
		case a.Key == "data-author" && a.Val != "":
			return squeeze(a.Val)
		case a.Key == "itemprop" && a.Val == "name",
			a.Key == "rel" && a.Val == "author",
			a.Key == "class" && hasClass(a.Val, "username"):
			if s := squeeze(textOf(n)); s != "" {
				return s
			}
		}
	}
	return ""
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return squeeze(a.Val)
		}
	}
	return ""
}

// repeated is the text that appears in at least half the containers, which is
// the definition of furniture that does not need a list of sites behind it.
//
// A signature is under every post by the same author, a Reply button is under
// every post by anybody, and both are indistinguishable from a sentence until
// you notice they occur forty times on one page. Half rather than all, because
// the first post of a thread is often laid out differently from the replies.
func repeated(posts []post) []string {
	if len(posts) < MinPosts {
		return nil
	}
	count := make(map[string]int)
	var order []string
	for _, p := range posts {
		for _, line := range dedupe(sortedCopy(p.lines)) {
			if count[line] == 0 {
				order = append(order, line)
			}
			count[line]++
		}
	}

	need := max((len(posts)+1)/2, MinPosts)
	var out []string
	for _, line := range order {
		if count[line] >= need {
			out = append(out, line)
		}
	}
	return out
}

// pageTitle is the thread title.
//
// It comes from the first h1 when the page has one, because that is the thread
// and nothing else, and from the document title otherwise. A document title
// carries the site name on the end after a separator, and the last segment is
// dropped when it is short enough to be a site name and there is more than one
// segment. Vietnamese thread titles contain dashes constantly, so only the last
// segment is ever a candidate and only when it is short.
func pageTitle(root *html.Node) string {
	if h1 := find(root, atom.H1); h1 != nil {
		if s := squeeze(textOf(h1)); s != "" {
			return s
		}
	}
	t := find(root, atom.Title)
	if t == nil {
		return ""
	}
	s := squeeze(textOf(t))
	for _, sep := range []string{" | ", " :: ", " - ", " – "} {
		parts := strings.Split(s, sep)
		if len(parts) < 2 {
			continue
		}
		last := parts[len(parts)-1]
		if len([]rune(last)) <= 40 {
			return strings.TrimSpace(strings.Join(parts[:len(parts)-1], sep))
		}
	}
	return s
}

func find(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := find(c, a); got != nil {
			return got
		}
	}
	return nil
}

// textOf is every text node under n, with the parts that are never text left
// out.
func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
			return
		}
		if n.Type == html.ElementNode && skip(n) {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func hasClass(val, want string) bool {
	for c := range strings.FieldsSeq(val) {
		if strings.Contains(c, want) {
			return true
		}
	}
	return false
}

func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func dedupe(in []string) []string {
	out := in[:0:0]
	var last string
	for i, s := range in {
		if i > 0 && s == last {
			continue
		}
		out = append(out, s)
		last = s
	}
	return out
}
