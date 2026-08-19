package doc

import (
	"encoding/hex"
	"fmt"
	"time"
)

// SchemaVersion is the version of the record layout in this package. It is a
// column on every document rather than a filename convention, because a store
// that is appended to by six producers across a pipeline upgrade will contain
// two versions in one segment, and the reader has to be able to tell.
const SchemaVersion uint16 = 1

// Document is one record in the store. The field groups are embedded rather than
// nested so that the JSON stays flat, which is what the Parquet release needs
// and what makes a record on disk readable without a tool.
//
// The schema is wide on purpose. Provenance is a required column set rather than
// a nice to have, and a document that cannot carry it is rejected rather than
// admitted with nulls: a corpus with partial provenance cannot be filtered by
// license, cannot honor a takedown, and cannot be reproduced.
type Document struct {
	// DocID is blake3 of the normalized text. It is the document's identity, so
	// two documents with the same normalized text are the same document no
	// matter which path found them.
	DocID Hash `json:"doc_id"`

	// RawID is blake3 of the pre-extraction bytes. It is what links a record
	// back to the WARC payload or the source file it came from.
	RawID Hash `json:"raw_id"`

	// Text is normalized: NFC, canonical tone mark placement, legacy encodings
	// already transcoded.
	Text string `json:"text"`

	SchemaVersion uint16 `json:"schema_version"`

	Provenance
	Crawl
	Language
	Quality
	Dedup
	Privacy
	Licensing
	Shape
}

// Provenance is the required column set. Every field here must be populated for
// a document to be admitted to the store.
type Provenance struct {
	Source Source `json:"source"`

	// SourceLocator points back into the source: shard and offset for an
	// ingested corpus, file, offset, and length for a WARC record.
	SourceLocator string `json:"source_locator"`

	URL  string `json:"url"`
	Host string `json:"host"`

	// URLTemplate is the URL with its variable path and query components
	// replaced by placeholders. It is what the crawl budgets against, and it is
	// how a calendar trap is recognized as one URL rather than as ten thousand.
	URLTemplate string `json:"url_template,omitempty"`

	FetchedAt time.Time `json:"fetched_at"`
	MediaType string    `json:"media_type"`

	// Extractor is name@semver. Two documents extracted by different versions of
	// the same extractor are not comparable and this is how we know.
	Extractor string `json:"extractor"`

	// PipelineVersion is the semver of the cleaning pipeline that produced this
	// row.
	PipelineVersion string `json:"pipeline_version"`
}

// Crawl holds the fields that only a fetched document has. They are empty for
// ingested corpora, where the fetch happened inside somebody else's pipeline.
type Crawl struct {
	HTTPStatus uint16 `json:"http_status,omitempty"`

	// RobotsDecision is allow, allow_default, or deferred, recorded per fetch
	// rather than assumed from a global setting, so that a consent question
	// years later has an answer.
	RobotsDecision string `json:"robots_decision,omitempty"`
	RobotsRule     string `json:"robots_rule,omitempty"`
	RobotsHash     Hash   `json:"robots_hash,omitzero"`

	// TDMSignals records the machine-readable text and data mining reservations
	// the response carried: TDMRep, X-Robots-Tag, and noai meta tags. It is
	// keyed by the mechanism and holds what that mechanism said, in the
	// spelling the site used, because "reserved" is a conclusion and
	// `tdm-reservation: 1` is what happened.
	TDMSignals map[string]string `json:"tdm_signals,omitempty"`

	// Consent is what the page said about being kept and being trained on,
	// read off TDMSignals and reduced to one word. See [Consent].
	Consent Consent `json:"consent,omitempty"`
}

// Consent is a document's own statement about being used, in one word.
//
// It is a column rather than a pair of booleans because a document that may not
// be kept is not a document with a flag on it, and because the two reservations
// are nested in practice: a site that will not be indexed will not be trained on
// either.
//
// The zero value is the important one. It means nobody asked, which is the true
// state of every document that came out of somebody else's corpus, and it is not
// the same as a site that was asked and said yes. Defaulting it to open would
// turn a column nobody filled in into a permission nobody gave.
type Consent string

