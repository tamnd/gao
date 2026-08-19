// Package husk peels the posts out of a forum thread and leaves the page behind.
//
// Boc is to husk, which is the one operation in rice processing where what you
// throw away weighs more than what you keep. A forum thread page is the same
// shape. The posts are the text somebody wrote and everything else is the
// software the site runs on, and on most Vietnamese forums the software is more
// bytes than the conversation.
//
// Generic article extraction gets this backwards, and not by a little. It works
// by finding the densest run of text on the page and keeping it, which is the
// right rule for a news article because a news article is one block of prose
// surrounded by furniture. A thread is forty small blocks of prose separated by
// furniture, none of them dense enough to win, and the densest single run on the
// page is frequently the sidebar listing the thirty most recent threads. Run a
// generic extractor over a forum and it returns the navigation and drops the
// conversation, silently, on every page.
//
// That matters here more than it would elsewhere. Forums are where informal
// written Vietnamese lives: the register with the slang, the regional
// vocabulary, the code switching, and the sentence shapes people actually use,
// none of which the news archives and the government gazettes contain in any
// quantity. Losing forums is not losing volume, it is losing the half of the
// language the rest of the corpus does not have.
//
// The handler works from repetition rather than from a list of sites. A thread
// page is a page with a repeated element in it, forty siblings built from the
// same template with different text inside, and finding that repetition is a
// property of the page rather than of the forum software. A selector list for
// vBulletin, XenForo, phpBB, Discourse and whatever voz is running this year is
// a file that is wrong within a year and wrong silently.
package husk

import (
	"fmt"
	"strings"
	"unicode"
)

// The shape a page has to have before there is a thread in it.
const (
	// MinPosts is how many sibling containers make a thread. Two is a question
	// and an answer, which is a thread. One is a page with something repeated
	// twice on it, which is every page.
	MinPosts = 2

	// MinRunes is the shortest run of text that counts as a post rather than as
	// a button. Vietnamese posts are short, so this is low, and the failure it
	// prevents is finding forty nav links and calling them a conversation.
	MinRunes = 24

	// MinKept is the share of a thread's containers that have to survive the
	// furniture stripping before the result is worth keeping. Below it, what was
	// found is a repeated element that is not a post list.
	MinKept = 0.5
)

// A Post is one message in a thread.
type Post struct {
	// Index is the position in the thread as the page presented it, from 0.
	// Threads are read in order and a post that quotes the one above it is
	// unreadable without knowing which one that was.
	Index int

	// Author is the display name, when the page says it in a form that is not a
	// guess. It is empty rather than approximated, because a wrong name attached
	// to an opinion is worse than no name.
	Author string

	// At is the timestamp verbatim as the page wrote it, from a machine readable
	// attribute rather than from the prose next to it. Forums write "2 giờ
	// trước" in the text and the real time in the markup, and the second one is
	// the only one that means anything a week later.
	At string

	// Text is the post, with the quoted material and the furniture out of it.
	Text string

	// Quoted is how many runes of quoted text were removed. It is kept because
	// a post that is 90% quotation and 10% "đúng rồi bác" is a post with almost
	// nothing in it, and the number is how that gets noticed.
	Quoted int
}

// Runes is the length of the post in runes, which is the unit a Vietnamese
// length is measured in here because a byte count says the language is longer
// than it is.
func (p Post) Runes() int { return len([]rune(p.Text)) }

// A Thread is what a forum page turned out to hold.
//
// It is returned whether or not the page was a thread, because "this is not a
// thread" is an answer the caller has to be able to act on and an error that
// says extraction failed is not. Ok reports which of the two happened.
type Thread struct {
	// Title is the thread title, from the page title with the site name taken
	// off the end where the page made that separable.
	Title string

	Posts []Post

	// Shape is the tag and class shape of the container the posts were found
	// in, which is the whole basis of the extraction and belongs in the record
	// so a bad result can be understood rather than just disbelieved.
	Shape string

	// Furniture is the text that repeated across containers and was dropped,
	// verbatim and deduplicated. Signature lines, reply buttons and report links
	// all land here, and it is kept verbatim because the way this handler fails
	// is by dropping something that was not furniture, and a count of what it
	// removed makes that failure invisible.
	Furniture []string

	// Skipped is how many containers of the right shape held too little text to
	// be posts.
	Skipped int

	// Why is why there is no thread here, empty when there is one.
	Why string
}

// Ok reports whether the page held a thread.
func (t Thread) Ok() bool { return t.Why == "" }

// Runes is the length of the conversation, which is the number that says
// whether peeling this page was worth the fetch.
func (t Thread) Runes() int {
	var n int
	for _, p := range t.Posts {
		n += p.Runes()
	}
	return n
}

// Quoted is how much of the thread was people quoting each other, as a share of
// what the page held before the quotes came out.
//
// It is a property of the thread rather than of any post because it is read as a
// judgement about the page: a thread that is two thirds quotation is a thread
// where the same sentences are about to enter the corpus three times each, and
// the deduplication stage is not going to catch it because each copy sits in a
// different document.
func (t Thread) Quoted() float64 {
	var kept, quoted int
	for _, p := range t.Posts {
		kept += p.Runes()
		quoted += p.Quoted
	}
	if kept+quoted == 0 {
		return 0
	}
	return float64(quoted) / float64(kept+quoted)
}

// Describe is the thread in one line, for a log that has to say what a fetch
// produced without printing the fetch.
func (t Thread) Describe() string {
	if !t.Ok() {
		return "no thread: " + t.Why
	}
	return fmt.Sprintf("%s: %d posts, %d runes, %.0f%% of it quotation, out of %s",
		title(t.Title), len(t.Posts), t.Runes(), 100*t.Quoted(), t.Shape)
}

func title(s string) string {
	if s == "" {
		return "untitled"
	}
	return s
}

// squeeze collapses runs of whitespace and trims the ends, which is what turns
// markup indentation into a sentence.
func squeeze(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var space bool
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}
