package cover

// The tables that make the detectors precise. Every one of them is finite and
// published, which is the whole reason the detectors can afford to look at
// digits at all.

import (
	"strings"

	"github.com/tamnd/gao/normalize"
)

// cccdProvinces is the three digit province code that opens a twelve digit
// national ID, from the administrative unit codes the ID numbering was built
// on. The list is the thing that makes a CCCD detector possible: without it,
// every twelve digit number in the corpus is a national ID, and there are a lot
// of twelve digit numbers in a corpus.
//
// The codes are the pre 2025 sixty three units, and they stay that way. A
// national ID issued under the old numbering does not change when the provinces
// merge, so a corpus of Vietnamese web text will hold both for decades and a
// detector that dropped the old codes would stop seeing most of the IDs.
var cccdProvinces = set([]string{
	"001", "002", "004", "006", "008", "010", "011", "012", "014", "015",
	"017", "019", "020", "022", "024", "025", "026", "027", "030", "031",
	"033", "034", "035", "036", "037", "038", "040", "042", "044", "045",
	"046", "048", "049", "051", "052", "054", "056", "058", "060", "062",
	"064", "066", "067", "068", "070", "072", "074", "075", "077", "079",
	"080", "082", "083", "084", "086", "087", "089", "091", "092", "093",
	"094", "095", "096",
})

// mobilePrefixes is the two digits that follow the leading zero of a Vietnamese
// mobile number. Every one of them was assigned to a carrier, and the ones that
// were not assigned are the reason a ten digit number starting with zero is not
// automatically a phone number.
var mobilePrefixes = set([]string{
	"32", "33", "34", "35", "36", "37", "38", "39", // Viettel
	"52", "55", "56", "58", "59", // Vietnamobile, Reddi, Gmobile
	"70", "76", "77", "78", "79", // MobiFone
	"81", "82", "83", "84", "85", "86", "87", "88", "89", // VinaPhone, Viettel, Itel, MobiFone
	"90", "91", "92", "93", "94", "96", "97", "98", "99", // the older assignments, unchanged in 2018
})

// addressCues open a segment of a Vietnamese address. An address is a chain of
// administrative units read from smallest to largest, and each link in the
// chain says what kind of unit it is, which is what makes the chain findable
// without a gazetteer of every street in the country.
var addressCues = []string{
	"số nhà", "số", "đường", "phố", "ngõ", "ngách", "hẻm", "tổ", "thôn", "ấp",
	"khu phố", "khu đô thị", "phường", "xã", "thị trấn", "thị xã", "quận",
	"huyện", "thành phố", "tỉnh", "tp.", "tp", "q.", "p.", "kp.",
}

// notAWay opens with a word from the address cues and does not name a place.
// A hotline is an đường dây, literally a line, and a contact block that gives an
// address and a hotline in the same sentence reads as one long address chain
// without this: the chain walks on past the address into the phone number, the
// number ends up inside the address span, and a phone number that was found is
// dropped for overlapping something bigger.
var notAWay = []string{"đường dây"}

// provinces is every province and centrally run city, under both numberings.
//
// Vietnam went from sixty three units to thirty four on 1 July 2025. A corpus
// of web text spans both, and most of the text in it was written before the
// merger, so a gazetteer holding only the current thirty four would fail to
// close an address chain on the majority of the addresses it meets. Holding
// both costs a slightly longer list and nothing else.
var provinces = bareOf([]string{
	// The thirty four units in force since July 2025.
	"Hà Nội", "Hải Phòng", "Huế", "Đà Nẵng", "Hồ Chí Minh", "Cần Thơ",
	"Cao Bằng", "Lai Châu", "Điện Biên", "Sơn La", "Lạng Sơn", "Quảng Ninh",
	"Thanh Hóa", "Nghệ An", "Hà Tĩnh", "Tuyên Quang", "Lào Cai", "Thái Nguyên",
	"Phú Thọ", "Bắc Ninh", "Hưng Yên", "Ninh Bình", "Quảng Trị", "Quảng Ngãi",
	"Gia Lai", "Khánh Hòa", "Lâm Đồng", "Đắk Lắk", "Đồng Nai", "Tây Ninh",
	"Vĩnh Long", "Đồng Tháp", "Cà Mau", "An Giang",

	// The units that were merged away, which is where most of the corpus lives.
	"Hà Giang", "Bắc Kạn", "Yên Bái", "Hòa Bình", "Bắc Giang", "Vĩnh Phúc",
	"Hải Dương", "Thái Bình", "Hà Nam", "Nam Định", "Quảng Bình",
	"Thừa Thiên Huế", "Quảng Nam", "Bình Định", "Phú Yên", "Ninh Thuận",
	"Bình Thuận", "Kon Tum", "Đắk Nông", "Bình Phước", "Bình Dương",
	"Bà Rịa Vũng Tàu", "Bà Rịa - Vũng Tàu", "Long An", "Tiền Giang", "Bến Tre",
	"Trà Vinh", "Kiên Giang", "Hậu Giang", "Sóc Trăng", "Bạc Liêu", "Sài Gòn",
})

// surnames are the Vietnamese family names a person's name can open with.
//
// The list is short and it is meant to be. The name detector is only ever
// allowed to fire beside an identifier that was already covered, so its job is
// to find the seller's name in a classified advertisement rather than to find
// every name in the corpus, and a longer list would buy recall in a place where
// recall is not what is wanted.
var surnames = bareOf([]string{
	"Nguyễn", "Trần", "Lê", "Phạm", "Hoàng", "Huỳnh", "Phan", "Vũ", "Võ",
	"Đặng", "Bùi", "Đỗ", "Hồ", "Ngô", "Dương", "Lý", "Đào", "Đoàn", "Vương",
	"Trịnh", "Đinh", "Lâm", "Phùng", "Mai", "Tô", "Trương", "Hà", "Tạ", "Chu",
	"Cao", "Lưu", "Lã", "Quách", "Thái", "Kiều", "Doãn", "Từ", "Tăng", "Hứa",
	"Triệu", "Đàm", "Nghiêm", "Ninh", "Vi", "Chử", "Khổng", "Lương",
})

