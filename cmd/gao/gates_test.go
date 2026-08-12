package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/dem"
)

// The suite itself is tested in dem, against tokenizers written to fail one
// gate each. What is left here is the report, so these build a result by hand
// rather than tokenizing anything. Nothing in this file needs the 4.7 MB model,
// which is the point: how a failure reads on a terminal is worth a test of its
// own and should not be skipped on a box that has not downloaded a protobuf.

func gateReport(gates ...dem.Gate) dem.GateReport {
	return dem.GateReport{
		Tokenizer:   "gemma-3",
		Vocab:       262144,
		Documents:   40,
		Chars:       4000,
		Syllables:   1000,
		Tokens:      1300,
		MBPerSecond: 31.5,
		Gates:       gates,
	}
}

func gatesRun(t *testing.T, report dem.GateReport) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := printGates(&stdout, &stderr, report)
	return stdout.String(), stderr.String(), code
}

func TestDemGatesPrintsEveryGateAndTheFertility(t *testing.T) {
	out, errOut, code := gatesRun(t, gateReport(
		dem.Gate{Name: "T1", What: "decode(encode(x)) is x", Unit: "documents", Checked: 40, Ran: true},
		dem.Gate{Name: "T9", What: "at least 20 MB/s on one core", Note: "31.5 MB/s on one core over 4.0 MB", Ran: true},
		dem.Gate{Name: "T10", What: "no piece is reachable only from text gao would reject", Unit: "pieces", Checked: 261888, Failed: 12, Audit: true, Note: "261888 pieces read, 256 control or byte fallback, 12 for a person to look at"},
	))

	if code != 0 {
		t.Fatalf("a report with nothing failing: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"gemma-3", "262144 pieces", "3.08 characters per token", "1.30 tokens per syllable", "T1", "passed", "T9", "31.5 MB/s", "T10", "audited", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	// The audit is not a pass and it is not a failure either, so it must not
	// make the run one.
	if strings.Contains(errOut, "not eligible") {
		t.Errorf("an audit with findings made the tokenizer ineligible:\n%s", errOut)
	}
}

func TestDemGatesSaysWhichGateFailedAndNamesTheDocuments(t *testing.T) {
	out, errOut, code := gatesRun(t, gateReport(
		dem.Gate{Name: "T1", What: "decode(encode(x)) is x", Unit: "documents", Checked: 40, Ran: true},
		dem.Gate{
			Name: "T5", What: "no token boundary separates a letter from its marks",
			Unit: "boundaries", Checked: 92311, Failed: 4, Ran: true,
			Sample: []string{"3f2a at byte 118, inside \"ế\"", "91bc at byte 44, between \"e\" and its mark"},
		},
	))

	if code != 1 {
		t.Fatalf("a failed gate: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "failed") || !strings.Contains(out, "4 of 92311 boundaries") {
		t.Errorf("the table does not say what T5 found:\n%s", out)
	}
	if !strings.Contains(out, `inside "ế"`) {
		t.Errorf("the report does not name the boundaries it found:\n%s", out)
	}
	for _, want := range []string{"not eligible", "T5 failed 4 of 92311 boundaries"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the failure does not say %q:\n%s", want, errOut)
		}
	}
}

// The failure this is most likely to have in practice: somebody points it at a
// sample that holds nothing a gate applies to, and a run that called that a
// pass would print ten green lines and mean nine things.
func TestDemGatesTreatsAGateWithNothingToRunOnAsAFailure(t *testing.T) {
	out, errOut, code := gatesRun(t, gateReport(
		dem.Gate{Name: "T1", What: "decode(encode(x)) is x", Unit: "documents", Checked: 40, Ran: true},
		dem.Gate{Name: "T3", What: "and on documents mixing Vietnamese, English and code", Unit: "documents", Why: "no document in the sample mixed Vietnamese, English and code"},
	))

	if code != 1 {
		t.Fatalf("a gate with nothing to run on: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "not run") {
		t.Errorf("the table does not mark T3 as not run:\n%s", out)
	}
	if !strings.Contains(errOut, "T3 did not run: no document in the sample mixed") {
		t.Errorf("the failure does not say why T3 could not run:\n%s", errOut)
	}
}

// A boundary gate says nothing about a document whose pieces do not add up to
// the document. Declining quietly would leave a count that reads as coverage.
func TestDemGatesReportsTheDocumentsAGateDeclinedToLookAt(t *testing.T) {
	out, _, _ := gatesRun(t, gateReport(
		dem.Gate{Name: "T4", What: "no token boundary lands inside a character", Unit: "boundaries", Checked: 800, Skipped: 3, Ran: true},
	))

	if !strings.Contains(out, "could not look at 3 documents") {
		t.Errorf("the report hides the documents the gate declined:\n%s", out)
	}
}

func coverageRun(t *testing.T, report dem.GateReport) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := printCoverage(&stdout, &stderr, report)
	return stdout.String(), stderr.String(), code
}

// The coverage set is four kilobytes and T9 declines on it. A run that reported
// that as a failed gate would be telling somebody their tokenizer is too slow
// on the strength of a letter chart.
func TestDemGatesOnTheCoverageSetDoesNotJudgeTheThroughput(t *testing.T) {
	out, errOut, code := coverageRun(t, gateReport(
		dem.Gate{Name: "T1", What: "decode(encode(x)) is x", Unit: "documents", Checked: 8, Ran: true},
		dem.Gate{Name: "T9", What: "at least 20 MB/s on one core", Why: "4227 bytes went through Encode, and a rate over less than 1000000 is a reading of the clock rather than a measurement of the tokenizer"},
		dem.Gate{Name: "T10", What: "no piece is reachable only from text gao would reject", Unit: "pieces", Checked: 261888, Failed: 12, Audit: true},
	))

	if code != 0 {
		t.Fatalf("a coverage run with only T9 unrun: exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "ran and passed on the coverage set") {
		t.Errorf("the report does not say the coverage set passed:\n%s", out)
	}
	if !strings.Contains(out, "this is not eligibility") {
		t.Errorf("the report lets a coverage run read as an eligibility decision:\n%s", out)
	}
	if strings.Contains(errOut, "T9") {
		t.Errorf("T9 declining was reported as something wrong:\n%s", errOut)
	}
}

// Everything else it does judge, and a correctness gate that did not run on the
// set is the set failing rather than the tokenizer.
func TestDemGatesOnTheCoverageSetStillJudgesTheRest(t *testing.T) {
	out, errOut, code := coverageRun(t, gateReport(
		dem.Gate{Name: "T6", What: "NFD and NFC encode the same", Unit: "documents", Checked: 8, Failed: 1, Ran: true},
		dem.Gate{Name: "T9", What: "at least 20 MB/s on one core", Why: "a rate computed from that is a reading of the clock"},
	))

	if code != 1 {
		t.Fatalf("a coverage run with T6 failing: exit %d\n%s", code, out)
	}
	if !strings.Contains(errOut, "T6 failed 1 of 8 documents") {
		t.Errorf("the failure does not say what T6 found:\n%s", errOut)
	}
	if strings.Contains(out, "ran and passed") {
		t.Errorf("a failing coverage run still reported a pass:\n%s", out)
	}
}

func TestDemGatesUsageErrors(t *testing.T) {
	if _, _, code := exec(t, "dem", "gates"); code != 2 {
		t.Errorf("no tokenizer and no files: exit %d, want 2", code)
	}
	if _, _, code := exec(t, "dem", "gates", "-tokenizer", "t.model"); code != 2 {
		t.Errorf("a tokenizer and no files: exit %d, want 2", code)
	}
	if _, _, code := exec(t, "dem", "gates", "page.txt"); code != 2 {
		t.Errorf("files and no tokenizer: exit %d, want 2", code)
	}
	// The coverage set replaces the files rather than adding to them, so
	// naming both is a question with two answers.
	if _, _, code := exec(t, "dem", "gates", "-tokenizer", "t.model", "-coverage", "page.txt"); code != 2 {
		t.Errorf("the coverage set and files: exit %d, want 2", code)
	}

	missing := filepath.Join(t.TempDir(), "nope.model")
	_, errOut, code := exec(t, "dem", "gates", "-tokenizer", missing, "page.txt")
	if code != 1 {
		t.Errorf("a tokenizer that is not there: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "nope.model") {
		t.Errorf("the failure does not name the file it could not open:\n%s", errOut)
	}
}

func TestDemGatesIsInTheSubcommandList(t *testing.T) {
	out, _, code := exec(t, "dem", "help")
	if code != 0 {
		t.Fatalf("gao dem help: exit %d", code)
	}
	if !strings.Contains(out, "gates") {
		t.Errorf("gates is not in the dem subcommand list:\n%s", out)
	}

	_, errOut, code := exec(t, "dem", "gates", "-h")
	if code != 2 {
		t.Errorf("gao dem gates -h: exit %d, want 2", code)
	}
	for _, want := range []string{"diacritic atomicity", "100.000%", "an audit rather than a threshold", "a sample of the corpus"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the usage does not explain %q:\n%s", want, errOut)
		}
	}
}
