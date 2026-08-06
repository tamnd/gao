package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/xep"
	"github.com/zeebo/blake3"
)

// xepPilot writes the published frame drawn small enough to label in a test,
// which is what a pilot is for outside a test as well.
func xepPilot(t *testing.T) (xep.Frame, string) {
	t.Helper()
	f := xep.Fixed()
	f.Size = 240
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "frame.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return f, path
}

// xepLabels writes what a labeling pass that went well returns, with change
// applied first so a test can break exactly one thing.
func xepLabels(t *testing.T, f xep.Frame, change func([]xep.Label) []xep.Label) string {
	t.Helper()
	digest := f.Digest()
	var labels []xep.Label
	n := 0
	for _, sl := range f.Slices {
		for range sl.Wanted(f.Size) {
			band := xep.Bands[n%len(xep.Bands)]
			id := doc.Hash(blake3.Sum256(fmt.Appendf(nil, "doc-%d", n)))
			labels = append(labels, xep.Label{Doc: id, Source: sl.Source, By: "an", Band: band, Frame: digest})
			if n%8 == 0 {
				second := band
				if n%32 == 0 {
					i := slices.Index(xep.Bands, band)
					if i+1 < len(xep.Bands) {
						second = xep.Bands[i+1]
					} else {
						second = xep.Bands[i-1]
					}
				}
				labels = append(labels, xep.Label{Doc: id, Source: sl.Source, By: "binh", Band: second, Frame: digest})
			}
			n++
		}
	}
	if change != nil {
		labels = change(labels)
	}

	lines := make([]string, 0, len(labels))
	for _, l := range labels {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	path := filepath.Join(t.TempDir(), "labels.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheFramePrintsTheDrawAndTheScale(t *testing.T) {
	out, errOut, code := exec(t, "xep", "frame")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, source := range []string{"hplt3", "crawl", "finepdfs", "glotcc"} {
		if !strings.Contains(out, source) {
			t.Errorf("%s is missing from the draw:\n%s", source, out)
		}
	}
	for _, band := range xep.Bands {
		if !strings.Contains(out, string(band)) {
			t.Errorf("%s is missing from the scale:\n%s", band, out)
		}
	}
	if !strings.Contains(out, xep.Fixed().Digest().String()) {
		t.Errorf("the frame does not print its digest:\n%s", out)
	}
	if !strings.Contains(out, xep.Repo) {
		t.Errorf("the frame does not say where it is published:\n%s", out)
	}
}

func TestTheRubricPrintsWhatPutsADocumentInEachBand(t *testing.T) {
	out, _, code := exec(t, "xep", "frame", "-rubric")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "a filed tax ruling is rich") {
		t.Errorf("the boundary between the top two bands is not printed:\n%s", out)
	}
	if !strings.Contains(out, "a novel chapter is rich") {
		t.Errorf("the rubric prints without the worked calls under it:\n%s", out)
	}
}

func TestTheFrameSpeaksJSON(t *testing.T) {
	out, _, code := exec(t, "xep", "frame", "-json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var report struct {
		Frame  xep.Frame `json:"frame"`
		Digest string    `json:"digest"`
		Faults []string  `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the frame is not JSON: %v\n%s", err, out)
	}
	if len(report.Frame.Slices) != len(xep.Fixed().Slices) {
		t.Errorf("the JSON carries %d slices", len(report.Frame.Slices))
	}
	if len(report.Faults) != 0 {
		t.Errorf("the frame we publish was faulted: %v", report.Faults)
	}
}

func TestARubricWithNoBoundaryOnItIsReported(t *testing.T) {
	f := xep.Fixed()
	f.Rules[0].Apart = ""
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "frame.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := exec(t, "xep", "frame", "-frame", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "the only part of a rubric two labelers ever need") {
		t.Errorf("the report does not say what the rubric is missing:\n%s", out)
	}
}

func TestALabelingThatMetTheGatePasses(t *testing.T) {
	f, frame := xepPilot(t)
	out, errOut, code := exec(t, "xep", "read", "-frame", frame, xepLabels(t, f, nil))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "pass:") {
		t.Errorf("the score does not say whether it passed:\n%s", out)
	}
	if !strings.Contains(out, "this is a pilot") {
		t.Errorf("a pilot went out without saying it was one:\n%s", out)
	}
	if !strings.Contains(out, "same band") || !strings.Contains(out, "within one band") {
		t.Errorf("the two agreement numbers do not go out together:\n%s", out)
	}
}

func TestARubricPeopleDoNotAgreeOnFailsTheGate(t *testing.T) {
	f, frame := xepPilot(t)
	path := xepLabels(t, f, func(ls []xep.Label) []xep.Label {
		for i, l := range ls {
			if l.By == "binh" {
				j := slices.Index(xep.Bands, l.Band)
				if j+1 < len(xep.Bands) {
					ls[i].Band = xep.Bands[j+1]
				} else {
					ls[i].Band = xep.Bands[j-1]
				}
			}
		}
		return ls
	})
	out, _, code := exec(t, "xep", "read", "-frame", frame, path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "the rubric is not deciding the band and the labeler is") {
		t.Errorf("the score does not say what low agreement means:\n%s", out)
	}
}

func TestALabelingOnePersonDidAloneIsReported(t *testing.T) {
	f, frame := xepPilot(t)
	path := xepLabels(t, f, func(ls []xep.Label) []xep.Label {
		out := ls[:0]
		for _, l := range ls {
			if l.By == "an" {
				out = append(out, l)
			}
		}
		return out
	})
	out, _, code := exec(t, "xep", "read", "-frame", frame, path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "one person's reading of it") {
		t.Errorf("a set nobody checked was published:\n%s", out)
	}
}

func TestADocumentFromOutsideTheDrawIsReported(t *testing.T) {
	f, frame := xepPilot(t)
	path := xepLabels(t, f, func(ls []xep.Label) []xep.Label {
		id := doc.Hash(blake3.Sum256([]byte("stray")))
		return append(ls, xep.Label{Doc: id, Source: "madlad400", By: "an", Band: xep.Plain})
	})
	out, _, code := exec(t, "xep", "read", "-frame", frame, path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "somebody had it open") {
		t.Errorf("a document from outside the draw was labeled into the set:\n%s", out)
	}
}

func TestTheLabelingSpeaksJSONToo(t *testing.T) {
	f, frame := xepPilot(t)
	out, _, code := exec(t, "xep", "read", "-json", "-frame", frame, xepLabels(t, f, nil))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var score struct {
		Labeled    int     `json:"labeled"`
		Double     int     `json:"double"`
		DoubleRate float64 `json:"double_rate"`
		Exact      float64 `json:"exact"`
		Adjacent   float64 `json:"adjacent"`
		Passed     bool    `json:"passed"`
		ByBand     []struct {
			Band      string `json:"band"`
			Documents int    `json:"documents"`
		} `json:"by_band"`
		ByPerson []struct {
			By     string `json:"by"`
			Labels int    `json:"labels"`
		} `json:"by_person"`
		Faults []string `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &score); err != nil {
		t.Fatalf("the score is not JSON: %v\n%s", err, out)
	}
	if !score.Passed || score.Labeled != 240 || score.Adjacent != 1 {
		t.Errorf("a labeling that met every gate scored %+v", score)
	}
	if len(score.ByBand) != len(xep.Bands) {
		t.Errorf("the JSON carries %d bands", len(score.ByBand))
	}
	if len(score.ByPerson) != 2 || score.ByPerson[0].By != "an" {
		t.Errorf("the JSON does not say who did the labeling: %+v", score.ByPerson)
	}
	if len(score.Faults) != 0 {
		t.Errorf("an honest labeling was faulted: %v", score.Faults)
	}
}

func TestNoLabelFileAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "xep", "read")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "The first label on a document is the band of record") {
		t.Errorf("the usage does not say how a second opinion is treated: %s", errOut)
	}
}

func TestALabelFileThatIsNotThereSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "xep", "read", filepath.Join(t.TempDir(), "nope.jsonl"))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao xep:") {
		t.Errorf("the error is not attributed: %s", errOut)
	}
}

func TestAFrameFileThatIsNotThereSaysSoToo(t *testing.T) {
	_, errOut, code := exec(t, "xep", "frame", "-frame", filepath.Join(t.TempDir(), "nope.json"))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao xep:") {
		t.Errorf("the error is not attributed: %s", errOut)
	}
}

func TestNoXepSubcommandAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "xep")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "gao-refset") {
		t.Errorf("the usage does not say what the set is: %s", errOut)
	}
	if !strings.Contains(errOut, "the share of the corpus that survives") {
		t.Errorf("the usage does not say why the frame is fixed first: %s", errOut)
	}
}

func TestAnUnknownXepSubcommandIsNamed(t *testing.T) {
	_, errOut, code := exec(t, "xep", "label")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "no subcommand named label") {
		t.Errorf("the error does not name the subcommand: %s", errOut)
	}
}
