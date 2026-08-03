package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

func exec(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(&stdout, &stderr, args)
	return stdout.String(), stderr.String(), code
}

func TestVersionFlagAndSubcommandAgree(t *testing.T) {
	for _, args := range [][]string{{"-version"}, {"--version"}, {"version"}} {
		out, _, code := exec(t, args...)
		if code != 0 {
			t.Fatalf("gao %v: exit %d, want 0", args, code)
		}
		if got := strings.TrimSpace(out); got != version {
			t.Errorf("gao %v printed %q, want %q", args, got, version)
		}
	}
}

func TestPlanListsEverySlice(t *testing.T) {
	out, _, code := exec(t, "plan")
	if code != 0 {
		t.Fatalf("gao plan: exit %d, want 0", code)
	}
	// The release smoke test greps this output for S0 and S9, so a slice that
	// stops appearing here breaks the release pipeline rather than a unit test.
	for _, s := range doc.Slices {
		if !strings.Contains(out, s.ID) {
			t.Errorf("gao plan omitted %s", s.ID)
		}
		if !strings.Contains(out, s.Title) {
			t.Errorf("gao plan omitted the title of %s", s.ID)
		}
	}
}

func TestPlanFullPrintsGatesAndKillCriteria(t *testing.T) {
	out, _, code := exec(t, "plan", "-full")
	if code != 0 {
		t.Fatalf("gao plan -full: exit %d, want 0", code)
	}
	for _, s := range doc.Slices {
		if !strings.Contains(out, s.Gate) {
			t.Errorf("gao plan -full omitted the gate of %s", s.ID)
		}
		if !strings.Contains(out, s.Kill) {
			t.Errorf("gao plan -full omitted the kill criterion of %s", s.ID)
		}
	}
}

func TestUnknownCommandExitsTwo(t *testing.T) {
	_, stderr, code := exec(t, "nau")
	if code != 2 {
		t.Fatalf("gao nau: exit %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr did not explain the failure: %q", stderr)
	}
}

func TestHelpIsTheDefault(t *testing.T) {
	bare, _, bareCode := exec(t)
	help, _, helpCode := exec(t, "help")
	if bareCode != 0 || helpCode != 0 {
		t.Fatalf("bare exit %d, help exit %d, want 0 and 0", bareCode, helpCode)
	}
	if bare != help {
		t.Error("bare invocation and 'gao help' printed different output")
	}
	for _, want := range []string{"plan", "version", "usage: gao"} {
		if !strings.Contains(help, want) {
			t.Errorf("help output is missing %q", want)
		}
	}
}

func TestBadFlagExitsTwo(t *testing.T) {
	if _, _, code := exec(t, "plan", "-nosuchflag"); code != 2 {
		t.Errorf("gao plan -nosuchflag: exit %d, want 2", code)
	}
}
