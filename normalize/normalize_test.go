package normalize

import (
	"reflect"
	"testing"
)

// The counts are the claim. A corpus that says a fifth of its documents were
// repaired and which repairs they were can be argued with, and one that says it
// was normalized cannot, so a count that is quietly wrong is worse here than a
// rule that does not fire.
func TestTheResultCountsWhatWasDone(t *testing.T) {
	got := Normalize("\u00f0oạn dduwowngj Hoà B\u200bình \u0007có xe.")

	for _, c := range []struct {
		what string
		got  int
		want int
	}{
		{"homoglyphs", got.Homoglyphs, 1},
		{"invisible characters", got.Invisible, 1},
		{"control characters", got.Controls, 1},
		{"tone marks", got.Tones, 1},
		{"residue", got.Residue, 1},
		{"syllables", got.Syllables, 6},
	} {
		if c.got != c.want {
			t.Errorf("counted %d %s, want %d", c.got, c.what, c.want)
		}
	}
	if want := "đoạn dduwowngj Hòa Bình có xe.\n"; got.Text != want {
		t.Errorf("Normalize = %q, want %q", got.Text, want)
	}
}

// Runes counts what arrived rather than what is left, because the question the
// control rate answers is whether the thing that arrived was text at all.
func TestRunesCountsTheDocumentThatArrived(t *testing.T) {
	got := Normalize("a\u0001b\u0002c")
	if got.Runes != 5 {
		t.Errorf("counted %d runes, want 5", got.Runes)
	}
	if got.Text != "abc\n" {
		t.Errorf("Normalize = %q, want %q", got.Text, "abc\n")
	}
	if want := 2.0 / 5.0; got.ControlRate() != want {
		t.Errorf("control rate %v, want %v", got.ControlRate(), want)
	}
}

func TestChangedIsTrueForOneByte(t *testing.T) {
	if got := Normalize("một hai"); !got.Changed {
		t.Error("adding the final newline is a change and the result says it is not")
	}
	if got := Normalize("một hai\n"); got.Changed {
		t.Error("nothing was done and the result says something was")
	}
}

// The tally is what a run over a shard reports, and the share of documents that
// changed at all is the one number this stage has a prediction riding on.
func TestTheTallyAddsUpARun(t *testing.T) {
	var got Tally
	for _, doc := range []string{
		"Hà Nội mùa này trời trở lạnh.\n", // nothing to do
		"Hoà bình.\n",       // one tone mark
		"ðường dài.\n",      // one homoglyph
		"Vi\u200bệt Nam.\n", // one zero width space
	} {
		got.Add(Normalize(doc))
	}

	want := Tally{
		Documents:  4,
		Changed:    3,
		Repaired:   3,
		Homoglyphs: 1,
		Invisible:  1,
		Tones:      1,
		Syllables:  13,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tally = %+v, want %+v", got, want)
	}
	if share := got.ChangedShare(); share != 0.75 {
		t.Errorf("changed share %v, want 0.75", share)
	}
}

// The first run over real parts came back saying normalization changed every
// document it saw, which is true and is a fact about the final newline rather
// than about Vietnamese. What the material is like is the other count.
func TestALayoutChangeIsNotARepair(t *testing.T) {
	var got Tally
	for _, doc := range []string{
		"Hà Nội mùa này trời trở lạnh.",     // no final newline, nothing else
		"Hà Nội mùa này   trời trở lạnh.\n", // a run of spaces
		"Hoà bình.\n", // one tone mark
	} {
		got.Add(Normalize(doc))
	}

	if got.Changed != 3 {
		t.Errorf("%d of the three documents changed, and all of them did", got.Changed)
	}
	if got.Repaired != 1 {
		t.Errorf("%d of them were repaired, and one was", got.Repaired)
	}
	if share := got.RepairedShare(); share > 0.34 || share < 0.33 {
		t.Errorf("repaired share %v, want a third", share)
	}
}

func TestAnEmptyTallyHasNoShare(t *testing.T) {
	var t0 Tally
	if got := t0.ChangedShare(); got != 0 {
		t.Errorf("changed share of nothing is %v, want 0", got)
	}
	if got := t0.RepairedShare(); got != 0 {
		t.Errorf("repaired share of nothing is %v, want 0", got)
	}
}

// Syllables are counted for the residue rate, so the count has to be the words a
// reader would count. Numbers on their own are not syllables and a word with a
// number stuck to it is one.
func TestSyllablesAreCountedTheWayAReaderWouldCountThem(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int
	}{
		{"Hà Nội", 2},
		{"Hà Nội, Việt Nam.", 4},
		{"Năm 1986", 1},
		{"1986", 0},
		{"mp3", 1},
		{"", 0},
		{"   ", 0},
		{"...", 0},
		{"một\nhai\nba", 3},
	} {
		if got := Normalize(tc.text); got.Syllables != tc.want {
			t.Errorf("Normalize(%q) counted %d syllables, want %d", tc.text, got.Syllables, tc.want)
		}
	}
}

// A document made of nothing this stage understands still has to come back
// whole. Vietnamese pages carry English, Chinese, code and mathematics, and none
// of it is the stage's business.
func TestTextThatIsNotVietnameseIsCarriedThrough(t *testing.T) {
	for _, s := range []string{
		"The quick brown fox jumps over the lazy dog.\n",
		"func main() { fmt.Println(\"hello\") }\n",
		"E = mc^2\n",
		"日本語のテキスト\n",
		"Пример текста на русском\n",
		"لغة عربية\n",
	} {
		if got := Normalize(s); got.Text != s {
			t.Errorf("Normalize(%q) = %q, want it unchanged", s, got.Text)
		}
	}
}
