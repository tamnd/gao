package phoi

import "testing"

func TestTelexKeystrokesAreFlagged(t *testing.T) {
	for _, w := range []string{
		"dduwowngj", // đường
		"dduwowcj",  // được
		"nguwowif",  // người
		"muoonj",    // muộn
		"ddaauf",    // đầu
		"chuyeenj",  // chuyện
	} {
		if !Residue(w) {
			t.Errorf("Residue(%q) = false, want it flagged", w)
		}
	}
}

func TestVniKeystrokesAreFlagged(t *testing.T) {
	for _, w := range []string{
		"d9u7o7ng2", // đường
		"nguo7i72",  // người
		"tie61ng",   // tiếng
		"vie65t",    // việt
		"ba81c",     // bắc
	} {
		if !Residue(w) {
			t.Errorf("Residue(%q) = false, want it flagged", w)
		}
	}
}

// A word flagged here is a word counted against the document, and enough of them
// throw the document away. Every one of these is something a good document
// contains, so each is a document the pipeline would have lost.
func TestOrdinaryWordsAreNotFlagged(t *testing.T) {
	for _, w := range []string{
		// Vietnamese that came through the input method working.
		"đường", "được", "người", "muộn", "tiếng", "Việt", "chuyện",
		// Vietnamese written without its diacritics, which is a document
		// somebody typed in a hurry rather than keystrokes that leaked.
		"duong", "nguoi", "tieng", "khong", "viet",
		// English ending in a Telex tone key.
		"goodness", "address", "shadows", "flows", "sees", "trees",
		"half", "chief", "brief", "beef", "roof", "proof", "staff",
		"under", "over", "after", "cheer", "door", "floor",
		"box", "fix", "index", "complex",
		// Codes and names with a digit in them, which VNI residue also has.
		"b2b", "p2p", "H2O", "mp3", "covid19", "IPv6", "3D", "A4",
		// Ordinary Vietnamese with a number attached the way people write it.
		"Covid", "SARS", "km2",
	} {
		if Residue(w) {
			t.Errorf("Residue(%q) = true, want it left alone", w)
		}
	}
}

// The doubled d is the only way to type đ and it is not how anything else is
// spelled, so it is the one keystroke the detector trusts on its own.
// What the detector does not catch, written down so that nobody has to rediscover
// it. Each of these is residue and each is a word whose shape an English word
// also has, so the rule that caught it would throw good documents away. The
// documents these come from are caught anyway, because a document typed with the
// input method off has the long syllables in it too.
func TestTheWordsTheDetectorDeliberatelyMisses(t *testing.T) {
	for _, w := range []string{
		"veef",    // về, and "beef"
		"lamf",    // làm
		"minhf",   // mình
		"khoongr", // khổng, one letter keystroke and a tone key
		"nguoi72", // người, where both digits are on the end
	} {
		if Residue(w) {
			t.Errorf("Residue(%q) = true, which is right, so this test is now the wrong test", w)
		}
	}
}

func TestTheDoubledDIsEnoughOnItsOwn(t *testing.T) {
	if !Residue("ddungj") {
		t.Error("a word starting dd and ending in a tone key was not flagged")
	}
	if Residue("ddung") {
		t.Error("a word starting dd with no tone key on the end was flagged")
	}
}

// j does not appear in Vietnamese and does not end an English word, so it is
// the strongest single marker there is.
func TestAWordEndingInJIsFlagged(t *testing.T) {
	if !Residue("hoocj") {
		t.Error("a word ending in j was not flagged")
	}
	if Residue("hoc") {
		t.Error("the same word without the tone key was flagged")
	}
}

// A digit at the end of a word is a version, a year or a model number. A digit
// with letters on both sides of it is the shape VNI leaves, and even that is not
// enough on its own.
func TestVniNeedsMoreThanOneDigitInsideAWord(t *testing.T) {
	for _, tc := range []struct {
		word string
		want bool
	}{
		{"nguo7i72", true},
		{"nguoi2", false},    // one digit, and it is on the end
		{"ngu7oi", false},    // one digit, inside, but only one
		{"windows10", false}, // two digits, neither of them inside
		{"Win32API", false},  // two digits inside, neither after a vowel
		{"utf8mb4", false},   // the same shape, and it is a character set
		{"a1b2c3", true},
	} {
		if got := Residue(tc.word); got != tc.want {
			t.Errorf("Residue(%q) = %v, want %v", tc.word, got, tc.want)
		}
	}
}

// Residue is counted over a document and the share is what the filter reads, so
// the counting has to survive punctuation and numbers sitting next to words.
func TestResidueIsCountedOverADocument(t *testing.T) {
	got := Normalize("Chiều nay đi lamf veef dduwowngj Nguyễn Trãi, muoonj quá.")
	if got.Residue != 2 {
		t.Errorf("counted %d syllables of residue, want 2", got.Residue)
	}
	if got.Syllables != 10 {
		t.Errorf("counted %d syllables, want 10", got.Syllables)
	}
	if rate := got.ResidueRate(); rate <= ResidueLimit {
		t.Errorf("residue rate %v, want it over the limit", rate)
	}
}

func TestADocumentWithNoResidueHasARateOfZero(t *testing.T) {
	got := Normalize("Hà Nội mùa này trời trở lạnh.")
	if got.Residue != 0 {
		t.Errorf("counted %d syllables of residue, want 0", got.Residue)
	}
	if got.ResidueRate() != 0 {
		t.Errorf("residue rate %v, want 0", got.ResidueRate())
	}
}

func TestAnEmptyDocumentHasNoRates(t *testing.T) {
	got := Normalize("")
	if got.ResidueRate() != 0 || got.ControlRate() != 0 {
		t.Errorf("empty document reports rates %v and %v, want both 0", got.ResidueRate(), got.ControlRate())
	}
}
