package fill

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/normalize"
)

// A pool of well formed Vietnamese syllables, built rather than listed, because
// the tests below need several hundred distinct spellings with marks on them
// and a hand written list of that size is a list nobody checks.
func pool() []string {
	onsets := strings.Fields("b c ch d đ g h k kh l m n ng nh ph qu r s t th tr v x")
	rimes := strings.Fields("à á ả ã ạ ê ề ế ơ ờ ớ ú ù í ì ó ò ăn ân ơn iên uôn ương")
	out := make([]string, 0, len(onsets)*len(rimes))
	for _, o := range onsets {
		for _, r := range rimes {
			out = append(out, o+r)
		}
	}
	return out
}

// A document of distinct syllables drawn with a bias toward the front of the
// pool, so the corpus has a frequency ranking worth being near in rather than a
// flat one where every syllable is every other syllable's neighbor.
func page(n int, syllables int) (doc.Hash, string) {
	p := pool()
	seed := uint64(n+1) * 0x9e3779b97f4a7c15
	next := func() uint64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return seed >> 33
	}

	used := map[int]bool{}
	words := make([]string, 0, syllables)
	for len(words) < syllables {
		i := int(next()%uint64(len(p))) * int(next()%uint64(len(p))) / len(p)
		if used[i] {
			continue
		}
		used[i] = true
		words = append(words, p[i])
	}
	text := strings.Join(words, " ")
	return doc.SumString(text), text
}

// A corpus of pages, and a vocabulary counted over pages the items were not
// built from, which is the arrangement the package insists on.
func corpus(t *testing.T, pages int) (*Vocabulary, []doc.Hash, []string) {
	t.Helper()
	v := NewVocabulary()
	for i := range 400 {
		_, text := page(10_000+i, 60)
		if !v.Add(text) {
			t.Fatalf("page %d was refused by the vocabulary", i)
		}
	}

	ids := make([]doc.Hash, 0, pages)
	texts := make([]string, 0, pages)
	for i := range pages {
		id, text := page(i, 60)
		ids = append(ids, id)
		texts = append(texts, text)
	}
	return v, ids, texts
}

func options() Options {
	o := Default()
	o.MinChars = 100
	o.Function = 5
	o.Band = 30
	return o
}

func build(t *testing.T, pages int) (*Builder, *Vocabulary) {
	t.Helper()
	v, ids, texts := corpus(t, pages)
	b := NewBuilder(options(), v)
	for i := range ids {
		b.Add(ids[i], texts[i])
	}
	return b, v
}

// The answer key is the page. An item that does not put its own passage back
// together has lost the thing that makes this benchmark free.
func TestTheAnswerIsWhatThePageSaid(t *testing.T) {
	b, _ := build(t, 40)
	if len(b.Items()) == 0 {
		t.Fatal("no items were built out of 40 pages")
	}
	for _, it := range b.Items() {
		if !strings.Contains(it.Prompt, Blank) {
			t.Fatalf("the prompt has no blank in it: %q", it.Prompt)
		}
		if strings.Contains(it.Passage(), Blank) {
			t.Errorf("filling the blank left a blank behind: %q", it.Passage())
		}
		if len(it.Choices) != Candidates {
			t.Errorf("the item offers %d choices, want %d", len(it.Choices), Candidates)
		}
		if it.Right() == "" {
			t.Errorf("the item has no right answer among %v", it.Choices)
		}
	}
}

// Every document leaves with an item or with exactly one reason, and the counts
// have to add up, or a builder can drop most of what it is given without
// anybody noticing.
func TestEveryDocumentIsAccountedFor(t *testing.T) {
	b, _ := build(t, 60)
	total := len(b.Items())
	for _, r := range Reasons() {
		total += b.Rejected(r)
	}
	if total != b.Read() {
		t.Errorf("%d documents read and %d accounted for", b.Read(), total)
	}
}

// The set has to rebuild identically, including when the documents arrive in a
// different order, or no two runs of the ablation slate are comparable.
func TestTheSetRebuildsTheSameWayInAnyOrder(t *testing.T) {
	v, ids, texts := corpus(t, 40)

	forward := NewBuilder(options(), v)
	for i := range ids {
		forward.Add(ids[i], texts[i])
	}
	backward := NewBuilder(options(), v)
	for i := len(ids) - 1; i >= 0; i-- {
		backward.Add(ids[i], texts[i])
	}

	got := map[doc.Hash]Item{}
	for _, it := range backward.Items() {
		got[it.DocID] = it
	}
	for _, want := range forward.Items() {
		it, ok := got[want.DocID]
		if !ok {
			t.Fatalf("the reverse pass did not build an item for %v", want.DocID)
		}
		if it.Prompt != want.Prompt || it.Answer != want.Answer || it.Rank != want.Rank {
			t.Errorf("the same document made two different items:\n%+v\n%+v", want, it)
		}
		for i := range it.Choices {
			if it.Choices[i] != want.Choices[i] {
				t.Errorf("the choices came out in a different order: %v against %v", want.Choices, it.Choices)
				break
			}
		}
	}
}

// The floor under the whole benchmark. If picking the most common candidate
// scores well, the benchmark measures the unigram distribution and a model that
// beats it has not been shown to have read anything.
func TestPickingTheCommonestCandidateScoresChance(t *testing.T) {
	b, v := build(t, 400)
	if len(b.Items()) < 50 {
		t.Fatalf("%d items is too few to say anything about a baseline", len(b.Items()))
	}
	r := NewReport("server1")
	for _, it := range b.Items() {
		r.Add(Grade(it, Frequent(v, it)))
	}
	if got := r.Accuracy(); got > Chance+0.12 {
		t.Errorf("picking the commonest candidate scores %.1f%% against a chance floor of %.1f%%, so the frequency ranks are not spread",
			100*got, 100*Chance)
	}
	if got := r.Skew(); got > 0.15 {
		t.Errorf("the frequency ranks are %.1f%% off an even spread", 100*got)
	}
}

