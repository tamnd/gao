package inspect

import (
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// The whole package rests on taking a Vietnamese letter apart the same way every
// time, so this is the test that has to be right before any of the rest means
// anything.
func TestWhatALetterIsMadeOf(t *testing.T) {
	for _, c := range []struct {
		in   rune
		base rune
		mod  rune
		tone Tone
	}{
		{'a', 'a', 0, Ngang},
		{'à', 'a', 0, Huyen},
		{'á', 'a', 0, Sac},
		{'ả', 'a', 0, Ask},
		{'ã', 'a', 0, Nga},
		{'ạ', 'a', 0, Nang},
		{'â', 'a', circumflex, Ngang},
		{'ấ', 'a', circumflex, Sac},
		{'ậ', 'a', circumflex, Nang},
		{'ă', 'a', breve, Ngang},
		{'ắ', 'a', breve, Sac},
		{'ê', 'e', circumflex, Ngang},
		{'ế', 'e', circumflex, Sac},
		{'ề', 'e', circumflex, Huyen},
		{'ô', 'o', circumflex, Ngang},
		{'ơ', 'o', horn, Ngang},
		{'ợ', 'o', horn, Nang},
		{'ư', 'u', horn, Ngang},
		{'ữ', 'u', horn, Nga},
		{'đ', 'd', stroke, Ngang},
		{'Đ', 'D', stroke, Ngang},
		{'Ế', 'E', circumflex, Sac},
		{'5', '5', 0, Ngang},
		{' ', ' ', 0, Ngang},
		{',', ',', 0, Ngang},
	} {
		got := Split(c.in)
		if got.Base != c.base || got.Mod != c.mod || got.Tone != c.tone {
			t.Errorf("%c came apart as base %c mod %U tone %s, want base %c mod %U tone %s",
				c.in, got.Base, got.Mod, got.Tone, c.base, c.mod, c.tone)
		}
	}
}

// Marked is the denominator of the rate this package exists for, so what counts
// as marked is worth stating twice.
func TestWhatCountsAsMarked(t *testing.T) {
	for _, c := range []struct {
		in   rune
		want bool
	}{
		{'a', false}, {'d', false}, {'x', false}, {'5', false}, {' ', false},
		{'à', true}, {'ê', true}, {'ơ', true}, {'đ', true}, {'ế', true}, {'ă', true},
	} {
		if got := Split(c.in).Marked(); got != c.want {
			t.Errorf("Split(%c).Marked() = %v, want %v", c.in, got, c.want)
		}
	}
}

// Vietnamese arrives written both ways and the two spellings are one letter. A
// metric that counted them differently would report an error rate that depended
// on which normalization form somebody's tooling emitted, which is the kind of
// number that gets argued about for a week.
func TestTheNormalizationFormDoesNotChangeTheAnswer(t *testing.T) {
	const s = "Tiếng Việt có sáu thanh điệu, và đó là chỗ mọi thứ hỏng."
	nfc, nfd := Letters(norm.NFC.String(s)), Letters(norm.NFD.String(s))
	if len(nfc) != len(nfd) {
		t.Fatalf("the same sentence came to %d letters composed and %d decomposed", len(nfc), len(nfd))
	}
	for i := range nfc {
		if nfc[i] != nfd[i] {
			t.Errorf("letter %d is %+v composed and %+v decomposed", i, nfc[i], nfd[i])
		}
	}
	if a, b := Measure(norm.NFC.String(s), norm.NFD.String(s)), (Score{}); a.Wrong+a.Dropped+a.Added != b.Wrong {
		t.Errorf("one sentence against itself in the other form came to %d errors", a.Wrong+a.Dropped+a.Added)
	}
}

// pages is a few paragraphs of ordinary Vietnamese, which is what the two tests
// below are arithmetic on. Nothing about them is special and that is the point:
// the shares they measure are properties of the language rather than of a
// fixture somebody tuned.
var pages = []string{
	"Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập, tự do, hạnh phúc.",
	"Chủ tịch Hồ Chí Minh đọc bản Tuyên ngôn độc lập ngày mùng hai tháng chín năm một " +
		"nghìn chín trăm bốn mươi lăm tại quảng trường Ba Đình, trước một biển người đã đi " +
		"bộ từ các tỉnh lân cận về thủ đô để nghe.",
	"Tiếng Việt là ngôn ngữ chính thức của Việt Nam, và là tiếng mẹ đẻ của khoảng tám " +
		"mươi lăm phần trăm dân số cả nước, cùng với hơn bốn triệu người Việt sống ở nước ngoài.",
	"Bán nhà mặt phố, diện tích năm mươi hai mét vuông, ba tầng, hướng đông nam, sổ đỏ " +
		"chính chủ, giá bốn tỷ tám trăm năm mươi triệu đồng, thương lượng.",
}

// The package documentation says about a quarter of the characters carry a mark
// and about a sixth carry a tone, and then does arithmetic on both. This is
// where those two numbers come from, so that the argument in the doc comment is
// measured rather than remembered.
func TestTheShapeOfTheLanguage(t *testing.T) {
	var total Score
	for _, p := range pages {
		total.Add(Measure(p, p))
	}

	marked := float64(total.Marked) / float64(total.Chars)
	toned := float64(total.Toned) / float64(total.Chars)
	t.Logf("%d characters, %.1f%% marked, %.1f%% carrying a tone", total.Chars, marked*100, toned*100)

	if marked < 0.20 || marked > 0.30 {
		t.Errorf("%.1f%% of the characters carry a mark, and the documentation says about a quarter", marked*100)
	}
	if toned < 0.13 || toned > 0.20 {
		t.Errorf("%.1f%% of the characters carry a tone, and the documentation says about a sixth", toned*100)
	}
}

// The argument the whole package is built on, as a test. A reading that is 2%
// wrong by the metric everybody quotes has lost one tone in eight, and the two
// numbers have to be able to say so next to each other.
func TestTheReadingThatLooksGoodAndIsNot(t *testing.T) {
	page := strings.Join(pages, "\n")
	read := dropSomeTones(page, 8)
	s := Measure(page, read)

	if s.CER() > 0.03 {
		t.Fatalf("character error rate is %.3f, and this is meant to be a reading that looks good", s.CER())
	}
	if s.DER() < s.CER()*3 {
		t.Errorf("character error rate is %.3f and diacritic error rate is %.3f, and the second is meant to be the sharper of the two", s.CER(), s.DER())
	}
	if s.ToneDeletionRate() < 0.10 {
		t.Errorf("tone deletion rate is %.3f, and one tone in eight was taken off", s.ToneDeletionRate())
	}
	t.Logf("this reading is %.1f%% accurate, lost %.1f%% of the page's marks and %.1f%% of its tones",
		s.Accuracy()*100, s.DER()*100, s.ToneDeletionRate()*100)

	if fails := S4.Check(s); len(fails) == 0 {
		t.Error("the S4 gate passed a reading missing one tone in eight")
	}
}

// Every tone taken off, which is the extreme of the same failure and the one
// worth writing down because of what it does not do. The diacritic error rate
// is not 1. The letter marks are still there, ê is still ê, and a rate that
// reported total loss when three quarters of the marks survived would be a rate
// that could not tell this reading from one that also flattened đ to d. The
// tone deletion rate is 1, and that is the line that names what happened.
func TestEveryToneTakenOff(t *testing.T) {
	page := strings.Join(pages, "\n")
	s := Measure(page, dropTones(page))

	if s.ToneDeletionRate() != 1 {
		t.Fatalf("tone deletion rate is %.3f and every tone was taken off, so it is 1", s.ToneDeletionRate())
	}
	if s.DER() >= 1 {
		t.Errorf("diacritic error rate is %.3f, and the letter marks came through untouched", s.DER())
	}
	if s.DER() < 0.5 {
		t.Errorf("diacritic error rate is %.3f, and most of a Vietnamese page's marks are tones", s.DER())
	}
	if s.ToneWrong != 0 || s.ToneAdded != 0 {
		t.Errorf("%d tones were read as another and %d were invented, and neither happened", s.ToneWrong, s.ToneAdded)
	}
	if s.ModWrong != 0 || s.DD != 0 {
		t.Errorf("%d letter marks and %d đ were reported wrong, and neither was touched", s.ModWrong, s.DD)
	}
	t.Logf("with every tone taken off: %.1f%% accurate, %.1f%% of marks lost, %.0f%% of tones lost",
		s.Accuracy()*100, s.DER()*100, s.ToneDeletionRate()*100)
}

// dropTones is the failure the metric is built to catch, written out so the
// tests above are testing the metric rather than testing a fixture somebody
// typed. It takes the tones off and leaves the letter marks, which is what an
// engine that cannot see a small mark above a letter actually does to a page.
func dropTones(s string) string { return dropSomeTones(s, 1) }

// dropSomeTones takes the tone off every nth toned character, so a test can ask
// for a reading of a given quality rather than only for the extreme.
func dropSomeTones(s string, every int) string {
	var b strings.Builder
	at := 0
	for _, r := range norm.NFD.String(s) {
		if _, isTone := toneOf(r); isTone {
			at++
			if at%every == 0 {
				continue
			}
		}
		b.WriteRune(r)
	}
	return norm.NFC.String(b.String())
}

// The three ways one letter can be wrong, each on its own, because they are
// fixed in different places in an engine and a report that ran them together
// would not say which one to go and look at.
func TestTheThreeWaysALetterGoesWrong(t *testing.T) {
	for _, c := range []struct {
		name, ref, read string
		want            Score
	}{
		{
			name: "the tone went missing",
			ref:  "ế", read: "ê",
			want: Score{Wrong: 1, Lost: 1, ToneDropped: 1},
		},
		{
			name: "the tone changed into another word",
			ref:  "ế", read: "ề",
			want: Score{Wrong: 1, Lost: 1, ToneWrong: 1},
		},
		{
			name: "a tone was invented",
			ref:  "e", read: "ế",
			want: Score{Wrong: 1, ToneAdded: 1, ModWrong: 1},
		},
		{
			name: "the letter mark went missing",
			ref:  "ơ", read: "o",
			want: Score{Wrong: 1, Lost: 1, ModWrong: 1},
		},
		{
			name: "both marks went missing",
			ref:  "ế", read: "e",
			want: Score{Wrong: 1, Lost: 1, ToneDropped: 1, ModWrong: 1},
		},
		{
			name: "đ read as d",
			ref:  "đ", read: "d",
			want: Score{Wrong: 1, Lost: 1, ModWrong: 1, DD: 1},
		},
		{
			name: "d read as đ",
			ref:  "d", read: "đ",
			want: Score{Wrong: 1, ModWrong: 1, DD: 1},
		},
		{
			name: "a different letter altogether",
			ref:  "ế", read: "x",
			want: Score{Wrong: 1, Lost: 1},
		},
		{
			name: "the character was dropped",
			ref:  "ế", read: "",
			want: Score{Dropped: 1, Lost: 1},
		},
		{
			name: "a character was invented",
			ref:  "", read: "ế",
			want: Score{Added: 1},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Measure(c.ref, c.read)
			got.Chars, got.Read, got.Marked, got.Toned, got.Confusion = 0, 0, 0, 0, [6][6]int{}
			if got != c.want {
				t.Errorf("%q read as %q came to %+v, want %+v", c.ref, c.read, got, c.want)
			}
		})
	}
}

