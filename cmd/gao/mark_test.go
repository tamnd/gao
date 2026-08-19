package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/mark"
	"github.com/tamnd/gao/normalize"
)

// markPages is real Vietnamese, long enough to clear the default length floor.
var markPages = []string{
	`Hà Nội là thủ đô của nước Cộng hòa xã hội chủ nghĩa Việt Nam, nằm ở phía bắc, bên bờ sông Hồng. Thành phố có lịch sử hơn một nghìn năm kể từ khi vua Lý Thái Tổ dời đô về đây và đặt tên là Thăng Long, nghĩa là rồng bay lên. Khu phố cổ với ba mươi sáu phố phường vẫn giữ được cách bố trí từ thời xưa, mỗi phố bán một mặt hàng riêng, và tên phố ngày nay vẫn nhắc lại nghề cũ dù nghề ấy đã mất từ lâu.`,

	`Cây lúa nước được trồng ở đồng bằng sông Cửu Long từ hàng trăm năm nay và là nguồn sống của phần lớn nông dân trong vùng. Mỗi năm người ta cấy hai đến ba vụ, tùy theo con nước và tùy theo giống lúa. Sau khi gặt, hạt thóc được phơi khô trên sân, rồi đem xay để tách vỏ trấu, sàng để loại bỏ tấm và cám, và cuối cùng là nhặt bỏ những hạt sạn còn sót lại trước khi nấu thành cơm.`,

	`Tiếng Việt có sáu thanh điệu, trong đó năm thanh được ghi bằng dấu và một thanh không có dấu nào. Điều đó khiến cho một âm tiết viết không dấu có thể ứng với nhiều từ khác hẳn nhau về nghĩa. Người đọc quen với ngôn ngữ này khôi phục dấu một cách tự nhiên nhờ ngữ cảnh, nhưng máy thì phải học điều đó, và đây chính là lý do bài toán khôi phục dấu vừa dễ tạo dữ liệu vừa khó giải cho đúng.`,
}

// markCorpus writes each page as its own text file, which is one document each.
func markCorpus(t *testing.T, texts []string) []string {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, 0, len(texts))
	for i, text := range texts {
		p := filepath.Join(dir, string(rune('a'+i))+".txt")
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

// markSet builds a task set on disk and returns its path.
func markSet(t *testing.T) (set string, items []mark.Item) {
	t.Helper()
	set = filepath.Join(t.TempDir(), "vi-diacritic.jsonl")
	args := append([]string{"mark", "build", "-o", set}, markCorpus(t, markPages)...)
	if _, errOut, code := exec(t, args...); code != 0 {
		t.Fatalf("gao mark build: exit %d\n%s", code, errOut)
	}
	b, err := os.ReadFile(set)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var it mark.Item
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			t.Fatalf("the set does not read back: %v", err)
		}
		items = append(items, it)
	}
	return set, items
}

func TestMarkBuildTurnsPagesIntoQuestions(t *testing.T) {
	_, items := markSet(t)

	if len(items) != len(markPages) {
		t.Fatalf("built %d items from %d pages", len(items), len(markPages))
	}
	for i, it := range items {
		if it.Prompt != normalize.Bare(it.Answer) {
			t.Errorf("item %d: the question is not the answer with its marks off", i)
		}
		if it.DocID.IsZero() {
			t.Errorf("item %d carries no document identity, so it cannot be held out", i)
		}
		if it.Marked == 0 {
			t.Errorf("item %d has nothing to restore", i)
		}
	}
}

// The identity is the whole reason this is safe to publish, so it has to
// survive the round trip through the file.
func TestMarkBuildKeepsTheIdentityADocumentCameWith(t *testing.T) {
	_, items := markSet(t)
	want := map[doc.Hash]bool{}
	for _, p := range markPages {
		want[doc.SumString(p)] = true
	}
	for _, it := range items {
		if !want[it.DocID] {
			t.Errorf("item %s does not match any document it was built from", it.DocID)
		}
	}
}

