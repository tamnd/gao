package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/may"
	"os"
	"path/filepath"
)

func TestGatPinsPrintsEverySourceAndItsRevision(t *testing.T) {
	out, _, code := exec(t, "gat", "pins")
	if code != 0 {
		t.Fatalf("gao gat pins: exit %d, want 0", code)
	}
	for _, p := range gat.Sources() {
		if !strings.Contains(out, string(p.Source)) {
			t.Errorf("gao gat pins omitted %s", p.Source)
		}
		if !strings.Contains(out, p.Repo) {
			t.Errorf("gao gat pins omitted the repo for %s", p.Source)
		}
		// The point of a pin is the revision, so an abbreviation short enough to
		// be ambiguous would defeat the command.
		if !strings.Contains(out, shortRevision(p.Revision)) {
			t.Errorf("gao gat pins omitted the revision for %s", p.Source)
		}
		if !strings.Contains(out, p.Class.String()) {
			t.Errorf("gao gat pins omitted the license class for %s", p.Source)
		}
	}
	if !strings.Contains(out, gat.PinnedOn()) {
		t.Error("gao gat pins does not say when the manifest was pinned")
	}
	if !strings.Contains(out, may.GB(gat.TotalBytes())) {
		t.Error("gao gat pins does not say how large the download is")
	}
}

// Gated is the one property that stops an ingest before it starts, so it has to
// be visible in the summary rather than only in the detail view.
func TestGatPinsSaysWhichSourceIsGated(t *testing.T) {
	out, _, code := exec(t, "gat", "pins")
	if code != 0 {
		t.Fatalf("gao gat pins: exit %d, want 0", code)
	}
	if !strings.Contains(out, "gated") {
		t.Error("gao gat pins does not mark the gated source")
	}
}

func TestGatPinsPrintsOneSourceInFull(t *testing.T) {
	out, _, code := exec(t, "gat", "pins", "-source", "hplt3")
	if code != 0 {
		t.Fatalf("gao gat pins -source hplt3: exit %d, want 0", code)
	}
	p, ok := gat.Pin("hplt3")
	if !ok {
		t.Fatal("hplt3 is not pinned")
	}
	// In full means the whole revision rather than the abbreviation, because
	// this is the view somebody copies from.
	if !strings.Contains(out, p.Revision) {
		t.Error("the detail view abbreviates the revision")
	}
	if !strings.Contains(out, p.Note) {
		t.Error("the detail view omits the note")
	}
	for _, f := range p.Files {
		if !strings.Contains(out, f.Path) {
			t.Errorf("the detail view omits %s", f.Path)
		}
	}

	_, errOut, code := exec(t, "gat", "pins", "-source", "nothing")
	if code == 0 {
		t.Error("gao gat pins accepted a source that is not pinned")
	}
	if !strings.Contains(errOut, "nothing") {
		t.Errorf("the error does not name the source asked for: %q", errOut)
	}
}

func TestGatPinsListsEveryFileOnRequest(t *testing.T) {
	out, _, code := exec(t, "gat", "pins", "-files")
	if code != 0 {
		t.Fatalf("gao gat pins -files: exit %d, want 0", code)
	}
	var missing int
	for _, p := range gat.Sources() {
		for _, f := range p.Files {
			if !strings.Contains(out, f.Path) {
				missing++
			}
		}
	}
	if missing > 0 {
		t.Errorf("gao gat pins -files omitted %d of %d files", missing, gat.Files())
	}
	if !strings.Contains(out, "held back") {
		t.Error("gao gat pins -files does not say what the manifest holds back")
	}
}

func TestGatRejectsWhatItCannotDo(t *testing.T) {
	for _, args := range [][]string{
		{"gat"},
		{"gat", "sang"},
		{"gat", "pins", "extra"},
		{"gat", "drift", "extra"},
		{"gat", "hf", "extra"},
		{"gat", "ledger", "extra"},
		// The one that matters. A bare fetch with no directory would start a
		// 608.9 GB download into whatever directory it was run from, and it did
		// exactly that once before this became an error.
		{"gat", "hf"},
		{"gat", "ledger"},
	} {
		if _, _, code := exec(t, args...); code != 2 {
			t.Errorf("gao %s: exit %d, want 2", strings.Join(args, " "), code)
		}
	}
	out, _, code := exec(t, "gat", "help")
	if code != 0 {
		t.Fatalf("gao gat help: exit %d, want 0", code)
	}
	for _, sub := range []string{"pins", "drift", "hf", "ledger"} {
		if !strings.Contains(out, sub) {
			t.Errorf("gao gat help omits %s", sub)
		}
	}
}

