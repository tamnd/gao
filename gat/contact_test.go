package gat_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/gat"
)

// LIEN-HE.md is the page every request points a webmaster at, and what it is
// for is telling somebody how to stop us. A page that says one thing while the
// parser does another is worse than no page, because the person who followed it
// believes they are covered.
//
// So the robots.txt examples on that page are run through our own parser here.
// They are not illustrations, they are the instructions, and they are tested as
// instructions.

const contactPage = "../LIEN-HE.md"

func page(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(contactPage)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTheBlockTheContactPageTellsPeopleToWriteStopsUs(t *testing.T) {
	examples := robotsExamples(t, page(t))
	if len(examples) < 3 {
		t.Fatalf("the page carries %d robots.txt examples, and it used to carry three", len(examples))
	}

	// The first one is the one that matters most, because it is the one
	// somebody copies at two in the morning after finding us in a log.
	stop := gat.ReadRobots([]byte(examples[0]))
	for _, path := range []string{"/", "/tin-tuc/", "/dien-dan/chu-de-123", "/robots.txt"} {
		if stop.Allows(gat.Bot, path) {
			t.Errorf("the page says this block stops us and %s is still allowed:\n%s", path, examples[0])
		}
	}

	// The second is a partial block, which has to stop what it names and
	// nothing else. A page that overstated its own effect would be telling a
	// site it had closed a door it had not.
	some := gat.ReadRobots([]byte(examples[1]))
	for _, path := range []string{"/thanh-vien/nguyen-van-a", "/tim-kiem?q=abc"} {
		if some.Allows(gat.Bot, path) {
			t.Errorf("the partial block on the page does not cover %s:\n%s", path, examples[1])
		}
	}
	if !some.Allows(gat.Bot, "/tin-tuc/bai-viet-1") {
		t.Errorf("the partial block on the page stops more than it names:\n%s", examples[1])
	}

	// The third is the delay, in the units the page gives.
	slow := gat.ReadRobots([]byte(examples[2]))
	if got := slow.Delay(gat.Bot); got != 30*time.Second {
		t.Errorf("the page says Crawl-delay: 30 gets 30 seconds and the parser gives %v", got)
	}
}

// robotsExamples pulls the fenced blocks out of the page, in the order they are
// written, and keeps the ones that are robots.txt.
func robotsExamples(t *testing.T, md string) []string {
	t.Helper()
	var out []string
	parts := strings.Split(md, "```")
	for i := 1; i < len(parts); i += 2 {
		block := strings.TrimSpace(parts[i])
		if strings.Contains(strings.ToLower(block), "user-agent:") {
			out = append(out, block+"\n")
		}
	}
	return out
}

// The header the page tells people to look for has to be the header we send.
// The version is what moves, so the page shows one and the check is on the
// shape around it.
func TestTheContactPageDescribesTheAgentWeActuallySend(t *testing.T) {
	md := page(t)

	if !strings.Contains(md, gat.Contact) {
		t.Errorf("the page does not carry %q, which is the URL every request points at", gat.Contact)
	}
	if !strings.Contains(md, gat.Bot+"/") {
		t.Errorf("the page does not show the %q header a site owner is looking for in a log", gat.Bot)
	}
	// A page in the wrong language for the people who read it is a page
	// nobody reads. Two words of Vietnamese that cannot be typed by accident.
	for _, want := range []string{"thu thập", "robots.txt", "Crawl-delay"} {
		if !strings.Contains(md, want) {
			t.Errorf("the page does not mention %q", want)
		}
	}
}

// The takedown address is the other thing on the page that has to work, and the
// part of it a test can check is that it is one address, it is ours, and it
// arrives pre-labeled so a request cannot sit unnoticed in a general queue.
func TestTheTakedownAddressIsOneReachablePlace(t *testing.T) {
	md := page(t)

	const takedown = "https://github.com/tamnd/gao/issues/new?labels=takedown"
	if !strings.Contains(md, takedown) {
		t.Errorf("the page does not give %q as the takedown address", takedown)
	}
	if strings.Count(md, "issues/new") != strings.Count(md, takedown) {
		t.Error("the page gives more than one takedown address, and one of them will be the wrong one")
	}
	// The promise is 72 hours and it is measured, so the number has to be on
	// the page in both languages rather than only in the one somebody wrote
	// first.
	if strings.Count(md, "72") < 2 {
		t.Error("the page states the response time in one language and not the other")
	}
}
