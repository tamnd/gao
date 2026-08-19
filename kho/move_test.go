package kho

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// hive is the layout the working repo had before this one, which is the only
// thing anything ever has to be moved off.
func hive(snapshot string, file, part int) string {
	return fmt.Sprintf("%s/snapshot=%s/file=%05d/part-%05d%s", DataDir, snapshot, file, part, ParquetExt)
}

// dehive is the rename a migration off the old layout is given. The real one
// lives in the command, since the old layout is a fact about one repo on the
// Hub rather than about this package, and like the real one it takes a path in
// either layout so that a repo halfway between the two re-lays the rest.
func dehive(path string) (string, bool) {
	if snapshot, file, part, ok := ParseStagePath(path); ok {
		return StagePath(snapshot, file, part), true
	}
	rest, ok := strings.CutPrefix(path, DataDir+"/snapshot=")
	if !ok {
		return "", false
	}
	snapshot, rest, ok := strings.Cut(rest, "/")
	if !ok {
		return "", false
	}
	var file, part int
	if _, err := fmt.Sscanf(rest, "file=%d/part-%d"+ParquetExt, &file, &part); err != nil {
		return "", false
	}
	return StagePath(snapshot, file, part), true
}

// fillAt puts parts in the repo at whatever paths it is given, through the
// pusher, so that what is listed is what a run would have left behind.
func fillAt(t *testing.T, h *hub, paths ...string) {
	t.Helper()
	p, dir := h.pusher(), t.TempDir()
	for i, path := range paths {
		local := filepath.Join(dir, fmt.Sprintf("part-%05d.parquet", i))
		body := strings.Repeat("the content of "+path+" ", 8)
		if err := os.WriteFile(local, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Push(t.Context(), local, path); err != nil {
			t.Fatalf("Push %s: %v", path, err)
		}
	}
}

func (h *hub) paths() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.files))
	for p := range h.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// The whole claim the move makes is that a quarter of a terabyte can be re-laid
// without a byte of it being read or written, so the test that matters is that
// storage was never touched.
func TestAMoveWritesThePathsAndNotTheBytes(t *testing.T) {
	h := newHub(t)
	old := []string{hive("glotcc-9ad140b6be3a", 0, 0), hive("glotcc-9ad140b6be3a", 0, 1), hive("hplt3-5b2785d5b11c", 3, 0)}
	fillAt(t, h, old...)
	uploads := h.count("storage")

	p := h.pusher()
	report, err := p.MoveTo(t.Context(), p, DataDir, dehive, nil)
	if err != nil {
		t.Fatalf("MoveTo: %v", err)
	}
	if len(report.Moved) != 3 || len(report.Skipped) != 0 {
		t.Fatalf("three parts moved as %d moved and %d skipped", len(report.Moved), len(report.Skipped))
	}
	if h.count("storage") != uploads {
		t.Errorf("the move sent %d files to storage, and the point of it is that it sends none", h.count("storage")-uploads)
	}
	if report.Spared == 0 {
		t.Error("the move reported no bytes spared, so it cannot say what it saved")
	}

	for _, want := range []string{
		StagePath("glotcc-9ad140b6be3a", 0, 0),
		StagePath("glotcc-9ad140b6be3a", 0, 1),
		StagePath("hplt3-5b2785d5b11c", 3, 0),
	} {
		if h.stored(want) == nil {
			t.Errorf("%s holds nothing after the move", want)
		}
	}
}

// The old path and the new one are two pointers at one object, and the object
// is what a reader gets. If they came back different the move would have
// rewritten content it never read.
func TestAMovedPathHoldsWhatTheOldOneHeld(t *testing.T) {
	h := newHub(t)
	from := hive("glotcc-9ad140b6be3a", 2, 7)
	fillAt(t, h, from)

	p := h.pusher()
	if _, err := p.MoveTo(t.Context(), p, DataDir, dehive, nil); err != nil {
		t.Fatalf("MoveTo: %v", err)
	}
	to := StagePath("glotcc-9ad140b6be3a", 2, 7)
	if a, b := string(h.stored(from)), string(h.stored(to)); a != b {
		t.Errorf("%s holds %q and %s holds %q", from, a, to, b)
	}
}

