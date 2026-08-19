package cover

import (
	"strings"
	"testing"
)

// kinds returns what was found, so that a test can say what it expected without
// spelling out offsets nobody can check by eye.
func kinds(found []Span) map[Kind]int {
	out := map[Kind]int{}
	for _, s := range found {
		out[s.Kind]++
	}
	return out
}

func texts(found []Span, k Kind) []string {
	var out []string
	for _, s := range found {
		if s.Kind == k {
			out = append(out, s.Text)
		}
	}
	return out
}

// The document this stage exists for. A classified advertisement is the densest
// personal data on the Vietnamese web, written by the person it belongs to, and
// everything in it has to come out.
func TestAClassifiedAdvertisementGivesUpEverythingInIt(t *testing.T) {
	got := kinds(Find(advert))
	for _, want := range []Kind{KindName, KindPhone, KindEmail, KindPlate, KindAddress} {
		if got[want] == 0 {
			t.Errorf("an advertisement with a %s in it yielded none", want)
		}
	}
}

// The document that decides whether this stage is usable. A news article holds
// names, dates, prices and street names, none of which is personal data, and a
// detector that matches digit runs turns it into a page of tags.
func TestANewsArticleComesBackUntouched(t *testing.T) {
	out, found := Redact(article, L2)
	if len(found) != 0 {
		t.Errorf("a news article yielded %d spans: %v", len(found), found)
	}
	if out != article {
		t.Error("a news article was changed by a stage that found nothing in it")
	}
}

// The other half of the same test, on the page a Vietnamese corpus is mostly
// made of. Prices, volumes, document numbers and flight numbers are all digit
// runs and none of them belongs to anybody.
func TestAPageOfNumbersIsNotAPageOfIdentifiers(t *testing.T) {
	out, found := Redact(prices, L2)
	if len(found) != 0 {
		t.Errorf("a page of prices yielded %d spans: %v", len(found), found)
	}
	if out != prices {
		t.Error("a page of prices was changed")
	}
}

// A phone number is found by its carrier prefix, which is what separates it
// from the ten digit numbers a corpus is full of.
func TestAPhoneNumberIsFoundByItsCarrierPrefix(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"Gọi 0912345678 nhé.", true},
		{"Gọi 0912 345 678 nhé.", true},
		{"Gọi 090.123.4567 nhé.", true},
		{"Số +84 912 345 678 là của tôi.", true},
		{"Máy bàn 024 3823 4567 vẫn dùng.", true},
		{"Máy bàn 0236 3888 999 ở Đà Nẵng.", true},
		{"Mã sản phẩm 0412345678 đã hết hàng.", false},
		{"Số hiệu 1912345678 trên giấy tờ.", false},
	} {
		got := kinds(Find(tc.text))[KindPhone] > 0
		if got != tc.want {
			t.Errorf("%q: found a phone %v, want %v", tc.text, got, tc.want)
		}
	}
}

// The national ID is found by its province code, and the list of province codes
// is what keeps every twelve digit number in the corpus from being one.
func TestTheNationalIDIsFoundByItsProvinceCode(t *testing.T) {
	if kinds(Find("Số 079187654321 ghi trên thẻ."))[KindCCCD] != 1 {
		t.Error("a national ID opening with a real province code was not found")
	}
	if len(Find("Mã đơn hàng 999187654321 đã giao.")) != 0 {
		t.Error("a twelve digit order number was taken for a national ID")
	}
}

// The old nine digit ID has no province table that holds up, so it is only ever
// found by being named. That is a recall limit and it is a deliberate one: nine
// digits is also every fifth number on a price page.
func TestTheOldNineDigitIDIsOnlyFoundWhenItIsNamed(t *testing.T) {
	if kinds(Find("CMND số 023456789 cấp năm 2010."))[KindCMND] != 1 {
		t.Error("a nine digit ID that was named as one was not found")
	}
	if len(Find("Mã hồ sơ 023456789 đã tiếp nhận.")) != 0 {
		t.Error("a bare nine digit number was taken for an ID")
	}
}

// The check digit is the one place a validator decides on its own, and it only
// ever decides to cover something. A cue covers a tax code whether or not the
// digits check out, so a wrong check digit rule costs precision on bare numbers
// rather than leaving a real tax code in a published corpus.
func TestTheTaxCheckDigitOnlyEverAddsToWhatIsCovered(t *testing.T) {
	if !taxChecksOut("0311234561") {
		t.Fatal("the fixture does not satisfy the check digit, so this test is about nothing")
	}
	if kinds(Find("Công ty có mã 0311234561 trong hồ sơ."))[KindTax] != 1 {
		t.Error("a bare tax code that passes the check digit was not found")
	}
	if kinds(Find("Mã số thuế: 0311234567"))[KindTax] != 1 {
		t.Error("a tax code that was named as one was not found, so the cue is not enough on its own")
	}
	if taxChecksOut("0311234567") {
		t.Error("the second fixture passes the check digit, so it does not test the cue path")
	}
}

