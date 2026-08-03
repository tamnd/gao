package gat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

func openLedger(t *testing.T) (*Ledger, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := OpenLedger(dir)
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, dir
}

func entry(source doc.Source, revision, path string, bytes int64) Entry {
	return Entry{Source: source, Revision: revision, Path: path, Bytes: bytes, Digest: sha([]byte(path))}
}

func TestAFinishedFileSurvivesTheProcessThatWroteIt(t *testing.T) {
	l, dir := openLedger(t)

	e := entry(doc.SourceHPLT3, "sha256:aaaa", "vie_Latn/5_1.jsonl.zst", 15_049_231_912)
	e.Documents = 12_000_000
	if err := l.Record(e); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The restart. A new process on the same directory has to see what the old
	// one finished, or an interruption costs the whole 608.9 GB.
	again, err := OpenLedger(dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = again.Close() }()

	p := Pinned{Source: doc.SourceHPLT3, Revision: "sha256:aaaa"}
	if !again.Done(p, File{Path: e.Path}) {
		t.Error("a file recorded before the restart is not done after it")
	}
	if again.Bytes() != e.Bytes {
		t.Errorf("the reopened ledger accounts for %d bytes, want %d", again.Bytes(), e.Bytes)
	}
	if again.Documents() != e.Documents {
		t.Errorf("the reopened ledger accounts for %d documents, want %d", again.Documents(), e.Documents)
	}
	if got := again.Entries(); len(got) != 1 || got[0].Digest != e.Digest {
		t.Errorf("the reopened ledger holds %d entries", len(got))
	}
}

