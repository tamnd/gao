package normalize

// The encodings themselves.
//
// A table like this is a fact about a font that shipped in 1995, and the way to
// be sure of it is to find the same fact written down by people who did not copy
// each other. These were read out of UniKey's vnconv, which is the reference
// implementation every Vietnamese input method has been ported from, and checked
// against a second transcription of it in vnkey, a Rust rewrite. The two agree
// on all 186 letters of every encoding here. TCVN3 and VISCII were then checked
// a third time against iconv, which carries its own tables for both under the
// names TCVN5712-1 and VISCII, and that third check is committed rather than
// remembered: testdata/legacy/*.iconv is what iconv encodes each of the 134
// letters of Vietnamese as, and the test reads it back against these tables on
// every run. They agree on every code both of them define. Where they do not
// overlap it is iconv defining a code these tables leave empty, always a
// capital, which for TCVN5712-1 is sixty of them and is the difference between
// the standard and the font encoding named below.
//
// TCVN3 and VNI-Windows were checked a fourth way, against the mojibake that
// anybody who reads Vietnamese can recite. "TiÕng ViÖt" and "Tieáng Vieät" both
// come back as "Tiếng Việt" under these tables, and "®­îc" and "ñöôïc" both come
// back as "được". Those two encodings are what almost all of the material is in,
// and they are the two that are checked hardest.
//
// VPS and the two BK HCM encodings rest on the first check alone. There is no
// second source for them that is not a port of the first, and no body of text in
// them to test against, so what is claimed for them is that two people read the
// same table the same way. The margin the detector demands is what bounds the
// cost of being wrong about one: a document is only read as VPS when VPS turns
// it into Vietnamese words and the other five do not.
//
// UniKey is under the GPL and gao is not, which is why nothing here is its code.
// A mapping from a byte to a letter is a fact about an encoding rather than an
// expression of it, it is published identically by everyone who implements the
// encoding, and it is written here in gao's own form and checked against sources
// that are not UniKey before it is used.

// singleTable builds the byte to letter map of an encoding whose letters are one
// byte each, from the eight rows of sixteen that follow.
//
// Written out as rows of characters rather than as a map from numbers, because
// the only useful review of a table like this is somebody reading the Vietnamese
// alphabet off it and seeing that it is in the order the encoding puts it in. A
// space is a byte the encoding does not use.
func singleTable(rows string) map[byte]rune {
	m := make(map[byte]rune, 128)
	n := 0
	for _, c := range rows {
		if c != ' ' {
			m[byte(0x80+n)] = c
		}
		n++
	}
	if n != 128 {
		panic("normalize: a single byte table covers 0x80 to 0xff and this one does not")
	}
	return m
}

// A markRow is one base byte, the mark bytes that may follow it, and the letters
// the two make together, in the same order.
type markRow struct {
	base    byte
	marks   string
	letters string
}

// pairTable builds the two byte map of an encoding that spells a letter as a
// base and a mark.
func pairTable(rows []markRow) map[[2]byte]rune {
	m := make(map[[2]byte]rune)
	for _, row := range rows {
		letters := []rune(row.letters)
		if len(letters) != len(row.marks) {
			panic("normalize: a mark row has to name one letter per mark byte")
		}
		for i := range letters {
			m[[2]byte{row.base, row.marks[i]}] = letters[i]
		}
	}
	return m
}

// tcvn3 is the ABC encoding, the one the .VnTime fonts are drawn for. It is most
// of the Vietnamese published between 1993 and about 2005 and it is what a
// Vietnamese archive is mostly full of.
//
// It leaves the ASCII range alone and puts its letters in 0xa1 to 0xfe, which is
// why a TCVN3 page read as Latin-1 comes out looking like ordinary European text
// with the wrong letters in it rather than like nonsense.
//
// There are no capital vowels with tone marks in it, and that is the encoding
// rather than an omission here. Headings were set in .VnTimeH, a second font
// with the capitals drawn in the same slots, so the bytes of "VIỆT NAM" and of
// "việt nam" are the same bytes and the case lives in the font and not in the
// file. Everything below the capitals row therefore decodes to lower case, and a
// heading transcoded out of TCVN3 arrives in lower case. Nothing here can know
// otherwise and nothing here guesses.
var tcvn3 = &Charset{
	name: "TCVN3",
	single: singleTable("" +
		"                " + // 0x80
		"                " + // 0x90
		" ĂÂÊÔƠƯĐăâêôơưđ " + // 0xa0
		"     àảãáạ ằẳẵắ " + // 0xb0
		"      ặầẩẫấậè ẻẽ" + // 0xc0
		"éẹềểễếệìỉ   ĩíịò" + // 0xd0
		" ỏõóọồổỗốộờởỡớợù" + // 0xe0
		" ủũúụừửữứựỳỷỹýỵ "), // 0xf0
}