func TestGatIsInTheTopLevelHelp(t *testing.T) {
	out, _, code := exec(t, "help")
	if code != 0 {
		t.Fatalf("gao help: exit %d, want 0", code)
	}
	if !strings.Contains(out, "gat") {
		t.Error("gao help does not list gat")
	}
}

// The drift check reads the network, so the test does not run it. What it can
// check without a network is that the manifest has somewhere to read every
// revision from, which is the part that silently rots.
func TestDriftHasAHostToAskForEverySource(t *testing.T) {
	for _, p := range gat.Sources() {
		if p.RevisionURL == "" {
			t.Errorf("%s has no address to check for drift", p.Source)
		}
	}
}

// The exit code is the whole interface when this runs on a schedule, so it is
// the part worth testing rather than the table above it.
func TestDriftReportsMovedLoudlyAndUnreachableSeparately(t *testing.T) {
	unchanged := driftResult{
		Source: doc.SourceHPLT3,
		Drift:  gat.Drift{Source: doc.SourceHPLT3, Pinned: "sha256:aaaa", Current: "sha256:aaaa"},
	}
	moved := driftResult{
		Source: doc.SourceFineWeb2,
		Drift: gat.Drift{
			Source:  doc.SourceFineWeb2,
			Pinned:  "af9c13333eb981300149d5ca60a8e9d659b276b9",
			Current: "0123456789abcdef0123456789abcdef01234567",
		},
	}
	broken := driftResult{Source: doc.SourceCulturaX, Err: errors.New("502 Bad Gateway")}

	for _, tc := range []struct {
		name     string
		in       []driftResult
		code     int
		out      string
		errOut   string
		notOut   string
		quietErr bool
	}{
		{
			name: "nothing moved",
			in:   []driftResult{unchanged, unchanged},
			code: 0,
			out:  "still serve the revision",
		},
		{
			name:   "something moved",
			in:     []driftResult{unchanged, moved},
			code:   1,
			out:    "have moved upstream",
			notOut: "still serve the revision",
		},
		{
			name:   "a host is down",
			in:     []driftResult{unchanged, broken},
			code:   1,
			errOut: "could not be reached",
		},
		{
			// A source that moved is the finding. A source that is down is
			// noise on the same run, and it must not bury the finding.
			name:     "a host is down and another moved",
			in:       []driftResult{broken, moved},
			code:     1,
			out:      "have moved upstream",
			quietErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := reportDrift(&stdout, &stderr, tc.in); got != tc.code {
				t.Errorf("reportDrift = %d, want %d", got, tc.code)
			}
			if tc.out != "" && !strings.Contains(stdout.String(), tc.out) {
				t.Errorf("stdout does not mention %q:\n%s", tc.out, stdout.String())
			}
			if tc.notOut != "" && strings.Contains(stdout.String(), tc.notOut) {
				t.Errorf("stdout mentions %q, which is the wrong conclusion:\n%s", tc.notOut, stdout.String())
			}
			if tc.errOut != "" && !strings.Contains(stderr.String(), tc.errOut) {
				t.Errorf("stderr does not mention %q:\n%s", tc.errOut, stderr.String())
			}
			if tc.quietErr && stderr.String() != "" {
				t.Errorf("the unreachable host is reported as well as the one that moved:\n%s", stderr.String())
			}
			for _, r := range tc.in {
				if !strings.Contains(stdout.String(), string(r.Source)) {
					t.Errorf("the report omits %s", r.Source)
				}
			}
		})
	}
}

