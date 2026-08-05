package doc

import "testing"

// A syllable count is a count and not one of the estimates in units.go, and it
// is what a per source number in a release note is allowed to be built from.
func TestSyllablesAreCountedAndNotEstimated(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint32
	}{
		{"Tiếng Việt", 2},
		{"Hà Nội, Việt Nam.", 4},
		{"", 0},
		{"123 456", 0},
		{"COVID-19", 1},
		{"một\nhai\tba", 3},
	} {
		if got := Syllables(tc.in); got != tc.want {
			t.Errorf("Syllables(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// The two units are far enough apart that using one where the other belongs
// would be a corpus size out by a factor, and a diacritic is a character and not
// a syllable of its own.
func TestCharactersAndSyllablesAreDifferentUnits(t *testing.T) {
	const text = "Tiếng Việt"
	if got := Chars(text); got != 10 {
		t.Errorf("Chars(%q) = %d, want 10", text, got)
	}
	if got := Syllables(text); got != 2 {
		t.Errorf("Syllables(%q) = %d, want 2", text, got)
	}
}

// Characters and bytes are also different units, and the gap between them is
// what the diacritics cost. A corpus that quoted one for the other would be
// wrong by half.
func TestCharactersAreNotBytes(t *testing.T) {
	const text = "Tiếng Việt"
	if Chars(text) >= uint32(len(text)) {
		t.Errorf("%q is %d characters and %d bytes, and Vietnamese is not ASCII", text, Chars(text), len(text))
	}
}
