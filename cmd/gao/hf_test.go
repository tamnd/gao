package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/gzip"

	"github.com/tamnd/gao/dem"
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
		fmt.Sprintf("0 of %d files done", gat.Files()),
		may.GB(gat.TotalBytes()),
		fmt.Sprintf("%d files to fetch", gat.Files()),
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
	done := len(p.Files)
	if !strings.Contains(out, fmt.Sprintf("%d of %d files done", done, gat.Files())) {
		t.Errorf("the plan does not count the finished source:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d files to fetch", gat.Files()-done)) {
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

// onlyList writes a file selection list and returns the path to it. The shape is
// what 'gao giao files -box NAME' prints, one name per line and nothing else.
func onlyList(t *testing.T, names ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mine.txt")
	if err := os.WriteFile(path, []byte(strings.Join(names, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The difference between -only and -limit, which is the whole reason -only
// exists. A limit takes the first files of whatever is left, so four boxes given
// the same limit fetch the same files four times. A list takes the files that
// were named, wherever they sit in the manifest, which is what lets a schedule
// be handed out.
func TestOnlyTakesTheFilesItWasNamedRatherThanTheFirstOnes(t *testing.T) {
	sources := gat.Sources()
	first, last := sources[0], sources[len(sources)-1]
	a := first.Files[len(first.Files)-1]
	b := last.Files[len(last.Files)-1]
	path := onlyList(t,
		string(first.Source)+"/"+a.Path,
		string(last.Source)+"/"+b.Path)

	out, errOut, code := exec(t, "gat", "hf", "-dir", t.TempDir(), "-only", path, "-plan")
	if code != 0 {
		t.Fatalf("gao gat hf -only: exit %d, %s", code, errOut)
	}
	want := fmt.Sprintf("names 2 files, 2 left to fetch, %s to move", may.GB(a.Bytes+b.Bytes))
	if !strings.Contains(out, want) {
		t.Errorf("the run does not say %q:\n%s", want, out)
	}

	// Neither of the two is near the front of the manifest, so a limit of two
	// picks two other files and moves a different number of bytes.
	limited, _, code := exec(t, "gat", "hf", "-dir", t.TempDir(), "-limit", "2", "-plan")
	if code != 0 {
		t.Fatalf("gao gat hf -limit 2: exit %d", code)
	}
	if strings.Contains(limited, may.GB(a.Bytes+b.Bytes)) {
		t.Errorf("a limit of two takes the same bytes as the list, so this proves nothing:\n%s", limited)
	}
}

// A list that names nothing and a list that names files the manifest does not
// pin are both refused, because the alternative is a run that fetches nothing
// and exits 0, which on a terminal is indistinguishable from a box that is
// already finished.
//
// Both are refused before the ledger is opened, so a box handed the wrong path
// leaves nothing behind. That is what the ledger check at the end of each case
// is for.
func TestOnlyRefusesAListThatWouldFetchNothing(t *testing.T) {
	p, ok := gat.Pin(doc.SourceHPLT3)
	if !ok {
		t.Fatal("hplt3 is not pinned")
	}

	for _, tc := range []struct {
		name  string
		list  []string
		wants []string
	}{
		{
			name:  "empty",
			list:  []string{},
			wants: []string{"names no files", "nothing left to do"},
		},
		{
			name:  "not pinned",
			list:  []string{string(p.Source) + "/" + p.Files[0].Path, "hplt3/vie_Latn/999_9.jsonl.zst"},
			wants: []string{"names 1 file this manifest does not pin", "999_9"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, errOut, code := exec(t, "gat", "hf", "-dir", dir, "-only", onlyList(t, tc.list...), "-plan")
			if code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
			for _, want := range tc.wants {
				if !strings.Contains(errOut, want) {
					t.Errorf("the refusal does not say %q: %q", want, errOut)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, gat.LedgerName)); err == nil {
				t.Error("a refused list still opened a ledger, so the check runs later than it should")
			}
		})
	}
}

// A list every file of which is in the ledger is a finished hand rather than a
// mistake, and it exits 0. This is the ordinary end of a run on a box that was
// handed nineteen files and fetched all nineteen.
func TestAListWhoseFilesAreAllDoneIsAFinishedHand(t *testing.T) {
	dir := t.TempDir()
	p, ok := gat.Pin(doc.SourceGlotCC)
	if !ok {
		t.Fatal("glotcc is not pinned")
	}

	l, err := gat.OpenLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range p.Files[:2] {
		if err := l.Record(gat.Entry{
			Source: p.Source, Revision: p.Revision, Path: f.Path, Bytes: f.Bytes,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	path := onlyList(t,
		string(p.Source)+"/"+p.Files[0].Path,
		string(p.Source)+"/"+p.Files[1].Path)
	out, errOut, code := exec(t, "gat", "hf", "-dir", dir, "-only", path, "-plan")
	if code != 0 {
		t.Fatalf("exit %d, %s", code, errOut)
	}
	if !strings.Contains(out, "names 2 files, 0 left to fetch") {
		t.Errorf("a finished hand does not read as finished:\n%s", out)
	}
}

// The two commands agree about what a file is called. This is the join the
// schedule was missing: 'gao giao files -box NAME' writes the list and 'gao gat
// hf -only' takes it, with nothing in between to edit it by hand.
func TestTheScheduleTheSplitPrintsIsTheOneTheFetcherTakes(t *testing.T) {
	names, errOut, code := exec(t, "giao", "files", "-box", "server3", giaoFleet(t))
	if code != 0 {
		t.Fatalf("gao giao files -box: exit %d, %s", code, errOut)
	}
	path := filepath.Join(t.TempDir(), "server3.txt")
	if err := os.WriteFile(path, []byte(names), 0o600); err != nil {
		t.Fatal(err)
	}

	want := len(strings.Fields(names))
	out, errOut, code := exec(t, "gat", "hf", "-dir", t.TempDir(), "-only", path, "-plan")
	if code != 0 {
		t.Fatalf("gao gat hf -only: exit %d, %s", code, errOut)
	}
	if want == 0 || want >= gat.Files() {
		t.Fatalf("the schedule hands server3 %d of the %d pinned files, which is not a share of it", want, gat.Files())
	}
	if !strings.Contains(out, fmt.Sprintf("names %d files, %d left to fetch", want, want)) {
		t.Errorf("the fetcher does not take all %d files the split named:\n%s", want, out)
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

// madlad400 is not in this list because it is dropped, not because it has no
// decoder. It has one, it decodes every record, and every record is then turned
// away for provenance it does not carry, which is the reason it is dropped.
func TestHFDecodesTheSourcesItHasAMappingFor(t *testing.T) {
	for _, name := range []string{"hplt3", "fineweb2", "finepdfs", "glotcc"} {
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
	if !strings.Contains(errOut, may.TokenEnv) {
		t.Errorf("the usage does not name %s:\n%s", may.TokenEnv, errOut)
	}

	// Which sources decode is in the usage rather than only in the error,
	// because it decides whether a run is worth starting.
	for _, want := range []string{"-decode", "-rejects", "-only", "gao giao files -box NAME", "CulturaX", "Parquet", "range request"} {
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

// The counts are on disk before the first byte is fetched and rewritten after
// every file. The version that wrote them once at the end left the previous
// run's counts.json in the directory for the whole of a run that takes days,
// and a stale report is worse than a missing one because it parses.
func TestTheCountsAreOnDiskBeforeTheRunEnds(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	var tally dem.Tally

	saveCounts(&stderr, dir, "server1", &tally, false)
	r, err := dem.ReadReport(dir)
	if err != nil {
		t.Fatalf("nothing was written before the run ended: %v", err)
	}
	if r.Box != "server1" {
		t.Errorf("the counts say they came from %q", r.Box)
	}
	if r.Complete {
		t.Error("a run that had not ended reported itself finished")
	}

	saveCounts(&stderr, dir, "server1", &tally, true)
	r, err = dem.ReadReport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Complete {
		t.Error("a run that ended still reports itself running")
	}
	if stderr.Len() != 0 {
		t.Errorf("writing the counts complained: %s", stderr.String())
	}
}

// The counts measure a download that is already paid for, so failing to write
// them is reported and does not end the run.
func TestCountsThatCannotBeWrittenDoNotStopTheRun(t *testing.T) {
	var stderr bytes.Buffer
	var tally dem.Tally

	saveCounts(&stderr, filepath.Join(t.TempDir(), "no-such-directory"), "server1", &tally, false)
	if !strings.Contains(stderr.String(), "writing the counts") {
		t.Errorf("a failed write said nothing: %q", stderr.String())
	}
}

// A directory with counts in it and no ledger is one somebody has half cleared
// out, not one to resume, and adding its counts to a fresh run would report a
// corpus twice the size of what is in the store.
func TestCountsAreCarriedOnlyWhenTheLedgerNamesTheFiles(t *testing.T) {
	dir := t.TempDir()
	var earlier dem.Tally
	count := earlier.Counting(nil, nil)
	if err := count(&doc.Document{Provenance: doc.Provenance{Source: doc.SourceFineWeb2}, Text: "Việt Nam"}); err != nil {
		t.Fatal(err)
	}
	if err := earlier.Report("server1", time.Now()).Write(dir); err != nil {
		t.Fatal(err)
	}

	ledger, err := gat.OpenLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()

	var tally dem.Tally
	var out bytes.Buffer
	carried, err := seedCounts(&out, dir, ledger, &tally)
	if err != nil {
		t.Fatal(err)
	}
	if carried.Documents != 0 {
		t.Errorf("an empty ledger reported %d documents carried", carried.Documents)
	}
	if got := tally.Natural(); got.Documents != 0 {
		t.Errorf("a run with an empty ledger carried %d documents forward from the counts file", got.Documents)
	}

	// And with the ledger naming a file, the same counts are carried.
	if err := ledger.Record(gat.Entry{Source: doc.SourceFineWeb2, Revision: "af9c13333eb9", Path: "data/vie_Latn/train/000_00000.parquet", Documents: 1}); err != nil {
		t.Fatal(err)
	}
	var resumed dem.Tally
	carried, err = seedCounts(&out, dir, ledger, &resumed)
	if err != nil {
		t.Fatal(err)
	}
	if carried.Documents != 1 {
		t.Errorf("seedCounts reported %d documents carried, want 1", carried.Documents)
	}
	if got := resumed.Natural(); got.Documents != 1 {
		t.Errorf("a run resuming a ledger of one file carried %d documents forward, want 1", got.Documents)
	}
	if !strings.Contains(out.String(), "carrying 1 documents") {
		t.Errorf("the run said %q, and a number it carried forward silently is a number nobody can check", out.String())
	}
}

// The summary prints what this run admitted and then what the counts file holds,
// and once a run can resume those are two different bodies of text. server3
// finished a resumed GlotCC batch saying "1500000 documents admitted" and
// "37.9 GB of text" one line apart, which is three files and nine files.
func TestAResumedRunSaysHowMuchOfTheTextItRead(t *testing.T) {
	var tally dem.Tally
	count := tally.Counting(nil, nil)
	for range 3 {
		if err := count(&doc.Document{Provenance: doc.Provenance{Source: doc.SourceGlotCC}, Text: "Việt Nam"}); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	printTally(&out, &tally, dem.Counts{})
	if strings.Contains(out.String(), "came off earlier runs") {
		t.Errorf("a run that started from nothing split its total anyway: %q", out.String())
	}

	out.Reset()
	printTally(&out, &tally, dem.Counts{Documents: 2, Bytes: 20})
	if !strings.Contains(out.String(), "of which 20 B came off earlier runs and 10 B was read by this one") {
		t.Errorf("the run said %q, and the line above it counts only what this run admitted", out.String())
	}
}
