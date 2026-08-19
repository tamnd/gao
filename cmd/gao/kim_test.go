package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kim"
)

// kimSet writes a set built to the frame and a run of answers to it, with each
// change applied first so a test can break one thing.
func kimSet(t *testing.T, breakSet func([]kim.Item) []kim.Item, answer func(kim.Item) string) (items, replies string) {
	t.Helper()

	var set []kim.Item
	for i, c := range kim.Frame() {
		for j := range c.Items {
			it := kim.Item{
				ID:       fmt.Sprintf("N%03d-%d", i, j),
				Kind:     c.Kind,
				Length:   c.Length,
				Depth:    c.Depth,
				Second:   c.Second,
				Haystack: doc.SumString("haystack"),
				Question: "Nghị quyết nào nói về việc này?",
			}
			switch c.Kind {
			case kim.Absent:
				it.Question = "Văn bản nói gì về sản lượng cà phê?"
			case kim.Toned:
				it.Answer, it.Near, it.Decoy = "Hoà Bình", []string{"Họa Bình"}, []string{"Ninh Bình"}
			default:
				it.Answer, it.Decoy = "số 42/2019/QH14", []string{"số 43/2019/QH14"}
			}
			set = append(set, it)
		}
	}
	if breakSet != nil {
		set = breakSet(set)
	}

	if answer == nil {
		answer = func(it kim.Item) string {
			if it.Kind == kim.Absent {
				return "Văn bản không đề cập đến điều đó."
			}
			return "Theo tài liệu, đó là " + it.Answer + "."
		}
	}
	run := make([]kim.Reply, 0, len(set))
	for _, it := range set {
		run = append(run, kim.Reply{
			Item: it.ID, Frame: kim.Digest(), Text: answer(it),
			Box: "gamingpc", Context: it.Length,
		})
	}

	dir := t.TempDir()
	items = filepath.Join(dir, "items.jsonl")
	replies = filepath.Join(dir, "replies.jsonl")
	writeKim(t, items, set)
	writeKim(t, replies, run)
	return items, replies
}

func writeKim[T any](t *testing.T, path string, rows []T) {
	t.Helper()
	var b strings.Builder
	for _, r := range rows {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTheGridIsReadableBeforeAnythingIsBuilt(t *testing.T) {
	out, errOut, code := exec(t, "kim", "frame")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"vi-needle", "plain", "toned", "split", "absent", "million tokens", "Nothing has been built yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("the frame does not mention %q:\n%s", want, out)
		}
	}
}

func TestTheFrameSaysWhyEachKindIsOnIt(t *testing.T) {
	out, _, _ := exec(t, "kim", "frame")
	for _, want := range []string{
		"which is the item an English set cannot have",
		"caught rather than rewarded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the frame does not say why a kind is on the grid: %q\n%s", want, out)
		}
	}
}

func TestEverySquareOfTheGridCanBePrinted(t *testing.T) {
	out, _, code := exec(t, "kim", "frame", "-cells")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "nothing to find") {
		t.Errorf("the absent squares do not say what is in them:\n%s", out)
	}
	if !strings.Contains(out, "% and ") {
		t.Errorf("a split square does not print both of its depths:\n%s", out)
	}
}

func TestTheFramePrintsAsJSON(t *testing.T) {
	out, errOut, code := exec(t, "kim", "frame", "-json", "-cells")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got kimFrameReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Items != kim.Size() || len(got.Cells) != len(kim.Frame()) {
		t.Errorf("%d items over %d cells, want %d over %d", got.Items, len(got.Cells), kim.Size(), len(kim.Frame()))
	}
	if got.Frame != kim.Digest() {
		t.Error("the frame printed a digest that is not its own")
	}
}

func TestASetBuiltToTheGridChecksOut(t *testing.T) {
	items, _ := kimSet(t, nil, nil)
	out, errOut, code := exec(t, "kim", "check", items)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "built to frame") {
		t.Errorf("a good set was not confirmed:\n%s", out)
	}
}

