package count

// Reading one column out of a part.
//
// Measuring how much two sources have in common means holding the identity of
// every document in each of them, and nothing else. A part carries fifty
// columns and almost all of its bytes are in one of them, the text, which this
// question has no use for at all: two documents are the same document when
// their normalized text hashes the same, and the hash is already a column.
//
// Parquet is columnar, so that is not a saying. The doc_id values of a part sit
// in their own chunk of the file and can be read without touching the pages the
// text is in. Against the store this is the difference between moving a corpus
// back out of the Hub to count it and moving thirty two bytes per document, and
// at four hundred million documents that is around 13 GB rather than around
// 900 GB.
//
// It is also why this reads pages rather than rows. The generic reader assembles
// whole rows, which means reading every column the schema has, and a row that is
// assembled is a row whose text was fetched.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/parquet-go/parquet-go"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/store"
)

// valueBatch is how many column values are decoded per call. A page holds
// thousands and the call is cheap, so this is small enough to stay out of the
// way of the read window and large enough that the per call cost disappears.
const valueBatch = 1024

// ErrNoColumn is returned when a part does not carry the column being read,
// which for doc_id means it was not written by gao.
var ErrNoColumn = errors.New("count: that part has no such column")

// DocIDs calls yield with the identity of every document in a part, reading only
// the column the identities are in.
//
// The part is anything that can be read at a known length, which is a file on
// disk and equally a part in the store read over HTTP. Yielding an error stops
// the read and returns it.
func DocIDs(r io.ReaderAt, size int64, yield func(doc.Hash) error) error {
	f, err := parquet.OpenFile(r, size)
	if err != nil {
		return fmt.Errorf("count: opening a part of %d bytes: %w", size, err)
	}
	return EachDocID(f, yield)
}

// EachDocID is [DocIDs] over a part that is already open, for a caller reading
// more than one column out of the same file.
func EachDocID(f *parquet.File, yield func(doc.Hash) error) error {
	return eachValue(f, store.DocIDColumn, func(v parquet.Value) error {
		b := v.ByteArray()
		if len(b) != doc.HashSize {
			return fmt.Errorf("count: a doc_id in this part is %d bytes rather than %d", len(b), doc.HashSize)
		}
		return yield(doc.Hash(b))
	})
}

// EachUint32 calls yield with every value of a fixed width numeric column.
//
// The shape columns are this shape, and it is why a recount over the whole
// corpus is affordable: four bytes per document per column against a few
// kilobytes of text, before the encoding takes most of even that away.
func EachUint32(f *parquet.File, name string, yield func(uint32) error) error {
	return eachValue(f, name, func(v parquet.Value) error {
		return yield(v.Uint32())
	})
}

// eachValue walks one named column of a part and yields every value in it, in
// row order.
func eachValue(f *parquet.File, name string, yield func(parquet.Value) error) error {
	at, err := columnIndex(f.Schema(), name)
	if err != nil {
		return err
	}
	buf := make([]parquet.Value, valueBatch)
	for _, group := range f.RowGroups() {
		if err := readColumn(group.ColumnChunks()[at], buf, name, yield); err != nil {
			return err
		}
	}
	return nil
}

// columnIndex returns the position of a named leaf column in a schema.
//
// Columns are addressed by position rather than by name once a file is open, and
// the position is the schema's rather than a constant, because a part written by
// an older pipeline has an older schema and the column it wants may have moved.
func columnIndex(s *parquet.Schema, name string) (int, error) {
	for i, path := range s.Columns() {
		if strings.Join(path, ".") == name {
			return i, nil
		}
	}
	return 0, fmt.Errorf("%w: looking for %s", ErrNoColumn, name)
}

// readColumn walks the pages of one column chunk and yields every value in it.
func readColumn(chunk parquet.ColumnChunk, buf []parquet.Value, name string, yield func(parquet.Value) error) error {
	pages := chunk.Pages()
	defer func() { _ = pages.Close() }()

	for {
		page, err := pages.ReadPage()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("count: reading a page of %s: %w", name, err)
		}
		err = readPage(page, buf, name, yield)
		// Released rather than left to the collector because these are pooled,
		// and a pass over a corpus that dropped every page on the floor would be
		// allocating a buffer per page for four hundred million documents.
		parquet.Release(page)
		if err != nil {
			return err
		}
	}
}

func readPage(page parquet.Page, buf []parquet.Value, name string, yield func(parquet.Value) error) error {
	values := page.Values()
	for {
		n, err := values.ReadValues(buf)
		for _, v := range buf[:n] {
			if err := yield(v); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("count: reading %s values: %w", name, err)
		}
	}
}
