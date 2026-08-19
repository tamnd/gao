package sift

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/reject"
)

// The labeled set is the whole argument for this piece of work, so the first
// test is the set, run end to end, with the number printed whether it passes or
// not. What is in each document and why it is there is in
// testdata/langid/README.md.
func TestTheLabelledSet(t *testing.T) {
	right, wrong := 0, 0
	for _, c := range []struct {
		dir  string
		want bool
	}{
		{"vietnamese", true},
		{"other", false},
	} {
		files, err := filepath.Glob(filepath.Join("testdata", "langid", c.dir, "*.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("no documents in testdata/langid/%s", c.dir)
		}
		for _, f := range files {
			text, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			l := Identify(string(text))
			if l.Vietnamese() != c.want {
				wrong++
				t.Errorf("%s: Vietnamese is %v and the document is labeled %v. %d tokens, %.3f syllables, %.3f bare, %.3f marked, %d function words, %d of them marked",
					filepath.Base(f), l.Vietnamese(), c.want, l.Tokens, l.Rate(), l.BareRate(), l.MarkRate(), l.StopWords, l.MarkedStopWords)
				continue
			}
			right++
		}
	}
	t.Logf("%d of %d documents in the labeled set are called right", right, right+wrong)
}

// The margin is what says whether the numbers above are a result or a
// coincidence. A set this size can be fitted by accident, and a threshold
// sitting one point away from a document on either side would be fitted whether
// anybody meant to or not.
func TestTheLabelledSetIsDecidedByAMarginAndNotByAPoint(t *testing.T) {
	worstVietnamese, bestOther := 1.0, 0.0
	for _, c := range []struct {
		dir  string
		want bool
	}{
		{"vietnamese", true},
		{"other", false},
	} {
		files, _ := filepath.Glob(filepath.Join("testdata", "langid", c.dir, "*.txt"))
		for _, f := range files {
			text, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			l := Identify(string(text))
			rate := l.Rate()
			if l.MarkRate() < MinMarkRate {
				rate = l.BareRate()
			}
			if c.want && rate < worstVietnamese {
				worstVietnamese = rate
			}
			if !c.want && rate > bestOther {
				bestOther = rate
			}
		}
	}
	t.Logf("the worst Vietnamese document scores %.3f and the best of the others scores %.3f", worstVietnamese, bestOther)
	if worstVietnamese-bestOther < 0.25 {
		t.Errorf("the two classes are %.3f apart, which is close enough that the thresholds are fitted rather than chosen", worstVietnamese-bestOther)
	}
}

// Vietnamese typed without tone marks is the case this identifier exists for.
// It is most of what gets typed on a phone, every model trained on news text
// calls it something else, and the pipeline that drops it drops the register
// that most Vietnamese people write in most days.
func TestVietnameseWithoutToneMarksIsVietnamese(t *testing.T) {
	l := Identify(unmarked)
	if !l.Vietnamese() {
		t.Errorf("unmarked Vietnamese was not called Vietnamese: %.3f bare, %d function words", l.BareRate(), l.StopWords)
	}
	if l.MarkRate() >= MinMarkRate {
		t.Errorf("the unmarked fixture carries marks on %.3f of its tokens, so it is not testing what it says", l.MarkRate())
	}
	if marked := Identify(article); !marked.Vietnamese() {
		t.Error("the same document with its marks on was not called Vietnamese")
	}
}

// The reverse mistake, and the one that is more expensive: text that is not
// Vietnamese but is full of Vietnamese words. An English article about Vietnam
// carries the place names and the personal names with their marks taken off,
// which is exactly the shape unmarked Vietnamese has.
func TestVietnameseNamesInAnotherLanguageAreNotVietnamese(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("testdata", "langid", "other", "ten-viet-trong-tieng-anh.txt"))
	if err != nil {
		t.Fatal(err)
	}
	l := Identify(string(text))
	if l.Vietnamese() {
		t.Errorf("English with Vietnamese names in it was called Vietnamese: %.3f bare, %d function words", l.BareRate(), l.StopWords)
	}
	if l.Bare == 0 {
		t.Error("no token in it matched a Vietnamese syllable, so the document is not testing the case it is here for")
	}
}

