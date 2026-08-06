package nau_test

import (
	"math"
	"strings"
	"testing"

	"github.com/tamnd/gao/nau"
)

// The point of the package is that the plan is checked rather than reviewed, so
// the first test is the check itself. Everything after it is a specific way the
// plan can go wrong that the check would otherwise have to be trusted about.
func TestThePlanHoldsTogether(t *testing.T) {
	for _, p := range nau.Check() {
		t.Error(p)
	}
	for _, p := range nau.CheckArms() {
		t.Error(p)
	}
}

func TestTheBudgetIsTheSizeItIsBoughtAt(t *testing.T) {
	const trillion = 1_000_000_000_000
	got := nau.Instances()
	if off := math.Abs(float64(got)-trillion) / trillion; off > 0.05 {
		t.Errorf("the run is %d token instances, and the compute is bought against a trillion", got)
	}
}

// The quality tiers are slices of the web rather than separate corpora, so
// adding every unique count together asks the crawl for text that already
// exists. This is the arithmetic behind the number the crawl is aimed at.
func TestTheQualityTiersAreNotCountedAsNewText(t *testing.T) {
	var all int64
	for _, c := range nau.Budget() {
		if c.Kind == nau.Natural {
			all += c.Unique
		}
	}
	if nau.NaturalUnique() >= all {
		t.Fatalf("natural Vietnamese counts %d unique tokens, which is every line added up, so the extra pass lines are being asked for twice", nau.NaturalUnique())
	}
	if want := int64(309_000_000_000); nau.NaturalUnique() != want {
		t.Errorf("the corpus target is %d unique natural tokens, and the plan is built on %d", nau.NaturalUnique(), want)
	}
}

// Epochs is what one line adds and Passes is what the text actually gets. The
// four pass ceiling is about the second one, and gao-edu is the line that sits
// on it.
func TestRepetitionIsCountedAgainstTheTextRatherThanTheLine(t *testing.T) {
	for _, c := range nau.Budget() {
		if c.Passes() < c.Epochs {
			t.Errorf("%s is read %.1f times and its own line asks for %.1f", c.Name, c.Passes(), c.Epochs)
		}
		if c.Passes() > 4 {
			t.Errorf("%s is read %.1f times, past the ceiling the whole repetition argument rests on", c.Name, c.Passes())
		}
	}
	edu, ok := find("gao-edu")
	if !ok {
		t.Fatal("gao-edu is not in the budget")
	}
	if edu.Passes() != 4 {
		t.Errorf("gao-edu is read %.1f times, and the plan argues for four", edu.Passes())
	}
}

func TestTheAnchorsBuyReasoningAndDoNotTakeOver(t *testing.T) {
	if v := nau.VietnameseShare(); math.Round(v) != 66 {
		t.Errorf("the mixture is %.1f%% Vietnamese, and the plan argues for 66", v)
	}
	if a := nau.KindShare(nau.Anchor); math.Round(a) != 34 {
		t.Errorf("the anchors are %.1f%% of the mixture, and the plan argues for 34", a)
	}
	if nau.KindShare(nau.Synthetic) >= nau.KindShare(nau.Natural) {
		t.Error("the run reads more Vietnamese a model wrote than Vietnamese people wrote")
	}
}

// Natural and synthetic are never summed into one headline, so the kinds have
// to partition the mixture rather than overlap it.
func TestEveryComponentIsCountedUnderExactlyOneKind(t *testing.T) {
	var total float64
	for _, k := range []nau.Kind{nau.Natural, nau.Synthetic, nau.Translated, nau.Anchor} {
		total += nau.KindShare(k)
	}
	if math.Abs(total-100) > 0.05 {
		t.Errorf("the kinds cover %.2f%% of the run", total)
	}
	if nau.Anchor.Vietnamese() {
		t.Error("the anchor languages are counted as Vietnamese")
	}
	for _, k := range []nau.Kind{nau.Natural, nau.Synthetic, nau.Translated} {
		if !k.Vietnamese() {
			t.Errorf("%s is not counted as Vietnamese", k)
		}
	}
}

