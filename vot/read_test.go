package vot_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/vot"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestARealLogIsReadRowForRow(t *testing.T) {
	steps, err := vot.ReadSteps("testdata/on-dinh.jsonl")
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}

	if len(steps) != 400 {
		t.Fatalf("the log holds 400 rows and %d came back", len(steps))
	}
	first := steps[0]
	if first.Step != 0 || first.Loss <= 0 || first.LR <= 0 || first.Grad <= 0 {
		t.Errorf("the first row came back as %+v", first)
	}
	if steps[len(steps)-1].Step != 3990 {
		t.Errorf("the last row is step %d", steps[len(steps)-1].Step)
	}
}

// A trainer logs throughput and memory and whatever the framework felt like
// emitting that release. Refusing the whole run over a counter nobody asked for
// is how a reader gets pointed at a real log exactly once.
func TestAFieldNobodyAskedForIsReadPastRatherThanRefused(t *testing.T) {
	path := write(t, "them-cot.jsonl", `{"step":0,"loss":4.2,"lr":0.0001,"grad_norm":0.9,"tokens_per_sec":184320,"mem":"41.2GB"}
{"step":1,"loss":4.1,"lr":0.0002,"grad_norm":0.8}
`)

	steps, err := vot.ReadSteps(path)
	if err != nil {
		t.Fatalf("a row with two extra columns was refused: %v", err)
	}
	if len(steps) != 2 || steps[0].Loss != 4.2 {
		t.Errorf("the rows came back as %+v", steps)
	}
}

// What is not an extra field is the reading missing.
func TestARowWithNothingToReadIsRefusedAndNamed(t *testing.T) {
	for _, c := range []struct {
		name, body, want string
	}{
		{"no loss", `{"step":40,"lr":0.0003}` + "\n", "step 40 carries no loss"},
		{"no step", `{"loss":4.2}` + "\n", "carries no step"},
		{"not JSON at all", "step 40 loss 4.2\n", ":1:"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := vot.ReadSteps(write(t, "hong.jsonl", c.body))
			if err == nil {
				t.Fatal("the row was read")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the error does not say %q: %v", c.want, err)
			}
		})
	}
}

func TestBlankLinesAreSkippedRatherThanCounted(t *testing.T) {
	path := write(t, "trong.jsonl", "\n{\"step\":0,\"loss\":4.2}\n\n{\"step\":10,\"loss\":4.1}\n\n")

	steps, err := vot.ReadSteps(path)
	if err != nil {
		t.Fatalf("reading a log with blank lines in it: %v", err)
	}
	if len(steps) != 2 {
		t.Errorf("%d rows came back off two rows and three blank lines", len(steps))
	}
}

func TestALogThatIsNotThereSaysSo(t *testing.T) {
	_, err := vot.ReadSteps(filepath.Join(t.TempDir(), "khong-co.jsonl"))

	if err == nil {
		t.Fatal("a log that does not exist was read")
	}
	if !strings.Contains(err.Error(), "khong-co.jsonl") {
		t.Errorf("the error does not name the file: %v", err)
	}
}