// vps is the encoding of the Vietnamese Professionals Society, which is what the
// diaspora press outside the country used through the nineties.
//
// It fills 0x80 to 0xff almost completely and it fills it in no order at all:
// capitals and lower case letters sit next to each other wherever there was
// room. That is not a transcription error below, it is what the encoding is.
var vps = &Charset{
	name: "VPS",
	single: singleTable("" +
		"ÀẢÃẤẦẨọỗĂếềểệẮẰẲ" + // 0x80
		"Ế  ỀỂỄỐỒỔỖýỷỵỚỜỞ" + // 0x90
		" ắằẳẵặỠớÙờởỡŨỨợỪ" + // 0xa0
		"ổỬỲỸÍÌộỈĨÓửữÒỎÕự" + // 0xb0
		"ầÁÂấẩẫậđẻÉÊẹỉễịỹ" + // 0xc0
		"ƯỦồốÔỏơÈừứÚũưÝẺ " + // 0xd0
		"àáâãảạă èéêẽìí ĩ" + // 0xe0
		"ẴĐòóôõ Ơụùúủ ỶẼỳ"), // 0xf0
}

// viscii is the one encoding here that became a standard rather than a font. It
// has an RFC and an IANA name, iconv reads it, and it is what Vietnamese mail
// and Usenet went out in.
//
// It is in this list for two reasons. It is a real part of the archive, and it
// is the encoding a detector that did not know about it would mistake for one of
// the others: a VISCII page read as TCVN3 decodes to Vietnamese letters all the
// way through and to no Vietnamese words at all.
//
// Six capitals are missing from the table because VISCII puts them in the
// control range, at 0x02, 0x05, 0x06, 0x14, 0x19 and 0x1e, and this stage does
// not read bytes below 0x80. A byte that low in a document that reached here is
// far more likely to be damage than to be a capital Ỵ.
//
// Six and not seven, and the arithmetic is the reason to be sure of it. VISCII
// carries all 134 letters of Vietnamese, 0x80 to 0xff is 128 codes, so exactly
// six letters go below 0x80 and every one of the 128 above it is a letter. This
// table had 127 of them and left 0xa0 empty, where Latin-1 keeps its non
// breaking space and where VISCII keeps Õ. The count is what caught it and iconv
// is what named it, and testdata/legacy/viscii.iconv is that check written down.
var viscii = &Charset{
	name: "VISCII",
	single: singleTable("" +
		"ẠẮẰẶẤẦẨẬẼẸẾỀỂỄỆỐ" + // 0x80
		"ỒỔỖỘỢỚỜỞỊỎỌỈỦŨỤỲ" + // 0x90
		"Õắằặấầẩậẽẹếềểễệố" + // 0xa0
		"ồổỗỠƠộờởịỰỨỪỬơớƯ" + // 0xb0
		"ÀÁÂÃẢĂẳẵÈÉÊẺÌÍĨỳ" + // 0xc0
		"ĐứÒÓÔạỷừửÙÚỹỵÝỡư" + // 0xd0
		"àáâãảăữẫèéêẻìíĩỉ" + // 0xe0
		"đựòóôõỏọụùúũủýợỮ"), // 0xf0
}

