package giu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specialists is the seven the plan trains, each with the benchmark its gain is
// measured on. They are named here because the item being tested is that every
// one of them is reported by name.
var specialists = [][2]string{
	{"diacritics", "vlsp-diacritics"},
	{"legal-citation", "vi-legal-qa"},
	{"math", "vi-gsm8k"},
	{"code", "vi-humaneval"},
	{"ocr-correction", "ocr-eval-vi"},
	{"dialect", "vi-dialect-nlu"},
	{"summary", "vi-xlsum"},
}

// panel builds a run where every specialist gained twelve points, the
// distillation kept between 91% and 96% of that, and merging the same
// checkpoints kept two thirds.
func panel() Panel {
	kept := []float64{11.0, 11.0, 11.2, 11.5, 10.9, 11.1, 11.3}
	p := Panel{Model: "gao-8b-distilled"}
	for i, s := range specialists {
		p.Specialists = append(p.Specialists, Specialist{
			Name:      s[0],
			Benchmark: s[1],
			Base:      50,
			Own:       62,
			Distilled: 50 + kept[i],
			Merged:    58,
			Runs:      5,
			Spread:    1.2,
			Box:       "gamingpc",
		})
	}
	return p
}

// at returns a copy of the panel with one specialist replaced, which is how
// every test below states the one thing it is about.
func at(p Panel, name string, f func(*Specialist)) Panel {
	out := Panel{Model: p.Model, Specialists: append([]Specialist(nil), p.Specialists...)}
	for i := range out.Specialists {
		if out.Specialists[i].Name == name {
			f(&out.Specialists[i])
			return out
		}
	}
	panic("no specialist named " + name)
}

func blocks(t *testing.T, p Panel, want string) {
	t.Helper()
	for _, f := range p.Blocking() {
		if strings.Contains(f, want) {
			return
		}
	}
	t.Errorf("nothing blocking mentions %q, got:\n  %s", want, strings.Join(p.Blocking(), "\n  "))
}

func TestAPanelThatHoldsSaysWhatItHeldAndWhatItBeat(t *testing.T) {
	p := panel()
	if !p.Settled() {
		t.Fatalf("a clean panel was faulted: %v", p.Blocking())
	}
	if !p.Holds() {
		t.Fatalf("distillation at %.3f against merging at %.3f did not hold P09-2", p.Mean(), p.MergedMean())
	}
	w, _ := p.Worst()
	if w.Name != "ocr-correction" {
		t.Errorf("the worst specialist came back as %s at %.3f", w.Name, w.Retention())
	}
	if p.Hides() {
		t.Errorf("a panel where every line is within five points was called misleading")
	}
	for _, want := range []string{"every specialist kept at least", "ocr-correction", "for averaging the same checkpoints"} {
		if !strings.Contains(p.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, p.Verdict())
		}
	}
}

// The item says individually, and this is why. Six specialists at 93% and one
// at 20% average 82%, which reads as a result while the model is worse at legal
// citation than the base model was before any of this started.
func TestAMeanRetentionIsTheOneNumberThatCannotBeActedOn(t *testing.T) {
	p := at(panel(), "legal-citation", func(s *Specialist) { s.Distilled = s.Base + 0.2*s.Gain() })
	if !p.Settled() {
		t.Fatalf("a panel with one dropped specialist was faulted: %v", p.Blocking())
	}
	if p.Holds() {
		t.Fatal("a distillation that dropped a specialist held P09-2")
	}
	if !p.Hides() {
		t.Errorf("a mean of %.3f over a worst of 0.200 was not called misleading", p.Mean())
	}
	if p.Mean() < 0.75 {
		t.Errorf("the mean came back at %.3f, which is not the case this test is about", p.Mean())
	}
	if first := p.Ranked()[0]; first.Name != "legal-citation" {
		t.Errorf("the ranking leads with %s rather than the line that decides this", first.Name)
	}
	for _, want := range []string{"legal-citation kept 20%", "the panel averages", "a model that works and a model that does not"} {
		if !strings.Contains(p.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, p.Verdict())
		}
	}
}

// Retention is a ratio of two differences and it inherits the evaluation's own
// spread twice, so a small gain measured on a noisy benchmark produces a number
// with a percent sign on it and nothing behind it.
func TestARetentionTheSizeOfTheEvaluationsOwnNoiseIsRefused(t *testing.T) {
	noisy := at(panel(), "dialect", func(s *Specialist) {
		s.Own, s.Distilled, s.Merged, s.Spread = 51.5, 51.4, 51.1, 1.0
	})
	if noisy.Settled() {
		t.Fatal("a gain of 1.5 points against a spread of 1.0 settled a retention")
	}
	blocks(t, noisy, "mostly noise")

	flat := at(panel(), "dialect", func(s *Specialist) { s.Own, s.Distilled, s.Merged = 50.5, 50.4, 50.2 })
	blocks(t, flat, "under the 1.0 a retention needs")

	once := at(panel(), "dialect", func(s *Specialist) { s.Runs = 1 })
	blocks(t, once, "was evaluated 1 time")
}

