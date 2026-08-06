package xep

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// pairs builds a labeling where n documents were placed twice, by two people,
// in the bands given. The documents are the ones the seed designates, so the
// designation faults do not fire in tests that are about something else.
func pairs(f Frame, of ...[2]Band) []Label {
	var out []Label
	source := f.Sources()[0]
	for i, n := 0, 0; n < len(of); i++ {
		d := doc.SumString(fmt.Sprintf("document %d", i))
		if !f.Doubled(d) {
			continue
		}
		out = append(out,
			Label{Doc: d, Source: source, By: "an", Band: of[n][0]},
			Label{Doc: d, Source: source, By: "binh", Band: of[n][1]},
		)
		n++
	}
	return out
}

// repeat is n comparisons that came out the same way.
func repeat(n int, a, b Band) [][2]Band {
	out := make([][2]Band, 0, n)
	for range n {
		out = append(out, [2]Band{a, b})
	}
	return out
}

// The number this whole file exists for. Two labelers who never read the rubric
// and always answer plain agree perfectly, and the rubric they never read is
// worth nothing.
func TestAgreementIsReportedAgainstWhatChanceWouldHaveGiven(t *testing.T) {
	f := Fixed()
	lazy := f.Agree(pairs(f, repeat(40, Plain, Plain)...))
	if lazy.Exact != 1 {
		t.Fatalf("two people who always answer plain agreed %.3f of the time", lazy.Exact)
	}
	if lazy.Kappa != 0 {
		t.Errorf("agreeing on the only band anybody used is worth %.3f above chance", lazy.Kappa)
	}
	if !strings.Contains(lazy.Verdict(), "two people agreeing on what the corpus mostly is") {
		t.Errorf("perfect raw agreement on one band read as a working rubric: %s", lazy.Verdict())
	}

	// The same raw figure, over a draw that uses the scale.
	real := repeat(10, Rich, Rich)
	real = append(real, repeat(10, Plain, Plain)...)
	real = append(real, repeat(10, Thin, Thin)...)
	real = append(real, repeat(10, Unusable, Unusable)...)
	spread := f.Agree(pairs(f, real...))
	if spread.Exact != lazy.Exact {
		t.Fatalf("the two labelings do not have the same raw agreement: %.3f and %.3f", spread.Exact, lazy.Exact)
	}
	if spread.Kappa <= lazy.Kappa {
		t.Errorf("the same raw agreement over four bands is worth %.3f and over one band %.3f", spread.Kappa, lazy.Kappa)
	}
	if spread.Chance >= lazy.Chance {
		t.Errorf("chance over four bands is %.3f and over one band %.3f", spread.Chance, lazy.Chance)
	}
}

// A rubric fails at a line rather than in general, and the line is the thing
// somebody can go and rewrite.
func TestTheDisagreementsAreCountedAgainstTheBoundaryTheyAreOn(t *testing.T) {
	f := Fixed()
	of := repeat(30, Rich, Rich)
	of = append(of, repeat(20, Plain, Plain)...)
	of = append(of, repeat(12, Plain, Thin)...)
	of = append(of, repeat(2, Rich, Plain)...)
	a := f.Agree(pairs(f, of...))

	worst, ok := a.Worst()
	if !ok {
		t.Fatal("nothing came back as the worst boundary")
	}
	if worst.A != Plain || worst.B != Thin || worst.Pairs != 12 {
		t.Errorf("the worst boundary is %s against %s over %d comparisons", worst.A, worst.B, worst.Pairs)
	}
	if len(a.Boundaries) != 2 {
		t.Errorf("%d boundaries came back and two lines were disagreed on: %+v", len(a.Boundaries), a.Boundaries)
	}
	for _, b := range a.Boundaries {
		if b.A == b.B || b.Apart < 1 {
			t.Errorf("%s against %s is not a boundary anybody disagreed across", b.A, b.B)
		}
	}
	if !strings.Contains(a.Verdict(), "plain against thin") {
		t.Errorf("the verdict does not name the line the rubric fails on: %s", a.Verdict())
	}
}

