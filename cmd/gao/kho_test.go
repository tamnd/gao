package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
	"github.com/tamnd/gao/may"
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

func TestKhoDatasetsPrintsEveryRepoAndHowToReadIt(t *testing.T) {
	out, _, code := exec(t, "kho", "datasets")
	if code != 0 {
		t.Fatalf("gao kho datasets: exit %d, want 0", code)
	}
	if !strings.Contains(out, kho.HubStore) {
		t.Error("gao kho datasets did not print the store of record")
	}
	for _, d := range kho.Datasets() {
		if !strings.Contains(out, d.Repo()) {
			t.Errorf("gao kho datasets did not print %s", d.Repo())
		}
		if !strings.Contains(out, d.Holds) {
			t.Errorf("gao kho datasets did not say what is in %s", d.Name)
		}
		// A working repo is private, so printing a query that reads it would be
		// printing a query that fails for everybody except us.
		q := d.Query("gao-v1.0")
		if d.Public() != strings.Contains(out, q) {
			t.Errorf("gao kho datasets printed the wrong thing for %s, which is %s", d.Name, d.Tier)
		}
	}
}

func TestKhoDatasetsTakesASnapshot(t *testing.T) {
	out, _, code := exec(t, "kho", "datasets", "-snapshot", "gao-v0.2")
	if code != 0 {
		t.Fatalf("gao kho datasets -snapshot: exit %d, want 0", code)
	}
	if !strings.Contains(out, "snapshot=gao-v0.2") {
		t.Error("gao kho datasets ignored the snapshot it was given")
	}
	if strings.Contains(out, "snapshot=gao-v1.0") {
		t.Error("gao kho datasets printed the default snapshot as well as the one it was given")
	}
}

// The store of record is a decision and GAO_STORE is where a run overrides it,
// so a run pointed somewhere else has to say so rather than printing the
// decision and writing elsewhere.
func TestKhoDatasetsSaysWhenTheRunIsPointedElsewhere(t *testing.T) {
	t.Setenv(may.StoreEnv, "file:///mnt/gao")
	out, _, code := exec(t, "kho", "datasets")
	if code != 0 {
		t.Fatalf("gao kho datasets: exit %d, want 0", code)
	}
	if !strings.Contains(out, "file:///mnt/gao") {
		t.Error("gao kho datasets did not print the store this run is actually pointed at")
	}
}

func TestKhoDatasetsTakesNoArguments(t *testing.T) {
	if _, _, code := exec(t, "kho", "datasets", "extra"); code != 2 {
		t.Errorf("gao kho datasets extra: exit %d, want 2", code)
	}
}

func TestKhoColumnsPrintsTheContract(t *testing.T) {
	out, _, code := exec(t, "kho", "columns")
	if code != 0 {
		t.Fatalf("gao kho columns: exit %d, want 0", code)
	}
	d, ok := kho.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the default dataset is not in the table")
	}
	if !strings.Contains(out, d.Repo()) {
		t.Error("gao kho columns did not say which repo it printed")
	}
	for _, c := range kho.Columns(kho.SchemaFor(d)) {
		if !strings.Contains(out, c) {
			t.Errorf("gao kho columns left out %s", c)
		}
	}
}

// The point of the flag is that the difference between a repo that carries text
// and one that withholds it is visible without downloading a file.
func TestKhoColumnsShowsTheWithheldText(t *testing.T) {
	out, _, code := exec(t, "kho", "columns", "-dataset", "vietnamese-web-urls")
	if code != 0 {
		t.Fatalf("gao kho columns -dataset: exit %d, want 0", code)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == kho.TextColumn {
			t.Error("gao kho columns listed text for a repo that withholds it")
		}
	}
	if !strings.Contains(out, "absent and not empty") {
		t.Error("gao kho columns did not say why the column is missing")
	}
	if !strings.Contains(out, "url") {
		t.Error("gao kho columns printed no columns at all")
	}
}

