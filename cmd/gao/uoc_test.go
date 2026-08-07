package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// partLine is one reading, at a rate near the 0.234 tokens a byte this project
// measured on GlotCC.
func partLine(n int, megabytes int64, rate float64) string {
	b := megabytes * 1_000_000
	return fmt.Sprintf(
		`{"path":"vie_Latn_%04d.parquet","bytes":%d,"manifest":%d,"tokens":%d,"tokenizer":"gao-64k"}`,
		n, b, b, int64(float64(b)*rate))
}

// uocSample writes k readings whose sizes vary the way real shards do.
func uocSample(t *testing.T, k int) string {
	t.Helper()
	lines := make([]string, 0, k)
	for i := range k {
		lines = append(lines, partLine(i, int64(220+(i%9)*180), 0.225+0.004*float64(i%7)))
	}
	return uocFile(t, lines...)
}

func uocFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func uocArgs(path string, extra ...string) []string {
	args := make([]string, 0, 12+len(extra))
	args = append(args,
		"uoc",
		"-source", "hplt-v3 vie_Latn",
		"-snapshot", "gao-2026-09",
		"-seed", "hplt-v3-2026-08",
		"-parts", "1214",
		"-bytes", "703000000000")
	return append(append(args, extra...), path)
}

func TestUocPublishesAnIntervalRatherThanANumber(t *testing.T) {
	out, errOut, code := exec(t, uocArgs(uocSample(t, 44))...)
	if code != 0 {
		t.Fatalf("a clean sample exited %d: %s\n%s", code, errOut, out)
	}
	if !strings.Contains(out, "what gets published is the interval rather than its middle") {
		t.Errorf("the verdict does not lead with the interval:\n%s", out)
	}
	if !strings.Contains(out, "ratio on bytes") || !strings.Contains(out, "mean per part") {
		t.Errorf("the two estimators are not both printed:\n%s", out)
	}
}

func TestUocRefusesASampleTooSmallToCarryAnInterval(t *testing.T) {
	out, _, code := exec(t, uocArgs(uocSample(t, 12))...)
	if code != 1 {
		t.Fatalf("a twelve part sample exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "reads as precision") {
		t.Errorf("the refusal does not say why a small sample is worse than none:\n%s", out)
	}
}

func TestUocRefusesASampleWithNoSeed(t *testing.T) {
	path := uocSample(t, 44)
	args := []string{"uoc", "-source", "hplt-v3", "-parts", "1214", "-bytes", "703000000000", path}
	out, _, code := exec(t, args...)
	if code != 1 {
		t.Fatalf("a sample with no seed exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "no sampling distribution") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
}

func TestUocExitsTwoWhenTheEstimateIsTooWideToPublish(t *testing.T) {
	// Rates spread from 0.16 to 0.44 across the band, which is a corpus nobody
	// has read enough of yet rather than a broken one.
	lines := make([]string, 0, 32)
	for i := range 32 {
		lines = append(lines, partLine(i, int64(220+(i%9)*180), 0.16+0.028*float64(i%11)))
	}
	out, _, code := exec(t, uocArgs(uocFile(t, lines...))...)
	if code != 2 {
		t.Fatalf("an estimate too wide to publish exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "more parts rather than another argument") {
		t.Errorf("the verdict does not price closing the interval:\n%s", out)
	}
	if !strings.Contains(out, "fourfold cost of halving an interval") {
		t.Errorf("the cost of narrowing is not stated:\n%s", out)
	}
}

func TestUocChecksAnExactCountAgainstTheIntervalItPublished(t *testing.T) {
	path := uocSample(t, 44)
	out, _, code := exec(t, uocArgs(path, "-exact", "166000000000")...)
	if code != 0 {
		t.Fatalf("an exact count inside the interval exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "the sampled number stood") {
		t.Errorf("a covered count does not read as one:\n%s", out)
	}

	out, _, code = exec(t, uocArgs(path, "-exact", "142000000000")...)
	if code != 2 {
		t.Fatalf("an exact count outside the interval exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "the sample missed") {
		t.Errorf("a missed estimate does not say so:\n%s", out)
	}
}

func TestUocJSONCarriesBothEstimatorsAndTheInterval(t *testing.T) {
	out, _, code := exec(t, uocArgs(uocSample(t, 44), "-json")...)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{`"estimate"`, `"mean"`, `"low"`, `"high"`, `"width"`, `"needed"`, `"seed"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the JSON does not carry %s:\n%s", want, out)
		}
	}
}

func TestUocWithoutTheManifestTotalsIsAUsageError(t *testing.T) {
	path := uocSample(t, 44)
	for _, args := range [][]string{
		{"uoc"},
		{"uoc", "-source", "x", path},
		{"uoc", "-source", "x", "-parts", "1214", path},
	} {
		if _, _, code := exec(t, args...); code != 2 {
			t.Errorf("%v exited %d and it is a usage error", args, code)
		}
	}
	if _, _, code := exec(t, uocArgs(filepath.Join(t.TempDir(), "nothing.jsonl"))...); code != 1 {
		t.Error("a sample file that is not there did not exit 1")
	}
}
