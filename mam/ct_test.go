package mam_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamnd/gao/mam"
)

// The seed set is the one input to a crawl that cannot be crawled for, so every
// test here is about a shape that appears in the real logs and is not a website.

func hosts(t *testing.T, body, suffix string) []mam.Host {
	t.Helper()
	got, err := mam.Hosts(strings.NewReader(body), suffix)
	if err != nil {
		t.Fatalf("reading results: %v", err)
	}
	return got
}

func names(hs []mam.Host) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Name)
	}
	return out
}

func has(hs []mam.Host, name string) bool {
	for _, h := range hs {
		if h.Name == name {
			return true
		}
	}
	return false
}

func TestACertificateNamesTheHostsItCovers(t *testing.T) {
	got := hosts(t, `[
	  {"name_value": "vnexpress.vn\nwww.vnexpress.vn", "common_name": "vnexpress.vn", "not_before": "2024-01-05T00:00:00"},
	  {"name_value": "diendan.com.vn", "common_name": "diendan.com.vn", "not_before": "2019-06-01T00:00:00"}
	]`, "vn")

	want := []string{"diendan.com.vn", "vnexpress.vn", "www.vnexpress.vn"}
	if strings.Join(names(got), " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", names(got), want)
	}
}

// Every certificate is logged twice, as a precertificate and as itself, and a
// host under continuous renewal has a new pair every ninety days. A caller that
// counted rows would be off by more than an order of magnitude.
func TestOneHostRenewedForYearsIsStillOneHost(t *testing.T) {
	got := hosts(t, `[
	  {"name_value": "tuoitre.vn", "not_before": "2024-01-05T00:00:00"},
	  {"name_value": "tuoitre.vn", "not_before": "2024-01-05T00:00:00"},
	  {"name_value": "tuoitre.vn", "not_before": "2023-04-01T00:00:00"},
	  {"name_value": "tuoitre.vn", "not_before": "2018-11-20T00:00:00"}
	]`, "vn")

	if len(got) != 1 {
		t.Fatalf("one host arrived as %d: %v", len(got), names(got))
	}
	if got[0].Certs != 4 {
		t.Errorf("the host was named by 4 certificates, counted %d", got[0].Certs)
	}
	if y := got[0].First.Year(); y != 2018 {
		t.Errorf("the earliest certificate is from 2018, got %d", y)
	}
}

// A wildcard is not a host. It is the statement that a host exists below a name,
// and the name is the useful part.
func TestAWildcardIsTheNameUnderIt(t *testing.T) {
	got := hosts(t, `[{"name_value": "*.vnexpress.vn\n*.sub.diendan.com.vn"}]`, "vn")

	for _, want := range []string{"vnexpress.vn", "sub.diendan.com.vn"} {
		if !has(got, want) {
			t.Errorf("the name under the wildcard was lost: %v", names(got))
		}
	}
	for _, h := range got {
		if strings.Contains(h.Name, "*") {
			t.Errorf("a wildcard reached the seed set: %q", h.Name)
		}
	}
}

// This is the one that pays for the public suffix list. Registrars hold wildcard
// certificates for the second level suffixes, and `.vn` has com.vn, edu.vn,
// gov.vn and the province names under it. Seeding those means asking for pages
// at a name that has never resolved to a web server.
func TestTheNameUnderARegistrarWildcardIsNotASite(t *testing.T) {
	got := hosts(t, `[{"name_value": "*.com.vn\n*.edu.vn\n*.hanoi.vn\n*.truong.edu.vn"}]`, "vn")

	for _, gone := range []string{"com.vn", "edu.vn", "hanoi.vn"} {
		if has(got, gone) {
			t.Errorf("%s is a public suffix and was seeded as a site: %v", gone, names(got))
		}
	}
	if !has(got, "truong.edu.vn") {
		t.Errorf("a real host below a public suffix was dropped with them: %v", names(got))
	}
}

// khachhang.vn.example.com is a shape vendors really use for staging, and it is
// not a Vietnamese host. A substring test would take it.
func TestVNInTheMiddleIsNotAVNHost(t *testing.T) {
	got := hosts(t, `[{"name_value": "khachhang.vn.example.com\nbao.vn"}]`, "vn")

	if has(got, "khachhang.vn.example.com") {
		t.Errorf("a .com host was read as .vn: %v", names(got))
	}
	if !has(got, "bao.vn") {
		t.Errorf("the .vn host was dropped: %v", names(got))
	}
}

// The last three characters of evn are not .vn either.
func TestASuffixThatOnlyLooksLikeOne(t *testing.T) {
	if has(hosts(t, `[{"name_value": "cong-ty.evn"}]`, "vn"), "cong-ty.evn") {
		t.Error("a host ending in evn was read as a .vn host")
	}
}

// A certificate issued to a person carries an email address in a subject
// alternative name. It ends in a domain and it is not a host.
func TestWhatIsInTheLogsAndIsNotAHost(t *testing.T) {
	got := hosts(t, `[{"name_value": "admin@bo.gov.vn\n_dmarc.bo.gov.vn\nmay_chu.bo.gov.vn\n192.168.1.10\n-xau.bo.gov.vn\nbo.gov.vn"}]`, "vn")

	if len(got) != 1 || got[0].Name != "bo.gov.vn" {
		t.Errorf("got %v, want only bo.gov.vn", names(got))
	}
}

