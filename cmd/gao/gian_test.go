package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

// gianPart writes one part holding count documents of the given source, each
// carrying the given token count, and returns the path to it. The lengths are
// written next to the text rather than counted off it, because what the command
// reads is the length column and what it does with it is the thing under test.
func gianPart(t *testing.T, source doc.Source, ntokens uint32, count int) string {
	t.Helper()
	d, ok := kho.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("vietnamese-web-text is not in the dataset table")
	}
	dir := t.TempDir()
	rel := kho.StagePath("gao-v1", 0, 0)
	p, err := kho.CreatePart(dir, rel, d, kho.Stamp{Snapshot: "gao-v1", Stage: "kho@0.1.0", Box: "server1"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Abandon()

	for i := range count {
		text := "Luận án này phân tích các phương pháp trích xuất văn bản từ tài liệu số hóa của thư viện quốc gia."
		rec := &doc.Document{
			RawID:         doc.SumString("raw:" + string(source) + ":" + itoa(int64(i))),
			DocID:         doc.SumString(string(source) + ":" + itoa(int64(i))),
			Text:          text,
			SchemaVersion: doc.SchemaVersion,
			Provenance: doc.Provenance{
				Source:          source,
				SourceLocator:   "gao-ingest-2026-09/00001.warc.gz@" + itoa(int64(i*4096)) + "+4096",
				URL:             "https://thuvienphapluat.vn/van-ban/" + itoa(int64(i)) + ".html",
				Host:            "thuvienphapluat.vn",
				FetchedAt:       time.Date(2026, 9, 14, 3, 22, 11, 0, time.UTC),
				MediaType:       "text/html",
				Extractor:       "go-trafilatura@1.4.0",
				PipelineVersion: "0.1.0",
			},
			Language:  doc.Language{Lang: "vie", LangScore: 0.997, Diacritics: "present"},
			Licensing: doc.Licensing{LicenseClass: doc.LicenseOpen, LicenseEvidence: "robots allow, no TDM reservation"},
		}
		rec.NChars = uint32(utf8.RuneCountInString(text))
		rec.NTokens = ntokens
		if err := p.Append(rec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Close(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, filepath.FromSlash(rel))
}

func TestGianLadderPrintsTheRungsWithWhatEachOneIsFor(t *testing.T) {
	out, errOut, code := exec(t, "gian", "ladder")
	if code != 0 {
		t.Fatalf("the ladder read as unclimbable, exit %d: %s", code, errOut)
	}
	for _, want := range []string{
		"131072",
		"any length",
		"32768 tokens",
		"YaRN, then a short finetune at the window",
		"naturally long Vietnamese only, and concatenated shorts for nothing",
		"vi-needle and vi-longdoc-qa at 131072",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the ladder does not say %q:\n%s", want, out)
		}
	}
}

func TestGianLadderQuotesTheLongDemandAsAShareOfTheStage(t *testing.T) {
	out, _, code := exec(t, "gian", "ladder")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// The anneal stage buys 18% of its tokens from the slices that hold the long
	// documents, and that share is the number the pool has to answer.
	if !strings.Contains(out, "18.0%") {
		t.Errorf("the anneal rung does not say how much of it comes off long documents:\n%s", out)
	}
}

func TestGianPoolReadsAPartAndSaysWhatTheReadCost(t *testing.T) {
	path := gianPart(t, doc.SourceFinePDFs, 70000, 300)
	out, errOut, code := exec(t, "gian", "pool", "-name", "a slice", path)
	if code != 2 {
		t.Fatalf("a pool of 300 documents supplied the anneal stage, exit %d: %s%s", code, out, errOut)
	}
	for _, want := range []string{
		"131072",
		"finepdfs",
		"of the parts, so the box doing the reading does not have to be the box holding them",
		"The longest document is 70,000 tokens",
		"the ladder cannot be climbed as written",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the reading does not say %q:\n%s", want, out)
		}
	}
}

func TestGianPoolRefusesAPartWithNoTokenCountsOnIt(t *testing.T) {
	path := gianPart(t, doc.SourceCrawl, 0, 20)
	out, _, code := exec(t, "gian", "pool", path)
	if code != 1 {
		t.Fatalf("a part with no lengths on it measured as a length distribution, exit %d: %s", code, out)
	}
	if !strings.Contains(out, "This is not a length distribution") {
		t.Errorf("a refused pool does not say why:\n%s", out)
	}
	if strings.Contains(out, "passes") {
		t.Errorf("a refused pool came back with readings taken off it anyway:\n%s", out)
	}
}

func TestGianPoolRefusesAPartThatIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "part-00000.parquet")
	_, errOut, code := exec(t, "gian", "pool", missing)
	if code != 1 {
		t.Fatalf("a part that is not there measured as one, exit %d", code)
	}
	if !strings.Contains(errOut, missing) {
		t.Errorf("the error does not name the file it could not read:\n%s", errOut)
	}
}

func TestGianPrintsTheSameReadingAsJSON(t *testing.T) {
	path := gianPart(t, doc.SourceMedia, 40000, 50)
	out, _, code := exec(t, "gian", "pool", "-name", "a slice", "-json", path)
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	var got gianPoolReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the JSON does not parse: %v", err)
	}
	if got.Name != "a slice" || got.Documents != 50 || got.Tokens != 50*40000 {
		t.Errorf("the JSON reads %s at %d documents and %d tokens", got.Name, got.Documents, got.Tokens)
	}
	if len(got.Bands) != 2 {
		t.Fatalf("%d bands extend something", len(got.Bands))
	}
	if got.Bands[1].Window != 131072 || got.Bands[1].Documents != 50 {
		t.Errorf("the top band is %d documents at a window of %d", got.Bands[1].Documents, got.Bands[1].Window)
	}
	if got.Holds || got.Verdict == "" || len(got.Faults) == 0 {
		t.Errorf("a pool of fifty documents holds the ladder: %+v", got)
	}

	ladder, _, code := exec(t, "gian", "ladder", "-json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rungs gianLadderReport
	if err := json.Unmarshal([]byte(ladder), &rungs); err != nil {
		t.Fatalf("the ladder JSON does not parse: %v", err)
	}
	if len(rungs.Rungs) != 3 || rungs.Rungs[2].Window != 131072 {
		t.Errorf("the ladder JSON is %d rungs", len(rungs.Rungs))
	}
}

func TestGianSaysHowToBeAskedProperly(t *testing.T) {
	for _, args := range [][]string{
		{"gian"},
		{"gian", "pool"},
		{"gian", "ladder", "extra"},
		{"gian", "khong-co"},
	} {
		if _, errOut, code := exec(t, args...); code != 2 {
			t.Errorf("%v exited %d rather than saying how to be asked: %s", args, code, errOut)
		}
	}
	out, _, code := exec(t, "gian", "help")
	if code != 0 || !strings.Contains(out, "usage: gao stretch ladder") {
		t.Errorf("gian help exited %d without a usage line:\n%s", code, out)
	}
}
