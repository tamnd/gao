package harvest

// robots.txt.
//
// The file is thirty years old, it was never standardized until RFC 9309 in
// 2022, and what is on the web is what people wrote by hand in the meantime.
// The parser therefore has two jobs that pull against each other: follow the
// specification, and read the file the site actually published. Where those
// disagree the tie goes to the site, because the file is a statement of what a
// site wants and a parser that reads it strictly enough to find nothing in it
// has not honored anything.
//
// The tolerance runs one way only. A misspelled `Disallow` is read as a
// disallow, because a site that typed it meant to keep us out and reading past
// it would let us in on a technicality. A misspelled `Allow` is not read as an
// allow, because the same reasoning in that direction would have us inventing
// permission out of a typo. That asymmetry is deliberate and it is the rule to
// keep in mind when adding to this file.

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/gao/reject"
)

// MaxRobotsSize is how much of a robots.txt is read. It is the limit Google
// publishes, so it is the limit sites have been written against, and a file that
// runs past it has already stopped being a file anybody is maintaining by hand.
// What is read past the limit is not a partial line: the tail is dropped at the
// last newline, so a truncated `Disallow` cannot become a shorter path than the
// one the site wrote.
const MaxRobotsSize = 512 * 1024

// Robots is a parsed robots.txt.
//
// The zero value allows everything, which is the correct reading of a site that
// does not publish the file at all, and it is what [ReadRobots] returns for an
// empty one.
type Robots struct {
	groups   []robotsGroup
	sitemaps []string
}

// robotsGroup is one block of rules and the agents it is addressed to. Groups
// are kept apart rather than flattened at parse time because which of them
// applies is a question about the crawler doing the asking, and gao is not the
// only agent this package will ever be asked about.
type robotsGroup struct {
	agents []string
	rules  []robotsRule
	delay  time.Duration

	// delaySaid is the crawl delay in the spelling the file used, kept for the
	// same reason every other statement in this package is kept in the site's
	// own words.
	delaySaid string
}

type robotsRule struct {
	// pattern is the path with its non-ASCII bytes percent encoded, so that a
	// rule written in Vietnamese and a request path that has already been
	// encoded are the same string by the time they are compared.
	pattern string
	allow   bool

	// said is the line as the file wrote it, which is what goes in the
	// robots_rule column. A record that says a fetch was allowed by
	// `/tin-tuc/` is answerable years later. One that says it was allowed by
	// rule 4 is not.
	said string
}

// A Decision is what robots.txt said about one path.
//
// Why is the value the record carries: `allow` when a rule allowed the path,
// `allow_default` when no rule matched, and `disallow` when one kept us out.
// The third never reaches the store, because a disallowed page is not fetched
// and so there is no document to carry it, but it is the same value the crawl
// logs and counts, and having one vocabulary for both is worth more than saving
// a constant.
type Decision struct {
	Allowed bool
	Why     string
	Rule    string
}

// The values of [Decision.Why].
const (
	RobotsAllow        = "allow"
	RobotsAllowDefault = "allow_default"
	RobotsDisallow     = "disallow"
)

// ReadRobots parses a robots.txt.
//
// It never fails. There is no such thing as a robots.txt that does not parse,
// only lines that mean something and lines that do not, and a parser that
// returned an error here would be handing the caller a choice between ignoring
// the file and ignoring the site.
func ReadRobots(data []byte) *Robots {
	if len(data) > MaxRobotsSize {
		data = data[:MaxRobotsSize]
		if cut := bytes.LastIndexByte(data, '\n'); cut >= 0 {
			data = data[:cut]
		}
	}
	// A file edited on Windows starts with a byte order mark often enough that
	// dropping it is not a nicety. Left in, it makes the first line read as a
	// directive nobody has ever heard of, which is usually the User-agent line
	// and therefore the whole file.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	r := &Robots{}
	var cur *robotsGroup
	// A run of agent lines with no rules between them addresses one group. The
	// flag is what tells `User-agent: a` followed by `User-agent: b` from a new
	// group starting.
	naming := false

	for line := range strings.Lines(string(data)) {
		field, value, ok := robotsLine(line)
		if !ok {
			continue
		}
		switch field {
		case "user-agent":
			if value == "" {
				continue
			}
			if !naming || cur == nil {
				r.groups = append(r.groups, robotsGroup{})
				cur = &r.groups[len(r.groups)-1]
				naming = true
			}
			cur.agents = append(cur.agents, strings.ToLower(value))
		case "allow", "disallow":
			naming = false
			if cur == nil {
				// Rules before the first agent line are addressed to
				// nobody. They are a common mistake and the specification
				// is clear about them, so they are dropped rather than
				// guessed at.
				continue
			}
			if value == "" {
				// An empty Disallow is the oldest way of writing "you may
				// have all of it", and an empty Allow says nothing at all.
				// Neither is a rule, and keeping either as a zero length
				// pattern would make it match every path.
				continue
			}
			cur.rules = append(cur.rules, robotsRule{
				pattern: robotsPath(value),
				allow:   field == "allow",
				said:    strings.TrimSpace(line),
			})
		case "crawl-delay":
			naming = false
			if cur == nil {
				continue
			}
			if d, ok := robotsDelay(value); ok {
				cur.delay, cur.delaySaid = d, value
			}
		case "sitemap":
			// Sitemaps belong to the file rather than to a group, wherever
			// in it they were written.
			if value != "" {
				r.sitemaps = append(r.sitemaps, value)
			}
		}
	}
	return r
}

