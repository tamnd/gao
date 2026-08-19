package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/slice"
	"github.com/tamnd/gao/store"
)

func sliceID(s string) doc.Hash { return doc.SumString(s) }

// sliceParent writes a small sealed snapshot and returns its directory and the
// manifest, so a test can pin a slice to it.
func sliceParent(t *testing.T, snapshot string) (string, *store.Manifest) {
	t.Helper()
	dir := t.TempDir()
	m := &store.Manifest{
		ManifestVersion: store.ManifestVersion,
		SchemaVersion:   doc.SchemaVersion,
		Snapshot:        snapshot,
		CreatedAt:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		Pipeline:        "0.1.0",
		Stages:          []store.Stage{{Name: "normalize@0.1.0", ConfigHash: sliceID("normalize")}},
		Counts: store.Counts{
			Documents: 1000, Natural: 1000,
			Bytes: 4_000_000, Chars: 3_600_000, Syllables: 800_000,
			Tokens: 1_200_000, Tokenizer: "gemma-3",
		},
		Shards: []store.Shard{
			{Name: "part-00000.parquet", Index: 0, Documents: 600, Bytes: 600_000, Hash: sliceID(snapshot + " shard 0")},
			{Name: "part-00001.parquet", Index: 1, Documents: 400, Bytes: 400_000, Hash: sliceID(snapshot + " shard 1")},
		},
		Root: sliceID(snapshot + " root"),
	}
	if err := store.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	return dir, m
}

// sliceSlice writes a slice over that snapshot and returns its directory.
func sliceFile(t *testing.T, name string, m *store.Manifest, change func(*slice.Slice)) string {
	t.Helper()
	s := &slice.Slice{
		SliceVersion: slice.SliceVersion,
		Name:         name,
		Dataset:      "vietnamese-web-text",
		Parent:       m.Snapshot,
		ParentDigest: m.Digest(),
		ParentRoot:   m.Root,
		CreatedAt:    time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
		Tokenizer:    "gemma-3",
		Classes:      []doc.LicenseClass{doc.LicenseOpen},
		Rule:         slice.Rule{Where: "quality >= 0.8", Note: "the part worth fine tuning on"},
		Members: []slice.Member{
			{
				Shard: 0, Hash: m.Shards[0].Hash, Documents: 120,
				Bytes: 480_000, Chars: 432_000, Syllables: 96_000, Tokens: 144_000,
				Docs: slice.Members([]doc.Hash{sliceID("a"), sliceID("b")}),
			},
			{
				Shard: 1, Hash: m.Shards[1].Hash, Documents: 80,
				Bytes: 320_000, Chars: 288_000, Syllables: 64_000, Tokens: 96_000,
				Docs: slice.Members([]doc.Hash{sliceID("c")}),
			},
		},
	}
	if change != nil {
		change(s)
	}
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := slice.Write(dir, s); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestASliceThatIsAViewPasses(t *testing.T) {
	snap, m := sliceParent(t, "gao-v1.0")
	edu := sliceFile(t, "gao-edu", m, nil)

	out, errOut, code := exec(t, "slice", "-snapshot", snap, edu)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "gao-edu") || !strings.Contains(out, "vietnamese-web-text") {
		t.Errorf("the table does not say what the slice is or where it goes:\n%s", out)
	}
	if !strings.Contains(out, "20.0%") {
		t.Errorf("the slice takes 200 of 1000 documents and the share is not printed:\n%s", out)
	}
}

func TestTheReportSaysWhatPublishingAsCopiesWouldHaveCost(t *testing.T) {
	// The disk is the smaller reason for a view and it is still worth printing,
	// because it is the part a reader can check against the corpus size.
	snap, m := sliceParent(t, "gao-v1.0")
	edu := sliceFile(t, "gao-edu", m, nil)

	out, _, code := exec(t, "slice", "-snapshot", snap, edu)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "holds a byte of its own") {
		t.Errorf("the report does not say the slices hold nothing:\n%s", out)
	}
	if !strings.Contains(out, "800.0 kB") {
		t.Errorf("the report does not say what the copies would have duplicated:\n%s", out)
	}
}

func TestASliceOverAResealedSnapshotFails(t *testing.T) {
	snap, m := sliceParent(t, "gao-v1.0")
	edu := sliceFile(t, "gao-edu", m, nil)

	// Reseal under the same name, which is what a slice pinned by name alone
	// would not notice.
	m.Counts.Rejected = 12
	if err := os.Remove(filepath.Join(snap, store.ManifestName)); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteManifest(snap, m); err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "slice", "-snapshot", snap, edu)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "resealed") {
		t.Errorf("the report does not say the snapshot moved underneath the slice:\n%s", out)
	}
}

