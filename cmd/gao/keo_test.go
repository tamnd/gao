package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resumeLine writes one resume the way a restart drill records it. Everything
// except where the bytes came from is held fixed, because where they came from
// is the item.
func resumeLine(step int, from, source string, ranks int, provision, pull float64, lossAfter float64) string {
	return fmt.Sprintf(
		`{"step":%d,"from":%q,"source":%q,"instance":"8xH100","bytes":112000000000,`+
			`"digest":"b3:9f2c41a7d0","verified":"b3:9f2c41a7d0","wrote_ranks":64,"read_ranks":%d,`+
			`"provision":%g,"pull":%g,"load":640,"lost":3120,"interval":7200,"loss_at":1.8421,"loss_after":%g}`,
		step, from, source, ranks, provision, pull, lossAfter)
}

// keoDrill writes a drill that came back intact unless a test says otherwise.
func keoDrill(t *testing.T, lines ...string) string {
	t.Helper()
	if len(lines) == 0 {
		lines = []string{
			resumeLine(24000, "fleet", "server3", 32, 1080, 8960, 1.8437),
			resumeLine(41000, "store", "open-index/com-8B-cpt-gao", 64, 600, 448, 1.8430),
		}
	}
	path := filepath.Join(t.TempDir(), "resumes.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestARestartDrillNamesTheCopyItCameBackFrom(t *testing.T) {
	out, errOut, code := exec(t, "keo", keoDrill(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"fleet", "store", "server3", "32 of 64", "104.3 GB",
		"came back from the fleet copy at step 24000 intact",
		"the copy that survives rather than the copy a live restart reads",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
	// The expensive restart is first, since that is the one a plan is written
	// around.
	rows := strings.Split(out, "\n")
	if !strings.HasPrefix(rows[1], "24000") {
		t.Errorf("the table does not lead with the most expensive restart:\n%s", out)
	}
}

// The item's own words, and the case the command exists to refuse.
func TestAResumeOffTheTrainingHostExitsOne(t *testing.T) {
	out, _, code := exec(t, "keo", keoDrill(t,
		resumeLine(24000, "host", "local", 64, 0, 210, 1.8437),
	))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "the path this item is about has not been run once") {
		t.Errorf("the report accepts a resume off the training host:\n%s", out)
	}
}

// The bytes arriving is not the state arriving, and this is what that looks
// like from the outside.
func TestAResumeThatVerifiedAndCameBackHigherExitsOne(t *testing.T) {
	out, _, code := exec(t, "keo", keoDrill(t,
		resumeLine(24000, "fleet", "server3", 32, 1080, 8960, 1.9032),
		resumeLine(41000, "store", "open-index/com-8B-cpt-gao", 64, 600, 448, 1.8430),
	))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"the digest matched", "drops the optimizer moments"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
}

// A fleet copy that came off a machine we do not have is not the copy that
// survives a reclaim, and the inventory is what says so.
func TestAFleetCopyIsCheckedAgainstTheFleet(t *testing.T) {
	out, _, code := exec(t, "keo", keoDrill(t,
		resumeLine(24000, "fleet", "server9", 32, 1080, 8960, 1.8437),
		resumeLine(41000, "store", "open-index/com-8B-cpt-gao", 64, 600, 448, 1.8430),
	))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "which is not a box on this fleet") {
		t.Errorf("the report accepts a machine nobody has:\n%s", out)
	}
}

// Correct and unaffordable is a real answer rather than a broken drill, and it
// is the answer when the fleet copy is the only way back in.
func TestARunWithNoAffordableWayBackInExitsTwo(t *testing.T) {
	out, _, code := exec(t, "keo", keoDrill(t,
		resumeLine(24000, "fleet", "server3", 32, 1080, 8960, 1.8437),
		resumeLine(41000, "fleet", "gamingpc", 64, 900, 7400, 1.8430),
	))
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "restarts more than it trains") {
		t.Errorf("the report does not say what an unaffordable restart is:\n%s", out)
	}
}

func TestTheDrillIsAlsoMachineReadable(t *testing.T) {
	out, errOut, code := exec(t, "keo", "-json", keoDrill(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got struct {
		Run          string  `json:"run"`
		Params       int64   `json:"params"`
		State        int64   `json:"state"`
		Resumes      int     `json:"resumes"`
		Fleet        int     `json:"fleet"`
		Resharded    int     `json:"resharded"`
		Cheapest     string  `json:"cheapest"`
		CheapestFrom string  `json:"cheapest_from"`
		Overhead     float64 `json:"overhead"`
		Unaffordable int     `json:"unaffordable"`
		Holds        bool    `json:"holds"`
		Readings     []struct {
			Step     int     `json:"step"`
			From     string  `json:"from"`
			Rate     float64 `json:"rate"`
			Restart  float64 `json:"restart"`
			Cost     float64 `json:"cost"`
			Overhead float64 `json:"overhead"`
			Drift    float64 `json:"drift"`
			Intact   bool    `json:"intact"`
			Reshards bool    `json:"reshards"`
			Fits     bool    `json:"fits"`
		} `json:"readings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Run != "com-8B-cpt-gao" || got.State != 112_000_000_000 || !got.Holds {
		t.Errorf("the drill came back as %+v", got)
	}
	if got.Resumes != 2 || got.Fleet != 1 || got.Resharded != 1 {
		t.Errorf("resumes %d, fleet %d, resharded %d", got.Resumes, got.Fleet, got.Resharded)
	}
	if got.CheapestFrom != "store" || got.Overhead < 0.23 || got.Overhead > 0.24 {
		t.Errorf("the cheapest way back in is the %s at %.2f", got.CheapestFrom, got.Overhead)
	}
	// The fleet copy is intact and unaffordable at once, which is the finding
	// rather than a fault.
	if got.Unaffordable != 1 {
		t.Errorf("%d resumes came back unaffordable", got.Unaffordable)
	}
	first := got.Readings[0]
	if first.From != "fleet" || !first.Intact || !first.Reshards || first.Fits {
		t.Errorf("the fleet row came back as %+v", first)
	}
	if first.Overhead < 1.4 || first.Overhead > 1.5 {
		t.Errorf("a fleet restart came back as %.0f%% of the interval", 100*first.Overhead)
	}
	if first.Cost <= first.Restart {
		t.Errorf("cost %.0f does not carry the recomputed training on top of a %.0f restart", first.Cost, first.Restart)
	}
	if first.Rate < 12_000_000 || first.Rate > 13_000_000 {
		t.Errorf("the fleet pull came back at %.0f bytes a second", first.Rate)
	}
}

func TestKeoRefusesWhatItCannotRead(t *testing.T) {
	if _, _, code := exec(t, "keo"); code != 2 {
		t.Errorf("no argument exited %d", code)
	}
	if _, _, code := exec(t, "keo", "a.jsonl", "b.jsonl"); code != 2 {
		t.Errorf("two arguments exited %d", code)
	}
	if _, _, code := exec(t, "keo", filepath.Join(t.TempDir(), "missing.jsonl")); code != 1 {
		t.Errorf("a file that is not there exited %d", code)
	}
}
