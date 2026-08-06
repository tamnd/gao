package tin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/zeebo/blake3"
)

// slate is the digest every honest row in these tests carries.
func slate() doc.Hash { return doc.Hash(blake3.Sum256([]byte("slate-1.0"))) }

// study builds a set of paired scores where the proxy tracks the anchor as
// closely as the argument being tested needs, with change applied last so a
// test can break exactly one thing.
//
// The anchor scores are spread wide enough that the noise floor does not eat
// the comparisons, because a fixture where every pair is too close to call is a
// fixture that passes whatever it is asked.
func study(recipes int, drift func(i int) float64, change func([]Pair) []Pair) []Pair {
	var out []Pair
	for i := range MinBaselines {
		out = append(out, Pair{
			Run: fmt.Sprintf("B%02d", i+1), Slate: slate(), Baseline: true,
			Proxy: 50 + float64(i)*0.2, Anchor: 60 + float64(i)*0.3,
			ProxyBox: "gamingpc", AnchorBox: "8xH100",
		})
	}
	for i := range recipes {
		anchor := 60 + float64(i+1)*2
		out = append(out, Pair{
			Run: fmt.Sprintf("A%02d", i+1), Slate: slate(),
			Proxy: 50 + float64(i+1)*2 + drift(i), Anchor: anchor,
			ProxyBox: "gamingpc", AnchorBox: "8xH100",
		})
	}
	if change != nil {
		out = change(out)
	}
	return out
}

// tracks is a proxy that follows the anchor exactly.
func tracks(int) float64 { return 0 }

func has(t *testing.T, ss []string, want string) {
	t.Helper()
	if !strings.Contains(strings.Join(ss, "\n"), want) {
		t.Errorf("want %q in:\n%s", want, strings.Join(ss, "\n"))
	}
}

func hasNot(t *testing.T, ss []string, want string) {
	t.Helper()
	if strings.Contains(strings.Join(ss, "\n"), want) {
		t.Errorf("did not want %q in:\n%s", want, strings.Join(ss, "\n"))
	}
}

func TestAProxyThatOrdersRecipesTheWayTheAnchorDoesIsBelievable(t *testing.T) {
	sc := Read(study(MinRecipes, tracks, nil))
	if !sc.Believable || sc.Exploratory {
		t.Fatalf("a proxy that agrees perfectly scored %+v", sc)
	}
	if sc.Spearman != 1 {
		t.Errorf("a perfect ordering correlated %.4f", sc.Spearman)
	}
	if sc.Agreement != 1 || len(sc.Missed) != 0 {
		t.Errorf("a perfect ordering missed %d of %d comparisons", len(sc.Missed), sc.Compared)
	}
	if why := sc.Publishable(); len(why) != 0 {
		t.Errorf("an honest study was refused: %v", why)
	}
	if !strings.Contains(sc.Verdict(), "can be believed about") {
		t.Errorf("the verdict does not say it passed: %s", sc.Verdict())
	}
}

func TestAProxyThatOrdersRecipesBackwardsIsKilledRatherThanCaveated(t *testing.T) {
	pairs := study(MinRecipes, tracks, func(ps []Pair) []Pair {
		for i, p := range ps {
			if !p.Baseline {
				ps[i].Proxy = 100 - p.Proxy
			}
		}
		return ps
	})
	sc := Read(pairs)
	if sc.Believable || !sc.Exploratory {
		t.Fatalf("a proxy pointing the wrong way scored %+v", sc)
	}
	if sc.Spearman >= 0 {
		t.Errorf("an inverted ordering correlated %.2f", sc.Spearman)
	}
	has(t, sc.Publishable(), "ordering recipes backwards rather than badly")
	if !strings.Contains(sc.Verdict(), "reported as exploratory") {
		t.Errorf("the kill criterion did not fire in the verdict: %s", sc.Verdict())
	}
}

func TestAProxyBetweenTheBarAndTheKillGoesOutWithTheCaveat(t *testing.T) {
	// The worst recipe is scored as the best, which is one badly placed run out
	// of thirteen: enough disorder to miss the bar and not enough to be killed.
	sc := Read(study(MinRecipes, func(i int) float64 {
		if i == 0 {
			return 24
		}
		return 0
	}, nil))
	if sc.Spearman < Kill || sc.Spearman >= Believable {
		t.Fatalf("this fixture correlated %.3f, which is outside the band it was built for", sc.Spearman)
	}
	if sc.Believable || sc.Exploratory {
		t.Errorf("a middling proxy came back as one or the other: %+v", sc)
	}
	// It clears the pairwise bar and not the rank correlation, which is the case
	// the two bars exist to tell apart. One badly placed recipe costs twelve
	// comparisons out of seventy eight and the whole ordering at once.
	if sc.Agreement < Agree {
		t.Errorf("the fixture missed both bars at once, so it does not show what having two of them buys: %.3f", sc.Agreement)
	}
	if !strings.Contains(sc.Verdict(), "neither validated nor dead") {
		t.Errorf("the middle band is not reported as itself: %s", sc.Verdict())
	}
}

