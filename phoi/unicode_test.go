package phoi

import "testing"

// A zero width space inside a syllable is invisible to a reader and permanent to
// a hash. It is the cheapest way to make one word into two, and it arrives by
// the thousand from pages that were laid out for print.
func TestInvisibleCharactersComeOutOfTheMiddleOfAWord(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"zero width space", "Vi\u200bệt"},
		{"zero width non joiner", "Vi\u200cệt"},
		{"zero width joiner", "Vi\u200dệt"},
		{"soft hyphen", "Vi\u00adệt"},
		{"byte order mark", "Vi\ufeffệt"},
		{"word joiner", "Vi\u2060ệt"},
		{"left to right mark", "Vi\u200eệt"},
	} {
		got := Normalize(tc.in)
		if got.Text != "Việt\n" {
			t.Errorf("%s: Normalize(%q) = %q, want %q", tc.name, tc.in, got.Text, "Việt")
		}
		if got.Invisible != 1 {
			t.Errorf("%s: removed %d invisible characters, want 1", tc.name, got.Invisible)
		}
	}
}

// The spaces that are not the space. They break tokenization and word counting
// and they are indistinguishable from a space on screen.
func TestTheOtherSpacesBecomeTheSpace(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"no break space", "Việt\u00a0Nam"},
		{"en space", "Việt\u2002Nam"},
		{"em space", "Việt\u2003Nam"},
		{"thin space", "Việt\u2009Nam"},
		{"narrow no break space", "Việt\u202fNam"},
		{"ideographic space", "Việt\u3000Nam"},
	} {
		if got := Normalize(tc.in); got.Text != "Việt Nam\n" {
			t.Errorf("%s: Normalize(%q) = %q, want %q", tc.name, tc.in, got.Text, "Việt Nam")
		}
	}
}

// Eth is Icelandic, the caron is Czech, and the Cyrillic look-alike is not the
// letter anybody typed. Each of them renders close enough to a Vietnamese letter
// to survive proofreading and none of them is one.
func TestHomoglyphsAreRepairedRatherThanKept(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"eth for d with stroke", "ði", "đi"},
		{"capital eth", "Ði", "Đi"},
		{"caron for breve", "nǎ", "nă"},
		{"cyrillic a", "nаm", "nam"},
		{"cyrillic o", "tоn", "ton"},
		{"cyrillic e", "bеn", "ben"},
		{"cyrillic capital a", "Аnh", "Anh"},
		{"greek omicron", "tοn", "ton"},
		{"fullwidth letters", "Ｖｉｅｔ", "Viet"},
		{"fullwidth digits", "１９４５", "1945"},
	} {
		got := Normalize(tc.in)
		if got.Text != tc.want+"\n" {
			t.Errorf("%s: Normalize(%q) = %q, want %q", tc.name, tc.in, got.Text, tc.want)
		}
		if got.Homoglyphs == 0 {
			t.Errorf("%s: repaired a homoglyph and did not count it", tc.name)
		}
	}
}

// A Cyrillic o in a Vietnamese word is damage. A Cyrillic o in a Russian word is
// a Russian word, and a corpus that repaired it would be corrupting the text it
// was cleaning. The test is the word around the letter rather than the letter.
func TestALookAlikeIsOnlyOneInAWordThatIsOtherwiseLatin(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"a Vietnamese word with one Cyrillic letter", "c\u043eng ty", "cong ty"},
		{"a Vietnamese word with two of them", "s\u0430\u043e", "sao"},
		{"a line of Russian", "\u041f\u0440\u0438\u043c\u0435\u0440 \u0442\u0435\u043a\u0441\u0442\u0430", "\u041f\u0440\u0438\u043c\u0435\u0440 \u0442\u0435\u043a\u0441\u0442\u0430"},
		{"a Russian word of nothing but look-alikes", "\u0441\u043e\u0440", "\u0441\u043e\u0440"},
		{"a word of Greek", "\u03bb\u03bf\u03b3\u03bf\u03c2", "\u03bb\u03bf\u03b3\u03bf\u03c2"},
		{"Russian in a Vietnamese sentence", "Anh ta n\u00f3i \u0434\u0430 v\u1edbi t\u00f4i", "Anh ta n\u00f3i \u0434\u0430 v\u1edbi t\u00f4i"},
	} {
		if got := Normalize(tc.in); got.Text != tc.want+"\n" {
			t.Errorf("%s: Normalize(%q) = %q, want %q", tc.name, tc.in, got.Text, tc.want)
		}
	}
}

