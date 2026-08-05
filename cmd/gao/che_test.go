package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamnd/gao/che"
)

// A classified advertisement, which is where a Vietnamese page keeps a name, a
// phone number and an address in the same three lines, and a news story, which
// keeps numbers and names and nothing anybody's privacy depends on.
const (
	cheAdvert = "Bán căn hộ hai phòng ngủ tại Số 25 đường Nguyễn Huệ, phường Bến Nghé, quận 1, TP Hồ Chí Minh.\n" +
		"Liên hệ chính chủ anh Nguyễn Văn Minh, điện thoại 0912 345 678, hoặc email minh.nguyen@gmail.com."

	cheArticle = "Chủ tịch Hồ Chí Minh đọc bản Tuyên ngôn độc lập ngày 2 tháng 9 năm 1945 tại quảng trường Ba Đình.\n" +
		"Theo Tổng cục Thống kê, dân số cả nước đạt 100.300.000 người vào cuối năm 2023.\n" +
		"Nhà thơ Nguyễn Du sinh năm 1765 và mất năm 1820, để lại Truyện Kiều gồm 3254 câu thơ."
)

// The default behavior is the one a pipeline gets when it pipes a document
// through without reading the flags, so the default has to be the safe half of
// the argument: identifiers covered, names left where they are.
func TestCheCoversIdentifiersByDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "advert.txt", cheAdvert)

	var stdout, stderr bytes.Buffer
	if code := runChe(&stdout, &stderr, []string{path}); code != 0 {
		t.Fatalf("gao che = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, gone := range []string{"0912 345 678", "minh.nguyen@gmail.com"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q is still in the covered text:\n%s", gone, out)
		}
	}
	for _, tag := range []string{"[SODIENTHOAI]", "[EMAIL]"} {
		if !strings.Contains(out, tag) {
			t.Errorf("%s is not in the covered text:\n%s", tag, out)
		}
	}
	if !strings.Contains(out, "Nguyễn Văn Minh") {
		t.Errorf("L1 took the name out, and only L2 is meant to:\n%s", out)
	}
}

// L2 is the level for text we crawled ourselves, where nobody upstream decided
// on our behalf, and it is the level that takes the name and the street.
func TestCheAtLevelTwoCoversTheNameAndTheAddress(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "advert.txt", cheAdvert)

	var stdout, stderr bytes.Buffer
	if code := runChe(&stdout, &stderr, []string{"-level", "L2", path}); code != 0 {
		t.Fatalf("gao che -level L2 = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "Nguyễn Văn Minh") {
		t.Errorf("L2 left the seller's name in:\n%s", out)
	}
	if !strings.Contains(out, "[HOTEN]") || !strings.Contains(out, "[DIACHI]") {
		t.Errorf("L2 did not tag the name and the address:\n%s", out)
	}
}

// A document with nothing private in it has to come back byte for byte, or the
// stage is not a filter, it is a rewriter.
func TestCheLeavesANewsStoryAlone(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "article.txt", cheArticle)

	var stdout, stderr bytes.Buffer
	if code := runChe(&stdout, &stderr, []string{"-level", "L2", path}); code != 0 {
		t.Fatalf("gao che = %d, want 0\n%s", code, stderr.String())
	}
	if stdout.String() != cheArticle {
		t.Errorf("the article came back changed:\n%s", stdout.String())
	}
}

// The counts a publication decision is made on: how many documents hold
// anything, how many spans there are, and what kinds they are.
func TestCheReportsWhatEachLevelWouldCover(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "advert.txt", cheAdvert),
		writeText(t, dir, "article.txt", cheArticle),
	}

	for _, c := range []struct {
		level   string
		covered int64
	}{
		{"L0", 0},
		{"L1", 2},
		{"L2", 4},
	} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"-report", "-json", "-level", c.level}, files...)
		if code := runChe(&stdout, &stderr, args); code != 0 {
			t.Fatalf("gao che -level %s = %d, want 0\n%s", c.level, code, stderr.String())
		}
		var got cheReport
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatalf("the report is not JSON: %v\n%s", err, stdout.String())
		}
		if got.Level != c.level {
			t.Errorf("the report says level %s and the flag said %s", got.Level, c.level)
		}
		if got.Total.Documents != 2 || got.Total.Carrying != 1 {
			t.Errorf("two files came to %d documents with %d carrying, want 2 and 1",
				got.Total.Documents, got.Total.Carrying)
		}
		if got.Total.Spans != 4 {
			t.Errorf("the advertisement holds four spans and the report found %d", got.Total.Spans)
		}
		if got.Total.Covered != c.covered {
			t.Errorf("%s covered %d of them, want %d", c.level, got.Total.Covered, c.covered)
		}
	}
}

// Every level reports the same spans, since what a source holds is worth
// knowing separately from what was done about it.
func TestCheCountsTheSameSpansAtEveryLevel(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "advert.txt", cheAdvert)

	var stdout, stderr bytes.Buffer
	if code := runChe(&stdout, &stderr, []string{"-report", "-json", "-level", "L0", path}); code != 0 {
		t.Fatalf("gao che -level L0 = %d, want 0\n%s", code, stderr.String())
	}
	var got cheReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, stdout.String())
	}
	for _, k := range []che.Kind{che.KindPhone, che.KindEmail, che.KindName, che.KindAddress} {
		if got.Total.ByKind[k] != 1 {
			t.Errorf("L0 found %d of kind %s, want 1", got.Total.ByKind[k], k)
		}
	}
}

