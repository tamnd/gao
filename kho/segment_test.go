package kho

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
	"github.com/tamnd/gao/doc"
)

// sample builds a valid document with distinct text, so that a test which reads
// document i can tell whether it got document i.
func sample(i int) *doc.Document {
	text := fmt.Sprintf(
		"Bài viết số %d. Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc. "+
			"Nội dung của tài liệu này đủ dài để vượt qua ngưỡng tối thiểu của hợp đồng nhập liệu.", i)
	d := &doc.Document{
		RawID:         doc.SumString("raw:" + text),
		Text:          text,
		SchemaVersion: doc.SchemaVersion,
		Provenance: doc.Provenance{
			Source:          doc.SourceCrawl,
			SourceLocator:   fmt.Sprintf("gao-crawl-2026-09/00001.warc.gz@%d+4096", i*4096),
			URL:             fmt.Sprintf("https://vnexpress.net/thoi-su/bai-viet-%d.html", i),
			Host:            "vnexpress.net",
			FetchedAt:       time.Date(2026, 9, 14, 3, 22, 11, 0, time.UTC),
			MediaType:       "text/html",
			Extractor:       "go-trafilatura@1.4.0",
			PipelineVersion: "0.1.0",
		},
		Language: doc.Language{Lang: "vie", LangScore: 0.997, Diacritics: "present"},
		Licensing: doc.Licensing{
			LicenseClass:    doc.LicenseOpen,
			LicenseEvidence: "robots allow, no TDM reservation",
		},
	}
	d.DocID = doc.SumString(d.Text)
	d.NChars = uint32(utf8.RuneCountInString(d.Text))
	return d
}

