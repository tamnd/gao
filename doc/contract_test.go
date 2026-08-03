package doc

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// sampleText is real Vietnamese rather than lorem ipsum with diacritics
// sprinkled on, because the tests below count characters and hash bytes and both
// of those go wrong in interesting ways on text that is not actually Vietnamese.
const sampleText = "Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc."

// valid returns a document that satisfies the contract. Tests break one thing at
// a time from here, which keeps each test about the rule it is testing.
func valid() *Document {
	d := &Document{
		RawID:         SumString("<html>" + sampleText + "</html>"),
		Text:          sampleText,
		SchemaVersion: SchemaVersion,
		Provenance: Provenance{
			Source:          SourceCrawl,
			SourceLocator:   "gao-crawl-2026-09/00042.warc.gz@1048576+8192",
			URL:             "https://vnexpress.net/thoi-su/mot-bai-viet-123456.html",
			Host:            "vnexpress.net",
			FetchedAt:       time.Date(2026, 9, 14, 3, 22, 11, 0, time.UTC),
			MediaType:       "text/html",
			Extractor:       "go-trafilatura@1.4.0",
			PipelineVersion: "0.1.0",
		},
		Language: Language{
			Lang:       "vie",
			LangScore:  0.998,
			Diacritics: "present",
		},
		Licensing: Licensing{
			LicenseClass:    LicenseOpen,
			LicenseEvidence: "robots allow, no TDM reservation, statutory exemption",
		},
	}
	d.DocID = SumString(d.Text)
	d.NChars = uint32(utf8.RuneCountInString(d.Text))
	return d
}

func TestValidDocumentIsAdmitted(t *testing.T) {
	if err := valid().Admit(); err != nil {
		t.Fatalf("the reference document was rejected: %v", err)
	}
}

