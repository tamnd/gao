package kho

import (
	"strings"
	"testing"
)

func indexRows() []Indexed {
	return []Indexed{
		{Source: "hplt3", Snapshot: "hplt3-5b2785d5b11c", File: 6, Part: 24,
			Path: "data/hplt3/hplt3-5b2785d5b11c-00006-00024.parquet", Documents: 41230, Bytes: 501234567},
		{Source: "glotcc", Snapshot: "glotcc-9ad140b6be3a", File: 3, Part: 0,
			Path: "data/glotcc/glotcc-9ad140b6be3a-00003-00000.parquet", Documents: 116631, Bytes: 524202221},
		{Source: "glotcc", Snapshot: "glotcc-9ad140b6be3a", File: 3, Part: 1,
			Path: "data/glotcc/glotcc-9ad140b6be3a-00003-00001.parquet", Documents: 118004, Bytes: 523991002},
		{Source: "glotcc", Snapshot: "glotcc-9ad140b6be3a", File: 4, Part: 0,
			Path: "data/glotcc/glotcc-9ad140b6be3a-00004-00000.parquet", Documents: 115900, Bytes: 522110090},
	}
}

func TestAnIndexSurvivesBeingWrittenAndReadBack(t *testing.T) {
	var b strings.Builder
	if err := WriteIndex(&b, indexRows()); err != nil {
		t.Fatalf("writing the index: %v", err)
	}
	back, err := ReadIndex(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("reading the index back: %v", err)
	}
	if len(back) != len(indexRows()) {
		t.Fatalf("wrote %d rows and read %d back", len(indexRows()), len(back))
	}
	for _, want := range indexRows() {
		found := false
		for _, got := range back {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s did not come back as it went in", want.Path)
		}
	}
}

// A run over a repo where nothing has changed has to produce the same bytes, or
// the nightly index puts a commit on the repo every night for no reason.
func TestTheIndexIsWrittenInPathOrderWhateverOrderItIsGivenIn(t *testing.T) {
	rows := indexRows()
	shuffled := []Indexed{rows[3], rows[0], rows[2], rows[1]}

	var one, two strings.Builder
	if err := WriteIndex(&one, rows); err != nil {
		t.Fatalf("writing the index: %v", err)
	}
	if err := WriteIndex(&two, shuffled); err != nil {
		t.Fatalf("writing the index: %v", err)
	}
	if one.String() != two.String() {
		t.Error("two orderings of the same parts wrote two different files")
	}

	lines := strings.Split(strings.TrimSpace(one.String()), "\n")
	if !strings.HasPrefix(lines[1], "glotcc,") || !strings.HasPrefix(lines[4], "hplt3,") {
		t.Errorf("the rows are not in path order:\n%s", one.String())
	}
}

// Reading by position rather than by name is how an index with a column added
// comes back with the document counts sitting in the byte counts.
func TestAnIndexWithTheWrongColumnsIsRefusedRatherThanReadByPosition(t *testing.T) {
	bad := "source,snapshot,file,part,path,rows,bytes\n" +
		"glotcc,glotcc-9ad140b6be3a,3,0,data/glotcc/glotcc-9ad140b6be3a-00003-00000.parquet,116631,524202221\n"
	if _, err := ReadIndex(strings.NewReader(bad)); err == nil {
		t.Error("an index whose columns this build does not write was read anyway")
	}
}

func TestASourceIsSummedAcrossItsPartsAndItsFiles(t *testing.T) {
	by := BySource(indexRows())
	if len(by) != 2 {
		t.Fatalf("four parts of two sources came to %d sources", len(by))
	}
	if by[0].Source != "glotcc" {
		t.Errorf("the largest source is %s and glotcc has three times the documents", by[0].Source)
	}
	got := by[0]
	if got.Parts != 3 || got.Files != 2 {
		t.Errorf("glotcc has %d parts over %d files, and it has 3 over 2", got.Parts, got.Files)
	}
	if want := int64(116631 + 118004 + 115900); got.Documents != want {
		t.Errorf("glotcc holds %d documents and its parts hold %d", got.Documents, want)
	}
	if len(got.Snapshots) != 1 || got.Snapshots[0] != "glotcc-9ad140b6be3a" {
		t.Errorf("glotcc is pinned at %v and it was ingested at one revision", got.Snapshots)
	}
}

// A source that was re-pinned without the old parts being swept holds every
// document twice, and a reader has to be told rather than left to find out by
// counting.
func TestASourceHeldAtTwoRevisionsSaysSo(t *testing.T) {
	rows := append(indexRows(), Indexed{
		Source: "glotcc", Snapshot: "glotcc-0000000000aa", File: 3, Part: 0,
		Path: "data/glotcc/glotcc-0000000000aa-00003-00000.parquet", Documents: 116631, Bytes: 524202221,
	})
	by := BySource(rows)
	if len(by[0].Snapshots) != 2 {
		t.Fatalf("a source at two revisions reported %v", by[0].Snapshots)
	}
	if by[0].Snapshots[0] != "glotcc-0000000000aa" || by[0].Snapshots[1] != "glotcc-9ad140b6be3a" {
		t.Errorf("the revisions are %v and they sort as the paths do", by[0].Snapshots)
	}
	if by[0].Files != 2 {
		t.Errorf("the same input file at two revisions counted as %d files", by[0].Files)
	}
}

func TestTheTotalIsWhatTheRepoGetsToClaim(t *testing.T) {
	documents, bytes := Total(indexRows())
	if want := int64(41230 + 116631 + 118004 + 115900); documents != want {
		t.Errorf("the index totals %d documents and its rows hold %d", documents, want)
	}
	if want := int64(501234567 + 524202221 + 523991002 + 522110090); bytes != want {
		t.Errorf("the index totals %d bytes and its rows hold %d", bytes, want)
	}
}

func TestAnEmptyIndexIsAHeaderAndNothingElse(t *testing.T) {
	var b strings.Builder
	if err := WriteIndex(&b, nil); err != nil {
		t.Fatalf("writing an empty index: %v", err)
	}
	back, err := ReadIndex(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("reading an empty index back: %v", err)
	}
	if len(back) != 0 {
		t.Errorf("an empty index read back as %d rows", len(back))
	}
}
