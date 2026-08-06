package phoi

// A page before anybody has decided what its bytes mean.
//
// The rest of this package takes characters, because that is how a document
// reaches it: something upstream read the bytes as an encoding and handed on the
// result. For a page that was written in a Vietnamese font encoding the upstream
// reading is always wrong, and the whole of legacy.go is about undoing it.
//
// This is the one place that starts from the bytes. It exists because a golden
// file for a legacy encoding is only worth anything if what is committed is the
// bytes the document was made of, and because a PDF text layer and a file off a
// disk both arrive that way, with no encoding declared and nobody to ask.

import "strings"

// Text reads eight bit bytes the way a fetcher reads a page that declares
// windows-1252, or that declares nothing at all.
//
// That is the reading almost every legacy encoded Vietnamese page gets, and it
// is deliberate here rather than a fallback. Windows-1252 throws nothing away:
// every byte comes out as a character and no two bytes come out as the same one,
// so the document is still every bit of what it arrived as, and [Detect] can put
// it back. The five bytes the encoding leaves undefined come through as
// themselves, which is what a Latin-1 reading does with them, and it is what
// makes those bytes recoverable too.
//
// A page that really is UTF-8 must not come through here. This is for the bytes
// nobody could name an encoding for.
func Text(b []byte) string {
	var s strings.Builder
	s.Grow(len(b))
	for _, c := range b {
		if c < 0x80 {
			s.WriteByte(c)
			continue
		}
		s.WriteRune(highBytes[c-0x80])
	}
	return s.String()
}

// highBytes is what windows-1252 draws 0x80 to 0xff as, built backwards out of
// [cp1252] so that the two tables cannot drift apart. Everything from 0xa0 up is
// its own code point, and so are the five codes windows-1252 does not use.
var highBytes = func() [128]rune {
	var t [128]rune
	for i := range t {
		t[i] = rune(0x80 + i)
	}
	for r, b := range cp1252 {
		t[b-0x80] = r
	}
	return t
}()
