// Package frontier is the crawl frontier: which URLs are worth asking for, in what
// order, and which ones are the same URL written twice.
//
// Biên is a border, and a frontier is where a crawl stops knowing what is on the
// other side. Everything in this package is about that edge: turning a link into
// the one spelling a crawl agrees to call it, recognizing when a thousand links
// are one page wearing different numbers, and deciding what a host has earned.
//
// The frontier is the part of a crawler that decides how big the crawl is. A
// fetcher that is polite and correct will still spend a year on one calendar if
// nothing upstream of it notices that every page it returns is empty.
package frontier

// Turning a link into the one spelling a crawl agrees to call it.
//
// Two URLs that are the same page have to canonicalize to the same string, or
// they are fetched twice and stored twice and the second copy is a duplicate
// nobody can find. Two URLs that are different pages must not, or one of them is
// never fetched at all. The whole file is that trade, one rule at a time, and
// the rules lean in different directions depending on which way the mistake
// costs more.

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/net/idna"
)

// Canon is the canonical form of a URL: the string two spellings of one page
// have to agree on.
//
// It is a string rather than a URL because that is what it is for. The frontier
// holds hundreds of millions of these in a seen set, compares them for equality
// and never takes them apart again, so the type that comes back is the type that
// gets stored.
func Canon(rawurl string) (string, error) {
	u, err := Parse(rawurl)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// Parse canonicalizes a URL and returns it whole, for callers that want the host
// or the path out of it without parsing the string again.
func Parse(rawurl string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawurl))
	if err != nil {
		return nil, fmt.Errorf("frontier: %s: %w", rawurl, err)
	}

	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("frontier: %s: %q is not a scheme a crawl follows", rawurl, u.Scheme)
	}

	host, err := canonHost(u)
	if err != nil {
		return nil, fmt.Errorf("frontier: %s: %w", rawurl, err)
	}
	u.Host = host

	// A fragment is the browser's business. The server never sees it and two
	// URLs differing only after the hash are one request.
	u.Fragment, u.RawFragment = "", ""

	// A user and a password in a URL is either a credential nobody meant to
	// publish or a phishing link dressed as a domain. Neither is something a
	// crawl carries around, and dropping it here means it cannot reach a log.
	u.User = nil

	u.RawPath = ""
	u.Path = canonPath(u.Path)
	u.RawQuery = canonQuery(u.RawQuery)
	return u, nil
}

// canonHost lowercases the host, drops the port when it is the default for the
// scheme, and puts an internationalized domain into the form that goes on the
// wire.
//
// The last of those matters more here than it would in most crawlers. Vietnam
// has had internationalized `.vn` domains since 2011, and a link written with
// the domain in Vietnamese letters and the same link written in punycode are one
// host that a byte comparison calls two.
func canonHost(u *url.URL) (string, error) {
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("no host")
	}

	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		// A host that IDNA refuses is a host with something in it that does not
		// belong in a domain name. It is not canonicalized quietly, because a
		// crawl that guesses here is a crawl making requests to somewhere it
		// cannot name.
		return "", fmt.Errorf("%q is not a host name: %w", host, err)
	}

	port := u.Port()
	if port == "" || (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		return ascii, nil
	}
	return ascii + ":" + port, nil
}

// canonPath resolves the path the way a server does before it looks at it.
//
// Go's url.Parse already decodes percent escapes into Path and re-encodes them
// on the way out, which handles the case of one page written with an escape and
// without it. What is left is the structure: an empty path is the root, a doubled
// slash is one slash, and a dot segment is arithmetic that every server does and
// no two links agree to have done for them.
func canonPath(p string) string {
	if p == "" {
		return "/"
	}

	// trailing tracks whether the path ends at a directory, which is decided by
	// the last segment rather than by the last byte. `/a/b/..` addresses `/a/`
	// and not `/a`, the same way it does in a shell, and a server resolving the
	// link would say so.
	trailing := false
	var out []string
	for seg := range strings.SplitSeq(p, "/") {
		switch seg {
		case "", ".":
			// A doubled slash, a trailing slash and a dot segment all address
			// the directory they are already in.
			trailing = true
		case "..":
			// Above the root there is nothing. A link that climbs past it is a
			// relative path resolved against the wrong base, and a server would
			// serve the root rather than an error.
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			trailing = true
		default:
			out = append(out, seg)
			trailing = false
		}
	}

	if len(out) == 0 {
		return "/"
	}
	joined := "/" + strings.Join(out, "/")
	if trailing {
		joined += "/"
	}
	return joined
}