func TestARevisionShortensWithoutLosingWhatItIs(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"af9c13333eb981300149d5ca60a8e9d659b276b9", "af9c13333eb9"},
		{"sha256:5b2785d5b11c576f98e4f5df4e6918ab1ebebae1f3aad353c39d856928a77cf2", "sha256:5b2785d5b11c"},
		{"short", "short"},
		{"sha256:abc", "sha256:abc"},
		{"", ""},
	} {
		if got := shortRevision(tc.in); got != tc.want {
			t.Errorf("shortRevision(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A source that is pinned and not fetched is in the table with the reason, not
// missing from it. A reader comparing the manifest against the dataset list
// should find an answer rather than a gap.
func TestPinsShowsADroppedSourceAndSaysWhy(t *testing.T) {
	out, _, code := exec(t, "gat", "pins")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out, "madlad400") {
		t.Errorf("the dropped source is missing from the table:\n%s", out)
	}
	if !strings.Contains(out, "dropped") {
		t.Errorf("the table does not say the source is dropped:\n%s", out)
	}
	if !strings.Contains(out, may.GB(gat.DroppedBytes())) {
		t.Errorf("the header does not say how much is pinned and not fetched:\n%s", out)
	}

	out, _, code = exec(t, "gat", "pins", "-source", "madlad400")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	for _, want := range []string{"dropped", "text"} {
		if !strings.Contains(out, want) {
			t.Errorf("the pin does not mention %q:\n%s", want, out)
		}
	}
}

// Asking for a dropped source by name is deliberate, so the answer is the
// reason rather than an empty plan and a clean exit.
func TestHFRefusesADroppedSourceWithTheReason(t *testing.T) {
	_, errOut, code := exec(t, "gat", "hf", "-dir", t.TempDir(), "-source", "madlad400", "-plan")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	for _, want := range []string{"madlad400", "dropped", "text"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the refusal does not mention %q: %q", want, errOut)
		}
	}
}

// The default plan is the sources that are fetched, and a dropped source is not
// one of them.
func TestTheDefaultPlanLeavesOutADroppedSource(t *testing.T) {
	out, _, code := exec(t, "gat", "hf", "-dir", t.TempDir(), "-plan")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out, may.GB(gat.TotalBytes())) {
		t.Errorf("the plan is not the download total:\n%s", out)
	}
	if strings.Contains(out, may.GB(gat.TotalBytes()+gat.DroppedBytes())) {
		t.Errorf("the plan includes a source that is not fetched:\n%s", out)
	}
}

// The identity has to be printable, because the answer to a webmaster asking
// what our crawler calls itself cannot be a grep through the source.
func TestGatAgentPrintsTheTokenAndTheHeaderSeparately(t *testing.T) {
	out, _, code := exec(t, "gat", "agent")
	if code != 0 {
		t.Fatalf("gao gat agent: exit %d\n%s", code, out)
	}

	for _, want := range []string{gat.Bot, gat.Contact, "User-agent: " + gat.Bot, "Disallow: /"} {
		if !strings.Contains(out, want) {
			t.Errorf("gao gat agent does not print %q:\n%s", want, out)
		}
	}
	// The header carries the version and the token does not, and printing them
	// on one line would lose exactly that distinction.
	if !strings.Contains(out, gat.Agent(version)) {
		t.Errorf("gao gat agent does not print the header it sends:\n%s", out)
	}
}

func TestGatAgentTakesNoArguments(t *testing.T) {
	if _, _, code := exec(t, "gat", "agent", "extra"); code != 2 {
		t.Errorf("gao gat agent with an argument: exit %d, want 2", code)
	}
}

func TestGatAgentIsInTheSubcommandList(t *testing.T) {
	out, _, code := exec(t, "gat", "help")
	if code != 0 {
		t.Fatalf("gao gat help: exit %d", code)
	}
	if !strings.Contains(out, "agent") {
		t.Errorf("agent is not in the gat subcommand list:\n%s", out)
	}
}