// Missing by one band and missing by two are different problems, and the
// weighted figure is where that distinction gets priced.
func TestAMissOfOneBandCountsForMoreThanAMissOfThree(t *testing.T) {
	f := Fixed()
	near := f.Agree(pairs(f, append(repeat(30, Rich, Rich), repeat(10, Plain, Thin)...)...))
	far := f.Agree(pairs(f, append(repeat(30, Rich, Rich), repeat(10, Rich, Unusable)...)...))

	if near.Exact != far.Exact {
		t.Fatalf("the two labelings do not have the same raw agreement: %.3f and %.3f", near.Exact, far.Exact)
	}
	if near.Weighted <= far.Weighted {
		t.Errorf("ten misses of one band are worth %.3f and ten misses of three are worth %.3f", near.Weighted, far.Weighted)
	}
	if near.Adjacent <= far.Adjacent {
		t.Errorf("adjacent agreement is %.3f on the near misses and %.3f on the far ones", near.Adjacent, far.Adjacent)
	}
	if !strings.Contains(strings.Join(far.Blocking(), "\n"), "four words in a list rather than a scale") {
		t.Errorf("a quarter of the comparisons three bands apart passed the scale check: %v", far.Blocking())
	}
}

// Left to choose, people check the documents they found hard, and the number
// that comes out is about those documents rather than about the draw.
func TestTheTenthThatGetsCheckedIsChosenBySeedRatherThanByLabelers(t *testing.T) {
	f := Fixed()
	var chosen int
	for i := range 2000 {
		if f.Doubled(doc.SumString(fmt.Sprintf("document %d", i))) {
			chosen++
		}
	}
	if chosen < 150 || chosen > 250 {
		t.Errorf("the seed designates %d of 2000 documents and the share is %.0f%%", chosen, 100*Double)
	}

	// The same seed twice, since a designation that moves between runs is not a
	// designation.
	seven, again := doc.SumString("document 7"), doc.SumString("document 7")
	if f.Doubled(seven) != f.Doubled(again) {
		t.Error("the same document was designated one way and then the other")
	}
	other := f
	other.Seed = "some other seed"
	var moved int
	for i := range 500 {
		d := doc.SumString(fmt.Sprintf("document %d", i))
		if f.Doubled(d) != other.Doubled(d) {
			moved++
		}
	}
	if moved == 0 {
		t.Error("a different seed designated exactly the same documents, so the seed is not what decides")
	}
}

func TestSecondOpinionsOnDocumentsNobodyDesignatedAreReported(t *testing.T) {
	f := Fixed()
	labels := pairs(f, repeat(20, Plain, Plain)...)

	// One more comparison, on a document the seed did not designate.
	var stray doc.Hash
	for i := range 100 {
		d := doc.SumString(fmt.Sprintf("stray %d", i))
		if !f.Doubled(d) {
			stray = d
			break
		}
	}
	if stray.IsZero() {
		t.Fatal("the seed designated a hundred documents in a row")
	}
	source := f.Sources()[0]
	labels = append(labels,
		Label{Doc: stray, Source: source, By: "an", Band: Rich},
		Label{Doc: stray, Source: source, By: "binh", Band: Rich},
	)

	a := f.Agree(labels)
	if a.Elsewhere != 1 {
		t.Errorf("%d second opinions came back as undesignated", a.Elsewhere)
	}
	if a.Drawn != 20 {
		t.Errorf("%d of the designated documents got a second opinion", a.Drawn)
	}
	if !strings.Contains(strings.Join(a.Blocking(), "\n"), "the documents they thought were worth checking") {
		t.Errorf("a second opinion nobody drew passed unremarked: %v", a.Blocking())
	}
}

func TestADesignatedDocumentNobodyCheckedTwiceIsAHole(t *testing.T) {
	f := Fixed()
	labels := pairs(f, repeat(20, Rich, Plain)...)

	// One designated document with a single label on it.
	source := f.Sources()[0]
	for i := 1000; i < 1200; i++ {
		d := doc.SumString(fmt.Sprintf("document %d", i))
		if f.Doubled(d) {
			labels = append(labels, Label{Doc: d, Source: source, By: "an", Band: Plain})
			break
		}
	}

	a := f.Agree(labels)
	if a.Designated != 21 || a.Drawn != 20 {
		t.Fatalf("%d designated and %d of them checked twice", a.Designated, a.Drawn)
	}
	if !strings.Contains(strings.Join(a.Blocking(), "\n"), "the tenth that was checked is not the tenth that was drawn") {
		t.Errorf("a designated document nobody checked twice passed unremarked: %v", a.Blocking())
	}
}

