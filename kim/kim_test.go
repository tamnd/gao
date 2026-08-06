package kim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// built returns a set matching the frame exactly, with change applied first so
// a test can break one thing about it.
func built(change func([]Item) []Item) []Item {
	var out []Item
	for _, c := range Frame() {
		for i := range c.Items {
			it := Item{
				ID:       cellKey(c.Length, c.Kind, c.Depth, c.Second) + "#" + string(rune('a'+i)),
				Kind:     c.Kind,
				Length:   c.Length,
				Depth:    c.Depth,
				Second:   c.Second,
				Haystack: doc.SumString("haystack"),
				Question: "Nghị quyết nào nói về việc này?",
			}
			switch c.Kind {
			case Absent:
				it.Question = "Văn bản nói gì về sản lượng cà phê?"
			case Toned:
				it.Answer = "Hoà Bình"
				it.Near = []string{"Họa Bình"}
				it.Decoy = []string{"Ninh Bình"}
			default:
				it.Answer = "số 42/2019/QH14"
				it.Decoy = []string{"số 43/2019/QH14"}
			}
			out = append(out, it)
		}
	}
	if change != nil {
		out = change(out)
	}
	return out
}

// answers replies to every item, with reply deciding what each one said.
func answers(items []Item, reply func(Item) string) []Reply {
	out := make([]Reply, 0, len(items))
	for _, it := range items {
		out = append(out, Reply{
			Item: it.ID, Frame: Digest(), Text: reply(it),
			Box: "gamingpc", Context: it.Length,
		})
	}
	return out
}

// perfect is a model that finds every needle and declines every absent item.
func perfect(it Item) string {
	if it.Kind == Absent {
		return "Văn bản không đề cập đến điều đó."
	}
	return "Theo tài liệu, đó là " + it.Answer + "."
}

func has(t *testing.T, got []string, want string) {
	t.Helper()
	for _, g := range got {
		if strings.Contains(g, want) {
			return
		}
	}
	t.Errorf("nothing said %q:\n%s", want, strings.Join(got, "\n"))
}

func hasNot(t *testing.T, got []string, want string) {
	t.Helper()
	for _, g := range got {
		if strings.Contains(g, want) {
			t.Errorf("something said %q, and it should not have:\n%s", want, g)
		}
	}
}

func TestTheFrameCoversEveryLengthAtEveryDepth(t *testing.T) {
	seen := map[int]map[float64]bool{}
	for _, c := range Frame() {
		if c.Kind != Plain {
			continue
		}
		if seen[c.Length] == nil {
			seen[c.Length] = map[float64]bool{}
		}
		seen[c.Length][c.Depth] = true
	}
	for _, n := range Lengths {
		for _, d := range Depths {
			if !seen[n][d] {
				t.Errorf("nothing is asked at %d tokens, %.0f%% of the way in", n, 100*d)
			}
		}
	}
}

func TestTheEndsAndTheMiddleAreBothInTheGrid(t *testing.T) {
	// A needle test whose depths are 0, 0.5 and 1 reports a model that skims the
	// middle as one number slightly under one, which is the reason the grid has
	// points just inside each end.
	for _, d := range []float64{0, 0.1, 0.5, 0.9, 1} {
		if !slices.Contains(Depths, d) {
			t.Errorf("depth %.1f is not in the grid", d)
		}
	}
}

func TestTheFrameHasItemsWithNothingToFind(t *testing.T) {
	var absent int
	for _, c := range Frame() {
		if c.Kind == Absent {
			absent += c.Items
		}
	}
	if absent == 0 {
		t.Fatal("every item has an answer in it, which rewards a model that always produces its most plausible span")
	}
}

func TestTheDigestMovesWhenTheGridDoes(t *testing.T) {
	before := Digest()
	old := Depths
	t.Cleanup(func() { Depths = old })
	Depths = []float64{0, 0.5, 1}
	if Digest() == before {
		t.Fatal("the grid changed and its digest did not, so a result could name a frame it was not built against")
	}
}

