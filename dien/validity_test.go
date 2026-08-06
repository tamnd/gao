package dien

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func recipe(name string, proxy, full float64) Recipe {
	return Recipe{Name: name, Proxy: proxy, Full: full, ProxyBox: "gamingpc", FullBox: "server1"}
}

// Five is the fewest that can tell agreement from luck, and under it the answer
// is that there is no answer rather than a number with a small sample beside it.
func TestFourRecipesCannotSayWhetherTheProxyWorks(t *testing.T) {
	runs := []Recipe{
		recipe("base", 0.41, 0.52),
		recipe("more web", 0.43, 0.54),
		recipe("more books", 0.45, 0.55),
		recipe("more news", 0.47, 0.58),
	}
	if _, err := Validate(runs); !errors.Is(err, ErrTooFew) {
		t.Errorf("four recipes returned %v, want %v", err, ErrTooFew)
	}
}

func TestAProxyThatRanksTheSlateTheSameWayIsDecisive(t *testing.T) {
	runs := []Recipe{
		recipe("base", 0.41, 0.52),
		recipe("more web", 0.43, 0.54),
		recipe("more books", 0.45, 0.55),
		recipe("more news", 0.47, 0.58),
		recipe("more forum", 0.49, 0.61),
	}
	v, err := Validate(runs)
	if err != nil {
		t.Fatal(err)
	}
	if v.Correlation != 1 {
		t.Errorf("two identical orderings correlate at %.2f", v.Correlation)
	}
	if v.Agree != v.Pairs || v.Pairs != 10 {
		t.Errorf("%d of %d pairs agreed, want 10 of 10", v.Agree, v.Pairs)
	}
	if !v.Decisive {
		t.Error("a proxy that got every pair right is not decisive")
	}
	if !strings.Contains(v.Why, "P10-1") {
		t.Errorf("the verdict does not say it cleared the prediction: %s", v.Why)
	}
}

// The kill criterion is the point of the measurement. A proxy that ranks the
// slate backwards has to say so in the words the release notes use, because the
// alternative is forty runs of tuning presented as evidence.
func TestAProxyThatRanksTheSlateBackwardsKillsTheSlate(t *testing.T) {
	runs := []Recipe{
		recipe("base", 0.41, 0.61),
		recipe("more web", 0.43, 0.58),
		recipe("more books", 0.45, 0.55),
		recipe("more news", 0.47, 0.54),
		recipe("more forum", 0.49, 0.52),
	}
	v, err := Validate(runs)
	if err != nil {
		t.Fatal(err)
	}
	if v.Correlation != -1 {
		t.Errorf("a reversed ordering correlates at %.2f", v.Correlation)
	}
	if v.Decisive {
		t.Error("a proxy that ranks the slate backwards was called decisive")
	}
	for _, want := range []string{"exploratory", "unvalidated"} {
		if !strings.Contains(v.Why, want) {
			t.Errorf("the verdict does not say %q: %s", want, v.Why)
		}
	}
}

// Between the kill criterion and the prediction is the case that gets written
// up wrong most easily, because the slate survived and the paper's number did
// not, and both of those have to be said.
func TestASlateThatSurvivesAPredictionThatDoesNotIsSaidPlainly(t *testing.T) {
	runs := []Recipe{
		recipe("base", 0.41, 0.54),
		recipe("more web", 0.43, 0.52),
		recipe("more books", 0.45, 0.58),
		recipe("more news", 0.47, 0.61),
		recipe("more forum", 0.49, 0.55),
	}
	v, err := Validate(runs)
	if err != nil {
		t.Fatal(err)
	}
	if v.Correlation < KillCorrelation || v.Correlation >= PredictedCorrelation {
		t.Fatalf("this fixture was meant to land between %.2f and %.2f, and it correlates at %.2f",
			KillCorrelation, PredictedCorrelation, v.Correlation)
	}
	if !v.Decisive {
		t.Error("a correlation over the kill criterion was called indecisive")
	}
	if !strings.Contains(v.Why, "The slate stands and the prediction does not") {
		t.Errorf("the verdict hides the missed prediction: %s", v.Why)
	}
}

