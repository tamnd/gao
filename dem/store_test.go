package dem

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/kho"
)

// store is enough of the Hub to read parts back out of: the listing and the
// resolve endpoint, the second of which answers range requests, because reading
// one column of a part is the whole point and it is done by range.
type store struct {
	t    *testing.T
	repo string

	mu     sync.Mutex
	files  map[string][]byte
	ranged int   // how many range requests arrived
	served int64 // how many bytes went out

	srv *httptest.Server
}

func newStore(t *testing.T) *store {
	t.Helper()
	s := &store{t: t, repo: kho.Org + "/vietnamese-source-text", files: map[string][]byte{}}
	s.srv = httptest.NewServer(s)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *store) store() *Store {
	// A window small enough that a test part crosses several of them, since a
	// fixture the size of a real part would be a fixture nobody waits for.
	return &Store{Repo: s.repo, Token: "hf_test", API: s.srv.URL, window: 4 << 10}
}

// storeAtRealWindows is store() without the test's small window, so that a test
// about which window a door opens with measures the real one.
func (s *store) storeAtRealWindows() *Store {
	return &Store{Repo: s.repo, Token: "hf_test", API: s.srv.URL}
}

func (s *store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "/tree/"):
		s.tree(w, r)
	case strings.Contains(r.URL.Path, "/resolve/"):
		s.resolve(w, r)
	default:
		s.t.Errorf("the pass asked for %s %s, which is not part of reading a repo", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *store) tree(w http.ResponseWriter, r *http.Request) {
	_, prefix, _ := strings.Cut(r.URL.Path, "/tree/main/")

	s.mu.Lock()
	var paths []string
	for p := range s.files {
		if strings.HasPrefix(p, prefix) {
			paths = append(paths, p)
		}
	}
	s.mu.Unlock()
	sort.Strings(paths)

	if len(paths) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	entries := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, map[string]any{
			"type": "file", "path": p, "oid": "pointer", "size": 134,
			"lfs": map[string]any{"oid": "sha256", "size": len(s.files[p])},
		})
	}
	_ = json.NewEncoder(w).Encode(entries)
}

func (s *store) resolve(w http.ResponseWriter, r *http.Request) {
	_, path, _ := strings.Cut(r.URL.Path, "/resolve/main/")
	s.mu.Lock()
	body, ok := s.files[path]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Header.Get("Authorization") != "Bearer hf_test" {
		s.t.Error("a part was read without the token, which is every private repo refused")
	}

	var from, to int64
	if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &from, &to); err != nil {
		s.t.Errorf("a part was asked for without a range: %q", r.Header.Get("Range"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	if to >= int64(len(body)) {
		to = int64(len(body)) - 1
	}
	s.mu.Lock()
	s.ranged++
	s.served += to - from + 1
	s.mu.Unlock()

	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to, len(body)))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(body[from : to+1])
}

// put builds a real part holding the given texts and puts it in the repo at the
// path an ingest would have written it to.
func (s *store) put(snapshot string, file, part int, texts ...string) {
	s.t.Helper()
	docs := make([]*doc.Document, len(texts))
	for i, text := range texts {
		docs[i] = document(text)
	}
	s.putDocs(snapshot, file, part, docs...)
}

// putDocs is put for a test that needs the documents to say something other than
// the truth about themselves, which is the case a spot check exists for.
func (s *store) putDocs(snapshot string, file, part int, docs ...*doc.Document) {
	s.t.Helper()
	d, ok := kho.Lookup("vietnamese-web-text")
	if !ok {
		s.t.Fatal("the published text repo is not in the dataset table")
	}
	dir := s.t.TempDir()
	rel := kho.StagePath(snapshot, file, part)
	p, err := kho.CreatePart(dir, rel, d, kho.Stamp{Snapshot: snapshot, Stage: "gat@0.1.0", Box: "server1"})
	if err != nil {
		s.t.Fatalf("CreatePart: %v", err)
	}
	defer p.Abandon()
	for _, d := range docs {
		if err := p.Append(d); err != nil {
			s.t.Fatalf("Append: %v", err)
		}
	}
	if _, err := p.Close(); err != nil {
		s.t.Fatalf("Close: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		s.t.Fatal(err)
	}
	s.mu.Lock()
	s.files[rel] = body
	s.mu.Unlock()
}

// document is a valid document carrying the given text, which is the only field
// any of this reads.
func document(text string) *doc.Document {
	d := &doc.Document{
		RawID:         doc.SumString("raw:" + text),
		Text:          text,
		SchemaVersion: doc.SchemaVersion,
		Provenance: doc.Provenance{
			Source:          doc.SourceGlotCC,
			SourceLocator:   "v1.0/vie-Latn_0.jsonl.zst@0+4096",
			URL:             "https://vnexpress.net/" + doc.SumString(text).String()[:8] + ".html",
			Host:            "vnexpress.net",
			FetchedAt:       time.Date(2026, 9, 14, 3, 22, 11, 0, time.UTC),
			MediaType:       "text/html",
			Extractor:       "go-trafilatura@1.4.0",
			PipelineVersion: "0.1.0",
		},
		Language:  doc.Language{Lang: "vie", LangScore: 0.997, Diacritics: "present"},
		Licensing: doc.Licensing{LicenseClass: doc.LicenseOpen, LicenseEvidence: "cc-by from the source"},
	}
	d.DocID = doc.SumString(d.Text)
	d.NChars = doc.Chars(d.Text)
	d.NSyllables = doc.Syllables(d.Text)
	return d
}

// texts returns n distinct documents' worth of Vietnamese, long enough to pass
// the ingest contract.
func texts(from, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(
			"Bài viết số %d. Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc. "+
				"Nội dung của tài liệu này đủ dài để vượt qua ngưỡng tối thiểu của hợp đồng nhập liệu.", from+i)
	}
	return out
}