func TestComparisonsInsideTheNoiseFloorAreNotCounted(t *testing.T) {
	// Two recipes a hair apart at the anchor, with a baseline spread wider than
	// the gap between them. Whatever the proxy says about those two, it cannot
	// be right or wrong.
	pairs := []Pair{
		{Run: "B01", Slate: slate(), Baseline: true, Proxy: 50, Anchor: 60, ProxyBox: "g", AnchorBox: "h"},
		{Run: "B02", Slate: slate(), Baseline: true, Proxy: 51, Anchor: 62, ProxyBox: "g", AnchorBox: "h"},
		{Run: "B03", Slate: slate(), Baseline: true, Proxy: 50.5, Anchor: 61, ProxyBox: "g", AnchorBox: "h"},
		{Run: "A01", Slate: slate(), Proxy: 70, Anchor: 80.0, ProxyBox: "g", AnchorBox: "h"},
		{Run: "A02", Slate: slate(), Proxy: 60, Anchor: 80.5, ProxyBox: "g", AnchorBox: "h"},
	}
	sc := Read(pairs)
	if sc.NoiseAnchor != 2 {
		t.Fatalf("the floor came out at %.2f from a baseline spread of 2", sc.NoiseAnchor)
	}
	if sc.TooClose == 0 {
		t.Error("two recipes half a point apart under a floor of two were counted as a comparison")
	}
	for _, m := range sc.Missed {
		if m.AnchorGap <= sc.NoiseAnchor {
			t.Errorf("a comparison inside the floor was reported as a miss: %+v", m)
		}
	}
}

func TestTheBaselineRepeatsAreOneRecipeInTheRanking(t *testing.T) {
	sc := Read(study(MinRecipes, tracks, nil))
	if sc.Baselines != MinBaselines {
		t.Fatalf("%d baselines read, want %d", sc.Baselines, MinBaselines)
	}
	if sc.Recipes != MinRecipes+1 {
		t.Errorf("%d recipes ranked from %d recipes plus %d repeats of one, want %d",
			sc.Recipes, MinRecipes, MinBaselines, MinRecipes+1)
	}
}

func TestAStudyWithNoRepeatedBaselineHasNoFloorAndSaysSo(t *testing.T) {
	sc := Read(study(MinRecipes, tracks, func(ps []Pair) []Pair {
		out := ps[:0]
		for _, p := range ps {
			if !p.Baseline || p.Run == "B01" {
				out = append(out, p)
			}
		}
		return out
	}))
	if !sc.Exploratory {
		t.Error("a study with no noise floor was allowed to be decisive")
	}
	has(t, sc.Publishable(), "read against a spread taken from too little to be a spread")
}

func TestAStudyOverTooFewRecipesIsRefusedHoweverWellItCorrelates(t *testing.T) {
	sc := Read(study(3, tracks, nil))
	if sc.Spearman != 1 {
		t.Fatalf("the fixture correlated %.2f rather than perfectly", sc.Spearman)
	}
	if sc.Believable {
		t.Error("a perfect correlation over four recipes was believed")
	}
	has(t, sc.Publishable(), "lands where it lands by accident")
}

func TestARunScoredTwiceKeepsTheFirstAndIsNamed(t *testing.T) {
	sc := Read(study(MinRecipes, tracks, func(ps []Pair) []Pair {
		again := ps[len(ps)-1]
		again.Proxy = 0
		return append(ps, again)
	}))
	if len(sc.Repeat) != 1 {
		t.Fatalf("a run scored twice came back as %v", sc.Repeat)
	}
	if sc.Spearman != 1 {
		t.Errorf("the second score was used: correlation %.2f", sc.Spearman)
	}
	has(t, sc.Publishable(), "somebody re-ran after seeing the first number")
}

func TestARunFromADifferentSlateIsLeftOutAndNamed(t *testing.T) {
	sc := Read(study(MinRecipes, tracks, func(ps []Pair) []Pair {
		return append(ps, Pair{
			Run: "A99", Slate: doc.Hash(blake3.Sum256([]byte("slate-0.9"))),
			Proxy: 99, Anchor: 1, ProxyBox: "g", AnchorBox: "h",
		})
	}))
	if len(sc.Elsewhere) != 1 || sc.Elsewhere[0] != "A99" {
		t.Fatalf("a row from another slate came back as %v", sc.Elsewhere)
	}
	if sc.Spearman != 1 {
		t.Errorf("a row from another slate was ranked: correlation %.2f", sc.Spearman)
	}
	has(t, sc.Publishable(), "is not one recipe and the comparison is not a comparison")
}

func TestAResultWithNoBoxOnItIsNamed(t *testing.T) {
	sc := Read(study(MinRecipes, tracks, func(ps []Pair) []Pair {
		ps[len(ps)-1].AnchorBox = ""
		return ps
	}))
	if len(sc.Nowhere) != 1 {
		t.Fatalf("a result with no machine on it came back as %v", sc.Nowhere)
	}
	has(t, sc.Publishable(), "cannot be ruled out as a locale difference")
}

