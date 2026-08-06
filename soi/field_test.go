package soi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// card is the one GPU on the fleet, which is the 24 GiB in gamingpc.
const card int64 = 25_769_803_776

// reading builds a score over a two hundred page set at the given rates, so a
// test can say what an engine did without saying it in counts.
func reading(der, cer, tone, dd float64) Score {
	return Score{
		Chars: 400_000, Read: 400_000, Marked: 100_000, Toned: 66_000,
		Lost: int(der * 100_000), Wrong: int(cer * 400_000),
		ToneDropped: int(tone * 66_000), DD: int(dd * 400_000),
	}
}

// entry is one engine that recorded everything the milestone asks for.
func entry(name string, der float64) Candidate {
	return Candidate{
		Engine: name, Version: "1.0", Set: "gao-ocr-eval", Pages: 200,
		Box: "gamingpc", Batch: 16, VRAM: 18 << 30, Rate: 2.0,
		Score: reading(der, 0.020, 0.005, 0.003),
	}
}

// field is four engines read on one set, with one clearing the gate.
func field(ders ...float64) Field {
	names := []string{"paddle", "surya", "tesseract", "got-ocr", "docling"}
	f := Field{Card: card, Pages: Slice, Gate: S4}
	for i, der := range ders {
		f.Candidates = append(f.Candidates, entry(names[i], der))
	}
	return f
}

func refuses(t *testing.T, f Field, want string) {
	t.Helper()
	for _, why := range f.Blocking() {
		if strings.Contains(why, want) {
			return
		}
	}
	t.Errorf("nothing blocking mentions %q, got:\n  %s", want, strings.Join(f.Blocking(), "\n  "))
}

// The item asks for the losers, and this is what having them buys: an order, a
// gap, and a table somebody outside the project can argue with.
func TestTheFieldPublishesTheLosersAndNamesTheWinner(t *testing.T) {
	f := field(0.012, 0.016, 0.024, 0.038)
	if !f.Settled() {
		t.Fatalf("a clean field was refused: %v", f.Blocking())
	}
	if !f.Passed() || !f.Affordable() || !f.Holds() {
		t.Fatalf("passed %v, affordable %v, holds %v", f.Passed(), f.Affordable(), f.Holds())
	}
	w, _ := f.Winner()
	if w.Engine != "paddle" {
		t.Errorf("the field was won by %s at %.3f", w.Engine, w.Score.DER())
	}
	if got := f.Losers(); len(got) != 3 || got[0].Engine != "surya" {
		t.Errorf("the losers came back as %v", got)
	}
	if got := f.Ranked()[3].Engine; got != "got-ocr" {
		t.Errorf("the ranking puts %s last", got)
	}
	for _, want := range []string{"wins the field of 4 engines", "1.20%", "batch 16", "24.0 GB card", "GPU hours for the slice"} {
		if !strings.Contains(f.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, f.Verdict())
		}
	}
}

// Two hundred pages hold about a hundred thousand marks, which places a rate to
// within a tenth of a point. Two engines closer together than that are one
// engine and the luckier draw.
func TestTwoEnginesInsideWhatTheSetCanResolveAreOneEngine(t *testing.T) {
	close := field(0.0120, 0.0125, 0.024, 0.038)
	if close.Separated() {
		t.Fatal("half a tenth of a point separated two engines on two hundred pages")
	}
	refuses(t, close, "naming which engine drew the easier pages")

	apart := field(0.0120, 0.0160, 0.024, 0.038)
	if !apart.Separated() {
		t.Errorf("four tenths of a point did not separate them: %v", apart.Blocking())
	}

	// One engine is not separated from anything, and the thing wrong with it is
	// that it is one engine.
	alone := field(0.012)
	if !alone.Separated() {
		t.Error("a field of one was called unseparated")
	}
	refuses(t, alone, "the engine somebody picked rather than a search")
}

// The milestone item is that a published result reproduces on the same card, and
// that takes the batch size, the memory it held, and room left on the card.
func TestAPublishedResultThatDoesNotReproduceOnTheCardIsRefused(t *testing.T) {
	full := field(0.012, 0.016, 0.024)
	full.Candidates[0].VRAM = 23_500_000_000
	refuses(t, full, "fails the first time anything else is on it")

	noBatch := field(0.012, 0.016, 0.024)
	noBatch.Candidates[1].Batch = 0
	refuses(t, noBatch, "which is the whole of what publishing it is for")

	noVRAM := field(0.012, 0.016, 0.024)
	noVRAM.Candidates[1].VRAM = 0
	refuses(t, noVRAM, "a setting rather than a result anybody can repeat")

	noVersion := field(0.012, 0.016, 0.024)
	noVersion.Candidates[2].Version = ""
	refuses(t, noVersion, "a different engine two releases later")
}

func TestEnginesReadOnDifferentPagesAreNotCompared(t *testing.T) {
	sets := field(0.012, 0.016, 0.024)
	sets.Candidates[2].Set = "someone-elses-set"
	refuses(t, sets, "two numbers rather than a difference")

	subset := field(0.012, 0.016, 0.024)
	subset.Candidates[2].Pages = 40
	refuses(t, subset, "compares a whole engine against part of a run")

	boxes := field(0.012, 0.016, 0.024)
	boxes.Candidates[2].Box = "server3"
	refuses(t, boxes, "a comparison of the boxes")

	twice := field(0.012, 0.016, 0.024)
	twice.Candidates = append(twice.Candidates, twice.Candidates[0])
	refuses(t, twice, "two readings by one engine are not two engines")
}

