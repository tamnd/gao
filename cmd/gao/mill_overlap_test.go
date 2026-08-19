package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamnd/gao/mill"
)

// The number the matrix exists to produce. Two sources that both carry the same
// article publish three documents and hold two, and the sum everybody quotes is
// off by the difference.
func TestMillOverlapCountsASharedDocumentOnce(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "hplt.txt", millArticle),
		writeText(t, dir, "fineweb2.txt", millSyndicated),
		writeText(t, dir, "glotcc.txt", millRiver),
	}

	var stdout, stderr bytes.Buffer
	if code := runMill(&stdout, &stderr, append([]string{"-overlap", "-json"}, files...)); code != 0 {
		t.Fatalf("gao mill -overlap = %d, want 0\n%s", code, stderr.String())
	}
	var m mill.Matrix
	if err := json.Unmarshal(stdout.Bytes(), &m); err != nil {
		t.Fatalf("the matrix is not JSON: %v\n%s", err, stdout.String())
	}
	if m.Sum != 3 {
		t.Errorf("the sources counted one at a time came to %d, want 3", m.Sum)
	}
	if m.Union != 2 {
		t.Errorf("three files from three sources hold %d distinct documents, want 2", m.Union)
	}
	if got := m.Inflation(); got < 1.4 || got > 1.6 {
		t.Errorf("inflation came back %.2f, want three counted where two are held", got)
	}
}

// A source is read off the file rather than typed at the shell, so the matrix
// says which source each row is talking about.
func TestMillOverlapNamesTheSources(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "hplt.txt", millArticle),
		writeText(t, dir, "fineweb2.txt", millRiver),
	}

	var stdout, stderr bytes.Buffer
	if code := runMill(&stdout, &stderr, append([]string{"-overlap"}, files...)); code != 0 {
		t.Fatalf("gao mill -overlap = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"hplt.txt", "fineweb2.txt", "the source on the left"} {
		if !strings.Contains(out, want) {
			t.Errorf("the matrix does not print %q:\n%s", want, out)
		}
	}
}

// Two measurements, one command, and running both at once would print one and
// silently drop the other.
func TestMillRefusesToRunTwoMeasurementsAtOnce(t *testing.T) {
	dir := t.TempDir()
	file := writeText(t, dir, "a.txt", millArticle)

	var stdout, stderr bytes.Buffer
	if code := runMill(&stdout, &stderr, []string{"-overlap", "-curve", file}); code != 2 {
		t.Errorf("gao mill -overlap -curve = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Run one of them") {
		t.Errorf("the refusal does not say what to do instead: %q", stderr.String())
	}
}
