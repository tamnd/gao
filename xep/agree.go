package xep

// Agreement between two labelers, which is the only evidence there is that the
// rubric decides the band rather than the person.
//
// Two people placing the same document in the same band 70% of the time sounds
// like a working rubric and often is not. If four fifths of the draw is plain
// Vietnamese, two people who never read the rubric and always answered plain
// would agree 80% of the time. So the raw figure is reported next to what it
// would have been by chance, and the difference between them is the number that
// says anything. It is Scott's pi rather than Cohen's kappa because there is no
// first labeler and second labeler here: a document gets placed by whoever picks
// it up, so the marginals are pooled across both positions and the statistic
// does not depend on who happened to be written to the file first.
//
// The other half is where the disagreements are. Exact and adjacent are reported
// apart already, because a rubric people miss by one band has a boundary problem
// and a rubric people miss by two has a scale problem. But knowing that 12% of
// the comparisons were one band apart does not say which line they were on, and
// that is the thing somebody can go and fix: the sentence telling plain from
// thin gets rewritten, or the two bands get merged because nobody can tell them
// apart. So every disagreement is counted against the pair of bands it is
// between, and the worst boundary is named.
//
// Which documents get a second opinion is decided by the seed, not by the
// labelers. This looks pedantic and is not. Left to choose, people double check
// the documents they found hard, and agreement measured over the hard tenth
// understates a working rubric. Left to choose the other way, people double
// check whatever is next in the file, and agreement measured over the easy
// documents overstates one that is not working. Both are invisible in the
// finished set. A hash of the seed and the document identity settles it before
// anybody sees a document.

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"math"
	"slices"

	"github.com/tamnd/gao/doc"
	"github.com/zeebo/blake3"
)

// MinKappa is the floor on chance corrected agreement. Sixty is the
// conventional line for a scale that carries information, and it is here rather
// than higher because four ordered bands with real boundary cases in them will
// not reach the figures people quote off binary tasks.
const MinKappa = 0.60

// Prevalent is the share of one band above which the raw agreement figure has
// stopped measuring the rubric. Past this, two people agreeing mostly means two
// people have noticed which band the corpus is.
const Prevalent = 0.70

// A Boundary is one line on the scale and how many disagreements landed on it.
type Boundary struct {
	A     Band    `json:"a"`
	B     Band    `json:"b"`
	Apart int     `json:"apart"`
	Pairs int     `json:"pairs"`
	Share float64 `json:"share"`
}

// An Agreement is the second opinions, read.
type Agreement struct {
	// Pairs is how many two person comparisons there were, and Documents how
	// many documents produced them.
	Documents int `json:"documents"`
	Pairs     int `json:"pairs"`

	// Drawn is how many of the documents the seed designated for a second
	// opinion got one, of Designated. Elsewhere is second opinions on documents
	// it did not designate, which is the labelers choosing what to check.
	Designated int `json:"designated"`
	Drawn      int `json:"drawn"`
	Elsewhere  int `json:"elsewhere"`

	// Exact is both people choosing the same band and Adjacent is both landing
	// within one, reported apart because they are different problems.
	Exact    float64 `json:"exact"`
	Adjacent float64 `json:"adjacent"`

	// Chance is what Exact would have been if both people had ignored the
	// document and answered out of the band distribution, and Kappa is Exact
	// corrected for it. Weighted does the same for the ordinal scale, counting a
	// one band miss as most of an agreement and a three band miss as none of one.
	Chance   float64 `json:"chance"`
	Kappa    float64 `json:"kappa"`
	Weighted float64 `json:"weighted"`

	// Common is the band the draw is mostly made of and Prevalence its share,
	// which is what decides whether Exact was ever going to mean anything.
	Common     Band    `json:"common"`
	Prevalence float64 `json:"prevalence"`

	// Boundaries is every pair of bands two people chose between, worst first.
	Boundaries []Boundary `json:"boundaries"`
}

