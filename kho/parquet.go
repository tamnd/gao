package kho

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/tamnd/gao/doc"
	"github.com/zeebo/blake3"
)

// The published format.
//
// Segments are how a shard moves through a stage and Parquet is how it leaves.
// The reason for the second format is the question a corpus gets asked once it
// exists, which is never "give me every document" and always "how much of this
// is from that host", "what does the score distribution look like", "how much
// of it may I redistribute". Parquet answers those by reading one column, and
// answers them off the Hub without downloading the file.
//
// The columns are written out in [Row] rather than reflected off
// [doc.Document]. A column name in a released dataset is somebody else's query,
// so renaming a Go field must not rename it, and the test that pins the column
// list is what makes that true. Drift in the other direction is worse and is
// caught the other way: a test walks every JSON tag on [doc.Document] and fails
// if the published format has no column for it, so adding a field to the record
// and forgetting the release is a build failure rather than a column that
// quietly is not there.
//
// A repo that does not carry text gets a schema with no text column, not a text
// column full of empty strings. That is the restricted publication pattern, and
// the difference matters: an empty column reads as text that was lost, and a
// missing one reads as text that was never shipped, which is what happened. The
// withholding is at the schema, so a caller cannot forget it, and the test for
// it looks for the text in the bytes of the file rather than in the column
// list.
//
// Nothing here is nullable. Every column is present on every row, and a field
// the record leaves empty writes as the zero value of its type. The alternative
// costs a definition level on half a billion rows to record a distinction
// between empty and absent that no query asks about.

// Row is one document as one row of the published format.
//
// The parquet tags are the published column names. The dict encodings are on
// the columns that repeat: at half a billion documents the host column holds a
// few million distinct values and the source column holds six.
type Row struct {
	DocID         doc.Hash `parquet:"doc_id"`
	RawID         doc.Hash `parquet:"raw_id"`
	Text          string   `parquet:"text"`
	SchemaVersion uint16   `parquet:"schema_version"`

	Source          string    `parquet:"source,dict"`
	SourceLocator   string    `parquet:"source_locator"`
	URL             string    `parquet:"url"`
	Host            string    `parquet:"host,dict"`
	URLTemplate     string    `parquet:"url_template,dict"`
	FetchedAt       time.Time `parquet:"fetched_at,timestamp(millisecond)"`
	MediaType       string    `parquet:"media_type,dict"`
	Extractor       string    `parquet:"extractor,dict"`
	PipelineVersion string    `parquet:"pipeline_version,dict"`

	HTTPStatus     uint16            `parquet:"http_status"`
	RobotsDecision string            `parquet:"robots_decision,dict"`
	RobotsRule     string            `parquet:"robots_rule,dict"`
	RobotsHash     doc.Hash          `parquet:"robots_hash"`
	TDMSignals     map[string]string `parquet:"tdm_signals"`

	Lang       string  `parquet:"lang,dict"`
	LangScore  float32 `parquet:"lang_score"`
	Diacritics string  `parquet:"diacritics,dict"`
	Translated bool    `parquet:"translated"`

	GaoQual    float32            `parquet:"gao_qual"`
	GaoEdu     float32            `parquet:"gao_edu"`
	HPLTBucket uint8              `parquet:"hplt_bucket"`
	Register   string             `parquet:"register,dict"`
	Heuristics map[string]float32 `parquet:"heuristics"`

	DupCluster       doc.Cluster `parquet:"dup_cluster"`
	DupClusterSize   uint32      `parquet:"dup_cluster_size"`
	IsRepresentative bool        `parquet:"is_representative"`

	PIILevel uint8    `parquet:"pii_level"`
	PIITypes []string `parquet:"pii_types,list"`
	PIISpans []Span   `parquet:"pii_spans,list"`

	LicenseClass    string `parquet:"license_class,dict"`
	LicenseEvidence string `parquet:"license_evidence,dict"`

	Structure      string            `parquet:"structure,dict"`
	NChars         uint32            `parquet:"n_chars"`
	NSyllables     uint32            `parquet:"n_syllables"`
	NTokens        uint32            `parquet:"n_tokens"`
	ContamFlags    []string          `parquet:"contam_flags,list"`
	UpstreamFields map[string]string `parquet:"upstream_fields"`
}

// Span is one detected identifier in the text, as a Parquet row.
//
// It is declared here rather than reused from [doc.PIISpan] for the same reason
// [Row] is: the field names are column names, and a column name is an interface.
type Span struct {
	Start uint32 `parquet:"start"`
	Len   uint32 `parquet:"len"`
	Type  string `parquet:"type,dict"`
}