// Tracking is the set of query parameters that identify the person or the
// campaign rather than the page.
//
// Every one of them is a parameter a page serves identical content with and
// without, so leaving them in turns one article into as many URLs as there are
// places it was shared. `fbclid` alone can produce thousands of spellings of one
// news story, and Vietnamese news travels on Facebook and Zalo.
//
// The list is closed and it is short on purpose. A prefix rule that dropped
// anything starting with `u` would eventually drop a parameter that selects a
// page, and a URL missing the parameter that selects the page is a URL that
// fetches the wrong thing quietly.
var Tracking = map[string]bool{
	"utm_source":     true,
	"utm_medium":     true,
	"utm_campaign":   true,
	"utm_term":       true,
	"utm_content":    true,
	"utm_id":         true,
	"utm_reader":     true,
	"utm_referrer":   true,
	"utm_social":     true,
	"utm_brand":      true,
	"fbclid":         true,
	"gclid":          true,
	"dclid":          true,
	"gbraid":         true,
	"wbraid":         true,
	"msclkid":        true,
	"twclid":         true,
	"ttclid":         true,
	"igshid":         true,
	"mc_cid":         true,
	"mc_eid":         true,
	"yclid":          true,
	"_ga":            true,
	"_gl":            true,
	"ref_src":        true,
	"ref_url":        true,
	"zarsrc":         true,
	"phpsessid":      true,
	"jsessionid":     true,
	"sid":            true,
	"sessionid":      true,
	"cfid":           true,
	"cftoken":        true,
	"aspxautodetect": true,
}

// canonQuery drops the parameters that are not about the page and puts the rest
// in an order two links can agree on.
//
// Sorting is the one rule here that can be wrong. The specification says the
// query is opaque and a server may read it however it likes, so a server that
// treats `?a=1&b=2` and `?b=2&a=1` as different pages is within its rights. In
// practice every framework parses the query into a map before anything sees it,
// and a server that depended on order would already be broken by every proxy and
// every share button. Against that, not sorting means one page arrives in the
// frontier once per ordering, and at 700M fetches the duplicates are the larger
// and the more certain cost.
//
// Values are kept as they were written. A parameter whose value is a date or a
// page number is what the shape in shape.go is for, and throwing the value away
// here would throw away the page.
func canonQuery(raw string) string {
	if raw == "" {
		return ""
	}

	// Parsed by hand rather than with url.ParseQuery, which drops a parameter
	// whose value will not decode and silently returns the rest. A link with one
	// bad escape in it is still a link, and the parameter that failed to decode
	// may be the one that selects the page.
	var keep []string
	for pair := range strings.SplitSeq(raw, "&") {
		if pair == "" {
			continue
		}
		name, _, _ := strings.Cut(pair, "=")
		if Tracking[strings.ToLower(name)] {
			continue
		}
		keep = append(keep, pair)
	}
	if len(keep) == 0 {
		return ""
	}
	sort.Strings(keep)
	return strings.Join(keep, "&")
}

// Same reports whether two URLs are the same page.
//
// A URL that will not canonicalize is not the same as anything, including
// itself, because the question this answers is whether a crawl may skip the
// second one, and the answer for something it cannot read is no.
func Same(a, b string) bool {
	ca, err := Canon(a)
	if err != nil {
		return false
	}
	cb, err := Canon(b)
	if err != nil {
		return false
	}
	return ca == cb
}
