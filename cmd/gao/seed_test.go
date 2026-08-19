package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedRun(t *testing.T, in string, args ...string) (string, string, int) {
	t.Helper()
	if in != "" {
		old := stdin
		stdin = strings.NewReader(in)
		t.Cleanup(func() { stdin = old })
	}
	var out, errb strings.Builder
	code := run(&out, &errb, append([]string{"seed"}, args...))
	return out.String(), errb.String(), code
}

const ctDump = `[
  {"name_value": "vnexpress.vn\nwww.vnexpress.vn", "not_before": "2024-01-05T00:00:00"},
  {"name_value": "vnexpress.vn", "not_before": "2019-02-01T00:00:00"},
  {"name_value": "*.com.vn", "not_before": "2023-01-01T00:00:00"},
  {"name_value": "*.ho-chi-minh.vn", "not_before": "2023-01-01T00:00:00"},
  {"name_value": "thu-vien.truong.edu.vn", "not_before": "2021-08-14T00:00:00"},
  {"name_value": "shop.example.com", "not_before": "2024-05-01T00:00:00"}
]`

func TestSeedCTPrintsASeedListAndNothingElse(t *testing.T) {
	out, _, code := seedRun(t, ctDump, "ct")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	lines := strings.Fields(out)
	want := []string{"ho-chi-minh.vn", "thu-vien.truong.edu.vn", "vnexpress.vn", "www.vnexpress.vn"}
	if strings.Join(lines, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", lines, want)
	}
}

// A registrar wildcard for a suffix the public suffix list does not carry comes
// through as a registrable name, and what tells it apart from a site is that
// nothing ever named it outright.
func TestSeedCTCanKeepOnlyWhatWasNamedOutright(t *testing.T) {
	out, _, code := seedRun(t, ctDump, "ct", "-direct")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.Contains(out, "ho-chi-minh.vn") {
		t.Errorf("a name seen only below a star survived -direct:\n%s", out)
	}
	if !strings.Contains(out, "vnexpress.vn") {
		t.Errorf("a real host was dropped by -direct:\n%s", out)
	}
}

func TestSeedCTCountsWhatNamedEachHostAndWhen(t *testing.T) {
	out, _, code := seedRun(t, ctDump, "ct", "-counts")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "2 certificates") {
		t.Errorf("the host named twice is not reported as such:\n%s", out)
	}
	if !strings.Contains(out, "2019-02-01") {
		t.Errorf("the earliest certificate is not reported:\n%s", out)
	}
	// Heaviest first, because that is the order somebody reads a list in.
	if !strings.HasPrefix(strings.TrimSpace(out), "vnexpress.vn") {
		t.Errorf("the most certified host is not first:\n%s", out)
	}
}

// This subtraction is the measurement the whole route is judged on. Certificate
// Transparency is worth running only to the extent that it names hosts a seed
// list does not.
func TestSeedCTSubtractsWhatWeAlreadyHave(t *testing.T) {
	seed := filepath.Join(t.TempDir(), "seed.txt")
	body := "# what we already had\nvnexpress.vn\nwww.vnexpress.vn\n\n"
	if err := os.WriteFile(seed, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := seedRun(t, ctDump, "ct", "-seed", seed)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.Contains(out, "vnexpress.vn") {
		t.Errorf("a host already in the seed was printed as new:\n%s", out)
	}
	if !strings.Contains(out, "thu-vien.truong.edu.vn") {
		t.Errorf("a host not in the seed was not printed:\n%s", out)
	}
	if !strings.Contains(errOut, "2 already in the seed, 2 new") {
		t.Errorf("the count does not say what the route was worth: %q", errOut)
	}
}

func TestSeedCTReadsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ct.json")
	if err := os.WriteFile(path, []byte(ctDump), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := seedRun(t, "", "ct", path)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "thu-vien.truong.edu.vn") {
		t.Errorf("reading from a file lost a host:\n%s", out)
	}
}

func TestSeedCTSearchesWhenAskedTo(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ctDump))
	}))
	defer s.Close()

	out, _, code := seedRun(t, "", "ct", "-search", s.URL)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "vnexpress.vn") {
		t.Errorf("the search returned nothing usable:\n%s", out)
	}
}

func TestSeedCTSaysWhenItCannotReadWhatItWasGiven(t *testing.T) {
	_, errOut, code := seedRun(t, "khong phai json\n", "ct")
	if code == 0 {
		t.Error("something that is not a search result was read as one")
	}
	if !strings.Contains(errOut, "certificate transparency") {
		t.Errorf("the error does not say what it failed to read: %q", errOut)
	}
	if _, _, code := seedRun(t, "", "ct", filepath.Join(t.TempDir(), "khong-co")); code != 1 {
		t.Error("a file that does not exist was not an error")
	}
}

