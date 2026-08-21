package law

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fleet"
)

// The posture and the license class have to agree on what publishable means, or
// the release step and the record disagree about the same document.
func TestTextShipsExactlyForThePublishableClasses(t *testing.T) {
	for _, c := range doc.LicenseClasses() {
		p := Publishes(c)
		if p.Text != c.Publishable() {
			t.Errorf("%s ships text=%v and reads as publishable=%v", c, p.Text, c.Publishable())
		}
		if p.Note == "" {
			t.Errorf("%s has no note saying what ships", c)
		}
	}
}

// The crawled class is most of the corpus and the whole reason the posture is
// written down, so what it ships is pinned here rather than left to the table.
//
// The pairing is the point. A page ships its text and it ships the address it
// was fetched from, and neither half is optional: text without the address is a
// corpus nobody can check or ask for a removal from, and the address without
// the text is the artifact this project spent a year deciding it did not have
// to settle for.
func TestCrawledPagesShipTheirTextAndTheirAddress(t *testing.T) {
	p := Publishes(doc.LicenseCrawled)
	if !p.Text {
		t.Error("a crawled page ships no text, which is the whole corpus withheld")
	}
	if !p.Metadata {
		t.Error("a crawled page ships text with no address on it, which is not attributable and not takedownable")
	}
	if !p.Counted {
		t.Error("crawled pages are not counted, so the headline hides most of the corpus")
	}
	if !doc.LicenseCrawled.RequiresAttribution() {
		t.Error("a crawled page carries no attribution obligation, so nothing forces the address to be there")
	}
}

// A page that reserved itself is fetched by the same crawler down the same pipe
// and must not come out the same end. This is the check that the crawled class
// did not swallow the reservation.
func TestAReservationOutranksTheCrawledClass(t *testing.T) {
	var found bool
	for _, d := range For(doc.SourceCrawl) {
		if !strings.Contains(d.Subject, "reservation") {
			continue
		}
		found = true
		if d.Class != doc.LicenseUnredistributable {
			t.Errorf("a page carrying a reservation reads as %s", d.Class)
		}
		if d.Class.Publishable() {
			t.Error("a page carrying a reservation is publishable")
		}
	}
	if !found {
		t.Error("there is no row for a crawled page that reserved its rights")
	}
}

// The restricted class no longer holds the crawl, and what is left in it is the
// material with a real term attached: theses under institutional terms and
// transcripts that inherit a recording's status. The metadata ships and the text
// does not, which is what lets somebody else rebuild it from their own lawful
// access.
func TestRestrictedShipsMetadataAndNotText(t *testing.T) {
	p := Publishes(doc.LicenseRestricted)
	if p.Text {
		t.Error("restricted documents ship their text")
	}
	if !p.Metadata {
		t.Error("restricted documents ship no metadata, which makes the class pointless")
	}
	if !p.Counted {
		t.Error("restricted documents are not counted, so the headline hides them")
	}
}

// A withheld document is still a document, and the release notes say so. The
// alternative is a corpus that reports only the flattering number.
func TestWithheldDocumentsAreStillCounted(t *testing.T) {
	p := Publishes(doc.LicenseUnredistributable)
	if p.Text || p.Metadata {
		t.Error("unredistributable material ships something")
	}
	if !p.Counted {
		t.Error("unredistributable material vanishes from the counts rather than being reported as withheld")
	}
}

// Unknown is not a class of document, it is a failure to determine, and nothing
// about it ships. The ingest contract already rejects it and this is the second
// place that has to agree.
func TestUnknownShipsNothingAtAll(t *testing.T) {
	p := Publishes(doc.LicenseUnknown)
	if p.Text || p.Metadata || p.Counted {
		t.Errorf("an undetermined document ships something: %+v", p)
	}

	if doc.LicenseUnknown.Publishable() {
		t.Error("an undetermined document reads as publishable")
	}
	if !strings.Contains(p.Note, "contract") {
		t.Errorf("the note does not say the contract already rejects it: %q", p.Note)
	}
}

