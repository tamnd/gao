package gat

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/text/unicode/norm"

	"github.com/tamnd/gao/doc"
)

// The fixtures are shaped like the records the hosts actually serve, field names
// and all, because the whole value of a mapping is that it agrees with somebody
// else's file and a fixture invented here would agree with itself.

const hpltLine = `{"f":"./segments/1498128329372.0/warc/CC-MAIN-20170629154125-20170629174125-00361.warc.gz",` +
	`"o":244782208,"s":31523,"rs":116583,"u":"http://maithao.vnweblogs.com/","c":"text/html",` +
	`"ts":"2017-06-29T15:42:29Z","de":"utf-8","crawl_id":"CC-MAIN-2017-26",` +
	`"lang":["vie_Latn","ydd_Hebr"],"prob":[0.97,0.01],` +
	`"text":"Triệu Văn Đồi là nhà văn viết cần mẫn và bền bỉ của tỉnh Hòa Bình.\nTruyện ngắn của anh có lối viết khá sắc với nhiều hình ảnh mang ý nghĩa biểu tượng.",` +
	`"html_lang":["en"],"cluster_size":8,"id":"53f8dd156ecc9372c3ac02e8c80575f8","filter":"keep",` +
	`"pii":[[23230,23254]],"doc_scores":[10,9.4],"web-register":{"NA":0.736,"IN":0.307,"OP":0.154}}`

const madladLine = `{"text":"Lực lượng Công an sẽ lập được những chiến công và thành tích mới trong năm nay.\nTheo ông Tạ Văn Thắng, đây là kết quả của nhiều năm."}`

func hpltPin(t *testing.T) (Pinned, File) {
	t.Helper()
	p, ok := Pin(doc.SourceHPLT3)
	if !ok {
		t.Fatal("hplt3 is not pinned")
	}
	return p, File{Path: "vie_Latn/10_1.jsonl.zst"}
}

func madladPin(t *testing.T) (Pinned, File) {
	t.Helper()
	p, ok := Pin(doc.SourceMADLAD400)
	if !ok {
		t.Fatal("madlad400 is not pinned")
	}
	return p, File{Path: "data/vi/vi_clean_0000.jsonl.gz"}
}

