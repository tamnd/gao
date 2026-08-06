package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// xoaRun runs a gao xoa command against a register written for the test, and
// pins the clock, because a report about response times that changes answer
// depending on the day the tests run is a report that gets ignored.
func xoaRun(t *testing.T, register string, args ...string) (string, string, int) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "GO-BO.toml")
	if err := os.WriteFile(path, []byte(register), 0o600); err != nil {
		t.Fatalf("writing the register: %v", err)
	}

	old := now
	now = func() time.Time { return time.Date(2026, 3, 20, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })

	var stdout, stderr bytes.Buffer
	code := runXoa(&stdout, &stderr, append([]string{args[0], "-file", path}, args[1:]...))
	return stdout.String(), stderr.String(), code
}

// field pulls one line of the summary out, so the tests are about the numbers
// rather than about how wide tabwriter made a column today.
func field(t *testing.T, out, name string) string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if rest, ok := strings.CutPrefix(line, name); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Errorf("the status has no %q line:\n%s", name, out)
	return ""
}

const filed = `
[[request]]
issue = 90
host = "example.vn"
scope = "stop"
asked = 2026-03-01T09:00:00Z
stopped = 2026-03-02T15:00:00Z

[[request]]
issue = 91
host = "cham.vn"
scope = "erase"
asked = 2026-03-10T09:00:00Z
`

func TestXoaStatusReportsWhatWasPromisedAndWhatHappened(t *testing.T) {
	stdout, _, code := xoaRun(t, filed, "status")
	if code == 0 {
		t.Error("a request ten days past a 72 hour promise exited 0")
	}
	for _, want := range []string{"issue 91", "cham.vn"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the status is missing %q:\n%s", want, stdout)
		}
	}
	for _, c := range []struct{ name, want string }{
		{"filed", "2 requests"},
		{"open", "1"},
		{"late", "1 past 72h0m0s"},
		{"worst", "30h0m0s"},
	} {
		if got := field(t, stdout, c.name); got != c.want {
			t.Errorf("%s came out as %q, want %q", c.name, got, c.want)
		}
	}
	// The one that was honored is not in the list, because the list is what is
	// still owed rather than a log.
	if strings.Contains(stdout, "issue 90") {
		t.Errorf("a finished request was reported as outstanding:\n%s", stdout)
	}
}

func TestXoaStatusCanShowTheWholeRecord(t *testing.T) {
	stdout, _, _ := xoaRun(t, filed, "status", "-all")
	if !strings.Contains(stdout, "issue 90") || !strings.Contains(stdout, "stopped in 30h0m0s") {
		t.Errorf("-all did not print the honored request:\n%s", stdout)
	}
}

// The one worth having. A report over an empty register that prints a median of
// zero and everything honored describes a system that has never done anything
// as one that has never failed.
func TestXoaStatusOverAnEmptyRegisterSaysSo(t *testing.T) {
	stdout, _, code := xoaRun(t, "", "status")
	if code != 0 {
		t.Errorf("an empty register exited %d", code)
	}
	for _, name := range []string{"worst", "median"} {
		if got := field(t, stdout, name); got != "not measured, because nothing has been filed" {
			t.Errorf("an empty register reported a %s of %q", name, got)
		}
	}
}

// P03-8 is a gate on the fraction of crawled hosts that objected, so the
// denominator has to be said out loud rather than assumed.
func TestXoaStatusReportsTheObjectionRate(t *testing.T) {
	stdout, _, _ := xoaRun(t, filed, "status", "-crawled", "1000")
	if !strings.Contains(stdout, "0.200% of 1000 hosts crawled") {
		t.Errorf("the rate is missing:\n%s", stdout)
	}
}

func TestXoaCheckFindsARowThatCannotBeTrue(t *testing.T) {
	_, stderr, code := xoaRun(t, `
[[request]]
issue = 92
host = "a.vn"
scope = "stop"
asked = 2026-03-05T09:00:00Z
stopped = 2026-03-01T09:00:00Z
`, "check")
	if code != 1 {
		t.Errorf("a register with the dates the wrong way round exited %d", code)
	}
	if !strings.Contains(stderr, "stopped before it was asked") {
		t.Errorf("the check did not say what was wrong:\n%s", stderr)
	}
}

func TestXoaCheckPassesAGoodRegister(t *testing.T) {
	stdout, stderr, code := xoaRun(t, filed, "check")
	if code != 0 {
		t.Errorf("a well formed register exited %d:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "2 requests, nothing wrong") {
		t.Errorf("the check said nothing useful:\n%s", stdout)
	}
}

// The register that ships in the repository is the one that matters, since it
// is the file LIEN-HE.md points a reader at.
func TestTheRegisterInTheRepositoryIsReadable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runXoa(&stdout, &stderr, []string{"check", "-file", filepath.Join("..", "..", "GO-BO.toml")}); code != 0 {
		t.Errorf("the register in the repository does not check out: %d\n%s", code, stderr.String())
	}
}

// The two gates are different questions with different answers, which is the
// thing about a takedown path that is easiest to get wrong.
func TestXoaURLAnswersBothGates(t *testing.T) {
	stdout, _, code := xoaRun(t, filed, "url", "-fetched", "2026-01-05", "https://tin.example.vn/bai/1")
	if code != 1 {
		t.Errorf("a blocked URL exited %d", code)
	}
	if !strings.Contains(stdout, "do not fetch, what was published stays") {
		t.Errorf("a stop request took a document published before it:\n%s", stdout)
	}

	stdout, _, _ = xoaRun(t, filed, "url", "-fetched", "2026-03-15", "https://tin.example.vn/bai/1")
	if !strings.Contains(stdout, "do not fetch, do not publish") {
		t.Errorf("a document fetched after the request was kept:\n%s", stdout)
	}
}

func TestXoaURLLeavesAStrangerAlone(t *testing.T) {
	stdout, _, code := xoaRun(t, filed, "url", "https://notexample.vn/a")
	if code != 0 {
		t.Errorf("a site nobody filed against exited %d", code)
	}
	if !strings.Contains(stdout, "nobody has asked us to stop") {
		t.Errorf("the answer for an uncovered URL is missing:\n%s", stdout)
	}
}

func TestXoaURLWantsAURL(t *testing.T) {
	if _, _, code := xoaRun(t, filed, "url"); code != 2 {
		t.Errorf("gao xoa url with nothing to check exited %d", code)
	}
}

func TestXoaOnARegisterThatIsNotThere(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runXoa(&stdout, &stderr, []string{"status", "-file", filepath.Join(t.TempDir(), "khong-co.toml")})
	if code != 1 {
		t.Errorf("a missing register exited %d", code)
	}
	if !strings.Contains(stderr.String(), "khong-co.toml") {
		t.Errorf("the failure did not name the file:\n%s", stderr.String())
	}
}

func TestXoaIsInTheCommandList(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(&stdout, &stderr, []string{"xoa"}); code != 2 {
		t.Errorf("gao xoa with no subcommand exited %d", code)
	}
	if !strings.Contains(stderr.String(), "status") {
		t.Errorf("gao xoa did not print its subcommands:\n%s", stderr.String())
	}

	stdout.Reset()
	if code := run(&stdout, &stderr, nil); code != 0 {
		t.Fatalf("gao with no arguments exited %d", code)
	}
	if !strings.Contains(stdout.String(), "xoa") {
		t.Errorf("xoa is not in the command list:\n%s", stdout.String())
	}
}
