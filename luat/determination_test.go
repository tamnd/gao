package luat

import (
	"slices"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// The one test that stops a source being ingested with no determination behind
// it. A path with no row here is a path whose documents would carry an unknown
// license class, and the ingest contract rejects those, so this failing means an
// acquisition path is about to reject everything it fetches.
func TestEveryAcquisitionPathHasADetermination(t *testing.T) {
	for _, s := range doc.Sources() {
		if len(For(s)) == 0 {
			t.Errorf("%s has no license determination, so nothing it fetches can be admitted", s)
		}
	}
}

func TestEveryDeterminationIsUsable(t *testing.T) {
	for _, d := range Determinations() {
		if d.Subject == "" {
			t.Error("a determination has no subject")
			continue
		}
		if !d.Class.Valid() {
			t.Errorf("%s has class %d, which is not a class", d.Subject, uint8(d.Class))
		}
		if d.Class == doc.LicenseUnknown {
			t.Errorf("%s determines nothing, which is not a determination", d.Subject)
		}
		if d.Evidence == "" {
			t.Errorf("%s records a class with no evidence, so the class is a guess", d.Subject)
		}
		for _, s := range d.Sources {
			if !s.Valid() {
				t.Errorf("%s names %q, which is not an acquisition path", d.Subject, s)
			}
		}
	}
}

// Every question a determination hangs on has to exist, or the row is pointing
// at an agenda item that was renamed or dropped.
func TestEveryQuestionADeterminationNamesIsOnTheAgenda(t *testing.T) {
	for _, d := range Determinations() {
		if d.Question == "" {
			continue
		}
		if _, ok := Ask(d.Question); !ok {
			t.Errorf("%s hangs on %s, which is not on the agenda", d.Subject, d.Question)
		}
	}
}

// A reservation is the one signal that has to be honored no matter what else the
// row says, so it is asserted by name rather than left to the loop above.
func TestAReservationIsNotRedistributable(t *testing.T) {
	var found bool
	for _, d := range Determinations() {
		if !strings.Contains(d.Subject, "reservation") {
			continue
		}
		found = true
		if d.Class != doc.LicenseUnredistributable {
			t.Errorf("reserved material reads as %s", d.Class)
		}
		if d.Class.Publishable() {
			t.Error("reserved material reads as publishable")
		}
		if d.Question != "Q2" {
			t.Errorf("reserved material does not hang on Q2, it hangs on %q", d.Question)
		}
	}
	if !found {
		t.Error("there is no row for material carrying a reservation")
	}
}

// The quiet win, and the reason luatdo exists. Vietnamese statutory and
// administrative material sits outside copyright by statute, which makes a
// complete Vietnamese legal corpus fully publishable with nothing attached to it.
// If this row ever stops being open, a downstream project loses its input.
func TestTheLegalCorpusIsOpenAndPublishable(t *testing.T) {
	var found bool
	for _, d := range For(doc.SourceCrawl) {
		if !strings.HasPrefix(d.Subject, "Vietnamese statutes") {
			continue
		}
		found = true
		if d.Class != doc.LicenseOpen {
			t.Errorf("Vietnamese statutory material reads as %s", d.Class)
		}
		if !d.Class.Publishable() {
			t.Error("Vietnamese statutory material is not publishable")
		}
		if d.Class.RequiresAttribution() {
			t.Error("Vietnamese statutory material carries an attribution obligation")
		}
	}
	if !found {
		t.Error("there is no row for Vietnamese statutory material")
	}
}

// Wikipedia is the row that could contaminate a release, and the containment is
// that it hangs on Q7 and is kept in its own shard rather than blended.
func TestWikipediaIsMarkedAsTheShareAlikeProblem(t *testing.T) {
	var found bool
	for _, d := range Determinations() {
		if !strings.Contains(d.Subject, "Wikipedia") {
			continue
		}
		found = true
		if d.Question != "Q7" {
			t.Errorf("Wikipedia hangs on %q rather than Q7", d.Question)
		}
		q, ok := Ask("Q7")
		if !ok {
			t.Fatal("Q7 is not on the agenda")
		}
		if !strings.Contains(q.Position(), "shard") {
			t.Errorf("the Q7 position does not say how the term is contained: %q", q.Position())
		}
	}
	if !found {
		t.Error("there is no row for Vietnamese Wikipedia")
	}
}

// The crawl arrives through one pipe and carries several licenses, which is the
// reason For returns a slice rather than a single row.
func TestOneAcquisitionPathCarriesSeveralLicenses(t *testing.T) {
	got := For(doc.SourceCrawl)
	if len(got) < 2 {
		t.Fatalf("the crawl has %d determinations, and it fetches statutes and forum threads through the same pipe", len(got))
	}
	classes := make(map[doc.LicenseClass]bool)
	for _, d := range got {
		classes[d.Class] = true
	}
	if len(classes) < 2 {
		t.Error("every crawled page reads as the same license class")
	}
	if !classes[doc.LicenseOpen] {
		t.Error("nothing the crawl fetches is open, which loses the legal corpus")
	}
}

func TestForRejectsAPathThatDoesNotExist(t *testing.T) {
	if got := For(doc.Source("nope")); len(got) != 0 {
		t.Errorf("an undefined source matched %d determinations", len(got))
	}
}

// Unresolved is the list to reread when an answer lands, so it has to track the
// answers rather than being a hand-maintained second list.
func TestUnresolvedFollowsTheAnswers(t *testing.T) {
	before := Unresolved()
	if len(before) == 0 {
		t.Fatal("nothing is waiting on counsel, which cannot be right while every question is open")
	}
	for _, d := range before {
		q, ok := Ask(d.Question)
		if !ok {
			t.Errorf("%s waits on %s, which is not on the agenda", d.Subject, d.Question)
			continue
		}
		if q.Answered() {
			t.Errorf("%s waits on %s, which is answered", d.Subject, d.Question)
		}
	}

	// Answering one question has to shorten the list, and shorten it by exactly
	// the rows that named it.
	answer(t, "Q2", "the reservation prohibits redistribution only")
	after := Unresolved()
	for _, d := range after {
		if d.Question == "Q2" {
			t.Errorf("%s still waits on Q2 after Q2 was answered", d.Subject)
		}
	}
	want := 0
	for _, d := range before {
		if d.Question != "Q2" {
			want++
		}
	}
	if len(after) != want {
		t.Errorf("answering Q2 left %d rows waiting, want %d", len(after), want)
	}
}

// answer sets a counsel answer for the duration of one test and restores the
// agenda afterwards. It writes to the package variable rather than to a copy,
// because the behavior under test is what the package reports once an answer
// lands.
func answer(t *testing.T, id, text string) {
	t.Helper()
	i := slices.IndexFunc(questions, func(q Question) bool { return q.ID == id })
	if i < 0 {
		t.Fatalf("%s is not on the agenda", id)
	}
	was := questions[i].Answer
	questions[i].Answer = text
	t.Cleanup(func() { questions[i].Answer = was })
}

func TestDeterminationsHandsOutACopy(t *testing.T) {
	got := Determinations()
	got[0].Class = doc.LicenseUnredistributable
	if Determinations()[0].Class == doc.LicenseUnredistributable {
		t.Error("editing the returned slice edited the table")
	}
}