// zstdOf and gzipOf compress a fixture the way its host serves it, so that the
// decompression the decoder picks by file extension is exercised rather than
// assumed.
func zstdOf(t *testing.T, s string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func gzipOf(t *testing.T, s string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func decodeAll(t *testing.T, p Pinned, f File, r io.Reader) []*doc.Document {
	t.Helper()
	dec, ok := DecoderFor(p.Source)
	if !ok {
		t.Fatalf("%s has no decoder", p.Source)
	}
	var out []*doc.Document
	if err := dec.Decode(p, f, r, func(d *doc.Document) error {
		out = append(out, d)
		return nil
	}); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return out
}

func TestAnHPLTRecordBecomesADocumentTheContractAdmits(t *testing.T) {
	p, f := hpltPin(t)
	docs := decodeAll(t, p, f, zstdOf(t, hpltLine+"\n"))
	if len(docs) != 1 {
		t.Fatalf("decoded %d documents", len(docs))
	}
	d := docs[0]
	if err := d.Admit(); err != nil {
		t.Fatalf("the document does not satisfy the ingest contract: %v", err)
	}

	for _, tc := range []struct{ name, got, want string }{
		{"source", string(d.Source), "hplt3"},
		{"source_locator", d.SourceLocator, "vie_Latn/10_1.jsonl.zst:1"},
		{"url", d.URL, "http://maithao.vnweblogs.com/"},
		{"host", d.Host, "maithao.vnweblogs.com"},
		{"media_type", d.MediaType, "text/html"},
		{"fetched_at", d.FetchedAt.Format("2006-01-02T15:04:05Z"), "2017-06-29T15:42:29Z"},
		{"extractor", d.Extractor, Extractor},
		{"pipeline_version", d.PipelineVersion, PipelineVersion},
		{"lang", d.Lang, "vie"},
		{"diacritics", d.Diacritics, "present"},
		{"register", d.Register, "NA"},
		{"license_evidence", d.LicenseEvidence, "CC0 on the release, and the release is what gao ingests"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s is %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if d.LangScore != 0.97 {
		t.Errorf("lang_score is %v, want the probability against the partition being ingested", d.LangScore)
	}
	if d.LicenseClass != doc.LicenseOpen {
		t.Errorf("license_class is %s", d.LicenseClass)
	}
	// The bucket is in the file name and nowhere else, and the file name stops
	// existing the moment the shard is written out.
	if d.HPLTBucket != 10 {
		t.Errorf("hplt_bucket is %d, want 10", d.HPLTBucket)
	}
	if d.FetchedAt.Location() != time.UTC {
		t.Errorf("fetched_at is in %s", d.FetchedAt.Location())
	}
}

// The producer's own metadata is kept because the alternative to keeping it is
// re-ingesting 234.5 GB the first time somebody asks a question about it.
func TestAnHPLTRecordKeepsWhatHPLTKnowsThatWeDoNot(t *testing.T) {
	p, f := hpltPin(t)
	d := decodeAll(t, p, f, zstdOf(t, hpltLine+"\n"))[0]

	for k, want := range map[string]string{
		"hplt_id":           "53f8dd156ecc9372c3ac02e8c80575f8",
		"hplt_filter":       "keep",
		"warc_file":         "./segments/1498128329372.0/warc/CC-MAIN-20170629154125-20170629174125-00361.warc.gz",
		"warc_offset":       "244782208",
		"crawl_id":          "CC-MAIN-2017-26",
		"source_encoding":   "utf-8",
		"hplt_cluster_size": "8",
		"hplt_pii_spans":    "1",
	} {
		if got := d.UpstreamFields[k]; got != want {
			t.Errorf("upstream_fields[%q] is %q, want %q", k, got, want)
		}
	}

	// The producer's duplicate cluster stays out of gao's dedup columns. Those
	// are for what xay finds across all six sources, and a query cannot tell the
	// two apart once they share a column.
	if !d.DupCluster.IsZero() || d.DupClusterSize != 0 {
		t.Error("the producer's cluster was written into gao's dedup columns")
	}
	// The producer's PII offsets address the producer's text, and gao's text has
	// been through NFC, so only the count survives the normalization honestly.
	if len(d.PIISpans) != 0 {
		t.Error("HPLT's PII offsets were copied onto text they do not address")
	}
	if d.Heuristics["hplt_doc_score"] != 9.7 {
		t.Errorf("hplt_doc_score is %v, want the mean of the per segment scores", d.Heuristics["hplt_doc_score"])
	}
}

// MADLAD-400's clean split is a text field and nothing else. This is the test
// that says so, and it is a test rather than a note because the consequence is
// 95.3 GB of download that admits no documents.
func TestAMADLADRecordCannotSatisfyTheContract(t *testing.T) {
	p, f := madladPin(t)
	docs := decodeAll(t, p, f, gzipOf(t, madladLine+"\n"))
	if len(docs) != 1 {
		t.Fatalf("decoded %d documents", len(docs))
	}
	d := docs[0]

	err := d.Admit()
	if err == nil {
		t.Fatal("a document with no URL, no timestamp, and no media type was admitted")
	}
	for _, want := range []string{"url is unset", "fetched_at is unset", "media_type is unset"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the contract does not report %q: %v", want, err)
		}
	}

	// What it does carry is worth carrying, because it is what a decision about
	// this source will be made from.
	if d.SourceLocator != "data/vi/vi_clean_0000.jsonl.gz:1" {
		t.Errorf("source_locator is %q, so the row cannot be found again", d.SourceLocator)
	}
	if d.DocID.IsZero() || d.RawID.IsZero() {
		t.Error("the document has no identity, so its rejection cannot be traced")
	}
	if d.LicenseClass != doc.LicensePermissiveAttribution {
		t.Errorf("license_class is %s", d.LicenseClass)
	}
}

// raw_id is what links a row in the store back to the bytes the host served, so
// it has to be a hash of those bytes and not of anything derived from them.
func TestTheIdentityColumnsHashWhatTheySay(t *testing.T) {
	p, f := hpltPin(t)
	d := decodeAll(t, p, f, zstdOf(t, hpltLine+"\n"))[0]

	if want := doc.Sum([]byte(hpltLine)); d.RawID != want {
		t.Errorf("raw_id is %s, want blake3 of the record as it arrived (%s)", d.RawID, want)
	}
	if want := doc.SumString(d.Text); d.DocID != want {
		t.Errorf("doc_id is %s, want blake3 of the normalized text (%s)", d.DocID, want)
	}
}

// Two encodings of the same Vietnamese string are the same document, and a
// corpus that hashes them separately deduplicates neither.
func TestTheSameTextInTwoEncodingsIsOneDocument(t *testing.T) {
	const composed = "Tiếng Việt là ngôn ngữ của người Việt Nam và là ngôn ngữ chính thức."
	decomposed := norm.NFD.String(composed)
	if composed == decomposed {
		t.Fatal("the fixture does not decompose, so this test proves nothing")
	}

	p, f := madladPin(t)
	one := decodeAll(t, p, f, gzipOf(t, jsonText(composed)+"\n"))[0]
	two := decodeAll(t, p, f, gzipOf(t, jsonText(decomposed)+"\n"))[0]

	if one.DocID != two.DocID {
		t.Errorf("the same text in two encodings hashed to %s and %s", one.DocID, two.DocID)
	}
	if one.Text != composed {
		t.Error("the text was not brought to NFC")
	}
	if one.NChars != two.NChars {
		t.Errorf("n_chars is %d and %d for the same text", one.NChars, two.NChars)
	}
}

// jsonText wraps a string as a MADLAD record.
func jsonText(s string) string {
	b, err := json.Marshal(struct {
		Text string `json:"text"`
	}{s})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestTheDecoderReadsEveryRecordInFileOrder(t *testing.T) {
	p, f := madladPin(t)
	lines := make([]string, 0, 3)
	for _, s := range []string{"một", "hai", "ba"} {
		lines = append(lines, jsonText(strings.Repeat(s+" chuyện dài về con sông quê hương ", 4)))
	}
	// A blank line between records is formatting and not corruption, and a
	// decoder that treats it as a record would emit an empty document.
	docs := decodeAll(t, p, f, gzipOf(t, strings.Join(lines, "\n")+"\n\n"))
	if len(docs) != 3 {
		t.Fatalf("decoded %d documents from three records", len(docs))
	}
	for i, d := range docs {
		want := f.Path + ":" + strconv.Itoa(i+1)
		if d.SourceLocator != want {
			t.Errorf("document %d is located at %q, want %q", i, d.SourceLocator, want)
		}
	}
}

// A bad row fails the file. Skipping is how a corpus loses three percent of a
// source without anybody finding out.
func TestARecordThatDoesNotParseStopsTheFile(t *testing.T) {
	p, f := madladPin(t)
	dec, _ := DecoderFor(p.Source)

	var seen int
	err := dec.Decode(p, f, gzipOf(t, madladLine+"\nthis is not a record\n"+madladLine+"\n"),
		func(*doc.Document) error { seen++; return nil })
	if !errors.Is(err, ErrBadRow) {
		t.Fatalf("Decode returned %v, want ErrBadRow", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("the error does not say which line: %v", err)
	}
	if seen != 1 {
		t.Errorf("%d documents were emitted before the bad row", seen)
	}
}

// The emit error comes back unchanged, because it is how a caller stops an
// ingest from inside the sink and a wrapped context error stops matching.
func TestAnEmitErrorComesBackAsItself(t *testing.T) {
	p, f := madladPin(t)
	dec, _ := DecoderFor(p.Source)

	stop := errors.New("the caller has had enough")
	err := dec.Decode(p, f, gzipOf(t, madladLine+"\n"+madladLine+"\n"),
		func(*doc.Document) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("Decode returned %v, want the emit error", err)
	}
}

func TestADecoderRefusesAFileItCannotOpen(t *testing.T) {
	p, f := hpltPin(t)
	dec, _ := DecoderFor(p.Source)

	// zstd framing, promised by the extension and not delivered.
	err := dec.Decode(p, f, strings.NewReader("plain text pretending to be a shard"),
		func(*doc.Document) error { return nil })
	if err == nil {
		t.Fatal("a file that is not compressed the way its name says was read anyway")
	}

	err = dec.Decode(p, File{Path: "vie_Latn/10_1.jsonl.br"}, strings.NewReader(""),
		func(*doc.Document) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "compressed") {
		t.Errorf("an unknown compression reads as %v", err)
	}
}

func TestOnlyTheSourcesWithAMappingHaveADecoder(t *testing.T) {
	for _, s := range []doc.Source{doc.SourceHPLT3, doc.SourceMADLAD400} {
		if _, ok := DecoderFor(s); !ok {
			t.Errorf("%s has no decoder", s)
		}
	}
	// The four Parquet sources are not decodable yet and the command has to be
	// able to say so before it starts a 279 GB download that ends in an error.
	ok, missing := Decodable(Sources())
	if ok {
		t.Fatal("Decodable reports every source, and four of them ship Parquet")
	}
	want := map[doc.Source]bool{
		doc.SourceFineWeb2: true, doc.SourceFinePDFs: true,
		doc.SourceCulturaX: true, doc.SourceGlotCC: true,
	}
	if len(missing) != len(want) {
		t.Fatalf("Decodable reports %v", missing)
	}
	for _, s := range missing {
		if !want[s] {
			t.Errorf("Decodable reports %s as missing a decoder", s)
		}
	}

	if ok, missing := Decodable([]Pinned{mustPin(t, doc.SourceHPLT3)}); !ok {
		t.Errorf("Decodable reports %v for a source that has one", missing)
	}
}

func mustPin(t *testing.T, s doc.Source) Pinned {
	t.Helper()
	p, ok := Pin(s)
	if !ok {
		t.Fatalf("%s is not pinned", s)
	}
	return p
}

// Vietnamese without tone marks is a real register and a different distribution,
// so it goes in a column rather than into the corpus unlabeled.
func TestTheDiacriticVerdictSeparatesTheTwoRegisters(t *testing.T) {
	const marked = "Hôm nay trời rất đẹp và chúng tôi đã đi chơi ở bờ sông cùng với gia đình.\n" +
		"Buổi chiều chúng tôi về nhà ăn cơm và xem phim với nhau rất vui vẻ.\n"
	const bare = "Hom nay troi rat dep va chung toi da di choi o bo song cung voi gia dinh.\n" +
		"Buoi chieu chung toi ve nha an com va xem phim voi nhau rat vui ve.\n"

	for _, tc := range []struct {
		name, text, want string
	}{
		{"written with tone marks", marked, "present"},
		{"typed without them", bare, "absent"},
		{"an article and the comments under it", marked + bare + bare, "mixed"},
		// Nothing long enough to judge a line by, so the document as a whole is
		// the only evidence there is.
		{"a headline", "Đã có kết quả\n", "present"},
		{"a short bare headline", "Da co ket qua\n", "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ratio := diacritics(tc.text)
			if got != tc.want {
				t.Errorf("diacritics = %q, want %q (ratio %v)", got, tc.want, ratio)
			}
			if ratio < 0 || ratio > 1 {
				t.Errorf("the ratio is %v", ratio)
			}
		})
	}
}

// The ratio is stored as well as the verdict, because the reject store keeps raw
// values and a threshold nobody can recompute is a threshold nobody can retune.
func TestTheDiacriticRatioIsStoredAndNotJustTheVerdict(t *testing.T) {
	p, f := hpltPin(t)
	d := decodeAll(t, p, f, zstdOf(t, hpltLine+"\n"))[0]

	ratio, ok := d.Heuristics["diacritic_ratio"]
	if !ok {
		t.Fatal("the ratio the verdict came from was not kept")
	}
	// Vietnamese prose runs between a quarter and two fifths of its letters
	// marked, which is what makes the floor safe.
	if ratio < 0.2 || ratio > 0.45 {
		t.Errorf("diacritic_ratio is %v for ordinary Vietnamese prose", ratio)
	}
}

// Đ is a modified letter that Unicode does not decompose, so the rule that
// covers the rest of the alphabet does not reach it and it is named separately.
func TestTheLetterUnicodeDoesNotDecomposeIsStillMarked(t *testing.T) {
	for _, tc := range []struct {
		in              string
		letters, marked int
	}{
		{"đo", 2, 1},
		{"Đo", 2, 1},
		{"do", 2, 0},
		{"á", 1, 1},
		{"ặ", 1, 1}, // two combining marks on one letter, counted once
		{"a1b", 2, 0},
	} {
		letters, marked := marks(tc.in)
		if letters != tc.letters || marked != tc.marked {
			t.Errorf("marks(%q) = %d, %d, want %d, %d", tc.in, letters, marked, tc.letters, tc.marked)
		}
	}
}

// A syllable count is a count and not one of the estimates in doc/units.go, and
// it is what a per source number in a release note is allowed to be built from.
func TestSyllablesAreCountedAndNotEstimated(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint32
	}{
		{"Tiếng Việt", 2},
		{"Hà Nội, Việt Nam.", 4},
		{"", 0},
		{"123 456", 0},
		{"COVID-19", 1},
		{"một\nhai\tba", 3},
	} {
		if got := syllables(tc.in); got != tc.want {
			t.Errorf("syllables(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
