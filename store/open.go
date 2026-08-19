package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"

	"github.com/klauspost/compress/zstd"
	"github.com/tamnd/gao/doc"
)

// Segment is a segment opened for random access. It holds the index in memory
// and decompresses one frame at a time, keeping the most recent frame, so a
// sequential walk decompresses each frame once and a random read decompresses
// only the frame it lands in.
//
// A Segment is not safe for concurrent use. Open it once per goroutine; the
// index is small and the file handle is cheap.
type Segment[P Record[T], T any] struct {
	src   io.ReaderAt
	close func() error
	index Index
	dec   *zstd.Decoder

	// held is the most recently decoded frame, kept because the two access
	// patterns that matter are a sequential walk and a burst of reads into the
	// same neighborhood.
	held     int
	heldDocs [][]byte
}

// Open opens a segment file for random access.
func Open[P Record[T], T any](path string) (*Segment[P, T], error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	s, err := OpenReaderAt[P](f, info.Size())
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("kho: %s: %w", path, err)
	}
	s.close = f.Close
	return s, nil
}

// OpenReaderAt opens a segment from anything addressable, which is what makes an
// object store a valid backing for the store as well as a local disk.
func OpenReaderAt[P Record[T], T any](r io.ReaderAt, size int64) (*Segment[P, T], error) {
	index, err := readIndex(r, size)
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("kho: creating the zstd decoder: %w", err)
	}
	return &Segment[P, T]{src: r, index: index, dec: dec, held: -1}, nil
}

// readIndex walks backwards from the end of the segment: the last eight bytes
// give the index length, which places the skippable frame header, which is
// checked before anything in it is believed.
func readIndex(r io.ReaderAt, size int64) (Index, error) {
	var index Index
	if size < trailerSize+skippableHeaderSize {
		return index, ErrNotASegment
	}

	trailer := make([]byte, trailerSize)
	if _, err := r.ReadAt(trailer, size-trailerSize); err != nil {
		return index, fmt.Errorf("kho: reading the trailer: %w", err)
	}
	n := int64(binary.LittleEndian.Uint64(trailer))
	if n < 0 || n > size-trailerSize-skippableHeaderSize {
		return index, fmt.Errorf("%w: trailer claims a %d byte index in a %d byte file", ErrNotASegment, n, size)
	}

	header := make([]byte, skippableHeaderSize)
	headerAt := size - trailerSize - n - skippableHeaderSize
	if _, err := r.ReadAt(header, headerAt); err != nil {
		return index, fmt.Errorf("kho: reading the index frame header: %w", err)
	}
	if got := binary.LittleEndian.Uint32(header[0:4]); got != skippableMagic {
		return index, fmt.Errorf("%w: index frame magic is %#x", ErrNotASegment, got)
	}
	if got, want := binary.LittleEndian.Uint32(header[4:8]), uint32(n)+trailerSize; got != want {
		return index, fmt.Errorf("%w: index frame declares %d payload bytes, trailer implies %d", ErrNotASegment, got, want)
	}

	body := make([]byte, n)
	if _, err := r.ReadAt(body, headerAt+skippableHeaderSize); err != nil {
		return index, fmt.Errorf("kho: reading the index: %w", err)
	}
	if err := json.Unmarshal(body, &index); err != nil {
		return index, fmt.Errorf("kho: decoding the index: %w", err)
	}
	if index.SchemaVersion > doc.SchemaVersion {
		return index, fmt.Errorf("kho: segment is schema version %d, this build understands %d",
			index.SchemaVersion, doc.SchemaVersion)
	}
	return index, nil
}

// Len returns the number of documents in the segment.
func (s *Segment[P, T]) Len() int { return s.index.Documents }

// Index returns the segment index.
func (s *Segment[P, T]) Index() Index { return s.index }

// At returns the document at position i.
func (s *Segment[P, T]) At(i int) (P, error) {
	if i < 0 || i >= s.index.Documents {
		return nil, fmt.Errorf("kho: document %d is outside the segment's %d documents", i, s.index.Documents)
	}
	f := s.frameFor(i)
	if err := s.load(f); err != nil {
		return nil, err
	}
	line := s.heldDocs[i-s.index.Frames[f].First]
	var v T
	if err := json.Unmarshal(line, &v); err != nil {
		return nil, fmt.Errorf("kho: decoding document %d: %w", i, err)
	}
	return P(&v), nil
}

// frameFor returns the index of the frame holding document i. Frames are in
// order and there are few of them, so a binary search is the obvious shape.
func (s *Segment[P, T]) frameFor(i int) int {
	lo, hi := 0, len(s.index.Frames)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if s.index.Frames[mid].First <= i {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// load decompresses frame f unless it is already held.
func (s *Segment[P, T]) load(f int) error {
	if s.held == f {
		return nil
	}
	fr := s.index.Frames[f]
	raw := make([]byte, fr.Bytes)
	if _, err := s.src.ReadAt(raw, fr.Offset); err != nil {
		return fmt.Errorf("kho: reading frame %d: %w", f, err)
	}
	plain, err := s.dec.DecodeAll(raw, nil)
	if err != nil {
		return fmt.Errorf("kho: decompressing frame %d: %w", f, err)
	}
	lines := splitLines(plain)
	if len(lines) != fr.Count {
		return fmt.Errorf("kho: frame %d holds %d documents, the index says %d", f, len(lines), fr.Count)
	}
	s.held, s.heldDocs = f, lines
	return nil
}

// splitLines splits on newlines without allocating a copy of each line, since
// the caller unmarshals immediately and does not retain them.
func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// All iterates the segment in order. Iteration stops at the first error, which
// is yielded with a nil document.
func (s *Segment[P, T]) All() iter.Seq2[P, error] {
	return func(yield func(P, error) bool) {
		for i := range s.index.Documents {
			d, err := s.At(i)
			if !yield(d, err) || err != nil {
				return
			}
		}
	}
}

// Close releases the decoder and the underlying file, if there is one.
func (s *Segment[P, T]) Close() error {
	s.dec.Close()
	if s.close != nil {
		return s.close()
	}
	return nil
}
