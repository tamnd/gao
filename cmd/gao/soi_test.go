package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// A page and three readings of it: one perfect, one that lost the tones and
// kept everything else, and one that lost the tones and the letter marks too,
// which is what an engine with no Vietnamese in it produces.
const (
	soiPage      = "Tiếng Việt có sáu thanh điệu, và đó là chỗ mọi thứ hỏng.\n"
	soiNoTones   = "Tiêng Viêt co sau thanh diêu, va do la chô moi thư hong.\n"
	soiNoMarks   = "Tieng Viet co sau thanh dieu, va do la cho moi thu hong.\n"
	soiOneWrong  = "Tiếng Việt có sáu thanh điệu, và đó là chổ mọi thứ hỏng.\n"
	soiSecondRef = "Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập, tự do, hạnh phúc.\n"
)

// The report has to put the two rates next to each other, because the whole
// reason this command exists is that reading either one on its own is what gets
// a corpus built out of a broken reading.
func TestSoiReportsBothRates(t *testing.T) {
	dir := t.TempDir()
	page := writeText(t, dir, "page.txt", soiPage)
	read := writeText(t, dir, "read.txt", soiNoMarks)

	var stdout, stderr bytes.Buffer
	if code := runSoi(&stdout, &stderr, []string{page, read}); code != 0 {
		t.Fatalf("gao inspect = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"cer", "der", "tone lost", "đ and d"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report has no %q column:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "100.0%") {
		t.Errorf("a reading with every mark taken off does not report a diacritic error rate of 100%%:\n%s", out)
	}
}

// The gate is what a pipeline calls, so a failing reading has to be a non zero
// exit and the reason has to be on standard error where a build log keeps it.
func TestSoiGateFailsALossyReading(t *testing.T) {
	dir := t.TempDir()
	page := writeText(t, dir, "page.txt", soiPage)
	bad := writeText(t, dir, "bad.txt", soiNoMarks)
	good := writeText(t, dir, "good.txt", soiPage)

	var stdout, stderr bytes.Buffer
	if code := runSoi(&stdout, &stderr, []string{"-gate", page, bad}); code != 1 {
		t.Fatalf("gao inspect -gate on a reading with no marks = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "diacritic error rate") {
		t.Errorf("the failure does not name the rate that failed:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runSoi(&stdout, &stderr, []string{"-gate", page, good}); code != 0 {
		t.Fatalf("gao inspect -gate on a perfect reading = %d, want 0\n%s", code, stderr.String())
	}

	// Without -gate the numbers still print and the command still succeeds,
	// because looking at a bad reading is not itself a failure.
	stdout.Reset()
	stderr.Reset()
	if code := runSoi(&stdout, &stderr, []string{page, bad}); code != 0 {
		t.Fatalf("gao inspect without -gate on a bad reading = %d, want 0\n%s", code, stderr.String())
	}
}

// The matrix is the second of the two deliverables and it has to be readable as
// a matrix: named in Vietnamese, rows the page and columns the reading.
func TestSoiPrintsTheToneConfusionMatrix(t *testing.T) {
	dir := t.TempDir()
	page := writeText(t, dir, "page.txt", soiPage)
	read := writeText(t, dir, "read.txt", soiOneWrong)

	var stdout, stderr bytes.Buffer
	if code := runSoi(&stdout, &stderr, []string{"-matrix", page, read}); code != 0 {
		t.Fatalf("gao inspect -matrix = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, tone := range []string{"ngang", "huyền", "sắc", "hỏi", "ngã", "nặng"} {
		if strings.Count(out, tone) < 2 {
			t.Errorf("%q does not appear as both a row and a column:\n%s", tone, out)
		}
	}
	if !strings.Contains(out, "rows are the page and the columns are the reading") {
		t.Errorf("the matrix does not say which way round it is:\n%s", out)
	}

	// ngã read as hỏi, which is the one thing that changed.
	var stdoutNoMatrix bytes.Buffer
	if code := runSoi(&stdoutNoMatrix, &stderr, []string{page, read}); code != 0 {
		t.Fatalf("gao inspect = %d", code)
	}
	if strings.Contains(stdoutNoMatrix.String(), "rows are the page") {
		t.Errorf("the matrix printed without -matrix:\n%s", stdoutNoMatrix.String())
	}
}

// An evaluation set is several pages in one run and one score over all of them,
// which is the difference between measuring a corpus and measuring a page.
func TestSoiScoresASetAsOneNumber(t *testing.T) {
	dir := t.TempDir()
	one := writeText(t, dir, "one.txt", soiPage)
	oneRead := writeText(t, dir, "one-read.txt", soiNoMarks)
	two := writeText(t, dir, "two.txt", soiSecondRef)
	twoRead := writeText(t, dir, "two-read.txt", soiSecondRef)

	var stdout, stderr bytes.Buffer
	if code := runSoi(&stdout, &stderr, []string{"-json", one, oneRead, two, twoRead}); code != 0 {
		t.Fatalf("gao inspect -json = %d, want 0\n%s", code, stderr.String())
	}

	var got struct {
		Readings []struct {
			Page    string `json:"page"`
			Reading string `json:"reading"`
			Chars   int    `json:"chars"`
			Marked  int    `json:"marked"`
			Lost    int    `json:"lost"`
		} `json:"readings"`
		Total struct {
			Chars  int `json:"chars"`
			Marked int `json:"marked"`
			Lost   int `json:"lost"`
		} `json:"total"`
		Fails []string `json:"fails"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the JSON does not parse: %v\n%s", err, stdout.String())
	}
	if len(got.Readings) != 2 {
		t.Fatalf("two pairs came back as %d readings", len(got.Readings))
	}
	if got.Readings[1].Lost != 0 {
		t.Errorf("the page read perfectly lost %d marks", got.Readings[1].Lost)
	}
	if want := got.Readings[0].Chars + got.Readings[1].Chars; got.Total.Chars != want {
		t.Errorf("the total is %d characters and the two pages are %d", got.Total.Chars, want)
	}
	if got.Total.Lost != got.Readings[0].Lost {
		t.Errorf("the total lost %d marks and the only page that lost any lost %d", got.Total.Lost, got.Readings[0].Lost)
	}
	// The set is scored over all of it, so the second page dilutes the first
	// rather than averaging with it at half a vote each.
	if got.Total.Lost == got.Total.Marked {
		t.Error("the set reports every mark lost, and one of its two pages came through untouched")
	}
	if len(got.Fails) == 0 {
		t.Error("the JSON does not say the set failed the gate")
	}
}

// The arguments are pairs, and an odd number of them means somebody forgot a
// reading, which is worth catching before it silently measures the wrong file
// against the wrong one.
func TestSoiWantsPairs(t *testing.T) {
	dir := t.TempDir()
	page := writeText(t, dir, "page.txt", soiPage)

	for _, args := range [][]string{
		{page},
		{page, page, page},
	} {
		var stdout, stderr bytes.Buffer
		if code := runSoi(&stdout, &stderr, args); code != 2 {
			t.Errorf("gao inspect with %d files = %d, want 2", len(args), code)
		}
		if !strings.Contains(stderr.String(), "pairs") {
			t.Errorf("the error does not say the files are read as pairs:\n%s", stderr.String())
		}
	}

	var stdout, stderr bytes.Buffer
	if code := runSoi(&stdout, &stderr, nil); code != 2 {
		t.Errorf("gao inspect with no files = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Errorf("gao inspect with no files does not print its usage:\n%s", stderr.String())
	}
}

// A file that is not there is an exit 1 and a sentence, not a panic and not a
// score of zero, which would read as a perfect reading.
func TestSoiOnAFileThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	page := writeText(t, dir, "page.txt", soiPage)

	var stdout, stderr bytes.Buffer
	if code := runSoi(&stdout, &stderr, []string{page, dir + "/nowhere.txt"}); code != 1 {
		t.Fatalf("gao inspect on a missing reading = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "gao inspect:") {
		t.Errorf("the error is not named:\n%s", stderr.String())
	}
}

// The reading that this whole command exists to catch: a page typed without
// tone marks scores well on the number everybody quotes and loses everything
// that matters, and the report has to show both at once.
func TestSoiSeparatesTheDamageThatShowsFromTheDamageThatDoesNot(t *testing.T) {
	dir := t.TempDir()
	page := writeText(t, dir, "page.txt", soiPage)
	tonesOnly := writeText(t, dir, "tones.txt", soiNoTones)

	var stdout, stderr bytes.Buffer
	if code := runSoi(&stdout, &stderr, []string{page, tonesOnly}); code != 0 {
		t.Fatalf("gao inspect = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "of the characters came through as themselves, and") {
		t.Errorf("the report does not put the two shares in one sentence:\n%s", out)
	}
	if strings.Contains(out, "characters are missing from the reading") {
		t.Errorf("nothing was dropped or added and the report says otherwise:\n%s", out)
	}
}
