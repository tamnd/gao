package crawl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
