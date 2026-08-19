package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/follow"
)

// What an answer looks like when the model stayed where it was asked.
const (
	heldVietnamese = "Câu hỏi này có câu trả lời rõ ràng. Phần dưới đây tóm tắt những gì các nguồn công khai ghi lại. Nếu bạn cần chi tiết hơn thì phần cuối nói kỹ hơn về điều đó."
	heldEnglish    = "This is the draft you asked for. The tone is formal and the whole thing is short enough to send. If you want any of it changed, say which part should move."
	droppedMarks   = "Cau hoi nay tra loi duoc. Phan duoi day tom tat nhung gi cac nguon cong khai ghi lai. Neu ban can chi tiet hon thi phan cuoi noi ky hon ve dieu do."
	turnedHalfway  = "Câu hỏi này có câu trả lời rõ ràng. Phần dưới đây tóm tắt những gì các nguồn ghi lại. The rest of this is worth spelling out because the details are what matter. There are three parts to it and they are not the same. The first of them is also the one that people get wrong."
)

// followReplies writes what a model that stays in the language would return, with
// change applied first so a test can break exactly one thing.
func followReplies(t *testing.T, change func([]follow.Reply) []follow.Reply) string {
	t.Helper()
	s := follow.Fixed()
	digest := s.Digest()

	replies := make([]follow.Reply, 0, len(s.Items))
	for _, it := range s.Items {
		text := heldVietnamese
		if it.Wants == follow.Eng {
			text = heldEnglish
		}
		replies = append(replies, follow.Reply{ID: it.ID, Set: digest, Text: text})
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

func TestTheAdherenceSetPrintsAKindAtATime(t *testing.T) {
	out, errOut, code := exec(t, "follow", "items")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, kind := range []string{"plain", "long", "technical", "quoted", "translate"} {
		if !strings.Contains(out, kind) {
			t.Errorf("%s is missing from the set:\n%s", kind, out)
		}
	}
	if !strings.Contains(out, follow.Fixed().Digest().String()) {
		t.Errorf("the set does not print its digest:\n%s", out)
	}
	if !strings.Contains(out, follow.Repo) {
		t.Errorf("the set does not say where it is published:\n%s", out)
	}
}

func TestEveryPromptPrintsWithTheReasonUnderIt(t *testing.T) {
	out, _, code := exec(t, "follow", "items", "-prompts")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "Nấu phở bò tại nhà") {
		t.Errorf("the prompts are not in Vietnamese:\n%s", out)
	}
	if !strings.Contains(out, "the terms are English and the audience is not") {
		t.Errorf("the prompts print without the reason under them:\n%s", out)
	}
	if n := strings.Count(out, "wants en"); n != follow.Fixed().Wanting(follow.Eng) {
		t.Errorf("%d items want English back", n)
	}
}

func TestTheAdherenceSetSpeaksJSON(t *testing.T) {
	out, _, code := exec(t, "follow", "items", "-json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var report struct {
		Set    follow.Set `json:"set"`
		Digest string     `json:"digest"`
		Faults []string   `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the set is not JSON: %v\n%s", err, out)
	}
	if len(report.Set.Items) != len(follow.Fixed().Items) {
		t.Errorf("the JSON carries %d items", len(report.Set.Items))
	}
	if len(report.Faults) != 0 {
		t.Errorf("the set we publish was faulted: %v", report.Faults)
	}
}

func TestASetThatOnlyEverWantsVietnameseIsReported(t *testing.T) {
	s := follow.Fixed()
	for i := range s.Items {
		s.Items[i].Wants = follow.Viet
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "set.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := exec(t, "follow", "items", "-set", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "learned never to write anything else") {
		t.Errorf("the report does not say what a Vietnamese only set rewards:\n%s", out)
	}
}

func TestAModelThatStaysInTheLanguagePasses(t *testing.T) {
	out, errOut, code := exec(t, "follow", "grade", followReplies(t, nil))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "pass:") {
		t.Errorf("the score does not say whether it passed:\n%s", out)
	}
	if !strings.Contains(out, "answered elsewhere") || !strings.Contains(out, "answered without marks") {
		t.Errorf("the two failures are not counted apart:\n%s", out)
	}
}

func TestAModelThatFinishesInEnglishFails(t *testing.T) {
	path := followReplies(t, func(rs []follow.Reply) []follow.Reply {
		for i, r := range rs {
			if strings.HasPrefix(r.ID, "long-") {
				rs[i].Text = turnedHalfway
			}
		}
		return rs
	})
	out, _, code := exec(t, "follow", "grade", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "fail:") {
		t.Errorf("the score passed a model that finished in English:\n%s", out)
	}
	if !strings.Contains(out, "would have missed") {
		t.Errorf("the score does not say what reading only the opening would have missed:\n%s", out)
	}
}

func TestAnswersWithTheMarksLeftOffAreCountedApart(t *testing.T) {
	path := followReplies(t, func(rs []follow.Reply) []follow.Reply {
		for i, r := range rs {
			if strings.HasPrefix(r.ID, "plain-") {
				rs[i].Text = droppedMarks
			}
		}
		return rs
	})
	out, _, code := exec(t, "follow", "grade", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "is a different problem from answering in English") {
		t.Errorf("bare Vietnamese was folded into the other number:\n%s", out)
	}
}

func TestAPersonsCallWinsOverTheClassifier(t *testing.T) {
	path := followReplies(t, func(rs []follow.Reply) []follow.Reply {
		for i := range rs {
			rs[i].Wrote = follow.Viet
			rs[i].Text = heldVietnamese
		}
		return rs
	})
	out, _, code := exec(t, "follow", "grade", "-json", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	var score struct {
		Detected int `json:"detected"`
		InOther  int `json:"in_other"`
	}
	if err := json.Unmarshal([]byte(out), &score); err != nil {
		t.Fatalf("the score is not JSON: %v\n%s", err, out)
	}
	if score.Detected != 0 {
		t.Errorf("the classifier ran on %d answers a person had already read", score.Detected)
	}
	if score.InOther != follow.Fixed().Wanting(follow.Eng) {
		t.Errorf("%d answers were counted in the wrong language, want %d", score.InOther, follow.Fixed().Wanting(follow.Eng))
	}
}

func TestAnItemThatNeverCameBackFromTheModelIsReported(t *testing.T) {
	path := followReplies(t, func(rs []follow.Reply) []follow.Reply { return rs[:len(rs)-3] })
	out, _, code := exec(t, "follow", "grade", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "whatever the harness did not drop") {
		t.Errorf("the score was published with holes in it:\n%s", out)
	}
}

func TestAnAnswerWithNoLanguageInItIsReported(t *testing.T) {
	path := followReplies(t, func(rs []follow.Reply) []follow.Reply {
		rs[0].Text = "42."
		return rs
	})
	out, _, code := exec(t, "follow", "grade", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "harness fault rather than a score") {
		t.Errorf("an unreadable answer was scored as something:\n%s", out)
	}
}

func TestTheAdherenceScoreSpeaksJSONToo(t *testing.T) {
	out, _, code := exec(t, "follow", "grade", "-json", followReplies(t, nil))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var score struct {
		Adherence float64 `json:"adherence"`
		InOther   int     `json:"in_other"`
		Bare      int     `json:"bare"`
		Drifted   int     `json:"drifted"`
		Detected  int     `json:"detected"`
		Passed    bool    `json:"passed"`
		ByKind    []struct {
			Kind  string `json:"kind"`
			Items int    `json:"items"`
		} `json:"by_kind"`
		Faults []string `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &score); err != nil {
		t.Fatalf("the score is not JSON: %v\n%s", err, out)
	}
	if !score.Passed || score.Adherence != 1 || score.InOther != 0 || score.Bare != 0 || score.Drifted != 0 {
		t.Errorf("a model that stayed in the language scored %+v", score)
	}
	if len(score.ByKind) != len(follow.Fixed().Kinds()) {
		t.Errorf("the JSON carries %d kinds", len(score.ByKind))
	}
	if score.Detected == 0 {
		t.Error("the JSON does not say how many verdicts the classifier made")
	}
	if len(score.Faults) != 0 {
		t.Errorf("an honest run was faulted: %v", score.Faults)
	}
}

func TestNoAdherenceRepliesFileAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "follow", "grade")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "that call wins over the") {
		t.Errorf("the usage does not say a person may override the classifier: %s", errOut)
	}
}

func TestAnAdherenceRepliesFileThatIsNotThereSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "follow", "grade", filepath.Join(t.TempDir(), "nope.jsonl"))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao follow:") {
		t.Errorf("the error is not attributed: %s", errOut)
	}
}

func TestNoFollowSubcommandAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "follow")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "vi-adherence") {
		t.Errorf("the usage does not say what the set is: %s", errOut)
	}
	if !strings.Contains(errOut, "none of them score the language it arrived in") {
		t.Errorf("the usage does not say why the benchmark exists: %s", errOut)
	}
}

func TestAnUnknownFollowSubcommandIsNamed(t *testing.T) {
	_, errOut, code := exec(t, "follow", "score")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "no subcommand named score") {
		t.Errorf("the error does not name the subcommand: %s", errOut)
	}
}
