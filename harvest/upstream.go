package harvest

// The per source mappings: which upstream field is which column of ours.
//
// One function per source, each one small and each one dull, because the value
// of this file is that somebody can read a line of it and check it against the
// dataset card. Anything clever here would be a claim about somebody else's data
// that nobody can verify.

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/gao/doc"
)

// HPLT v3.
//
// The richest of the six by a distance. Every record carries the WARC file and
// byte offset it was extracted from, the crawl it belongs to, the language
// identifier's full ranking with probabilities, and the producer's own register
// classification and quality scores. That is most of what the ingest contract
// asks for, which is why HPLT is source zero: it is the one that can be checked
// against its own upstream.
type hplt struct {
	WARCFile   string `json:"f"`
	WARCOffset int64  `json:"o"`

	URL       string `json:"u"`
	MediaType string `json:"c"`
	FetchedAt string `json:"ts"`
	Encoding  string `json:"de"`
	CrawlID   string `json:"crawl_id"`

	Lang []string  `json:"lang"`
	Prob []float32 `json:"prob"`

	Text string `json:"text"`
	ID   string `json:"id"`

	// Filter is HPLT's own keep or drop verdict. gao keeps its own counsel on
	// quality and records theirs.
	Filter string `json:"filter"`

	ClusterSize uint32 `json:"cluster_size"`

	// PII is a list of start and end offsets into HPLT's text. gao does not
	// carry them, for the reason given where they are read.
	PII [][2]int `json:"pii"`

	// Register is the producer's register classifier, one probability per label.
	Register map[string]float32 `json:"web-register"`

	DocScores []float32 `json:"doc_scores"`
}

// registerFloor is how confident the producer's register classifier has to be
// before gao records its label. Below it the classifier is saying it does not
// know, and writing down its best guess would turn that into a fact.
const registerFloor = 0.5

func hpltRow(r row) (*doc.Document, error) {
	var in hplt
	if err := unmarshal(r.Raw, &in); err != nil {
		return nil, err
	}

	d := build(r, in.Text)
	d.URL = in.URL
	d.Host = hostOf(in.URL)
	d.MediaType = in.MediaType

	if in.FetchedAt != "" {
		at, err := time.Parse(time.RFC3339, in.FetchedAt)
		if err != nil {
			return nil, fmt.Errorf("ts %q is not a timestamp: %w", in.FetchedAt, err)
		}
		d.FetchedAt = at.UTC()
	}

	d.Lang, d.LangScore = language(in.Lang, in.Prob, r.Pin.Config)
	d.HPLTBucket = hpltBucket(r.File.Path)
	d.Register = topLabel(in.Register, registerFloor)
	if len(in.DocScores) > 0 {
		d.Heuristics["hplt_doc_score"] = mean(in.DocScores)
	}

	d.UpstreamFields = map[string]string{
		"hplt_id":     in.ID,
		"hplt_filter": in.Filter,
		"warc_file":   in.WARCFile,
		"warc_offset": strconv.FormatInt(in.WARCOffset, 10),
		"crawl_id":    in.CrawlID,
	}
	if in.Encoding != "" {
		// The encoding the page declared. It is the first thing phoi will want
		// when it goes after the legacy Vietnamese fonts, and it is not
		// recoverable once the ingest has thrown the record away.
		d.UpstreamFields["source_encoding"] = in.Encoding
	}
	if in.ClusterSize > 1 {
		// The producer's duplicate cluster, kept out of gao's dedup columns on
		// purpose. Those columns are for what xay finds across all six sources,
		// and putting somebody else's answer in them would make the two
		// indistinguishable in a query.
		d.UpstreamFields["hplt_cluster_size"] = strconv.FormatUint(uint64(in.ClusterSize), 10)
	}
	if n := len(in.PII); n > 0 {
		// The count and not the spans. HPLT's offsets address HPLT's text, and
		// gao's text has been through NFC, so a span copied across would point
		// at the wrong characters in most documents and at the right ones in
		// enough of them to look correct. The count is what survives the
		// normalization honestly, and gao's own detector runs later anyway.
		d.UpstreamFields["hplt_pii_spans"] = strconv.Itoa(n)
	}
	return d, nil
}

