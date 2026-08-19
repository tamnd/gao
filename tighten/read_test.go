package tighten

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestARecipeSurvivesBeingWrittenDownAndReadBack(t *testing.T) {
	b, err := json.Marshal(Plan())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecipe(write(t, "recipe.json", string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if got != Plan() {
		t.Errorf("the recipe came back as %+v", got)
	}
}

func TestAKnobUnderAnotherNameIsAnErrorRatherThanADefault(t *testing.T) {
	_, err := ReadRecipe(write(t, "recipe.json", `{"group":16,"clip_eps_high":0.28}`))
	if err == nil {
		t.Fatal("a configuration naming a knob this package does not know was read as the plan's own")
	}
	if !strings.Contains(err.Error(), "clip_eps_high") {
		t.Errorf("the field that could not be read is not named: %v", err)
	}

	if _, err := ReadRecipe(filepath.Join(t.TempDir(), "nothing.json")); err == nil {
		t.Error("a missing configuration read as something")
	}
}

func TestALogIsReadIntoTheOrderTheStepsWereTaken(t *testing.T) {
	var b strings.Builder
	for _, s := range []Step{steps(3)[2], steps(3)[0], steps(3)[1]} {
		line, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteString("\n  \n")

	r, err := ReadRun("dau", write(t, "steps.jsonl", b.String()), Plan())
	if err != nil {
		t.Fatal(err)
	}
	if r.Specialist != "dau" || len(r.Steps) != 3 {
		t.Fatalf("%s came back with %d steps, and the blank lines are not steps", r.Specialist, len(r.Steps))
	}
	for i, s := range r.Steps {
		if s.Step != i+1 {
			t.Errorf("step %d is in position %d", s.Step, i+1)
		}
	}
	if len(r.Blocking()) > 0 {
		t.Errorf("a log written and read back was refused: %v", r.Blocking())
	}
}

func TestTheLineThatCouldNotBeReadIsTheOneNamed(t *testing.T) {
	path := write(t, "steps.jsonl", "{\"step\":1}\n{\"step\":2}\n{\"step\":3,\"kept\":\"most of them\"}\n")
	_, err := ReadRun("dau", path, Plan())
	if err == nil {
		t.Fatal("a log with a string where a count belongs was read")
	}
	if !strings.Contains(err.Error(), "steps.jsonl:3") {
		t.Errorf("the line is not named: %v", err)
	}

	if _, err := ReadRun("dau", filepath.Join(t.TempDir(), "nothing.jsonl"), Plan()); err == nil {
		t.Error("a missing log read as a run")
	}
}

func TestOneFaultIsWrittenAsOneThingAndAnUnnamedRunIsCalledTheRun(t *testing.T) {
	r := run(40)
	r.Specialist = ""
	for i := range r.Steps {
		r.Steps[i].ClipHigh = 0
	}
	if faults := r.Faults(); len(faults) != 1 {
		t.Fatalf("this run has %d faults, and the singular is what is being read here", len(faults))
	}
	v := r.Verdict()
	if !strings.HasPrefix(v, "the run ran 40 steps") {
		t.Errorf("a log with no specialist on it is named something else:\n  %s", v)
	}
	if !strings.Contains(v, "One thing to read before the reward is") {
		t.Errorf("one fault is written as a plural:\n  %s", v)
	}
}

func TestABatchNobodySampledForDoesNotFill(t *testing.T) {
	var none Run
	if none.Fills() {
		t.Error("a run with no steps in it fills a batch")
	}

	penalized := Plan()
	penalized.Penalized = true
	for _, row := range penalized.Rows() {
		if row.Element == "overlong" && row.Setting != "penalized" {
			t.Errorf("a penalized configuration prints its overlong setting as %q", row.Setting)
		}
	}
}
