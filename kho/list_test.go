package kho

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// tree answers the listing endpoint a page at a time, the way the Hub does.
//
// The paging is the part worth faking. A source of a few hundred gigabytes is
// well over a page of parts, so a listing that stopped at the first page would
// be wrong in production and right in every test that only ever put three files
// in the repo.
func (h *hub) tree(w http.ResponseWriter, r *http.Request) {
	if !h.step(w, r, "tree") {
		return
	}
	_, prefix, _ := strings.Cut(r.URL.Path, "/tree/main/")

	h.mu.Lock()
	paths := make([]string, 0, len(h.files))
	for p := range h.files {
		if strings.HasPrefix(p, prefix) {
			paths = append(paths, p)
		}
	}
	linked := make(map[string]bool, len(h.linked))
	for p, l := range h.linked {
		linked[p] = l
	}
	h.mu.Unlock()
	sort.Strings(paths)

	if len(paths) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	from, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
	size := h.pageSize
	if size <= 0 {
		size = listPage
	}
	to := min(from+size, len(paths))

	entries := make([]map[string]any, 0, to-from)
	for _, p := range paths[from:to] {
		body := h.stored(p)
		e := map[string]any{"type": "file", "path": p, "oid": "a-git-blob-id", "size": len(body)}
		if linked[p] {
			// A file the Hub keeps in lfs is committed as a pointer, so the size
			// on the entry itself is the pointer's and the real length is inside
			// the lfs block. A reader that took the outer one would open every
			// part as though it were 134 bytes long.
			e["size"] = 134
			e["lfs"] = map[string]any{"oid": h.files[p], "size": len(body)}
		}
		entries = append(entries, e)
	}
	if to < len(paths) {
		next := fmt.Sprintf("%s/api/datasets/%s/tree/main/%s?recursive=true&limit=%d&cursor=%d",
			h.srv.URL, testRepo, prefix, size, to)
		w.Header().Set("Link", `<`+next+`>; rel="next"`)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entries)
}

// fill puts n parts of one snapshot in the repo, through the pusher, so that
// what is listed is what a run would have left behind.
func fill(t *testing.T, h *hub, snapshot string, n int) {
	t.Helper()
	p, dir := h.pusher(), t.TempDir()
	for i := range n {
		local := filepath.Join(dir, fmt.Sprintf("part-%05d.parquet", i))
		body := strings.Repeat(fmt.Sprintf("part %d of %s ", i, snapshot), 64)
		if err := os.WriteFile(local, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Push(t.Context(), local, StagePath(snapshot, 0, i)); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
}

func TestAListingNamesEveryPartInASnapshot(t *testing.T) {
	h := newHub(t)
	fill(t, h, "glotcc-9ad140b6be3a", 3)
	fill(t, h, "fineweb2-1c0ffee1c0ff", 2)

	got, err := h.pusher().List(t.Context(), SourceDir("glotcc-9ad140b6be3a"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("the listing found %d parts, want 3", len(got))
	}
	for _, s := range got {
		if !strings.Contains(s.Path, "glotcc") {
			t.Errorf("the listing of one snapshot returned %s from another", s.Path)
		}
		if !s.Parquet() {
			t.Errorf("%s is not recognized as a part", s.Path)
		}
	}
}

// The length on the entry itself is the pointer's, and a reader that took it
// would ask for 134 bytes of a file and get a footer that is not there.
func TestAListingCarriesTheLengthOfTheObjectRatherThanOfThePointer(t *testing.T) {
	h := newHub(t)
	fill(t, h, "glotcc-9ad140b6be3a", 1)

	got, err := h.pusher().List(t.Context(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("the listing found %d files, want 1", len(got))
	}
	if !got[0].LFS {
		t.Fatal("a part is not listed as an lfs file")
	}
	if want := int64(len(h.stored(got[0].Path))); got[0].Bytes != want {
		t.Errorf("the listing says %s is %d bytes, want %d", got[0].Path, got[0].Bytes, want)
	}
	if got[0].OID != h.files[got[0].Path] {
		t.Errorf("the listing names %s by %q rather than by its object digest", got[0].Path, got[0].OID)
	}
}

// This is the one that matters at the size the store reaches. A source of 234 GB
// is more parts than one page holds, and a listing that stopped at the first
// page would measure a third of a corpus and report it as all of it.
func TestAListingFollowsThePagingToTheEnd(t *testing.T) {
	h := newHub(t)
	h.pageSize = 2
	fill(t, h, "glotcc-9ad140b6be3a", 7)

	got, err := h.pusher().List(t.Context(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 7 {
		t.Fatalf("the listing found %d parts of 7, so it stopped at a page boundary", len(got))
	}
	if pages := h.count("tree"); pages != 4 {
		t.Errorf("seven parts at two a page took %d requests, want 4", pages)
	}
}

// A snapshot nobody has written to yet is a fact about the run rather than a
// failure, and a caller that had to tell 404 from an error to find that out
// would get it wrong.
func TestListingASnapshotThatIsNotThereFindsNothingRatherThanFailing(t *testing.T) {
	h := newHub(t)
	fill(t, h, "glotcc-9ad140b6be3a", 1)

	got, err := h.pusher().List(t.Context(), SnapshotDir("hplt3-0123456789ab"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a snapshot with nothing in it listed %d files", len(got))
	}
}

func TestAListingSendsTheToken(t *testing.T) {
	h := newHub(t)
	fill(t, h, "glotcc-9ad140b6be3a", 1)

	if _, err := h.pusher().List(t.Context(), ""); err != nil {
		t.Fatalf("List: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.authed["tree"] {
		t.Error("the listing went out without the token, which is every private repo refused")
	}
}

func TestARefusedListingSaysWhichTokenItNeeds(t *testing.T) {
	h := newHub(t)
	fill(t, h, "glotcc-9ad140b6be3a", 1)
	h.status["tree"] = http.StatusForbidden

	_, err := h.pusher().List(t.Context(), "")
	if err == nil {
		t.Fatal("a refused listing came back without an error")
	}
	if !strings.Contains(err.Error(), Org) {
		t.Errorf("the error does not say what the token needs access to: %v", err)
	}
}

func TestTheNextPageIsTheOneTheLinkHeaderPointsAt(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{`<https://huggingface.co/api/datasets/a/b?cursor=2>; rel="next"`, "https://huggingface.co/api/datasets/a/b?cursor=2"},
		{`<https://x/1>; rel="prev", <https://x/2>; rel="next"`, "https://x/2"},
		{`<https://x/1>; rel="prev"`, ""},
		{"", ""},
		{"nonsense", ""},
	} {
		if got := nextLink(tc.header); got != tc.want {
			t.Errorf("nextLink(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}

func TestAResolveURLPointsAtTheBranchTheRepoIsRead(t *testing.T) {
	p := &Pusher{Repo: testRepo, API: "https://hub.example"}
	want := "https://hub.example/datasets/" + testRepo + "/resolve/main/data/x/part-00000.parquet"
	if got := p.ResolveURL("data/x/part-00000.parquet"); got != want {
		t.Errorf("ResolveURL = %q, want %q", got, want)
	}
}
