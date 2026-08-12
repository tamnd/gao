package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/siet"
)

// sietLog writes a training log of n steps and returns the path. fix is handed
// each step before it is written, so a test can spoil one reading and leave the
// rest of the run alone.
func sietLog(t *testing.T, n int, fix func(i int, s *siet.Step)) string {
	t.Helper()
	var b strings.Builder
	for i := range n {
		f := float64(i) / float64(n)
		groups := 1536
		s := siet.Step{
			Step:      i + 1,
			Box:       "8xH200 booked",
			Groups:    groups,
			Kept:      int(float64(groups) * (0.85 - 0.30*f)),
			Rollouts:  groups * siet.Group,
			Truncated: groups * siet.Group / 100,
			ClipLow:   0.031,
			ClipHigh:  0.004,
			Entropy:   0.92 - 0.22*f,
			Reward:    0.41 + 0.33*f,
		}
		if fix != nil {
			fix(i, &s)
		}
		line, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	path := filepath.Join(t.TempDir(), "steps.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sietConfigFile(t *testing.T, r siet.Recipe) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "recipe.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSietRecipePrintsTheSettingsAndWhatTheyFix(t *testing.T) {
	out, errOut, code := exec(t, "siet", "recipe")
	if code != 0 {
		t.Fatalf("the planned recipe exits %d: %s", code, errOut)
	}
	for _, want := range []string{"critic", "group size", "clipping", "aggregation", "flat groups", "overlong", "0.20 low, 0.28 high", "token"} {
		if !strings.Contains(out, want) {
			t.Errorf("the recipe does not print %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "a value network is a second model") {
		t.Error("the reasons print without -why, which makes the table something to scroll past")
	}

	why, _, code := exec(t, "siet", "recipe", "-why")
	if code != 0 {
		t.Fatalf("-why exits %d", code)
	}
	if !strings.Contains(why, "a value network is a second model whose errors become the objective") {
		t.Errorf("-why prints no reason next to the critic:\n%s", why)
	}
}

func TestSietRecipeRefusesAConfigurationThatIsNotWhatItSaysItIs(t *testing.T) {
	r := siet.Plan()
	r.EpsHigh = r.EpsLow
	out, _, code := exec(t, "siet", "recipe", "-config", sietConfigFile(t, r))
	if code != 2 {
		t.Fatalf("symmetric clipping exits %d, and a configuration that cannot hold is worth an exit code", code)
	}
	if !strings.Contains(out, "This configuration is not what it says it is") {
		t.Errorf("the refusal is not written down:\n%s", out)
	}
	if !strings.Contains(out, "symmetric clipping with clip-higher written next to it") {
		t.Errorf("the refusal does not say what is wrong with it:\n%s", out)
	}
}

func TestSietReadsACleanRunAsOne(t *testing.T) {
	out, errOut, code := exec(t, "siet", "read", "-specialist", "dau", sietLog(t, 400, nil))
	if code != 0 {
		t.Fatalf("a clean run exits %d: %s", code, errOut)
	}
	for _, want := range []string{
		"groups that taught",
		"dau, 400 steps on 8xH200 booked.",
		"clipped tokens rather than sitting unused",
		"what the reward did is what the training did",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the reading does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "to read before the reward is") {
		t.Errorf("a clean run comes back with faults:\n%s", out)
	}
}

func TestSietReadsAStalledRunAsTheThreeThingsThatStalledIt(t *testing.T) {
	path := sietLog(t, 400, func(i int, s *siet.Step) {
		s.Truncated = s.Rollouts * 14 / 100
		if i >= 390 {
			s.Kept = s.Groups * 18 / 100
			s.Entropy = 0.407
		}
	})
	out, _, code := exec(t, "siet", "read", "-specialist", "dau", path)
	if code != 2 {
		t.Fatalf("a stalled run exits %d, and a run worth reading before the reward is worth an exit code", code)
	}
	for _, want := range []string{
		"3 things to read before the reward is",
		"the policy closing rather than the policy learning",
		"the length limit is grading answers the verifier never saw",
		"wants 5.6x to fill",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the reading does not say %q:\n%s", want, out)
		}
	}
}

func TestSietNamesTheRunItWasNotToldTheSpecialistFor(t *testing.T) {
	out, _, code := exec(t, "siet", "read", sietLog(t, 40, nil))
	if code != 0 {
		t.Fatalf("an unnamed run exits %d", code)
	}
	if !strings.Contains(out, "an unnamed specialist, 40 steps") {
		t.Errorf("a log with no name on it is not read:\n%s", out)
	}
}

func TestSietRefusesALogThatIsNotOneRun(t *testing.T) {
	path := sietLog(t, 20, func(i int, s *siet.Step) {
		if i == 7 {
			s.Box = "somewhere else"
		}
	})
	out, _, code := exec(t, "siet", "read", path)
	if code != 1 {
		t.Fatalf("two boxes in one log exits %d, and a log that is not one run is a different failure from a run that went wrong", code)
	}
	if !strings.Contains(out, "This log is not one training run, so nothing in it is read") {
		t.Errorf("the refusal is not written down:\n%s", out)
	}
	if strings.Contains(out, "groups that taught") {
		t.Errorf("a refused log is read anyway:\n%s", out)
	}
}

func TestSietReadsAMissingOrMalformedLogAsTheFileItIs(t *testing.T) {
	_, errOut, code := exec(t, "siet", "read", filepath.Join(t.TempDir(), "nothing.jsonl"))
	if code != 1 {
		t.Fatalf("a missing log exits %d", code)
	}
	if !strings.Contains(errOut, "no such file") {
		t.Errorf("the missing file is not named: %s", errOut)
	}

	bad := filepath.Join(t.TempDir(), "steps.jsonl")
	if err := os.WriteFile(bad, []byte("{\"step\":1}\n{\"step\":2,\"kept\":\"most\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code = exec(t, "siet", "read", bad)
	if code != 1 {
		t.Fatalf("a malformed log exits %d", code)
	}
	if !strings.Contains(errOut, "steps.jsonl:2") {
		t.Errorf("the line that could not be read is not named: %s", errOut)
	}
}

func TestSietPrintsJSONWithTheSameReadingsInIt(t *testing.T) {
	out, _, code := exec(t, "siet", "read", "-specialist", "dau", "-json", sietLog(t, 400, nil))
	if code != 0 {
		t.Fatalf("-json exits %d", code)
	}
	var got struct {
		Specialist string   `json:"specialist"`
		Box        string   `json:"box"`
		Steps      int      `json:"steps"`
		Binds      bool     `json:"upper_bound_binds"`
		Fills      bool     `json:"batch_fills"`
		Holds      bool     `json:"holds"`
		Faults     []string `json:"faults"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Specialist != "dau" || got.Box != "8xH200 booked" || got.Steps != 400 {
		t.Errorf("the JSON reads %s over %d steps on %s", got.Specialist, got.Steps, got.Box)
	}
	if !got.Binds || !got.Fills || !got.Holds || len(got.Faults) > 0 {
		t.Errorf("the JSON disagrees with the printed reading of the same run: %+v", got)
	}

	recipe, _, code := exec(t, "siet", "recipe", "-json")
	if code != 0 {
		t.Fatalf("recipe -json exits %d", code)
	}
	var conf struct {
		Recipe  siet.Recipe `json:"recipe"`
		Context int         `json:"context"`
		Holds   bool        `json:"holds"`
	}
	if err := json.Unmarshal([]byte(recipe), &conf); err != nil {
		t.Fatalf("%v\n%s", err, recipe)
	}
	if conf.Recipe != siet.Plan() || conf.Context != siet.Context || !conf.Holds {
		t.Errorf("the JSON recipe is not the one the plan fixed: %+v", conf)
	}
}

func TestSietUsageIsPrintedForWhatItCannotDo(t *testing.T) {
	out, _, code := exec(t, "siet", "help")
	if code != 0 {
		t.Fatalf("help exits %d", code)
	}
	if !strings.Contains(out, "gao siet recipe") || !strings.Contains(out, "gao siet read") {
		t.Errorf("help does not list the subcommands:\n%s", out)
	}

	for _, args := range [][]string{
		{"siet"},
		{"siet", "measure"},
		{"siet", "read"},
		{"siet", "read", "one.jsonl", "two.jsonl"},
		{"siet", "recipe", "extra"},
	} {
		_, errOut, code := exec(t, args...)
		if code != 2 {
			t.Errorf("%v exits %d", args, code)
		}
		if !strings.Contains(errOut, "usage: gao siet") {
			t.Errorf("%v does not print usage: %s", args, errOut)
		}
	}

	_, errOut, code := exec(t, "siet", "recipe", "-config", filepath.Join(t.TempDir(), "nothing.json"))
	if code != 1 {
		t.Fatalf("a missing configuration exits %d", code)
	}
	if !strings.Contains(errOut, "nothing.json") {
		t.Errorf("the missing configuration is not named: %s", errOut)
	}
}

func TestSietReadsARunAgainstTheConfigurationItWasTakenUnder(t *testing.T) {
	r := siet.Plan()
	r.Oversample = 8
	out, _, code := exec(t, "siet", "read", "-config", sietConfigFile(t, r), sietLog(t, 40, func(i int, s *siet.Step) {
		if i >= 30 {
			s.Kept = s.Groups / 5
		}
	}))
	if code != 0 {
		t.Fatalf("a run sampled at 8.0x exits %d:\n%s", code, out)
	}
	if !strings.Contains(out, "the batch fills at the yield the run is at now") {
		t.Errorf("a fifth yield under 8.0x sampling reads as a batch that stopped filling:\n%s", out)
	}
	if !strings.Contains(out, "20.0%") {
		t.Errorf("the late yield is not printed:\n%s", out)
	}
}
