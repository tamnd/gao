package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/husk"
)

func runHusk(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("husk", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	text := fs.Bool("text", false, "print the posts rather than a summary of them")
	furniture := fs.Bool("furniture", false, "print what was dropped as furniture, which is how a bad extraction gets caught")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao husk [-text] [-furniture] [-json] page.html [page.html...]

Peel the posts out of a forum thread and leave the page behind.

Generic article extraction keeps the densest run of text on a page, which is the
right rule for a news article and the wrong one for a thread. A thread is forty
small blocks of prose separated by furniture, none of them dense enough to win,
and the densest single run on the page is usually the sidebar listing the thirty
most recent threads. Run a generic extractor over a forum and it returns the
navigation and drops the conversation, on every page, without saying so.

That is worth a handler of its own because forums are where informal written
Vietnamese lives. The slang, the regional vocabulary, the code switching and the
sentence shapes people actually use are not in the news archives and they are
not in the government gazettes. Losing forums is not losing volume, it is losing
the register the rest of the corpus does not have.

The method is repetition rather than a list of sites. A thread page has a
repeated element on it and an article page does not, and that is a property of
the page rather than of the forum software, so it holds for vBulletin, XenForo,
phpBB and whatever voz is running this year. A page with no thread in it comes
back saying so and exits 0, because the caller's next move is the generic
extractor and an error would have it dropping the page instead.

With -furniture it prints the repeated lines it removed. Read them: the way this
handler fails is by dropping something that was not a signature, and a count of
what it removed would make that invisible.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	pages := fs.Args()
	if len(pages) == 0 {
		fs.Usage()
		return 2
	}

	lines := make([]huskLine, 0, len(pages))
	for _, name := range pages {
		page, err := readDocument(name)
		if err != nil {
			fmt.Fprintf(stderr, "gao husk: %v\n", err)
			return 1
		}
		t, err := husk.Peel(bytes.NewReader(page))
		if err != nil {
			fmt.Fprintf(stderr, "gao husk: %s: %v\n", name, err)
			return 1
		}
		lines = append(lines, huskLine{Page: name, Thread: t})
	}

	if *asJSON {
		return printJSON(stdout, stderr, huskReport{Pages: lines, Threads: threadsIn(lines)})
	}
	printHusk(stdout, lines, *text, *furniture)
	return 0
}

// huskLine is one page and what came out of it.
type huskLine struct {
	Page string `json:"page"`
	husk.Thread
}

type huskReport struct {
	Pages []huskLine `json:"pages"`

	// Threads is how many of the pages held a conversation, which on a crawl of
	// a forum should be most of them and is the number that says the handler has
	// stopped working when the site changes its template.
	Threads int `json:"threads"`
}

func threadsIn(lines []huskLine) int {
	var n int
	for _, l := range lines {
		if l.Ok() {
			n++
		}
	}
	return n
}

func printHusk(w io.Writer, lines []huskLine, text, furniture bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "page\tposts\trunes\tquoted\tskipped\tshape\n")
	for _, l := range lines {
		if !l.Ok() {
			fmt.Fprintf(tw, "%s\t.\t.\t.\t.\tno thread\n", l.Page)
			continue
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%d\t%s\n",
			l.Page, len(l.Posts), l.Runes(), percent(l.Quoted()), l.Skipped, l.Shape)
	}
	_ = tw.Flush()

	for _, l := range lines {
		if l.Ok() && !text && !furniture {
			continue
		}
		fmt.Fprintf(w, "\n%s\n", l.Page)
		if !l.Ok() {
			fmt.Fprintf(w, "  %s\n", l.Why)
			continue
		}
		if text {
			fmt.Fprintf(w, "  %s\n", l.Title)
			for _, p := range l.Posts {
				fmt.Fprintf(w, "\n  [%d] %s\n", p.Index, byline(p))
				fmt.Fprintf(w, "  %s\n", p.Text)
				if p.Quoted > 0 {
					fmt.Fprintf(w, "  (%d runes of quotation taken out)\n", p.Quoted)
				}
			}
		}
		if furniture && len(l.Furniture) > 0 {
			fmt.Fprint(w, "\n  dropped as furniture:\n")
			for _, f := range l.Furniture {
				fmt.Fprintf(w, "    %s\n", f)
			}
		}
	}
}

// byline is the author and the time, with the parts the page did not say left
// out rather than filled in with a placeholder that reads like a value.
func byline(p husk.Post) string {
	switch {
	case p.Author != "" && p.At != "":
		return p.Author + ", " + p.At
	case p.Author != "":
		return p.Author
	case p.At != "":
		return p.At
	}
	return "no name and no time on this one"
}
