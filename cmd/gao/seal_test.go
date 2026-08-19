package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/seal"
)

// writeResults puts a result file where the command can read it, one result per
// line, which is the shape a run appends to.
func writeResults(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// digest is what the fixed harness hashes to, which every result has to carry.
func digest(t *testing.T) string {
	t.Helper()
	h, err := seal.Fixed()
	if err != nil {
		t.Fatal(err)
	}
	return h.Digest().String()
}

// honest is a complete result for one arm, with a number on every task the
// harness holds.
func honest(t *testing.T, arm string, score float64) string {
	t.Helper()
	h, err := seal.Fixed()
	if err != nil {
		t.Fatal(err)
	}
	scores := make(map[string]float64, len(h.Tasks))
	for _, task := range h.Tasks {
		scores[task.Benchmark] = score
	}
	b, err := json.Marshal(seal.Result{Harness: h.Digest(), Arm: arm, Scores: scores})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheHarnessPrintsWhatItMeasures(t *testing.T) {
	out, errOut, code := exec(t, "seal", "harness")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"vmlu", "com-8B-cpt-gao", "likelihood", "der", "20260806"} {
		if !strings.Contains(out, want) {
			t.Errorf("the harness does not show %s:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "promises") {
		t.Errorf("the harness does not say how many numbers it promises:\n%s", out)
	}
}

func TestTheHarnessNamesWhatIsNotYetReproducible(t *testing.T) {
	// Unpinned benchmarks are the work list standing between this harness and a
	// comparison somebody else can run, so they are printed rather than counted.
	out, _, code := exec(t, "seal", "harness")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "revision") {
		t.Errorf("the table does not carry the benchmark revisions:\n%s", out)
	}
}

func TestThePromptsComeOutVerbatimWhenAsked(t *testing.T) {
	// The prompt is part of the measurement, so it has to be readable without
	// opening the file.
	plain, _, _ := exec(t, "seal", "harness")
	full, _, code := exec(t, "seal", "harness", "-prompts")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(full) <= len(plain) {
		t.Error("asking for the prompts printed no more than not asking")
	}
	if !strings.Contains(full, "Đáp án:") {
		t.Errorf("the prompts are not in the output:\n%s", full)
	}
}

func TestTheDigestIsOneLineSomethingCanBePastedFrom(t *testing.T) {
	out, _, code := exec(t, "seal", "digest")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	got := strings.TrimSpace(out)
	if got != digest(t) {
		t.Errorf("the digest printed is %q", got)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("the digest came out over several lines:\n%q", out)
	}
}

func TestAnHonestRunAudits(t *testing.T) {
	path := writeResults(t,
		honest(t, "com-8B-cpt-gao", 0.6),
		honest(t, "com-8B-cpt-culturax", 0.5),
		honest(t, "com-8B-cpt-culturax-filtered", 0.55),
	)
	out, errOut, code := exec(t, "seal", "audit", path)
	if code != 0 {
		t.Fatalf("a complete run failed the audit: exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "comparable") {
		t.Errorf("the audit does not say what passing means:\n%s", out)
	}
	if !strings.Contains(out, "won by") {
		t.Errorf("the table has no winner column:\n%s", out)
	}
}

func TestADroppedBenchmarkFailsTheAudit(t *testing.T) {
	one := honest(t, "com-8B-cpt-culturax", 0.5)
	var r seal.Result
	if err := json.Unmarshal([]byte(one), &r); err != nil {
		t.Fatal(err)
	}
	delete(r.Scores, "vmlu")
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	path := writeResults(t,
		honest(t, "com-8B-cpt-gao", 0.6),
		string(b),
		honest(t, "com-8B-cpt-culturax-filtered", 0.55),
	)
	out, _, code := exec(t, "seal", "audit", path)
	if code != 1 {
		t.Fatalf("an arm that quietly dropped the primary benchmark exited %d", code)
	}
	if !strings.Contains(out, "vmlu") {
		t.Errorf("the audit does not name the benchmark that went missing:\n%s", out)
	}
}

func TestResultsFromAnotherHarnessFailTheAudit(t *testing.T) {
	var r seal.Result
	if err := json.Unmarshal([]byte(honest(t, "com-8B-cpt-gao", 0.9)), &r); err != nil {
		t.Fatal(err)
	}
	r.Harness = seal.Harness{Version: "made up"}.Digest()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	path := writeResults(t,
		string(b),
		honest(t, "com-8B-cpt-culturax", 0.5),
		honest(t, "com-8B-cpt-culturax-filtered", 0.55),
	)
	out, _, code := exec(t, "seal", "audit", path)
	if code != 1 {
		t.Fatalf("numbers from a different harness exited %d", code)
	}
	if !strings.Contains(out, "not comparable") {
		t.Errorf("the audit does not say why the numbers cannot be put in one table:\n%s", out)
	}
}

func TestTheAuditLeavesAMissingNumberMissing(t *testing.T) {
	// Printed as zero it would read as an arm that scored nothing, which on
	// accuracy is the worst result there is rather than no result.
	path := writeResults(t, honest(t, "com-8B-cpt-gao", 0.6))
	out, _, code := exec(t, "seal", "audit", path)
	if code != 1 {
		t.Fatalf("a comparison missing two of its three arms exited %d", code)
	}
	if !strings.Contains(out, "\t.") && !strings.Contains(out, " . ") {
		t.Errorf("a column nobody reported was not left blank:\n%s", out)
	}
	if strings.Contains(out, "0.000") {
		t.Errorf("a missing number was printed as zero:\n%s", out)
	}
}

func TestTheAuditSpeaksJSON(t *testing.T) {
	path := writeResults(t, honest(t, "com-8B-cpt-gao", 0.6))
	out, _, code := exec(t, "seal", "audit", "-json", path)
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	var a seal.Audit
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("the audit is not JSON: %v\n%s", err, out)
	}
	if a.OK() {
		t.Error("an incomplete run came back clean")
	}
	if a.Harness.String() != digest(t) {
		t.Error("the audit does not carry the harness digest")
	}
}

func TestTheHarnessSpeaksJSON(t *testing.T) {
	out, _, code := exec(t, "seal", "harness", "-json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var report struct {
		Harness  seal.Harness `json:"harness"`
		Digest   string       `json:"digest"`
		Unpinned []string     `json:"unpinned"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the harness is not JSON: %v", err)
	}
	if report.Digest != digest(t) {
		t.Error("the JSON does not carry the digest the results have to carry")
	}
	if len(report.Harness.Tasks) == 0 {
		t.Error("the JSON holds no tasks")
	}
}

func TestATypoInAResultFileIsAnErrorRatherThanADefault(t *testing.T) {
	path := writeResults(t, `{"harness":"`+digest(t)+`","arm":"com-8B-cpt-gao","score":{"vmlu":0.6}}`)
	_, errOut, code := exec(t, "seal", "audit", path)
	if code != 1 {
		t.Fatalf("a misspelled key exited %d", code)
	}
	if !strings.Contains(errOut, "score") {
		t.Errorf("the error does not name the key nobody meant to write: %s", errOut)
	}
}

func TestAHarnessFromAFileCanBeAudited(t *testing.T) {
	// The harness in the repository is the one that counts, but an older one has
	// to be readable, since checking whether last month's numbers were produced
	// under last month's harness is the whole point.
	path := filepath.Join(t.TempDir(), "harness.json")
	body, err := os.ReadFile("../../seal/harness.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := exec(t, "seal", "digest", "-harness", path)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.TrimSpace(out) != digest(t) {
		t.Error("the same harness read from a file hashes differently")
	}
}

func TestSayingNothingAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "seal")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "audit") {
		t.Errorf("the usage does not list the subcommands: %s", errOut)
	}
}

func TestASubcommandNobodyWroteIsNamed(t *testing.T) {
	_, errOut, code := exec(t, "seal", "kiem")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "kiem") {
		t.Errorf("the error does not say what was typed: %s", errOut)
	}
}

func TestAResultFileThatIsNotThereSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "seal", "audit", filepath.Join(t.TempDir(), "khong-co.json"))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "khong-co.json") {
		t.Errorf("the error does not name the file: %s", errOut)
	}
}