// Doubled reports whether a document is in the share of the draw that gets a
// second opinion. It is a hash of the seed and the document, so the tenth is
// fixed before labeling starts and a third party with the seed gets the same
// tenth.
func (f Frame) Doubled(d doc.Hash) bool {
	h := blake3.New()
	_, _ = h.Write([]byte(f.Seed))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(d[:])
	n := binary.LittleEndian.Uint64(h.Sum(nil)[:8])
	return float64(n) < Double*float64(math.MaxUint64)
}

// Agree measures agreement over the documents that were placed twice.
//
// Labels that do not belong to the frame are skipped rather than reported,
// since Read already reports them and this is the same file read a second way.
func (f Frame) Agree(labels []Label) Agreement {
	byDoc := map[doc.Hash][]Label{}
	seenBy := map[string]bool{}
	designated := map[doc.Hash]bool{}
	for _, l := range labels {
		if _, ok := f.Slice(l.Source); !ok {
			continue
		}
		if slices.Index(Bands, l.Band) < 0 {
			continue
		}
		key := l.Doc.String() + "\x00" + l.By
		if seenBy[key] {
			continue
		}
		seenBy[key] = true
		if f.Doubled(l.Doc) {
			designated[l.Doc] = true
		}
		byDoc[l.Doc] = append(byDoc[l.Doc], l)
	}

	a := Agreement{Designated: len(designated)}

	// The confusion matrix, symmetric because the pairs are unordered.
	k := len(Bands)
	matrix := make([][]int, k)
	for i := range matrix {
		matrix[i] = make([]int, k)
	}
	seen := make([]int, k)
	placed := map[Band]int{}

	for d, ls := range byDoc {
		placed[ls[0].Band]++
		if len(ls) < 2 {
			continue
		}
		a.Documents++
		if designated[d] {
			a.Drawn++
		} else {
			a.Elsewhere++
		}
		for i := range ls {
			for j := i + 1; j < len(ls); j++ {
				x, y := slices.Index(Bands, ls[i].Band), slices.Index(Bands, ls[j].Band)
				matrix[x][y]++
				matrix[y][x]++
				seen[x]++
				seen[y]++
				a.Pairs++
			}
		}
	}

	for _, b := range Bands {
		if share := share(placed[b], len(byDoc)); share > a.Prevalence {
			a.Common, a.Prevalence = b, share
		}
	}
	if a.Pairs == 0 {
		return a
	}

	// Observed agreement, plain and weighted. A miss of one band on a four band
	// scale counts as two thirds of an agreement, two bands as a third, three as
	// nothing, which is the linear weighting.
	var exact, adjacent int
	var observed float64
	for i := range k {
		for j := i; j < k; j++ {
			n := matrix[i][j]
			switch {
			case i == j:
				// The diagonal is filled twice by the symmetric fill above.
				n /= 2
				exact += n
			case j-i == 1:
				adjacent += n
			}
			observed += float64(n) * weight(i, j)
		}
	}
	observed /= float64(a.Pairs)
	adjacent += exact
	a.Exact = float64(exact) / float64(a.Pairs)
	a.Adjacent = float64(adjacent) / float64(a.Pairs)

	// Chance, from the marginals pooled across both positions.
	p := make([]float64, k)
	for i := range k {
		p[i] = float64(seen[i]) / float64(2*a.Pairs)
	}
	var chance, chanceWeighted float64
	for i := range k {
		chance += p[i] * p[i]
		for j := range k {
			chanceWeighted += p[i] * p[j] * weight(i, j)
		}
	}
	a.Chance = chance
	a.Kappa = corrected(a.Exact, chance)
	a.Weighted = corrected(observed, chanceWeighted)

	for i := range k {
		for j := i + 1; j < k; j++ {
			if matrix[i][j] == 0 {
				continue
			}
			a.Boundaries = append(a.Boundaries, Boundary{
				A: Bands[i], B: Bands[j], Apart: j - i,
				Pairs: matrix[i][j], Share: float64(matrix[i][j]) / float64(a.Pairs),
			})
		}
	}
	slices.SortStableFunc(a.Boundaries, func(x, y Boundary) int {
		if n := cmp.Compare(y.Pairs, x.Pairs); n != 0 {
			return n
		}
		return cmp.Compare(y.Apart, x.Apart)
	})
	return a
}

