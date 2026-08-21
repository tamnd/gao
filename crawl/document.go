package crawl

// From a fetch to a document.
//
// This is the gate. Everything upstream of it is a page that was on the web and
// everything downstream of it is a row in a corpus, and the difference is the
// checks in this file: the page had content, the content is Vietnamese, the site
// did not reserve its rights, and the record carries every column the ingest
// contract requires. A page that fails one of them is written down as a
// rejection with what it measured, not dropped, because a threshold that turns
// out to be wrong is only recoverable if the documents it removed can be found.

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/frontier"
	"github.com/tamnd/gao/harvest"
	"github.com/tamnd/gao/law"
	"github.com/tamnd/gao/normalize"
	"github.com/tamnd/gao/reject"
	"github.com/tamnd/gao/sift"
	"github.com/tamnd/gao/store"
)

// The stage names that go on a rejection, so that a query over the reject store
// can ask which part of the crawl turned a page down.
const (
	StageFetch    = "crawl.fetch"
	StageExtract  = "crawl.extract"
	StageReserve  = "crawl.reserve"
	StageSift     = "crawl.sift"
	StageContract = "crawl.contract"
)

// A Verdict is what one fetched page turned into.
//
// Kept and Doc go together and so do Reason and Detail, but a rejected page
// still carries a document, filled in as far as it got, because that is what
// makes a rejection something anybody can act on. A row that says "language"
// and nothing else is a row that cannot be argued with.
type Verdict struct {
	Doc      *doc.Document
	Measured sift.Result

	Kept   bool
	Stage  string
	Reason reject.Reason
	Detail string
}

// BuildOptions are what a build needs that the page does not carry.
type BuildOptions struct {
	// Locator points back into the WARC: file, offset and length. It is required,
	// because a document that cannot be traced back to the bytes it was
	// extracted from fails the contract, and rightly.
	Locator string

	// FetchedAt is when the request was made. Zero means now, in UTC.
	FetchedAt time.Time

	// Limits are the sift thresholds. The zero value takes [sift.Default].
	Limits sift.Limits
}

// Build turns a fetched page into a document, or says why it is not one.
func Build(v *harvest.Visit, p *Page, o BuildOptions) Verdict {
	limits := o.Limits
	if limits.MinSyllables == 0 {
		limits = sift.Default()
	}
	at := o.FetchedAt
	if at.IsZero() {
		at = time.Now()
	}

	d := &doc.Document{SchemaVersion: doc.SchemaVersion}
	d.RawID = doc.Sum(v.Body)
	d.Source = doc.SourceCrawl
	d.SourceLocator = o.Locator
	d.URL = v.URL
	d.Host = hostname(v.URL)
	d.URLTemplate = template(v.URL)
	d.FetchedAt = at.UTC()
	d.MediaType = mediaType(v.Header.Get("Content-Type"))
	d.Extractor = Extractor
	d.PipelineVersion = PipelineVersion
	d.HTTPStatus = uint16(v.Status)
	d.RobotsDecision = v.Robots.Why
	d.RobotsRule = v.Robots.Rule

	// The response headers and the page itself are merged before anything is
	// concluded from either. A site that reserves in a header and says nothing
	// in the HTML has reserved, and so has one that does it the other way round.
	reserve := v.Reserve
	if p != nil {
		reserve = reserve.Merge(p.Reserve)
	}
	d.TDMSignals = reserve.Signals()
	d.Consent = reserve.Consent()
	d.LicenseClass, d.LicenseEvidence = licenseFor(reserve)

	// The status is checked after the provenance is filled and before anything
	// is read out of the body, because a rejection for a 404 is worth having and
	// a rejection with no URL on it is not. A body came back with most of these
	// and it is the site's error page.
	if v.Status != http.StatusOK {
		return refuse(d, StageFetch, reject.ReasonFetch,
			fmt.Sprintf("the server answered %d", v.Status))
	}
	if p == nil || p.Text == "" {
		return refuse(d, StageExtract, reject.ReasonBoilerplate,
			"nothing on the page reads as content, which is what a listing or a gallery looks like")
	}

	// Normalization comes before anything measures the text, because a page in a
	// legacy font encoding is not Vietnamese to any test that reads characters
	// and is Vietnamese to a reader who has the font.
	n := normalize.Normalize(p.Text)
	d.Text = n.Text
	d.NChars = doc.Chars(d.Text)
	d.NSyllables = doc.Syllables(d.Text)
	d.DocID = doc.SumString(d.Text)

	m := sift.Measure(d.Text)
	d.Lang = store.LangValue
	d.LangScore = float32(m.Language.Rate())
	d.Diacritics = m.Diacritic()
	d.Heuristics = m.Heuristics()

	// The reservation is honored after the measurements are taken and before
	// anything is kept. Taking the measurements costs nothing and a rejection
	// that carries them can be counted, which is how the crawl knows what
	// honoring reservations costs it.
	if reason, detail, bad := reserve.Reject(); bad {
		out := refuse(d, StageReserve, reason, detail)
		out.Measured = m
		return out
	}
	if reason, detail, bad := limits.Reject(m); bad {
		out := refuse(d, StageSift, reason, detail)
		out.Measured = m
		return out
	}

	// The two markdown renderings are normalized last, after the page is known
	// to be one this crawl keeps.
	//
	// They used to run above, beside the text, and three quarters of every
	// crawl's work on them was thrown away: the crawl keeps about one page in
	// four, and a rejection is written out with its stage, its reason and its
	// measurements and none of its writing. The published rejects carry no text
	// column, no markdown column and no body column, so what these two calls
	// produced for a rejected page was serialized to a local segment and dropped
	// again at export.
	//
	// They are not cheap either. Over 500 real pages off a live crawl the two
	// together are 13.9ms of a page's 46.3ms, and the body one alone is 9.4ms
	// because it is the whole document rather than the part of it that reads as
	// content.
	//
	// Nothing above this line reads either field, which is what makes the move
	// safe: the language decision, the thresholds and the reservation all work
	// on the text.
	d.Markdown = normalize.Markup(p.Markdown)
	d.Body = normalize.Markup(p.Body)

	if err := d.Admit(); err != nil {
		out := refuse(d, StageContract, reject.ReasonContract, err.Error())
		out.Measured = m
		return out
	}
	return Verdict{Doc: d, Measured: m, Kept: true}
}

