package graft

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// budget is the continued pretraining run's token budget, which every recovery
// is a share of.
const budget int64 = 40_000_000_000

// expansion is one graft onto gemma-3-12b, whose embeddings are tied and whose
// width is 3840.
func expansion(method string, covered, after float64, recovered int64) Expansion {
	return Expansion{
		Base: "gemma-3-12b", Method: method, Tied: true,
		Vocab: 262_144, New: 32_768, Dim: 3840,
		Covered: covered, Before: 2.11, After: after,
		BaseNorm: 1.42, NewNorm: 1.38,
		Frozen: 2000, LossBefore: 2.0412, Spike: 2.6180, Recovered: recovered,
		Box: "gamingpc",
	}
}

func trial() Trial {
	return Trial{Budget: budget, Runs: []Expansion{
		expansion("pieces", 0.31, 1.62, 1_800_000_000),
		expansion("mean", 0.31, 1.62, 5_600_000_000),
	}}
}

func refuses(t *testing.T, tr Trial, want string) {
	t.Helper()
	for _, why := range tr.Blocking() {
		if strings.Contains(why, want) {
			return
		}
	}
	t.Errorf("nothing blocking mentions %q, got:\n  %s", want, strings.Join(tr.Blocking(), "\n  "))
}

func TestTheGraftIsJudgedByWhatItNetsRatherThanByFertility(t *testing.T) {
	tr := trial()
	if !tr.Settled() {
		t.Fatalf("a clean trial was refused: %v", tr.Blocking())
	}
	if !tr.Holds() {
		t.Fatalf("a clean trial did not hold: %s", tr.Verdict())
	}
	b, _ := tr.Best()
	if b.Method != "pieces" {
		t.Errorf("the best method came back as %s", b.Method)
	}
	if got := b.Gain(); got < 0.23 || got > 0.24 {
		t.Errorf("2.11 to 1.62 tokens a syllable came back as %.3f", got)
	}
	// Both methods bought the same fertility, because fertility is a property
	// of the tokenizer and not of the rows. Only the recovery separates them.
	other := tr.Ranked()[1]
	if other.Gain() != b.Gain() {
		t.Errorf("two initializations of one tokenizer gave %.3f and %.3f", b.Gain(), other.Gain())
	}
	if b.Net(budget) <= other.Net(budget) {
		t.Errorf("a %s recovery netted no better than a %s one", billions(b.Recovered), billions(other.Recovered))
	}
	// Tied embeddings are one matrix, so the graft is 32768 rows and not 65536.
	if got := b.Params(); got != 32_768*3840 {
		t.Errorf("a tied graft came back as %d parameters", got)
	}
	for _, want := range []string{"gemma-3-12b by pieces is the best of 2 methods", "240 MB of new parameters", "4.5% of the run spent recovering", "nets 18.7%"} {
		if !strings.Contains(tr.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, tr.Verdict())
		}
	}
}

// The failure the package exists for. The fertility number is free, correct,
// and available before a step is trained, and it is not the answer.
func TestFertilityImprovingIsNotTheExpansionPayingForItself(t *testing.T) {
	slow := trial()
	slow.Runs[0].Recovered = 9_000_000_000
	slow.Runs[1].Recovered = 11_000_000_000
	if slow.Holds() {
		t.Fatal("an expansion that spent almost a quarter of the run recovering held")
	}
	for _, want := range []string{"22.5% of the run getting back to the loss it started at", "the sequences are shorter and the run is worse"} {
		if !strings.Contains(slow.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, slow.Verdict())
		}
	}

	never := trial()
	never.Runs[0].Recovered = 0
	never.Runs[1].Recovered = 0
	if never.Holds() {
		t.Fatal("an expansion that never came back held")
	}
	if !strings.Contains(never.Verdict(), "the fertility figure is the only thing that improved") {
		t.Errorf("the verdict does not say what never recovering means: %s", never.Verdict())
	}
	if got := len(never.Stranded()); got != 2 {
		t.Errorf("%d expansions came back stranded", got)
	}

	small := trial()
	small.Runs[0].After = 1.95
	small.Runs[1].After = 1.95
	if small.Holds() {
		t.Fatal("a 7.6% fertility gain cleared a 15% line")
	}
	if !strings.Contains(small.Verdict(), "stays on the vocabulary it came with") {
		t.Errorf("the verdict does not say what a small gain means: %s", small.Verdict())
	}
}

