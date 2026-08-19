// Package seed finds hosts nobody handed us a list of.
//
// A crawl starts from a seed set, and the seed set is the one input to a crawl
// that cannot be crawled for. Every other decision the frontier makes is about
// URLs it has already been shown. This is about where the first ones come from.
//
// The problem is specific to Vietnam rather than general. There is no VNNIC zone
// file to work from, so there is no list of `.vn` domains to start from, and the
// lists that do circulate are search engine exports that carry the same bias the
// crawl is trying to correct: they contain the sites a search engine already
// found worth indexing. The two routes here are not indexes. Certificate
// Transparency names a host because somebody asked a certificate authority for a
// certificate for it, and OAI-PMH names a document because a repository publishes
// a catalog of what it holds. Neither has any opinion about whether the host is
// interesting, which is the whole point.
package seed

// Certificate Transparency, read as a host list.
//
// Every publicly trusted certificate issued since 2018 is logged, in public,
// because browsers stopped trusting the ones that were not. A certificate names
// the hosts it is valid for. So the logs are, incidentally, a list of hosts that
// existed at the moment somebody was willing to prove they controlled them. For
// a country with no zone file that is the closest thing to a zone file there is.
//
// It is a list of leads rather than a list of sites. A host in the logs may be
// gone, may be an internal service, may be a certificate somebody provisioned and
// never used. That is fine: the cost of a dead lead is one request that fails
// fast, and the cost of a missing host is a site that never enters the corpus.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Host is one hostname we found and the earliest date the source puts on it.
//
// Certs carries how many certificates named it, which is worth keeping because
// it separates a host somebody has renewed for eight years from a host that got
// one certificate in 2019 and was never seen again. Both go in the seed set. The
// difference decides which one is worth retrying after a failure.
//
// Direct is how many of those named it outright rather than through a wildcard,
// and it is the difference between evidence that a host exists and evidence that
// something exists below it. See [Clean] for why that distinction has to be kept
// rather than resolved at parse time.
type Host struct {
	Name   string    // punycode, lowercase, no trailing dot
	First  time.Time // the earliest notBefore among the certificates naming it
	Certs  int       // how many log entries named it
	Direct int       // how many of those named it outright rather than as *.name
}

// Entry is one row of a Certificate Transparency search.
//
// The field names are crt.sh's, which is the search front end in front of the
// logs rather than a log itself. Reading the logs directly means get-entries and
// parsing X.509 out of a Merkle tree, which is the right answer at full scale and
// a lot of machinery for a seed list. The parsing below takes an io.Reader, so a
// dump from any source that can produce these fields works without the network.
type Entry struct {
	NameValue  string `json:"name_value"`  // newline separated, the subject alternative names
	CommonName string `json:"common_name"` // legacy, usually a duplicate of one of the above
	NotBefore  string `json:"not_before"`  // 2006-01-02T15:04:05
	Issuer     string `json:"issuer_name"`
}

// Hosts reads a Certificate Transparency search result and returns the hosts
// under suffix that it names, deduplicated, sorted, one entry per host.
//
// Deduplication is not optional bookkeeping here. Every certificate is logged
// twice, once as a precertificate and once as the certificate itself, and a host
// under continuous renewal has a new pair every ninety days. A `.vn` search
// returns tens of millions of rows for something in the low millions of hosts,
// so a caller counting rows is off by more than an order of magnitude.
func Hosts(r io.Reader, suffix string) ([]Host, error) {
	var entries []Entry
	if err := json.NewDecoder(r).Decode(&entries); err != nil {
		return nil, fmt.Errorf("seed: reading certificate transparency results: %w", err)
	}
	return collect(entries, suffix), nil
}

