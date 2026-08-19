// Package kho is the warehouse: the on-disk store every pipeline stage reads and
// writes through.
//
// The store is JSONL in zstd frames rather than Parquet, and that is a
// deliberate split from the published format. Six producers append to the store
// continuously, across pipeline upgrades that change the schema, and Parquet is
// a bad fit for both of those. The release is Parquet, written once by
// [gao store release], because consumers want columnar reads and predicate
// pushdown. Both carry the same schema and the conversion is lossless.
//
// Segments are seekable. Deduplication at half a billion documents and
// decontamination scans both need to pull one document by position without
// decompressing the segment around it, so documents are grouped into frames of a
// bounded uncompressed size and an index of frame offsets is written at the end.
// The index lives in a zstd skippable frame, which means a plain zstd reader
// walks straight past it and a whole segment decompresses with the command line
// tool as if the index were not there.
package kho

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/klauspost/compress/zstd"
	"github.com/tamnd/gao/doc"
	"github.com/zeebo/blake3"
)

const (
	// skippableMagic is the first zstd skippable frame magic number. A compliant
	// decoder ignores the frame entirely, which is what lets the index ride
	// along inside an otherwise ordinary zstd stream.
	skippableMagic = 0x184D2A50

	// trailerSize is the byte count of the little-endian index length that
	// closes the segment. It is how a reader finds the index frame by seeking
	// backwards from the end of the file.
	trailerSize = 8

	// skippableHeaderSize is the magic number plus the payload length.
	skippableHeaderSize = 8

	// DefaultFrameBytes is the uncompressed size a frame is filled to before it
	// is closed. Larger frames compress better and cost more to seek into: at
	// 4 MiB a random document read decompresses about a thousand neighbors,
	// which is cheap enough to be uninteresting and large enough that zstd's
	// window sees the shared boilerplate of documents from one host.
	DefaultFrameBytes = 4 << 20
)

// ErrNotASegment is returned when a file does not end in a gao segment index.
var ErrNotASegment = errors.New("kho: not a gao segment")

// Record is what a segment can hold. The type parameter is the value type and
// the interface is satisfied by a pointer to it, which is the shape that lets a
// reader allocate one.
//
// Admit is the record's own admission rule. For a document it is the ingest
// contract; for a reject it is the much weaker requirement that the rejection
// have a stage and a reason, since a reject store entry that does not say why it
// was rejected is a document nobody can ever re-examine.
type Record[T any] interface {
	*T
	Admit() error
}

// Frame records where one group of documents lives in a segment.
type Frame struct {
	// Offset is the byte offset of the frame within the segment.
	Offset int64 `json:"offset"`
	// Bytes is the compressed length of the frame.
	Bytes int64 `json:"bytes"`
	// First is the index of the frame's first document within the segment.
	First int `json:"first"`
	// Count is the number of documents in the frame.
	Count int `json:"count"`
}

// Index is the segment trailer. It is small: one entry per few thousand
// documents rather than one per document, which keeps it in memory at any
// corpus size we will reach.
type Index struct {
	SchemaVersion uint16  `json:"schema_version"`
	Documents     int     `json:"documents"`
	Frames        []Frame `json:"frames"`
}

// writerOptions is the settled configuration a [Writer] is built from. It is a
// plain struct rather than methods on the writer because [Writer] is generic and
// an option that named it would have to name its type parameters at every call
// site.
type writerOptions struct {
	frameBytes int
	validate   bool
}

// WriterOption configures a [Writer].
type WriterOption func(*writerOptions)

// FrameBytes sets the uncompressed size a frame is filled to before it closes.
func FrameBytes(n int) WriterOption {
	return func(o *writerOptions) { o.frameBytes = n }
}

// Unvalidated turns off the ingest contract check on append.
//
// Exactly one caller should use this: the reject store, whose whole purpose is
// to hold documents that failed the contract. A pipeline stage that reaches for
// this to make a stubborn document go in is doing the thing the contract exists
// to prevent.
func Unvalidated() WriterOption {
	return func(o *writerOptions) { o.validate = false }
}

// Writer appends documents to a segment.
//
// The segment's content hash is available from [Writer.Hash] after [Writer.Close]
// and is what the manifest records. It is computed over the bytes as they are
// written rather than by re-reading the file, so a segment that was written
// correctly and stored incorrectly is caught by verification rather than
// blessed by it.
type Writer[P Record[T], T any] struct {
	out    io.Writer
	digest *blake3.Hasher

	enc *zstd.Encoder
	// buf accumulates the compressed bytes of the frame being built, because the
	// frame's length is not known until it is closed and the index needs it.
	buf     bytes.Buffer
	pending int // documents in the open frame
	raw     int // uncompressed bytes in the open frame

	frameBytes int
	validate   bool

	offset int64
	index  Index
	closed bool
	hash   doc.Hash
}

// NewWriter returns a Writer that appends documents to w.
func NewWriter[P Record[T], T any](w io.Writer, opts ...WriterOption) (*Writer[P, T], error) {
	cfg := writerOptions{frameBytes: DefaultFrameBytes, validate: true}
	for _, opt := range opts {
		opt(&cfg)
	}
	sw := &Writer[P, T]{
		out:        w,
		digest:     blake3.New(),
		frameBytes: cfg.frameBytes,
		validate:   cfg.validate,
		index:      Index{SchemaVersion: doc.SchemaVersion},
	}
	enc, err := zstd.NewWriter(&sw.buf, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, fmt.Errorf("kho: creating the zstd encoder: %w", err)
	}
	sw.enc = enc
	return sw, nil
}

