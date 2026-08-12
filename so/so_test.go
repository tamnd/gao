package so_test

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/tamnd/gao/so"
)

const (
	native     = "com-8b-sft-native"
	translated = "com-8b-sft-translated"
)

// build lays out a protocol that does everything right, and then lets a test
// break one thing about it. Every pair is shown in both orders to different
// raters, so a fifth of the items are read twice and the order comes out at a
// half without anybody arranging it.
type build struct {
	items int

	// pick decides what the rater on the left hand system chose, in terms of
	// systems rather than positions, so a test can set a preference without
	// having to think about which side anything was on.
	pick func(item, reader int) string

	// grow is how many syllables longer the named system's answer is on an item.
	grow func(item int, system string) int

	// raters is how many people the work is spread over.
	raters int
}

func (b build) pairs() []so.Pair {
	if b.raters == 0 {
		b.raters = 8
	}
	if b.pick == nil {
		b.pick = func(item, _ int) string {
			// A clear win, at about two thirds.
			if item%3 == 0 {
				return translated
			}
			return native
		}
	}
	if b.grow == nil {
		b.grow = func(int, string) int { return 0 }
	}

	out := make([]so.Pair, 0, b.items+b.items/5)
	for i := range b.items {
		// Every fifth item is read twice, and the second reading is shown in
		// the opposite order.
		reads := 1
		if i%5 == 0 {
			reads = 2
		}
		for k := range reads {
			left, right := native, translated
			// Half the items put the translated system on the left, and a
			// second reading of an item flips whatever the first one did.
			if (i%2 == 0) != (k == 1) {
				left, right = translated, native
			}

			choice := so.Tie
			switch b.pick(i, k) {
			case left:
				choice = so.Left
			case right:
				choice = so.Right
			}

			out = append(out, so.Pair{
				Item:           fmt.Sprintf("prompt-%04d", i),
				Rater:          fmt.Sprintf("r%02d", (i*3+k*5)%b.raters),
				Left:           left,
				Right:          right,
				LeftSyllables:  120 + b.grow(i, left),
				RightSyllables: 120 + b.grow(i, right),
				Choice:         choice,
			})
		}
	}
	return out
}

func read(b build) so.Reading { return so.Read(b.pairs()) }

func says(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, want) {
			return
		}
	}
	t.Errorf("nothing said %q, and what came back was:\n  %s", want, strings.Join(lines, "\n  "))
}

func silent(t *testing.T, lines []string, unwanted string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, unwanted) {
			t.Errorf("something said %q and should not have:\n  %s", unwanted, l)
		}
	}
}

func TestAProtocolThatWasRunProperlyReadsAsAResult(t *testing.T) {
	r := read(build{items: 400})

	if why := r.Blocking(); len(why) > 0 {
		t.Fatalf("an ordinary protocol was refused:\n  %s", strings.Join(why, "\n  "))
	}
	if faults := r.Faults(); len(faults) > 0 {
		t.Fatalf("an ordinary protocol carries faults:\n  %s", strings.Join(faults, "\n  "))
	}
	if !r.Holds() || !r.Separates() {
		t.Errorf("a two thirds win over 480 judgements does not separate: %.1f%% from %.1f%% to %.1f%%", r.Rate*100, r.Low*100, r.High*100)
	}
	if r.A != native || r.B != translated {
		t.Errorf("the systems came back as %s against %s", r.A, r.B)
	}
	if r.Items != 400 {
		t.Errorf("%d items, want 400", r.Items)
	}
}

// A rater shown two answers side by side picks the left one more often than the
// right one whether or not it is better, and the effect is large enough to carry
// a protocol on its own.
func TestPickingTheLeftHandAnswerIsCaughtBeforeTheWinRateIsBelieved(t *testing.T) {
	// A rater who reads nothing and always takes the left hand answer. The two
	// systems are genuinely tied and the left hand column wins every pair,
	// which is a preference the build cannot express through a system name.
	pairs := build{items: 400}.pairs()
	for i := range pairs {
		pairs[i].Choice = so.Left
	}

	r := so.Read(pairs)

	if r.First < 0.99 {
		t.Fatalf("every pick went left and the reading says %.1f%%", r.First*100)
	}
	says(t, r.Faults(), "of the picks went to whichever answer was shown first")
	says(t, r.Faults(), "the layout rather than the answers")
	if r.Holds() {
		t.Error("a protocol that measured its own layout holds")
	}
}

