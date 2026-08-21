package law

import "github.com/tamnd/gao/doc"

// The publication posture.
//
// One rule, and everything below is it applied to each license class: gao
// publishes what it may publish and publishes the recipe for the rest.
//
// The crawled row is the one that matters, because that is where most of the
// crawl lands. A crawled document ships as full text with its address on it,
// under the posture Common Crawl fetches and publishes under and that every
// corpus derived from Common Crawl inherits: publicly reachable, robots.txt
// honored, reservations honored, published as fetched, takedowns acted on. gao
// reads the same web the same way, so gao passes it on the same way.
//
// That row used to say restricted, and restricted used to mean the text was
// withheld and the URL shipped so that somebody else could refetch the pages
// under their own lawful access. Keeping it there would not have been the
// careful choice, it would have been an inconsistent one: gao ingests FineWeb-2,
// HPLT and GlotCC, all three of which are the bodies of crawled pages published
// by somebody who made exactly the determination gao was declining to make.
// Withholding gao's own crawl while training on everybody else's is a posture
// that cannot be written down without embarrassment, which is the test this file
// exists to apply.
//
// The restricted row is now what it says on the type: material with a term gao
// cannot satisfy in bulk. Theses under institutional terms and transcripts that
// inherit a recording's status are what is left in it.
//
// The consequence is that gao's headline token count includes tokens gao cannot
// ship, so the release notes carry both numbers rather than the flattering one.
// [ProjectedTotalTokens] and [ProjectedPublishableTokens] are the prediction, and
// they are written down before the measurement per the standing rule that a
// prediction lives next to its result including the ones we got wrong.

// Publication is what ships for a document of one license class.
type Publication struct {
	Class doc.LicenseClass

	// Text reports whether the document's text appears in the published
	// artifact.
	Text bool

	// Metadata reports whether the URL, the scores, and the provenance columns
	// appear, which is a separate question from the text and is the whole point
	// of the restricted class.
	Metadata bool

	// Counted reports whether the document appears in the release note totals.
	// A withheld document is still counted, because a number that quietly
	// disappears reads as a number that was never there.
	Counted bool

	// Note is what the row means, in one sentence.
	Note string
}

var publications = []Publication{
	{
		Class:    doc.LicenseOpen,
		Text:     true,
		Metadata: true,
		Counted:  true,
		Note:     "full text, with nothing to carry alongside it",
	},
	{
		Class:    doc.LicensePermissiveAttribution,
		Text:     true,
		Metadata: true,
		Counted:  true,
		Note:     "full text, with the attribution in the record rather than in a notices file nobody reads",
	},
	{
		Class:    doc.LicenseCrawled,
		Text:     true,
		Metadata: true,
		Counted:  true,
		Note:     "full text and full markup, with the address it was fetched from on every row, which is both the attribution and the takedown handle",
	},
	{
		Class:    doc.LicenseRestricted,
		Text:     false,
		Metadata: true,
		Counted:  true,
		Note:     "the URL, every metadata column, every score, and the dedup cluster id, and not the text",
	},
	{
		Class:    doc.LicenseUnredistributable,
		Text:     false,
		Metadata: false,
		Counted:  true,
		Note:     "nothing, though the count and the reason go in the release notes",
	},
	{
		Class:    doc.LicenseUnknown,
		Text:     false,
		Metadata: false,
		Counted:  false,
		Note:     "nothing, and it should not exist: an unknown class is a document the pipeline failed to determine, and the ingest contract rejects it",
	},
}

// Publications returns the posture for every class, strongest first.
func Publications() []Publication {
	out := make([]Publication, len(publications))
	copy(out, publications)
	return out
}

// Publishes returns what ships for a document of the given class. An
// undefined class gets the same answer as an unknown one, which is nothing.
func Publishes(c doc.LicenseClass) Publication {
	for _, p := range publications {
		if p.Class == c {
			return p
		}
	}
	return Publication{Class: c, Note: "not a license class, so nothing ships"}
}

// The projected split between what the corpus holds and what the corpus can
// ship. These are predictions rather than measurements, and the theses and the
// pages that reserved themselves are where the difference is expected to come
// from.
//
// The publishable projection was 210B while the crawl was classed restricted,
// because the crawl is most of the corpus and the whole of it was being
// withheld. Moving the crawl to [doc.LicenseCrawled] moved that block across,
// and what is left on the other side is the material with a real term on it:
// institutional repository theses, transcripts that inherit a recording's
// status, and every page that carried a machine readable reservation. Those
// three are a tenth of the plan, so the projection is a tenth withheld.
//
// It is a prediction and it is recorded here before the measurement, which is
// the standing rule. The number to check it against is the one [cmd/gao law]
// prints off a counted store, not this one.
const (
	ProjectedTotalTokens       int64 = 300_000_000_000
	ProjectedPublishableTokens int64 = 270_000_000_000
)

// Fallback is what gao does if a question comes back the wrong way. There is one
// of these, because there is one question whose answer can end the project as
// specified, and writing the response down in advance is what turns it from an
// ending into a reduction in scope.
type Fallback struct {
	// Question is the counsel question that triggers it.
	Question string

	// If is the answer that triggers it.
	If string

	// Then is what gao does instead.
	Then string

	// Publishes is what still ships under it.
	Publishes []string

	// Withholds is what does not.
	Withholds string
}

// RecipeOnly is the standing fallback for Q1.
//
// Even in the narrowest reading of the text and data mining allowance, gao weigh
// publish the URL list, every metadata column, every classifier and its reference
// set, and the whole pipeline. That is still the largest and most reproducible
// Vietnamese corpus artifact anybody has published. It is a build script rather
// than a download, and it takes a day of compute to turn one into the other.
var RecipeOnly = Fallback{
	Question: "Q1",
	If:       "counsel reads the text and data mining allowance as not covering model training",
	Then:     "gao publishes the recipe and not the corpus, and the project continues at reduced scope rather than ending",
	Publishes: []string{
		"the URL list, one row per document, with the crawl timestamp that fetched it",
		"every metadata column and every quality score, which is the expensive part",
		"the classifiers, their training data, and the reference set they were measured against",
		"the pipeline, the configs, the manifests, and the version of every stage",
	},
	Withholds: "the extracted text of every document",
}
