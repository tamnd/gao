package kho

import (
	"crypto/sha1" //nolint:gosec // the hub names small files the way git does and this reproduces that
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tamnd/gao/may"
)

// hub is enough of the Hugging Face upload protocol to test against: the
// resolve endpoint that says what a path holds, the preupload check, the LFS
// batch endpoint that says whether the bytes are needed, storage, and commit.
//
// It keeps objects and paths in separate maps on purpose, because that
// separation is the thing being tested. An object can be in storage with no
// path pointing at it, which is exactly the state an interrupted push leaves
// behind, and a fake that collapsed the two could not produce it.
type hub struct {
	t *testing.T

	mu      sync.Mutex
	objects map[string][]byte // oid to bytes, which is storage
	files   map[string]string // path to the etag the repo answers with
	linked  map[string]bool   // whether that etag is an lfs digest or a git blob id
	created []map[string]any
	calls   map[string]int
	authed  map[string]bool
	commits []string

	pageSize  int    // how many entries a listing answers with, zero is [listPage]
	mode      string // what preupload answers, empty is lfs
	ignore    bool
	status    map[string]int // endpoint to status, for making one step fail
	storageOK bool           // set when the PUT arrived without our token on it

	srv *httptest.Server
}

const testRepo = Org + "/vietnamese-text-staging"

func newHub(t *testing.T) *hub {
	t.Helper()
	h := &hub{
		t:       t,
		objects: map[string][]byte{},
		files:   map[string]string{},
		linked:  map[string]bool{},
		calls:   map[string]int{},
		authed:  map[string]bool{},
		status:  map[string]int{},
	}
	h.srv = httptest.NewServer(h)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hub) pusher() *Pusher {
	return &Pusher{Repo: testRepo, Token: "hf_test", API: h.srv.URL, Client: h.srv.Client()}
}

// stored returns what the repo holds at a path. It needs both maps because the
// repo names a file by its lfs digest or its git blob id and storage names the
// bytes by their sha256, which is the same two identities the pusher deals with.
func (h *hub) stored(path string) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	tag, ok := h.files[path]
	if !ok {
		return nil
	}
	if h.linked[path] {
		return h.objects[tag]
	}
	for _, b := range h.objects {
		if gitBlobID(b) == tag {
			return b
		}
	}
	return nil
}

func (h *hub) count(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[name]
}

