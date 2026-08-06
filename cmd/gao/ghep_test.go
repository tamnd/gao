package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// expansionLine writes one graft onto gemma-3-12b the way an expansion run
// records it. Everything the tokenizer decides is held fixed across the
// methods, because the whole point is that the tokenizer decides the same thing
// either way and only the recovery separates them.
func expansionLine(method, box string, recovered int64) string {
	return fmt.Sprintf(
		`{"base":"gemma-3-12b","method":%q,"tied":true,"vocab":262144,"new":32768,"dim":3840,`+
			`"covered":0.31,"before":2.11,"after":1.62,"base_norm":1.42,"new_norm":1.38,`+
			`"frozen":2000,"loss_before":2.0412,"spike":2.618,"recovered":%d,"box":%q}`,
		method, recovered, box)
}

// ghepTrial writes a trial that paid for itself unless a test says otherwise.
func ghepTrial(t *testing.T, lines ...string) string {
	t.Helper()
	if len(lines) == 0 {
		lines = []string{
			expansionLine("pieces", "gamingpc", 1_800_000_000),
			expansionLine("mean", "gamingpc", 5_600_000_000),
		}
	}
	path := filepath.Join(t.TempDir(), "expansions.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheGraftIsCostedAgainstTheRunRatherThanTheTokenizer(t *testing.T) {
	out, errOut, code := exec(t, "ghep", ghepTrial(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"pieces", "mean", "240 MB", "2.11 to 1.62", "23.2%", "gamingpc",
		"is the best of 2 methods", "nets 18.7%",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
	// Both methods bought the same fertility and the cheaper recovery is first,
	// which is the ordering the milestone item is asking about.
	rows := strings.Split(out, "\n")
	if !strings.HasPrefix(rows[1], "pieces") {
		t.Errorf("the table does not lead with the method that netted most:\n%s", out)
	}
}

// The failure the command exists to catch. The fertility improved, every number
// on the tokenizer side reads like a win, and the run is worse for it.
func TestAGraftThatSpendsTheRunRecoveringExitsTwo(t *testing.T) {
	out, _, code := exec(t, "ghep", ghepTrial(t,
		expansionLine("pieces", "gamingpc", 9_000_000_000),
		expansionLine("mean", "gamingpc", 11_000_000_000),
	))
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"22.5% of the run getting back to the loss it started at", "the sequences are shorter and the run is worse"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
}

// Never recovering is not a slow recovery, and the table says so in the column
// rather than by printing a zero.
func TestAGraftThatNeverCameBackExitsTwo(t *testing.T) {
	out, _, code := exec(t, "ghep", ghepTrial(t,
		expansionLine("pieces", "gamingpc", 0),
		expansionLine("mean", "gamingpc", 0),
	))
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"never", "2 methods never came back", "the fertility figure is the only thing that improved"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
}

// The mechanics the item asks for are the rows, and rows drawn from a normal
// are the case it is worth naming.
func TestRowsDrawnFromANormalExitOne(t *testing.T) {
	out, _, code := exec(t, "ghep", ghepTrial(t,
		expansionLine("random", "gamingpc", 1_800_000_000),
		expansionLine("mean", "gamingpc", 5_600_000_000),
	))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "goes through every layer of a body that was already right") {
		t.Errorf("the report accepts rows drawn from a normal:\n%s", out)
	}
}

// A fertility number is a measurement, and a measurement taken on a machine
// nobody has is not one anybody can reproduce.
func TestAFertilityNumberIsCheckedAgainstTheFleet(t *testing.T) {
	out, _, code := exec(t, "ghep", ghepTrial(t,
		expansionLine("pieces", "server9", 1_800_000_000),
		expansionLine("mean", "gamingpc", 5_600_000_000),
	))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "which is not a box on this fleet") {
		t.Errorf("the report accepts a machine nobody has:\n%s", out)
	}
}

func TestTheTrialIsAlsoMachineReadable(t *testing.T) {
	out, errOut, code := exec(t, "ghep", "-json", ghepTrial(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got struct {
		Base     string  `json:"base"`
		Budget   int64   `json:"budget"`
		Methods  int     `json:"methods"`
		Stranded int     `json:"stranded"`
		Best     string  `json:"best"`
		Gain     float64 `json:"gain"`
		Net      float64 `json:"net"`
		Holds    bool    `json:"holds"`
		Readings []struct {
			Method    string  `json:"method"`
			Params    int64   `json:"params"`
			Weight    int64   `json:"weight"`
			Gain      float64 `json:"gain"`
			Ratio     float64 `json:"ratio"`
			Recovered int64   `json:"recovered"`
			Share     float64 `json:"share"`
			Net       float64 `json:"net"`
			Paid      bool    `json:"paid"`
		} `json:"readings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Base != "gemma-3-12b" || got.Methods != 2 || got.Stranded != 0 || !got.Holds {
		t.Errorf("the trial came back as %+v", got)
	}
	if got.Best != "pieces" {
		t.Errorf("the best method came back as %s", got.Best)
	}
	first, second := got.Readings[0], got.Readings[1]
	// Tied embeddings are one matrix, so the graft is 32768 rows rather than
	// twice that, and the two methods graft the identical rows.
	if first.Params != 32_768*3840 || first.Weight != first.Params*2 {
		t.Errorf("a tied graft came back as %d parameters weighing %d bytes", first.Params, first.Weight)
	}
	if first.Gain != second.Gain {
		t.Errorf("one tokenizer gave two fertility numbers, %.4f and %.4f", first.Gain, second.Gain)
	}
	if first.Net <= second.Net || !first.Paid {
		t.Errorf("the cheaper recovery netted %.4f against %.4f", first.Net, second.Net)
	}
	if first.Share < 0.044 || first.Share > 0.046 {
		t.Errorf("1.8B tokens of a 40B budget came back as %.4f", first.Share)
	}
	if first.Ratio < 0.97 || first.Ratio > 0.98 {
		t.Errorf("rows at 1.38 against 1.42 came back as %.4f of the norm", first.Ratio)
	}
}

func TestGhepRefusesWhatItCannotRead(t *testing.T) {
	if _, _, code := exec(t, "ghep"); code != 2 {
		t.Errorf("no argument exited %d", code)
	}
	if _, _, code := exec(t, "ghep", "a.jsonl", "b.jsonl"); code != 2 {
		t.Errorf("two arguments exited %d", code)
	}
	if _, _, code := exec(t, "ghep", filepath.Join(t.TempDir(), "missing.jsonl")); code != 1 {
		t.Errorf("a file that is not there exited %d", code)
	}
}
