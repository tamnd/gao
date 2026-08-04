package gat

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"

	"github.com/tamnd/gao/doc"
)

// parquetOf writes rows to an in memory Parquet file, so that the tests read a
// real file through the real reader rather than a mock of one. The mapping is
// most of what is being tested and the format is the rest of it.
func parquetOf[T any](t *testing.T, rows []T) ([]byte, int64) {
	t.Helper()
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[T](&buf)
	if _, err := w.Write(rows); err != nil {
		t.Fatalf("writing the test file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the test file: %v", err)
	}
	return buf.Bytes(), int64(buf.Len())
}

// decodeAllAt runs a source's random decoder over an in memory file and returns
// what came out.
func decodeAllAt[T any](t *testing.T, source doc.Source, rows []T) []*doc.Document {
	t.Helper()
	out, err := tryDecodeAt(t, source, rows)
	if err != nil {
		t.Fatalf("DecodeAt: %v", err)
	}
	return out
}

func tryDecodeAt[T any](t *testing.T, source doc.Source, rows []T) ([]*doc.Document, error) {
	t.Helper()
	dec, ok := RandomDecoderFor(source)
	if !ok {
		t.Fatalf("%s has no random decoder", source)
	}
	p := mustPin(t, source)
	f := p.Files[0]

	b, size := parquetOf(t, rows)
	var out []*doc.Document
	err := dec.DecodeAt(p, f, bytes.NewReader(b), size, func(d *doc.Document) error {
		out = append(out, d)
		return nil
	})
	return out, err
}

// A real FineWeb2 row, copied field for field off the first row of
// data/vie_Latn/train/000_00000.parquet.
func fineweb2Fixture() fineweb2 {
	return fineweb2{
		Text:          "Con cò bay lả bay la, bay từ cửa phủ bay ra cánh đồng.",
		ID:            "<urn:uuid:6f2b9e60-4f6a-4a1e-8e3c-1a9d5c7b2f10>",
		URL:           "https://vnexpress.net/tin-tuc/thoi-su/ca-dao-2831.html",
		Date:          "2013-05-19T09:25:23Z",
		Dump:          "CC-MAIN-2013-20",
		FilePath:      "s3://commoncrawl/crawl-data/CC-MAIN-2013-20/segments/1368696381249/warc/CC-MAIN-20130516092621-00000-ip-10-60-113-184.ec2.internal.warc.gz",
		Language:      "vie",
		LanguageScore: 0.9714,
		Script:        "Latn",
		TopLangs:      "{}",
		ClusterSize:   3,
	}
}

func TestAFineWeb2RowBecomesADocumentTheContractAdmits(t *testing.T) {
	in := fineweb2Fixture()
	d := decodeAllAt(t, doc.SourceFineWeb2, []fineweb2{in})[0]

	if err := d.Admit(); err != nil {
		t.Fatalf("a full FineWeb2 row was turned away: %v", err)
	}
	for _, tc := range []struct{ column, got, want string }{
		{"text", d.Text, in.Text},
		{"url", d.URL, in.URL},
		{"host", d.Host, "vnexpress.net"},
		{"media_type", d.MediaType, "text/html"},
		{"lang", d.Lang, "vie"},
		{"fetched_at", d.FetchedAt.Format("2006-01-02T15:04:05Z"), in.Date},
		{"source", string(d.Source), string(doc.SourceFineWeb2)},
		{"extractor", d.Extractor, Extractor},
	} {
		if tc.got != tc.want {
			t.Errorf("%s is %q, want %q", tc.column, tc.got, tc.want)
		}
	}
	if d.LangScore != 0.9714 {
		t.Errorf("lang_score is %v, want the producer's own score", d.LangScore)
	}
	for k, want := range map[string]string{
		"fineweb_id":           in.ID,
		"warc_file":            in.FilePath,
		"crawl_id":             in.Dump,
		"language_script":      "Latn",
		"fineweb_cluster_size": "3",
	} {
		if got := d.UpstreamFields[k]; got != want {
			t.Errorf("upstream_fields[%q] is %q, want %q", k, got, want)
		}
	}
	// The producer writes an empty ranking as an empty JSON object, and carrying
	// that across would put a column of "{}" in the corpus.
	if _, ok := d.UpstreamFields["fineweb_top_languages"]; ok {
		t.Error("an empty top_langs was carried across as a field")
	}
}

