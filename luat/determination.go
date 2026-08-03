package luat

import (
	"slices"

	"github.com/tamnd/gao/doc"
)

// The per source license determination.
//
// A corpus assembled from four acquisition paths and hundreds of thousands of
// hosts does not have a license, it has a distribution of them, and the only way
// to filter a release by license is to have made the determination per document
// at ingest. The table below is what an acquisition path reads to do that: the
// class it writes into the record, and the evidence string it writes next to it.
//
// Evidence is not decoration. A class without the reason it was assigned cannot
// be rechecked when the reason changes, and the reasons here do change: an
// upstream dataset can relicense, a repository can rewrite its terms, and a
// counsel answer can move a whole row. The record carries the evidence so that
// the recheck is a query rather than an argument.

// Determination is the license class gao records for one body of material, and
// what decided it.
type Determination struct {
	// Subject is the material, named the way a person would name it rather than
	// the way the pipeline addresses it.
	Subject string

	// Sources is the acquisition paths this material arrives on. It is empty
	// when the material arrives inside another source rather than as one of its
	// own, such as Wikipedia inside the Hugging Face ingest, and that is a fact
	// about the plumbing rather than about the license.
	Sources []doc.Source

	// Class is what goes in the record's license_class column.
	Class doc.LicenseClass

	// PerItem marks a row where the determination is made one item at a time and
	// Class is only the usual outcome. Two rows are like this and both are
	// honest about it: repository terms vary by institution and journal terms
	// vary by journal, and averaging them into a single class would be recording
	// a guess as a fact.
	PerItem bool

	// Evidence is what decided the class, and it is the string the record's
	// license_evidence column gets.
	Evidence string

	// Question is the counsel question that could move this row, empty when
	// nothing outstanding bears on it.
	Question string
}

// determinations is the table. Ingested corpora first, then the material gao
// acquires itself, then gao's own output.
var determinations = []Determination{
	{
		Subject:  "HPLT v3",
		Sources:  []doc.Source{doc.SourceHPLT3},
		Class:    doc.LicenseOpen,
		Evidence: "CC0 on the release, and the release is what gao ingests",
	},
	{
		Subject:  "FineWeb2 and FinePDFs",
		Sources:  []doc.Source{doc.SourceFineWeb2, doc.SourceFinePDFs},
		Class:    doc.LicensePermissiveAttribution,
		Evidence: "ODC-By, whose attribution obligation is satisfiable at corpus level",
	},
	{
		Subject:  "CulturaX",
		Sources:  []doc.Source{doc.SourceCulturaX},
		Class:    doc.LicensePermissiveAttribution,
		Evidence: "inherits the mC4 and OSCAR terms, and the strictest inherited term is the one that controls",
	},
	{
		Subject:  "GlotCC",
		Sources:  []doc.Source{doc.SourceGlotCC},
		Class:    doc.LicenseOpen,
		Evidence: "CC0",
	},
	{
		Subject:  "MADLAD-400",
		Sources:  []doc.Source{doc.SourceMADLAD400},
		Class:    doc.LicensePermissiveAttribution,
		Evidence: "ODC-By",
	},
	{
		Subject:  "Vietnamese Wikipedia",
		Class:    doc.LicensePermissiveAttribution,
		Evidence: "CC-BY-SA, and the share alike term is the complication rather than the attribution",
		Question: "Q7",
	},
	{
		Subject:  "Vietnamese statutes, decrees, circulars, and gazettes",
		Sources:  []doc.Source{doc.SourceCrawl},
		Class:    doc.LicenseOpen,
		Evidence: "article 15 of the intellectual property law puts legal and administrative documents outside copyright protection",
	},
	{
		Subject:  "Government portal content that is not a legal document",
		Sources:  []doc.Source{doc.SourceCrawl},
		Class:    doc.LicenseRestricted,
		Evidence: "a ministry's news article is an ordinary copyrighted work, and the domain it sits on does not change that",
	},
	{
		Subject:  "Crawled news, forums, and blogs",
		Sources:  []doc.Source{doc.SourceCrawl, doc.SourceCCRecovery},
		Class:    doc.LicenseRestricted,
		Evidence: "no license grant and no reservation, so gao may process it and may not pass it on",
	},
	{
		Subject:  "Crawled pages carrying a reservation",
		Sources:  []doc.Source{doc.SourceCrawl, doc.SourceCCRecovery},
		Class:    doc.LicenseUnredistributable,
		Evidence: "a machine readable text and data mining opt out, honored in the published artifact and, pending Q2, in training",
		Question: "Q2",
	},
	{
		Subject:  "University theses and institutional repository PDFs",
		Sources:  []doc.Source{doc.SourceMedia},
		Class:    doc.LicenseRestricted,
		PerItem:  true,
		Evidence: "repository terms vary by institution and are read one institution at a time",
	},
	{
		Subject:  "Journal content from VJOL and elsewhere",
		Sources:  []doc.Source{doc.SourceMedia},
		Class:    doc.LicensePermissiveAttribution,
		PerItem:  true,
		Evidence: "many journals carry a Creative Commons license and each journal's own statement is read",
	},
	{
		Subject:  "Speech transcripts",
		Sources:  []doc.Source{doc.SourceMedia},
		Class:    doc.LicenseRestricted,
		PerItem:  true,
		Evidence: "a transcript inherits the status of the recording, so the determination is the recording's determination",
	},
	{
		Subject:  "gao-synth",
		Sources:  []doc.Source{doc.SourceSynth},
		Class:    doc.LicenseOpen,
		Evidence: "gao's own output, subject to whatever the generator's license says about its outputs",
		Question: "Q8",
	},
}

// Determinations returns the table, in the order above.
func Determinations() []Determination {
	out := make([]Determination, len(determinations))
	copy(out, determinations)
	return out
}

// For returns the determinations that apply to an acquisition path. A path can
// have several, because gao-crawl fetches a statute and a forum thread through
// the same pipe and they are not the same license.
func For(s doc.Source) []Determination {
	var out []Determination
	for _, d := range determinations {
		if slices.Contains(d.Sources, s) {
			out = append(out, d)
		}
	}
	return out
}

// Unresolved returns the determinations that an outstanding counsel question
// could move. It is the list to reread on the day an answer arrives.
func Unresolved() []Determination {
	var out []Determination
	for _, d := range determinations {
		if d.Question == "" {
			continue
		}
		if q, ok := Ask(d.Question); ok && q.Answered() {
			continue
		}
		out = append(out, d)
	}
	return out
}
