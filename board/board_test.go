package board

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/pick"
)

func roster(t *testing.T) pick.Roster {
	t.Helper()
	ros, err := pick.Rostered()
	if err != nil {
		t.Fatal(err)
	}
	return ros
}

// pinned is the roster as it will read once the nine entries that are waiting on
// an address or on a split have one, so the tests below can describe a board
// rather than the reason there is not one yet. What today's roster does instead
// is its own test.
func pinned(t *testing.T) pick.Roster {
	t.Helper()
	ros := roster(t)
	out := make([]pick.Entry, len(ros.Benchmarks))
	copy(out, ros.Benchmarks)
	for i := range out {
		if out[i].Version == pick.Unpinned {
			out[i].Version = "0000000000000000000000000000000000000000"
			out[i].Pending = ""
		}
	}
	ros.Benchmarks = out
	return ros
}

// full scores every benchmark on the roster, with the given margin over the
// baseline for the native ones and the given margin for the translated ones,
// so a test can say what shape of run it is describing in two numbers.
func full(t *testing.T, nativeMargin, translatedMargin float64) []Score {
	t.Helper()
	ros := roster(t)
	out := make([]Score, 0, len(ros.Benchmarks))
	for _, e := range ros.Benchmarks {
		m := nativeMargin
		if e.Origin == pick.Translated {
			m = translatedMargin
		}
		out = append(out, Score{
			Benchmark: e.Name,
			Score:     50 + m,
			Baseline:  50,
			Against:   "sailor2-8b-chat",
			Runs:      3,
			Spread:    0.4,
			Box:       "gamingpc",
			On:        "2026-08-12",
		})
	}
	return out
}

func TestABoardIsTwoArmsAndNeverOneAverage(t *testing.T) {
	b := Read(pinned(t), full(t, 4.0, 4.0))

	if why := b.Blocking(); len(why) > 0 {
		t.Fatalf("a board with every benchmark scored is refused: %q", why)
	}
	native, ok := b.Arm(pick.Native)
	if !ok {
		t.Fatal("a roster with sixteen Vietnamese benchmarks on it produced no native arm")
	}
	translated, ok := b.Arm(pick.Translated)
	if !ok {
		t.Fatal("a roster with six translated benchmarks on it produced no translated arm")
	}
	if native.Origin != pick.Native || translated.Origin != pick.Translated {
		t.Errorf("the arms came out as %s and %s", native.Origin, translated.Origin)
	}
	if len(native.Scores)+len(translated.Scores) == len(b.scores) {
		t.Error("the two arms hold every score, so the code benchmarks went into one of them")
	}

	// The verdict has to quote both, and it has to quote the native one first,
	// because that is the arm the claim is about.
	v := b.Verdict()
	first := strings.Index(v, "written in Vietnamese")
	second := strings.Index(v, "translated into it")
	switch {
	case first < 0 || second < 0:
		t.Fatalf("the verdict does not name both arms: %q", v)
	case second < first:
		t.Errorf("the verdict puts the translated arm first: %q", v)
	}
}

func TestAModelThatReadsTranslatedEnglishIsCaught(t *testing.T) {
	// Half a point ahead on Vietnamese written by Vietnamese speakers and five
	// points ahead on English translated into it. A single average across the
	// board reports this as a good release.
	b := Read(pinned(t), full(t, 0.5, 5.0))

	if edge := b.Edge(); edge <= MaxTranslationEdge {
		t.Fatalf("the gap between the arms is %.1f points and the rule allows %.1f", edge, MaxTranslationEdge)
	}
	faults := b.Faults()
	if len(faults) == 0 {
		t.Fatal("a model four and a half points better at translated English than at Vietnamese passes")
	}
	if !strings.Contains(faults[0], "reads translated English rather than one that writes Vietnamese") {
		t.Errorf("the fault is %q", faults[0])
	}
	if b.Holds() {
		t.Error("a board with a fault holds")
	}
	if !strings.Contains(b.Verdict(), "cannot be published as it stands") {
		t.Errorf("the verdict is %q", b.Verdict())
	}
}

func TestABoardCarriedByItsOwnAuthorsBenchmarksSaysSo(t *testing.T) {
	scores := full(t, 1.0, 1.0)
	for i, s := range scores {
		if strings.HasPrefix(s.Benchmark, "vi-") {
			scores[i].Score = 50 + 9.0
		}
	}
	b := Read(pinned(t), scores)

	var said bool
	for _, f := range b.Faults() {
		if strings.Contains(f, "instruments its own authors designed") {
			said = true
		}
	}
	if !said {
		t.Fatalf("gao's own benchmarks put the model %.1f ahead and everybody else's %.1f, and the board does not say so: %q",
			b.Own, b.Others, b.Faults())
	}
	// The two halves are disjoint and both non empty, or the comparison is not
	// a comparison.
	if b.Own <= b.Others {
		t.Errorf("the own margin is %.1f and the others margin is %.1f", b.Own, b.Others)
	}
}

func TestAMarginInsideItsOwnNoiseIsNotAWin(t *testing.T) {
	scores := full(t, 4.0, 4.0)
	// vmlu is the anchor benchmark. Three tenths ahead with the same three runs
	// spread over four tenths is a coin flip that landed.
	for i, s := range scores {
		if s.Benchmark == "vmlu" {
			scores[i].Score = 50.3
			scores[i].Spread = 0.4
		}
	}
	b := Read(pinned(t), scores)

	if len(b.Coin) != 1 {
		t.Fatalf("the board reports %d rows inside their own noise: %q", len(b.Coin), b.Coin)
	}
	if !strings.HasPrefix(b.Coin[0], "vmlu is") {
		t.Errorf("the row named is %q", b.Coin[0])
	}
	var said bool
	for _, f := range b.Faults() {
		if strings.Contains(f, "inside its own noise") {
			said = true
		}
	}
	if !said {
		t.Errorf("a coin flip counted as a win: %q", b.Faults())
	}
	// A row inside the noise is still counted in the arm's mean, because leaving
	// it out would be choosing which rows count after seeing them. What changes
	// is that it is not counted as decided.
	native, _ := b.Arm(pick.Native)
	if native.Decided != len(native.Scores)-1 {
		t.Errorf("%d of %d native rows are decided", native.Decided, len(native.Scores))
	}
}