func TestRunningTheWholeSetIsPricedInTokens(t *testing.T) {
	if Tokens() < 1_000_000 {
		t.Fatalf("%d tokens is too cheap to be this set", Tokens())
	}
	if !strings.Contains(Describe(), "million tokens") {
		t.Errorf("the description does not say what running it costs: %s", Describe())
	}
}

func TestASetBuiltToTheFrameHasNothingWrongWithIt(t *testing.T) {
	if faults := Check(built(nil)); len(faults) > 0 {
		t.Fatalf("a set built to the frame was refused:\n%s", strings.Join(faults, "\n"))
	}
}

func TestASetWithHolesInItIsRefusedRatherThanAveraged(t *testing.T) {
	faults := Check(built(func(items []Item) []Item {
		return slices.DeleteFunc(items, func(it Item) bool { return it.Depth > 0.6 && it.Kind == Plain })
	}))
	has(t, faults, "the easy squares")
}

func TestANeedleThatAppearsOnceIsFoundByStringSearchRatherThanByReading(t *testing.T) {
	faults := Check(built(func(items []Item) []Item {
		items[0].Decoy = nil
		return items
	}))
	has(t, faults, "never reading the question")
}

func TestATonedItemWhoseNearMissIsADifferentWordIsNotATonedItem(t *testing.T) {
	faults := Check(built(func(items []Item) []Item {
		for i := range items {
			if items[i].Kind == Toned {
				items[i].Near = []string{"Sơn La"}
				break
			}
		}
		return items
	}))
	has(t, faults, "did something else")
}

func TestATonedItemWithNoNearMissMeasuresOrdinaryRetrieval(t *testing.T) {
	faults := Check(built(func(items []Item) []Item {
		for i := range items {
			if items[i].Kind == Toned {
				items[i].Near = nil
				break
			}
		}
		return items
	}))
	has(t, faults, "nothing for the marks to distinguish")
}

func TestAnAbsentItemThatCarriesAnAnswerIsNotAbsent(t *testing.T) {
	faults := Check(built(func(items []Item) []Item {
		for i := range items {
			if items[i].Kind == Absent {
				items[i].Answer = "số 42/2019/QH14"
				break
			}
		}
		return items
	}))
	has(t, faults, "scored as an invention")
}

func TestAnItemAddedToAFixedSetIsRefused(t *testing.T) {
	faults := Check(built(func(items []Item) []Item {
		extra := items[0]
		extra.ID = "extra"
		return append(items, extra)
	}))
	has(t, faults, "somebody wanted in it")
}

func TestAnItemThatAppearsTwiceIsNamed(t *testing.T) {
	items := built(nil)
	faults := Check(append(items, items[0]))
	has(t, faults, "appear twice")
}

func TestAModelThatReadsTheWholeContextPasses(t *testing.T) {
	items := built(nil)
	g := Grade(items, answers(items, perfect))
	if !g.Passes {
		t.Fatalf("a model that answered everything failed: %s", g.Verdict())
	}
	if len(g.Blocking()) > 0 {
		t.Errorf("a clean run was held back:\n%s", strings.Join(g.Blocking(), "\n"))
	}
	if g.Recall != 1 {
		t.Errorf("recall %.3f, want 1", g.Recall)
	}
}

func TestAModelThatOnlyReadsTheEndsFailsOnTheSpreadRatherThanOnRecall(t *testing.T) {
	items := built(nil)
	g := Grade(items, answers(items, func(it Item) string {
		// The ends come back and everything past a quarter of the way in does
		// not, which is the shape a 128k model sold on one number actually has.
		if it.Kind != Absent && it.Depth > 0.2 && it.Depth < 0.8 {
			return "Tài liệu không nói rõ."
		}
		return perfect(it)
	}))
	if g.Spread <= MaxSpread {
		t.Fatalf("spread is %.3f, and a model that answered nothing in the middle should not clear %.2f", g.Spread, MaxSpread)
	}
	if g.Passes {
		t.Fatal("a model that reads the ends of a context passed a long context benchmark")
	}
	if !strings.Contains(g.Verdict(), "reads the ends of a context") {
		t.Errorf("the verdict does not say what went wrong: %s", g.Verdict())
	}
}