func TestALabelingNobodyCheckedTwiceHasNoAgreementInIt(t *testing.T) {
	f := Fixed()
	source := f.Sources()[0]
	labels := make([]Label, 0, 40)
	for i := range 40 {
		labels = append(labels, Label{Doc: doc.SumString(fmt.Sprintf("alone %d", i)), Source: source, By: "an", Band: Plain})
	}

	a := f.Agree(labels)
	if a.Passed() {
		t.Fatal("a labeling with no second opinion in it passed")
	}
	if a.Pairs != 0 || a.Kappa != 0 {
		t.Errorf("%d comparisons produced a kappa of %.3f", a.Pairs, a.Kappa)
	}
	if !strings.Contains(a.Verdict(), "tested against one reading of it") {
		t.Errorf("the verdict does not say there is nothing here to measure: %s", a.Verdict())
	}
	if _, ok := a.Worst(); ok {
		t.Error("a boundary came back off a labeling with no disagreements in it")
	}
}

// A person agreeing with themselves is not agreement, and neither is a label
// against a source the frame does not draw from.
func TestOneLabelerTwiceIsNotAComparison(t *testing.T) {
	f := Fixed()
	d := doc.SumString("document 0")
	if !f.Doubled(d) {
		t.Skip("document 0 is not in the designated tenth of this frame")
	}
	source := f.Sources()[0]
	a := f.Agree([]Label{
		{Doc: d, Source: source, By: "an", Band: Rich},
		{Doc: d, Source: source, By: "an", Band: Unusable},
		{Doc: d, Source: "somewhere nobody draws from", By: "binh", Band: Thin},
		{Doc: d, Source: source, By: "binh", Band: "excellent"},
	})
	if a.Pairs != 0 {
		t.Errorf("%d comparisons came out of one person's labels", a.Pairs)
	}
}

// A rubric read backwards is worse than nobody reading it, and that is worth
// being able to see rather than clamping to zero.
func TestARubricReadBackwardsIsWorseThanChance(t *testing.T) {
	f := Fixed()
	of := repeat(10, Rich, Unusable)
	of = append(of, repeat(10, Unusable, Rich)...)
	of = append(of, repeat(10, Plain, Thin)...)
	of = append(of, repeat(10, Thin, Plain)...)
	a := f.Agree(pairs(f, of...))

	if a.Exact != 0 {
		t.Fatalf("two people who never chose the same band agreed %.3f of the time", a.Exact)
	}
	if a.Kappa >= 0 {
		t.Errorf("never agreeing is worth %.3f, and chance was %.3f", a.Kappa, a.Chance)
	}
	if a.Passed() {
		t.Error("a labeling where nobody ever agreed passed")
	}
}

// A working rubric, which is the case everything above is measured against.
func TestARubricThatDecidesPassesAndSaysWhereItStillCosts(t *testing.T) {
	f := Fixed()
	of := repeat(28, Rich, Rich)
	of = append(of, repeat(28, Plain, Plain)...)
	of = append(of, repeat(24, Thin, Thin)...)
	of = append(of, repeat(16, Unusable, Unusable)...)
	of = append(of, repeat(4, Plain, Thin)...)
	a := f.Agree(pairs(f, of...))

	if !a.Passed() {
		t.Fatalf("a rubric two people follow did not pass: %v", a.Blocking())
	}
	if a.Kappa < MinKappa || a.Kappa > 1 {
		t.Errorf("kappa is %.3f", a.Kappa)
	}
	if a.Weighted <= a.Kappa {
		t.Errorf("the weighted figure is %.3f and the plain one %.3f, and the only misses were one band apart", a.Weighted, a.Kappa)
	}
	if !strings.Contains(a.Verdict(), "above chance") {
		t.Errorf("a passing verdict does not report the chance correction: %s", a.Verdict())
	}
}
