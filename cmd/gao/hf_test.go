package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/gzip"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/may"
	"github.com/tamnd/gao/vo"
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

// The refusal has to come before the ledger is opened and before a byte moves,
// because the alternative is finding out that a source cannot be decoded after
// two hundred gigabytes of somebody else's bandwidth.
func TestHFRefusesToDecodeASourceItCannotDecode(t *testing.T) {
	for _, args := range [][]string{
		{"gat", "hf", "-dir", t.TempDir(), "-decode", "-plan"},
		{"gat", "hf", "-dir", t.TempDir(), "-source", "culturax", "-decode", "-plan"},
		// -rejects implies -decode, so it is refused on the same grounds.
		{"gat", "hf", "-dir", t.TempDir(), "-rejects", filepath.Join(t.TempDir(), "vo.jsonl.zst"), "-plan"},
	} {
		out, errOut, code := exec(t, args...)
		if code != 1 {
			t.Fatalf("%v: exit %d, want 1", args, code)
		}
		if !strings.Contains(errOut, "no decoder") || !strings.Contains(errOut, "culturax") {
			t.Errorf("%v: the error does not name what cannot be decoded: %q", args, errOut)
		}
		// The way out is in the message, because the person who hit this wants to
		// know what to run next and not what went wrong.
		if !strings.Contains(errOut, "-source") {
			t.Errorf("%v: the error does not say what to do instead: %q", args, errOut)
		}
		if strings.Contains(out, "files to fetch") {
			t.Errorf("%v: a plan was printed for a run that cannot happen:\n%s", args, out)
		}
	}
}

func TestHFDecodesTheSourcesItHasAMappingFor(t *testing.T) {
	for _, name := range []string{"hplt3", "madlad400", "fineweb2", "finepdfs", "glotcc"} {
		out, errOut, code := exec(t, "gat", "hf", "-dir", t.TempDir(), "-source", name, "-decode", "-plan")
		if code != 0 {
			t.Fatalf("%s: exit %d, want 0: %s", name, code, errOut)
		}
		if !strings.Contains(out, "files to fetch") {
			t.Errorf("%s: no plan was printed:\n%s", name, out)
		}
	}
}

// A share outside zero to one is a typo, and the one it usually is turns a one
// percent sample into every reject keeping its text.
func TestARejectSampleThatIsNotAShareIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vo.jsonl.zst")
	for _, sample := range []float64{-0.5, 1.5} {
		if _, _, err := openDocs(path, sample); err == nil {
			t.Errorf("-sample %v was accepted", sample)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the reject store was created before the share was checked")
	}

	docs, closeFn, err := openDocs(path, 1)
	if err != nil {
		t.Fatalf("openDocs: %v", err)
	}
	if docs.Rejects == nil {
		t.Error("a reject path produced a sink with nowhere to put rejects")
	}
	if err := closeFn(); err != nil {
		t.Errorf("closing the reject store: %v", err)
	}
	// The segment has to be finished on the way out, since one with no index
	// cannot be read back.
	seg, err := vo.Open(path)
	if err != nil {
		t.Fatalf("the reject store cannot be read: %v", err)
	}
	if err := seg.Close(); err != nil {
		t.Errorf("closing the segment: %v", err)
	}
}

// The byte counts do not say the thing a decoding run exists to find out, which
// is how many of the records in those bytes gao is allowed to keep.
func TestADecodingRunReportsWhatItKeptAndWhatItTurnedAway(t *testing.T) {
	var w bytes.Buffer

	// A run without -decode has no counts, and printing zeroes for it would read
	// as a run that admitted nothing.
	printAdmitted(&w, nil)
	if w.Len() != 0 {
		t.Errorf("a run that only counted bytes reported documents:\n%s", w.String())
	}

	docs := &gat.Docs{}
	p, ok := gat.Pin(doc.SourceMADLAD400)
	if !ok {
		t.Fatal("madlad400 is not pinned")
	}
	if _, err := docs.Consume(t.Context(), p, p.Files[0], gzipOf(t, madladRecord)); err == nil {
		t.Fatal("a file that admitted nothing was reported as fine")
	}

	printAdmitted(&w, docs)
	for _, want := range []string{"0 documents admitted", "2 turned away", "contract", "2"} {
		if !strings.Contains(w.String(), want) {
			t.Errorf("the summary does not mention %q:\n%s", want, w.String())
		}
	}
	// Only the reasons that happened, because a column of zeroes is how the one
	// number that matters stops being visible.
	if strings.Contains(w.String(), "language") {
		t.Errorf("a reason nothing hit was printed:\n%s", w.String())
	}
}

// madladRecord is two records of the clean split, which is a text field and
// nothing else, so neither of them can satisfy the ingest contract.
const madladRecord = `{"text":"Lực lượng Công an sẽ lập được những chiến công mới trong năm nay."}
{"text":"Theo ông Tạ Văn Thắng, đây là kết quả của rất nhiều năm làm việc."}
`

func gzipOf(t *testing.T, s string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
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

	// Which sources decode is in the usage rather than only in the error,
	// because it decides whether a run is worth starting.
	for _, want := range []string{"-decode", "-rejects", "CulturaX", "Parquet", "range request"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the usage does not mention %q:\n%s", want, errOut)
		}
	}
}

// One ingest at a time in one directory. Two of them do not corrupt the ledger,
// which dedupes on read, they double the bytes and the document totals in it
// while the file count still looks right, and they interleave writes into the
// same document segment.
func TestHFRefusesADirectoryAnotherIngestIsHolding(t *testing.T) {
	dir := t.TempDir()
	lock, err := gat.LockDir(dir, "gao gat hf")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()

	_, errOut, code := exec(t, "gat", "hf", "-dir", dir, "-source", "glotcc", "-limit", "1")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	for _, want := range []string{"another ingest", may.Label(), gat.LockName} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the refusal does not mention %q: %q", want, errOut)
		}
	}
}

// A plan writes nothing, so it stays readable while an ingest is running. That
// is the moment somebody most wants to read it.
func TestHFCanStillPrintThePlanWhileAnIngestHoldsTheDirectory(t *testing.T) {
	dir := t.TempDir()
	lock, err := gat.LockDir(dir, "gao gat hf")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()

	out, _, code := exec(t, "gat", "hf", "-dir", dir, "-source", "glotcc", "-plan")
	if code != 0 {
		t.Fatalf("exit %d, want 0, a plan should not need the lock", code)
	}
	if !strings.Contains(out, "files to fetch") {
		t.Errorf("the plan did not print:\n%s", out)
	}
}

// Whether an ingest is running changes what the ledger totals mean, so the
// read only command says so rather than leaving it to be guessed at.
func TestTheLedgerCommandSaysWhenAnIngestIsRunning(t *testing.T) {
	dir := t.TempDir()
	out, _, code := exec(t, "gat", "ledger", "-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if strings.Contains(out, "an ingest is running") {
		t.Errorf("an unlocked directory reported a running ingest:\n%s", out)
	}

	lock, err := gat.LockDir(dir, "gao gat hf")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()

	out, _, code = exec(t, "gat", "ledger", "-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d with a lock held, want 0, this command claims nothing", code)
	}
	if !strings.Contains(out, "an ingest is running") {
		t.Errorf("a locked directory did not report the ingest:\n%s", out)
	}
	if !strings.Contains(out, may.Label()) {
		t.Errorf("the report does not say which box is holding it:\n%s", out)
	}
}