func TestASetWithHolesInItIsRefusedBeforeAModelSeesIt(t *testing.T) {
	items, _ := kimSet(t, func(set []kim.Item) []kim.Item { return set[:40] }, nil)
	out, _, code := exec(t, "kim", "check", items)
	if code == 0 {
		t.Fatalf("a set missing most of the grid checked out:\n%s", out)
	}
	if !strings.Contains(out, "the easy squares") {
		t.Errorf("the report does not say what a hole in the grid does to the average:\n%s", out)
	}
}

func TestAModelThatReadsTheWholeContextPassesAtTheCommandLine(t *testing.T) {
	items, replies := kimSet(t, nil, nil)
	out, errOut, code := exec(t, "kim", "grade", "-items", items, replies)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "vi-needle passes") {
		t.Errorf("a model that answered everything did not pass:\n%s", out)
	}
}

func TestAModelThatSkimsTheMiddleFailsOnTheShapeRatherThanTheAverage(t *testing.T) {
	items, replies := kimSet(t, nil, func(it kim.Item) string {
		if it.Kind != kim.Absent && it.Depth > 0.2 && it.Depth < 0.8 {
			return "Tài liệu không nói rõ."
		}
		if it.Kind == kim.Absent {
			return "Văn bản không đề cập đến điều đó."
		}
		return "Theo tài liệu, đó là " + it.Answer + "."
	})
	out, _, code := exec(t, "kim", "grade", "-items", items, replies)
	if code == 0 {
		t.Fatalf("a model that answered nothing in the middle passed:\n%s", out)
	}
	if !strings.Contains(out, "reads the ends of a context") {
		t.Errorf("the verdict does not name the failure:\n%s", out)
	}
}

func TestThePositionCurveCanBePrinted(t *testing.T) {
	items, replies := kimSet(t, nil, nil)
	out, _, _ := exec(t, "kim", "grade", "-items", items, "-curve", replies)
	if !strings.Contains(out, "recall by length and depth") {
		t.Fatalf("the curve was not printed:\n%s", out)
	}
	if !strings.Contains(out, "128000 tokens") {
		t.Errorf("the longest length is not on the curve:\n%s", out)
	}
}

func TestTheWholeGradeIsAvailableAsJSON(t *testing.T) {
	items, replies := kimSet(t, nil, nil)
	out, errOut, code := exec(t, "kim", "grade", "-items", items, "-json", replies)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got kimGradeReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Graded != kim.Size() {
		t.Errorf("graded %d of %d", got.Graded, kim.Size())
	}
	if !got.Passes {
		t.Errorf("a clean run did not pass: %s", got.Verdict)
	}
}

func TestGradingWithoutTheSetIsAUsageError(t *testing.T) {
	_, replies := kimSet(t, nil, nil)
	out, errOut, code := exec(t, "kim", "grade", replies)
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "usage: gao needle") {
		t.Errorf("no usage printed: %s", errOut)
	}
}

func TestAMissingFileIsAFailureRatherThanAnEmptyRun(t *testing.T) {
	items, _ := kimSet(t, nil, nil)
	_, errOut, code := exec(t, "kim", "grade", "-items", items, filepath.Join(t.TempDir(), "nothing.jsonl"))
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errOut)
	}
	if !strings.Contains(errOut, "gao needle:") {
		t.Errorf("the failure was not attributed: %s", errOut)
	}
}

func TestKimWithoutASubcommandPrintsUsage(t *testing.T) {
	_, errOut, code := exec(t, "kim")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
	if !strings.Contains(errOut, "usage: gao needle") {
		t.Errorf("no usage printed: %s", errOut)
	}
}

func TestAnUnknownKimSubcommandSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "kim", "find")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
	if !strings.Contains(errOut, "no subcommand named find") {
		t.Errorf("the unknown subcommand was not named: %s", errOut)
	}
}

func TestKimHelpGoesToStdout(t *testing.T) {
	out, _, code := exec(t, "kim", "help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "usage: gao needle") {
		t.Errorf("help did not go to stdout: %s", out)
	}
}