// Control characters are removed and counted, and the count is the tell. Text
// carries none of them, and a file that was sniffed as text and is really a font
// or an archive carries them everywhere.
func TestControlCharactersAreRemovedAndCounted(t *testing.T) {
	got := Normalize("Vi\u0001ệt Na\u0002m")
	if got.Text != "Việt Nam\n" {
		t.Errorf("Normalize = %q, want %q", got.Text, "Việt Nam")
	}
	if got.Controls != 2 {
		t.Errorf("removed %d control characters, want 2", got.Controls)
	}
	if got.ControlRate() <= ControlLimit {
		t.Errorf("control rate %v, want it over the limit for a string this short", got.ControlRate())
	}
}

// The tab and the newline are whitespace rather than damage. The newline is a
// line break and stays one, and the tab becomes a space because it is layout,
// but neither of them is a control character and counting them as damage would
// put every indented document over the limit.
func TestTabsAndNewlinesAreNotDamage(t *testing.T) {
	got := Normalize("một\thai\nba")
	if got.Text != "một hai\nba\n" {
		t.Errorf("Normalize = %q, want %q", got.Text, "một hai\nba\n")
	}
	if got.Controls != 0 {
		t.Errorf("counted %d control characters, want 0", got.Controls)
	}
}

func TestLineEndingsBecomeTheOneKind(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"windows", "một\r\nhai"},
		{"old mac", "một\rhai"},
		{"line separator", "một\u2028hai"},
		{"paragraph separator", "một\u2029hai"},
	} {
		if got := Normalize(tc.in); got.Text != "một\nhai\n" {
			t.Errorf("%s: Normalize(%q) = %q, want %q", tc.name, tc.in, got.Text, "một\nhai\n")
		}
	}
}

// Whitespace is layout rather than content. A page that was scraped out of HTML
// arrives with the indentation of the markup in it, and every stage after this
// one would carry it.
func TestLayoutIsRegular(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"runs of spaces collapse", "một    hai", "một hai\n"},
		{"tabs and spaces collapse together", "một \t  hai", "một hai\n"},
		{"leading space on a line goes", "    một hai", "một hai\n"},
		{"trailing space on a line goes", "một hai   ", "một hai\n"},
		{"blank lines collapse to one", "một\n\n\n\n\nhai", "một\n\nhai\n"},
		{"one blank line is kept", "một\n\nhai", "một\n\nhai\n"},
		{"leading blank lines go", "\n\n\nmột", "một\n"},
		{"trailing blank lines go", "một\n\n\n", "một\n"},
		{"a document ends with one newline", "một", "một\n"},
		{"a document that already did is unchanged", "một\n", "một\n"},
		{"nothing stays nothing", "", ""},
		{"whitespace alone is nothing", "  \n\n \t ", ""},
	} {
		if got := Normalize(tc.in); got.Text != tc.want {
			t.Errorf("%s: Normalize(%q) = %q, want %q", tc.name, tc.in, got.Text, tc.want)
		}
	}
}

// The paragraph break is the one piece of layout that is content. A document
// whose paragraphs run together reads as one block and trains as one, so the
// blank line survives everything above.
func TestParagraphsSurvive(t *testing.T) {
	in := "Đoạn thứ nhất.\r\n\r\n\r\nĐoạn thứ hai.\r\n"
	want := "Đoạn thứ nhất.\n\nĐoạn thứ hai.\n"
	if got := Normalize(in); got.Text != want {
		t.Errorf("Normalize = %q, want %q", got.Text, want)
	}
}

// Text that is already in the form gao writes has to come back byte for byte,
// or every count this stage reports is noise and the corpus is rewritten on
// every pass through the pipeline.
func TestTextThatIsAlreadyRightIsNotTouched(t *testing.T) {
	for _, s := range []string{
		"Việt Nam là một quốc gia ở Đông Nam Á.\n",
		"Hà Nội là thủ đô của Việt Nam.\n",
		"Tôi đi học lúc bảy giờ sáng.\n",
		"Đoạn thứ nhất.\n\nĐoạn thứ hai.\n",
		"The quick brown fox jumps over the lazy dog.\n",
		"1 + 1 = 2\n",
	} {
		got := Normalize(s)
		if got.Text != s {
			t.Errorf("Normalize(%q) = %q, want it unchanged", s, got.Text)
		}
		if got.Changed {
			t.Errorf("Normalize(%q) says it changed the text and it did not", s)
		}
	}
}
