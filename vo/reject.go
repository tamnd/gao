// Package vo is the reject store: every document the pipeline threw away,
// carrying the stage that threw it away and the reason.
//
// A cleaning pipeline is a long sequence of thresholds, and every one of them is
// wrong on the first attempt. The only way to find out how wrong is to look at
// what a threshold removed, which means the removals have to still exist. So
// nothing is deleted. A document that fails language identification, or the
// quality classifier, or the boilerplate detector, is written here with the
// stage and the reason attached, and retuning that threshold later is a query
// rather than a re-crawl.
//
// The reject store shares the segment format with the corpus store, and a reject
// embeds a whole document, so a document that comes back from vo needs no
// conversion to go into kho. That is the point: the round trip is the mechanism
// by which a threshold gets loosened.
//
// Rejects outnumber the corpus. At the scale gao is aiming for, keeping the text
// of every rejected document costs more than keeping the corpus, so the text of
// most rejects is elided and only the measurements are kept. Which rejects keep
// their text is decided by hashing the document identity rather than by a random
// draw, so the same input produces the same sample on every run.
package vo

import (
	"errors"
	"fmt"

	"github.com/tamnd/gao/doc"
)

// Reason is why a document was rejected. It is a closed set because the reject
// store's whole value is being able to ask how many documents a given stage
// removed for a given reason, and a free text field turns that question into
// string matching.
type Reason string

// The rejection reasons, in roughly the order a document meets them.
const (
	// ReasonFetch is a fetch that did not produce a usable response: a non-200
	// status, a timeout, a body that was not the declared media type.
	ReasonFetch Reason = "fetch"

	// ReasonRobots is a fetch declined by robots.txt or by a machine readable
	// text and data mining reservation.
	ReasonRobots Reason = "robots"

	// ReasonExtract is a document the extractor could not turn into text: a
	// scanned PDF with no text layer, an HTML page that was all navigation.
	ReasonExtract Reason = "extract"

	// ReasonEncoding is text that could not be brought to valid UTF-8, including
	// legacy Vietnamese font encodings that no transcoder recognized.
	ReasonEncoding Reason = "encoding"

	// ReasonResidue is a document with too many syllables that look like the
	// keystrokes of a Vietnamese input method rather than its output. The
	// keystrokes are never repaired, because repairing them guesses at what
	// somebody meant to type, so a document with enough of them is a document
	// whose text nobody can recover.
	ReasonResidue Reason = "residue"

	// ReasonControl is a document carrying more control characters than text
	// carries. It is almost always a binary that survived a content type sniff.
	ReasonControl Reason = "control"

	// ReasonContract is a document that failed the ingest contract in doc.
	ReasonContract Reason = "contract"

	// ReasonShort is a document with too little text to be one: a caption, a
	// headline on its own, a cookie notice that survived extraction. It is
	// separate from quality because it is the one rejection where nothing else
	// about the document could be measured, since every rate a filter looks at
	// is a ratio over almost nothing.
	ReasonShort Reason = "short"

	// ReasonRepetition is a document that is mostly the same text over and
	// over: a page of one repeated line, a generated listing, a transcript
	// whose extractor looped. It is separate from boilerplate because the
	// repetition is inside the document rather than shared with the rest of the
	// site, and separate from duplicate because there is no other document
	// involved.
	ReasonRepetition Reason = "repetition"

	// ReasonLanguage is a document that is not Vietnamese, or is Vietnamese
	// below the language identification threshold.
	ReasonLanguage Reason = "language"

	// ReasonTranslated is machine translated Vietnamese. It reads as fluent to a
	// metric and as wrong to a native speaker, which is why it is its own reason
	// rather than a quality score.
	ReasonTranslated Reason = "translated"

	// ReasonQuality is a document below the quality classifier's threshold.
	ReasonQuality Reason = "quality"

	// ReasonBoilerplate is a document that survived extraction but is mostly
	// template: menus, footers, cookie notices, pagination.
	ReasonBoilerplate Reason = "boilerplate"

	// ReasonDuplicate is a document removed as a near duplicate of a cluster
	// representative.
	ReasonDuplicate Reason = "duplicate"

	// ReasonPrivacy is a document removed because the personal data in it could
	// not be redacted without destroying it.
	ReasonPrivacy Reason = "privacy"

	// ReasonLicense is a document whose redistribution determination came back
	// against publication.
	ReasonLicense Reason = "license"

	// ReasonContamination is a document that overlaps an evaluation benchmark.
	ReasonContamination Reason = "contamination"

	// ReasonTrap is a document from a crawl trap: an infinite calendar, a faceted
	// search, a session-id maze.
	ReasonTrap Reason = "trap"

	// ReasonTakedown is a document removed on request. It stays in the reject
	// store as a tombstone so that a later crawl does not fetch it again.
	ReasonTakedown Reason = "takedown"
)

