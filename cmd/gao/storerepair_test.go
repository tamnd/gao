package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/store"
)

// badPart writes a part the way the crawler used to: with a url_template column
// holding the bytes a percent encoded link decoded to. It goes through
// parquet-go directly because the conversion the crawler uses now refuses to
// produce one, which is the point of the fix and makes this the only way to get
// the file the fix has to clean up.
func badPart(t *testing.T, d store.Dataset, snapshot string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, snapshot+"-00002-00017.parquet")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	row := store.RejectRow{
		Row: store.Row{
			DocID:           doc.SumString("một trang tiếng Việt"),
			SchemaVersion:   doc.SchemaVersion,
			Source:          string(doc.SourceCrawl),
			URL:             "https://tramtrieuthaytramtrieutro.com/blogpost1",
			Host:            "tramtrieuthaytramtrieutro.com",
			URLTemplate:     "tramtrieuthaytramtrieutro.com/blogpost1-Thong-tin-so-v\xe1\xbb\x9b\xffi",
			MediaType:       "text/html",
			Extractor:       "trafilatura@1.12.2",
			PipelineVersion: "0.7.0",
			LicenseClass:    doc.LicenseRestricted.String(),
			Lang:            "vie",
		},
		RejectStage:  "crawl.sift",
		RejectReason: "short",
	}
	w := parquet.NewGenericWriter[store.RejectRow](f,
		store.SchemaFor(d),
		parquet.KeyValueMetadata("gao.snapshot", snapshot),
		parquet.KeyValueMetadata("gao.stage", "crawl@0.7.0"),
		parquet.KeyValueMetadata("gao.box", "server3"),
	)
	if _, err := w.Write([]store.RejectRow{row}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// The eight parts this was written for were already on the Hub and unreadable.
// Repairing one means the bytes come back as text and the row is still there,
// because a repair that dropped the row would be the same loss with a better
// name on it.
func TestARepairedPartIsReadableAndStillHoldsItsRow(t *testing.T) {
	d, ok := store.Lookup("vitweb-rejects")
	if !ok {
		t.Fatal("vitweb-rejects is not a dataset")
	}
	path := badPart(t, d, "web-20260820d")

	bad, snapshot, err := repairPart(path, d)
	if err != nil {
		t.Fatalf("repairPart: %v", err)
	}
	if bad != 1 {
		t.Fatalf("%d rows held bytes that are not text, want 1", bad)
	}
	if snapshot != "web-20260820d" {
		t.Fatalf("the repair read the snapshot as %q", snapshot)
	}

	rows, err := store.ReadRejectPart(path)
	if err != nil {
		t.Fatalf("ReadRejectPart: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("the repaired part holds %d rows, want 1", len(rows))
	}
	got := rows[0]
	if !rowIsText(got.Row) {
		t.Errorf("the repaired part still holds bytes that are not text: %q", got.URLTemplate)
	}
	if !strings.HasPrefix(got.URLTemplate, "tramtrieuthaytramtrieutro.com/blogpost1-Thong-tin-so-v") {
		t.Errorf("the repair lost more than the bad bytes: %q", got.URLTemplate)
	}
	if got.RejectStage != "crawl.sift" || got.RejectReason != "short" {
		t.Errorf("the rejection came back as %s/%s", got.RejectStage, got.RejectReason)
	}

	// The stamp travels inside the file, so a repaired part still says which
	// snapshot and which box it belongs to.
	meta, err := store.PartMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"gao.snapshot": "web-20260820d",
		"gao.stage":    "crawl@0.7.0",
		"gao.box":      "server3",
	} {
		if meta[k] != want {
			t.Errorf("%s came back %q, want %q", k, meta[k], want)
		}
	}
}

// Running the repair over a directory has to be safe, which means a part that
// was always text is left exactly as it was rather than rewritten with the same
// rows and a different digest.
func TestAPartThatIsAlreadyTextIsNotRewritten(t *testing.T) {
	d, ok := store.Lookup("vitweb-rejects")
	if !ok {
		t.Fatal("vitweb-rejects is not a dataset")
	}
	path := badPart(t, d, "web-20260820d")
	if _, _, err := repairPart(path, d); err != nil {
		t.Fatalf("repairPart: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	bad, _, err := repairPart(path, d)
	if err != nil {
		t.Fatalf("second repairPart: %v", err)
	}
	if bad != 0 {
		t.Fatalf("a repaired part still reports %d bad rows", bad)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a part that is already text was rewritten anyway")
	}
}