// A migration killed halfway has to finish from where it got to, and one that
// has already finished has to write nothing at all, or a nightly run puts a
// commit on the repo every night.
func TestASecondMoveOverTheSamePartsWritesNothing(t *testing.T) {
	h := newHub(t)
	fillAt(t, h, hive("glotcc-9ad140b6be3a", 0, 0), hive("glotcc-9ad140b6be3a", 0, 1))

	p := h.pusher()
	if _, err := p.MoveTo(t.Context(), p, DataDir, dehive, nil); err != nil {
		t.Fatalf("first MoveTo: %v", err)
	}
	commits := h.count("commit")

	report, err := p.MoveTo(t.Context(), p, DataDir, dehive, nil)
	if err != nil {
		t.Fatalf("second MoveTo: %v", err)
	}
	if len(report.Moved) != 0 || report.Commits != 0 {
		t.Errorf("the second move wrote %d paths in %d commits", len(report.Moved), report.Commits)
	}
	if h.count("commit") != commits {
		t.Error("the second move put a commit on the repo")
	}
	// Four skips: two old paths that now have a new one, and the two new paths
	// themselves, which parse as already being where they belong.
	if len(report.Skipped) != 4 {
		t.Errorf("the second move skipped %d paths", len(report.Skipped))
	}
}

// A part that was already written at the right path is not work, and reporting
// it as moved would make a finished migration look like it did something.
func TestAPartAlreadyAtItsPathComesBackSkipped(t *testing.T) {
	h := newHub(t)
	good := StagePath("glotcc-9ad140b6be3a", 1, 0)
	fillAt(t, h, good)

	p := h.pusher()
	report, err := p.MoveTo(t.Context(), p, DataDir, dehive, nil)
	if err != nil {
		t.Fatalf("MoveTo: %v", err)
	}
	if len(report.Moved) != 0 {
		t.Fatalf("a part already in place was moved: %v", report.Moved)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].From != report.Skipped[0].To {
		t.Errorf("a part already in place came back as %+v", report.Skipped)
	}
}

// Five hundred parts in five hundred commits would be five hundred entries in
// the repo's history for one decision, and the batch is also the granularity a
// killed run resumes at.
func TestAMoveGoesUpInBatchesRatherThanOneCommitAPart(t *testing.T) {
	h := newHub(t)
	paths := make([]string, 0, MoveBatch+5)
	for i := range MoveBatch + 5 {
		paths = append(paths, hive("glotcc-9ad140b6be3a", 0, i))
	}
	fillAt(t, h, paths...)

	p := h.pusher()
	report, err := p.MoveTo(t.Context(), p, DataDir, dehive, nil)
	if err != nil {
		t.Fatalf("MoveTo: %v", err)
	}
	if len(report.Moved) != len(paths) {
		t.Fatalf("%d parts moved as %d", len(paths), len(report.Moved))
	}
	if report.Commits != 2 {
		t.Errorf("%d parts went up in %d commits, and the batch is %d", len(paths), report.Commits, MoveBatch)
	}
}

// A file that is not a part is not something the move has an opinion about, and
// deleting or relocating one would be the migration touching what it was not
// asked to.
func TestAFileTheRenameDoesNotClaimIsLeftAlone(t *testing.T) {
	h := newHub(t)
	fillAt(t, h, hive("glotcc-9ad140b6be3a", 0, 0), DataDir+"/a-note-somebody-left.txt")

	p := h.pusher()
	report, err := p.MoveTo(t.Context(), p, DataDir, dehive, nil)
	if err != nil {
		t.Fatalf("MoveTo: %v", err)
	}
	if len(report.Moved) != 1 {
		t.Fatalf("the move carried %d files and one of the two is a part", len(report.Moved))
	}
	if h.stored(DataDir+"/a-note-somebody-left.txt") == nil {
		t.Error("the move removed a file it was not asked to carry")
	}
}

func TestDeletingTakesThePathAndLeavesTheObject(t *testing.T) {
	h := newHub(t)
	from := hive("glotcc-9ad140b6be3a", 0, 0)
	fillAt(t, h, from)

	p := h.pusher()
	if _, err := p.MoveTo(t.Context(), p, DataDir, dehive, nil); err != nil {
		t.Fatalf("MoveTo: %v", err)
	}
	if _, err := p.Delete(t.Context(), []string{from}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	to := StagePath("glotcc-9ad140b6be3a", 0, 0)
	if h.stored(from) != nil {
		t.Errorf("%s is still in the repo after being deleted", from)
	}
	if h.stored(to) == nil {
		t.Errorf("deleting %s took the content %s points at with it", from, to)
	}
	for _, p := range h.paths() {
		if strings.Contains(p, "snapshot=") {
			t.Errorf("%s is still spelled the old way", p)
		}
	}
}

func TestDeletingNothingIsNotACommit(t *testing.T) {
	h := newHub(t)
	fillAt(t, h, StagePath("glotcc-9ad140b6be3a", 0, 0))
	commits := h.count("commit")

	n, err := h.pusher().Delete(t.Context(), nil)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if n != 0 || h.count("commit") != commits {
		t.Errorf("deleting nothing took %d commits", n)
	}
}