// bkhcm1 is the one byte encoding of the Bach Khoa fonts out of the Ho Chi Minh
// City University of Technology, which is what a good deal of southern
// institutional material was typed in.
//
// It lays each vowel out as six consecutive codes, base then acute, grave, hook,
// tilde and dot, and that regularity is what settles the two places the source
// tables are inconsistent. UniKey gives Ặ the code 0x98 and Ỵ the code 0x8e,
// where Ụ and Ị already are, and leaves 0x9e and 0xa0 unused. Ụ closes the run
// Ú Ù Ủ Ũ and Ị closes the run Í Ì Ỉ Ĩ, so those two are certain, and the one
// free code that continues the Ă run is 0x9e and the one left over for Ỵ is
// 0xa0. Any other reading breaks a pattern that holds for the eleven other
// vowels, so that is the reading here, and it is written down rather than left
// as a silent correction.
//
// Six capitals are not in the table at all. BK HCM1 puts Ầ, Đ, Ý, Ỳ, Ỷ and Ỹ in
// the ASCII range, on ^, `, {, |, } and ~. Decoding those would rewrite ordinary
// punctuation in any document this stage read wrongly, and a page that loses a
// capital is a smaller wrong than a page whose braces turn into vowels.
var bkhcm1 = &Charset{
	name: "BK HCM1",
	single: singleTable("" +
		"ÁÀẢÃẠÉÈẺẼẸÍÌỈĨỊÓ" + // 0x80
		"ÒỎÕỌÚÙỦŨỤĂẮẰẲẴẶÂ" + // 0x90
		"ỴẦẨẪẬÊẾỀỂỄỆÔỐỒỔỖ" + // 0xa0
		"ỘƠỚỜỞỠỢƯỨỪỬỮỰđáà" + // 0xb0
		"ảãạéèẻẽẹíìỉĩịóòỏ" + // 0xc0
		"õọúùủũụăắằẳẵặâấầ" + // 0xd0
		"ẩẫậêếềểễệôốồổỗộơ" + // 0xe0
		"ớờởỡợưứừửữựýỳỷỹỵ"), // 0xf0
}

// vniWin is VNI for Windows, the encoding of the VNI fonts, and after TCVN3 it
// is the most of what is left.
//
// It spells a marked letter as two bytes: the plain letter as it is typed, then
// a byte that carries the vowel mark and the tone together. So à is "a" followed
// by 0xf8 and ế is "e" followed by 0xe1, which is why VNI mojibake reads as an
// ASCII word with a European letter wedged into the middle of it, "Tieáng" and
// "ñöôïc" and "Haø Noäi".
//
// The mark bytes are different after a capital and after a lower case letter, so
// unlike TCVN3 this encoding keeps its capitals. The letters that carry a mark
// of their own and no tone are one byte, which is the table below the pairs.
var vniWin = &Charset{
	name: "VNI-WIN",
	single: singleTable("" +
		"                " + // 0x80
		"                " + // 0x90
		"                " + // 0xa0
		"                " + // 0xb0
		"      Ỉ     ÌÍỴ " + // 0xc0
		" ĐỊĨƠ Ư         " + // 0xd0
		"      ỉ     ìíỵ " + // 0xe0
		" địĩơ ư         "), // 0xf0
	pairs: pairTable([]markRow{
		{'A', "\xc0\xc1\xc2\xc3\xc4\xc5\xc8\xc9\xca\xcb\xcf\xd5\xd8\xd9\xda\xdb\xdc", "ẦẤÂẪẬẨẰẮĂẶẠÃÀÁẲẢẴ"},
		{'E', "\xc0\xc1\xc2\xc3\xc4\xc5\xcf\xd5\xd8\xd9\xdb", "ỀẾÊỄỆỂẸẼÈÉẺ"},
		{'O', "\xc0\xc1\xc2\xc3\xc4\xc5\xcf\xd5\xd8\xd9\xdb", "ỒỐÔỖỘỔỌÕÒÓỎ"},
		{'U', "\xcf\xd5\xd8\xd9\xdb", "ỤŨÙÚỦ"},
		{'Y', "\xd5\xd8\xd9\xdb", "ỸỲÝỶ"},
		{0xd4, "\xcf\xd5\xd8\xd9\xdb", "ỢỠỜỚỞ"}, // Ơ
		{0xd6, "\xcf\xd5\xd8\xd9\xdb", "ỰỮỪỨỬ"}, // Ư
		{'a', "\xe0\xe1\xe2\xe3\xe4\xe5\xe8\xe9\xea\xeb\xef\xf5\xf8\xf9\xfa\xfb\xfc", "ầấâẫậẩằắăặạãàáẳảẵ"},
		{'e', "\xe0\xe1\xe2\xe3\xe4\xe5\xef\xf5\xf8\xf9\xfb", "ềếêễệểẹẽèéẻ"},
		{'o', "\xe0\xe1\xe2\xe3\xe4\xe5\xef\xf5\xf8\xf9\xfb", "ồốôỗộổọõòóỏ"},
		{'u', "\xef\xf5\xf8\xf9\xfb", "ụũùúủ"},
		{'y', "\xf5\xf8\xf9\xfb", "ỹỳýỷ"},
		{0xf4, "\xef\xf5\xf8\xf9\xfb", "ợỡờớở"}, // ơ
		{0xf6, "\xef\xf5\xf8\xf9\xfb", "ựữừứử"}, // ư
	}),
}

