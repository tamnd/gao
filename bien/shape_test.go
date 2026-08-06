package bien_test

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/bien"
)

// The shape is the measure the whole budget rests on, and it fails in two
// directions. Too coarse and every article on a news site collapses onto one
// shape, so the site gets the budget of a single page. Too fine and a calendar
// gets a fresh budget for every day of every year, forever. Most of these tests
// are one direction or the other named out loud.

func shape(t *testing.T, rawurl string) bien.Shape {
	t.Helper()
	s, err := bien.Of(rawurl)
	if err != nil {
		t.Fatalf("%s: %v", rawurl, err)
	}
	return s
}

func shapeOf(t *testing.T, rawurl string) string {
	t.Helper()
	return shape(t, rawurl).String()
}

// The whole point, in one test. A thousand URLs off one template are one shape.
func TestOneTemplateIsOneShape(t *testing.T) {
	groups := [][]string{
		{
			"https://diendan.vn/thread/1",
			"https://diendan.vn/thread/2",
			"https://diendan.vn/thread/998877",
		},
		{
			"https://diendan.vn/f/12?page=1",
			"https://diendan.vn/f/9?page=40",
		},
		{
			"https://lich.vn/su-kien/2024-03-15",
			"https://lich.vn/su-kien/2019-11-02",
		},
		{
			"https://shop.vn/gio-hang/550e8400-e29b-41d4-a716-446655440000",
			"https://shop.vn/gio-hang/6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		},
	}
	for _, group := range groups {
		first := shapeOf(t, group[0])
		for _, rawurl := range group[1:] {
			if got := shapeOf(t, rawurl); got != first {
				t.Errorf("%s and %s are one template and shape to %q and %q", group[0], rawurl, first, got)
			}
		}
	}
}

// The expensive direction. Two pages that shape together share one budget, so a
// news site would get the crawl allowance of a single article.
func TestTwoTemplatesStayTwoShapes(t *testing.T) {
	apart := [][2]string{
		{"https://vnexpress.net/the-thao/bai-1", "https://vnexpress.net/kinh-doanh/bai-1"},
		{"https://vnexpress.net/tin-tuc/1", "https://vnexpress.net/tin-tuc/1/binh-luan"},
		{"https://diendan.vn/f/12?page=2", "https://diendan.vn/f/12?sort=moi"},
		{"https://diendan.vn/f/12", "https://diendan.vn/f/12/"},
		{"https://vnexpress.net/a", "https://tuoitre.vn/a"},
	}
	for _, pair := range apart {
		a, b := shapeOf(t, pair[0]), shapeOf(t, pair[1])
		if a == b {
			t.Errorf("%s and %s are two templates and both shape to %q", pair[0], pair[1], a)
		}
	}
}

// The slug is the part of a URL a person wrote, and it names the page rather
// than selecting it. Two articles with different slugs are two pages and have to
// survive as two shapes, or a news site with a hundred thousand articles reads
// as one page with a title that keeps changing.
func TestTheSlugIsWhatMakesAPageAPage(t *testing.T) {
	a := shapeOf(t, "https://vnexpress.net/tin-tuc/gia-lua-tang-manh-4712345.html")
	b := shapeOf(t, "https://vnexpress.net/tin-tuc/mua-lu-mien-trung-4712346.html")
	if a == b {
		t.Errorf("two articles shaped to the same %q", a)
	}
	// And the numeric part still collapses, or the same article at two ids
	// would be two shapes.
	if !strings.Contains(shapeOf(t, "https://vnexpress.net/tin-tuc/12345"), bien.KindNumber) {
		t.Error("an article id did not collapse to a number")
	}
}

// A date and a number are different findings, and the reason is what happens
// next. A number in a path is an article. A date in a path is either an archive,
// which is finite, or a calendar, which is not, and only the date reading gives
// a trap detector anything to work with.
func TestADateIsNotJustANumber(t *testing.T) {
	for _, dated := range []string{
		"https://lich.vn/su-kien/2024-03-15",
		"https://lich.vn/su-kien/2024_03_15",
		"https://lich.vn/su-kien/20240315",
		"https://lich.vn/su-kien/15-03-2024",
		"https://lich.vn/su-kien/15_03_2024",
	} {
		s := shape(t, dated)
		if !strings.Contains(s.Path, bien.KindDate) {
			t.Errorf("%s shaped to %q with no date in it", dated, s.Path)
		}
		if !s.Dated() {
			t.Errorf("%s is not reported as dated", dated)
		}
	}
}

// A Vietnamese site writes the day first, the way most of the world does. A
// crawler built against American layouts reads 03-06-2024 as the third of June
// and then does not recognize 15-03-2024 as a date at all, which is the half of
// the calendar it then walks into.
func TestTheDayComesFirstHere(t *testing.T) {
	s := shape(t, "https://lich.vn/su-kien/15-03-2024")
	if !s.Dated() {
		t.Error("15-03-2024 was not read as the fifteenth of March")
	}
}

// A year on its own is a number. Every article id in a certain range would
// otherwise read as a year, and /tin-tuc/2019/ is doing the same job as
// /tin-tuc/47/.
func TestAYearOnItsOwnIsANumber(t *testing.T) {
	s := shape(t, "https://vnexpress.net/luu-tru/2019")
	if s.Dated() {
		t.Errorf("a bare year was read as a date: %q", s.Path)
	}
	if !strings.Contains(s.Path, bien.KindNumber) {
		t.Errorf("a bare year shaped to %q", s.Path)
	}
}

// A number that is not a date must not be turned into one by a lenient parse.
// 99991231 parses as a date in the year 9999, and a URL with that in it is a
// template nobody filled in.
func TestSomethingShapedLikeADateButNotOneIsANumber(t *testing.T) {
	for _, notADate := range []string{"99991231", "20241332", "18001225", "00000000"} {
		s := shape(t, "https://vnexpress.net/a/"+notADate)
		if s.Dated() {
			t.Errorf("%s was read as a date: %q", notADate, s.Path)
		}
	}
}

// The Vietnamese half of the calendar problem. A locally written event calendar
// puts the date in the query with the field named in Vietnamese, and a detector
// that only knew `month` and `year` would see nothing there at all.
func TestACalendarNamedInVietnameseIsStillACalendar(t *testing.T) {
	for _, rawurl := range []string{
		"https://truong.edu.vn/lich?thang=3&nam=2024",
		"https://truong.edu.vn/su-kien?ngay=15",
		"https://truong.edu.vn/lich-hoc?tuan=12",
		"https://truong.edu.vn/tim?tu-ngay=01-01-2024",
	} {
		if !shape(t, rawurl).Dated() {
			t.Errorf("%s is a calendar and was not read as one", rawurl)
		}
	}
	// And the English half, since half the Vietnamese web runs WordPress.
	for _, rawurl := range []string{
		"https://blog.vn/events?month=3&year=2024",
		"https://blog.vn/archive?from=2024-01-01&to=2024-12-31",
	} {
		if !shape(t, rawurl).Dated() {
			t.Errorf("%s is a calendar and was not read as one", rawurl)
		}
	}
}

// A session or a hash in the path is a machine generated identifier, and a
// budget that counted them apart would count one page per session for as long as
// the sessions kept coming.
func TestAnOpaqueIdentifierCollapses(t *testing.T) {
	for _, rawurl := range []string{
		"https://shop.vn/gio-hang/550e8400-e29b-41d4-a716-446655440000",
		"https://shop.vn/gio-hang/9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
	} {
		s := shape(t, rawurl)
		if !strings.Contains(s.Path, bien.KindID) {
			t.Errorf("%s shaped to %q", rawurl, s.Path)
		}
	}
}

// The other direction on the same rule. A Vietnamese slug with the marks taken
// off is long and made of letters, and some of those letters are the ones hex
// uses. Reading it as an identifier would collapse every article under a
// category onto one shape.
func TestALongSlugIsNotAnIdentifier(t *testing.T) {
	for _, slug := range []string{
		"chuyen-de-dac-biet-ve-nong-nghiep-cao",
		"deadbeefcafedecadedbadfadedfacade",
		"bai-viet-ve-cac-be-cac-ca-de-be",
	} {
		s := shape(t, "https://vnexpress.net/tin-tuc/"+slug)
		if strings.Contains(s.Path, bien.KindID) {
			t.Errorf("%q was read as a machine generated identifier: %q", slug, s.Path)
		}
	}
}

// The signature of a relative link resolved against itself. A server serving
// /a/b/ that answers a relative link to b/ produces /a/b/b/, then /a/b/b/b/, and
// it never stops. Depth alone will not name it until the crawl is thousands of
// pages in.
func TestALinkResolvedAgainstItselfIsVisibleOnTheThirdHop(t *testing.T) {
	steps := []struct {
		url  string
		want int
	}{
		{"https://truong.edu.vn/tin/bai/", 1},
		{"https://truong.edu.vn/tin/bai/bai/", 2},
		{"https://truong.edu.vn/tin/bai/bai/bai/", 3},
	}
	for _, step := range steps {
		if got := shape(t, step.url).Repeats(); got != step.want {
			t.Errorf("%s repeats %d, want %d", step.url, got, step.want)
		}
	}
	// A path that repeats a segment without being adjacent is an ordinary path.
	// /tin/bai/khac/bai is a site with a word it uses twice.
	if got := shape(t, "https://truong.edu.vn/tin/bai/khac/bai").Repeats(); got != 1 {
		t.Errorf("a path with one word used twice reports %d repeats", got)
	}
}

// Depth is the cheapest signal there is and it is not a detector on its own.
// Vietnamese forums nest genuinely deep, so this is measured and handed on
// rather than acted on here.
func TestDepthIsCounted(t *testing.T) {
	cases := map[string]int{
		"https://diendan.vn":                    0,
		"https://diendan.vn/":                   0,
		"https://diendan.vn/f":                  1,
		"https://diendan.vn/f/12":               2,
		"https://diendan.vn/f/12/thread/34/p/2": 6,
	}
	for rawurl, want := range cases {
		if got := shape(t, rawurl).Depth(); got != want {
			t.Errorf("%s is %d deep, want %d", rawurl, got, want)
		}
	}
}

// Two URLs that canonicalize together have to shape together, because the shape
// is taken of the canonical form and there is one entry point that guarantees
// it. A second path in would be a second set of rules to keep in agreement.
func TestWhatCanonicalizesTogetherShapesTogether(t *testing.T) {
	pairs := [][2]string{
		{"https://VnExpress.net/tin-tuc/1?utm_source=zalo", "https://vnexpress.net/tin-tuc/1"},
		{"https://diendan.vn/f/12?b=2&a=1", "https://diendan.vn/f/12?a=1&b=2"},
		{"https://lich.vn/a/./2024-03-15#top", "https://lich.vn/a/2024-03-15"},
	}
	for _, pair := range pairs {
		if !bien.Same(pair[0], pair[1]) {
			t.Fatalf("%s and %s do not canonicalize together, which is a different bug", pair[0], pair[1])
		}
		if a, b := shapeOf(t, pair[0]), shapeOf(t, pair[1]); a != b {
			t.Errorf("%s and %s canonicalize together and shape to %q and %q", pair[0], pair[1], a, b)
		}
	}
}

// The shape is what a person reads when they ask why a host stopped being
// crawled, so it has to say what it found rather than only what it is.
func TestAShapeSaysWhatIsWrongWithIt(t *testing.T) {
	plain := shape(t, "https://vnexpress.net/tin-tuc/bai-viet")
	if plain.Describe() != plain.String() {
		t.Errorf("an ordinary URL is described as %q", plain.Describe())
	}

	dated := shape(t, "https://lich.vn/su-kien/2024-03-15")
	if !strings.Contains(dated.Describe(), "date") {
		t.Errorf("a dated shape is described as %q", dated.Describe())
	}

	looping := shape(t, "https://truong.edu.vn/tin/bai/bai/bai/bai")
	if !strings.Contains(looping.Describe(), "repeats") {
		t.Errorf("a looping shape is described as %q", looping.Describe())
	}
}

// The shape of something that is not a URL is not a shape. It fails the same way
// canonicalization does, because it goes through the same door.
func TestSomethingThatIsNotAURLHasNoShape(t *testing.T) {
	for _, rawurl := range []string{"javascript:void(0)", "/no-host", "mailto:a@b.vn"} {
		if s, err := bien.Of(rawurl); err == nil {
			t.Errorf("%s shaped to %q", rawurl, s)
		}
	}
}
