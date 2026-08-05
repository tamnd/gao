package phoi

// Vietnamese with its tone marks taken off.

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Bare returns the text with its tone marks and vowel marks removed.
//
// This is not a normalization and nothing writes it back into a document. It
// exists because Vietnamese typed without its marks is a register rather than a
// defect, so half the corpus writes hoà as hoa, and a word list that only
// matches the marked spelling stops working on that half. Matching bare forms
// is a deliberate loosening: hoa, hoà, hóa and họa all come back as hoa, so a
// list that uses it is matching more than it names and has to be written
// knowing that.
//
// The stroke on đ is handled separately because it is not a combining mark. It
// has no decomposition at all in Unicode, so a d with a stroke survives every
// amount of NFD and has to be spelled out here.
func Bare(s string) string {
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