// The consent states.
const (
	// ConsentUnasked is the zero value: this document reached the store by a
	// path that did not read a reservation. Every ingested corpus is here.
	ConsentUnasked Consent = ""

	// ConsentOpen is a page that was asked and reserved nothing.
	ConsentOpen Consent = "open"

	// ConsentNoTrain is a page that reserved its text and data mining rights,
	// through TDMRep or one of the AI specific directives.
	ConsentNoTrain Consent = "no-train"

	// ConsentNoIndex is a page that asked not to be kept at all.
	ConsentNoIndex Consent = "no-index"
)

// Valid reports whether c is one of the four states.
func (c Consent) Valid() bool {
	switch c {
	case ConsentUnasked, ConsentOpen, ConsentNoTrain, ConsentNoIndex:
		return true
	}
	return false
}

// Reserved reports whether the page said no to something.
func (c Consent) Reserved() bool {
	return c == ConsentNoTrain || c == ConsentNoIndex
}

// Language is the output of language identification.
type Language struct {
	// Lang is always vie in gao. It is stored anyway, because a column that is
	// constant today is a column that does not need a migration tomorrow.
	Lang      string  `json:"lang"`
	LangScore float32 `json:"lang_score"`

	// Diacritics is present, absent, or mixed. Vietnamese written without tone
	// marks is still Vietnamese and it is still useful, as a diacritic
	// restoration training pair if nothing else, but it is not the same
	// distribution and it is not mixed into the corpus silently.
	Diacritics string `json:"diacritics"`

	// Translated is the machine translation detector's verdict. Translated
	// Vietnamese reads as fluent to a metric and as wrong to a native speaker.
	Translated bool `json:"translated"`
}

// Quality is the output of the classifiers and heuristics.
type Quality struct {
	GaoQual float32 `json:"gao_qual"`
	GaoEdu  float32 `json:"gao_edu"`

	// HPLTBucket and Register carry the source corpus's own labels where it has
	// them, so that gao's classifier can be compared against an independent one
	// rather than only against itself.
	HPLTBucket uint8  `json:"hplt_bucket,omitempty"`
	Register   string `json:"register,omitempty"`

	// Heuristics stores raw values, not verdicts. A corpus that records "passed
	// the length filter" cannot be re-filtered at a different threshold without
	// recomputing it from the text; one that records the length can. This costs
	// about 60 bytes per row and it is the cheapest future-proofing available.
	Heuristics map[string]float32 `json:"heuristics,omitempty"`
}

// Cluster identifies a duplicate cluster. It is 16 bytes rather than 32 because
// it is an index into a partition of the corpus, not a content commitment, and
// at half a billion documents the collision probability is negligible while the
// storage saving across every row is not.
type Cluster [16]byte

// IsZero reports whether the cluster identifier is unset.
func (c Cluster) IsZero() bool { return c == Cluster{} }

// String returns the identifier as lowercase hex.
func (c Cluster) String() string { return hex.EncodeToString(c[:]) }

// MarshalText implements [encoding.TextMarshaler].
func (c Cluster) MarshalText() ([]byte, error) {
	out := make([]byte, hex.EncodedLen(len(c)))
	hex.Encode(out, c[:])
	return out, nil
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (c *Cluster) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*c = Cluster{}
		return nil
	}
	if len(text) != hex.EncodedLen(len(c)) {
		return fmt.Errorf("doc: cluster must be %d hex characters, got %d", hex.EncodedLen(len(c)), len(text))
	}
	_, err := hex.Decode(c[:], text)
	return err
}