// The gate has a second half. An engine that clears the diacritic rate and reads
// at a tenth of the rate is not the engine this slice ships.
func TestAnEngineThatClearsTheGateAndCannotFinishTheSliceIsNotTheWinner(t *testing.T) {
	slow := field(0.012, 0.016, 0.024)
	for i := range slow.Candidates {
		slow.Candidates[i].Rate = 0.4
	}
	if !slow.Settled() || !slow.Passed() {
		t.Fatalf("a slow field was refused on something else: %v", slow.Blocking())
	}
	if slow.Affordable() {
		t.Fatalf("%.0f GPU hours came back affordable", slow.Ranked()[0].Cost(slow.Pages))
	}
	if !strings.Contains(slow.Verdict(), "no engine that clears the gate is faster") {
		t.Errorf("the verdict does not price the slice: %s", slow.Verdict())
	}

	// And the engine that reads best is not always the one that ships, which is
	// the case the second column exists for.
	split := field(0.012, 0.014, 0.024)
	split.Candidates[0].Rate = 0.4
	if w, ok := split.Winner(); !ok || w.Engine != "surya" {
		t.Fatalf("the path that ships came back as %s", w.Engine)
	}
	if !strings.Contains(split.Verdict(), "so the path that ships is surya") {
		t.Errorf("the verdict does not separate reading best from shipping: %s", split.Verdict())
	}

	none := field(0.012, 0.016, 0.024)
	none.Candidates[1].Rate = 0
	refuses(t, none, "hold its throughput across a full batch")
}

// P04-4 is about an engine with no Vietnamese finetune on it. A field where only
// the finetuned entries clear meets the gate and refutes the prediction, and the
// report says both.
func TestP044IsAboutAnEngineNobodyFinetuned(t *testing.T) {
	f := field(0.010, 0.020, 0.030)
	f.Candidates[0].Finetuned = true
	if !f.Settled() || !f.Passed() {
		t.Fatalf("passed %v: %v", f.Passed(), f.Blocking())
	}
	if f.Holds() {
		t.Fatal("a finetuned winner held a prediction about not finetuning")
	}
	if !strings.Contains(f.Verdict(), "somebody has to keep training") {
		t.Errorf("the verdict does not say what the finetune costs: %s", f.Verdict())
	}
}

// Nothing clearing the gate is the kill criterion, and the verdict says what
// happens next rather than naming the threshold that was crossed.
func TestNoEngineClearingTheGateSaysWhatShipsInstead(t *testing.T) {
	f := field(0.019, 0.026, 0.034)
	if f.Passed() || f.Holds() {
		t.Fatal("a field where nothing cleared the gate passed it")
	}
	if !strings.Contains(f.Verdict(), "the born-digital subset") {
		t.Errorf("the verdict does not say what ships: %s", f.Verdict())
	}
	if !strings.Contains(f.Verdict(), "diacritic error rate is 1.90%") {
		t.Errorf("the verdict does not say what it missed by: %s", f.Verdict())
	}

	empty := Field{Card: card, Pages: Slice, Gate: S4}
	if empty.Settled() || empty.Passed() || empty.Holds() {
		t.Error("an empty field settled the OCR path")
	}
	if _, ok := empty.Winner(); ok {
		t.Error("an empty field has a winner")
	}
	if !strings.Contains(empty.Verdict(), "whatever somebody picks") {
		t.Errorf("the verdict on nothing reads %q", empty.Verdict())
	}
}

// A diacritic error rate over a page with no marks on it is zero for the wrong
// reason, and a score with nothing behind it is not a score.
func TestAScoreWithNothingBehindItIsNotAResult(t *testing.T) {
	blank := field(0.012, 0.016, 0.024)
	blank.Candidates[2].Score = Score{}
	refuses(t, blank, "has no characters behind its score")

	unmarked := field(0.012, 0.016, 0.024)
	unmarked.Candidates[2].Score = Score{Chars: 400_000, Read: 400_000}
	refuses(t, unmarked, "zero for the wrong reason")

	nameless := field(0.012, 0.016, 0.024)
	nameless.Candidates[2].Engine = ""
	refuses(t, nameless, "cannot be published as a result for one")
}

func TestAFieldIsReadFromWhatTheHarnessAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "engines.jsonl")
	body := `{"engine":"paddle","version":"3.0","set":"gao-ocr-eval","pages":200,"box":"gamingpc","batch":16,"vram":19327352832,"rate":2.4,"score":{"chars":400000,"marked":100000,"toned":66000,"lost":1200,"wrong":8000}}

{"engine":"surya","version":"0.14","finetuned":true,"set":"gao-ocr-eval","pages":200,"box":"gamingpc","batch":8,"vram":19327352832,"rate":1.1,"score":{"chars":400000,"marked":100000,"toned":66000,"lost":2400,"wrong":9000}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ReadField(card, Slice, S4, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Candidates) != 2 {
		t.Fatalf("read %d engines", len(f.Candidates))
	}
	if w, _ := f.Winner(); w.Engine != "paddle" || w.Cost(f.Pages) < 1000 {
		t.Errorf("the winner came back as %s at %.0f GPU hours", w.Engine, w.Cost(f.Pages))
	}
	if h := f.Candidates[0].Headroom(card); h < 0.24 || h > 0.26 {
		t.Errorf("18 GiB of a 24 GiB card left %.3f", h)
	}

	// A column nobody declared is the harness and the reader disagreeing about
	// what was written down.
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"engine":"paddle","der":0.012}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadField(card, Slice, S4, bad); err == nil {
		t.Error("a line with a column nobody declared was read")
	}

	blank := filepath.Join(dir, "blank.jsonl")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadField(card, Slice, S4, blank); err == nil {
		t.Error("an empty file was read as a field")
	}
	if _, err := ReadField(card, Slice, S4, filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a field that is not there was read")
	}
}
