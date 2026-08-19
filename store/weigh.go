package store

import (
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/parquet-go/parquet-go"
)

// Weighing a file without reading it.
//
// What a release costs on disk is a question about columns rather than about
// files, and Parquet answers it out of the footer: every column chunk of every
// row group carries the bytes it took compressed and the bytes it would take
// uncompressed. That means a 400 GB release can be weighed by reading a few
// megabytes, which is the difference between a measurement that runs on the
// fleet and one that needs the corpus resident on the box taking it.
//
// The reads are counted rather than assumed, because that claim is the reason
// this exists and an unmeasured claim about how little was read is exactly the
// sort of thing that turns out to be false after somebody changes a library
// version.

// A ColumnWeight is what one column of a file costs, summed over its row
// groups.
type ColumnWeight struct {
	Name string
	// Compressed is what the column takes in the file and Uncompressed is what
	// it would take without the codec. The ratio between them is a property of
	// the data rather than of the writer, which is why both are kept.
	Compressed   int64
	Uncompressed int64
}

// A PartWeight is one file as its footer describes it.
type PartWeight struct {
	Path string
	// Bytes is the file as the filesystem has it, which is the number a disk
	// budget is spent against. Footer is what reading this cost.
	Bytes  int64
	Footer int64
	Rows   int64

	Columns  []ColumnWeight
	Metadata map[string]string
}

// Weight returns the file's compressed and uncompressed totals over all
// columns.
func (p PartWeight) Weight() (compressed, uncompressed int64) {
	for _, c := range p.Columns {
		compressed += c.Compressed
		uncompressed += c.Uncompressed
	}
	return compressed, uncompressed
}

// WeighPart reads a part's footer and returns what its columns cost. The rows
// are not read and the pages are not decompressed.
func WeighPart(path string) (PartWeight, error) {
	f, err := os.Open(path)
	if err != nil {
		return PartWeight{}, err
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return PartWeight{}, err
	}
	counted := &countingReaderAt{r: f}
	pf, err := parquet.OpenFile(counted, stat.Size())
	if err != nil {
		return PartWeight{}, fmt.Errorf("store: opening %s: %w", path, err)
	}

	w := PartWeight{
		Path:     path,
		Bytes:    stat.Size(),
		Rows:     pf.NumRows(),
		Metadata: make(map[string]string),
	}
	for _, kv := range pf.Metadata().KeyValueMetadata {
		w.Metadata[kv.Key] = kv.Value
	}

	// A column is spread over one chunk per row group, so the chunks are summed
	// by name rather than reported per row group. The name is the schema path
	// joined the way the column list prints it, so a weight lines up with what
	// gao store columns says the file carries.
	byName := make(map[string]int)
	for _, rg := range pf.Metadata().RowGroups {
		for _, c := range rg.Columns {
			name := joinPath(c.MetaData.PathInSchema)
			i, ok := byName[name]
			if !ok {
				i = len(w.Columns)
				byName[name] = i
				w.Columns = append(w.Columns, ColumnWeight{Name: name})
			}
			w.Columns[i].Compressed += c.MetaData.TotalCompressedSize
			w.Columns[i].Uncompressed += c.MetaData.TotalUncompressedSize
		}
	}
	slices.SortFunc(w.Columns, func(a, b ColumnWeight) int {
		switch {
		case a.Compressed > b.Compressed:
			return -1
		case a.Compressed < b.Compressed:
			return 1
		}
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	w.Footer = counted.n
	return w, nil
}

// countingReaderAt counts what was read through it, so the claim that weighing
// a release reads its footers and not its pages is measured.
type countingReaderAt struct {
	r io.ReaderAt
	n int64
}

func (c *countingReaderAt) ReadAt(b []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(b, off)
	c.n += int64(n)
	return n, err
}
