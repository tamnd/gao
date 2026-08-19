package store

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fleet"
)

// roll returns a roll writing into a fresh directory, with a limit small enough
// that a handful of documents crosses it.
func roll(t *testing.T, textPerPart int64) (*Roll, string) {
	t.Helper()
	dir := t.TempDir()
	return &Roll{
		Dir:         dir,
		Dataset:     textDataset(t),
		Stamp:       stamp,
		File:        3,
		TextPerPart: textPerPart,
	}, dir
}

// textOf is the number the roll actually counts, so a test that wants two parts
// asks for a limit in the same unit rather than guessing at a file size.
func textOf(docs ...*doc.Document) int64 {
	var n int64
	for _, d := range docs {
		n += int64(len(d.Text))
	}
	return n
}

func TestARollWritesOnePartWhenTheTextFits(t *testing.T) {
	docs := []*doc.Document{sample(0), sample(1), sample(2)}
	r, dir := roll(t, textOf(docs...)*2)
	for _, d := range docs {
		if err := r.Append(d); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	files, err := r.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("wrote %d parts, want 1", len(files))
	}
	if files[0].Documents != len(docs) {
		t.Errorf("the part holds %d documents, want %d", files[0].Documents, len(docs))
	}
	rows, err := ReadPart(filepath.Join(dir, filepath.FromSlash(files[0].Path)))
	if err != nil {
		t.Fatalf("ReadPart: %v", err)
	}
	if len(rows) != len(docs) {
		t.Errorf("read back %d rows, want %d", len(rows), len(docs))
	}
}

// The bound on peak disk is the whole reason this type exists, so the test is
// that a roll given more text than one part holds writes more than one part.
func TestARollRollsOverOnText(t *testing.T) {
	docs := []*doc.Document{sample(0), sample(1), sample(2), sample(3)}
	r, dir := roll(t, textOf(docs[0], docs[1]))
	for _, d := range docs {
		if err := r.Append(d); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	files, err := r.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("wrote %d parts, want at least 2", len(files))
	}

	var total int
	seen := make(map[string]bool)
	for i, f := range files {
		if seen[f.Path] {
			t.Errorf("two parts landed at the same path: %s", f.Path)
		}
		seen[f.Path] = true
		total += f.Documents
		snapshot, file, part, ok := ParseStagePath(f.Path)
		if !ok {
			t.Errorf("part %d is not at a staging path: %s", i, f.Path)
			continue
		}
		if snapshot != stamp.Snapshot || file != 3 || part != i {
			t.Errorf("part %d landed at %s %d %d", i, snapshot, file, part)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.Path))); err != nil {
			t.Errorf("part %d is not on disk: %v", i, err)
		}
	}
	if total != len(docs) {
		t.Errorf("the parts hold %d documents between them, want %d", total, len(docs))
	}
}

// Nothing is written until there is something to write. An empty part in a repo
// reads as a shard whose documents went missing.
func TestARollThatTookNothingWritesNothing(t *testing.T) {
	r, dir := roll(t, 0)
	files, err := r.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("wrote %d parts without being given a document", len(files))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("left %d entries behind in the directory", len(entries))
	}
}

// This is the seam the upload plugs into, so the ordering matters: a part is
// handed off as it closes and before the next one opens, which is what keeps
// peak disk at one part rather than at all of them.
func TestEachPartIsHandedOffAsItCloses(t *testing.T) {
	docs := []*doc.Document{sample(0), sample(1), sample(2), sample(3)}
	r, dir := roll(t, textOf(docs[0]))

	var handed []PartFile
	r.Finished = func(f PartFile) error {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.Path))); err != nil {
			t.Errorf("%s was handed off before it was in place: %v", f.Path, err)
		}
		handed = append(handed, f)
		return nil
	}

	for _, d := range docs {
		if err := r.Append(d); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	files, err := r.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(handed) != len(files) {
		t.Fatalf("handed off %d parts and wrote %d", len(handed), len(files))
	}
	for i := range files {
		if handed[i].Path != files[i].Path || handed[i].Hash != files[i].Hash {
			t.Errorf("part %d was handed off as %s and written as %s", i, handed[i].Path, files[i].Path)
		}
	}
}

// A part that could not be pushed is not a part that was written. Carrying on
// would leave the ledger claiming a file whose documents are on a disk that is
// about to be reused.
func TestAPartThatCannotBeHandedOffStopsTheRoll(t *testing.T) {
	fail := errors.New("the hub said no")
	docs := []*doc.Document{sample(0), sample(1), sample(2)}
	r, _ := roll(t, textOf(docs[0]))
	r.Finished = func(PartFile) error { return fail }

	var err error
	for _, d := range docs {
		if err = r.Append(d); err != nil {
			break
		}
	}
	if err == nil {
		_, err = r.Close()
	}
	if !errors.Is(err, fail) {
		t.Fatalf("the roll carried on past a failed handoff: %v", err)
	}
	// The path is in the message, because the first question about a failed
	// upload is which one.
	if !strings.Contains(err.Error(), ParquetExt) {
		t.Errorf("the error does not name the part: %v", err)
	}
}

