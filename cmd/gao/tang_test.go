package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The buckets HPLT publishes, with the low quality end holding most of the
// corpus, which is the shape that makes skipping it expensive. The numbers are
// invented, since nothing has been ingested.
var tangBuckets = []struct {
	rank   int
	stored int64
	pack   float64
	rate   float64
}{
	{1, 50_000_000_000, 3.40, 0.228},
	{2, 42_000_000_000, 3.35, 0.230},
	{3, 35_000_000_000, 3.30, 0.232},
	{4, 28_000_000_000, 3.25, 0.234},
	{5, 24_000_000_000, 3.20, 0.236},
	{6, 20_000_000_000, 3.15, 0.238},
	{7, 17_000_000_000, 3.10, 0.240},
	{8, 14_000_000_000, 3.05, 0.242},
	{9, 9_000_000_000, 3.00, 0.244},
	{10, 6_000_000_000, 2.95, 0.246},
}

// tangLayers writes a layer file with the named buckets read at 40 MB each.
func tangLayers(t *testing.T, read ...int) string {
	t.Helper()
	want := map[int]bool{}
	for _, r := range read {
		want[r] = true
	}

	lines := make([]string, 0, len(tangBuckets))
	for _, b := range tangBuckets {
		if !want[b.rank] {
			lines = append(lines, fmt.Sprintf(`{"name":"bucket %d","rank":%d,"stored":%d}`, b.rank, b.rank, b.stored))
			continue
		}
		const n = 40_000_000
		text := int64(n * b.pack)
		lines = append(lines, fmt.Sprintf(
			`{"name":"bucket %d","rank":%d,"stored":%d,"read":%d,"text":%d,"tokens":%d,"tokenizer":"gao-64k"}`,
			b.rank, b.rank, b.stored, n, text, int64(float64(text)*b.rate)))
	}

	path := filepath.Join(t.TempDir(), "layers.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The five buckets the 176B estimate was taken off.
func tangSample(t *testing.T) string { return tangLayers(t, 5, 7, 8, 9, 10) }

func TestTangSaysWhatTheLayersNobodyOpenedAreWorth(t *testing.T) {
	out, errOut, code := exec(t, "tang", "-source", "hplt-v3 vie_Latn", tangSample(t))

	if code != 2 {
		t.Fatalf("a reading that skipped 71%% of the corpus: exit %d, want 2\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"bucket 10",
		"tokens a stored byte",
		"5 of 10 layers were read",
		"nobody read is scaled at",
		"sits below every layer that was read",
		"5 layers holding 71.4% of the source were never read",
		"the rate of the cleaner end of the corpus",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

// The unread layers are the only thing the range is about, so the report has to
// be readable as that rather than as an interval that will close on its own.
func TestTangSaysTheRangeDoesNotCloseByReadingTheSameLayersAgain(t *testing.T) {
	out, _, _ := exec(t, "tang", "-source", "hplt-v3 vie_Latn", tangSample(t))

	if !strings.Contains(out, "does not close by reading more of the 5 that were") {
		t.Errorf("the verdict lets the range read as a sampling interval:\n%s", out)
	}
}

// tangWhole writes a layer file with every layer read end to end rather than
// sampled, which is the only shape that has nothing left to assume.
func tangWhole(t *testing.T) string {
	t.Helper()
	lines := make([]string, 0, len(tangBuckets))
	for _, b := range tangBuckets {
		text := int64(float64(b.stored) * b.pack)
		lines = append(lines, fmt.Sprintf(
			`{"name":"bucket %d","rank":%d,"stored":%d,"read":%d,"text":%d,"tokens":%d,"tokenizer":"gao-64k"}`,
			b.rank, b.rank, b.stored, b.stored, text, int64(float64(text)*b.rate)))
	}
	path := filepath.Join(t.TempDir(), "whole.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A corpus read right through has no unread layers to bound and no layer left
// scaled off a prefix of itself, so it prints one number and exits 0.
func TestTangHoldsWhenEveryLayerWasRead(t *testing.T) {
	out, errOut, code := exec(t, "tang", "-source", "hplt-v3 vie_Latn", tangWhole(t))

	if code != 0 {
		t.Fatalf("a complete reading: exit %d, want 0\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "Every layer has a rate of its own") {
		t.Errorf("the verdict does not say the corpus was read right through:\n%s", out)
	}
	if strings.Contains(out, "carries more than sampling error") {
		t.Errorf("a complete reading reported faults:\n%s", out)
	}
	if strings.Contains(out, "as rich as the richest") {
		t.Errorf("a complete reading printed a range over the part nobody read:\n%s", out)
	}
}

// The real reading, run through the command the way somebody publishing the
// number would run it. Six buckets of HPLT v3 vie_Latn at seed s1, which is the
// file gao taste wrote and the file this project's estimate now rests on.
func TestTangOverTheRealReadingOfEveryBucket(t *testing.T) {
	out, errOut, code := exec(t, "tang", "-source", "hplt3", filepath.Join("..", "..", "tang", "testdata", "hplt3-vie_Latn-s1.jsonl"))

	if code != 2 {
		t.Fatalf("the real reading: exit %d, want 2\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"6 of 6 layers were read, holding 234.5 GB of the 234.5 GB",
		"Every layer has a rate of its own",
		"hplt3 estimates 143.7B tokens over 234.5 GB on disk",
		"5 layers were read over under 1.0% of themselves each, thinnest bucket 8 at 40.0 MB of 94.9 GB",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	// The two faults this fixed. Both are about scaling a rate over a layer
	// nobody read, and every layer here was read.
	for _, gone := range []string{
		"a single pooled rate over the layers nobody read",
		"every unread layer is weighted by",
		"nobody read is scaled at",
		"as rich as the richest",
	} {
		if strings.Contains(out, gone) {
			t.Errorf("a complete reading still says %q:\n%s", gone, out)
		}
	}
}

// The number in the README is the one worth checking, and a reading that does
// not cover it has to say so where somebody publishing the README will see it.
func TestTangChecksTheNumberTheProjectPublishes(t *testing.T) {
	out, _, code := exec(t, "tang", "-source", "hplt-v3 vie_Latn", "-quoted", "400000000000", tangSample(t))

	if code != 2 {
		t.Fatalf("a quoted number outside the reading: exit %d, want 2", code)
	}
	if !strings.Contains(out, "the number this project publishes is 400.0B") {
		t.Errorf("the report does not check the published number:\n%s", out)
	}
}

func TestTangPrintsTheSameReadingAsJSON(t *testing.T) {
	out, _, code := exec(t, "tang", "-source", "hplt-v3 vie_Latn", "-json", tangSample(t))
	if code != 2 {
		t.Fatalf("exit %d, want 2\n%s", code, out)
	}

	var got struct {
		Source    string   `json:"source"`
		Layers    int      `json:"layers"`
		Read      int      `json:"read"`
		Dark      int64    `json:"dark"`
		DarkShare float64  `json:"dark_share"`
		Under     int64    `json:"under"`
		Estimate  int64    `json:"estimate"`
		Low       int64    `json:"low"`
		High      int64    `json:"high"`
		Faults    []string `json:"faults"`
		Holds     bool     `json:"holds"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the JSON does not parse: %v\n%s", err, out)
	}

	if got.Layers != 10 || got.Read != 5 {
		t.Errorf("%d of %d layers read, want 5 of 10", got.Read, got.Layers)
	}
	if got.Dark != 175_000_000_000 {
		t.Errorf("the unread layers hold %d bytes, want 175000000000", got.Dark)
	}
	if got.Under != 155_000_000_000 {
		t.Errorf("the layers below the sample hold %d bytes, want 155000000000", got.Under)
	}
	if got.Low >= got.Estimate || got.High <= got.Estimate {
		t.Errorf("the estimate %d does not sit inside %d to %d", got.Estimate, got.Low, got.High)
	}
	// Three: the unread layers, the gap below the sample, and the five read
	// layers whose rate came off a prefix of themselves.
	if len(got.Faults) != 3 || got.Holds {
		t.Errorf("the reading came back with %d faults and holds=%v", len(got.Faults), got.Holds)
	}
}

func TestTangRefusesAReadingThatIsNotStratified(t *testing.T) {
	out, errOut, code := exec(t, "tang", "-source", "hplt-v3 vie_Latn", tangLayers(t))

	if code != 1 {
		t.Fatalf("a file where no layer was read: exit %d, want 1\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "no layer was read") {
		t.Errorf("the refusal does not say what is missing:\n%s", out)
	}
	if !strings.Contains(out, "This is not a reading of the source") {
		t.Errorf("the refusal does not lead with what it refused:\n%s", out)
	}
}

func TestTangSaysWhichLineOfTheLayersIsWrong(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layers.jsonl")
	if err := os.WriteFile(path, []byte(`{"name":"bucket 10","rank":10,"stored":6000000000}
{"name":"bucket 9","rank":9,"compressed":1}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := exec(t, "tang", "-source", "hplt-v3 vie_Latn", path)
	if code != 1 {
		t.Fatalf("a layer file with a column nobody reads: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, ":2:") || !strings.Contains(errOut, "compressed") {
		t.Errorf("the failure does not name the line and the column:\n%s", errOut)
	}
}

func TestTangUsageErrors(t *testing.T) {
	if _, _, code := exec(t, "tang"); code != 2 {
		t.Errorf("no source and no file: exit %d, want 2", code)
	}
	if _, _, code := exec(t, "tang", "layers.jsonl"); code != 2 {
		t.Errorf("a file and no source: exit %d, want 2", code)
	}

	_, errOut, code := exec(t, "tang", "-h")
	if code != 2 {
		t.Errorf("gao layers -h: exit %d, want 2", code)
	}
	for _, want := range []string{"not a sampling interval", "sit below every layer that was read", "194B"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the usage does not explain %q:\n%s", want, errOut)
		}
	}
}

func TestTangIsInTheCommandList(t *testing.T) {
	out, _, code := exec(t, "help")
	if code != 0 {
		t.Fatalf("gao help: exit %d", code)
	}
	if !strings.Contains(out, "tang") {
		t.Errorf("tang is not in the command list:\n%s", out)
	}
}