func TestTheCurriculumGetsLongerAndNeverRepeatsAPhase(t *testing.T) {
	seen := map[string]bool{}
	last := 0
	for _, p := range nau.Curriculum() {
		if seen[p.Name] {
			t.Errorf("%s runs twice", p.Name)
		}
		seen[p.Name] = true
		if p.Sequence <= last {
			t.Errorf("%s trains at %d tokens of context, which is not longer than the phase before it", p.Name, p.Sequence)
		}
		last = p.Sequence
		if p.Why == "" {
			t.Errorf("%s is in the run with no argument for looking the way it does", p.Name)
		}
	}
}

// Every phase spends its own tokens, and the phases spend the run. A phase mix
// that adds to 98 is a run with two percent of nothing in it.
func TestEveryPhaseSpendsItselfAndThePhasesSpendTheRun(t *testing.T) {
	var whole int64
	for _, p := range nau.Curriculum() {
		var mix float64
		for _, s := range p.Mix {
			mix += s.Percent
		}
		if math.Abs(mix-100) > 0.05 {
			t.Errorf("%s reads %.1f%% of itself", p.Name, mix)
		}
		whole += p.Tokens()
	}
	if off := math.Abs(float64(whole-nau.Instances())) / float64(nau.Instances()); off > 0.001 {
		t.Errorf("the phases spend %d tokens and the budget buys %d", whole, nau.Instances())
	}
}

// The two tables were written by different arguments and they disagree. What is
// enforced is not that they agree but that every disagreement is somebody's to
// settle, in both directions: a gap with no question fails, and a question
// about a gap that has closed fails too.
func TestEveryGapBetweenTheTablesBelongsToSomebody(t *testing.T) {
	asked := map[string]bool{}
	for _, q := range nau.Questions() {
		asked[q.Component] = true
		if q.ID == "" || q.Ask == "" {
			t.Errorf("a question about %s is missing its number or its ask", q.Component)
		}
	}
	open := map[string]bool{}
	for _, g := range nau.Reconcile() {
		if math.Abs(g.Off()) <= nau.Tolerance && g.Spends > 0 {
			continue
		}
		open[g.Component] = true
		if !asked[g.Component] {
			t.Errorf("the budget buys %.1f%% of the run as %s and the curriculum spends %.1f%%, with no question about it", g.Buys, g.Component, g.Spends)
		}
	}
	for c := range asked {
		if !open[c] {
			t.Errorf("%s still has an open question and the two tables agree about it", c)
		}
	}
}

// The largest disagreement is the one worth naming in a test, because it is the
// one somebody will be tempted to close by editing whichever table is nearer.
func TestTheWebSlicesGapIsTheOneOnTheTable(t *testing.T) {
	worst := nau.Reconcile()[0]
	if worst.Component != "gao-web" {
		t.Errorf("the widest gap is %s, and the plan is written around gao-web being it", worst.Component)
	}
	if worst.Off() <= 0 {
		t.Errorf("the curriculum spends %.1f%% on gao-web and the budget buys %.1f%%, and the problem is that it spends more", worst.Spends, worst.Buys)
	}
}

func TestReconcileCoversEveryComponentOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range nau.Reconcile() {
		if seen[g.Component] {
			t.Errorf("%s appears twice", g.Component)
		}
		seen[g.Component] = true
	}
	for _, c := range nau.Budget() {
		if !seen[c.Name] {
			t.Errorf("%s is bought and never reconciled", c.Name)
		}
	}
}

// A grouped curriculum line covers two budget components, and reading the group
// at one rate spends them in the ratio the budget holds them. Splitting it
// evenly would credit the four billion token legal slice with as much of the
// phase as the eight billion token speech slice.
func TestAGroupedLineIsSplitInTheRatioTheBudgetHoldsIt(t *testing.T) {
	var legal, voice nau.Gap
	for _, g := range nau.Reconcile() {
		switch g.Component {
		case "gao-legal":
			legal = g
		case "gao-voice":
			voice = g
		}
	}
	if legal.Spends == 0 || voice.Spends == 0 {
		t.Fatal("the grouped line spends nothing on one of the components under it")
	}
	if got := voice.Spends / legal.Spends; math.Abs(got-2) > 0.01 {
		t.Errorf("the group spends %.2f times as much on speech as on statutes, and the budget holds twice as much", got)
	}
}