// Showing each pair in both orders only helps if it happened, and a harness that
// was supposed to alternate and did not is invisible in the finished file.
func TestAHarnessThatDidNotAlternateTheOrderIsNamed(t *testing.T) {
	pairs := build{items: 400}.pairs()
	for i := range pairs {
		if pairs[i].Left != native {
			pairs[i].Left, pairs[i].Right = pairs[i].Right, pairs[i].Left
			switch pairs[i].Choice {
			case so.Left:
				pairs[i].Choice = so.Right
			case so.Right:
				pairs[i].Choice = so.Left
			}
			pairs[i].LeftSyllables, pairs[i].RightSyllables = pairs[i].RightSyllables, pairs[i].LeftSyllables
		}
	}

	r := so.Read(pairs)
	if r.Order < 0.99 {
		t.Fatalf("one system was on the left every time and the reading says %.1f%%", r.Order*100)
	}
	says(t, r.Faults(), "rather than about half, so the harness did not alternate the order")
}

// The strongest confound in preference evaluation, and it does not need a
// careless rater, since a longer answer really does look more thorough in the
// two minutes somebody spends on it.
func TestAnEvaluationOfLengthIsNamedAsOne(t *testing.T) {
	r := read(build{
		items: 400,
		grow: func(_ int, system string) int {
			if system == native {
				return 80
			}
			return 0
		},
		pick: func(item, _ int) string {
			// The longer system wins nearly every pair.
			if item%20 == 0 {
				return translated
			}
			return native
		},
	})

	if r.Compared != r.Decided {
		t.Fatalf("%d of %d decided pairs differed in length, want all of them", r.Compared, r.Decided)
	}
	says(t, r.Faults(), "the longer answer won 91.7% of the")
	says(t, r.Faults(), "reads as an evaluation of length")
}

// A win rate of 54% over 200 pairs is a tie, and reporting it as a win is how a
// project ends up with a headline the next run cannot reproduce.
func TestAWinRateThatDoesNotClearItsOwnIntervalIsNotAResult(t *testing.T) {
	r := read(build{
		items: 400,
		pick: func(item, _ int) string {
			if item%25 == 0 {
				return native
			}
			if item%2 == 0 {
				return translated
			}
			return native
		},
	})

	if r.Separates() {
		t.Fatalf("a near tie separated: %.1f%% from %.1f%% to %.1f%%", r.Rate*100, r.Low*100, r.High*100)
	}
	says(t, r.Faults(), "covers a half, so this evaluation does not say either system won")
	if r.Holds() {
		t.Error("an evaluation that does not separate the systems holds")
	}
}

// The interval is the whole point of the check above, so it has to move with the
// number of judgements rather than with the win rate alone.
func TestTheIntervalNarrowsAsMorePeopleRead(t *testing.T) {
	small := read(build{items: 400})
	large := read(build{items: 4000})

	if math.Abs(small.Rate-large.Rate) > 0.02 {
		t.Fatalf("the two runs are not the same preference: %.1f%% and %.1f%%", small.Rate*100, large.Rate*100)
	}
	if large.High-large.Low >= small.High-small.Low {
		t.Errorf("ten times the reading did not narrow the interval: %.3f then %.3f", small.High-small.Low, large.High-large.Low)
	}
}

// Agreement is what says the choice was about the answers rather than about the
// rater, and it is corrected for chance because three choices with ties rare
// means two people who read nothing would agree most of the time.
func TestRatersWhoDoNotAgreeWithEachOtherAreNamed(t *testing.T) {
	r := read(build{
		items: 400,
		pick: func(item, reader int) string {
			// The two readings of a doubled item disagree every time.
			if reader == 1 {
				return translated
			}
			if item%3 == 0 {
				return translated
			}
			return native
		},
	})

	if r.Pi > 0.4 {
		t.Fatalf("readings that disagreed on every doubled item came back at pi %.2f", r.Pi)
	}
	says(t, r.Faults(), "once chance is taken out")
	says(t, r.Faults(), "more about the rater than about the answers")
}

func TestAgreementIsReadOverTheSystemThatWonRatherThanTheSideThatWasPicked(t *testing.T) {
	// Every doubled item is read twice in opposite orders and both readings
	// choose the same system, which is perfect agreement and looks like perfect
	// disagreement to anything comparing left against right.
	r := read(build{items: 400})

	if r.Exact != 1 || r.Pi != 1 {
		t.Errorf("two readings that chose the same system every time agreed %.1f%% of the time, which came out at pi %.2f", r.Exact*100, r.Pi)
	}
	silent(t, r.Faults(), "once chance is taken out")
}

// Everybody choosing the same way every time is perfect agreement and it is
// also the case Scott's pi is not defined on, so it is reported as the
// agreement it is with the prevalence beside it rather than as a number that
// came out of a division by zero.
func TestPerfectAgreementOnOneOutcomeIsReportedAsWhatItIs(t *testing.T) {
	r := read(build{items: 400, pick: func(int, int) string { return native }})

	if r.Exact != 1 || r.Pi != 1 {
		t.Errorf("a protocol nobody disagreed on came back at %.1f%% agreement and pi %.2f", r.Exact*100, r.Pi)
	}
	if r.Common != native || r.Prevalence != 1 {
		t.Errorf("the second opinions came out as %s at %.1f%%", r.Common, r.Prevalence*100)
	}
	says(t, r.Faults(), "of the second opinions came out as")
	says(t, r.Faults(), "says little about whether the rest of the protocol was read")
	silent(t, r.Faults(), "once chance is taken out")
}

