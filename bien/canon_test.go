package bien_test

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/bien"
)

// Canonicalization is a trade, and every test here is one side of it. Merging
// two URLs that are one page saves a fetch. Merging two URLs that are two pages
// loses one of them permanently and quietly, because nothing downstream can tell
// that a page was never asked for.

func canon(t *testing.T, rawurl string) string {
	t.Helper()
	got, err := bien.Canon(rawurl)
	if err != nil {
		t.Fatalf("%s: %v", rawurl, err)
	}
	return got
}

// The spellings of one page. Every pair here is one document that a crawl
// without this would fetch twice.
func TestTwoSpellingsOfOnePageBecomeOne(t *testing.T) {
	same := [][2]string{
		{"http://VnExpress.net/tin-tuc", "http://vnexpress.net/tin-tuc"},
		{"http://vnexpress.net:80/a", "http://vnexpress.net/a"},
		{"https://vnexpress.net:443/a", "https://vnexpress.net/a"},
		{"https://vnexpress.net", "https://vnexpress.net/"},
		{"https://vnexpress.net/a#phan-2", "https://vnexpress.net/a"},
		{"https://vnexpress.net/a//b", "https://vnexpress.net/a/b"},
		{"https://vnexpress.net/a/./b", "https://vnexpress.net/a/b"},
		{"https://vnexpress.net/a/b/../c", "https://vnexpress.net/a/c"},
		{"https://vnexpress.net/../a", "https://vnexpress.net/a"},
		{"https://vnexpress.net/a?b=1&a=2", "https://vnexpress.net/a?a=2&b=1"},
		{"https://vnexpress.net/a?utm_source=facebook", "https://vnexpress.net/a"},
		{"https://vnexpress.net/a?id=7&fbclid=IwAR0xyz", "https://vnexpress.net/a?id=7"},
		{"  https://vnexpress.net/a  ", "https://vnexpress.net/a"},
		{"HTTPS://vnexpress.net/a", "https://vnexpress.net/a"},
	}
	for _, pair := range same {
		a, b := canon(t, pair[0]), canon(t, pair[1])
		if a != b {
			t.Errorf("%s and %s are one page and canonicalize to %q and %q", pair[0], pair[1], a, b)
		}
	}
}

// The other side of the trade, and the expensive side. Each of these pairs is
// two pages, and a rule that merged them would lose one of them with nothing
// downstream able to notice.
func TestTwoPagesStayTwoPages(t *testing.T) {
	apart := [][2]string{
		{"https://vnexpress.net/a", "https://vnexpress.net/a/"},
		{"https://vnexpress.net/a", "http://vnexpress.net/a"},
		{"https://vnexpress.net/a", "https://www.vnexpress.net/a"},
		{"https://vnexpress.net/A", "https://vnexpress.net/a"},
		{"https://vnexpress.net/a?p=1", "https://vnexpress.net/a?p=2"},
		{"https://vnexpress.net/a?p=1", "https://vnexpress.net/a"},
		{"https://vnexpress.net:8080/a", "https://vnexpress.net/a"},
		{"https://vnexpress.net/a", "https://tuoitre.vn/a"},
	}
	for _, pair := range apart {
		a, b := canon(t, pair[0]), canon(t, pair[1])
		if a == b {
			t.Errorf("%s and %s are two pages and both canonicalize to %q", pair[0], pair[1], a)
		}
	}
}

// A trailing slash is kept, which is the entry in the list above that people
// argue with, so it gets its own reason. A server is free to serve different
// things at /tin-tuc and /tin-tuc/, and most of them serve a redirect from one
// to the other. Following that redirect costs one request. Merging them by hand
// costs whichever of the two pages was real, and there is no way to find out
// afterwards which one that was.
func TestATrailingSlashIsTheServersBusiness(t *testing.T) {
	if bien.Same("https://vnexpress.net/tin-tuc", "https://vnexpress.net/tin-tuc/") {
		t.Error("a trailing slash was canonicalized away, and only the server knows whether that was right")
	}
	// The root is the exception, because there is no such thing as a request
	// for a host with no path. Every client sends a slash.
	if !bien.Same("https://vnexpress.net", "https://vnexpress.net/") {
		t.Error("the root with and without its slash are two URLs")
	}
}

// A .vn domain can be written in Vietnamese, and a link written that way and a
// link written in punycode are one host that a byte comparison calls two.
func TestAVietnameseDomainAndItsPunycodeAreOneHost(t *testing.T) {
	if !bien.Same("https://cà-phê.vn/tin-tuc", "https://xn--c-ph-0na7d.vn/tin-tuc") {
		a, _ := bien.Canon("https://cà-phê.vn/tin-tuc")
		t.Errorf("a Vietnamese domain canonicalized to %q, and the punycode spelling is a different string", a)
	}
	// And the punycode form is what comes out, since that is what goes on the
	// wire and what a seen set of 280M entries should be holding. Case in a
	// Vietnamese domain folds the same way it does anywhere else.
	got := canon(t, "https://Cà-Phê.vn/tin-tuc")
	if got != "https://xn--c-ph-0na7d.vn/tin-tuc" {
		t.Errorf("the canonical form is %q", got)
	}
	if strings.ContainsAny(got, "àê") {
		t.Errorf("the canonical form is %q and carries letters a DNS query cannot", got)
	}
}

