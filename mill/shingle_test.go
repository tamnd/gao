package mill

import "testing"

// The key is what a republisher cannot change without changing the document.
// Everything a second site does to an article on the way through is dropped
// here, or every syndicated article in the corpus is its own document.
func TestTheKeyDropsWhatARepublisherChanges(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"case", "Hà Nội", "hà nội"},
		{"punctuation", "Hà Nội, thủ đô!", "hà nội thủ đô"},
		{"curly quotes", "ông “nói” rằng", "ông nói rằng"},
		{"runs of spacing", "hà   nội\n\nthủ đô", "hà nội thủ đô"},
		{"leading and trailing", "  hà nội.  ", "hà nội"},
		{"the i and y pair", "Kỹ thuật của Mỹ", "kĩ thuật của mĩ"},
	} {
		if got := Key(tc.in); got != tc.want {
			t.Errorf("%s: Key(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// Two documents that differ in a number are two documents. Dropping digits with
// the punctuation is the tempting simplification, and in a corpus of Vietnamese
// statistics it would keep one year of everything.
func TestTheKeyKeepsDigits(t *testing.T) {
	a := Key("Tổng sản phẩm trong nước năm 2023 tăng 5,05 phần trăm.")
	b := Key("Tổng sản phẩm trong nước năm 2024 tăng 7,09 phần trăm.")
	if a == b {
		t.Errorf("two years of one statistic have the same key: %q", a)
	}
	if got := Key("Năm 1986"); got != "năm 1986" {
		t.Errorf("Key dropped the year: %q", got)
	}
}

// Punctuation becomes a space rather than nothing. A line that ends without one
// and a heading that runs into the sentence under it must not fuse the two
// syllables on either side into a word that is in neither document.
func TestPunctuationSeparatesRatherThanDisappears(t *testing.T) {
	if got, want := Key("mùa hè.Nhiệt độ"), "mùa hè nhiệt độ"; got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}
}

func TestShinglesCoverTheKey(t *testing.T) {
	key := Key("Hà Nội là thủ đô")
	n := 0
	for range Shingles(key) {
		n++
	}
	if want := len([]rune(key)) - ShingleSize + 1; n != want {
		t.Errorf("%q produced %d shingles, want %d", key, n, want)
	}
}

// A document shorter than the window still has to have an identity of its own.
// Without this the shortest documents in the corpus would produce no shingles,
// sign identically, and collapse into one cluster.
func TestADocumentShorterThanTheWindowIsOneShingle(t *testing.T) {
	seen := map[uint64]bool{}
	for _, s := range []string{"hà", "nội", "một", "hai"} {
		n := 0
		var h uint64
		for got := range Shingles(Key(s)) {
			h = got
			n++
		}
		if n != 1 {
			t.Errorf("%q produced %d shingles, want 1", s, n)
		}
		if seen[h] {
			t.Errorf("%q hashes to a shingle another short document already produced", s)
		}
		seen[h] = true
	}
}

func TestNothingHasNoShingles(t *testing.T) {
	for _, s := range []string{"", "   ", "...", "\n\n"} {
		for range Shingles(Key(s)) {
			t.Errorf("Shingles(%q) produced one, want none", s)
		}
	}
}

// The window slides over characters, not bytes. Vietnamese letters are two and
// three bytes each, so a byte window would cut them apart and hash the halves,
// and the shingles of a document would depend on where its accents happened to
// fall.
func TestTheWindowSlidesOverCharacters(t *testing.T) {
	ascii, viet := 0, 0
	for range Shingles("abcdefghij") {
		ascii++
	}
	for range Shingles("đường bộ ơ") {
		viet++
	}
	if ascii != viet {
		t.Errorf("ten ASCII characters produced %d shingles and ten Vietnamese ones produced %d", ascii, viet)
	}
}