func TestKhoColumnsRefusesADatasetThatIsNotOne(t *testing.T) {
	_, errOut, code := exec(t, "kho", "columns", "-dataset", "vietnamese-everything")
	if code != 1 {
		t.Fatalf("gao kho columns -dataset vietnamese-everything: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "vietnamese-everything") {
		t.Error("the error does not name the dataset that was asked for")
	}
	if !strings.Contains(errOut, "gao kho datasets") {
		t.Error("the error does not say where the list of real ones is")
	}
}

// Reading the file rather than the build is the whole point when the file is
// one somebody downloaded a year ago.
func TestKhoColumnsReadsAFile(t *testing.T) {
	d, ok := kho.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the dataset is not in the table")
	}
	dir := t.TempDir()
	part, err := kho.CreatePart(dir, "part-00000", d, kho.Stamp{
		Snapshot: "gao-v1.0", Stage: "test@0.1.0", Box: "server1",
	})
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if err := part.Append(document(t, 0)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	file, err := part.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// PartFile.Path is the path inside the repo, so a reader on disk joins it
	// with the directory the part was written under.
	out, errOut, code := exec(t, "kho", "columns", filepath.Join(dir, file.Path))
	if code != 0 {
		t.Fatalf("gao kho columns FILE: exit %d, want 0: %s", code, errOut)
	}
	for _, want := range []string{"gao-v1.0", "test@0.1.0", "server1", "doc_id", kho.TextColumn} {
		if !strings.Contains(out, want) {
			t.Errorf("gao kho columns FILE did not print %s", want)
		}
	}
}

func TestKhoColumnsRefusesAFileThatIsNotOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-part.parquet")
	if err := os.WriteFile(path, []byte("this is not parquet"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := exec(t, "kho", "columns", path); code != 1 {
		t.Errorf("gao kho columns on a file that is not parquet: exit %d, want 1", code)
	}
}

func TestKhoColumnsTakesOneFileAtMost(t *testing.T) {
	if _, _, code := exec(t, "kho", "columns", "a.parquet", "b.parquet"); code != 2 {
		t.Error("gao kho columns took two files")
	}
}

// A push from the command line is how a part that an interrupted run left on a
// disk gets off it, and how the files that are not parts get up there.
func TestKhoPushSendsAFileAndSaysWhatItDid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/resolve/"):
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "/preupload/"):
			fmt.Fprint(w, `{"files":[{"uploadMode":"regular"}]}`)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	t.Setenv(may.StoreEnv, srv.URL)

	path := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(path, []byte("# vietnamese-text-staging\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := exec(t, "kho", "push", "-as", "README.md", path)
	if code != 0 {
		t.Fatalf("gao kho push: exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, kho.Staging().Repo()) {
		t.Errorf("the push does not say where the file went:\n%s", out)
	}
	if !strings.Contains(out, "pushed") {
		t.Errorf("the push does not say what it did:\n%s", out)
	}
}

// The second push of a file already up there should say so rather than report
// an upload that did not happen, because on a box being cleaned up the
// difference is the whole question.
func TestKhoPushSaysWhenThereIsNothingToDo(t *testing.T) {
	body := []byte("already there\n")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/resolve/") {
			w.Header().Set("X-Linked-Etag", `"`+hex.EncodeToString(sum[:])+`"`)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv(may.StoreEnv, srv.URL)

	path := filepath.Join(t.TempDir(), "part-00000.parquet")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := exec(t, "kho", "push", "-as", "data/x.parquet", path)
	if code != 0 {
		t.Fatalf("gao kho push: exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "nothing moved") {
		t.Errorf("a push with nothing to do does not say so:\n%s", out)
	}
}

func TestKhoPushRefusesADatasetThatIsNotThere(t *testing.T) {
	_, errOut, code := exec(t, "kho", "push", "-dataset", "vietnamese-nonsense", "x")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao kho datasets") {
		t.Errorf("the error does not say how to find the list:\n%s", errOut)
	}
}

func TestKhoPushIsInTheSubcommandList(t *testing.T) {
	out, _, code := exec(t, "kho", "help")
	if code != 0 {
		t.Fatalf("gao kho help: exit %d", code)
	}
	if !strings.Contains(out, "push") {
		t.Errorf("the subcommand list does not mention push:\n%s", out)
	}
}

// A card printed rather than pushed is what somebody reads before a release,
// which is why the default is to print it.
func TestKhoCardPrintsTheCardForADataset(t *testing.T) {
	out, errOut, code := exec(t, "kho", "card", "-dataset", "vietnamese-web-text")
	if code != 0 {
		t.Fatalf("gao kho card: exit %d\n%s", code, errOut)
	}
	if !strings.HasPrefix(out, "---\n") {
		t.Errorf("the card has no front matter:\n%s", out)
	}
	if !strings.Contains(out, "# Vietnamese Web Text") {
		t.Errorf("the card has no title:\n%s", out)
	}
}

func TestKhoCardReadsTheCountsFromASnapshotManifest(t *testing.T) {
	dir := t.TempDir()
	m := &kho.Manifest{
		ManifestVersion: kho.ManifestVersion,
		SchemaVersion:   doc.SchemaVersion,
		Snapshot:        "2026-09",
		CreatedAt:       time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Pipeline:        "0.4.1",
		Counts:          kho.Counts{Documents: 7, Bytes: 70, Chars: 700},
		Shards: []kho.Shard{
			{Name: "part-00000.parquet", Index: 0, Documents: 7, Bytes: 70, Hash: doc.SumString("x")},
		},
	}
	m.Root = m.ComputeRoot()
	if err := kho.WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	out, errOut, code := exec(t, "kho", "card", "-dataset", "vietnamese-web-text", "-from", dir)
	if code != 0 {
		t.Fatalf("gao kho card -from: exit %d\n%s", code, errOut)
	}
	for _, want := range []string{"| documents | 7 |", "snapshot=2026-09", "not a release"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card does not carry %q:\n%s", want, out)
		}
	}
}

func TestKhoCardPushesTheCardAndSaysWhereItWent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/resolve/"):
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "/preupload/"):
			fmt.Fprint(w, `{"files":[{"uploadMode":"regular"}]}`)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	t.Setenv(may.StoreEnv, srv.URL)

	out, errOut, code := exec(t, "kho", "card", "-dataset", kho.StageRepo, "-push")
	if code != 0 {
		t.Fatalf("gao kho card -push: exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "pushed the card") || !strings.Contains(out, kho.Staging().Repo()) {
		t.Errorf("the push does not say what it did or where:\n%s", out)
	}
}

func TestKhoCardRefusesADatasetThatIsNotThere(t *testing.T) {
	_, errOut, code := exec(t, "kho", "card", "-dataset", "vietnamese-nonsense")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao kho datasets") {
		t.Errorf("the error does not say how to find the list:\n%s", errOut)
	}
}

// Without a dataset there is nothing to generate a card for, and guessing one
// would be putting the wrong repo's card on somebody's screen.
func TestKhoCardNeedsADataset(t *testing.T) {
	if _, _, code := exec(t, "kho", "card"); code != 2 {
		t.Error("gao kho card ran without a dataset")
	}
}

func TestKhoCardIsInTheSubcommandList(t *testing.T) {
	out, _, code := exec(t, "kho", "help")
	if code != 0 {
		t.Fatalf("gao kho help: exit %d", code)
	}
	if !strings.Contains(out, "card") {
		t.Errorf("the subcommand list does not mention card:\n%s", out)
	}
}
