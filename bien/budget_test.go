package bien_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/tamnd/gao/bien"
)

// A budget is judged on two runs that have to come out differently. A forum that
// keeps producing threads has to be able to grow past whatever it started with,
// and a calendar that produces nothing has to stop without anybody noticing it
// and intervening. Most of these tests are one of those two runs, written small
// enough to read.

// crawl walks a template with the same result every time and reports how many
// URLs got through before the budget refused, and why it refused.
func crawl(b *bien.Budget, format string, r bien.Result, limit int) (int, string) {
	for i := range limit {
		url := fmt.Sprintf(format, i+1)
		ok, why := b.Offer(url)
		if !ok {
			return i, why
		}
		b.Fetched(url, r)
	}
	return limit, ""
}

func small() bien.Options {
	return bien.Options{
		HostStart:  1000,
		HostEarn:   4,
		ShapeStart: 10,
		ShapeEarn:  4,
		DatedStart: 6,
		Barren:     4,
		Facets:     3,
		Depth:      6,
		Repeats:    3,
	}
}

// The run this exists to allow. A template producing text has to be able to get
// past where it started, or the cap is just the old per host cap with more
// arithmetic in front of it.
func TestATemplateThatProducesTextKeepsGoing(t *testing.T) {
	b := bien.NewBudget(small())
	got, why := crawl(b, "https://diendan.vn/thread/%d", bien.New, 500)
	if got != 500 {
		t.Fatalf("a forum producing new text on every page stopped after %d: %s", got, why)
	}

	spent, gained, shapes := b.Spent("diendan.vn")
	if spent != 500 || gained != 500 || shapes != 1 {
		t.Errorf("the host spent %d, gained %d, over %d templates", spent, gained, shapes)
	}
}

// The run this exists to stop. Same shape of loop, nothing coming back.
func TestATemplateThatProducesNothingStops(t *testing.T) {
	b := bien.NewBudget(small())
	got, why := crawl(b, "https://lich.vn/su-kien/%d", bien.Empty, 500)
	if got >= 500 {
		t.Fatal("a template returning nothing was never closed")
	}
	if got > small().Barren+1 {
		t.Errorf("it took %d fetches to notice, and every one of them was a request to somebody else's server", got)
	}
	if !strings.Contains(why, "nothing new") {
		t.Errorf("the reason given was %q", why)
	}
}

// The middle case, and the one the arithmetic rather than the run of empties has
// to catch. This is what a real municipal calendar looks like: mostly nothing,
// with an event every week or so, which is enough to keep resetting the
// consecutive counter forever. The yield is what has to stop it.
func TestATemplateThatProducesTextOnceInAWhileRunsOut(t *testing.T) {
	o := small()
	o.Barren = 6 // longer than the run of empties this produces, so it never fires
	b := bien.NewBudget(o)

	spent := 0
	for i := range 500 {
		url := fmt.Sprintf("https://lich.vn/ngay/%d", i)
		ok, _ := b.Offer(url)
		if !ok {
			break
		}
		spent++
		if i%5 == 0 {
			b.Fetched(url, bien.New)
		} else {
			b.Fetched(url, bien.Empty)
		}
	}
	if spent >= 500 {
		t.Fatal("a template earning one page in five ran forever")
	}
	if spent < o.ShapeStart {
		t.Errorf("it was cut off after %d, which is not a fair reading of it", spent)
	}
	// One page in five against four URLs earned per page is a template spending
	// faster than it earns, so it converges rather than stopping at the number
	// it started with.
	if spent <= o.ShapeStart*2 {
		t.Errorf("it stopped after %d, which is barely past its starting allowance of %d", spent, o.ShapeStart)
	}
}

// Text we already have is not text. A print view of every article on a site is
// the same corpus twice, and it costs the same requests as the first copy.
func TestPagesWeAlreadyHaveEarnNothing(t *testing.T) {
	b := bien.NewBudget(small())
	got, why := crawl(b, "https://vnexpress.net/in/%d", bien.Repeat, 500)
	if got >= 500 {
		t.Fatal("a template returning documents already in the corpus ran forever")
	}
	if !strings.Contains(why, "nothing new") {
		t.Errorf("the reason given was %q", why)
	}
}

