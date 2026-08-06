package dem_test

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/tamnd/gao/dem"
	"github.com/tamnd/gao/doc"
)

// The gates are only worth having if they can fail, and the tokenizer this
// project uses passes all ten, so every test below runs the suite against a
// tokenizer written to break one rule and leave the others alone.
//
// A toy is a splitter, a vocabulary it fills in as it goes, and an optional way
// of getting the text back wrong. That is enough to be a tokenizer for the
// purposes of a gate, which is the point of the gates taking an interface.
type toy struct {
	name   string
	split  func(text string) []string
	garble func(text string) string
	nfc    bool

	pieces []string
	ids    map[string]int
}

func newToy(name string, split func(string) []string, seed ...string) *toy {
	t := &toy{name: name, split: split, ids: map[string]int{}}
	for _, s := range seed {
		t.id(s)
	}
	return t
}

func (t *toy) Name() string { return t.name }
func (t *toy) Vocab() int   { return len(t.pieces) }

func (t *toy) Encode(text string) []dem.Piece {
	if t.nfc {
		text = norm.NFC.String(text)
	}
	parts := t.split(text)
	ps := make([]dem.Piece, len(parts))
	for i, s := range parts {
		ps[i] = dem.Piece{ID: t.id(s), Text: s}
	}
	return ps
}

func (t *toy) Decode(ids []int) string {
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(t.pieces[id])
	}
	if t.garble != nil {
		return t.garble(b.String())
	}
	return b.String()
}

func (t *toy) id(s string) int {
	if id, ok := t.ids[s]; ok {
		return id
	}
	id := len(t.pieces)
	t.pieces = append(t.pieces, s)
	t.ids[s] = id
	return id
}

// byRune is a tokenizer that does nothing clever and breaks no rule.
func byRune(text string) []string {
	out := make([]string, 0, len(text))
	for _, r := range text {
		out = append(out, string(r))
	}
	return out
}

// byByte is the same thing one level down, which is where characters get cut in
// half.
func byByte(text string) []string {
	out := make([]string, 0, len(text))
	for i := range len(text) {
		out = append(out, text[i:i+1])
	}
	return out
}

// gatePages are three pages of Vietnamese carrying between them everything the
// suite needs to be able to run: marks, a run of digits, syllables to check the
// leading space against, and one page mixing Vietnamese with foreign words and
// with code.
var gatePages = []string{
	"Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc. Năm 2026 là năm thứ 40 của công cuộc đổi mới.",
	"Hà Nội có 30 quận huyện và dân số khoảng 8 triệu người, đứng thứ hai cả nước sau Thành phố Hồ Chí Minh.",
	"Máy chủ chạy framework nội bộ, cấu hình nằm trong /etc/gao/gao.conf với tham số workers = 8; đọc lúc khởi động.",
}

// feed runs a set of documents through a suite and closes it.
func feed(enc dem.Encoder, opts dem.GateOptions, texts ...string) dem.GateReport {
	g := dem.NewGates(enc, opts)
	for _, text := range texts {
		g.Add(doc.SumString(text), text)
	}
	return g.Report()
}