// hpltBucket reads the quality bucket out of the file name, which is where HPLT
// puts it: vie_Latn/10_1.jsonl.zst is bucket 10. It is a column rather than a
// path because the path stops existing the moment the shard is written out.
func hpltBucket(p string) uint8 {
	name := path.Base(p)
	digits, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 || n > 255 {
		return 0
	}
	return uint8(n)
}

// MADLAD-400.
//
// The clean split is a JSON object with one field in it, text, and that is the
// whole record. There is no URL, no timestamp, and no media type, because Allen
// AI did not publish them.
//
// The mapping is written anyway, and it is written to be honest rather than to
// pass. What it produces is a document with its text, its identity, its license,
// and a locator that names the file and line it came from, which is everything
// this source actually knows. The ingest contract then rejects it for the
// provenance it does not have, the reject store records that, and the count of
// what a MADLAD shard yields is a measurement rather than an assumption.
func madladRow(r row) (*doc.Document, error) {
	var in struct {
		Text string `json:"text"`
	}
	if err := unmarshal(r.Raw, &in); err != nil {
		return nil, err
	}
	return build(r, in.Text), nil
}

// The media type that none of the Parquet sources publish.
//
// All four have a column for the URL, the fetch date, and the WARC record the
// document came out of, and none of them has one for what was served at that
// URL. The contract requires it, so the choice is to reject four sources and 279
// GB for a field that is knowable with certainty, or to assert it here.
//
// It is asserted here, and it is asserted per source rather than globally,
// because what makes it certain is what the source is. FineWeb2 and GlotCC are
// text extracted from the HTML pages of Common Crawl WARCs, and FinePDFs is text
// extracted from PDFs found in the same crawls, which is the whole reason it
// exists as a separate dataset. Neither is an inference about a particular
// document. The extractor column records which mapping made the assertion and at
// which version, so a document carrying one of these can be found again if the
// reading of a dataset card turns out to be wrong.
const (
	mediaHTML = "text/html"
	mediaPDF  = "application/pdf"
)

// FineWeb2.
//
// Common Crawl HTML, extracted and filtered by Hugging Face, and the second
// largest of the six at 130.1 GB. It carries the columns the contract wants
// except for the media type, and the identifier is the WARC record's own urn,
// which is a stronger row identity than a line number.
type fineweb2 struct {
	Text string `parquet:"text"`
	ID   string `parquet:"id"`

	URL      string `parquet:"url"`
	Date     string `parquet:"date"`
	Dump     string `parquet:"dump"`
	FilePath string `parquet:"file_path"`

	Language      string  `parquet:"language"`
	LanguageScore float64 `parquet:"language_score"`
	Script        string  `parquet:"language_script"`
	TopLangs      string  `parquet:"top_langs"`

	ClusterSize int64 `parquet:"minhash_cluster_size"`
}

func fineweb2Row(r row, in *fineweb2) (*doc.Document, error) {
	d := build(r, in.Text)
	d.URL = in.URL
	d.Host = hostOf(in.URL)
	d.MediaType = mediaHTML

	at, err := when(in.Date)
	if err != nil {
		return nil, err
	}
	d.FetchedAt = at

	d.Lang = code(in.Language)
	d.LangScore = probability(in.LanguageScore)
	d.UpstreamFields = fields(map[string]string{
		"fineweb_id":            in.ID,
		"warc_file":             in.FilePath,
		"crawl_id":              in.Dump,
		"language_script":       in.Script,
		"fineweb_cluster_size":  clusterSize(in.ClusterSize),
		"fineweb_top_languages": topLangs(in.TopLangs),
	})
	return d, nil
}

