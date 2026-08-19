package seal

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/pick"
)

// small is a valid harness with nothing in it that is not needed, so that a test
// changing one field is changing one thing.
func small() Harness {
	return Harness{
		Version: "2026-08-07",
		Roster:  "2026-08-07",
		Arms:    []string{"a", "b"},
		Tasks: []Task{
			{Benchmark: "vmlu", Metric: Accuracy, Shots: 5, Seed: 1, Prompt: "{{shots}}\n{{item}}", Extract: Likelihood},
			{Benchmark: "vi-diacritic", Metric: DER, Prompt: "{{item}}", Extract: Whole},
		},
	}
}

func TestTheFixedHarnessIsAHarness(t *testing.T) {
	h, err := Fixed()
	if err != nil {
		t.Fatalf("the harness in the repository does not load: %v", err)
	}
	if len(h.Arms) != 3 {
		t.Errorf("the harness names %d arms, and S7 compares three", len(h.Arms))
	}
	if len(h.Tasks) < 10 {
		t.Errorf("the harness holds %d tasks", len(h.Tasks))
	}
	if h.Digest().IsZero() {
		t.Error("the harness has no digest")
	}
}

func TestEveryTaskNamesABenchmarkOnTheRoster(t *testing.T) {
	h, err := Fixed()
	if err != nil {
		t.Fatal(err)
	}
	r, err := pick.Rostered()
	if err != nil {
		t.Fatal(err)
	}
	// The roster only grows, so a name it does not hold is a name nobody has
	// agreed gao is judged on.
	for _, f := range h.Against(r) {
		t.Error(f)
	}
}

func TestTheUnpinnedBenchmarksAreNamedRatherThanCounted(t *testing.T) {
	h, err := Fixed()
	if err != nil {
		t.Fatal(err)
	}
	r, err := pick.Rostered()
	if err != nil {
		t.Fatal(err)
	}
	// This is expected to be non-empty today. The test is that the harness can
	// say which ones, because that is the work list standing between it and a
	// result somebody else can reproduce.
	for _, name := range h.Unpinned(r) {
		e, ok := entry(r, name)
		if !ok {
			t.Fatalf("%s came back unpinned and is not on the roster", name)
		}
		if e.Version != pick.Unpinned {
			t.Errorf("%s came back unpinned and its revision is %s", name, e.Version)
		}
		if e.Pending == "" {
			t.Errorf("%s is unpinned and does not say what it is waiting for", name)
		}
	}
}

func entry(r pick.Roster, name string) (pick.Entry, bool) {
	for _, e := range r.Benchmarks {
		if e.Name == name {
			return e, true
		}
	}
	return pick.Entry{}, false
}

func TestTheDigestDoesNotMoveWhenTheFileIsRearranged(t *testing.T) {
	a := small()
	b := small()
	b.Arms = []string{"b", "a"}
	b.Tasks = []Task{b.Tasks[1], b.Tasks[0]}
	if a.Digest() != b.Digest() {
		t.Error("reordering the arms and the tasks changed the digest, so every edit to the file would void the results")
	}
}

func TestImprovingANoteDoesNotChangeTheDigest(t *testing.T) {
	a := small()
	b := small()
	b.Note = "a sentence about why this exists"
	b.Tasks[0].Note = "a clearer sentence about what this task measures"
	if a.Digest() != b.Digest() {
		t.Error("writing a better explanation changed the digest, which teaches people to stop writing explanations")
	}
}

func TestChangingTheMeasurementChangesTheDigest(t *testing.T) {
	base := small().Digest()
	for name, change := range map[string]func(*Harness){
		"the prompt":     func(h *Harness) { h.Tasks[0].Prompt = "khác: {{shots}} {{item}}" },
		"the shot count": func(h *Harness) { h.Tasks[0].Shots = 4 },
		"the seed":       func(h *Harness) { h.Tasks[0].Seed = 2 },
		"the metric":     func(h *Harness) { h.Tasks[0].Metric = ExactMatch },
		"the extraction": func(h *Harness) { h.Tasks[0].Extract = FirstOption },
		"the version":    func(h *Harness) { h.Version = "2026-09-01" },
		"the roster":     func(h *Harness) { h.Roster = "2026-09-01" },
		"an added arm":   func(h *Harness) { h.Arms = append(h.Arms, "c") },
		"a dropped arm":  func(h *Harness) { h.Arms = h.Arms[:1] },
		"an added task": func(h *Harness) {
			h.Tasks = append(h.Tasks, Task{Benchmark: "mbpp", Metric: PassRate, Prompt: "{{item}}", Extract: CodeBlock})
		},
		"a dropped task": func(h *Harness) { h.Tasks = h.Tasks[:1] },
	} {
		h := small()
		change(&h)
		if h.Digest() == base {
			t.Errorf("changing %s did not change the digest", name)
		}
	}
}

func TestADigestCannotBeForgedBySpellingAFieldLikeTwo(t *testing.T) {
	// The length prefix in the canonical form is what stops this. Without it a
	// prompt could be written to close its own field and open the next one, and
	// two different harnesses would hash the same.
	a := small()
	a.Tasks[0].Extract = Whole
	a.Tasks[0].Prompt = "{{item}}"

	b := small()
	b.Tasks[0].Extract = Whole
	b.Tasks[0].Prompt = "{{item}}\nextract 5:whole"

	if a.Digest() == b.Digest() {
		t.Error("two harnesses with different prompts hash the same")
	}
}