// Dedup is the document's position in its duplicate cluster.
type Dedup struct {
	DupCluster     Cluster `json:"dup_cluster,omitzero"`
	DupClusterSize uint32  `json:"dup_cluster_size,omitempty"`

	// IsRepresentative marks the one document per cluster that a deduplicated
	// view keeps. The others stay in the store, because deduplication is tuned
	// rather than maximized and retuning it must not require re-crawling.
	IsRepresentative bool `json:"is_representative"`
}

// RedactionLevel is how much personal data has been removed from a document.
type RedactionLevel uint8

const (
	// RedactNone is the unredacted document. Internal only.
	RedactNone RedactionLevel = 0
	// RedactStandard removes structured identifiers: national ID, phone, email,
	// tax code, plate.
	RedactStandard RedactionLevel = 1
	// RedactStrict additionally removes addresses and names in the contexts
	// where co-occurrence makes them identifying.
	RedactStrict RedactionLevel = 2
)

// PIISpan locates a detected identifier in the text.
type PIISpan struct {
	Start uint32 `json:"start"`
	Len   uint32 `json:"len"`
	Type  string `json:"type"`
}

// Privacy is the personal data handling applied to this row.
type Privacy struct {
	PIILevel RedactionLevel `json:"pii_level"`
	PIITypes []string       `json:"pii_types,omitempty"`

	// PIISpans carries where the identifiers were found, and the cleaning line
	// clears it on every row it writes. Two reasons, and either one is enough.
	// The offsets index the text that was searched, and covering rewrote that
	// text, so on a covered row they point at the wrong bytes. And offsets
	// published next to covered text re-identify what was covered, which makes
	// the covering a performance. The field stays in the contract because a
	// caller that runs [cover] itself gets the spans back from it.
	PIISpans []PIISpan `json:"pii_spans,omitempty"`
}

// Licensing is the per-document redistribution determination.
type Licensing struct {
	LicenseClass LicenseClass `json:"license_class"`

	// LicenseEvidence records what determined the class: the upstream corpus
	// license, a robots or TDM reservation, a statutory exemption, or a manual
	// determination. A class without evidence is a guess.
	LicenseEvidence string `json:"license_evidence"`
}

// Shape is what the document is and how big it is.
type Shape struct {
	// Structure is article, forum_thread, legal, thesis, gazette, transcript,
	// and so on. It drives the extraction handler and the mixture weights.
	Structure string `json:"structure,omitempty"`

	NChars     uint32 `json:"n_chars"`
	NSyllables uint32 `json:"n_syllables"`

	// NTokens is gao tokens, produced by tokenizing. The unit conversions in
	// units.go are for estimates and never for a count that ships.
	NTokens uint32 `json:"n_tokens"`

	// ContamFlags lists benchmarks this document overlaps with. A document is
	// flagged rather than deleted, so that the same store can serve a training
	// run that excludes them and an analysis that counts them.
	ContamFlags []string `json:"contam_flags,omitempty"`

	// UpstreamFields keeps the source corpus's own metadata verbatim. It is the
	// difference between being able to answer a question about provenance later
	// and having to re-ingest.
	UpstreamFields map[string]string `json:"upstream_fields,omitempty"`
}

// Natural reports whether the document counts toward the corpus size.
func (d *Document) Natural() bool {
	return d.Source.Natural()
}

// Publishable reports whether the document may appear in a published artifact at
// the given redaction level. Three conditions have to hold: the license has to
// permit redistribution, the row has to have been redacted at least as strictly
// as the artifact requires, and the page must not have reserved itself.
//
// The consent check is here rather than only at the fetch because a reservation
// that is honored in one place is a reservation that is honored while that place
// is the only way in. A document can enter the store before its reservation is
// read, from a path that predates this column, or from a site that changed its
// mind, and the publish step is the last point where anybody can still act on it.
func (d *Document) Publishable(at RedactionLevel) bool {
	return d.LicenseClass.Publishable() && d.PIILevel >= at && !d.Consent.Reserved()
}
