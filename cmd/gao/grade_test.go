package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/grade"
	"github.com/tamnd/gao/normalize"
)

// writeLines writes one JSON object per line and returns the path.
func writeLines(t *testing.T, name string, values ...any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, v := range values {
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// gradeRolloutLine is the rollout file record, written here the way a sampler
// would write it rather than by reusing the reader's own struct.
type gradeRolloutLine struct {
	Prompt   string   `json:"prompt"`
	Must     []string `json:"must,omitempty"`
	Answers  []string `json:"answers"`
	Overlong []bool   `json:"overlong,omitempty"`
}

func TestGradeRosterSaysWhichVerifiersAreNotBuilt(t *testing.T) {
	out, _, code := exec(t, "grade", "roster")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, name := range []string{"dau", "trich", "kim", "theo", "toan", "ma", "tu-choi"} {
		if !strings.Contains(out, name) {
			t.Errorf("the roster does not mention %q", name)
		}
	}
	if !strings.Contains(out, "5 of 7 verifiers are specified and not built") {
		t.Errorf("the roster does not say what is missing:\n%s", out)
	}
}

func TestGradeRosterPrintsTheSameThingAsJSON(t *testing.T) {
	out, _, code := exec(t, "grade", "roster", "-json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got []grade.Specialist
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v in %q", err, out)
	}
	if len(got) != len(grade.Specialists()) {
		t.Fatalf("the JSON roster holds %d arms and the package holds %d", len(got), len(grade.Specialists()))
	}
	for _, s := range got {
		if s.Checked == "" {
			t.Errorf("%q is published without saying what its reward is computed from", s.Name)
		}
	}
}

func TestGradeMarkGradesRolloutsAgainstThePagesTheyCameFrom(t *testing.T) {
	page := markPages[0]
	prompt := normalize.Bare(page)

	// A perfect answer, a half restored one, the question handed back, and a
	// different page. The spread is what makes the group worth a backward pass.
	half := strings.Fields(page)
	for i := range half {
		if i%2 == 1 {
			half[i] = normalize.Bare(half[i])
		}
	}
	rollouts := writeLines(t, "rollouts.jsonl", gradeRolloutLine{
		Prompt:  prompt,
		Answers: []string{page, strings.Join(half, " "), prompt, markPages[1]},
	})

	out, errOut, code := exec(t, append([]string{"grade", "mark", "-rollouts", rollouts, "-v"}, markCorpus(t, markPages)...)...)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "the key holds 3 pages") {
		t.Errorf("the key was not built from all three pages:\n%s", out)
	}
	if !strings.Contains(out, "1 groups, 1 kept") {
		t.Errorf("the group was not kept:\n%s", out)
	}
	if !strings.Contains(out, "marks came back") {
		t.Errorf("the sample log does not say what was counted:\n%s", out)
	}
}

func TestGradeMarkScoresThePerfectAnswerAboveTheQuestionHandedBack(t *testing.T) {
	page := markPages[2]
	prompt := normalize.Bare(page)
	rollouts := writeLines(t, "rollouts.jsonl", gradeRolloutLine{
		Prompt:  prompt,
		Answers: []string{page, prompt, prompt, prompt},
	})

	out, errOut, code := exec(t, append([]string{"grade", "mark", "-rollouts", rollouts, "-json"}, markCorpus(t, markPages)...)...)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	var report struct {
		Specialist string `json:"specialist"`
		Groups     []struct {
			Rollouts []grade.Rollout `json:"rollouts"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v in %q", err, out)
	}
	if report.Specialist != "dau" {
		t.Fatalf("the report is attributed to %q", report.Specialist)
	}
	got := report.Groups[0].Rollouts
	if got[0].Verdict.Reward != 1 {
		t.Errorf("the page itself scored %v", got[0].Verdict.Reward)
	}
	if got[1].Verdict.Reward != 0 {
		t.Errorf("the question handed back scored %v", got[1].Verdict.Reward)
	}
	if got[0].Advantage <= 0 || got[1].Advantage >= 0 {
		t.Errorf("the advantages are %+.2f and %+.2f", got[0].Advantage, got[1].Advantage)
	}
}

func TestGradeMarkDropsWhatTheSamplerCutOff(t *testing.T) {
	page := markPages[0]
	prompt := normalize.Bare(page)
	rollouts := writeLines(t, "rollouts.jsonl", gradeRolloutLine{
		Prompt:   prompt,
		Answers:  []string{page, prompt, page, prompt},
		Overlong: []bool{false, false, true, true},
	})

	out, errOut, code := exec(t, append([]string{"grade", "mark", "-rollouts", rollouts}, markCorpus(t, markPages)...)...)
	if code == 0 {
		t.Fatalf("a batch with nothing in it to learn from exited 0\n%s", out)
	}
	if !strings.Contains(out, "50% of them unchecked") {
		t.Errorf("the batch does not report what it could not grade:\n%s", out)
	}
	if !strings.Contains(out, "bought nothing") {
		t.Errorf("the batch does not say that the step was wasted:\n%s\n%s", out, errOut)
	}
}

func TestGradeMarkRefusesACorpusTypedWithoutMarks(t *testing.T) {
	bare := make([]string, len(markPages))
	for i, p := range markPages {
		bare[i] = normalize.Bare(p)
	}
	rollouts := writeLines(t, "rollouts.jsonl", gradeRolloutLine{
		Prompt:  bare[0],
		Answers: []string{markPages[0]},
	})

	_, errOut, code := exec(t, append([]string{"grade", "mark", "-rollouts", rollouts}, markCorpus(t, bare)...)...)
	if code == 0 {
		t.Fatal("a key built out of pages with no marks on them was accepted")
	}
	if !strings.Contains(errOut, "typed without marks") {
		t.Errorf("the error does not say why: %q", errOut)
	}
}

func TestGradeMarkSaysWhichFileItCouldNotRead(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "khong-co.jsonl")
	_, errOut, code := exec(t, append([]string{"grade", "mark", "-rollouts", missing}, markCorpus(t, markPages)...)...)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "khong-co.jsonl") {
		t.Errorf("the error does not name the file: %q", errOut)
	}
}

// gradeRegisterLine is a register file record.
type gradeRegisterLine struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Articles int    `json:"articles"`
}

func gradeRegister(t *testing.T) string {
	t.Helper()
	return writeLines(t, "register.jsonl",
		gradeRegisterLine{Kind: "nghị định", ID: "13/2023/NĐ-CP", Articles: 44},
		gradeRegisterLine{Kind: "luật", ID: "24/2018/QH14", Articles: 43},
		gradeRegisterLine{Kind: "quyết định", ID: "749/QĐ-TTg"},
	)
}

const gradeQuestion = "Doanh nghiệp phải làm gì khi dữ liệu cá nhân của khách hàng bị lộ?"

func TestGradeQuoteGradesCitationsAgainstTheRegister(t *testing.T) {
	rollouts := writeLines(t, "rollouts.jsonl", gradeRolloutLine{
		Prompt: gradeQuestion,
		Must:   []string{"nghị định số 13/2023/NĐ-CP"},
		Answers: []string{
			"Phải thông báo trong 72 giờ theo Nghị định số 13/2023/NĐ-CP.",
			"Phải thông báo trong 72 giờ theo Nghị định số 99/2024/NĐ-CP.",
			"Phải thông báo trong 72 giờ.",
			"Xem Nghị định số 13/2023/NĐ-CP, Luật số 24/2018/QH14 và Quyết định số 749/QĐ-TTg.",
		},
	})

	out, errOut, code := exec(t, "grade", "quote", "-register", gradeRegister(t), "-json", rollouts)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	var report struct {
		Groups []struct {
			Rollouts []grade.Rollout `json:"rollouts"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v in %q", err, out)
	}
	got := report.Groups[0].Rollouts
	if got[0].Verdict.Reward != 1 {
		t.Errorf("the right citation scored %v: %s", got[0].Verdict.Reward, got[0].Verdict.Why)
	}
	for i, name := range []string{"an invented citation", "no citation at all"} {
		if r := got[i+1].Verdict.Reward; r != 0 {
			t.Errorf("%s scored %v: %s", name, r, got[i+1].Verdict.Why)
		}
	}
	// One of three citations is the one required, so precision is a third and
	// recall is one.
	if want := 2 * (1.0 / 3) / (1.0/3 + 1); math.Abs(got[3].Verdict.Reward-want) > 1e-9 {
		t.Errorf("citing the register at the question scored %v, want %v", got[3].Verdict.Reward, want)
	}
}

func TestGradeQuoteRefusesAQuestionNoAnswerCouldWin(t *testing.T) {
	rollouts := writeLines(t, "rollouts.jsonl", gradeRolloutLine{
		Prompt:  gradeQuestion,
		Must:    []string{"nghị định số 99/2024/NĐ-CP"},
		Answers: []string{"Theo Nghị định số 99/2024/NĐ-CP."},
	})

	_, errOut, code := exec(t, "grade", "quote", "-register", gradeRegister(t), rollouts)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "no answer to it could win") {
		t.Errorf("the error does not say why: %q", errOut)
	}
}

func TestGradeQuoteSaysWhichRegisterLineItCannotUse(t *testing.T) {
	bad := writeLines(t, "register.jsonl",
		gradeRegisterLine{Kind: "nghị định", ID: "13/2023/NĐ-CP", Articles: 44},
		gradeRegisterLine{Kind: "nghị định", ID: "749/QĐ-TTg"},
	)
	rollouts := writeLines(t, "rollouts.jsonl", gradeRolloutLine{
		Prompt:  gradeQuestion,
		Must:    []string{"nghị định số 13/2023/NĐ-CP"},
		Answers: []string{"Theo Nghị định số 13/2023/NĐ-CP."},
	})

	_, errOut, code := exec(t, "grade", "quote", "-register", bad, rollouts)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "register.jsonl:2") {
		t.Errorf("the error does not say which line: %q", errOut)
	}
}

