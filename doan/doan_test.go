package doan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// result is a measurement that came back the way one should: the claim it was
// scored against, what the number was, what produced it and where it ran.
func result(r Register, id, state, reading string) Result {
	var claim string
	for _, p := range r.Predictions {
		if p.ID == id {
			claim = p.Claim
		}
	}
	return Result{ID: id, Claim: claim, State: state, Reading: reading, By: "gao count counts", Box: "server1"}
}

// resolve marks the first n predictions of the register, alternating so that the
// rate comes out at hits over n.
func resolve(r Register, hits, misses int) Register {
	results := make([]Result, 0, hits+misses)
	for i := 0; i < hits+misses; i++ {
		state := Landed
		if i >= hits {
			state = Missed
		}
		results = append(results, result(r, r.Predictions[i].ID, state, "197B tokens"))
	}
	out, why := r.Apply(results)
	if len(why) > 0 {
		panic(strings.Join(why, "; "))
	}
	return out
}

func refuses(t *testing.T, r Register, want string) {
	t.Helper()
	why := r.Blocking()
	if len(why) == 0 {
		t.Fatalf("the register was published and it should have been refused for %q", want)
	}
	for _, w := range why {
		if strings.Contains(w, want) {
			return
		}
	}
	t.Errorf("no refusal mentions %q, and what came back was:\n  %s", want, strings.Join(why, "\n  "))
}

func TestTheRegisterIsPublishedBeforeAnythingHasAResult(t *testing.T) {
	r := Published()
	if why := r.Blocking(); len(why) > 0 {
		t.Fatalf("the published register does not publish: %v", why)
	}
	if len(r.Predictions) != Declared {
		t.Fatalf("the register holds %d predictions and %d were declared", len(r.Predictions), Declared)
	}
	if len(r.Waiting()) != Declared {
		t.Errorf("%d of %d predictions already carry a result", Declared-len(r.Waiting()), Declared)
	}
	if !r.Holds() {
		t.Error("a register nobody has measured reads as failing, which says the work is going badly rather than that it has not run")
	}
	if v := r.Verdict(); !strings.Contains(v, "None of them has a result") {
		t.Errorf("the verdict does not say the register is unmeasured:\n  %s", v)
	}
}

// The digest is the whole enforcement. It is pinned here so that editing a claim
// is a diff on a pull request with a reviewer on it rather than an afternoon's
// work after the numbers came in.
func TestTheClaimsArePinnedByTheirDigest(t *testing.T) {
	const pinned = "ee4b35363bf4"
	got := Published().Digest().String()[:12]
	if got != pinned {
		t.Errorf("the register digest is %s and %s was published.\n"+
			"A claim or the roster changed. That is allowed, and it is allowed here, in a diff, with the old digest in the history.", got, pinned)
	}
}

func TestAResultAgainstAnEditedClaimIsRefusedRatherThanApplied(t *testing.T) {
	r := Published()
	stale := result(r, "P03-1", Landed, "180B tokens")
	stale.Claim = "the exact HPLT v3 vie_Latn token count lands within 40% of the 176B estimate"

	out, why := r.Apply([]Result{stale})
	if len(why) != 1 || !strings.Contains(why[0], "a claim the register does not hold") {
		t.Fatalf("the stale result was taken, and what came back was %v", why)
	}
	for _, p := range out.Predictions {
		if p.ID == "P03-1" && p.State != Open {
			t.Errorf("P03-1 is %s after a refused result", p.State)
		}
	}
}

func TestAResultHasToNameWhatMeasuredIt(t *testing.T) {
	r := Published()
	bare := result(r, "P03-1", Landed, "180B tokens")
	bare.By = ""
	out, why := r.Apply([]Result{bare})
	if len(why) > 0 {
		t.Fatalf("Apply refused a result it should have taken: %v", why)
	}
	refuses(t, out, "does not name what measured it")

	silent := result(r, "P03-1", Missed, "")
	out, _ = r.Apply([]Result{silent})
	refuses(t, out, "does not say what came back")
}

func TestAWithdrawalWithNoReasonIsADeletion(t *testing.T) {
	r := Published()
	pulled := result(r, "P04-6", Withdrawn, "")
	out, why := r.Apply([]Result{pulled})
	if len(why) > 0 {
		t.Fatalf("Apply refused a withdrawal it should have taken: %v", why)
	}
	refuses(t, out, "a withdrawal with no reason is a deletion")

	pulled.Why = "the extraction stage was cut to born digital, so the GPU hours it predicts are never spent"
	out, _ = r.Apply([]Result{pulled})
	if why := out.Blocking(); len(why) > 0 {
		t.Fatalf("a withdrawal carrying its reason was refused: %v", why)
	}
	if len(out.Pulled()) != 1 {
		t.Errorf("%d predictions came back withdrawn", len(out.Pulled()))
	}
}

