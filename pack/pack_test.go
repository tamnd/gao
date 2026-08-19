package pack

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

// part builds a weight the way a footer would describe one, so that the
// arithmetic can be tested at release sizes without writing a release.
func part(path string, bytes int64, text, meta int64) store.PartWeight {
	return store.PartWeight{
		Path:   path,
		Bytes:  bytes,
		Footer: 8 << 10,
		Rows:   100_000,
		Columns: []store.ColumnWeight{
			{Name: Text, Compressed: text, Uncompressed: text * 3},
			{Name: "host", Compressed: meta / 2, Uncompressed: meta * 2},
			{Name: "doc_id", Compressed: meta / 2, Uncompressed: meta / 2},
		},
		Metadata: map[string]string{
			"gao.snapshot":       "gao-v1.0",
			"gao.schema_version": "1",
			"gao.box":            "server3",
		},
	}
}

// release builds n shards of the given size, each spending share of its columns
// on metadata.
func release(n int, size int64, share float64) Release {
	r := Release{Name: "gao-v1.0"}
	for i := range n {
		meta := int64(float64(size) * share)
		r.Parts = append(r.Parts, part(fmt.Sprintf("data/%05d.parquet", i), size, size-meta, meta))
	}
	return r
}

func refuses(t *testing.T, r Release, want string) {
	t.Helper()
	why := r.Blocking()
	if len(why) == 0 {
		t.Fatalf("the release was added up and it should have been refused for %q", want)
	}
	for _, w := range why {
		if strings.Contains(w, want) {
			return
		}
	}
	t.Errorf("no refusal mentions %q, and what came back was:\n  %s", want, strings.Join(why, "\n  "))
}

func TestAReleaseUnderBothLinesHolds(t *testing.T) {
	r := release(700, TargetShard, 0.08)
	if why := r.Blocking(); len(why) > 0 {
		t.Fatalf("a release of seven hundred matched shards was refused: %v", why)
	}
	if !r.Holds() {
		t.Fatalf("a release of %s at %.1f%% metadata does not hold", bytes(r.Bytes()), r.Share()*100)
	}
	if got := r.Rows(); got != 70_000_000 {
		t.Errorf("the release holds %d rows", got)
	}
	if len(r.Loose()) != 0 {
		t.Errorf("%d shards at the target size read as loose", len(r.Loose()))
	}
	if v := r.Verdict(); !strings.Contains(v, "It fits inside the 420.0 GB P06-1 claims") {
		t.Errorf("the verdict does not say the release fits:\n  %s", v)
	}
}

func TestAReleaseOverTheCeilingMissesP061(t *testing.T) {
	r := release(900, TargetShard, 0.08)
	if r.Bytes() < Ceiling {
		t.Fatalf("this case needs a release over %s and built one of %s", bytes(Ceiling), bytes(r.Bytes()))
	}
	if r.Holds() {
		t.Fatal("a release over the ceiling holds")
	}
	if v := r.Verdict(); !strings.Contains(v, "so the prediction misses and the release notes carry the real number") {
		t.Errorf("the verdict does not say what a miss costs:\n  %s", v)
	}
}

func TestMetadataOverAnEighthOfTheReleaseMissesP064(t *testing.T) {
	r := release(100, TargetShard, 0.20)
	if got := r.Share(); got < 0.19 || got > 0.21 {
		t.Fatalf("the metadata share came out at %.3f", got)
	}
	if r.Holds() {
		t.Fatal("a release spending a fifth of its bytes on metadata holds")
	}
	v := r.Verdict()
	if !strings.Contains(v, "more expensive than it was predicted to be") {
		t.Errorf("the verdict does not say what the miss means:\n  %s", v)
	}
	if !strings.Contains(v, "that is the number to argue about rather than the rule") {
		t.Errorf("the verdict does not say which of the two is up for argument:\n  %s", v)
	}
}

func TestTwoSnapshotsAreNotOneRelease(t *testing.T) {
	r := release(4, TargetShard, 0.08)
	r.Parts[2].Metadata["gao.snapshot"] = "gao-v1.1"
	refuses(t, r, "two snapshots summed read as one release twice the size")
	if r.Snapshot() != "" {
		t.Errorf("a release of two snapshots names one: %q", r.Snapshot())
	}
	if !strings.Contains(r.Verdict(), "These shards are not one release") {
		t.Errorf("the verdict adds up shards it refused:\n  %s", r.Verdict())
	}

	versions := release(3, TargetShard, 0.08)
	versions.Parts[1].Metadata["gao.schema_version"] = "2"
	refuses(t, versions, "columns that changed meaning between versions do not add up")

	loose := release(2, TargetShard, 0.08)
	loose.Parts[0].Metadata = map[string]string{}
	refuses(t, loose, "carries no snapshot stamp")
}

func TestARepoThatWithholdsTextIsNotWeighedWithOneThatShipsIt(t *testing.T) {
	r := release(3, TargetShard, 0.08)
	r.Parts[2].Columns = r.Parts[2].Columns[1:]
	refuses(t, r, "a repo that withholds a column was weighed with one that ships it")

	extra := release(3, TargetShard, 0.08)
	extra.Parts[1].Columns = append(extra.Parts[1].Columns, store.ColumnWeight{Name: "tokens", Compressed: 10, Uncompressed: 20})
	refuses(t, extra, "carries tokens and data/00000.parquet does not")

	both := release(2, TargetShard, 0.08)
	both.Parts[1].Columns = []store.ColumnWeight{{Name: "url", Compressed: 10, Uncompressed: 20}}
	refuses(t, both, "two formats rather than one release")
}

