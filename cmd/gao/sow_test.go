package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/sow"
)

// sowCard writes a card to a directory and returns it, so a test can bend one
// field and see what the checker says.
func sowCard(t *testing.T, change func(*sow.Card)) string {
	t.Helper()
	r := sow.Fixed()
	c := sow.Card{
		Recipe:    r.Digest(),
		Version:   r.Version,
		Box:       "gamingpc",
		Batch:     "vllm 0.9, batch 256, 2 sequences per prompt",
		RanAt:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Generated: 1_000_000,
		Kept:      812_400,
		Tokens:    1_940_000_000,
		Tokenizer: "gemma-3",
		Rejects: sow.Rejects{
			"vi-only":       9_100,
			"faithful":      74_800,
			"not-a-copy":    41_200,
			"degenerate":    31_500,
			"refusal":       12_600,
			"contamination": 18_400,
		},
		GPUHours: 96.5,
	}
	if change != nil {
		change(&c)
	}
	dir := t.TempDir()
	if err := sow.WriteCard(dir, c); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTheRecipePrintsWhatDecidesTheText(t *testing.T) {
	out, errOut, code := exec(t, "sow", "recipe")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"qwen3-235b-a22b-instruct", "seed 20260401", "4 registers", "6 gates", "contamination"} {
		if !strings.Contains(out, want) {
			t.Errorf("the recipe does not print %q:\n%s", want, out)
		}
	}
}

func TestTheRecipeSaysWhetherTheGeneratorReadTheCorpus(t *testing.T) {
	// A model trained on gao rephrasing gao is the corpus fed back into itself,
	// and the answer belongs on the printed card rather than in somebody's head.
	out, _, code := exec(t, "sow", "recipe")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "read gao") {
		t.Errorf("the recipe does not say whether the generator read gao:\n%s", out)
	}
}

func TestThePromptsComeOutVerbatim(t *testing.T) {
	out, _, code := exec(t, "sow", "recipe", "-prompts")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := sow.Fixed().Styles[0].Prompt
	if !strings.Contains(out, want) {
		t.Errorf("the prompt did not come out as it is:\n%s", out)
	}
	if !strings.Contains(out, "{{item}}") {
		t.Errorf("the slot the source goes into was not printed:\n%s", out)
	}
	if !strings.Contains(out, sow.Fixed().Digest().String()) {
		t.Errorf("the prompts came out without the recipe they belong to:\n%s", out)
	}
}

