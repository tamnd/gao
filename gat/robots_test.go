package gat

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/vo"
)

// The file the tests are mostly about. It is close to what a Vietnamese news
// site publishes: a wildcard block written years ago, a block added later for
// one crawler, paths written in Vietnamese with the diacritics left in, a crawl
// delay, and a sitemap at the bottom.
const vietnameseRobots = "\xef\xbb\xbf" + `# robots.txt
User-agent: *
Disallow: /admin/
Disallow: /tìm-kiếm
Disallow: /*?utm_source=
Crawl-delay: 10

User-agent: gaobot
Disallow: /
Allow: /tin-tuc/
Crawl-delay: 5

Sitemap: https://example.vn/sitemap.xml
`

func TestTheBlockWrittenForUsIsTheOneThatApplies(t *testing.T) {
	r := ReadRobots([]byte(vietnameseRobots))

	// The site shut gaobot out of everything and then let it back into one
	// directory, which is how these files are written.
	if d := r.Check("gaobot", "/gioi-thieu"); d.Allowed {
		t.Errorf("/gioi-thieu is allowed, and the block for us disallows everything: %+v", d)
	}
	d := r.Check("gaobot", "/tin-tuc/bai-viet")
	if !d.Allowed {
		t.Errorf("/tin-tuc/bai-viet is disallowed, and the block for us allows it: %+v", d)
	}
	if d.Why != RobotsAllow {
		t.Errorf("Why is %q, want %q, since a rule allowed it rather than nothing matching", d.Why, RobotsAllow)
	}
	if !strings.Contains(d.Rule, "/tin-tuc/") {
		t.Errorf("the decision does not name the rule that made it: %q", d.Rule)
	}

	// The wildcard block is not read at all once one names us, so its /admin/
	// rule does not apply and our own disallow of everything is what keeps us
	// out.
	if d := r.Check("gaobot", "/admin/x"); d.Rule != "Disallow: /" {
		t.Errorf("/admin/x was decided by %q, want the rule from our own block", d.Rule)
	}

	// Everybody else gets the wildcard block.
	if !r.Allows("otherbot", "/gioi-thieu") {
		t.Error("/gioi-thieu is disallowed for otherbot, and the wildcard block does not mention it")
	}
	if r.Allows("otherbot", "/admin/x") {
		t.Error("/admin/x is allowed for otherbot, and the wildcard block disallows it")
	}
}

// The case this file exists for. A site writes its own paths in its own
// language, the crawler asks for them percent encoded, and a byte comparison
// says the rule does not apply to the page it was written about.
func TestAPathWrittenInVietnameseMatchesTheEncodedRequest(t *testing.T) {
	r := ReadRobots([]byte(vietnameseRobots))
	for _, path := range []string{
		"/tìm-kiếm",
		"/t%C3%ACm-ki%E1%BA%BFm",
		"/t%c3%acm-ki%e1%ba%bfm",
		"/tìm-kiếm?q=lúa",
	} {
		if r.Allows("otherbot", path) {
			t.Errorf("%s is allowed, and the site disallowed its own search page", path)
		}
	}
	if !r.Allows("otherbot", "/tim-kiem") {
		t.Error("/tim-kiem is disallowed, and it is a different path from /tìm-kiếm")
	}
}

// A rule is a prefix unless it says otherwise, which is the part of the format
// that surprises people who write it.
func TestARuleIsAPrefixAndNotADirectory(t *testing.T) {
	r := ReadRobots([]byte("User-agent: *\nDisallow: /tin\n"))
	for _, path := range []string{"/tin", "/tin/", "/tin-tuc/bai-viet", "/tintuc"} {
		if r.Allows("gaobot", path) {
			t.Errorf("%s is allowed, and /tin is a prefix rather than a directory", path)
		}
	}
	if !r.Allows("gaobot", "/bao/tin") {
		t.Error("/bao/tin is disallowed, and a rule matches from the start of the path")
	}
}