func TestAShardThatDoesNotDescribeItselfIsRefused(t *testing.T) {
	empty := release(2, TargetShard, 0.08)
	empty.Parts[1].Rows = 0
	refuses(t, empty, "an empty shard in a release is a stage that failed quietly")

	truncated := release(2, TargetShard, 0.08)
	truncated.Parts[0].Bytes = 1 << 10
	refuses(t, truncated, "so its footer does not describe it")

	var none Release
	refuses(t, none, "there is nothing to add up")
	if none.Share() != 0 || none.Ratio() != 0 {
		t.Error("an empty release has a metadata share and a compression ratio")
	}
}

func TestShardsFarFromTheTargetSizeAreNamedRatherThanRefused(t *testing.T) {
	r := release(4, TargetShard, 0.08)
	r.Parts = append(r.Parts,
		part("data/small.parquet", 4<<20, 3<<20, 1<<20),
		part("data/large.parquet", 3<<30, 2<<30, 1<<30))
	if why := r.Blocking(); len(why) > 0 {
		t.Fatalf("shards of the wrong size were refused rather than reported: %v", why)
	}
	loose := r.Loose()
	if len(loose) != 2 {
		t.Fatalf("%d shards read as outside the band", len(loose))
	}
	if loose[0].Bytes > loose[1].Bytes {
		t.Error("the loose shards are not smallest first")
	}
	// Within a quarter of the target is the same shard.
	near := release(2, TargetShard, 0.08)
	near.Parts[0].Bytes = TargetShard - TargetShard/5
	if len(near.Loose()) != 0 {
		t.Error("a shard a fifth under the target reads as loose")
	}
}

func TestTheColumnsAreReportedHeaviestFirst(t *testing.T) {
	r := release(4, TargetShard, 0.10)
	columns := r.Columns()
	if len(columns) != 3 {
		t.Fatalf("%d columns came off three", len(columns))
	}
	if columns[0].Name != Text {
		t.Errorf("the heaviest column of a corpus is %s", columns[0].Name)
	}
	for i := 1; i < len(columns); i++ {
		if columns[i-1].Compressed < columns[i].Compressed {
			t.Fatalf("%s sorted above %s", columns[i-1].Name, columns[i].Name)
		}
	}
	if got := columns[0].Share(r.Stored()); got < 0.89 || got > 0.91 {
		t.Errorf("the text column takes %.3f of a release that spends a tenth on metadata", got)
	}
	if got := r.Ratio(); got < 2.7 || got > 3.0 {
		t.Errorf("the compression ratio came out at %.2f", got)
	}
}

// sample is the same document the store tests write, built here so that this
// package can weigh real Parquet rather than only the arithmetic over it.
func sample(i int) *doc.Document {
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
		Language: doc.Language{Lang: "vie", LangScore: 0.997, Diacritics: "present"},
		Licensing: doc.Licensing{
			LicenseClass:    doc.LicenseOpen,
			LicenseEvidence: "robots allow, no TDM reservation",
		},
	}
	d.DocID = doc.SumString(d.Text)
	d.NChars = uint32(utf8.RuneCountInString(d.Text))
	return d
}

func TestWeighingRealPartsOffDisk(t *testing.T) {
	dataset, ok := store.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("vietnamese-web-text is not in the dataset table")
	}
	stamp := store.Stamp{Snapshot: "gao-v1.0", Stage: "store@0.1.0", Box: "server3"}

	dir := t.TempDir()
	paths := make([]string, 0, 3)
	for file := range 3 {
		rel := store.StagePath(stamp.Snapshot, file, 0)
		p, err := store.CreatePart(dir, rel, dataset, stamp)
		if err != nil {
			t.Fatal(err)
		}
		for i := range 50 {
			if err := p.Append(sample(file*50 + i)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.Close(); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, filepath.Join(dir, filepath.FromSlash(rel)))
	}

	r, err := Weigh("gao-v1.0", paths)
	if err != nil {
		t.Fatal(err)
	}
	if why := r.Blocking(); len(why) > 0 {
		t.Fatalf("three parts written by one stage were refused: %v", why)
	}
	if r.Snapshot() != "gao-v1.0" {
		t.Errorf("the release names %q", r.Snapshot())
	}
	if r.Rows() != 150 {
		t.Errorf("%d rows came off 150 documents", r.Rows())
	}
	if r.Read() >= r.Bytes() {
		t.Errorf("weighing %d bytes of parts read %d, which is the files rather than their footers", r.Bytes(), r.Read())
	}
	if r.TextBytes() <= 0 {
		t.Error("a release of prose weighs nothing in its text column")
	}
	// Every one of these shards is far under 512 MB, which is a fact about the
	// fixture and is reported rather than refused.
	if len(r.Loose()) != len(paths) {
		t.Errorf("%d of %d tiny shards read as the right size", len(paths)-len(r.Loose()), len(paths))
	}

	if _, err := Weigh("gao-v1.0", []string{filepath.Join(dir, "nothing.parquet")}); err == nil {
		t.Error("a file that is not there was weighed")
	}
}
