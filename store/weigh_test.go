package store

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tamnd/gao/doc"
)

// weighed writes a part holding n documents and weighs it.
func weighed(t *testing.T, d Dataset, n int) PartWeight {
	t.Helper()
	docs := make([]*doc.Document, 0, n)
	for i := range n {
		rec := sample(i)
		if !d.Admits(rec.LicenseClass) {
			rec.LicenseClass = doc.LicenseRestricted
		}
		docs = append(docs, rec)
	}
	path, _ := writePart(t, d, docs...)
	w, err := WeighPart(path)
	if err != nil {
		t.Fatalf("WeighPart: %v", err)
	}
	return w
}

func TestWeighingAPartReadsItsFooterAndNotItsPages(t *testing.T) {
	w := weighed(t, textDataset(t), 200)

	if w.Rows != 200 {
		t.Errorf("the weight says %d rows and the part holds 200", w.Rows)
	}
	if w.Footer >= w.Bytes {
		t.Errorf("weighing a %d byte part read %d bytes, which is the whole file rather than its footer", w.Bytes, w.Footer)
	}
	compressed, uncompressed := w.Weight()
	if compressed <= 0 || compressed > w.Bytes {
		t.Errorf("the columns claim %d bytes of a %d byte file", compressed, w.Bytes)
	}
	if uncompressed <= compressed {
		t.Errorf("%d bytes of text compressed to %d, which is not compression", uncompressed, compressed)
	}
}

func TestAColumnIsWeighedOverEveryRowGroupItIsSpreadOver(t *testing.T) {
	w := weighed(t, textDataset(t), 40)

	names := make([]string, 0, len(w.Columns))
	for _, c := range w.Columns {
		if c.Compressed <= 0 {
			t.Errorf("%s weighs %d bytes, and every column is present on every row", c.Name, c.Compressed)
		}
		if slices.Contains(names, c.Name) {
			t.Errorf("%s was weighed twice rather than summed over its chunks", c.Name)
		}
		names = append(names, c.Name)
	}

	columns, err := PartColumns(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	slices.Sort(columns)
	if !slices.Equal(names, columns) {
		t.Errorf("the weighed columns are not the columns the file carries:\n  %v\n  %v", names, columns)
	}

	// The heaviest column first is what makes the reading readable, since the
	// question a footprint answers is which column is the release.
	for i := 1; i < len(w.Columns); i++ {
		if w.Columns[i-1].Compressed < w.Columns[i].Compressed {
			t.Fatalf("%s at %d bytes sorted above %s at %d",
				w.Columns[i-1].Name, w.Columns[i-1].Compressed, w.Columns[i].Name, w.Columns[i].Compressed)
		}
	}
	// Which column comes out heaviest is a fact about the data rather than
	// about the format, and at forty short documents the two identity hashes
	// weigh more than the prose does, since 32 bytes of blake3 compress to 32
	// bytes. Over a release it is the other way around, which is the reading
	// the footprint is taken for.
	if !slices.Contains(names, TextColumn) {
		t.Error("a repo that carries text was weighed without a text column")
	}
}

func TestAPartThatWithholdsTextIsWeighedWithoutTheColumn(t *testing.T) {
	w := weighed(t, urlDataset(t), 20)
	for _, c := range w.Columns {
		if c.Name == TextColumn {
			t.Fatalf("a repo that withholds text was weighed with %d bytes of it", c.Compressed)
		}
	}
	if w.Metadata["gao.snapshot"] != stamp.Snapshot {
		t.Errorf("the weight does not carry the snapshot the part belongs to: %v", w.Metadata)
	}
	if w.Metadata["gao.box"] != stamp.Box {
		t.Errorf("the weight does not carry the box that wrote the part: %v", w.Metadata)
	}
}

func TestWeighingSomethingThatIsNotAPart(t *testing.T) {
	if _, err := WeighPart(filepath.Join(t.TempDir(), "nothing.parquet")); err == nil {
		t.Error("a file that is not there was weighed")
	}

	path := filepath.Join(t.TempDir(), "truncated.parquet")
	if err := os.WriteFile(path, []byte("PAR1 and then nothing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WeighPart(path); err == nil {
		t.Error("a file with no footer was weighed")
	}
}