func TestGradeQuotePrintsWhatTheRegisterAndTheKeyHold(t *testing.T) {
	rollouts := writeLines(t, "rollouts.jsonl", gradeRolloutLine{
		Prompt: gradeQuestion,
		Must:   []string{"nghị định số 13/2023/NĐ-CP"},
		Answers: []string{
			"Theo Nghị định số 13/2023/NĐ-CP.",
			"Theo Nghị định số 99/2024/NĐ-CP.",
			"Không có căn cứ nào.",
			"Theo Điều 3 Nghị định số 13/2023/NĐ-CP.",
		},
	})

	out, errOut, code := exec(t, "grade", "quote", "-register", gradeRegister(t), rollouts)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "the register holds 3 instruments and the key 1 prompts") {
		t.Errorf("the header does not say what was loaded:\n%s", out)
	}
	if !strings.Contains(out, "1 groups, 1 kept") {
		t.Errorf("the group was not kept:\n%s", out)
	}
}

func TestGradeIsInTheHelp(t *testing.T) {
	out, _, code := exec(t, "help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "cham") {
		t.Errorf("cham is not in the command list:\n%s", out)
	}

	out, _, code = exec(t, "grade", "help")
	if code != 0 {
		t.Fatalf("gao grade help: exit %d", code)
	}
	for _, sub := range []string{"roster", "mark", "quote"} {
		if !strings.Contains(out, sub) {
			t.Errorf("gao grade help does not mention %q", sub)
		}
	}

	// The two subcommands that were renamed still answer to the arm names the
	// roster publishes, which is what anybody who read the roster first will
	// type. Both are run with a flag that does not exist, because what is
	// being checked is which subcommand the argument reached rather than what
	// it did once it got there.
	for old, want := range map[string]string{"dau": "grade mark", "trich": "grade quote"} {
		_, errOut, code := exec(t, "grade", old, "-nosuchflag")
		if code != 2 {
			t.Errorf("gao grade %s exited %d, want 2", old, code)
		}
		if !strings.Contains(errOut, want) {
			t.Errorf("gao grade %s did not reach %q:\n%s", old, want, errOut)
		}
	}

	_, errOut, code := exec(t, "grade", "khong-co")
	if code != 2 {
		t.Errorf("an unknown subcommand exited %d, want 2", code)
	}
	if !strings.Contains(errOut, "khong-co") {
		t.Errorf("the error does not name the subcommand: %q", errOut)
	}
}
