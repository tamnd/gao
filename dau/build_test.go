package dau

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/phoi"
)

func TestAnOrdinaryPageBecomesAnItem(t *testing.T) {
	b := NewBuilder(Options{})
	it, reason, ok := b.Add(hash(0), pages[0])
	if !ok {
		t.Fatalf("a page of ordinary Vietnamese was rejected: %s", reason)
	}
	if reason != "" {
		t.Errorf("an accepted document came back with reason %q", reason)
	}
	if it.Answer != pages[0] {
		t.Error("the item is not the page it was made from")
	}
	if len(b.Items()) != 1 {
		t.Errorf("the builder kept %d items, want 1", len(b.Items()))
	}
}

// This is the rejection that matters. Roughly half the Vietnamese online is
// typed without its marks, and every one of those documents is the question
// rather than the answer.
func TestADocumentTypedWithoutMarksIsNotAnAnswerKey(t *testing.T) {
	bare := phoi.Bare(pages[0])
	b := NewBuilder(Options{})

	_, reason, ok := b.Add(doc.SumString(bare), bare)
	if ok {
		t.Fatal("a page typed without marks was taken as an answer key")
	}
	if reason != Unmarked {
		t.Errorf("reason = %q, want %q", reason, Unmarked)
	}
	if b.Rejected(Unmarked) != 1 {
		t.Errorf("the rejection was not counted under %q", Unmarked)
	}
}

// Vietnamese typed with tones but without the letter marks is the awkward
// middle case, and it still has to fail, because a page that writes duong for
// đường has already thrown away half the answer.
func TestHalfMarkedVietnameseAlsoFails(t *testing.T) {
	stripped := strings.NewReplacer(
		"đ", "d", "Đ", "D", "ê", "e", "ô", "o", "ơ", "o", "ư", "u", "â", "a", "ă", "a",
	).Replace(pages[1])
	if stripped == pages[1] {
		t.Fatal("the fixture did not change anything")
	}

	b := NewBuilder(Options{MinMarked: 0.20})
	if _, reason, ok := b.Add(doc.SumString(stripped), stripped); ok || reason != Unmarked {
		t.Errorf("a half marked page was accepted, reason %q", reason)
	}
}

func TestADocumentTooShortToRestoreFromIsRejected(t *testing.T) {
	b := NewBuilder(Options{})
	_, reason, ok := b.Add(doc.SumString("Hà Nội mùa thu."), "Hà Nội mùa thu.")
	if ok {
		t.Fatal("a one line document became an item")
	}
	if reason != TooShort {
		t.Errorf("reason = %q, want %q", reason, TooShort)
	}
}

func TestTheSameDocumentTwiceIsOneItem(t *testing.T) {
	b := NewBuilder(Options{})
	if _, _, ok := b.Add(hash(0), pages[0]); !ok {
		t.Fatal("the first copy was rejected")
	}
	_, reason, ok := b.Add(hash(0), pages[0])
	if ok {
		t.Fatal("the same document became a second item")
	}
	if reason != Duplicate {
		t.Errorf("reason = %q, want %q", reason, Duplicate)
	}
}

// A builder that quietly drops nine documents in ten is a builder nobody can
// debug, so every document handed in has to be somewhere in the accounting.
func TestEveryDocumentIsAccountedFor(t *testing.T) {
	b := NewBuilder(Options{OneIn: 3})
	for i := range 200 {
		text := fmt.Sprintf("%s Bài số %d của loạt bài này.", pages[i%len(pages)], i)
		b.Add(doc.SumString(text), text)
	}

	total := len(b.Items())
	for _, r := range Reasons() {
		total += b.Rejected(r)
	}
	if total != b.Read() {
		t.Errorf("%d documents were read and %d are accounted for", b.Read(), total)
	}
	if b.Read() != 200 {
		t.Errorf("read = %d, want 200", b.Read())
	}
}

// Sampling is by document identity rather than by a random number, so a build
// is reproducible without a seed file and two boxes given the same corpus agree
// without talking to each other.
func TestTheSamplingIsDecidedByTheDocumentAndNotByChance(t *testing.T) {
	build := func() []doc.Hash {
		b := NewBuilder(Options{OneIn: 4})
		for i := range 400 {
			text := fmt.Sprintf("%s Đây là tài liệu số %d trong bộ.", pages[i%len(pages)], i)
			b.Add(doc.SumString(text), text)
		}
		out := make([]doc.Hash, 0, len(b.Items()))
		for _, it := range b.Items() {
			out = append(out, it.DocID)
		}
		return out
	}

	first, second := build(), build()
	if len(first) != len(second) {
		t.Fatalf("two builds over the same corpus kept %d and %d items", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("the builds disagree at item %d", i)
		}
	}
	// One in four, within the slack a hash gives on four hundred documents.
	if len(first) < 70 || len(first) > 130 {
		t.Errorf("one in four of 400 documents kept %d, which is not one in four", len(first))
	}
}

func TestTheLimitStopsTheBuild(t *testing.T) {
	b := NewBuilder(Options{Limit: 3})
	for i := range 50 {
		text := fmt.Sprintf("%s Phần %d của bài viết.", pages[i%len(pages)], i)
		b.Add(doc.SumString(text), text)
	}
	if len(b.Items()) != 3 {
		t.Errorf("the builder kept %d items against a limit of 3", len(b.Items()))
	}
}

// A question that stops halfway through a syllable is an easier question, since
// the fragment narrows the answer.
func TestTheWindowStopsAtABoundary(t *testing.T) {
	long := strings.Join(pages, " ")
	got := window(long, 300)

	if n := len([]rune(got)); n > 300 {
		t.Fatalf("the window is %d characters, want at most 300", n)
	}
	if n := len([]rune(got)); n < 150 {
		t.Fatalf("the window is %d characters, which is less than half of what was asked for", n)
	}
	if !strings.HasPrefix(long, got) {
		t.Error("the window is not a prefix of the text")
	}
	last, _ := lastRune(got)
	if isLetter(last) {
		t.Errorf("the window ends inside a syllable: %q", tail(got, 30))
	}
}

func TestAShortDocumentIsNotWindowed(t *testing.T) {
	if got := window(pages[0], 100000); got != strings.TrimSpace(pages[0]) {
		t.Error("a document under the window was changed")
	}
}

func TestTheOptionsFallBackRatherThanTakingZero(t *testing.T) {
	b := NewBuilder(Options{})
	d := Default()
	if b.opts.MinChars != d.MinChars || b.opts.MaxChars != d.MaxChars {
		t.Errorf("a zero length window was taken literally: %+v", b.opts)
	}
	if b.opts.MinMarked != d.MinMarked {
		t.Errorf("a zero marked floor was taken literally: %v", b.opts.MinMarked)
	}
	if b.opts.OneIn != 1 {
		t.Errorf("a zero sampling rate kept one document in %d", b.opts.OneIn)
	}
}

func TestTheDefaultFloorSitsWellBelowTheLanguage(t *testing.T) {
	// The gap is the whole point: a page about a subject short of marked vowels
	// is still Vietnamese, and a floor set at the average would keep the easy
	// pages and throw away the hard ones.
	if got := Default().MinMarked; got > 0.15 {
		t.Errorf("the marked floor is %.2f, which is close enough to the language's own rate to reject real pages", got)
	}
}

func lastRune(s string) (rune, bool) {
	rs := []rune(s)
	if len(rs) == 0 {
		return 0, false
	}
	return rs[len(rs)-1], true
}

func isLetter(r rune) bool {
	return len(syllables(string(r))) == 1
}

func tail(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[len(rs)-n:])
}