// A tone invented on a letter that already carried a mark is a real diacritic
// error, because that character is one of the page's marked ones and what it
// carries is not what was written. A tone invented on a bare letter is not, and
// the difference is the only place the rate's denominator is subtle.
func TestAToneInventedOnAMarkedLetterCounts(t *testing.T) {
	bare := Measure("e", "é")
	if bare.Marked != 0 || bare.Lost != 0 || bare.DER() != 0 {
		t.Errorf("a tone invented on a bare letter came to %d marked and %d lost", bare.Marked, bare.Lost)
	}
	if bare.ToneAdded != 1 {
		t.Errorf("the invented tone was not reported at all: %+v", bare)
	}

	marked := Measure("ă", "ắ")
	if marked.Marked != 1 || marked.Lost != 1 || marked.DER() != 1 {
		t.Errorf("a tone invented on ă came to %d marked, %d lost, rate %.3f", marked.Marked, marked.Lost, marked.DER())
	}
}

// The rate can never outrun its denominator, which is what makes a gate on it
// mean anything. Every marked character can lose its marks once.
func TestTheDiacriticRateStaysBetweenZeroAndOne(t *testing.T) {
	const page = "Đường Nguyễn Trãi, phường Thanh Xuân Bắc, số 25, giá 4,85 tỷ đồng."
	for _, read := range []string{
		page, dropTones(page), "", "xxxxxxxxxx", strings.Repeat(page, 3),
		"Duong Nguyen Trai, phuong Thanh Xuan Bac, so 25, gia 4,85 ty dong.",
	} {
		s := Measure(page, read)
		if s.DER() < 0 || s.DER() > 1 {
			t.Errorf("reading %q gave a diacritic error rate of %.3f", read, s.DER())
		}
		if s.Lost > s.Marked {
			t.Errorf("reading %q lost %d marks off %d marked characters", read, s.Lost, s.Marked)
		}
		if s.ToneDropped > s.Toned {
			t.Errorf("reading %q dropped %d tones off %d toned characters", read, s.ToneDropped, s.Toned)
		}
	}
}

