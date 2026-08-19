package pick

import (
	"strings"
	"testing"
)

// Three benchmark items long enough to have windows, and one that is not.
const (
	capital = "Thủ đô của nước Việt Nam là thành phố nào sau đây trong bốn lựa chọn"
	delta   = "Diện tích của đồng bằng sông Cửu Long lớn hơn diện tích của đồng bằng sông Hồng bao nhiêu lần"
	novel   = "Nhân vật chính trong tác phẩm Chí Phèo của nhà văn Nam Cao là người như thế nào"

	principle = "Nguyên lý cơ bản của chủ nghĩa duy vật biện chứng được trình bày trong tác phẩm nào"

	short = "Hà Nội là thủ đô"
)

// Prose that shares no window with any of them.
const (
	morning = "Buổi sáng hôm ấy trời trở lạnh và những người bán hàng rong đi ngang qua con phố nhỏ."
	market  = "Chợ nổi họp từ lúc bốn giờ sáng cho đến khi mặt trời lên cao trên mặt sông rộng."
)

// run of the first n syllables of a text, which is how a document is given an
// exact number of shared windows.
func run(text string, n int) string {
	s := strings.Fields(text)
	if n > len(s) {
		n = len(s)
	}
	return strings.Join(s[:n], " ")
}

// document puts a fragment between two paragraphs that match nothing, so that
// the only windows it shares are the ones the fragment carries.
func document(fragment string) string {
	return morning + "\n\n" + fragment + "\n\n" + market
}

func bench(name string, items ...string) Benchmark {
	return Benchmark{Entry: Entry{Name: name, Version: "1", Origin: Native}, Items: items}
}

func heldOut(name string, items ...string) Benchmark {
	b := bench(name, items...)
	b.HeldOut = true
	return b
}

func index(t *testing.T, benchmarks ...Benchmark) *Index {
	t.Helper()
	x, err := NewIndex(List{Version: "test", Benchmarks: benchmarks})
	if err != nil {
		t.Fatalf("building the index: %v", err)
	}
	return x
}

func touch(t *testing.T, r Result, name string) Touch {
	t.Helper()
	for _, b := range r.Benchmarks {
		if b.Benchmark == name {
			return b
		}
	}
	t.Fatalf("nothing was reported for %s, and the result carries %d benchmarks", name, len(r.Benchmarks))
	return Touch{}
}

func TestADocumentCarryingATestItemIsFoundAndDropped(t *testing.T) {
	x := index(t, bench("vmlu", capital, delta))
	r := x.Check(document(capital))
	if !r.Flagged() {
		t.Fatal("a document holding a whole test item was not flagged")
	}
	if !r.Dropped() {
		t.Error("a document holding a whole test item was flagged and kept")
	}
	if got := touch(t, r, "vmlu").Items; got != 1 {
		t.Errorf("the overlap came from %d items, want 1", got)
	}
}

func TestADocumentThatSharesNothingIsClean(t *testing.T) {
	x := index(t, bench("vmlu", capital, delta))
	r := x.Check(document(novel))
	if r.Flagged() {
		t.Errorf("a document with no benchmark text in it was flagged on %d windows", r.Hits)
	}
	if r.Grams == 0 {
		t.Error("the document reported no windows at all, so nothing was checked")
	}
	if len(r.Benchmarks) != 0 {
		t.Errorf("a clean document named %d benchmarks", len(r.Benchmarks))
	}
}

func TestOneSharedWindowIsReportedAndNotDropped(t *testing.T) {
	x := index(t, bench("vmlu", capital))
	r := x.Check(document(run(capital, GramSize)))
	if !r.Flagged() {
		t.Fatal("a document sharing a window with a test item was not flagged")
	}
	if got := touch(t, r, "vmlu").Grams; got != 1 {
		t.Fatalf("the document shared %d windows, want 1", got)
	}
	if r.Dropped() {
		t.Error("one shared window dropped the document, which makes an ordinary phrase enough to lose a page")
	}
}

func TestThreeSharedWindowsAreEnoughToDrop(t *testing.T) {
	x := index(t, bench("vmlu", capital))
	r := x.Check(document(run(capital, GramSize+DropAt-1)))
	if got := touch(t, r, "vmlu").Grams; got != DropAt {
		t.Fatalf("the document shared %d windows, want %d", got, DropAt)
	}
	if !r.Dropped() {
		t.Error("a run of fifteen syllables from a test item did not drop the document")
	}
}