// The inversion, stated as a test. The old rule was one cap for the whole host,
// which cuts off a site with many templates and lets a site with one template
// spend the lot. A deep forum has to get depth on every template it has.
func TestADeepForumGetsDepthAndAShallowShopDoesNot(t *testing.T) {
	forum := bien.NewBudget(small())
	for _, section := range []string{"tin-tuc", "the-thao", "am-nhac", "xe-co", "nha-dat"} {
		got, why := crawl(forum, "https://diendan.vn/"+section+"/thread/%d", bien.New, 100)
		if got != 100 {
			t.Fatalf("the %s board stopped after %d: %s", section, got, why)
		}
	}
	spent, _, shapes := forum.Spent("diendan.vn")
	if shapes != 5 || spent != 500 {
		t.Errorf("the forum spent %d over %d templates", spent, shapes)
	}

	// The shop has one template and it produces the same three sentences of
	// marketing copy on every product page.
	shop := bien.NewBudget(small())
	got, _ := crawl(shop, "https://shop.vn/san-pham/%d", bien.Repeat, 40000)
	if got > 20 {
		t.Errorf("the shop got %d product pages out of a budget meant for a site with something to say", got)
	}
}

// A dated template is judged sooner because a date is the one segment kind that
// can be filled in forever. An archive earns its way past that and a calendar
// does not, and the difference is in what comes back rather than in the URL.
func TestAnArchiveAndACalendarLookTheSameAndEndDifferently(t *testing.T) {
	archive := bien.NewBudget(small())
	got, why := crawl(archive, "https://vnexpress.net/luu-tru/2024-03-%02d", bien.New, 30)
	if got != 30 {
		t.Fatalf("a real archive stopped after %d days: %s", got, why)
	}

	calendar := bien.NewBudget(small())
	got, why = crawl(calendar, "https://truong.edu.vn/lich/2024-03-%02d", bien.Empty, 30)
	if got >= 30 {
		t.Fatal("an empty calendar was never closed")
	}
	if !strings.Contains(why, "nothing new") {
		t.Errorf("the reason given was %q", why)
	}
}

// The dated start on its own, with the run of empties taken out of the way so
// that the starting number is the only thing left to do the work. A date is the
// one segment kind that can be filled in forever, so a template carrying one is
// asked to prove itself sooner than a template carrying an article id.
func TestATemplateWithADateInItIsJudgedSooner(t *testing.T) {
	o := small()
	o.Barren = 1000
	b := bien.NewBudget(o)

	plain, _ := crawl(b, "https://truong.edu.vn/bai/%d", bien.Empty, 500)
	dated, _ := crawl(b, "https://truong.edu.vn/lich/2024-03-%02d", bien.Empty, 500)

	if plain != o.ShapeStart {
		t.Errorf("a plain template got %d, want %d", plain, o.ShapeStart)
	}
	if dated != o.DatedStart {
		t.Errorf("a dated template got %d, want %d", dated, o.DatedStart)
	}
	if dated >= plain {
		t.Error("a dated template was not judged sooner than a plain one")
	}
}

// The two refusals that need no fetch at all, which is the point of them. A URL
// refused here has cost nothing but the parse.
func TestSomeURLsAreRefusedWithoutAsking(t *testing.T) {
	b := bien.NewBudget(small())

	looping := "https://truong.edu.vn/tin/bai/bai/bai/"
	ok, why := b.Offer(looping)
	if ok {
		t.Error("a link resolving against itself was offered")
	}
	if !strings.Contains(why, "resolving against itself") {
		t.Errorf("the reason given was %q", why)
	}

	deep := "https://diendan.vn/a/b/c/d/e/f/g/h"
	if ok, why := b.Offer(deep); ok {
		t.Error("a path past the depth limit was offered")
	} else if !strings.Contains(why, "deep") {
		t.Errorf("the reason given was %q", why)
	}

	// Neither of them charged anything, because neither of them was asked for.
	if spent, _, _ := b.Spent("truong.edu.vn"); spent != 0 {
		t.Errorf("a URL refused before the request spent %d", spent)
	}
}

// Two filtered listings differing only in the values of their filters are one
// template already, so the per template budget bounds them and the facet rule is
// not needed for that. Saying so here means the rule below is understood to be
// about the other explosion.
func TestOneGridOfValuesIsOneTemplate(t *testing.T) {
	b := bien.NewBudget(small())
	for _, q := range []string{"mau=do&size=m", "mau=do&size=l", "mau=xanh&size=m", "mau=den&size=l"} {
		if ok, why := b.Offer("https://shop.vn/san-pham?" + q); !ok {
			t.Fatalf("?%s was refused: %s", q, why)
		}
	}
	if _, _, shapes := b.Spent("shop.vn"); shapes != 1 {
		t.Errorf("four values of two filters produced %d templates", shapes)
	}
}