func TestTheWildcardAndTheEndAnchor(t *testing.T) {
	r := ReadRobots([]byte("User-agent: *\nDisallow: /*.pdf$\nDisallow: /*?utm_source=\n"))
	tests := []struct {
		path    string
		allowed bool
	}{
		{"/tai-lieu/bao-cao.pdf", false},
		{"/tai-lieu/bao-cao.pdf?tai-ve=1", true},
		{"/tai-lieu/pdf-la-gi", true},
		{"/tin-tuc?utm_source=facebook", false},
		// The rule the site wrote has a question mark in it, so it catches
		// the tracking parameter only where it comes first. That is what the
		// file says and it is not the parser's job to improve on it, but it
		// is worth having the case written down, since a site that believes
		// it has excluded its tracking URLs mostly has not.
		{"/tin-tuc?q=1&utm_source=facebook", true},
		{"/tin-tuc?q=1", true},
	}
	for _, tt := range tests {
		if got := r.Allows("gaobot", tt.path); got != tt.allowed {
			t.Errorf("Allows(%q) = %v, want %v", tt.path, got, tt.allowed)
		}
	}
}

// The longest rule decides, and an allow of the same length beats a disallow.
// Without that tie break a site cannot release anything out of a directory it
// has closed.
func TestTheMostSpecificRuleDecidesAndATieGoesToTheAllow(t *testing.T) {
	r := ReadRobots([]byte("User-agent: *\nDisallow: /tai-lieu/\nAllow: /tai-lieu/cong-khai/\n"))
	if r.Allows("gaobot", "/tai-lieu/noi-bo/x") {
		t.Error("/tai-lieu/noi-bo/x is allowed, and only /tai-lieu/cong-khai/ was released")
	}
	if !r.Allows("gaobot", "/tai-lieu/cong-khai/x") {
		t.Error("/tai-lieu/cong-khai/x is disallowed, and it is the longer rule")
	}

	tie := ReadRobots([]byte("User-agent: *\nDisallow: /tai-lieu/\nAllow: /tai-lieu/\n"))
	if !tie.Allows("gaobot", "/tai-lieu/y") {
		t.Error("/tai-lieu/y is disallowed, and an allow of the same length as the disallow wins")
	}
}

// Nothing in the file about this path is a different answer from a rule that
// allowed it, and the record keeps them apart.
func TestAPathNothingMatchesIsAllowedByDefault(t *testing.T) {
	r := ReadRobots([]byte("User-agent: *\nDisallow: /admin/\n"))
	d := r.Check("gaobot", "/tin-tuc/")
	if !d.Allowed || d.Why != RobotsAllowDefault {
		t.Fatalf("Check = %+v, want allowed by default", d)
	}
	if d.Rule != "" {
		t.Errorf("the default decision names a rule (%q), and no rule was involved", d.Rule)
	}
}

// Half of what is on the web is not what the specification describes, and a
// parser that reads only the specification reads half of these files as empty.
func TestTheFileIsReadAsItWasWrittenRatherThanAsItShouldHaveBeen(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{"a byte order mark from a Windows editor", "\xef\xbb\xbfUser-agent: *\nDisallow: /admin/\n"},
		{"carriage returns", "User-agent: *\r\nDisallow: /admin/\r\n"},
		{"shouting", "USER-AGENT: *\nDISALLOW: /admin/\n"},
		{"no space after the colon", "User-agent:*\nDisallow:/admin/\n"},
		{"trailing comments", "User-agent: * # everybody\nDisallow: /admin/ # khu vuc quan tri\n"},
		{"a misspelled disallow", "User-agent: *\nDissallow: /admin/\n"},
		{"a run-together user agent", "Useragent: *\nDisallow: /admin/\n"},
		{"blank lines everywhere", "\n\nUser-agent: *\n\n\nDisallow: /admin/\n\n"},
		{"no trailing newline", "User-agent: *\nDisallow: /admin/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ReadRobots([]byte(tt.file)).Allows("gaobot", "/admin/x") {
				t.Error("/admin/x is allowed, and the file disallowed it")
			}
		})
	}
}

// The tolerance runs one way. A typo that would keep us out is read, a typo that
// would let us in is not, because inventing permission out of a misspelling is
// how a crawler ends up somewhere it was not invited.
func TestAMisspelledAllowIsNotAnAllow(t *testing.T) {
	r := ReadRobots([]byte("User-agent: *\nDisallow: /tai-lieu/\nAlow: /tai-lieu/cong-khai/\n"))
	if r.Allows("gaobot", "/tai-lieu/cong-khai/x") {
		t.Error("/tai-lieu/cong-khai/x is allowed, and Alow is not a directive")
	}
}

