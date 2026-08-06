package gat

// Who the crawler says it is.
//
// A crawler that does not identify itself cannot be blocked, and a crawler that
// cannot be blocked is not being polite by accident. Everything here is one
// string, published in one place, and the same on every request. There is no
// rotation, no fallback, and no second agent for the hosts that said no, and the
// tests below are as much of an enforcement of that as a package can have.

import (
	"fmt"
	"strings"
)

// Bot is the product token: what a site writes in a robots.txt rule to address
// this crawler, and what gets passed to [Robots.Check].
//
// It is not the User-Agent header and passing the header here instead is the
// mistake this pair of names exists to prevent. A site that writes
//
//	User-agent: gaobot
//	Disallow: /
//
// has addressed us, and a crawler that matched that line against its full header
// would find no match and crawl the site anyway, having read the file that told
// it not to. RFC 9309 says the token and means the token.
const Bot = "gaobot"

// Contact is where a webmaster goes. It is in the User-Agent header of every
// request, so it is the one URL a site owner is guaranteed to have, and it has
// to keep working for longer than the crawl does.
const Contact = "https://github.com/tamnd/gao/blob/main/LIEN-HE.md"

// Agent is the User-Agent header, built from the version the binary was built
// with.
//
// The shape is the one webmasters and log analyzers already parse: a product
// token, a version, and a URL in parentheses after a plus sign. Anything more
// elaborate gets truncated by somebody's log pipeline and the contact URL is the
// part that must survive.
func Agent(version string) string {
	return fmt.Sprintf("%s/%s (+%s)", Bot, strings.TrimSpace(version), Contact)
}