// RowOf converts a document into a row.
//
// The conversion is a function somebody can read rather than a struct tag on
// somebody else's type, which is what makes the published format reviewable.
func RowOf(d *doc.Document) Row {
	spans := make([]Span, len(d.PIISpans))
	for i, s := range d.PIISpans {
		spans[i] = Span{Start: s.Start, Len: s.Len, Type: s.Type}
	}
	return Row{
		DocID:         d.DocID,
		RawID:         d.RawID,
		Text:          d.Text,
		SchemaVersion: d.SchemaVersion,

		Source:          string(d.Source),
		SourceLocator:   d.SourceLocator,
		URL:             d.URL,
		Host:            d.Host,
		URLTemplate:     d.URLTemplate,
		FetchedAt:       d.FetchedAt,
		MediaType:       d.MediaType,
		Extractor:       d.Extractor,
		PipelineVersion: d.PipelineVersion,

		HTTPStatus:     d.HTTPStatus,
		RobotsDecision: d.RobotsDecision,
		RobotsRule:     d.RobotsRule,
		RobotsHash:     d.RobotsHash,
		TDMSignals:     d.TDMSignals,

		Lang:       d.Lang,
		LangScore:  d.LangScore,
		Diacritics: d.Diacritics,
		Translated: d.Translated,

		GaoQual:    d.GaoQual,
		GaoEdu:     d.GaoEdu,
		HPLTBucket: d.HPLTBucket,
		Register:   d.Register,
		Heuristics: d.Heuristics,

		DupCluster:       d.DupCluster,
		DupClusterSize:   d.DupClusterSize,
		IsRepresentative: d.IsRepresentative,

		PIILevel: uint8(d.PIILevel),
		PIITypes: d.PIITypes,
		PIISpans: spans,

		// The class is stored by name and not by its integer, so that a file
		// read without gao says restricted rather than 3.
		LicenseClass:    d.LicenseClass.String(),
		LicenseEvidence: d.LicenseEvidence,

		Structure:      d.Structure,
		NChars:         d.NChars,
		NSyllables:     d.NSyllables,
		NTokens:        d.NTokens,
		ContamFlags:    d.ContamFlags,
		UpstreamFields: d.UpstreamFields,
	}
}

// TextColumn is the column a repo withholds when it does not carry text.
const TextColumn = "text"

// schemaName is the root name of the published schema. It is what a Parquet
// tool prints above the column list.
const schemaName = "document"

// withText is the full published schema, and withoutText is the same schema
// with the text column removed. Both are built once: a schema is immutable, and
// building one per file would parse the struct tags for every shard of every
// snapshot.
//
// The two hold their columns in different orders, because a schema built from
// the struct keeps the order the fields are declared in and one built from a
// group sorts them. That is a difference in physical layout and not in the
// contract: a column is addressed by name everywhere, and neither Parquet nor
// any reader of it cares which came first.
var (
	withText = sync.OnceValue(func() *parquet.Schema {
		return parquet.SchemaOf(Row{})
	})
	withoutText = sync.OnceValue(func() *parquet.Schema {
		g := parquet.Group{}
		for _, f := range withText().Fields() {
			if f.Name() == TextColumn {
				continue
			}
			g[f.Name()] = f
		}
		return parquet.NewSchema(schemaName, g)
	})
)

// SchemaFor returns the schema a dataset's files are written with, which is the
// published schema for a repo that carries text and the published schema
// without the text column for one that does not.
func SchemaFor(d Dataset) *parquet.Schema {
	if d.Text {
		return withText()
	}
	return withoutText()
}

// Columns returns the column names of the schema, in the order the file holds
// them. A leaf inside a map or a list is named by its full path, because
// pii_spans.list.element.start is what a query has to say.
func Columns(s *parquet.Schema) []string {
	out := make([]string, 0, 64)
	for _, path := range s.Columns() {
		out = append(out, joinPath(path))
	}
	return out
}

// joinPath renders a column path the way Parquet tools print it.
func joinPath(path []string) string {
	return strings.Join(path, ".")
}