// gate picks one gate out of a report by the name doc 07 gives it.
func gate(t *testing.T, r dem.GateReport, name string) dem.Gate {
	t.Helper()
	for _, g := range r.Gates {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("%s is not in the report", name)
	return dem.Gate{}
}

// failed asserts that a gate caught something and named it.
func failed(t *testing.T, r dem.GateReport, name string) dem.Gate {
	t.Helper()
	g := gate(t, r, name)
	if g.Passed() {
		t.Fatalf("%s passed, and it should not have: %+v", name, g)
	}
	if g.Failed == 0 {
		t.Fatalf("%s reports no failures: %+v", name, g)
	}
	if len(g.Sample) == 0 {
		t.Errorf("%s failed %d times and named nothing", name, g.Failed)
	}
	return g
}

// passed asserts that a gate ran and found nothing.
func passed(t *testing.T, r dem.GateReport, name string) dem.Gate {
	t.Helper()
	g := gate(t, r, name)
	if !g.Ran {
		t.Fatalf("%s did not run: %s", name, g.Why)
	}
	if g.Failed != 0 {
		t.Fatalf("%s failed %d of %d %s, starting with %v", name, g.Failed, g.Checked, g.Unit, g.Sample)
	}
	return g
}

func TestATokenizerThatBreaksNoRulePassesTheGates(t *testing.T) {
	r := feed(newToy("runes", byRune), dem.GateOptions{}, gatePages...)

	// T9 is left out on purpose. It measures the machine the suite ran on, not
	// the suite, and a test that asserts a throughput asserts that the builder
	// is not busy. It has a test of its own below, in the direction that is
	// stable: a tokenizer that sleeps is slow anywhere.
	for _, name := range []string{"T1", "T2", "T3", "T4", "T5", "T6", "T7", "T8"} {
		passed(t, r, name)
	}
	if g := gate(t, r, "T10"); g.Passed() {
		t.Error("the audit reports itself as a pass, and an audit is a list for a person rather than a pass")
	}
}

func TestATokenizerThatLosesTheMarksFailsTheRoundTrip(t *testing.T) {
	// Decoding drops the tone off ộ, which is the tone loss failure this whole
	// project is built around, arriving through the tokenizer instead.
	lossy := newToy("lossy", byRune)
	lossy.garble = func(s string) string { return strings.ReplaceAll(s, "ộ", "ô") }

	r := feed(lossy, dem.GateOptions{}, gatePages...)

	g := failed(t, r, "T1")
	if g.Unit != "documents" {
		t.Errorf("T1 counts %s, and it is counted in documents", g.Unit)
	}
	// And the same tokenizer is fine on text with no marks on it, which is why
	// T2 is a separate gate rather than a subset of T1.
	passed(t, r, "T2")
}

// The undiacriticized slice is half of the Vietnamese web and it is its own
// gate because a tokenizer can be perfect on marked text and wrong on bare
// text, which is the direction that hides.
func TestTheBareSliceIsCheckedSeparately(t *testing.T) {
	bare := newToy("bare", byRune)
	bare.garble = func(s string) string { return strings.ReplaceAll(s, "cong", "công") }

	r := feed(bare, dem.GateOptions{}, gatePages...)

	passed(t, r, "T1")
	failed(t, r, "T2")
}

func TestSplittingACharacterInHalfIsCaught(t *testing.T) {
	r := feed(newToy("bytes", byByte), dem.GateOptions{}, gatePages...)

	t4 := failed(t, r, "T4")
	if t4.Unit != "boundaries" {
		t.Errorf("T4 counts %s, and one document holds thousands of them", t4.Unit)
	}
	// Every marked Vietnamese character is two or three bytes, so a byte
	// splitter parts a great many letters from their marks.
	t5 := failed(t, r, "T5")
	if t5.Failed > t4.Failed {
		t.Errorf("T5 found %d violations and T4 found %d, and every T5 violation of this kind is a T4 violation", t5.Failed, t4.Failed)
	}
	if !strings.Contains(strings.Join(t5.Sample, " "), "inside") {
		t.Errorf("T5 does not say which character was cut: %v", t5.Sample)
	}
}

// The other way a mark comes off is cleaner and harder to see: every boundary
// lands on a character, and one of those characters is a combining mark that
// belongs to the letter before it. T4 has nothing to say about that and T5 is
// the gate that does.
func TestACutBetweenALetterAndItsCombiningMarkIsCaught(t *testing.T) {
	decomposed := make([]string, len(gatePages))
	for i, p := range gatePages {
		decomposed[i] = norm.NFD.String(p)
	}

	r := feed(newToy("runes", byRune), dem.GateOptions{}, decomposed...)

	passed(t, r, "T4")
	g := failed(t, r, "T5")
	if !strings.Contains(strings.Join(g.Sample, " "), "its mark") {
		t.Errorf("T5 does not say what the boundary fell between: %v", g.Sample)
	}
}

// T6 is about the tokenizer and the sample can make it look like it is not. A
// corpus that is already NFC compares every document against itself and the
// gate passes without having been asked anything, so the report says that in
// as many words.
func TestNFCStabilityAndWhenItIsVacuous(t *testing.T) {
	clean := feed(newToy("runes", byRune), dem.GateOptions{}, gatePages...)
	g := passed(t, clean, "T6")
	if !strings.Contains(g.Note, "against itself") {
		t.Errorf("T6 passed on an all NFC sample without saying that it was vacuous: %q", g.Note)
	}

	decomposed := norm.NFD.String(gatePages[0])
	r := feed(newToy("runes", byRune), dem.GateOptions{}, decomposed)
	failed(t, r, "T6")

	// A tokenizer that normalizes first is stable by construction, which is the
	// only way to pass this gate rather than avoid it.
	first := newToy("nfc first", byRune)
	first.nfc = true
	ok := feed(first, dem.GateOptions{}, decomposed)
	g = passed(t, ok, "T6")
	if !strings.Contains(g.Note, "1 of 1 documents were not NFC") {
		t.Errorf("T6 does not say how much of the sample it was actually asked about: %q", g.Note)
	}
}

func TestDigitsThatGroupDifferentlyInDifferentCompanyAreCaught(t *testing.T) {
	// Pairs after a letter, singles elsewhere, which is the shape a byte level
	// merge table produces when the merges were learned from prose.
	context := newToy("digits by context", func(text string) []string {
		var out []string
		var prev rune
		for i, r := range text {
			if unicode.IsDigit(r) && unicode.IsLetter(prev) && i+1 < len(text) && unicode.IsDigit(rune(text[i+1])) {
				out = append(out, text[i:i+2])
				prev = 0
				continue
			}
			if len(out) > 0 && prev == 0 {
				prev = r
				continue
			}
			out = append(out, string(r))
			prev = r
		}
		return out
	})

	r := feed(context, dem.GateOptions{}, "Năm 2026 và năm2026 là hai cách viết cùng một con số 2026 trong tài liệu.")

	g := failed(t, r, "T7")
	if g.Unit != "digit runs" {
		t.Errorf("T7 counts %s, want digit runs", g.Unit)
	}
	if !strings.Contains(strings.Join(g.Sample, " "), "2026") {
		t.Errorf("T7 does not name the run that moved: %v", g.Sample)
	}
}

func TestTheLeadingSpaceHasToBeHandledTheSameWayEveryTime(t *testing.T) {
	// The space is folded into the syllable when the syllable opens with a
	// marked letter and left alone otherwise, so đổi is one token sequence at
	// the start of a line and another in the middle of one.
	sticky := newToy("sticky space", func(text string) []string {
		parts := byRune(text)
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == " " && len(parts[i+1]) > 1 {
				parts[i] += parts[i+1]
				parts = append(parts[:i+1], parts[i+2:]...)
			}
		}
		return parts
	})

	r := feed(sticky, dem.GateOptions{}, gatePages...)

	g := failed(t, r, "T8")
	if g.Unit != "syllables" {
		t.Errorf("T8 counts %s, want syllables", g.Unit)
	}
	if !strings.Contains(g.Note, "folded in") || !strings.Contains(g.Note, "its own token") {
		t.Errorf("T8 does not say what the two behaviors were: %q", g.Note)
	}

	// And a tokenizer that does one thing everywhere passes, whichever thing it
	// is. The gate is about uniformity and not about the choice.
	uniform := passed(t, feed(newToy("runes", byRune), dem.GateOptions{}, gatePages...), "T8")
	if strings.Count(uniform.Note, ",") != 0 {
		t.Errorf("a uniform tokenizer reported more than one behavior: %q", uniform.Note)
	}
}