// write builds a segment holding n documents and returns its bytes and its
// content hash.
func write(t *testing.T, n int, opts ...WriterOption) ([]byte, doc.Hash) {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter[*doc.Document](&buf, opts...)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for i := range n {
		if err := w.Append(sample(i)); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}
	if got := w.Count(); got != n {
		t.Fatalf("Count is %d after %d appends", got, n)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes(), w.Hash()
}

func TestStreamingRoundTrip(t *testing.T) {
	const n = 200
	raw, _ := write(t, n)

	r, err := NewReader[*doc.Document](bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

	for i := range n {
		d, err := r.Next()
		if err != nil {
			t.Fatalf("Next(%d): %v", i, err)
		}
		if want := sample(i); d.DocID != want.DocID {
			t.Fatalf("document %d has id %s, want %s", i, d.DocID, want.DocID)
		}
		if err := d.Admit(); err != nil {
			t.Fatalf("document %d no longer satisfies the contract after a round trip: %v", i, err)
		}
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("reading past the end gave %v, want io.EOF", err)
	}
}

func TestIndexFrameIsSkippedByAPlainZstdReader(t *testing.T) {
	// The index rides in a zstd skippable frame precisely so that anyone can run
	// the segment through `zstd -d` and get JSONL. If that stops being true, the
	// store has quietly become a private format.
	raw, _ := write(t, 50, FrameBytes(4096))

	dec, err := zstd.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("zstd.NewReader: %v", err)
	}
	defer dec.Close()

	plain, err := io.ReadAll(dec.IOReadCloser())
	if err != nil {
		t.Fatalf("decompressing the whole segment: %v", err)
	}
	lines := strings.Count(string(plain), "\n")
	if lines != 50 {
		t.Errorf("plain decompression gave %d lines, want 50", lines)
	}
	if !strings.HasPrefix(string(plain), `{"doc_id":`) {
		t.Errorf("decompressed output does not start with a record: %.60s", plain)
	}
	if strings.Contains(string(plain), `"frames"`) {
		t.Error("the index leaked into the decompressed stream")
	}
}

func TestRandomAccessAcrossFrames(t *testing.T) {
	const n = 500
	// A small frame size forces many frames, which is the case that exercises
	// the index. At the default size this whole segment would be one frame and
	// the seek path would never run.
	raw, _ := write(t, n, FrameBytes(8192))
	dir := t.TempDir()
	path := filepath.Join(dir, "00000.jsonl.zst")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open[*doc.Document](path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if s.Len() != n {
		t.Fatalf("segment holds %d documents, want %d", s.Len(), n)
	}
	if len(s.Index().Frames) < 5 {
		t.Fatalf("segment has %d frames, expected the small frame size to produce several", len(s.Index().Frames))
	}

	// Out of order on purpose, including the frame boundaries and both ends.
	for _, i := range []int{n - 1, 0, 250, 1, n - 2, 137, 137, 42} {
		d, err := s.At(i)
		if err != nil {
			t.Fatalf("At(%d): %v", i, err)
		}
		if d.DocID != sample(i).DocID {
			t.Errorf("At(%d) returned the wrong document", i)
		}
	}

	for _, i := range []int{-1, n, n + 1000} {
		if _, err := s.At(i); err == nil {
			t.Errorf("At(%d) succeeded on an out of range position", i)
		}
	}
}

func TestAllIteratesInOrder(t *testing.T) {
	const n = 120
	raw, _ := write(t, n, FrameBytes(4096))
	s, err := OpenReaderAt[*doc.Document](bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("OpenReaderAt: %v", err)
	}
	defer func() { _ = s.Close() }()

	i := 0
	for d, err := range s.All() {
		if err != nil {
			t.Fatalf("All at %d: %v", i, err)
		}
		if d.DocID != sample(i).DocID {
			t.Fatalf("All returned document %d out of order", i)
		}
		i++
	}
	if i != n {
		t.Errorf("All yielded %d documents, want %d", i, n)
	}
}

func TestContentHashIsStableAndSensitive(t *testing.T) {
	// The manifest records this hash, so two runs over the same input have to
	// agree and any change to the input has to show.
	a, ha := write(t, 30)
	b, hb := write(t, 30)
	if !bytes.Equal(a, b) {
		t.Error("two identical runs produced different bytes")
	}
	if ha != hb {
		t.Errorf("two identical runs produced different hashes: %s and %s", ha, hb)
	}
	_, hc := write(t, 31)
	if hc == ha {
		t.Error("a segment with one more document has the same hash")
	}
	if ha.IsZero() {
		t.Error("the content hash is zero after Close")
	}
}

func TestHashIsZeroBeforeClose(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter[*doc.Document](&buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(sample(0)); err != nil {
		t.Fatal(err)
	}
	if !w.Hash().IsZero() {
		t.Error("Hash returned a value before Close, which would be a hash of a partial segment")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if w.Hash().IsZero() {
		t.Error("Hash is still zero after Close")
	}
}

func TestAppendEnforcesTheIngestContract(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter[*doc.Document](&buf)
	if err != nil {
		t.Fatal(err)
	}
	d := sample(0)
	d.LicenseClass = doc.LicenseUnknown
	if err := w.Append(d); !errors.Is(err, doc.ErrIncomplete) {
		t.Fatalf("Append of a contract violation gave %v, want ErrIncomplete", err)
	}
	if w.Count() != 0 {
		t.Error("a rejected document was counted")
	}
}

func TestUnvalidatedAcceptsRejects(t *testing.T) {
	// The reject store holds documents that failed the contract, so it is the
	// one caller that has to be able to write them.
	var buf bytes.Buffer
	w, err := NewWriter[*doc.Document](&buf, Unvalidated())
	if err != nil {
		t.Fatal(err)
	}
	d := sample(0)
	d.LicenseClass = doc.LicenseUnknown
	d.Host = ""
	if err := w.Append(d); err != nil {
		t.Fatalf("the unvalidated writer rejected a document anyway: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if w.Count() != 1 {
		t.Errorf("Count is %d, want 1", w.Count())
	}
}

func TestAppendAfterCloseFails(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter[*doc.Document](&buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(sample(0)); err == nil {
		t.Error("appending to a closed segment succeeded")
	}
	if err := w.Close(); err != nil {
		t.Errorf("closing twice returned %v, want nil", err)
	}
}

func TestEmptySegment(t *testing.T) {
	raw, hash := write(t, 0)
	if hash.IsZero() {
		t.Error("an empty segment has no content hash")
	}
	s, err := OpenReaderAt[*doc.Document](bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("OpenReaderAt on an empty segment: %v", err)
	}
	defer func() { _ = s.Close() }()
	if s.Len() != 0 {
		t.Errorf("an empty segment reports %d documents", s.Len())
	}
	for range s.All() {
		t.Fatal("All yielded a document from an empty segment")
	}
}

func TestOpenRejectsNonSegments(t *testing.T) {
	cases := map[string][]byte{
		"empty file":   {},
		"short file":   []byte("gao"),
		"random bytes": bytes.Repeat([]byte{0x17}, 512),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := OpenReaderAt[*doc.Document](bytes.NewReader(raw), int64(len(raw)))
			if err == nil {
				t.Fatal("a file that is not a segment opened as one")
			}
			if !errors.Is(err, ErrNotASegment) {
				t.Errorf("error does not wrap ErrNotASegment: %v", err)
			}
		})
	}
}

func TestOpenRejectsATruncatedSegment(t *testing.T) {
	// Truncation is the realistic corruption: a run that died partway through
	// leaves a file with frames and no index, and it must not open as a short
	// but valid segment.
	raw, _ := write(t, 100, FrameBytes(4096))
	cut := raw[:len(raw)-40]
	if _, err := OpenReaderAt[*doc.Document](bytes.NewReader(cut), int64(len(cut))); err == nil {
		t.Fatal("a truncated segment opened successfully")
	}
}

func TestOpenReportsAMissingFile(t *testing.T) {
	if _, err := Open[*doc.Document](filepath.Join(t.TempDir(), "nope.jsonl.zst")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("opening a missing file gave %v", err)
	}
}

func BenchmarkAppend(b *testing.B) {
	d := sample(0)
	for b.Loop() {
		w, err := NewWriter[*doc.Document](io.Discard)
		if err != nil {
			b.Fatal(err)
		}
		for range 100 {
			if err := w.Append(d); err != nil {
				b.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRandomRead(b *testing.B) {
	var buf bytes.Buffer
	w, _ := NewWriter[*doc.Document](&buf, FrameBytes(1<<20))
	for i := range 5000 {
		if err := w.Append(sample(i)); err != nil {
			b.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		b.Fatal(err)
	}
	s, err := OpenReaderAt[*doc.Document](bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		if _, err := s.At((i * 1237) % 5000); err != nil {
			b.Fatal(err)
		}
		i++
	}
}