func TestAnEmptyDisallowIsThePermissionItLooksLike(t *testing.T) {
	r := ReadRobots([]byte("User-agent: *\nDisallow:\n"))
	if !r.Allows("gaobot", "/bat-cu-dau") {
		t.Error("/bat-cu-dau is disallowed, and an empty Disallow allows everything")
	}
	if d := r.Check("gaobot", "/"); d.Why != RobotsAllowDefault {
		t.Errorf("Why is %q, and an empty Disallow leaves no rule to have decided anything", d.Why)
	}
}

func TestRulesBeforeTheFirstAgentLineAreAddressedToNobody(t *testing.T) {
	r := ReadRobots([]byte("Disallow: /admin/\nUser-agent: *\nDisallow: /rieng-tu/\n"))
	if !r.Allows("gaobot", "/admin/x") {
		t.Error("/admin/x is disallowed by a rule that names no agent")
	}
	if r.Allows("gaobot", "/rieng-tu/x") {
		t.Error("/rieng-tu/x is allowed, and the rule after the agent line disallows it")
	}
}

// A file grows by appending, and a site that adds a second block for the same
// crawler has added to what it said rather than replaced it.
func TestTwoBlocksForOneCrawlerCombine(t *testing.T) {
	r := ReadRobots([]byte("User-agent: gaobot\nDisallow: /admin/\n\nUser-agent: gaobot\nDisallow: /rieng-tu/\n"))
	for _, path := range []string{"/admin/x", "/rieng-tu/x"} {
		if r.Allows("gaobot", path) {
			t.Errorf("%s is allowed, and one of the two blocks for us disallows it", path)
		}
	}
}

// Consecutive agent lines address one block. A parser that starts a new block on
// each of them gives the second agent a block with no rules in it.
func TestASingleBlockCanBeAddressedToSeveralCrawlers(t *testing.T) {
	r := ReadRobots([]byte("User-agent: gaobot\nUser-agent: otherbot\nDisallow: /admin/\n"))
	for _, agent := range []string{"gaobot", "otherbot"} {
		if r.Allows(agent, "/admin/x") {
			t.Errorf("/admin/x is allowed for %s, and the block names it", agent)
		}
	}
}

func TestTheCrawlDelayIsReadAndTheLongerOneWins(t *testing.T) {
	r := ReadRobots([]byte(vietnameseRobots))
	if got := r.Delay("gaobot"); got != 5*time.Second {
		t.Errorf("Delay(gaobot) = %v, want 5s", got)
	}
	if got := r.Delay("otherbot"); got != 10*time.Second {
		t.Errorf("Delay(otherbot) = %v, want 10s", got)
	}
	if got := r.DelaySaid("otherbot"); got != "10" {
		t.Errorf("DelaySaid(otherbot) = %q, want the file's own spelling", got)
	}

	both := ReadRobots([]byte("User-agent: gaobot\nCrawl-delay: 1\n\nUser-agent: gaobot\nCrawl-delay: 30\n"))
	if got := both.Delay("gaobot"); got != 30*time.Second {
		t.Errorf("Delay = %v, want 30s, since two delays for one crawler combine the restrictive way", got)
	}
}

func TestACrawlDelayThatIsNotOneIsIgnored(t *testing.T) {
	for _, said := range []string{"-1", "0", "moi luc", "", "999999"} {
		r := ReadRobots([]byte("User-agent: *\nCrawl-delay: " + said + "\n"))
		if got := r.Delay("gaobot"); got != 0 {
			t.Errorf("Crawl-delay: %q was read as %v, want it ignored", said, got)
		}
	}
	if got := ReadRobots([]byte("User-agent: *\nCrawl-delay: 0.5\n")).Delay("gaobot"); got != 500*time.Millisecond {
		t.Errorf("Crawl-delay: 0.5 = %v, want 500ms, since some sites do write fractions", got)
	}
}

func TestSitemapsBelongToTheFileAndNotToABlock(t *testing.T) {
	r := ReadRobots([]byte(vietnameseRobots))
	want := []string{"https://example.vn/sitemap.xml"}
	if got := r.Sitemaps(); !slices.Equal(got, want) {
		t.Errorf("Sitemaps() = %v, want %v", got, want)
	}
}

