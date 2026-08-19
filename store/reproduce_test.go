package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// The claim, once. A snapshot gao wrote rebuilds to the bytes gao recorded, and
// the whole release story rests on this being true before anything else is
// tried.
func TestASnapshotRebuildsToTheSameBytes(t *testing.T) {
	dir, _ := removable(t, 40, 3)

	report, err := Reproduce(dir)
	if err != nil {
		t.Fatalf("Reproduce: %v", err)
	}
	if report.Different != 0 {
		t.Fatalf("%d of %d shards did not rebuild", report.Different, len(report.Shards))
	}
	if report.Same != 3 {
		t.Errorf("%d shards rebuilt, want 3", report.Same)
	}
	if report.Documents != 40 {
		t.Errorf("the rebuild read %d documents, want 40", report.Documents)
	}
	for _, rb := range report.Shards {
		if rb.Got != rb.Want {
			t.Errorf("%s: rebuilt to %s, recorded as %s", rb.Name, rb.Got, rb.Want)
		}
		if rb.Rebuilt != rb.Bytes {
			t.Errorf("%s: rebuilt to %d bytes, recorded as %d", rb.Name, rb.Rebuilt, rb.Bytes)
		}
		if rb.Diff != -1 {
			t.Errorf("%s: a shard that matched reported a difference at %d", rb.Name, rb.Diff)
		}
	}
}

// The one that would make byte identity a fiction. zstd compresses in blocks and
// the encoder is concurrent, so if the block boundaries or the worker count
// reached the output then a shard's bytes would depend on how busy the machine
// was, and no two boxes would ever agree.
func TestTheBytesDoNotDependOnHowManyCoresRanThem(t *testing.T) {
	dir, _ := removable(t, 60, 2)

	was := runtime.GOMAXPROCS(1)
	one, err := Reproduce(dir)
	runtime.GOMAXPROCS(was)
	if err != nil {
		t.Fatalf("Reproduce on one core: %v", err)
	}

	was = runtime.GOMAXPROCS(8)
	many, err := Reproduce(dir)
	runtime.GOMAXPROCS(was)
	if err != nil {
		t.Fatalf("Reproduce on eight cores: %v", err)
	}

	for i := range one.Shards {
		if one.Shards[i].Got != many.Shards[i].Got {
			t.Errorf("%s hashed to %s on one core and %s on eight",
				one.Shards[i].Name, one.Shards[i].Got, many.Shards[i].Got)
		}
	}
}

// Twice in a row, which is the weakest possible version of the claim and the one
// that has to hold before the interesting versions are worth asking about.
func TestTwoRebuildsAgree(t *testing.T) {
	dir, _ := removable(t, 25, 2)

	first, err := Reproduce(dir)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := Reproduce(dir)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	for i := range first.Shards {
		if first.Shards[i] != second.Shards[i] {
			t.Errorf("%s rebuilt differently on the second run", first.Shards[i].Name)
		}
	}
}

// The failure this is built to catch, and it is not corruption. A corrupted
// shard cannot be decompressed at all, so verification catches it and a rebuild
// never gets to see it. The case that gets past verification is a shard that is
// intact, holds exactly the right documents, and is not the file this build of
// gao would write, which is what a settings change or a compressor upgrade looks
// like from the outside.
func TestAShardWrittenWithOtherSettingsDoesNotRebuild(t *testing.T) {
	dir, _ := removable(t, 40, 2, ShardWriterOptions(FrameBytes(2<<10)))

	// It verifies. Every hash is right and every byte is the byte that was
	// written, and it still is not reproducible.
	if _, err := Verify(dir); err != nil {
		t.Fatalf("the fixture does not verify: %v", err)
	}

	report, err := Reproduce(dir)
	if !errors.Is(err, ErrNotReproducible) {
		t.Fatalf("Reproduce: %v, want %v", err, ErrNotReproducible)
	}
	if report == nil {
		t.Fatal("no report came back, so nobody can tell which shard it was")
		return
	}
	if report.Different != 2 {
		t.Fatalf("%d of 2 shards were reported as differing", report.Different)
	}
	for _, rb := range report.Shards {
		if rb.Got == rb.Want {
			t.Errorf("%s: the hashes agree and the shard was called different", rb.Name)
		}
		if rb.Diff < 0 {
			t.Errorf("%s: the rebuild differs and no offset was reported", rb.Name)
		}
		if rb.Frame < 0 {
			t.Errorf("%s: the difference at %d was not placed in a frame", rb.Name, rb.Diff)
		}
	}
	// And the documents are all still there. That is the distinction the whole
	// file exists to keep: the corpus is intact and the container is not the one
	// this binary writes, and reporting that as a damaged corpus would send
	// somebody looking for a failing disk.
	if report.Documents != 40 {
		t.Errorf("the rebuild read %d documents, want 40", report.Documents)
	}
}

