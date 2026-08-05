package che

// The detectors.
//
// Every one of them works the same way. Find something that could be an
// identifier, then try to prove it is one, and take a cue in the surrounding
// text as proof on its own. What cannot be proved either way is left in the
// text, and what that costs is written down in the recall measurement rather
// than assumed away.

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tamnd/gao/phoi"
)

var emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

// numberRun matches a run of digits with the separators Vietnamese writers put
// inside them: a space, a full stop, or a hyphen. A slash is deliberately not a
// separator here, because a slash between digits is a date or a document number
// and neither is an identifier.
var numberRun = regexp.MustCompile(`\+?\d+(?:[ .\-]\d+)*`)

// platePattern matches a Vietnamese registration plate in either the five digit
// or the four digit series. The letter is what makes this findable: no ordinary
// number in Vietnamese text has a capital letter wedged between a two digit
// prefix and a five digit tail.
var platePattern = regexp.MustCompile(`\b(\d{2})[ \-]?([A-Z]{1,2}\d?)[ \-]{0,3}(\d{3}[. ]?\d{2}|\d{4,5})\b`)

func findEmails(text string) []Span {
	at := emailPattern.FindAllStringIndex(text, -1)
	found := make([]Span, 0, len(at))
	for _, m := range at {
		found = append(found, Span{Start: m[0], End: m[1], Kind: KindEmail, Text: text[m[0]:m[1]]})
	}
	return found
}

// findNumbers classifies every digit run in the document. One pass rather than
// one pass per identifier, because the identifiers compete for the same digits
// and deciding between them once is both faster and more predictable than
// letting four detectors match and sorting out the overlaps afterwards.
func findNumbers(text string) []Span {
	lowered := strings.ToLower(text)
	var found []Span
	for _, m := range numberRun.FindAllStringIndex(text, -1) {
		raw := text[m[0]:m[1]]
		if !groupsLookDeliberate(raw) {
			continue
		}
		digits := onlyDigits(raw)
		kind, cued, ok := classify(digits, lowered, m[0], m[1])
		if !ok {
			continue
		}
		found = append(found, Span{Start: m[0], End: m[1], Kind: kind, Text: raw, Cued: cued})
	}
	return found
}

// groupsLookDeliberate rejects a run whose separators were not separating one
// number into groups. A table of small numbers separated by spaces reads to the
// scanner as one long run, and the tell is a group of a single digit: nobody
// writes a phone number or a tax code with a lone digit in the middle of it.
func groupsLookDeliberate(raw string) bool {
	if !strings.ContainsAny(raw, " .-") {
		return true
	}
	for _, group := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == '.' || r == '-'
	}) {
		group = strings.TrimPrefix(group, "+")
		if len(group) < 2 {
			return false
		}
	}
	return true
}

// classify decides what a bare string of digits is. The order is by how much
// structure each answer requires, most first, since the strongest explanation
// of the same digits is the right one.
func classify(digits, lowered string, start, end int) (Kind, bool, bool) {
	switch len(digits) {
	case 12:
		cued := cuedBefore(lowered, start, KindCCCD)
		if cued || cccdProvinces[digits[:3]] {
			return KindCCCD, cued, true
		}
	case 13:
		// A tax code with a branch suffix. There is nothing in thirteen digits
		// to validate beyond the ten digit code inside it, so this one needs to
		// be told what it is.
		if cuedBefore(lowered, start, KindTax) {
			return KindTax, true, true
		}
	case 11:
		if k, ok := asPhone(digits); ok {
			return k, false, true
		}
		if cuedBefore(lowered, start, KindPhone) {
			return KindPhone, true, true
		}
	case 10:
		if k, ok := asPhone(digits); ok {
			return k, false, true
		}
		if k, ok := nearestCue(lowered, start, KindPhone, KindTax); ok {
			return k, true, true
		}
		// A bare ten digit number is a tax code only if it passes the check
		// digit and is not money. This is the one place a validator decides on
		// its own, and it decides in the safe direction: getting it wrong
		// covers a number that was not personal.
		if taxChecksOut(digits) && !followedByCurrency(lowered, end) {
			return KindTax, false, true
		}
	case 9:
		// Nine digits is the old national ID and also every fifth number in a
		// price list, a stock code, and a phone number missing its leading
		// zero. There is no province table for the old numbering that holds up,
		// so this one is only ever found by being named.
		if k, ok := nearestCue(lowered, start, KindCMND, KindPhone); ok {
			return k, true, true
		}
	}
	return "", false, false
}

