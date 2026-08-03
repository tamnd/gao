package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/may"
)

// The tests here never fetch anything. What a real fetch does is settled in the
// gat package against test hosts, and what is left for the command is the part
// that decides whether to fetch at all: the plan, the source flag, the limit,
// and the refusal to start without a directory.

func TestTheHFPlanSaysWhatIsLeftAndFetchesNothing(t *testing.T) {
	dir := t.TempDir()
	out, _, code := exec(t, "gat", "hf", "-dir", dir, "-plan")
	if code != 0 {
		t.Fatalf("gao gat hf -plan: exit %d, want 0", code)
	}
	for _, want := range []string{
		"0 of 154 files done",
		may.GB(gat.TotalBytes()),
		"154 files to fetch",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not mention %q:\n%s", want, out)
		}
	}

	// A plan opens the ledger, because a plan that did not would report every
	// file as still to do on a box that has already fetched half of them.
	if _, err := os.Stat(filepath.Join(dir, gat.LedgerName)); err != nil {
		t.Errorf("the plan left no ledger: %v", err)
	}
}

func TestTheHFPlanSkipsWhatTheLedgerAlreadyHas(t *testing.T) {
	dir := t.TempDir()
	p, ok := gat.Pin(doc.SourceHPLT3)
	if !ok {
		t.Fatal("hplt3 is not pinned")
	}

	l, err := gat.OpenLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range p.Files {
		if err := l.Record(gat.Entry{
			Source: p.Source, Revision: p.Revision, Path: f.Path, Bytes: f.Bytes,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "gat", "hf", "-dir", dir, "-source", "hplt3", "-plan")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out, "nothing left to fetch") {
		t.Errorf("a source that is fully fetched still has work:\n%s", out)
	}

	out, _, code = exec(t, "gat", "hf", "-dir", dir, "-plan")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out, "12 of 154 files done") {
		t.Errorf("the plan does not count the finished source:\n%s", out)
	}
	if !strings.Contains(out, "142 files to fetch") {
		t.Errorf("the plan does not subtract the finished source:\n%s", out)
	}
}

func TestTheHFLimitBoundsWhatARunWillTake(t *testing.T) {
	dir := t.TempDir()
	out, _, code := exec(t, "gat", "hf", "-dir", dir, "-limit", "3", "-plan")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out, "stopping after 3 files") {
		t.Errorf("the limit is not reported, so a short run would look like a full one:\n%s", out)
	}

	// A limit larger than the work is not an error and does not lie about it.
	out, _, code = exec(t, "gat", "hf", "-dir", dir, "-source", "finepdfs", "-limit", "99", "-plan")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if strings.Contains(out, "stopping after") {
		t.Errorf("a limit nothing hit was reported:\n%s", out)
	}
}

func TestHFRefusesASourceItHasNoPinFor(t *testing.T) {
	_, errOut, code := exec(t, "gat", "hf", "-dir", t.TempDir(), "-source", "commoncrawl", "-plan")
	if code == 0 {
		t.Error("gao gat hf accepted a source that is not pinned")
	}
	if !strings.Contains(errOut, "commoncrawl") {
		t.Errorf("the error does not name the source asked for: %q", errOut)
	}
}

func TestHFRefusesALedgerItCannotRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, gat.LedgerName), []byte("this is not an entry\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := exec(t, "gat", "hf", "-dir", dir, "-plan")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "not an ingest entry") {
		t.Errorf("the error does not say what is wrong: %q", errOut)
	}
}