func TestAProxyThatScoresEverythingTheSameIsDecliningToAnswer(t *testing.T) {
	sc := Read(study(MinRecipes, tracks, func(ps []Pair) []Pair {
		for i := range ps {
			ps[i].Proxy = 50
		}
		return ps
	}))
	if sc.Spearman != 0 {
		t.Errorf("a flat proxy correlated %.2f", sc.Spearman)
	}
	if sc.Flat == 0 {
		t.Error("a proxy that scored everything identically reported no flat comparisons")
	}
	has(t, sc.Publishable(), "declining to answer rather than answering wrong")
}

func TestBaselinesThatScoredIdenticallyAtTheAnchorAreNotAFloorOfZero(t *testing.T) {
	sc := Read(study(MinRecipes, tracks, func(ps []Pair) []Pair {
		for i, p := range ps {
			if p.Baseline {
				ps[i].Anchor = 60
			}
		}
		return ps
	}))
	if sc.NoiseAnchor != 0 {
		t.Fatalf("three identical baselines gave a floor of %.2f", sc.NoiseAnchor)
	}
	if sc.TooClose != 0 {
		t.Errorf("a floor of zero still dropped %d comparisons", sc.TooClose)
	}
	has(t, sc.Publishable(), "make every comparison below look decisive")
}

func TestTheComparisonsTheProxyCalledBackwardsComeOutWidestFirst(t *testing.T) {
	sc := Read(study(MinRecipes, func(i int) float64 {
		if i%2 == 0 {
			return 40
		}
		return 0
	}, nil))
	if len(sc.Missed) < 2 {
		t.Fatalf("a badly scrambled proxy missed %d comparisons", len(sc.Missed))
	}
	for i := 1; i < len(sc.Missed); i++ {
		if sc.Missed[i].AnchorGap > sc.Missed[i-1].AnchorGap {
			t.Errorf("the misses are not widest first: %+v then %+v", sc.Missed[i-1], sc.Missed[i])
		}
	}
	if sc.Missed[0].Better == "" || sc.Missed[0].Worse == "" {
		t.Errorf("a miss does not name both recipes: %+v", sc.Missed[0])
	}
}

func TestNothingScoredAtAllIsSaidPlainly(t *testing.T) {
	sc := Read(nil)
	if sc.Believable {
		t.Error("an empty study was believed")
	}
	why := sc.Publishable()
	if len(why) != 1 {
		t.Fatalf("an empty study got a list of reasons rather than the one: %v", why)
	}
	has(t, why, "there is no study here")
}

func TestTheStudySaysWhatItIsBeforeItSaysWhatItFound(t *testing.T) {
	d := Describe()
	for _, want := range []string{Proxy, Anchor, ProxyScale, AnchorScale, "12 recipes", "3 repeats"} {
		if !strings.Contains(d, want) {
			t.Errorf("the description does not carry %q: %s", want, d)
		}
	}
}

func TestTiedScoresShareARankRatherThanBeingBrokenByInputOrder(t *testing.T) {
	got := ranks([]float64{5, 1, 5, 3})
	want := []float64{3.5, 1, 3.5, 2}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ranks came out %v, want %v", got, want)
		}
	}
}

func TestPairedScoresAreReadOneToALine(t *testing.T) {
	pairs := study(MinRecipes, tracks, nil)
	lines := make([]string, 0, len(pairs)+2)
	lines = append(lines, "# the ablation slate against the 8B runs", "")
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

	got, err := ReadPairs(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(pairs) {
		t.Fatalf("%d rows read from %d written", len(got), len(pairs))
	}
	if sc := Read(got); !sc.Believable {
		t.Errorf("the round trip lost something: %+v", sc)
	}
}

func TestAScoreWithNoRunOnItIsRefusedAtTheLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairs.jsonl")
	if err := os.WriteFile(path, []byte(`{"proxy":1,"anchor":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadPairs(path)
	if err == nil || !strings.Contains(err.Error(), "cannot be matched to a recipe") {
		t.Errorf("a score with no run on it read as %v", err)
	}
}

func TestAnEmptyFileIsNotAStudy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairs.jsonl")
	if err := os.WriteFile(path, []byte("# nothing yet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPairs(path); !errors.Is(err, ErrNoPairs) {
		t.Errorf("an empty file read as %v", err)
	}
}

func TestAFieldNobodyDefinedIsRefusedRatherThanIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairs.jsonl")
	if err := os.WriteFile(path, []byte(`{"run":"A01","proxy":1,"anchor":2,"vmlu_pro":3}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPairs(path); err == nil {
		t.Error("a field nobody defined was read and dropped")
	}
}

func TestAListOfRunsStopsBeingAListSomebodyReads(t *testing.T) {
	ids := []string{"A07", "A01", "A03", "A02", "A05", "A04", "A06"}
	got := join(ids)
	if !strings.HasPrefix(got, "A01, A02, A03, A04, A05") {
		t.Errorf("the list is not sorted: %s", got)
	}
	if !strings.HasSuffix(got, "and 2 mores") && !strings.HasSuffix(got, "and 2 more") {
		t.Errorf("the list does not say how many it left out: %s", got)
	}
	hasNot(t, []string{join(ids[:3])}, "more")
}
