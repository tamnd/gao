package vo

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

// document returns a document that satisfies the ingest contract, so that the
// tests below fail for the reason they are testing rather than because the
// fixture was thin.
func document(text string) *doc.Document {
	d := &doc.Document{
		DocID: doc.SumString(text),
		RawID: doc.SumString("raw:" + text),
		Text:  text,
		Provenance: doc.Provenance{
			Source:          doc.SourceHPLT3,
			SourceLocator:   "shard=0007,offset=1234",
			URL:             "https://vnexpress.net/bai-viet-mau-1234567.html",
			Host:            "vnexpress.net",
			FetchedAt:       time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC),
			MediaType:       "text/html",
			Extractor:       "trafilatura@1.12.2",
			PipelineVersion: "0.1.0",
		},
		Language: doc.Language{Lang: "vie", LangScore: 0.99, Diacritics: "present"},
		Licensing: doc.Licensing{
			LicenseClass:    doc.LicenseOpen,
			LicenseEvidence: "hplt3 corpus release, cc0",
		},
		Shape: doc.Shape{NChars: uint32(len([]rune(text)))},
	}
	d.SchemaVersion = doc.SchemaVersion
	return d
}

func TestRejectRoundTripsThroughASegment(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	d := document("Chủ tịch nước ký quyết định về việc bổ nhiệm nhân sự cấp cao.")
	if err := w.Reject(d, "lang@0.3.1", ReasonLanguage, "lang_score=0.41"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

	got, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.Text != d.Text {
		t.Errorf("text is %q, want %q", got.Text, d.Text)
	}
	if got.Stage != "lang@0.3.1" || got.Reason != ReasonLanguage {
		t.Errorf("rejection is %s/%s, want lang@0.3.1/language", got.Stage, got.Reason)
	}
	if got.Detail != "lang_score=0.41" {
		t.Errorf("detail is %q, want lang_score=0.41", got.Detail)
	}
	// The point of embedding the document is that a readmitted reject needs no
	// conversion, so the document it carries has to still pass the contract.
	if err := got.Document.Admit(); err != nil {
		t.Errorf("the document did not survive the round trip: %v", err)
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("second Next returned %v, want io.EOF", err)
	}
}

func TestRejectKeepsTheDocumentJSONFlat(t *testing.T) {
	d := document("Một đoạn văn bản tiếng Việt đủ dài để vượt qua ngưỡng tối thiểu.")
	r := &Reject{Document: *d, Stage: "qual@1.0.0", Reason: ReasonQuality, Detail: "gao_qual=0.11"}

	var columns map[string]any
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := json.Unmarshal(b, &columns); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, want := range []string{"doc_id", "text", "source", "lang", "reject_stage", "reject_reason"} {
		if _, ok := columns[want]; !ok {
			t.Errorf("column %q is missing, so the record is not flat", want)
		}
	}
	if _, ok := columns["Document"]; ok {
		t.Error("the embedded document became a nested object, which breaks the Parquet mapping")
	}
}

func TestAdmitRequiresAnActionableRejection(t *testing.T) {
	good := document("Văn bản mẫu dùng cho kiểm thử kho vỏ của dự án gạo.")

	cases := []struct {
		name string
		fix  func(*Reject)
		want string
	}{
		{"no stage", func(r *Reject) { r.Stage = "" }, "reject_stage"},
		{"undefined reason", func(r *Reject) { r.Reason = "vibes" }, "not a defined reason"},
		{"no identity", func(r *Reject) { r.DocID, r.RawID = doc.Hash{}, doc.Hash{} }, "trace it back"},
		{"elided but still carrying text", func(r *Reject) { r.Elided = true }, "still carries text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reject{Document: *good, Stage: "dedup@2.1.0", Reason: ReasonDuplicate}
			tc.fix(r)
			err := r.Admit()
			if err == nil {
				t.Fatal("Admit accepted the rejection")
			}
			if !errors.Is(err, ErrNotRejectable) {
				t.Errorf("error does not wrap ErrNotRejectable: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error is %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestAdmitAcceptsADocumentThatFailedTheIngestContract(t *testing.T) {
	// This is the case the reject store exists for. A document with a broken
	// identity and no provenance cannot go into kho, and has to go into vo, or
	// the pipeline has no record that it ever saw it.
	r := &Reject{
		Document: doc.Document{RawID: doc.SumString("some warc payload"), Text: "\xff\xfe not utf-8"},
		Stage:    "extract@0.9.0",
		Reason:   ReasonEncoding,
		Detail:   "no transcoder claimed the bytes",
	}
	if err := r.Document.Admit(); err == nil {
		t.Fatal("the fixture passes the ingest contract, so it does not test anything")
	}
	if err := r.Admit(); err != nil {
		t.Errorf("the reject store refused a document that failed the contract: %v", err)
	}
}

func TestSamplingElidesTextAndIsDeterministic(t *testing.T) {
	const n = 400
	const fraction = 0.25

	write := func() (map[string]bool, int) {
		var buf bytes.Buffer
		w, err := NewWriter(&buf, fraction, kho.FrameBytes(4096))
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		for i := range n {
			d := document(strings.Repeat("Tiếng Việt là ngôn ngữ chính thức. ", 2) + string(rune('a'+i%26)) + strings.Repeat("x", i))
			if err := w.Reject(d, "qual@1.0.0", ReasonQuality, ""); err != nil {
				t.Fatalf("Reject %d: %v", i, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		kept := make(map[string]bool)
		full := 0
		r, err := NewReader(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("NewReader: %v", err)
		}
		defer func() { _ = r.Close() }()
		for {
			rec, err := r.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if rec.Elided {
				if rec.Text != "" {
					t.Fatal("an elided reject still carries its text")
				}
				continue
			}
			if rec.Text == "" {
				t.Fatal("a reject that was not elided has no text")
			}
			full++
			kept[rec.DocID.String()] = true
		}
		return kept, full
	}

	first, full := write()
	// Hash based sampling is not exact, so the tolerance is generous. What is
	// being tested is that the fraction is roughly honored, not that it is
	// stratified.
	if want := int(fraction * n); full < want/2 || full > want*2 {
		t.Errorf("kept the text of %d rejects out of %d, want about %d", full, n, want)
	}
	second, _ := write()
	if len(first) != len(second) {
		t.Fatalf("two runs kept %d and %d documents", len(first), len(second))
	}
	for id := range first {
		if !second[id] {
			t.Fatalf("document %s kept its text on one run and not the other", id)
		}
	}
}

func TestKeepsTextHonorsTheEndpoints(t *testing.T) {
	id := doc.SumString("bất kỳ tài liệu nào")
	if KeepsText(id, 0) {
		t.Error("a zero sample kept text")
	}
	if !KeepsText(id, 1) {
		t.Error("a full sample elided text")
	}
	if KeepsText(id, -1) {
		t.Error("a negative sample kept text")
	}
}

func TestCountsBreakDownByReason(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 0)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	plan := []Reason{ReasonQuality, ReasonQuality, ReasonDuplicate, ReasonLanguage, ReasonQuality}
	for i, reason := range plan {
		d := document("Tài liệu thứ " + string(rune('a'+i)) + " trong tập kiểm thử của dự án.")
		if err := w.Reject(d, "mixer@0.2.0", reason, ""); err != nil {
			t.Fatalf("Reject %d: %v", i, err)
		}
	}
	counts := w.Counts()
	if counts[ReasonQuality] != 3 || counts[ReasonDuplicate] != 1 || counts[ReasonLanguage] != 1 {
		t.Errorf("counts are %v, want quality=3 duplicate=1 language=1", counts)
	}
	if w.Count() != len(plan) {
		t.Errorf("Count is %d, want %d", w.Count(), len(plan))
	}
	// The map is a copy, so a caller holding it cannot corrupt the writer.
	counts[ReasonQuality] = 99
	if w.Counts()[ReasonQuality] != 3 {
		t.Error("Counts handed out the writer's own map")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if w.Hash().IsZero() {
		t.Error("the segment hash is zero after Close")
	}
}

func TestRejectRefusesANilDocument(t *testing.T) {
	w, err := NewWriter(io.Discard, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Reject(nil, "stage@1.0.0", ReasonQuality, ""); !errors.Is(err, ErrNotRejectable) {
		t.Errorf("Reject(nil) returned %v, want ErrNotRejectable", err)
	}
}

func TestEveryReasonIsListedAndDescribed(t *testing.T) {
	listed := make(map[Reason]bool)
	for _, r := range Reasons() {
		if !r.Valid() {
			t.Errorf("Reasons lists %q, which Valid rejects", r)
		}
		if listed[r] {
			t.Errorf("Reasons lists %q twice", r)
		}
		listed[r] = true
	}
	if len(listed) != len(reasons) {
		t.Errorf("Reasons lists %d of %d defined reasons", len(listed), len(reasons))
	}
	if got := Reason("nonsense").Describe(); !strings.Contains(got, "nonsense") {
		t.Errorf("Describe on an undefined reason returned %q", got)
	}
}