func TestWithdrawingTheOnesThatWereGoingToMissIsCapped(t *testing.T) {
	r := Published()
	results := make([]Result, 0, 8)
	for i := range 8 {
		res := result(r, r.Predictions[i].ID, Withdrawn, "")
		res.Why = "the run it needed was cut"
		results = append(results, res)
	}
	out, _ := r.Apply(results)
	if out.Holds() {
		t.Fatalf("%d of %d withdrawn still holds", len(out.Pulled()), len(out.Predictions))
	}
	if v := out.Verdict(); !strings.Contains(v, "which predictions were pulled rather than which ones held") {
		t.Errorf("the verdict does not say what the withdrawals cost:\n  %s", v)
	}
}

func TestTheRateIsNotQuotedUntilHalfTheRegisterHasResolved(t *testing.T) {
	r := resolve(Published(), 4, 6)
	if r.Settled() {
		t.Fatal("ten of fifty eight resolved reads as settled")
	}
	if !r.Holds() {
		t.Error("a register that is 40% right on a tenth of its rows reads as failing")
	}
	if v := r.Verdict(); !strings.Contains(v, "which measurements were cheap to make") {
		t.Errorf("the verdict quotes a rate it should not:\n  %s", v)
	}
}

func TestARegisterThatComesBackMostlyWrongWasWrittenFromHope(t *testing.T) {
	r := resolve(Published(), 12, 28)
	if !r.Settled() {
		t.Fatal("forty of fifty eight resolved does not read as settled")
	}
	if r.Holds() {
		t.Fatalf("a register at %.0f%% holds", r.Rate()*100)
	}
	if v := r.Verdict(); !strings.Contains(v, "written from hope rather than from evidence") {
		t.Errorf("the verdict does not say what a low rate means:\n  %s", v)
	}
}

func TestARegisterThatComesBackEntirelyRightWasWrittenToBeMet(t *testing.T) {
	r := resolve(Published(), 40, 0)
	if r.Holds() {
		t.Fatal("a register that got everything right holds, and it should not")
	}
	if v := r.Verdict(); !strings.Contains(v, "written to be met rather than to be tested") {
		t.Errorf("the verdict does not say what a perfect rate means:\n  %s", v)
	}

	// Two thirds is the honest target, and it is the case the band exists for.
	honest := resolve(Published(), 26, 14)
	if !honest.Holds() {
		t.Fatalf("a register at %.0f%% does not hold", honest.Rate()*100)
	}
	if v := honest.Verdict(); !strings.Contains(v, "the misses are the part of it worth reading") {
		t.Errorf("the verdict does not lead with the misses:\n  %s", v)
	}
}

func TestEveryGateStandsOnAPredictionThatIsOnTheRegister(t *testing.T) {
	gated := Gated()
	if len(gated) == 0 {
		t.Fatal("no slice gate names a prediction, and four of them do")
	}
	on := make(map[string]bool, Declared)
	for _, p := range Published().Predictions {
		on[p.ID] = true
	}
	for id, named := range gated {
		for _, want := range named {
			if !on[want] {
				t.Errorf("the %s gate stands on %s, which is not on the register", id, want)
			}
		}
	}

	short := Register{Name: "short", Predictions: Published().Predictions[1:]}
	refuses(t, short, "the S1 gate stands on P03-1, which is not on the register")
}

func TestARegisterThatChangesSizeIsNotARegister(t *testing.T) {
	var empty Register
	refuses(t, empty, "cannot be wrong about anything")
	if empty.Verdict() != empty.Blocking()[0] {
		t.Error("the verdict does not lead with the reason there is nothing to report")
	}

	r := Published()
	refuses(t, Register{Name: "cut", Predictions: r.Predictions[:57]}, "a register that changes size is not a register")

	twice := Register{Name: "twice", Predictions: append(append([]Prediction{}, r.Predictions...), r.Predictions[0])}
	refuses(t, twice, "is on the register twice")

	same := append([]Prediction{}, r.Predictions...)
	same[7].Claim = same[6].Claim
	refuses(t, Register{Name: "same", Predictions: same}, "neither of them can miss on its own")
}

func TestAPredictionFiledUnderNothingIsNotFiled(t *testing.T) {
	r := Published()
	loose := append([]Prediction{}, r.Predictions...)
	loose[0].Slice = "S12"
	refuses(t, Register{Name: "loose", Predictions: loose}, "which is not a slice of the build plan")

	misnumbered := append([]Prediction{}, r.Predictions...)
	misnumbered[0].ID = "prediction one"
	refuses(t, Register{Name: "misnumbered", Predictions: misnumbered}, "is not shaped like a prediction identifier")

	blank := append([]Prediction{}, r.Predictions...)
	blank[0].Claim = ""
	refuses(t, Register{Name: "blank", Predictions: blank}, "nothing about it could come back wrong")

	wrapped := append([]Prediction{}, r.Predictions...)
	wrapped[0].Claim = "the exact HPLT v3 count lands\nwithin 15% of the estimate"
	refuses(t, Register{Name: "wrapped", Predictions: wrapped}, "broken across lines")
}

