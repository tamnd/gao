package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/dien"
)

// dienPages are pages to build items out of. They are on the same subjects as
// dauPages, which is what the ranking is counted over here, because a blank is
// only worth setting over a syllable the ranking has seen.
var dienPages = []string{
	`Thành phố nằm bên bờ sông Hồng và có lịch sử hơn một nghìn năm kể từ khi vua Lý Thái Tổ dời đô về đây. Người ta vẫn nhắc lại tên cũ của mỗi con phố, dù cái nghề mà tên ấy nói đến thì đã mất từ lâu rồi. Phần lớn khách đến thăm khu phố cổ đều dừng lại ở những hàng quán nhỏ trên vỉa hè, và ăn sáng ở đó trước khi đi tiếp vào trong.`,

	`Nông dân trong vùng đồng bằng sông Cửu Long cấy lúa nước theo con nước, mỗi năm hai đến ba vụ tùy giống. Sau khi gặt, người ta phơi thóc khô trên sân rồi đem xay để tách vỏ trấu ra khỏi hạt. Cám và tấm được sàng bỏ, những hạt sạn còn sót lại thì phải nhặt bằng tay, và chỉ sau tất cả những việc ấy thì gạo mới đem nấu thành cơm được.`,

	`Một âm tiết viết không dấu trong tiếng Việt có thể ứng với nhiều từ khác hẳn nhau về nghĩa, và người đọc quen với ngôn ngữ này khôi phục dấu một cách tự nhiên nhờ ngữ cảnh. Máy thì phải học điều đó từ đầu. Đây chính là lý do bài toán khôi phục dấu vừa dễ tạo ra dữ liệu để học vừa khó giải cho thật đúng, và nó là bài toán đáng để đo.`,
}

// dienSet builds a task set on disk and returns its path and its items. The
// ranking is counted over dauPages and the items are built out of dienPages,
// which is the separation the package insists on.
func dienSet(t *testing.T) (set string, items []dien.Item) {
	t.Helper()
	set = filepath.Join(t.TempDir(), "vi-cloze.jsonl")
	args := append([]string{"dien", "build", "-o", set,
		"-count", strings.Join(dauCorpus(t, dauPages), ","),
		"-function", "5", "-band", "20", "-min-chars", "100"},
		dauCorpus(t, dienPages)...)
	if _, errOut, code := exec(t, args...); code != 0 {
		t.Fatalf("gao dien build: exit %d\n%s", code, errOut)
	}
	b, err := os.ReadFile(set)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var it dien.Item
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			t.Fatalf("the set does not read back: %v", err)
		}
		items = append(items, it)
	}
	return set, items
}

func TestDienBuildWritesASetThatReadsBack(t *testing.T) {
	_, items := dienSet(t)
	if len(items) == 0 {
		t.Fatal("no item came out of three pages")
	}
	for _, it := range items {
		if !strings.Contains(it.Prompt, dien.Blank) {
			t.Errorf("an item came back with no blank in it: %q", it.Prompt)
		}
		if len(it.Choices) != dien.Candidates {
			t.Errorf("an item offers %d choices, want %d", len(it.Choices), dien.Candidates)
		}
	}
}

// The whole benchmark rests on the ranking not having seen the passage, so the
// command refuses the mistake rather than producing a set that looks fine.
func TestDienBuildRefusesToCountTheFilesItBuildsFrom(t *testing.T) {
	files := dauCorpus(t, dauPages)
	_, errOut, code := exec(t, "dien", "build", "-count", strings.Join(files, ","), files[0])
	if code == 0 {
		t.Fatal("counting the ranking over the file the items came from was allowed")
	}
	if !strings.Contains(errOut, "with the right one in view") {
		t.Errorf("the refusal does not say why:\n%s", errOut)
	}
}

func TestDienBuildAccountsForEveryDocumentItRead(t *testing.T) {
	set := filepath.Join(t.TempDir(), "vi-cloze.jsonl")
	args := append([]string{"dien", "build", "-o", set,
		"-count", strings.Join(dauCorpus(t, dauPages), ","),
		"-function", "5", "-band", "20", "-min-chars", "100"},
		dauCorpus(t, dienPages)...)
	out, _, code := exec(t, args...)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, r := range dien.Reasons() {
		if !strings.Contains(out, string(r)) {
			t.Errorf("the accounting does not print a line for %q:\n%s", r, out)
		}
	}
	if !strings.Contains(out, "hold these document identities out") {
		t.Errorf("the build does not say the set has to be held out:\n%s", out)
	}
}

