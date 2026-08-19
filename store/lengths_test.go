package store

import (
	"errors"
	"testing"

	"github.com/tamnd/gao/doc"
)

// lengths writes a part of n documents and reads their lengths back off it.
func lengths(t *testing.T, n int) ([]Length, int64, PartWeight) {
	t.Helper()
	d := textDataset(t)
	docs := make([]*doc.Document, 0, n)
	for i := range n {
		rec := sample(i)
		if !d.Admits(rec.LicenseClass) {
			rec.LicenseClass = doc.LicenseRestricted
		}
		rec.NTokens = uint32(1000 + i)
		docs = append(docs, rec)
	}
	path, _ := writePart(t, d, docs...)

	var got []Length
	read, err := ScanLengths(path, func(l Length) error {
		got = append(got, l)
		return nil
	})
	if err != nil {
		t.Fatalf("ScanLengths: %v", err)
	}
	w, err := WeighPart(path)
	if err != nil {
		t.Fatalf("WeighPart: %v", err)
	}
	return got, read, w
}

func TestALengthDistributionIsReadWithoutReadingTheText(t *testing.T) {
	got, read, w := lengths(t, 200)

	if len(got) != 200 {
		t.Fatalf("the part holds 200 documents and %d lengths came back", len(got))
	}
	for i, l := range got {
		if l.NTokens != uint32(1000+i) {
			t.Fatalf("document %d came back with %d tokens", i, l.NTokens)
		}
		if l.NChars == 0 || l.Source == "" {
			t.Fatalf("document %d came back with no length or no source: %+v", i, l)
		}
	}

	// The whole reason for the projection. Text is most of what a part weighs,
	// so a length distribution that reads it reads the corpus to count it.
	var text int64
	for _, c := range w.Columns {
		if c.Name == TextColumn {
			text = c.Compressed
		}
	}
	if text == 0 {
		t.Fatal("the part carries no text column, and this test is about not reading it")
	}
	if read >= w.Bytes-text {
		t.Errorf("reading the lengths off a %d byte part read %d bytes, and the text alone is %d of it", w.Bytes, read, text)
	}
}

func TestALengthReaderStopsWhereItsCallerStops(t *testing.T) {
	stop := errors.New("far enough")
	path, _ := writePart(t, textDataset(t), sample(1), sample(2), sample(3))

	seen := 0
	if _, err := ScanLengths(path, func(Length) error {
		seen++
		return stop
	}); !errors.Is(err, stop) {
		t.Fatalf("the reader returned %v rather than what its caller stopped with", err)
	}
	if seen != 1 {
		t.Errorf("the reader handed over %d documents after being told to stop at the first", seen)
	}

	if _, err := ScanLengths(t.TempDir()+"/nothing.parquet", func(Length) error { return nil }); err == nil {
		t.Error("a part that is not there read as a part")
	}
}