// The report prints numbers. Tuning a detector needs the text it fired on, and
// -spans is the flag that says so out loud.
func TestCheListsTheMatchesWhenAsked(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "advert.txt", cheAdvert)

	var stdout, stderr bytes.Buffer
	if code := runChe(&stdout, &stderr, []string{"-report", "-spans", "-level", "L2", path}); code != 0 {
		t.Fatalf("gao che -spans = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"0912 345 678", "Nguyễn Văn Minh", "minh.nguyen@gmail.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("-spans did not print %q:\n%s", want, out)
		}
	}
}

// The report reads as prose under the table, and the prose has to be right
// about which way round the level went.
func TestCheSaysWhatTheLevelDid(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "advert.txt", cheAdvert)

	var stdout, stderr bytes.Buffer
	if code := runChe(&stdout, &stderr, []string{"-report", path}); code != 0 {
		t.Fatalf("gao che -report = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "L1 covers 2 of the 4") {
		t.Errorf("the report does not say what L1 covered:\n%s", out)
	}
	if !strings.Contains(out, "100.0% of the documents hold personal data") {
		t.Errorf("the report does not say how many documents carry anything:\n%s", out)
	}
}

// A level that does not exist is a typo, and covering nothing quietly is the
// worst thing a privacy stage can do about a typo.
func TestCheRefusesALevelItDoesNotHave(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runChe(&stdout, &stderr, []string{"-level", "L3"}); code != 2 {
		t.Fatalf("gao che -level L3 = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "L0, L1 and L2") {
		t.Errorf("the error does not name the levels: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("a refused run still wrote text: %q", stdout.String())
	}
}

// -json and -spans shape a report, and asking for them without asking for the
// report means the run was not the one that was meant.
func TestCheRefusesReportFlagsWithoutTheReport(t *testing.T) {
	for _, flag := range []string{"-json", "-spans"} {
		var stdout, stderr bytes.Buffer
		if code := runChe(&stdout, &stderr, []string{flag}); code != 2 {
			t.Fatalf("gao che %s = %d, want 2", flag, code)
		}
		if !strings.Contains(stderr.String(), "only mean something with -report") {
			t.Errorf("the error for %s does not say why: %q", flag, stderr.String())
		}
	}
}

// The recall measurement is in the binary so that somebody who installed gao
// can ask what the detectors find without a checkout, and the question it
// answers is the one people actually ask about a redaction pass.
func TestCheMeasuresItselfAgainstTheLabeledSet(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runChe(&stdout, &stderr, []string{"-recall"}); code != 0 {
		t.Fatalf("gao che -recall = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"detector", "marked", "covered", "recall", "precision", "documents, marked by hand"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not hold %q:\n%s", want, out)
		}
	}
	for _, k := range che.Kinds() {
		if !strings.Contains(out, string(k)) {
			t.Errorf("the report has no row for %s:\n%s", k, out)
		}
	}
	// The misses are the half of the report worth reading, so they are printed
	// with it rather than left for whoever thinks to ask.
	if !strings.Contains(out, "Not covered:") {
		t.Errorf("the report does not list what was missed:\n%s", out)
	}
}

// The same numbers, for whatever is watching them over time.
func TestCheReportsTheRecallAsJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runChe(&stdout, &stderr, []string{"-recall", "-json"}); code != 0 {
		t.Fatalf("gao che -recall -json = %d, want 0\n%s", code, stderr.String())
	}
	var got che.Score
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, stdout.String())
	}
	if got.Documents < 8 {
		t.Errorf("the labeled set came back with %d documents", got.Documents)
	}
	if got.Total.Gold != 49 {
		t.Errorf("the labeled set came back with %d marked spans, and it holds 49", got.Total.Gold)
	}
	if r := got.Total.Recall(); r < 0.93 {
		t.Errorf("recall over the whole set is %.3f, and it was 0.939", r)
	}
	if p := got.Total.Precision(); p < 1 {
		t.Errorf("precision over the whole set is %.3f, and every span the detectors found was one somebody marked", p)
	}
}

// -recall reads the set it was built with. A file on the command line means the
// caller expected it to measure that file, and measuring something else instead
// would hand back a number about the wrong text.
func TestCheRecallTakesNoFiles(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "advert.txt", cheAdvert)

	for _, args := range [][]string{{"-recall", path}, {"-recall", "-report"}, {"-recall", "-spans"}} {
		var stdout, stderr bytes.Buffer
		if code := runChe(&stdout, &stderr, args); code != 2 {
			t.Fatalf("gao che %s = %d, want 2", strings.Join(args, " "), code)
		}
		if !strings.Contains(stderr.String(), "built into this binary") {
			t.Errorf("the error for %s does not say what -recall measures: %q", strings.Join(args, " "), stderr.String())
		}
	}
}

// A part holds thousands of documents. Covering them onto one stream would
// produce a file nobody can split back up, so the stage says so instead.
func TestCheRefusesToCoverAPartOntoOneStream(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "part.parquet", "")

	var stdout, stderr bytes.Buffer
	if code := runChe(&stdout, &stderr, []string{path}); code != 2 {
		t.Fatalf("gao che part.parquet = %d, want 2\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Use -report") {
		t.Errorf("the error does not say what to do instead: %q", stderr.String())
	}
}
