package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/tin"
)

// tinPairs writes a study where the proxy tracks the anchor, with change applied
// first so a test can break exactly one thing about it.
func tinPairs(t *testing.T, change func([]tin.Pair) []tin.Pair) string {
	t.Helper()
	slate := doc.Sum([]byte("slate"))

	pairs := []tin.Pair{
		{Run: "B01", Baseline: true, Proxy: 0.610, Anchor: 0.512},
		{Run: "B02", Baseline: true, Proxy: 0.613, Anchor: 0.515},
		{Run: "B03", Baseline: true, Proxy: 0.609, Anchor: 0.511},
	}
	for i := range 14 {
		pairs = append(pairs, tin.Pair{
			Run:    "R" + string(rune('A'+i)),
			Proxy:  0.60 + float64(i)*0.006,
			Anchor: 0.50 + float64(i)*0.009,
		})
	}
	for i := range pairs {
		pairs[i].Slate = slate
		pairs[i].ProxyBox = "gamingpc"
		pairs[i].AnchorBox = "8x H100 SXM"
	}
	if change != nil {
		pairs = change(pairs)
	}

	lines := make([]string, 0, len(pairs))
	for _, p := range pairs {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	path := filepath.Join(t.TempDir(), "pairs.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheStudyIsReadableBeforeAnyOfItHasRun(t *testing.T) {
	out, errOut, code := exec(t, "tin", "study")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{tin.Proxy, tin.Anchor, "0.70", "0.50", "0.80", "exploratory", "Nothing has been scored"} {
		if !strings.Contains(out, want) {
			t.Errorf("study does not mention %q:\n%s", want, out)
		}
	}
}

func TestTheStudyPrintsAsJSON(t *testing.T) {
	out, errOut, code := exec(t, "tin", "study", "-json")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got tinStudyReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Proxy != tin.Proxy || got.Anchor != tin.Anchor {
		t.Errorf("study is about %s and %s, not %s and %s", got.Proxy, got.Anchor, tin.Proxy, tin.Anchor)
	}
	if got.Kill != tin.Kill {
		t.Errorf("kill criterion %v, want %v", got.Kill, tin.Kill)
	}
}

func TestAProxyThatTracksTheAnchorCanBeBelieved(t *testing.T) {
	path := tinPairs(t, nil)
	out, errOut, code := exec(t, "tin", "read", path)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "can be believed about") {
		t.Errorf("a proxy that tracks the anchor was not believed:\n%s", out)
	}
	if strings.Contains(out, "not publishable") {
		t.Errorf("a clean study was held back:\n%s", out)
	}
}

func TestReadingTheStudyReportsTheNoiseFloorItWasReadAgainst(t *testing.T) {
	path := tinPairs(t, nil)
	out, _, _ := exec(t, "tin", "read", path)
	if !strings.Contains(out, "noise floor") {
		t.Errorf("the floor every comparison was read against is not in the report:\n%s", out)
	}
	if !strings.Contains(out, "too close to call") {
		t.Errorf("the comparisons left out are not in the report:\n%s", out)
	}
}

func TestAProxyThatOrdersRecipesBackwardsExitsNonZero(t *testing.T) {
	path := tinPairs(t, func(pairs []tin.Pair) []tin.Pair {
		for i := range pairs {
			if !pairs[i].Baseline {
				pairs[i].Proxy = 1 - pairs[i].Proxy
			}
		}
		return pairs
	})
	out, errOut, code := exec(t, "tin", "read", path)
	if code == 0 {
		t.Fatalf("a proxy pointing the wrong way was published:\n%s", out)
	}
	if !strings.Contains(out, "exploratory") {
		t.Errorf("the kill criterion was not reported:\n%s\n%s", out, errOut)
	}
}

func TestAStudyTooSmallToSaySoIsNotPublishedEvenWhenTheProxyLooksGood(t *testing.T) {
	path := tinPairs(t, func(pairs []tin.Pair) []tin.Pair { return pairs[:6] })
	out, _, code := exec(t, "tin", "read", path)
	if code == 0 {
		t.Fatalf("a six row study was published:\n%s", out)
	}
	if !strings.Contains(out, "by accident") {
		t.Errorf("the report does not say the correlation lands where it lands by accident:\n%s", out)
	}
}

func TestTheComparisonsTheProxyCalledBackwardsCanBeListed(t *testing.T) {
	// RA is the worst recipe at the anchor and RN is the best, so handing the
	// proxy their scores the wrong way round is the widest miss available.
	path := tinPairs(t, func(pairs []tin.Pair) []tin.Pair {
		a, n := index(pairs, "RA"), index(pairs, "RN")
		pairs[a].Proxy, pairs[n].Proxy = pairs[n].Proxy, pairs[a].Proxy
		return pairs
	})
	out, _, _ := exec(t, "tin", "read", "-missed", path)
	if !strings.Contains(out, "called backwards") {
		t.Fatalf("the misses were not listed:\n%s", out)
	}
	if !strings.Contains(out, "at the anchor") {
		t.Errorf("a miss was listed without how far apart the anchor put the two:\n%s", out)
	}
}

func TestARunWithNoMachineOnItIsNamedRatherThanCounted(t *testing.T) {
	path := tinPairs(t, func(pairs []tin.Pair) []tin.Pair {
		pairs[index(pairs, "RE")].AnchorBox = ""
		return pairs
	})
	out, _, code := exec(t, "tin", "read", path)
	if code == 0 {
		t.Fatalf("a study with a run nobody can reproduce was published:\n%s", out)
	}
	if !strings.Contains(out, "RE") {
		t.Errorf("the run with no box on it was not named:\n%s", out)
	}
}

// index finds a run by ID, so a test can break one row without counting rows.
func index(pairs []tin.Pair, run string) int {
	return slices.IndexFunc(pairs, func(p tin.Pair) bool { return p.Run == run })
}

func TestTheWholeScoreIsAvailableAsJSON(t *testing.T) {
	path := tinPairs(t, nil)
	out, errOut, code := exec(t, "tin", "read", "-json", path)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got tinReadReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Recipes != 15 {
		t.Errorf("%d recipes, want 15 once the three baseline repeats fold into one", got.Recipes)
	}
	if got.Baselines != 3 {
		t.Errorf("%d baselines, want 3", got.Baselines)
	}
	if !got.Believable {
		t.Errorf("a proxy that tracks the anchor was not believed: %s", got.Verdict)
	}
}

func TestAFileThatIsNotThereIsAFailureRatherThanAnEmptyStudy(t *testing.T) {
	out, errOut, code := exec(t, "tin", "read", filepath.Join(t.TempDir(), "nothing.jsonl"))
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "gao tin:") {
		t.Errorf("the failure was not attributed: %s", errOut)
	}
}

func TestReadWithoutAFileIsAUsageError(t *testing.T) {
	out, errOut, code := exec(t, "tin", "read")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "usage: gao tin") {
		t.Errorf("no usage printed: %s", errOut)
	}
}

func TestTinWithoutASubcommandPrintsUsage(t *testing.T) {
	_, errOut, code := exec(t, "tin")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
	if !strings.Contains(errOut, "usage: gao tin") {
		t.Errorf("no usage printed: %s", errOut)
	}
}

func TestAnUnknownTinSubcommandSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "tin", "prove")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
	if !strings.Contains(errOut, "no subcommand named prove") {
		t.Errorf("the unknown subcommand was not named: %s", errOut)
	}
}

func TestTinHelpGoesToStdout(t *testing.T) {
	out, _, code := exec(t, "tin", "help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "usage: gao tin") {
		t.Errorf("help did not go to stdout: %s", out)
	}
}
