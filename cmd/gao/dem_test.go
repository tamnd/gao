package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/dem"
	"github.com/tamnd/gao/doc"
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
	for _, want := range []string{"model", "counts", "tokenizer"} {
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
