package gat_test

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/gat"
)

// The mistake this pair of names exists to prevent, written as a test because a
// comment does not fail a build. A site addresses a crawler by its product
// token, and a crawler that matched robots.txt against its full header would
// read the file that told it to stay out and then not stay out.
func TestARuleAddressedToUsIsMatchedByTheTokenAndNotByTheHeader(t *testing.T) {
	r := gat.ReadRobots([]byte("User-agent: " + gat.Bot + "\nDisallow: /\n"))

	if r.Allows(gat.Bot, "/tin-tuc/") {
		t.Error("a block addressed to us by name did not apply to us")
	}
	if !r.Allows(gat.Agent("1.0"), "/tin-tuc/") {
		t.Error("the full header matched a rule written for the token, which means the two are being confused somewhere")
	}
}

// Case is the site's to choose. GAOBOT, Gaobot and gaobot are the same crawler,
// and a site that shouted at us has still addressed us.
func TestTheTokenIsMatchedWhateverCaseTheSiteWroteItIn(t *testing.T) {
	for _, written := range []string{"gaobot", "GAOBOT", "GaoBot"} {
		r := gat.ReadRobots([]byte("User-agent: " + written + "\nDisallow: /\n"))
		if r.Allows(gat.Bot, "/") {
			t.Errorf("a block addressed to %q did not apply to us", written)
		}
	}
}

// The token is the thing a person types into a file by hand, at the end of a
// long day, having found us in a log. Anything in it that needs escaping or
// shifting is a token that gets typed wrong.
func TestTheTokenIsOneWordAPersonCanType(t *testing.T) {
	if gat.Bot != strings.ToLower(gat.Bot) {
		t.Errorf("the token is %q and a site owner will type it in lower case", gat.Bot)
	}
	if strings.ContainsAny(gat.Bot, " \t/:()") {
		t.Errorf("the token is %q, and a robots.txt parser splits on some of that", gat.Bot)
	}
}

// The contact URL is the only part of the header that has to survive being read
// by a person, so it is checked for being reachable rather than for being
// present. A header that carries a URL nobody can open is a header that says we
// did not want to be found.
func TestEveryRequestCarriesSomewhereToComplainTo(t *testing.T) {
	agent := gat.Agent("0.4.1")

	if !strings.HasPrefix(agent, gat.Bot+"/0.4.1 ") {
		t.Errorf("the header is %q, and a log analyzer reads the token and the version off the front", agent)
	}
	if !strings.Contains(agent, "(+"+gat.Contact+")") {
		t.Errorf("the header is %q and does not carry %q", agent, gat.Contact)
	}
	if !strings.HasPrefix(gat.Contact, "https://") {
		t.Errorf("the contact address is %q, and a webmaster pastes it into a browser", gat.Contact)
	}
}

// There is one agent. A crawler with two is a crawler with one it uses on the
// hosts that blocked the other, whatever the intention was when the second one
// was added, so the way to not have that is to have nowhere to put it.
func TestThereIsOneAgentAndItDoesNotVary(t *testing.T) {
	first, second := gat.Agent("1.2.3"), gat.Agent("1.2.3")
	if first != second {
		t.Errorf("two calls gave %q and %q", first, second)
	}
	// The version is the only thing that moves, and it moves when the binary
	// does rather than when the host does.
	if gat.Agent("1.2.3") == gat.Agent("1.2.4") {
		t.Error("the header does not carry the version, so an old build cannot be told from a new one in a log")
	}
}

// A build with no version stamped on it is the ordinary case for somebody
// running from source, and the header still has to be a header.
func TestAnUnstampedBuildStillSaysWhoItIs(t *testing.T) {
	agent := gat.Agent("dev")

	if !strings.HasPrefix(agent, gat.Bot+"/dev ") {
		t.Errorf("an unstamped build sends %q", agent)
	}
	if !strings.Contains(agent, gat.Contact) {
		t.Errorf("an unstamped build sends %q, with nowhere to complain to", agent)
	}
}