// The matrix is what says which pairs an engine cannot tell apart, and the
// reason it is worth having rather than a single count is that hỏi read as ngã
// is one fault and ngã read as nothing is another.
func TestTheToneConfusionMatrix(t *testing.T) {
	s := Measure("mả mã má mà mạ ma", "mã mả má mà ma ma")

	if got := s.Confusion[Ask][Nga]; got != 1 {
		t.Errorf("hỏi read as ngã came to %d, want 1", got)
	}
	if got := s.Confusion[Nga][Ask]; got != 1 {
		t.Errorf("ngã read as hỏi came to %d, want 1", got)
	}
	if got := s.Confusion[Nang][Ngang]; got != 1 {
		t.Errorf("nặng dropped came to %d, want 1", got)
	}
	if got := s.Confusion[Sac][Sac]; got != 1 {
		t.Errorf("sắc read correctly came to %d, want 1", got)
	}
	if got := s.Confusion[Ngang][Ngang]; got != 0 {
		t.Errorf("the ngang to ngang cell holds %d, and it is left empty on purpose", got)
	}
	if s.ToneWrong != 2 || s.ToneDropped != 1 {
		t.Errorf("%d tones read as another and %d dropped, want 2 and 1", s.ToneWrong, s.ToneDropped)
	}
}

