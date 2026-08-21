package normalize

import (
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// TestTheASCIIShortcutInBareAgreesWithTheLongWayRound holds down the fast path
// in [Bare], which claims a string of ASCII bytes is already bare.
//
// The claim is about Unicode and not about Vietnamese: NFD is the identity on
// ASCII, no ASCII byte is a combining mark, and đ is two bytes so no ASCII
// string holds one. That is true and a later edit could quietly stop it being
// true, so it is checked against the loop it replaced rather than against a
// list of answers somebody typed.
func TestTheASCIIShortcutInBareAgreesWithTheLongWayRound(t *testing.T) {
	for _, in := range []string{
		"hoa", "hòa", "tiếng", "Việt", "nguyễn", "đường", "ĐƯỜNG", "đ", "Đ",
		"abc", "HTTP", "x", "", "a1", "1234", "co2", "e-mail", "a.b", " ",
		"café", "naïve", "ȭ", "日本語", "một câu tiếng Việt",
		strings.Repeat("khong-dau-", 5),
	} {
		if got, want := Bare(in), bareSlow(in); got != want {
			t.Errorf("Bare(%q) = %q, want %q", in, got, want)
		}
	}
}

// bareSlow is bare without the shortcut, kept so the shortcut has something to
// be checked against.
func bareSlow(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range norm.NFD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, c):
		case c == 'đ':
			b.WriteRune('d')
		case c == 'Đ':
			b.WriteRune('D')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}
