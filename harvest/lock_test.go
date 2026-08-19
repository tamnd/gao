package harvest_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/harvest"
)

// lockFile is where the claim lands, which several tests need to write by hand
// because the interesting states are the ones a healthy run never produces.
func lockFile(dir string) string { return filepath.Join(dir, harvest.LockName) }

// writeLock plants a lock file the way a previous run would have left one.
func writeLock(t *testing.T, dir string, h harvest.Holder) {
	t.Helper()
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockFile(dir), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLockingAnEmptyDirectorySucceedsAndSaysWhoHoldsIt(t *testing.T) {
	dir := t.TempDir()
	lock, err := harvest.LockDir(dir, "gao harvest hf")
	if err != nil {
		t.Fatalf("locking a fresh directory: %v", err)
	}
	defer func() { _ = lock.Release() }()

	h, err := harvest.ReadHolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.PID != os.Getpid() {
		t.Errorf("the lock names pid %d, and this process is %d", h.PID, os.Getpid())
	}
	if h.Box != fleet.Label() {
		t.Errorf("the lock names box %q, and this box is %q", h.Box, fleet.Label())
	}
	if h.Command != "gao harvest hf" {
		t.Errorf("the lock names command %q, want the one that took it", h.Command)
	}
	if h.Started == "" {
		t.Error("the lock does not say when it was taken")
	}
}

// LockDir creates the directory, because an ingest into a path that does not
// exist yet is the ordinary first run.
func TestLockingCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ingest")
	lock, err := harvest.LockDir(dir, "gao harvest hf")
	if err != nil {
		t.Fatalf("locking a directory that does not exist yet: %v", err)
	}
	defer func() { _ = lock.Release() }()

	if _, err := os.Stat(lockFile(dir)); err != nil {
		t.Errorf("the lock file is not there: %v", err)
	}
}

func TestASecondIngestIsRefusedWhileTheFirstHoldsTheDirectory(t *testing.T) {
	dir := t.TempDir()
	first, err := harvest.LockDir(dir, "gao harvest hf")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release() }()

	second, err := harvest.LockDir(dir, "gao harvest hf")
	if err == nil {
		_ = second.Release()
		t.Fatal("a second ingest took a directory the first one is holding")
	}
	if !errors.Is(err, harvest.ErrLocked) {
		t.Errorf("the refusal is %v, want it to wrap ErrLocked", err)
	}
}