// Not everything that parses is a URL a crawl follows. The refusals are as much
// a part of this as the merges, because the alternative to refusing is a request
// to somewhere nobody can name.
func TestWhatWillNotCanonicalize(t *testing.T) {
	bad := []string{
		"javascript:alert(1)",
		"mailto:ai@vnexpress.net",
		"file:///etc/passwd",
		"ftp://ftp.vnexpress.net/a",
		"/tin-tuc/khong-co-host",
		"https://",
		"http://[not a host]/a",
	}
	for _, rawurl := range bad {
		if got, err := bien.Canon(rawurl); err == nil {
			t.Errorf("%s canonicalized to %q", rawurl, got)
		}
	}
}

// A URL carrying a password is either a credential nobody meant to publish or a
// phishing link dressed as a domain, and either way it is not something a crawl
// writes into a frontier of 280M entries that ends up in a log.
func TestACredentialInALinkDoesNotSurvive(t *testing.T) {
	got := canon(t, "https://nguoidung:matkhau@vnexpress.net/a")
	if strings.Contains(got, "matkhau") || strings.Contains(got, "nguoidung") {
		t.Errorf("the canonical form is %q", got)
	}
	if got != "https://vnexpress.net/a" {
		t.Errorf("the canonical form is %q, want the host and the path", got)
	}
}

// The tracking list is where a crawler quietly loses pages, so it is closed and
// the closing is the test. A prefix rule would eventually eat a parameter that
// selects the page, and a URL missing that parameter fetches the wrong thing
// without failing.
func TestOnlyTheNamedParametersAreDropped(t *testing.T) {
	// These are the ones a page serves identically with and without.
	for _, drop := range []string{"utm_source=zalo", "fbclid=IwAR0", "gclid=abc", "PHPSESSID=deadbeef"} {
		got := canon(t, "https://vnexpress.net/a?"+drop)
		if strings.Contains(got, "?") {
			t.Errorf("%s survived canonicalization: %q", drop, got)
		}
	}
	// And these are the ones that select the page. Every one of them is a real
	// parameter off a Vietnamese site, and losing any of them loses content.
	for _, keep := range []string{"utm=1", "page=4", "id=88231", "trang=2", "chuyen-muc=the-thao", "s=tim+kiem", "start=40"} {
		got := canon(t, "https://vnexpress.net/a?"+keep)
		if !strings.Contains(got, keep) {
			t.Errorf("%s was dropped: %q", keep, got)
		}
	}
}

// A link with one bad escape in it is still a link, and the parameter that
// failed to decode may be the one that selects the page. url.ParseQuery would
// drop it and return the rest, which is the failure that reads as success.
func TestABadEscapeDoesNotSilentlyLoseAParameter(t *testing.T) {
	got := canon(t, "https://vnexpress.net/a?id=7&q=%zz")
	if !strings.Contains(got, "id=7") {
		t.Errorf("the good parameter was lost along with the bad one: %q", got)
	}
	if !strings.Contains(got, "%zz") {
		t.Errorf("the parameter that would not decode was dropped: %q", got)
	}
}

// Canonicalizing twice has to change nothing. It is the property that makes the
// seen set work: a URL that arrives already canonical, from a sitemap or from a
// previous run, has to land on the same string as one that arrives raw.
func TestCanonicalizingACanonicalURLChangesNothing(t *testing.T) {
	for _, rawurl := range []string{
		"http://VnExpress.net:80/a/./b/../c?b=2&utm_source=x&a=1#top",
		"https://cà-phê.vn/quán/",
		"https://vnexpress.net",
		"https://diendan.vn/f/12/thread-345?page=2",
	} {
		once := canon(t, rawurl)
		twice := canon(t, once)
		if once != twice {
			t.Errorf("%s canonicalized to %q and then to %q", rawurl, once, twice)
		}
	}
}

// Same is asked whether a crawl may skip the second URL, so a URL it cannot read
// has to answer no. Answering yes would skip it, and skipping something because
// it could not be parsed is how a frontier loses a host.
func TestAURLThatWillNotParseIsNotTheSameAsAnything(t *testing.T) {
	if bien.Same("javascript:void(0)", "javascript:void(0)") {
		t.Error("two copies of something that is not a URL were called one page")
	}
	if bien.Same("https://vnexpress.net/a", "javascript:void(0)") {
		t.Error("a URL and a non URL were called one page")
	}
}
