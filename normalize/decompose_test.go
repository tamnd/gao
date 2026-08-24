package normalize

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// decomposed is the string [Decomposed] walks, built so a test can compare a
// sequence against a sequence.
func decomposed(s string) string {
	var b strings.Builder
	for c := range Decomposed(s) {
		b.WriteRune(c)
	}
	return b.String()
}

func TestWalkingTheDecompositionGivesTheDecomposition(t *testing.T) {
	t.Parallel()
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue // surrogates are not runes
		}
		if !utf8.ValidRune(r) {
			continue
		}
		s := string(r)
		if got, want := decomposed(s), norm.NFD.String(s); got != want {
			t.Fatalf("U+%04X walked as %q and NFD says %q", r, got, want)
		}
	}
}

// TestWalkingTheDecompositionGivesTheDecompositionInContext is the same claim
// about runes next to each other rather than alone, which is where canonical
// ordering has something to do and so where a walk that cannot reorder would
// give itself away.
func TestWalkingTheDecompositionGivesTheDecompositionInContext(t *testing.T) {
	t.Parallel()
	// A base, a letter that carries marks of two different combining classes,
	// loose marks typed in either order, and a Hangul syllable with and without
	// a final letter.
	parts := []string{
		"a", "e", "ệ", "ê", "ó", "đ", "Đ",
		"̀", "́", "̣", "̂", "̉",
		"한", "가", "쓺", "ȭ", "ﬁ", "①", " ", "1",
	}
	for _, a := range parts {
		for _, b := range parts {
			for _, c := range parts {
				s := a + b + c
				if got, want := decomposed(s), norm.NFD.String(s); got != want {
					t.Fatalf("%q walked as %q and NFD says %q", s, got, want)
				}
			}
		}
	}
}

func TestWalkingTheDecompositionGivesTheDecompositionOverRealText(t *testing.T) {
	t.Parallel()
	for line := range strings.SplitSeq(toneCorpus, "\n") {
		if got, want := decomposed(line), norm.NFD.String(line); got != want {
			t.Fatalf("%q walked as %q and NFD says %q", line, got, want)
		}
		for tok := range strings.FieldsSeq(line) {
			if got, want := decomposed(tok), norm.NFD.String(tok); got != want {
				t.Fatalf("%q walked as %q and NFD says %q", tok, got, want)
			}
		}
	}
}

// TestTheSlowPathIsTakenOnlyWhenItHasTo pins the condition the fast path rests
// on, because the whole argument for this file is that the condition is exact
// and that real text is on the right side of it.
func TestTheSlowPathIsTakenOnlyWhenItHasTo(t *testing.T) {
	t.Parallel()
	// Written as escapes because a syllable carrying its marks inside its letters
	// and one carrying them loose beside it are the same thing on the screen,
	// which is most of the reason this package exists at all.
	for _, s := range []string{
		"ho\u00e0", "h\u00f2a", "nghi\u00eang", "\u0110\u1ea3ng",
		"hello", "\ud55c\uad6d\uc5b4", "\u1ec7",
	} {
		if loose(s) {
			t.Errorf("loose(%q) is true and every mark in it is inside a letter", s)
		}
	}
	for _, s := range []string{"e\u0301", "e\u0302\u0301", "a\u0323", "\u0300"} {
		if !loose(s) {
			t.Errorf("loose(%q) is false and it carries a mark of its own", s)
		}
	}
	var slow int
	for tok := range strings.FieldsSeq(toneCorpus) {
		if loose(tok) {
			slow++
		}
	}
	if slow != 0 {
		t.Errorf("%d of the corpus tokens take the slow path and none of them should, since the corpus is NFC", slow)
	}
}

func TestHangulComesApartTheWayUnicodeSaysItDoes(t *testing.T) {
	t.Parallel()
	for r := rune(hangulBase); r < hangulBase+hangulCount; r++ {
		l, v, tail, ok := jamo(r)
		if !ok {
			t.Fatalf("U+%04X is in the Hangul block and jamo refused it", r)
		}
		want := string(l) + string(v)
		if tail != 0 {
			want += string(tail)
		}
		if got := norm.NFD.String(string(r)); got != want {
			t.Fatalf("U+%04X came apart as %q and NFD says %q", r, want, got)
		}
	}
	for _, r := range []rune{'a', 0xABFF, hangulBase + hangulCount, 0x1100, 'ệ'} {
		if _, _, _, ok := jamo(r); ok {
			t.Errorf("jamo(U+%04X) claimed a syllable and it is not one", r)
		}
	}
}

func BenchmarkDecomposed(b *testing.B) {
	toks := strings.Fields(toneCorpus)
	b.Run("walked", func(b *testing.B) {
		b.ReportAllocs()
		var n int
		for b.Loop() {
			for _, tok := range toks {
				for c := range Decomposed(tok) {
					n += int(c)
				}
			}
		}
		_ = n
	})
	b.Run("built", func(b *testing.B) {
		b.ReportAllocs()
		var n int
		for b.Loop() {
			for _, tok := range toks {
				for _, c := range norm.NFD.String(tok) {
					n += int(c)
				}
			}
		}
		_ = n
	})
}

func BenchmarkBare(b *testing.B) {
	toks := strings.Fields(toneCorpus)
	b.ReportAllocs()
	for b.Loop() {
		for _, tok := range toks {
			bare(tok)
		}
	}
}

func BenchmarkRetone(b *testing.B) {
	toks := strings.Fields(toneCorpus)
	b.ReportAllocs()
	for b.Loop() {
		for _, tok := range toks {
			retone(tok)
		}
	}
}