// A distilled model beating its own teacher is not a triumph. It is either a
// specialist nobody trained to convergence or a benchmark that leaked into the
// distillation set, and both are worth stopping for.
func TestKeepingMoreThanTheTeacherIsNotATriumph(t *testing.T) {
	p := at(panel(), "math", func(s *Specialist) { s.Distilled = s.Own + 1 })
	if p.Settled() {
		t.Fatal("a distilled model above its teacher settled")
	}
	blocks(t, p, "a benchmark that is in the distillation set")
	if p.Holds() {
		t.Error("a panel nobody can read held P09-2")
	}
}

// Distillation keeping 90% is not a result on its own. The cheap thing it has to
// beat is averaging the seven checkpoints, which costs an afternoon.
func TestDistillationIsOnlyAResultNextToWhatMergingKeeps(t *testing.T) {
	p := panel()
	for i := range p.Specialists {
		p.Specialists[i].Merged = p.Specialists[i].Base + 0.88*p.Specialists[i].Gain()
	}
	if !p.Settled() {
		t.Fatalf("a panel with a strong baseline was faulted: %v", p.Blocking())
	}
	if p.Holds() {
		t.Fatalf("merging at %.3f still left P09-2 holding", p.MergedMean())
	}
	if !strings.Contains(p.Verdict(), "an afternoon of weight arithmetic") {
		t.Errorf("the verdict does not say what the pipeline bought: %s", p.Verdict())
	}

	none := at(panel(), "code", func(s *Specialist) { s.Merged = 0 })
	blocks(t, none, "only a result next to what averaging the checkpoints keeps")
}

// A retention is a difference between two scores, so both have to come off the
// same card, and a panel spread across boxes is measuring the boxes.
func TestARetentionIsADifferenceBetweenTwoScoresOffOneCard(t *testing.T) {
	elsewhere := at(panel(), "summary", func(s *Specialist) { s.Box = "server3" })
	if elsewhere.Settled() {
		t.Fatal("a panel scored on two boxes settled")
	}
	blocks(t, elsewhere, "a panel spread across boxes is measuring the boxes")
	blocks(t, elsewhere, "gamingpc and server3")

	nowhere := at(panel(), "summary", func(s *Specialist) { s.Box = "" })
	blocks(t, nowhere, "both have to come off the same card")

	nameless := at(panel(), "summary", func(s *Specialist) { s.Name = "" })
	blocks(t, nameless, "cannot be reported individually")
}

func TestTheSpecialistsNobodyEvaluatedAreTheOnesThatDidNotWork(t *testing.T) {
	p := panel()
	p.Specialists = p.Specialists[:5]
	if p.Settled() {
		t.Fatal("five of seven specialists settled the distillation")
	}
	blocks(t, p, "5 of the 7 specialists were measured")

	twice := panel()
	twice.Specialists = append(twice.Specialists, twice.Specialists[0])
	blocks(t, twice, "two readings of one specialist are not two specialists")

	empty := Panel{Model: "gao-8b-distilled"}
	if empty.Settled() || empty.Holds() {
		t.Error("an unmeasured panel settled P09-2")
	}
	if _, ok := empty.Worst(); ok {
		t.Error("an empty panel has a worst specialist")
	}
	if !strings.Contains(empty.Verdict(), "P09-2 is where it started") {
		t.Errorf("the verdict on nothing reads %q", empty.Verdict())
	}
}

func TestAPanelIsReadFromWhatAnEvaluationAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "retention.jsonl")
	body := `{"name":"diacritics","benchmark":"vlsp-diacritics","base":50,"own":62,"distilled":61,"merged":58,"runs":5,"spread":1.2,"box":"gamingpc"}

{"name":"math","benchmark":"vi-gsm8k","base":50,"own":62,"distilled":61.5,"merged":58,"runs":5,"spread":1.2,"box":"gamingpc"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPanel("gao-8b-distilled", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Specialists) != 2 {
		t.Fatalf("read %d specialists", len(p.Specialists))
	}
	if w, _ := p.Worst(); w.Name != "diacritics" {
		t.Errorf("the worst came back as %s", w.Name)
	}

	// A column nobody declared is the evaluation and the reader disagreeing about
	// what was written, which is worth failing over rather than ignoring.
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"name":"math","base":50,"own":62,"retention":0.9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPanel("gao-8b-distilled", bad); err == nil {
		t.Error("a line with a column nobody declared was read")
	}

	blank := filepath.Join(dir, "blank.jsonl")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPanel("gao-8b-distilled", blank); err == nil {
		t.Error("an empty file was read as a panel")
	}
	if _, err := ReadPanel("gao-8b-distilled", filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a panel that is not there was read")
	}
}
