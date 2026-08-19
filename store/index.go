package store

// The parts index.
//
// A working repo grows one part at a time from four boxes over weeks, and the
// first question anybody asks of it is what is in there: which sources, which
// revision of each, how many documents landed, and whether the source they want
// is finished or halfway through. Answering that by opening the Parquet is a
// quarter of a terabyte of question. Answering it out of a forty kilobyte CSV
// sitting beside the Parquet is not, and the CSV is the only artifact in the
// repo that a reader with no Parquet reader at all can still use.
//
// One row per part, and the rows are generated from the parts rather than from
// the runs that wrote them. A run that died between pushing a part and writing
// down that it had is exactly the case an index has to be right about, and a
// run's own log is the one source that cannot be right about it.

import (
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strconv"
)

// IndexName is the file the parts index lives in on the Hub. It is at the root
// rather than under [DataDir] because everything under there is loaded by the
// configs the card declares, and a CSV in with the Parquet is a config that
// fails to parse.
const IndexName = "parts.csv"

// indexHeader is the first line of the file, which is also the field order.
var indexHeader = []string{"source", "snapshot", "file", "part", "path", "documents", "bytes"}

// An Indexed is one part as the index describes it.
type Indexed struct {
	// Source is the corpus the part was ingested from, and Snapshot is that
	// source with the revision it was pinned at. Both are in the path, and both
	// are columns anyway, because the point of the file is to be readable by
	// something that is not going to parse our paths.
	Source   string
	Snapshot string

	// File is the input file of the source the part came out of, and Part is
	// which part of that file this is. Together they say whether a source is
	// finished: the highest file reached, and no gaps below it.
	File int
	Part int

	// Path is where the part is in the repo, so that a row can be turned back
	// into a read without reconstructing the layout.
	Path string

	// Documents is the part's row count, read from its Parquet footer rather
	// than from what a run reported writing.
	Documents int64

	// Bytes is the part on the Hub's storage, which is what a download costs.
	Bytes int64
}

// WriteIndex writes the index as CSV, sorted by path so that two runs over an
// unchanged repo produce the same bytes and pushing the second one is a no-op.
func WriteIndex(w io.Writer, rows []Indexed) error {
	sorted := slices.Clone(rows)
	slices.SortFunc(sorted, func(a, b Indexed) int {
		switch {
		case a.Path < b.Path:
			return -1
		case a.Path > b.Path:
			return 1
		}
		return 0
	})

	c := csv.NewWriter(w)
	if err := c.Write(indexHeader); err != nil {
		return err
	}
	for _, r := range sorted {
		if err := c.Write([]string{
			r.Source, r.Snapshot,
			strconv.Itoa(r.File), strconv.Itoa(r.Part), r.Path,
			strconv.FormatInt(r.Documents, 10), strconv.FormatInt(r.Bytes, 10),
		}); err != nil {
			return err
		}
	}
	c.Flush()
	return c.Error()
}

// ReadIndex reads back what [WriteIndex] wrote.
//
// It refuses a file whose header is not the one written here rather than
// reading by position, because an index with a column inserted would otherwise
// come back with the document counts silently shifted into the byte counts.
func ReadIndex(r io.Reader) ([]Indexed, error) {
	c := csv.NewReader(r)
	c.FieldsPerRecord = len(indexHeader)

	head, err := c.Read()
	if err != nil {
		return nil, fmt.Errorf("kho: reading the %s header: %w", IndexName, err)
	}
	if !slices.Equal(head, indexHeader) {
		return nil, fmt.Errorf("kho: %s has the columns %v and this build writes %v", IndexName, head, indexHeader)
	}

	var out []Indexed
	for line := 2; ; line++ {
		rec, err := c.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("kho: reading %s: %w", IndexName, err)
		}
		row := Indexed{Source: rec[0], Snapshot: rec[1], Path: rec[4]}
		for _, f := range []struct {
			name string
			text string
			into any
		}{
			{"file", rec[2], &row.File},
			{"part", rec[3], &row.Part},
			{"documents", rec[5], &row.Documents},
			{"bytes", rec[6], &row.Bytes},
		} {
			n, err := strconv.ParseInt(f.text, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("kho: %s line %d has %s = %q, which is not a number", IndexName, line, f.name, f.text)
			}
			switch into := f.into.(type) {
			case *int:
				*into = int(n)
			case *int64:
				*into = n
			}
		}
		out = append(out, row)
	}
}

// A SourceIndex is one source's parts summed, which is the row a card prints
// and the answer to what is in the repo.
type SourceIndex struct {
	Source string

	// Snapshots are the revisions of this source the repo holds, newest last as
	// the paths sort. There is normally one. Two means a source was re-pinned
	// and the old parts have not been swept, which is a thing a reader has to be
	// told rather than left to discover by counting a document twice.
	Snapshots []string

	// Files is how many input files of the source have parts here, and Parts is
	// how many parts they came to.
	Files int
	Parts int

	Documents int64
	Bytes     int64
}

// BySource sums the index per source, largest first.
func BySource(rows []Indexed) []SourceIndex {
	at := map[string]int{}
	var out []SourceIndex
	files := map[string]map[int]bool{}
	snaps := map[string]map[string]bool{}

	for _, r := range rows {
		i, ok := at[r.Source]
		if !ok {
			i = len(out)
			at[r.Source] = i
			out = append(out, SourceIndex{Source: r.Source})
			files[r.Source] = map[int]bool{}
			snaps[r.Source] = map[string]bool{}
		}
		out[i].Parts++
		out[i].Documents += r.Documents
		out[i].Bytes += r.Bytes
		files[r.Source][r.File] = true
		snaps[r.Source][r.Snapshot] = true
	}

	for i := range out {
		s := &out[i]
		s.Files = len(files[s.Source])
		for snapshot := range snaps[s.Source] {
			s.Snapshots = append(s.Snapshots, snapshot)
		}
		slices.Sort(s.Snapshots)
	}
	slices.SortFunc(out, func(a, b SourceIndex) int {
		switch {
		case a.Documents > b.Documents:
			return -1
		case a.Documents < b.Documents:
			return 1
		case a.Source < b.Source:
			return -1
		case a.Source > b.Source:
			return 1
		}
		return 0
	})
	return out
}

// Total sums an index, which is the headline the repo gets to claim.
func Total(rows []Indexed) (documents, bytes int64) {
	for _, r := range rows {
		documents += r.Documents
		bytes += r.Bytes
	}
	return documents, bytes
}