// And with the setting it was written at, it rebuilds. Which is the other half
// of the same point: the mismatch above is about how the file was written and
// nothing else.
func TestTheFrameSizeCanBeGivenWhenItIsNotTheDefault(t *testing.T) {
	dir, _ := removable(t, 40, 2, ShardWriterOptions(FrameBytes(2<<10)))

	report, err := Reproduce(dir, ReproduceFrameBytes(2<<10))
	if err != nil {
		t.Fatalf("Reproduce at the size it was written at: %v", err)
	}
	if report.Different != 0 {
		t.Errorf("%d shards did not rebuild at the size they were written at", report.Different)
	}
}

// Verification runs first on purpose. Asking whether bytes rebuild to what a
// manifest says is not a question worth answering until something has
// established that the manifest is the one that was signed.
func TestARebuildRefusesASnapshotThatDoesNotVerify(t *testing.T) {
	dir, _ := removable(t, 20, 2)
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Snapshot = "2026-10"
	if err := os.Remove(filepath.Join(dir, ManifestName)); err != nil {
		t.Fatal(err)
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	if _, err := Reproduce(dir); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Reproduce on an edited manifest: %v, want %v", err, ErrBadSignature)
	}
}

func TestARebuildStopsEarlyWhenAsked(t *testing.T) {
	dir, _ := removable(t, 60, 4, ShardWriterOptions(FrameBytes(2<<10)))

	report, _ := Reproduce(dir, ReproduceStopEarly())
	if report == nil {
		t.Fatal("no report")
		return
	}
	if len(report.Shards) != 1 {
		t.Errorf("stopping early read %d shards, want 1", len(report.Shards))
	}

	// And without it, every shard, because one bad shard and all of them bad are
	// different diagnoses and stopping at the first makes them look the same.
	full, _ := Reproduce(dir)
	if full == nil || full.Different != 4 {
		t.Errorf("the full rebuild did not account for all four shards")
	}
}

func TestProgressIsReportedPerShard(t *testing.T) {
	dir, _ := removable(t, 30, 3)

	var seen []string
	if _, err := Reproduce(dir, ReproduceProgress(func(rb Rebuild) {
		seen = append(seen, rb.Name)
	})); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Errorf("progress fired %d times over 3 shards", len(seen))
	}
}

// A stage that is a function of the document can be checked against the snapshot
// without its inputs, by asking whether the document is a fixed point of it.
func TestARegisteredStageCheckRunsOverEveryDocument(t *testing.T) {
	dir, _ := removable(t, 40, 2)
	t.Cleanup(func() { delete(checks, "gat") })

	var n int
	RegisterStageCheck("gat", func(*doc.Document) error {
		n++
		return nil
	})

	report, err := Reproduce(dir)
	if err != nil {
		t.Fatalf("Reproduce: %v", err)
	}
	if n != 40 {
		t.Errorf("the check saw %d documents, the snapshot holds 40", n)
	}
	if len(report.Stages) != 1 {
		t.Fatalf("%d stages reported, want 1", len(report.Stages))
	}
	st := report.Stages[0]
	if !st.Ran {
		t.Error("a stage with a registered check was reported as not run")
	}
	if st.Checked != 40 || st.Disagreed != 0 {
		t.Errorf("the stage checked %d documents and %d disagreed", st.Checked, st.Disagreed)
	}
}