// The four hundreds mean there is no file, which is a site that has asked for
// nothing. Everything else means we could not tell, and a crawler that reads
// "I cannot answer you" as "I have no objection" hits hardest exactly when a
// site is least able to take it.
func TestAFileWeCouldNotReadIsNotAFileThatSaidYes(t *testing.T) {
	tests := []struct {
		status  int
		allowed bool
	}{
		{404, true},
		{403, true},
		{410, true},
		{429, false},
		{500, false},
		{503, false},
		{0, false},
	}
	for _, tt := range tests {
		got := RobotsUnavailable(tt.status).Allows("gaobot", "/tin-tuc/")
		if got != tt.allowed {
			t.Errorf("status %d allows /tin-tuc/ = %v, want %v", tt.status, got, tt.allowed)
		}
	}
	d := RobotsUnavailable(503).Check("gaobot", "/tin-tuc/")
	if !strings.Contains(d.Rule, "503") {
		t.Errorf("the refusal does not say why we stopped: %q", d.Rule)
	}
}

func TestNoFileAtAllAllowsEverything(t *testing.T) {
	for _, r := range []*Robots{ReadRobots(nil), ReadRobots([]byte("")), ReadRobots([]byte("# nothing here\n")), {}} {
		if !r.Allows("gaobot", "/tin-tuc/") {
			t.Error("/tin-tuc/ is disallowed by a file that says nothing")
		}
	}
}

// A file past the cap is read up to the cap and no further, and the tail is
// dropped at a line boundary, so a truncated Disallow cannot turn into a
// shorter path than the one the site wrote.
func TestAnEnormousFileIsReadUpToTheCapAndCutAtALine(t *testing.T) {
	var b strings.Builder
	b.WriteString("User-agent: *\nDisallow: /admin/\n")
	for b.Len() < MaxRobotsSize {
		b.WriteString("Disallow: /muc/" + strings.Repeat("x", 64) + "\n")
	}
	b.WriteString("Disallow: /cuoi-cung/\n")
	r := ReadRobots([]byte(b.String()))

	if r.Allows("gaobot", "/admin/x") {
		t.Error("/admin/x is allowed, and it is inside the cap")
	}
	if !r.Allows("gaobot", "/cuoi-cung/x") {
		t.Error("/cuoi-cung/x is disallowed, and it is past the cap")
	}
	if !r.Allows("gaobot", "/muc/") {
		t.Error("/muc/ is disallowed, which is a truncated rule matching more than the site wrote")
	}
}

// A URL we did not fetch leaves one record and only one. Without it a crawl
// cannot tell a host that closed its doors from a host it never reached, and
// those are the two numbers a yield report is made of.
func TestASkippedURLIsWrittenDownRatherThanSkipped(t *testing.T) {
	r := ReadRobots([]byte("User-agent: *\nDisallow: /admin/\n"))

	reason, detail, rejected := r.Check("gaobot", "/admin/x").Reject()
	if !rejected {
		t.Fatal("a disallowed URL is not rejected, so nothing records that it was skipped")
	}
	if reason != vo.ReasonRobots {
		t.Errorf("reason is %q, want %q", reason, vo.ReasonRobots)
	}
	if !strings.Contains(detail, "Disallow: /admin/") {
		t.Errorf("the rejection does not carry the rule that caused it: %q", detail)
	}

	if _, _, rejected := r.Check("gaobot", "/tin-tuc/").Reject(); rejected {
		t.Error("an allowed URL is rejected")
	}
}

// The decision is one vocabulary, because the crawl counts disallows and the
// store records allows, and two vocabularies for one question is how the two
// numbers stop adding up.
func TestTheDecisionSaysWhichOfTheThreeItWas(t *testing.T) {
	r := ReadRobots([]byte("User-agent: *\nDisallow: /admin/\nAllow: /admin/cong-khai/\n"))
	tests := []struct {
		path string
		why  string
	}{
		{"/admin/x", RobotsDisallow},
		{"/admin/cong-khai/x", RobotsAllow},
		{"/tin-tuc/", RobotsAllowDefault},
	}
	for _, tt := range tests {
		if got := r.Check("gaobot", tt.path).Why; got != tt.why {
			t.Errorf("Check(%q).Why = %q, want %q", tt.path, got, tt.why)
		}
	}
}