// Vietnamese with the English terms left in is the other case, and it is the one
// that decides where the marked threshold sits. A page of Vietnamese technical
// writing is a third English by token, and it is a Vietnamese document.
func TestVietnameseWithTheEnglishLeftInIsStillVietnamese(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("testdata", "langid", "vietnamese", "chen-tieng-anh.txt"))
	if err != nil {
		t.Fatal(err)
	}
	l := Identify(string(text))
	if !l.Vietnamese() {
		t.Errorf("code switched Vietnamese was not called Vietnamese: %.3f syllables, %d marked function words", l.Rate(), l.MarkedStopWords)
	}
	if l.Rate() > 0.9 {
		t.Errorf("%.3f of the tokens are Vietnamese syllables, so the fixture is not code switched enough to test anything", l.Rate())
	}
}

// A document with no tokens in it is not Vietnamese, and asking is not an error.
// Every rate here divides by the token count and an empty document is the one
// that arrives from an extractor that found nothing.
func TestAnEmptyDocumentIsNotVietnamese(t *testing.T) {
	for _, text := range []string{"", "   \n\n  ", "12 34 56 ---"} {
		if l := Identify(text); l.Vietnamese() {
			t.Errorf("%q was called Vietnamese", text)
		}
	}
}

// The verdict has to reach the reject store as a language rejection, with a
// message that says which of the two bars it missed, because the whole point of
// writing rejections down is being able to ask what a threshold cost.
func TestARejectedDocumentSaysWhichBarItMissed(t *testing.T) {
	limits := Default()
	limits.MinSyllables = 10
	limits.MinStopWords = 0

	for _, c := range []struct {
		name string
		text string
		want string
	}{
		{"another language", english, "of tokens are Vietnamese syllables"},
		{"romanized Chinese", pinyin, "written without tone marks"},
	} {
		t.Run(c.name, func(t *testing.T) {
			reason, detail, ok := limits.Reject(Measure(c.text))
			if !ok {
				t.Fatal("the document was kept")
			}
			if reason != reject.ReasonLanguage {
				t.Fatalf("filed under %q, want %q", reason, reject.ReasonLanguage)
			}
			if !strings.Contains(detail, c.want) {
				t.Errorf("the reason reads %q and does not say %q", detail, c.want)
			}
		})
	}
}

// The identifier can be turned off, and this is the test that keeps it that way.
// The claim on the board is that it admits documents a stock model rejects, and
// the only way to measure that is to run the same corpus twice.
func TestTheIdentifierCanBeTurnedOff(t *testing.T) {
	limits := Default()
	limits.MinSyllables = 10
	limits.MinStopWords = 0

	r := Measure(pinyin)
	if _, _, ok := limits.Reject(r); !ok {
		t.Fatal("romanized Chinese went through with the identifier on")
	}
	limits.Identify = false
	if _, detail, ok := limits.Reject(r); ok {
		t.Errorf("it was still rejected with the identifier off, as %q", detail)
	}
}

// pinyin is the hardest of the negatives and it is here in the code rather than
// in a file because of what it is for. Romanized Chinese is written one syllable
// at a time with a space between them, the syllables are short and open, and
// about half of them are also Vietnamese syllables. It is the document that sets
// the unmarked threshold.
const pinyin = `zhong guo you hen duo da cheng shi, mei ge cheng shi dou you zi ji de li shi he wen hua.
wo men zuo tian qu le bei jing, kan le gu gong he chang cheng, ren tai duo le, pai dui pai le hen jiu.
jin tian wo men da suan qu shang hai, zuo huo che yao si ge xiao shi, peng you shuo shang hai de dong xi bi jiao gui.
wo xiang qu kan kan wai tan, ting shuo wan shang de deng guang hen piao liang.`