// FinePDFs.
//
// The one source that is not web HTML, and the one whose extraction gao does not
// have to redo. It is also the most measured: it publishes two language
// identifications, per page and over the whole document, its own token count, and
// the name of the extractor that produced the text, which is either a layout
// model or an OCR model and is worth knowing about a document before training on
// it.
type finepdfs struct {
	Text string `parquet:"text"`
	ID   string `parquet:"id"`

	URL      string `parquet:"url"`
	Date     string `parquet:"date"`
	Dump     string `parquet:"dump"`
	FilePath string `parquet:"file_path"`
	Offset   int64  `parquet:"offset"`

	Language  string  `parquet:"language"`
	FullLID   string  `parquet:"full_doc_lid"`
	FullScore float64 `parquet:"full_doc_lid_score"`
	PageLID   string  `parquet:"page_average_lid"`
	PageScore float64 `parquet:"page_average_lid_score"`

	// Extractor is the producer's, docling or rolmOCR, and it is not gao's. The
	// column of that name holds the mapping that built the document, so this one
	// goes to the upstream fields under a name that says whose it is.
	Extractor string `parquet:"extractor"`

	Tokens      int64 `parquet:"token_count"`
	Truncated   bool  `parquet:"is_truncated"`
	ClusterSize int64 `parquet:"minhash_cluster_size"`
	Duplicates  int64 `parquet:"duplicate_count"`
}

func finepdfsRow(r row, in *finepdfs) (*doc.Document, error) {
	d := build(r, in.Text)
	d.URL = in.URL
	d.Host = hostOf(in.URL)
	d.MediaType = mediaPDF

	at, err := when(in.Date)
	if err != nil {
		return nil, err
	}
	d.FetchedAt = at

	// The whole document identification rather than the per page average. A PDF
	// with a Vietnamese cover page and forty pages of English is not a Vietnamese
	// document, and the per page average is the number that says it is.
	d.Lang = code(in.FullLID)
	d.LangScore = probability(in.FullScore)
	if d.Lang == "" {
		d.Lang = code(in.Language)
	}

	d.UpstreamFields = fields(map[string]string{
		"finepdfs_id":           in.ID,
		"warc_file":             in.FilePath,
		"warc_offset":           strconv.FormatInt(in.Offset, 10),
		"crawl_id":              in.Dump,
		"pdf_extractor":         in.Extractor,
		"finepdfs_tokens":       strconv.FormatInt(in.Tokens, 10),
		"page_average_lid":      code(in.PageLID),
		"finepdfs_duplicates":   clusterSize(in.Duplicates),
		"finepdfs_cluster_size": clusterSize(in.ClusterSize),
	})
	if in.Truncated {
		// A document the producer cut short is not a document that ends where
		// its author stopped, which is the kind of thing a training mixture
		// should be able to ask about.
		d.UpstreamFields["pdf_truncated"] = "true"
	}
	if in.PageScore > 0 {
		d.Heuristics["finepdfs_page_lid_score"] = float32(in.PageScore)
	}
	return d, nil
}

// GlotCC.
//
// Common Crawl again, filtered by a different group with a different language
// identifier, and it keeps the WARC record identifier and the fuzzy hash of its
// own text. The hash is worth carrying: it is a near duplicate signal computed by
// somebody else over the same document, which is exactly the thing to check gao's
// own dedup against rather than to trust in place of it.
type glotcc struct {
	Content  string `parquet:"content"`
	RecordID string `parquet:"warc-record-id"`

	URI  string `parquet:"warc-target-uri"`
	Date string `parquet:"warc-date"`

	Lang       string  `parquet:"identification-language"`
	Prob       float64 `parquet:"identification-prob"`
	Consistent float64 `parquet:"identification-consistency"`
	Script     float64 `parquet:"script-percentage"`

	Length int64  `parquet:"content-length"`
	Sents  int64  `parquet:"num-sents"`
	TLSH   string `parquet:"tlsh"`
}

func glotccRow(r row, in *glotcc) (*doc.Document, error) {
	d := build(r, in.Content)
	d.URL = in.URI
	d.Host = hostOf(in.URI)
	d.MediaType = mediaHTML

	at, err := when(in.Date)
	if err != nil {
		return nil, err
	}
	d.FetchedAt = at

	d.Lang = code(in.Lang)
	d.LangScore = probability(in.Prob)
	d.UpstreamFields = fields(map[string]string{
		"warc_record_id":   in.RecordID,
		"glotcc_tlsh":      in.TLSH,
		"glotcc_sentences": clusterSize(in.Sents),
	})
	if in.Consistent > 0 {
		d.Heuristics["glotcc_lid_consistency"] = float32(in.Consistent)
	}
	if in.Script > 0 {
		d.Heuristics["glotcc_script_share"] = float32(in.Script)
	}
	return d, nil
}