func TestTheVocabularyAuditNamesPiecesGaoWouldHaveThrownAway(t *testing.T) {
	// A soft hyphen, a zero width joiner and a decomposed syllable, which is
	// what a vocabulary trained on uncleaned crawl carries: pieces reachable
	// only from text this project rejects.
	dirty := newToy("dirty", byRune, "\u00adso", "quan\u200dhe", norm.NFD.String("nghiêng"))

	r := feed(dirty, dem.GateOptions{}, gatePages...)

	g := gate(t, r, "T10")
	if g.Failed != 3 {
		t.Errorf("the audit flagged %d pieces, want the 3 that were seeded: %v", g.Failed, g.Sample)
	}
	if g.Passed() {
		t.Error("the audit called itself a pass")
	}
	if !strings.Contains(g.Note, "for a person to look at") {
		t.Errorf("the audit does not say what it wants: %q", g.Note)
	}
	// And a clean vocabulary is clean.
	if g := gate(t, feed(newToy("runes", byRune), dem.GateOptions{}, gatePages...), "T10"); g.Failed != 0 {
		t.Errorf("the audit flagged %d pieces of a vocabulary built from clean text: %v", g.Failed, g.Sample)
	}
}

// A gate with nothing to run on is the failure this suite is most likely to
// have in practice: somebody points it at a thousand documents, three gates
// find nothing to look at, and the run prints ten green lines.
func TestAGateWithNothingToRunOnIsNotAPass(t *testing.T) {
	r := feed(newToy("runes", byRune), dem.GateOptions{}, "Hôm nay trời đẹp.")

	g := gate(t, r, "T3")
	if g.Ran {
		t.Fatal("T3 ran on a sample with no mixed document in it")
	}
	if g.Passed() {
		t.Error("a gate that did not run reported itself as passed")
	}
	if !strings.Contains(g.Why, "mixed") {
		t.Errorf("T3 does not say why it could not run: %q", g.Why)
	}
	if r.Eligible() {
		t.Error("a tokenizer is eligible on a suite that did not finish")
	}
}

