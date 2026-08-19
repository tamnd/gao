package sow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
)

// honest is a card that matches the fixed recipe and describes a run somebody
// actually watched.
func honest() Card {
	r := Fixed()
	return Card{
		Recipe:    r.Digest(),
		Version:   r.Version,
		Box:       "gamingpc",
		Batch:     "vllm 0.9, batch 256, 2 sequences per prompt",
		RanAt:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Generated: 1_000_000,
		Kept:      812_400,
		Tokens:    1_940_000_000,
		Tokenizer: "gemma-3",
		Rejects: Rejects{
			"vi-only":       9_100,
			"faithful":      74_800,
			"not-a-copy":    41_200,
			"degenerate":    31_500,
			"refusal":       12_600,
			"contamination": 18_400,
		},
		GPUHours: 96.5,
	}
}

func faultAbout(t *testing.T, faults []string, want string) {
	t.Helper()
	for _, f := range faults {
		if strings.Contains(f, want) {
			return
		}
	}
	t.Errorf("no fault mentions %q, got:\n  %s", want, strings.Join(faults, "\n  "))
}

func noFaultAbout(t *testing.T, faults []string, want string) {
	t.Helper()
	for _, f := range faults {
		if strings.Contains(f, want) {
			t.Errorf("unexpected fault about %q: %s", want, f)
		}
	}
}

func TestTheFixedRecipeIsARecipe(t *testing.T) {
	if err := Fixed().check(); err != nil {
		t.Fatalf("the recipe this project ships does not pass its own checks:\n%v", err)
	}
}

func TestAnHonestCardPasses(t *testing.T) {
	if faults := honest().Against(Fixed()); len(faults) > 0 {
		t.Errorf("a good card was faulted:\n  %s", strings.Join(faults, "\n  "))
	}
	if faults := honest().Publishable(); len(faults) > 0 {
		t.Errorf("a good card was refused publication:\n  %s", strings.Join(faults, "\n  "))
	}
}

func TestARecipeWithFewerStylesThanItPromisesIsRefused(t *testing.T) {
	// The mixture spends 150 billion tokens on this and says four styles is why
	// it is safe to. Three is a different bet than the one that was approved.
	r := Fixed()
	r.Styles = r.Styles[:3]

	err := r.check()
	if err == nil {
		t.Fatal("three styles passed")
	}
	if !strings.Contains(err.Error(), "narrows the distribution") {
		t.Errorf("the error does not say what is wrong with three styles:\n%v", err)
	}
}

func TestTwoStylesWithTheSamePromptAreOneStyle(t *testing.T) {
	r := Fixed()
	r.Styles[2].Prompt = r.Styles[1].Prompt

	err := r.check()
	if err == nil {
		t.Fatal("a duplicated prompt passed as two styles")
	}
	if !strings.Contains(err.Error(), "counted twice") {
		t.Errorf("the error does not name the duplication:\n%v", err)
	}
}

func TestAPromptWithNowhereForTheSourceToGoRephrasesNothing(t *testing.T) {
	r := Fixed()
	r.Styles[0].Prompt = "Viết một đoạn văn tiếng Việt theo văn phong báo chí."

	err := r.check()
	if err == nil {
		t.Fatal("a prompt that generates from nothing passed as a rephrase")
	}
	if !strings.Contains(err.Error(), "does not rephrase anything") {
		t.Errorf("the error does not say the prompt reads no source:\n%v", err)
	}
}

func TestAGeneratorTrainedOnGaoIsRefused(t *testing.T) {
	r := Fixed()
	r.ReadGao = true

	err := r.check()
	if err == nil {
		t.Fatal("the corpus was allowed to rephrase itself")
	}
	if !strings.Contains(err.Error(), "feeds the corpus back into itself") {
		t.Errorf("the error does not say why that is circular:\n%v", err)
	}
}

func TestAnUnpinnedSourceIsNotARephraseOfAnything(t *testing.T) {
	r := Fixed()
	r.SourceDigest = doc.Hash{}

	err := r.check()
	if err == nil {
		t.Fatal("an unpinned source passed")
	}
	if !strings.Contains(err.Error(), "which text was rephrased") {
		t.Errorf("the error does not say what the pin is for:\n%v", err)
	}
}

func TestARunWithoutASeedCannotBeRunAgain(t *testing.T) {
	r := Fixed()
	r.Decoding.Seed = 0

	err := r.check()
	if err == nil {
		t.Fatal("an unseeded run passed")
	}
	if !strings.Contains(err.Error(), "including us") {
		t.Errorf("the error does not say who else cannot reproduce it:\n%v", err)
	}
}