func TestTheSliceRowsCoverTheWholeBuildPlanIncludingTheSliceWithNothingToBeWrongAbout(t *testing.T) {
	r := resolve(Published(), 6, 4)
	rows := r.Slices()
	if len(rows) != len(doc.Slices) {
		t.Fatalf("%d rows came off %d slices", len(rows), len(doc.Slices))
	}
	var total, resolved int
	for _, row := range rows {
		total += row.Count
		resolved += row.Resolved()
		if row.Slice == "S0" && row.Count != 0 {
			t.Errorf("S0 carries %d predictions and its gate is a set of questions for counsel", row.Count)
		}
	}
	if total != Declared {
		t.Errorf("the slice rows hold %d of %d predictions", total, Declared)
	}
	if resolved != r.Resolved() {
		t.Errorf("the rows resolve %d and the register resolves %d", resolved, r.Resolved())
	}
}

func TestReadingResultsOffDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.jsonl")
	r := Published()
	lines := []string{
		fmt.Sprintf(`{"id":"P03-1","claim":%q,"state":"dung","reading":"181.4B tokens","by":"gao count counts","box":"server1"}`, r.Predictions[0].Claim),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadResults(path)
	if err != nil {
		t.Fatal(err)
	}
	out, why := r.Apply(got)
	if len(why) > 0 {
		t.Fatalf("a good result was refused: %v", why)
	}
	if len(out.Hits()) != 1 || out.Hits()[0].Reading != "181.4B tokens" {
		t.Errorf("the reading did not land: %+v", out.Hits())
	}

	unknown := filepath.Join(dir, "unknown.jsonl")
	if err := os.WriteFile(unknown, []byte(`{"id":"P99-9","claim":"x","state":"dung","reading":"y","by":"z","box":"server1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = ReadResults(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if _, why := r.Apply(got); len(why) != 1 || !strings.Contains(why[0], "is not on the register") {
		t.Errorf("a result for a prediction nobody made was taken: %v", why)
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"id":"P03-1","verdict":"good"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadResults(bad); err == nil {
		t.Error("a result carrying an undeclared field was read")
	}
	if _, err := ReadResults(filepath.Join(dir, "nothing.jsonl")); err == nil {
		t.Error("a file that is not there read as results")
	}
}

func TestTwoResultsForOnePredictionAreRefusedRatherThanOrdered(t *testing.T) {
	r := Published()
	first := result(r, "P05-5", Landed, "4.1% of documents changed")
	second := result(r, "P05-5", Missed, "2.2% of documents changed")
	out, why := r.Apply([]Result{first, second})
	if len(why) != 1 || !strings.Contains(why[0], "which of them is the later one") {
		t.Fatalf("two results for one prediction went through: %v", why)
	}
	if len(out.Hits()) != 1 {
		t.Errorf("%d predictions held after a refused second result", len(out.Hits()))
	}

	wrong := result(r, "P05-5", "ok", "4.1% of documents changed")
	if _, why := r.Apply([]Result{wrong}); len(why) != 1 || !strings.Contains(why[0], "not one a result can be in") {
		t.Errorf("a result in an invented state went through: %v", why)
	}
}

// The one result this project has actually measured, checked in as the file it
// arrived as. It is here rather than written inline because a result is a file
// by design, and because the README quotes what this produces: a fixture whose
// input is not in the repo is a fixture nobody can check.
func TestTheMeasuredResultLandsOnTheRegister(t *testing.T) {
	got, err := ReadResults(filepath.Join("testdata", "results.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	out, why := Published().Apply(got)
	if len(why) > 0 {
		t.Fatalf("the measured result was refused: %v", why)
	}
	if len(out.Misses()) != 1 {
		t.Fatalf("%d predictions came back wrong, the file holds one", len(out.Misses()))
	}
	m := out.Misses()[0]
	if m.ID != "P07-5" {
		t.Errorf("the result landed on %s rather than on P07-5", m.ID)
	}
	// 3.28 is what doc.CharsPerToken now says, and P07-5 predicted 3.0 give or
	// take 0.15. The two have to keep disagreeing or one of them was edited.
	if !strings.Contains(m.Reading, "3.28 characters per token") {
		t.Errorf("the reading is %q, the measurement was 3.28 characters per token", m.Reading)
	}
	if m.Box != "server3" {
		t.Errorf("the result came off %s, it was measured on server3", m.Box)
	}
}