func TestAHeldOutBenchmarkDropsOnOneWindow(t *testing.T) {
	x := index(t, heldOut("vi-cloze", capital))
	r := x.Check(document(run(capital, GramSize)))
	if got := touch(t, r, "vi-cloze").Grams; got != 1 {
		t.Fatalf("the document shared %d windows, want 1", got)
	}
	if !r.Dropped() {
		t.Error("a document overlapping a held out benchmark was kept, which leaves the hold out unheld")
	}
}

func TestAnItemShorterThanTheWindowIsNotIndexed(t *testing.T) {
	x := index(t, bench("vmlu", short, capital))
	if r := x.Check(document(short)); r.Flagged() {
		t.Error("a five syllable phrase was indexed, so every page that says it is now contaminated")
	}
	// The long item on the same benchmark is still there.
	if r := x.Check(document(capital)); !r.Dropped() {
		t.Error("the long item on the same benchmark stopped being found")
	}
}

func TestTheItemIsFoundThroughTheThingsARepublisherChanges(t *testing.T) {
	x := index(t, bench("vmlu", capital))
	// Curly quotes around it, capitals, and the punctuation moved, none of which
	// is the document being a different document.
	messy := "“" + strings.ToUpper(capital) + "”, theo đề thi năm ngoái."
	r := x.Check(document(messy))
	if !r.Dropped() {
		t.Errorf("the same item in capitals and curly quotes was not found, %d windows shared", r.Hits)
	}
}

func TestTheItemIsFoundThroughTheIAndYSpelling(t *testing.T) {
	// Both spellings are correct under different orthographic positions and a
	// republisher picks one. A check that could be defeated by lí for lý would
	// be a check against careful copying only.
	x := index(t, bench("vmlu", principle))
	r := x.Check(document(strings.ReplaceAll(principle, "lý", "lí")))
	if !r.Dropped() {
		t.Errorf("the same item spelled lí rather than lý was not found, %d windows shared", r.Hits)
	}
}

func TestADocumentThatRepeatsALineDoesNotCrossTheThresholdOnIt(t *testing.T) {
	x := index(t, bench("vmlu", capital))
	one := run(capital, GramSize)
	repeated := document(one + "\n\n" + one + "\n\n" + one)
	r := x.Check(repeated)
	if got := touch(t, r, "vmlu").Grams; got != 1 {
		t.Fatalf("the same window counted %d times, want 1", got)
	}
	if r.Dropped() {
		t.Error("a page that repeats one shared phrase three times was dropped, which is one overlap counted three times")
	}
}

func TestAWindowTwoBenchmarksShareIsAttributedToBoth(t *testing.T) {
	x := index(t, bench("vmlu", capital), bench("mmlu-vi", capital))
	r := x.Check(document(capital))
	if len(r.Benchmarks) != 2 {
		t.Fatalf("the overlap was attributed to %d benchmarks, want 2", len(r.Benchmarks))
	}
	for _, b := range r.Benchmarks {
		if !b.Dropped {
			t.Errorf("%s did not drop the document", b.Benchmark)
		}
	}
}

func TestOneWindowFromEachOfThreeBenchmarksIsNotADrop(t *testing.T) {
	x := index(t, bench("vmlu", capital), bench("vinli", delta), bench("vimmrc", novel))
	body := document(run(capital, GramSize) + "\n\n" + run(delta, GramSize) + "\n\n" + run(novel, GramSize))
	r := x.Check(body)
	if r.Hits != 3 {
		t.Fatalf("the document shared %d windows, want 3", r.Hits)
	}
	if r.Dropped() {
		t.Error("three coincidences on three unrelated benchmarks were treated as a leak")
	}
}

func TestShareIsTakenOverTheWindowsTheDocumentHas(t *testing.T) {
	x := index(t, bench("vmlu", capital))
	r := x.Check(document(capital))
	if r.Grams <= r.Hits {
		t.Fatalf("the document reported %d windows and %d hits, so the surrounding prose was not counted", r.Grams, r.Hits)
	}
	if s := r.Share(); s <= 0 || s >= 1 {
		t.Errorf("the share of windows in the list is %v, and this document is part benchmark and part prose", s)
	}
	if s := (Result{}).Share(); s != 0 {
		t.Errorf("a document with no windows has a share of %v, want 0", s)
	}
}

