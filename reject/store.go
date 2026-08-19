package reject

import (
	"fmt"
	"io"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/store"
)

// sampleBuckets is the resolution of the text sampling decision. Sixty five
// thousand buckets puts the smallest expressible sample at about one in sixty
// five thousand, which is finer than any stage will ask for.
const sampleBuckets = 1 << 16

// KeepsText reports whether a reject with this identity keeps its text at the
// given sample fraction.
//
// The decision comes from the document identity rather than from a random draw,
// for two reasons. A rerun of the same stage over the same input produces the
// same sample, so two runs are comparable. And a document that is rejected twice
// by two different stages is either kept by both or dropped by both, so the
// sample is a coherent subset of the corpus rather than a different subset per
// stage.
func KeepsText(id doc.Hash, fraction float64) bool {
	switch {
	case fraction <= 0:
		return false
	case fraction >= 1:
		return true
	}
	return doc.Shard(id, sampleBuckets) < int(fraction*sampleBuckets)
}

// Writer appends rejections to a reject store segment.
//
// It wraps a [store.Writer] with two things the corpus store does not do: it
// elides the text of the rejects outside the sample, and it counts rejections by
// reason so that the stage that owns the writer can report what it removed
// without a second pass.
type Writer struct {
	seg    *store.Writer[*Reject, Reject]
	sample float64
	counts map[Reason]int
}

// NewWriter returns a Writer over w. The sample fraction is the share of
// rejects that keep their text; the rest are elided. Pass 1 to keep everything,
// which is what a small run or a test wants and what a full pipeline pass cannot
// afford.
func NewWriter(w io.Writer, sample float64, opts ...store.WriterOption) (*Writer, error) {
	// The reject store holds documents that failed the ingest contract, so the
	// contract check has to be off. Reject.Admit is the rule that applies here,
	// and it still runs.
	seg, err := store.NewWriter[*Reject](w, append([]store.WriterOption{store.Unvalidated()}, opts...)...)
	if err != nil {
		return nil, err
	}
	return &Writer{seg: seg, sample: sample, counts: make(map[Reason]int)}, nil
}

// Reject records that stage threw d away for reason. It does not modify d.
func (w *Writer) Reject(d *doc.Document, stage string, reason Reason, detail string) error {
	if d == nil {
		return fmt.Errorf("%w: no document", ErrNotRejectable)
	}
	r := &Reject{Document: *d, Stage: stage, Reason: reason, Detail: detail}
	id := r.DocID
	if id.IsZero() {
		id = r.RawID
	}
	if !KeepsText(id, w.sample) {
		r.Elide()
	}
	if err := w.seg.Append(r); err != nil {
		return err
	}
	w.counts[reason]++
	return nil
}

// Count returns the number of rejections written so far.
func (w *Writer) Count() int { return w.seg.Count() }

// Counts returns the rejections written so far, broken down by reason. The map
// is a copy, so a caller can hold it across further writes.
func (w *Writer) Counts() map[Reason]int {
	out := make(map[Reason]int, len(w.counts))
	for k, v := range w.counts {
		out[k] = v
	}
	return out
}

// Close finishes the segment. It does not close the underlying writer.
func (w *Writer) Close() error { return w.seg.Close() }

// Hash returns the content hash of the segment, which is only meaningful after
// [Writer.Close].
func (w *Writer) Hash() doc.Hash { return w.seg.Hash() }

// Index returns the segment index.
func (w *Writer) Index() store.Index { return w.seg.Index() }

// Segment is a reject store segment opened for random access.
type Segment = store.Segment[*Reject, Reject]

// Open opens a reject store segment.
func Open(path string) (*Segment, error) { return store.Open[*Reject](path) }

// Reader reads a reject store segment in order.
type Reader = store.Reader[*Reject, Reject]

// NewReader returns a Reader over the zstd stream in r.
func NewReader(r io.Reader) (*Reader, error) { return store.NewReader[*Reject](r) }