// Append writes one document. Unless the writer was created with [Unvalidated],
// the document must satisfy the ingest contract, and a document that does not is
// rejected here rather than admitted and discovered later.
func (w *Writer[P, T]) Append(d P) error {
	if w.closed {
		return errors.New("kho: append to a closed segment")
	}
	if w.validate {
		if err := d.Admit(); err != nil {
			return err
		}
	}
	line, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("kho: encoding document %d: %w", w.Count(), err)
	}
	line = append(line, '\n')
	if _, err := w.enc.Write(line); err != nil {
		return fmt.Errorf("kho: writing document %d: %w", w.Count(), err)
	}
	w.pending++
	w.raw += len(line)
	if w.raw >= w.frameBytes {
		return w.flush()
	}
	return nil
}

// flush closes the open frame and records it in the index. A frame with no
// documents is not written, so an empty segment is an index and a trailer.
func (w *Writer[P, T]) flush() error {
	if w.pending == 0 {
		return nil
	}
	if err := w.enc.Close(); err != nil {
		return fmt.Errorf("kho: closing a frame: %w", err)
	}
	n, err := w.emit(w.buf.Bytes())
	if err != nil {
		return err
	}
	w.index.Frames = append(w.index.Frames, Frame{
		Offset: w.offset - n,
		Bytes:  n,
		First:  w.index.Documents,
		Count:  w.pending,
	})
	w.index.Documents += w.pending
	w.pending, w.raw = 0, 0
	w.buf.Reset()
	w.enc.Reset(&w.buf)
	return nil
}

// emit writes b to the output and to the running digest, and advances the
// offset. It returns the number of bytes written.
func (w *Writer[P, T]) emit(b []byte) (int64, error) {
	n, err := w.out.Write(b)
	w.offset += int64(n)
	_, _ = w.digest.Write(b[:n])
	if err != nil {
		return int64(n), fmt.Errorf("kho: writing segment: %w", err)
	}
	return int64(n), nil
}

// Count returns the number of documents appended so far.
func (w *Writer[P, T]) Count() int {
	return w.index.Documents + w.pending
}

// Close flushes the open frame, writes the index, and finalizes the content
// hash. It does not close the underlying writer.
func (w *Writer[P, T]) Close() error {
	if w.closed {
		return nil
	}
	if err := w.flush(); err != nil {
		return err
	}
	w.closed = true

	body, err := json.Marshal(w.index)
	if err != nil {
		return fmt.Errorf("kho: encoding the index: %w", err)
	}

	// The index rides in a zstd skippable frame so that an ordinary zstd reader
	// walks past it. The payload is the index followed by its own length, which
	// is what lets a reader find the frame by seeking backwards from the end of
	// the file without scanning.
	payload := binary.LittleEndian.AppendUint64(body, uint64(len(body)))

	// A skippable frame declares its payload length in a uint32. An index that
	// large would take tens of millions of frames in one segment, which is well
	// past the point where the writer should have rolled over to a new segment,
	// so this is an error rather than a length that silently wraps and produces a
	// file that reads as valid and is not.
	if uint64(len(payload)) > math.MaxUint32 {
		return fmt.Errorf("kho: the index is %d bytes, which does not fit in a skippable frame", len(payload))
	}
	payloadLen := uint32(len(payload))

	header := make([]byte, skippableHeaderSize)
	binary.LittleEndian.PutUint32(header[0:4], skippableMagic)
	binary.LittleEndian.PutUint32(header[4:8], payloadLen)

	if _, err := w.emit(header); err != nil {
		return err
	}
	if _, err := w.emit(payload); err != nil {
		return err
	}
	w.hash = doc.Hash(w.digest.Sum(nil)[:doc.HashSize])
	return nil
}

// Hash returns the content hash of the segment. It is only meaningful after
// [Writer.Close] and is the zero hash before it.
func (w *Writer[P, T]) Hash() doc.Hash {
	return w.hash
}

// Index returns the segment index. It is only complete after [Writer.Close].
func (w *Writer[P, T]) Index() Index {
	return w.index
}

// Reader reads documents from a segment in order. It works on any reader,
// including a pipe, and does not need the index. Use [Open] when the segment is
// a file and random access is wanted.
type Reader[P Record[T], T any] struct {
	dec  *zstd.Decoder
	scan *bufio.Scanner
	n    int
}

// maxDocumentBytes bounds a single JSON record. Vietnamese legal gazettes and
// theses run long, and a scanner that gives up at 64 KiB would drop exactly the
// documents the corpus most wants.
const maxDocumentBytes = 64 << 20

// NewReader returns a Reader over the zstd stream in r.
func NewReader[P Record[T], T any](r io.Reader) (*Reader[P, T], error) {
	dec, err := zstd.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("kho: creating the zstd decoder: %w", err)
	}
	scan := bufio.NewScanner(dec.IOReadCloser())
	scan.Buffer(make([]byte, 0, 64<<10), maxDocumentBytes)
	return &Reader[P, T]{dec: dec, scan: scan}, nil
}

// Next returns the next document, or [io.EOF] when the segment is exhausted.
func (r *Reader[P, T]) Next() (P, error) {
	if !r.scan.Scan() {
		if err := r.scan.Err(); err != nil {
			return nil, fmt.Errorf("kho: reading segment at document %d: %w", r.n, err)
		}
		return nil, io.EOF
	}
	var v T
	if err := json.Unmarshal(r.scan.Bytes(), &v); err != nil {
		return nil, fmt.Errorf("kho: decoding document %d: %w", r.n, err)
	}
	r.n++
	return P(&v), nil
}

// Close releases the decoder.
func (r *Reader[P, T]) Close() error {
	r.dec.Close()
	return nil
}