func TestMarkBuildSaysWhatItThrewAway(t *testing.T) {
	texts := append([]string{}, markPages...)
	texts = append(texts, normalize.Bare(markPages[0]), "Hà Nội mùa thu.")
	args := append([]string{"mark", "build", "-o", filepath.Join(t.TempDir(), "set.jsonl")}, markCorpus(t, texts)...)

	out, errOut, code := exec(t, args...)
	if code != 0 {
		t.Fatalf("gao mark build: exit %d\n%s", code, errOut)
	}
	for _, want := range []string{"3 items from 5 documents", "typed without marks  1", "too short            1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the accounting does not say %q:\n%s", want, out)
		}
	}
}

func TestMarkBuildOnNothingUsableIsAnError(t *testing.T) {
	bare := []string{normalize.Bare(markPages[0]), normalize.Bare(markPages[1])}
	args := append([]string{"mark", "build"}, markCorpus(t, bare)...)

	_, errOut, code := exec(t, args...)
	if code != 1 {
		t.Fatalf("gao mark build on unusable input: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "nothing to hold out") {
		t.Errorf("the error does not say why an empty set is useless:\n%s", errOut)
	}
}

// The two numbers that have to be published with any result.
func TestMarkBaselinePrintsBothFloors(t *testing.T) {
	set, _ := markSet(t)
	counting := markCorpus(t, []string{
		`Thủ đô của nước này nằm bên bờ một con sông lớn ở phía bắc và có lịch sử rất lâu đời. Người dân ở đây trồng lúa nước, phơi thóc cho khô rồi đem xay và sàng trước khi nấu thành cơm. Ngôn ngữ của họ có sáu thanh điệu và chỉ năm thanh được ghi bằng dấu, cho nên một âm tiết viết không dấu có thể là nhiều từ khác nhau về nghĩa.`,
	})

	out, errOut, code := exec(t, append([]string{"mark", "baseline", "-items", set}, counting...)...)
	if code != 0 {
		t.Fatalf("gao mark baseline: exit %d\n%s", code, errOut)
	}
	for _, want := range []string{"answer with the question", "answer from a table", "marks restored", "syllables exact", "character accuracy"} {
		if !strings.Contains(out, want) {
			t.Errorf("the baseline does not report %q:\n%s", want, out)
		}
	}
	// Doing nothing restores no marks, and character accuracy still reads high,
	// which is the reason the first number is the one reported.
	if !strings.Contains(out, "marks restored     0.000") {
		t.Errorf("answering with the question did not report zero marks restored:\n%s", out)
	}
}

func TestMarkGradeScoresAPerfectRunAndAnEmptyOne(t *testing.T) {
	set, items := markSet(t)
	dir := t.TempDir()

	perfect := filepath.Join(dir, "perfect.jsonl")
	writeAnswers(t, perfect, items, func(it mark.Item) string { return it.Answer })
	out, errOut, code := exec(t, "mark", "grade", "-items", set, perfect)
	if code != 0 {
		t.Fatalf("gao mark grade: exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "marks restored     1.000") {
		t.Errorf("a perfect run did not restore every mark:\n%s", out)
	}
	if !strings.Contains(out, "syllables exact    1.000") {
		t.Errorf("a perfect run did not get every syllable:\n%s", out)
	}

	lazy := filepath.Join(dir, "lazy.jsonl")
	writeAnswers(t, lazy, items, func(it mark.Item) string { return it.Prompt })
	out, _, code = exec(t, "mark", "grade", "-items", set, lazy)
	if code != 0 {
		t.Fatalf("gao mark grade: exit %d", code)
	}
	if !strings.Contains(out, "marks restored     0.000") {
		t.Errorf("handing the question back was not scored as restoring nothing:\n%s", out)
	}
}

// A model that declined to answer has not earned the item, and a grader that
// quietly skipped it would report a score over whatever the model felt like
// attempting.
func TestMarkGradeCountsTheItemsWithNoAnswer(t *testing.T) {
	set, items := markSet(t)
	partial := filepath.Join(t.TempDir(), "partial.jsonl")
	writeAnswers(t, partial, items[:1], func(it mark.Item) string { return it.Answer })

	out, _, code := exec(t, "mark", "grade", "-items", set, partial)
	if code != 0 {
		t.Fatalf("gao mark grade: exit %d", code)
	}
	if !strings.Contains(out, "2 items had no answer") {
		t.Errorf("the grade does not say how many items went unanswered:\n%s", out)
	}
	if !strings.Contains(out, "3 items") {
		t.Errorf("the grade is not over the whole set:\n%s", out)
	}
}

func TestMarkGradeMarksAnAnswerThatRewroteTheText(t *testing.T) {
	set, items := markSet(t)
	rewritten := filepath.Join(t.TempDir(), "rewritten.jsonl")
	writeAnswers(t, rewritten, items, func(mark.Item) string {
		return "Hà Nội là thủ đô của Việt Nam và thành phố này rất đẹp."
	})

	out, _, code := exec(t, "mark", "grade", "-items", set, rewritten)
	if code != 0 {
		t.Fatalf("gao mark grade: exit %d", code)
	}
	if !strings.Contains(out, "unfaithful") {
		t.Errorf("an answer that changed the letters was not reported as unfaithful:\n%s", out)
	}
	if !strings.Contains(out, "syllables exact    0.000") {
		t.Errorf("syllables were counted right against a rewritten answer:\n%s", out)
	}
}

func TestMarkGradePerItem(t *testing.T) {
	set, items := markSet(t)
	answers := filepath.Join(t.TempDir(), "answers.jsonl")
	writeAnswers(t, answers, items, func(it mark.Item) string { return it.Answer })

	out, _, code := exec(t, "mark", "grade", "-v", "-items", set, answers)
	if code != 0 {
		t.Fatalf("gao mark grade -v: exit %d", code)
	}
	for _, it := range items {
		if !strings.Contains(out, it.DocID.String()) {
			t.Errorf("-v did not report item %s:\n%s", it.DocID, out)
		}
	}
}

func TestMarkUsageErrors(t *testing.T) {
	set, _ := markSet(t)
	answers := filepath.Join(t.TempDir(), "a.jsonl")
	if err := os.WriteFile(answers, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string][]string{
		"no subcommand":          {"mark"},
		"unknown":                {"mark", "restore"},
		"build with no files":    {"mark", "build"},
		"baseline with no set":   {"mark", "baseline", "x.txt"},
		"baseline with no files": {"mark", "baseline", "-items", set},
		"grade with no set":      {"mark", "grade", answers},
		"grade with two files":   {"mark", "grade", "-items", set, answers, answers},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, code := exec(t, args...); code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
		})
	}
}

func TestMarkOnAFileThatIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.txt")
	if _, _, code := exec(t, "mark", "build", missing); code != 1 {
		t.Errorf("gao mark build on a missing file: exit %d, want 1", code)
	}
	set, _ := markSet(t)
	if _, _, code := exec(t, "mark", "grade", "-items", set, missing); code != 1 {
		t.Errorf("gao mark grade on a missing file: exit %d, want 1", code)
	}
	if _, _, code := exec(t, "mark", "grade", "-items", missing, missing); code != 1 {
		t.Errorf("gao mark grade with a missing set: exit %d, want 1", code)
	}
}

func TestMarkHelpAndTheCommandList(t *testing.T) {
	out, _, code := exec(t, "mark", "help")
	if code != 0 {
		t.Fatalf("gao mark help: exit %d", code)
	}
	for _, want := range []string{"build", "baseline", "grade", "vi-diacritic"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help does not mention %q:\n%s", want, out)
		}
	}
	top, _, code := exec(t, "help")
	if code != 0 {
		t.Fatalf("gao help: exit %d", code)
	}
	if !strings.Contains(top, "dau") {
		t.Errorf("dau is not in the command list:\n%s", top)
	}
}

func writeAnswers(t *testing.T, path string, items []mark.Item, answer func(mark.Item) string) {
	t.Helper()
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, it := range items {
		line := struct {
			DocID  doc.Hash `json:"doc_id"`
			Answer string   `json:"answer"`
		}{it.DocID, answer(it)}
		if err := enc.Encode(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
