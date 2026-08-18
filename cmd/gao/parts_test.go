package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/kho"
)

// hpltLine is shaped like the records HPLT actually serves, since a fixture
// invented here would only agree with itself. It is the one source with a
// mapping whose files are streamed rather than read out of order, which is what
// makes it the one to drive this sink with.
const hpltLine = `{"f":"./segments/1498128329372.0/warc/CC-MAIN-20170629154125-20170629174125-00361.warc.gz",` +
	`"o":244782208,"s":31523,"rs":116583,"u":"http://maithao.vnweblogs.com/","c":"text/html",` +
	`"ts":"2017-06-29T15:42:29Z","de":"utf-8","crawl_id":"CC-MAIN-2017-26",` +
	`"lang":["vie_Latn"],"prob":[0.97],` +
	`"text":"Triệu Văn Đồi là nhà văn viết cần mẫn và bền bỉ của tỉnh Hòa Bình.\nTruyện ngắn của anh có lối viết khá sắc với nhiều hình ảnh mang ý nghĩa biểu tượng.",` +
	`"html_lang":["vi"],"cluster_size":8,"id":"53f8dd156ecc9372c3ac02e8c80575f8","filter":"keep",` +
	`"pii":[[23230,23254]],"doc_scores":[10,9.4],"web-register":{"NA":0.736}}`

func hpltPin(t *testing.T) (gat.Pinned, gat.File) {
	t.Helper()
	p, ok := gat.Pin(doc.SourceHPLT3)
	if !ok {
		t.Fatal("hplt3 is not pinned")
	}
	return p, p.Files[0]
}

func zstdOf(t *testing.T, s string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// ingest runs one file through the writing sink and returns the directory it
// wrote to and what it printed.
func ingest(t *testing.T, lines int) (*parts, string, string) {
	t.Helper()
	dir := t.TempDir()
	var out bytes.Buffer
	docs := &gat.Docs{}
	sink := newParts(dir, docs, "server1", &out)
	docs.Emit = sink.write

	p, f := hpltPin(t)
	body := zstdOf(t, strings.Repeat(hpltLine+"\n", lines))
	n, err := sink.Consume(t.Context(), p, f, body)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if n != int64(lines) {
		t.Fatalf("admitted %d documents of %d", n, lines)
	}
	return sink, dir, out.String()
}

func TestAnIngestWritesWhatItAdmits(t *testing.T) {
	sink, dir, out := ingest(t, 3)

	if sink.written != 1 {
		t.Fatalf("wrote %d parts, want 1", sink.written)
	}
	p, _ := hpltPin(t)
	want := kho.StagePath(p.Snapshot(), 0, 0)
	path := filepath.Join(dir, filepath.FromSlash(want))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the part is not at %s: %v", want, err)
	}

	rows, err := kho.ReadPart(path)
	if err != nil {
		t.Fatalf("ReadPart: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("the part holds %d documents, want 3", len(rows))
	}
	if rows[0].Source != string(doc.SourceHPLT3) {
		t.Errorf("the rows say they came from %q", rows[0].Source)
	}
	if rows[0].URL == "" || rows[0].Text == "" {
		t.Error("a row came back without the columns the contract requires")
	}
	if !strings.Contains(out, want) {
		t.Errorf("the run did not print the part it wrote:\n%s", out)
	}
}

// The stamp is what a shard says about itself once it is somewhere else, so the
// run that wrote it has to fill it in rather than leave it for the manifest.
func TestAWrittenPartSaysWhereItCameFrom(t *testing.T) {
	_, dir, _ := ingest(t, 1)
	p, _ := hpltPin(t)

	meta, err := kho.PartMetadata(filepath.Join(dir, filepath.FromSlash(kho.StagePath(p.Snapshot(), 0, 0))))
	if err != nil {
		t.Fatalf("PartMetadata: %v", err)
	}
	for k, want := range map[string]string{
		"gao.snapshot": p.Snapshot(),
		"gao.stage":    gat.Stage,
		"gao.box":      "server1",
	} {
		if meta[k] != want {
			t.Errorf("%s is %q, want %q", k, meta[k], want)
		}
	}
}

// A part holding the first half of a file that could not be read is worse than
// no part at all, because the ledger has no entry for the file and the restart
// would write the rest of it beside a fragment nobody knows is a fragment.
func TestAFileThatFailsToDecodeLeavesNoPart(t *testing.T) {
	dir := t.TempDir()
	docs := &gat.Docs{}
	sink := newParts(dir, docs, "server1", io.Discard)
	docs.Emit = sink.write

	p, f := hpltPin(t)
	body := zstdOf(t, hpltLine+"\n{ this is not a record }\n")
	if _, err := sink.Consume(t.Context(), p, f, body); err == nil {
		t.Fatal("a file that could not be decoded was reported as fine")
	}

	var found []string
	err := filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("a failed file left %v behind", found)
	}
}

