package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mamRun(t *testing.T, in string, args ...string) (string, string, int) {
	t.Helper()
	if in != "" {
		old := stdin
		stdin = strings.NewReader(in)
		t.Cleanup(func() { stdin = old })
	}
	var out, errb strings.Builder
	code := run(&out, &errb, append([]string{"mam"}, args...))
	return out.String(), errb.String(), code
}

const ctDump = `[
  {"name_value": "vnexpress.vn\nwww.vnexpress.vn", "not_before": "2024-01-05T00:00:00"},
  {"name_value": "vnexpress.vn", "not_before": "2019-02-01T00:00:00"},
  {"name_value": "*.com.vn", "not_before": "2023-01-01T00:00:00"},
  {"name_value": "*.ho-chi-minh.vn", "not_before": "2023-01-01T00:00:00"},
  {"name_value": "thu-vien.truong.edu.vn", "not_before": "2021-08-14T00:00:00"},
  {"name_value": "shop.example.com", "not_before": "2024-05-01T00:00:00"}
]`

func TestMamCTPrintsASeedListAndNothingElse(t *testing.T) {
	out, _, code := mamRun(t, ctDump, "ct")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	lines := strings.Fields(out)
	want := []string{"ho-chi-minh.vn", "thu-vien.truong.edu.vn", "vnexpress.vn", "www.vnexpress.vn"}
	if strings.Join(lines, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", lines, want)
	}
}

// A registrar wildcard for a suffix the public suffix list does not carry comes
// through as a registrable name, and what tells it apart from a site is that
// nothing ever named it outright.
func TestMamCTCanKeepOnlyWhatWasNamedOutright(t *testing.T) {
	out, _, code := mamRun(t, ctDump, "ct", "-direct")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.Contains(out, "ho-chi-minh.vn") {
		t.Errorf("a name seen only below a star survived -direct:\n%s", out)
	}
	if !strings.Contains(out, "vnexpress.vn") {
		t.Errorf("a real host was dropped by -direct:\n%s", out)
	}
}

func TestMamCTCountsWhatNamedEachHostAndWhen(t *testing.T) {
	out, _, code := mamRun(t, ctDump, "ct", "-counts")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "2 certificates") {
		t.Errorf("the host named twice is not reported as such:\n%s", out)
	}
	if !strings.Contains(out, "2019-02-01") {
		t.Errorf("the earliest certificate is not reported:\n%s", out)
	}
	// Heaviest first, because that is the order somebody reads a list in.
	if !strings.HasPrefix(strings.TrimSpace(out), "vnexpress.vn") {
		t.Errorf("the most certified host is not first:\n%s", out)
	}
}

// This subtraction is the measurement the whole route is judged on. Certificate
// Transparency is worth running only to the extent that it names hosts a seed
// list does not.
func TestMamCTSubtractsWhatWeAlreadyHave(t *testing.T) {
	seed := filepath.Join(t.TempDir(), "seed.txt")
	body := "# what we already had\nvnexpress.vn\nwww.vnexpress.vn\n\n"
	if err := os.WriteFile(seed, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := mamRun(t, ctDump, "ct", "-seed", seed)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.Contains(out, "vnexpress.vn") {
		t.Errorf("a host already in the seed was printed as new:\n%s", out)
	}
	if !strings.Contains(out, "thu-vien.truong.edu.vn") {
		t.Errorf("a host not in the seed was not printed:\n%s", out)
	}
	if !strings.Contains(errOut, "2 already in the seed, 2 new") {
		t.Errorf("the count does not say what the route was worth: %q", errOut)
	}
}

func TestMamCTReadsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ct.json")
	if err := os.WriteFile(path, []byte(ctDump), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := mamRun(t, "", "ct", path)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "thu-vien.truong.edu.vn") {
		t.Errorf("reading from a file lost a host:\n%s", out)
	}
}

func TestMamCTSearchesWhenAskedTo(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ctDump))
	}))
	defer s.Close()

	out, _, code := mamRun(t, "", "ct", "-search", s.URL)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "vnexpress.vn") {
		t.Errorf("the search returned nothing usable:\n%s", out)
	}
}

func TestMamCTSaysWhenItCannotReadWhatItWasGiven(t *testing.T) {
	_, errOut, code := mamRun(t, "khong phai json\n", "ct")
	if code == 0 {
		t.Error("something that is not a search result was read as one")
	}
	if !strings.Contains(errOut, "certificate transparency") {
		t.Errorf("the error does not say what it failed to read: %q", errOut)
	}
	if _, _, code := mamRun(t, "", "ct", filepath.Join(t.TempDir(), "khong-co")); code != 1 {
		t.Error("a file that does not exist was not an error")
	}
}

func TestMamIsInTheSubcommandList(t *testing.T) {
	var out, errb strings.Builder
	if code := run(&out, &errb, []string{"help"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "mam") {
		t.Errorf("mam is not listed:\n%s", out.String())
	}

	out.Reset()
	if code := run(&out, &errb, []string{"mam", "help"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "ct") {
		t.Errorf("ct is not in the mam usage:\n%s", out.String())
	}
}

func TestMamRefusesASubcommandItDoesNotHave(t *testing.T) {
	var out, errb strings.Builder
	if code := run(&out, &errb, []string{"mam", "zone"}); code == 0 {
		t.Error("an unknown subcommand exited zero")
	}
	if !strings.Contains(errb.String(), "unknown subcommand") {
		t.Errorf("the error does not say what happened: %q", errb.String())
	}
}