// A letter read as a different letter is not a tone the engine got wrong, and
// the matrix that exists to isolate tone errors must not hold letter errors.
func TestALetterErrorIsNotAToneError(t *testing.T) {
	s := Measure("số", "s6")
	if s.Wrong != 1 {
		t.Errorf("ố read as 6 came to %d character errors, want 1", s.Wrong)
	}
	if s.ToneDropped+s.ToneWrong+s.ToneAdded != 0 {
		t.Errorf("a letter read as a digit was counted as a tone failure: %+v", s)
	}
	for i := range s.Confusion {
		for j := range s.Confusion[i] {
			if s.Confusion[i][j] != 0 {
				t.Errorf("the matrix holds %d at %s to %s and no tone was compared", s.Confusion[i][j], Tone(i), Tone(j))
			}
		}
	}
	if s.Lost != 1 {
		t.Errorf("the marks on ố did not survive and %d were counted as lost", s.Lost)
	}
}

// The unmarked register is not a reading error. Vietnamese typed without tone
// marks is how most of the web writes, so this is what the rate says about it,
// stated as a test so that nobody later reads a diacritic error rate of 1 as a
// broken engine when it is a page that never had marks on it.
func TestUnmarkedVietnameseIsATotalDiacriticLoss(t *testing.T) {
	s := Measure("Tiếng Việt", "Tieng Viet")
	if s.DER() != 1 {
		t.Errorf("diacritic error rate is %.3f, want 1", s.DER())
	}
	if s.CER() > 0.6 {
		t.Errorf("character error rate is %.3f, and only the marked characters changed", s.CER())
	}
	if s.Dropped != 0 || s.Added != 0 {
		t.Errorf("nothing was dropped or added and the score says %d and %d", s.Dropped, s.Added)
	}
}

