package normalize

import (
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// article is the paragraph the encodings are tested on. It is lower case
// throughout except for the two proper nouns, which carry no tone marks,
// because TCVN3 has no capital vowel with a tone mark in it and a fixture that
// used one would be testing a thing the encoding cannot do.
const article = "Hà Nội mùa này trời trở lạnh, và những người đi làm về muộn " +
	"vẫn dừng lại ở góc phố cũ để mua một gói xôi.\n" +
	"Bà cụ bán hàng ở đó đã ngồi chỗ ấy từ trước khi con phố có tên mới, " +
	"và bà vẫn nhớ mặt từng người khách.\n"

// The two encodings almost all of the material is in are checked against
// mojibake rather than against a round trip, because a round trip only proves
// that this package disagrees with itself in no place. These strings are what a
// Vietnamese reader has seen on a broken page for thirty years, and they are
// written here as characters rather than as bytes so that anybody can read what
// is being claimed.
func TestTheMojibakeEverybodyKnowsComesBackAsVietnamese(t *testing.T) {
	for _, c := range []struct {
		charset string
		broken  string
		want    string
	}{
		{"TCVN3", "TiÕng ViÖt", "Tiếng Việt"},
		{"TCVN3", "Hµ Néi", "Hà Nội"},
		{"TCVN3", "®\u00adîc", "được"}, // ư is the soft hyphen, escaped so it is not invisible here
		{"TCVN3", "ChÝnh phñ ViÖt Nam", "Chính phủ Việt Nam"},
		{"VNI-WIN", "Tieáng Vieät", "Tiếng Việt"},
		{"VNI-WIN", "Haø Noäi", "Hà Nội"},
		{"VNI-WIN", "ñöôïc", "được"},
	} {
		var found *Charset
		for _, set := range Charsets() {
			if set.Name() == c.charset {
				found = set
			}
		}
		if found == nil {
			t.Fatalf("there is no %s", c.charset)
		}
		if got, _ := found.Decode(c.broken); got != c.want {
			t.Errorf("%s reads %q as %q, want %q", c.charset, c.broken, got, c.want)
		}
	}
}

// Every encoding has to survive the trip out and back. This proves less than the
// test above it, because both directions read the same table, and it proves the
// one thing that test cannot: that the table covers the language.
func TestEveryEncodingCarriesAParagraphAndComesBack(t *testing.T) {
	for _, set := range Charsets() {
		t.Run(set.Name(), func(t *testing.T) {
			broken := encode(t, set, article)
			if broken == article {
				t.Fatal("the encoded paragraph is the paragraph, so nothing was encoded")
			}
			found := Detect(broken)
			if found == nil {
				t.Fatalf("a page of %s was not recognized as one", set.Name())
			}
			if found.Name() != set.Name() {
				t.Fatalf("a page of %s was read as %s", set.Name(), found.Name())
			}
			got, letters := found.Decode(broken)
			if got != article {
				t.Errorf("the paragraph came back as\n%q\nwant\n%q", got, article)
			}
			if letters == 0 {
				t.Error("no letters were counted")
			}
		})
	}
}

// A document that is already Vietnamese is the case this stage has to be
// hardest on itself about. é and ô are ordinary letters in it and they are also
// ordinary bytes in four of these encodings, so a stage that went looking for
// evidence rather than for a reason to stop would rewrite a perfectly good page.
func TestUnicodeVietnameseIsNotTouched(t *testing.T) {
	if set := Detect(article); set != nil {
		t.Fatalf("a Unicode page was read as %s", set.Name())
	}
	got := Normalize(article)
	if got.Legacy != "" || got.Transcoded != 0 {
		t.Errorf("normalize reports %q and %d letters, want neither", got.Legacy, got.Transcoded)
	}
}

// The other half of that: a page in a European language is full of the same
// bytes and none of the words.
func TestAnotherLanguageIsNotReadAsVietnamese(t *testing.T) {
	for _, text := range []string{
		"Le président a déclaré que la réunion aurait lieu à Genève, où les " +
			"délégués représentés étaient déjà arrivés très tôt dans la journée.",
		"Die Universität überprüfte größere Änderungen an den Prüfungen, während " +
			"mehrere Studierende dagegen Einspruch erhoben hätten müssen.",
		"El señor Muñoz añadió que la situación económica todavía está más " +
			"difícil que el año pasado según los últimos números publicados.",
	} {
		if set := Detect(text); set != nil {
			t.Errorf("%q was read as %s", text[:24], set.Name())
		}
	}
}

// A page that mixes the two is left alone, and this is the test that says so out
// loud rather than the comment on the rule. Transcoding it would rewrite the
// half that was already right.
func TestAPageThatIsHalfUnicodeIsLeftAlone(t *testing.T) {
	mixed := encode(t, tcvn3, article) + "\n" + article
	if set := Detect(mixed); set != nil {
		t.Fatalf("a mixed page was read as %s", set.Name())
	}
}

// The margin is the part of the design that decides what this stage does when it
// is not sure, so it is tested at the edge rather than in the middle.
func TestTooLittleEvidenceIsNotEnough(t *testing.T) {
	for _, text := range []string{
		"",
		"Hello.",
		encode(t, tcvn3, "một"), // one word, and too short to try
		encode(t, tcvn3, "của một người bình thường"), // three words, but not enough bytes
	} {
		if set := Detect(text); set != nil {
			t.Errorf("%q was read as %s", text, set.Name())
		}
	}
}

// Two of these encodings put letters where Latin-1 puts control characters, so a
// page of VPS read that way arrives full of them. The stage that strips control
// characters runs after this one for exactly that reason, and this is the test
// that would fail if somebody reordered them.
func TestLettersInTheControlRangeSurviveNormalization(t *testing.T) {
	broken := latin1(encode(t, vps, article))
	controls := 0
	for _, c := range broken {
		if c >= 0x80 && c <= 0x9f {
			controls++
		}
	}
	if controls == 0 {
		t.Fatal("the fixture holds no bytes in the control range, so it proves nothing")
	}

	got := Normalize(broken)
	if got.Legacy != "VPS" {
		t.Fatalf("normalize reports %q, want VPS", got.Legacy)
	}
	if got.Controls != 0 {
		t.Errorf("%d letters were counted as control characters", got.Controls)
	}
	if !strings.Contains(got.Text, "Hà Nội mùa này trời trở lạnh") {
		t.Errorf("the transcoded document reads\n%q", got.Text)
	}
}

// A byte the encoding does not use is a character somebody pasted in, and it
// comes through as itself. A page of TCVN3 with a curly quote in it is a page of
// TCVN3 with a curly quote in it.
func TestACharacterTheEncodingDoesNotUseComesThrough(t *testing.T) {
	broken := encode(t, tcvn3, article) + "“abc”"
	got, _ := tcvn3.Decode(broken)
	if !strings.HasSuffix(got, "“abc”") {
		t.Errorf("the quotes came back as %q", got[len(got)-12:])
	}
}

// BK HCM1 puts six capitals on ASCII punctuation and this stage does not read
// them, because a document it was wrong about would come out with its braces
// turned into vowels.
func TestTheASCIIRangeIsNeverRewritten(t *testing.T) {
	const code = "if (a[i] > b) { x = ~y | z; }"
	for _, set := range Charsets() {
		got, letters := set.Decode(code)
		if got != code || letters != 0 {
			t.Errorf("%s read %q as %q", set.Name(), code, got)
		}
	}
}

// TCVN3 cannot say whether a vowel with a tone mark on it was a capital, because
// the capitals were a second font with the same bytes in it. The table has to
// hold no capital toned vowel at all, and if somebody ever adds one this is the
// test that says what they have decided on the language's behalf.
func TestTCVN3HasNoCapitalWithAToneMark(t *testing.T) {
	for b, c := range tcvn3.single {
		if unicode.IsUpper(c) && hasTone(c) {
			t.Errorf("0x%02x is %q, which TCVN3 cannot have", b, c)
		}
	}
}

// The counts are the claim here as much as anywhere else in this package.
func TestTheTallyRecordsWhichEncodingsTheCorpusMet(t *testing.T) {
	var got Tally
	for _, text := range []string{
		encode(t, tcvn3, article),
		encode(t, tcvn3, article),
		encode(t, vniWin, article),
		article,
	} {
		got.Add(Normalize(text))
	}
	if got.Legacy["TCVN3"] != 2 || got.Legacy["VNI-WIN"] != 1 || len(got.Legacy) != 2 {
		t.Errorf("the tally reports %v", got.Legacy)
	}
	if got.Repaired != 3 {
		t.Errorf("%d documents were repaired, want 3", got.Repaired)
	}
}

// Charsets hands out the list rather than the list itself, so that a caller who
// sorts it does not sort the package's copy.
func TestCharsetsIsACopy(t *testing.T) {
	got := Charsets()
	if len(got) == 0 {
		t.Fatal("there are no encodings")
	}
	got[0] = nil
	if Charsets()[0] == nil {
		t.Error("the package's own list was written through")
	}
}

// Every table has to be a table: one letter per code, and a letter that is
// Vietnamese.
func TestTheTablesHoldVietnameseAndNothingElse(t *testing.T) {
	for _, set := range Charsets() {
		for b, c := range set.single {
			if b < 0x80 {
				t.Errorf("%s puts %q at 0x%02x, below the range this stage reads", set.Name(), c, b)
			}
			if !vietnameseLetter(c) {
				t.Errorf("%s puts %q at 0x%02x, which is not a Vietnamese letter", set.Name(), c, b)
			}
		}
		for code, c := range set.pairs {
			if !vietnameseLetter(c) {
				t.Errorf("%s makes %q out of 0x%02x 0x%02x, which is not a Vietnamese letter",
					set.Name(), c, code[0], code[1])
			}
		}
	}
}

// encode writes a document into an encoding and then renders the bytes the way a
// crawler that believed the page was windows-1252 would. It fails the test on a
// letter the encoding has no code for, which is how the round trip above knows
// that a table covers the language rather than most of it.
func encode(t *testing.T, set *Charset, text string) string {
	t.Helper()
	single := map[rune]byte{}
	for b, c := range set.single {
		single[c] = b
	}
	pairs := map[rune][2]byte{}
	for code, c := range set.pairs {
		pairs[c] = code
	}

	var b strings.Builder
	for _, c := range text {
		switch {
		case c < 0x80:
			b.WriteRune(c)
		case single[c] != 0:
			b.WriteRune(windows1252(single[c]))
		default:
			code, ok := pairs[c]
			if !ok {
				t.Fatalf("%s has no code for %q", set.Name(), c)
			}
			b.WriteRune(windows1252(code[0]))
			b.WriteRune(windows1252(code[1]))
		}
	}
	return b.String()
}

// windows1252 is the character a byte comes out as when a page is read as
// windows-1252, which is what a crawler that was told nothing does.
func windows1252(b byte) rune {
	for c, code := range cp1252 {
		if code == b {
			return c
		}
	}
	return rune(b)
}

// latin1 re-renders a document as though the crawler had read it as Latin-1
// instead, which sends the bytes windows-1252 draws as punctuation to the
// control characters.
func latin1(text string) string {
	var b strings.Builder
	for _, c := range text {
		if code, ok := cp1252[c]; ok {
			b.WriteRune(rune(code))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

func vietnameseLetter(c rune) bool {
	bare := strings.ToLower(Bare(string(c)))
	return len(bare) == 1 && strings.ContainsAny(bare, "aeiouyd")
}

// hasTone reports whether a letter carries one of the five tone marks, as
// opposed to the circumflex or the breve or the horn, which are part of the
// letter rather than the tone.
func hasTone(c rune) bool {
	for _, r := range norm.NFD.String(string(c)) {
		if tone(r) {
			return true
		}
	}
	return false
}