// The file's position in the source's list is half the path its parts are
// written at, so a file the source does not pin has nowhere to go and saying so
// beats writing under a made up index.
func TestAFileTheSourceDoesNotPinIsRefused(t *testing.T) {
	dir := t.TempDir()
	docs := &gat.Docs{}
	sink := newParts(dir, docs, "server1", io.Discard)
	docs.Emit = sink.write

	p, _ := hpltPin(t)
	_, err := sink.Consume(t.Context(), p, gat.File{Path: "vie_Latn/nowhere.jsonl.zst"}, zstdOf(t, hpltLine+"\n"))
	if err == nil {
		t.Fatal("a file from outside the manifest was written anyway")
	}
	if !strings.Contains(err.Error(), "nowhere.jsonl.zst") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

// A document arriving with no file open would mean the sink was wired wrong,
// and writing it somewhere plausible is how that goes unnoticed.
func TestADocumentWithNoFileOpenIsABug(t *testing.T) {
	sink := newParts(t.TempDir(), &gat.Docs{}, "server1", io.Discard)
	if err := sink.write(&doc.Document{}); err == nil {
		t.Error("a document was written with no file open")
	}
}

func TestARunThatWroteNothingSaysNothing(t *testing.T) {
	var w bytes.Buffer
	sink := newParts(t.TempDir(), &gat.Docs{}, "server1", io.Discard)
	sink.summary(&w)
	if w.Len() != 0 {
		t.Errorf("a run that wrote no parts printed a summary:\n%s", w.String())
	}
}

func TestARunThatWroteSomethingSaysWhereItIs(t *testing.T) {
	sink, dir, _ := ingest(t, 2)
	var w bytes.Buffer
	sink.summary(&w)
	if !strings.Contains(w.String(), dir) {
		t.Errorf("the summary does not say where the parts are:\n%s", w.String())
	}
	if !strings.Contains(w.String(), "1 parts written") {
		t.Errorf("the summary does not say how many parts were written:\n%s", w.String())
	}
}

// -out implies -decode, since writing documents means having decoded them.
func TestTheOutFlagIsInTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "gat", "hf", "-h")
	if code != 2 {
		t.Fatalf("gao gat hf -h: exit %d, want 2", code)
	}
	for _, want := range []string{"-out", "parquet"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the usage does not mention %q", want)
		}
	}
}

// The dataset an ingest writes to is the working one, which is a release nobody
// has signed rather than a private place. Writing ingest output straight into a
// published repo would put text that no stage has cleaned under a name that
// says it was.
func TestAnIngestWritesToTheWorkingRepo(t *testing.T) {
	d := kho.Staging()
	if d.Tier != kho.Working {
		t.Errorf("%s is a release and an ingest writes there", d.Repo())
	}
	sink := newParts(t.TempDir(), &gat.Docs{}, "server1", io.Discard)
	if sink.dataset.Name != d.Name {
		t.Errorf("an ingest writes to %s, want %s", sink.dataset.Name, d.Name)
	}
}