func TestASliceBehindARemovalIsReportedStale(t *testing.T) {
	snap, m := sliceParent(t, "gao-v1.0")
	edu := sliceFile(t, "gao-edu", m, nil)

	head, hm := sliceParent(t, "gao-v1.1")
	hm.Parent = "gao-v1.0"
	hm.Tombstones = []store.Tombstone{
		{DocID: sliceID("a"), Reason: store.ReasonTakedown, RemovedAt: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)},
	}
	if err := os.Remove(filepath.Join(head, store.ManifestName)); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteManifest(head, hm); err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "slice", "-snapshot", snap, "-head", head, edu)
	if code != 1 {
		t.Fatalf("a slice behind a takedown exited %d", code)
	}
	if !strings.Contains(out, "re-derive") {
		t.Errorf("the report does not say what to do about a stale slice:\n%s", out)
	}
	if !strings.Contains(out, "gao-v1.1") {
		t.Errorf("the report does not name the snapshot that superseded the parent:\n%s", out)
	}
}

func TestASliceWithoutAHeadIsNotCheckedForStaleness(t *testing.T) {
	// Only -head can answer that question, and a report that guessed at it would
	// be reporting on a lineage it was not shown.
	snap, m := sliceParent(t, "gao-v1.0")
	edu := sliceFile(t, "gao-edu", m, nil)

	out, _, code := exec(t, "slice", "-snapshot", snap, edu)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out, "re-derive") {
		t.Errorf("staleness was reported without a head to compare against:\n%s", out)
	}
}

func TestASliceIntoARepoThatWillNotHaveItFails(t *testing.T) {
	snap, m := sliceParent(t, "gao-v1.0")
	bad := sliceFile(t, "gao-restricted", m, func(s *slice.Slice) {
		s.Classes = []doc.LicenseClass{doc.LicenseRestricted}
	})

	out, _, code := exec(t, "slice", "-snapshot", snap, bad)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "does not admit") {
		t.Errorf("the report does not say why the repo refuses the slice:\n%s", out)
	}
}

func TestSeveralSlicesAreReportedTogether(t *testing.T) {
	snap, m := sliceParent(t, "gao-v1.0")
	edu := sliceFile(t, "gao-edu", m, nil)
	legal := sliceFile(t, "gao-legal", m, func(s *slice.Slice) {
		s.Name = "gao-legal"
		s.Dataset = "vietnamese-legal-text"
		s.Rule.Where = "source = 'legal'"
		s.Members = s.Members[:1]
	})

	out, _, code := exec(t, "slice", "-snapshot", snap, edu, legal)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "2 slices") {
		t.Errorf("the report does not count the slices:\n%s", out)
	}
	if !strings.Contains(out, "gao-legal") || !strings.Contains(out, "gao-edu") {
		t.Errorf("a slice is missing from the report:\n%s", out)
	}
}

func TestTheReportSpeaksJSON(t *testing.T) {
	snap, m := sliceParent(t, "gao-v1.0")
	edu := sliceFile(t, "gao-edu", m, nil)

	out, _, code := exec(t, "slice", "-json", "-snapshot", snap, edu)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var report struct {
		Snapshot string `json:"snapshot"`
		Saved    int64  `json:"saved"`
		Faults   int    `json:"faults"`
		Slices   []struct {
			Slice struct {
				Name string `json:"Name"`
			} `json:"slice"`
		} `json:"slices"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out)
	}
	if report.Snapshot != "gao-v1.0" || report.Saved != 800_000 || report.Faults != 0 {
		t.Errorf("the JSON does not carry the report: %+v", report)
	}
	if len(report.Slices) != 1 || report.Slices[0].Slice.Name != "gao-edu" {
		t.Errorf("the JSON does not carry the slice: %+v", report)
	}
}

func TestNoSnapshotAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "slice")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "-snapshot") {
		t.Errorf("the usage does not say what is missing: %s", errOut)
	}
}

func TestASnapshotThatIsNotThereSaysSo(t *testing.T) {
	dir := t.TempDir()
	_, errOut, code := exec(t, "slice", "-snapshot", dir, dir)
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, store.ManifestName) {
		t.Errorf("the error does not name what was missing: %s", errOut)
	}
}
