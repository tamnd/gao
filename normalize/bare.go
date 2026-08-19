package normalize

// Vietnamese with its tone marks taken off.

import (
	"strings"
	"sync"
	"sync/atomic"
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
// Almost every call is one syllable, and a syllable arrives over and over: the
// sift asks this of every token of every document, and Vietnamese has a closed
// syllable inventory of a few thousand. Decomposing one costs two allocations
// and a table walk, and doing that a hundred million times over is most of what
// the sift spends its time on, so the answer for a short string is remembered.
func Bare(s string) string {
	if len(s) > bareMax {
		return bare(s)
	}
	if got, ok := bareSeen.Load(s); ok {
		return got.(string)
	}
	out := bare(s)
	// The cache stops growing rather than growing without a bound, because the
	// tokens of a real corpus are not a closed set: a page of hex digests is a
	// few thousand strings that will never be asked for again, and a cache that
	// kept them would be a leak with a good excuse.
	if bareSeen.Len() < bareCap {
		bareSeen.Store(s, out)
	}
	return out
}

// bareMax is the longest string worth remembering, which is a generous syllable
// rather than a phrase. bareCap is how many are kept.
const (
	bareMax = 24
	bareCap = 1 << 16
)

var bareSeen counted

// counted is a sync.Map that knows how large it is, since sync.Map does not and
// the cap above is the whole point of the cache.
type counted struct {
	m sync.Map
	n atomic.Int64
}

func (c *counted) Load(k string) (any, bool) { return c.m.Load(k) }

func (c *counted) Store(k, v string) {
	if _, loaded := c.m.LoadOrStore(k, v); !loaded {
		c.n.Add(1)
	}
}

func (c *counted) Len() int64 { return c.n.Load() }

func bare(s string) string {
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
