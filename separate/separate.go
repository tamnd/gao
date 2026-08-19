// Package separate separates what a page says from what it is wrapped in.
//
// Tách is to separate, as in tách vỏ trấu, taking the husk off the grain. It is
// the same operation on a web page and it fails in the same way: take too
// little off and the corpus is menus, take too much off and the corpus is
// nothing.
//
// This package is the forum handler specifically. Generic article extraction is
// tuned for a page with one body of text on it, and a forum thread is thirty
// bodies of text with a menu around each one. Run a generic extractor over a
// thread and it picks the largest single block, which on most forum software is
// the sidebar, and throws away the posts. That is why forums are the target
// class in this project's crawl plan and also the class every general crawler
// handles worst, and the two facts are the same fact.
//
// Three decisions in here are worth arguing with.
//
// The posts are found structurally rather than by recognizing forum software. A
// thread is a run of sibling elements that share a tag and a class and each hold
// real text, which is true of phpBB, XenForo, Discourse, vBulletin, and of the
// hand rolled PHP that a surprising amount of Vietnamese forum traffic still
// runs on. A list of class names for known engines would be shorter to write and
// would age into a list of engines nobody uses.
//
// Quoted text is removed rather than kept. In a thread where each reply quotes
// the post above it, the same sentences appear three and four times, and a
// corpus built from that has its own duplicates baked inside single documents
// where deduplication cannot see them. It would be found later as a thread that
// deduplicates to nothing, which is the expensive way to find it.
//
// A line that appears verbatim in more than one post is dropped. That is what a
// signature is, and it is also what "Sent from my phone" is, and per post
// navigation, without needing a rule for each. The cost is that a thread where
// everybody replies with the same two words yields nothing, which is the right
// answer for that thread.
package separate

import (
	"fmt"
	"strings"
)

// A Post is one message in a thread.
type Post struct {
	// Index is the position in the thread, from one, kept because a thread is a
	// conversation and the order is part of what it says.
	Index int `json:"index"`

	// Author is who wrote it when the page says so plainly, and empty when it
	// does not. Nothing here depends on it being right.
	Author string `json:"author,omitempty"`

	// Text is the post with its quotes and its repeated lines taken out.
	Text string `json:"text"`

	// Quoted is how many characters of quoted text were removed, which is worth
	// keeping because a thread that is mostly quotation is a thread whose
	// remaining text is worth suspecting.
	Quoted int `json:"quoted"`
}

// A Thread is a forum page read as the conversation it is.
type Thread struct {
	Title string `json:"title,omitempty"`
	Posts []Post `json:"posts"`

	// Dropped is how many characters the page held that no post kept: the
	// navigation, the sidebar, the footer, and the quotes. It is reported rather
	// than discarded because an extractor that is throwing away four fifths of
	// every page is either working perfectly or badly broken, and the number
	// alone does not say which.
	Dropped int `json:"dropped"`

	// Repeated is how many lines were dropped for appearing in more than one
	// post, which is the signature count under a name that does not claim to
	// know why the line repeated.
	Repeated int `json:"repeated"`
}

// Text is the thread as one document, posts separated by a blank line.
func (t Thread) Text() string {
	parts := make([]string, 0, len(t.Posts))
	for _, p := range t.Posts {
		parts = append(parts, p.Text)
	}
	return strings.Join(parts, "\n\n")
}

// Chars is how much text the thread kept.
func (t Thread) Chars() int {
	n := 0
	for _, p := range t.Posts {
		n += len([]rune(p.Text))
	}
	return n
}

// Quoted is how much quotation was taken out of it.
func (t Thread) Quoted() int {
	n := 0
	for _, p := range t.Posts {
		n += p.Quoted
	}
	return n
}

// Yield is the share of the page's text that survived as posts. On a thread
// this sits well under half, and that is the point: the rest was the menu.
func (t Thread) Yield() float64 {
	total := t.Chars() + t.Dropped
	if total == 0 {
		return 0
	}
	return float64(t.Chars()) / float64(total)
}

// String renders a thread the way it goes into a log line.
func (t Thread) String() string {
	return fmt.Sprintf("%d posts, %d characters kept, %d dropped, %d of them quoted",
		len(t.Posts), t.Chars(), t.Dropped, t.Quoted())
}

// Options are the floors a run of siblings has to clear to be read as posts.
type Options struct {
	// MinPosts is how many siblings have to share a shape before the run is a
	// thread. Two is the floor because one post is an article and this package
	// is not an article extractor.
	MinPosts int

	// MinChars is the shortest a post can be and still count toward the run. A
	// row of "thanks" replies is a real thing on a real forum and it is not
	// text worth training on. Pass one to take every post there is, since an
	// empty post is dropped whatever this says.
	MinChars int

	// MaxDepth is how far down the tree to look for the run. It exists to stop
	// the search rather than to express a belief about markup.
	MaxDepth int
}

// Default is what the ingest runs with.
func Default() Options {
	return Options{MinPosts: 2, MinChars: 40, MaxDepth: 32}
}

// or fills in whatever the caller left at zero, so that the zero Options is the
// default one rather than a set of floors nobody meant to ask for.
func (o Options) or(d Options) Options {
	if o.MinPosts <= 0 {
		o.MinPosts = d.MinPosts
	}
	if o.MinChars <= 0 {
		o.MinChars = d.MinChars
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = d.MaxDepth
	}
	return o
}

// A Report is what a run of pages came to.
//
// The routing distribution is the number this slice is costed on, so the pages
// that were not threads are counted rather than skipped. An extractor that
// silently declines half its input looks identical to one that had half as much
// input.
type Report struct {
	Pages   int `json:"pages"`
	Threads int `json:"threads"`
	Posts   int `json:"posts"`

	Kept     int `json:"kept"`
	Dropped  int `json:"dropped"`
	QuotedCh int `json:"quoted"`
	Repeated int `json:"repeated"`
}

// Add folds one page in, thread or not.
func (r *Report) Add(t *Thread) {
	r.Pages++
	if t == nil {
		return
	}
	r.Threads++
	r.Posts += len(t.Posts)
	r.Kept += t.Chars()
	r.Dropped += t.Dropped
	r.QuotedCh += t.Quoted()
	r.Repeated += t.Repeated
}

// Yield is the share of all the text seen that was kept as posts.
func (r Report) Yield() float64 {
	total := r.Kept + r.Dropped
	if total == 0 {
		return 0
	}
	return float64(r.Kept) / float64(total)
}

// ThreadShare is the share of pages that read as threads at all.
func (r Report) ThreadShare() float64 {
	if r.Pages == 0 {
		return 0
	}
	return float64(r.Threads) / float64(r.Pages)
}

// String renders the report the way it gets published.
func (r Report) String() string {
	return fmt.Sprintf("%d pages, %d threads (%.0f%%), %d posts, %d characters kept of %d (%.0f%% yield), %d quoted, %d repeated lines dropped",
		r.Pages, r.Threads, 100*r.ThreadShare(), r.Posts, r.Kept, r.Kept+r.Dropped, 100*r.Yield(), r.QuotedCh, r.Repeated)
}
