package kho

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/parquet-go/parquet-go"
)

// Reading how long the documents are without reading the documents.
//
// The length distribution of a release decides whether it can train a long
// context at all, and it is a question about two columns. Parquet is columnar
// so that a question about two columns costs two columns, and text is 96% of
// what a part weighs, so scanning the rows to add up a length reads the corpus
// to count it. That is the difference between a distribution a box can measure
// off the store and one that needs the release resident on the disk taking the
// measurement.
//
// What was read is returned rather than asserted, for the same reason
// [WeighPart] counts its footer reads: the saving is the whole point of the
// function and an unmeasured claim about it is the sort of thing that quietly
// stops being true when a library changes how it buffers.

// A Length is one document's length and where it came from.
//
// The fields carry the same column names as the matching fields of [Row], which
// is what makes this a projection of the part rather than a second format.
type Length struct {
	Source  string `parquet:"source,dict"`
	NChars  uint32 `parquet:"n_chars"`
	NTokens uint32 `parquet:"n_tokens"`
}

// sizedReaderAt is a counted read that knows how long the file is, which is what
// the row reader asks for and what a plain [io.ReaderAt] cannot answer.
type sizedReaderAt struct {
	countingReaderAt
	size int64
}

func (s *sizedReaderAt) Size() int64 { return s.size }

// ScanLengths reads the length columns of a part and hands each document's
// length to fn, returning the bytes it read to do it.
//
// Returning an error from fn stops the read and comes back out of here
// unwrapped, so a caller can stop early on its own terms.
func ScanLengths(path string, fn func(Length) error) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return 0, err
	}
	counted := &sizedReaderAt{countingReaderAt: countingReaderAt{r: f}, size: stat.Size()}
	if _, err := parquet.OpenFile(counted, stat.Size()); err != nil {
		return counted.n, fmt.Errorf("kho: opening %s: %w", path, err)
	}

	rd := parquet.NewGenericReader[Length](counted)
	defer func() { _ = rd.Close() }()
	buf := make([]Length, 256)
	for {
		n, err := rd.Read(buf)
		for i := range buf[:n] {
			if err := fn(buf[i]); err != nil {
				return counted.n, err
			}
			buf[i] = Length{}
		}
		if errors.Is(err, io.EOF) || n == 0 {
			return counted.n, nil
		}
		if err != nil {
			return counted.n, fmt.Errorf("kho: reading %s: %w", path, err)
		}
	}
}
