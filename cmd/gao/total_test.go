package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countLine is one row of a counts file, with the byte, character and syllable
// columns in the proportions real Vietnamese comes back in, so a test that means
// to change the tokens changes only the tokens.
func countLine(source, class, origin string, tokens int64) string {
	return `{"source":"` + source + `","snapshot":"gao-2026-09","origin":"` + origin + `","class":"` + class + `",` +
		`"documents":` + itoa(tokens/400) +
		`,"bytes":` + itoa(int64(float64(tokens)*2.4)) +
		`,"chars":` + itoa(int64(float64(tokens)*1.8)) +
		`,"syllables":` + itoa(int64(float64(tokens)/1.9)) +
		`,"tokens":` + itoa(tokens) +
		`,"tokenizer":"gao-64k"}`
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func totalFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "counts.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTotalPublishesTheNaturalNumberAndNothingElse(t *testing.T) {
	path := totalFile(t,
		countLine("gao-crawl", "open", "natural", 200_000_000_000),
		countLine("hplt-v3", "open", "natural", 110_000_000_000),
		countLine("gao-synth", "open", "synthetic", 40_000_000_000),
	)
	out, errOut, code := exec(t, "total", path)
	if code != 0 {
		t.Fatalf("a release that met its claim exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "The headline is 310.0B of natural tokens") {
		t.Errorf("the headline is not the natural number:\n%s", out)
	}
	// 350B is the sum this command exists to never print as a headline.
	if strings.Contains(out, "headline is 350.0B") {
		t.Errorf("generated text was added into the headline:\n%s", out)
	}
	if !strings.Contains(out, "40.0B of generated text sits beside it") {
		t.Errorf("the synthetic line is missing:\n%s", out)
	}
}

func TestTotalStatesWhatShipsApartFromWhatWasCounted(t *testing.T) {
	path := totalFile(t,
		countLine("gao-crawl", "open", "natural", 260_000_000_000),
		countLine("phap-luat", "restricted", "natural", 30_000_000_000),
		countLine("gao-voice", "unredistributable", "natural", 10_000_000_000),
	)
	out, _, code := exec(t, "total", path)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "260.0B of the headline ships and 40.0B stays in the store") {
		t.Errorf("the publishable subset is not stated apart from the total:\n%s", out)
	}
	for _, want := range []string{"restricted", "unredistributable", "held"} {
		if !strings.Contains(out, want) {
			t.Errorf("the class table does not name %s:\n%s", want, out)
		}
	}
}

func TestTotalRefusesTwoTokenizersRatherThanAddingThem(t *testing.T) {
	other := strings.Replace(countLine("hplt-v3", "open", "natural", 110_000_000_000), "gao-64k", "gao-32k", 1)
	path := totalFile(t, countLine("gao-crawl", "open", "natural", 200_000_000_000), other)
	out, _, code := exec(t, "total", path)
	if code != 1 {
		t.Fatalf("counts in two units exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "two tokenizers are two units") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
}

func TestTotalExitsTwoOnTheKillCriterion(t *testing.T) {
	path := totalFile(t, countLine("gao-crawl", "open", "natural", 180_000_000_000))
	out, _, code := exec(t, "total", path)
	if code != 2 {
		t.Fatalf("a corpus under the kill criterion exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "rather than defended") {
		t.Errorf("the verdict does not read as the kill criterion:\n%s", out)
	}
	// The ratios have to be the ones this corpus earned rather than the ones the
	// plan was written with.
	if strings.Contains(out, "2.1x HPLT") {
		t.Errorf("the ratios were quoted rather than computed:\n%s", out)
	}
	if !strings.Contains(out, "1.3x HPLT v3 vie_Latn") {
		t.Errorf("180B against HPLT's 143.7B did not restate as 1.3x:\n%s", out)
	}
}

func TestTotalShortOfTheTargetIsNotTheSameAsDead(t *testing.T) {
	path := totalFile(t, countLine("gao-crawl", "open", "natural", 270_000_000_000))
	out, _, code := exec(t, "total", path)
	if code != 0 {
		t.Fatalf("a corpus short of the target but over the floor exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "against the 300.0B claimed") {
		t.Errorf("the shortfall is not stated:\n%s", out)
	}
}

func TestTotalJSONCarriesTheThreeTotalsSeparately(t *testing.T) {
	path := totalFile(t,
		countLine("gao-crawl", "open", "natural", 260_000_000_000),
		countLine("phap-luat", "restricted", "natural", 30_000_000_000),
		countLine("gao-synth", "open", "synthetic", 40_000_000_000),
	)
	out, _, code := exec(t, "total", "-json", path)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		`"headline": 290000000000`,
		`"corpus"`,
		`"publishable"`,
		`"generated"`,
		`"against"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the JSON does not carry %s:\n%s", want, out)
		}
	}
}

func TestTotalWithoutAFileIsAUsageError(t *testing.T) {
	if _, _, code := exec(t, "total"); code != 2 {
		t.Errorf("cong with no counts file exited %d", code)
	}
	if _, _, code := exec(t, "total", filepath.Join(t.TempDir(), "nothing.jsonl")); code != 1 {
		t.Errorf("cong on a file that is not there exited %d", code)
	}
}
