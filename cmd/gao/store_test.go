package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/store"
)

// buildSnapshot writes a small signed snapshot and returns the directory and the
// public key it was signed with, in hex.
func buildSnapshot(t *testing.T) (dir, pub string) {
	t.Helper()
	dir = t.TempDir()

	set, err := store.NewShardSet[*doc.Document](dir, 4, func(d *doc.Document) doc.Hash { return d.DocID })
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

	m := &store.Manifest{
		Snapshot:  "2026-09",
		CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		Pipeline:  "0.1.0",
		Box:       "server1",
		Stages:    []store.Stage{{Name: "harvest@0.1.0", ConfigHash: doc.SumString("harvest config")}},
		Shards:    shards,
	}
	for _, s := range shards {
		m.Counts.Documents += int64(s.Documents)
		m.Counts.Bytes += s.Bytes
	}
	m.Counts.Natural = m.Counts.Documents

	_, priv, err := store.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Seal(priv, m.CreatedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := store.WriteManifest(dir, m); err != nil {
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

func TestStoreVerifyAcceptsAGoodSnapshot(t *testing.T) {
	dir, pub := buildSnapshot(t)

	out, _, code := exec(t, "store", "verify", "-key", pub, dir)
	if code != 0 {
		t.Fatalf("gao store verify: exit %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"snapshot 2026-09", "40", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

func TestStoreVerifyFailsOnACorruptedShard(t *testing.T) {
	dir, pub := buildSnapshot(t)
	m, err := store.ReadManifest(dir)
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

	_, errOut, code := exec(t, "store", "verify", "-key", pub, dir)
	if code != 1 {
		t.Fatalf("gao store verify on a corrupted snapshot: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, m.Shards[0].Name) {
		t.Errorf("the error does not name the bad shard:\n%s", errOut)
	}
}

func TestStoreVerifyRejectsTheWrongKey(t *testing.T) {
	dir, _ := buildSnapshot(t)
	other, _, err := store.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	_, errOut, code := exec(t, "store", "verify", "-key", fmt.Sprintf("%x", other), dir)
	if code != 1 {
		t.Fatalf("gao store verify with the wrong key: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "signature") {
		t.Errorf("the error does not mention the signature:\n%s", errOut)
	}
}

func TestStoreVerifyTakesAKeyFile(t *testing.T) {
	dir, pub := buildSnapshot(t)
	path := filepath.Join(t.TempDir(), "gao.pub")
	if err := os.WriteFile(path, []byte(pub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := exec(t, "store", "verify", "-key", path, dir); code != 0 {
		t.Fatalf("gao store verify with a key file: exit %d, want 0\n%s", code, errOut)
	}
}

func TestStoreVerifyQuickSaysWhatItDidNotCheck(t *testing.T) {
	dir, pub := buildSnapshot(t)
	out, _, code := exec(t, "store", "verify", "-quick", "-key", pub, dir)
	if code != 0 {
		t.Fatalf("gao store verify -quick: exit %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "not checked") {
		t.Errorf("a quick verification did not say the bytes went unchecked:\n%s", out)
	}
}

func TestStoreVerifyVerboseListsEveryShard(t *testing.T) {
	dir, pub := buildSnapshot(t)
	m, err := store.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	out, _, code := exec(t, "store", "verify", "-v", "-key", pub, dir)
	if code != 0 {
		t.Fatalf("gao store verify -v: exit %d, want 0\n%s", code, out)
	}
	for _, s := range m.Shards {
		if !strings.Contains(out, s.Name) {
			t.Errorf("-v did not list %s:\n%s", s.Name, out)
		}
	}
}

func TestStoreKeygenWritesAUsablePair(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "keys", "gao")
	out, errOut, code := exec(t, "store", "keygen", "-out", prefix)
	if code != 0 {
		t.Fatalf("gao store keygen: exit %d, want 0\n%s", code, errOut)
	}
	if !strings.Contains(out, "public key ") {
		t.Errorf("keygen did not print the public key:\n%s", out)
	}

	priv, err := store.LoadPrivateKey(prefix + ".key")
	if err != nil {
		t.Fatalf("the generated private key does not load: %v", err)
	}
	pub, err := store.LoadPublicKey(prefix + ".pub")
	if err != nil {
		t.Fatalf("the generated public key does not load: %v", err)
	}
	if !pub.Equal(priv.Public()) {
		t.Fatal("the two files are not a pair")
	}

	// A second run must not quietly replace the key every published snapshot was
	// signed with.
	if _, _, code := exec(t, "store", "keygen", "-out", prefix); code == 0 {
		t.Fatal("gao store keygen overwrote an existing key")
	}
	again, err := store.LoadPrivateKey(prefix + ".key")
	if err != nil {
		t.Fatal(err)
	}
	if !again.Equal(priv) {
		t.Fatal("the original signing key was replaced")
	}
}

func TestStoreUsageErrors(t *testing.T) {
	cases := [][]string{
		{"store"},
		{"store", "polish"},
		{"store", "verify"},
		{"store", "verify", "one", "two"},
		{"store", "keygen", "surprise"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, _, code := exec(t, args...); code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
		})
	}
}

func TestStoreHelpIsNotAnError(t *testing.T) {
	out, _, code := exec(t, "store", "help")
	if code != 0 {
		t.Fatalf("gao store help: exit %d, want 0", code)
	}
	for _, want := range []string{"verify", "keygen"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help does not mention %s:\n%s", want, out)
		}
	}
}

func TestStoreDatasetsPrintsEveryRepoAndHowToReadIt(t *testing.T) {
	out, _, code := exec(t, "store", "datasets")
	if code != 0 {
		t.Fatalf("gao store datasets: exit %d, want 0", code)
	}
	if !strings.Contains(out, store.HubStore) {
		t.Error("gao store datasets did not print the store of record")
	}
	for _, d := range store.Datasets() {
		if !strings.Contains(out, d.Repo()) {
			t.Errorf("gao store datasets did not print %s", d.Repo())
		}
		if !strings.Contains(out, d.Holds) {
			t.Errorf("gao store datasets did not say what is in %s", d.Name)
		}
		// Every repo is public, so every repo gets the line somebody pastes to
		// read it.
		if q := d.Query("gao-v1.0"); !strings.Contains(out, q) {
			t.Errorf("gao store datasets printed no way to read %s, which is %s", d.Name, d.Tier)
		}
	}
}

func TestStoreDatasetsTakesASnapshot(t *testing.T) {
	out, _, code := exec(t, "store", "datasets", "-snapshot", "gao-v0.2")
	if code != 0 {
		t.Fatalf("gao store datasets -snapshot: exit %d, want 0", code)
	}
	if !strings.Contains(out, "/gao-v0.2/") {
		t.Error("gao store datasets ignored the snapshot it was given")
	}
	if strings.Contains(out, "/gao-v1.0/") {
		t.Error("gao store datasets printed the default snapshot as well as the one it was given")
	}
}

// The store of record is a decision and GAO_STORE is where a run overrides it,
// so a run pointed somewhere else has to say so rather than printing the
// decision and writing elsewhere.
func TestStoreDatasetsSaysWhenTheRunIsPointedElsewhere(t *testing.T) {
	t.Setenv(fleet.StoreEnv, "file:///mnt/gao")
	out, _, code := exec(t, "store", "datasets")
	if code != 0 {
		t.Fatalf("gao store datasets: exit %d, want 0", code)
	}
	if !strings.Contains(out, "file:///mnt/gao") {
		t.Error("gao store datasets did not print the store this run is actually pointed at")
	}
}

func TestStoreDatasetsTakesNoArguments(t *testing.T) {
	if _, _, code := exec(t, "store", "datasets", "extra"); code != 2 {
		t.Errorf("gao store datasets extra: exit %d, want 2", code)
	}
}

func TestStoreColumnsPrintsTheContract(t *testing.T) {
	out, _, code := exec(t, "store", "columns")
	if code != 0 {
		t.Fatalf("gao store columns: exit %d, want 0", code)
	}
	d, ok := store.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the default dataset is not in the table")
	}
	if !strings.Contains(out, d.Repo()) {
		t.Error("gao store columns did not say which repo it printed")
	}
	for _, c := range store.Columns(store.SchemaFor(d)) {
		if !strings.Contains(out, c) {
			t.Errorf("gao store columns left out %s", c)
		}
	}
}

// The point of the flag is that the difference between a repo that carries text
// and one that withholds it is visible without downloading a file.
func TestStoreColumnsShowsTheWithheldText(t *testing.T) {
	out, _, code := exec(t, "store", "columns", "-dataset", "vietnamese-web-urls")
	if code != 0 {
		t.Fatalf("gao store columns -dataset: exit %d, want 0", code)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == store.TextColumn {
			t.Error("gao store columns listed text for a repo that withholds it")
		}
	}
	if !strings.Contains(out, "absent and not empty") {
		t.Error("gao store columns did not say why the column is missing")
	}
	if !strings.Contains(out, "url") {
		t.Error("gao store columns printed no columns at all")
	}
}

func TestStoreSchemaExplainsEveryColumn(t *testing.T) {
	out, _, code := exec(t, "store", "schema")
	if code != 0 {
		t.Fatalf("gao store schema: exit %d, want 0", code)
	}
	for _, c := range store.Schema() {
		if !strings.Contains(out, c.Name) {
			t.Errorf("gao store schema left out %s", c.Name)
		}
		if !strings.Contains(out, c.Meaning) {
			t.Errorf("gao store schema printed %s with nothing about what it means", c.Name)
		}
	}
	// The fields inside pii_spans are columns somebody has to read too.
	if !strings.Contains(out, "pii_spans.start") {
		t.Error("gao store schema stopped at the top level")
	}
}

func TestStoreSchemaPrintsWhatParquetSees(t *testing.T) {
	out, _, code := exec(t, "store", "schema", "-parquet")
	if code != 0 {
		t.Fatalf("gao store schema -parquet: exit %d, want 0", code)
	}
	if !strings.HasPrefix(out, "message document {") {
		t.Errorf("the definition does not start where a parquet tool would print it:\n%s", out)
	}

	// A repo that withholds text has no text column, and the definition is
	// where that is easiest to check.
	out, _, code = exec(t, "store", "schema", "-parquet", "-dataset", "vietnamese-web-urls")
	if code != 0 {
		t.Fatalf("gao store schema -parquet -dataset: exit %d, want 0", code)
	}
	if strings.Contains(out, " text (STRING)") {
		t.Error("the definition for a repo that withholds text has a text column in it")
	}
}

// The page on the web and the page the binary prints are the same page, and a
// reader who compares them and finds a difference stops trusting both.
func TestStoreSchemaPrintsThePageThatShips(t *testing.T) {
	out, _, code := exec(t, "store", "schema", "-md")
	if code != 0 {
		t.Fatalf("gao store schema -md: exit %d, want 0", code)
	}
	shipped, err := os.ReadFile(filepath.Join("..", "..", "SCHEMA.md"))
	if err != nil {
		t.Fatalf("reading SCHEMA.md: %v", err)
	}
	if out != string(shipped) {
		t.Error("gao store schema -md and SCHEMA.md are not the same page, run `make schema`")
	}
}

func TestStoreSchemaRefusesTwoOutputsAtOnce(t *testing.T) {
	if _, _, code := exec(t, "store", "schema", "-md", "-parquet"); code != 2 {
		t.Error("gao store schema -md -parquet did not say it could only do one")
	}
	if _, _, code := exec(t, "store", "schema", "extra"); code != 2 {
		t.Error("gao store schema takes no arguments and accepted one")
	}
}

func TestStoreColumnsRefusesADatasetThatIsNotOne(t *testing.T) {
	_, errOut, code := exec(t, "store", "columns", "-dataset", "vietnamese-everything")
	if code != 1 {
		t.Fatalf("gao store columns -dataset vietnamese-everything: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "vietnamese-everything") {
		t.Error("the error does not name the dataset that was asked for")
	}
	if !strings.Contains(errOut, "gao store datasets") {
		t.Error("the error does not say where the list of real ones is")
	}
}

// Reading the file rather than the build is the whole point when the file is
// one somebody downloaded a year ago.
func TestStoreColumnsReadsAFile(t *testing.T) {
	d, ok := store.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the dataset is not in the table")
	}
	dir := t.TempDir()
	part, err := store.CreatePart(dir, "part-00000", d, store.Stamp{
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
	out, errOut, code := exec(t, "store", "columns", filepath.Join(dir, file.Path))
	if code != 0 {
		t.Fatalf("gao store columns FILE: exit %d, want 0: %s", code, errOut)
	}
	for _, want := range []string{"gao-v1.0", "test@0.1.0", "server1", "doc_id", store.TextColumn} {
		if !strings.Contains(out, want) {
			t.Errorf("gao store columns FILE did not print %s", want)
		}
	}
}

// A part written without a tokenizer carries a token column of zeros, and the
// reader has to be told that rather than left to sum it.
func TestStoreColumnsSaysWhenNothingCountedTheTokens(t *testing.T) {
	d, _ := store.Lookup("vietnamese-web-text")
	dir := t.TempDir()
	part, err := store.CreatePart(dir, "part-00000", d, store.Stamp{
		Snapshot: "glotcc-9ad140b6be3a", Stage: "harvest@0.1.0", Box: "server3",
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

	out, errOut, code := exec(t, "store", "columns", filepath.Join(dir, file.Path))
	if code != 0 {
		t.Fatalf("gao store columns FILE: exit %d, want 0: %s", code, errOut)
	}
	if !strings.Contains(out, "no tokenizer ran") {
		t.Errorf("a part nothing counted did not say so:\n%s", out)
	}
	if !strings.Contains(out, "gao.tokenizer      none") {
		t.Errorf("the metadata does not carry an explicit empty tokenizer:\n%s", out)
	}
}

func TestStoreColumnsRefusesAFileThatIsNotOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-part.parquet")
	if err := os.WriteFile(path, []byte("this is not parquet"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := exec(t, "store", "columns", path); code != 1 {
		t.Errorf("gao store columns on a file that is not parquet: exit %d, want 1", code)
	}
}

func TestStoreColumnsTakesOneFileAtMost(t *testing.T) {
	if _, _, code := exec(t, "store", "columns", "a.parquet", "b.parquet"); code != 2 {
		t.Error("gao store columns took two files")
	}
}

// A push from the command line is how a part that an interrupted run left on a
// disk gets off it, and how the files that are not parts get up there.
func TestStorePushSendsAFileAndSaysWhatItDid(t *testing.T) {
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
	t.Setenv(fleet.StoreEnv, srv.URL)

	path := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(path, []byte("# vietnamese-source-text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := exec(t, "store", "push", "-as", "README.md", path)
	if code != 0 {
		t.Fatalf("gao store push: exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, store.Staging().Repo()) {
		t.Errorf("the push does not say where the file went:\n%s", out)
	}
	if !strings.Contains(out, "pushed") {
		t.Errorf("the push does not say what it did:\n%s", out)
	}
}

// The second push of a file already up there should say so rather than report
// an upload that did not happen, because on a box being cleaned up the
// difference is the whole question.
func TestStorePushSaysWhenThereIsNothingToDo(t *testing.T) {
	body := []byte("already there\n")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/resolve/") {
			w.Header().Set("X-Linked-Etag", `"`+hex.EncodeToString(sum[:])+`"`)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv(fleet.StoreEnv, srv.URL)

	path := filepath.Join(t.TempDir(), "part-00000.parquet")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := exec(t, "store", "push", "-as", "data/x.parquet", path)
	if code != 0 {
		t.Fatalf("gao store push: exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "nothing moved") {
		t.Errorf("a push with nothing to do does not say so:\n%s", out)
	}
}

func TestStorePushRefusesADatasetThatIsNotThere(t *testing.T) {
	_, errOut, code := exec(t, "store", "push", "-dataset", "vietnamese-nonsense", "x")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao store datasets") {
		t.Errorf("the error does not say how to find the list:\n%s", errOut)
	}
}

func TestStorePushIsInTheSubcommandList(t *testing.T) {
	out, _, code := exec(t, "store", "help")
	if code != 0 {
		t.Fatalf("gao store help: exit %d", code)
	}
	if !strings.Contains(out, "push") {
		t.Errorf("the subcommand list does not mention push:\n%s", out)
	}
}

// A card printed rather than pushed is what somebody reads before a release,
// which is why the default is to print it.
func TestStoreCardPrintsTheCardForADataset(t *testing.T) {
	out, errOut, code := exec(t, "store", "card", "-dataset", "vietnamese-web-text")
	if code != 0 {
		t.Fatalf("gao store card: exit %d\n%s", code, errOut)
	}
	if !strings.HasPrefix(out, "---\n") {
		t.Errorf("the card has no front matter:\n%s", out)
	}
	if !strings.Contains(out, "# Vietnamese Web Text") {
		t.Errorf("the card has no title:\n%s", out)
	}
}

func TestStoreCardReadsTheCountsFromASnapshotManifest(t *testing.T) {
	dir := t.TempDir()
	m := &store.Manifest{
		ManifestVersion: store.ManifestVersion,
		SchemaVersion:   doc.SchemaVersion,
		Snapshot:        "2026-09",
		CreatedAt:       time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Pipeline:        "0.4.1",
		Counts:          store.Counts{Documents: 7, Bytes: 70, Chars: 700},
		Shards: []store.Shard{
			{Name: "part-00000.parquet", Index: 0, Documents: 7, Bytes: 70, Hash: doc.SumString("x")},
		},
	}
	m.Root = m.ComputeRoot()
	if err := store.WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	out, errOut, code := exec(t, "store", "card", "-dataset", "vietnamese-web-text", "-from", dir)
	if code != 0 {
		t.Fatalf("gao store card -from: exit %d\n%s", code, errOut)
	}
	for _, want := range []string{"| documents | 7 |", "data/2026-09/", "not a release"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card does not carry %q:\n%s", want, out)
		}
	}
}

func TestStoreCardPushesTheCardAndSaysWhereItWent(t *testing.T) {
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
	t.Setenv(fleet.StoreEnv, srv.URL)

	out, errOut, code := exec(t, "store", "card", "-dataset", store.StageRepo, "-push")
	if code != 0 {
		t.Fatalf("gao store card -push: exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "pushed the card") || !strings.Contains(out, store.Staging().Repo()) {
		t.Errorf("the push does not say what it did or where:\n%s", out)
	}
}

func TestStoreCardRefusesADatasetThatIsNotThere(t *testing.T) {
	_, errOut, code := exec(t, "store", "card", "-dataset", "vietnamese-nonsense")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao store datasets") {
		t.Errorf("the error does not say how to find the list:\n%s", errOut)
	}
}

// Without a dataset there is nothing to generate a card for, and guessing one
// would be putting the wrong repo's card on somebody's screen.
func TestStoreCardNeedsADataset(t *testing.T) {
	if _, _, code := exec(t, "store", "card"); code != 2 {
		t.Error("gao store card ran without a dataset")
	}
}

func TestStoreCardIsInTheSubcommandList(t *testing.T) {
	out, _, code := exec(t, "store", "help")
	if code != 0 {
		t.Fatalf("gao store help: exit %d", code)
	}
	if !strings.Contains(out, "card") {
		t.Errorf("the subcommand list does not mention card:\n%s", out)
	}
}

// removableSnapshot writes a signed snapshot with a complete counts block, and
// the signing key beside it, which is what a removal needs and a verify does
// not. The counts are complete because a removal arrives at the child's by
// subtracting from the parent's, so a fixture with zeros in it would produce a
// snapshot claiming a negative number of characters.
func removableSnapshot(t *testing.T, n, shards int) (dir, keyPath string, docs []*doc.Document) {
	t.Helper()
	dir = t.TempDir()

	set, err := store.NewShardSet[*doc.Document](dir, shards, func(d *doc.Document) doc.Hash { return d.DocID })
	if err != nil {
		t.Fatalf("NewShardSet: %v", err)
	}
	m := &store.Manifest{
		Snapshot:  "2026-09",
		CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		Pipeline:  "0.1.0",
		Box:       "server1",
		Stages:    []store.Stage{{Name: "harvest@0.1.0", ConfigHash: doc.SumString("harvest config")}},
	}
	m.Counts.BySource = map[string]int64{}
	for i := range n {
		d := document(t, i)
		if err := set.Append(d); err != nil {
			t.Fatalf("Append: %v", err)
		}
		docs = append(docs, d)
		m.Counts.Documents++
		m.Counts.Natural++
		m.Counts.Bytes += int64(len(d.Text))
		m.Counts.Chars += int64(d.NChars)
		m.Counts.BySource[string(d.Source)]++
	}
	shardRecs, err := set.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	m.Shards = shardRecs

	_, priv, err := store.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Seal(priv, m.CreatedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := store.WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	keyPath = filepath.Join(t.TempDir(), "gao.key")
	if err := store.WritePrivateKey(keyPath, priv); err != nil {
		t.Fatal(err)
	}
	return dir, keyPath, docs
}

func TestStoreRemoveTakesADocumentOutAndSaysWhatItCost(t *testing.T) {
	src, key, docs := removableSnapshot(t, 40, 4)
	dst := filepath.Join(t.TempDir(), "2026-09-r1")

	out, errOut, code := exec(t, "store", "remove",
		"-from", src, "-to", dst, "-snapshot", "2026-09-r1",
		"-key", key, "-reason", "takedown", "-note", "request 118",
		docs[7].DocID.String())
	if code != 0 {
		t.Fatalf("gao store remove: exit %d, want 0\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"2026-09-r1", "parent    2026-09", "removed   1", "39 remain", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
	// A removal that touched one shard is supposed to say that it left the other
	// three alone, because that is the difference between a takedown answered in
	// minutes and one answered tomorrow.
	if !strings.Contains(out, "1 rewritten, 3 copied") {
		t.Errorf("output does not account for the shards it did not rewrite:\n%s", out)
	}
}

func TestStoreRemoveLeavesASnapshotThatVerifies(t *testing.T) {
	src, key, docs := removableSnapshot(t, 40, 4)
	dst := filepath.Join(t.TempDir(), "2026-09-r1")

	if _, errOut, code := exec(t, "store", "remove",
		"-from", src, "-to", dst, "-snapshot", "2026-09-r1",
		"-key", key, "-reason", "legal", docs[3].DocID.String()); code != 0 {
		t.Fatalf("gao store remove: exit %d\n%s", code, errOut)
	}

	pub, err := store.LoadPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, code := exec(t, "store", "verify", "-key",
		hex.EncodeToString(pub.Public().(ed25519.PublicKey)), dst)
	if code != 0 {
		t.Fatalf("the removal wrote a snapshot that does not verify: exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "parent    2026-09") {
		t.Errorf("the new snapshot does not name its parent:\n%s", out)
	}
	if !strings.Contains(out, "documents 39") {
		t.Errorf("the new snapshot does not hold 39 documents:\n%s", out)
	}
}

// A request naming four documents of which one is a typo is a request nobody
// has answered yet, and half answering it is worse than not starting, because a
// signed snapshot reads as done.
func TestStoreRemoveWillNotAnswerHalfARequest(t *testing.T) {
	src, key, docs := removableSnapshot(t, 40, 4)
	dst := filepath.Join(t.TempDir(), "2026-09-r1")
	missing := doc.SumString("a document that was never crawled")

	out, errOut, code := exec(t, "store", "remove",
		"-from", src, "-to", dst, "-snapshot", "2026-09-r1",
		"-key", key, "-reason", "takedown",
		docs[1].DocID.String(), missing.String())
	if code != 1 {
		t.Fatalf("gao store remove: exit %d, want 1\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, missing.String()) {
		t.Errorf("the error does not name the identity that was not found:\n%s", errOut)
	}
	if strings.Contains(errOut, docs[1].DocID.String()) {
		t.Errorf("the error names an identity that was found:\n%s", errOut)
	}
	if !strings.Contains(errOut, "nothing was written") {
		t.Errorf("the error does not say the snapshot was not written:\n%s", errOut)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("a removal that could not answer the whole request left a directory behind")
	}
}

func TestStoreRemoveOnAnIdentityThatIsNotThereWritesNothing(t *testing.T) {
	src, key, _ := removableSnapshot(t, 40, 4)
	dst := filepath.Join(t.TempDir(), "2026-09-r1")

	_, errOut, code := exec(t, "store", "remove",
		"-from", src, "-to", dst, "-snapshot", "2026-09-r1",
		"-key", key, "-reason", "takedown",
		doc.SumString("not in the corpus").String())
	if code != 1 {
		t.Fatalf("gao store remove: exit %d, want 1\n%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dst, "manifest.toml")); err == nil {
		t.Fatal("a removal that found nothing still signed a snapshot")
	}
}

func TestStoreRemoveReadsAListOfIdentities(t *testing.T) {
	src, key, docs := removableSnapshot(t, 40, 4)
	dst := filepath.Join(t.TempDir(), "2026-09-r1")

	list := filepath.Join(t.TempDir(), "request-118.txt")
	body := "# request 118, received 2026-10-02\n\n" +
		docs[2].DocID.String() + "  the first article\n" +
		docs[9].DocID.String() + "\n" +
		docs[21].DocID.String() + "  the one with the photograph\n"
	if err := os.WriteFile(list, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := exec(t, "store", "remove",
		"-from", src, "-to", dst, "-snapshot", "2026-09-r1",
		"-key", key, "-reason", "privacy", "-list", list)
	if code != 0 {
		t.Fatalf("gao store remove -list: exit %d, want 0\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "removed   3") {
		t.Errorf("the list was not read as three documents:\n%s", out)
	}
	if !strings.Contains(out, "37 remain") {
		t.Errorf("the counts do not come down by three:\n%s", out)
	}
}

// Running the same takedown twice is what happens when a script is retried, and
// it has to be safe and has to say what it found.
func TestStoreRemoveTwiceReportsTheDocumentAlreadyGone(t *testing.T) {
	src, key, docs := removableSnapshot(t, 40, 4)
	tmp := t.TempDir()
	first, second := filepath.Join(tmp, "r1"), filepath.Join(tmp, "r2")
	id := docs[5].DocID.String()

	if _, errOut, code := exec(t, "store", "remove", "-from", src, "-to", first,
		"-snapshot", "2026-09-r1", "-key", key, "-reason", "takedown", id); code != 0 {
		t.Fatalf("the first removal failed: %s", errOut)
	}
	out, errOut, code := exec(t, "store", "remove", "-from", first, "-to", second,
		"-snapshot", "2026-09-r2", "-key", key, "-reason", "takedown", id)
	if code != 0 {
		t.Fatalf("the second removal failed: exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "already   1") {
		t.Errorf("the rerun does not say the document was already tombstoned:\n%s", out)
	}
}

func TestStoreRemoveVerboseAccountsForEveryShard(t *testing.T) {
	src, key, docs := removableSnapshot(t, 40, 4)
	dst := filepath.Join(t.TempDir(), "2026-09-r1")
	m, err := store.ReadManifest(src)
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, code := exec(t, "store", "remove", "-v",
		"-from", src, "-to", dst, "-snapshot", "2026-09-r1",
		"-key", key, "-reason", "takedown", docs[0].DocID.String())
	if code != 0 {
		t.Fatalf("gao store remove -v: exit %d\n%s", code, errOut)
	}
	for _, s := range m.Shards {
		if !strings.Contains(out, s.Name) {
			t.Errorf("-v did not account for %s:\n%s", s.Name, out)
		}
	}
	if !strings.Contains(out, "rewritten") || !strings.Contains(out, "copied") {
		t.Errorf("-v does not say which shards were rewritten and which were copied:\n%s", out)
	}
}

func TestStoreRemoveWillNotWriteOverASnapshot(t *testing.T) {
	src, key, docs := removableSnapshot(t, 40, 4)
	other, _, _ := removableSnapshot(t, 40, 4)

	_, errOut, code := exec(t, "store", "remove",
		"-from", src, "-to", other, "-snapshot", "2026-09-r1",
		"-key", key, "-reason", "takedown", docs[0].DocID.String())
	if code != 1 {
		t.Fatalf("gao store remove: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "already holds a snapshot") {
		t.Errorf("the error does not say the destination is taken:\n%s", errOut)
	}
}

func TestStoreRemoveWantsAReasonItKnows(t *testing.T) {
	src, key, docs := removableSnapshot(t, 40, 4)
	dst := filepath.Join(t.TempDir(), "2026-09-r1")

	for _, reason := range []string{"", "because"} {
		_, errOut, code := exec(t, "store", "remove",
			"-from", src, "-to", dst, "-snapshot", "2026-09-r1",
			"-key", key, "-reason", reason, docs[0].DocID.String())
		if code != 2 {
			t.Fatalf("gao store remove -reason %q: exit %d, want 2", reason, code)
		}
		for _, want := range store.Reasons() {
			if !strings.Contains(errOut, want) {
				t.Errorf("the error does not offer %q:\n%s", want, errOut)
			}
		}
	}
}

func TestStoreRemoveUsageErrors(t *testing.T) {
	src, key, docs := removableSnapshot(t, 40, 4)
	dst := filepath.Join(t.TempDir(), "2026-09-r1")
	id := docs[0].DocID.String()

	cases := map[string][]string{
		"no source":      {"store", "remove", "-to", dst, "-snapshot", "r1", "-key", key, "-reason", "takedown", id},
		"no destination": {"store", "remove", "-from", src, "-snapshot", "r1", "-key", key, "-reason", "takedown", id},
		"no name":        {"store", "remove", "-from", src, "-to", dst, "-key", key, "-reason", "takedown", id},
		"no key":         {"store", "remove", "-from", src, "-to", dst, "-snapshot", "r1", "-reason", "takedown", id},
		"no documents":   {"store", "remove", "-from", src, "-to", dst, "-snapshot", "r1", "-key", key, "-reason", "takedown"},
		"not an id":      {"store", "remove", "-from", src, "-to", dst, "-snapshot", "r1", "-key", key, "-reason", "takedown", "the third one"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, code := exec(t, args...); code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
		})
	}
}

func TestStoreRemoveIsInTheSubcommandList(t *testing.T) {
	out, _, code := exec(t, "store", "help")
	if code != 0 {
		t.Fatalf("gao store help: exit %d", code)
	}
	if !strings.Contains(out, "remove") {
		t.Errorf("the subcommand list does not mention remove:\n%s", out)
	}
}

// orderLog writes readings of n shards measured both ways, on two boxes, with
// the sorted run saving the fraction given.
func orderLog(t *testing.T, n int, saves float64) string {
	t.Helper()
	var b strings.Builder
	for i := range n {
		box := "server3"
		if i%2 == 1 {
			box = "gamingpc"
		}
		const raw, arrival = 1_500_000_000, 500_000_000
		for _, o := range []struct {
			name string
			size int64
		}{{"arrival", arrival}, {"host", int64(arrival * (1 - saves))}} {
			fmt.Fprintf(&b, `{"shard":"shard-%05d-of-00750","ordering":%q,"level":19,"raw":%d,"compressed":%d,"documents":11800,"hosts":900,"biggest":0.05,"box":%q}`+"\n",
				i, o.name, raw, o.size, box)
		}
	}
	path := filepath.Join(t.TempDir(), "order.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWhatSortingAShardByHostBuysIsReportedAgainstWhatItCosts(t *testing.T) {
	out, errOut, code := exec(t, "store", "order", "-text", "1200000000000", orderLog(t, 3, 0.08))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"saved", "8.0%", "resident", "held in memory while one shard is sorted", "per shard, best first"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}

	// The shard count is downstream of the measured ratio, and the release is
	// shaped like the shard count.
	if !strings.Contains(out, "shards") || !strings.Contains(out, "of text at that ratio") {
		t.Errorf("the shard count is not reported against the corpus size:\n%s", out)
	}
}

func TestASavingThatDoesNotPayForTheMemoryExitsNonzero(t *testing.T) {
	out, _, code := exec(t, "store", "order", orderLog(t, 3, 0.01))
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "is not worth that") {
		t.Errorf("a 1%% saving passed:\n%s", out)
	}
}

func TestTheOrderingComparisonSpeaksJSON(t *testing.T) {
	out, _, code := exec(t, "store", "order", "-json", "-text", "1200000000000", orderLog(t, 3, 0.08))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var report struct {
		Median   float64  `json:"median"`
		Ratio    float64  `json:"ratio"`
		Target   int64    `json:"target"`
		Resident int64    `json:"resident"`
		Shards   int      `json:"shards"`
		Boxes    []string `json:"boxes"`
		Settled  bool     `json:"settled"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if !report.Settled || len(report.Boxes) != 2 {
		t.Fatalf("settled=%v off %v", report.Settled, report.Boxes)
	}
	if report.Resident <= report.Target {
		t.Errorf("a %d byte shard was reported as needing %d bytes resident", report.Target, report.Resident)
	}
	if report.Shards < 600 || report.Shards > 900 {
		t.Errorf("1.2 TB of text at %.2f to 1 came to %d shards", report.Ratio, report.Shards)
	}
}

func TestOrderAsksForAReadingsFile(t *testing.T) {
	if _, _, code := exec(t, "store", "order"); code != 2 {
		t.Error("no readings file did not read as a usage error")
	}
	if _, errOut, code := exec(t, "store", "order", filepath.Join(t.TempDir(), "nowhere.jsonl")); code != 1 || !strings.Contains(errOut, "nowhere.jsonl") {
		t.Errorf("exit %d and %q from a readings file that is not there", code, errOut)
	}
}