// FineWeb2's fastText scores come back at 1.0000098943710327, which the contract
// rejects and which is a float landing above one rather than a claim to be more
// certain than certain. Every row in the source would fail on the eighth decimal
// place.
func TestAScoreJustAboveOneIsAFloatAndNotAClaim(t *testing.T) {
	in := fineweb2Fixture()
	in.LanguageScore = 1.0000098943710327

	d := decodeAllAt(t, doc.SourceFineWeb2, []fineweb2{in})[0]
	if d.LangScore != 1 {
		t.Errorf("lang_score is %v, want 1", d.LangScore)
	}
	if err := d.Admit(); err != nil {
		t.Fatalf("a row scored just above one was turned away: %v", err)
	}
}

func TestTheScoreClampHoldsAtBothEnds(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want float32
	}{
		{0.5, 0.5},
		{1, 1},
		{1.0000098943710327, 1},
		{2, 1},
		{0, 0},
		{-0.1, 0},
	} {
		if got := probability(tc.in); got != tc.want {
			t.Errorf("probability(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func finepdfsFixture() finepdfs {
	return finepdfs{
		Text:      "Báo cáo thường niên năm 2022 của Ngân hàng Nhà nước Việt Nam.",
		ID:        "<urn:uuid:2c1f8a44-9b3d-4e21-9a77-5d0e6f3b8c12>",
		URL:       "https://sbv.gov.vn/bao-cao-2022.pdf",
		Date:      "2023-01-31T06:34:48+00:00",
		Dump:      "CC-MAIN-2023-06",
		FilePath:  "s3://commoncrawl/crawl-data/CC-MAIN-2023-06/segments/1674764499790/warc/CC-MAIN-20230130201044-00000.warc.gz",
		Offset:    908372611,
		Language:  "vie_Latn",
		FullLID:   "vie_Latn",
		FullScore: 0.9931,
		PageLID:   "vie_Latn",
		PageScore: 0.9812,
		Extractor: "docling",
		Tokens:    1843,
		Truncated: false,

		ClusterSize: 1,
		Duplicates:  0,
	}
}

func TestAFinePDFsRowBecomesADocumentTheContractAdmits(t *testing.T) {
	in := finepdfsFixture()
	d := decodeAllAt(t, doc.SourceFinePDFs, []finepdfs{in})[0]

	if err := d.Admit(); err != nil {
		t.Fatalf("a full FinePDFs row was turned away: %v", err)
	}
	if d.MediaType != "application/pdf" {
		t.Errorf("media_type is %q, and FinePDFs is PDFs", d.MediaType)
	}
	if d.Lang != "vie" || d.LangScore != 0.9931 {
		t.Errorf("the language is %q at %v, want the whole document identification", d.Lang, d.LangScore)
	}
	// The offset into the WARC is the difference between a document that can be
	// traced back to the crawl and one that can only be traced to a file.
	if got := d.UpstreamFields["warc_offset"]; got != "908372611" {
		t.Errorf("warc_offset is %q", got)
	}
	// A timestamp written with an explicit zero offset rather than with Z, which
	// FinePDFs uses for some of its rows and not others.
	if got := d.FetchedAt.Format("2006-01-02T15:04:05Z"); got != "2023-01-31T06:34:48Z" {
		t.Errorf("fetched_at is %q", got)
	}
}

// The producer has a column called extractor and so does gao, and they mean
// different things. Ours says which mapping built the document, theirs says
// which model turned a PDF into text, and a document that answered the first
// question with the second would be untraceable.
func TestTheProducersExtractorDoesNotOverwriteOurs(t *testing.T) {
	for _, name := range []string{"docling", "rolmOCR"} {
		in := finepdfsFixture()
		in.Extractor = name

		d := decodeAllAt(t, doc.SourceFinePDFs, []finepdfs{in})[0]
		if d.Extractor != Extractor {
			t.Errorf("extractor is %q, want %q", d.Extractor, Extractor)
		}
		if got := d.UpstreamFields["pdf_extractor"]; got != name {
			t.Errorf("pdf_extractor is %q, want %q", got, name)
		}
	}
}

// FinePDFs publishes a token count and gao has a column of that name, and they
// are counts by two different tokenizers. Writing theirs into ours would make a
// mixture built on token budgets wrong by whatever the two disagree by.
func TestTheProducersTokenCountIsNotOurs(t *testing.T) {
	in := finepdfsFixture()
	d := decodeAllAt(t, doc.SourceFinePDFs, []finepdfs{in})[0]

	if d.NTokens != 0 {
		t.Errorf("n_tokens is %d, and nothing in gao has tokenized this document", d.NTokens)
	}
	if got := d.UpstreamFields["finepdfs_tokens"]; got != "1843" {
		t.Errorf("finepdfs_tokens is %q, want the producer's count kept as theirs", got)
	}
}

// A document the producer cut short does not end where its author stopped, and a
// mixture should be able to ask about that.
func TestATruncatedPDFSaysSo(t *testing.T) {
	in := finepdfsFixture()
	in.Truncated = true

	d := decodeAllAt(t, doc.SourceFinePDFs, []finepdfs{in})[0]
	if got := d.UpstreamFields["pdf_truncated"]; got != "true" {
		t.Errorf("pdf_truncated is %q", got)
	}

	whole := decodeAllAt(t, doc.SourceFinePDFs, []finepdfs{finepdfsFixture()})[0]
	if _, ok := whole.UpstreamFields["pdf_truncated"]; ok {
		t.Error("a whole document was marked as truncated")
	}
}

func glotccFixture() glotcc {
	return glotcc{
		Content:    "Hà Nội mùa này vắng những cơn mưa, cái rét đầu đông khăn em bay hiu hiu gió lạnh.",
		RecordID:   "<urn:uuid:8d4a1c33-7e52-4f19-b6c0-3a8e2d5f9017>",
		URI:        "https://vietnamnet.vn/ha-noi-mua-thu-2148372.html",
		Date:       "2024-03-01T04:32:12Z",
		Lang:       "vie-Latn",
		Prob:       0.9967,
		Consistent: 0.9821,
		Script:     0.9994,
		Length:     80,
		Sents:      2,
		TLSH:       "T1A3B2C4D5E6F70819",
	}
}

func TestAGlotCCRowBecomesADocumentTheContractAdmits(t *testing.T) {
	in := glotccFixture()
	d := decodeAllAt(t, doc.SourceGlotCC, []glotcc{in})[0]

	if err := d.Admit(); err != nil {
		t.Fatalf("a full GlotCC row was turned away: %v", err)
	}
	if d.Text != in.Content {
		t.Errorf("the text is %q", d.Text)
	}
	if d.MediaType != "text/html" {
		t.Errorf("media_type is %q, and GlotCC is Common Crawl HTML", d.MediaType)
	}
	// vie-Latn and vie_Latn are the same language written in the same script by
	// two producers who disagree about punctuation.
	if d.Lang != "vie" {
		t.Errorf("lang is %q", d.Lang)
	}
	// The producer's near duplicate hash, which is the thing to check gao's own
	// dedup against rather than to trust in place of it.
	if got := d.UpstreamFields["glotcc_tlsh"]; got != in.TLSH {
		t.Errorf("glotcc_tlsh is %q", got)
	}
	for _, k := range []string{"glotcc_lid_consistency", "glotcc_script_share"} {
		if _, ok := d.Heuristics[k]; !ok {
			t.Errorf("%s was not kept", k)
		}
	}
}

// A Parquet row has no bytes of its own, so raw_id is a hash of the fields as
// gao read them. Two rows that are the same in every column gao reads are the
// same row as far as this identity goes, and one that differs anywhere is not.
func TestRawIDIdentifiesTheRowByWhatWasReadFromIt(t *testing.T) {
	same := fineweb2Fixture()
	other := fineweb2Fixture()
	other.Text += " Thêm một câu nữa."

	docs := decodeAllAt(t, doc.SourceFineWeb2, []fineweb2{same, same, other})
	if len(docs) != 3 {
		t.Fatalf("decoded %d rows, want 3", len(docs))
	}
	if docs[0].RawID != docs[1].RawID {
		t.Error("two identical rows hash differently")
	}
	if docs[0].RawID == docs[2].RawID {
		t.Error("two different rows hash the same")
	}
	if docs[0].DocID == docs[2].DocID {
		t.Error("two different texts have the same doc_id")
	}
}

// Row order is file order, and the locator has to name a row somebody can go
// back and look at.
func TestTheRowsComeBackInFileOrderWithTheirLocators(t *testing.T) {
	rows := make([]glotcc, 5)
	for i := range rows {
		rows[i] = glotccFixture()
		rows[i].Content = "Câu số " + string(rune('1'+i)) + " của bài viết này."
	}

	p := mustPin(t, doc.SourceGlotCC)
	docs := decodeAllAt(t, doc.SourceGlotCC, rows)
	for i, d := range docs {
		want := p.Files[0].Path + ":" + string(rune('1'+i))
		if d.SourceLocator != want {
			t.Errorf("row %d has locator %q, want %q", i+1, d.SourceLocator, want)
		}
		if d.Text != rows[i].Content {
			t.Errorf("row %d is %q, out of order", i+1, d.Text)
		}
	}
}

// A row that does not map is a fault in this mapping or in what the host
// published, and both are worth stopping a file for.
func TestARowThatDoesNotMapStopsTheFileAndNamesIt(t *testing.T) {
	rows := []fineweb2{fineweb2Fixture(), fineweb2Fixture(), fineweb2Fixture()}
	rows[1].Date = "19/05/2013"

	out, err := tryDecodeAt(t, doc.SourceFineWeb2, rows)
	if !errors.Is(err, ErrBadRow) {
		t.Fatalf("DecodeAt returned %v, want ErrBadRow", err)
	}
	if !strings.Contains(err.Error(), "row 2") {
		t.Errorf("the error does not say which row: %v", err)
	}
	if !strings.Contains(err.Error(), "is not a timestamp") {
		t.Errorf("the error does not say what was wrong: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("%d rows came out before the bad one, want 1", len(out))
	}
}

// The emit error is what a sink stops an ingest with, so it has to come back
// unchanged rather than wrapped in a complaint about the file.
func TestAnErrorFromTheSinkComesBackUnchanged(t *testing.T) {
	stop := errors.New("the sink has had enough")
	dec, _ := RandomDecoderFor(doc.SourceGlotCC)
	p := mustPin(t, doc.SourceGlotCC)

	b, size := parquetOf(t, []glotcc{glotccFixture(), glotccFixture()})
	err := dec.DecodeAt(p, p.Files[0], bytes.NewReader(b), size, func(*doc.Document) error {
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("DecodeAt returned %v, want the sink's own error", err)
	}
}

// The production structs cover the columns gao wants and not the LIST columns
// FinePDFs and GlotCC also publish, so reading a subset of a wider file is the
// ordinary case rather than an unusual one.
func TestAFileWithMoreColumnsThanTheMappingReadsIsFine(t *testing.T) {
	type wider struct {
		Content    string    `parquet:"content"`
		RecordID   string    `parquet:"warc-record-id"`
		URI        string    `parquet:"warc-target-uri"`
		Date       string    `parquet:"warc-date"`
		Lang       string    `parquet:"identification-language"`
		Prob       float64   `parquet:"identification-prob"`
		Consistent float64   `parquet:"identification-consistency"`
		Script     float64   `parquet:"script-percentage"`
		Length     int64     `parquet:"content-length"`
		Sents      int64     `parquet:"num-sents"`
		TLSH       string    `parquet:"tlsh"`
		Categories []string  `parquet:"categories"`
		Warnings   []string  `parquet:"quality-warnings"`
		Extra      []float64 `parquet:"page-scores"`
	}
	in := glotccFixture()
	b, size := parquetOf(t, []wider{{
		Content: in.Content, RecordID: in.RecordID, URI: in.URI, Date: in.Date,
		Lang: in.Lang, Prob: in.Prob, Consistent: in.Consistent, Script: in.Script,
		Length: in.Length, Sents: in.Sents, TLSH: in.TLSH,
		Categories: []string{"news"}, Warnings: []string{"short_sentences"},
		Extra: []float64{0.1, 0.2},
	}})

	dec, _ := RandomDecoderFor(doc.SourceGlotCC)
	p := mustPin(t, doc.SourceGlotCC)

	var out []*doc.Document
	err := dec.DecodeAt(p, p.Files[0], bytes.NewReader(b), size, func(d *doc.Document) error {
		out = append(out, d)
		return nil
	})
	if err != nil {
		t.Fatalf("DecodeAt over a wider file: %v", err)
	}
	if len(out) != 1 || out[0].Text != in.Content {
		t.Fatalf("decoded %d rows", len(out))
	}
	if err := out[0].Admit(); err != nil {
		t.Errorf("the row was turned away: %v", err)
	}
}

// Everything after the decoder is the same as a streamed source. The contract
// does not get a second set of rules for the sources that could not be streamed.
func TestARandomlyReadFileGoesThroughTheSameContract(t *testing.T) {
	rows := []fineweb2{fineweb2Fixture(), fineweb2Fixture(), fineweb2Fixture()}
	// A row whose own identifier called it something else. It is not relabeled
	// and it is not dropped silently, it is counted as a rejection.
	rows[2].Language = "eng"

	b, size := parquetOf(t, rows)
	p := mustPin(t, doc.SourceFineWeb2)

	var kept int
	docs := &Docs{Emit: func(*doc.Document) error { kept++; return nil }}
	n, err := docs.ConsumeAt(t.Context(), p, p.Files[0], bytes.NewReader(b), size)
	if err != nil {
		t.Fatalf("ConsumeAt: %v", err)
	}
	if n != 2 || kept != 2 {
		t.Errorf("ConsumeAt admitted %d and emitted %d, want 2 and 2", n, kept)
	}
	if docs.Rejected() != 1 {
		t.Errorf("%d rows were turned away, want 1", docs.Rejected())
	}
}

// The sixth source. It is gated, its terms have not been granted, and nobody has
// read a byte of it, so the honest thing is to refuse it by name rather than to
// ship a mapping written from the dataset card.
func TestCulturaXIsRefusedByNameRatherThanGuessedAt(t *testing.T) {
	if _, ok := RandomDecoderFor(doc.SourceCulturaX); ok {
		t.Error("CulturaX has a decoder, and nobody has read the file it maps")
	}
	docs := &Docs{}
	p := mustPin(t, doc.SourceCulturaX)

	_, err := docs.ConsumeAt(t.Context(), p, p.Files[0], strings.NewReader(""), 0)
	if !errors.Is(err, ErrNoDecoder) {
		t.Fatalf("ConsumeAt returned %v, want ErrNoDecoder", err)
	}
	if !strings.Contains(err.Error(), string(doc.SourceCulturaX)) {
		t.Errorf("the error does not name the source: %v", err)
	}
}

// A file that is not Parquet fails as that, naming the file and the source,
// rather than as a puzzle about a row that does not exist.
func TestSomethingThatIsNotParquetFailsSayingSo(t *testing.T) {
	dec, _ := RandomDecoderFor(doc.SourceFineWeb2)
	p := mustPin(t, doc.SourceFineWeb2)
	junk := []byte(strings.Repeat("khong phai parquet", 100))

	err := dec.DecodeAt(p, p.Files[0], bytes.NewReader(junk), int64(len(junk)),
		func(*doc.Document) error { return nil })
	if err == nil {
		t.Fatal("a file that is not Parquet decoded")
	}
	if !strings.Contains(err.Error(), p.Files[0].Path) {
		t.Errorf("the error does not name the file: %v", err)
	}
	if !strings.Contains(err.Error(), string(doc.SourceFineWeb2)) {
		t.Errorf("the error does not name the source: %v", err)
	}
}

// The whole path end to end: a real Parquet file on a host that answers range
// requests, read out of order, decoded, and recorded. Everything above this test
// is a piece of it, and the pieces passing separately is not the same as a file
// going through.
func TestAParquetFileIsReadByRangeDecodedAndRecorded(t *testing.T) {
	rows := make([]fineweb2, 500)
	for i := range rows {
		rows[i] = fineweb2Fixture()
		rows[i].Text = strings.Repeat("Con cò bay lả bay la. ", 20+i%40)
	}
	b, size := parquetOf(t, rows)

	h := &ranger{content: b}
	s := httptest.NewUnstartedServer(h)
	s.Config.ErrorLog = quietLog()
	s.Start()
	t.Cleanup(s.Close)

	p := mustPin(t, doc.SourceFineWeb2)
	p.Origin, p.Repo = Direct, s.URL
	f := File{Path: p.Files[0].Path, Bytes: size}

	l, _ := openLedger(t)
	var kept int
	docs := &Docs{Emit: func(*doc.Document) error { kept++; return nil }}

	var reports []Report
	in := &Ingest{
		Fetcher:  &Fetcher{Client: s.Client()},
		Ledger:   l,
		Sink:     docs,
		Box:      "server1",
		Progress: func(r Report) { reports = append(reports, r) },
	}
	if _, err := in.Run(t.Context(), []Work{{Pin: p, File: f}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if kept != len(rows) {
		t.Errorf("%d of %d rows came through", kept, len(rows))
	}

	if len(reports) != 1 {
		t.Fatalf("progress was reported %d times", len(reports))
	}
	rep := reports[0]
	if rep.Access != Random {
		t.Errorf("the file was read %s", rep.Access)
	}
	if rep.Digest != "" {
		t.Errorf("a file read in pieces reported digest %q", rep.Digest)
	}
	if rep.Requests == 0 || rep.Moved == 0 {
		t.Errorf("the read cost %d requests and %d bytes", rep.Requests, rep.Moved)
	}

	entries := l.Entries()
	if len(entries) != 1 {
		t.Fatalf("the ledger has %d entries", len(entries))
	}
	e := entries[0]
	// The pinned length rather than what moved, because that is what a restart
	// must not fetch again, and Moved is where the difference is recorded.
	if e.Bytes != size {
		t.Errorf("the entry accounts for %d bytes, want the pinned %d", e.Bytes, size)
	}
	if e.Digest != "" {
		t.Errorf("the entry has digest %q, and nothing hashed this file", e.Digest)
	}
	if e.Access != "random" {
		t.Errorf("the entry says the file was read %q", e.Access)
	}
	if e.Moved != rep.Moved || e.Requests != rep.Requests {
		t.Errorf("the entry says %d bytes in %d requests, the report says %d in %d",
			e.Moved, e.Requests, rep.Moved, rep.Requests)
	}
	if e.Documents != int64(len(rows)) {
		t.Errorf("the entry records %d documents", e.Documents)
	}
}

// Verifying a source is still a stream, Parquet included. The footer at the end
// of the file stops a decoder from reading forwards, it does not stop a digest,
// and a run that is checking the manifest should still come away with one.
func TestAParquetSourceIsStreamedWhenNothingIsDecodingIt(t *testing.T) {
	b, size := parquetOf(t, []fineweb2{fineweb2Fixture()})

	h := &host{content: b}
	s, p, _ := serveFile(t, h)
	p.Source = doc.SourceFineWeb2
	f := File{Path: "data/vie_Latn/train/000_00000.parquet", Bytes: size, Digest: sha(b)}

	l, _ := openLedger(t)
	in := &Ingest{Fetcher: &Fetcher{Client: s.Client()}, Ledger: l, Sink: Count}
	if _, err := in.Run(t.Context(), []Work{{Pin: p, File: f}}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	e := l.Entries()[0]
	if e.Digest != sha(b) {
		t.Errorf("a streamed Parquet file has digest %q, want the file's own", e.Digest)
	}
	if e.Access != "" {
		t.Errorf("a streamed file was recorded as read %q", e.Access)
	}
}

var _ RandomSink = (*Docs)(nil)
var _ io.ReaderAt = (*bytes.Reader)(nil)