func refuse(d *doc.Document, stage string, reason reject.Reason, detail string) Verdict {
	return Verdict{Doc: d, Stage: stage, Reason: reason, Detail: detail}
}

// Refused is the rejection of a URL that never became a fetch: robots.txt said
// no, the host has blocked us, the connection failed.
//
// It is written down for the same reason a failed filter is. A host that is
// absent from the corpus is a question somebody asks eventually, and the answer
// is either in a row that says which rule declined it or it is nowhere.
func Refused(rawurl string, at time.Time, reason reject.Reason, detail string) Verdict {
	if at.IsZero() {
		at = time.Now()
	}
	d := &doc.Document{SchemaVersion: doc.SchemaVersion}
	// There are no bytes to hash, so the identity is the address. It is what
	// the row is about, and a row with no identity at all cannot be counted
	// twice or once.
	d.RawID = doc.SumString(rawurl)
	d.Source = doc.SourceCrawl
	d.URL = rawurl
	d.Host = hostname(rawurl)
	d.URLTemplate = template(rawurl)
	d.FetchedAt = at.UTC()
	d.Extractor = Extractor
	d.PipelineVersion = PipelineVersion
	d.LicenseClass, d.LicenseEvidence = licenseFor(harvest.Reservation{})
	return Verdict{Doc: d, Stage: StageFetch, Reason: reason, Detail: detail}
}

// licenseFor is the determination that applies to a crawled page, read out of
// the table in law rather than written here, so that the string in a hundred
// million rows is the same string the luat command prints.
//
// There are two rows and the reservation picks between them. A page that
// reserved its rights is not redistributable, and a page that said nothing is
// restricted: gao may process it and may not pass it on.
func licenseFor(r harvest.Reservation) (doc.LicenseClass, string) {
	want := "Crawled news, forums, and blogs"
	if r.Reserved() {
		want = "Crawled pages carrying a reservation"
	}
	for _, det := range law.For(doc.SourceCrawl) {
		if det.Subject == want {
			return det.Class, det.Evidence
		}
	}
	// Unreachable while the table holds those two rows, and a test pins them. A
	// class of unknown fails the contract, which is the right way for a missing
	// determination to show up: as documents that will not be admitted rather
	// than as documents admitted under a license nobody made.
	return doc.LicenseUnknown, ""
}

// hostname is the host column, which is what takedowns, budgets and sorting all
// operate on.
func hostname(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// template is the URL with its variable parts replaced, which is what the budget
// counts against and what makes a calendar trap one URL instead of ten thousand.
func template(raw string) string {
	canonical, err := frontier.Canon(raw)
	if err != nil {
		return ""
	}
	s, err := frontier.Of(canonical)
	if err != nil {
		return ""
	}
	return s.String()
}

// mediaType is the type without its parameters. The charset is not dropped
// because it does not matter, it is dropped because by this point the bytes have
// been decoded and the column is answering what kind of thing was fetched.
func mediaType(header string) string {
	if header == "" {
		// A server that sent HTML without saying so is common enough that
		// refusing the document over it would be refusing a real part of the
		// Vietnamese web. What was fetched is what the crawler asked for.
		return "text/html"
	}
	t, _, err := mime.ParseMediaType(header)
	if err != nil {
		if i := strings.IndexByte(header, ';'); i > 0 {
			return strings.ToLower(strings.TrimSpace(header[:i]))
		}
		return strings.ToLower(strings.TrimSpace(header))
	}
	return t
}