func (h *hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/cdn/"):
		w.Header().Set("Etag", `"a-cdn-entity-tag-that-is-nobodys-digest"`)
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(path, "/storage/"):
		h.storage(w, r)
	case strings.HasSuffix(path, "/info/lfs/objects/batch"):
		h.batch(w, r)
	case strings.Contains(path, "/preupload/"):
		h.preupload(w, r)
	case strings.Contains(path, "/commit/"):
		h.commit(w, r)
	case path == "/api/repos/create":
		h.create(w, r)
	case strings.Contains(path, "/resolve/"):
		h.resolve(w, r)
	case strings.Contains(path, "/tree/"):
		h.tree(w, r)
	default:
		h.t.Errorf("the pusher called %s %s, which is not part of the protocol", r.Method, path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// step records the call and reports whether the test wants it to fail.
func (h *hub) step(w http.ResponseWriter, r *http.Request, name string) bool {
	h.mu.Lock()
	h.calls[name]++
	code := h.status[name]
	h.mu.Unlock()
	if r.Header.Get("Authorization") == "Bearer hf_test" {
		h.mu.Lock()
		h.authed[name] = true
		h.mu.Unlock()
	}
	if code != 0 {
		w.WriteHeader(code)
		fmt.Fprint(w, `{"error":"the hub said no"}`)
		return false
	}
	return true
}

func (h *hub) resolve(w http.ResponseWriter, r *http.Request) {
	if !h.step(w, r, "resolve") {
		return
	}
	_, path, _ := strings.Cut(r.URL.Path, "/resolve/main/")
	h.mu.Lock()
	tag, ok := h.files[path]
	linked := h.linked[path]
	h.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// The Hub names a large file by its lfs digest and a small one by its git
	// blob id, and which header it comes back in is how a caller can tell. A
	// large one also answers 302 to a CDN, and the digest is on the redirect
	// rather than on what it points at, so a client that follows it sees an
	// entity tag belonging to somebody else's cache.
	if linked {
		w.Header().Set("X-Linked-Etag", `"`+tag+`"`)
		w.Header().Set("Location", h.srv.URL+"/cdn/"+tag)
		w.WriteHeader(http.StatusFound)
		return
	}
	w.Header().Set("Etag", `"`+tag+`"`)
	w.WriteHeader(http.StatusOK)
}

func (h *hub) preupload(w http.ResponseWriter, r *http.Request) {
	if !h.step(w, r, "preupload") {
		return
	}
	var in struct {
		Files []struct {
			Path   string `json:"path"`
			Sample string `json:"sample"`
			Size   int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.t.Errorf("preupload body: %v", err)
	}
	if len(in.Files) != 1 {
		h.t.Fatalf("preupload asked about %d files", len(in.Files))
	}
	if _, err := base64.StdEncoding.DecodeString(in.Files[0].Sample); err != nil {
		h.t.Errorf("the sample is not base64: %v", err)
	}
	mode := h.mode
	if mode == "" {
		mode = "lfs"
	}
	h.reply(w, map[string]any{"files": []map[string]any{{
		"path": in.Files[0].Path, "uploadMode": mode, "shouldIgnore": h.ignore,
	}}})
}

func (h *hub) batch(w http.ResponseWriter, r *http.Request) {
	if !h.step(w, r, "batch") {
		return
	}
	var in struct {
		Objects []struct {
			OID  string `json:"oid"`
			Size int64  `json:"size"`
		} `json:"objects"`
		HashAlgo string `json:"hash_algo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.t.Errorf("batch body: %v", err)
	}
	if in.HashAlgo != "sha_256" {
		h.t.Errorf("the batch asked for %q rather than sha_256", in.HashAlgo)
	}
	o := in.Objects[0]
	out := map[string]any{"oid": o.OID, "size": o.Size}

	h.mu.Lock()
	_, have := h.objects[o.OID]
	h.mu.Unlock()
	if !have {
		out["actions"] = map[string]any{"upload": map[string]any{
			"href":   h.srv.URL + "/storage/" + o.OID,
			"header": map[string]string{"x-gao-test": "1"},
		}}
	}
	h.reply(w, map[string]any{"objects": []map[string]any{out}})
}

func (h *hub) storage(w http.ResponseWriter, r *http.Request) {
	if !h.step(w, r, "storage") {
		return
	}
	if r.Header.Get("Authorization") != "" {
		h.t.Error("the repo token was sent to storage, which is somebody else's server")
	} else {
		h.mu.Lock()
		h.storageOK = true
		h.mu.Unlock()
	}
	if r.Header.Get("x-gao-test") != "1" {
		h.t.Error("the headers the batch handed back were not sent with the bytes")
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.t.Errorf("reading the upload: %v", err)
	}
	oid := strings.TrimPrefix(r.URL.Path, "/storage/")
	if got := hex.EncodeToString(sha256sum(body)); got != oid {
		h.t.Errorf("the bytes hash to %s and were uploaded as %s", got, oid)
	}
	h.mu.Lock()
	h.objects[oid] = body
	h.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (h *hub) commit(w http.ResponseWriter, r *http.Request) {
	if !h.step(w, r, "commit") {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.t.Fatalf("reading the commit: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		h.t.Fatalf("the commit has %d lines, want a header and a file:\n%s", len(lines), body)
	}
	var head, op struct {
		Key   string         `json:"key"`
		Value map[string]any `json:"value"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil {
		h.t.Fatalf("the commit header: %v", err)
	}
	if head.Key != "header" {
		h.t.Errorf("the commit starts with %q rather than a header", head.Key)
	}
	if err := json.Unmarshal([]byte(lines[1]), &op); err != nil {
		h.t.Fatalf("the commit file: %v", err)
	}

	path, _ := op.Value["path"].(string)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.commits = append(h.commits, fmt.Sprint(head.Value["summary"]))
	switch op.Key {
	case "lfsFile":
		oid, _ := op.Value["oid"].(string)
		if _, ok := h.objects[oid]; !ok {
			h.t.Errorf("%s was committed against %s, which storage does not have", path, oid)
		}
		h.files[path] = oid
		h.linked[path] = true
	case "file":
		content, _ := op.Value["content"].(string)
		raw, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			h.t.Errorf("the inline content is not base64: %v", err)
		}
		h.objects[hex.EncodeToString(sha256sum(raw))] = raw
		h.files[path] = gitBlobID(raw)
		h.linked[path] = false
	default:
		h.t.Errorf("the commit carries a %q, which is not a file", op.Key)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *hub) create(w http.ResponseWriter, r *http.Request) {
	if !h.step(w, r, "create") {
		return
	}
	var in map[string]any
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.t.Errorf("create body: %v", err)
	}
	h.mu.Lock()
	h.created = append(h.created, in)
	h.mu.Unlock()
	h.reply(w, map[string]any{"url": "https://huggingface.co/datasets/" + testRepo})
}

func (h *hub) reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.t.Errorf("writing the reply: %v", err)
	}
}

// gitBlobID is how git names a small file, which is what the Hub answers with
// for anything it did not store through lfs.
func gitBlobID(b []byte) string {
	h := sha1.New() //nolint:gosec // this reproduces a name git chose, it does not choose one
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func sha256sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

// partOnDisk writes something that stands in for a finished part and returns
// its local path, the path it takes inside the repo, and its digest.
func partOnDisk(t *testing.T, body string) (local, path, oid string) {
	t.Helper()
	path = StagePath("glotcc-9ad140b6be3a", 0, 0)
	local = filepath.Join(t.TempDir(), filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return local, path, hex.EncodeToString(sha256sum([]byte(body)))
}

func TestAPushPutsTheBytesInStorageAndPointsThePathAtThem(t *testing.T) {
	h := newHub(t)
	const body = "the first part of the first file"
	local, path, oid := partOnDisk(t, body)

	got, err := h.pusher().Push(t.Context(), local, path)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !got.Transferred || !got.Committed || got.Skipped() {
		t.Errorf("a first push reported %+v", got)
	}
	if got.OID != oid {
		t.Errorf("the push named %s and the file hashes to %s", got.OID, oid)
	}
	if got.Bytes != int64(len(body)) {
		t.Errorf("the push reported %d bytes", got.Bytes)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if string(h.objects[oid]) != body {
		t.Errorf("storage holds %q", h.objects[oid])
	}
	if h.files[path] != oid {
		t.Errorf("%s points at %q", path, h.files[path])
	}
	if !h.storageOK {
		t.Error("the upload never reached storage")
	}
	for _, step := range []string{"resolve", "preupload", "batch", "commit"} {
		if !h.authed[step] {
			t.Errorf("%s went to the hub without the token", step)
		}
	}
	if len(h.commits) != 1 || !strings.Contains(h.commits[0], "part-00000") {
		t.Errorf("the commit message does not name the file: %v", h.commits)
	}
}

// The second run over a directory of parts that were already pushed should cost
// one HEAD each and nothing else, because that is what makes it reasonable to
// just start the ingest again after a box reboots.
func TestASecondPushOfTheSamePartDoesNothing(t *testing.T) {
	h := newHub(t)
	local, path, _ := partOnDisk(t, "already up there")
	p := h.pusher()

	if _, err := p.Push(t.Context(), local, path); err != nil {
		t.Fatalf("first Push: %v", err)
	}
	got, err := p.Push(t.Context(), local, path)
	if err != nil {
		t.Fatalf("second Push: %v", err)
	}
	if !got.Skipped() {
		t.Errorf("the second push reported %+v", got)
	}
	if n := h.count("commit"); n != 1 {
		t.Errorf("%d commits for two pushes of the same file", n)
	}
	if n := h.count("storage"); n != 1 {
		t.Errorf("the bytes went up %d times", n)
	}
	if n := h.count("resolve"); n != 2 {
		t.Errorf("%d resolve calls, want one per push", n)
	}
}

// This is the case the whole design of the resume rests on: the bytes arrived,
// the commit did not, and the process died in between. The batch endpoint keys
// on the digest, so the retry skips the gigabyte and writes the commit.
func TestAnInterruptedPushDoesNotSendTheBytesAgain(t *testing.T) {
	h := newHub(t)
	local, path, oid := partOnDisk(t, "uploaded but never committed")
	p := h.pusher()

	h.status["commit"] = http.StatusInternalServerError
	if _, err := p.Push(t.Context(), local, path); err == nil {
		t.Fatal("a failed commit was reported as a push")
	}
	h.mu.Lock()
	_, inStorage := h.objects[oid]
	_, committed := h.files[path]
	h.mu.Unlock()
	if !inStorage || committed {
		t.Fatalf("the interrupted state is wrong: in storage %v, committed %v", inStorage, committed)
	}

	delete(h.status, "commit")
	got, err := p.Push(t.Context(), local, path)
	if err != nil {
		t.Fatalf("the retry: %v", err)
	}
	if got.Transferred {
		t.Error("the retry sent the bytes again")
	}
	if !got.Committed {
		t.Error("the retry did not commit")
	}
	if n := h.count("storage"); n != 1 {
		t.Errorf("the bytes went up %d times", n)
	}
}

// A path is a function of what is in it, so a difference means the file was
// rewritten and the new one is the one that belongs there.
func TestAPathHoldingSomethingElseIsOverwritten(t *testing.T) {
	h := newHub(t)
	local, path, _ := partOnDisk(t, "the second attempt")
	h.mu.Lock()
	h.files[path] = strings.Repeat("0", 64)
	h.mu.Unlock()

	got, err := h.pusher().Push(t.Context(), local, path)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !got.Committed || !got.Transferred {
		t.Errorf("the push reported %+v", got)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.files[path] != got.OID {
		t.Errorf("%s still points at the old object", path)
	}
}

func TestARefusedTokenNamesTheVariableAndTheAccessItNeeds(t *testing.T) {
	h := newHub(t)
	local, path, _ := partOnDisk(t, "no")
	h.status["resolve"] = http.StatusForbidden

	_, err := h.pusher().Push(t.Context(), local, path)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a 403 came back as %v", err)
	}
	if !strings.Contains(err.Error(), Org) {
		t.Errorf("the error does not say which org the token needs: %v", err)
	}

	p := h.pusher()
	p.Token = ""
	_, err = p.Push(t.Context(), local, path)
	if !strings.Contains(err.Error(), may.TokenEnv) {
		t.Errorf("a push with no token does not say where one goes: %v", err)
	}
}

// A part is not a small file, and committing one inline would mean base64 of a
// gigabyte through a JSON field. Reaching that means the repo is not tracking
// Parquet with LFS, which is a repo to fix rather than an upload to attempt.
func TestALargeFileTheRepoWillNotTrackWithLFSIsRefused(t *testing.T) {
	h := newHub(t)
	h.mode = "regular"
	local, path, _ := partOnDisk(t, strings.Repeat("x", InlineMax+1))

	_, err := h.pusher().Push(t.Context(), local, path)
	if err == nil {
		t.Fatal("a regular upload of a part was attempted")
	}
	if !strings.Contains(err.Error(), "gitattributes") {
		t.Errorf("the error does not say what to fix: %v", err)
	}
	if n := h.count("commit"); n != 0 {
		t.Errorf("it committed anyway, %d times", n)
	}
}

// Small files do go inline, because the dataset card and the manifest are files
// too and they are the other thing that gets pushed into these repos.
func TestASmallFileGoesUpInTheCommitItself(t *testing.T) {
	h := newHub(t)
	h.mode = "regular"
	local, path, oid := partOnDisk(t, "# vietnamese-text-staging\n")

	got, err := h.pusher().Push(t.Context(), local, path)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !got.Committed {
		t.Errorf("the push reported %+v", got)
	}
	if n := h.count("batch"); n != 0 {
		t.Errorf("a regular file went through the lfs endpoint %d times", n)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.objects[oid]; !ok {
		t.Errorf("%s did not put the bytes anywhere", path)
	}
}

// A dataset card is pushed on every run and changes on almost none of them, so
// a check that only understood lfs digests would re-commit it every time and
// fill the repo history with commits that changed nothing.
func TestASmallFileAlreadyThereIsNotCommittedAgain(t *testing.T) {
	h := newHub(t)
	h.mode = "regular"
	local, path, _ := partOnDisk(t, "# vietnamese-text-staging\n")
	p := h.pusher()

	if _, err := p.Push(t.Context(), local, path); err != nil {
		t.Fatalf("first Push: %v", err)
	}
	got, err := p.Push(t.Context(), local, path)
	if err != nil {
		t.Fatalf("second Push: %v", err)
	}
	if !got.Skipped() {
		t.Errorf("the second push of an unchanged small file reported %+v", got)
	}
	if n := h.count("commit"); n != 1 {
		t.Errorf("%d commits for two pushes of the same file", n)
	}
}

func TestAPathTheRepoIgnoresIsAFailureRatherThanASilentDrop(t *testing.T) {
	h := newHub(t)
	h.ignore = true
	local, path, _ := partOnDisk(t, "into the void")

	_, err := h.pusher().Push(t.Context(), local, path)
	if err == nil {
		t.Fatal("a path the repo ignores was reported as pushed")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error does not name the path: %v", err)
	}
}

func TestAFailedUploadSaysWhatStorageSaid(t *testing.T) {
	h := newHub(t)
	h.status["storage"] = http.StatusServiceUnavailable
	local, path, _ := partOnDisk(t, "storage is down")

	_, err := h.pusher().Push(t.Context(), local, path)
	if err == nil {
		t.Fatal("a failed upload was reported as a push")
	}
	if !strings.Contains(err.Error(), "the hub said no") {
		t.Errorf("the error does not carry what came back: %v", err)
	}
	if n := h.count("commit"); n != 0 {
		t.Errorf("it committed bytes that never arrived, %d times", n)
	}
}

// The working repos hold unfiltered text, so one created public would be a
// publication rather than a mistake with a fix.
func TestAWorkingRepoIsCreatedPrivate(t *testing.T) {
	h := newHub(t)
	if err := h.pusher().EnsureRepo(t.Context(), Staging()); err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.created) != 1 {
		t.Fatalf("%d repos created", len(h.created))
	}
	got := h.created[0]
	if got["private"] != true {
		t.Error("the staging repo was created public")
	}
	if got["name"] != StageRepo || got["organization"] != Org || got["type"] != "dataset" {
		t.Errorf("it created %v", got)
	}
}

func TestAPublishedRepoIsCreatedPublic(t *testing.T) {
	h := newHub(t)
	d, ok := Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("vietnamese-web-text is not in the table")
	}
	if err := h.pusher().EnsureRepo(t.Context(), d); err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.created[0]["private"] != false {
		t.Error("a published repo was created private")
	}
}

// Creating a repo that is already there is the normal case on every run after
// the first, and it is not a failure.
func TestCreatingARepoThatExistsIsFine(t *testing.T) {
	h := newHub(t)
	h.status["create"] = http.StatusConflict
	if err := h.pusher().EnsureRepo(t.Context(), Staging()); err != nil {
		t.Errorf("a repo that already exists came back as %v", err)
	}
}

// The size guard exists for a backend limit, and the point of it is that
// nothing this project writes can reach it. If the roll ever rolls at more text
// than a single upload takes, the guard stops being a bug report and starts
// being a wall.
func TestNoPartTheRollWritesCanBeTooLargeToPush(t *testing.T) {
	if TextPerPart >= MaxUpload {
		t.Errorf("a part holds %d bytes of text and an upload takes %d, so a part can be refused by its own uploader",
			TextPerPart, int64(MaxUpload))
	}
}

func TestANewRepoGetsACardSoItsFrontPageIsNotAFileListing(t *testing.T) {
	h := newHub(t)
	if err := h.pusher().EnsureRepo(t.Context(), Staging()); err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.files[CardName]; !ok {
		t.Fatalf("a repo was created with no %s on it", CardName)
	}
	if len(h.commits) != 1 {
		t.Errorf("%d commits, want the card and nothing else", len(h.commits))
	}
}

// A repo that is already there keeps the card it has. That card may have been
// generated from a manifest this caller does not have, and replacing it with the
// empty one would be deleting the release notes on every ingest.
func TestARepoThatAlreadyExistsKeepsTheCardItHas(t *testing.T) {
	h := newHub(t)
	h.status["create"] = http.StatusConflict
	if err := h.pusher().EnsureRepo(t.Context(), Staging()); err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	if got := h.count("commit"); got != 0 {
		t.Errorf("%d commits against a repo that was already there", got)
	}
}

func TestACardGoesUpAsTheReadme(t *testing.T) {
	h := newHub(t)
	d := Staging()

	sent, err := h.pusher().PushCard(t.Context(), d, nil)
	if err != nil {
		t.Fatalf("PushCard: %v", err)
	}
	if sent.Path != CardName {
		t.Errorf("the card went to %s", sent.Path)
	}
	if !sent.Committed {
		t.Error("the card was not committed")
	}

	// A card is a page of text and goes up in the commit itself. Sending it
	// through lfs would work and would put a page of markdown in object storage
	// where nothing can read it as text.
	if h.count("batch") != 0 {
		t.Error("the card went through lfs")
	}

	if body := h.stored(CardName); string(body) != Card(d, nil) {
		t.Errorf("what landed is not the card:\n%s", body)
	}
}

func TestACardThatSaysTheSameThingIsNotCommittedAgain(t *testing.T) {
	h := newHub(t)
	d := Staging()
	p := h.pusher()

	if _, err := p.PushCard(t.Context(), d, nil); err != nil {
		t.Fatalf("PushCard: %v", err)
	}
	sent, err := p.PushCard(t.Context(), d, nil)
	if err != nil {
		t.Fatalf("PushCard again: %v", err)
	}
	if !sent.Skipped() {
		t.Error("the same card was pushed twice")
	}
	if got := h.count("commit"); got != 1 {
		t.Errorf("%d commits for one card", got)
	}
}

func TestACardWithNewCountsReplacesTheOneUpThere(t *testing.T) {
	h := newHub(t)
	d, p := published(t), h.pusher()

	if _, err := p.PushCard(t.Context(), d, nil); err != nil {
		t.Fatalf("PushCard: %v", err)
	}
	m := released(t)
	if _, err := p.PushCard(t.Context(), d, m); err != nil {
		t.Fatalf("PushCard with a snapshot: %v", err)
	}

	if body := string(h.stored(CardName)); !strings.Contains(body, "412000000") {
		t.Errorf("the card up there is not the one with the counts:\n%s", body)
	}
}
