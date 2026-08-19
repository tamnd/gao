package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// engineLine writes one candidate's row of a field file. The other error counts
// scale with the diacritic rate the way a worse reading actually degrades, so a
// row that fails one line of the gate and a row that fails four are both in the
// fixture.
func engineLine(name string, der float64, batch int, vram int64, rate float64) string {
	return fmt.Sprintf(
		`{"engine":%q,"version":"1.0","set":"gao-ocr-eval","pages":200,"box":"gamingpc","batch":%d,"vram":%d,"rate":%.2f,`+
			`"score":{"chars":400000,"read":400000,"marked":100000,"toned":66000,"wrong":%d,"lost":%d,"tone_dropped":%d,"dd":%d}}`,
		name, batch, vram, rate,
		int(der*400_000*1.6), int(der*100_000), int(der*66_000*0.4), int(der*400_000*0.25))
}

// engineField writes a field of four engines, one of which clears the gate.
func engineField(t *testing.T, lines ...string) string {
	t.Helper()
	if len(lines) == 0 {
		lines = []string{
			engineLine("paddleocr", 0.012, 16, 18<<30, 2.4),
			engineLine("surya", 0.018, 8, 18<<30, 1.1),
			engineLine("got-ocr2", 0.027, 4, 18<<30, 0.6),
			engineLine("tesseract", 0.093, 1, 1<<30, 3.8),
		}
	}
	path := filepath.Join(t.TempDir(), "engines.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheFieldIsPublishedWithTheEnginesThatLost(t *testing.T) {
	out, errOut, code := exec(t, "inspect", "field", engineField(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"paddleocr", "surya", "got-ocr2", "tesseract", "3 engines did not clear it", "an announcement"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table does not carry %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "paddleocr wins the field of 4 engines") {
		t.Errorf("the verdict does not name the winner:\n%s", out)
	}
	if !strings.Contains(out, "RTX 4090") || !strings.Contains(out, "24.0 GB card") {
		t.Errorf("the card the result reproduces on is not in the report:\n%s", out)
	}
}

// The batch size and what the card held are the milestone item, and a batch
// sized to fill the GPU is refused rather than published.
func TestABatchSizedToFillTheCardIsRefused(t *testing.T) {
	path := engineField(t,
		engineLine("paddleocr", 0.012, 64, 23_500_000_000, 2.4),
		engineLine("surya", 0.018, 8, 18<<30, 1.1),
		engineLine("got-ocr2", 0.027, 4, 18<<30, 0.6),
	)
	out, errOut, code := exec(t, "inspect", "field", path)
	if code != 1 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "fails the first time anything else is on it") {
		t.Errorf("the report does not say what the batch costs:\n%s", out)
	}
}

// Two engines closer together than the set can resolve are one engine and the
// luckier draw, and naming one of them the winner is publishing the draw.
func TestAWinnerInsideTheSetsOwnPrecisionIsRefused(t *testing.T) {
	path := engineField(t,
		engineLine("paddleocr", 0.0120, 16, 18<<30, 2.4),
		engineLine("surya", 0.0125, 8, 18<<30, 1.1),
		engineLine("got-ocr2", 0.027, 4, 18<<30, 0.6),
	)
	out, errOut, code := exec(t, "inspect", "field", path)
	if code != 1 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "drew the easier pages") {
		t.Errorf("the report does not say the gap is not one:\n%s", out)
	}
}

