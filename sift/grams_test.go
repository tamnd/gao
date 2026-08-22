package sift

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The gram measures are the part of the sift with the most machinery behind the
// least code, and the machinery is there for speed rather than for meaning. A
// gram is an integer, folded up a pair at a time, and the fold is carried from
// one gram size to the next. None of that shows in the answer, and it is exactly
// the kind of thing that can go subtly wrong on a long document and never on a
// short one.
//
// So the measures are checked against the slow obvious version, which joins each
// gram into a string and counts the strings, and they are checked on real
// documents rather than on a fixture written to suit them.

func refGrams(syllables []string, n int) []string {
	if len(syllables) < n {
		return nil
	}
	out := make([]string, 0, len(syllables)-n+1)
	for i := 0; i+n <= len(syllables); i++ {
		// The separator is a byte no syllable holds, so that two different
		// sequences cannot join to the same string.
		out = append(out, strings.Join(syllables[i:i+n], "\x00"))
	}
	return out
}

func refCoverage(syllables []string, n int, grams []string, want func(string) bool) float64 {
	covered := make([]bool, len(syllables))
	for i, g := range grams {
		if want(g) {
			for j := i; j < i+n; j++ {
				covered[j] = true
			}
		}
	}
	var runes, total int
	for i, s := range syllables {
		r := utf8.RuneCountInString(s)
		total += r
		if covered[i] {
			runes += r
		}
	}
	if total == 0 {
		return 0
	}
	return float64(runes) / float64(total)
}

func refTop(syllables []string, n int) float64 {
	grams := refGrams(syllables, n)
	if grams == nil {
		return 0
	}
	counts := make(map[string]int, len(grams))
	for _, g := range grams {
		counts[g]++
	}
	best, bestCount, bestRunes := "", 0, 0
	for i, g := range grams {
		runes := 0
		for _, s := range syllables[i : i+n] {
			runes += utf8.RuneCountInString(s)
		}
		if c := counts[g]; c > bestCount || (c == bestCount && runes > bestRunes) {
			best, bestCount, bestRunes = g, c, runes
		}
	}
	if bestCount < 2 {
		return 0
	}
	return refCoverage(syllables, n, grams, func(g string) bool { return g == best })
}

func refRepeat(syllables []string, n int) float64 {
	grams := refGrams(syllables, n)
	if grams == nil {
		return 0
	}
	counts := make(map[string]int, len(grams))
	for _, g := range grams {
		counts[g]++
	}
	return refCoverage(syllables, n, grams, func(g string) bool { return counts[g] > 1 })
}

// tokens is the syllable slice measureGrams is handed, built the way Measure
// builds it.
func tokens(text string) []string {
	fields := strings.Fields(text)
	out := make([]string, 0, len(fields))
	for _, tok := range fields {
		out = append(out, strings.ToLower(strip(tok)))
	}
	return out
}

func TestTheGramFoldAgreesWithJoiningTheGramsIntoStrings(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*", "*", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no documents to measure")
	}
	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		syllables := tokens(string(text))
		if len(syllables) == 0 {
			continue
		}
		g := newGrams(syllables)
		for _, n := range TopGramSizes {
			if got, want := g.top(n), refTop(syllables, n); math.Abs(got-want) > 1e-12 {
				t.Errorf("%s: the most frequent gram of %d syllables covers %v of it, and joining the grams says %v", file, n, got, want)
			}
		}
		for _, n := range RepeatGramSizes {
			if got, want := g.repeat(n), refRepeat(syllables, n); math.Abs(got-want) > 1e-12 {
				t.Errorf("%s: repeated grams of %d syllables cover %v of it, and joining the grams says %v", file, n, got, want)
			}
		}
	}
}

// The labeled documents are a few hundred syllables each and most of them
// repeat nothing, so on their own they check the fold where it is least likely
// to be wrong. This puts every one of them end to end and then says the whole
// thing twice, which is a document of some thousands of syllables in which every
// gram of every size occurs exactly twice, and it is the case where an
// off by one in the fold has somewhere to hide.
func TestTheGramFoldAgreesOnALongDocumentThatRepeatsItself(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*", "*", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(text)
		b.WriteString("\n")
	}
	once := b.String()
	syllables := tokens(once + "\n" + once)
	if len(syllables) < 2000 {
		t.Fatalf("the corpus twice over measured %d syllables, which is too few to be the long case", len(syllables))
	}

	g := newGrams(syllables)
	for _, n := range TopGramSizes {
		got, want := g.top(n), refTop(syllables, n)
		if math.Abs(got-want) > 1e-12 {
			t.Errorf("the most frequent gram of %d syllables covers %v of it, and joining the grams says %v", n, got, want)
		}
	}
	for _, n := range RepeatGramSizes {
		got, want := g.repeat(n), refRepeat(syllables, n)
		if math.Abs(got-want) > 1e-12 {
			t.Errorf("repeated grams of %d syllables cover %v of it, and joining the grams says %v", n, got, want)
		}
		if got < 0.9 {
			t.Errorf("a document that says everything twice has only %v of it inside a repeated gram of %d syllables", got, n)
		}
	}
}

// The fold is carried from one gram size to the next, which is only sound while
// the sizes arrive in increasing order. They do, and the two size lists are
// exported, so a caller can reorder them and this says what happens when one
// does.
func TestTheGramFoldAnswersTheSameWhicheverOrderTheSizesArriveIn(t *testing.T) {
	syllables := tokens(article)
	if len(syllables) < 20 {
		t.Fatalf("the article measured %d syllables, which is too few to gram", len(syllables))
	}
	sizes := []int{17, 3, 12, 5, 8, 7}

	up := newGrams(syllables)
	want := make(map[int]float64, len(sizes))
	for _, n := range []int{3, 5, 7, 8, 12, 17} {
		want[n] = up.repeat(n)
	}

	down := newGrams(syllables)
	for _, n := range sizes {
		if got := down.repeat(n); math.Abs(got-want[n]) > 1e-12 {
			t.Errorf("asked out of order, repeated grams of %d syllables cover %v, and in order they cover %v", n, got, want[n])
		}
	}
}