// asPhone reports whether the digits are a Vietnamese telephone number, in
// national or international form.
func asPhone(digits string) (Kind, bool) {
	// International form: 84 followed by the national number without its zero.
	if len(digits) == 11 && strings.HasPrefix(digits, "84") {
		return asPhone("0" + digits[2:])
	}
	if !strings.HasPrefix(digits, "0") {
		return "", false
	}
	switch {
	case len(digits) == 10 && mobilePrefixes[digits[1:3]]:
		return KindPhone, true
	case (len(digits) == 10 || len(digits) == 11) && digits[1] == '2':
		// Landlines all sit under area codes beginning with two, at ten or
		// eleven digits depending on how long the area code is.
		return KindPhone, true
	}
	return "", false
}

// taxWeights are the positional weights of the check digit on a ten digit
// Vietnamese tax code.
var taxWeights = [9]int{31, 29, 23, 19, 17, 13, 7, 5, 3}

func taxChecksOut(digits string) bool {
	if len(digits) != 10 {
		return false
	}
	sum := 0
	for i := 0; i < 9; i++ {
		n, err := strconv.Atoi(digits[i : i+1])
		if err != nil {
			return false
		}
		sum += n * taxWeights[i]
	}
	want := 10 - sum%11
	if want > 9 {
		return false
	}
	return want == int(digits[9]-'0')
}

// followedByCurrency reports whether the next word after a number says it was
// money. Ten digits is what a billion dong looks like written out, and a corpus
// of Vietnamese web text is largely prices.
func followedByCurrency(lowered string, end int) bool {
	rest := lowered[min(end, len(lowered)):]
	rest = strings.TrimLeft(rest, " \t")
	for unit := range currencies {
		if strings.HasPrefix(rest, unit) {
			return true
		}
	}
	return false
}

func findPlates(text string) []Span {
	var found []Span
	for _, m := range platePattern.FindAllStringSubmatchIndex(text, -1) {
		province, err := strconv.Atoi(text[m[2]:m[3]])
		if err != nil || province < 11 || province == 13 {
			continue
		}
		found = append(found, Span{Start: m[0], End: m[1], Kind: KindPlate, Text: text[m[0]:m[1]]})
	}
	return found
}

// houseNumber opens an address. Either the number is announced, or it sits in
// front of a word that says what kind of way it is on. Both spellings of every
// word are in the pattern, because an address typed without tone marks is still
// an address and it is the commonest way an address gets typed.
var houseNumber = regexp.MustCompile(`(?i)(?:số nhà|so nha|số|so)\s*\d+[a-zA-Z]?(?:/\d+[a-zA-Z]?)*` +
	`|\d+[a-zA-Z]?(?:/\d+[a-zA-Z]?)*\s+(?:đường|duong|phố|pho|ngõ|ngo|ngách|ngach|hẻm|hem)\b`)

// findAddresses walks the chain of administrative units a Vietnamese address is
// written as. The chain runs from the smallest unit to the largest, each link
// says what kind of unit it is, and that is what makes an address findable
// without a gazetteer of every street in the country. A chain that never
// reaches a ward or higher is a house number in a sentence rather than an
// address.
func findAddresses(text string) []Span {
	var found []Span
	for _, m := range houseNumber.FindAllStringIndex(text, -1) {
		end, reached := walkChain(text, m[1])
		if !reached {
			continue
		}
		found = append(found, Span{Start: m[0], End: end, Kind: KindAddress, Text: text[m[0]:end]})
	}
	return found
}

// walkChain extends an address from the house number through the comma
// separated links that follow it, and reports whether it got as far as an
// administrative unit rather than stopping at a street.
func walkChain(text string, at int) (int, bool) {
	end, reached := at, false
	for at < len(text) {
		rest := text[at:]
		comma := strings.IndexAny(rest, ",\n")
		if comma < 0 || rest[comma] == '\n' {
			break
		}
		next := at + comma + 1
		stop := len(text)
		if i := strings.IndexAny(text[next:], ",\n."); i >= 0 {
			stop = next + i
		}
		segment := strings.ToLower(strings.TrimSpace(text[next:stop]))
		unit, ok := opensAdministrativeUnit(segment)
		if !ok {
			break
		}
		end, at = stop, stop
		if unit {
			reached = true
		}
	}
	return end, reached
}