func TestTheArmsDifferInTheDataAndInNothingElse(t *testing.T) {
	arms := nau.Arms()
	if len(arms) != 3 {
		t.Fatalf("the comparison has %d arms", len(arms))
	}
	seen := map[string]bool{}
	for _, a := range arms {
		if a.ID == "" || a.Data == "" || a.Why == "" {
			t.Errorf("%q is missing a name, its data, or what it separates", a.ID)
		}
		if seen[a.ID] {
			t.Errorf("%s is in the comparison twice", a.ID)
		}
		seen[a.ID] = true
	}
}

// The filtered arm is the one that makes a win mean something, and it is the
// one a schedule slip would drop first.
func TestTheComparisonCanSeparateMoreDataFromCleanerData(t *testing.T) {
	var filtered bool
	for _, a := range nau.Arms() {
		if strings.Contains(a.Data, "CulturaX") && strings.Contains(a.Data, "gao") {
			filtered = true
		}
	}
	if !filtered {
		t.Error("no arm runs the baseline data through gao's cleaning, so a win says gao is better and not why")
	}
}

func TestTheRecipeIsAWholeRun(t *testing.T) {
	r := nau.Matched()
	if r.Vietnamese+r.Replay != 100 {
		t.Errorf("the recipe reads %.0f%% Vietnamese and replays %.0f%%", r.Vietnamese, r.Replay)
	}
	if r.Tokens <= 0 || r.Batch <= 0 {
		t.Error("the recipe has no length or no batch size")
	}
	if r.Gate == "" {
		t.Error("the comparison has no gate, so no result can fail it")
	}
	for _, s := range []string{r.LR, r.Tokenizer} {
		if s == "" {
			t.Error("the recipe leaves something for each arm to decide on its own")
		}
	}
}

// The fleet is the hardware we own and every other stage runs on it, so the
// assumption that this one does too is the natural one and it is wrong by
// nearly three orders of magnitude. The ratio is the part worth pinning.
func TestTheFleetIsNowhereNearBigEnoughToTrainOn(t *testing.T) {
	need, have, times := nau.Shortfall()
	if have <= 0 {
		t.Fatal("the fleet reports no accelerator memory at all")
	}
	if need <= have {
		t.Errorf("a run needs %d bytes of accelerator memory and the fleet has %d, so the plan should say it trains here", need, have)
	}
	if times < 100 {
		t.Errorf("the fleet is %.0f times short, and the plan is written around the gap being far too large to close with a smaller batch", times)
	}
	if !strings.Contains(nau.Fleet(), "gamingpc") {
		t.Error("the fleet's role does not name the box with the only GPU on it")
	}
}

// The prose in this package is read by people deciding whether to spend money,
// so it is held to the same bar as the README.
func TestThePlanReadsLikeSomebodyWroteIt(t *testing.T) {
	text := make([]string, 0, len(nau.Budget())+2*len(nau.Curriculum())+len(nau.Questions())+len(nau.Arms())+3)
	for _, c := range nau.Budget() {
		text = append(text, c.Why)
	}
	for _, p := range nau.Curriculum() {
		text = append(text, p.Why, p.LR)
	}
	for _, q := range nau.Questions() {
		text = append(text, q.Ask)
	}
	for _, a := range nau.Arms() {
		text = append(text, a.Why)
	}
	r := nau.Matched()
	text = append(text, r.LR, r.Tokenizer, r.Gate)

	for _, s := range text {
		if strings.Contains(s, "—") {
			t.Errorf("%q has an em dash in it", s)
		}
		if strings.Contains(s, "\n") {
			t.Errorf("%q has a line break inside it", s)
		}
		if strings.TrimSpace(s) != s {
			t.Errorf("%q is padded", s)
		}
	}
}

func find(name string) (nau.Component, bool) {
	for _, c := range nau.Budget() {
		if c.Name == name {
			return c, true
		}
	}
	return nau.Component{}, false
}