// The explosion that is left, and the one the rule is for. Four filters over one
// listing is fifteen subsets of those filters, and each subset is a template
// with a starting allowance of its own. Eight filters is two hundred and fifty
// six of them.
func TestTheSubsetsOfAFilterSetStopMultiplying(t *testing.T) {
	b := bien.NewBudget(small())

	combos := []string{
		"mau=do&size=m",
		"mau=do&sort=gia",
		"size=m&sort=gia",
		"mau=do&size=m&sort=gia",
		"mau=do&size=m&trang=2",
	}
	allowed, refused := 0, ""
	for _, q := range combos {
		ok, why := b.Offer("https://shop.vn/san-pham?" + q)
		if ok {
			allowed++
			continue
		}
		refused = why
	}
	if allowed != small().Facets {
		t.Errorf("%d filter combinations got through, want %d", allowed, small().Facets)
	}
	if !strings.Contains(refused, "single filter") {
		t.Errorf("the reason given was %q", refused)
	}

	// A combination already asked for is not a new combination, or a crawl that
	// met the same filtered listing twice would close the path sooner every time
	// somebody linked to it.
	if ok, why := b.Offer("https://shop.vn/san-pham?mau=do&size=m"); !ok {
		t.Errorf("a combination already being crawled was refused: %s", why)
	}

	// The single filter views stay open, because that is where the products are
	// reachable from, and so does the listing itself.
	for _, u := range []string{
		"https://shop.vn/san-pham?mau=vang",
		"https://shop.vn/san-pham?trang=3",
		"https://shop.vn/san-pham",
	} {
		if ok, why := b.Offer(u); !ok {
			t.Errorf("%s was refused: %s", u, why)
		}
	}

	// And another path on the same host is another listing. The rule is about a
	// grid over one page rather than about a site that uses query parameters.
	if ok, why := b.Offer("https://shop.vn/khuyen-mai?mau=do&size=m"); !ok {
		t.Errorf("a different listing was refused: %s", why)
	}
}

// A host with hundreds of templates, none of them worth anything, is the parked
// domain and the auto generated directory, and there are a lot of both in a
// frontier of 900,000 hosts. The per template rule alone would give each of
// those templates its own allowance, so the host wide number is the backstop.
func TestAHostCanRunOutEvenWhenNoTemplateHas(t *testing.T) {
	o := small()
	o.HostStart = 40
	b := bien.NewBudget(o)

	spent := 0
	for i := range 100 {
		url := fmt.Sprintf("https://rac.vn/muc-%d/trang", i)
		ok, why := b.Offer(url)
		if !ok {
			if !strings.Contains(why, "the host has spent its budget") {
				t.Fatalf("the host stopped for the wrong reason: %s", why)
			}
			break
		}
		spent++
		b.Fetched(url, bien.Empty)
	}
	if spent != o.HostStart {
		t.Errorf("the host spent %d against a starting budget of %d", spent, o.HostStart)
	}
}

// Asking is what costs, so asking is what is charged. A budget that is only
// checked and never debited is not a budget.
func TestAskingTwiceSpendsTwice(t *testing.T) {
	b := bien.NewBudget(small())
	for range 3 {
		if ok, _ := b.Offer("https://vnexpress.net/tin-tuc/bai-viet"); !ok {
			t.Fatal("the same URL was refused before the budget ran out")
		}
	}
	if spent, _, _ := b.Spent("vnexpress.net"); spent != 3 {
		t.Errorf("three offers spent %d", spent)
	}
}

// Two hosts running the same forum software are two sites, and one of them
// running out has nothing to do with the other.
func TestOneHostRunningOutDoesNotTouchAnother(t *testing.T) {
	b := bien.NewBudget(small())
	crawl(b, "https://mot.vn/thread/%d", bien.Empty, 100)

	if ok, why := b.Offer("https://hai.vn/thread/1"); !ok {
		t.Errorf("a second host was refused because the first one failed: %s", why)
	}
	if b.Hosts() != 2 {
		t.Errorf("the budget knows of %d hosts", b.Hosts())
	}
}

// The report a person reads when a host they expected in the corpus is not in
// it. The template that spent the most comes first, because that is the one to
// look at.
func TestTheHeaviestSpenderIsWhatGetsReported(t *testing.T) {
	b := bien.NewBudget(small())
	crawl(b, "https://diendan.vn/thread/%d", bien.New, 40)
	crawl(b, "https://diendan.vn/lich/2024-03-%02d", bien.Empty, 20)

	lines := b.Lines("diendan.vn")
	if len(lines) != 2 {
		t.Fatalf("the host has %d templates in the report", len(lines))
	}
	if lines[0].Spent < lines[1].Spent {
		t.Error("the report does not lead with the template that spent the most")
	}
	if lines[0].Closed != "" {
		t.Errorf("the template that is still running is reported as closed: %q", lines[0].Closed)
	}
	if lines[1].Closed == "" {
		t.Error("the template that was closed does not say so")
	}
	if lines[1].Empty == 0 {
		t.Error("the report does not say what came back from the closed template")
	}
}

