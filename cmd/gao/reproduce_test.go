package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
	"github.com/tamnd/gao/phoi"
)

// prose is a page of real Vietnamese as the cleaning stages would leave it: long
// enough to clear sang's length floor, varied enough to clear its repetition
// bound, and put through phoi so that phoi run again over it changes nothing.
func prose(i int) string {
	return phoi.Normalize(fmt.Sprintf("%s Đây là tài liệu số %d trong bộ.", dauPages[i%len(dauPages)], i)).Text
}

// rebuildable is [removableSnapshot] with the stage list under the test's
// control, because what a rebuild says about a stage is most of what it says.
func rebuildable(t *testing.T, n, shards int, stages []string, text func(i int) string) string {
	t.Helper()
	dir := t.TempDir()

	set, err := kho.NewShardSet[*doc.Document](dir, shards, func(d *doc.Document) doc.Hash { return d.DocID })
	if err != nil {
		t.Fatalf("NewShardSet: %v", err)
	}
	m := &kho.Manifest{
		Snapshot:  "2026-09",
		CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		Pipeline:  "0.1.0",
		Box:       "server1",
	}
	for _, s := range stages {
		m.Stages = append(m.Stages, kho.Stage{Name: s, ConfigHash: doc.SumString(s + " config")})
	}
	m.Counts.BySource = map[string]int64{}
	for i := range n {
		d := document(t, i)
		if text != nil {
			d.Text = text(i)
			d.DocID = doc.SumString(d.Text)
			d.RawID = doc.SumString("raw:" + d.Text)
			d.NChars = uint32(len([]rune(d.Text)))
			if err := d.Admit(); err != nil {
				t.Fatalf("the fixture does not satisfy the ingest contract: %v", err)
			}
		}
		if err := set.Append(d); err != nil {
			t.Fatalf("Append: %v", err)
		}
		m.Counts.Documents++
		m.Counts.Natural++
		m.Counts.Bytes += int64(len(d.Text))
		m.Counts.Chars += int64(d.NChars)
		m.Counts.BySource[string(d.Source)]++
	}
	m.Shards, err = set.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, priv, err := kho.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Seal(priv, m.CreatedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := kho.WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	return dir
}

func TestKhoReproduceRebuildsASnapshotAndSaysWhatItRanOn(t *testing.T) {
	dir := rebuildable(t, 40, 3, []string{"gat@0.1.0"}, nil)

	out, errOut, code := exec(t, "kho", "reproduce", dir)
	if code != 0 {
		t.Fatalf("gao store reproduce: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"2026-09", "40", "3 rebuilt to the same bytes, 0 did not", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	// The versions of the compressors are the answer to the question two boxes
	// that disagree will be asking, so they are printed on a clean run too.
	if !strings.Contains(out, "compress@") {
		t.Errorf("the report does not name the compressor it was built against:\n%s", out)
	}
}

// A shard that is intact, holds the right documents, and is not the file this
// build would write. That is what a settings change or a compressor upgrade
// looks like, and it is the only failure a rebuild sees that verification does
// not, since a corrupted shard cannot be read back at all.
func TestKhoReproduceOnASnapshotWrittenWithOtherSettings(t *testing.T) {
	dir := rebuildable(t, 30, 2, []string{"gat@0.1.0"}, nil)

	out, errOut, code := exec(t, "kho", "reproduce", "-frame-bytes", "2048", dir)
	if code != 1 {
		t.Fatalf("gao store reproduce at the wrong frame size: exit %d\n%s", code, out)
	}
	for _, want := range []string{"the shards that did not rebuild", "recorded", "rebuilt", "first differ at byte"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	// The sentence that stops somebody replacing a disk over this.
	if !strings.Contains(errOut, "the documents are intact") {
		t.Errorf("the failure does not say the corpus is fine:\n%s", errOut)
	}
}

func TestKhoReproduceVerboseAccountsForEveryShard(t *testing.T) {
	dir := rebuildable(t, 30, 3, []string{"gat@0.1.0"}, nil)
	m, err := kho.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "kho", "reproduce", "-v", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, sh := range m.Shards {
		if !strings.Contains(out, sh.Name) {
			t.Errorf("-v did not account for %s:\n%s", sh.Name, out)
		}
	}
}

func TestKhoReproduceStopsAtTheFirstFailureWhenAsked(t *testing.T) {
	dir := rebuildable(t, 40, 4, []string{"gat@0.1.0"}, nil)

	out, _, code := exec(t, "kho", "reproduce", "-stop", "-frame-bytes", "2048", "-v", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if n := strings.Count(out, "differs"); n != 1 {
		t.Errorf("stopping early reported %d shards, want 1:\n%s", n, out)
	}
}

// The stages this binary knows how to re-run are run. Normalizing an already
// normalized document, and classifying an already accepted one, are the same
// check twice: a stage that is a function has to leave its own output alone.
func TestKhoReproduceRerunsTheStagesItKnows(t *testing.T) {
	dir := rebuildable(t, 20, 2, []string{"phoi@0.1.0", "sang@0.1.0"}, prose)

	out, errOut, code := exec(t, "kho", "reproduce", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if n := strings.Count(out, "20 documents agree"); n != 2 {
		t.Errorf("%d of 2 stages were re-run over every document:\n%s", n, out)
	}
	for _, want := range []string{"phoi@0.1.0", "sang@0.1.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not name %s:\n%s", want, out)
		}
	}
}

func TestKhoReproduceCatchesDocumentsAStageWouldNotProduce(t *testing.T) {
	// Text with a no-break space and a decomposed syllable in it, which is
	// exactly what a snapshot normalized by an older phoi looks like.
	dir := rebuildable(t, 12, 2, []string{"phoi@0.1.0"}, func(i int) string {
		return "Ba\u0300i viê\u0301t sô\u0301 " + string(rune('A'+i)) + ".\u00a0" +
			"Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc. " +
			"Nội dung của tài liệu này đủ dài để vượt qua ngưỡng tối thiểu của hợp đồng nhập liệu."
	})

	out, errOut, code := exec(t, "kho", "reproduce", dir)
	if code != 1 {
		t.Fatalf("a snapshot phoi would rewrite was accepted: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "documents disagree") {
		t.Errorf("the report does not say the stage disagreed:\n%s", out)
	}
	if !strings.Contains(errOut, "not what its stage produces") {
		t.Errorf("the failure does not name the fault:\n%s", errOut)
	}
	// And the bytes were fine. Conflating the two would send somebody looking
	// for a failing disk.
	if !strings.Contains(out, "2 rebuilt to the same bytes, 0 did not") {
		t.Errorf("a stage failure was reported as a byte failure:\n%s", out)
	}
	// Named, so the next step is opening a document rather than looking for one.
	if !strings.Contains(out, "phoi@0.1.0, starting with:") {
		t.Errorf("no document was named:\n%s", out)
	}
}

// Most stages will never have a check. Saying so is the point, because a report
// that lists only what it checked reads as a report that checked everything.
func TestKhoReproduceSaysWhichStagesItCouldNotCheck(t *testing.T) {
	dir := rebuildable(t, 10, 1, []string{"gat@0.1.0", "xay@0.2.0"}, nil)

	out, _, code := exec(t, "kho", "reproduce", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"gat@0.1.0", "xay@0.2.0", "not checked"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

func TestKhoReproduceRefusesASnapshotThatDoesNotVerify(t *testing.T) {
	dir := rebuildable(t, 10, 1, []string{"gat@0.1.0"}, nil)
	m, err := kho.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Snapshot = "2026-10"
	if err := os.Remove(filepath.Join(dir, kho.ManifestName)); err != nil {
		t.Fatal(err)
	}
	if err := kho.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := exec(t, "kho", "reproduce", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "signature") {
		t.Errorf("the failure does not say the manifest is not the one that was signed:\n%s", errOut)
	}
}

func TestKhoReproduceUsageErrors(t *testing.T) {
	dir := rebuildable(t, 5, 1, []string{"gat@0.1.0"}, nil)

	if _, _, code := exec(t, "kho", "reproduce"); code != 2 {
		t.Errorf("no snapshot: exit %d, want 2", code)
	}
	if _, _, code := exec(t, "kho", "reproduce", dir, dir); code != 2 {
		t.Errorf("two snapshots: exit %d, want 2", code)
	}
	if _, _, code := exec(t, "kho", "reproduce", filepath.Join(t.TempDir(), "nope")); code != 1 {
		t.Errorf("a snapshot that is not there: exit %d, want 1", code)
	}
}

func TestKhoReproduceIsInTheSubcommandList(t *testing.T) {
	out, _, code := exec(t, "kho", "help")
	if code != 0 {
		t.Fatalf("gao store help: exit %d", code)
	}
	if !strings.Contains(out, "reproduce") {
		t.Errorf("reproduce is not in the subcommand list:\n%s", out)
	}
}
