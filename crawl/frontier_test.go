package crawl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamnd/gao/frontier"
)

func open(t *testing.T, o FrontierOptions) *Frontier {
	t.Helper()
	if o.Dir == "" {
		o.Dir = t.TempDir()
	}
	f, err := OpenFrontier(o)
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func offer(t *testing.T, f *Frontier, urls ...string) {
	t.Helper()
	for _, u := range urls {
		if _, _, err := f.Offer(u); err != nil {
			t.Fatalf("Offer(%s): %v", u, err)
		}
	}
}

func next(t *testing.T, f *Frontier, n int) []string {
	t.Helper()
	got, err := f.Next(n)
	if err != nil {
		t.Fatalf("Next(%d): %v", n, err)
	}
	return got
}

func TestAFrontierHandsBackWhatItWasGiven(t *testing.T) {
	f := open(t, FrontierOptions{})
	offer(t, f, "http://a.example/one", "https://b.example/two", "https://c.example/three")

	got := next(t, f, 10)
	if len(got) != 3 {
		t.Fatalf("Next returned %v", got)
	}
	// The canonical form is what comes back, not the string that was offered.
	want := map[string]bool{
		"http://a.example/one":    true,
		"https://b.example/two":   true,
		"https://c.example/three": true,
	}
	for _, u := range got {
		if !want[u] {
			t.Errorf("Next returned %q, which was not offered", u)
		}
	}
	if s := f.Stats(); s.Admitted != 3 || s.Handed != 3 || s.Queued() != 0 {
		t.Errorf("the stats after three URLs in and three out are %+v", s)
	}
	if rest := next(t, f, 10); len(rest) != 0 {
		t.Errorf("an emptied frontier returned %v", rest)
	}
}

// The seen set is the whole point. A crawl that fetches a URL twice has paid
// twice for a document it will deduplicate later anyway.
func TestAURLOfferedTwiceIsQueuedOnce(t *testing.T) {
	f := open(t, FrontierOptions{})

	ok, _, err := f.Offer("https://a.example/page")
	if err != nil || !ok {
		t.Fatalf("the first offer: %v %v", ok, err)
	}
	// The same page spelled three other ways: a fragment, a dot segment, and a
	// default port. Canon is what makes those one URL and this is the check that
	// the frontier uses it rather than the raw string.
	for _, u := range []string{
		"https://a.example/page#comments",
		"https://a.example/./page",
		"https://a.example:443/page",
	} {
		ok, why, err := f.Offer(u)
		if err != nil {
			t.Fatalf("Offer(%s): %v", u, err)
		}
		if ok {
			t.Errorf("%s was queued a second time", u)
		}
		if !strings.Contains(why, "already") {
			t.Errorf("Offer(%s) refused with %q", u, why)
		}
	}
	if got := next(t, f, 10); len(got) != 1 {
		t.Errorf("the frontier holds %v", got)
	}
	if s := f.Stats(); s.Duplicate != 3 {
		t.Errorf("the stats record %d duplicates, want 3", s.Duplicate)
	}
}

// A batch that is one host is a batch that runs at that host's crawl delay
// however many workers are watching it, so the frontier will not hand one out.
func TestABatchIsSpreadOverHosts(t *testing.T) {
	f := open(t, FrontierOptions{PerHost: 2})
	for i := range 20 {
		offer(t, f, fmt.Sprintf("https://busy.example/page-%d", i))
	}
	offer(t, f, "https://quiet.example/only")

	got := next(t, f, 10)
	hosts := map[string]int{}
	for _, u := range got {
		h, err := hostOf(u)
		if err != nil {
			t.Fatalf("hostOf(%s): %v", u, err)
		}
		hosts[h]++
	}
	if hosts["busy.example"] > 2 {
		t.Errorf("one batch took %d URLs from one host", hosts["busy.example"])
	}
	if hosts["quiet.example"] != 1 {
		t.Errorf("the batch missed the quiet host: %v", got)
	}

	// The rest was not dropped. It went back to the tail of its bucket and it
	// comes out on the batches after this one.
	seen := len(got)
	for range 20 {
		batch := next(t, f, 10)
		if len(batch) == 0 {
			break
		}
		seen += len(batch)
	}
	if seen != 21 {
		t.Errorf("the frontier gave out %d of 21 URLs", seen)
	}
}

// A crawl is killed. It is killed by the machine rebooting, by a disk filling,
// and by somebody wanting the terminal back, and what it cost is what it has to
// fetch again.
func TestAFrontierPicksUpWhereItStopped(t *testing.T) {
	dir := t.TempDir()
	f := open(t, FrontierOptions{Dir: dir})
	for i := range 10 {
		offer(t, f, fmt.Sprintf("https://host-%d.example/page", i))
	}
	first := next(t, f, 4)
	if len(first) != 4 {
		t.Fatalf("the first batch is %v", first)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again := open(t, FrontierOptions{Dir: dir})
	rest := next(t, again, 100)
	if len(rest) != 6 {
		t.Fatalf("the reopened frontier holds %d URLs, want 6", len(rest))
	}
	for _, u := range rest {
		for _, done := range first {
			if u == done {
				t.Errorf("%s was handed out before the restart and again after it", u)
			}
		}
	}
	// The seen set came back too, so the pages that were fetched before the
	// restart are not queued again by the links on the pages after it.
	ok, why, err := again.Offer(first[0])
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if ok {
		t.Errorf("a URL handed out before the restart was queued again")
	}
	if !strings.Contains(why, "already") {
		t.Errorf("it was refused with %q", why)
	}
	if s := again.Stats(); s.Admitted != 10 {
		t.Errorf("the counters did not survive the restart: %+v", s)
	}
}

// The exact set is on disk in sorted runs that merge as they grow. This is the
// test that a hash written into a run and merged twice is still found, because
// everything else in the frontier is built on that answer being right.
func TestTheSeenSetSurvivesBeingSpilledAndMerged(t *testing.T) {
	dir := t.TempDir()
	// A spill every four URLs, so a hundred of them is twenty five runs and
	// every merge the compaction does.
	f := open(t, FrontierOptions{Dir: dir, Pending: 4})

	urls := make([]string, 0, 100)
	for i := range 100 {
		u := fmt.Sprintf("https://h%02d.example/p%d", i%17, i)
		urls = append(urls, u)
		offer(t, f, u)
	}
	if err := f.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	names, err := filepath.Glob(filepath.Join(dir, "seen-*.hashes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("nothing was spilled to disk")
	}
	if len(names) > 8 {
		t.Errorf("a hundred URLs left %d runs behind, which is not merging", len(names))
	}

	for _, u := range urls {
		ok, _, err := f.Offer(u)
		if err != nil {
			t.Fatalf("Offer(%s): %v", u, err)
		}
		if ok {
			t.Errorf("%s was queued twice across a spill", u)
		}
	}

	// And it still says no after a restart, which is the same question asked of
	// the runs rather than of anything in memory.
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	again := open(t, FrontierOptions{Dir: dir, Pending: 4})
	for _, u := range urls {
		ok, _, err := again.Offer(u)
		if err != nil {
			t.Fatalf("Offer(%s): %v", u, err)
		}
		if ok {
			t.Errorf("%s was queued again after a restart", u)
		}
	}
	// Seventeen hosts at two URLs a batch is thirty four per batch, so this
	// takes several of them, which is the scheduler working rather than a
	// problem with the runs.
	var got int
	for range 100 {
		n := len(next(t, again, 1000))
		if n == 0 {
			break
		}
		got += n
	}
	if got != 100 {
		t.Errorf("the frontier gave back %d of 100 URLs", got)
	}
}

// A budget refusal is remembered, because a trap generates the same URL from
// every page on the site and answering it once is the point.
func TestARefusedURLIsNotAskedAboutTwice(t *testing.T) {
	f := open(t, FrontierOptions{Budget: frontier.NewBudget(frontier.Options{Depth: 3})})

	deep := "https://a.example/one/two/three/four/five"
	ok, why, err := f.Offer(deep)
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if ok {
		t.Fatal("a URL past the depth limit was queued")
	}
	if why == "" {
		t.Error("the frontier refused a URL without saying why")
	}

	ok, why, err = f.Offer(deep)
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if ok {
		t.Fatal("a refused URL was queued the second time")
	}
	if !strings.Contains(why, "already") {
		t.Errorf("the second offer went to the budget again: %q", why)
	}
	if s := f.Stats(); s.Refused != 1 || s.Duplicate != 1 || s.Queued() != 0 {
		t.Errorf("the stats after one refusal offered twice are %+v", s)
	}
}

// The queue is on scratch disk and the machines it runs on are at ninety five
// percent, so a file that only grows is a crawl that stops.
func TestAQueueFileGivesBackTheDiskItHasConsumed(t *testing.T) {
	dir := t.TempDir()
	f := open(t, FrontierOptions{Dir: dir, Compact: 1 << 10, Buckets: 1})

	for i := range 200 {
		offer(t, f, fmt.Sprintf("https://h%d.example/a-reasonably-long-path/%d", i, i))
	}
	if err := f.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	path := filepath.Join(dir, "queue", "0000.urls")
	full := size(t, path)
	if full < 1<<10 {
		t.Fatalf("the queue file is %d bytes, which is too small to test compaction", full)
	}

	for range 100 {
		if len(next(t, f, 20)) == 0 {
			break
		}
	}
	if err := f.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := size(t, path); got >= full {
		t.Errorf("the queue file is %d bytes after being emptied, from %d", got, full)
	}

	// Compaction moved the file under the reader, so what is left has to still
	// come out in one piece.
	offer(t, f, "https://after.example/compaction")
	got := next(t, f, 10)
	if len(got) != 1 || got[0] != "https://after.example/compaction" {
		t.Errorf("after compaction the frontier returned %v", got)
	}
}

func size(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return info.Size()
}

// Every URL's bucket is decided by the bucket count, so opening a frontier with
// a different one puts every URL in a file nothing will look in.
func TestAFrontierRefusesToOpenWithADifferentShape(t *testing.T) {
	dir := t.TempDir()
	f := open(t, FrontierOptions{Dir: dir, Buckets: 8})
	offer(t, f, "https://a.example/one")
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := OpenFrontier(FrontierOptions{Dir: dir, Buckets: 16})
	if err == nil {
		t.Fatal("a frontier opened with a different bucket count")
	}
	if !strings.Contains(err.Error(), "buckets") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

// Twenty workers offering the links they found is the ordinary shape of a crawl,
// and a frontier that needs them to take turns is a frontier that is the crawl's
// bottleneck rather than the network.
func TestOfferingFromEveryWorkerAtOnceIsSafe(t *testing.T) {
	f := open(t, FrontierOptions{Pending: 64})

	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 200 {
				// Every worker offers the same two hundred URLs, so all but the
				// first to arrive are duplicates and the seen set is what is
				// being hammered.
				if _, _, err := f.Offer(fmt.Sprintf("https://h%d.example/p%d", i%40, i)); err != nil {
					t.Errorf("worker %d: %v", w, err)
					return
				}
			}
		})
	}
	wg.Wait()

	s := f.Stats()
	if s.Offered != 1600 {
		t.Errorf("the frontier was offered %d URLs, want 1600", s.Offered)
	}
	if s.Admitted != 200 {
		t.Errorf("it queued %d of them, want 200", s.Admitted)
	}
	var got int
	for range 100 {
		n := len(next(t, f, 100))
		if n == 0 {
			break
		}
		got += n
	}
	if got != 200 {
		t.Errorf("it handed out %d URLs, want 200", got)
	}
}

// A fetch that did not happen is not a URL that has been crawled, and Offer will
// not take it back because it is already in the seen set.
func TestAFetchThatDidNotHappenGoesBackInTheQueue(t *testing.T) {
	f := open(t, FrontierOptions{})
	offer(t, f, "https://busy.example/page")
	got := next(t, f, 10)
	if len(got) != 1 {
		t.Fatalf("Next returned %v", got)
	}
	if err := f.Requeue(got[0]); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	again := next(t, f, 10)
	if len(again) != 1 || again[0] != got[0] {
		t.Errorf("a requeued URL came back as %v", again)
	}
}

func TestAURLThatIsNotOneIsRefusedWithoutTouchingTheQueue(t *testing.T) {
	f := open(t, FrontierOptions{})
	for _, u := range []string{"", "not a url", "ftp://a.example/f", "https:///nohost", "mailto:a@b.example"} {
		ok, why, err := f.Offer(u)
		if err != nil {
			t.Fatalf("Offer(%q): %v", u, err)
		}
		if ok {
			t.Errorf("%q was queued", u)
		}
		if why == "" {
			t.Errorf("%q was refused without a reason", u)
		}
	}
	if s := f.Stats(); s.Malformed != 5 || s.Admitted != 0 {
		t.Errorf("the stats after five bad URLs are %+v", s)
	}
}

// A page's links go in together, and what that has to leave behind is exactly
// what offering them one at a time would have. The batch is where a crawl's
// frontier traffic is, so it is worth knowing that the shortcut is only a
// shortcut through the lock and not through any of the checks.
func TestAPageOfLinksGoesInTogether(t *testing.T) {
	one := open(t, FrontierOptions{})
	all := open(t, FrontierOptions{})

	links := []string{
		"https://baodongthap.example/tin-1.html",
		"https://baodongthap.example/tin-2.html",
		"https://baodongthap.example/tin-1.html", // the same page twice, as a menu does
		"not a url",
		"mailto:toasoan@baodongthap.example",
		"https://vnexpress.example/thoi-su/bai-viet.html",
	}
	for _, u := range links {
		if _, _, err := one.Offer(u); err != nil {
			t.Fatalf("Offer(%s): %v", u, err)
		}
	}
	queued, err := all.OfferAll(links)
	if err != nil {
		t.Fatalf("OfferAll: %v", err)
	}

	a, b := one.Stats(), all.Stats()
	if a != b {
		t.Errorf("one at a time left %+v and the batch left %+v", a, b)
	}
	if int64(queued) != b.Admitted {
		t.Errorf("OfferAll says it queued %d and the stats say %d", queued, b.Admitted)
	}
	if b.Admitted != 3 || b.Duplicate != 1 || b.Malformed != 2 {
		t.Errorf("the batch left %+v, want 3 queued, 1 duplicate and 2 malformed", b)
	}
}

// A page with no links is the ordinary case on a crawl and it must not cost a
// turn at the lock every worker is waiting for.
func TestAPageWithNoLinksOffersNothing(t *testing.T) {
	f := open(t, FrontierOptions{})
	queued, err := f.OfferAll(nil)
	if err != nil {
		t.Fatalf("OfferAll: %v", err)
	}
	if queued != 0 {
		t.Errorf("a page with no links queued %d URLs", queued)
	}
	if s := f.Stats(); s.Offered != 0 {
		t.Errorf("a page with no links was counted as %d offers", s.Offered)
	}
}

// The yield counters are what a status line reads on a run that has been going
// for a week, and they are also what the budget earns on.
func TestWhatCameBackIsCountedAndCharged(t *testing.T) {
	b := frontier.NewBudget(frontier.Options{})
	f := open(t, FrontierOptions{Budget: b})

	offer(t, f, "https://a.example/article/1")
	f.Fetched("https://a.example/article/1", frontier.New)
	f.Fetched("https://a.example/article/2", frontier.Repeat)
	f.Fetched("https://a.example/article/3", frontier.Empty)

	s := f.Stats()
	if s.Fetched != 3 || s.New != 1 || s.Repeat != 1 || s.Empty != 1 {
		t.Errorf("the yield counters are %+v", s)
	}
	if _, gained, _ := b.Spent("a.example"); gained == 0 {
		t.Error("a page that produced new text earned the host nothing")
	}
}

func TestTheHostIsReadOutOfACanonicalURL(t *testing.T) {
	cases := []struct{ url, host string }{
		{"https://a.example/one", "a.example"},
		{"http://a.example:8080/one", "a.example:8080"},
		{"https://a.example", "a.example"},
		{"https://a.example?q=1", "a.example"},
	}
	for _, c := range cases {
		got, err := hostOf(c.url)
		if err != nil {
			t.Errorf("hostOf(%s): %v", c.url, err)
			continue
		}
		if got != c.host {
			t.Errorf("hostOf(%s) = %q, want %q", c.url, got, c.host)
		}
	}
	if _, err := hostOf("/relative/path"); err == nil {
		t.Error("hostOf took a relative path")
	}
}

// The fleet split: three boxes over the same list of hosts take a share each,
// every host lands on exactly one of them, and every URL of a host lands on the
// box that host landed on.
func TestAFleetSplitsTheHostsAndNotTheURLs(t *testing.T) {
	const boxes = 3
	var f [boxes]*Frontier
	for i := range f {
		f[i] = open(t, FrontierOptions{Shard: i, Fleet: boxes})
	}

	mine := map[string]int{}
	for i := range 300 {
		host := fmt.Sprintf("host-%d.example", i)
		took := 0
		for shard := range f {
			ok, _, err := f[shard].Offer("https://" + host + "/trang-chu")
			if err != nil {
				t.Fatalf("Offer: %v", err)
			}
			if ok {
				took++
				mine[host] = shard
			}
		}
		if took != 1 {
			t.Fatalf("%s was taken by %d boxes, want exactly one", host, took)
		}
	}

	// A second URL on a host goes to the box that took the first one, because
	// the split is on the host. A split on the URL would put two pages of one
	// site on two boxes, and the site would be asked twice as often as it said.
	for host, shard := range mine {
		for i := range f {
			ok, why, err := f[i].Offer("https://" + host + "/tin/1.html")
			if err != nil {
				t.Fatalf("Offer: %v", err)
			}
			if (i == shard) != ok {
				t.Fatalf("%s on box %d: queued=%v, want %v (%s)", host, i, ok, i == shard, why)
			}
		}
	}

	var queued, foreign int64
	for i := range f {
		s := f[i].Stats()
		queued += s.Queued()
		foreign += s.Foreign
	}
	if queued != 600 {
		t.Errorf("the fleet queued %d URLs, want the 600 it was offered", queued)
	}
	if foreign != 1200 {
		t.Errorf("the fleet turned away %d URLs as another box's, want 1200", foreign)
	}
}

// A box cannot be resumed as a different member of the fleet, because its queue
// holds one shard's hosts and its seen set holds one shard's URLs.
func TestAFrontierWillNotChangeShardOnResume(t *testing.T) {
	dir := t.TempDir()
	f := open(t, FrontierOptions{Dir: dir, Shard: 0, Fleet: 3})
	offer(t, f, "https://bao.example/trang-chu")
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := OpenFrontier(FrontierOptions{Dir: dir, Shard: 1, Fleet: 3}); err == nil {
		t.Error("a frontier written by box 0 was reopened as box 1")
	}
}

// Fetched is the call a worker makes for every URL that comes back, and it used
// to take the frontier's lock to add one to four numbers. On a 2,500 worker box
// that put 765 workers in a queue behind the offers and the batches, so the
// counters are atomics now and the call takes no lock at all.
//
// What has to stay true is that none of the counting is lost when everything
// happens at once, which is the thing a lock was buying. The race detector has
// its own opinion and this is the arithmetic.
func TestCountingWhileTheCrawlOffersAndTakesLosesNothing(t *testing.T) {
	f, err := OpenFrontier(FrontierOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenFrontier: %v", err)
	}
	defer func() { _ = f.Close() }()

	const workers, each = 16, 500
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				u := fmt.Sprintf("https://bao%d.com/tin/%d.html", w, i)
				if _, _, err := f.Offer(u); err != nil {
					t.Errorf("Offer: %v", err)
					return
				}
				f.Fetched(u, frontier.New)
				// Read it while it moves, since a progress line does.
				_ = f.Stats().Queued()
			}
		}()
	}
	wg.Wait()

	s := f.Stats()
	if s.Fetched != workers*each {
		t.Errorf("the frontier counted %d fetches, want %d", s.Fetched, workers*each)
	}
	if s.New != workers*each {
		t.Errorf("the frontier counted %d new, want %d", s.New, workers*each)
	}
	if s.Offered != workers*each {
		t.Errorf("the frontier counted %d offers, want %d", s.Offered, workers*each)
	}
	if s.Admitted != workers*each {
		t.Errorf("the frontier admitted %d, want %d, so an offer was lost", s.Admitted, workers*each)
	}
}