func TestGreedyDecodingCollapsesTheStyles(t *testing.T) {
	r := Fixed()
	r.Decoding.Temperature = 0

	err := r.check()
	if err == nil {
		t.Fatal("temperature zero passed")
	}
	if !strings.Contains(err.Error(), "one voice") {
		t.Errorf("the error does not connect greedy decoding to narrowing:\n%v", err)
	}
}

func TestAFilterWithoutAConfigHashCannotBeRerun(t *testing.T) {
	r := Fixed()
	r.Filters[1].ConfigHash = doc.Hash{}

	err := r.check()
	if err == nil {
		t.Fatal("an unpinned filter passed")
	}
	if !strings.Contains(err.Error(), "faithful has no config hash") {
		t.Errorf("the error does not name the filter:\n%v", err)
	}
}

func TestACardWhoseRecipeMovedAfterTheRunFails(t *testing.T) {
	// This is the whole reason the recipe is hashed. A prompt improved after
	// seeing the output produces text nobody can attribute to a recipe.
	r := Fixed()
	c := honest()
	r.Styles[0].Prompt += " Viết hay hơn nữa."

	faultAbout(t, c.Against(r), "moved after the run")
}

func TestANoteDoesNotMoveTheRecipeDigest(t *testing.T) {
	a := Fixed()
	b := Fixed()
	b.Note = "a longer explanation of why the source is the educational slice"
	b.Styles[0].Note = "and why this register is first"
	b.Filters[0].Why = "and what the language filter is actually catching"

	if a.Digest() != b.Digest() {
		t.Error("writing a clearer explanation invalidated the recipe, which teaches people to stop writing explanations")
	}
}

func TestTheDigestMovesWithAnythingThatChangesTheText(t *testing.T) {
	base := Fixed().Digest()
	for _, tc := range []struct {
		what   string
		change func(*Recipe)
	}{
		{"the generator", func(r *Recipe) { r.Generator = "something-else" }},
		{"the revision", func(r *Recipe) { r.Revision = "2026-05-01" }},
		{"a prompt", func(r *Recipe) { r.Styles[0].Prompt += " " }},
		{"the temperature", func(r *Recipe) { r.Decoding.Temperature = 0.7 }},
		{"the seed", func(r *Recipe) { r.Decoding.Seed = 1 }},
		{"the token limit", func(r *Recipe) { r.Decoding.MaxTokens = 2048 }},
		{"the source", func(r *Recipe) { r.SourceDigest = doc.SumString("another slice") }},
		{"a filter's settings", func(r *Recipe) { r.Filters[0].ConfigHash = doc.SumString("looser") }},
		{"the roster", func(r *Recipe) { r.Roster = "nhat-2026.09" }},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := Fixed()
			tc.change(&r)
			if r.Digest() == base {
				t.Errorf("changing %s did not change the recipe", tc.what)
			}
		})
	}
}

func TestTheDigestDoesNotDependOnTheOrderTheStylesWereListed(t *testing.T) {
	r := Fixed()
	r.Styles[0], r.Styles[3] = r.Styles[3], r.Styles[0]
	r.Filters[0], r.Filters[2] = r.Filters[2], r.Filters[0]

	if r.Digest() != Fixed().Digest() {
		t.Error("reordering the styles changed the recipe, which would make a rebase look like a different run")
	}
}

func TestARejectRateOfZeroIsAGateThatDidNotRun(t *testing.T) {
	c := honest()
	c.Kept = c.Generated
	c.Rejects = Rejects{}
	for _, f := range Fixed().Filters {
		c.Rejects[f.Name] = 0
	}

	faultAbout(t, c.Against(Fixed()), "text nothing checked")
}

func TestACardWhoseArithmeticDoesNotCloseFails(t *testing.T) {
	c := honest()
	c.Rejects["faithful"] -= 1_000

	faultAbout(t, c.Against(Fixed()), "missing an account of 1000 documents")
}

func TestAFilterTheCardNeverMentionsIsNotAFilterThatPassedEverything(t *testing.T) {
	c := honest()
	c.Kept += c.Rejects["refusal"]
	delete(c.Rejects, "refusal")

	faultAbout(t, c.Against(Fixed()), "refusal is in the recipe and the card says nothing about it")
}

func TestAGateNobodyDeclaredIsNamed(t *testing.T) {
	c := honest()
	c.Kept -= 500
	c.Rejects["something-new"] = 500

	faultAbout(t, c.Against(Fixed()), "not a filter in the recipe")
}