func TestABoardMissingHalfTheSuiteIsNotABoard(t *testing.T) {
	scores := full(t, 4.0, 4.0)
	b := Read(pinned(t), scores[:4])

	why := b.Blocking()
	if len(why) == 0 {
		t.Fatal("a board with four of twenty four benchmarks on it reads as a result")
	}
	if !strings.Contains(strings.Join(why, " "), "chosen after the results") {
		t.Errorf("the refusal is %q", why)
	}
	if len(b.Missing) != len(scores)-4 {
		t.Errorf("the board reports %d missing benchmarks out of %d unscored", len(b.Missing), len(scores)-4)
	}
	if b.Faults() != nil {
		t.Error("a board that is not a board still reports faults under the refusal")
	}
	if !strings.HasPrefix(b.Verdict(), "This is not a scoreboard") {
		t.Errorf("the verdict is %q", b.Verdict())
	}
}

func TestAScoreThatCannotBePlacedIsRefused(t *testing.T) {
	cases := []struct {
		name string
		fix  func([]Score) []Score
		says string
	}{
		{"a benchmark nobody rostered", func(s []Score) []Score {
			return append(s, Score{Benchmark: "vi-vibes", Score: 90, Baseline: 50, Against: "x", Runs: 1})
		}, "not on the roster"},
		{"one benchmark scored twice", func(s []Score) []Score {
			return append(s, s[0])
		}, "scored twice"},
		{"a score off the scale", func(s []Score) []Score {
			s[0].Score = 580
			return s
		}, "points out of a hundred"},
		{"a run that never ran", func(s []Score) []Score {
			s[0].Runs = 0
			return s
		}, "was run 0 times"},
		{"a baseline off no model", func(s []Score) []Score {
			s[0].Against = ""
			return s
		}, "margin over nothing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := Read(pinned(t), c.fix(full(t, 4.0, 4.0)))
			why := strings.Join(b.Blocking(), " ")
			if !strings.Contains(why, c.says) {
				t.Errorf("the refusal is %q, it does not say %q", why, c.says)
			}
			if b.Holds() {
				t.Error("a board that is not a board holds")
			}
		})
	}
}

func TestTodaysRosterCannotProduceABoardThatHolds(t *testing.T) {
	// This is the state of the world rather than a hypothetical. Nine of the
	// twenty four entries are waiting on an address or on a split, and a number
	// taken on one of those is a number nobody can take again.
	ros := roster(t)
	b := Read(ros, full(t, 4.0, 4.0))

	var unpinned int
	for _, e := range ros.Benchmarks {
		if e.Version == pick.Unpinned {
			unpinned++
		}
	}
	if unpinned == 0 {
		t.Skip("every entry on the roster is pinned now, so there is nothing left to carry")
	}
	if why := b.Blocking(); len(why) > 0 {
		t.Fatalf("an unpinned revision stopped the board from being a board at all: %q", why)
	}
	if len(b.Unpinned) != unpinned {
		t.Errorf("the roster has %d unpinned entries and the board carries %d", unpinned, len(b.Unpinned))
	}
	if b.Holds() {
		t.Fatal("a board with rows nobody can run again holds")
	}
	if !strings.Contains(strings.Join(b.Faults(), " "), "no pinned revision") {
		t.Errorf("the faults do not mention the unpinned rows: %q", b.Faults())
	}
}

func TestTheArmsAreDisjointAndCoverTheRoster(t *testing.T) {
	b := Read(pinned(t), full(t, 4.0, 4.0))

	seen := map[string]bool{}
	var n int
	for _, arm := range b.Arms {
		for _, s := range arm.Scores {
			if seen[s.Benchmark] {
				t.Errorf("%s is in two arms", s.Benchmark)
			}
			seen[s.Benchmark] = true
			n++
		}
	}
	if n != len(roster(t).Benchmarks) {
		t.Errorf("the arms hold %d scores and the roster has %d benchmarks", n, len(roster(t).Benchmarks))
	}
	// The arms come out in the order a board prints them.
	order := make([]string, 0, len(b.Arms))
	for _, arm := range b.Arms {
		order = append(order, arm.Origin)
	}
	if strings.Join(order, ",") != strings.Join(Origins(), ",") {
		t.Errorf("the arms came out as %v, the board prints %v", order, Origins())
	}
}

func TestAMarginIsTheModelAgainstTheBaselineAndNothingElse(t *testing.T) {
	s := Score{Score: 58.2, Baseline: 54.1, Spread: 0.4}
	if got := s.Margin(); got < 4.09 || got > 4.11 {
		t.Errorf("58.2 against 54.1 is a margin of %.2f", got)
	}
	if !s.Decided() {
		t.Error("four points ahead over a spread of four tenths reads as a coin flip")
	}
	if (Score{Score: 50.3, Baseline: 50, Spread: 0.4}).Decided() {
		t.Error("three tenths ahead over a spread of four tenths reads as decided")
	}
	// A loss inside the noise is a coin flip too, and it is the direction people
	// forget to check.
	if (Score{Score: 49.7, Baseline: 50, Spread: 0.4}).Decided() {
		t.Error("three tenths behind over a spread of four tenths reads as decided")
	}
}