func TestDienGradeScoresAPerfectRunAtOneHundred(t *testing.T) {
	set, items := dienSet(t)
	answers := filepath.Join(t.TempDir(), "answers.jsonl")
	var b strings.Builder
	for _, it := range items {
		line, err := json.Marshal(map[string]any{"doc_id": it.DocID, "choice": it.Answer})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(answers, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := exec(t, "dien", "grade", "-items", set, "-box", "gamingpc", answers)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "100.0%") {
		t.Errorf("answering every item right did not score 100%%:\n%s", out)
	}
	if !strings.Contains(out, "gamingpc") {
		t.Errorf("the report does not say which box it came off:\n%s", out)
	}
}

// A model that answers with the text of a candidate rather than its index is
// not doing anything wrong, and a harness that made it convert would be a
// harness with a bug in it.
func TestDienGradeTakesTheCandidateItselfAsAnAnswer(t *testing.T) {
	set, items := dienSet(t)
	answers := filepath.Join(t.TempDir(), "answers.jsonl")
	var b strings.Builder
	for _, it := range items {
		line, err := json.Marshal(map[string]any{"doc_id": it.DocID, "answer": it.Right()})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(answers, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := exec(t, "dien", "grade", "-items", set, answers)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "100.0%") {
		t.Errorf("answering with the candidate itself did not score 100%%:\n%s", out)
	}
}

// A model that declined half the set and scored well on the rest has not scored
// well on the set, and the count of what it skipped has to be printed for that
// to be visible.
func TestDienGradeCountsTheItemsNobodyAnswered(t *testing.T) {
	set, items := dienSet(t)
	answers := filepath.Join(t.TempDir(), "answers.jsonl")
	line, err := json.Marshal(map[string]any{"doc_id": items[0].DocID, "choice": items[0].Answer})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(answers, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := exec(t, "dien", "grade", "-items", set, answers)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if len(items) > 1 && !strings.Contains(out, "had no answer and are scored wrong") {
		t.Errorf("the unanswered items are not counted:\n%s", out)
	}
}

func TestDienBaselinePrintsWhatReadingNothingScores(t *testing.T) {
	set, _ := dienSet(t)
	args := append([]string{"dien", "baseline", "-items", set}, dauCorpus(t, dauPages)...)
	out, errOut, code := exec(t, args...)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	for _, want := range []string{dien.Name, "25.0%", "has learned which syllables are common"} {
		if !strings.Contains(out, want) {
			t.Errorf("the baseline does not say %q:\n%s", want, out)
		}
	}
}

func writeRecipes(t *testing.T, recipes []dien.Recipe) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recipes.json")
	b, err := json.Marshal(recipes)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func dienRecipe(name string, proxy, full float64) dien.Recipe {
	return dien.Recipe{Name: name, Proxy: proxy, Full: full, ProxyBox: "gamingpc", FullBox: "server1"}
}

func TestDienValidateReportsTheAgreementAndTheVerdict(t *testing.T) {
	path := writeRecipes(t, []dien.Recipe{
		dienRecipe("base", 0.41, 0.52),
		dienRecipe("more web", 0.43, 0.54),
		dienRecipe("more books", 0.45, 0.55),
		dienRecipe("more news", 0.47, 0.58),
		dienRecipe("more forum", 0.49, 0.61),
	})
	out, errOut, code := exec(t, "dien", "validate", path)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	for _, want := range []string{"rank correlation 1.00", "10 of 10 pairs", "P10-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the verdict does not say %q:\n%s", want, out)
		}
	}
}

// The kill criterion is worth nothing if the command that measures it exits
// zero and prints a paragraph nobody reads.
func TestDienValidateExitsNonZeroOnRecipesThatCannotSayAnything(t *testing.T) {
	path := writeRecipes(t, []dien.Recipe{
		dienRecipe("base", 0.41, 0.52),
		dienRecipe("more web", 0.43, 0.54),
	})
	_, errOut, code := exec(t, "dien", "validate", path)
	if code == 0 {
		t.Fatal("two recipes were accepted as a validity measurement")
	}
	if !strings.Contains(errOut, "too few recipes") {
		t.Errorf("the refusal does not say what was wrong:\n%s", errOut)
	}
}

func TestDienValidateSaysWhichRecipeIsMalformed(t *testing.T) {
	path := writeRecipes(t, []dien.Recipe{
		dienRecipe("base", 0.41, 0.52),
		dienRecipe("more web", 0.43, 0.54),
		{Name: "more books", Proxy: 0.45, Full: 0.55, ProxyBox: "gamingpc"},
		dienRecipe("more news", 0.47, 0.58),
		dienRecipe("more forum", 0.49, 0.61),
	})
	_, errOut, code := exec(t, "dien", "validate", path)
	if code == 0 {
		t.Fatal("a pair with no box behind its full scale score was accepted")
	}
	if !strings.Contains(errOut, "more books") {
		t.Errorf("the refusal does not name the recipe:\n%s", errOut)
	}
}

func TestDienValidateSaysWhichFileItCouldNotRead(t *testing.T) {
	_, errOut, code := exec(t, "dien", "validate", filepath.Join(t.TempDir(), "missing.json"))
	if code == 0 {
		t.Fatal("a file that is not there was read")
	}
	if !strings.Contains(errOut, "missing.json") {
		t.Errorf("the error does not name the file:\n%s", errOut)
	}
}

func TestDienIsInTheHelp(t *testing.T) {
	out, _, code := exec(t, "help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "dien") {
		t.Errorf("the command list does not hold dien:\n%s", out)
	}
}
