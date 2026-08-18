package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamnd/gao/may"
)

// A trace is only worth having if the peak in it is the peak that happened, so
// the test writes a part, watches the watcher see it, deletes it the way a push
// does, and asks whether the file still remembers the high point.
func TestWatchSeesThePeakAndThenTheDeletion(t *testing.T) {
	old := watchEvery
	watchEvery = 5 * time.Millisecond
	defer func() { watchEvery = old }()

	dir := t.TempDir()
	out := filepath.Join(dir, "parts")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "disk.jsonl")

	stage := "write"
	w, err := watch(trace, []string{dir, out}, "server1", func() string { return stage })
	if err != nil {
		t.Fatal(err)
	}

	part := filepath.Join(out, "part-0000.parquet")
	if err := os.WriteFile(part, make([]byte, 64*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, trace, func(ss []may.Sample) bool {
		return len(ss) > 0 && ss[len(ss)-1].Bytes >= 64*1024
	})

	// What a push does. The watcher has to keep sampling through it, because a
	// run that deleted its parts is the run this whole design is for.
	stage = "push"
	if err := os.Remove(part); err != nil {
		t.Fatal(err)
	}
	waitFor(t, trace, func(ss []may.Sample) bool {
		return ss[len(ss)-1].Bytes == 0 && ss[len(ss)-1].Stage == "push"
	})

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	ss := readTrace(t, trace)
	var peak int64
	var sawWrite bool
	for _, s := range ss {
		if s.Bytes > peak {
			peak = s.Bytes
		}
		if s.Stage == "write" {
			sawWrite = true
		}
		if s.Box != "server1" {
			t.Errorf("sample says box %q, want server1", s.Box)
		}
		if s.Second < 0 {
			t.Errorf("sample at second %d, which is before the run started", s.Second)
		}
	}
	if peak != 64*1024 {
		t.Errorf("the trace peaks at %d bytes, want 65536", peak)
	}
	if !sawWrite {
		t.Error("no sample was taken while the run was writing")
	}
	if last := ss[len(ss)-1]; last.Bytes != 0 {
		t.Errorf("the last sample holds %d bytes, and the part was deleted before the close", last.Bytes)
	}
}

// Closing has to be safe on a run that fetched nothing, since that is what a
// source with an empty todo list does and it is not a failure.
func TestWatchOverAnEmptyRun(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "disk.jsonl")
	w, err := watch(trace, []string{t.TempDir(), ""}, "server3", func() string { return "fetch" })
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	ss := readTrace(t, trace)
	if len(ss) != 2 {
		t.Fatalf("an empty run wrote %d samples, want the one at the start and the one at the close", len(ss))
	}
	for _, s := range ss {
		if s.Bytes != 0 || s.Stage != "fetch" {
			t.Errorf("sample %+v, want nothing held during a fetch", s)
		}
	}
}

func TestWatchRefusesAPathItCannotWrite(t *testing.T) {
	_, err := watch(filepath.Join(t.TempDir(), "no-such-dir", "disk.jsonl"), nil, "server1", func() string { return "fetch" })
	if err == nil {
		t.Fatal("watch accepted a path in a directory that does not exist")
	}
}

// heldBytes counts across the directories a run writes to and does not count a
// directory twice when -out is under -dir, which is how the flags are usually
// given.
func TestHeldBytes(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "parts")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ledger.jsonl"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "part-0000.parquet"), make([]byte, 2000), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := heldBytes([]string{dir, out, dir, ""})
	if err != nil {
		t.Fatal(err)
	}
	// dir contains out, so walking dir already counted the part. Naming both is
	// the normal case and must not double it.
	if got != 3000 {
		t.Errorf("held %d bytes, want 3000", got)
	}
}

// A directory that is not there yet is nothing held, not a failure. The parts
// directory is created when the first part opens, which is after the watcher
// has already taken its first sample.
func TestHeldBytesTreatsAMissingDirectoryAsEmpty(t *testing.T) {
	got, err := heldBytes([]string{filepath.Join(t.TempDir(), "not-yet")})
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("held %d bytes over a directory that does not exist, want 0", got)
	}
}

func waitFor(t *testing.T, path string, ok func([]may.Sample) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ss := readTrace(t, path); len(ss) > 0 && ok(ss) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the trace never reached the state the test was waiting for: %s", path)
}

func readTrace(t *testing.T, path string) []may.Sample {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var ss []may.Sample
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var s may.Sample
		if err := json.Unmarshal(line, &s); err != nil {
			// A half written line is what a reader gets when it reads the trace
			// while the run is still going, and it is not the failure here.
			continue
		}
		ss = append(ss, s)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return ss
}
