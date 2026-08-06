package mam

// The other route into hosts nobody handed us a list of, and the only one where
// the site tells us what it holds instead of us guessing.
//
// A university repository is a DSpace or an Eprints install with theses, journal
// issues and conference papers in it, most of it long form Vietnamese prose
// written by people who were paid to write carefully. It is the highest quality
// text per byte anywhere in this project. It is also close to invisible to a
// crawler: the landing pages are behind a search form, the identifiers are
// handles rather than paths, and a link graph walk reaches a fraction of it.
//
// OAI-PMH is the way in, and it has been since 2002. A repository that speaks it
// will hand over a catalog of everything it holds, in order, with dates, in a
// format that has not changed in twenty years. The point of harvesting the
// catalog rather than crawling the site is that the catalog is complete and the
// crawl is not.
//
// Most of the care below is about not reporting a working repository as broken.
// P03-6 is a prediction about what fraction of Vietnamese university
// repositories expose working OAI-PMH, and every protocol detail handled wrongly
// pushes that number down while looking like a finding.

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Repository is what a repository says about itself.
type Repository struct {
	Base        string
	Name        string
	Protocol    string
	Earliest    time.Time
	Granularity string   // as declared: 2006-01-02 or 2006-01-02T15:04:05Z
	Deleted     string   // no, persistent, or transient
	Admin       []string // where to write when something is wrong
	Formats     []string // the metadata prefixes it offers
}

// Item is one record out of a repository's catalog.
//
// Links is the part the crawl uses. Everything else is here because a harvest
// that keeps only URLs has to go back to the network to answer any question
// about what it harvested.
type Item struct {
	ID       string
	Stamp    time.Time
	Deleted  bool
	Sets     []string
	Title    string
	Language string
	Rights   string
	Type     string
	Links    []string
}

// ErrNoRecords is what a repository says when nothing matched the date range.
//
// It is a protocol level error code and it is not a failure. A repository with
// nothing published since last Tuesday answers a request for records since last
// Tuesday with noRecordsMatch, and a harvester that reads every error code the
// same way marks that repository broken. That single confusion would be enough
// to move P03-6 by more than the effect it is trying to measure.
var ErrNoRecords = errors.New("mam: the repository has no records in that range")

// Fault is an OAI-PMH error the repository reported, as against an error moving
// bytes. The distinction matters: a fault means the protocol worked.
type Fault struct {
	Code    string
	Message string
}

func (f *Fault) Error() string {
	if f.Message == "" {
		return "mam: the repository answered " + f.Code
	}
	return "mam: the repository answered " + f.Code + ": " + f.Message
}

// Stamp formats a time the way this repository will accept it.
//
// A repository declaring day granularity answers badArgument to a from with a
// time of day in it, so the caller cannot use one format everywhere. When the
// declared granularity is missing or unreadable this uses the day form, which is
// the direction that works: a day stamp is accepted by a repository that supports
// seconds, and a second stamp is refused by one that does not.
func (r Repository) Stamp(t time.Time) string {
	if strings.Contains(r.Granularity, "T") {
		return t.UTC().Format("2006-01-02T15:04:05Z")
	}
	return t.UTC().Format(time.DateOnly)
}

// Offers says whether the repository advertised a metadata prefix.
func (r Repository) Offers(prefix string) bool {
	return slices.Contains(r.Formats, prefix)
}

// DublinCore is the prefix to ask for.
//
// Every OAI-PMH repository is required to offer it, so it is the only one that
// can be asked for without first asking what is on offer. Richer formats carry
// more, and a harvest that preferred one and gave up when it was missing would
// report a working repository as broken.
const DublinCore = "oai_dc"

// Identify asks a repository who it is.
//
// This is also the reachability test. A base URL that answers Identify with a
// well formed response is a repository, whatever else it turns out not to
// support, and that is the question P03-6 is asking.
func Identify(ctx context.Context, c *http.Client, base string) (Repository, error) {
	r := Repository{Base: base}
	body, err := ask(ctx, c, base, url.Values{"verb": {"Identify"}})
	if err != nil {
		return r, err
	}
	var resp response
	if err := xml.Unmarshal(body, &resp); err != nil {
		return r, fmt.Errorf("mam: %s does not answer OAI-PMH: %w", base, err)
	}
	if err := resp.fault(); err != nil {
		return r, err
	}
	if resp.Identify == nil {
		return r, fmt.Errorf("mam: %s answered without an Identify", base)
	}

	id := resp.Identify
	r.Name = strings.TrimSpace(id.Name)
	r.Protocol = strings.TrimSpace(id.Protocol)
	r.Granularity = strings.TrimSpace(id.Granularity)
	r.Deleted = strings.TrimSpace(id.Deleted)
	r.Earliest = parseStamp(id.Earliest)
	for _, a := range id.Admin {
		if a = strings.TrimSpace(a); a != "" {
			r.Admin = append(r.Admin, a)
		}
	}

	// The formats are a second request, and a repository that refuses it is
	// still a repository. Dublin Core is mandatory, so assuming it when the
	// question goes unanswered is the reading that matches the protocol.
	if formats, err := Formats(ctx, c, base); err == nil {
		r.Formats = formats
	} else {
		r.Formats = []string{DublinCore}
	}
	return r, nil
}