// currency words after a number say it was money, which is the commonest thing
// a ten digit run in Vietnamese text turns out to be.
var currencies = set([]string{
	"đồng", "đ", "vnđ", "vnd", "usd", "eur", "yên", "won", "tỷ", "triệu",
	"nghìn", "ngàn", "vnđ/tháng", "đ/tháng", "đ/m2", "đồng/tháng",
})

// cues are the words that name an identifier before it appears. When one of
// these sits within a short distance of a number, the number is covered whether
// or not its digits validate, because somebody writing "số CMND" before nine
// digits has told us what the nine digits are.
var cues = map[Kind][]string{
	KindPhone: {
		"điện thoại", "đt", "sđt", "sdt", "hotline", "liên hệ", "gọi", "zalo",
		"số máy", "tel", "phone", "mobile", "di động",
	},
	KindCCCD: {"cccd", "căn cước", "căn cước công dân", "thẻ căn cước"},
	KindCMND: {"cmnd", "cmt", "chứng minh nhân dân", "chứng minh thư", "giấy cmnd"},
	KindTax:  {"mã số thuế", "mst", "mã số doanh nghiệp", "msdn", "mã số đăng ký"},
}

// cueWindow is how far before a number a cue may sit and still explain it, in
// bytes. It is generous, because "Số điện thoại liên hệ của chủ nhà là" is a
// normal thing to write and none of it is noise.
const cueWindow = 60

// cuedBefore reports whether one of the cues for k appears in the text just
// before offset.
func cuedBefore(lowered string, at int, k Kind) bool {
	_, ok := cueGap(lowered, at, k)
	return ok
}

// cueGap reports how far the nearest cue for k sits before a number, in bytes
// between the end of the cue and the start of the number, and whether there was
// one at all. The text is lowercased by the caller once per document rather
// than once per candidate, since a document holds many numbers and one lower
// case copy of itself.
//
// The distance is measured in the window it was found in, and a cue found with
// its tone marks off is measured in the bare window, which is a few bytes
// shorter for the same words. That makes the two slightly incomparable and it
// does not matter: the number is only ever used to decide which of two cues is
// nearer, and two cues that are within a few bytes of each other are not the
// case this is for.
func cueGap(lowered string, at int, k Kind) (int, bool) {
	start := max(at-cueWindow, 0)
	at = min(at, len(lowered))
	window := lowered[start:at]
	bare := normalize.Bare(window)

	gap, found := 0, false
	for i, cue := range cues[k] {
		for _, c := range []struct{ hay, needle string }{{window, cue}, {bare, bareCues[k][i]}} {
			at := strings.LastIndex(c.hay, c.needle)
			if at < 0 {
				continue
			}
			if d := len(c.hay) - (at + len(c.needle)); !found || d < gap {
				gap, found = d, true
			}
		}
	}
	return gap, found
}

// nearestCue picks between the kinds a number could be, by which one the text
// named closest to it.
//
// A contact block names two kinds of identifier inside one cue window often
// enough to matter: "điện thoại 028 3845 2210, mã số thuế đơn vị 0301 447 926"
// puts a phone cue and a tax cue both within sixty bytes of the tax code, and
// taking them in a fixed order files the tax code under whichever kind the code
// happens to check first. Each number belongs to the word next to it.
func nearestCue(lowered string, at int, kinds ...Kind) (Kind, bool) {
	best, gap, found := Kind(""), 0, false
	for _, k := range kinds {
		d, ok := cueGap(lowered, at, k)
		if !ok || (found && d >= gap) {
			continue
		}
		best, gap, found = k, d, true
	}
	return best, found
}

// bareCues is the same list with the marks taken off, in the same order, built
// once rather than per document.
var bareCues = func() map[Kind][]string {
	out := make(map[Kind][]string, len(cues))
	for k, list := range cues {
		bare := make([]string, len(list))
		for i, cue := range list {
			bare[i] = normalize.Bare(cue)
		}
		out[k] = bare
	}
	return out
}()

func set(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// bareSet holds a word list twice, once as it is written and once with its tone
// marks off, and matches either.
//
// This is the same loosening `sang` takes for its function words and it is here
// for the same reason. Vietnamese typed without its marks is a register rather
// than a defect, `sang` keeps it, and a corpus that keeps it holds contact
// blocks that read "lien he anh Nguyen Van Minh". A surname list that only
// matched Nguyễn would find nobody in that half of the corpus, and the half it
// missed is the half people type in a hurry, which is the half with phone
// numbers in it.
//
// The cost is that a bare form matches every marked word it could have come
// from: hoa matches hoà and họa as well as itself. For a surname list guarded by
// the co-occurrence policy that is an acceptable trade, since a name is only
// covered when an identifier is already sitting beside it.
type bareSet map[string]bool

func bareOf(items []string) bareSet {
	m := make(bareSet, len(items)*2)
	for _, s := range items {
		s = strings.ToLower(s)
		m[s] = true
		m[normalize.Bare(s)] = true
	}
	return m
}

// has reports whether the word is in the list, written either way.
func (b bareSet) has(word string) bool {
	word = strings.ToLower(word)
	return b[word] || b[normalize.Bare(word)]
}
