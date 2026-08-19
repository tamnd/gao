package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mauSource writes the two files the plan is drawn from: the layers, five of
// them read at 40 MB each, and a listing that cuts every layer into shards of
// about a gigabyte.
func mauSource(t *testing.T, shard int64) (layers, files string) {
	t.Helper()

	stored := []int64{
		42_000_000_000, 55_000_000_000, 71_000_000_000, 96_000_000_000, 104_000_000_000,
		98_000_000_000, 88_000_000_000, 66_000_000_000, 49_000_000_000, 31_000_000_000,
	}
	read := map[int]bool{5: true, 7: true, 8: true, 9: true, 10: true}

	dir := t.TempDir()
	layerLines := make([]string, 0, len(stored))
	var fileLines []string
	for i, n := range stored {
		name := fmt.Sprintf("bucket-%d", i+1)
		row := map[string]any{"name": name, "rank": i + 1, "stored": n}
		if read[i+1] {
			row["read"] = 40_000_000
			row["text"] = 100_000_000
			row["tokens"] = 26_000_000
			row["tokenizer"] = "gemma-3"
		}
		line, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		layerLines = append(layerLines, string(line))

		for k, left := 0, n; left > 0; k++ {
			take := min(left, shard)
			line, err := json.Marshal(map[string]any{
				"layer": name,
				"path":  fmt.Sprintf("%s/%04d.jsonl.zst", name, k),
				"bytes": take,
			})
			if err != nil {
				t.Fatal(err)
			}
			fileLines = append(fileLines, string(line))
			left -= take
		}
	}

	layers = filepath.Join(dir, "layers.jsonl")
	files = filepath.Join(dir, "files.jsonl")
	if err := os.WriteFile(layers, []byte(strings.Join(layerLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files, []byte(strings.Join(fileLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return layers, files
}

func TestMauPlansTheReadingTheEstimateHasBeenWaitingOn(t *testing.T) {
	layers, files := mauSource(t, 900_000_000)

	out, errOut, code := exec(t, "mau", "-source", "hplt-v3", "-seed", "hplt-v3-2026-08", "-layers", layers, files)
	if code != 0 {
		t.Fatalf("a plan over the five unread buckets: exit %d, want 0\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"hplt-v3, 5 of 10 layers already read, 5 layers to open at 40.0 MB each",
		"seed hplt-v3-2026-08, digest ",
		"This plan reads 200.0 MB off 80 shards across 5 layers of 10 layers",
		"takes hplt-v3 from 5 of 10 layers read to 10 of 10",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not say %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"bucket-5 ", "bucket-7 ", "bucket-10 "} {
		if strings.Contains(out, gone) {
			t.Errorf("the plan reads %q, which somebody already read:\n%s", gone, out)
		}
	}
}

// The list a fetcher consumes, which is the plan itself rather than a summary of
// it.
func TestMauPrintsTheReadListOnItsOwn(t *testing.T) {
	layers, files := mauSource(t, 900_000_000)

	out, _, code := exec(t, "mau", "-source", "hplt-v3", "-seed", "s", "-layers", layers, "-takes", files)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 80 {
		t.Fatalf("%d lines in the read list, want 80", len(lines))
	}
	for _, line := range lines {
		var path string
		var bytes, of int64
		if _, err := fmt.Sscanf(line, "%s %d %d", &path, &bytes, &of); err != nil {
			t.Fatalf("a line a fetcher cannot read: %q", line)
		}
		if bytes != 2_500_000 || of < bytes || of > 900_000_000 {
			t.Errorf("%s reads %d bytes of %d", path, bytes, of)
		}
	}
}

func TestMauDrawsTheSameShardsForTheSameSeedAndOthersForAnother(t *testing.T) {
	layers, files := mauSource(t, 900_000_000)

	first, _, _ := exec(t, "mau", "-source", "hplt-v3", "-seed", "one", "-layers", layers, "-takes", files)
	again, _, _ := exec(t, "mau", "-source", "hplt-v3", "-seed", "one", "-layers", layers, "-takes", files)
	other, _, _ := exec(t, "mau", "-source", "hplt-v3", "-seed", "two", "-layers", layers, "-takes", files)

	if first != again {
		t.Error("the same seed drew a different read list the second time")
	}
	if first == other {
		t.Error("two seeds drew the same read list, so the seed decides nothing")
	}
}

func TestMauPrintsTheSamePlanAsJSON(t *testing.T) {
	layers, files := mauSource(t, 900_000_000)

	out, _, code := exec(t, "mau", "-source", "hplt-v3", "-seed", "s", "-layers", layers, "-json", files)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}

	var got struct {
		Source string `json:"source"`
		Seed   string `json:"seed"`
		Want   int64  `json:"want"`
		Digest string `json:"digest"`
		Bytes  int64  `json:"bytes"`
		Takes  int    `json:"takes"`
		Lit    int    `json:"lit"`
		Opens  int    `json:"opens"`
		Shut   int    `json:"shut"`
		Reads  []struct {
			Layer string `json:"layer"`
			Takes []struct {
				Path  string `json:"path"`
				Bytes int64  `json:"bytes"`
			} `json:"takes"`
		} `json:"reads"`
		Covered int      `json:"covered"`
		Layers  int      `json:"layers"`
		Holds   bool     `json:"holds"`
		Faults  []string `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the JSON does not parse: %v\n%s", err, out)
	}

	if got.Source != "hplt-v3" || got.Seed != "s" || got.Want != 40_000_000 {
		t.Errorf("the plan came back as %s at seed %s reading %d", got.Source, got.Seed, got.Want)
	}
	if len(got.Digest) != 64 {
		t.Errorf("the digest is %d characters", len(got.Digest))
	}
	if got.Bytes != 200_000_000 || got.Takes != 80 {
		t.Errorf("%d bytes off %d shards, want 200000000 off 80", got.Bytes, got.Takes)
	}
	if got.Lit != 5 || got.Opens != 5 || got.Shut != 0 {
		t.Errorf("%d read, %d opened, %d shut", got.Lit, got.Opens, got.Shut)
	}
	if got.Covered != 10 || got.Layers != 10 {
		t.Errorf("the plan covers %d of %d layers", got.Covered, got.Layers)
	}
	if len(got.Reads) != 5 || len(got.Reads[0].Takes) != 16 {
		t.Fatalf("%d layers with %d shards on the first", len(got.Reads), len(got.Reads[0].Takes))
	}
	if !got.Holds || len(got.Faults) != 0 {
		t.Errorf("an ordinary plan came back holds=%v with %d faults", got.Holds, len(got.Faults))
	}
}

// The reading this project quotes is 40 MB a bucket, and 40 MB off shards this
// big is four shards, which is four stretches of the crawl.
func TestMauSaysWhenALayerCannotBeSpreadAcrossEnoughShards(t *testing.T) {
	layers, files := mauSource(t, 12_000_000_000)

	out, _, code := exec(t, "mau", "-source", "hplt-v3", "-seed", "s", "-layers", layers, files)
	if code != 2 {
		t.Fatalf("a layer of four huge shards: exit %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "5 layers are read off fewer than 16 shards each, starting with bucket-1 at 4") {
		t.Errorf("the plan does not say the reading is not spread:\n%s", out)
	}
	if !strings.Contains(out, "This is not the sample it looks like:") {
		t.Errorf("the plan does not lead its faults:\n%s", out)
	}
}

func TestMauRefusesAPlanNobodyCanDrawAgain(t *testing.T) {
	layers, files := mauSource(t, 900_000_000)

	out, _, code := exec(t, "mau", "-source", "hplt-v3", "-layers", layers, files)
	if code != 1 {
		t.Fatalf("a plan with no seed: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "This is not a plan anybody can run, so no shard was drawn:") {
		t.Errorf("the refusal does not lead with what it refused:\n%s", out)
	}
	if !strings.Contains(out, "names no seed") {
		t.Errorf("the refusal does not say what is missing:\n%s", out)
	}
}

func TestMauSaysWhichLineOfTheListingIsWrong(t *testing.T) {
	layers, files := mauSource(t, 900_000_000)
	body, err := os.ReadFile(files)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	lines[1] = strings.Replace(lines[1], `"bytes"`, `"rows"`, 1)
	if err := os.WriteFile(files, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := exec(t, "mau", "-source", "hplt-v3", "-seed", "s", "-layers", layers, files)
	if code != 1 {
		t.Fatalf("a listing with a column nobody reads: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, ":2:") || !strings.Contains(errOut, "rows") {
		t.Errorf("the failure does not name the line and the column:\n%s", errOut)
	}
}

func TestMauUsageErrors(t *testing.T) {
	layers, files := mauSource(t, 900_000_000)

	if _, _, code := exec(t, "mau", "-source", "hplt-v3", "-seed", "s", files); code != 2 {
		t.Errorf("no layer file: exit %d, want 2", code)
	}
	if _, _, code := exec(t, "mau", "-source", "hplt-v3", "-seed", "s", "-layers", layers); code != 2 {
		t.Errorf("no listing: exit %d, want 2", code)
	}

	_, errOut, code := exec(t, "mau", "-h")
	if code != 2 {
		t.Errorf("gao sample -h: exit %d, want 2", code)
	}
	for _, want := range []string{
		"cannot be entered in the middle",
		"the file count as",
		"blake3 of the seed with the path",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the usage does not explain %q:\n%s", want, errOut)
		}
	}
}

func TestMauIsInTheCommandList(t *testing.T) {
	out, _, code := exec(t, "help")
	if code != 0 {
		t.Fatalf("gao help: exit %d", code)
	}
	if !strings.Contains(out, "which shards of a layer nobody has read get read") {
		t.Errorf("mau is not in the command list:\n%s", out)
	}
}
