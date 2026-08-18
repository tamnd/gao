package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The logs these tests run against are the real training runs in vot/testdata,
// which is the point: a report about training stability that has only ever been
// pointed at a curve somebody wrote down is a report nobody should act on.
func log(name string) string { return filepath.Join("..", "..", "vot", "testdata", name+".jsonl") }

func TestVotPrintsWhatTheRunDidAndWhatItWouldHaveCost(t *testing.T) {
	out, errOut, code := exec(t, "vot", "-run", "on-dinh", "-total", "100000", "-checkpoint", "200", log("on-dinh"))

	if code != 0 {
		t.Errorf("a clean run exited %d:\n%s", code, out)
	}
	if errOut != "" {
		t.Errorf("something went to stderr: %s", errOut)
	}
	for _, want := range []string{
		"on-dinh, 400 rows from step 0 to step 3,990, every 10 steps.",
		"median loss",
		"times the scatter",
		"checkpoint every 200 steps",
		"of a 100,000 step run",
		"the protocol had nothing to do",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

func TestVotExitsTwoWhenTheRunIsNotTheOneItLooksLike(t *testing.T) {
	out, _, code := exec(t, "vot", "-run", "phan-ky", "-total", "100000", "-checkpoint", "200", log("phan-ky"))

	if code != 2 {
		t.Errorf("a run that diverged exited %d:\n%s", code, out)
	}
	for _, want := range []string{
		"1 spike over the band:",
		"never",
		"This is not the run it looks like:",
		"never came back inside the band",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

// The response to a spike is a rewind to a checkpoint, so a log with no cadence
// beside it is a detector and not a protocol.
func TestVotExitsOneWhenTheLogCannotBeReadAgainstTheProtocol(t *testing.T) {
	out, _, code := exec(t, "vot", "-run", "on-dinh", "-total", "100000", log("on-dinh"))

	if code != 1 {
		t.Errorf("a log with no checkpoint cadence exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "This log cannot be read against the protocol:") {
		t.Errorf("the report does not say why:\n%s", out)
	}
	if !strings.Contains(out, "there is a detector here and no protocol") {
		t.Errorf("the report does not name the missing cadence:\n%s", out)
	}
}

func TestVotPrintsJSON(t *testing.T) {
	out, _, _ := exec(t, "vot", "-run", "vot-len", "-total", "100000", "-checkpoint", "200", "-json", log("vot-len"))

	var got struct {
		Run     string  `json:"run"`
		Rows    int     `json:"rows"`
		Every   int     `json:"every"`
		Scatter float64 `json:"scatter"`
		Spikes  []struct {
			Step   int     `json:"step"`
			Rewind int     `json:"rewind"`
			Back   int     `json:"back"`
			Grad   float64 `json:"grad_norm"`
		} `json:"spikes"`
		Cost    float64  `json:"cost"`
		Faults  []string `json:"faults"`
		Holds   bool     `json:"holds"`
		Verdict string   `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out)
	}
	if got.Run != "vot-len" || got.Rows != 400 || got.Every != 10 {
		t.Errorf("the reading came back as %+v", got)
	}
	if len(got.Spikes) != 1 || got.Spikes[0].Step != 2530 || got.Spikes[0].Rewind != 130 {
		t.Errorf("the spike came back as %+v", got.Spikes)
	}
	if got.Spikes[0].Grad == 0 {
		t.Error("the gradient norm is not on the record, and it is what separates one kind of spike from another")
	}
	if !got.Holds || len(got.Faults) != 0 || got.Verdict == "" {
		t.Errorf("a run that spiked once and came back did not hold: %+v", got)
	}
}

// The table is printed at a length somebody reads, the reading behind it is not
// cut down to match, and the header says which of the two the reader is looking
// at.
func TestVotPrintsAsManySpikesAsItWasAsked(t *testing.T) {
	out, _, code := exec(t, "vot", "-run", "vot-nhieu", "-total", "100000", "-checkpoint", "200", "-top", "3", log("vot-nhieu"))

	if code != 2 {
		t.Errorf("a run with a hundred spikes in it exited %d", code)
	}
	if !strings.Contains(out, "spikes over the band, the first 3:") {
		t.Errorf("the table header does not say how much of it is printed:\n%s", out)
	}
	if n := strings.Count(out, "rows out"); n != 1 {
		t.Errorf("the spike table was printed %d times", n)
	}
	if !strings.Contains(out, "the curve is the finding") {
		t.Errorf("the report does not say the count is the finding:\n%s", out)
	}
}

func TestVotRefusesTheUsageErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"no log", []string{"vot", "-run", "on-dinh"}},
		{"two logs", []string{"vot", "-run", "on-dinh", log("on-dinh"), log("vot-len")}},
		{"a table of no rows at all", []string{"vot", "-run", "on-dinh", "-top", "-1", log("on-dinh")}},
		{"a flag nobody has", []string{"vot", "-nope"}},
		{"the help", []string{"vot", "-h"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := exec(t, c.args...)
			if code != 2 {
				t.Errorf("exited %d, want 2\n%s%s", code, out, errOut)
			}
			if errOut == "" {
				t.Error("nothing was said about it on stderr")
			}
		})
	}
}

func TestVotRefusesALogThatIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "khong-co.jsonl")

	out, errOut, code := exec(t, "vot", "-run", "on-dinh", "-total", "100000", "-checkpoint", "200", missing)

	if code != 1 {
		t.Errorf("exited %d, want 1\n%s", code, out)
	}
	if !strings.Contains(errOut, "khong-co.jsonl") {
		t.Errorf("stderr does not name the file: %s", errOut)
	}
}

func TestVotIsInTheCommandList(t *testing.T) {
	out, _, _ := exec(t, "help")

	if !strings.Contains(out, "vot") {
		t.Error("gao help does not list vot")
	}
}
