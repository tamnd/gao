package count

// The coverage set: the text a tokenizer is put through before it is put
// through a corpus.
//
// The gate suite has a failure mode that looks like a pass. A gate that found
// nothing in the sample to run on reports itself as not run, and a run over a
// few thousand documents of clean modern web text will leave four of the ten
// there: nothing mixed Vietnamese with English and code, nothing arrived
// decomposed, nothing came out of a legacy encoding, and the rarer letters never
// appeared at all. The suite says so rather than pretending otherwise, which is
// why [Gate.Ran] exists, but a suite that has to be pointed at hundreds of
// gigabytes before it can answer is a suite nobody runs while changing a
// tokenizer.
//
// The coverage set is the fix. It is a few kilobytes, it is fixed, and it holds
// one document for each thing a gate needs to see: every letter of the language,
// the same letters decomposed, the mixed text T3 asks for, digit runs in
// different company, and the text a document arrives as when it comes out of
// each of the legacy encodings phoi reads. Running it takes a millisecond and
// leaves no gate unrun.
//
// It is not a sample of the corpus and no number measured on it is a number
// about gao. Fertility on this set is fertility on a letter chart. What it
// answers is the prior question, which is whether the suite is measuring
// anything at all.

import (
	_ "embed"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/tamnd/gao/doc"
)

// The three legacy documents, which are the same text phoi's own golden files
// carry after transcoding.
//
// They are copied here rather than read from phoi's testdata because one
// package cannot embed another's files, and a fixture that reaches across a
// directory at run time breaks the first time somebody reorganizes the other
// package. The copies are checked against the originals by a test, so a copy
// that drifts is a failure rather than a surprise.
var (
	//go:embed testdata/coverage/song-hong.txt
	songHong string

	//go:embed testdata/coverage/pho-co-ha-noi.txt
	phoCoHaNoi string

	//go:embed testdata/coverage/cong-bao.txt
	congBao string
)

// A CoverageDoc is one document of the set, with what it is there to cover.
type CoverageDoc struct {
	// Name is what a report calls it.
	Name string

	// Why is what this document is in the set for, in a sentence. It is not
	// decoration: a coverage set nobody can read is a coverage set that grows
	// documents nobody can remove.
	Why string

	// Charsets names the legacy encodings a document of this text arrives
	// from, empty for the documents that are not about an encoding.
	Charsets []string

	Text string
}

// ID is the document identity, which is what the gate suite keys its sampling
// and its failure reports on.
func (c CoverageDoc) ID() doc.Hash { return doc.SumString(c.Text) }

// Coverage is the set, in a fixed order.
//
// The order is fixed and the contents are fixed, which is what lets the same
// run on four boxes be compared as four numbers rather than four reports. A
// difference between them is then a difference in the build or the locale or
// the tokenizer file, and never in what was read.
func Coverage() []CoverageDoc {
	return []CoverageDoc{
		{
			Name: "chu-cai",
			Why:  "every one of the 134 letters of Vietnamese, so that T4 and T5 see the marks that are rare in a web sample and common in a book",
			Text: letterChart(),
		},
		{
			Name: "chu-cai-tach",
			Why:  "the same letters with their marks written separately, which is what T6 needs and what a document out of a Mac word processor arrives as",
			Text: norm.NFD.String(letterChart()),
		},
		{
			Name:     "song-hong",
			Why:      "a paragraph of ordinary prose, and the text all six legacy encodings carry once they are transcoded",
			Charsets: []string{"TCVN3", "VNI-WIN", "VPS", "VISCII", "BK HCM1", "BK HCM2"},
			Text:     songHong,
		},
		{
			Name:     "pho-co-ha-noi",
			Why:      "a real page from 1998 that reached the corpus as mojibake, in lower case throughout because TCVN3 keeps its capitals in a second font rather than in the file",
			Charsets: []string{"TCVN3"},
			Text:     phoCoHaNoi,
		},
		{
			Name:     "cong-bao",
			Why:      "an official header in capitals, which is where three of these encodings put the codes windows-1252 leaves undefined and where a decoder loses ẮẾỐ without saying so",
			Charsets: []string{"VISCII"},
			Text:     congBao,
		},
		{
			Name: "khong-dau",
			Why:  "Vietnamese typed without its marks, which is a third of what a forum holds and what T2 asks the tokenizer about",
			Text: unmarked,
		},
		{
			Name: "lap-trinh",
			Why:  "Vietnamese, English and code in one document, which is the T3 case and the place a round trip breaks if it is going to",
			Text: mixedText,
		},
		{
			Name: "con-so",
			Why:  "the same digit runs in a date, a price, a decree number and a plain count, which is the only way T7 can tell grouping from context dependence",
			Text: digitsText,
		},
	}
}