// Ten digits is what a billion dong looks like written out, and a Vietnamese
// corpus is largely prices.
func TestMoneyIsNotATaxCode(t *testing.T) {
	for _, text := range []string{
		"Tổng cộng 1.500.000.000 đồng cho cả hợp đồng.",
		"Giá bán 3111234561 đồng theo niêm yết.",
	} {
		if got := kinds(Find(text))[KindTax]; got != 0 {
			t.Errorf("%q: %d tax codes found in a price", text, got)
		}
	}
}

// A table of small numbers reads to the scanner as one long run, and the tell
// is a group of a single digit.
func TestATableOfSmallNumbersIsNotOneLongNumber(t *testing.T) {
	if found := Find("Kết quả 1 23 45 6 78 90 12 3 45 67 89."); len(found) != 0 {
		t.Errorf("a row of small numbers yielded %v", found)
	}
}

// The co-occurrence policy, which is the reason this package does not simply
// redact every name it can find. A corpus with no Vietnamese names in it is not
// a Vietnamese corpus.
func TestANameIsOnlyPersonalDataBesideAWayOfReachingThePerson(t *testing.T) {
	alone := "Nhà thơ Nguyễn Du sinh năm 1765 và để lại Truyện Kiều."
	if got := kinds(Find(alone))[KindName]; got != 0 {
		t.Errorf("a poet in a sentence about poetry was redacted as personal data")
	}

	beside := "Chị Nguyễn Thu Trang, số điện thoại 0987654321, là người bán."
	if got := texts(Find(beside), KindName); len(got) != 1 || got[0] != "Nguyễn Thu Trang" {
		t.Errorf("the seller beside her phone number came back as %v", got)
	}
}

// Vietnamese streets, wards and schools are named after people, so the name of
// a place is a person's name with a word in front of it saying it is not.
func TestAStreetNamedAfterSomebodyIsAStreet(t *testing.T) {
	text := "Liên hệ 0912345678, cửa hàng ở đường Lê Lợi gần trường Nguyễn Du."
	if got := texts(Find(text), KindName); len(got) != 0 {
		t.Errorf("a street and a school came back as people: %v", got)
	}
}

// Vietnamese companies are routinely named after their founder, and a company
// is not a natural person. Its registered name is public by law, so covering it
// removes published information from the corpus for nothing.
func TestACompanyNamedAfterItsFounderIsACompany(t *testing.T) {
	found := Find(contract)
	for _, name := range texts(found, KindName) {
		if strings.Contains(name, "Hoàng Long") {
			t.Errorf("the company name %q was redacted as a person", name)
		}
	}
	if got := texts(found, KindName); len(got) != 1 || got[0] != "Trần Thị Hương" {
		t.Errorf("the person named in the contract came back as %v", got)
	}
}

// Vietnamese typed without its tone marks is a register that `sang` keeps, and
// it is the register people type contact details in. A word list that only
// matched the marked spelling would find nothing in that half of the corpus.
func TestPersonalDataTypedWithoutToneMarksIsStillPersonalData(t *testing.T) {
	text := "lien he anh Nguyen Van Minh sdt 0912345678, dia chi so 12 duong Le Loi, phuong Ben Nghe, quan 1, TP Ho Chi Minh."
	got := kinds(Find(text))
	for _, want := range []Kind{KindName, KindPhone, KindAddress} {
		if got[want] == 0 {
			t.Errorf("an unmarked contact block yielded no %s", want)
		}
	}
}

// An address is a chain of administrative units read from smallest to largest.
// A chain that never reaches a ward is a house number in a sentence.
func TestAnAddressHasToReachAnAdministrativeUnit(t *testing.T) {
	if got := kinds(Find("Số 25 đường Nguyễn Huệ nằm ở trung tâm."))[KindAddress]; got != 0 {
		t.Error("a street with a house number on it was taken for an address")
	}
	full := "Địa chỉ: Số 25 đường Nguyễn Huệ, phường Bến Nghé, quận 1, TP Hồ Chí Minh."
	if got := texts(Find(full), KindAddress); len(got) != 1 {
		t.Fatalf("a full address came back as %v", got)
	} else if !strings.HasSuffix(got[0], "TP Hồ Chí Minh") {
		t.Errorf("the address stopped at %q rather than at the city", got[0])
	}
}

// The gazetteer holds both numberings. Vietnam went from sixty three units to
// thirty four in July 2025, and most of the corpus was written before that.
func TestTheGazetteerHoldsBothNumberings(t *testing.T) {
	for _, province := range []string{"Hà Nội", "Bắc Giang", "Hải Dương", "Đắk Nông", "Tuyên Quang"} {
		if !provinces.has(province) {
			t.Errorf("the gazetteer does not hold %s", province)
		}
	}
}

