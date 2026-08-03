package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/may"
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
		{"gat", "hf"},
		{"gat", "pins", "extra"},
		{"gat", "drift", "extra"},
	} {
		if _, _, code := exec(t, args...); code != 2 {
			t.Errorf("gao %s: exit %d, want 2", strings.Join(args, " "), code)
		}
	}
	out, _, code := exec(t, "gat", "help")
	if code != 0 {
		t.Fatalf("gao gat help: exit %d, want 0", code)
	}
	for _, sub := range []string{"pins", "drift"} {
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
