package doc

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// The ingest contract. Every acquisition path, whether it pulls from Hugging
// Face, recovers from Common Crawl, crawls the web directly, or extracts a PDF,
// produces documents that satisfy [Document.Admit] or produces nothing.
//
// This is the earliest gate in the pipeline and the strictest one on purpose.
// The alternative to rejecting an incomplete document is admitting it with nulls
// in its provenance columns, and a corpus where some rows have provenance and
// some do not is a corpus that cannot be filtered by license, cannot honor a
// takedown, and cannot be reproduced. Those are not three problems, they are one
// problem: nobody can trust any row, because there is no way to tell the good
// rows from the bad ones after the fact.
//
// The cost of this strictness is a rejection rate, and rejections go to the
// reject store rather than to nowhere, so the contract can be loosened later
// without re-acquiring anything.

// ErrIncomplete is the sentinel for a document that fails the ingest contract.
// Errors returned by [Document.Admit] wrap it.
var ErrIncomplete = errors.New("doc: document fails the ingest contract")

// MinChars is the shortest document the contract admits. It is a floor against
// empty and near-empty extractions rather than a quality filter: quality
// filtering happens later, with thresholds tuned against a reference set, and
// doing it here would hide the decision inside the ingest path.
const MinChars = 32

// Admit checks the document against the ingest contract and returns an error
// describing every violation, not just the first. A caller writing to the reject
// store wants all of them, since fixing one at a time across a 500 million
// document run is not a plan.
func (d *Document) Admit() error {
	var problems []error
	bad := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(format, args...))
	}

	// Identity.
	if d.DocID.IsZero() {
		bad("doc_id is unset")
	}
	if d.RawID.IsZero() {
		bad("raw_id is unset")
	}
	if d.SchemaVersion == 0 {
		bad("schema_version is unset")
	} else if d.SchemaVersion > SchemaVersion {
		bad("schema_version %d is newer than this build understands (%d)", d.SchemaVersion, SchemaVersion)
	}

	// Text. The identity check is the one that matters: doc_id is blake3 of the
	// normalized text, so a mismatch means the text was changed after the
	// identifier was computed, and every downstream dedup and shard decision
	// made about this document is about a document that no longer exists.
	switch {
	case d.Text == "":
		bad("text is empty")
	case !utf8.ValidString(d.Text):
		bad("text is not valid UTF-8")
	case utf8.RuneCountInString(d.Text) < MinChars:
		bad("text is %d characters, below the %d character floor", utf8.RuneCountInString(d.Text), MinChars)
	default:
		if got := SumString(d.Text); got != d.DocID {
			bad("doc_id %s does not match blake3 of the text (%s)", d.DocID, got)
		}
	}

	// Provenance. Every field, every time.
	if !d.Source.Valid() {
		bad("source %q is not an acquisition path", string(d.Source))
	}
	if d.SourceLocator == "" {
		bad("source_locator is unset, so this document cannot be traced back to its input")
	}
	problems = append(problems, checkURL(d.URL, d.Host)...)
	if d.FetchedAt.IsZero() {
		bad("fetched_at is unset")
	} else if d.FetchedAt.Location() != time.UTC {
		bad("fetched_at is not UTC")
	}
	if d.MediaType == "" {
		bad("media_type is unset")
	}
	if err := checkVersioned("extractor", d.Extractor); err != nil {
		problems = append(problems, err)
	}
	if d.PipelineVersion == "" {
		bad("pipeline_version is unset")
	}

	// Language. gao is a Vietnamese corpus and a document that is not Vietnamese
	// is not a gao document, whatever else is true about it.
	if d.Lang != "vie" {
		bad("lang is %q, gao admits only vie", d.Lang)
	}
	if d.LangScore <= 0 || d.LangScore > 1 {
		bad("lang_score %v is outside (0, 1]", d.LangScore)
	}
	switch d.Diacritics {
	case "present", "absent", "mixed":
	case "":
		bad("diacritics is unset")
	default:
		bad("diacritics is %q, want present, absent, or mixed", d.Diacritics)
	}

	// Consent. Unasked is allowed, because most documents come out of somebody
	// else's corpus and nobody was there to ask. What is not allowed is a
	// document that carries a reservation it read and then a state that does
	// not follow from it, which is the shape a dropped honor check takes: the
	// evidence is in the row and the conclusion has been quietly softened.
	if !d.Consent.Valid() {
		bad("consent is %q, want open, no-train, no-index, or unset", string(d.Consent))
	}
	if len(d.TDMSignals) > 0 && d.Consent == ConsentUnasked {
		bad("tdm_signals holds %d reservations and consent is unset, so something read them and did not record what they said", len(d.TDMSignals))
	}

	// Licensing. Unknown is a valid value of the column and an invalid value for
	// an admitted document: it means nobody looked.
	switch {
	case !d.LicenseClass.Valid():
		bad("license_class %d is not a class", uint8(d.LicenseClass))
	case d.LicenseClass == LicenseUnknown:
		bad("license_class is unknown, which means no determination was made")
	}
	if d.LicenseEvidence == "" {
		bad("license_evidence is unset, so the license class is a guess")
	}

	// Privacy. The level-2 rule is a re-identification rule rather than a
	// bookkeeping one: publishing the offsets of redacted personal data next to
	// the redacted text undoes the redaction.
	if d.PIILevel > RedactStrict {
		bad("pii_level %d is not a redaction level", uint8(d.PIILevel))
	}
	if d.PIILevel == RedactStrict && len(d.PIISpans) > 0 {
		bad("pii_spans present at pii_level 2, which re-identifies what the redaction removed")
	}
	for i, s := range d.PIISpans {
		if s.Type == "" {
			bad("pii_spans[%d] has no type", i)
		}
		if int(s.Start)+int(s.Len) > len(d.Text) {
			bad("pii_spans[%d] runs past the end of the text", i)
		}
	}

	// Counts. These are cheap to check and they catch a whole class of bug where
	// a stage rewrites the text and forgets to recount.
	if d.NChars == 0 {
		bad("n_chars is unset")
	} else if got := uint32(utf8.RuneCountInString(d.Text)); got != d.NChars {
		bad("n_chars is %d, text has %d characters", d.NChars, got)
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrIncomplete, errors.Join(problems...))
}

// checkURL validates the URL and that the host column agrees with it. The host
// is stored separately because it is what takedowns, budgets, and sorting all
// operate on, and recomputing it from the URL on every read at half a billion
// rows is not free.
func checkURL(raw, host string) []error {
	if raw == "" {
		return []error{errors.New("url is unset")}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return []error{fmt.Errorf("url %q does not parse: %w", raw, err)}
	}
	var problems []error
	if !u.IsAbs() {
		problems = append(problems, fmt.Errorf("url %q is not absolute", raw))
	}
	if u.Hostname() == "" {
		problems = append(problems, fmt.Errorf("url %q has no host", raw))
	}
	switch {
	case host == "":
		problems = append(problems, errors.New("host is unset"))
	case !strings.EqualFold(host, u.Hostname()):
		problems = append(problems, fmt.Errorf("host %q does not match the url host %q", host, u.Hostname()))
	}
	return problems
}

// checkVersioned enforces name@version. An extraction result without the version
// of the extractor that produced it cannot be compared against a result from a
// later version, which makes every extraction improvement unmeasurable.
func checkVersioned(field, v string) error {
	if v == "" {
		return fmt.Errorf("%s is unset", field)
	}
	name, version, ok := strings.Cut(v, "@")
	if !ok || name == "" || version == "" {
		return fmt.Errorf("%s is %q, want name@version", field, v)
	}
	return nil
}