// The mechanics the item asks for are the rows, and this is where they go wrong.
func TestTheNewRowsAreTheMechanics(t *testing.T) {
	random := trial()
	random.Runs[0].Method = "random"
	refuses(t, random, "goes through every layer of a body that was already right")

	unsaid := trial()
	unsaid.Runs[0].Method = ""
	refuses(t, unsaid, "the whole of the mechanics this item asks for")

	odd := trial()
	odd.Runs[0].Method = "zeros"
	refuses(t, odd, "not one of random, mean, or pieces")

	// Averaging a quarter of a million vectors mostly cancels, and the row that
	// comes out is a token the output head cannot reach.
	flat := trial()
	flat.Runs[0].NewNorm = 0.11
	refuses(t, flat, "produces a flat logit for")

	loud := trial()
	loud.Runs[0].NewNorm = 3.4
	refuses(t, loud, "reaches for before it has any reason to")

	unmeasured := trial()
	unmeasured.Runs[0].NewNorm = 0
	refuses(t, unmeasured, "small in a way nothing about the method's name says")

	// Untied embeddings are two decisions and only one of them gets made.
	untied := trial()
	untied.Runs[0].Tied = false
	refuses(t, untied, "nothing said about writing them")
	if got := untied.Runs[0].Params(); got != 2*32_768*3840 {
		t.Errorf("an untied graft came back as %d parameters", got)
	}

	warm := trial()
	warm.Runs[0].Frozen = 0
	refuses(t, warm, "went into weights that were already trained")
}

// Two ids for one string is the quiet one, because everything about the model
// keeps working and merge order decides which id the text becomes.
func TestATokenTheBaseAlreadyHadIsTwoIdsForOneString(t *testing.T) {
	tr := trial()
	tr.Runs[0].Duplicate = 1174
	refuses(t, tr, "merge order decides which the model is shown, having trained on the other")

	thin := trial()
	thin.Runs[0].Covered = 0.012
	refuses(t, thin, "bought a vocabulary this text does not use")

	unknown := trial()
	unknown.Runs[0].Covered = 0
	refuses(t, unknown, "the parameters bought something unmeasured")
}

func TestATrialHasToBeAComparisonAndAMeasurement(t *testing.T) {
	one := Trial{Budget: budget, Runs: []Expansion{expansion("pieces", 0.31, 1.62, 1_800_000_000)}}
	refuses(t, one, "the method somebody picked rather than the answer to which one is better")

	twice := trial()
	twice.Runs[1] = twice.Runs[0]
	refuses(t, twice, "two readings of one graft are not two grafts")

	mixed := trial()
	mixed.Runs[1].Base = "llama-3.3-8b"
	refuses(t, mixed, "a comparison of the bodies")

	nobox := trial()
	nobox.Runs[0].Box = ""
	refuses(t, nobox, "is one box's locale")

	nogain := trial()
	nogain.Runs[0].After = 2.4
	refuses(t, nogain, "no more cheaply than the one it replaced")

	oneside := trial()
	oneside.Runs[0].Before = 0
	refuses(t, oneside, "fertility on only one side of the graft")

	blown := trial()
	blown.Runs[0].Spike = 4.2
	refuses(t, blown, "no way to read what it was being shown")

	nobudget := Trial{Runs: trial().Runs}
	refuses(t, nobudget, "nothing to be a share of")

	overbudget := trial()
	overbudget.Runs[0].Recovered = 50_000_000_000
	refuses(t, overbudget, "not a recovery inside this run")

	nobase := trial()
	nobase.Runs[0].Base = ""
	refuses(t, nobase, "a cost against weights that already exist")

	empty := Trial{Budget: budget}
	if empty.Settled() || empty.Holds() {
		t.Error("a trial with nothing in it settled the mechanics item")
	}
	if _, ok := empty.Best(); ok {
		t.Error("a trial with nothing in it has a best method")
	}
	if !strings.Contains(empty.Verdict(), "whatever the base model arrived with") {
		t.Errorf("the verdict on nothing reads %q", empty.Verdict())
	}
}

func TestATrialIsReadFromWhatTheExpansionRunsAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "expansions.jsonl")
	body := `{"base":"gemma-3-12b","method":"pieces","tied":true,"vocab":262144,"new":32768,"dim":3840,"covered":0.31,"before":2.11,"after":1.62,"base_norm":1.42,"new_norm":1.38,"frozen":2000,"loss_before":2.0412,"spike":2.618,"recovered":1800000000,"box":"gamingpc"}

{"base":"gemma-3-12b","method":"mean","tied":true,"vocab":262144,"new":32768,"dim":3840,"covered":0.31,"before":2.11,"after":1.62,"base_norm":1.42,"new_norm":1.38,"frozen":2000,"loss_before":2.0412,"spike":2.618,"recovered":5600000000,"box":"gamingpc"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := ReadTrial(budget, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Runs) != 2 || !tr.Holds() {
		t.Fatalf("read %d expansions, holds %v: %s", len(tr.Runs), tr.Holds(), tr.Verdict())
	}
	if b, _ := tr.Best(); b.Method != "pieces" {
		t.Errorf("the best method came back as %s", b.Method)
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"base":"gemma-3-12b","fertility":1.62}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrial(budget, bad); err == nil {
		t.Error("a line with a column nobody declared was read")
	}

	blank := filepath.Join(dir, "blank.jsonl")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrial(budget, blank); err == nil {
		t.Error("an empty file was read as a trial")
	}
	if _, err := ReadTrial(budget, filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a trial that is not there was read")
	}
}