func TestAdmitRejectsIncompleteProvenance(t *testing.T) {
	// One case per required provenance column. A document that cannot carry
	// provenance is dropped rather than admitted with nulls, and this is the
	// test that says so.
	cases := map[string]struct {
		break_ func(*Document)
		want   string
	}{
		"no source":           {func(d *Document) { d.Source = "" }, "not an acquisition path"},
		"unknown source":      {func(d *Document) { d.Source = "scraped-from-somewhere" }, "not an acquisition path"},
		"no source locator":   {func(d *Document) { d.SourceLocator = "" }, "source_locator is unset"},
		"no url":              {func(d *Document) { d.URL = "" }, "url is unset"},
		"relative url":        {func(d *Document) { d.URL = "/thoi-su/bai-viet.html"; d.Host = "" }, "is not absolute"},
		"no host":             {func(d *Document) { d.Host = "" }, "host is unset"},
		"host disagrees":      {func(d *Document) { d.Host = "example.com" }, "does not match the url host"},
		"no fetch time":       {func(d *Document) { d.FetchedAt = time.Time{} }, "fetched_at is unset"},
		"fetch time not utc":  {func(d *Document) { d.FetchedAt = d.FetchedAt.In(time.FixedZone("ICT", 7*3600)) }, "not UTC"},
		"no media type":       {func(d *Document) { d.MediaType = "" }, "media_type is unset"},
		"no extractor":        {func(d *Document) { d.Extractor = "" }, "extractor is unset"},
		"unversioned":         {func(d *Document) { d.Extractor = "go-trafilatura" }, "want name@version"},
		"no pipeline version": {func(d *Document) { d.PipelineVersion = "" }, "pipeline_version is unset"},
		"no raw id":           {func(d *Document) { d.RawID = Hash{} }, "raw_id is unset"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := valid()
			tc.break_(d)
			err := d.Admit()
			if err == nil {
				t.Fatal("document was admitted")
			}
			if !errors.Is(err, ErrIncomplete) {
				t.Errorf("error does not wrap ErrIncomplete: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestAdmitRejectsMismatchedIdentity(t *testing.T) {
	// The failure this catches is a stage that rewrites the text and forgets to
	// recompute the identifier. Every dedup and shard decision downstream would
	// then be about a document that no longer exists.
	d := valid()
	d.Text = strings.ReplaceAll(d.Text, "hòa", "hoà")
	d.NChars = uint32(utf8.RuneCountInString(d.Text))
	err := d.Admit()
	if err == nil {
		t.Fatal("a document whose text no longer matches its doc_id was admitted")
	}
	if !strings.Contains(err.Error(), "does not match blake3") {
		t.Errorf("error %q does not name the mismatch", err)
	}
}

func TestAdmitRejectsStaleCharacterCount(t *testing.T) {
	d := valid()
	d.NChars += 5
	if err := d.Admit(); err == nil || !strings.Contains(err.Error(), "n_chars") {
		t.Fatalf("stale n_chars was not caught: %v", err)
	}
}

func TestAdmitRejectsNonVietnamese(t *testing.T) {
	d := valid()
	d.Lang = "eng"
	if err := d.Admit(); err == nil || !strings.Contains(err.Error(), "admits only vie") {
		t.Fatalf("a non-Vietnamese document was admitted: %v", err)
	}
}

func TestAdmitRejectsUndeterminedLicense(t *testing.T) {
	d := valid()
	d.LicenseClass = LicenseUnknown
	err := d.Admit()
	if err == nil || !strings.Contains(err.Error(), "no determination was made") {
		t.Fatalf("a document with no license determination was admitted: %v", err)
	}

	d = valid()
	d.LicenseEvidence = ""
	if err := d.Admit(); err == nil || !strings.Contains(err.Error(), "license_evidence") {
		t.Fatalf("a license class with no evidence was admitted: %v", err)
	}
}

func TestAdmitRejectsSpansAtStrictRedaction(t *testing.T) {
	// Shipping the offsets of redacted personal data alongside the redacted text
	// re-identifies it, so level 2 carries types and no spans.
	d := valid()
	d.PIILevel = RedactStrict
	d.PIISpans = []PIISpan{{Start: 0, Len: 4, Type: "cccd"}}
	err := d.Admit()
	if err == nil || !strings.Contains(err.Error(), "re-identifies") {
		t.Fatalf("spans at level 2 were admitted: %v", err)
	}
}

func TestAdmitRejectsSpansPastTheEnd(t *testing.T) {
	d := valid()
	d.PIILevel = RedactStandard
	d.PIISpans = []PIISpan{{Start: uint32(len(d.Text)) - 2, Len: 99, Type: "phone"}}
	if err := d.Admit(); err == nil || !strings.Contains(err.Error(), "past the end") {
		t.Fatalf("an out of range span was admitted: %v", err)
	}
}

func TestAdmitRejectsTooShort(t *testing.T) {
	d := valid()
	d.Text = "Xin chào."
	d.DocID = SumString(d.Text)
	d.NChars = uint32(utf8.RuneCountInString(d.Text))
	if err := d.Admit(); err == nil || !strings.Contains(err.Error(), "character floor") {
		t.Fatalf("a document below the floor was admitted: %v", err)
	}
}

func TestAdmitReportsEveryProblemAtOnce(t *testing.T) {
	// Fixing one violation at a time across a 500 million document run is not a
	// plan, so Admit reports all of them.
	d := valid()
	d.SourceLocator = ""
	d.MediaType = ""
	d.PipelineVersion = ""
	err := d.Admit()
	if err == nil {
		t.Fatal("document was admitted")
	}
	for _, want := range []string{"source_locator", "media_type", "pipeline_version"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omitted %s: %v", want, err)
		}
	}
}

func TestPublishable(t *testing.T) {
	cases := []struct {
		class LicenseClass
		level RedactionLevel
		at    RedactionLevel
		want  bool
	}{
		{LicenseOpen, RedactStandard, RedactStandard, true},
		{LicenseOpen, RedactStrict, RedactStandard, true},
		{LicenseOpen, RedactNone, RedactStandard, false},
		{LicensePermissiveAttribution, RedactStandard, RedactStandard, true},
		{LicenseRestricted, RedactStrict, RedactStandard, false},
		{LicenseUnredistributable, RedactStrict, RedactStandard, false},
		{LicenseUnknown, RedactStrict, RedactStandard, false},
	}
	for _, tc := range cases {
		d := valid()
		d.LicenseClass = tc.class
		d.PIILevel = tc.level
		if got := d.Publishable(tc.at); got != tc.want {
			t.Errorf("%s at level %d, publishing at %d: got %v, want %v",
				tc.class, tc.level, tc.at, got, tc.want)
		}
	}
}