func TestTheLedgerCommandReportsPerSourceAndInTotal(t *testing.T) {
	dir := t.TempDir()
	out, _, code := exec(t, "gat", "ledger", "-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out, "nothing fetched yet") {
		t.Errorf("an empty ledger reads as:\n%s", out)
	}

	l, err := gat.OpenLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Record(gat.Entry{
		Source: doc.SourceHPLT3, Revision: "sha256:a", Path: "vie_Latn/5_1.jsonl.zst",
		Bytes: 15_049_231_912, Documents: 12_000_000, Reconnects: 3, Box: "server1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	out, _, code = exec(t, "gat", "ledger", "-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	for _, want := range []string{"hplt3", "12000000", "total", may.GB(15_049_231_912)} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
	// One file of the twelve, and the twelve has to be visible or the number
	// reads as progress against nothing.
	if !strings.Contains(out, "12") {
		t.Errorf("the report does not say how many files the source has:\n%s", out)
	}

	out, _, code = exec(t, "gat", "ledger", "-dir", dir, "-files")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	for _, want := range []string{"vie_Latn/5_1.jsonl.zst", "server1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the file listing does not mention %q:\n%s", want, out)
		}
	}
}

// A finished file is one line, and over 154 files across several days that line
// is the only thing anybody watches, so what is on it is worth asserting.
func TestAFetchedFileReportsWhatItCost(t *testing.T) {
	p, ok := gat.Pin(doc.SourceHPLT3)
	if !ok {
		t.Fatal("hplt3 is not pinned")
	}

	var w bytes.Buffer
	printFetched(&w, gat.Report{
		Pin: p, File: p.Files[1], Documents: 12_000_000, Reconnects: 3,
		Elapsed: 90 * 60 * 1e9,
	})
	line := w.String()
	for _, want := range []string{"hplt3", p.Files[1].Path, may.GB(p.Files[1].Bytes), "12000000 documents", "3 reconnects"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line does not mention %q: %s", want, line)
		}
	}

	// A transfer nothing interrupted should not carry a zero it invites a reader
	// to interpret.
	w.Reset()
	printFetched(&w, gat.Report{Pin: p, File: p.Files[1], Elapsed: 1e9})
	if strings.Contains(w.String(), "reconnects") {
		t.Errorf("a clean transfer reported reconnects: %s", w.String())
	}
	if strings.Contains(w.String(), "documents") {
		t.Errorf("a sink that counted nothing reported documents: %s", w.String())
	}

	w.Reset()
	printFetched(&w, gat.Report{Pin: p, File: p.Files[1], Err: os.ErrDeadlineExceeded, Elapsed: 1e9})
	if !strings.Contains(w.String(), "failed") {
		t.Errorf("a failed file does not read as failed: %s", w.String())
	}
}

func TestAnInterruptedRunIsNotAFailedOne(t *testing.T) {
	var stderr bytes.Buffer
	// Ctrl-C is somebody stopping a transfer they meant to stop, and everything
	// it finished is in the ledger, so the next run picks up from there.
	if code := hfError(&stderr, context.Canceled); code != 0 {
		t.Errorf("a canceled run exits %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "ledger has everything that finished") {
		t.Errorf("a canceled run says %q", stderr.String())
	}

	stderr.Reset()
	if code := hfError(&stderr, gat.ErrGated); code != 1 {
		t.Errorf("a gated source exits %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "gated") {
		t.Errorf("a gated failure says %q", stderr.String())
	}

	stderr.Reset()
	if code := hfError(&stderr, os.ErrDeadlineExceeded); code != 1 {
		t.Errorf("a timed out run exits %d, want 1", code)
	}
}

func TestHFIsInTheGatHelpAndTheUsage(t *testing.T) {
	out, _, code := exec(t, "gat", "help")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	for _, want := range []string{"hf", "ledger", "resuming"} {
		if !strings.Contains(out, want) {
			t.Errorf("gao gat help does not mention %q:\n%s", want, out)
		}
	}

	// The usage has to name the token variable, because a gated source fails at
	// the first fetch and this is where somebody looks.
	_, errOut, code := exec(t, "gat", "hf", "-h")
	if code != 2 {
		t.Errorf("gao gat hf -h: exit %d, want 2", code)
	}
	if !strings.Contains(errOut, gat.TokenEnv) {
		t.Errorf("the usage does not name %s:\n%s", gat.TokenEnv, errOut)
	}
}