// The failure this prevents is a corpus built half from one revision of a source
// and half from the next, which nothing downstream could detect.
func TestRepinningASourceInvalidatesWhatWasFetchedAtTheOldOne(t *testing.T) {
	l, _ := openLedger(t)

	f := File{Path: "vi/vi_part_00000.parquet"}
	old := Pinned{Source: doc.SourceCulturaX, Revision: "6a8734bc69fefcbb7735f4f9250f43e4cd7a442e"}
	if err := l.Record(entry(old.Source, old.Revision, f.Path, 100)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if !l.Done(old, f) {
		t.Fatal("a file fetched at the pinned revision is not done")
	}

	repinned := old
	repinned.Revision = "0123456789abcdef0123456789abcdef01234567"
	if l.Done(repinned, f) {
		t.Error("a file fetched at the old revision reads as done at the new one")
	}
}

func TestAPlanSkipsWhatIsDoneAndKeepsTheRest(t *testing.T) {
	l, _ := openLedger(t)

	sources := Sources()
	todo, doneFiles, doneBytes := l.Plan(sources)
	if doneFiles != 0 || doneBytes != 0 {
		t.Errorf("an empty ledger reports %d files and %d bytes done", doneFiles, doneBytes)
	}
	if len(todo) != Files() {
		t.Fatalf("the first plan has %d files, want all %d", len(todo), Files())
	}
	if Remaining(todo) != TotalBytes() {
		t.Errorf("the first plan moves %d bytes, want %d", Remaining(todo), TotalBytes())
	}

	// Ingest order is the manifest order, and HPLT is first because it is the
	// spine. A plan that started anywhere else would spend the disk budget on
	// the sources the headline is not a claim about.
	if todo[0].Pin.Source != doc.SourceHPLT3 {
		t.Errorf("the plan starts with %s", todo[0].Pin.Source)
	}

	first := todo[0]
	if err := l.Record(entry(first.Pin.Source, first.Pin.Revision, first.File.Path, first.File.Bytes)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	todo, doneFiles, doneBytes = l.Plan(sources)
	if doneFiles != 1 || doneBytes != first.File.Bytes {
		t.Errorf("after one file the plan reports %d files and %d bytes done", doneFiles, doneBytes)
	}
	if len(todo) != Files()-1 {
		t.Errorf("the second plan has %d files, want %d", len(todo), Files()-1)
	}
	for _, w := range todo {
		if w.Pin.Source == first.Pin.Source && w.File.Path == first.File.Path {
			t.Fatalf("the plan still lists %s, which is already done", w.File.Path)
		}
	}
}

// A ledger with a corrupt tail might be missing the entry for a file that did
// finish, and re-downloading 26.6 GB is cheaper than being wrong about what is
// in the corpus.
func TestALedgerItCannotReadIsAnErrorRatherThanAFreshStart(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"a truncated line", `{"source":"hplt3","revision":"sha256:a","pa`, "not an ingest entry"},
		{"a line that is not JSON", "the disk filled up here\n", "not an ingest entry"},
		{"an entry with no revision", `{"source":"hplt3","path":"a.zst"}` + "\n", "which file at which revision"},
		{"an entry with no path", `{"source":"hplt3","revision":"sha256:a"}` + "\n", "which file at which revision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			good := `{"source":"hplt3","revision":"sha256:a","path":"vie_Latn/5_1.jsonl.zst","bytes":1}` + "\n"
			if err := os.WriteFile(filepath.Join(dir, LedgerName), []byte(good+tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := OpenLedger(dir)
			if err == nil {
				t.Fatal("OpenLedger accepted a ledger it could not read")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not say what is wrong: %v", err)
			}
			// Which line, because a person is about to open the file and look.
			if !strings.Contains(err.Error(), "line 2") {
				t.Errorf("the error does not say which line: %v", err)
			}
		})
	}
}

// A blank line is what a text editor leaves behind, and it says nothing about
// what was ingested, so it is not worth failing a restart over.
func TestABlankLineIsNotCorruption(t *testing.T) {
	dir := t.TempDir()
	content := `{"source":"hplt3","revision":"sha256:a","path":"a.zst","bytes":1}` + "\n\n" +
		`{"source":"hplt3","revision":"sha256:a","path":"b.zst","bytes":2}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, LedgerName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadLedger(dir)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("ReadLedger found %d entries, want 2", len(entries))
	}
}

func TestReadingALedgerDoesNotRequireTakingItOver(t *testing.T) {
	l, dir := openLedger(t)
	if err := l.Record(entry(doc.SourceGlotCC, "9ad140b6", "v1.0/vie-Latn/x.parquet", 42)); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// A report on one box should not be able to interfere with an ingest running
	// on another that shares the directory.
	entries, err := ReadLedger(dir)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(entries) != 1 || entries[0].Bytes != 42 {
		t.Errorf("ReadLedger returned %+v", entries)
	}

	empty, err := ReadLedger(t.TempDir())
	if err != nil {
		t.Fatalf("ReadLedger on a directory with no ledger: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("a directory with no ledger has %d entries", len(empty))
	}
}

func TestALedgerEntryRecordsWhenAndWhere(t *testing.T) {
	l, dir := openLedger(t)
	e := entry(doc.SourceMADLAD400, "9d886a76", "data/vi/clean_0000.jsonl.gz", 7)
	e.Box = "server1"
	e.Reconnects = 4
	if err := l.Record(e); err != nil {
		t.Fatalf("Record: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, LedgerName))
	if err != nil {
		t.Fatal(err)
	}
	line := string(raw)
	for _, want := range []string{`"box":"server1"`, `"reconnects":4`, `"finished":"20`} {
		if !strings.Contains(line, want) {
			t.Errorf("the recorded line does not contain %s: %s", want, line)
		}
	}
}

func TestClosingALedgerTwiceIsHarmless(t *testing.T) {
	l, _ := openLedger(t)
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
}

func TestALedgerNeedsSomewhereToLive(t *testing.T) {
	// A file where the directory should be, which is what a typo in a path flag
	// looks like from here.
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLedger(blocked); err == nil {
		t.Error("OpenLedger accepted a file as its directory")
	}
}