// Choosing between ma, má and mà is a real task and it is dau's task. An item
// here that offered two spellings of one syllable would be that task in
// disguise, and the two benchmarks would be measuring one thing while appearing
// to measure two.
func TestNoChoiceIsTheAnswerWithDifferentMarks(t *testing.T) {
	b, _ := build(t, 200)
	for _, it := range b.Items() {
		want := normalize.Fold(it.Right())
		for i, s := range it.Choices {
			if i != it.Answer && normalize.Fold(s) == want {
				t.Errorf("%q is offered beside %q, which is diacritic restoration rather than cloze", s, it.Right())
			}
		}
	}
}

// A blank over của or và is answered by grammar. A proxy made of those
// saturates before the slate starts and stops separating recipes exactly when
// it is needed.
func TestTheCommonestSyllablesAreNeverBlanked(t *testing.T) {
	b, v := build(t, 200)
	o := options()
	for _, it := range b.Items() {
		at, ok := v.Rank(it.Right())
		if !ok {
			t.Errorf("%q was blanked and the vocabulary has never seen it", it.Right())
			continue
		}
		if at < o.Function {
			t.Errorf("%q is rank %d and the first %d are function words", it.Right(), at, o.Function)
		}
	}
}

// A syllable that appears twice in the passage can be copied from its other
// occurrence, and a benchmark won by pattern matching inside the prompt
// measures pattern matching.
func TestTheAnswerAppearsNowhereElseInThePassage(t *testing.T) {
	b, _ := build(t, 200)
	for _, it := range b.Items() {
		want := normalize.Fold(it.Right())
		for _, s := range syllables(it.Prompt) {
			if normalize.Fold(s.text) == want {
				t.Errorf("%q is still in the prompt beside its own blank: %q", it.Right(), it.Prompt)
				break
			}
		}
	}
}

// Half the Vietnamese online is typed without marks. A page typed that way is
// not a passage anybody can be asked to fill a blank in, because the bare form
// of the answer is a candidate.
func TestAPageTypedWithoutMarksIsRefused(t *testing.T) {
	v, ids, texts := corpus(t, 1)
	b := NewBuilder(options(), v)
	if _, r, ok := b.Add(ids[0], normalize.Bare(texts[0])); ok || r != Unmarked {
		t.Errorf("a bare page came back with %q, want %q", r, Unmarked)
	}
}

func TestAShortPageAndARepeatAreRefusedAndCounted(t *testing.T) {
	v, ids, texts := corpus(t, 2)
	b := NewBuilder(options(), v)

	if _, r, ok := b.Add(doc.SumString("ngắn"), "một hai ba"); ok || r != TooShort {
		t.Errorf("three syllables came back with %q, want %q", r, TooShort)
	}
	b.Add(ids[0], texts[0])
	if _, r, ok := b.Add(ids[0], texts[0]); ok || r != Duplicate {
		t.Errorf("the same document twice came back with %q, want %q", r, Duplicate)
	}
	_ = ids[1]
	if b.Rejected(TooShort) != 1 || b.Rejected(Duplicate) != 1 {
		t.Errorf("the rejections were not counted: %d short, %d duplicate", b.Rejected(TooShort), b.Rejected(Duplicate))
	}
}

// The vocabulary is the whole of what the benchmark knows about Vietnamese, so
// a page typed bare must not get into it.
func TestTheVocabularyRefusesBarePages(t *testing.T) {
	v := NewVocabulary()
	_, text := page(1, 60)
	if v.Add(normalize.Bare(text)) {
		t.Error("a page typed without marks was counted into the ranking")
	}
	if v.Size() != 0 {
		t.Errorf("the ranking holds %d syllables off a page it refused", v.Size())
	}
}

func TestTheRankingIsStableAndSplitsAroundASyllable(t *testing.T) {
	v, _, _ := corpus(t, 1)
	s, ok := v.At(300)
	if !ok {
		t.Fatal("the ranking is shorter than 300 syllables")
	}
	at, ok := v.Rank(s)
	if !ok || at != 300 {
		t.Errorf("the syllable at rank 300 reports rank %d", at)
	}
	above, below := v.Neighbors(s, 10)
	if len(above) != 10 || len(below) != 10 {
		t.Fatalf("a syllable in the middle of the ranking has %d neighbors above and %d below", len(above), len(below))
	}
	if v.Count(above[0]) < v.Count(s) || v.Count(below[0]) > v.Count(s) {
		t.Error("the neighbors are not split by how common they are")
	}
}

func TestTheReportSaysWhatItIsAgainst(t *testing.T) {
	b, _ := build(t, 60)
	r := NewReport("gamingpc")
	for _, it := range b.Items() {
		r.Add(Grade(it, it.Answer))
	}
	if r.Accuracy() != 1 {
		t.Errorf("answering every item right scored %.1f%%", 100*r.Accuracy())
	}
	out := r.String()
	for _, want := range []string{Name, "gamingpc", "25.0%", "even spread"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "—") {
		t.Error("the report has an em dash in it")
	}
}

func TestAReportWithNoBoxSaysSoRatherThanLeavingItBlank(t *testing.T) {
	r := NewReport("")
	if !strings.Contains(r.String(), "did not say which") {
		t.Errorf("a report off an unnamed box hides it:\n%s", r.String())
	}
}