func TestOnePersonCarryingTheEvaluationIsNamed(t *testing.T) {
	r := read(build{items: 400, raters: 3})

	if r.Raters[0].Share < 0.25 {
		t.Fatalf("three people came back with a busiest share of %.1f%%", r.Raters[0].Share*100)
	}
	says(t, r.Faults(), "of the judgements, so the result is that person's preference with a sample size next to it")
}

func TestTooFewSecondOpinionsIsAFaultRatherThanARefusal(t *testing.T) {
	pairs := build{items: 400}.pairs()
	// Keep the first doubled item and drop the second reading of the rest.
	kept := make([]so.Pair, 0, len(pairs))
	seen := map[string]bool{}
	for _, p := range pairs {
		if seen[p.Item] && p.Item != "prompt-0000" {
			continue
		}
		seen[p.Item] = true
		kept = append(kept, p)
	}

	r := so.Read(kept)
	if why := r.Blocking(); len(why) > 0 {
		t.Fatalf("an evaluation with one second opinion was refused:\n  %s", strings.Join(why, "\n  "))
	}
	says(t, r.Faults(), "were read by more than one person")
	says(t, r.Faults(), "measured over too little of the set")
}

func TestAnEvaluationThatCannotBeReadIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(pairs []so.Pair) []so.Pair
		want  string
	}{
		{"no judgements", func([]so.Pair) []so.Pair { return nil }, "holds no judgements"},
		{"too few judgements", func(p []so.Pair) []so.Pair { return p[:100] }, "under the 200 this reading needs"},
		{"a judgement with no item", func(p []so.Pair) []so.Pair { p[3].Item = ""; return p }, "no item on it"},
		{"a judgement with no rater", func(p []so.Pair) []so.Pair { p[3].Rater = ""; return p }, "does not say who made it"},
		{"one person reading a pair twice", func(p []so.Pair) []so.Pair {
			p[3].Item, p[3].Rater = p[2].Item, p[2].Rater
			return p
		}, "is one person reading the same pair twice"},
		{"a system against itself", func(p []so.Pair) []so.Pair { p[3].Right = p[3].Left; return p }, "on both sides"},
		{"a choice that is not a choice", func(p []so.Pair) []so.Pair { p[3].Choice = "better"; return p }, `"better"`},
		{"a third system", func(p []so.Pair) []so.Pair { p[3].Right = "com-8b-sft-mixed"; return p }, "two system protocol"},
		{"every judgement a tie", func(p []so.Pair) []so.Pair {
			for i := range p {
				p[i].Choice = so.Tie
			}
			return p
		}, "no win rate to report"},
		{"nobody read anything twice", func(p []so.Pair) []so.Pair {
			out := make([]so.Pair, 0, len(p))
			seen := map[string]bool{}
			for _, one := range p {
				if seen[one.Item] {
					continue
				}
				seen[one.Item] = true
				out = append(out, one)
			}
			return out
		}, "no item was read by more than one person"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := so.Read(tc.spoil(build{items: 400}.pairs()))

			why := r.Blocking()
			if len(why) == 0 {
				t.Fatalf("the evaluation was accepted and should have been refused for %q", tc.want)
			}
			says(t, why, tc.want)
			if r.Holds() {
				t.Error("an evaluation that was refused also holds")
			}
			if len(r.Faults()) != 0 {
				t.Errorf("a refused evaluation also reported faults:\n  %s", strings.Join(r.Faults(), "\n  "))
			}
		})
	}
}

// One bad harness produces the same complaint on every line of the file, and a
// reader needs it once.
func TestARefusalThatIsTrueOfEveryLineIsSaidOnce(t *testing.T) {
	pairs := build{items: 400}.pairs()
	for i := range pairs {
		pairs[i].Rater = ""
	}

	why := so.Read(pairs).Blocking()
	if len(why) > 2 {
		t.Errorf("a file where every line has the same problem came back with %d refusals:\n  %s", len(why), strings.Join(why, "\n  "))
	}
}

func TestTheVerdictCarriesTheNumbersTheDecisionIsMadeOn(t *testing.T) {
	v := read(build{items: 400}).Verdict()

	for _, want := range []string{
		"read 480 pairs over 400 items",
		"picked com-8b-sft-native over com-8b-sft-translated",
		"called a tie",
		"beat com-8b-sft-translated here",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the verdict does not say %q:\n%s", want, v)
		}
	}
}
