package soi

import (
	"math/rand/v2"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// The one property of the alignment that is worth asserting and the one that is
// easy to get wrong. Hirschberg's method is an optimization of a table nobody
// would get wrong, so the table is written here and the optimization is held to
// it on every input small enough to hold both.
func TestTheAlignmentIsAsGoodAsTheTableVersion(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("aăâeêoôơuưiđ dàáảãạếềộứ,5")

	pick := func(n int) []Letter {
		out := make([]Letter, n)
		for i := range out {
			out[i] = Split(alphabet[r.IntN(len(alphabet))])
		}
		return out
	}

	for range 400 {
		ref, read := pick(r.IntN(14)), pick(r.IntN(14))
		ops := Align(ref, read)

		if got, want := Cost(ops), naiveCost(ref, read); got != want {
			t.Fatalf("%s against %s aligned at cost %d, and the table version finds %d",
				show(ref), show(read), got, want)
		}
		if got, want := shown(ops, true), show(ref); got != want {
			t.Fatalf("the alignment of %s reads back the reference as %s", show(ref), got)
		}
		if got, want := shown(ops, false), show(read); got != want {
			t.Fatalf("the alignment of %s reads back the reading as %s", show(read), got)
		}
	}
}

// The alignment has to reach for a marked variant of a letter rather than for
// anything else nearby, because that is the whole reason for the cost table and
// the only reason this package can tell a lost tone from a lost letter.
func TestAnAlignmentPrefersALetterToItsOwnMarkedForm(t *testing.T) {
	for _, c := range []struct{ name, ref, read, want string }{
		{"a mark taken off in the middle of a word", "tiếng", "tieng", "tiếng|tieng"},
		{"a character dropped", "tiếng", "ting", "tiếng|ti-ng"},
		{"a character invented", "ting", "tiếng", "ti-ng|tiếng"},
		{"a run of marks taken off", "đường", "duong", "đường|duong"},
		{"nothing in common", "abc", "xyz", "abc|xyz"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ops := Align(Letters(c.ref), Letters(c.read))
			var a, b strings.Builder
			for _, op := range ops {
				if op.HaveRef {
					a.WriteRune(compose(op.Ref))
				} else {
					a.WriteByte('-')
				}
				if op.HaveRead {
					b.WriteRune(compose(op.Read))
				} else {
					b.WriteByte('-')
				}
			}
			if got := a.String() + "|" + b.String(); got != c.want {
				t.Errorf("%q against %q lined up as %s, want %s", c.ref, c.read, got, c.want)
			}
		})
	}
}

// The edges, because a page against nothing and nothing against a page are both
// things an evaluation set will contain the first time an engine times out.
func TestAligningAgainstNothing(t *testing.T) {
	page := Letters("Việt")

	dropped := Align(page, nil)
	if len(dropped) != 4 || Cost(dropped) != 4*costGap {
		t.Errorf("a page read as nothing came to %d steps at cost %d", len(dropped), Cost(dropped))
	}
	for _, op := range dropped {
		if op.HaveRead {
			t.Error("a step of a reading that produced nothing holds a character")
		}
	}

	invented := Align(nil, page)
	if len(invented) != 4 || Cost(invented) != 4*costGap {
		t.Errorf("nothing read as a page came to %d steps at cost %d", len(invented), Cost(invented))
	}

	if ops := Align(nil, nil); len(ops) != 0 {
		t.Errorf("nothing against nothing came to %d steps", len(ops))
	}
}

// Hirschberg splits the reference in half and recurses, so the sizes where it
// stops splitting are the sizes where it is worth checking by hand.
func TestTheSmallSizesWhereTheRecursionStops(t *testing.T) {
	for _, c := range []struct{ ref, read string }{
		{"a", ""}, {"", "a"}, {"a", "a"}, {"a", "á"}, {"a", "xyz"}, {"á", "xáy"},
		{"ab", "ba"}, {"abc", "b"}, {"ế", "ếế"}, {"ếế", "ế"},
	} {
		ops := Align(Letters(c.ref), Letters(c.read))
		if got, want := Cost(ops), naiveCost(Letters(c.ref), Letters(c.read)); got != want {
			t.Errorf("%q against %q aligned at cost %d, and the table version finds %d", c.ref, c.read, got, want)
		}
	}
}

// A page of real length, since the reason this is written in linear space is
// that the table version cannot hold one and nobody finds that out until the
// evaluation set has a book in it.
func TestAPageSizedAlignment(t *testing.T) {
	const line = "Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập, tự do, hạnh phúc. "
	ref := Letters(strings.Repeat(line, 200))
	read := Letters(strings.Repeat(dropTones(line), 200))

	ops := Align(ref, read)
	if len(ops) != len(ref) {
		t.Fatalf("%d characters against the same %d lined up in %d steps", len(ref), len(read), len(ops))
	}
	if s := MeasureLetters(ref, read); s.ToneDeletionRate() != 1 || s.Dropped != 0 || s.Added != 0 {
		t.Errorf("a page of %d characters with the tones taken off came to %+v", len(ref), s)
	}
}

// naiveCost is the full table, which is what the linear space version has to
// agree with. It is deliberately the dumbest correct implementation.
func naiveCost(ref, read []Letter) int {
	d := make([][]int, len(ref)+1)
	for i := range d {
		d[i] = make([]int, len(read)+1)
		d[i][0] = i * costGap
	}
	for j := range d[0] {
		d[0][j] = j * costGap
	}
	for i, a := range ref {
		for j, b := range read {
			sub, _ := substitution(a, b)
			c := d[i][j] + sub
			if v := d[i][j+1] + costGap; v < c {
				c = v
			}
			if v := d[i+1][j] + costGap; v < c {
				c = v
			}
			d[i+1][j+1] = c
		}
	}
	return d[len(ref)][len(read)]
}

// compose puts a letter back together, which only the tests need: the metric
// never writes text out, and a failure message with three code points in it
// where a reader expects one character is a failure message nobody can read.
func compose(l Letter) rune {
	if l.Mod == stroke {
		if l.Base == 'D' {
			return 'Đ'
		}
		return 'đ'
	}
	var b strings.Builder
	b.WriteRune(l.Base)
	if l.Mod != 0 {
		b.WriteRune(l.Mod)
	}
	for _, m := range []struct {
		t Tone
		r rune
	}{{Huyen, grave}, {Sac, acute}, {Hoi, hook}, {Nga, tilde}, {Nang, dot}} {
		if l.Tone == m.t {
			b.WriteRune(m.r)
		}
	}
	for _, r := range norm.NFC.String(b.String()) {
		return r
	}
	return l.Base
}

func show(ls []Letter) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteRune(compose(l))
	}
	return b.String()
}

// shown reads one side of an alignment back out, so a test can require that an
// alignment is a rearrangement of its two inputs and not a rewriting of them.
func shown(ops []Op, ref bool) string {
	var b strings.Builder
	for _, op := range ops {
		switch {
		case ref && op.HaveRef:
			b.WriteRune(compose(op.Ref))
		case !ref && op.HaveRead:
			b.WriteRune(compose(op.Read))
		}
	}
	return b.String()
}