func TestFertilityIsReportedInBothUnits(t *testing.T) {
	r := feed(newToy("runes", byRune), dem.GateOptions{}, gatePages...)

	// One token per character, so the fertility of this tokenizer is one by
	// construction and the number can be checked rather than eyeballed.
	if got := r.CharsPerToken(); got != 1 {
		t.Errorf("a tokenizer of one token per character has fertility %.3f, want 1", got)
	}
	if r.Syllables == 0 {
		t.Fatal("no syllables were counted, so tokens per syllable is meaningless")
	}
	want := float64(r.Tokens) / float64(r.Syllables)
	if got := r.TokensPerSyllable(); got != want {
		t.Errorf("tokens per syllable is %.3f, want %.3f", got, want)
	}
	if r.Tokenizer != "runes" {
		t.Errorf("the report names %q as its tokenizer", r.Tokenizer)
	}
}

func TestTheSampleIsChosenByIdentitySoTwoRunsCheckTheSameDocuments(t *testing.T) {
	texts := make([]string, 40)
	for i := range texts {
		texts[i] = gatePages[i%len(gatePages)] + strings.Repeat(" thêm", i)
	}

	var first []string
	for range 2 {
		g := dem.NewGates(newToy("runes", byRune), dem.GateOptions{OneIn: 4})
		var kept []string
		for _, text := range texts {
			if g.Add(doc.SumString(text), text) {
				kept = append(kept, text)
			}
		}
		if first == nil {
			first = kept
			continue
		}
		if strings.Join(kept, "|") != strings.Join(first, "|") {
			t.Fatal("two runs over the same documents at the same rate checked different documents")
		}
	}
	if len(first) == 0 || len(first) == len(texts) {
		t.Fatalf("one in four kept %d of %d, which is not a sample", len(first), len(texts))
	}
}

func TestTheLimitStopsTheRun(t *testing.T) {
	g := dem.NewGates(newToy("runes", byRune), dem.GateOptions{Limit: 2})
	for _, text := range gatePages {
		g.Add(doc.SumString(text), text)
	}

	if !g.Full() {
		t.Error("the suite is not full after its limit")
	}
	if got := g.Report().Documents; got != 2 {
		t.Errorf("the run took %d documents against a limit of 2", got)
	}
}

func TestASlowTokenizerFailsTheThroughputGate(t *testing.T) {
	slow := newToy("slow", func(text string) []string {
		time.Sleep(20 * time.Millisecond)
		return byRune(text)
	})

	r := feed(slow, dem.GateOptions{}, gatePages...)

	g := failed(t, r, "T9")
	if !strings.Contains(g.Note, "MB/s on one core") {
		t.Errorf("T9 does not report what it measured: %q", g.Note)
	}
	if r.MBPerSecond >= dem.ThroughputFloor {
		t.Errorf("a tokenizer sleeping 20ms a document ran at %.1f MB/s", r.MBPerSecond)
	}
}

// The suite against the tokenizer it exists to judge. Three pages are not the
// ten million documents doc 07 asks for, so this is not the run that makes the
// tokenizer eligible. It is the run that catches a change in the model or the
// library before the long one is started.
func TestThePinnedTokenizerAgainstTheGates(t *testing.T) {
	r := feed(tokenizer(t), dem.GateOptions{}, gatePages...)

	for _, name := range []string{"T1", "T2", "T3", "T4", "T5"} {
		passed(t, r, name)
	}
	// T7 and T8 are reported rather than asserted, because what they say about
	// this tokenizer is a fact about somebody else's vocabulary and the useful
	// form of it is the note, not a boolean in this file.
	for _, name := range []string{"T7", "T8", "T10"} {
		g := gate(t, r, name)
		t.Logf("%s %s: %d of %d %s, %s", g.Name, g.Status(), g.Failed, g.Checked, g.Unit, g.Note)
	}
	if got := r.CharsPerToken(); got < 2 || got > 5 {
		t.Errorf("fertility is %.2f characters per token, which is outside anything plausible", got)
	}
	t.Logf("fertility %.2f chars/token, %.2f tokens/syllable, %.1f MB/s",
		r.CharsPerToken(), r.TokensPerSyllable(), r.MBPerSecond)
}

// The report closes three gates that cannot be settled per document. Doing that
// twice must not count anything twice, because a caller printing a report and
// then asking it a question is an ordinary thing to do.
func TestTheReportSaysTheSameThingTwice(t *testing.T) {
	g := dem.NewGates(newToy("runes", byRune), dem.GateOptions{})
	for _, text := range gatePages {
		g.Add(doc.SumString(text), text)
	}

	first, second := g.Report(), g.Report()
	for i := range first.Gates {
		a, b := first.Gates[i], second.Gates[i]
		if a.Checked != b.Checked || a.Failed != b.Failed || a.Status() != b.Status() {
			t.Errorf("%s reads %d/%d %s the first time and %d/%d %s the second",
				a.Name, a.Failed, a.Checked, a.Status(), b.Failed, b.Checked, b.Status())
		}
	}
}
