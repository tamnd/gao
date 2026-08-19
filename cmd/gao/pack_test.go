package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/store"
)

// packDoc is a document with enough Vietnamese on it to be worth compressing.
func packDoc(i int) *doc.Document {
	text := fmt.Sprintf(
		"Bài viết số %d. Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc. "+
			"Nội dung của tài liệu này đủ dài để vượt qua ngưỡng tối thiểu của hợp đồng nhập liệu.", i)
	d := &doc.Document{
		RawID:         doc.SumString("raw:" + text),
		Text:          text,
		SchemaVersion: doc.SchemaVersion,
		Provenance: doc.Provenance{
			Source:          doc.SourceCrawl,
			SourceLocator:   fmt.Sprintf("gao-crawl-2026-09/00001.warc.gz@%d+4096", i*4096),
			URL:             fmt.Sprintf("https://vnexpress.net/thoi-su/bai-viet-%d.html", i),
			Host:            "vnexpress.net",
			FetchedAt:       time.Date(2026, 9, 14, 3, 22, 11, 0, time.UTC),
			MediaType:       "text/html",
			Extractor:       "go-trafilatura@1.4.0",
			PipelineVersion: "0.1.0",
		},
		Language:  doc.Language{Lang: "vie", LangScore: 0.997, Diacritics: "present"},
		Licensing: doc.Licensing{LicenseClass: doc.LicenseOpen, LicenseEvidence: "robots allow, no TDM reservation"},
	}
	d.DocID = doc.SumString(d.Text)
	d.NChars = uint32(utf8.RuneCountInString(d.Text))
	return d
}

// packParts writes n parts of 50 documents each under one snapshot and returns
// their paths.
func packParts(t *testing.T, dataset string, snapshot string, n int) []string {
	t.Helper()
	d, ok := store.Lookup(dataset)
	if !ok {
		t.Fatalf("%s is not in the dataset table", dataset)
	}
	dir := t.TempDir()
	stamp := store.Stamp{Snapshot: snapshot, Stage: "kho@0.1.0", Box: "server3"}
	paths := make([]string, 0, n)
	for file := range n {
		rel := store.StagePath(snapshot, file, 0)
		p, err := store.CreatePart(dir, rel, d, stamp)
		if err != nil {
			t.Fatal(err)
		}
		for i := range 50 {
			rec := packDoc(file*50 + i)
			if !d.Admits(rec.LicenseClass) {
				rec.LicenseClass = doc.LicenseRestricted
			}
			if err := p.Append(rec); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.Close(); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, filepath.Join(dir, filepath.FromSlash(rel)))
	}
	return paths
}

func TestPackWeighsAReleaseColumnByColumn(t *testing.T) {
	args := append([]string{"pack"}, packParts(t, "vietnamese-web-text", "gao-v1.0", 3)...)
	out, errOut, code := exec(t, args...)
	// Three parts of fifty short documents spend most of their bytes on the
	// identity and provenance columns, so the metadata line fails and the
	// command says so rather than rounding it away.
	if code != 2 {
		t.Fatalf("weighing a fixture exited %d: %s\n%s", code, errOut, out)
	}
	for _, want := range []string{
		"column",
		"of release",
		"P06-1, the release on disk",
		"P06-4, the metadata columns",
		"3 shards",
		"150 documents",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "more columns, which -columns prints") {
		t.Errorf("the column table does not say what it folded away:\n%s", out)
	}
	if !strings.Contains(out, "so the smallest box on the fleet can take this reading") {
		t.Errorf("the report does not say what the footer read cost:\n%s", out)
	}
	if !strings.Contains(out, "more expensive than it was predicted to be") {
		t.Errorf("the verdict does not report the metadata miss:\n%s", out)
	}
}

func TestPackRefusesTwoSnapshotsSummedIntoOneRelease(t *testing.T) {
	paths := append(packParts(t, "vietnamese-web-text", "gao-v1.0", 2), packParts(t, "vietnamese-web-text", "gao-v1.1", 1)...)
	out, _, code := exec(t, append([]string{"pack"}, paths...)...)
	if code != 1 {
		t.Fatalf("two snapshots weighed together exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "two snapshots summed read as one release twice the size") {
		t.Errorf("the refusal does not say what the sum would have been:\n%s", out)
	}
	if strings.Contains(out, "P06-1, the release on disk") {
		t.Errorf("the gate was reported against shards that are not one release:\n%s", out)
	}
}

func TestPackRefusesARepoThatWithholdsTextWeighedWithOneThatShipsIt(t *testing.T) {
	paths := append(packParts(t, "vietnamese-web-text", "gao-v1.0", 1), packParts(t, "vietnamese-web-urls", "gao-v1.0", 1)...)
	out, _, code := exec(t, append([]string{"pack"}, paths...)...)
	if code != 1 {
		t.Fatalf("two formats weighed together exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "a repo that withholds a column was weighed with one that ships it") {
		t.Errorf("the refusal does not say what was mixed:\n%s", out)
	}
}

func TestPackNamesTheShardsOutsideTheSizeBand(t *testing.T) {
	paths := packParts(t, "vietnamese-web-text", "gao-v1.0", 7)
	out, _, _ := exec(t, append([]string{"pack"}, paths...)...)
	if !strings.Contains(out, "7 shards outside the band around the 512 MB shard target") {
		t.Errorf("the loose shards are not counted:\n%s", out)
	}
	if !strings.Contains(out, "and 2 more, which -loose prints") {
		t.Errorf("the report does not say what it truncated:\n%s", out)
	}

	all, _, _ := exec(t, append([]string{"pack", "-loose"}, paths...)...)
	if strings.Contains(all, "more, which -loose prints") {
		t.Errorf("-loose still truncated the list:\n%s", all)
	}
}

func TestPackJSONCarriesTheColumnsAndWhatWasRead(t *testing.T) {
	args := append([]string{"pack", "-json", "-name", "gao-v1.0-check"}, packParts(t, "vietnamese-web-text", "gao-v1.0", 2)...)
	out, _, code := exec(t, args...)
	if code != 2 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{`"columns"`, `"metadata_share"`, `"metadata_line"`, `"ceiling"`, `"read"`, `"box"`, `"holds"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the JSON does not carry %s:\n%s", want, out)
		}
	}
	if !strings.Contains(out, `"name": "gao-v1.0-check"`) {
		t.Errorf("-name did not reach the report:\n%s", out)
	}
}

func TestPackWithoutAShardIsAUsageError(t *testing.T) {
	if _, _, code := exec(t, "pack"); code != 2 {
		t.Error("gao pack with no shards did not exit 2")
	}
	if _, _, code := exec(t, "pack", filepath.Join(t.TempDir(), "nothing.parquet")); code != 1 {
		t.Error("a shard that is not there did not exit 1")
	}
}