func TestARollThatIsAbandonedLeavesNoPartBehind(t *testing.T) {
	r, dir := roll(t, 0)
	if err := r.Append(sample(0)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	r.Abandon()

	var found []string
	err := filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("an abandoned roll left %v behind", found)
	}
}

// Abandoning after a rollover keeps what was already handed off, since those
// parts are somewhere else by then and taking them back would take back the
// wrong copy.
func TestAbandonKeepsThePartsThatFinished(t *testing.T) {
	docs := []*doc.Document{sample(0), sample(1)}
	r, dir := roll(t, textOf(docs[0]))
	for _, d := range docs {
		if err := r.Append(d); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	done := r.Files()
	if len(done) == 0 {
		t.Fatal("nothing rolled over, so this test is not testing what it says")
	}
	r.Abandon()
	for _, f := range done {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.Path))); err != nil {
			t.Errorf("abandon took back a finished part: %v", err)
		}
	}
}

func TestAppendingToAClosedRollIsAnError(t *testing.T) {
	r, _ := roll(t, 0)
	if _, err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := r.Append(sample(0)); !errors.Is(err, ErrRollClosed) {
		t.Errorf("Append after Close: %v, want ErrRollClosed", err)
	}
}

// Restarting an input file writes over what the dead run left rather than
// beside it, which is what makes an interrupted ingest safe to run again.
func TestARestartWritesTheSamePaths(t *testing.T) {
	docs := []*doc.Document{sample(0), sample(1), sample(2)}
	first, dir := roll(t, textOf(docs[0]))
	for _, d := range docs {
		if err := first.Append(d); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	before, err := first.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := &Roll{Dir: dir, Dataset: textDataset(t), Stamp: stamp, File: 3, TextPerPart: textOf(docs[0])}
	for _, d := range docs {
		if err := second.Append(d); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	after, err := second.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(before) != len(after) {
		t.Fatalf("the restart wrote %d parts and the first run wrote %d", len(after), len(before))
	}
	for i := range before {
		if before[i].Path != after[i].Path {
			t.Errorf("part %d moved from %s to %s", i, before[i].Path, after[i].Path)
		}
	}
}

// The default is derived from the disk budget rather than picked, so a change to
// one should be a change to the other.
func TestTheDefaultPartHoldsAShardOfText(t *testing.T) {
	if TextPerPart <= 0 {
		t.Fatalf("TextPerPart is %d", TextPerPart)
	}
	r, _ := roll(t, 0)
	if r.limit() != TextPerPart {
		t.Errorf("a roll with no limit of its own uses %d, want %d", r.limit(), TextPerPart)
	}
}

func TestARollCountsWhatItHasWritten(t *testing.T) {
	docs := []*doc.Document{sample(0), sample(1), sample(2)}
	r, _ := roll(t, textOf(docs[0]))
	for i, d := range docs {
		if err := r.Append(d); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if got := r.Documents(); got != i+1 {
			t.Errorf("after %d documents the roll counts %d", i+1, got)
		}
	}
	if _, err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := r.Documents(); got != len(docs) {
		t.Errorf("a closed roll counts %d documents, want %d", got, len(docs))
	}
}

// The text limit is the shard target divided by a compression ratio measured on
// one source, and a source that compresses worse than that ratio blows through
// the target without ever reaching the limit. That is not hypothetical: the
// first published GlotCC and FinePDFs parts held the same 1.06 GB of text and
// came out at 512 MB and at 988 MB.
//
// So the roll also closes a part on the size it has reached. The text limit
// here is large enough that nothing could ever hit it, which is the point.
func TestAPartClosesOnSizeWhenTheTextLimitIsNeverReached(t *testing.T) {
	r, _ := roll(t, 1<<40)
	r.BytesPerPart = 20_000

	docs := 400
	for i := 0; i < docs; i++ {
		if err := r.Append(sample(i)); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	files, err := r.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(files) < 2 {
		t.Fatalf("%d documents wrote %d part, and the size limit is %d bytes", docs, len(files), r.BytesPerPart)
	}
	total := 0
	for _, f := range files {
		total += f.Documents
	}
	if total != docs {
		t.Errorf("the parts hold %d documents between them and %d went in", total, docs)
	}
	// Nothing here is within four orders of magnitude of the text limit, so a
	// part that closed did so on its size.
	if r.Documents() == 0 || int64(docs)*1024 >= r.TextPerPart {
		t.Error("the documents could have reached the text limit, so this proves nothing")
	}
}

// The estimate is the ratio the part has measured on itself, and before a row
// group closes there is nothing to measure. A part that has never flushed has
// to fall back to the assumed ratio rather than to zero, because a part
// reporting no size at all would never close on size and the rule would only
// bite on files large enough to have flushed already.
func TestAPartWithNoClosedRowGroupEstimatesAtTheAssumedRatio(t *testing.T) {
	p, err := CreatePart(t.TempDir(), "part-00000.parquet", textDataset(t), stamp)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	d := sample(0)
	if err := p.Append(d); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if p.Bytes() != 0 {
		t.Fatalf("one document put %d bytes on the disk, so a row group closed and this tests the wrong path", p.Bytes())
	}
	if want := int64(float64(len(d.Text)) / fleet.Compression); p.Size() != want {
		t.Errorf("the part estimates %d bytes from %d of text, want %d at the assumed %.2fx",
			p.Size(), len(d.Text), want, fleet.Compression)
	}
	if _, err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// Once a row group has closed the ratio is measured, and on text that
// compresses nothing like the assumption the two answers are far apart. This is
// the whole point of the estimate: FinePDFs compresses at 1.07 and the roll was
// dividing its text by 2.07.
func TestAPartEstimatesAtTheRatioItHasMeasured(t *testing.T) {
	p, err := CreatePart(t.TempDir(), "part-00000.parquet", textDataset(t), stamp)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	// Small groups, so this measures the estimate rather than the disk. The real
	// one is 256 MB and a part is two of them, which is most of a gigabyte
	// through a temp directory to watch some arithmetic.
	defer func(n int64) { RowGroupText = n }(RowGroupText)
	RowGroupText = 8_000_000

	// Documents the size FinePDFs documents are, in text zstd can do nothing
	// with, so the measured ratio comes out near 1.0 the way FinePDFs does and
	// nowhere near the assumed 2.07.
	seed := uint64(1)
	noise := func() string {
		b := make([]byte, 32*1024)
		for i := range b {
			seed = seed*6364136223846793005 + 1442695040888963407
			b[i] = byte(' ' + seed>>59)
		}
		return string(b)
	}
	n := 0
	for ; p.Bytes() == 0; n++ {
		d := sample(n)
		d.Text = noise()
		if err := p.Append(d); err != nil {
			t.Fatalf("Append %d: %v", n, err)
		}
		if n > 100_000 {
			t.Fatal("no row group closed, so nothing here was measured")
		}
	}
	// One short of as much again, so the open group the estimate has to guess at
	// is nearly the size of the closed one it measured against and has not
	// closed itself. Stopping at the flush would leave nothing to guess and the
	// test would pass on any ratio at all.
	for i := 0; i < n-1; i++ {
		d := sample(n + i)
		d.Text = noise()
		if err := p.Append(d); err != nil {
			t.Fatalf("Append %d: %v", n+i, err)
		}
	}

	// The test is against what the file actually came to, which is the only
	// thing the estimate is trying to be.
	estimate, floor, open := p.Size(), p.Bytes(), p.w.OpenText()
	f, err := p.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if open == 0 {
		t.Fatal("the open row group was empty, so the estimate had nothing to estimate")
	}

	if off := math.Abs(float64(estimate-f.Bytes)) / float64(f.Bytes); off > 0.05 {
		t.Errorf("the part estimated %d bytes and closed at %d, off by %.1f%%", estimate, f.Bytes, off*100)
	}
	// What the assumed ratio would have said about the same open group, which is
	// the number this replaced. It reads low, which is the direction that carries
	// a part past the target rather than stopping short of it.
	assumed := floor + int64(float64(open)/fleet.Compression)
	if off := math.Abs(float64(assumed-f.Bytes)) / float64(f.Bytes); off < 0.20 {
		t.Errorf("the assumed %.2fx would have said %d against %d, only %.1f%% out, so this text does not separate the two",
			fleet.Compression, assumed, f.Bytes, off*100)
	}
}

// A row group is buffered in memory, so the row count that bounds it is a
// memory bound only for documents the size it was picked for. FinePDFs averages
// 29.5 KB against the few kilobytes DefaultRowGroup assumes.
func TestARowGroupClosesOnTextBeforeItsRowCount(t *testing.T) {
	var buf bytes.Buffer
	w := NewParquetWriter(&buf, textDataset(t), stamp)

	big := strings.Repeat("Cộng hòa xã hội chủ nghĩa Việt Nam. ", 1024)
	rows := int(RowGroupText/int64(len(big))) + 2
	for i := 0; i < rows; i++ {
		d := sample(i)
		d.Text = big
		if err := w.Append(d); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if rows >= DefaultRowGroup {
		t.Fatalf("%d documents is more than a row group by count, so this proves nothing", rows)
	}
	if buf.Len() == 0 {
		t.Fatalf("%d documents and %d bytes of text produced no row group, so the whole thing is still in memory",
			rows, int64(rows)*int64(len(big)))
	}
}