// RobotsUnavailable is the robots.txt to use when there is no robots.txt to
// read, which is a question about the status code and not about the site.
//
// A 4xx means the file is not there, and a site with no file has not asked for
// anything, so everything is allowed. Anything else means we could not tell:
// the server is broken, or overloaded, or it is a 429 telling us in as many
// words to go away. In all of those the answer is that nothing is allowed until
// the file can be read, because a crawler that treats "I cannot reach you" as
// "you did not object" is a crawler that hits hardest exactly when a site is
// least able to take it.
func RobotsUnavailable(status int) *Robots {
	if status >= 400 && status < 500 && status != 429 {
		return &Robots{}
	}
	return &Robots{groups: []robotsGroup{{
		agents: []string{"*"},
		rules:  []robotsRule{{pattern: "/", said: "robots.txt returned " + strconv.Itoa(status)}},
	}}}
}

// Check is what the file says about one path, with the rule that decided it.
//
// path is the path and query of the request, not the whole URL. Matching is on
// the path alone because that is what the file is written in terms of, and a
// rule that names a query string names it there too.
func (r *Robots) Check(agent, path string) Decision {
	g := r.group(agent)
	if g == nil {
		return Decision{Allowed: true, Why: RobotsAllowDefault}
	}
	if path == "" {
		path = "/"
	}
	path = robotsPath(path)

	// The most specific rule wins, measured by the length of the pattern, and an
	// Allow beats a Disallow of the same length. That tie break is what lets a
	// site disallow a directory and then release one file inside it, which is
	// the shape most of these files are actually written in.
	best := -1
	var found robotsRule
	for _, rule := range g.rules {
		if !robotsMatch(rule.pattern, path) {
			continue
		}
		n := len(rule.pattern)
		if n > best || (n == best && rule.allow && !found.allow) {
			best, found = n, rule
		}
	}
	if best < 0 {
		return Decision{Allowed: true, Why: RobotsAllowDefault}
	}
	if found.allow {
		return Decision{Allowed: true, Why: RobotsAllow, Rule: found.said}
	}
	return Decision{Allowed: false, Why: RobotsDisallow, Rule: found.said}
}

// Allows is Check without the paperwork, for the callers that only need the
// answer.
func (r *Robots) Allows(agent, path string) bool { return r.Check(agent, path).Allowed }

// Reject is what the decision means for the URL, in the form the reject store
// takes, and it is where reading turns into honoring.
//
// A disallowed URL is not fetched, so nothing about it is ever a document, and
// the only record it can leave is this one. Leaving it is the point. A crawl
// that skips a million URLs and writes down none of them cannot tell a host that
// closed its doors from a host that was never reached, and those are the two
// numbers a yield report is made of.
func (d Decision) Reject() (reject.Reason, string, bool) {
	if d.Allowed {
		return "", "", false
	}
	return reject.ReasonRobots, d.Why + ": " + d.Rule, true
}

// Delay is the crawl delay the file asked us for, or zero if it asked for none.
//
// It is returned as the site wrote it rather than clamped. A site that asks for
// a minute gets a minute from the fetcher's own policy, which is where a floor
// and a ceiling belong, because a parser that quietly rewrote a delay it thought
// was unreasonable would be answering a question nobody asked it.
func (r *Robots) Delay(agent string) time.Duration {
	if g := r.group(agent); g != nil {
		return g.delay
	}
	return 0
}

// DelaySaid is the crawl delay in the spelling the file used.
func (r *Robots) DelaySaid(agent string) string {
	if g := r.group(agent); g != nil {
		return g.delaySaid
	}
	return ""
}

// Sitemaps is every sitemap the file pointed at, in the order it listed them.
// They are seeds, and a site that publishes one is telling the crawler where
// its content is rather than making it guess from links.
func (r *Robots) Sitemaps() []string { return r.sitemaps }