// Stamp is what a part file says about itself.
//
// It is written into the Parquet key value metadata, which travels inside the
// file, so a shard that somebody downloaded a year ago and kept still names the
// snapshot it belongs to, the stage that wrote it, and the box that ran it. A
// manifest says the same things and a manifest is a separate file.
type Stamp struct {
	// Snapshot is the release or working snapshot the part belongs to, which is
	// also the partition its path sits under.
	Snapshot string

	// Stage is name@semver of whatever wrote it. Two parts written by different
	// versions of the same stage are not comparable and this is how a reader
	// knows.
	Stage string

	// Box is the machine label, so a number that only reproduces on one box can
	// be traced to it.
	Box string
}

// Metadata returns the key value pairs a part carries.
func (s Stamp) Metadata() map[string]string {
	return map[string]string{
		"gao.snapshot":       s.Snapshot,
		"gao.stage":          s.Stage,
		"gao.box":            s.Box,
		"gao.schema_version": fmt.Sprintf("%d", doc.SchemaVersion),
	}
}

// ErrNotAdmitted is returned when a document's license class is not one the
// dataset carries.
var ErrNotAdmitted = errors.New("kho: that dataset does not admit that license class")

// DefaultRowGroup is how many rows a row group holds.
//
// A row group is buffered in memory before it is written, so this is the number
// that decides whether an ingest fits on a box with 6 GB. At Vietnamese web
// documents of a few kilobytes each, 50000 rows is a few hundred megabytes of
// buffer and a row group large enough that a column read is sequential rather
// than a scatter of small reads.
const DefaultRowGroup = 50_000

// ParquetWriter writes documents to one Parquet file.
type ParquetWriter struct {
	dataset Dataset
	w       *parquet.GenericWriter[Row]
	buf     []Row
	n       int
	text    int64
	closed  bool
}

// NewParquetWriter returns a writer that writes rows for dataset d to w.
func NewParquetWriter(w io.Writer, d Dataset, s Stamp) *ParquetWriter {
	meta := s.Metadata()
	opts := make([]parquet.WriterOption, 0, 4+len(meta))
	opts = append(opts,
		SchemaFor(d),
		parquet.Compression(&parquet.Zstd),
		parquet.MaxRowsPerRowGroup(DefaultRowGroup),
		// A bloom filter on the identity column, because the question asked of
		// a shard from outside is whether it holds a document, and answering it
		// by reading the column defeats the point of having the file remote.
		parquet.BloomFilters(parquet.SplitBlockFilter(10, "doc_id")),
	)
	for k, v := range meta {
		opts = append(opts, parquet.KeyValueMetadata(k, v))
	}
	return &ParquetWriter{dataset: d, w: parquet.NewGenericWriter[Row](w, opts...)}
}

// Append writes one document.
//
// A document whose license class the dataset does not carry is refused here,
// before an upload rather than after a release. The check is the same one
// [Dataset.Admits] makes, and having it at the write is what stops a stage from
// being the place somebody has to remember it.
func (p *ParquetWriter) Append(d *doc.Document) error {
	if p.closed {
		return errors.New("kho: append to a closed parquet file")
	}
	if !p.dataset.Admits(d.LicenseClass) {
		return fmt.Errorf("%w: %s does not carry %s", ErrNotAdmitted, p.dataset.Name, d.LicenseClass)
	}
	p.buf = append(p.buf[:0], RowOf(d))
	if _, err := p.w.Write(p.buf); err != nil {
		return fmt.Errorf("kho: writing row %d: %w", p.n, err)
	}
	p.n++
	p.text += int64(len(d.Text))
	return nil
}

// Documents returns how many rows have been written.
func (p *ParquetWriter) Documents() int { return p.n }

// Text returns the total length of the text appended so far.
//
// It is the number a caller bounds a file by, and it is not the file size. A
// row group is buffered and compressed on the way out, so how large the file
// has become is not knowable until it closes, and a caller that has to decide
// whether to roll over to the next one has to decide on something it can see.
func (p *ParquetWriter) Text() int64 { return p.text }

// Close flushes the last row group and writes the footer. It does not close the
// underlying writer.
func (p *ParquetWriter) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	if err := p.w.Close(); err != nil {
		return fmt.Errorf("kho: closing the parquet file: %w", err)
	}
	return nil
}

// PartFile is one finished Parquet file, as the manifest and the upload record
// it.
type PartFile struct {
	// Path is the path inside the repo, which is also the path under the
	// directory it was written to.
	Path string

	Documents int
	Bytes     int64

	// Hash is blake3 over the bytes of the file, computed as they were written
	// rather than by reading the file back, so a file written correctly and
	// stored incorrectly fails verification rather than passing it.
	Hash doc.Hash
}