// letterChart writes out the alphabet.
//
// Vietnamese has 134 letters: twelve vowel bases, each carrying one of five
// tones or none, plus đ, and the same again in capitals. Sixty-seven of them are
// lower case and six of the twelve unmarked bases are plain ASCII, which is how
// 12 by 6 comes to 67 rather than to 72.
//
// The chart is built rather than typed. A hand-typed list of 134 characters is a
// list with a letter missing in it, and the missing one is always one of the
// rare ones, which is exactly what the chart exists to carry.
func letterChart() string {
	var b strings.Builder
	b.WriteString("Bảng chữ cái tiếng Việt, đủ cả dấu thanh và dấu phụ.\n")
	for _, base := range vowelBases {
		for i, mark := range toneMarks {
			if i > 0 {
				b.WriteByte(' ')
			}
			lower := compose(base, mark)
			b.WriteString(lower)
			b.WriteByte(' ')
			b.WriteString(strings.ToUpper(lower))
		}
		b.WriteByte('\n')
	}
	b.WriteString("đ Đ\n")
	return b.String()
}

// vowelBases are the twelve vowels that take tone marks. The consonants and the
// unmarked ASCII vowels are in the prose of the other documents.
const vowelBases = "aăâeêioôơuưy"

// toneMarks are the five tones and the absence of one, as combining characters,
// in the order a Vietnamese primary school teaches them: ngang, huyền, hỏi,
// ngã, sắc, nặng.
var toneMarks = []rune{0, 0x0300, 0x0309, 0x0303, 0x0301, 0x0323}

// compose puts a mark on a base and returns the precomposed character.
func compose(base, mark rune) string {
	if mark == 0 {
		return string(base)
	}
	return norm.NFC.String(string([]rune{base, mark}))
}

// Letters is every precomposed Vietnamese letter the chart carries, in the order
// it carries them. It is exported because the coverage set is only as good as
// its inventory, and an inventory nobody can check against a second source is an
// assertion.
func Letters() []rune {
	var out []rune
	for _, r := range letterChart() {
		if r < utf8.RuneSelf || !unicode.IsLetter(r) {
			continue
		}
		if !containsRune(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func containsRune(rs []rune, r rune) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}

// The three written documents. They are written rather than taken from the
// corpus because each one has to hold a specific thing, and finding a real
// document that holds it is harder than writing one that does and saying so.

// unmarked is Vietnamese as it is typed when the keyboard is not set up for it,
// which is not a corruption and not a different language. It is what T2 asks
// about, and the tokenizer has to come back with the same bytes.
const unmarked = `Hom nay troi dep, minh di uong ca phe o pho co roi ve som nhe.
Ai co tai lieu on thi dai hoc mon toan thi cho minh xin voi, cam on nhieu.
Gia phong tro khu vuc Cau Giay bay gio khoang bao nhieu mot thang vay moi nguoi?
`

// mixedText is the T3 case. The three things have to be in one document rather
// than in three, because what the gate is about is the tokenizer switching
// between them.
const mixedText = `Đội mình đang viết lại phần ingest bằng Go, dùng ` + "`json.Unmarshal`" + ` cho cấu hình.
The pipeline reads a manifest, validates it, and writes Parquet parts to object storage.
Cấu hình mẫu: {"batch_size": 512, "workers": 8, "output": "s3://gao/parts/"}
Chạy thử bằng ` + "`go test ./... -run TestIngest -count=1`" + ` rồi xem log nhé.
Nếu gặp lỗi ` + "`context deadline exceeded`" + ` thì tăng timeout lên 30s là được.
`

// digitsText is the T7 case. The same runs appear in four kinds of company,
// because the gate is not about how a tokenizer groups digits, which is a
// defensible choice either way, but about whether it groups the same run the
// same way wherever it finds it.
const digitsText = `Nghị định 15/2020/NĐ-CP có hiệu lực từ ngày 15 tháng 4 năm 2020.
Giá thuê là 2020 nghìn đồng một tháng, tăng 15 phần trăm so với năm ngoái.
Dân số thành phố năm 2020 là 8 993 082 người, gấp 15 lần con số năm 1954.
Mã bưu chính 100000, số điện thoại 024 3825 2020, phòng 15 tầng 4.
`
