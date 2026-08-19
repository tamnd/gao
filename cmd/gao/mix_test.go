package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mixRows is the composed set, at the slate's shares, with a translated arm
// held aside whose capability mix follows the mixture's.
var mixRows = []struct {
	capability                  string
	native, translated, holdout int64
}{
	{"hoi-dap", 148_000, 28_000, 39_000},
	{"viet", 138_000, 6_000, 36_000},
	{"doc-hieu", 98_000, 14_000, 26_000},
	{"tom-tat", 84_000, 12_000, 22_000},
	{"dau-cau", 80_000, 0, 0},
	{"ma-nguon", 30_000, 50_000, 8_000},
	{"phap-ly", 62_000, 2_000, 16_000},
	{"dich", 12_000, 36_000, 3_000},
}

func mixLine(source, capability, origin string, n int64, held bool) string {
	audit := ""
	if origin == "native" {
		audit = `,"audited":600,"passed":588`
	}
	aside := ""
	if held {
		aside = `,"held":true`
	}
	return fmt.Sprintf(
		`{"source":%q,"capability":%q,"origin":%q,"examples":%d,"turns":%d%s%s,"license":"open"}`,
		source, capability, origin, n, n*3, audit, aside)
}

// mixSet writes the composed set, with each row's counts run through change
// before it is written, so a test moving one number moves one number.
func mixSet(t *testing.T, change func(capability string, native, translated, holdout int64) (int64, int64, int64)) string {
	t.Helper()
	var lines []string
	for _, r := range mixRows {
		native, translated, holdout := r.native, r.translated, r.holdout
		if change != nil {
			native, translated, holdout = change(r.capability, native, translated, holdout)
		}
		if native > 0 {
			lines = append(lines, mixLine("nguoi-viet-"+r.capability, r.capability, "native", native, false))
		}
		if translated > 0 {
			lines = append(lines, mixLine("dich-may-"+r.capability, r.capability, "translated", translated, false))
		}
		if holdout > 0 {
			lines = append(lines, mixLine("arm-"+r.capability, r.capability, "translated", holdout, true))
		}
	}
	path := filepath.Join(t.TempDir(), "sft.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMixReportsTheOriginsApartRatherThanAsATotal(t *testing.T) {
	out, errOut, code := exec(t, "mix", mixSet(t, nil))
	if code != 0 {
		t.Fatalf("a composed set exited %d: %s\n%s", code, errOut, out)
	}
	if !strings.Contains(out, "652,000 of them native and 148,000 translated") {
		t.Errorf("the two origins are not stated apart:\n%s", out)
	}
	if !strings.Contains(out, "what a comparison of them measures is origin") {
		t.Errorf("the verdict does not say what the arms are worth:\n%s", out)
	}
	if !strings.Contains(out, "150,000 translated examples are held aside") {
		t.Errorf("the held aside arm is not reported:\n%s", out)
	}
	if !strings.Contains(out, "leaves out dau-cau") {
		t.Errorf("the excluded capability is not named:\n%s", out)
	}
}

func TestMixPrintsTheSlateBeforeAnySetExists(t *testing.T) {
	out, _, code := exec(t, "mix", "-slate")
	if code != 0 {
		t.Fatalf("the slate exited %d\n%s", code, out)
	}
	for _, want := range []string{"hoi-dap", "viet", "dau-cau", "phap-ly", "800,000 examples over 8 capabilities"} {
		if !strings.Contains(out, want) {
			t.Errorf("the slate does not carry %s:\n%s", want, out)
		}
	}
	if _, _, code := exec(t, "mix", "-slate", "somewhere.jsonl"); code != 2 {
		t.Error("the slate took a file argument")
	}
}

func TestMixExitsTwoWhenTheArmsWouldNotMeasureOrigin(t *testing.T) {
	// The arm composed the way somebody composes it when translated writing data
	// is the easiest thing to get, which it is.
	skewed := mixSet(t, func(capability string, native, translated, holdout int64) (int64, int64, int64) {
		if capability == "viet" {
			return native, translated, 90_000
		}
		return native, translated, holdout / 2
	})
	out, _, code := exec(t, "mix", skewed)
	if code != 2 {
		t.Fatalf("arms with different mixes exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "capability mix rather than of the origin") {
		t.Errorf("the report does not say what the mix costs:\n%s", out)
	}

	small := mixSet(t, func(_ string, native, translated, holdout int64) (int64, int64, int64) {
		return native, translated, holdout / 6
	})
	out, _, code = exec(t, "mix", small)
	if code != 2 {
		t.Fatalf("an arm too small to train exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "how little each one read") {
		t.Errorf("the report does not say what a small arm measures:\n%s", out)
	}
}

func TestMixRefusesASetWhoseWritingIsMostlyTranslated(t *testing.T) {
	path := mixSet(t, func(capability string, native, translated, holdout int64) (int64, int64, int64) {
		if capability == "viet" {
			return 48_000, 96_000, holdout
		}
		return native, translated, holdout
	})
	out, _, code := exec(t, "mix", path)
	if code != 1 {
		t.Fatalf("a set that is a third native on writing exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "a claim about the translator") {
		t.Errorf("the refusal does not say what the claim would be about:\n%s", out)
	}
}

func TestMixRefusesASetMissingACapabilityTheSlateNames(t *testing.T) {
	path := mixSet(t, func(capability string, native, translated, holdout int64) (int64, int64, int64) {
		if capability == "phap-ly" {
			return 0, 0, 0
		}
		return native, translated, holdout
	})
	out, _, code := exec(t, "mix", path)
	if code != 1 {
		t.Fatalf("a set with no legal examples exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "a hole rather than a shorter set") {
		t.Errorf("the refusal does not say what a missing capability is:\n%s", out)
	}
}

func TestMixJSONCarriesTheArmsAndTheComposition(t *testing.T) {
	out, _, code := exec(t, "mix", "-json", mixSet(t, nil))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		`"native"`, `"translated"`, `"aside"`, `"composition"`, `"excluded"`,
		`"arm"`, `"drift"`, `"matched"`, `"reproducible"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the JSON does not carry %s:\n%s", want, out)
		}
	}
}

func TestMixWithoutASetIsAUsageError(t *testing.T) {
	if _, _, code := exec(t, "mix"); code != 2 {
		t.Error("tron with no argument did not exit 2")
	}
	if _, _, code := exec(t, "mix", filepath.Join(t.TempDir(), "nothing.jsonl")); code != 1 {
		t.Error("a set file that is not there did not exit 1")
	}
}