func TestAModelThatHasFoldedTheToneMarksIsCountedApartFromOneThatMissed(t *testing.T) {
	items := built(nil)
	g := Grade(items, answers(items, func(it Item) string {
		if it.Kind == Toned {
			return "Đó là " + it.Near[0] + "."
		}
		return perfect(it)
	}))
	if g.Counts[Tone] == 0 {
		t.Fatal("answering with the near miss was not counted as tone confusion")
	}
	if g.Counts[Missed] > 0 {
		t.Errorf("%d tone confusions were counted as ordinary misses", g.Counts[Missed])
	}
	if g.Tone <= MaxTone {
		t.Errorf("tone confusion is %.3f and every toned item was wrong", g.Tone)
	}
	if !strings.Contains(g.Verdict(), "folded the tone marks") {
		t.Errorf("the verdict does not name the bug: %s", g.Verdict())
	}
}

func TestAModelThatMatchesTheSurfaceFormLandsOnTheDecoy(t *testing.T) {
	items := built(nil)
	g := Grade(items, answers(items, func(it Item) string {
		if it.Kind == Plain {
			return "Tài liệu ghi nghị quyết " + it.Decoy[0] + "."
		}
		return perfect(it)
	}))
	if g.Counts[Decoyed] == 0 {
		t.Fatal("an answer quoting the decoy sentence was not counted as a decoy")
	}
}

func TestAModelThatAnswersAnItemWithNoNeedleIsInventing(t *testing.T) {
	items := built(nil)
	g := Grade(items, answers(items, func(it Item) string {
		if it.Kind == Absent {
			return "Sản lượng cà phê năm đó là 1,8 triệu tấn."
		}
		return perfect(it)
	}))
	if g.Invent != 1 {
		t.Fatalf("invention rate %.3f, and every absent item got an answer", g.Invent)
	}
	if g.Passes {
		t.Fatal("a model that answers questions the document does not answer passed")
	}
}

func TestDecliningWithTheToneMarksLeftOffIsStillDeclining(t *testing.T) {
	items := built(nil)
	g := Grade(items, answers(items, func(it Item) string {
		if it.Kind == Absent {
			return "Tai lieu khong de cap den dieu do."
		}
		return perfect(it)
	}))
	if g.Invent != 0 {
		t.Fatalf("invention rate %.3f, and a model writing without marks is a register rather than an invention", g.Invent)
	}
}

func TestAnAnswerFromLessContextThanTheItemIsADifferentItem(t *testing.T) {
	items := built(nil)
	replies := answers(items, perfect)
	for i := range replies {
		if replies[i].Context == slices.Max(Lengths) {
			replies[i].Context = 8_000
		}
	}
	g := Grade(items, replies)
	has(t, g.Blocking(), "it is a different item")
	if g.Passes {
		t.Fatal("a run that was silently truncated to 8k passed at 128k")
	}
}

func TestASplitItemIsCountedAtBothOfItsDepths(t *testing.T) {
	items := built(nil)
	g := Grade(items, answers(items, perfect))
	var asked int
	for _, p := range g.Curve {
		asked += p.Asked
	}
	var split, needled int
	for _, it := range items {
		switch it.Kind {
		case Absent:
		case Split:
			split++
			needled++
		default:
			needled++
		}
	}
	if asked != needled+split {
		t.Fatalf("the curve holds %d points and the set has %d needles across %d items, so a split item is not being counted at both ends", asked, needled+split, needled)
	}
}