// bkhcm2 is the two byte member of the Bach Khoa pair, built the way VNI is: a
// base byte and then a mark.
//
// Its bases are the plain vowels for the ones ASCII has and its own one byte
// codes for â, ê, ô, ă, ơ and ư, so ậ is 0xea followed by 0xe5 rather than "a"
// followed by anything.
//
// One entry is irregular. Every other capital takes its marks from 0xc1 to 0xce
// and Ệ takes 0xe5, out of the lower case range, which is where Ậ and Ộ take
// their dot from as well under a different base. It is carried here as the
// source has it, because there is no second reading of this encoding to check it
// against and inventing the regular code would be inventing an encoding.
var bkhcm2 = &Charset{
	name: "BK HCM2",
	single: singleTable("" +
		"                " + // 0x80
		"                " + // 0x90
		"                " + // 0xa0
		"                " + // 0xb0
		"Đ         Â    Ê" + // 0xc0
		" ÍÌỈĨỊÔ  ĂƠƯ    " + // 0xd0
		"đ         â    ê" + // 0xe0
		" íìỉĩịô  ăơư    "), // 0xf0
	pairs: pairTable([]markRow{
		{'A', "\xc1\xc2\xc3\xc4\xc5", "ÁÀẢÃẠ"},
		{'E', "\xc1\xc2\xc3\xc4\xc5", "ÉÈẺẼẸ"},
		{'O', "\xc1\xc2\xc3\xc4\xc5", "ÓÒỎÕỌ"},
		{'U', "\xc1\xc2\xc3\xc4\xc5", "ÚÙỦŨỤ"},
		{'Y', "\xc1\xc2\xc3\xc4\xc5", "ÝỲỶỸỴ"},
		{0xca, "\xc5\xcb\xcc\xcd\xce", "ẬẤẦẨẪ"}, // Â
		{0xcf, "\xcb\xcc\xcd\xce\xe5", "ẾỀỂỄỆ"}, // Ê
		{0xd6, "\xc5\xcb\xcc\xcd\xce", "ỘỐỒỔỖ"}, // Ô
		{0xd9, "\xc5\xc6\xc7\xc8\xc9", "ẶẮẰẲẴ"}, // Ă
		{0xda, "\xc1\xc2\xc3\xc4\xc5", "ỚỜỞỠỢ"}, // Ơ
		{0xdb, "\xc1\xc2\xc3\xc4\xc5", "ỨỪỬỮỰ"}, // Ư
		{'a', "\xe1\xe2\xe3\xe4\xe5", "áàảãạ"},
		{'e', "\xe1\xe2\xe3\xe4\xe5", "éèẻẽẹ"},
		{'o', "\xe1\xe2\xe3\xe4\xe5", "óòỏõọ"},
		{'u', "\xe1\xe2\xe3\xe4\xe5", "úùủũụ"},
		{'y', "\xe1\xe2\xe3\xe4\xe5", "ýỳỷỹỵ"},
		{0xea, "\xe5\xeb\xec\xed\xee", "ậấầẩẫ"}, // â
		{0xef, "\xe5\xeb\xec\xed\xee", "ệếềểễ"}, // ê
		{0xf6, "\xe5\xeb\xec\xed\xee", "ộốồổỗ"}, // ô
		{0xf9, "\xe5\xe6\xe7\xe8\xe9", "ặắằẳẵ"}, // ă
		{0xfa, "\xe1\xe2\xe3\xe4\xe5", "ớờởỡợ"}, // ơ
		{0xfb, "\xe1\xe2\xe3\xe4\xe5", "ứừửữự"}, // ư
	}),
}

// cp1252 is the reverse of the twenty seven characters windows-1252 puts in
// 0x80 to 0x9f, which is the only part of it that is not Latin-1.
//
// It is here so that a document can be read back to the bytes it was made of
// whichever of the two encodings a crawler decoded it with. Latin-1 sends those
// bytes to the C1 controls, which come back through the rune value itself, and
// windows-1252 sends them to these characters, and no character is in both
// sets, so one table answers for both readings.
var cp1252 = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84,
	'…': 0x85, '†': 0x86, '‡': 0x87, 'ˆ': 0x88,
	'‰': 0x89, 'Š': 0x8a, '‹': 0x8b, 'Œ': 0x8c,
	'Ž': 0x8e, '‘': 0x91, '’': 0x92, '“': 0x93,
	'”': 0x94, '•': 0x95, '–': 0x96, '—': 0x97,
	'˜': 0x98, '™': 0x99, 'š': 0x9a, '›': 0x9b,
	'œ': 0x9c, 'ž': 0x9e, 'Ÿ': 0x9f,
}