// The queue survives being stopped and started, which is a thing the fleet does
// every time a binary is replaced.
//
// Losing a URL here is silent: the crawl simply never fetches that page and no
// counter moves. This test was written for a change that held over quota URLs in
// memory between batches, and it found that change losing 26 URLs of 200 across
// a restart. That change is gone and the test is not, because the property it
// checks is one the queue is supposed to have whatever is behind it.
func TestNoURLIsLostBetweenBatches(t *testing.T) {
	dir := t.TempDir()
	f := open(t, FrontierOptions{Dir: dir, PerHost: 2})

	want := map[string]bool{}
	for h := range 5 {
		for i := range 40 {
			u := fmt.Sprintf("https://bao%d.com/tin/%d.html", h, i)
			ok, why, err := f.Offer(u)
			if err != nil {
				t.Fatalf("Offer: %v", err)
			}
			if !ok {
				t.Fatalf("%s was refused: %s", u, why)
			}
			want[u] = true
		}
	}

	got := map[string]int{}
	for round := range 200 {
		batch, err := f.Next(8)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		for _, u := range batch {
			got[u]++
		}
		// The stop and the start, partway through draining the queue.
		if round == 3 {
			if err := f.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			f = open(t, FrontierOptions{Dir: dir, PerHost: 2})
		}
		if len(got) == len(want) {
			break
		}
	}

	for u := range want {
		if got[u] == 0 {
			t.Errorf("%s went in and never came out", u)
		}
		if got[u] > 1 {
			t.Errorf("%s came out %d times", u, got[u])
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d URLs came back out of %d", len(got), len(want))
	}
}

// The whole point of the per host cap is that a batch is spread over hosts, and
// nothing about how the leftovers are handled may quietly undo it. A batch that
// is one host is a batch that finishes at that host's crawl delay however many
// workers are watching, which is the difference between ninety pages a second
// and two.
func TestABatchIsStillSpreadOverHosts(t *testing.T) {
	f := open(t, FrontierOptions{PerHost: 2})
	for h := range 5 {
		for i := range 40 {
			if _, _, err := f.Offer(fmt.Sprintf("https://bao%d.com/tin/%d.html", h, i)); err != nil {
				t.Fatalf("Offer: %v", err)
			}
		}
	}
	batch, err := f.Next(8)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	per := map[string]int{}
	for _, u := range batch {
		host, err := hostOf(u)
		if err != nil {
			t.Fatal(err)
		}
		per[host]++
	}
	for host, n := range per {
		if n > 2 {
			t.Errorf("the batch took %d URLs from %s, and the cap is two", n, host)
		}
	}
}

// A URL the write buffer spilled halfway through comes back whole.
//
// The bucket's 64 KB write buffer fills in the middle of a URL as often as
// anywhere else, and when it does the front of that URL is on the disk and the
// rest is still in memory. A reader that arrives in between is told the bucket
// has bytes left, reaches the end of the file partway through a line, and
// whatever it returns it cannot un-read those bytes. They used to be dropped,
// which meant the front of the URL was gone and its tail came back on the next
// read looking like a URL of its own.
//
// [Frontier.Next] flushes every bucket before it reads, so this was out of reach
// while a requeue also held the frontier's lock. It is not out of reach now: a
// requeue takes one bucket and can land between that flush and that read. The
// bucket is driven directly here because that race is not one a test can arrange
// on demand.
func TestAURLTheBufferSplitComesBackWhole(t *testing.T) {
	f := open(t, FrontierOptions{Buckets: 1})
	b := f.queue[0]

	want := make([]string, 0, 1000)
	for i := range 1000 {
		u := fmt.Sprintf("https://bao.com/tin/%d/%s.html", i, strings.Repeat("a", 200))
		if err := b.push(u); err != nil {
			t.Fatalf("push: %v", err)
		}
		want = append(want, u)
	}

	// Read what the buffer spilled by itself, with no flush, which is the state a
	// requeue leaves the file in.
	var got []string
	drain := func() {
		for {
			line, err := b.take()
			if err != nil {
				t.Fatalf("take: %v", err)
			}
			if line == "" {
				return
			}
			got = append(got, line)
		}
	}
	drain()
	if len(got) == 0 {
		t.Fatal("the buffer never spilled, so this test is checking nothing")
	}
	if len(got) == len(want) {
		t.Fatal("the buffer spilled all of it, so this test is checking nothing")
	}

	// What [Frontier.Next] does before it reads a bucket, which is where the
	// rest of the URLs come from.
	if err := b.arm(); err != nil {
		t.Fatalf("arm: %v", err)
	}
	drain()

	if len(got) != len(want) {
		t.Fatalf("%d URLs came out of the bucket and %d went in", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("URL %d came back as %q and went in as %q", i, got[i], want[i])
		}
	}
}

// Putting URLs back while the frontier is handing them out and taking new ones
// loses none of them.
//
// Requeue no longer takes the frontier's lock, only the lock on the one bucket
// it writes to, and this is the property that change had to keep. Run it under
// -race, which is where a bucket written without its lock shows up.
func TestPuttingURLsBackWhileTheCrawlRunsLosesNothing(t *testing.T) {
	f := open(t, FrontierOptions{Buckets: 8, PerHost: 1 << 20})

	const hosts, each = 16, 50
	for h := range hosts {
		for i := range each {
			u := fmt.Sprintf("https://bao%d.com/tin/%d.html", h, i)
			if ok, why, err := f.Offer(u); err != nil || !ok {
				t.Fatalf("Offer %s: %v %s", u, err, why)
			}
		}
	}

	// Every URL is taken out and put straight back, from many workers at once,
	// and the last round takes them out for good.
	var mu sync.Mutex
	got := map[string]int{}
	for round := range 4 {
		last := round == 3
		var batch []string
		for {
			b, err := f.Next(64)
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if len(b) == 0 {
				break
			}
			batch = append(batch, b...)
		}
		var wg sync.WaitGroup
		for _, u := range batch {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if last {
					mu.Lock()
					got[u]++
					mu.Unlock()
					return
				}
				if err := f.Requeue(u); err != nil {
					t.Errorf("Requeue %s: %v", u, err)
				}
			}()
		}
		wg.Wait()
	}

	if len(got) != hosts*each {
		t.Fatalf("%d URLs came back out of %d that went in", len(got), hosts*each)
	}
	for u, n := range got {
		if n != 1 {
			t.Fatalf("%s came out %d times on the last round", u, n)
		}
	}
}

