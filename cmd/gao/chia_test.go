package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A one page PDF with a text layer, written out by hand so the test says what
// it is testing rather than pointing at a binary fixture.
func writePDF(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	doc := "%PDF-1.7\n" +
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n" +
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>\nendobj\n" +
		"4 0 obj\n<< /Length 1 >>\nstream\n" + body + "\nendstream\nendobj\n" +
		"trailer\n<< /Root 1 0 R >>\n%%EOF\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const pdfProse = "BT (Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc. " +
	"Điều 1. Luật này quy định về quyền và nghĩa vụ của công dân trong việc tiếp cận " +
	"thông tin và trách nhiệm của cơ quan nhà nước trong việc bảo đảm quyền đó.) Tj ET"

func TestChiaRoutesEachDocumentAndCountsThem(t *testing.T) {
	dir := t.TempDir()
	a := writePDF(t, dir, "born-digital.pdf", pdfProse)
	b := writePDF(t, dir, "scan.pdf", "BT (Trang 1) Tj ET")

	out, _, code := exec(t, "chia", a, b)
	if code != 0 {
		t.Fatalf("gao route: exit %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "T\t"+a) {
		t.Errorf("the page of prose was not routed to T:\n%s", out)
	}
	if !strings.Contains(out, "O\t"+b) {
		t.Errorf("the page image was not routed to O:\n%s", out)
	}
	if !strings.Contains(out, "2 documents routed on") {
		t.Errorf("the run printed no distribution, which is the number the slice is costed from:\n%s", out)
	}
}

// The distribution is published, so it has to carry the hardware it came off.
func TestChiaLabelsTheDistributionWithTheBoxItRanOn(t *testing.T) {
	dir := t.TempDir()
	out, _, code := exec(t, "chia", "-box", "gamingpc", writePDF(t, dir, "a.pdf", pdfProse))
	if code != 0 {
		t.Fatalf("gao route: exit %d, want 0", code)
	}
	if !strings.Contains(out, "routed on gamingpc") {
		t.Errorf("the distribution does not name the box:\n%s", out)
	}
}

func TestChiaExplainsItselfWhenAsked(t *testing.T) {
	dir := t.TempDir()
	out, _, code := exec(t, "chia", "-why", writePDF(t, dir, "a.pdf", pdfProse))
	if code != 0 {
		t.Fatalf("gao route -why: exit %d, want 0", code)
	}
	for _, want := range []string{"pages", "characters a page", "image"} {
		if !strings.Contains(out, want) {
			t.Errorf("gao route -why did not print %q:\n%s", want, out)
		}
	}
}

// A file that cannot be read is a failure of the run rather than a document
// that routes somewhere, because a routing distribution missing the documents
// it could not open is a distribution that understates its own cost.
func TestChiaFailsOnAFileItCannotRead(t *testing.T) {
	_, errOut, code := exec(t, "chia", filepath.Join(t.TempDir(), "khong-co.pdf"))
	if code != 1 {
		t.Errorf("gao route on a missing file: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "khong-co.pdf") {
		t.Errorf("stderr did not name the file it could not read: %q", errOut)
	}
}

func TestChiaWithNoFilesSaysWhatItIsFor(t *testing.T) {
	_, errOut, code := exec(t, "chia")
	if code != 2 {
		t.Errorf("gao route: exit %d, want 2", code)
	}
	for _, want := range []string{"born digital", "legacy", "OCR"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the usage does not mention %q: %q", want, errOut)
		}
	}
}
