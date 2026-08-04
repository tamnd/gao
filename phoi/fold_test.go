package phoi

import "testing"

// The pairs the key exists for. Each of these is one word with two accepted
// spellings, and each pair is two documents to a deduplicator that hashes the
// text as it stands.
func TestTheISpellingAndTheYSpellingShareAKey(t *testing.T) {
	for _, tc := range [][2]string{
		{"Mỹ", "Mĩ"},
		{"kỹ", "kĩ"},
		{"lý", "lí"},
		{"quý", "quí"},
		{"hy vọng", "hi vọng"},
		{"vật lý", "vật lí"},
		{"kỹ thuật", "kĩ thuật"},
		{"bác sỹ", "bác sĩ"},
		{"công ty trách nhiệm hữu hạn", "công ty trách nhiệm hữu hạn"},
	} {
		if !Folded(tc[0], tc[1]) {
			t.Errorf("Fold(%q) = %q and Fold(%q) = %q, want the same key",
				tc[0], Fold(tc[0]), tc[1], Fold(tc[1]))
		}
	}
}

// Words that are not a pair have to keep their own keys, or deduplication starts
// throwing away documents that are different.
func TestWordsThatAreNotAPairKeepTheirOwnKeys(t *testing.T) {
	for _, tc := range [][2]string{
		{"lý", "lì"},
		{"kỹ", "kỳ"},
		{"Mỹ", "mỹ"},
		{"hòa", "hoa"},
		// A y with a vowel beside it is half of a sound rather than a whole
		// one, and the i in its place is a different word. Folding these would
		// lose the second of every pair.
		{"tay", "tai"},
		{"hay", "hai"},
		{"máy", "mái"},
		{"thúy", "thúi"},
		{"tùy", "tùi"},
		{"yêu", "iêu"},
		{"Nguyễn", "Nguiễn"},
		{"chuyện", "chuiện"},
	} {
		if Folded(tc[0], tc[1]) {
			t.Errorf("Fold(%q) and Fold(%q) came out the same, want different keys", tc[0], tc[1])
		}
	}
}

// The tone mark rides on the letter, so folding y to i has to carry it across
// rather than drop it. This is the whole reason the fold decomposes first.
func TestTheToneMarkSurvivesTheFold(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Mỹ", "Mĩ"},
		{"lý", "lí"},
		{"kỳ", "kì"},
		{"tỷ", "tỉ"},
		{"ỵ", "ị"},
		{"y", "i"},
		{"Y", "I"},
	} {
		if got := Fold(tc.in); got != tc.want {
			t.Errorf("Fold(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The u of qu is part of the consonant, so the y after it is the whole vowel and
// quý and quí are the pair they look like. Every other u before a y is a vowel
// and the pair is not there.
func TestTheUOfQuIsNotAVowelForThisPurpose(t *testing.T) {
	for _, tc := range [][2]string{
		{"quý", "quí"},
		{"quỳ", "quì"},
		{"quỹ", "quĩ"},
		{"Quý", "Quí"},
	} {
		if !Folded(tc[0], tc[1]) {
			t.Errorf("Fold(%q) = %q and Fold(%q) = %q, want the same key",
				tc[0], Fold(tc[0]), tc[1], Fold(tc[1]))
		}
	}
	for _, tc := range [][2]string{
		{"thủy", "thủi"},
		{"huy", "hui"},
		{"suy", "sui"},
	} {
		if Folded(tc[0], tc[1]) {
			t.Errorf("Fold(%q) and Fold(%q) came out the same, want different keys", tc[0], tc[1])
		}
	}
}

// The fold is a key and never text. Nothing in the pipeline writes it back into
// a document, and this is the test that says so out loud: the two spellings both
// survive normalization exactly as they arrived.
func TestNormalizationDoesNotFoldTheText(t *testing.T) {
	for _, s := range []string{"Mỹ", "Mĩ", "vật lý", "vật lí", "bác sỹ", "bác sĩ"} {
		if got := Normalize(s); got.Text != s+"\n" {
			t.Errorf("Normalize(%q) = %q, want the spelling left alone", s, got.Text)
		}
	}
}

// Case is left to whoever builds the key, so the fold has to leave it alone.
// Folding both here would be two decisions with one test and neither of them
// arguable on its own.
func TestTheFoldDoesNotTouchCase(t *testing.T) {
	if Folded("Mỹ", "mĩ") {
		t.Error("Fold folded case as well as the spelling")
	}
	if got := Fold("Mỹ"); got != "Mĩ" {
		t.Errorf("Fold(%q) = %q, want %q", "Mỹ", got, "Mĩ")
	}
}

// Text with no y in it comes back as the same string, which is the fast path and
// also the promise that the fold changes nothing else.
func TestTextWithNoYIsReturnedAsItIs(t *testing.T) {
	for _, s := range []string{
		"Hà Nội mùa này trời trở lạnh.",
		"đường",
		"",
		"1234",
	} {
		if got := Fold(s); got != s {
			t.Errorf("Fold(%q) = %q, want it unchanged", s, got)
		}
	}
}

// A sentence folds a word at a time without disturbing anything around it.
func TestASentenceFoldsInPlace(t *testing.T) {
	const a = "Sách vật lý của bác sỹ Mỹ."
	const b = "Sách vật lí của bác sĩ Mĩ."
	if !Folded(a, b) {
		t.Errorf("Fold(%q) = %q\nFold(%q) = %q\nwant the same key", a, Fold(a), b, Fold(b))
	}
}