func TestAStageThatDisagreesFailsTheRebuild(t *testing.T) {
	dir, _ := removable(t, 20, 2)
	t.Cleanup(func() { delete(checks, "gat") })

	// Every document, so the sample being a sample is the thing under test as
	// much as the failure is.
	RegisterStageCheck("gat", func(*doc.Document) error {
		return errors.New("this is not what the stage produces")
	})

	report, err := Reproduce(dir)
	if !errors.Is(err, ErrStageDisagrees) {
		t.Fatalf("Reproduce: %v, want %v", err, ErrStageDisagrees)
	}
	// The bytes are fine. It is the stage claim that failed, and conflating the
	// two would send somebody looking for a corrupted disk.
	if report.Different != 0 {
		t.Errorf("%d shards were reported as not rebuilding", report.Different)
	}
	st := report.Stages[0]
	if st.Disagreed != 20 {
		t.Errorf("%d of 20 documents were reported as disagreeing", st.Disagreed)
	}
	if len(st.Sample) == 0 {
		t.Error("nothing was named, so there is no document to go and open")
	}
	if len(st.Sample) > 5 {
		t.Errorf("%d documents were named, which is a list rather than a sample", len(st.Sample))
	}
}

// Most stages have no check and never will: an ingest stage cannot be re-run
// without the network. Saying so is the point, because a report that lists only
// what it checked reads as a report that checked everything.
func TestAStageWithNoCheckSaysSo(t *testing.T) {
	dir, _ := removable(t, 10, 1)

	report, err := Reproduce(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := report.Stages[0]
	if st.Ran {
		t.Fatal("a stage with no registered check was reported as run")
	}
	if st.Why == "" {
		t.Error("a stage that was not checked did not say why")
	}
	if st.Name != "gat@0.1.0" {
		t.Errorf("the stage is named %q, which is not what the manifest says", st.Name)
	}
	if st.ConfigHash.IsZero() {
		t.Error("the stage was reported without the config hash that makes it rerunnable")
	}
}

// The version is part of a stage's name and is not part of what a check is
// registered under, because the check is the current version of the stage and
// the question is whether the snapshot agrees with it.
func TestTheStageVersionIsNotPartOfTheRegistration(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"gat@0.1.0", "gat"},
		{"phoi@2.3.4-rc1", "phoi"},
		{"sang", "sang"},
		{"", ""},
	} {
		if got := stageName(tc.in); got != tc.want {
			t.Errorf("stageName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The environment is in the report because byte identity is a claim about a
// build. Two boxes that disagree need to be able to see what differed, and it is
// almost always a compressor rather than anything in gao.
func TestTheReportSaysWhatItRanOn(t *testing.T) {
	dir, _ := removable(t, 10, 1)

	report, err := Reproduce(dir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Env.Go == "" || report.Env.OS == "" || report.Env.Arch == "" {
		t.Errorf("the environment is incomplete: %+v", report.Env)
	}
	for _, name := range Encoders {
		if _, ok := report.Env.Modules[name]; !ok {
			t.Errorf("%s decides what bytes come out and is not in the report", name)
		}
	}
	line := report.Env.String()
	if !strings.Contains(line, runtime.GOOS) || !strings.Contains(line, "compress@") {
		t.Errorf("the environment line does not carry what it is for: %q", line)
	}
}

// Two runs of the same binary have to render the environment identically, which
// a map does not do for free.
func TestTheEnvironmentLineIsStable(t *testing.T) {
	want := Environment().String()
	for range 8 {
		if got := Environment().String(); got != want {
			t.Fatalf("two readings of the same build rendered as %q and %q", want, got)
		}
	}
}

func TestTheMirrorFindsTheFirstDifferingByte(t *testing.T) {
	for _, tc := range []struct {
		name       string
		src, write string
		want       int64
	}{
		{"identical", "abcdefgh", "abcdefgh", -1},
		{"first byte", "abcdefgh", "Xbcdefgh", 0},
		{"last byte", "abcdefgh", "abcdefgX", 7},
		{"the write is short", "abcdefgh", "abcd", -1},
		{"the source is short", "abcd", "abcdefgh", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &mirror{src: strings.NewReader(tc.src), diff: -1}
			// A byte at a time, because that is the shape a real write arrives
			// in: many small writes rather than one that lines up with anything.
			for i := range len(tc.write) {
				if _, err := m.Write([]byte{tc.write[i]}); err != nil {
					t.Fatalf("Write: %v", err)
				}
			}
			if m.diff != tc.want {
				t.Errorf("first difference at %d, want %d", m.diff, tc.want)
			}
			if m.off != int64(len(tc.write)) {
				t.Errorf("the mirror counted %d bytes written, want %d", m.off, len(tc.write))
			}
		})
	}
}
