package frontier

// Which box in the fleet owns a host.

import (
	"encoding/binary"

	"github.com/tamnd/gao/doc"
)

// Box says which box of a fleet of n owns the given host.
//
// The fleet splits on the host and on nothing else, which is what lets three
// boxes crawl at once with no coordinator between them: a box that stops takes
// its own hosts down with it and nobody else's, and two boxes never fetch the
// same page because they never see the same host. The politeness follows from
// the same rule, since a crawl delay is per host and a host has one box.
//
// The hash is the document hash of the host name, which is the hash everything
// else in this repository identifies things with, and the box is its first
// eight bytes modulo the fleet. It is here rather than beside the crawler's
// queue because a seed list has to answer the same question before the frontier
// ever sees a URL, and two spellings of one rule is a fleet where a third of
// the seed goes to the box that will refuse it.
//
// n of one is a fleet of one and the answer is always zero.
func Box(host string, n int) int {
	if n <= 1 {
		return 0
	}
	h := doc.SumString(host)
	return int(binary.BigEndian.Uint64(h[:8]) % uint64(n))
}
