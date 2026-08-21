package normalize

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

// The pairs are the whole point of the stage. Two spellings of one word are two
// documents to every hash downstream, so each of these has to come out on the
// side gao writes.
func TestTheToneMarkMovesToTheConventionGaoWrites(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"hoà", "hòa"},
		{"khoẻ", "khỏe"},
		{"loà", "lòa"},
		{"xoã", "xõa"},
		{"toạ", "tọa"},
		{"thuý", "thúy"},
		{"tuỳ", "tùy"},
		{"thuỷ", "thủy"},
		{"nguỵ", "ngụy"},
		{"oà", "òa"},
		{"uỷ", "ủy"},
		{"Thuỷ", "Thủy"},
		{"HOÀ", "HÒA"},
	} {
		got := Normalize(tc.in)
		if got.Text != tc.want+"\n" {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got.Text, tc.want)
		}
		if got.Tones != 1 {
			t.Errorf("Normalize(%q) moved %d tone marks, want 1", tc.in, got.Tones)
		}
	}
}

// A rule that fires where it should not is worse than one that does not fire.
// Every case here is a word that would be spelled wrong if the mark moved.
func TestTheToneMarkStaysWhereTheConventionDoesNotApply(t *testing.T) {
	for _, s := range []string{
		// A final consonant settles the placement, so there is no convention
		// left to canonicalize.
		"hoàn", "toàn", "khoét", "hoạt", "nguyệt", "chuyện",
		// The u of qu belongs to the onset, so the mark is already on the
		// nucleus and moving it would invent a word.
		"quý", "quỳ", "quỷ", "quỹ", "quỵ", "quả", "quà",
		// Nuclei of three letters have one placement and it is this one.
		"hoài", "ngoài", "khuya", "nguyễn", "chuyển",
		// Nothing to move.
		"hòa", "thủy", "ngụy", "một", "hai", "Việt Nam",
		// A syllable that is not Vietnamese at all.
		"the", "quick", "brown",
	} {
		got := Normalize(s)
		if got.Text != s+"\n" {
			t.Errorf("Normalize(%q) = %q, want it left alone", s, got.Text)
		}
		if got.Tones != 0 {
			t.Errorf("Normalize(%q) moved a tone mark", s)
		}
	}
}

// The mark moves within the syllable it is in and the rest of the sentence is
// not touched, which is what makes the count in the result readable.
func TestOnlyTheSyllablesThatNeedItAreChanged(t *testing.T) {
	got := Normalize("Hoà bình là điều thuỷ chung mà ai cũng muốn.")
	want := "Hòa bình là điều thủy chung mà ai cũng muốn.\n"
	if got.Text != want {
		t.Errorf("Normalize = %q, want %q", got.Text, want)
	}
	if got.Tones != 2 {
		t.Errorf("the sentence reports %d moved tone marks, want 2", got.Tones)
	}
	if !got.Changed {
		t.Error("the sentence changed and the result says it did not")
	}
}

// A syllable arrives fully precomposed, partly composed, or fully decomposed,
// and all three are the same word. Everything downstream hashes these bytes.
func TestEveryCompositionOfASyllableComesOutTheSame(t *testing.T) {
	const want = "ti\u1ebfng Vi\u1ec7t"
	for _, tc := range []struct{ name, in string }{
		{"precomposed", "ti\u1ebfng Vi\u1ec7t"},
		{"partly composed", "ti\u00ea\u0301ng Vi\u00ea\u0323t"},
		{"decomposed", "tie\u0302\u0301ng Vie\u0302\u0323t"},
		{"marks out of canonical order", "tie\u0301\u0302ng Vie\u0302\u0323t"},
	} {
		got := Normalize(tc.in)
		if got.Text != want+"\n" {
			t.Errorf("%s: Normalize(%q) = %q, want %q", tc.name, tc.in, got.Text, want)
		}
	}
}

