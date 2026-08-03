package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

// buildSnapshot writes a small signed snapshot and returns the directory and the
// public key it was signed with, in hex.
func buildSnapshot(t *testing.T) (dir, pub string) {
	t.Helper()
	dir = t.TempDir()

	set, err := kho.NewShardSet[*doc.Document](dir, 4, func(d *doc.Document) doc.Hash { return d.DocID })
	if err != nil {
		t.Fatalf("NewShardSet: %v", err)
	}
	for i := range 40 {
		if err := set.Append(document(t, i)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	shards, err := set.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	m := &kho.Manifest{
		Snapshot:  "2026-09",
		CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		Pipeline:  "0.1.0",
		Box:       "server1",
		Stages:    []kho.Stage{{Name: "gat@0.1.0", ConfigHash: doc.SumString("gat config")}},
		Shards:    shards,
	}
	for _, s := range shards {
		m.Counts.Documents += int64(s.Documents)
		m.Counts.Bytes += s.Bytes
	}
	m.Counts.Natural = m.Counts.Documents

	_, priv, err := kho.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Seal(priv, m.CreatedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := kho.WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	return dir, m.Signature.PublicKey
}

// document builds a document that satisfies the ingest contract.
func document(t *testing.T, i int) *doc.Document {
	t.Helper()
	text := "Bài viết số " + string(rune('A'+i%26)) + ". " +
		"Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc. " +
		"Nội dung của tài liệu này đủ dài để vượt qua ngưỡng tối thiểu của hợp đồng nhập liệu, " +
		"và nó khác nhau ở mỗi tài liệu để người đọc phân biệt được."
	text += strings.Repeat(" thêm một câu nữa cho đủ độ dài.", i%3+1)

	d := &doc.Document{
		RawID:         doc.SumString("raw:" + text),
		Text:          text,
		SchemaVersion: doc.SchemaVersion,
		Provenance: doc.Provenance{
			Source:          doc.SourceCrawl,
			SourceLocator:   "gao-crawl-2026-09/00001.warc.gz@0+4096",
			URL:             "https://vnexpress.net/thoi-su/bai-viet.html",
			Host:            "vnexpress.net",
			FetchedAt:       time.Date(2026, 9, 14, 3, 22, 11, 0, time.UTC),
			MediaType:       "text/html",
			Extractor:       "go-trafilatura@1.4.0",
			PipelineVersion: "0.1.0",
		},
		Language:  doc.Language{Lang: "vie", LangScore: 0.997, Diacritics: "present"},
		Licensing: doc.Licensing{LicenseClass: doc.LicenseOpen, LicenseEvidence: "robots allow, no TDM reservation"},
	}
	d.DocID = doc.SumString(d.Text)
	d.NChars = uint32(len([]rune(d.Text)))
	if err := d.Admit(); err != nil {
		t.Fatalf("the fixture does not satisfy the ingest contract: %v", err)
	}
	return d
}

func TestKhoVerifyAcceptsAGoodSnapshot(t *testing.T) {
	dir, pub := buildSnapshot(t)

	out, _, code := exec(t, "kho", "verify", "-key", pub, dir)
	if code != 0 {
		t.Fatalf("gao kho verify: exit %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"snapshot 2026-09", "40", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

func TestKhoVerifyFailsOnACorruptedShard(t *testing.T) {
	dir, pub := buildSnapshot(t)
	m, err := kho.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, m.Shards[0].Name)
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0x01
	if err := os.WriteFile(target, b, 0o644); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := exec(t, "kho", "verify", "-key", pub, dir)
	if code != 1 {
		t.Fatalf("gao kho verify on a corrupted snapshot: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, m.Shards[0].Name) {
		t.Errorf("the error does not name the bad shard:\n%s", errOut)
	}
}

func TestKhoVerifyRejectsTheWrongKey(t *testing.T) {
	dir, _ := buildSnapshot(t)
	other, _, err := kho.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	_, errOut, code := exec(t, "kho", "verify", "-key", fmt.Sprintf("%x", other), dir)
	if code != 1 {
		t.Fatalf("gao kho verify with the wrong key: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "signature") {
		t.Errorf("the error does not mention the signature:\n%s", errOut)
	}
}

func TestKhoVerifyTakesAKeyFile(t *testing.T) {
	dir, pub := buildSnapshot(t)
	path := filepath.Join(t.TempDir(), "gao.pub")
	if err := os.WriteFile(path, []byte(pub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := exec(t, "kho", "verify", "-key", path, dir); code != 0 {
		t.Fatalf("gao kho verify with a key file: exit %d, want 0\n%s", code, errOut)
	}
}

func TestKhoVerifyQuickSaysWhatItDidNotCheck(t *testing.T) {
	dir, pub := buildSnapshot(t)
	out, _, code := exec(t, "kho", "verify", "-quick", "-key", pub, dir)
	if code != 0 {
		t.Fatalf("gao kho verify -quick: exit %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "not checked") {
		t.Errorf("a quick verification did not say the bytes went unchecked:\n%s", out)
	}
}

func TestKhoVerifyVerboseListsEveryShard(t *testing.T) {
	dir, pub := buildSnapshot(t)
	m, err := kho.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	out, _, code := exec(t, "kho", "verify", "-v", "-key", pub, dir)
	if code != 0 {
		t.Fatalf("gao kho verify -v: exit %d, want 0\n%s", code, out)
	}
	for _, s := range m.Shards {
		if !strings.Contains(out, s.Name) {
			t.Errorf("-v did not list %s:\n%s", s.Name, out)
		}
	}
}

func TestKhoKeygenWritesAUsablePair(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "keys", "gao")
	out, errOut, code := exec(t, "kho", "keygen", "-out", prefix)
	if code != 0 {
		t.Fatalf("gao kho keygen: exit %d, want 0\n%s", code, errOut)
	}
	if !strings.Contains(out, "public key ") {
		t.Errorf("keygen did not print the public key:\n%s", out)
	}

	priv, err := kho.LoadPrivateKey(prefix + ".key")
	if err != nil {
		t.Fatalf("the generated private key does not load: %v", err)
	}
	pub, err := kho.LoadPublicKey(prefix + ".pub")
	if err != nil {
		t.Fatalf("the generated public key does not load: %v", err)
	}
	if !pub.Equal(priv.Public()) {
		t.Fatal("the two files are not a pair")
	}

	// A second run must not quietly replace the key every published snapshot was
	// signed with.
	if _, _, code := exec(t, "kho", "keygen", "-out", prefix); code == 0 {
		t.Fatal("gao kho keygen overwrote an existing key")
	}
	again, err := kho.LoadPrivateKey(prefix + ".key")
	if err != nil {
		t.Fatal(err)
	}
	if !again.Equal(priv) {
		t.Fatal("the original signing key was replaced")
	}
}

func TestKhoUsageErrors(t *testing.T) {
	cases := [][]string{
		{"kho"},
		{"kho", "polish"},
		{"kho", "verify"},
		{"kho", "verify", "one", "two"},
		{"kho", "keygen", "surprise"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, _, code := exec(t, args...); code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
		})
	}
}

func TestKhoHelpIsNotAnError(t *testing.T) {
	out, _, code := exec(t, "kho", "help")
	if code != 0 {
		t.Fatalf("gao kho help: exit %d, want 0", code)
	}
	for _, want := range []string{"verify", "keygen"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help does not mention %s:\n%s", want, out)
		}
	}
}