// administrativeUnits are the links that make a chain an address rather than a
// street. A ward is enough: an address that names a ward names a place small
// enough to matter.
var administrativeUnits = []string{
	"phường", "xã", "thị trấn", "thị xã", "quận", "huyện", "thành phố",
	"tỉnh", "tp.", "tp ", "q.", "p.", "kp.",
}

// opensAdministrativeUnit reports whether a segment continues an address, and
// whether the link it opens with is an administrative unit rather than a street
// or a block.
func opensAdministrativeUnit(segment string) (bool, bool) {
	if segment == "" {
		return false, false
	}
	bare := phoi.Bare(segment)
	if opensWith(segment, bare, notAWay) {
		return false, false
	}
	if opensWith(segment, bare, administrativeUnits) {
		return true, true
	}
	if provinces.has(segment) {
		return true, true
	}
	if opensWith(segment, bare, addressCues) {
		return false, true
	}
	return false, false
}

// opensWith reports whether a segment begins with one of the words, written
// with its tone marks or without them.
func opensWith(segment, bare string, words []string) bool {
	for _, w := range words {
		if strings.HasPrefix(segment, w) || strings.HasPrefix(bare, phoi.Bare(w)) {
			return true
		}
	}
	return false
}

// notNameSyllables are words that begin a label rather than continue a name.
// Without them a name that runs straight into the next field takes the field
// label with it, since "Điện" is a perfectly good Vietnamese given name and
// "Điện thoại" is not a person.
var notNameSyllables = bareOf([]string{
	"Điện", "Đt", "Sđt", "Số", "Địa", "Gọi", "Hotline", "Email",
	"Zalo", "Tel", "Mã", "Đường", "Phố", "Ngõ", "Hẻm", "Phường", "Xã", "Quận",
	"Huyện", "Tỉnh", "Tp", "Cccd", "Cmnd", "Mst", "Căn", "Nhà", "Ngày", "Tháng",
})

// markedNotName is the other half of that list: the label words whose bare form
// is an ordinary Vietnamese given name.
//
// Năm means year and Nam is one of the commonest given names in the country,
// and with the tone marks off they are the same four letters. So are Thành and
// Thanh, Giá and Gia, Công and Công, Liên and Liên. Matching those on the bare
// form ends a name one syllable early every time somebody is called Nam, which
// is a real name lost to protect against a field label. These are matched as
// they are written instead, which is exact in text that carries its marks and
// gives up in text that does not. That is the right way round: the failure it
// allows is a name that runs one syllable into a label and gets covered, which
// costs a little of the sentence, and the failure it prevents is a person's
// name published in full.
var markedNotName = set([]string{
	"năm", "tháng", "thành", "chứng", "giá", "bán", "mua", "diện", "công", "liên",
})

// blocksName reports whether a word ends a name rather than continuing it.
func blocksName(word string) bool {
	return notNameSyllables.has(word) || markedNotName[strings.ToLower(word)]
}

// beforeName are the words that turn what follows into a place rather than a
// person. Vietnamese streets, wards and schools are named after people, so
// "đường Lê Lợi" is a street and "Lê Lợi" on its own is an emperor, and neither
// is somebody whose privacy is at stake.
var beforeName = bareOf([]string{
	"đường", "phố", "ngõ", "ngách", "hẻm", "phường", "xã", "quận", "huyện",
	"tỉnh", "thị", "trường", "chợ", "cầu", "bệnh", "công", "khu", "quảng",
})

// businessMarkers say that what follows is the name of a company rather than
// of a person. Vietnamese companies are routinely named after their founder, so
// "Công ty TNHH Thương mại Hoàng Long" ends in something indistinguishable from
// a person's name and is not one. A company is not a natural person and its
// registered name is public by law, so covering it would be removing published
// information from the corpus for nothing.
var businessMarkers = bareOf([]string{
	"công ty", "tnhh", "cổ phần", "doanh nghiệp", "cửa hàng", "trung tâm",
	"nhà máy", "xí nghiệp", "hợp tác xã", "tập đoàn", "chi nhánh", "cty",
	"showroom", "garage", "salon", "khách sạn", "nhà hàng", "quán",
})