func TestACardThatDoesNotSayWhichBoxItRanOnFails(t *testing.T) {
	c := honest()
	c.Box = ""
	c.Batch = ""

	faults := c.Against(Fixed())
	faultAbout(t, faults, "which box it ran on")
	faultAbout(t, faults, "how it was batched")
}

func TestARunOverBudgetSaysSo(t *testing.T) {
	c := honest()
	c.GPUHours = Budget + 1

	faultAbout(t, c.Against(Fixed()), "rather than from the electricity")
}

func TestContaminatedOutputIsNotPublishable(t *testing.T) {
	c := honest()
	c.Contaminated = 3

	faultAbout(t, c.Publishable(), "scoring a model on its own training data")
	noFaultAbout(t, c.Against(Fixed()), "training data")
}

func TestSyntheticTextIsNeverCountedAsNatural(t *testing.T) {
	c := honest()
	if c.Natural() != 0 {
		t.Errorf("a synthetic card contributed %d natural documents", c.Natural())
	}
	if c.Source().Natural() {
		t.Error("gao-synth is claiming to be natural text")
	}
}

func TestTheYieldAndRejectRateAgree(t *testing.T) {
	c := honest()
	if got := c.Yield() + c.RejectRate(); got < 0.999 || got > 1.001 {
		t.Errorf("yield plus reject rate is %v", got)
	}
	if c.RejectRate() < 0.18 || c.RejectRate() > 0.19 {
		t.Errorf("the honest card rejects %v, which is not what its numbers say", c.RejectRate())
	}
}

func TestAnEmptyCardReportsNothingRatherThanDividingByZero(t *testing.T) {
	var c Card
	if c.Yield() != 0 || c.RejectRate() != 0 {
		t.Error("an empty card produced a rate")
	}
}

func TestACardRoundTripsThroughItsFile(t *testing.T) {
	dir := t.TempDir()
	want := honest()
	if err := WriteCard(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCard(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RanAt.Equal(want.RanAt) {
		t.Errorf("ran_at came back %v", got.RanAt)
	}
	got.RanAt, want.RanAt = time.Time{}, time.Time{}
	if a, b := mustJSON(t, got), mustJSON(t, want); a != b {
		t.Errorf("the card changed on the way through the file:\n%s\n%s", a, b)
	}
}

func TestACardIsNotOverwritten(t *testing.T) {
	// A card records a run that happened. A second run is a second card.
	dir := t.TempDir()
	if err := WriteCard(dir, honest()); err != nil {
		t.Fatal(err)
	}
	if err := WriteCard(dir, honest()); err == nil {
		t.Error("a card was overwritten")
	}
}

func TestATypoInACardIsAnErrorRatherThanADefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, CardName), []byte(`{"gpu_hour": 96.5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCard(dir); err == nil {
		t.Error("a misspelled field was read as an absent one")
	}
}

func TestACardThatAccountsForNoRejectionsIsNotACard(t *testing.T) {
	dir := t.TempDir()
	b, err := json.Marshal(Card{Box: "gamingpc", Generated: 10, Kept: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, CardName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCard(dir); err == nil {
		t.Error("a card with no rejection accounting was read")
	}
}

func TestARecipeCanBeReadFromAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipe.json")
	b, err := json.Marshal(Fixed())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := ReadRecipe(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Digest() != Fixed().Digest() {
		t.Error("a recipe written and read back is a different recipe")
	}
}

func TestARecipeThatIsNotThereSaysSo(t *testing.T) {
	if _, err := ReadRecipe(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("a missing recipe read fine")
	}
}

func TestTheDescriptionSaysWhatTheDataIs(t *testing.T) {
	got := Fixed().Describe()
	for _, want := range []string{"gao-synth", "4 registers", "6 gates", "never counted as any"} {
		if !strings.Contains(got, want) {
			t.Errorf("the description does not say %q:\n%s", want, got)
		}
	}
}

func TestTheStylesAndFiltersCanBeLookedUpByName(t *testing.T) {
	r := Fixed()
	if s, ok := r.Style("hoi-dap"); !ok || s.Prompt == "" {
		t.Error("hoi-dap is not in the recipe")
	}
	if _, ok := r.Style("nothing"); ok {
		t.Error("a style nobody wrote was found")
	}
	if f, ok := r.Filter("contamination"); !ok || f.ConfigHash.IsZero() {
		t.Error("the contamination gate is not in the recipe")
	}
	if _, ok := r.Filter("nothing"); ok {
		t.Error("a filter nobody wrote was found")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