func TestARunThatSkippedItemsIsNotARecallOverTheSet(t *testing.T) {
	items := built(nil)
	replies := answers(items, perfect)
	g := Grade(items, replies[:len(replies)-20])
	has(t, g.Blocking(), "the ones it did not are the long ones")
	if g.Passes {
		t.Fatal("a run that did not finish passed")
	}
}

func TestAnswersFromTwoBoxesAreTwoRuns(t *testing.T) {
	items := built(nil)
	replies := answers(items, perfect)
	replies[0].Box = "server3"
	g := Grade(items, replies)
	has(t, g.Blocking(), "a run split across boxes is two runs")
}

func TestAnAnswerWithNoBoxOnItCannotBeReproduced(t *testing.T) {
	items := built(nil)
	replies := answers(items, perfect)
	replies[0].Box = ""
	g := Grade(items, replies)
	has(t, g.Blocking(), "cannot be reproduced")
}

func TestAnAnswerNamingADifferentFrameIsNotAComparison(t *testing.T) {
	items := built(nil)
	replies := answers(items, perfect)
	replies[0].Frame = doc.SumString("some other grid")
	g := Grade(items, replies)
	has(t, g.Blocking(), "the numbers do not compare")
}

func TestAnAnswerToAnItemNobodyAskedIsNamed(t *testing.T) {
	items := built(nil)
	replies := append(answers(items, perfect), Reply{Item: "ghost", Frame: Digest(), Box: "gamingpc"})
	g := Grade(items, replies)
	has(t, g.Blocking(), "not in the set")
}

func TestAnItemAnsweredTwiceKeepsTheFirstAnswer(t *testing.T) {
	items := built(nil)
	replies := answers(items, perfect)
	second := replies[0]
	second.Text = "Tài liệu không nói."
	g := Grade(items, append(replies, second))
	has(t, g.Blocking(), "after reading the first answer")
	if g.Recall != 1 {
		t.Errorf("the second answer was scored: recall %.3f", g.Recall)
	}
}

func TestGradingNothingIsNotAResult(t *testing.T) {
	g := Grade(built(nil), nil)
	has(t, g.Blocking(), "there is no result here")
	hasNot(t, g.Blocking(), "went unanswered")
	if g.Passes {
		t.Fatal("an empty run passed")
	}
}

func TestARunReadsBackFromDisk(t *testing.T) {
	items := built(nil)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "items.jsonl"), items)
	write(t, filepath.Join(dir, "replies.jsonl"), answers(items, perfect))

	got, err := ReadItems(filepath.Join(dir, "items.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	replies, err := ReadReplies(filepath.Join(dir, "replies.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(items) || len(replies) != len(items) {
		t.Fatalf("read %d items and %d replies from %d items", len(got), len(replies), len(items))
	}
	if !Grade(got, replies).Passes {
		t.Error("a set that round-tripped through disk stopped passing")
	}
}

func TestAnItemWithNoIDCannotBeMatchedToAnAnswer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "items.jsonl")
	if err := os.WriteFile(path, []byte(`{"kind":"plain","length":4000}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadItems(path); err == nil {
		t.Fatal("an item with no ID was read")
	}
}

func TestAnEmptyRunSaysSoRatherThanScoringZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replies.jsonl")
	if err := os.WriteFile(path, []byte("# nothing ran yet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReplies(path); err == nil {
		t.Fatal("an empty file read as a run")
	}
}

func TestAFieldNobodyDeclaredIsRefusedRatherThanDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replies.jsonl")
	if err := os.WriteFile(path, []byte(`{"item":"a","text":"x","confidence":0.9}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReplies(path); err == nil {
		t.Fatal("a reply carrying a field the grader does not read was accepted")
	}
}

func write[T any](t *testing.T, path string, rows []T) {
	t.Helper()
	var b strings.Builder
	for _, r := range rows {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
