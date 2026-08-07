package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doan"
)

// claim returns the claim the register holds for an identifier, since a result
// carrying anything else is refused and that is the point of the file.
func claim(t *testing.T, id string) string {
	t.Helper()
	for _, p := range doan.Published().Predictions {
		if p.ID == id {
			return p.Claim
		}
	}
	t.Fatalf("%s is not on the register", id)
	return ""
}

func doanResults(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "results.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func doanResult(t *testing.T, id, state, reading, box string) string {
	t.Helper()
	return fmt.Sprintf(`{"id":%q,"claim":%q,"state":%q,"reading":%q,"by":"gao dem counts","box":%q}`,
		id, claim(t, id), state, reading, box)
}

func TestDoanPrintsTheRegisterBeforeAnythingHasAResult(t *testing.T) {
	out, errOut, code := exec(t, "doan")
	if code != 0 {
		t.Fatalf("the published register exited %d: %s\n%s", code, errOut, out)
	}
	if !strings.Contains(out, "None of them has a result") {
		t.Errorf("the verdict does not say the register is unmeasured:\n%s", out)
	}
	if !strings.Contains(out, "58 predictions") {
		t.Errorf("the register does not print its size:\n%s", out)
	}
	// S0 has no predictions and that is a fact about S0 rather than a hole.
	if !strings.Contains(out, "S0     Foundations and law             .") {
		t.Errorf("S0 does not print as carrying nothing:\n%s", out)
	}
}

func TestDoanPrintsOneSliceWhenAskedForOne(t *testing.T) {
	out, _, code := exec(t, "doan", "-slice", "S7")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "P08-2") || !strings.Contains(out, "P10-2") {
		t.Errorf("the S7 predictions are not printed:\n%s", out)
	}
	if strings.Contains(out, "P03-1  ") {
		t.Errorf("a prediction from another slice printed:\n%s", out)
	}
}

func TestDoanPrintsTheMissesWhateverElseWasAskedFor(t *testing.T) {
	path := doanResults(t,
		doanResult(t, "P07-5", "truot", "3.28 characters per token", "server3"),
		doanResult(t, "P05-5", "dung", "6.2% of documents changed", "server2"),
	)
	out, _, code := exec(t, "doan", "-results", path)
	if code != 0 {
		t.Fatalf("two results exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "1 prediction came back wrong") {
		t.Errorf("the miss is not printed in full:\n%s", out)
	}
	if !strings.Contains(out, "3.28 characters per token, measured by gao dem counts on server3") {
		t.Errorf("the miss does not carry its reading and its box:\n%s", out)
	}
}

func TestDoanRefusesAResultMeasuredAgainstAnEditedClaim(t *testing.T) {
	path := doanResults(t,
		`{"id":"P03-1","claim":"the HPLT v3 count is somewhere near the estimate","state":"dung","reading":"181.4B tokens","by":"gao dem counts","box":"server1"}`,
	)
	out, _, code := exec(t, "doan", "-results", path)
	if code != 1 {
		t.Fatalf("a result against an edited claim exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "the claim was edited after the number landed") {
		t.Errorf("the refusal does not say what the mismatch means:\n%s", out)
	}
}

func TestDoanRefusesAMeasurementFromABoxThatIsNotInTheFleet(t *testing.T) {
	path := doanResults(t, doanResult(t, "P03-1", "dung", "181.4B tokens", "laptop"))
	out, _, code := exec(t, "doan", "-results", path)
	if code != 1 {
		t.Fatalf("a result off an unknown box exited %d\n%s", code, out)
	}
	if !strings.Contains(out, `measured on "laptop", which is not a box in the fleet`) {
		t.Errorf("the refusal does not name the box:\n%s", out)
	}

	ok := doanResults(t, doanResult(t, "P03-1", "dung", "181.4B tokens", "gamingpc"))
	if _, _, code := exec(t, "doan", "-results", ok); code != 0 {
		t.Errorf("a result off gamingpc exited %d", code)
	}
}

func TestDoanExitsTwoWhenTheRegisterComesBackTooRightToBeWorthMaking(t *testing.T) {
	lines := make([]string, 0, 40)
	for _, p := range doan.Published().Predictions[:40] {
		lines = append(lines, doanResult(t, p.ID, "dung", "inside the claim", "server1"))
	}
	out, _, code := exec(t, "doan", "-results", doanResults(t, lines...))
	if code != 2 {
		t.Fatalf("a register that got everything right exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "written to be met rather than to be tested") {
		t.Errorf("the verdict does not say what a perfect rate means:\n%s", out)
	}
}

func TestDoanJSONCarriesTheDigestAndTheBand(t *testing.T) {
	out, _, code := exec(t, "doan", "-json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{`"digest"`, `"slices"`, `"rate"`, `"settled"`, `"min_rate"`, `"max_withdrawn"`, `"holds"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the JSON does not carry %s:\n%s", want, out)
		}
	}
}

func TestDoanWithAnArgumentIsAUsageError(t *testing.T) {
	if _, _, code := exec(t, "doan", "S1"); code != 2 {
		t.Error("gao doan with a positional argument did not exit 2")
	}
	if _, _, code := exec(t, "doan", "-results", filepath.Join(t.TempDir(), "nothing.jsonl")); code != 1 {
		t.Error("a results file that is not there did not exit 1")
	}
}
