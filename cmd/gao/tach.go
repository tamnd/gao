package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/tamnd/gao/tach"
)

func runTach(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("tach", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	text := fs.Bool("text", false, "print the extracted thread instead of the measurements")
	minPosts := fs.Int("min-posts", 0, "how many messages a page needs before it is a thread")
	minChars := fs.Int("min-chars", 0, "the shortest a message can be and still count")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: gao tach [-text] [-min-posts n] [-min-chars n] [-json] page.html [page.html...]

Read forum pages as the threads they are.

Generic article extraction is built for a page with one body of text on it. A
forum thread is thirty bodies of text with a menu around each one, so a generic
extractor takes the largest single block, which on most forum software is the
sidebar, and throws the posts away. Forums are the biggest single source of
native Vietnamese prose on the open web and they are the page class every
general crawler handles worst, and those two facts are why this exists.

A page that is not a thread comes back as one, which is a routing answer rather
than a failure. An article is an article and the article handler gets it.

Three things come out of every post and none of them are the post: the
navigation, the quoted text, and any line that appears in more than one post,
which is what a signature is. The counts are printed rather than swallowed,
because an extractor throwing away four fifths of every page is either working
exactly as intended or badly broken and the yield alone does not say which.

With -text it prints the threads themselves, which is what to look at before
trusting any of the numbers.

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

	o := tach.Options{MinPosts: *minPosts, MinChars: *minChars}
	report := tachReport{Pages: make([]tachLine, 0, len(pages))}
	for _, name := range pages {
		page, err := readDocument(name)
		if err != nil {
			fmt.Fprintf(stderr, "gao tach: %v\n", err)
			return 1
		}
		var thread *tach.Thread
		if t, ok := tach.Forum(page, o); ok {
			thread = t
		}
		report.Run.Add(thread)
		report.Pages = append(report.Pages, tachLine{Page: name, Thread: thread})
	}

	if *asJSON {
		if code := printJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else if *text {
		printTachText(stdout, report)
	} else {
		printTach(stdout, report)
	}

	if report.Run.Threads == 0 {
		// Nothing here was a thread. That is a real answer about the input and a
		// pipeline that fed this a directory of forum pages wants to hear it.
		return 1
	}
	return 0
}

// tachLine is one page and what it turned out to be. A page that was not a
// thread keeps its nil rather than being left out of the list, since the share
// of pages that route here and are not threads is the number this is costed on.
type tachLine struct {
	Page   string       `json:"page"`
	Thread *tach.Thread `json:"thread"`
}

type tachReport struct {
	Pages []tachLine  `json:"pages"`
	Run   tach.Report `json:"run"`
}

func printTach(w io.Writer, report tachReport) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "page\tposts\tkept\tdropped\tquoted\trepeated\tyield\tthread\n")
	for _, l := range report.Pages {
		if l.Thread == nil {
			fmt.Fprintf(tw, "%s\t.\t.\t.\t.\t.\t.\t%s\n", l.Page, "not a thread")
			continue
		}
		t := l.Thread
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n",
			l.Page, len(t.Posts), t.Chars(), t.Dropped, t.Quoted(), t.Repeated,
			percent(t.Yield()), ellipsis(t.Title, 48))
	}
	_ = tw.Flush()

	r := report.Run
	fmt.Fprintf(w, "\n%d of %d pages read as threads, holding %d posts and %d characters.\n",
		r.Threads, r.Pages, r.Posts, r.Kept)
	if r.Kept+r.Dropped > 0 {
		fmt.Fprintf(w, "%s of the text on those pages was the thread and the rest was what surrounds it.\n",
			percent(r.Yield()))
	}
	if r.QuotedCh > 0 || r.Repeated > 0 {
		fmt.Fprintf(w, "%d characters of quotation came out, along with %d lines that appeared under more than one post.\n",
			r.QuotedCh, r.Repeated)
	}
}

func printTachText(w io.Writer, report tachReport) {
	for _, l := range report.Pages {
		if l.Thread == nil {
			continue
		}
		if l.Thread.Title != "" {
			fmt.Fprintf(w, "# %s\n\n", l.Thread.Title)
		}
		for _, p := range l.Thread.Posts {
			if p.Author != "" {
				fmt.Fprintf(w, "%s:\n", p.Author)
			}
			fmt.Fprintf(w, "%s\n\n", p.Text)
		}
	}
}