// Worst is the line on the scale most of the disagreement is on.
func (a Agreement) Worst() (Boundary, bool) {
	if len(a.Boundaries) == 0 {
		return Boundary{}, false
	}
	return a.Boundaries[0], true
}

// Blocking is every reason the agreement number does not go out as it stands.
func (a Agreement) Blocking() []string {
	if a.Pairs == 0 {
		return []string{"no document was placed twice, so there is no agreement here to report and the rubric has been tested against one reading of it"}
	}

	var out []string
	if a.Exact < MinExact {
		out = append(out, fmt.Sprintf("two people chose the same band %.1f%% of the time against a floor of %.0f%%, which says the rubric is not deciding the band and the labeler is",
			100*a.Exact, 100*MinExact))
	}
	if a.Adjacent < MinAdjacent {
		out = append(out, fmt.Sprintf("two people landed more than one band apart %.1f%% of the time, and a scale people can miss by two steps is four words in a list rather than a scale",
			100*(1-a.Adjacent)))
	}
	if a.Kappa < MinKappa {
		if a.Exact >= MinExact && a.Prevalence > Prevalent {
			out = append(out, fmt.Sprintf("the same band came up %.1f%% of the time and %.0f%% of the draw is %s, so chance alone gets %.1f%% and the rubric is worth %.2f above it, which is two people agreeing on what the corpus mostly is",
				100*a.Exact, 100*a.Prevalence, a.Common, 100*a.Chance, a.Kappa))
		} else {
			out = append(out, fmt.Sprintf("agreement corrected for chance is %.2f against a floor of %.2f, so %.1f%% of the raw figure is what two people guessing from the band distribution would have got",
				a.Kappa, MinKappa, 100*a.Chance))
		}
	}
	if a.Designated > 0 && a.Drawn < a.Designated {
		out = append(out, fmt.Sprintf("%d of the %d documents the seed designated for a second opinion did not get one, so the tenth that was checked is not the tenth that was drawn",
			a.Designated-a.Drawn, a.Designated))
	}
	if a.Elsewhere > 0 {
		out = append(out, fmt.Sprintf("%s the seed did not designate, and agreement measured over documents somebody chose to check is agreement over the documents they thought were worth checking",
			plural(a.Elsewhere, "second opinion is on a document")))
	}
	return out
}

// Passed reports whether the agreement number can be published.
func (a Agreement) Passed() bool { return len(a.Blocking()) == 0 }

// Verdict is the agreement in one sentence.
func (a Agreement) Verdict() string {
	if why := a.Blocking(); len(why) > 0 {
		return why[0]
	}
	worst, _ := a.Worst()
	return fmt.Sprintf("two people chose the same band %.1f%% of the time over %s, which is %.2f above chance, and most of what is left is %s against %s",
		100*a.Exact, plural(a.Pairs, "comparison"), a.Kappa, worst.A, worst.B)
}

// weight is how much of an agreement a miss of this size is worth, linear over
// the scale, so the ends of it are worth nothing and next door is worth most.
func weight(i, j int) float64 {
	return 1 - float64(abs(i-j))/float64(len(Bands)-1)
}

// corrected is the observed agreement against what chance would have produced,
// which is the whole idea: perfect is 1, chance is 0, and worse than chance is
// negative rather than clamped, because a rubric people read backwards is worth
// knowing about.
func corrected(observed, chance float64) float64 {
	if chance >= 1 {
		return 0
	}
	return (observed - chance) / (1 - chance)
}

func share(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return float64(n) / float64(of)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