// Closed answers the same question without a report, for the URL in front of
// somebody rather than the host behind it.
func TestWhyAParticularURLWillNotBeAskedFor(t *testing.T) {
	b := bien.NewBudget(small())
	crawl(b, "https://lich.vn/ngay/%d", bien.Empty, 100)

	why, closed := b.Closed("https://lich.vn/ngay/999")
	if !closed || !strings.Contains(why, "nothing new") {
		t.Errorf("Closed said %q, %v", why, closed)
	}

	// A template nobody has asked about is not closed, and neither is a host.
	if why, closed := b.Closed("https://lich.vn/gioi-thieu"); closed {
		t.Errorf("an untouched template is reported closed: %q", why)
	}
	if why, closed := b.Closed("https://khac.vn/a"); closed {
		t.Errorf("an untouched host is reported closed: %q", why)
	}

	// Something that will not shape is not something to ask for either.
	if _, closed := b.Closed("javascript:void(0)"); !closed {
		t.Error("something that is not a URL was reported as askable")
	}
}

// A refusal that does not say why is indistinguishable from a bug, and the
// person reading it is on the fleet at three in the morning.
func TestEveryRefusalSaysWhy(t *testing.T) {
	b := bien.NewBudget(small())
	refusals := []string{
		"https://truong.edu.vn/a/a/a/",
		"https://diendan.vn/a/b/c/d/e/f/g/h",
		"javascript:void(0)",
	}
	for _, u := range refusals {
		ok, why := b.Offer(u)
		if ok {
			t.Errorf("%s was allowed", u)
			continue
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s was refused with no reason", u)
		}
	}
	// And the same for a refusal that comes from the arithmetic rather than the
	// shape, which is the one most likely to be met in a log.
	_, why := crawl(b, "https://lich.vn/ngay/%d", bien.Empty, 100)
	if strings.TrimSpace(why) == "" {
		t.Error("a template closed by its own results was refused with no reason")
	}
}

// The budget is one thing that every worker consults, because a per worker idea
// of what a host has been asked for is no idea at all.
func TestManyWorkersShareOneBudget(t *testing.T) {
	o := small()
	o.HostStart = 100
	o.ShapeStart = 100
	b := bien.NewBudget(o)

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for w := range 20 {
		wg.Go(func() {
			for i := range 50 {
				url := fmt.Sprintf("https://diendan.vn/thread/%d", w*50+i)
				if ok, _ := b.Offer(url); ok {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()

	if allowed != o.HostStart {
		t.Errorf("twenty workers between them got %d URLs out of a budget of %d", allowed, o.HostStart)
	}
	if spent, _, _ := b.Spent("diendan.vn"); spent != o.HostStart {
		t.Errorf("the ledger says %d", spent)
	}
}

// A caller that never says what came back gets the starting allowance and
// nothing more, which is the right failure: the crawl narrows rather than runs
// away, and the report says the template earned nothing.
func TestNotSayingWhatCameBackIsNotFree(t *testing.T) {
	b := bien.NewBudget(small())
	spent := 0
	for i := range 500 {
		if ok, _ := b.Offer(fmt.Sprintf("https://diendan.vn/thread/%d", i)); !ok {
			break
		}
		spent++
	}
	if spent != small().ShapeStart {
		t.Errorf("a template nobody reported on spent %d, want the starting allowance of %d", spent, small().ShapeStart)
	}
}

// Two spellings of one URL are one URL to the budget as well, or a site could
// buy itself budget by linking to its own pages with a tracking parameter on
// them.
func TestTheBudgetCountsPagesRatherThanSpellings(t *testing.T) {
	b := bien.NewBudget(small())
	for _, u := range []string{
		"https://diendan.vn/thread/1",
		"https://diendan.vn/thread/2?utm_source=facebook",
		"https://DienDan.vn/thread/3#binh-luan",
	} {
		if ok, why := b.Offer(u); !ok {
			t.Fatalf("%s was refused: %s", u, why)
		}
	}
	if _, _, shapes := b.Spent("diendan.vn"); shapes != 1 {
		t.Errorf("three spellings of one template produced %d templates", shapes)
	}
}
