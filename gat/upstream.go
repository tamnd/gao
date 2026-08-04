package gat

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
