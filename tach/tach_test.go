package tach

import (
	"strings"
	"testing"
)

func TestAThreadIsOneDocumentWithTheOrderKept(t *testing.T) {
	th := Thread{Posts: []Post{
		{Index: 1, Text: "Câu hỏi."},
		{Index: 2, Text: "Câu trả lời."},
	}}
	if got, want := th.Text(), "Câu hỏi.\n\nCâu trả lời."; got != want {
		t.Errorf("the thread reads as %q, want %q", got, want)
	}
	// Eight characters and twelve, and the blank line between them is not text
	// the page held.
	if got := th.Chars(); got != 20 {
		t.Errorf("the thread is %d characters, want 20", got)
	}
}

func TestCharactersAreCountedAsRunesRatherThanBytes(t *testing.T) {
	// Every one of these is one character to a reader and three bytes to Go, and
	// a corpus sized in bytes overstates Vietnamese by about half.
	th := Thread{Posts: []Post{{Text: "ưỡng"}}}
	if got := th.Chars(); got != 4 {
		t.Errorf("counted %d characters in a four character word", got)
	}
	if len("ưỡng") == 4 {
		t.Fatal("the test word stopped being multibyte, so this test proves nothing")
	}
}

func TestAnEmptyThreadYieldsNothingRatherThanDividingByZero(t *testing.T) {
	var th Thread
	if got := th.Yield(); got != 0 {
		t.Errorf("an empty thread yielded %v", got)
	}
	var r Report
	if got := r.Yield(); got != 0 {
		t.Errorf("an empty report yielded %v", got)
	}
	if got := r.ThreadShare(); got != 0 {
		t.Errorf("an empty report has a thread share of %v", got)
	}
}

func TestYieldIsTheShareOfThePageThatWasThread(t *testing.T) {
	th := Thread{Posts: []Post{{Text: strings.Repeat("a", 300)}}, Dropped: 700}
	if got := th.Yield(); got != 0.3 {
		t.Errorf("300 characters kept out of 1000 is a yield of %v", got)
	}
}

func TestTheReportCountsThePagesThatWereNotThreads(t *testing.T) {
	var r Report
	r.Add(&Thread{Posts: []Post{{Text: "một"}, {Text: "hai"}}, Dropped: 24, Repeated: 2})
	r.Add(nil)
	r.Add(nil)

	if r.Pages != 3 {
		t.Errorf("the report saw %d pages, want 3", r.Pages)
	}
	if r.Threads != 1 {
		t.Errorf("the report counted %d threads, want 1", r.Threads)
	}
	if got := r.ThreadShare(); got < 0.33 || got > 0.34 {
		t.Errorf("one thread in three pages is a share of %v", got)
	}
	if r.Posts != 2 {
		t.Errorf("the report counted %d posts, want 2", r.Posts)
	}
	if r.Kept != 6 || r.Dropped != 24 {
		t.Errorf("the report kept %d and dropped %d, want 6 and 24", r.Kept, r.Dropped)
	}
	if got := r.Yield(); got != 0.2 {
		t.Errorf("6 characters kept of 30 is a yield of %v", got)
	}
	if r.Repeated != 2 {
		t.Errorf("the report counted %d repeated lines, want 2", r.Repeated)
	}
}

func TestQuotationIsCountedAcrossThePostsAndAcrossTheRun(t *testing.T) {
	th := &Thread{Posts: []Post{{Text: "một", Quoted: 40}, {Text: "hai", Quoted: 60}}}
	if got := th.Quoted(); got != 100 {
		t.Errorf("the thread counted %d characters of quotation, want 100", got)
	}
	var r Report
	r.Add(th)
	r.Add(th)
	if r.QuotedCh != 200 {
		t.Errorf("the report counted %d characters of quotation, want 200", r.QuotedCh)
	}
}

func TestTheDefaultsAreTheFloorsAndZeroMeansTakeTheDefault(t *testing.T) {
	d := Default()
	if got := (Options{}).or(d); got != d {
		t.Errorf("an empty Options came back as %+v, want %+v", got, d)
	}
	if got := (Options{MinPosts: 5}).or(d); got.MinPosts != 5 || got.MinChars != d.MinChars || got.MaxDepth != d.MaxDepth {
		t.Errorf("setting one floor changed the others: %+v", got)
	}
	// Taking every post there is has to be sayable, and it is said with one
	// rather than with zero, because zero is what a caller who said nothing
	// leaves behind and those two are not the same request.
	if got := (Options{MinChars: 1}).or(d); got.MinChars != 1 {
		t.Errorf("a floor of one came back as %d", got.MinChars)
	}
}

func TestAThreadAndAReportSayWhatTheyAreInOneLine(t *testing.T) {
	th := &Thread{Posts: []Post{{Text: "một", Quoted: 10}, {Text: "hai"}}, Dropped: 44}
	if got, want := th.String(), "2 posts, 6 characters kept, 44 dropped, 10 of them quoted"; got != want {
		t.Errorf("the thread logs as %q, want %q", got, want)
	}

	var r Report
	r.Add(th)
	r.Add(nil)
	want := "2 pages, 1 threads (50%), 2 posts, 6 characters kept of 50 (12% yield), 10 quoted, 0 repeated lines dropped"
	if got := r.String(); got != want {
		t.Errorf("the report logs as %q, want %q", got, want)
	}
}
