package dem_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/dem"
	"github.com/tamnd/gao/doc"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestAReportSurvivesARoundTripThroughADirectory(t *testing.T) {
	var tally dem.Tally
	count := tally.Counting(nil, nil)
	for range 3 {
		if err := count(document(doc.SourceGlotCC, "Việt Nam", 2)); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	want := tally.Report("server1", at("2026-08-04T07:32:22Z"))

	if err := want.Write(dir); err != nil {
		t.Fatalf("writing the report: %v", err)
	}
	got, err := dem.ReadReport(dir)
	if err != nil {
		t.Fatalf("reading the report back: %v", err)
	}

	if got.Box != "server1" {
		t.Errorf("the box came back as %q, want server1", got.Box)
	}
	if !got.Finished.Equal(want.Finished) {
		t.Errorf("the time came back as %v, want %v", got.Finished, want.Finished)
	}
	if got.Total != want.Total {
		t.Errorf("the total came back as %+v, want %+v", got.Total, want.Total)
	}
	if len(got.Sources) != 1 || got.Sources[0].Source != doc.SourceGlotCC {
		t.Errorf("the sources came back as %+v, want one line for glotcc", got.Sources)
	}
}

func TestReadingCountsFromADirectoryWithNoneSaysSo(t *testing.T) {
	_, err := dem.ReadReport(t.TempDir())
	if !errors.Is(err, dem.ErrNoReport) {
		t.Fatalf("reading an empty directory returned %v, want ErrNoReport", err)
	}
}

// A report written by a machine that went down mid-write would parse, which is
// the reason for the rename. Nothing but the finished file is ever left behind
// under the name a reader looks for.
func TestAWrittenReportLeavesNoTemporaryFileBehind(t *testing.T) {
	dir := t.TempDir()
	var tally dem.Tally
	tally.Add(document(doc.SourceGlotCC, "một", 1))

	if err := tally.Report("server1", at("2026-08-04T07:32:22Z")).Write(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != dem.File {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("the directory holds %v, want only %s", names, dem.File)
	}
}

func TestAReportNamesItsTokenizerOrLeavesItEmpty(t *testing.T) {
	var counted, uncounted dem.Tally
	counted.Tokenizer = "gemma-3"
	counted.Add(document(doc.SourceGlotCC, "một", 1))
	uncounted.Add(document(doc.SourceGlotCC, "một", 1))

	if got := counted.Report("server1", at("2026-08-04T07:32:22Z")).Tokenizer; got != "gemma-3" {
		t.Errorf("a counted report names %q as its tokenizer, want gemma-3", got)
	}
	if got := uncounted.Report("server1", at("2026-08-04T07:32:22Z")).Tokenizer; got != "" {
		t.Errorf("an uncounted report names %q as its tokenizer, want nothing", got)
	}
}

func TestMergingTwoBoxesThatEachDidHalfTheShards(t *testing.T) {
	one := dem.Report{
		Box:       "server1",
		Tokenizer: "gemma-3",
		Finished:  at("2026-08-04T07:00:00Z"),
		Sources: []dem.SourceCounts{
			{Source: doc.SourceGlotCC, Counts: dem.Counts{Documents: 10, Chars: 100, Tokens: 33}},
		},
	}
	two := dem.Report{
		Box:       "server2",
		Tokenizer: "gemma-3",
		Finished:  at("2026-08-04T08:00:00Z"),
		Sources: []dem.SourceCounts{
			{Source: doc.SourceGlotCC, Counts: dem.Counts{Documents: 5, Chars: 50, Tokens: 17}},
			{Source: doc.SourceFineWeb2, Counts: dem.Counts{Documents: 2, Chars: 20, Tokens: 7}},
		},
	}

	got, err := dem.Merge(one, two)
	if err != nil {
		t.Fatalf("merging two boxes: %v", err)
	}

	if got.Box != "" {
		t.Errorf("the merged report claims box %q, and it came from more than one", got.Box)
	}
	if !got.Finished.Equal(two.Finished) {
		t.Errorf("the merged report finished at %v, want the later of the two", got.Finished)
	}
	if got.Total.Documents != 17 {
		t.Errorf("the merged total is %d documents, want 17", got.Total.Documents)
	}
	if len(got.Sources) != 2 || got.Sources[0].Source != doc.SourceFineWeb2 {
		t.Fatalf("the merged sources are %+v, want fineweb2 then glotcc", got.Sources)
	}
	if got.Sources[1].Documents != 15 {
		t.Errorf("glotcc merged to %d documents, want 15", got.Sources[1].Documents)
	}
}

func TestMergingRefusesTwoDifferentTokenizers(t *testing.T) {
	one := dem.Report{Tokenizer: "gemma-3"}
	two := dem.Report{Tokenizer: "llama-3"}

	_, err := dem.Merge(one, two)
	if !errors.Is(err, dem.ErrMixedTokenizers) {
		t.Fatalf("merging two tokenizers returned %v, want ErrMixedTokenizers", err)
	}
	if !strings.Contains(err.Error(), "gemma-3") || !strings.Contains(err.Error(), "llama-3") {
		t.Errorf("the message is %q, and it should name both tokenizers", err)
	}
}

// A run that did not tokenize has no tokenizer to disagree with one that did, so
// it merges. What comes out is a token count for part of the material, which is
// why the document counts are what the reader is meant to compare.
func TestMergingAnUncountedReportIntoACountedOne(t *testing.T) {
	counted := dem.Report{Tokenizer: "gemma-3"}
	uncounted := dem.Report{}

	got, err := dem.Merge(counted, uncounted)
	if err != nil {
		t.Fatalf("merging an uncounted report: %v", err)
	}
	if got.Tokenizer != "gemma-3" {
		t.Errorf("the merged tokenizer is %q, want gemma-3", got.Tokenizer)
	}
}

func TestMergingKeepsSyntheticOutOfTheCorpusSize(t *testing.T) {
	got, err := dem.Merge(dem.Report{
		Sources: []dem.SourceCounts{
			{Source: doc.SourceGlotCC, Counts: dem.Counts{Documents: 10, Tokens: 100}},
			{Source: doc.SourceSynth, Counts: dem.Counts{Documents: 5, Tokens: 50}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Total.Tokens != 150 {
		t.Errorf("the total is %d tokens, want 150", got.Total.Tokens)
	}
	if got.Natural.Tokens != 100 {
		t.Errorf("the corpus is %d tokens, want 100", got.Natural.Tokens)
	}
}

func TestAReportIsWrittenWhereTheLedgerLives(t *testing.T) {
	dir := t.TempDir()
	var tally dem.Tally
	tally.Add(document(doc.SourceGlotCC, "một", 1))
	if err := tally.Report("server1", at("2026-08-04T07:32:22Z")).Write(dir); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "counts.json")); err != nil {
		t.Errorf("the report is not at counts.json in the ingest directory: %v", err)
	}
}

// A run over a large source is rewritten after every file, so most of the
// reports that exist at any moment describe runs that are still going, and the
// flag that says so is the difference between a prefix and a total.
func TestAPartialReportSaysThatItIsOne(t *testing.T) {
	dir := t.TempDir()
	var tally dem.Tally
	r := tally.Report("server2", time.Now())
	if r.Complete {
		t.Error("a report is finished before anybody says it is")
	}
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}

	back, err := dem.ReadReport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if back.Complete {
		t.Error("an unfinished report came back finished")
	}

	r.Complete = true
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}
	if back, err = dem.ReadReport(dir); err != nil {
		t.Fatal(err)
	}
	if !back.Complete {
		t.Error("a finished report came back unfinished")
	}
}

// One box still fetching makes the fleet total a prefix of the corpus, so the
// sum of four reports is complete only when every one of them is.
func TestASumIsFinishedOnlyWhenEveryBoxIs(t *testing.T) {
	done := dem.Report{Box: "server1", Complete: true}
	going := dem.Report{Box: "server3"}

	both, err := dem.Merge(done, going)
	if err != nil {
		t.Fatal(err)
	}
	if both.Complete {
		t.Error("a sum that includes a running box called itself finished")
	}

	all, err := dem.Merge(done, dem.Report{Box: "server2", Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	if !all.Complete {
		t.Error("a sum of finished boxes called itself unfinished")
	}

	none, err := dem.Merge()
	if err != nil {
		t.Fatal(err)
	}
	if none.Complete {
		t.Error("a sum of nothing called itself finished")
	}
}