// beforePerson are the words that introduce a person, and they beat the
// business markers because they sit between the business and the name. A quán
// is a business and the chủ quán is the person who owns it, so "chủ quán tên
// Trịnh Văn Đức" names somebody and "quán Trịnh Văn Đức" is a sign over a door.
var beforePerson = bareOf([]string{
	"chủ", "tên", "anh", "chị", "ông", "bà", "cô", "chú", "bác", "em", "cháu",
	"người", "gặp", "hỏi", "chính chủ", "đại diện", "giám đốc", "kế toán",
})

// namesABusiness reports whether the words before a candidate name make it a
// business name. The scan stops at the start of the line, since a company on
// one line of a contract says nothing about the person named on the next, and
// it stops early at anything that introduces a person.
func namesABusiness(before []word, from int) bool {
	for i := len(before) - 1; i >= 0 && before[i].start >= from; i-- {
		w := strings.ToLower(before[i].text)
		if beforePerson.has(w) {
			return false
		}
		if businessMarkers.has(w) {
			return true
		}
		if i > 0 && businessMarkers.has(strings.ToLower(before[i-1].text)+" "+w) {
			return true
		}
	}
	return false
}

func lineStart(text string, at int) int {
	if i := strings.LastIndexByte(text[:at], '\n'); i >= 0 {
		return i + 1
	}
	return 0
}

// findNames applies the co-occurrence policy. A name is personal data when it
// sits beside a way of reaching the person, and is otherwise the subject of a
// sentence. So the paragraph has to hold an identifier before any name in it is
// a candidate at all.
//
// A paragraph here is a block separated by a blank line rather than a line,
// because the shape this is aimed at is a contact block, and a contact block
// puts the name on one line and the number on the next.
func findNames(text string, identifiers []Span) []Span {
	var found []Span
	for _, para := range paragraphs(text) {
		if !holdsIdentifier(identifiers, para.start, para.end) {
			continue
		}
		found = append(found, namesIn(text, para.start, para.end)...)
	}
	return found
}

type block struct{ start, end int }

func paragraphs(text string) []block {
	var out []block
	start, blank := 0, 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) {
			if i > start {
				out = append(out, block{start, i})
			}
			break
		}
		if text[i] != '\n' {
			blank = 0
			continue
		}
		blank++
		if blank < 2 {
			continue
		}
		if i-1 > start {
			out = append(out, block{start, i - 1})
		}
		start, blank = i+1, 0
	}
	if len(out) == 0 && len(text) > 0 {
		out = append(out, block{0, len(text)})
	}
	return out
}

func holdsIdentifier(found []Span, start, end int) bool {
	for _, s := range found {
		if s.Start >= start && s.End <= end && s.Kind != KindAddress && s.Kind != KindName {
			return true
		}
	}
	return false
}

// namesIn finds every surname headed run of capitalized syllables in a block.
func namesIn(text string, start, end int) []Span {
	words := wordsIn(text, start, end)
	var found []Span
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !surnames.has(w.text) || !startsUpper(w.text) {
			continue
		}
		if i > 0 && beforeName.has(words[i-1].text) {
			continue
		}
		if namesABusiness(words[:i], lineStart(text, w.start)) {
			continue
		}
		last := i
		for j := i + 1; j < len(words) && j-i < 4; j++ {
			next := words[j]
			if next.start != words[j-1].end+1 || !startsUpper(next.text) {
				break
			}
			if blocksName(next.text) {
				break
			}
			last = j
		}
		if last == i {
			continue
		}
		found = append(found, Span{
			Start: w.start, End: words[last].end, Kind: KindName,
			Text: text[w.start:words[last].end],
		})
		i = last
	}
	return found
}

type word struct {
	text       string
	start, end int
}

// wordsIn splits a block into runs of letters with their offsets. Digits and
// punctuation end a word, which is what stops a name from running through a
// phone number.
func wordsIn(text string, start, end int) []word {
	var out []word
	at := start
	for at < end {
		r, size := utf8.DecodeRuneInString(text[at:])
		if !unicode.IsLetter(r) {
			at += size
			continue
		}
		from := at
		for at < end {
			r, size = utf8.DecodeRuneInString(text[at:])
			if !unicode.IsLetter(r) {
				break
			}
			at += size
		}
		out = append(out, word{text: text[from:at], start: from, end: at})
	}
	return out
}

func startsUpper(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

func onlyDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