// The gate has a throughput half, and an engine that clears the diacritic rate
// and cannot read the slice inside the budget exits non zero.
func TestAnEngineThatCannotReadTheSliceInTimeExitsNonZero(t *testing.T) {
	path := engineField(t,
		engineLine("paddleocr", 0.012, 16, 18<<30, 0.4),
		engineLine("surya", 0.018, 8, 18<<30, 0.3),
		engineLine("got-ocr2", 0.027, 4, 18<<30, 0.2),
	)
	out, errOut, code := exec(t, "inspect", "field", path)
	if code != 2 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "no engine that clears the gate is faster") {
		t.Errorf("the report does not price the slice:\n%s", out)
	}
	if !strings.Contains(out, "the 4500 OCR has of the extraction stage's 6000") {
		t.Errorf("the report does not say what OCR's share of the budget is:\n%s", out)
	}

	// The engine that reads best is not always the one that ships, and when they
	// are different the report says both rather than picking one.
	split := engineField(t,
		engineLine("got-ocr2", 0.0094, 4, 19<<30, 0.5),
		engineLine("paddleocr", 0.012, 16, 18<<30, 2.4),
		engineLine("surya", 0.018, 8, 18<<30, 1.1),
	)
	both, _, code := exec(t, "inspect", "field", split)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, both)
	}
	if !strings.Contains(both, "so the path that ships is paddleocr") {
		t.Errorf("the report does not separate reading best from shipping:\n%s", both)
	}

	// And the page count is a plan estimate, so a measured one moves the cost.
	cheap, _, code := exec(t, "inspect", "field", "-pages", "1000000", path)
	if code != 0 {
		t.Fatalf("exit %d with a tenth of the pages: %s", code, cheap)
	}
	if !strings.Contains(cheap, "1.0M pages") {
		t.Errorf("the report does not say what it costed:\n%s", cheap)
	}
}

func TestTheFieldIsAlsoMachineReadable(t *testing.T) {
	out, errOut, code := exec(t, "inspect", "field", "-json", engineField(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got struct {
		Card      string  `json:"card"`
		CardBytes int64   `json:"card_bytes"`
		Engines   int     `json:"engines"`
		Losers    int     `json:"losers"`
		Winner    string  `json:"winner"`
		Holds     bool    `json:"holds"`
		Budget    float64 `json:"budget"`
		Results   []struct {
			Engine   string   `json:"engine"`
			DER      float64  `json:"der"`
			StdErr   float64  `json:"stderr"`
			Batch    int      `json:"batch"`
			Headroom float64  `json:"headroom"`
			GPUHours float64  `json:"gpu_hours"`
			Fails    []string `json:"fails"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Engines != 4 || got.Losers != 3 || got.Winner != "paddleocr" || !got.Holds {
		t.Errorf("the field came back as %+v", got)
	}
	if got.CardBytes != 25_757_614_080 || !strings.Contains(got.Card, "4090") {
		t.Errorf("the card came back as %s at %d bytes", got.Card, got.CardBytes)
	}
	first := got.Results[0]
	if first.Engine != "paddleocr" || len(first.Fails) != 0 || first.Batch != 16 {
		t.Errorf("the winning row came back as %+v", first)
	}
	if first.StdErr <= 0 || first.StdErr > 0.001 {
		t.Errorf("the standard error on a hundred thousand marks came back as %g", first.StdErr)
	}
	if first.GPUHours < 1000 || first.GPUHours > 2000 {
		t.Errorf("the slice came back at %.0f GPU hours", first.GPUHours)
	}
	if last := got.Results[3]; len(last.Fails) < 3 {
		t.Errorf("tesseract failed only %d lines of the gate", len(last.Fails))
	}
}

func TestInspectFieldRefusesWhatItCannotRead(t *testing.T) {
	if _, _, code := exec(t, "inspect", "field"); code != 2 {
		t.Errorf("no argument exited %d", code)
	}
	if _, _, code := exec(t, "inspect", "field", "a.jsonl", "b.jsonl"); code != 2 {
		t.Errorf("two arguments exited %d", code)
	}
	if _, _, code := exec(t, "inspect", "field", filepath.Join(t.TempDir(), "missing.jsonl")); code != 1 {
		t.Errorf("a file that is not there exited %d", code)
	}
	// server1 has no accelerator, and a field is a fact about a card.
	if _, _, code := exec(t, "inspect", "field", "-box", "server1", engineField(t)); code != 2 {
		t.Errorf("a box with no GPU exited %d", code)
	}
}