// group is the block of rules addressed to this agent, merging every group that
// names it, or the wildcard groups if none does.
//
// A file may address the same agent twice, and the specification says the groups
// combine rather than the second replacing the first. Merging matters more than
// it sounds: a site that appends a block for one crawler at the bottom of the
// file, which is how these files grow, would otherwise have its original rules
// dropped by the addition.
func (r *Robots) group(agent string) *robotsGroup {
	agent = strings.ToLower(agent)
	var named, wild []robotsGroup
	for _, g := range r.groups {
		for _, a := range g.agents {
			if a == agent {
				named = append(named, g)
				break
			}
			if a == "*" {
				wild = append(wild, g)
				break
			}
		}
	}
	// A group addressed to us by name replaces the wildcard entirely, rather
	// than adding to it. That is the specification and it is also what a site
	// means: the block with our name on it is the one written about us.
	use := named
	if len(use) == 0 {
		use = wild
	}
	if len(use) == 0 {
		return nil
	}
	out := &robotsGroup{}
	for _, g := range use {
		out.rules = append(out.rules, g.rules...)
		// Two delays for one crawler combine the way two reservations do,
		// which is the restrictive way. A site that wrote ten seconds in one
		// block and one in another has said ten seconds somewhere, and
		// reading the shorter one is a way of not having read it.
		if out.delay == 0 || (g.delay != 0 && g.delay > out.delay) {
			out.delay, out.delaySaid = g.delay, g.delaySaid
		}
	}
	return out
}

// robotsLine splits one line into its field and value.
//
// Comments run to the end of the line from a `#` anywhere in it, which is worth
// stating because it means a URL with a fragment in it cannot be written in this
// file, and sites that try produce a rule for the part before the `#`.
//
// The field names take the misspellings that are common in the wild, in the one
// direction described at the top of this file: `dissallow` is a disallow,
// `alow` is nothing at all.
func robotsLine(line string) (field, value string, ok bool) {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	field, value, ok = strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	field = strings.ToLower(strings.TrimSpace(field))
	value = strings.TrimSpace(value)
	switch field {
	case "user-agent", "useragent", "user agent":
		return "user-agent", value, true
	case "disallow", "dissallow", "disalow", "disallows":
		return "disallow", value, true
	case "allow":
		return "allow", value, true
	case "crawl-delay", "crawldelay", "crawl delay":
		return "crawl-delay", value, true
	case "sitemap":
		return "sitemap", value, true
	}
	return "", "", false
}

// robotsPath puts a path into the one spelling both sides of a comparison can be
// written in.
//
// This is the piece that matters on Vietnamese sites and almost nowhere else.
// A site writes `Disallow: /tìm-kiếm` in the file, in UTF-8, because that is
// what it typed, and the crawler asks for `/t%C3%ACm-ki%E1%BA%BFm`, because that
// is what a URL is. They are the same path and a byte comparison says they are
// not, so every rule a Vietnamese site writes about its own search page silently
// stops applying. Encoding both sides the same way is the fix, and encoding
// rather than decoding is what keeps a literal `%2A` in a path from becoming the
// wildcard `*`.
func robotsPath(p string) string {
	if isASCII(p) {
		return upperHex(p)
	}
	var b strings.Builder
	b.Grow(len(p) + 8)
	for i := range len(p) {
		if c := p[i]; c < 0x80 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigit(p[i] >> 4))
		b.WriteByte(hexDigit(p[i] & 0xf))
	}
	return upperHex(b.String())
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func hexDigit(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'A' + n - 10
}

// upperHex uppercases the hex of existing percent escapes, so that `%c3` and
// `%C3` are one string. It leaves everything else alone, including a stray `%`
// that is not an escape, which some sites do write.
func upperHex(s string) string {
	i := strings.IndexByte(s, '%')
	if i < 0 {
		return s
	}
	b := []byte(s)
	for ; i+2 < len(b); i++ {
		if b[i] != '%' || !isHex(b[i+1]) || !isHex(b[i+2]) {
			continue
		}
		b[i+1], b[i+2] = toUpperHex(b[i+1]), toUpperHex(b[i+2])
		i += 2
	}
	return string(b)
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func toUpperHex(c byte) byte {
	if c >= 'a' && c <= 'f' {
		return c - 'a' + 'A'
	}
	return c
}

// robotsMatch is the pattern language: `*` stands for any run of characters and
// a trailing `$` anchors the end. Everything else is a prefix match, which is
// the part that surprises people. `Disallow: /tin` keeps us out of `/tin-tuc`
// as well as `/tin/`, and a site that meant the directory has to write the
// slash.
func robotsMatch(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = pattern[:len(pattern)-1]
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(path, parts[0]) {
		return false
	}
	rest := path[len(parts[0]):]
	for i, part := range parts[1:] {
		last := i == len(parts)-2
		if last && anchored {
			if !strings.HasSuffix(rest, part) {
				return false
			}
			// The suffix has to start at or after where we are, or a
			// pattern has matched text it already consumed.
			return len(rest) >= len(part)
		}
		j := strings.Index(rest, part)
		if j < 0 {
			return false
		}
		rest = rest[j+len(part):]
	}
	if anchored {
		return rest == ""
	}
	return true
}

// robotsDelay reads a crawl delay. Sites write it as an integer, and some write
// it as a fraction, so it is parsed as a float and rejected if it is negative or
// absurd rather than clamped, since a delay of minus one is a typo and not a
// request.
func robotsDelay(v string) (time.Duration, bool) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 || f > 86400 {
		return 0, false
	}
	return time.Duration(f * float64(time.Second)), true
}