// naiveTime is RFC 3339 with the zone left off, which is not RFC 3339 and is
// what FinePDFs writes for some of its rows.
const naiveTime = "2006-01-02T15:04:05"

// when parses a producer's timestamp.
//
// FinePDFs writes its fetch dates three ways in one file: 2023-01-31T06:34:48Z,
// 2019-01-18T22:02:13+00:00, and 2019-11-18T18:50:20. The first two are the two
// spellings of the same zero offset and the parser takes either. The third has
// no zone at all, and it turned up 416 rows into the first shard.
//
// It is read as UTC. The alternative is to fail the row, and that would throw
// away an unknown share of a 13.0 GB source over a formatting difference rather
// than over anything about the document. Reading it as UTC is safe here for a
// reason specific to this data: the field is the WARC fetch date, WARC records
// carry UTC, and every zoned timestamp in the same file is a zero offset. It is
// not a general rule about naive timestamps and it should not become one.
func when(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if at, err := time.Parse(time.RFC3339, s); err == nil {
		return at.UTC(), nil
	}
	at, err := time.Parse(naiveTime, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q is not a timestamp: %w", s, err)
	}
	return at.UTC(), nil
}

// probability clamps a producer's score into the range the contract allows.
//
// FineWeb2's language scores come out of fastText at 1.0000098943710327, which is
// a float landing slightly above one rather than a claim to be more certain than
// certain. Rejecting 130.1 GB over the eighth decimal place would be reporting a
// rounding artifact as a language finding.
func probability(f float64) float32 {
	switch {
	case f > 1:
		return 1
	case f < 0:
		return 0
	}
	return float32(f)
}

// clusterSize formats a producer's count, and returns the empty string for zero
// so that [fields] drops it. A count nobody published and a count of zero are
// different things and neither is worth a column of its own.
func clusterSize(n int64) string {
	if n <= 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

// topLangs drops the producer's empty ranking, which it writes as an empty JSON
// object rather than as nothing.
func topLangs(s string) string {
	if s == "" || s == "{}" {
		return ""
	}
	return s
}

// fields drops the empty values from an upstream map, so that a column the
// producer left blank is absent rather than present and empty.
func fields(m map[string]string) map[string]string {
	for k, v := range m {
		if v == "" {
			delete(m, k)
		}
	}
	return m
}

// language picks the score for the partition being ingested out of the
// producer's ranking.
//
// A record whose ranking does not contain the language its file is filed under
// is not a record in that language, whatever the file name says. It comes back
// as whatever the producer ranked first, so that the contract rejects it and the
// reject store records what it actually was, which is the only way the rate of
// this ever gets measured.
func language(langs []string, probs []float32, want string) (string, float32) {
	for i, l := range langs {
		if l != want {
			continue
		}
		if i < len(probs) {
			return code(l), probs[i]
		}
		return code(l), 0
	}
	if len(langs) > 0 {
		var score float32
		if len(probs) > 0 {
			score = probs[0]
		}
		return code(langs[0]), score
	}
	return "", 0
}

// code takes the language out of a producer's tag. vie_Latn and vie-Latn are the
// same language written in the same script by two producers who disagree about
// punctuation, and the column holds the language.
func code(tag string) string {
	for i, r := range tag {
		if r == '_' || r == '-' {
			return tag[:i]
		}
	}
	return tag
}

// topLabel returns the highest scoring label in a classifier's output, or the
// empty string when nothing clears the floor. Ties break on the label so that
// two runs over the same input agree.
func topLabel(scores map[string]float32, floor float32) string {
	labels := make([]string, 0, len(scores))
	for l := range scores {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	var best string
	var high float32
	for _, l := range labels {
		if scores[l] > high {
			best, high = l, scores[l]
		}
	}
	if high < floor {
		return ""
	}
	return best
}

// mean is the average of a producer's per segment scores, which is what gets
// stored, because the array itself is one float per paragraph and the corpus is
// half a billion documents long.
func mean(xs []float32) float32 {
	var sum float32
	for _, x := range xs {
		sum += x
	}
	return sum / float32(len(xs))
}
