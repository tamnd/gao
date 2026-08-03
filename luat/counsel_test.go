package luat

import (
	"strings"
	"testing"
)

// The point of the agenda is that no question blocks the work, so the property
// worth asserting is not that the questions are good, it is that every one of
// them has a position gao can act on today.
func TestEveryQuestionHasSomethingToActOn(t *testing.T) {
	for _, q := range Questions() {
		if q.ID == "" {
			t.Fatalf("a question has no id: %q", q.Ask)
		}
		if !strings.HasSuffix(q.Ask, "?") {
			t.Errorf("%s does not read as a question: %q", q.ID, q.Ask)
		}
		if q.Default == "" {
			t.Errorf("%s has no default, so an unanswered %s stops the work", q.ID, q.ID)
		}
		if q.Position() == "" {
			t.Errorf("%s has no position", q.ID)
		}
		if q.Stakes == "" {
			t.Errorf("%s does not say what its answer changes", q.ID)
		}
	}
}

// Filed is the field that separates an agenda from a wish. It is asserted per
// question rather than in bulk so that a failure names the one that is only
// written down.
func TestEveryQuestionWasActuallyFiled(t *testing.T) {
	if FiledOn == "" {
		t.Fatal("the agenda has no filing date")
	}
	for _, q := range Questions() {
		if !q.Filed {
			t.Errorf("%s was written down and not filed", q.ID)
		}
	}
}

// Q5 is the one with the earliest deadline, so it gets its own test rather than
// being one row of a loop. The assessment takes longer than the hardware order,
// which is why it has to be filed before procurement rather than before training.
func TestQ5IsFiled(t *testing.T) {
	q, ok := Ask("Q5")
	if !ok {
		t.Fatal("Q5 is not on the agenda")
	}
	if !q.Filed {
		t.Error("Q5 is not filed, and it is the question with the earliest deadline")
	}
	if !strings.Contains(q.Position(), "procurement") {
		t.Errorf("the Q5 position does not name the deadline it is against: %q", q.Position())
	}
}

// The gate on S0 names Q1, Q2, and Q3 specifically: each is either answered or
// running under a written default. Both halves of that are acceptable and the
// third state, an open question with nothing written down, is not.
func TestTheGateQuestionsAreAnsweredOrRunningUnderADefault(t *testing.T) {
	for _, id := range []string{"Q1", "Q2", "Q3"} {
		q, ok := Ask(id)
		if !ok {
			t.Errorf("%s is not on the agenda", id)
			continue
		}
		if !q.Answered() && q.Default == "" {
			t.Errorf("%s is open with nothing written down to proceed under", id)
		}
	}
}

func TestPositionPrefersCounselOverTheDefault(t *testing.T) {
	q := Question{ID: "QX", Default: "assume the safe reading"}
	if got := q.Position(); got != "assume the safe reading" {
		t.Errorf("an unanswered question acts on %q", got)
	}
	if q.Answered() {
		t.Error("a question with no answer reads as answered")
	}

	q.Answer = "the allowance covers training"
	if got := q.Position(); got != "the allowance covers training" {
		t.Errorf("an answered question still acts on %q", got)
	}
	if !q.Answered() {
		t.Error("a question with an answer reads as unanswered")
	}
}

// Outstanding is what a status report prints, so it has to agree with the
// per-question view rather than being a second implementation of the same rule.
func TestOutstandingIsExactlyTheUnansweredQuestions(t *testing.T) {
	open := make(map[string]bool)
	for _, q := range Outstanding() {
		if q.Answered() {
			t.Errorf("%s is answered and reads as outstanding", q.ID)
		}
		open[q.ID] = true
	}
	for _, q := range Questions() {
		if !q.Answered() && !open[q.ID] {
			t.Errorf("%s is unanswered and does not read as outstanding", q.ID)
		}
	}
}

func TestTheIDsAreUniqueAndAskFindsThem(t *testing.T) {
	seen := make(map[string]bool)
	for _, q := range Questions() {
		if seen[q.ID] {
			t.Errorf("%s appears twice", q.ID)
		}
		seen[q.ID] = true

		got, ok := Ask(q.ID)
		if !ok {
			t.Errorf("Ask(%q) found nothing", q.ID)
			continue
		}
		if got.Ask != q.Ask {
			t.Errorf("Ask(%q) returned a different question", q.ID)
		}
	}
	if _, ok := Ask("Q99"); ok {
		t.Error("Ask found a question that does not exist")
	}
}

// The exported slice is a copy, because the agenda is a fact about the project
// and a caller printing it should not be able to edit it.
func TestQuestionsHandsOutACopy(t *testing.T) {
	got := Questions()
	got[0].Default = "do whatever"
	if Questions()[0].Default == "do whatever" {
		t.Error("editing the returned slice edited the agenda")
	}
}
