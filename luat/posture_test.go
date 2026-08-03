package luat

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/may"
)

// The posture and the license class have to agree on what publishable means, or
// the release step and the record disagree about the same document.
func TestTextShipsExactlyForThePublishableClasses(t *testing.T) {
	for _, c := range []doc.LicenseClass{
		doc.LicenseUnknown, doc.LicenseOpen, doc.LicensePermissiveAttribution,
		doc.LicenseRestricted, doc.LicenseUnredistributable,
	} {
		p := Publishes(c)
		if p.Text != c.Publishable() {
			t.Errorf("%s ships text=%v and reads as publishable=%v", c, p.Text, c.Publishable())
		}
		if p.Note == "" {
			t.Errorf("%s has no note saying what ships", c)
		}
	}
}

// The restricted class is most of the crawl and the whole reason the posture is
// written down: the metadata ships and the text does not, which is what makes the
// corpus reproducible by somebody else from their own lawful access.
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
	for c := doc.LicenseUnknown; c <= doc.LicenseUnredistributable; c++ {
		if !have[c] {
			t.Errorf("%s has no publication rule", c)
		}
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
	if ProjectedTotalTokens != may.TargetTokens {
		t.Errorf("the legal projection is against %d tokens and the plan targets %d",
			ProjectedTotalTokens, may.TargetTokens)
	}
	share := float64(ProjectedPublishableTokens) / float64(ProjectedTotalTokens)
	if share < 0.6 || share > 0.8 {
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
