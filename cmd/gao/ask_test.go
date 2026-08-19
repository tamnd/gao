package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var askKinds = []string{"tong-hop", "so-sanh", "trinh-tu", "sua-doi", "dem-so"}

// askLine is one question that came back the way a good one comes back.
func askLine(i, tokens int, doc string, edit func(*map[string]any)) string {
	q := map[string]any{
		"id":          fmt.Sprintf("vi-longdoc-%04d", i),
		"document":    doc,
		"kind":        askKinds[i%len(askKinds)],
		"tokens":      tokens,
		"closed_book": true,
		"recalled":    false,
		"graders":     2,
		"agreed":      2,
	}
	spans := fmt.Sprintf(`[{"start":%d,"end":%d},{"start":%d,"end":%d}]`,
		tokens/12, tokens/12+180, tokens*8/10, tokens*8/10+240)
	if edit != nil {
		edit(&q)
	}
	if s, ok := q["spans"].(string); ok {
		spans = s
		delete(q, "spans")
	}
	fields := make([]string, 0, 10)
	for _, k := range []string{"id", "document", "kind", "tokens", "closed_book", "recalled", "graders", "agreed"} {
		fields = append(fields, fmt.Sprintf("%q:%s", k, value(q[k])))
	}
	fields = append(fields, `"spans":`+spans)
	return "{" + strings.Join(fields, ",") + "}"
}

// askSet writes n questions across all three rungs and enough documents that no
// one of them carries the set.
func askSet(t *testing.T, n int, edit func(i int, q *map[string]any)) string {
	t.Helper()
	lengths := []int{38_000, 44_000, 71_000, 88_000, 140_000, 210_000}
	lines := make([]string, 0, n)
	for i := range n {
		var apply func(*map[string]any)
		if edit != nil {
			apply = func(q *map[string]any) { edit(i, q) }
		}
		doc := fmt.Sprintf("vbpl-%d-%03d", 2004+i%20, i%97)
		lines = append(lines, askLine(i, lengths[i%len(lengths)], doc, apply))
	}
	return askFile(t, lines...)
}

func askFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vi-longdoc-qa.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAskReportsTheLadderRatherThanTheCount(t *testing.T) {
	out, errOut, code := exec(t, "ask", askSet(t, 600, nil))
	if code != 0 {
		t.Fatalf("a composed set exited %d: %s\n%s", code, errOut, out)
	}
	if !strings.Contains(out, "Every rung of the context ladder is filled") {
		t.Errorf("the verdict does not report the ladder:\n%s", out)
	}
	for _, rung := range []string{"32,000", "65,536", "131,072"} {
		if !strings.Contains(out, rung) {
			t.Errorf("the rung %s is missing from the table:\n%s", rung, out)
		}
	}
	if !strings.Contains(out, "leans on hardest") {
		t.Errorf("the report does not say which document the set leans on:\n%s", out)
	}
}

func TestAskExitsTwoWhenNothingInTheSetLivesAtTheTopRung(t *testing.T) {
	lengths := []int{38_000, 44_000, 71_000, 88_000}
	lines := make([]string, 0, 600)
	for i := range 600 {
		lines = append(lines, askLine(i, lengths[i%len(lengths)], fmt.Sprintf("vbpl-%d-%03d", 2004+i%20, i%97), nil))
	}
	out, _, code := exec(t, "ask", askFile(t, lines...))
	if code != 2 {
		t.Fatalf("a set with an empty top rung exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "cannot say whether the extension to that length worked") {
		t.Errorf("the verdict does not say what the hole costs:\n%s", out)
	}
}

func TestAskCountsTheQuestionsAModelAnsweredWithNoDocument(t *testing.T) {
	path := askSet(t, 640, func(i int, q *map[string]any) {
		if i%16 == 0 {
			(*q)["recalled"] = true
		}
	})
	out, _, code := exec(t, "ask", path)
	if code != 0 {
		t.Fatalf("a set with forty memory questions in it exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "40 of them thrown out for being answered with no document attached") {
		t.Errorf("the closed book run is not reported:\n%s", out)
	}
	if !strings.Contains(out, "the check most sets of this kind skip") {
		t.Errorf("the verdict does not say what the check is worth:\n%s", out)
	}
}

func TestAskRefusesASetBuiltOnOneDocument(t *testing.T) {
	path := askSet(t, 600, func(i int, q *map[string]any) {
		if i%4 == 0 {
			(*q)["document"] = "luat-dat-dai-2024"
		}
	})
	out, _, code := exec(t, "ask", path)
	if code != 1 {
		t.Fatalf("a set that is a quarter one document exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "so what this measures is that document") {
		t.Errorf("the refusal does not say what a leaning set measures:\n%s", out)
	}
}

func TestAskNamesWhatEachRejectedQuestionFailed(t *testing.T) {
	path := askSet(t, 600, func(i int, q *map[string]any) {
		switch i {
		case 5:
			(*q)["spans"] = `[{"start":400,"end":700},{"start":2100,"end":2600}]`
		case 9:
			(*q)["closed_book"] = false
		case 13:
			(*q)["agreed"] = 1
		}
	})
	out, _, code := exec(t, "ask", "-rejects", path)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"a model that reads the opening answers it",
		"never put to a model without the document",
		"a question rather than a test item",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rejects do not name %q:\n%s", want, out)
		}
	}
}

func TestAskJSONCarriesTheLadderAndTheClosedBookRun(t *testing.T) {
	out, _, code := exec(t, "ask", "-json", askSet(t, 600, nil))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		`"ladder"`, `"composition"`, `"recalled"`, `"admitted"`, `"heaviest"`, `"reach"`, `"holds"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the JSON does not carry %s:\n%s", want, out)
		}
	}
}

func TestAskWithoutASetIsAUsageError(t *testing.T) {
	if _, _, code := exec(t, "ask"); code != 2 {
		t.Error("hoi with no argument did not exit 2")
	}
	if _, _, code := exec(t, "ask", filepath.Join(t.TempDir(), "nothing.jsonl")); code != 1 {
		t.Error("a set file that is not there did not exit 1")
	}
}
