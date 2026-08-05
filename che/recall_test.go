package che

import (
	"sort"
	"strings"
	"testing"
)

// The whole measurement, printed whether it passes or not, because the number
// is the deliverable and a number that only appears on a failure is a number
// nobody reads.
func TestTheRecallOfEachDetector(t *testing.T) {
	set, err := Labeled()
	if err != nil {
		t.Fatal(err)
	}
	s := Measure(set)

	// The floors are what was measured, minus nothing. They are not targets and
	// they are not aspirations: a detector that drops below the number it was
	// shipped at has regressed, and the test says so with the span it lost.
	floors := map[Kind]struct{ recall, precision float64 }{
		KindEmail:   {0.83, 1.00},
		KindPhone:   {1.00, 1.00},
		KindCCCD:    {1.00, 1.00},
		KindCMND:    {0.66, 1.00},
		KindTax:     {1.00, 1.00},
		KindPlate:   {1.00, 1.00},
		KindName:    {1.00, 1.00},
		KindAddress: {0.87, 1.00},
	}

	t.Logf("%d documents, %d marked spans, %d covered", s.Documents, s.Total.Gold, s.Total.Hit)
	for _, k := range Kinds() {
		c := s.ByKind[k]
		t.Logf("%-8s marked %2d, covered %2d, recall %.3f, found %2d, precision %.3f",
			k, c.Gold, c.Hit, c.Recall(), c.Found, c.Precision())
		floor := floors[k]
		if c.Gold == 0 {
			t.Errorf("nothing in the labeled set is marked %s, so its recall is not measured at all", k)
			continue
		}
		if c.Recall() < floor.recall {
			t.Errorf("%s recall is %.3f and it was %.3f", k, c.Recall(), floor.recall)
		}
		if c.Precision() < floor.precision {
			t.Errorf("%s precision is %.3f and it was %.3f", k, c.Precision(), floor.precision)
		}
	}
	for _, m := range s.Missed {
		t.Logf("not covered: %s in %s, %q", m.Span.Kind, m.Document, m.Span.Text)
	}
	for _, k := range Kinds() {
		for _, m := range s.Spurious[k] {
			t.Logf("nothing marked: %s in %s, %q", k, m.Document, m.Span.Text)
		}
	}
}

// The three spans nothing covers, named. A measurement that reports a recall
// under one and does not say which spans it lost is a measurement nobody can
// act on, and each of these is a class rather than an accident.
func TestWhatTheDetectorsDoNotFind(t *testing.T) {
	set, err := Labeled()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"quancafe.sangnhuong (a) gmail.com": "an email address written to get past a site's own filter, which is how they are written in classified listings",
		"031947265":                         "an old national ID with nothing in the text naming it, which nine digits alone cannot be told from a price",
		"thôn Đông Trại, xã Nghĩa Trụ, huyện Văn Giang, Hưng Yên": "a rural address with no house number to open the chain",
	}

	got := map[string]bool{}
	for _, m := range Measure(set).Missed {
		got[m.Span.Text] = true
		if _, known := want[m.Span.Text]; !known {
			t.Errorf("%s in %s is not covered and is not one of the known gaps: %q", m.Span.Kind, m.Document, m.Span.Text)
		}
	}
	for text, why := range want {
		if !got[text] {
			t.Logf("this is now covered and the list of known gaps should lose it: %q, %s", text, why)
		}
	}
}

// A document with nothing marked in it is the other half of the set and the
// half that is easy to leave out. Every one of them is full of the digit runs a
// Vietnamese page is full of, and a detector that fires on any of them is
// putting holes in the middle of ordinary sentences.
func TestTheDocumentsWithNothingInThem(t *testing.T) {
	set, err := Labeled()
	if err != nil {
		t.Fatal(err)
	}
	empty := 0
	for _, m := range set {
		if len(m.Gold) > 0 {
			continue
		}
		empty++
		if found := Find(m.Text); len(found) > 0 {
			for _, s := range found {
				t.Errorf("%s: %s covered %q and there is no personal data in that document", m.Name, s.Kind, s.Text)
			}
		}
	}
	if empty < 2 {
		t.Errorf("the labeled set holds %d documents with nothing marked in them, and precision is not measured without them", empty)
	}
}

// Every kind this package produces has to appear in the set. A detector nobody
// wrote a document for reports a recall of zero over zero, which reads as
// nothing at all.
func TestEveryDetectorIsInTheLabeledSet(t *testing.T) {
	set, err := Labeled()
	if err != nil {
		t.Fatal(err)
	}
	s := Measure(set)
	for _, k := range Kinds() {
		if s.ByKind[k].Gold == 0 {
			t.Errorf("no document in the labeled set holds a %s", k)
		}
	}
}

// The marks come out of the text exactly, and the offsets they leave behind
// point at what was marked. This is the test that keeps the measurement honest
// at its cheapest point: a parser that dropped a byte would move every offset
// after it and turn hits into misses that nobody could explain.
func TestTheMarksComeOutOfTheTextCleanly(t *testing.T) {
	m, err := ParseMarked("test", "Gọi {{phone:0912 345 678}} hoặc {{email:a@b.vn}} nhé.")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Gọi 0912 345 678 hoặc a@b.vn nhé."; m.Text != want {
		t.Fatalf("the text came out as %q, want %q", m.Text, want)
	}
	if len(m.Gold) != 2 {
		t.Fatalf("%d spans came out, want 2", len(m.Gold))
	}
	for _, s := range m.Gold {
		if got := m.Text[s.Start:s.End]; got != s.Text {
			t.Errorf("the %s span points at %q and holds %q", s.Kind, got, s.Text)
		}
	}
	if strings.Contains(m.Text, markOpen) || strings.Contains(m.Text, markClose) {
		t.Errorf("a mark survived into the text: %q", m.Text)
	}
}

// A labeled set that loses a label reports a recall of one on a detector that
// found nothing, so every way of writing a mark wrong is an error rather than a
// span quietly dropped.
func TestAMarkThatIsWrongIsAnError(t *testing.T) {
	for _, c := range []struct{ name, text, want string }{
		{"no kind", "gọi {{0912 345 678}} nhé", "does not say what kind"},
		{"unknown kind", "gọi {{telephone:0912 345 678}} nhé", "not a kind"},
		{"nothing in it", "gọi {{phone:}} nhé", "no text in it"},
		{"never closes", "gọi {{phone:0912 345 678 nhé", "never closes"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseMarked("test", c.text)
			if err == nil {
				t.Fatal("the mark was accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the error reads %q and does not say %q", err, c.want)
			}
		})
	}
}

// The set has to hold more than one shape of document, since a recall measured
// on ten copies of a contact block is a recall on contact blocks.
func TestTheLabeledSetIsNotAllOneShapeOfDocument(t *testing.T) {
	set, err := Labeled()
	if err != nil {
		t.Fatal(err)
	}
	if len(set) < 8 {
		t.Errorf("the labeled set holds %d documents", len(set))
	}
	names := make([]string, 0, len(set))
	for _, m := range set {
		names = append(names, m.Name)
		if len(strings.Fields(m.Text)) < 60 {
			t.Errorf("%s holds %d tokens, which is a fragment rather than a page", m.Name, len(strings.Fields(m.Text)))
		}
	}
	sort.Strings(names)
	t.Logf("the labeled set: %s", strings.Join(names, ", "))
}