// Two recipes the proxy could not tell apart is the proxy declining to make a
// call, and the ranking has to record that rather than crediting it with
// whichever was listed first.
func TestTwoRecipesTheProxyCouldNotSeparateShareARank(t *testing.T) {
	runs := []Recipe{
		recipe("base", 0.41, 0.52),
		recipe("more web", 0.45, 0.54),
		recipe("more books", 0.45, 0.55),
		recipe("more news", 0.47, 0.58),
		recipe("more forum", 0.49, 0.61),
	}
	proxy := ranks(runs, func(r Recipe) float64 { return r.Proxy })
	if proxy[1] != proxy[2] {
		t.Errorf("two recipes that scored the same got ranks %.1f and %.1f", proxy[1], proxy[2])
	}
	if proxy[1] != 1.5 {
		t.Errorf("a tie over the second and third places ranks at %.1f, want 1.5", proxy[1])
	}

	reversed := []Recipe{runs[0], runs[2], runs[1], runs[3], runs[4]}
	a, err := Validate(runs)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Validate(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(a.Correlation-b.Correlation) > 1e-12 {
		t.Errorf("listing the tied pair the other way round moved the correlation from %.4f to %.4f",
			a.Correlation, b.Correlation)
	}
	if a.Agree != b.Agree {
		t.Errorf("listing the tied pair the other way round moved the agreement from %d to %d", a.Agree, b.Agree)
	}
}

func TestOneRecipeScoredTwiceIsOnePieceOfEvidence(t *testing.T) {
	runs := []Recipe{
		recipe("base", 0.41, 0.52),
		recipe("more web", 0.43, 0.54),
		recipe("more web", 0.45, 0.55),
		recipe("more news", 0.47, 0.58),
		recipe("more forum", 0.49, 0.61),
	}
	if _, err := Validate(runs); err == nil {
		t.Fatal("the same recipe twice was accepted as two pairs")
	}
	problems := CheckRecipes(runs)
	if len(problems) != 1 || !strings.Contains(problems[0], "appears twice") {
		t.Errorf("the duplicate was not the problem reported: %v", problems)
	}
}

func TestAPairThatDoesNotSayWhereItRanIsRefused(t *testing.T) {
	runs := []Recipe{
		recipe("base", 0.41, 0.52),
		recipe("more web", 0.43, 0.54),
		{Name: "more books", Proxy: 0.45, Full: 0.55, ProxyBox: "gamingpc"},
		recipe("more news", 0.47, 0.58),
		recipe("more forum", 0.49, 0.61),
	}
	if _, err := Validate(runs); err == nil {
		t.Fatal("a pair with no box behind its full scale score was accepted")
	}
	problems := CheckRecipes(runs)
	if len(problems) != 1 || !strings.Contains(problems[0], "which box") {
		t.Errorf("the missing box was not the problem reported: %v", problems)
	}
}

func TestARecipeWithNoNameCannotBeReportedAgainst(t *testing.T) {
	runs := []Recipe{recipe("base", 0.41, 0.52), {Proxy: 0.43, Full: 0.54, ProxyBox: "gamingpc", FullBox: "server1"}}
	problems := CheckRecipes(runs)
	if len(problems) != 1 || !strings.Contains(problems[0], "no name") {
		t.Errorf("the unnamed recipe was not the problem reported: %v", problems)
	}
}

func TestTheVerdictReadsTheWayItIsPublished(t *testing.T) {
	runs := []Recipe{
		recipe("base", 0.41, 0.52),
		recipe("more web", 0.43, 0.54),
		recipe("more books", 0.45, 0.55),
		recipe("more news", 0.47, 0.58),
		recipe("more forum", 0.49, 0.61),
	}
	v, err := Validate(runs)
	if err != nil {
		t.Fatal(err)
	}
	out := v.String()
	for _, want := range []string{Name, "rank correlation 1.00", "10 of 10 pairs"} {
		if !strings.Contains(out, want) {
			t.Errorf("the verdict does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "P10-4") {
		t.Errorf("a run that cleared the predicted agreement still reported it as missed:\n%s", out)
	}
	if strings.Contains(out, "—") {
		t.Error("the verdict has an em dash in it")
	}
}

func TestARunUnderThePredictedAgreementSaysSo(t *testing.T) {
	runs := []Recipe{
		recipe("base", 0.41, 0.55),
		recipe("more web", 0.43, 0.54),
		recipe("more books", 0.45, 0.52),
		recipe("more news", 0.47, 0.58),
		recipe("more forum", 0.49, 0.61),
	}
	v, err := Validate(runs)
	if err != nil {
		t.Fatal(err)
	}
	if v.Agreement() >= PredictedAgreement {
		t.Fatalf("this fixture was meant to miss %.2f agreement and it came in at %.2f", PredictedAgreement, v.Agreement())
	}
	if !strings.Contains(v.String(), "P10-4") {
		t.Errorf("a run under the predicted agreement did not say so:\n%s", v.String())
	}
}