// An evaluation set is one number over all of it rather than an average of
// per page numbers, because a caption and a page of body text are not one vote
// each.
func TestAnEvaluationSetIsOneNumber(t *testing.T) {
	var total Score
	total.Add(Measure("ế", "e"))
	total.Add(Measure(strings.Repeat("ế", 99), strings.Repeat("ế", 99)))

	if total.Chars != 100 || total.Marked != 100 {
		t.Fatalf("two documents came to %d characters and %d marked", total.Chars, total.Marked)
	}
	if got := total.DER(); got != 0.01 {
		t.Errorf("one lost mark in a hundred came to %.4f, want 0.01", got)
	}
	if avg := (1.0 + 0.0) / 2; avg == total.DER() {
		t.Error("the set was scored as an average of the two documents")
	}
}

// A page read perfectly is a score of nothing at all, which is worth asserting
// because every rate in here divides by something that can be zero.
func TestAPerfectReading(t *testing.T) {
	const page = "Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập, tự do, hạnh phúc."
	s := Measure(page, page)
	if s.CER() != 0 || s.DER() != 0 || s.ToneDeletionRate() != 0 {
		t.Errorf("a page against itself came to %+v", s)
	}
	if fails := S4.Check(s); len(fails) != 0 {
		t.Errorf("the gate failed a perfect reading: %v", fails)
	}
	if empty := Measure("", ""); empty.CER() != 0 || empty.DER() != 0 {
		t.Errorf("nothing against nothing came to %+v", empty)
	}
}

// The gate names what failed and by how much, because a checker that says no
// without saying which threshold it means is a checker somebody has to reverse
// engineer under time pressure.
func TestTheGateSaysWhatItFailed(t *testing.T) {
	const page = "Tiếng Việt có sáu thanh điệu và đó là chỗ mọi thứ hỏng."
	fails := S4.Check(Measure(page, "Tieng Viet co sau thanh dieu va do la cho moi thu hong."))
	if len(fails) < 2 {
		t.Fatalf("a page with no tones on it failed %d parts of the gate: %v", len(fails), fails)
	}
	if !strings.Contains(fails[0], "diacritic error rate") {
		t.Errorf("the first thing reported is %q, and the gate is set on the diacritic rate first", fails[0])
	}
	for _, f := range fails {
		if !strings.Contains(f, "%") || !strings.Contains(f, "gate is") {
			t.Errorf("%q does not say what the threshold was", f)
		}
	}
}

// The tone names are what a report is read with, so they are Vietnamese and they
// go into JSON as themselves.
func TestTheTonesAreNamedInVietnamese(t *testing.T) {
	want := []string{"ngang", "huyền", "sắc", "hỏi", "ngã", "nặng"}
	for i, t2 := range Tones() {
		if got := t2.String(); got != want[i] {
			t.Errorf("tone %d is called %q, want %q", i, got, want[i])
		}
		b, err := t2.MarshalText()
		if err != nil || string(b) != want[i] {
			t.Errorf("tone %d marshals to %q, %v", i, b, err)
		}
	}
}