// A part that cannot be handed off fails the file it came from, which is the
// property the upload will depend on.
func TestAPartThatCannotBeHandedOffFailsTheFile(t *testing.T) {
	dir := t.TempDir()
	docs := &gat.Docs{}
	sink := newParts(dir, docs, "server1", io.Discard)
	docs.Emit = sink.write

	fail := errors.New("the hub said no")
	p, f := hpltPin(t)
	if err := sink.open(t.Context(), p, f); err != nil {
		t.Fatal(err)
	}
	sink.roll.Finished = func(kho.PartFile) error { return fail }
	if err := sink.write(document(t, 0)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := sink.close(nil); !errors.Is(err, fail) {
		t.Errorf("the file was reported as written: %v", err)
	}
}

// acceptingHub is enough of the Hub to push at. It says yes to everything,
// because what is being tested here is what the ingest does with a push that
// worked, and the protocol itself is tested in kho.
func acceptingHub(t *testing.T) *kho.Pusher {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/resolve/"):
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "/preupload/"):
			fmt.Fprint(w, `{"files":[{"uploadMode":"lfs"}]}`)
		case strings.HasSuffix(r.URL.Path, "/info/lfs/objects/batch"):
			fmt.Fprintf(w, `{"objects":[{"actions":{"upload":{"href":%q}}}]}`, srv.URL+"/storage")
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return &kho.Pusher{Repo: kho.Staging().Repo(), API: srv.URL, Client: srv.Client(), Token: "hf_test"}
}

// The whole point of the push is that the box does not keep what it has
// finished, so a part still on disk after a successful push is the failure this
// guards against.
func TestAPushedPartIsDeletedFromTheBoxThatWroteIt(t *testing.T) {
	dir := t.TempDir()
	docs := &gat.Docs{}
	var out bytes.Buffer
	sink := newParts(dir, docs, "server1", &out)
	sink.push = acceptingHub(t)
	docs.Emit = sink.write

	p, f := hpltPin(t)
	if _, err := sink.Consume(t.Context(), p, f, zstdOf(t, strings.Repeat(hpltLine+"\n", 3))); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	path := filepath.Join(dir, filepath.FromSlash(kho.StagePath(p.Snapshot(), 0, 0)))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the part is still on the box that pushed it: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("the directory the part sat in was left behind empty")
	}
	if !strings.Contains(out.String(), "pushed") {
		t.Errorf("the run does not say it pushed anything:\n%s", out.String())
	}

	var w bytes.Buffer
	sink.summary(&w)
	if !strings.Contains(w.String(), kho.Staging().Repo()) {
		t.Errorf("the summary does not say where the parts went:\n%s", w.String())
	}
}

// A part that could not be pushed is the one part that has to stay, because the
// only copy of it is the one on this disk.
func TestAPartThatFailedToPushIsNotDeleted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	dir := t.TempDir()
	docs := &gat.Docs{}
	sink := newParts(dir, docs, "server1", io.Discard)
	sink.push = &kho.Pusher{Repo: kho.Staging().Repo(), API: srv.URL, Client: srv.Client(), Token: "hf_test"}
	docs.Emit = sink.write

	p, f := hpltPin(t)
	if err := sink.open(t.Context(), p, f); err != nil {
		t.Fatal(err)
	}
	if err := sink.write(document(t, 0)); err != nil {
		t.Fatal(err)
	}
	if err := sink.close(nil); err == nil {
		t.Fatal("a part that could not be pushed was reported as written")
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(kho.StagePath(p.Snapshot(), 0, 0)))); err != nil {
		t.Errorf("the only copy of an unpushed part was deleted: %v", err)
	}
}

// Pushing needs something to push, and a flag combination that cannot work
// should say so before the download starts rather than after it.
func TestPushingWithNowhereToWriteIsRefused(t *testing.T) {
	_, errOut, code := exec(t, "gat", "hf", "-dir", t.TempDir(), "-push")
	if code != 2 {
		t.Fatalf("gao gat hf -push with no -out: exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "-out") {
		t.Errorf("the error does not say what is missing:\n%s", errOut)
	}
}

func TestThePushFlagIsInTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "gat", "hf", "-h")
	if code != 2 {
		t.Fatalf("gao gat hf -h: exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "-push") {
		t.Errorf("the usage does not mention -push:\n%s", errOut)
	}
}