func TestComposingASyllableIsCountedSoTheSourceCanBeIdentified(t *testing.T) {
	got := Normalize("tie\u0302\u0301ng Vie\u0302\u0323t nam")
	if got.Composed != 2 {
		t.Errorf("two decomposed syllables came out as %d", got.Composed)
	}
	if got.Tones != 0 {
		t.Error("composing a syllable was counted as moving a tone mark")
	}
}

// A syllable that arrives decomposed and in the other convention has both things
// done to it, in that order, because the tone rule reads letters and a
// decomposed syllable does not have them yet.
func TestASyllableIsComposedBeforeItsToneMarkIsRead(t *testing.T) {
	got := Normalize("hoa\u0300")
	if got.Text != "h\u00f2a\n" {
		t.Errorf("Normalize = %q, want %q", got.Text, "h\u00f2a")
	}
	if got.Composed != 1 || got.Tones != 1 {
		t.Errorf("composed %d and moved %d, want one of each", got.Composed, got.Tones)
	}
}

// Two tone marks on one syllable is damage rather than a convention, and this
// stage does not repair damage it would have to guess about.
func TestASyllableWithTwoToneMarksIsLeftAlone(t *testing.T) {
	in := "ho\u00e0\u0301"
	if got, moved := retone(in); moved || got != in {
		t.Errorf("retone(%q) = %q, %v, want it left alone", in, got, moved)
	}
}

func TestALetterThatIsNotVietnameseKeepsItsMarks(t *testing.T) {
	// U+0303 is the Vietnamese ngã and it is also the tilde of ȭ, and the two
	// are the same codepoint. Decomposing ȭ hands split a tilde and a macron,
	// split calls the tilde a tone because that is what a tilde is here, and
	// moving it past the macron gives back o with a macron and a loose tilde
	// beside it. That is a different letter, and gao has no business writing it.
	//
	// 91 syllables in one 310MB WARC volume off a live crawl went through that
	// door. It is rare and it is real, and a corpus that is quietly wrong about
	// 91 letters is a corpus nobody finds out is wrong.
	for _, in := range []string{"ȭ", "Ȭ", "THȬ", "iȬJ"} {
		if got := compose(in); got != in {
			t.Errorf("compose(%q) = %q, and %q is a letter of somebody else's language", in, got, in)
		}
	}
}

func TestSettledIsOnlyClaimedForSyllablesComposeWouldNotTouch(t *testing.T) {
	// For Vietnamese the fast path only skips work that would have changed
	// nothing, and the long way round is the check. The one letter that is not
	// true of is the one in the test above, which is a letter gao should never
	// have been rewriting, and it is left out of this list on purpose.
	for _, in := range []string{"hoa", "h\u00f2a", "ti\u1ebfng", "Vi\u1ec7t", "abc"} {
		if !settled(in) {
			t.Errorf("settled(%q) is false and this syllable has no loose marks", in)
			continue
		}
		if got := slow(in); got != in {
			t.Errorf("settled(%q) is true but the long way round gives %q", in, got)
		}
	}

	// A syllable whose marks are still loose has to be seen as loose, or the fast
	// path is skipping the work it exists to allow. These are written as escapes
	// because a decomposed syllable and a precomposed one look the same on the
	// screen, which is most of the reason this stage exists at all.
	loose := []string{
		"ho\u0061\u0300",         // hoa with the grave still sitting on its own
		"e\u0301\u0302",          // e with the acute typed ahead of the circumflex
		"ti\u0065\u0302\u0301ng", // tieng decomposed the whole way down
	}
	for _, in := range loose {
		if settled(in) {
			t.Errorf("settled(%q) is true and this syllable carries a loose mark", in)
		}
	}
}

// slow is compose without the fast path, which is what the test above compares
// against. It is here rather than in the package because the package has no use
// for it.
func slow(s string) string {
	d := norm.NFD.String(s)
	if units, ok := split(d); ok {
		d = join(units)
	}
	return norm.NFC.String(d)
}
