package mam_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/mam"
)

// A university repository holds theses and journal issues, which is the highest
// quality Vietnamese prose per byte anywhere in this project, and it is close to
// invisible to a crawler. Every test here is about not reporting one of those as
// broken when it is working, because P03-6 is a claim about Vietnamese
// universities and a protocol bug on our side would look like one about them.

// repo is an OAI-PMH server that answers from a table of canned responses keyed
// by the verb, so a test can say what a repository does rather than build one.
type repo struct {
	Server *httptest.Server
	Client *http.Client
}

func serve(t *testing.T, answer func(q url.Values) string) *repo {
	t.Helper()
	r := &repo{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(answer(req.URL.Query())))
	}))
	t.Cleanup(r.Server.Close)
	r.Client = r.Server.Client()
	return r
}

func envelope(inner string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<OAI-PMH xmlns="http://www.openarchives.org/OAI/2.0/">
  <responseDate>2026-03-15T09:30:00Z</responseDate>
` + inner + `
</OAI-PMH>`
}

func identifyXML(granularity string) string {
	return envelope(`  <Identify>
    <repositoryName>Kho tài liệu số Đại học Quốc gia</repositoryName>
    <baseURL>http://example.edu.vn/oai</baseURL>
    <protocolVersion>2.0</protocolVersion>
    <adminEmail>thuvien@example.edu.vn</adminEmail>
    <earliestDatestamp>2009-04-01T00:00:00Z</earliestDatestamp>
    <deletedRecord>persistent</deletedRecord>
    <granularity>` + granularity + `</granularity>
  </Identify>`)
}

const formatsXML = `  <ListMetadataFormats>
    <metadataFormat><metadataPrefix>oai_dc</metadataPrefix></metadataFormat>
    <metadataFormat><metadataPrefix>didl</metadataPrefix></metadataFormat>
  </ListMetadataFormats>`

// one record, with the shape a DSpace install really produces: several
// dc:identifier values of which one is a link, and a language field that is not
// a verdict about the text.
func recordXML(id, stamp, title string) string {
	return `    <record>
      <header><identifier>` + id + `</identifier><datestamp>` + stamp + `</datestamp><setSpec>com_123</setSpec></header>
      <metadata>
        <oai_dc:dc xmlns:oai_dc="http://www.openarchives.org/OAI/2.0/oai_dc/" xmlns:dc="http://purl.org/dc/elements/1.1/">
          <dc:title>` + title + `</dc:title>
          <dc:identifier>ISSN 1859-1388</dc:identifier>
          <dc:identifier>http://tainguyenso.example.edu.vn/handle/123456789/4021</dc:identifier>
          <dc:language>vi</dc:language>
          <dc:rights>Truy cập mở</dc:rights>
          <dc:type>Thesis</dc:type>
        </oai_dc:dc>
      </metadata>
    </record>`
}

func standard(q url.Values) string {
	switch q.Get("verb") {
	case "Identify":
		return identifyXML("YYYY-MM-DDThh:mm:ssZ")
	case "ListMetadataFormats":
		return envelope(formatsXML)
	case "ListRecords":
		return envelope("  <ListRecords>\n" + recordXML("oai:example.edu.vn:123456789/4021", "2021-11-02T03:14:00Z", "Nghiên cứu về xử lý ngôn ngữ tự nhiên tiếng Việt") + "\n  </ListRecords>")
	}
	return envelope(`  <error code="badVerb">unknown verb</error>`)
}

func TestARepositorySaysWhoItIs(t *testing.T) {
	s := serve(t, standard)
	r, err := mam.Identify(context.Background(), s.Client, s.Server.URL)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if !strings.Contains(r.Name, "Đại học") {
		t.Errorf("the name came back as %q", r.Name)
	}
	if r.Earliest.Year() != 2009 {
		t.Errorf("the earliest datestamp came back as %v", r.Earliest)
	}
	if len(r.Admin) != 1 || r.Admin[0] != "thuvien@example.edu.vn" {
		t.Errorf("there is nobody to write to: %v", r.Admin)
	}
	if !r.Offers("oai_dc") || !r.Offers("didl") {
		t.Errorf("the formats came back as %v", r.Formats)
	}
}

func TestWhatARecordCarriesThatIsWorthKeeping(t *testing.T) {
	s := serve(t, standard)
	r, err := mam.Identify(context.Background(), s.Client, s.Server.URL)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	items, err := mam.Records(context.Background(), s.Client, r, mam.Harvest{})
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d records, want 1", len(items))
	}
	it := items[0]
	if !strings.Contains(it.Title, "tiếng Việt") {
		t.Errorf("the title came back as %q", it.Title)
	}
	if it.Stamp.Year() != 2021 {
		t.Errorf("the datestamp came back as %v", it.Stamp)
	}
	// dc:identifier is repeatable and mostly is not a URL. Taking the first one
	// would take an ISSN about as often as a link.
	if len(it.Links) != 1 || !strings.HasPrefix(it.Links[0], "http") {
		t.Errorf("the links came back as %v, an ISSN is not a link", it.Links)
	}
	if it.Language != "vi" || it.Rights == "" {
		t.Errorf("the record lost fields worth keeping: %+v", it)
	}
}

// This is the classic OAI-PMH bug. The protocol says a request carrying a
// resumptionToken carries nothing else, so a harvester that helpfully keeps
// sending metadataPrefix gets badArgument on page two and reports a repository
// with fifty thousand theses as one with a hundred.
func TestTheResumptionTokenGoesOnItsOwn(t *testing.T) {
	s := serve(t, func(q url.Values) string {
		switch q.Get("verb") {
		case "Identify":
			return identifyXML("YYYY-MM-DDThh:mm:ssZ")
		case "ListMetadataFormats":
			return envelope(formatsXML)
		}
		token := q.Get("resumptionToken")
		if token != "" && len(q) != 2 {
			return envelope(`  <error code="badArgument">resumptionToken must be the only argument</error>`)
		}
		switch token {
		case "":
			return envelope("  <ListRecords>\n" + recordXML("oai:x:1", "2021-01-01T00:00:00Z", "Mot") + "\n    <resumptionToken>trang-2</resumptionToken>\n  </ListRecords>")
		case "trang-2":
			return envelope("  <ListRecords>\n" + recordXML("oai:x:2", "2021-01-02T00:00:00Z", "Hai") + "\n    <resumptionToken>trang-3</resumptionToken>\n  </ListRecords>")
		default:
			// The last page carries an empty element, which is the protocol
			// saying this is the end and is not the same as carrying no element.
			return envelope("  <ListRecords>\n" + recordXML("oai:x:3", "2021-01-03T00:00:00Z", "Ba") + "\n    <resumptionToken/>\n  </ListRecords>")
		}
	})

	r, err := mam.Identify(context.Background(), s.Client, s.Server.URL)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	items, err := mam.Records(context.Background(), s.Client, r, mam.Harvest{})
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d records across three pages, want 3", len(items))
	}
}

// A response carrying an empty resumptionToken element is saying this was the
// last page. A harvester reading that as a token asks for it forever.
func TestAnEmptyTokenIsTheEndAndNotAToken(t *testing.T) {
	asks := 0
	s := serve(t, func(q url.Values) string {
		if q.Get("verb") != "ListRecords" {
			return standard(q)
		}
		asks++
		if asks > 3 {
			t.Fatal("the harvest kept asking after the last page")
		}
		return envelope("  <ListRecords>\n" + recordXML("oai:x:1", "2021-01-01T00:00:00Z", "Mot") + "\n    <resumptionToken>   </resumptionToken>\n  </ListRecords>")
	})

	r := mam.Repository{Base: s.Server.URL}
	if _, err := mam.Records(context.Background(), s.Client, r, mam.Harvest{}); err != nil {
		t.Fatalf("records: %v", err)
	}
	if asks != 1 {
		t.Errorf("a whitespace token was followed: %d requests", asks)
	}
}

// noRecordsMatch is a protocol error code and is not a failure. A repository
// with nothing published since last Tuesday says it this way, and reading every
// error code alike marks that repository broken.
func TestNothingInTheRangeIsAnAnswer(t *testing.T) {
	s := serve(t, func(q url.Values) string {
		if q.Get("verb") == "ListRecords" {
			return envelope(`  <error code="noRecordsMatch">nothing changed</error>`)
		}
		return standard(q)
	})

	r, err := mam.Identify(context.Background(), s.Client, s.Server.URL)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	_, err = mam.Records(context.Background(), s.Client, r, mam.Harvest{From: time.Now()})
	if !errors.Is(err, mam.ErrNoRecords) {
		t.Errorf("nothing in the range came back as %v", err)
	}
	// And it does not make the repository broken, which is what P03-6 counts.
	if _, err := mam.Works(context.Background(), s.Client, s.Server.URL); err != nil {
		t.Errorf("a repository with nothing new was reported as not working: %v", err)
	}
}

// A repository declaring day granularity answers badArgument to a from with a
// time of day in it, so one format everywhere is a harvest that fails on the
// repositories with the oldest software.
func TestTheDateIsWrittenTheWayTheRepositoryAsked(t *testing.T) {
	for _, tc := range []struct {
		granularity string
		want        string
	}{
		{"YYYY-MM-DD", "2026-03-15"},
		{"YYYY-MM-DDThh:mm:ssZ", "2026-03-15T09:30:00Z"},
	} {
		s := serve(t, func(q url.Values) string {
			if q.Get("verb") == "ListRecords" {
				if got := q.Get("from"); got != tc.want {
					return envelope(fmt.Sprintf(`  <error code="badArgument">from was %s</error>`, got))
				}
			}
			if q.Get("verb") == "Identify" {
				return identifyXML(tc.granularity)
			}
			return standard(q)
		})

		r, err := mam.Identify(context.Background(), s.Client, s.Server.URL)
		if err != nil {
			t.Fatalf("identify: %v", err)
		}
		from := time.Date(2026, 3, 15, 9, 30, 0, 0, time.UTC)
		if _, err := mam.Records(context.Background(), s.Client, r, mam.Harvest{From: from}); err != nil {
			t.Errorf("granularity %s: %v", tc.granularity, err)
		}
	}
}

// A repository that does not declare a granularity gets the day form, because a
// day stamp is accepted by one that supports seconds and a second stamp is
// refused by one that does not.
func TestAnUndeclaredGranularityGetsTheFormThatWorksEverywhere(t *testing.T) {
	var r mam.Repository
	if got := r.Stamp(time.Date(2026, 3, 15, 9, 30, 0, 0, time.UTC)); got != "2026-03-15" {
		t.Errorf("an undeclared granularity produced %q", got)
	}
}

// A deleted record carries a header and no metadata at all. Reading the metadata
// regardless is how a harvest ends up with documents that have no text.
func TestADeletedRecordIsAHeaderAndNothingElse(t *testing.T) {
	s := serve(t, func(q url.Values) string {
		if q.Get("verb") != "ListRecords" {
			return standard(q)
		}
		return envelope(`  <ListRecords>
    <record><header status="deleted"><identifier>oai:x:9</identifier><datestamp>2024-02-02T00:00:00Z</datestamp></header></record>
` + recordXML("oai:x:1", "2021-01-01T00:00:00Z", "Mot") + `
  </ListRecords>`)
	})

	items, err := mam.Records(context.Background(), s.Client, mam.Repository{Base: s.Server.URL}, mam.Harvest{})
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d records, want 2", len(items))
	}
	if !items[0].Deleted {
		t.Error("a withdrawn record was not marked deleted")
	}
	if len(items[0].Links) != 0 {
		t.Errorf("a withdrawn record produced links to fetch: %v", items[0].Links)
	}
	if items[1].Deleted {
		t.Error("a live record was marked deleted")
	}
}

// A repository handing back a token it has already given is a loop, and a loop
// against somebody else's server is not a thing to discover by watching a graph.
func TestARepositoryThatNeverEnds(t *testing.T) {
	s := serve(t, func(q url.Values) string {
		if q.Get("verb") != "ListRecords" {
			return standard(q)
		}
		return envelope("  <ListRecords>\n" + recordXML("oai:x:1", "2021-01-01T00:00:00Z", "Mot") + "\n    <resumptionToken>mai-mai</resumptionToken>\n  </ListRecords>")
	})

	_, err := mam.Records(context.Background(), s.Client, mam.Repository{Base: s.Server.URL}, mam.Harvest{})
	if err == nil {
		t.Fatal("a repository returning one token forever was harvested to completion")
	}
	if !strings.Contains(err.Error(), "same resumption token twice") {
		t.Errorf("the error does not say what happened: %v", err)
	}
}

func TestAHarvestCanBeCutShort(t *testing.T) {
	s := serve(t, func(q url.Values) string {
		if q.Get("verb") != "ListRecords" {
			return standard(q)
		}
		return envelope("  <ListRecords>\n" +
			recordXML("oai:x:1", "2021-01-01T00:00:00Z", "Mot") + "\n" +
			recordXML("oai:x:2", "2021-01-02T00:00:00Z", "Hai") + "\n" +
			recordXML("oai:x:3", "2021-01-03T00:00:00Z", "Ba") + "\n" +
			"    <resumptionToken>trang-2</resumptionToken>\n  </ListRecords>")
	})

	items, err := mam.Records(context.Background(), s.Client, mam.Repository{Base: s.Server.URL}, mam.Harvest{Max: 2})
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("got %d records, want 2", len(items))
	}
}

// The reachability question P03-6 asks is not the same as the harvest question.
func TestWhatCountsAsWorking(t *testing.T) {
	working := serve(t, standard)
	if _, err := mam.Works(context.Background(), working.Client, working.Server.URL); err != nil {
		t.Errorf("a working repository was reported as broken: %v", err)
	}

	// A site that answers with a page rather than a protocol is a repository
	// nobody can harvest, however healthy it looks in a browser.
	page := serve(t, func(url.Values) string { return "<html><body>Thư viện số</body></html>" })
	if _, err := mam.Works(context.Background(), page.Client, page.Server.URL); err == nil {
		t.Error("a web page was accepted as an OAI-PMH endpoint")
	}
}

func TestARepositoryThatIsDownIsNotARepositoryThatSaidNo(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "maintenance", http.StatusServiceUnavailable)
	}))
	defer s.Close()

	_, err := mam.Identify(context.Background(), s.Client(), s.URL)
	if err == nil {
		t.Fatal("a 503 was read as a repository")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("the error does not say what happened: %v", err)
	}
	var f *mam.Fault
	if errors.As(err, &f) {
		t.Errorf("a transport failure was reported as a protocol answer: %v", err)
	}
}

// A base URL with a query string on it already is a shape real installs use, and
// appending another question mark produces a request nothing answers.
func TestABaseURLThatAlreadyHasAQuery(t *testing.T) {
	var path string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.String()
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(standard(r.URL.Query())))
	}))
	defer s.Close()

	if _, err := mam.Identify(context.Background(), s.Client(), s.URL+"/index.php?option=oai"); err != nil {
		t.Fatalf("identify: %v", err)
	}
	if strings.Count(path, "?") != 1 {
		t.Errorf("the request came out as %q", path)
	}
	if !strings.Contains(path, "option=oai") {
		t.Errorf("the base URL lost its own query: %q", path)
	}
}

// dc:language on a DSpace install is whatever the deposit form defaulted to,
// which is often en_US on a thesis written in Vietnamese. It is a hint that
// travels with the record and not a verdict, and sang decides.
func TestTheLanguageFieldIsKeptAndNotBelieved(t *testing.T) {
	s := serve(t, func(q url.Values) string {
		if q.Get("verb") != "ListRecords" {
			return standard(q)
		}
		rec := strings.Replace(recordXML("oai:x:1", "2021-01-01T00:00:00Z", "Nghiên cứu về cây lúa"),
			"<dc:language>vi</dc:language>", "<dc:language>en_US</dc:language>", 1)
		return envelope("  <ListRecords>\n" + rec + "\n  </ListRecords>")
	})

	items, err := mam.Records(context.Background(), s.Client, mam.Repository{Base: s.Server.URL}, mam.Harvest{})
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if items[0].Language != "en_US" {
		t.Errorf("the declared language was rewritten to %q", items[0].Language)
	}
	if len(items[0].Links) == 0 {
		t.Error("a record was dropped for the language its deposit form defaulted to")
	}
}
