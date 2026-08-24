package sift

import (
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// The syllables that break an inventory built carelessly. Every one of them is
// ordinary Vietnamese and every one of them sits on a rule: the front vowel
// spellings, the glide written o in one place and u in another, gi swallowing
// the i of the rhyme, qu spelling the glide with its own u, the two written
// forms of the ia diphthong, and the handful of rhymes that exist in a dozen
// words and are missing from every short list of them.
var hardSyllables = strings.Fields(`
	gì gìn giếng giết giá giữ giường giới giặt giúp
	nghiêng nghỉ nghe nghĩa ghé ghềnh kẻo kiềm kỳ
	quyền quýt quỳnh quốc quận quả quay quăng quơ
	khuya thuở huơ khuếch khuất bâng khuâng
	nguyễn nguyên uyên yếm yên yêu ỉa ăn ưng
	oái oăm ngoằn ngoèo xoèn xoẹt tuyệt duyệt
	loong xoong coóc bậc tấc hoạ goá thuý luỹ
	mỹ kỹ tỷ hy vy ly ý ừ ạ ố ê ơ
	ưu bưu hươu rượu người được những cũng đã sẽ
	trước tuần ngoáy thuyền chuyện
`)

// The inventory is a claim about the language, so the first test is whether it
// holds the language. These are the syllables that are easy to leave out, and a
// missing rhyme here is a class of real Vietnamese rejected in production.
func TestTheAwkwardSyllablesAreAllInTheInventory(t *testing.T) {
	for _, s := range hardSyllables {
		if !Syllable(s) {
			t.Errorf("%q is Vietnamese and the inventory does not hold it", s)
		}
	}
}

// The other half of the claim: every syllable of every Vietnamese document this
// package keeps has to be in it. Fixtures written to exercise a threshold are
// not written to exercise the inventory, which is what makes them worth running
// it over. The menu fixture is left out, for the reason the identifier exists:
// half of a Vietnamese navigation bar is Video and Podcast and Infographics,
// which are not Vietnamese syllables and are on the page.
func TestEverySyllableOfTheCorpusIsInTheInventory(t *testing.T) {
	for name, text := range map[string]string{
		"article":  article,
		"caption":  caption,
		"listing":  listing,
		"looped":   looped,
		"chanted":  chanted,
		"unmarked": unmarked,
	} {
		t.Run(name, func(t *testing.T) {
			bare := name == "unmarked"
			for _, tok := range strings.Fields(text) {
				word := trimToLetters(tok)
				if word == "" || !allLetters(word) {
					continue
				}
				if bare {
					if !BareSyllable(word) {
						t.Errorf("%q is Vietnamese without its marks and the inventory does not hold it", word)
					}
					continue
				}
				if !Syllable(word) {
					t.Errorf("%q is Vietnamese and the inventory does not hold it", word)
				}
			}
		})
	}
}

// c and k spell one sound, and so do g and gh, and ng and ngh. Which one is
// written is decided by the vowel that follows and never by anything else, so
// half of each pair is not a Vietnamese spelling. It is the cheapest test there
// is for a string that was built to look Vietnamese rather than written in it.
func TestTheSpellingRulesThatHaveNoExceptions(t *testing.T) {
	for _, c := range []struct {
		written string
		ok      bool
	}{
		{"kẻ", true}, {"cẻ", false},
		{"kinh", true}, {"cinh", false},
		{"ca", true}, {"ka", false},
		{"cong", true}, {"kong", false},
		{"ghe", true}, {"ge", false},
		{"ghi", true}, {"gi", true}, // gi is the onset, not g before i
		{"gà", true}, {"ghà", false},
		{"nghe", true}, {"nge", false},
		{"nghiêm", true}, {"ngiêm", false},
		{"ngân", true}, {"nghân", false},
	} {
		if got := Syllable(c.written); got != c.ok {
			t.Errorf("Syllable(%q) = %v, want %v", c.written, got, c.ok)
		}
	}
}

// A syllable that ends in p, t, c or ch is stopped, and a stopped syllable takes
// the rising tone or the heavy one and can take no other. This is a fact about
// the language rather than a rule anybody is taught, it holds in every document
// written by somebody who has never heard of it, and it rules out four sixths of
// every spelling that ends in a stop.
func TestAStoppedSyllableTakesTwoOfTheSixTones(t *testing.T) {
	for _, c := range []struct {
		written string
		ok      bool
	}{
		{"mác", true}, {"mạc", true},
		{"màc", false}, {"mảc", false}, {"mãc", false},
		{"bát", true}, {"bạt", true}, {"bàt", false}, {"bảt", false},
		{"hếch", true}, {"hệch", true}, {"hềch", false},
		{"đẹp", true}, {"đép", true}, {"đèp", false},
		{"bàn", true}, {"bản", true}, {"bãn", true}, // no stop, all six
	} {
		if got := Syllable(c.written); got != c.ok {
			t.Errorf("Syllable(%q) = %v, want %v", c.written, got, c.ok)
		}
	}
}

// Both tone mark conventions have to be read, because both are correct
// Vietnamese and normalize settles them to one afterwards rather than before.
func TestBothToneConventionsAreVietnamese(t *testing.T) {
	for _, pair := range [][2]string{
		{"hoà", "hòa"}, {"khoẻ", "khỏe"}, {"thuỷ", "thủy"}, {"loà", "lòa"},
	} {
		for _, s := range pair {
			if !Syllable(s) {
				t.Errorf("%q is Vietnamese under one of the two conventions", s)
			}
		}
	}
}

// Taking the marks off is a loosening and the package says so where it is
// defined. This is the test that says how much: a spelling that is four
// different syllables with its marks on is one token without them, and a
// spelling that is not Vietnamese at all with its marks on can become Vietnamese
// once they are gone.
func TestBareMatchingAdmitsWhatMarkedMatchingDoesNot(t *testing.T) {
	for _, s := range []string{"duong", "nguoi", "duoc", "thuong", "nguyen"} {
		if Syllable(s) {
			t.Errorf("%q is not a Vietnamese spelling and Syllable took it", s)
		}
		if !BareSyllable(s) {
			t.Errorf("%q is Vietnamese with the marks off and BareSyllable did not take it", s)
		}
	}
}

// A syllable carries one tone mark. Two is either damage or an input method
// that did not run, and normalize has already had its say about both.
func TestTwoToneMarksIsNotASyllable(t *testing.T) {
	for _, s := range []string{"bàá", "hòó", "tóò"} {
		if Syllable(s) {
			t.Errorf("%q carries two tone marks and Syllable took it", s)
		}
	}
}

// Nothing that is not letters is a syllable, which matters because the
// identifier trims punctuation off the ends of a token and leaves the inside
// alone, so a hyphenated loanword arrives here whole.
func TestOnlyLettersAreASyllable(t *testing.T) {
	for _, s := range []string{"", "ki-lô", "b2b", "hà.nội", "co2"} {
		if Syllable(s) {
			t.Errorf("Syllable(%q) is true and it holds something that is not a letter", s)
		}
	}
}

// The inventory over-generates on purpose and the comment on it says so. This
// is the test that keeps the over-generation the size it was argued for: a few
// thousand spellings, not a few hundred thousand, which is what dropping one of
// the rules would produce.
func TestTheInventoryStaysTheSizeItWasArguedFor(t *testing.T) {
	if n := len(Syllables()); n < 3000 || n > 5000 {
		t.Errorf("the inventory holds %d spellings, and the argument for it was a few thousand", n)
	}
}

func allLetters(s string) bool {
	for _, c := range s {
		if !unicode.IsLetter(c) && !unicode.Is(unicode.Mn, c) {
			return false
		}
	}
	return s != ""
}

// TestTheASCIIShortcutAgreesWithTheLongWayRound holds down the fast path in
// [untone], which claims that a token of ASCII letters is its own untoned form.
//
// The claim is about Unicode rather than about Vietnamese: NFD and NFC are both
// the identity on ASCII, no tone mark is an ASCII byte, and no ASCII byte is a
// combining mark. That is true and it is the kind of true that a later edit to
// either function can quietly stop being, so it is checked against the loop it
// replaced rather than against a list of expected answers.
func TestTheASCIIShortcutAgreesWithTheLongWayRound(t *testing.T) {
	for _, tok := range untoneCases {
		gotBare, gotTone, gotOK := untone(tok)
		wantBare, wantTone, wantOK := untoneSlow(tok)
		if gotBare != wantBare || gotTone != wantTone || gotOK != wantOK {
			t.Errorf("untone(%q) = %q, %q, %v, want %q, %q, %v",
				tok, gotBare, gotTone, gotOK, wantBare, wantTone, wantOK)
		}
	}
}

// untoneSlow is untone without the shortcut, kept here so the shortcut has
// something to be checked against.
func untoneSlow(tok string) (string, rune, bool) {
	if tok == "" {
		return "", 0, false
	}
	var b strings.Builder
	tone := noTone
	for _, c := range norm.NFD.String(tok) {
		switch c {
		case grave, acute, hook, tilde, dot:
			if tone != noTone {
				return "", 0, false
			}
			tone = c
			continue
		}
		if !unicode.IsLetter(c) && !unicode.Is(unicode.Mn, c) {
			return "", 0, false
		}
		b.WriteRune(c)
	}
	return norm.NFC.String(b.String()), tone, true
}

// untoneCases is what [untone] is checked against [untoneSlow] over.
//
// The last group is the reason it is a variable rather than a literal inside one
// test. [untone] used to range over norm.NFD.String and now ranges over
// normalize.Decomposed, which walks the same runes without building the string
// and cannot put combining marks into canonical order. Those are the tokens
// where that distinction is capable of showing, so they are the ones a reader
// should look at when this test fails.
var untoneCases = []string{
	"hoa", "hòa", "tiếng", "Việt", "nguyễn", "đường", "ĐƯỜNG",
	"abc", "HTTP", "x", "", "a1", "1234", "co2", "e-mail", "a.b",
	"café", "naïve", "ȭ", "日本語", "hoa2", "  ", "\t",

	"é",      // a loose acute rather than é
	"tiếng", // tieng decomposed the whole way down
	"tié̂ng", // the same marks in the order NFD would swap
	"Việt",  // a mark of one class ahead of a mark of another
	"한국어",     // Hangul, which decomposes by arithmetic
	"ệ", "đ",  // precomposed ệ and đ
	"ạ́",       // two marks where one of them is a tone
	"̀", "̣hoa", // a token that begins with a mark
}

// TestUntoneAgreesWithTheLongWayRoundOverRealText is the same claim as the test
// above over prose rather than over a list, because the list is the cases
// somebody thought of.
func TestUntoneAgreesWithTheLongWayRoundOverRealText(t *testing.T) {
	t.Parallel()
	for tok := range strings.FieldsSeq(untoneCorpus) {
		gotBare, gotTone, gotOK := untone(tok)
		wantBare, wantTone, wantOK := untoneSlow(tok)
		if gotBare != wantBare || gotTone != wantTone || gotOK != wantOK {
			t.Errorf("untone(%q) = %q, %q, %v, want %q, %q, %v",
				tok, gotBare, gotTone, gotOK, wantBare, wantTone, wantOK)
		}
	}
}

// untoneCorpus is Vietnamese prose with the Latin, brand and number noise a real
// page carries, because a token stream that is all Vietnamese is not one.
const untoneCorpus = `Hà Nội là thủ đô của Việt Nam và là trung tâm chính trị của cả nước.
Thành phố Hồ Chí Minh có dân số đông nhất, khoảng 9 triệu người theo thống kê 2019.
Tiếng Việt được viết bằng chữ Quốc ngữ, một hệ chữ dựa trên bảng chữ cái Latinh.
Nhiều trang web tiếng Việt dùng WordPress, Shopify hoặc Google Analytics.
Giá vé máy bay từ SGN đi HAN khoảng 1.200.000 đồng vào mùa thấp điểm.
Đội tuyển bóng đá quốc gia đã thi đấu tại vòng loại World Cup 2022.
Công nghệ AI và machine learning đang được ứng dụng rộng rãi trong y tế.
Chợ Bến Thành mở cửa từ 6h sáng đến 18h hàng ngày, trừ dịp Tết Nguyên đán.
Sinh viên đại học Bách khoa nghiên cứu về năng lượng tái tạo và pin lithium.
Bài viết này được cập nhật lần cuối vào ngày 15 tháng 3 năm 2024 lúc 14:30.`

func BenchmarkUntone(b *testing.B) {
	toks := strings.Fields(untoneCorpus)
	b.ReportAllocs()
	for b.Loop() {
		for _, tok := range toks {
			untone(tok)
		}
	}
}