// A URL already written out to a run is refused however many workers offer it
// at once.
//
// The runs are read with the frontier's lock let go, so this is the pass that
// the lock used to make trivially correct. Small Pending forces the hashes onto
// the disk, and offering every one of them again from every goroutine at once is
// what puts many readers in a run file together.
func TestAURLOnDiskIsRefusedHoweverManyWorkersAskAtOnce(t *testing.T) {
	f := open(t, FrontierOptions{Pending: 64})

	urls := make([]string, 0, 800)
	for h := range 20 {
		for i := range 40 {
			urls = append(urls, fmt.Sprintf("https://bao%d.com/tin/%d.html", h, i))
		}
	}
	for _, u := range urls {
		if ok, why, err := f.Offer(u); err != nil || !ok {
			t.Fatalf("Offer %s: %v %s", u, err, why)
		}
	}
	if err := f.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(f.runs) == 0 {
		t.Fatal("nothing spilled to a run, so the disk pass is not being tested")
	}

	var queued atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := f.OfferAll(urls)
			if err != nil {
				t.Errorf("OfferAll: %v", err)
				return
			}
			queued.Add(int64(n))
		}()
	}
	wg.Wait()

	if got := queued.Load(); got != 0 {
		t.Fatalf("%d URLs were queued a second time and every one of them was already in a run", got)
	}
}