// Formats asks what metadata prefixes a repository offers.
func Formats(ctx context.Context, c *http.Client, base string) ([]string, error) {
	body, err := ask(ctx, c, base, url.Values{"verb": {"ListMetadataFormats"}})
	if err != nil {
		return nil, err
	}
	var resp response
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("mam: %s: %w", base, err)
	}
	if err := resp.fault(); err != nil {
		return nil, err
	}
	var out []string
	if resp.ListFormats != nil {
		for _, f := range resp.ListFormats.Formats {
			if p := strings.TrimSpace(f.Prefix); p != "" {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// Harvest reads a repository's catalog.
type Harvest struct {
	Prefix string    // the metadata prefix, DublinCore when empty
	Set    string    // one set, or every record when empty
	From   time.Time // records changed since, formatted to the declared granularity
	Until  time.Time
	Max    int // stop after this many records, or every record when zero
}

// Records reads r's catalog into memory, following resumption tokens.
//
// Two things about resumption are the whole reason this is not fifteen lines.
//
// The token has to be sent on its own. The protocol says a request carrying a
// resumptionToken carries nothing else, so a harvester that helpfully keeps
// sending metadataPrefix alongside it gets badArgument on page two and reports a
// repository with fifty thousand theses as one with a hundred.
//
// An empty token element ends the list. A response carrying <resumptionToken/>
// with nothing in it is saying this was the last page, which is a different
// statement from carrying no element at all, and a harvester that reads the empty
// element as a token asks for it forever.
func Records(ctx context.Context, c *http.Client, r Repository, h Harvest) ([]Item, error) {
	prefix := h.Prefix
	if prefix == "" {
		prefix = DublinCore
	}
	q := url.Values{"verb": {"ListRecords"}, "metadataPrefix": {prefix}}
	if h.Set != "" {
		q.Set("set", h.Set)
	}
	if !h.From.IsZero() {
		q.Set("from", r.Stamp(h.From))
	}
	if !h.Until.IsZero() {
		q.Set("until", r.Stamp(h.Until))
	}

	var out []Item
	seen := make(map[string]bool)
	for {
		body, err := ask(ctx, c, r.Base, q)
		if err != nil {
			return out, err
		}
		var resp response
		if err := xml.Unmarshal(body, &resp); err != nil {
			return out, fmt.Errorf("mam: %s: %w", r.Base, err)
		}
		if err := resp.fault(); err != nil {
			// Nothing in the range is an answer, and on the first page it is
			// the whole answer.
			if f := new(Fault); errors.As(err, &f) && f.Code == "noRecordsMatch" {
				if len(out) > 0 {
					return out, nil
				}
				return nil, ErrNoRecords
			}
			return out, err
		}
		if resp.ListRecords == nil {
			return out, fmt.Errorf("mam: %s answered without a ListRecords", r.Base)
		}

		for _, rec := range resp.ListRecords.Records {
			out = append(out, item(rec))
			if h.Max > 0 && len(out) >= h.Max {
				return out, nil
			}
		}

		token := strings.TrimSpace(resp.ListRecords.Token.Value)
		if token == "" {
			return out, nil
		}
		// A repository handing back a token it has already given is a loop, and
		// a loop against somebody else's server is not something to discover by
		// watching a graph.
		if seen[token] {
			return out, fmt.Errorf("mam: %s returned the same resumption token twice after %d records", r.Base, len(out))
		}
		seen[token] = true
		q = url.Values{"verb": {"ListRecords"}, "resumptionToken": {token}}
	}
}

// Works is the measurement P03-6 is about.
//
// A repository works when it says who it is, offers Dublin Core, and answers a
// request for records with either records or the statement that it has none in
// that range. Anything short of that is a repository somebody has to open by
// hand, and the reason to be careful about the difference is that every protocol
// detail handled wrongly here would show up as a Vietnamese university that does
// not publish a catalog, which is a claim about them rather than about us.
func Works(ctx context.Context, c *http.Client, base string) (Repository, error) {
	r, err := Identify(ctx, c, base)
	if err != nil {
		return r, err
	}
	if !r.Offers(DublinCore) {
		return r, fmt.Errorf("mam: %s does not offer %s, which every repository is required to", base, DublinCore)
	}
	if _, err := Records(ctx, c, r, Harvest{Max: 1}); err != nil && !errors.Is(err, ErrNoRecords) {
		return r, err
	}
	return r, nil
}

func item(rec record) Item {
	it := Item{
		ID:      strings.TrimSpace(rec.Header.Identifier),
		Stamp:   parseStamp(rec.Header.Datestamp),
		Deleted: strings.EqualFold(rec.Header.Status, "deleted"),
		Sets:    rec.Header.Sets,
	}
	// A deleted record carries a header and no metadata at all. Reading the
	// metadata regardless is how a harvest ends up with empty documents that
	// have titles and no text.
	if it.Deleted || rec.Metadata == nil {
		return it
	}

	dc := rec.Metadata.DC
	it.Title = first(dc.Title)
	it.Language = first(dc.Language)
	it.Rights = first(dc.Rights)
	it.Type = first(dc.Type)

	// dc:identifier is repeatable and is mostly not a URL. Repositories put
	// ISSNs, DOIs, call numbers and citation strings in it, and the handle URL
	// sits among them. Taking the first one would take a citation about as often
	// as a link.
	for _, id := range append(append([]string{}, dc.Identifier...), dc.Relation...) {
		if link := strings.TrimSpace(id); isHTTP(link) {
			it.Links = append(it.Links, link)
		}
	}
	return it
}

func isHTTP(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func first(v []string) string {
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

func parseStamp(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", time.DateOnly} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func ask(ctx context.Context, c *http.Client, base string, q url.Values) ([]byte, error) {
	if c == nil {
		c = http.DefaultClient
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+sep+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("mam: %w", err)
	}
	req.Header.Set("Accept", "text/xml, application/xml")

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mam: asking %s: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mam: %s answered %s", base, resp.Status)
	}
	// Repositories with a lot of records return large pages, and a repository
	// serving something that is not XML at all can serve a great deal of it.
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// The wire format. Element names are matched without their namespace except
// inside the Dublin Core payload, where the dc prefix is what tells the metadata
// apart from the envelope around it.
type response struct {
	XMLName     xml.Name     `xml:"OAI-PMH"`
	Errors      []faultXML   `xml:"error"`
	Identify    *identifyXML `xml:"Identify"`
	ListFormats *formatsXML  `xml:"ListMetadataFormats"`
	ListRecords *recordsXML  `xml:"ListRecords"`
}

func (r *response) fault() error {
	for _, e := range r.Errors {
		return &Fault{Code: strings.TrimSpace(e.Code), Message: strings.TrimSpace(e.Message)}
	}
	return nil
}

type faultXML struct {
	Code    string `xml:"code,attr"`
	Message string `xml:",chardata"`
}

type identifyXML struct {
	Name        string   `xml:"repositoryName"`
	Protocol    string   `xml:"protocolVersion"`
	Earliest    string   `xml:"earliestDatestamp"`
	Deleted     string   `xml:"deletedRecord"`
	Granularity string   `xml:"granularity"`
	Admin       []string `xml:"adminEmail"`
}

type formatsXML struct {
	Formats []struct {
		Prefix string `xml:"metadataPrefix"`
	} `xml:"metadataFormat"`
}

type recordsXML struct {
	Records []record `xml:"record"`
	Token   struct {
		Value string `xml:",chardata"`
	} `xml:"resumptionToken"`
}

type record struct {
	Header struct {
		Status     string   `xml:"status,attr"`
		Identifier string   `xml:"identifier"`
		Datestamp  string   `xml:"datestamp"`
		Sets       []string `xml:"setSpec"`
	} `xml:"header"`
	Metadata *struct {
		DC struct {
			Title      []string `xml:"http://purl.org/dc/elements/1.1/ title"`
			Identifier []string `xml:"http://purl.org/dc/elements/1.1/ identifier"`
			Relation   []string `xml:"http://purl.org/dc/elements/1.1/ relation"`
			Language   []string `xml:"http://purl.org/dc/elements/1.1/ language"`
			Rights     []string `xml:"http://purl.org/dc/elements/1.1/ rights"`
			Type       []string `xml:"http://purl.org/dc/elements/1.1/ type"`
		} `xml:"dc"`
	} `xml:"metadata"`
}
