package gat

// Reading the sources that ship Parquet.
//
// The format is columnar and its footer is at the end, so a file is opened by
// reading the tail, and the rows come back a batch at a time out of a [RangeAt]
// that fetches only the pages the reader asks for. What arrives here is already
// a typed Go value rather than a line of JSON, so the mappings in upstream.go
// look the same as the ones for HPLT and MADLAD and only the plumbing differs.
//
// One thing does differ and it is worth naming. A Parquet row has no bytes of its
// own. It is a slice through as many column chunks as the schema is wide, sitting
// in separate pages that may not even be adjacent in the file, so there is no
// equivalent of the JSON line whose hash becomes raw_id. What gao hashes instead
// is the row's fields as it read them, in schema order, which identifies the row
// and is reproducible from the same file and is not a hash of anything the host
// sent as a unit. Two rows with identical values in every column gao reads hash
// the same, which is correct for what raw_id is for.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/parquet-go/parquet-go"

	"github.com/tamnd/gao/doc"
)

// parquetBatch is how many rows are decoded per call into the Parquet reader.
//
// Large enough that the per call cost disappears and small enough that the batch
// itself is not the thing that decides how much memory an ingest holds. A batch
// of documents at FineWeb2's average length is a few megabytes.
const parquetBatch = 256

// RandomDecoder turns one pinned file into documents, reading it out of order.
//
// It exists next to [Decoder] rather than replacing it because most of the
// corpus can be streamed and streaming is what gets verified against a digest.
// A source only reaches for this when its format leaves no choice.
type RandomDecoder interface {
	DecodeAt(p Pinned, f File, r io.ReaderAt, size int64, emit func(*doc.Document) error) error
}

// RandomDecoderFor returns the decoder for a source whose files have to be read
// out of order, which is the ones that ship Parquet.
//
// CulturaX is pinned, ships Parquet, and is not here. It is gated, and the terms
// have not been granted, so nobody working on gao has read a byte of it. A
// mapping written from the dataset card alone would be a guess with a version
// number on it, and the other five were all written against the real file. It
// gets a decoder when it gets a grant.
func RandomDecoderFor(s doc.Source) (RandomDecoder, bool) {
	switch s {
	case doc.SourceFineWeb2:
		return parquetRows[fineweb2]{row: fineweb2Row}, true
	case doc.SourceFinePDFs:
		return parquetRows[finepdfs]{row: finepdfsRow}, true
	case doc.SourceGlotCC:
		return parquetRows[glotcc]{row: glotccRow}, true
	}
	return nil, false
}

// parquetRows decodes a Parquet file whose rows read into T.
type parquetRows[T any] struct {
	row func(r row, in *T) (*doc.Document, error)
}

// DecodeAt implements [RandomDecoder].
func (j parquetRows[T]) DecodeAt(p Pinned, f File, r io.ReaderAt, size int64, emit func(*doc.Document) error) error {
	file, err := parquet.OpenFile(r, size)
	if err != nil {
		return fmt.Errorf("gat: opening %s from %s: %w", f.Path, p.Source, err)
	}

	rows := parquet.NewGenericReader[T](file)
	defer func() { _ = rows.Close() }()

	batch := make([]T, parquetBatch)
	var n int64
	for {
		got, err := rows.Read(batch)
		for i := range got {
			n++
			raw, mErr := json.Marshal(&batch[i])
			if mErr != nil {
				return fmt.Errorf("%w: %s row %d: %w", ErrBadRow, f.Path, n, mErr)
			}
			d, rErr := j.row(row{Pin: p, File: f, Line: n, Raw: raw}, &batch[i])
			if rErr != nil {
				return fmt.Errorf("%w: %s row %d: %w", ErrBadRow, f.Path, n, rErr)
			}
			if eErr := emit(d); eErr != nil {
				return eErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("gat: reading %s from %s at row %d: %w", f.Path, p.Source, n, err)
		}
	}
}
