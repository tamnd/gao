package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stageLine writes one stage's reading the way the benchmark appends it: eight
// workers on server3 for ten minutes, with the box's own numbers on the line so
// the reading can be checked against the machine it claims.
func stageLine(name string, rate float64, rss int64) string {
	docs := int64(rate * 600)
	return fmt.Sprintf(
		`{"name":%q,"box":"server3","threads":8,"memory":25199222784,"workers":8,`+
			`"docs":%d,"seconds":600,"bytes":%d,"peak_rss":%d,"solo":%.2f}`,
		name, docs, docs*4200, rss, rate/8/0.85)
}

// throughputStages writes the four stages of the pipeline, all of them publishable
// unless a test hands in something else.
func throughputStages(t *testing.T, lines ...string) string {
	t.Helper()
	if len(lines) == 0 {
		lines = []string{
			stageLine("normalize", 1400, 1<<30),
			stageLine("filter", 900, 1200<<20),
			stageLine("classify", 210, 2<<30),
			stageLine("dedup", 620, 1900<<20),
		}
	}
	path := filepath.Join(t.TempDir(), "stages.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestThePipelinePutsTheBoxOnEveryRate(t *testing.T) {
	out, errOut, code := exec(t, "throughput", throughputStages(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"normalize", "filter", "classify", "dedup",
		"server3", "classify is the slowest stage",
		"an estimated 200M documents", "2.5 GB ceiling",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
	// Every row carries its box, since that is the item rather than a column.
	for _, line := range strings.Split(out, "\n") {
		for _, stage := range []string{"normalize", "filter", "classify", "dedup"} {
			if strings.HasPrefix(line, stage) && !strings.Contains(line, "server3") {
				t.Errorf("the %s row has no box on it: %s", stage, line)
			}
		}
	}

	// The document count is an estimate until somebody counts, and the sentence
	// says which, because every hours figure is linear in it.
	counted, _, code := exec(t, "throughput", "-counted", "-docs", "412000000", throughputStages(t))
	if code != 0 {
		t.Fatalf("a counted corpus exited %d: %s", code, counted)
	}
	if !strings.Contains(counted, "a counted 412M documents") {
		t.Errorf("a counted corpus reads as an estimate:\n%s", counted)
	}
}

// The box label is only worth having if it can be wrong, and the fleet
// inventory is the only thing that can say so.
func TestAReadingThatClaimsABoxItDidNotRunOnExitsOne(t *testing.T) {
	path := throughputStages(t,
		stageLine("normalize", 1400, 1<<30),
		stageLine("filter", 900, 1200<<20),
		strings.Replace(stageLine("classify", 210, 2<<30), `"threads":8`, `"threads":32`, 1),
		stageLine("dedup", 620, 1900<<20),
	)
	out, _, code := exec(t, "throughput", path)
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "the inventory says 8") {
		t.Errorf("the report does not check the reading against the fleet:\n%s", out)
	}

	elsewhere := throughputStages(t,
		stageLine("normalize", 1400, 1<<30),
		stageLine("filter", 900, 1200<<20),
		strings.Replace(stageLine("classify", 210, 2<<30), `"box":"server3"`, `"box":"server4"`, 1),
		stageLine("dedup", 620, 1900<<20),
	)
	out, _, code = exec(t, "throughput", elsewhere)
	if code != 1 {
		t.Fatalf("a box that is not on the fleet exited %d: %s", code, out)
	}
	if !strings.Contains(out, "not a box on this fleet") {
		t.Errorf("the report accepts a machine nobody has:\n%s", out)
	}
}

// The memory half of the milestone is per worker, and a stage that crosses the
// line does not get to keep its throughput number.
func TestAWorkerOverTheCeilingExitsTwo(t *testing.T) {
	path := throughputStages(t,
		stageLine("normalize", 1400, 1<<30),
		stageLine("filter", 900, 1200<<20),
		stageLine("classify", 210, 3<<30),
		stageLine("dedup", 620, 1900<<20),
	)
	out, _, code := exec(t, "throughput", path)
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"3.0 GB in its worst worker", "ceiling of 2.5 GB", "it does not run on server3"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
}

// All eight cores busy is a claim about efficiency, and the reading that
// separates it from top is the single worker rate.
func TestAStageThatOnlyLooksBusyExitsOne(t *testing.T) {
	half := strings.Replace(stageLine("filter", 900, 1200<<20), `"solo":132.35`, `"solo":281.25`, 1)
	out, _, code := exec(t, "throughput", throughputStages(t,
		stageLine("normalize", 1400, 1<<30),
		half,
		stageLine("classify", 210, 2<<30),
		stageLine("dedup", 620, 1900<<20),
	))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "busy in the sense that top says they are busy") {
		t.Errorf("the report does not separate the two senses:\n%s", out)
	}
}

func TestThePipelineIsAlsoMachineReadable(t *testing.T) {
	out, errOut, code := exec(t, "throughput", "-json", throughputStages(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got struct {
		Docs          int64   `json:"docs"`
		Measured      bool    `json:"measured"`
		Stages        int     `json:"stages"`
		Bottleneck    string  `json:"bottleneck"`
		BottleneckBox string  `json:"bottleneck_box"`
		Hours         float64 `json:"hours"`
		Ceiling       int64   `json:"ceiling"`
		Over          int     `json:"over_ceiling"`
		Swapping      int     `json:"swapping"`
		Holds         bool    `json:"holds"`
		Rates         []struct {
			Stage     string  `json:"stage"`
			Box       string  `json:"box"`
			Workers   int     `json:"workers"`
			Rate      float64 `json:"rate"`
			PerWorker float64 `json:"per_worker"`
			Scaling   float64 `json:"scaling"`
			PeakRSS   int64   `json:"peak_rss"`
			Resident  int64   `json:"resident"`
			Hours     float64 `json:"hours"`
			Over      bool    `json:"over_ceiling"`
		} `json:"rates"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Docs != 200_000_000 || got.Measured || got.Stages != 4 || !got.Holds {
		t.Errorf("the pipeline came back as %+v", got)
	}
	if got.Bottleneck != "classify" || got.BottleneckBox != "server3" {
		t.Errorf("the bottleneck came back as %s on %s", got.Bottleneck, got.BottleneckBox)
	}
	if got.Over != 0 || got.Swapping != 0 || got.Ceiling != 2_684_354_560 {
		t.Errorf("over %d, swapping %d, ceiling %d", got.Over, got.Swapping, got.Ceiling)
	}
	slowest := got.Rates[0]
	if slowest.Stage != "classify" || slowest.Box != "server3" || slowest.Workers != 8 {
		t.Errorf("the first row came back as %+v", slowest)
	}
	if slowest.Rate < 209 || slowest.Rate > 211 {
		t.Errorf("classify came back at %.1f documents a second", slowest.Rate)
	}
	if slowest.PerWorker < 26 || slowest.PerWorker > 27 {
		t.Errorf("eight workers at %.0f a second is %.1f each", slowest.Rate, slowest.PerWorker)
	}
	if slowest.Scaling < 0.84 || slowest.Scaling > 0.86 {
		t.Errorf("classify scaled to %.2f", slowest.Scaling)
	}
	if slowest.Resident != slowest.PeakRSS*8 || slowest.Over {
		t.Errorf("resident %d against a peak of %d", slowest.Resident, slowest.PeakRSS)
	}
	// The pipeline is four separate passes over parquet, so its cost is the sum
	// of the stages rather than the slowest of them.
	if got.Hours < slowest.Hours*1.5 {
		t.Errorf("a four stage pipeline costs %.0f hours with a %.0f hour bottleneck", got.Hours, slowest.Hours)
	}
}

func TestThroughputRefusesWhatItCannotRead(t *testing.T) {
	if _, _, code := exec(t, "throughput"); code != 2 {
		t.Errorf("no argument exited %d", code)
	}
	if _, _, code := exec(t, "throughput", "a.jsonl", "b.jsonl"); code != 2 {
		t.Errorf("two arguments exited %d", code)
	}
	if _, _, code := exec(t, "throughput", filepath.Join(t.TempDir(), "missing.jsonl")); code != 1 {
		t.Errorf("a file that is not there exited %d", code)
	}
}
