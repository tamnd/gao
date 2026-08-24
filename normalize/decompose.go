package normalize

// The canonical decomposition, walked rather than built.

import (
	"iter"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Decomposed walks the canonical decomposition of s one rune at a time.
//
// It is what a range over norm.NFD.String(s) gives, without the string. That
// string is the single most expensive object this program makes. An allocation
// profile of a live crawl on server2 had norm.Form.String holding 34.68 GB of
// the 139.01 GB the process had ever allocated, a quarter of every byte, and
// around 40% of that box's CPU was going to the collector, so every one of those
// strings was paid for twice. #200 took the largest caller off it and this is
// what the rest of them range over instead.
//
// Nothing here copies anything. [norm.Properties.Decomposition] hands back the
// decomposition of one rune as a slice of the package's own tables, ASCII is
// emitted without a lookup because no ASCII rune decomposes, and a rune with no
// decomposition is emitted as it came.
//
// # Why this is not simply the same walk
//
// NFD does two things and only one of them is per rune. It replaces each rune
// with its decomposition, which is a table lookup, and then it puts the
// combining marks into canonical order, which is a sort across runes. A walk
// that visits one rune at a time can do the first and cannot do the second, so
// on a string where the sort would move something this would hand back a
// different sequence.
//
// The condition under which it cannot is exact rather than approximate. If no
// rune of s is itself a combining mark, then each rune's decomposition is a base
// followed by that base's own marks, already in canonical order because that is
// how the tables store it, and the next rune's decomposition starts with another
// base. Marks never meet across that boundary, so there is nothing for the sort
// to do and concatenation is the answer. If some rune of s is a loose combining
// mark, typed in rather than composed into its letter, then it can land beside
// the marks of the rune before it and the order it lands in is the whole
// question, so that string is handed to NFD and walked the old way.
//
// Text reaching this is nearly all in the first case, because [Text] brings a
// document to NFC long before anything here sees it, and a precomposed letter
// carries no loose mark by definition.
func Decomposed(s string) iter.Seq[rune] {
	return func(yield func(rune) bool) {
		if loose(s) {
			for _, c := range norm.NFD.String(s) {
				if !yield(c) {
					return
				}
			}
			return
		}
		for i := 0; i < len(s); {
			c, w := utf8.DecodeRuneInString(s[i:])
			if c < utf8.RuneSelf {
				if !yield(c) {
					return
				}
				i++
				continue
			}
			if l, v, t, ok := jamo(c); ok {
				if !yield(l) || !yield(v) {
					return
				}
				if t != 0 && !yield(t) {
					return
				}
				i += w
				continue
			}
			d := norm.NFD.PropertiesString(s[i:]).Decomposition()
			if len(d) == 0 {
				if !yield(c) {
					return
				}
				i += w
				continue
			}
			for j := 0; j < len(d); {
				r, n := utf8.DecodeRune(d[j:])
				if !yield(r) {
					return
				}
				j += n
			}
			i += w
		}
	}
}

// loose reports whether s carries a combining mark of its own rather than only
// letters with their marks composed into them. It is the question [Decomposed]
// has to answer before it can skip the canonical sort, and the answer is no for
// nearly every string this program handles.
func loose(s string) bool {
	for i := 0; i < len(s); {
		c, w := utf8.DecodeRuneInString(s[i:])
		if c < utf8.RuneSelf {
			i++
			continue
		}
		if norm.NFD.PropertiesString(s[i:]).CCC() != 0 {
			return true
		}
		i += w
	}
	return false
}

// The Hangul syllable block, which decomposes by arithmetic rather than by
// table, so [norm.Properties.Decomposition] returns nothing for it although the
// rune does decompose.
const (
	hangulBase   = 0xAC00
	hangulLead   = 0x1100
	hangulVowel  = 0x1161
	hangulTail   = 0x11A7
	hangulVowels = 21
	hangulTails  = 28
	hangulCount  = 19 * hangulVowels * hangulTails
)

// jamo takes a Hangul syllable apart into the two or three letters it is
// written with.
//
// Korean is not what this package is for and a Vietnamese tone is never in a
// Hangul syllable, so the temptation is to leave the syllable alone. That would
// be a difference from NFD rather than a shortcut past it, and the point of
// [Decomposed] is that there is no difference to find. A crawl of the open web
// turns up Korean whether or not the corpus wants it, and a function that agrees
// with NFD except on one block is a function somebody has to remember the
// exception for.
func jamo(c rune) (lead, vowel, tail rune, ok bool) {
	i := c - hangulBase
	if i < 0 || i >= hangulCount {
		return 0, 0, 0, false
	}
	lead = hangulLead + i/(hangulVowels*hangulTails)
	vowel = hangulVowel + (i%(hangulVowels*hangulTails))/hangulTails
	if t := i % hangulTails; t != 0 {
		tail = hangulTail + t
	}
	return lead, vowel, tail, true
}
