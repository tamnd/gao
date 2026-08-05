package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/dem"
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

// Nothing here downloads a tokenizer. What a fetch does is settled in dem
// against the digest, and what is left for the command is the table.

func TestDemIsInTheHelpAndHasItsOwnUsage(t *testing.T) {
	out, _, code := exec(t, "help")
	if code != 0 {
		t.Fatalf("gao help: exit %d, want 0", code)
	}
	if !strings.Contains(out, "dem") {
		t.Errorf("dem is not in the command list:\n%s", out)
	}

	_, errOut, code := exec(t, "dem")
	if code != 2 {
		t.Errorf("gao dem with no subcommand: exit %d, want 2", code)
	}
	for _, want := range []string{"model", "counts", "keys", "overlap", "tokenizer"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the dem usage does not mention %q:\n%s", want, errOut)
		}
	}
}

func TestDemRejectsAnUnknownSubcommand(t *testing.T) {
	_, errOut, code := exec(t, "dem", "sift")
	if code != 2 {
		t.Errorf("gao dem sift: exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "sift") {
		t.Errorf("the error does not name the subcommand:\n%s", errOut)
	}
}

// With no -o the command prints the pin and touches the network for nothing,
// which is what makes it safe to assert on here.
func TestDemModelPrintsThePinAndDownloadsNothing(t *testing.T) {
	out, _, code := exec(t, "dem", "model")
	if code != 0 {
		t.Fatalf("gao dem model: exit %d, want 0", code)
	}
	for _, want := range []string{
		dem.Gemma3.Digest,
		"262144",
		"gated",
		"-o PATH",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the model output does not mention %q:\n%s", want, out)
		}
	}
}

func TestDemCountsNeedsADirectory(t *testing.T) {
	_, _, code := exec(t, "dem", "counts")
	if code != 2 {
		t.Errorf("gao dem counts with no directory: exit %d, want 2", code)
	}
}

func TestDemCountsSaysWhenADirectoryHasNoCounts(t *testing.T) {
	_, errOut, code := exec(t, "dem", "counts", t.TempDir())
	if code != 1 {
		t.Errorf("gao dem counts on an empty directory: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "no counts") {
		t.Errorf("the error does not say the directory has no counts:\n%s", errOut)
	}
}

// writeCounts puts a report where the command will find it.
func writeCounts(t *testing.T, r dem.Report) string {
	t.Helper()
	dir := t.TempDir()
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDemCountsPrintsWhatAnIngestCounted(t *testing.T) {
	dir := writeCounts(t, dem.Report{
		Box:       "server1",
		Tokenizer: "gemma-3",
		Finished:  time.Now().UTC(),
		Sources: []dem.SourceCounts{
			{Source: doc.SourceGlotCC, Counts: dem.Counts{Documents: 500000, Bytes: 1320, Chars: 1000, Syllables: 200, Tokens: 330}},
		},
		Natural: dem.Counts{Documents: 500000, Bytes: 1320, Chars: 1000, Syllables: 200, Tokens: 330},
		Total:   dem.Counts{Documents: 500000, Bytes: 1320, Chars: 1000, Syllables: 200, Tokens: 330},
	})

	out, _, code := exec(t, "dem", "counts", dir)
	if code != 0 {
		t.Fatalf("gao dem counts: exit %d, want 0", code)
	}
	for _, want := range []string{
		"counted on server1",
		"gemma-3",
		"glotcc",
		"500000",
		"3.03", // 1000 characters over 330 tokens
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the counts do not mention %q:\n%s", want, out)
		}
	}
}

// A zero in a token column reads as a measurement, so it prints a dash.
func TestDemCountsPrintsADashRatherThanZeroTokens(t *testing.T) {
	dir := writeCounts(t, dem.Report{
		Box:      "server2",
		Finished: time.Now().UTC(),
		Sources: []dem.SourceCounts{
			{Source: doc.SourceGlotCC, Counts: dem.Counts{Documents: 10, Chars: 100, Syllables: 20}},
		},
		Natural: dem.Counts{Documents: 10, Chars: 100, Syllables: 20},
		Total:   dem.Counts{Documents: 10, Chars: 100, Syllables: 20},
	})

	out, _, code := exec(t, "dem", "counts", dir)
	if code != 0 {
		t.Fatalf("gao dem counts: exit %d, want 0", code)
	}
	if !strings.Contains(out, "no tokenizer") {
		t.Errorf("an untokenized report should say so:\n%s", out)
	}
	if strings.Contains(out, "0") && strings.Contains(out, "0.00") {
		t.Errorf("an untokenized report should print no ratio at all:\n%s", out)
	}
	// The token and ratio columns are dashes, and the rest of the table is real.
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "glotcc") {
			continue
		}
		if !strings.HasSuffix(strings.TrimRight(line, " "), "-") {
			t.Errorf("the glotcc line should end in a dash for the ratio, and it is %q", line)
		}
		if !strings.Contains(line, "20") {
			t.Errorf("the syllable count is missing from %q", line)
		}
	}
}