var reasons = map[Reason]string{
	ReasonFetch:         "the fetch did not produce a usable response",
	ReasonRobots:        "robots.txt or a mining reservation declined the fetch",
	ReasonExtract:       "extraction produced no usable text",
	ReasonEncoding:      "the bytes could not be brought to valid UTF-8",
	ReasonResidue:       "too much of the document is input method keystrokes",
	ReasonControl:       "the document carries more control characters than text",
	ReasonContract:      "the document failed the ingest contract",
	ReasonShort:         "the document holds too little text to be one",
	ReasonRepetition:    "the document is mostly the same text repeated",
	ReasonLanguage:      "the document is not Vietnamese above threshold",
	ReasonTranslated:    "the document is machine translated",
	ReasonQuality:       "the document is below the quality threshold",
	ReasonBoilerplate:   "the document is mostly template",
	ReasonDuplicate:     "the document is a near duplicate",
	ReasonPrivacy:       "the personal data could not be redacted",
	ReasonLicense:       "redistribution is not permitted",
	ReasonContamination: "the document overlaps an evaluation benchmark",
	ReasonTrap:          "the document came from a crawl trap",
	ReasonTakedown:      "the document was removed on request",
}

// Valid reports whether r is a defined rejection reason.
func (r Reason) Valid() bool {
	_, ok := reasons[r]
	return ok
}

// Describe returns a human readable explanation of the reason.
func (r Reason) Describe() string {
	if d, ok := reasons[r]; ok {
		return d
	}
	return fmt.Sprintf("unknown reason %q", string(r))
}

// Reasons returns every defined rejection reason, in pipeline order. Reports
// that break rejections down by reason iterate this, so a new reason appears in
// them without anybody editing the report.
func Reasons() []Reason {
	return []Reason{
		ReasonFetch, ReasonRobots, ReasonExtract, ReasonEncoding,
		ReasonResidue, ReasonControl,
		ReasonContract, ReasonShort, ReasonRepetition,
		ReasonLanguage, ReasonTranslated, ReasonQuality,
		ReasonBoilerplate, ReasonDuplicate, ReasonPrivacy, ReasonLicense,
		ReasonContamination, ReasonTrap, ReasonTakedown,
	}
}

// ErrNotRejectable is returned when a rejection is missing the information that
// would let somebody act on it later.
var ErrNotRejectable = errors.New("vo: rejection is not recordable")

// Reject is one rejected document. The document is embedded so that the JSON
// stays flat and so that a reject that gets readmitted is already a document.
type Reject struct {
	doc.Document

	// Stage is the pipeline stage that made the call, as name@semver. The
	// version matters: the interesting question about a threshold is usually
	// what changed between two versions of the stage that set it.
	Stage string `json:"reject_stage"`

	// Reason is the closed-set category.
	Reason Reason `json:"reject_reason"`

	// Detail is the specific value that failed, in whatever form the stage
	// naturally has it: the language score, the classifier output, the cluster
	// the document lost to. It is for a human reading one row, not for a query.
	Detail string `json:"reject_detail,omitempty"`

	// Elided marks a reject whose text was dropped to keep the reject store from
	// outgrowing the corpus. Everything else about the row survives, so a
	// threshold can be retuned against the measurements and only the sample can
	// be read.
	Elided bool `json:"reject_elided,omitempty"`
}

// Admit is the reject store's admission rule, and it is deliberately much weaker
// than the corpus store's. A reject that failed the ingest contract obviously
// cannot satisfy it, and requiring it here would mean the store could not hold
// the documents it exists to hold.
//
// What it does require is that the rejection be actionable: a stage, a defined
// reason, and enough identity to trace the row back to something. A rejection
// nobody can trace is a deletion with extra steps.
func (r *Reject) Admit() error {
	var problems []error
	if r.Stage == "" {
		problems = append(problems, errors.New("reject_stage is empty"))
	}
	if !r.Reason.Valid() {
		problems = append(problems, fmt.Errorf("reject_reason %q is not a defined reason", string(r.Reason)))
	}
	if r.DocID.IsZero() && r.RawID.IsZero() {
		problems = append(problems, errors.New("reject has neither doc_id nor raw_id, so nothing can trace it back to a source"))
	}
	if r.Elided && r.Text != "" {
		problems = append(problems, errors.New("reject is marked elided but still carries text"))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrNotRejectable, errors.Join(problems...))
	}
	return nil
}

// Elide drops the document text and marks the row. Identity, provenance, and
// every measurement survive, which is what retuning a threshold actually needs.
// The text is recoverable from the source through source_locator for as long as
// the source is around.
func (r *Reject) Elide() {
	r.Text = ""
	r.Elided = true
}