func TestSeedIsInTheSubcommandList(t *testing.T) {
	var out, errb strings.Builder
	if code := run(&out, &errb, []string{"help"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "mam") {
		t.Errorf("mam is not listed:\n%s", out.String())
	}

	out.Reset()
	if code := run(&out, &errb, []string{"seed", "help"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "ct") {
		t.Errorf("ct is not in the mam usage:\n%s", out.String())
	}
}

func TestSeedRefusesASubcommandItDoesNotHave(t *testing.T) {
	var out, errb strings.Builder
	if code := run(&out, &errb, []string{"seed", "zone"}); code == 0 {
		t.Error("an unknown subcommand exited zero")
	}
	if !strings.Contains(errb.String(), "unknown subcommand") {
		t.Errorf("the error does not say what happened: %q", errb.String())
	}
}

// oaiSite is a repository that answers the three verbs, so the CLI tests can be
// about what the command reports rather than about the protocol, which mam's own
// tests cover.
func oaiSite(t *testing.T, name string, records int) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		open := `<?xml version="1.0" encoding="UTF-8"?>
<OAI-PMH xmlns="http://www.openarchives.org/OAI/2.0/">`
		switch r.URL.Query().Get("verb") {
		case "Identify":
			_, _ = fmt.Fprintf(w, `%s<Identify><repositoryName>%s</repositoryName><protocolVersion>2.0</protocolVersion><granularity>YYYY-MM-DD</granularity><earliestDatestamp>2011-05-02</earliestDatestamp><adminEmail>thuvien@%s</adminEmail></Identify></OAI-PMH>`, open, name, "example.edu.vn")
		case "ListMetadataFormats":
			_, _ = fmt.Fprintf(w, `%s<ListMetadataFormats><metadataFormat><metadataPrefix>oai_dc</metadataPrefix></metadataFormat></ListMetadataFormats></OAI-PMH>`, open)
		case "ListRecords":
			_, _ = fmt.Fprint(w, open+"<ListRecords>")
			for i := range records {
				_, _ = fmt.Fprintf(w, `<record><header><identifier>oai:x:%d</identifier><datestamp>2021-01-0%d</datestamp></header><metadata><oai_dc:dc xmlns:oai_dc="http://www.openarchives.org/OAI/2.0/oai_dc/" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Luận án %d</dc:title><dc:identifier>ISSN 1859-1388</dc:identifier><dc:identifier>https://tainguyenso.example.edu.vn/handle/1/%d</dc:identifier></oai_dc:dc></metadata></record>`, i+1, i+1, i+1, i+1)
			}
			_, _ = fmt.Fprint(w, "</ListRecords></OAI-PMH>")
		default:
			_, _ = fmt.Fprintf(w, `%s<error code="badVerb">no</error></OAI-PMH>`, open)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestSeedOAIReportsWhatARepositorySaysAboutItself(t *testing.T) {
	s := oaiSite(t, "Kho tài liệu số", 2)
	out, _, code := seedRun(t, "", "oai", s.URL)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"Kho tài liệu số", "oai_dc", "2011-05-02", "thuvien@example.edu.vn"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is not in the report:\n%s", want, out)
		}
	}
}

func TestSeedOAIHarvestsLinksForTheFrontier(t *testing.T) {
	s := oaiSite(t, "Kho tài liệu số", 3)
	out, _, code := seedRun(t, "", "oai", "-links", s.URL)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	lines := strings.Fields(out)
	if len(lines) != 3 {
		t.Fatalf("got %d links, want 3:\n%s", len(lines), out)
	}
	// An ISSN is a dc:identifier and it is not a link.
	for _, l := range lines {
		if !strings.HasPrefix(l, "https://") {
			t.Errorf("something that is not a URL reached the frontier: %q", l)
		}
	}
}

// This count is what P03-6 is a prediction about, so a repository that is down
// has to be visible rather than quietly missing from a list of links.
func TestSeedOAICountsHowManyAnswered(t *testing.T) {
	good := oaiSite(t, "Kho một", 1)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>Thư viện</body></html>"))
	}))
	defer bad.Close()

	out, errOut, code := seedRun(t, "", "oai", good.URL, bad.URL)
	if code != 1 {
		t.Errorf("a repository that does not answer: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(errOut, "1 of 2 repositories answered") {
		t.Errorf("the count does not say what happened:\n%s", errOut)
	}
	if !strings.Contains(errOut, bad.URL) {
		t.Errorf("the repository that failed is not named:\n%s", errOut)
	}
}

func TestSeedOAIReadsAListOfRepositories(t *testing.T) {
	s := oaiSite(t, "Kho tài liệu số", 1)
	in := "# the ones we know about\n" + s.URL + "\n\n"
	out, errOut, code := seedRun(t, in, "oai", "-links")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(errOut, "1 of 1 repository answered") {
		t.Errorf("blank lines or comments were counted as repositories:\n%s", errOut)
	}
}

func TestSeedOAIWantsADateItCanRead(t *testing.T) {
	s := oaiSite(t, "Kho tài liệu số", 1)
	_, errOut, code := seedRun(t, "", "oai", "-from", "hom qua", s.URL)
	if code != 2 {
		t.Errorf("an unreadable date: exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "2024-03-15") {
		t.Errorf("the error does not say what a date looks like: %q", errOut)
	}
}

func TestSeedOAINeedsSomethingToAsk(t *testing.T) {
	_, errOut, code := seedRun(t, "", "oai")
	if code == 0 {
		t.Error("an empty list of repositories exited zero")
	}
	if !strings.Contains(errOut, "no urls") {
		t.Errorf("the error does not say what is missing: %q", errOut)
	}
}