func TestTheSameHostSpelledLoudlyIsTheSameHost(t *testing.T) {
	got := hosts(t, `[{"name_value": "VnExpress.VN\nvnexpress.vn.\n  vnexpress.vn  "}]`, "vn")

	if len(got) != 1 {
		t.Errorf("three spellings of one host arrived as %d: %v", len(got), names(got))
	}
}

// Certificates carry internationalized names as A-labels, which is the same form
// the frontier canonicalizes to, so the two agree without a conversion step.
func TestAnInternationalizedNameArrivesReadyToUse(t *testing.T) {
	got := hosts(t, `[{"name_value": "xn--th-e0a.vn"}]`, "vn")

	if !has(got, "xn--th-e0a.vn") {
		t.Errorf("a punycode host was dropped: %v", names(got))
	}
}

// Certificates issued before subject alternative names became mandatory carry
// the host only in the common name, and those sit on the oldest and least
// indexed hosts, which is exactly the population this route exists to find.
func TestAnOldCertificateWithNoSubjectAlternativeName(t *testing.T) {
	got := hosts(t, `[{"name_value": "", "common_name": "cu.org.vn", "not_before": "2014-02-01T00:00:00"}]`, "vn")

	if !has(got, "cu.org.vn") {
		t.Errorf("a host named only in the common name was lost: %v", names(got))
	}
}

func TestSomethingThatIsNotASearchResult(t *testing.T) {
	if _, err := mam.Hosts(strings.NewReader("<html>rate limited</html>"), "vn"); err == nil {
		t.Error("a page that is not a search result was read as one")
	}
}

// Certificate Transparency is worth the trouble only to the extent that it names
// hosts a seed list does not, so the measurement is a subtraction.
func TestWhatIsNewIsWhatThisIsFor(t *testing.T) {
	found := hosts(t, `[{"name_value": "vnexpress.vn\nnoi-bo.truong.edu.vn\nhtx-caphe.com.vn"}]`, "vn")
	got := mam.New(found, []string{"vnexpress.vn", "www.tuoitre.vn"})

	if len(got) != 2 {
		t.Fatalf("got %d new hosts, want 2: %v", len(got), names(got))
	}
	if has(got, "vnexpress.vn") {
		t.Errorf("a host already in the seed was reported as new: %v", names(got))
	}
}

func TestASeedSpelledDifferentlyIsStillTheSameSeed(t *testing.T) {
	found := hosts(t, `[{"name_value": "vnexpress.vn"}]`, "vn")
	if got := mam.New(found, []string{"VnExpress.vn."}); len(got) != 0 {
		t.Errorf("a seed spelled loudly did not match: %v", names(got))
	}
}

func TestSearchAsksForTheSuffixAndReadsTheAnswer(t *testing.T) {
	var asked string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name_value": "bao.vn", "not_before": "2024-01-01T00:00:00"}]`))
	}))
	defer s.Close()

	got, err := mam.Search(context.Background(), s.Client(), s.URL, ".vn")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(asked, "q=%25.vn") {
		t.Errorf("the query does not ask for the suffix: %q", asked)
	}
	if len(got) != 1 || got[0].Name != "bao.vn" {
		t.Errorf("got %v, want bao.vn", names(got))
	}
}

// Being rate limited returns a page rather than an error, and a search that read
// it as an empty result would report that Vietnam has no hosts.
func TestBeingTurnedAwayIsNotAnEmptyResult(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer s.Close()

	got, err := mam.Search(context.Background(), s.Client(), s.URL, "vn")
	if err == nil {
		t.Fatalf("being rate limited was read as %d hosts", len(got))
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("the error does not say what happened: %v", err)
	}
}

// The public suffix list is not complete for `.vn`. It carries the generic
// second levels and some provinces and not others, so a registrar wildcard for a
// province it does not carry comes through as a registrable name. What separates
// that from a real site is not the list but the evidence: a name that only ever
// appeared below a star is a name nobody proved they were serving.
func TestWhatOnlyEverAppearedBelowAStarIsAWeakerLead(t *testing.T) {
	got := hosts(t, `[
	  {"name_value": "*.ho-chi-minh.vn"},
	  {"name_value": "*.truong.edu.vn\ntruong.edu.vn"}
	]`, "vn")

	for _, h := range got {
		switch h.Name {
		case "ho-chi-minh.vn":
			if h.Direct != 0 {
				t.Errorf("a name seen only below a star was counted as named directly: %+v", h)
			}
		case "truong.edu.vn":
			if h.Direct != 1 {
				t.Errorf("a host named outright was not counted as such: %+v", h)
			}
			if h.Certs != 2 {
				t.Errorf("both certificates should count toward the host: %+v", h)
			}
		default:
			t.Errorf("unexpected host %q", h.Name)
		}
	}
}
