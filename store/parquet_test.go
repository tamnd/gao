package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// stamp is what the tests write into every part, since the stamp is not what
// most of them are about.
var stamp = Stamp{Snapshot: "glotcc-9ad140b6be3a", Stage: "harvest@0.1.0", Box: "server1"}

// textDataset is a repo that carries text, and urlDataset is one that does not.
// Both are looked up rather than constructed, so a change to the real table is
// a change these tests see.
func textDataset(t *testing.T) Dataset {
	t.Helper()
	d, ok := Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("vietnamese-web-text is not in the dataset table")
	}
	return d
}

func urlDataset(t *testing.T) Dataset {
	t.Helper()
	d, ok := Lookup("vietnamese-web-urls")
	if !ok {
		t.Fatal("vietnamese-web-urls is not in the dataset table")
	}
	if d.Text {
		t.Fatal("vietnamese-web-urls carries text, and this test needs the repo that does not")
	}
	return d
}

// writePart builds a part holding the given documents and returns its path.
func writePart(t *testing.T, d Dataset, docs ...*doc.Document) (string, PartFile) {
	t.Helper()
	dir := t.TempDir()
	rel := StagePath(stamp.Snapshot, 3, 0)
	p, err := CreatePart(dir, rel, d, stamp)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	defer p.Abandon()
	for i, rec := range docs {
		if err := p.Append(rec); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}
	rec, err := p.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return filepath.Join(dir, filepath.FromSlash(rel)), rec
}