func TestTheRecipeSpeaksJSON(t *testing.T) {
	out, _, code := exec(t, "sow", "recipe", "-json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var report struct {
		Recipe struct {
			Generator string `json:"generator"`
			Styles    []struct {
				Name string `json:"name"`
			} `json:"styles"`
		} `json:"recipe"`
		Digest string   `json:"digest"`
		Faults []string `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out)
	}
	if report.Digest != sow.Fixed().Digest().String() {
		t.Errorf("the JSON carries a different digest: %s", report.Digest)
	}
	if len(report.Recipe.Styles) != sow.Styles || len(report.Faults) != 0 {
		t.Errorf("the JSON does not carry the recipe: %+v", report)
	}
}

func TestAnHonestCardPasses(t *testing.T) {
	dir := sowCard(t, nil)
	out, errOut, code := exec(t, "sow", "card", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "gamingpc") {
		t.Errorf("the card does not say which box it ran on:\n%s", out)
	}
	if !strings.Contains(out, "18.8%") {
		t.Errorf("the card does not print the reject rate:\n%s", out)
	}
	if !strings.Contains(out, "vietnamese-synthetic-text") {
		t.Errorf("the card does not say where the data goes:\n%s", out)
	}
	if !strings.Contains(out, "ever added to a natural count") {
		t.Errorf("the card does not say this is not natural text:\n%s", out)
	}
}

func TestACardFromAnotherRecipeFails(t *testing.T) {
	dir := sowCard(t, func(c *sow.Card) { c.Recipe = sow.Fixed().Digest(); c.Version = "0.9" })
	out, _, code := exec(t, "sow", "card", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "recipe 0.9") {
		t.Errorf("the report does not say the versions disagree:\n%s", out)
	}
}

func TestACardWhereNothingWasRejectedFails(t *testing.T) {
	dir := sowCard(t, func(c *sow.Card) {
		c.Kept = c.Generated
		for name := range c.Rejects {
			c.Rejects[name] = 0
		}
	})
	out, _, code := exec(t, "sow", "card", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "text nothing checked") {
		t.Errorf("the report does not say a zero reject rate is a gate that did not run:\n%s", out)
	}
}

func TestACardCarryingBenchmarkItemsIsNotPublishable(t *testing.T) {
	dir := sowCard(t, func(c *sow.Card) { c.Contaminated = 214 })
	out, _, code := exec(t, "sow", "card", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "scoring a model on its own training data") {
		t.Errorf("the report does not say why contamination matters:\n%s", out)
	}
}

func TestACardWhoseArithmeticDoesNotCloseFails(t *testing.T) {
	dir := sowCard(t, func(c *sow.Card) { c.Rejects["degenerate"] = 0 })
	out, _, code := exec(t, "sow", "card", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "missing an account of") {
		t.Errorf("the report does not say the numbers do not add up:\n%s", out)
	}
}

func TestTheCardSpeaksJSON(t *testing.T) {
	dir := sowCard(t, nil)
	out, _, code := exec(t, "sow", "card", "-json", dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var report struct {
		Card struct {
			Box    string `json:"box"`
			Kept   int64  `json:"kept"`
			Tokens int64  `json:"tokens"`
		} `json:"card"`
		Repo   string   `json:"repo"`
		Faults []string `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out)
	}
	if report.Card.Box != "gamingpc" || report.Card.Kept != 812_400 || report.Repo != sow.Repo {
		t.Errorf("the JSON does not carry the card: %+v", report)
	}
	if len(report.Faults) != 0 {
		t.Errorf("a good card was faulted: %v", report.Faults)
	}
}

func TestARecipeFromAFileCanBeUsed(t *testing.T) {
	// The shipped recipe is the one that matters, and a file is how somebody
	// checks a card that was produced under a different one.
	r := sow.Fixed()
	r.Version = "1.1"
	r.Decoding.Seed = 7
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "recipe.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "sow", "recipe", "-recipe", path)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "seed 7") {
		t.Errorf("the recipe from the file was not the one printed:\n%s", out)
	}

	dir := sowCard(t, nil)
	out, _, code = exec(t, "sow", "card", "-recipe", path, dir)
	if code != 1 {
		t.Fatalf("a card from the shipped recipe passed against a different one, exit %d", code)
	}
	if !strings.Contains(out, "moved after the run") {
		t.Errorf("the report does not say the recipes disagree:\n%s", out)
	}
}

func TestABrokenRecipeIsReportedRatherThanPrinted(t *testing.T) {
	r := sow.Fixed()
	r.Styles = r.Styles[:2]
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "recipe.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "sow", "recipe", "-recipe", path)
	if code != 1 {
		t.Fatalf("a two style recipe printed clean, exit %d", code)
	}
	if !strings.Contains(out, "narrows the distribution") {
		t.Errorf("the report does not say what is wrong with two styles:\n%s", out)
	}
}

func TestACardThatIsNotThereSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "sow", "card", t.TempDir())
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, sow.CardName) {
		t.Errorf("the error does not name what was missing: %s", errOut)
	}
}

func TestSayingNothingToSowAsksForTheUsage(t *testing.T) {
	_, errOut, code := exec(t, "sow")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "recipe") || !strings.Contains(errOut, "card") {
		t.Errorf("the usage does not list the subcommands: %s", errOut)
	}
}

func TestASowSubcommandNobodyWroteIsNamed(t *testing.T) {
	_, errOut, code := exec(t, "sow", "sow")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "no subcommand named sow") {
		t.Errorf("the error does not name it: %s", errOut)
	}
}