func TestDemCountsAddsUpSeveralBoxes(t *testing.T) {
	one := writeCounts(t, dem.Report{
		Box: "server1", Tokenizer: "gemma-3", Finished: time.Now().UTC(),
		Sources: []dem.SourceCounts{{Source: doc.SourceGlotCC, Counts: dem.Counts{Documents: 10, Tokens: 100}}},
	})
	two := writeCounts(t, dem.Report{
		Box: "server2", Tokenizer: "gemma-3", Finished: time.Now().UTC(),
		Sources: []dem.SourceCounts{{Source: doc.SourceGlotCC, Counts: dem.Counts{Documents: 5, Tokens: 50}}},
	})

	out, _, code := exec(t, "dem", "counts", one, two)
	if code != 0 {
		t.Fatalf("gao dem counts on two boxes: exit %d, want 0", code)
	}
	if !strings.Contains(out, "server1, server2") {
		t.Errorf("the header does not name both boxes:\n%s", out)
	}
	if !strings.Contains(out, "150") {
		t.Errorf("the tokens were not added up:\n%s", out)
	}
}

func TestDemCountsRefusesToAddUpTwoTokenizers(t *testing.T) {
	one := writeCounts(t, dem.Report{Box: "server1", Tokenizer: "gemma-3", Finished: time.Now().UTC()})
	two := writeCounts(t, dem.Report{Box: "server2", Tokenizer: "llama-3", Finished: time.Now().UTC()})

	_, errOut, code := exec(t, "dem", "counts", one, two)
	if code != 1 {
		t.Errorf("gao dem counts across two tokenizers: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "different tokenizers") {
		t.Errorf("the error does not say why the sum is refused:\n%s", errOut)
	}
}

// Design rule 2 as it reaches the terminal. The corpus line and the total line
// are different numbers and the output says which is which.
func TestDemCountsSeparatesTheCorpusFromTheTotal(t *testing.T) {
	dir := writeCounts(t, dem.Report{
		Box: "server1", Tokenizer: "gemma-3", Finished: time.Now().UTC(),
		Sources: []dem.SourceCounts{
			{Source: doc.SourceGlotCC, Counts: dem.Counts{Documents: 10, Chars: 300, Tokens: 100}},
			{Source: doc.SourceSynth, Counts: dem.Counts{Documents: 5, Chars: 150, Tokens: 50}},
		},
		Natural: dem.Counts{Documents: 10, Chars: 300, Tokens: 100},
		Total:   dem.Counts{Documents: 15, Chars: 450, Tokens: 150},
	})

	out, _, code := exec(t, "dem", "counts", dir)
	if code != 0 {
		t.Fatalf("gao dem counts: exit %d, want 0", code)
	}
	for _, want := range []string{"corpus", "total", "never in a headline"} {
		if !strings.Contains(out, want) {
			t.Errorf("the counts do not mention %q:\n%s", want, out)
		}
	}
}

func TestPrintCountsWithNoReportsSaysSo(t *testing.T) {
	var buf bytes.Buffer
	printCounts(&buf, dem.Report{}, nil)
	if !strings.Contains(buf.String(), "no counts") {
		t.Errorf("printing nothing should say so:\n%s", buf.String())
	}
}

// The tokenizer is loaded before the first byte is fetched, because the ingest
// after it runs for days and a wrong path should not cost a download.
func TestHFRefusesABadTokenizerBeforeItFetchesAnything(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-tokenizer")
	if err := os.WriteFile(path, []byte("this is not a protobuf"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := exec(t, "gat", "hf", "-dir", dir, "-source", "glotcc", "-tokenizer", path)
	if code != 1 {
		t.Fatalf("gao gat hf with a bad tokenizer: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "gao dem model") {
		t.Errorf("the error does not say how to get a tokenizer:\n%s", errOut)
	}
}

func TestHFMentionsCountingInItsUsage(t *testing.T) {
	_, errOut, code := exec(t, "gat", "hf", "-h")
	if code != 2 && code != 0 {
		t.Fatalf("gao gat hf -h: exit %d", code)
	}
	for _, want := range []string{"-tokenizer", "counts.json", "gao dem"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the hf usage does not mention %q:\n%s", want, errOut)
		}
	}
}

// keyFile writes a key file the way a pass over the store would, so the command
// is tested against the format rather than against a fixture that agrees with it.
func keyFile(t *testing.T, name string, texts ...string) string {
	t.Helper()
	dir := t.TempDir()
	b := dem.NewBuilder(dir)
	for _, s := range texts {
		if err := b.Add(doc.SumString(s)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	path := filepath.Join(dir, name+dem.KeysExt)
	if _, err := b.Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return path
}

func TestDemOverlapNeedsKeyFiles(t *testing.T) {
	_, errOut, code := exec(t, "dem", "overlap")
	if code != 2 {
		t.Errorf("gao dem overlap with no files: exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "FILE") {
		t.Errorf("the usage does not say what it wants:\n%s", errOut)
	}
}

func TestDemOverlapPrintsWhatTheSourcesHaveInCommon(t *testing.T) {
	a := keyFile(t, "glotcc", "một", "hai", "ba", "bốn")
	b := keyFile(t, "fineweb2", "ba", "bốn", "năm")

	out, errOut, code := exec(t, "dem", "overlap", a, b)
	if code != 0 {
		t.Fatalf("gao dem overlap: exit %d, want 0\n%s", code, errOut)
	}
	for _, want := range []string{"glotcc", "fineweb2", "glotcc and fineweb2", "50.0%", "66.7%"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table does not carry %q:\n%s", want, out)
		}
	}
}

// The matrix is something a release note quotes, so it has to come out in a form
// something other than a terminal can read.
func TestDemOverlapPrintsJSON(t *testing.T) {
	a := keyFile(t, "a", "một", "hai")
	b := keyFile(t, "b", "hai")

	out, errOut, code := exec(t, "dem", "overlap", "-json", a, b)
	if code != 0 {
		t.Fatalf("gao dem overlap -json: exit %d, want 0\n%s", code, errOut)
	}
	var m dem.Matrix
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("the output is not json: %v\n%s", err, out)
	}
	if m.Distinct != 2 || m.Both("a", "b") != 1 {
		t.Errorf("the matrix came out as %+v", m)
	}
}

func TestDemOverlapSaysWhenAKeyFileIsNotOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notkeys"+dem.KeysExt)
	if err := os.WriteFile(path, []byte("this is not a key file, and it is long enough to look like one"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := exec(t, "dem", "overlap", path)
	if code != 1 {
		t.Errorf("gao dem overlap on a file that is not one: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "key file") {
		t.Errorf("the error does not say what is wrong:\n%s", errOut)
	}
}

func TestPrintOverlapWithOneSourceSaysThereIsNothingToCompare(t *testing.T) {
	m, err := dem.Measure(keyFile(t, "only", "một", "hai"))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	printOverlap(&b, m)
	if !strings.Contains(b.String(), "nothing to compare") {
		t.Errorf("one source printed as if it had a pair:\n%s", b.String())
	}
}

// A share of nothing prints as a zero rather than as a rounded nothing, because
// 0.0% reads as small and 0% reads as none.
func TestPercentPrintsZeroAsZero(t *testing.T) {
	if got := percent(0); got != "0%" {
		t.Errorf("percent(0) = %q, want %q", got, "0%")
	}
	if got := percent(0.125); got != "12.5%" {
		t.Errorf("percent(0.125) = %q, want %q", got, "12.5%")
	}
}

func TestDemKeysSaysWhenThereIsNoSuchRepo(t *testing.T) {
	_, errOut, code := exec(t, "dem", "keys", "-repo", "vietnamese-nothing", "snapshot")
	if code != 1 {
		t.Errorf("gao dem keys on an unknown repo: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "vietnamese-nothing") {
		t.Errorf("the error does not name the repo:\n%s", errOut)
	}
}

func TestDemKeysExplainsWhatItMovesAndWhatItDoesNot(t *testing.T) {
	_, errOut, code := exec(t, "dem", "keys", "-h")
	if code != 2 && code != 0 {
		t.Errorf("gao dem keys -h: exit %d", code)
	}
	for _, want := range []string{"doc_id", "resumable", "SNAPSHOT"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the usage does not mention %q:\n%s", want, errOut)
		}
	}
}

// A run over a large source takes days, and its counts are on disk from the
// first file. Reading them mid run is the point, and quoting them as a source
// total is the mistake the last line of the report exists to prevent.
func TestDemCountsSaysWhenARunHadNotFinished(t *testing.T) {
	dir := writeCounts(t, dem.Report{
		Box:      "server2",
		Finished: time.Now().UTC(),
		Sources: []dem.SourceCounts{
			{Source: doc.SourceFineWeb2, Counts: dem.Counts{Documents: 561137, Chars: 1000, Syllables: 200}},
		},
		Natural: dem.Counts{Documents: 561137, Chars: 1000, Syllables: 200},
		Total:   dem.Counts{Documents: 561137, Chars: 1000, Syllables: 200},
	})

	out, _, code := exec(t, "dem", "counts", dir)
	if code != 0 {
		t.Fatalf("gao dem counts: exit %d, want 0", code)
	}
	if !strings.Contains(out, "server2 was still running") {
		t.Errorf("the report does not say the run had not ended:\n%s", out)
	}
	if !strings.Contains(out, "not a source total") {
		t.Errorf("the report does not say what the number is not:\n%s", out)
	}
}

// And says nothing of the kind about a run that ended, since a finished count
// carrying a caveat is a count nobody quotes.
func TestDemCountsIsQuietAboutAFinishedRun(t *testing.T) {
	dir := writeCounts(t, dem.Report{
		Box:      "server1",
		Complete: true,
		Finished: time.Now().UTC(),
		Sources: []dem.SourceCounts{
			{Source: doc.SourceGlotCC, Counts: dem.Counts{Documents: 500000, Chars: 1000, Syllables: 200}},
		},
		Natural: dem.Counts{Documents: 500000, Chars: 1000, Syllables: 200},
		Total:   dem.Counts{Documents: 500000, Chars: 1000, Syllables: 200},
	})

	out, _, code := exec(t, "dem", "counts", dir)
	if code != 0 {
		t.Fatalf("gao dem counts: exit %d, want 0", code)
	}
	if strings.Contains(out, "still running") {
		t.Errorf("a finished run was reported as running:\n%s", out)
	}
}

// Four boxes, one of them still going, makes the sum a prefix of the corpus.
func TestDemCountsNamesEveryBoxThatWasStillGoing(t *testing.T) {
	done := writeCounts(t, dem.Report{
		Box:      "server1",
		Complete: true,
		Finished: time.Now().UTC(),
		Sources: []dem.SourceCounts{
			{Source: doc.SourceGlotCC, Counts: dem.Counts{Documents: 10, Chars: 100, Syllables: 20}},
		},
		Natural: dem.Counts{Documents: 10, Chars: 100, Syllables: 20},
		Total:   dem.Counts{Documents: 10, Chars: 100, Syllables: 20},
	})
	going := writeCounts(t, dem.Report{
		Box:      "server3",
		Finished: time.Now().UTC(),
		Sources: []dem.SourceCounts{
			{Source: doc.SourceFinePDFs, Counts: dem.Counts{Documents: 5, Chars: 50, Syllables: 10}},
		},
		Natural: dem.Counts{Documents: 5, Chars: 50, Syllables: 10},
		Total:   dem.Counts{Documents: 5, Chars: 50, Syllables: 10},
	})

	out, _, code := exec(t, "dem", "counts", done, going)
	if code != 0 {
		t.Fatalf("gao dem counts: exit %d, want 0", code)
	}
	if !strings.Contains(out, "server3 was still running") {
		t.Errorf("the box that was still going is not named:\n%s", out)
	}
	if strings.Contains(out, "server1 was still running") {
		t.Errorf("the box that had finished was named as running:\n%s", out)
	}
}

// Nothing below reaches a store. What a pass over one does is settled in dem,
// and what is left for the command is the plan it prints before it starts and
// the verdict it prints at the end.

func TestDemVerifyIsInTheUsageAndSaysWhatItMoves(t *testing.T) {
	_, errOut, code := exec(t, "dem")
	if code != 2 {
		t.Fatalf("gao dem with no subcommand: exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "verify") {
		t.Errorf("verify is not in the dem usage:\n%s", errOut)
	}

	_, errOut, _ = exec(t, "dem", "verify", "-h")
	for _, want := range []string{"n_chars", "twelve bytes per document", "sample", "uniformly a little off"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the verify usage does not mention %q:\n%s", want, errOut)
		}
	}
}

func TestDemVerifyRejectsALevelThatIsNotOne(t *testing.T) {
	_, errOut, code := exec(t, "dem", "verify", "-level", "everything")
	if code != 2 {
		t.Errorf("gao dem verify at an unknown level: exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "everything") {
		t.Errorf("the error does not name the level asked for:\n%s", errOut)
	}
}

func TestDemVerifySaysWhenThereIsNoSuchRepo(t *testing.T) {
	_, errOut, code := exec(t, "dem", "verify", "-repo", "vietnamese-nothing")
	if code != 1 {
		t.Errorf("gao dem verify on an unknown repo: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "vietnamese-nothing") {
		t.Errorf("the error does not name the repo:\n%s", errOut)
	}
}

// plan is a snapshot the size of the one this protocol was written for.
func plan(t *testing.T, documents int64) dem.Plan {
	t.Helper()
	parts := make([]kho.Stored, 400)
	for i := range parts {
		parts[i] = kho.Stored{Path: kho.StagePath("hplt-v3-0f2b4c1d9e", i, 0), Bytes: 700 << 20}
	}
	return dem.Planned("hplt-v3-0f2b4c1d9e", parts, documents, 0.05, 0.99, "s1-2026-08")
}

// The plan is the argument. Somebody deciding whether to run this needs the two
// halves priced separately and the alternative priced beside them, before the
// run rather than during it.
func TestTheVerifyPlanPricesBothLevelsAgainstTheDownloadItReplaces(t *testing.T) {
	var b bytes.Buffer
	printVerifyPlan(&b, plan(t, 4_000_000), "tamnd/gao-work", 100)
	out := b.String()

	for _, want := range []string{"level one", "level two", "s1-2026-08", "5.0%", "99.0%", "tamnd/gao-work"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not mention %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "6.5 hours at 100 Mbit") {
		t.Errorf("the plan does not price the download it replaces:\n%s", out)
	}
}

// A plan with no report to size level one from still names its sample, because
// level two is the half that costs real money and it costs the same either way.
func TestAVerifyPlanWithNothingClaimedYetStillPricesTheSample(t *testing.T) {
	var b bytes.Buffer
	printVerifyPlan(&b, plan(t, 0), "tamnd/gao-work", 100)
	out := b.String()

	if !strings.Contains(out, "no report yet") {
		t.Errorf("the plan does not say why level one has no size on it:\n%s", out)
	}
	if !strings.Contains(out, "90 parts read in full") {
		t.Errorf("the plan does not price level two:\n%s", out)
	}
}

// A budget without the rate it assumed is a budget for one person's broadband.
func TestABudgetNamesTheRateItAssumed(t *testing.T) {
	const gb = 1 << 30
	for _, tc := range []struct {
		bytes int64
		mbit  float64
		want  string
	}{
		{100 * gb, 100, "2.4 hours at 100 Mbit"},
		{700 * gb, 100, "16.7 hours at 100 Mbit"},
		{700 * 400 * gb, 1000, "27.8 days at 1000 Mbit"},
		{48 * gb, 0, "no budget without a link rate"},
		{1 << 20, 100, "under a minute at 100 Mbit"},
	} {
		if got := budget(tc.bytes, tc.mbit); got != tc.want {
			t.Errorf("budget(%d, %g) = %q, want %q", tc.bytes, tc.mbit, got, tc.want)
		}
	}
}

func TestPrintDifferencesSaysTheCountsAreTheOnesInTheStore(t *testing.T) {
	c := dem.Counts{Documents: 1000, Chars: 40000, Syllables: 9000}
	var b bytes.Buffer
	ok := printDifferences(&b, dem.Compare(
		dem.Report{Sources: []dem.SourceCounts{{Source: doc.SourceGlotCC, Counts: c}}},
		map[doc.Source]dem.Counts{doc.SourceGlotCC: c},
	))
	if !ok {
		t.Errorf("a report that matches the store was reported as a difference:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "are the counts in the store") {
		t.Errorf("the verdict is not in the output:\n%s", b.String())
	}
}

// The failure this exists for: a report written from a run that stopped early,
// which is a real number about less corpus than there is.
func TestPrintDifferencesNamesTheColumnThatIsOff(t *testing.T) {
	claimed := dem.Counts{Documents: 900, Chars: 40000, Syllables: 9000}
	stored := dem.Counts{Documents: 1000, Chars: 44000, Syllables: 9000}

	var b bytes.Buffer
	ok := printDifferences(&b, dem.Compare(
		dem.Report{Sources: []dem.SourceCounts{{Source: doc.SourceGlotCC, Counts: claimed}}},
		map[doc.Source]dem.Counts{doc.SourceGlotCC: stored},
	))
	if ok {
		t.Fatalf("a report a hundred documents short of the store agreed with it:\n%s", b.String())
	}
	out := b.String()
	if !strings.Contains(out, "differs on") || !strings.Contains(out, "documents, chars") {
		t.Errorf("the output does not name the columns that are off:\n%s", out)
	}
	if strings.Contains(out, "syllables,") {
		t.Errorf("a column that matched is named as one that did not:\n%s", out)
	}
}

func TestPrintSpotSaysWhichDocumentAndWhichColumn(t *testing.T) {
	var b bytes.Buffer
	printSpot(&b, dem.Spot{
		Part:      "data/hplt-v3-0f2b4c1d9e/f00003-p00012.parquet",
		Documents: 40000,
		Wrong:     12,
		Mismatches: []dem.Mismatch{
			{Row: 7, DocID: "3f2a91c4", Column: "n_chars", Stored: 940, Counted: 900},
		},
	})
	out := b.String()
	for _, want := range []string{"f00003-p00012.parquet", "row 7", "3f2a91c4", "n_chars", "940", "900", "11 more"} {
		if !strings.Contains(out, want) {
			t.Errorf("the mismatch line does not mention %q:\n%s", want, out)
		}
	}
}

// The sample is a bound and not a proof, and the output has to say so where the
// number is rather than in a footnote somewhere.
func TestPrintSpotsSaysWhatTheSampleDoesNotProve(t *testing.T) {
	counted := dem.Counts{Documents: 40000, Bytes: 1 << 30, Chars: 700_000_000, Syllables: 160_000_000}
	spots := []dem.Spot{{
		Part:      "data/hplt-v3-0f2b4c1d9e/f00003-p00012.parquet",
		Documents: 40000,
		Checked:   []string{"n_chars", "n_syllables"},
		Counted:   counted,
	}}

	var b bytes.Buffer
	if !printSpots(&b, spots, 0.05, 0.99) {
		t.Fatalf("a sample with nothing wrong in it failed:\n%s", b.String())
	}
	out := b.String()
	for _, want := range []string{"n_chars, n_syllables", "no more than 5.0%", "99.0% confidence", "uniformly a little off"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary does not mention %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "bytes per character") {
		t.Errorf("the sample is the only place a byte count is measured and it is not reported:\n%s", out)
	}
}

func TestPrintSpotsFailsWhenAColumnDoesNotDescribeItsText(t *testing.T) {
	var b bytes.Buffer
	ok := printSpots(&b, []dem.Spot{{
		Part:      "data/hplt-v3-0f2b4c1d9e/f00003-p00012.parquet",
		Documents: 40000,
		Checked:   []string{"n_chars"},
		Counted:   dem.Counts{Documents: 40000, Bytes: 1 << 30, Chars: 700_000_000},
		Wrong:     12,
	}}, 0.05, 0.99)
	if ok {
		t.Fatalf("a part with twelve wrong documents in it passed:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "12 documents across 1 parts") {
		t.Errorf("the summary does not count what was wrong:\n%s", b.String())
	}
}

// A dash rather than a zero, for the same reason the counts table does it: a
// zero in a token column reads as a measurement.
func TestPrintShapeDoesNotClaimAByteCountItCannotHave(t *testing.T) {
	var b bytes.Buffer
	printShape(&b, "hplt-v3-0f2b4c1d9e", dem.Counts{Documents: 1000, Chars: 40000, Syllables: 9000})
	out := b.String()
	if !strings.Contains(out, "tokens     -") {
		t.Errorf("a run with no token column printed a number for it:\n%s", out)
	}
	if !strings.Contains(out, "nothing stores the byte length") {
		t.Errorf("the output does not say why there is no byte count:\n%s", out)
	}
}