// Part is a Parquet file being written to disk.
//
// It is built under a .part extension and renamed into place only after it
// closes cleanly, which is the same discipline the segments use and for the
// same reason: a run that dies halfway should not leave a file that a later
// upload mistakes for a finished one.
type Part struct {
	rel    string
	final  string
	part   string
	f      *os.File
	digest *blake3.Hasher
	size   *counter
	w      *ParquetWriter
	done   bool
}

// counter counts the bytes written through it, since the file size is wanted
// without a stat and the digest is wanted without a second pass.
type counter struct {
	w io.Writer
	n int64
}

func (c *counter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}

// CreatePart opens a part file at rel under dir, creating the directories the
// path names.
func CreatePart(dir, rel string, d Dataset, s Stamp) (*Part, error) {
	final := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return nil, fmt.Errorf("kho: creating the directory for %s: %w", rel, err)
	}
	part := final + partExt
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("kho: opening %s: %w", rel, err)
	}
	digest := blake3.New()
	size := &counter{w: io.MultiWriter(f, digest)}
	return &Part{
		rel:    rel,
		final:  final,
		part:   part,
		f:      f,
		digest: digest,
		size:   size,
		w:      NewParquetWriter(size, d, s),
	}, nil
}

// Append writes one document to the part.
func (p *Part) Append(d *doc.Document) error { return p.w.Append(d) }

// Documents returns how many rows the part holds.
func (p *Part) Documents() int { return p.w.Documents() }

// Text returns the total length of the text appended so far, which is what a
// caller rolls a part over on.
func (p *Part) Text() int64 { return p.w.Text() }

// Close finishes the file and moves it into place.
func (p *Part) Close() (PartFile, error) {
	if p.done {
		return PartFile{}, errors.New("kho: closing a part that is already finished")
	}
	p.done = true
	if err := p.w.Close(); err != nil {
		_ = p.f.Close()
		return PartFile{}, err
	}
	// Synced before the rename. Without that a crash can leave the rename
	// durable and the contents not, which is a part that is present, named as
	// finished, and truncated.
	if err := p.f.Sync(); err != nil {
		_ = p.f.Close()
		return PartFile{}, fmt.Errorf("kho: syncing %s: %w", p.rel, err)
	}
	if err := p.f.Close(); err != nil {
		return PartFile{}, fmt.Errorf("kho: closing %s: %w", p.rel, err)
	}
	if err := os.Rename(p.part, p.final); err != nil {
		return PartFile{}, fmt.Errorf("kho: moving %s into place: %w", p.rel, err)
	}
	return PartFile{
		Path:      p.rel,
		Documents: p.w.Documents(),
		Bytes:     p.size.n,
		Hash:      doc.Hash(p.digest.Sum(nil)[:doc.HashSize]),
	}, nil
}

// Abandon closes the file and deletes the partial. Calling it after
// [Part.Close] does nothing, so it is safe to defer.
func (p *Part) Abandon() {
	if p.done {
		return
	}
	p.done = true
	_ = p.f.Close()
	_ = os.Remove(p.part)
}

// ReadPart reads a Parquet file back as rows, which is what the tests and
// verification use and what a reader outside gao would write for itself.
func ReadPart(path string) ([]Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	pf, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		return nil, fmt.Errorf("kho: opening %s: %w", path, err)
	}
	// Read against the Row schema rather than the file's own. They name the same
	// columns, and the file's is a group schema built from the footer, which
	// reconstructs into a map and not into a Go struct.
	rows := make([]Row, 0, pf.NumRows())
	r := parquet.NewGenericReader[Row](f)
	defer func() { _ = r.Close() }()
	buf := make([]Row, 64)
	for {
		n, err := r.Read(buf)
		rows = append(rows, buf[:n]...)
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return rows, fmt.Errorf("kho: reading %s: %w", path, err)
		}
		if n == 0 {
			return rows, nil
		}
	}
}

// PartMetadata returns the key value metadata a part carries, which is how a
// file separated from its manifest is identified.
func PartMetadata(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	pf, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		return nil, fmt.Errorf("kho: opening %s: %w", path, err)
	}
	out := make(map[string]string)
	for _, kv := range pf.Metadata().KeyValueMetadata {
		out[kv.Key] = kv.Value
	}
	return out, nil
}

// PartColumns returns the column names a written file actually holds, which is
// what a test asserts against rather than against the schema it was told to
// use.
func PartColumns(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	pf, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		return nil, fmt.Errorf("kho: opening %s: %w", path, err)
	}
	return slices.Clone(Columns(pf.Schema())), nil
}