// The levels, and what each one is for.
func TestEachLevelCoversWhatItSaysItCovers(t *testing.T) {
	at0, found := Redact(advert, L0)
	if at0 != advert {
		t.Error("L0 changed the text")
	}
	if len(found) == 0 {
		t.Fatal("L0 found nothing, so it cannot be what the recall measurement runs against")
	}

	at1, _ := Redact(advert, L1)
	if strings.Contains(at1, "0912 345 678") {
		t.Error("L1 left a phone number in the text")
	}
	if !strings.Contains(at1, "Nguyễn Văn Minh") {
		t.Error("L1 removed a name, which is L2's job")
	}

	at2, _ := Redact(advert, L2)
	if strings.Contains(at2, "Nguyễn Văn Minh") {
		t.Error("L2 left a name beside a phone number")
	}
	if !strings.Contains(at2, tags[KindName]) {
		t.Error("L2 removed the name without saying that a name was there")
	}
}

// What replaces a span has to say that something was there. A number replaced
// by zeros teaches a model that phone numbers are zeros, and a number removed
// outright teaches it that sentences end in the middle.
func TestWhatCoversASpanSaysWhatWasThere(t *testing.T) {
	out, found := Redact("Gọi 0912345678 nhé.", L1)
	if out != "Gọi [SODIENTHOAI] nhé." {
		t.Errorf("the covered text is %q", out)
	}
	if len(found) != 1 || found[0].Text != "0912345678" {
		t.Errorf("the span does not carry what it covered: %v", found)
	}
	for _, k := range Kinds() {
		if k.Tag() == "" || !k.Valid() {
			t.Errorf("%s has no tag", k)
		}
	}
}

// Spans are what the recall measurement counts, so they are returned at every
// level including the one that removes nothing.
func TestSpansAreReportedAtEveryLevel(t *testing.T) {
	levels := []Level{L0, L1, L2}
	counts := make([]int, 0, len(levels))
	for _, level := range levels {
		_, found := Redact(advert, level)
		counts = append(counts, len(found))
	}
	for i := 1; i < len(counts); i++ {
		if counts[i] != counts[0] {
			t.Errorf("L%d reported %d spans and L0 reported %d", i, counts[i], counts[0])
		}
	}
}

// Two detectors that match the same bytes have to be resolved once, or the text
// comes back with a tag inside a tag.
func TestNoTwoSpansOverlap(t *testing.T) {
	for _, text := range []string{advert, contract, forum, article, prices} {
		found := Find(text)
		for i := 1; i < len(found); i++ {
			if found[i].Start < found[i-1].End {
				t.Errorf("%s overlaps %s", found[i].Kind, found[i-1].Kind)
			}
		}
	}
}

func TestAnEmptyDocumentFindsNothingRatherThanPanicking(t *testing.T) {
	out, found := Redact("", L2)
	if out != "" || len(found) != 0 {
		t.Errorf("an empty document came back as %q with %v", out, found)
	}
}

// The tally is what a run reports, and the number a publication decision turns
// on is how many documents held anything at all.
func TestTheTallyCountsDocumentsAsWellAsSpans(t *testing.T) {
	var tally Tally
	for _, text := range []string{advert, contract, article, prices, forum} {
		_, found := Redact(text, L1)
		tally.Add(L1, found)
	}
	if tally.Documents != 5 {
		t.Errorf("the tally counted %d documents, want 5", tally.Documents)
	}
	if tally.Carrying != 3 {
		t.Errorf("the tally says %d documents carry personal data, want the advert, the contract and the forum post", tally.Carrying)
	}
	if tally.Covered >= tally.Spans {
		t.Errorf("L1 covered %d of %d spans, and it is meant to leave the names and addresses", tally.Covered, tally.Spans)
	}
	if tally.Cued == 0 {
		t.Error("nothing was found by a cue, and the contract names three of its identifiers")
	}
	if got := tally.Rate(); got < 0.59 || got > 0.61 {
		t.Errorf("three documents in five is a rate of %v", got)
	}
}

func TestAnEmptyTallyDividesByNothing(t *testing.T) {
	var tally Tally
	if got := tally.Rate(); got != 0 {
		t.Errorf("a tally of no documents has a rate of %v", got)
	}
}

func TestALevelSurvivesBeingWrittenDownAndReadBack(t *testing.T) {
	for _, level := range []Level{L0, L1, L2} {
		got, ok := ParseLevel(level.String())
		if !ok || got != level {
			t.Errorf("%s read back as %s, %v", level, got, ok)
		}
	}
	if _, ok := ParseLevel("L3"); ok {
		t.Error("L3 was accepted, and there are three levels")
	}
}
