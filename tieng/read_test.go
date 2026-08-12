package tieng_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/tieng"
)

func write(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestADocumentIsNamedByTheFileItCameOutOf(t *testing.T) {
	path := write(t, "bai-viet.txt", []byte(line(0)+"\n"))

	docs, err := tieng.ReadDocs([]string{path})
	if err != nil {
		t.Fatalf("reading one document: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("%d documents came back, want 1", len(docs))
	}
	if docs[0].Name != "bai-viet.txt" {
		t.Errorf("the document came back named %q, and a fault that quotes half a screen of directory is one nobody reads", docs[0].Name)
	}
	if !strings.HasPrefix(docs[0].Text, "của") {
		t.Errorf("the text came back as %q", docs[0].Text)
	}
}

// Text in a legacy Vietnamese font encoding is the case this catches, and it is
// the one where reading it anyway would produce a governed share of nearly zero
// and no indication why.
func TestTextThatIsNotUTF8IsRefusedRatherThanCounted(t *testing.T) {
	path := write(t, "vni.txt", []byte{0xc3, 0x28, 0xa0, 0xa1})

	_, err := tieng.ReadDocs([]string{path})
	if err == nil {
		t.Fatal("bytes that are not UTF-8 were read as text")
	}
	if !strings.Contains(err.Error(), "vni.txt") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

func TestADocumentThatIsNotThereSaysSo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "khong-co.txt")

	_, err := tieng.ReadDocs([]string{missing})
	if err == nil {
		t.Fatal("a document that does not exist was read")
	}
	if !strings.Contains(err.Error(), "khong-co.txt") {
		t.Errorf("the error does not name the file: %v", err)
	}
}