// gao gat fetch is the crawler doing one page, and what it has to print is the
// part of that a person cannot see in the page itself.
func fetchSite(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func html(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

func TestGatFetchPrintsTheDecisionAndNotJustThePage(t *testing.T) {
	s := fetchSite(t, map[string]http.HandlerFunc{
		"/robots.txt": html("User-agent: *\nAllow: /\n"),
		"/bai-viet":   html("<p>xin chào</p>"),
	})

	out, errOut, code := exec(t, "gat", "fetch", "-delay", "1ms", s.URL+"/bai-viet")
	if code != 0 {
		t.Fatalf("gao gat fetch: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"robots", "status", "200", "mining", "next"} {
		if !strings.Contains(out, want) {
			t.Errorf("gao gat fetch does not print %q:\n%s", want, out)
		}
	}
	// The page itself is not the output. A summary that quietly included the
	// body would be a summary nobody could read.
	if strings.Contains(out, "xin chào") {
		t.Errorf("gao gat fetch printed the page instead of what happened to it:\n%s", out)
	}
}

func TestGatFetchRefusesWhatRobotsRefusesAndSaysWhy(t *testing.T) {
	s := fetchSite(t, map[string]http.HandlerFunc{
		"/robots.txt":       html("User-agent: gaobot\nDisallow: /thanh-vien/\n"),
		"/thanh-vien/nguoi": html("<p>ho so</p>"),
	})

	out, errOut, code := exec(t, "gat", "fetch", "-delay", "1ms", s.URL+"/thanh-vien/nguoi")
	if code != 1 {
		t.Fatalf("a disallowed URL: exit %d, want 1\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "robots") {
		t.Errorf("the refusal does not say it came from robots.txt:\n%s", errOut)
	}
	if !strings.Contains(errOut, "Disallow: /thanh-vien/") {
		t.Errorf("the refusal does not name the rule that caused it:\n%s", errOut)
	}
}

func TestGatFetchWithBodyWritesTheBytesAndNothingElse(t *testing.T) {
	const page = "<p>xin chào</p>"
	s := fetchSite(t, map[string]http.HandlerFunc{
		"/robots.txt": html("User-agent: *\nAllow: /\n"),
		"/bai-viet":   html(page),
	})

	out, _, code := exec(t, "gat", "fetch", "-delay", "1ms", "-body", s.URL+"/bai-viet")
	if code != 0 {
		t.Fatalf("gao gat fetch -body: exit %d", code)
	}
	if out != page {
		t.Errorf("gao gat fetch -body wrote %q, want %q", out, page)
	}
}

func TestGatFetchNeedsSomethingToFetch(t *testing.T) {
	if _, _, code := exec(t, "gat", "fetch"); code != 2 {
		t.Errorf("gao gat fetch with no URL: exit %d, want 2", code)
	}
}

func TestGatFetchIsInTheSubcommandList(t *testing.T) {
	out, _, code := exec(t, "gat", "help")
	if code != 0 {
		t.Fatalf("gao gat help: exit %d", code)
	}
	if !strings.Contains(out, "fetch") {
		t.Errorf("fetch is not in the gat subcommand list:\n%s", out)
	}
}

// A WARC is the difference between an extraction bug that costs a rerun of the
// parser and one that costs a rerun of the crawl. These tests are about the two
// halves of that being connected: what -warc writes, gao gat warc reads.
func TestAFetchWritesAWARCThatComesBackOut(t *testing.T) {
	const page = "<p>xin chào, đây là bài viết</p>"
	s := fetchSite(t, map[string]http.HandlerFunc{
		"/robots.txt": html("User-agent: *\nAllow: /\n"),
		"/bai-viet":   html(page),
	})
	path := filepath.Join(t.TempDir(), "gao.warc.gz")

	out, errOut, code := exec(t, "gat", "fetch", "-delay", "1ms", "-warc", path, s.URL+"/bai-viet")
	if code != 0 {
		t.Fatalf("gao gat fetch -warc: exit %d\n%s\n%s", code, out, errOut)
	}

	out, errOut, code = exec(t, "gat", "warc", "-uri", s.URL+"/bai-viet", path)
	if code != 0 {
		t.Fatalf("gao gat warc -uri: exit %d\n%s\n%s", code, out, errOut)
	}
	if out != page {
		t.Errorf("the page came back out as %q, want %q", out, page)
	}
}

func TestTheListingSaysWhatIsInTheFile(t *testing.T) {
	s := fetchSite(t, map[string]http.HandlerFunc{
		"/robots.txt": html("User-agent: *\nAllow: /\n"),
		"/mot":        html("<p>mot</p>"),
		"/hai":        html("<p>hai</p>"),
	})
	path := filepath.Join(t.TempDir(), "gao.warc.gz")

	if _, errOut, code := exec(t, "gat", "fetch", "-delay", "1ms", "-warc", path, s.URL+"/mot", s.URL+"/hai"); code != 0 {
		t.Fatalf("gao gat fetch -warc: exit %d\n%s", code, errOut)
	}

	out, _, code := exec(t, "gat", "warc", path)
	if code != 0 {
		t.Fatalf("gao gat warc: exit %d\n%s", code, out)
	}
	// A warcinfo, and a request and a response for each of the two pages.
	if !strings.Contains(out, "warcinfo") {
		t.Errorf("the listing does not mention who wrote the file:\n%s", out)
	}
	if !strings.Contains(out, "5 records, 2 of them pages") {
		t.Errorf("the count is wrong:\n%s", out)
	}
	for _, want := range []string{"/mot", "/hai"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is not in the listing:\n%s", want, out)
		}
	}
}

// The fields are where the parts of a fetch that are not bytes ended up: the
// robots rule that allowed it, what the response said about mining, and the
// lengths the site sent before the transport decompressed them.
func TestTheFieldsCarryWhatTheSummaryWouldHaveLost(t *testing.T) {
	s := fetchSite(t, map[string]http.HandlerFunc{
		"/robots.txt": html("User-agent: *\nAllow: /tin/\n"),
		"/tin/mot": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("X-Robots-Tag", "noai")
			_, _ = w.Write([]byte("<p>mot</p>"))
		},
	})
	path := filepath.Join(t.TempDir(), "gao.warc.gz")

	if _, errOut, code := exec(t, "gat", "fetch", "-delay", "1ms", "-warc", path, s.URL+"/tin/mot"); code != 0 {
		t.Fatalf("gao gat fetch -warc: exit %d\n%s", code, errOut)
	}

	out, _, code := exec(t, "gat", "warc", "-fields", path)
	if code != 0 {
		t.Fatalf("gao gat warc -fields: exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"WARC-Payload-Digest",
		"WARC-Concurrent-To",
		"X-Gao-Robots",
		"X-Gao-Reservation-Said",
		"gaobot",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is not in the fields:\n%s", want, out)
		}
	}
}

func TestAskingForAPageTheCrawlNeverFetched(t *testing.T) {
	s := fetchSite(t, map[string]http.HandlerFunc{
		"/robots.txt": html("User-agent: *\nAllow: /\n"),
		"/mot":        html("<p>mot</p>"),
	})
	path := filepath.Join(t.TempDir(), "gao.warc.gz")

	if _, errOut, code := exec(t, "gat", "fetch", "-delay", "1ms", "-warc", path, s.URL+"/mot"); code != 0 {
		t.Fatalf("gao gat fetch -warc: exit %d\n%s", code, errOut)
	}

	out, errOut, code := exec(t, "gat", "warc", "-uri", s.URL+"/hai", path)
	if code == 0 {
		t.Errorf("a page that is not in the file was reported as found:\n%s", out)
	}
	if !strings.Contains(errOut, "no response for") {
		t.Errorf("the error does not say what was missing: %q", errOut)
	}
}

func TestReadingSomethingThatIsNotAWARC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "khong-phai.warc")
	if err := os.WriteFile(path, []byte("day khong phai la mot warc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := exec(t, "gat", "warc", path)
	if code == 0 {
		t.Error("a file that is not a WARC was read as one")
	}
	if !strings.Contains(errOut, "WARC") {
		t.Errorf("the error does not say what was wrong: %q", errOut)
	}
}

func TestGatWARCNeedsAFile(t *testing.T) {
	if _, _, code := exec(t, "gat", "warc"); code != 2 {
		t.Error("gao gat warc with no file did not report the usage")
	}
	if _, errOut, code := exec(t, "gat", "warc", filepath.Join(t.TempDir(), "khong-co")); code != 1 {
		t.Errorf("a file that does not exist: exit %d, want 1: %s", code, errOut)
	}
}

func TestGatWARCIsInTheSubcommandList(t *testing.T) {
	out, _, code := exec(t, "gat", "help")
	if code != 0 {
		t.Fatalf("gao gat help: exit %d", code)
	}
	if !strings.Contains(out, "warc") {
		t.Errorf("warc is not in the gat subcommand list:\n%s", out)
	}
}