const snapshot = "glotcc-9ad140b6be3a"

func TestKeysComeOutOfTheStoreWithoutTheCorpusComingWithThem(t *testing.T) {
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 40)...)
	s.put(snapshot, 1, 0, texts(40, 40)...)

	dir := t.TempDir()
	out := filepath.Join(dir, "glotcc"+KeysExt)
	got, err := KeysOf(t.Context(), s.store(), snapshot, filepath.Join(dir, "work"), out, nil)
	if err != nil {
		t.Fatalf("KeysOf: %v", err)
	}
	if got.Documents != 80 || got.Distinct != 80 {
		t.Errorf("eighty distinct documents came out as %+v", got)
	}

	header, keys := read(t, out)
	if header != got {
		t.Errorf("KeysOf returned %+v and wrote %+v", got, header)
	}
	if len(keys) != 80 {
		t.Errorf("the key file holds %d keys, want 80", len(keys))
	}
}

// The identities in the key file have to be the identities in the parts, and
// nothing about reading one column out of order should change that.
func TestTheKeysAreTheDocumentsThatWereWritten(t *testing.T) {
	s := newStore(t)
	written := texts(0, 40)
	s.put(snapshot, 0, 0, written...)

	dir := t.TempDir()
	out := filepath.Join(dir, "glotcc"+KeysExt)
	if _, err := KeysOf(t.Context(), s.store(), snapshot, filepath.Join(dir, "work"), out, nil); err != nil {
		t.Fatalf("KeysOf: %v", err)
	}

	want := map[Key]bool{}
	for _, text := range written {
		want[KeyOf(doc.SumString(text))] = true
	}
	_, keys := read(t, out)
	for _, k := range keys {
		if !want[k] {
			t.Fatalf("the key file holds %d, which is not the identity of anything that was written", k)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("%d documents that were written are not in the key file", len(want))
	}
}

// This is the claim the whole approach rests on. If reading identities moved the
// text as well, the pass would be a download of the corpus with extra steps.
func TestReadingIdentitiesDoesNotMoveTheText(t *testing.T) {
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 400)...)

	s.mu.Lock()
	size := int64(len(s.files[kho.StagePath(snapshot, 0, 0)]))
	s.mu.Unlock()

	dir := t.TempDir()
	if _, err := KeysOf(t.Context(), s.store(), snapshot, filepath.Join(dir, "work"), filepath.Join(dir, "k"+KeysExt), nil); err != nil {
		t.Fatalf("KeysOf: %v", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ranged == 0 {
		t.Fatal("the part was not read by range at all")
	}
	if s.served >= size {
		t.Errorf("reading one column moved %d bytes of a %d byte part, so it read the whole file", s.served, size)
	}
}

// A pass over a few hundred parts will be interrupted, and the unit of lost work
// has to be one part rather than a source.
func TestAnInterruptedPassPicksUpAtThePartItStoppedOn(t *testing.T) {
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 40)...)
	s.put(snapshot, 1, 0, texts(40, 40)...)

	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	out := filepath.Join(dir, "glotcc"+KeysExt)
	first, err := KeysOf(t.Context(), s.store(), snapshot, work, out, nil)
	if err != nil {
		t.Fatalf("KeysOf: %v", err)
	}

	s.mu.Lock()
	s.ranged = 0
	s.mu.Unlock()

	second, err := KeysOf(t.Context(), s.store(), snapshot, work, out, nil)
	if err != nil {
		t.Fatalf("KeysOf again: %v", err)
	}
	if second != first {
		t.Errorf("the second pass came out as %+v and the first as %+v", second, first)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ranged != 0 {
		t.Errorf("the second pass read %d ranges, so it did the work over again", s.ranged)
	}
}