// The refusal has to be actionable. Somebody reading it at two in the morning
// needs the box, the process, and the file to remove.
func TestTheRefusalNamesTheHolderAndTheFileToRemove(t *testing.T) {
	dir := t.TempDir()
	lock, err := harvest.LockDir(dir, "gao harvest hf")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()

	_, err = harvest.LockDir(dir, "gao harvest hf")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{fleet.Label(), "gao harvest hf", harvest.LockName} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

func TestReleasingLetsTheNextIngestIn(t *testing.T) {
	dir := t.TempDir()
	first, err := harvest.LockDir(dir, "gao harvest hf")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("releasing: %v", err)
	}
	if _, err := os.Stat(lockFile(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock file survived the release: %v", err)
	}

	second, err := harvest.LockDir(dir, "gao harvest hf")
	if err != nil {
		t.Fatalf("locking after a release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestReleasingTwiceIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	lock, err := harvest.LockDir(dir, "gao harvest hf")
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("releasing a lock that is already released: %v", err)
	}
}

// The case this whole file is about. A run killed with SIGKILL leaves its lock,
// and the next run on the same box has to be able to get in without anybody
// deleting a file by hand.
func TestALockLeftByADeadProcessOnThisBoxIsBroken(t *testing.T) {
	dir := t.TempDir()
	writeLock(t, dir, harvest.Holder{
		Box:     fleet.Label(),
		PID:     deadPID(t),
		Started: "2026-08-04T03:14:53Z",
		Command: "gao harvest hf",
	})

	lock, err := harvest.LockDir(dir, "gao harvest hf")
	if err != nil {
		t.Fatalf("locking over a dead holder: %v", err)
	}
	defer func() { _ = lock.Release() }()

	h, err := harvest.ReadHolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.PID != os.Getpid() {
		t.Errorf("the lock still names pid %d, want this process", h.PID)
	}
}

// A live process is never evicted, whoever owns it. This is the failure the
// timeout design would have had: a stalled 26.6 GB download looks exactly like
// a dead one from outside.
func TestALockHeldByALiveProcessIsNeverBroken(t *testing.T) {
	dir := t.TempDir()
	writeLock(t, dir, harvest.Holder{
		Box: fleet.Label(), PID: os.Getpid(),
		Started: "2026-08-04T03:14:53Z", Command: "gao harvest hf",
	})

	if _, err := harvest.LockDir(dir, "gao harvest hf"); !errors.Is(err, harvest.ErrLocked) {
		t.Errorf("locking over a live holder gave %v, want ErrLocked", err)
	}
}

// A PID from another machine means nothing on this one, so a lock from another
// box is never broken no matter what that number does here.
func TestALockFromAnotherBoxIsNeverBroken(t *testing.T) {
	dir := t.TempDir()
	writeLock(t, dir, harvest.Holder{
		Box: "some-other-box", PID: deadPID(t),
		Started: "2026-08-04T03:14:53Z", Command: "gao harvest hf",
	})

	_, err := harvest.LockDir(dir, "gao harvest hf")
	if !errors.Is(err, harvest.ErrLocked) {
		t.Fatalf("locking over another box gave %v, want ErrLocked", err)
	}
	if !strings.Contains(err.Error(), "some-other-box") {
		t.Errorf("the refusal does not name the other box: %v", err)
	}
}

// A lock file that was truncated mid-write still locks the directory. Reading
// unreadable as absent is how a partial write turns into two ingests.
func TestATruncatedLockFileStillLocksTheDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(lockFile(dir), []byte(`{"box":"server1","pi`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := harvest.LockDir(dir, "gao harvest hf")
	if !errors.Is(err, harvest.ErrLocked) {
		t.Fatalf("locking over an unreadable lock gave %v, want ErrLocked", err)
	}
	if !strings.Contains(err.Error(), harvest.LockName) {
		t.Errorf("the refusal does not say which file to remove: %v", err)
	}
}

// Release does not delete a claim it does not hold, because the run whose lock
// was broken should not take the replacement down with it when it exits.
func TestReleaseLeavesALockThatBelongsToSomebodyElse(t *testing.T) {
	dir := t.TempDir()
	lock, err := harvest.LockDir(dir, "gao harvest hf")
	if err != nil {
		t.Fatal(err)
	}
	writeLock(t, dir, harvest.Holder{
		Box: fleet.Label(), PID: os.Getpid() + 1,
		Started: "2026-08-04T03:14:53Z", Command: "gao harvest hf",
	})

	if err := lock.Release(); err == nil {
		t.Error("releasing a lock that now belongs to another process reported success")
	}
	if _, err := os.Stat(lockFile(dir)); err != nil {
		t.Errorf("the other process's lock was deleted: %v", err)
	}
}

func TestReadHolderOnADirectoryWithNoLockIsNotAnError(t *testing.T) {
	h, err := harvest.ReadHolder(t.TempDir())
	if err != nil {
		t.Fatalf("reading an unlocked directory: %v", err)
	}
	if h.PID != 0 || h.Box != "" {
		t.Errorf("an unlocked directory reported holder %+v, want the zero one", h)
	}
}

func TestAHolderPrintsAsOneLine(t *testing.T) {
	h := harvest.Holder{Box: "server1", PID: 4242, Started: "2026-08-04T03:14:53Z", Command: "gao harvest hf"}
	s := h.String()
	if strings.Contains(s, "\n") {
		t.Errorf("a holder printed over more than one line: %q", s)
	}
	for _, want := range []string{"server1", "4242", "gao harvest hf", "2026-08-04T03:14:53Z"} {
		if !strings.Contains(s, want) {
			t.Errorf("the holder line does not mention %q: %q", want, s)
		}
	}
}

// deadPID returns the PID of a process that has exited, by running this test
// binary with a filter that matches nothing and waiting for it.
//
// The alternative is picking a large number and hoping, which is how a test
// like this passes for a year and then fails on a busy box for reasons nobody
// connects to locking.
func deadPID(t *testing.T) int {
	t.Helper()
	// os.Executable rather than os.Args[0], which on a test binary invoked by
	// name is a bare relative path, and Go refuses to run one of those.
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("finding this test binary: %v", err)
	}
	cmd := exec.Command(self, "-test.run=TestThisMatchesNothingOnPurpose")
	if err := cmd.Run(); err != nil {
		t.Fatalf("running a probe process: %v", err)
	}
	return cmd.Process.Pid
}