func TestTheReportCarriesTheBenchmarksNothingTouched(t *testing.T) {
	x := index(t, bench("vmlu", capital), bench("vinli", delta))
	tally := NewTally(x)
	for _, text := range []string{document(capital), document(morning), document(market)} {
		tally.Add(x, text, x.Check(text))
	}
	if len(tally.Benchmarks) != 2 {
		t.Fatalf("the report holds %d rows, want one per benchmark", len(tally.Benchmarks))
	}
	var clean BenchmarkReport
	for _, b := range tally.Benchmarks {
		if b.Benchmark == "vinli" {
			clean = b
		}
	}
	if clean.Benchmark == "" {
		t.Fatal("the benchmark nothing touched is not in the report, so it cannot be read as clean")
	}
	if clean.Documents != 0 || clean.ItemsTouched != 0 {
		t.Errorf("the clean benchmark reports %d documents and %d items touched", clean.Documents, clean.ItemsTouched)
	}
	if clean.Items != 1 {
		t.Errorf("the clean benchmark reports %d items, and a count of nothing found means nothing without one", clean.Items)
	}
	if !tally.Contaminated() {
		t.Error("a run that found a whole test item in the corpus reported no contamination")
	}
	if tally.Documents != 3 || tally.Flagged != 1 || tally.Dropped != 1 {
		t.Errorf("the run reports %d documents, %d flagged, %d dropped, want 3, 1, 1", tally.Documents, tally.Flagged, tally.Dropped)
	}
}

func TestItemsTouchedCountsItemsRatherThanDocuments(t *testing.T) {
	x := index(t, bench("vmlu", capital, delta, novel))
	tally := NewTally(x)
	// One item on three pages is not the same problem as three items on one
	// page, and the count of items is what tells them apart.
	for _, text := range []string{document(capital), document(capital), document(capital)} {
		tally.Add(x, text, x.Check(text))
	}
	got := tally.Benchmarks[0]
	if got.Documents != 3 {
		t.Errorf("the benchmark was found in %d documents, want 3", got.Documents)
	}
	if got.ItemsTouched != 1 {
		t.Errorf("%d of the benchmark's items were found, want 1", got.ItemsTouched)
	}
	if share := got.ItemShare(); share > 0.34 {
		t.Errorf("one of three items is reported as %v of the benchmark", share)
	}
	if share := (BenchmarkReport{}).ItemShare(); share != 0 {
		t.Errorf("a benchmark with no items reports a share of %v, want 0", share)
	}
}

func TestACleanRunSaysSo(t *testing.T) {
	x := index(t, bench("vmlu", capital))
	tally := NewTally(x)
	for _, text := range []string{document(morning), document(novel)} {
		tally.Add(x, text, x.Check(text))
	}
	if tally.Contaminated() {
		t.Error("a run that found nothing reported contamination")
	}
	if tally.Flagged != 0 {
		t.Errorf("%d documents were flagged in a run that found nothing", tally.Flagged)
	}
}

func TestTheIndexReportsWhatIsInIt(t *testing.T) {
	x := index(t, bench("vmlu", capital), bench("vinli", delta))
	if x.Grams() == 0 {
		t.Fatal("the index holds no windows")
	}
	if got := x.List().Items(); got != 2 {
		t.Errorf("the list holds %d items, want 2", got)
	}
	names := x.List().Names()
	if len(names) != 2 || names[0] != "vinli" || names[1] != "vmlu" {
		t.Errorf("the names came back as %v, want them sorted", names)
	}
}

func TestGramsAreWindowsAndNotSyllables(t *testing.T) {
	n := 0
	for range Grams(capital) {
		n++
	}
	want := len(strings.Fields(capital)) - GramSize + 1
	if n != want {
		t.Errorf("a %d syllable text produced %d windows, want %d", len(strings.Fields(capital)), n, want)
	}
	for range Grams(short) {
		t.Fatal("a text shorter than the window produced a window")
	}
}

func TestTwoDifferentSplitsOfTheSameLettersAreDifferentWindows(t *testing.T) {
	a := hash(strings.Fields("mot hai ba bon nam sau bay tam chin muoi mot hai ba"))
	b := hash(strings.Fields("mot hai ba bon nam sau bay tam chin muoi mot haiba"))
	if a == b {
		t.Error("the space between syllables is not hashed, so two different texts are one window")
	}
}

func TestAnIndexOverAnEmptyListIsRefused(t *testing.T) {
	if _, err := NewIndex(List{Version: "test"}); err == nil {
		t.Error("an index was built over no benchmarks, and a run against it would report a clean corpus")
	}
}