func TestAPassReportsEveryPartAsItGoes(t *testing.T) {
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 10)...)
	s.put(snapshot, 1, 0, texts(10, 10)...)
	s.put(snapshot, 2, 0, texts(20, 10)...)

	var seen []string
	var last int
	dir := t.TempDir()
	_, err := KeysOf(t.Context(), s.store(), snapshot, filepath.Join(dir, "work"), filepath.Join(dir, "k"+KeysExt),
		func(part kho.Stored, i, of int, keys Keys, moved int64) {
			seen = append(seen, part.Path)
			if of != 3 {
				t.Errorf("the pass reported %d parts, want 3", of)
			}
			if i != last+1 {
				t.Errorf("the pass reported part %d after part %d", i, last)
			}
			last = i
			if keys.Documents != 10 {
				t.Errorf("%s reported %d documents, want 10", part.Path, keys.Documents)
			}
			if moved <= 0 {
				t.Errorf("%s reported %d bytes moved", part.Path, moved)
			}
		})
	if err != nil {
		t.Fatalf("KeysOf: %v", err)
	}
	if len(seen) != 3 {
		t.Errorf("the pass reported %d parts, want 3", len(seen))
	}
	if !sort.StringsAreSorted(seen) {
		t.Errorf("the parts were read out of path order: %v", seen)
	}
}

func TestASnapshotWithNoPartsSaysSoRatherThanWritingAnEmptyAnswer(t *testing.T) {
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 10)...)

	dir := t.TempDir()
	_, err := KeysOf(t.Context(), s.store(), "hplt3-0123456789ab", filepath.Join(dir, "work"), filepath.Join(dir, "k"+KeysExt), nil)
	if err == nil {
		t.Fatal("measuring a snapshot that is not there came back without an error")
	}
	if !strings.Contains(err.Error(), "hplt3-0123456789ab") {
		t.Errorf("the error does not name the snapshot: %v", err)
	}
}

func TestTheSnapshotsInTheStoreAreWhatHasBeenIngested(t *testing.T) {
	s := newStore(t)
	s.put("glotcc-9ad140b6be3a", 0, 0, texts(0, 10)...)
	s.put("glotcc-9ad140b6be3a", 1, 0, texts(10, 10)...)
	s.put("fineweb2-1c0ffee1c0ff", 0, 0, texts(20, 10)...)

	got, err := s.store().Snapshots(t.Context())
	if err != nil {
		t.Fatalf("Snapshots: %v", err)
	}
	want := []string{"fineweb2-1c0ffee1c0ff", "glotcc-9ad140b6be3a"}
	if len(got) != len(want) {
		t.Fatalf("Snapshots returned %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Snapshots returned %v, want %v", got, want)
		}
	}
}

func TestASnapshotNamesTheSourceItCameFrom(t *testing.T) {
	for _, tc := range []struct{ snapshot, want string }{
		{"glotcc-9ad140b6be3a", "glotcc"},
		{"hplt3-0123456789ab", "hplt3"},
		{"fineweb2", "fineweb2"},
	} {
		if got := SourceOf(tc.snapshot); got != tc.want {
			t.Errorf("SourceOf(%q) = %q, want %q", tc.snapshot, got, tc.want)
		}
	}
}

// A key file is named after the part it came from, so that a pass over a
// snapshot that has grown since the last one lines up with the parts that are
// there rather than shifting everything along by one.
func TestAPartsKeyFileIsNamedAfterThePart(t *testing.T) {
	if got, want := partKeys(kho.StagePath(snapshot, 3, 7)), "f00003-p00007"+KeysExt; got != want {
		t.Errorf("partKeys = %q, want %q", got, want)
	}
	if got, want := partKeys("data/snapshot=x/part-00002-of-00774.parquet"), "part-00002-of-00774"+KeysExt; got != want {
		t.Errorf("partKeys for a published shard = %q, want %q", got, want)
	}
}

// The two doors open with different windows, and the difference is worth a test
// because it is invisible from the call site and it was wrong for as long as
// nobody ran the pass against a real part. Reading the shape columns of one
// 511.6 MB part of glotcc-9ad140b6be3a at the default window moved 58.1 MB to
// sum 1.5 MB of them. At the column window it moves 1.8 MB and costs ten more
// requests.
func TestTheColumnDoorAsksForLessThanTheRowDoor(t *testing.T) {
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 40)...)

	st := s.storeAtRealWindows()
	parts, err := st.Parts(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("Parts: %v", err)
	}

	column, err := st.Open(t.Context(), parts[0])
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := column.Window(); got != gat.ColumnWindow {
		t.Errorf("the column pass opened with a %d byte window, want the %d byte column window", got, gat.ColumnWindow)
	}

	rows, err := st.OpenRows(t.Context(), parts[0])
	if err != nil {
		t.Fatalf("OpenRows: %v", err)
	}
	if got := rows.Window(); got != gat.DefaultWindow {
		t.Errorf("the row pass opened with a %d byte window, want the %d byte default window", got, gat.DefaultWindow)
	}
}