// The column list is the published interface. A rename here is a rename in
// somebody else's query, so it is a test rather than a convention.
func TestThePublishedColumnsAreTheOnesWrittenDown(t *testing.T) {
	want := []string{
		"doc_id",
		"raw_id",
		"text",
		"schema_version",
		"source",
		"source_locator",
		"url",
		"host",
		"url_template",
		"fetched_at",
		"media_type",
		"extractor",
		"pipeline_version",
		"http_status",
		"robots_decision",
		"robots_rule",
		"robots_hash",
		"tdm_signals.key_value.key",
		"tdm_signals.key_value.value",
		"consent",
		"lang",
		"lang_score",
		"diacritics",
		"translated",
		"gao_qual",
		"gao_edu",
		"hplt_bucket",
		"register",
		"heuristics.key_value.key",
		"heuristics.key_value.value",
		"dup_cluster",
		"dup_cluster_size",
		"is_representative",
		"pii_level",
		"pii_types.list.element",
		"pii_spans.list.element.start",
		"pii_spans.list.element.len",
		"pii_spans.list.element.type",
		"license_class",
		"license_evidence",
		"structure",
		"n_chars",
		"n_syllables",
		"n_tokens",
		"contam_flags.list.element",
		"upstream_fields.key_value.key",
		"upstream_fields.key_value.value",
	}
	got := Columns(SchemaFor(Dataset{Text: true}))
	if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Errorf("the published columns are\n%s\nand the contract says\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The other direction, and the one that costs a release if it is missed: a
// field added to the record with no column for it is a field that silently does
// not ship.
func TestEveryFieldOfTheRecordHasAColumn(t *testing.T) {
	record := jsonNames(reflect.TypeFor[doc.Document]())
	published := parquetNames(reflect.TypeFor[Row]())

	for _, name := range record {
		if !slices.Contains(published, name) {
			t.Errorf("doc.Document has %q and the published format has no column for it", name)
		}
	}
	for _, name := range published {
		if !slices.Contains(record, name) {
			t.Errorf("the published format has a column %q that no record field carries", name)
		}
	}
}

// jsonNames collects the JSON field names of a struct, following embedded
// structs, which is how the record is laid out.
func jsonNames(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, jsonNames(f.Type)...)
			continue
		}
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// parquetNames collects the column names a row declares, which are the top
// level ones only, since a nested leaf belongs to the column above it.
func parquetNames(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("parquet"), ",")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func TestADocumentSurvivesTheRoundTrip(t *testing.T) {
	in := sample(7)
	in.TDMSignals = map[string]string{"tdmrep": "0"}
	in.Consent = doc.ConsentOpen
	in.Heuristics = map[string]float32{"mean_line_length": 63.5}
	in.ContamFlags = []string{"vmlu"}
	in.UpstreamFields = map[string]string{"bucket": "9"}
	in.PIITypes = []string{"phone"}
	in.PIISpans = []doc.PIISpan{{Start: 12, Len: 10, Type: "phone"}}
	in.NSyllables = 31
	in.NTokens = 44

	path, _ := writePart(t, textDataset(t), in)
	rows, err := ReadPart(path)
	if err != nil {
		t.Fatalf("ReadPart: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("read %d rows, want 1", len(rows))
	}
	got, want := rows[0], RowOf(in)

	// Compared field by field through the row rather than by DeepEqual on the
	// document, because a timestamp column holds milliseconds and time.Time
	// holds a monotonic reading and a location.
	if !got.FetchedAt.Equal(want.FetchedAt) {
		t.Errorf("fetched_at came back %v, want %v", got.FetchedAt, want.FetchedAt)
	}
	got.FetchedAt = want.FetchedAt
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the row came back different:\ngot  %+v\nwant %+v", got, want)
	}
}

// A repo that does not carry text does not have a text column, and the check is
// against the bytes of the file rather than against the schema it was handed,
// because the schema is the thing under test.
func TestARepoThatWithholdsTextShipsNoText(t *testing.T) {
	in := sample(1)
	in.LicenseClass = doc.LicenseRestricted
	in.Text = "một đoạn văn bản không được phép phát hành lại"
	in.DocID = doc.SumString(in.Text)

	path, _ := writePart(t, urlDataset(t), in)

	cols, err := PartColumns(path)
	if err != nil {
		t.Fatalf("PartColumns: %v", err)
	}
	if slices.Contains(cols, TextColumn) {
		t.Error("the file has a text column, and this repo withholds text")
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(in.Text)) {
		t.Error("the withheld text is in the bytes of the file")
	}
}

// The withheld column is absent rather than empty. The distinction is the whole
// point of the restricted pattern: an empty column reads as text that got lost.
func TestTheWithheldColumnIsMissingAndNotEmpty(t *testing.T) {
	with := Columns(SchemaFor(Dataset{Text: true}))
	without := Columns(SchemaFor(Dataset{Text: false}))
	if len(with) != len(without)+1 {
		t.Fatalf("the two schemas differ by %d columns, want exactly one", len(with)-len(without))
	}
	if slices.Contains(without, TextColumn) {
		t.Error("the withholding schema still has a text column")
	}
	for _, c := range with {
		if c != TextColumn && !slices.Contains(without, c) {
			t.Errorf("withholding text also dropped %q", c)
		}
	}
}

func rejectDataset(t *testing.T) Dataset {
	t.Helper()
	d, ok := Lookup("vietnamese-text-rejects")
	if !ok {
		t.Fatal("vietnamese-text-rejects is not in the dataset table")
	}
	if !d.Reject {
		t.Fatal("vietnamese-text-rejects is not marked as the rejects repo")
	}
	return d
}

// The rejects repo carries every column the corpus carries, minus the text and
// plus the three that say what happened, so a query written against one works
// against the other.
func TestTheRejectsSchemaIsTheCorpusSchemaAndThreeColumns(t *testing.T) {
	without := Columns(SchemaFor(urlDataset(t)))
	rejects := Columns(SchemaFor(rejectDataset(t)))
	if len(rejects) != len(without)+3 {
		t.Fatalf("the rejects schema has %d columns against %d, want three more", len(rejects), len(without))
	}
	for _, c := range without {
		if !slices.Contains(rejects, c) {
			t.Errorf("the rejects schema is missing %q", c)
		}
	}
	for _, c := range []string{"reject_stage", "reject_reason", "reject_detail"} {
		if !slices.Contains(rejects, c) {
			t.Errorf("the rejects schema has no %q column", c)
		}
	}
	if slices.Contains(rejects, TextColumn) {
		t.Error("the rejects schema carries text, and a document that failed a filter has the license it always had")
	}
}

func TestARejectionCarriesTheStageAndTheReason(t *testing.T) {
	in := sample(4)
	in.LicenseClass = doc.LicenseRestricted
	in.Text = "một tài liệu bị loại vì quá ngắn"
	in.DocID = doc.SumString(in.Text)

	dir := t.TempDir()
	rel := StagePath(stamp.Snapshot, 0, 0)
	p, err := CreatePart(dir, rel, rejectDataset(t), stamp)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	defer p.Abandon()
	if err := p.AppendReject(in, "crawl.sift", "short", "42 syllables against a floor of 60"); err != nil {
		t.Fatalf("AppendReject: %v", err)
	}
	if _, err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := filepath.Join(dir, filepath.FromSlash(rel))
	rows, err := ReadRejectPart(path)
	if err != nil {
		t.Fatalf("ReadRejectPart: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("read %d rows, want 1", len(rows))
	}
	got := rows[0]
	if got.RejectStage != "crawl.sift" || got.RejectReason != "short" {
		t.Errorf("the rejection came back as %q %q", got.RejectStage, got.RejectReason)
	}
	if got.RejectDetail == "" {
		t.Error("the detail did not survive, and a reason with no number behind it cannot be argued with")
	}
	if got.URL != in.URL {
		t.Errorf("the url came back %q, want %q", got.URL, in.URL)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(in.Text)) {
		t.Error("the text of a rejected document is in the bytes of the file")
	}
}

// The two write paths are not interchangeable, and each says so rather than
// writing a row that is missing half of what its repo promises.
func TestARepoTakesEitherDocumentsOrRejectionsAndNotBoth(t *testing.T) {
	in := sample(5)
	in.LicenseClass = doc.LicenseRestricted

	var b bytes.Buffer
	w := NewParquetWriter(&b, rejectDataset(t), stamp)
	if err := w.Append(in); !errors.Is(err, ErrNotAdmitted) {
		t.Errorf("the rejects repo took a document with no rejection: %v", err)
	}
	if err := w.AppendReject(in, "crawl.sift", "", "nothing"); err == nil {
		t.Error("a rejection with no reason was written, and the reason is the column anybody counts by")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	b.Reset()
	w = NewParquetWriter(&b, urlDataset(t), stamp)
	if err := w.AppendReject(in, "crawl.sift", "short", "too short"); !errors.Is(err, ErrNotAdmitted) {
		t.Errorf("the url repo took a rejection: %v", err)
	}
}

// The legal check is at the write, so that a stage cannot be the place somebody
// has to remember it.
func TestADocumentOfAClassTheRepoDoesNotCarryIsRefused(t *testing.T) {
	legal, ok := Lookup("vietnamese-legal-text")
	if !ok {
		t.Fatal("vietnamese-legal-text is not in the dataset table")
	}
	in := sample(1)
	in.LicenseClass = doc.LicenseRestricted

	var buf bytes.Buffer
	w := NewParquetWriter(&buf, legal, stamp)
	err := w.Append(in)
	if !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("appending a restricted document to a public text repo gave %v, want ErrNotAdmitted", err)
	}
	for _, want := range []string{legal.Name, doc.LicenseRestricted.String()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// A page that reserved itself does not go into a published text repo, whatever
// its license says. The two are separate questions and a permissive license is
// not an answer to the second one.
func TestAPageThatReservedItselfIsRefusedByAPublishedTextRepo(t *testing.T) {
	for _, said := range []doc.Consent{doc.ConsentNoTrain, doc.ConsentNoIndex} {
		t.Run(string(said), func(t *testing.T) {
			in := sample(1)
			in.LicenseClass = doc.LicenseOpen
			in.Consent = said

			var buf bytes.Buffer
			w := NewParquetWriter(&buf, textDataset(t), stamp)
			err := w.Append(in)
			if !errors.Is(err, ErrNotAdmitted) {
				t.Fatalf("appending a %s document to a public text repo gave %v, want ErrNotAdmitted", said, err)
			}
			if !strings.Contains(err.Error(), string(said)) {
				t.Errorf("the refusal does not say what the page said: %v", err)
			}
		})
	}
}

// The working repo used to take it, on the grounds that processing material is
// not publishing it. It is public now, so it does not, and a page that reserved
// itself reaches the reject store instead, where it is counted and carries no
// text.
func TestTheWorkingRepoRefusesAPageThatReservedItself(t *testing.T) {
	staging := Staging()
	if !staging.Text {
		t.Fatal("the working repo carries no text, and this test is about the text it carries")
	}
	in := sample(1)
	in.Consent = doc.ConsentNoTrain

	var buf bytes.Buffer
	w := NewParquetWriter(&buf, staging, stamp)
	err := w.Append(in)
	if !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("a public repo took a reserved page: %v", err)
	}
	if !strings.Contains(err.Error(), staging.Name) {
		t.Errorf("the refusal does not name the repo: %v", err)
	}
}

// A part is not in place until it closes, so an upload that lists the directory
// cannot pick up a file that is still being written.
func TestAPartIsNotInPlaceUntilItCloses(t *testing.T) {
	dir := t.TempDir()
	rel := StagePath(stamp.Snapshot, 0, 0)
	p, err := CreatePart(dir, rel, textDataset(t), stamp)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Abandon()
	if err := p.Append(sample(1)); err != nil {
		t.Fatal(err)
	}

	final := filepath.Join(dir, filepath.FromSlash(rel))
	if _, err := os.Stat(final); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the final name exists while the part is still open: %v", err)
	}
	if _, err := os.Stat(final + partExt); err != nil {
		t.Errorf("the partial file is not there: %v", err)
	}
	if _, err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(final); err != nil {
		t.Errorf("the part is not in place after closing: %v", err)
	}
	if _, err := os.Stat(final + partExt); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the partial file survived the close: %v", err)
	}
}

func TestAnAbandonedPartLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	rel := StagePath(stamp.Snapshot, 0, 0)
	p, err := CreatePart(dir, rel, textDataset(t), stamp)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Append(sample(1)); err != nil {
		t.Fatal(err)
	}
	p.Abandon()

	entries, err := os.ReadDir(filepath.Dir(filepath.Join(dir, filepath.FromSlash(rel))))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("abandoning left %d files behind", len(entries))
	}
}

