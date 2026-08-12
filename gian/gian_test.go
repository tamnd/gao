package gian

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

// A document is one length and one source, which is all a length distribution
// is about. The text is short and the token count is written next to it rather
// than counted off it, because what is under test here is the distribution and
// not the tokenizer.
type document struct {
	source doc.Source
	tokens uint32
	count  int
}

func record(i int, source doc.Source, ntokens uint32) *doc.Document {
	text := fmt.Sprintf(
		"Tài liệu số %d. Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc. "+
			"Đoạn văn này đủ dài để vượt qua ngưỡng tối thiểu của hợp đồng nhập liệu.", i)
	d := &doc.Document{
		RawID:         doc.SumString(fmt.Sprintf("raw:%d:%s", i, source)),
		Text:          text,
		SchemaVersion: doc.SchemaVersion,
		Provenance: doc.Provenance{
			Source:          source,
			SourceLocator:   fmt.Sprintf("gao-ingest-2026-09/00001.warc.gz@%d+4096", i*4096),
			URL:             fmt.Sprintf("https://thuvienphapluat.vn/van-ban/%d.html", i),
			Host:            "thuvienphapluat.vn",
			FetchedAt:       time.Date(2026, 9, 14, 3, 22, 11, 0, time.UTC),
			MediaType:       "text/html",
			Extractor:       "go-trafilatura@1.4.0",
			PipelineVersion: "0.1.0",
		},
		Language:  doc.Language{Lang: "vie", LangScore: 0.997, Diacritics: "present"},
		Licensing: doc.Licensing{LicenseClass: doc.LicenseOpen, LicenseEvidence: "robots allow, no TDM reservation"},
	}
	d.DocID = doc.SumString(fmt.Sprintf("%d:%s:%d", i, source, ntokens))
	d.NChars = uint32(utf8.RuneCountInString(d.Text))
	d.NTokens = ntokens
	return d
}

// measured writes one part holding the given documents and measures it.
func measured(t *testing.T, docs ...document) Pool {
	t.Helper()
	d, ok := kho.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("vietnamese-web-text is not in the dataset table")
	}
	dir := t.TempDir()
	rel := kho.StagePath("gao-v1", 0, 0)
	p, err := kho.CreatePart(dir, rel, d, kho.Stamp{Snapshot: "gao-v1", Stage: "kho@0.1.0", Box: "server3"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Abandon()

	i := 0
	for _, spec := range docs {
		for range spec.count {
			if err := p.Append(record(i, spec.source, spec.tokens)); err != nil {
				t.Fatal(err)
			}
			i++
		}
	}
	if _, err := p.Close(); err != nil {
		t.Fatal(err)
	}

	pool, err := Measure("gao-v1", []string{filepath.Join(dir, filepath.FromSlash(rel))})
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

// counted folds the given documents in without writing a part, which is how a
// pool the size of a real release gets tested. The fold is the same one
// [Measure] uses, so this is the corpus arriving by another door rather than a
// second implementation of the arithmetic.
func counted(name string, docs ...document) Pool {
	rungs := Ladder()
	p := Pool{Name: name, Parts: 1, bands: make([]counter, len(rungs))}
	for i := range p.bands {
		p.bands[i].sources = make(map[string]Part)
	}
	for _, spec := range docs {
		for range spec.count {
			p.add(rungs, Top(), kho.Length{Source: string(spec.source), NTokens: spec.tokens})
		}
	}
	return p
}

func says(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, want) {
			return
		}
	}
	t.Errorf("nothing says %q, and what came back was:\n  %s", want, strings.Join(lines, "\n  "))
}

func TestTheLadderIsTheCurriculumSeenFromTheOtherSide(t *testing.T) {
	if why := CheckLadder(); len(why) > 0 {
		t.Fatalf("the ladder does not hold: %v", why)
	}
	l := Ladder()
	if len(l) != 3 {
		t.Fatalf("the ladder has %d rungs", len(l))
	}
	if l[0].Floor != 0 {
		t.Errorf("the first rung extends something, at a floor of %d", l[0].Floor)
	}
	for i, r := range l {
		if r.Method == "" || r.Data == "" || r.Eval == "" {
			t.Errorf("stage %d is a window with no method, data rule, or evaluation next to it", r.Stage)
		}
		if i > 0 && r.Floor != l[i-1].Window {
			t.Errorf("stage %d asks for documents over %d and the stage below it trained at %d", r.Stage, r.Floor, l[i-1].Window)
		}
		if r.Demand <= 0 || r.Demand >= r.Tokens {
			t.Errorf("stage %d demands %d of the %d tokens it spends", r.Stage, r.Demand, r.Tokens)
		}
	}
	if Top() != l[len(l)-1].Window {
		t.Errorf("the top window is %d and the last rung trains at %d", Top(), l[len(l)-1].Window)
	}
}