func TestTheClosedHarnessKeepsItsDigest(t *testing.T) {
	// The digest goes in a release note and in every published result, so it is
	// pinned here rather than recomputed. This test failing means the harness
	// changed, which is the event the whole package exists to make loud. If the
	// change was meant, the version goes up, this number is replaced, and the
	// results produced under the old one are not comparable to the new.
	const want = "e4d71047c881575bd9d77f37c06dc99beed2596e1840f689b8dea6d22b030a57"
	h, err := Fixed()
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Digest().String(); got != want {
		t.Errorf("the harness digest is now\n  %s\nand the results were promised\n  %s", got, want)
	}
}

func TestAHarnessThatCannotBeCheckedIsRejected(t *testing.T) {
	for name, spoil := range map[string]func(*Harness){
		"no version":            func(h *Harness) { h.Version = "" },
		"no roster":             func(h *Harness) { h.Roster = "" },
		"one arm":               func(h *Harness) { h.Arms = h.Arms[:1] },
		"an unnamed arm":        func(h *Harness) { h.Arms[1] = "" },
		"the same arm twice":    func(h *Harness) { h.Arms[1] = h.Arms[0] },
		"no tasks":              func(h *Harness) { h.Tasks = nil },
		"an unnamed task":       func(h *Harness) { h.Tasks[0].Benchmark = "" },
		"the same task twice":   func(h *Harness) { h.Tasks[1].Benchmark = h.Tasks[0].Benchmark },
		"an unknown metric":     func(h *Harness) { h.Tasks[0].Metric = "vibes" },
		"an unknown rule":       func(h *Harness) { h.Tasks[0].Extract = "somehow" },
		"negative shots":        func(h *Harness) { h.Tasks[0].Shots = -1 },
		"shots with no seed":    func(h *Harness) { h.Tasks[0].Seed = 0 },
		"no prompt":             func(h *Harness) { h.Tasks[0].Prompt = "   " },
		"nowhere for the shots": func(h *Harness) { h.Tasks[0].Prompt = "{{item}}" },
		"nowhere for the item":  func(h *Harness) { h.Tasks[1].Prompt = "hãy trả lời" },
	} {
		h := small()
		spoil(&h)
		if err := h.check(); err == nil {
			t.Errorf("a harness with %s was accepted", name)
		}
	}
}

func TestTheReasonIsInTheError(t *testing.T) {
	h := small()
	h.Tasks[0].Seed = 0
	err := h.check()
	if err == nil {
		t.Fatal("five shots with no seed was accepted")
	}
	if !strings.Contains(err.Error(), "vmlu") {
		t.Errorf("the error does not name the task: %v", err)
	}
}

func TestAFieldNobodyMeantToWriteIsRejected(t *testing.T) {
	// DisallowUnknownFields, so a typo in the file is an error rather than a
	// setting that silently does nothing.
	_, err := Decode(strings.NewReader(`{"version":"1","roster":"1","armss":["a","b"],"tasks":[]}`))
	if err == nil {
		t.Error("a harness with a misspelled key was accepted")
	}
}

func TestAHarnessClosedAgainstAnotherRosterSaysSo(t *testing.T) {
	h := small()
	h.Roster = "2020-01-01"
	r, err := pick.Rostered()
	if err != nil {
		t.Fatal(err)
	}
	faults := h.Against(r)
	if len(faults) == 0 {
		t.Fatal("a harness closed against a roster from 2020 checked clean against today's")
	}
	if !strings.Contains(faults[0], "2020-01-01") {
		t.Errorf("the fault does not name the roster it was closed against: %q", faults[0])
	}
}

func TestABenchmarkNotOnTheRosterIsAFault(t *testing.T) {
	h := small()
	h.Tasks[0].Benchmark = "khong-co-that"
	r, err := pick.Rostered()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range h.Against(r) {
		if strings.Contains(f, "khong-co-that") {
			found = true
		}
	}
	if !found {
		t.Error("a benchmark nobody has agreed to was not reported")
	}
}

func TestDiacriticErrorRateRunsTheOtherWay(t *testing.T) {
	if !Better(Accuracy, 0.7, 0.6) {
		t.Error("a higher accuracy is not being read as better")
	}
	if Better(DER, 0.7, 0.6) {
		t.Error("a higher diacritic error rate is being read as better, which hands the win to whichever arm is worst at Vietnamese")
	}
	if !Better(DER, 0.02, 0.04) {
		t.Error("a lower diacritic error rate is not being read as better")
	}
}

func TestATaskCanBeLookedUpAndAnArmChecked(t *testing.T) {
	h := small()
	if _, ok := h.Task("vmlu"); !ok {
		t.Error("vmlu is on the harness and was not found")
	}
	if _, ok := h.Task("khong-co"); ok {
		t.Error("a task nobody added was found")
	}
	if !h.Has("a") || h.Has("c") {
		t.Error("the arm check does not agree with the arms")
	}
}