// Two workers offering the same new URL at the same moment queue it once.
//
// Once is what the third pass is for. It cannot be guaranteed, because two
// workers can be inside the run files together and neither has recorded
// anything, and the whole trade is that an exact overlap costs one page fetched
// twice rather than costing every offer a disk read under the frontier's lock.
// This is the size of that: with nothing on disk to read there is no window at
// all, and the count has to be exactly one.
func TestOneNewURLOfferedByEveryWorkerAtOnceIsQueuedOnce(t *testing.T) {
	f := open(t, FrontierOptions{})

	var queued atomic.Int64
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, err := f.Offer("https://bao.com/tin/1.html")
			if err != nil {
				t.Errorf("Offer: %v", err)
				return
			}
			if ok {
				queued.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := queued.Load(); got != 1 {
		t.Fatalf("one URL offered by sixteen workers was queued %d times", got)
	}
}

func TestAPageWorthOfLinksToOneSiteDoesNotJamTheBatches(t *testing.T) {
	// What a crawl actually offers: a page links to its own site sixty times and
	// every one of those arrives together. If those sixty land in one queue file
	// then a batch reads a run of one host, hands out the two it is allowed and
	// puts the other fifty eight back, which is a bucket read and a push each and
	// all of it in the goroutine that fills the batch.
	f := open(t, FrontierOptions{})
	for h := range 64 {
		for i := range 60 {
			offer(t, f, fmt.Sprintf("https://bao%d.example/tin/%d.html", h, i))
		}
	}

	handed := 0
	for {
		got := next(t, f, 100)
		if len(got) == 0 {
			break
		}
		handed += len(got)
	}
	if handed != 64*60 {
		t.Fatalf("drained %d URLs of %d", handed, 64*60)
	}

	// One deferral per URL handed out is already generous: it means the queue
	// gets read through twice. The bug being held off here was twenty nine.
	deferred := f.Stats().Deferred
	if deferred > int64(handed) {
		t.Fatalf("put %d URLs back to hand out %d, so the batches are reading a run of one host at a time",
			deferred, handed)
	}
	t.Logf("deferred %d to hand out %d", deferred, handed)
}

// A bucket that still has URLs on the disk is not flushed to find more.
//
// The flush is what [Frontier.Next] used to do to all sixty four buckets on
// every call. On a queue with twenty six million URLs on it the reader is
// nowhere near the end of any of them, so every one of those flushes was a lock
// and a write against the workers appending to that same bucket, for nothing.
func TestABucketWithURLsOnTheDiskIsNotFlushedToFindMore(t *testing.T) {
	f := open(t, FrontierOptions{Buckets: 1})
	b := f.queue[0]

	for i := range 100 {
		if err := b.push(fmt.Sprintf("https://bao.com/tin/%d.html", i)); err != nil {
			t.Fatalf("push: %v", err)
		}
	}
	if err := b.arm(); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if b.bw.Buffered() != 0 {
		t.Fatalf("an empty bucket was not flushed, so it had nothing to hand out")
	}

	// More URLs arrive and sit in the buffer, and one comes off the disk, which
	// leaves ninety nine there.
	for i := 100; i < 200; i++ {
		if err := b.push(fmt.Sprintf("https://bao.com/tin/%d.html", i)); err != nil {
			t.Fatalf("push: %v", err)
		}
	}
	if _, err := b.take(); err != nil {
		t.Fatalf("take: %v", err)
	}
	buffered := b.bw.Buffered()
	if buffered == 0 {
		t.Fatal("the second hundred spilled on its own, so this test is checking nothing")
	}
	if err := b.arm(); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if b.bw.Buffered() != buffered {
		t.Fatalf("the bucket was flushed with ninety nine URLs still unread on the disk")
	}
}

// Batches filled from several goroutines at once hand out every URL and hand out
// none of them twice.
//
// Next takes no lock on the frontier any more, so this is the property that has
// to hold: the rotation counter is an atomic and the lines come off the buckets
// under the bucket's own lock. Run it under -race.
func TestSeveralBatchesFilledAtOnceHandOutEveryURLOnce(t *testing.T) {
	f := open(t, FrontierOptions{Buckets: 8, PerHost: 1 << 20})

	want := 0
	for h := range 40 {
		for i := range 50 {
			offer(t, f, fmt.Sprintf("https://bao%d.com/tin/%d.html", h, i))
			want++
		}
	}

	var mu sync.Mutex
	got := map[string]int{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for {
				batch, err := f.Next(37)
				if err != nil {
					t.Errorf("Next: %v", err)
					return
				}
				if len(batch) == 0 {
					return
				}
				mu.Lock()
				for _, u := range batch {
					got[u]++
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if len(got) != want {
		t.Fatalf("%d URLs came out of the frontier and %d went in", len(got), want)
	}
	for u, n := range got {
		if n != 1 {
			t.Fatalf("%s was handed out %d times", u, n)
		}
	}
}

// The crawl goes on offering while a run is being written, which is what taking
// the spill off the frontier's lock was for.
//
// Before the change, the worker whose offer tripped the threshold sorted the
// hashes, wrote the run and merged runs with the frontier's lock held, so every
// other worker on the box stopped for the length of it. A dump of server3 at 130
// pages a second had 1,098 goroutines waiting there.
//
// The spill is parked at its seam, and then two things are asked of the frontier
// with it parked. A URL it has never seen has to be queued, which is the claim
// about the lock. A URL that is in the frozen map has to be refused, which is
// the claim that setting the map aside did not lose anything: those hashes are
// in neither pending nor a run for the length of the write.
func TestTheCrawlGoesOnOfferingWhileARunIsBeingWritten(t *testing.T) {
	f := open(t, FrontierOptions{Pending: 4})

	parked := make(chan struct{})
	release := make(chan struct{})
	f.beforeRun = func() {
		close(parked)
		<-release
	}

	frozen := []string{
		"https://one.example/a",
		"https://two.example/b",
		"https://three.example/c",
		"https://four.example/d",
	}
	spilling := make(chan error, 1)
	go func() {
		_, err := f.OfferAll(frozen)
		spilling <- err
	}()

	select {
	case <-parked:
	case err := <-spilling:
		t.Fatalf("the offer finished without spilling, so this test is checking nothing: %v", err)
	}

	done := make(chan struct{})
	var queued bool
	var why string
	var err error
	go func() {
		defer close(done)
		queued, why, err = f.Offer("https://five.example/e")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("an offer made while a run was being written never came back, so the spill is still holding the frontier's lock")
	}
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if !queued {
		t.Errorf("a new URL offered during a spill was refused: %s", why)
	}

	for _, u := range frozen {
		ok, why, err := f.Offer(u)
		if err != nil {
			t.Fatalf("Offer(%s): %v", u, err)
		}
		if ok {
			t.Errorf("%s was queued a second time while its hash was being spilled", u)
		}
		if why != "already offered" {
			t.Errorf("%s was refused during a spill for %q rather than for having been offered", u, why)
		}
	}

	close(release)
	if err := <-spilling; err != nil {
		t.Fatalf("OfferAll: %v", err)
	}
}

// A crawl killed while it was writing a run opens again knowing the hashes that
// run was going to hold.
//
// The state that leaves on the disk is a rotated pending-NNNNNN.hashes holding
// them, a fresh pending.hashes holding whatever was offered after the freeze, a
// manifest that does not name the run, and the run itself either missing or part
// written. Copying the directory with the spill parked at its seam is that state
// exactly, and the copy is opened rather than the original so the parked spill
// is left alone to finish.
func TestHashesSurviveACrawlKilledWhileItWasWritingARun(t *testing.T) {
	dir := t.TempDir()
	f := open(t, FrontierOptions{Dir: dir, Pending: 4})

	parked := make(chan struct{})
	release := make(chan struct{})
	f.beforeRun = func() {
		close(parked)
		<-release
	}

	frozen := []string{
		"https://one.example/a",
		"https://two.example/b",
		"https://three.example/c",
		"https://four.example/d",
	}
	spilling := make(chan error, 1)
	go func() {
		_, err := f.OfferAll(frozen)
		spilling <- err
	}()
	select {
	case <-parked:
	case err := <-spilling:
		t.Fatalf("the offer finished without spilling, so this test is checking nothing: %v", err)
	}

	// Offered after the freeze, so it is in the fresh log rather than the
	// rotated one, and it has to come back too.
	after := "https://five.example/e"
	if _, _, err := f.Offer(after); err != nil {
		t.Fatalf("Offer(%s): %v", after, err)
	}
	if err := f.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	killed := copyDir(t, dir)
	if _, err := os.Stat(filepath.Join(killed, "pending-000001.hashes")); err != nil {
		t.Fatalf("the killed frontier has no rotated log, so this test is checking nothing: %v", err)
	}
	close(release)
	if err := <-spilling; err != nil {
		t.Fatalf("OfferAll: %v", err)
	}

	again := open(t, FrontierOptions{Dir: killed, Pending: 4})
	for _, u := range append(append([]string{}, frozen...), after) {
		ok, _, err := again.Offer(u)
		if err != nil {
			t.Fatalf("Offer(%s): %v", u, err)
		}
		if ok {
			t.Errorf("%s was queued again after a kill during a spill", u)
		}
	}

	// And the rotated log is folded into the live one rather than left to be
	// read at every open from here on.
	left, err := filepath.Glob(filepath.Join(killed, "pending-*.hashes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("a rotated log was left behind after recovery: %v", left)
	}
}

// A run file no manifest names is removed at open.
//
// It is what a kill during a write or a merge leaves, nothing reads it again,
// and it is the size of the run that was being written, on boxes where the disk
// is the thing that runs out first.
func TestARunNoManifestNamesIsRemovedAtOpen(t *testing.T) {
	dir := t.TempDir()
	f := open(t, FrontierOptions{Dir: dir, Pending: 4})
	for i := range 20 {
		offer(t, f, fmt.Sprintf("https://h%02d.example/p%d", i, i))
	}
	if err := f.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	real, err := filepath.Glob(filepath.Join(dir, "seen-*.hashes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(real) == 0 {
		t.Fatal("nothing spilled, so this test is checking nothing")
	}
	stray := filepath.Join(dir, "seen-999999.hashes")
	if err := os.WriteFile(stray, make([]byte, 32), 0o644); err != nil {
		t.Fatal(err)
	}

	again := open(t, FrontierOptions{Dir: dir, Pending: 4})
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Errorf("a run no manifest names survived an open: %v", err)
	}
	for _, name := range real {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("%s was removed and the manifest names it: %v", filepath.Base(name), err)
		}
	}
	if _, _, err := again.Offer("https://h00.example/p0"); err != nil {
		t.Fatalf("Offer: %v", err)
	}
}

// copyDir copies a frontier directory, which is how a test gets at the state a
// kill would have left without killing anything.
func copyDir(t *testing.T, dir string) string {
	t.Helper()
	to := t.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		from := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(filepath.Join(to, e.Name()), 0o755); err != nil {
				t.Fatal(err)
			}
			sub, err := os.ReadDir(from)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range sub {
				b, err := os.ReadFile(filepath.Join(from, s.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(to, e.Name(), s.Name()), b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			continue
		}
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return to
}

func TestQueuedDoesNotCountADeferralAsAnArrival(t *testing.T) {
	f := open(t, FrontierOptions{PerHost: 2})
	offer(t, f,
		"https://one.example/a", "https://one.example/b", "https://one.example/c",
		"https://one.example/d", "https://one.example/e")

	if got := next(t, f, 10); len(got) != 2 {
		t.Fatalf("a batch from one host under PerHost 2 returned %d URLs", len(got))
	}

	s := f.Stats()
	if s.Admitted != 5 || s.Handed != 2 || s.Deferred != 3 {
		t.Fatalf("the counters after one batch are %+v", s)
	}
	// Five went in and two came out, so three are waiting. The three deferrals
	// are those same three URLs taken out of a bucket and put straight back, so
	// counting them as arrivals reports more URLs queued than were ever offered.
	if q := s.Queued(); q != 3 {
		t.Errorf("Queued reports %d URLs waiting, and 3 were left", q)
	}
}

func TestAShortBatchIsReportedAsRunningOutOfHosts(t *testing.T) {
	f := open(t, FrontierOptions{PerHost: 2})

	// Three hosts, twenty URLs each. A batch can hold six of these, ever,
	// however many are queued and however many are asked for.
	for _, host := range []string{"one.example", "two.example", "three.example"} {
		for i := range 20 {
			offer(t, f, fmt.Sprintf("https://%s/p/%d", host, i))
		}
	}

	if got := next(t, f, 500); len(got) != 6 {
		t.Fatalf("a batch over three hosts under PerHost 2 returned %d URLs", len(got))
	}
	if s := f.Stats(); s.Exhausted != 1 {
		t.Errorf("a batch that asked for 500 and got 6 reported Exhausted %d", s.Exhausted)
	}

	// A batch that gets everything it asked for is not short and must not be
	// counted, or the number stops meaning anything.
	g := open(t, FrontierOptions{PerHost: 2})
	offer(t, g, "https://a.example/1", "https://b.example/1")
	if got := next(t, g, 2); len(got) != 2 {
		t.Fatalf("a full batch returned %d URLs", len(got))
	}
	if s := g.Stats(); s.Exhausted != 0 {
		t.Errorf("a batch that was filled reported Exhausted %d", s.Exhausted)
	}
}

// TestTheFleetSplitHereIsTheOneASeedListCanCompute pins the crawler's split
// against [frontier.Box].
//
// There are two spellings of one rule and there have to be. The crawler already
// holds the host's hash by the time it asks, because it computed it outside the
// lock along with everything else an offer needs, and going through a function
// that hashes it again would put a blake3 back on the path of every link on
// every page. A seed list has no such hash and no reason to have one.
//
// Two spellings is a fleet where a third of the seed goes to the box that will
// refuse it, unless something says they agree. This is that something.
func TestTheFleetSplitHereIsTheOneASeedListCanCompute(t *testing.T) {
	hosts := []string{
		"vnexpress.net", "tuoitre.vn", "voz.vn", "otofun.net",
		"example.com", "vi.wikipedia.org", "kenh14.vn", "baochinhphu.vn",
	}
	for _, fleet := range []int{2, 3, 5} {
		for _, host := range hosts {
			p := parseOffer("https://" + host + "/")
			if p.bad {
				t.Fatalf("parseOffer(%q): %s", host, p.why)
			}
			here := int(p.hostHash % uint64(fleet))
			if there := frontier.Box(host, fleet); here != there {
				t.Errorf("%s of %d: the crawler says box %d and a seed list says box %d",
					host, fleet, here, there)
			}
		}
	}
}