func TestAbandonAfterCloseLeavesTheFileAlone(t *testing.T) {
	path, _ := writePart(t, textDataset(t), sample(1))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the closed part is not there: %v", err)
	}
}

// The size and the hash are computed on the way out, so a file written
// correctly and stored incorrectly fails verification rather than passing it.
func TestAPartRecordsItsOwnSizeAndHash(t *testing.T) {
	path, rec := writePart(t, textDataset(t), sample(1), sample(2), sample(3))

	if rec.Documents != 3 {
		t.Errorf("the part records %d documents, want 3", rec.Documents)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Bytes != int64(len(b)) {
		t.Errorf("the part records %d bytes and the file is %d", rec.Bytes, len(b))
	}
	if got := doc.Sum(b); got != rec.Hash {
		t.Errorf("the part records hash %s and the file hashes to %s", rec.Hash, got)
	}
}

// A file separated from its manifest still says what it is.
func TestAPartCarriesItsStamp(t *testing.T) {
	path, _ := writePart(t, textDataset(t), sample(1))
	meta, err := PartMetadata(path)
	if err != nil {
		t.Fatalf("PartMetadata: %v", err)
	}
	for key, want := range stamp.Metadata() {
		if got := meta[key]; got != want {
			t.Errorf("%s is %q in the file, want %q", key, got, want)
		}
	}
}

// A part written by a run with no tokenizer says so, because the alternative is
// a column of zeros that reads as a count.
//
// This came off the first published parts. A query over 500000 real documents on
// the Hub returned 500000 documents and 0 tokens, and there was nothing in the
// file to say which of the two things that meant. counts.json says it, and a
// part on the Hub does not travel with counts.json.
func TestAPartSaysWhetherAnythingCountedItsTokens(t *testing.T) {
	none := Stamp{Snapshot: "glotcc-9ad140b6be3a", Stage: "harvest@0.1.0", Box: "server3"}
	if got, ok := none.Metadata()["gao.tokenizer"]; !ok || got != "" {
		t.Errorf("a run with no tokenizer stamps %q with present=%v, want an empty value that is present", got, ok)
	}

	counted := none
	counted.Tokenizer = "gemma-3@sha256:1299c11d"
	if got := counted.Metadata()["gao.tokenizer"]; got != counted.Tokenizer {
		t.Errorf("the tokenizer is %q in the metadata, want %q", got, counted.Tokenizer)
	}

	// And it survives the round trip through the file, which is the whole point
	// of putting it in the footer rather than in a manifest beside it.
	dir := t.TempDir()
	p, err := CreatePart(dir, "part-00000.parquet", textDataset(t), counted)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Append(sample(1)); err != nil {
		t.Fatal(err)
	}
	f, err := p.Close()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := PartMetadata(filepath.Join(dir, f.Path))
	if err != nil {
		t.Fatalf("PartMetadata: %v", err)
	}
	if got := meta["gao.tokenizer"]; got != counted.Tokenizer {
		t.Errorf("the file says %q wrote its tokens, want %q", got, counted.Tokenizer)
	}
}

// Text is the text, not the file. A caller rolling a part over needs a number
// it can see before the row group flushes.
func TestTextCountsTheTextAndNotTheFile(t *testing.T) {
	var buf bytes.Buffer
	w := NewParquetWriter(&buf, textDataset(t), stamp)

	var want int64
	for i := range 5 {
		d := sample(i)
		if err := w.Append(d); err != nil {
			t.Fatal(err)
		}
		want += int64(len(d.Text))
	}
	if got := w.Text(); got != want {
		t.Errorf("Text is %d, want %d", got, want)
	}
	if got := w.Documents(); got != 5 {
		t.Errorf("Documents is %d, want 5", got)
	}
}

func TestAppendingToAClosedFileIsAnError(t *testing.T) {
	var buf bytes.Buffer
	w := NewParquetWriter(&buf, textDataset(t), stamp)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(sample(1)); err == nil {
		t.Error("appending to a closed file reported success")
	}
}

// An empty part is still a Parquet file with the schema in it, because a source
// file that decoded to nothing should produce something a reader can open and
// see is empty rather than a zero byte file it has to guess about.
func TestAnEmptyPartIsStillAParquetFile(t *testing.T) {
	path, rec := writePart(t, textDataset(t))
	if rec.Documents != 0 {
		t.Errorf("an empty part records %d documents", rec.Documents)
	}
	cols, err := PartColumns(path)
	if err != nil {
		t.Fatalf("reading an empty part: %v", err)
	}
	if !slices.Contains(cols, TextColumn) {
		t.Error("an empty part has no schema in it")
	}
	rows, err := ReadPart(path)
	if err != nil {
		t.Fatalf("ReadPart on an empty part: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("an empty part read back %d rows", len(rows))
	}
}

// A part off the fleet is around 700 MB of compressed text, which comes back as
// several gigabytes of strings. Reading one to measure it has to cost what one
// row costs and not what the file costs, or the boxes can only ever measure the
// parts that happen to be small.
func TestScanPartHandsOverEveryRowInOrder(t *testing.T) {
	docs := make([]*doc.Document, 5)
	for i := range docs {
		docs[i] = sample(i)
	}
	path, _ := writePart(t, textDataset(t), docs...)

	var got []string
	if err := ScanPart(path, func(r Row) error {
		got = append(got, r.Text)
		return nil
	}); err != nil {
		t.Fatalf("ScanPart: %v", err)
	}
	if len(got) != len(docs) {
		t.Fatalf("the scan saw %d rows, want %d", len(got), len(docs))
	}
	for i, rec := range docs {
		if got[i] != rec.Text {
			t.Errorf("row %d came back as %q, want %q", i, got[i], rec.Text)
		}
	}
}

// A caller that has seen enough should be able to stop, and what it stopped for
// is what it wants back rather than something wrapped around it.
func TestScanPartStopsWhenTheCallerDoes(t *testing.T) {
	docs := make([]*doc.Document, 200)
	for i := range docs {
		docs[i] = sample(i)
	}
	path, _ := writePart(t, textDataset(t), docs...)

	enough := errors.New("enough")
	seen := 0
	err := ScanPart(path, func(Row) error {
		seen++
		if seen == 3 {
			return enough
		}
		return nil
	})
	if !errors.Is(err, enough) {
		t.Fatalf("ScanPart returned %v, want the caller's own error", err)
	}
	if seen != 3 {
		t.Errorf("the scan carried on to row %d after being told to stop", seen)
	}
}

func TestScanPartSaysWhichFileIsNotAPart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-part.parquet")
	if err := os.WriteFile(path, []byte("this is not parquet"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ScanPart(path, func(Row) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "not-a-part.parquet") {
		t.Errorf("scanning a file that is not a part returned %v, and it should name the file", err)
	}
}

// The inverse. A working repo is an input as well as an output: the cleaning
// stage reads the raw corpus back out of Parquet and puts documents through the
// line, and every column it drops on the way in is a column the clean corpus
// loses. DeepEqual on the whole document is the point of the test, because a
// field added to the record and forgotten in DocumentOf is exactly the bug that
// would otherwise ship quietly.
func TestARowComesBackAsTheDocumentItCameFrom(t *testing.T) {
	in := sample(11)
	in.TDMSignals = map[string]string{"tdmrep": "0", "ai-robots": "disallow"}
	in.Consent = doc.ConsentOpen
	in.Heuristics = map[string]float32{"mean_line_length": 63.5, "symbol_rate": 0.01}
	in.ContamFlags = []string{"vmlu", "vi-mmlu"}
	in.UpstreamFields = map[string]string{"bucket": "9", "warc": "CC-MAIN-2026-05"}
	in.PIITypes = []string{"phone", "email"}
	in.PIISpans = []doc.PIISpan{{Start: 12, Len: 10, Type: "phone"}}
	in.PIILevel = doc.RedactStandard
	in.DupCluster = doc.Cluster{9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2, 3, 4, 5, 6}
	in.DupClusterSize = 4
	in.IsRepresentative = true
	in.NSyllables = 31
	in.NTokens = 44
	in.GaoQual = 0.71
	in.GaoEdu = 0.44
	in.HPLTBucket = 9
	in.Register = "narrative"

	got := DocumentOf(RowOf(in))

	// The timestamp is compared first, for the same reason as above: the column
	// holds milliseconds and time.Time holds a location and a monotonic reading.
	if !got.FetchedAt.Equal(in.FetchedAt) {
		t.Errorf("fetched_at came back %v, want %v", got.FetchedAt, in.FetchedAt)
	}
	got.FetchedAt = in.FetchedAt
	if !reflect.DeepEqual(got, in) {
		t.Errorf("the document came back different:\ngot  %+v\nwant %+v", got, in)
	}
	if err := got.Admit(); err != nil {
		t.Errorf("a document read back out of a row fails the contract: %v", err)
	}
}

// A license class the build does not know is not a reason to lose the row. It
// is a reason to read it as unknown, which is the class that ships nowhere and
// is the safe end of the scale.
func TestARowWithAnUnreadableLicenseComesBackUnknown(t *testing.T) {
	row := RowOf(sample(3))
	row.LicenseClass = "invented-later"
	if got := DocumentOf(row).LicenseClass; got != doc.LicenseUnknown {
		t.Errorf("a license class this build cannot read came back as %q, want %q", got, doc.LicenseUnknown)
	}
}