func TestAPoolThatCarriesTheLadderReadsAsOne(t *testing.T) {
	// A pool the shape the corpus would have to be: 70,000 theses and codes over
	// the 32768 floor, 320,000 mid length documents over the 4096 one, and web
	// text under both.
	p := counted("gao-v1",
		document{doc.SourceMedia, 70000, 38500},
		document{doc.SourceCrawl, 70000, 31500},
		document{doc.SourceGlotCC, 15000, 320000},
		document{doc.SourceCrawl, 800, 20000},
	)
	if why := p.Blocking(); len(why) > 0 {
		t.Fatalf("a measurable pool was refused: %v", why)
	}
	if faults := p.Faults(); len(faults) > 0 {
		t.Fatalf("a pool that carries the ladder came back with faults:\n  %s", strings.Join(faults, "\n  "))
	}
	if !p.Holds() {
		t.Fatal("Holds disagrees with Faults")
	}

	bands := p.Extending()
	if len(bands) != 2 {
		t.Fatalf("%d rungs extend something", len(bands))
	}
	top := bands[1]
	if top.Documents != 70000 || top.Tokens != 70000*70000 {
		t.Errorf("the top pool is %d documents and %d tokens", top.Documents, top.Tokens)
	}
	if got := top.Largest(); got.Source != string(doc.SourceMedia) || got.Share < 0.54 || got.Share > 0.56 {
		t.Errorf("the top pool leans on %s at %.2f", got.Source, got.Share)
	}
	if got := top.Passes(); got < 3.7 || got > 3.9 {
		t.Errorf("supplying the last stage out of this pool takes %.2f passes", got)
	}
	if v := p.Verdict(); !strings.Contains(v, "rather than on shorts packed to look like them") {
		t.Errorf("the verdict of a pool that carries the ladder does not say so:\n  %s", v)
	}
}

func TestAPoolIsReadOffTheParquetWithoutReadingTheDocuments(t *testing.T) {
	p := measured(t,
		document{doc.SourceMedia, 60000, 300},
		document{doc.SourceCrawl, 20000, 200},
		document{doc.SourceGlotCC, 800, 500},
	)
	if p.Documents != 1000 {
		t.Errorf("the pool read %d documents off a part holding 1000", p.Documents)
	}
	if p.Tokens != 300*60000+200*20000+500*800 {
		t.Errorf("the pool adds up to %d tokens", p.Tokens)
	}
	if p.Read <= 0 || p.Read >= p.Bytes {
		t.Errorf("reading the lengths off %d bytes of Parquet read %d", p.Bytes, p.Read)
	}
	if p.Longest != 60000 {
		t.Errorf("the longest document is 60000 tokens and the pool says %d", p.Longest)
	}
	if p.Over != 0 {
		t.Errorf("%d documents are over the top window, and none of these are", p.Over)
	}

	top := p.Extending()[1]
	if top.Documents != 300 || top.Tokens != 300*60000 {
		t.Errorf("the top pool is %d documents and %d tokens", top.Documents, top.Tokens)
	}
	if got := top.Largest(); got.Source != string(doc.SourceMedia) || got.Share != 1 {
		t.Errorf("the top pool leans on %s at %.2f", got.Source, got.Share)
	}
}

func TestAPoolTooSmallForTheStageSaysHowManyPassesItWouldTake(t *testing.T) {
	p := measured(t,
		document{doc.SourceMedia, 40000, 20},
		document{doc.SourceCrawl, 9000, 400},
	)
	says(t, p.Faults(), "passes over the same 20 documents against a ceiling of 4")
	if p.Holds() {
		t.Error("a pool that would take thousands of passes holds")
	}
	if !strings.Contains(p.Verdict(), "the ladder cannot be climbed as written") {
		t.Errorf("the verdict reads a pool that cannot supply the stage as one that can:\n  %s", p.Verdict())
	}
}

func TestAPoolThatDoesNotReachIntoTheWindowIsNamed(t *testing.T) {
	// Every document clears the 32768 floor and none of them comes close to the
	// 131072 window, which is the failure that reads as a full pool.
	p := measured(t, document{doc.SourceMedia, 33000, 4000})
	says(t, p.Faults(), "of the window, so every position past that is trained by packing")
	says(t, p.Faults(), "average 33,000 tokens")
}

func TestAPoolLeaningOnOneSourceIsNamed(t *testing.T) {
	p := measured(t,
		document{doc.SourceMedia, 60000, 900},
		document{doc.SourceCrawl, 60000, 100},
	)
	says(t, p.Faults(), "90% of the 131072 pool is gao-media")
}

func TestNothingLongEnoughIsADifferentSentenceFromNotEnoughOfIt(t *testing.T) {
	p := measured(t, document{doc.SourceCrawl, 5000, 100})
	says(t, p.Faults(), "nothing in the corpus is longer than 32768 tokens")
	says(t, p.Faults(), "which teaches positions and not what is across them")
}

func TestADocumentWithNoTokenCountIsRefusedRatherThanEstimated(t *testing.T) {
	p := measured(t,
		document{doc.SourceMedia, 60000, 10},
		document{doc.SourceCrawl, 0, 3},
	)
	says(t, p.Blocking(), "3 of 13 documents carry no token count")
	if len(p.Faults()) != 0 {
		t.Errorf("a pool that is not a length distribution came back with readings about it: %v", p.Faults())
	}
	if !strings.HasPrefix(p.Verdict(), "This is not a length distribution") {
		t.Errorf("the verdict reads a pool it refused:\n  %s", p.Verdict())
	}
}

func TestAPoolWithNoPartsUnderItIsRefused(t *testing.T) {
	p, err := Measure("gao-v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	says(t, p.Blocking(), "no parts were read")

	if _, err := Measure("gao-v1", []string{filepath.Join(t.TempDir(), "nothing.parquet")}); err == nil {
		t.Error("a part that is not there measured as one")
	}
}

func TestDocumentsLongerThanTheTopWindowAreCounted(t *testing.T) {
	p := measured(t,
		document{doc.SourceMedia, 200000, 100},
		document{doc.SourceCrawl, 60000, 100},
	)
	if p.Over != 100 {
		t.Errorf("%d documents are longer than the top window and 100 of them are", p.Over)
	}
	if p.Longest != 200000 {
		t.Errorf("the longest document reads as %d tokens", p.Longest)
	}
}
