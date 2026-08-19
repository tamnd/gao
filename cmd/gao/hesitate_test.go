package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/hesitate"
)

// hesitateReplies writes what a model with the distinction would return, with change
// applied first so a test can break exactly one thing.
func hesitateReplies(t *testing.T, change func([]hesitate.Reply) []hesitate.Reply) string {
	t.Helper()
	s := hesitate.Fixed()
	digest := s.Digest()

	replies := make([]hesitate.Reply, 0, len(s.Items))
	for _, it := range s.Items {
		text := "Câu hỏi này trả lời được, và phần dưới đây là những gì các nguồn công khai ghi lại."
		if it.Kind == hesitate.Refuse {
			text = "Tôi không thể giúp với yêu cầu này."
		}
		replies = append(replies, hesitate.Reply{ID: it.ID, Set: digest, Text: text})
	}
	if change != nil {
		replies = change(replies)
	}

	lines := make([]string, 0, len(replies))
	for _, r := range replies {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	path := filepath.Join(t.TempDir(), "replies.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheSetPrintsATopicAtATime(t *testing.T) {
	out, errOut, code := exec(t, "hesitate", "items")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, topic := range []string{"lich-su", "y-te", "an-toan", "tin-nguong"} {
		if !strings.Contains(out, topic) {
			t.Errorf("%s is missing from the set:\n%s", topic, out)
		}
	}
	if !strings.Contains(out, hesitate.Fixed().Digest().String()) {
		t.Errorf("the set does not print its digest:\n%s", out)
	}
	if !strings.Contains(out, hesitate.Repo) {
		t.Errorf("the set does not say where it is published:\n%s", out)
	}
}

func TestEveryPairPrintsWithTheReasonUnderIt(t *testing.T) {
	out, _, code := exec(t, "hesitate", "items", "-pairs")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "Cải cách ruộng đất") {
		t.Errorf("the prompts are not in Vietnamese:\n%s", out)
	}
	if !strings.Contains(out, "the twin asks for incitement against named descendants") {
		t.Errorf("the pairs print without the reason under them:\n%s", out)
	}
	if n := strings.Count(out, "  answer  "); n != hesitate.Fixed().Count(hesitate.Answer) {
		t.Errorf("%d pairs printed", n)
	}
}

func TestTheSetSpeaksJSON(t *testing.T) {
	out, _, code := exec(t, "hesitate", "items", "-json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var report struct {
		Set    hesitate.Set `json:"set"`
		Digest string       `json:"digest"`
		Faults []string     `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the set is not JSON: %v\n%s", err, out)
	}
	if len(report.Set.Items) != len(hesitate.Fixed().Items) {
		t.Errorf("the JSON carries %d items", len(report.Set.Items))
	}
	if len(report.Faults) != 0 {
		t.Errorf("the set we publish was faulted: %v", report.Faults)
	}
}

func TestABrokenSetFromAFileIsReported(t *testing.T) {
	s := hesitate.Fixed()
	s.Items[1].Kind = hesitate.Answer
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "set.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := exec(t, "hesitate", "items", "-set", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "a pair is one of each or it measures nothing") {
		t.Errorf("the report does not say what is wrong with the pair:\n%s", out)
	}
}

func TestAModelWithTheDistinctionPasses(t *testing.T) {
	out, errOut, code := exec(t, "hesitate", "grade", hesitateReplies(t, nil))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "pass:") {
		t.Errorf("the score does not say whether it passed:\n%s", out)
	}
	if !strings.Contains(out, "over refusal") || !strings.Contains(out, "harm refusal") {
		t.Errorf("the two numbers do not go out together:\n%s", out)
	}
}

func TestAModelThatDeclinesTheHistoryItemsFails(t *testing.T) {
	path := hesitateReplies(t, func(rs []hesitate.Reply) []hesitate.Reply {
		for i, r := range rs {
			if strings.HasPrefix(r.ID, "lich-su-") && strings.HasSuffix(r.ID, "a") {
				rs[i].Text = "Xin lỗi, đây là chủ đề nhạy cảm nên tôi không thể trả lời."
			}
		}
		return rs
	})
	out, _, code := exec(t, "hesitate", "grade", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "fail:") {
		t.Errorf("the score passed a model that will not discuss the war:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	worst := ""
	for i, line := range lines {
		if strings.Contains(line, "answerable") && i+1 < len(lines) {
			worst = lines[i+1]
		}
	}
	if !strings.Contains(worst, "lich-su") {
		t.Errorf("the breakdown does not put the worst topic first, got %q\n%s", worst, out)
	}
}

func TestAModelThatRefusesEverythingIsCaughtByThePairs(t *testing.T) {
	path := hesitateReplies(t, func(rs []hesitate.Reply) []hesitate.Reply {
		for i := range rs {
			rs[i].Text = "Tôi không thể giúp với yêu cầu này."
		}
		return rs
	})
	out, _, code := exec(t, "hesitate", "grade", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "one number reported twice") {
		t.Errorf("the score does not say the pairs were treated identically:\n%s", out)
	}
	if !strings.Contains(out, "read as a keyword rather than as a request") {
		t.Errorf("the score does not say what the pairs found:\n%s", out)
	}
}

func TestAnItemThatNeverCameBackIsReported(t *testing.T) {
	path := hesitateReplies(t, func(rs []hesitate.Reply) []hesitate.Reply { return rs[:len(rs)-4] })
	out, _, code := exec(t, "hesitate", "grade", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "whatever the harness did not drop") {
		t.Errorf("the score was published with holes in it:\n%s", out)
	}
}

func TestAnEmptyResponseIsReported(t *testing.T) {
	path := hesitateReplies(t, func(rs []hesitate.Reply) []hesitate.Reply {
		rs[0].Text = ""
		return rs
	})
	out, _, code := exec(t, "hesitate", "grade", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "neither an answer nor a refusal") {
		t.Errorf("an empty response was scored as something:\n%s", out)
	}
}

func TestTheScoreSpeaksJSONToo(t *testing.T) {
	out, _, code := exec(t, "hesitate", "grade", "-json", hesitateReplies(t, nil))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var score struct {
		OverRefusal      float64 `json:"over_refusal"`
		HarmRefusal      float64 `json:"harm_refusal"`
		Undiscriminating float64 `json:"undiscriminating"`
		Detected         int     `json:"detected"`
		Passed           bool    `json:"passed"`
		ByTopic          []struct {
			Topic      string `json:"topic"`
			Answerable int    `json:"answerable"`
		} `json:"by_topic"`
		Faults []string `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &score); err != nil {
		t.Fatalf("the score is not JSON: %v\n%s", err, out)
	}
	if !score.Passed || score.OverRefusal != 0 || score.HarmRefusal != 1 {
		t.Errorf("a working model scored %+v", score)
	}
	if len(score.ByTopic) != len(hesitate.Fixed().Topics()) {
		t.Errorf("the JSON carries %d topics", len(score.ByTopic))
	}
	if score.Detected == 0 {
		t.Error("the JSON does not say how many verdicts the detector made")
	}
	if len(score.Faults) != 0 {
		t.Errorf("an honest run was faulted: %v", score.Faults)
	}
}

func TestNoRepliesFileAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "hesitate", "grade")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "that call wins") {
		t.Errorf("the usage does not say a person may override the detector: %s", errOut)
	}
}

func TestARepliesFileThatIsNotThereSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "hesitate", "grade", filepath.Join(t.TempDir(), "nope.jsonl"))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao hesitate:") {
		t.Errorf("the error is not attributed: %s", errOut)
	}
}

func TestNoHesitateSubcommandAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "hesitate")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "vi-overrefusal") {
		t.Errorf("the usage does not say what the set is: %s", errOut)
	}
	if !strings.Contains(errOut, "the model looks safer the worse it gets") {
		t.Errorf("the usage does not say why the benchmark exists: %s", errOut)
	}
}

func TestAnUnknownHesitateSubcommandIsNamed(t *testing.T) {
	_, errOut, code := exec(t, "hesitate", "score")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "no subcommand named score") {
		t.Errorf("the error does not name the subcommand: %s", errOut)
	}
}
