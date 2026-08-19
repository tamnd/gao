package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// attachPage is one page of a scanned Vietnamese legal document, which is the
// shape of most of what route O sees.
func attachPage(doc string, n int, route string, edit func(*map[string]any)) string {
	p := map[string]any{
		"document": doc,
		"page":     n,
		"route":    route,
		"image":    fmt.Sprintf("gao-pdf/2026-09/%s/%04d.jpg", doc, n),
		"bytes":    880_000 + (n%7)*40_000,
		"hash":     fmt.Sprintf("sha256:%s-%04d", doc, n),
		"dpi":      300,
		"chars":    2_400 + (n%11)*90,
		"ink":      0.062,
		"stored":   true,
	}
	if edit != nil {
		edit(&p)
	}
	fields := make([]string, 0, len(p))
	for _, k := range []string{"document", "page", "route", "image", "bytes", "hash", "dpi", "chars", "ink", "stored"} {
		fields = append(fields, fmt.Sprintf("%q:%s", k, value(p[k])))
	}
	return "{" + strings.Join(fields, ",") + "}"
}

func value(v any) string {
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("%q", t)
	case bool:
		return fmt.Sprintf("%t", t)
	case float64:
		return fmt.Sprintf("%g", t)
	}
	return fmt.Sprintf("%v", v)
}

// attachBatch writes docs documents of pages pages each, with edit applied to
// every page, so a test that changes one thing changes one thing.
func attachBatch(t *testing.T, docs, pages int, edit func(doc string, n int, p *map[string]any)) string {
	t.Helper()
	lines := make([]string, 0, docs*pages)
	for d := range docs {
		name := fmt.Sprintf("vbpl-2019-%03d", d)
		for n := 1; n <= pages; n++ {
			var apply func(*map[string]any)
			if edit != nil {
				apply = func(p *map[string]any) { edit(name, n, p) }
			}
			lines = append(lines, attachPage(name, n, "O", apply))
		}
	}
	return attachFile(t, lines...)
}

func attachFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pages.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAttachReportsThePairsRatherThanTheImages(t *testing.T) {
	out, errOut, code := exec(t, "attach", attachBatch(t, 24, 14, nil))
	if code != 0 {
		t.Fatalf("a clean batch exited %d: %s\n%s", code, errOut, out)
	}
	if !strings.Contains(out, "the pairs are what the vision work later reads rather than the pages") {
		t.Errorf("the verdict does not say what the pairs are for:\n%s", out)
	}
	if !strings.Contains(out, "still on the box") || !strings.Contains(out, "in the store") {
		t.Errorf("the disk split is not reported:\n%s", out)
	}
}

func TestAttachNamesTheDocumentThatCameBackWithAHoleInIt(t *testing.T) {
	// Page 6 of one document never rendered. Renumbering around it would shift
	// every pair after it and nothing downstream could tell.
	lines := make([]string, 0, 40)
	for d := range 4 {
		name := fmt.Sprintf("vbpl-2019-%03d", d)
		for n := 1; n <= 10; n++ {
			if d == 2 && n == 6 {
				continue
			}
			lines = append(lines, attachPage(name, n, "O", nil))
		}
	}
	out, _, code := exec(t, "attach", attachFile(t, lines...))
	if code != 1 {
		t.Fatalf("a document with a hole in it exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "vbpl-2019-002 runs to page 10 and is missing page 6") {
		t.Errorf("the gap is not named:\n%s", out)
	}
	if !strings.Contains(out, "closing a gap shifts every pair after it") {
		t.Errorf("the report does not say why the gap is left open:\n%s", out)
	}
	if strings.Count(out, "missing page 6") != 1 {
		t.Errorf("the gap is reported twice, which reads as two problems:\n%s", out)
	}
}

func TestAttachRefusesTextThatCameOffSomeOtherPage(t *testing.T) {
	path := attachBatch(t, 4, 8, func(doc string, n int, p *map[string]any) {
		if doc == "vbpl-2019-001" && n == 5 {
			(*p)["ink"] = 0.0003
		}
	})
	out, _, code := exec(t, "attach", path)
	if code != 1 {
		t.Fatalf("a page paired with somebody else's text exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "the text came off some other page") {
		t.Errorf("the refusal does not say what is wrong with the pair:\n%s", out)
	}
}

func TestAttachExitsTwoWhenTheExtractionLostTooMuchOfTheBatch(t *testing.T) {
	path := attachBatch(t, 10, 10, func(_ string, n int, p *map[string]any) {
		if n == 3 || n == 7 {
			(*p)["chars"] = 4
		}
	})
	out, _, code := exec(t, "attach", path)
	if code != 2 {
		t.Fatalf("a batch the extraction lost a fifth of exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "a report about the extraction rather than a batch") {
		t.Errorf("the verdict does not say what a lossy batch is:\n%s", out)
	}
}

func TestAttachExitsTwoWhenTheRendersAreStillOnTheBox(t *testing.T) {
	path := attachBatch(t, 20, 20, func(_ string, _ int, p *map[string]any) {
		(*p)["stored"] = false
	})
	out, _, code := exec(t, "attach", "-free", "120000000", path)
	if code != 2 {
		t.Fatalf("a batch that outgrew its box exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "a disk that is full rather than a queue that is long") {
		t.Errorf("the verdict does not say what a full box costs:\n%s", out)
	}
	if !strings.Contains(out, "which it does not have room for") {
		t.Errorf("the resident figure is printed without saying what it means:\n%s", out)
	}
	// The same batch against the window the project sets fits, so the flag is
	// what made the difference rather than the batch.
	if _, _, code := exec(t, "attach", path); code != 0 {
		t.Errorf("the same batch against the project window exited %d", code)
	}
}

func TestAttachRefusesARenderThatCannotCarryToneMarks(t *testing.T) {
	path := attachBatch(t, 3, 6, func(_ string, n int, p *map[string]any) {
		if n == 2 {
			(*p)["dpi"] = 144
		}
	})
	out, _, code := exec(t, "attach", path)
	if code != 1 {
		t.Fatalf("a batch rendered too small exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "where Vietnamese tone marks survive") {
		t.Errorf("the refusal does not say what the resolution costs:\n%s", out)
	}
}

func TestAttachJSONCarriesTheRoutesAndTheDiskSplit(t *testing.T) {
	out, _, code := exec(t, "attach", "-json", attachBatch(t, 6, 10, nil))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		`"paired"`, `"attached"`, `"lost"`, `"routes"`, `"resident"`, `"stored"`, `"window"`, `"fits"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the JSON does not carry %s:\n%s", want, out)
		}
	}
}

func TestAttachWithoutABatchIsAUsageError(t *testing.T) {
	if _, _, code := exec(t, "attach"); code != 2 {
		t.Error("dinh with no argument did not exit 2")
	}
	if _, _, code := exec(t, "attach", filepath.Join(t.TempDir(), "nothing.jsonl")); code != 1 {
		t.Error("a batch file that is not there did not exit 1")
	}
}