func collect(entries []Entry, suffix string) []Host {
	suffix = strings.ToLower(strings.TrimPrefix(suffix, "."))
	found := make(map[string]*Host)
	for _, e := range entries {
		when := notBefore(e.NotBefore)
		for _, raw := range names(e) {
			name, ok := Clean(raw, suffix)
			if !ok {
				continue
			}
			h, seen := found[name]
			if !seen {
				h = &Host{Name: name, First: when}
				found[name] = h
			}
			h.Certs++
			if !strings.HasPrefix(strings.TrimSpace(raw), "*.") {
				h.Direct++
			}
			if !when.IsZero() && (h.First.IsZero() || when.Before(h.First)) {
				h.First = when
			}
		}
	}

	out := make([]Host, 0, len(found))
	for _, h := range found {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// names is every string in one entry that claims to be a host.
//
// The common name is included even though it is almost always a duplicate of a
// subject alternative name, because almost always is not always: certificates
// issued before the SAN extension became mandatory carry the host only there,
// and those are exactly the certificates on the oldest and least indexed hosts.
func names(e Entry) []string {
	out := strings.Split(e.NameValue, "\n")
	if e.CommonName != "" {
		out = append(out, e.CommonName)
	}
	return out
}

func notBefore(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05", time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// Clean turns one name out of a certificate into a host worth asking, or says
// that it is not one.
//
// The rules are short and every one of them is a thing that appears in the real
// logs and is not a website.
func Clean(raw, suffix string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = strings.TrimSuffix(name, ".")

	// A subject alternative name can be an email address, and a certificate
	// issued to a person carries one. It ends in a domain and it is not a host.
	if strings.ContainsAny(name, "@ \t,;/") {
		return "", false
	}

	// A wildcard is not a host. It is the statement that a host exists somewhere
	// below a name, and the name it is below is the useful part: *.vnexpress.vn
	// says vnexpress.vn is real. Dropping wildcards outright would lose that,
	// and keeping the star would put a URL in the frontier that resolves to
	// nothing.
	name = strings.TrimPrefix(name, "*.")

	// The suffix test is on a label boundary and not on the string, because
	// khachhang.vn.example.com is a shape that vendors really do use for staging
	// and it is not a Vietnamese host. A substring test would take it.
	if suffix != "" && !strings.HasSuffix(name, "."+suffix) {
		return "", false
	}
	if !valid(name) {
		return "", false
	}

	// Below a public suffix there is a registrable name, and at one there is
	// nothing to fetch. This matters here more than it would elsewhere: `.vn`
	// has com.vn, edu.vn, gov.vn and the province names under it, so a wildcard
	// certificate for *.com.vn, which registrars hold, would otherwise seed
	// com.vn as though it were a site. It is not one and never resolves.
	//
	// The list is not complete for `.vn`. It carries the generic second levels
	// and some of the provinces and not others, so ho-chi-minh.vn comes through
	// here as a registrable name. That is left alone rather than patched with a
	// hand written list of provinces, because a hand written list goes stale
	// silently and the cost of the gap is one request that fails. What covers it
	// instead is evidence: a name that only ever appeared as *.name has Direct
	// zero, which is what a registrar wildcard looks like and what a real site
	// does not.
	if ps, _ := publicsuffix.PublicSuffix(name); ps == name {
		return "", false
	}
	return name, true
}

// valid is a hostname check rather than a DNS lookup.
//
// The logs contain internal names, names with underscores that are DNS records
// rather than hosts, and names long enough that no resolver would answer for
// them. All of that is cheaper to drop here than to discover one request at a
// time later.
func valid(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 {
			return false
		}
		if l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := range len(l) {
			c := l[i]
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			default:
				return false
			}
		}
	}
	// A name whose last label is all digits is an address written out long, and
	// every certificate for a bare IP address arrives in this shape.
	last := labels[len(labels)-1]
	return strings.Trim(last, "0123456789") != ""
}

// Search asks a Certificate Transparency search front end for the hosts under
// one suffix.
//
// The query is by domain rather than by issuer or by date, because the question
// is which hosts exist and not which certificates were issued. It is one request
// returning a large body, which is why the parse above is separate: at the scale
// this runs at, the sensible thing is to pull a dump once and read it many times
// rather than to ask again every time somebody wants a different filter.
func Search(ctx context.Context, c *http.Client, base, suffix string) ([]Host, error) {
	if c == nil {
		c = http.DefaultClient
	}
	suffix = strings.ToLower(strings.TrimPrefix(suffix, "."))
	q := url.Values{"q": {"%." + suffix}, "output": {"json"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("seed: asking %s: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("seed: %s answered %s", base, resp.Status)
	}
	return Hosts(resp.Body, suffix)
}

// New is the hosts in found that are not already in seed.
//
// This is the measurement the prediction is about rather than a convenience.
// Certificate Transparency is worth the trouble only to the extent that it names
// hosts a seed list assembled from search engine exports does not, and the way
// to know that is to subtract one from the other rather than to count either.
func New(found []Host, seed []string) []Host {
	have := make(map[string]bool, len(seed))
	for _, s := range seed {
		if name, ok := Clean(s, ""); ok {
			have[name] = true
		}
	}
	out := make([]Host, 0, len(found))
	for _, h := range found {
		if !have[h.Name] {
			out = append(out, h)
		}
	}
	return out
}