func TestAnUndefinedClassShipsNothing(t *testing.T) {
	p := Publishes(doc.LicenseClass(200))
	if p.Text || p.Metadata || p.Counted {
		t.Errorf("a class that does not exist ships something: %+v", p)
	}
}

// Every class defined in doc needs a row here, or the release step meets a
// document it has no rule for and has to invent one.
func TestEveryLicenseClassHasAPosture(t *testing.T) {
	have := make(map[doc.LicenseClass]bool)
	for _, p := range Publications() {
		if have[p.Class] {
			t.Errorf("%s has two rows", p.Class)
		}
		have[p.Class] = true
	}
	for _, c := range doc.LicenseClasses() {
		if !have[c] {
			t.Errorf("%s has no publication rule", c)
		}
	}
	if len(have) != len(doc.LicenseClasses()) {
		t.Errorf("the posture has %d rows and there are %d classes", len(have), len(doc.LicenseClasses()))
	}
}

// Every class a determination assigns has to be one the posture knows how to
// publish. This is the join between the two tables and it is the thing that
// would silently break if a class were added to one of them alone.
func TestEveryDeterminedClassCanBePublished(t *testing.T) {
	for _, d := range Determinations() {
		p := Publishes(d.Class)
		if p.Class != d.Class {
			t.Errorf("%s is %s and there is no publication rule for that", d.Subject, d.Class)
		}
	}
}

// The projected split is a prediction, written before the measurement, and this
// is the test that keeps it honest arithmetic rather than a nicer number.
func TestTheProjectedSplitIsBelowTheCorpusAndAboveNothing(t *testing.T) {
	if ProjectedPublishableTokens >= ProjectedTotalTokens {
		t.Errorf("the publishable projection of %d is not below the corpus at %d, which would mean the license work found nothing",
			ProjectedPublishableTokens, ProjectedTotalTokens)
	}
	if ProjectedTotalTokens != fleet.TargetTokens {
		t.Errorf("the legal projection is against %d tokens and the plan targets %d",
			ProjectedTotalTokens, fleet.TargetTokens)
	}
	// The band was 60 to 80 percent while the crawl was classed restricted and
	// the whole of it was being withheld. It is 85 to 95 now for the same
	// reason it was 60 to 80 then: it is the range the release plan assumes,
	// and a projection outside it means the plan and the license table have
	// stopped agreeing about what this corpus is. The upper bound is the one
	// doing the work. Nothing here should ever reach 100 percent, because the
	// theses, the transcripts and the pages that reserved themselves are real
	// and a projection that forgot them would be the mistake this test is for.
	share := float64(ProjectedPublishableTokens) / float64(ProjectedTotalTokens)
	if share < 0.85 || share > 0.95 {
		t.Errorf("the projection publishes %.0f%% of the corpus, which is outside what the release plan assumes", share*100)
	}
}

// The fallback is the answer to the only risk on the register that can end the
// project, so it has to be complete enough to act on rather than a note that says
// we will think of something.
func TestTheFallbackIsSomethingYouCouldActOn(t *testing.T) {
	f := RecipeOnly
	if _, ok := Ask(f.Question); !ok {
		t.Errorf("the fallback triggers on %q, which is not on the agenda", f.Question)
	}
	if f.If == "" || f.Then == "" || f.Withholds == "" {
		t.Errorf("the fallback does not say what triggers it, what happens, or what is withheld: %+v", f)
	}
	if len(f.Publishes) < 4 {
		t.Errorf("the fallback publishes %d things, and the whole point is that most of the work still ships", len(f.Publishes))
	}
	var pipeline bool
	for _, s := range f.Publishes {
		if strings.Contains(s, "pipeline") {
			pipeline = true
		}
	}
	if !pipeline {
		t.Error("the fallback does not publish the pipeline, which is what makes it a recipe rather than a list")
	}
	if !strings.Contains(f.Then, "rather than ending") {
		t.Errorf("the fallback does not say the project continues: %q", f.Then)
	}
}

func TestPublicationsHandsOutACopy(t *testing.T) {
	got := Publications()
	got[0].Text = false
	if !Publications()[0].Text {
		t.Error("editing the returned slice edited the posture")
	}
}
