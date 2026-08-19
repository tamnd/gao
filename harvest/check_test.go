package harvest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

const (
	pinnedCommit = "af9c13333eb981300149d5ca60a8e9d659b276b9"
	movedCommit  = "0123456789abcdef0123456789abcdef01234567"
)

func hubPin(url string) Pinned {
	return Pinned{
		Source:      doc.SourceFineWeb2,
		Origin:      Hub,
		Repo:        "HuggingFaceFW/fineweb-2",
		Revision:    pinnedCommit,
		RevisionURL: url,
	}
}

func directPin(url, revision string) Pinned {
	return Pinned{
		Source:      doc.SourceHPLT3,
		Origin:      Direct,
		Repo:        "https://data.hplt-project.org/three/sorted",
		Revision:    revision,
		RevisionURL: url,
	}
}

func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestAHubSourceThatHasNotMovedReadsAsUnchanged(t *testing.T) {
	s := serve(t, http.StatusOK, `{"id":"HuggingFaceFW/fineweb-2","sha":"`+pinnedCommit+`"}`)
	d, err := Check(t.Context(), s.Client(), hubPin(s.URL))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Moved() {
		t.Errorf("a source at its pinned commit reads as moved: %s", d)
	}
	if !strings.Contains(d.String(), "unchanged") {
		t.Errorf("the unchanged case reads as %q", d)
	}
	if d.Source != doc.SourceFineWeb2 {
		t.Errorf("the drift is attributed to %s", d.Source)
	}
}

// The case the whole file exists for. Upstream re-uploaded, and gao finds out by
// asking rather than by a corpus quietly changing what it was built from.
func TestAHubSourceThatMovedReadsAsMoved(t *testing.T) {
	s := serve(t, http.StatusOK, `{"sha":"`+movedCommit+`"}`)
	d, err := Check(t.Context(), s.Client(), hubPin(s.URL))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Moved() {
		t.Error("a source at a different commit reads as unchanged")
	}
	if d.Current != movedCommit || d.Pinned != pinnedCommit {
		t.Errorf("Check reports pinned %q and current %q", d.Pinned, d.Current)
	}
	// Both revisions have to be legible in the report, because the person
	// reading it is deciding whether to re-pin and needs to look at both.
	for _, want := range []string{pinnedCommit[:12], movedCommit[:12]} {
		if !strings.Contains(d.String(), want) {
			t.Errorf("%q does not mention %s", d, want)
		}
	}
}

// A direct source has no commit, so its revision is the digest of the file that
// fixes its shard list, and drift is that file changing.
func TestADirectSourceIsCheckedByHashingItsFileList(t *testing.T) {
	const list = "https://data.hplt-project.org/three/sorted/vie_Latn/5_1.jsonl.zst\n"
	s := serve(t, http.StatusOK, list)

	d, err := Check(t.Context(), s.Client(), directPin(s.URL, digestOf(list)))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Moved() {
		t.Errorf("an unchanged shard list reads as moved: %s", d)
	}

	d, err = Check(t.Context(), s.Client(), directPin(s.URL, digestOf(list+"and-one-more.jsonl.zst\n")))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Moved() {
		t.Error("a changed shard list reads as unchanged")
	}
	if !strings.Contains(d.String(), "sha256:") {
		t.Errorf("a digest prints as if it were a commit: %s", d)
	}
}

func TestCheckReportsWhatWentWrongRatherThanGuessing(t *testing.T) {
	for _, tc := range []struct {
		name string
		pin  func(url string) Pinned
		srv  func(t *testing.T) *httptest.Server
		want string
	}{
		{
			name: "the host is down",
			pin:  hubPin,
			srv:  func(t *testing.T) *httptest.Server { return serve(t, http.StatusBadGateway, "") },
			want: "502",
		},
		{
			name: "the repo is gone",
			pin:  hubPin,
			srv:  func(t *testing.T) *httptest.Server { return serve(t, http.StatusNotFound, "") },
			want: "404",
		},
		{
			name: "the answer is not JSON",
			pin:  hubPin,
			srv:  func(t *testing.T) *httptest.Server { return serve(t, http.StatusOK, "<html>maintenance</html>") },
			want: "fineweb2",
		},
		{
			name: "the answer names a branch",
			pin:  hubPin,
			srv:  func(t *testing.T) *httptest.Server { return serve(t, http.StatusOK, `{"sha":"main"}`) },
			want: "not a commit SHA",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.srv(t)
			_, err := Check(t.Context(), s.Client(), tc.pin(s.URL))
			if err == nil {
				t.Fatal("Check reported no error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Check said %q, which does not mention %q", err, tc.want)
			}
		})
	}
}

// The other end of the connection is not ours, so a host that answers forever
// does not get to hold the process forever.
func TestCheckDoesNotReadAHostForever(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := strings.Repeat("x", 1<<20)
		for range (MaxRevisionBytes >> 20) + 4 {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(s.Close)

	// A direct source hashes whatever it is given, so this is the path where an
	// unbounded read would actually run to the end.
	d, err := Check(t.Context(), s.Client(), directPin(s.URL, digestOf("anything")))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Moved() {
		t.Error("a host serving nothing like the pinned file reads as unchanged")
	}
	want := sha256.Sum256([]byte(strings.Repeat("x", MaxRevisionBytes)))
	if d.Current != "sha256:"+hex.EncodeToString(want[:]) {
		t.Error("Check read past the cap, or stopped short of it")
	}
}

func TestCheckStopsWhenItsCallerDoes(t *testing.T) {
	s := serve(t, http.StatusOK, `{"sha":"`+pinnedCommit+`"}`)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Check(ctx, s.Client(), hubPin(s.URL)); err == nil {
		t.Error("Check ran with a canceled context")
	}
}

func TestCheckFallsBackToTheDefaultClient(t *testing.T) {
	s := serve(t, http.StatusOK, `{"sha":"`+pinnedCommit+`"}`)
	if _, err := Check(t.Context(), nil, hubPin(s.URL)); err != nil {
		t.Errorf("Check with no client: %v", err)
	}
	if _, err := Check(t.Context(), nil, hubPin("://not a url")); err == nil {
		t.Error("Check accepted an address that is not one")
	}
}

// Every pinned source has to be checkable, or drift detection has a hole in it
// exactly where somebody would not think to look.
func TestEveryPinnedSourceCanBeChecked(t *testing.T) {
	for _, p := range Sources() {
		if !strings.HasPrefix(p.RevisionURL, "https://") {
			t.Errorf("%s reads its revision from %q", p.Source, p.RevisionURL)
		}
		switch p.Origin {
		case Hub:
			if !strings.Contains(p.RevisionURL, "/api/datasets/"+p.Repo) {
				t.Errorf("%s reads its revision from %s, which is not the API for %s", p.Source, p.RevisionURL, p.Repo)
			}
		case Direct:
			if !strings.HasPrefix(p.RevisionURL, p.Repo+"/") {
				t.Errorf("%s reads its revision from %s, which is not on the host it downloads from", p.Source, p.RevisionURL)
			}
		}
	}
}
