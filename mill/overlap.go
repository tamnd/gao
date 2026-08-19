package mill

// Measuring how much of one source is already in another.
//
// Five Hugging Face sources are ingested and every one of them is built out of
// Common Crawl. Adding their published token counts together is the number
// nobody should quote, because a document that appears in HPLT and in FineWeb2
// and in CulturaX has been counted three times, and there is no way to know how
// far off that sum is except by measuring.
//
// The measurement is deliberately asymmetric. GlotCC is a fraction of the size
// of HPLT, so "most of GlotCC is already in HPLT" and "a little of HPLT is
// already in GlotCC" are the same fact, and only the first is worth acting on.
// A symmetric similarity between two sets of wildly different size is a number
// that reports mostly the size difference, which is why what comes out of here
// is containment in each direction rather than one figure per pair.

import (
	"fmt"
	"math/bits"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
)

// MaxSources is how many sources one overlap measurement holds.
//
// The membership of each document is a bitset in a uint64, since a document
// that is in three of five sources has to carry which three, and half a billion
// of those is four gigabytes at eight bytes each and thirty two at anything
// wider. Five sources are ingested and the cap is well above that.
const MaxSources = 64

// An Overlap measures how much several sources share.
//
// It is one index over every source's documents rather than one index per
// source, because the question is whether two sources hold the same document
// and that is answered by them landing in the same cluster.
type Overlap struct {
	index   *Index
	names   []string
	byName  map[string]int
	seen    map[doc.Hash]uint64
	added   []int
	distinc []int
}

// NewOverlap starts a measurement at a banding.
//
// The banding should be a wide one. A pair that is never proposed as a
// candidate cannot be found at any threshold, so a matrix built at the
// pipeline's operating banding understates overlap and does it silently.
func NewOverlap(b Banding) (*Overlap, error) {
	x, err := New(b)
	if err != nil {
		return nil, err
	}
	return &Overlap{index: x, byName: map[string]int{}, seen: map[doc.Hash]uint64{}}, nil
}

// Add fingerprints one document from one source.
//
// A source that hands us the same document twice is counted once for that
// source, since the question here is what a source holds and not how many times
// it holds it. Within source duplication is [Index]'s question and it answers it
// separately.
func (o *Overlap) Add(source, text string) (doc.Hash, error) {
	i, ok := o.byName[source]
	if !ok {
		if len(o.names) == MaxSources {
			return doc.Hash{}, fmt.Errorf("xay: %d sources is the most one overlap measurement holds", MaxSources)
		}
		i = len(o.names)
		o.names = append(o.names, source)
		o.byName[source] = i
		o.added = append(o.added, 0)
		o.distinc = append(o.distinc, 0)
	}
	id := o.index.AddText(text)
	o.added[i]++
	if o.seen[id]&(1<<uint(i)) == 0 {
		o.distinc[i]++
	}
	o.seen[id] |= 1 << uint(i)
	return id, nil
}

// Sources is every source added, in the order first seen.
func (o *Overlap) Sources() []string { return slices.Clone(o.names) }

// Documents is how many documents a source handed over, its own repeats
// included.
func (o *Overlap) Documents(source string) int {
	if i, ok := o.byName[source]; ok {
		return o.added[i]
	}
	return 0
}

// A Matrix is the answer at one threshold.
type Matrix struct {
	// Threshold is the similarity two documents had to reach to be called the
	// same document.
	Threshold float64 `json:"threshold"`

	// Sources is the row and column order.
	Sources []string `json:"sources"`

	// Distinct is each source's own document count after its internal exact
	// duplicates are removed.
	Distinct []int `json:"distinct"`

	// Shared is how many of the clusters a source holds are also held by
	// another. Shared[i][j] and Shared[j][i] are the same count, and the two
	// containments that come out of it are not.
	Shared [][]int `json:"shared"`

	// Union is how many distinct documents the sources hold between them, which
	// is the only honest total.
	Union int `json:"union"`

	// Sum is the per source counts added together, which is what quoting five
	// published sizes assumes and what Union has to be compared against.
	Sum int `json:"sum"`

	// Only is how many documents each source is the sole holder of. It is the
	// number that says whether a source was worth ingesting, and it is not
	// recoverable from Shared, because a document in three sources is shared
	// with each of the other two and unique to none of them.
	Only []int `json:"only"`
}

// Matrix measures the overlap at one threshold.
func (o *Overlap) Matrix(threshold float64) Matrix {
	n := len(o.names)
	m := Matrix{
		Threshold: threshold,
		Sources:   slices.Clone(o.names),
		Distinct:  slices.Clone(o.distinc),
		Shared:    make([][]int, n),
		Only:      make([]int, n),
	}
	for i := range m.Shared {
		m.Shared[i] = make([]int, n)
		m.Sum += o.distinc[i]
	}

	// One cluster is one document as far as this measurement is concerned, so
	// the memberships of everything in a cluster are combined and counted once.
	clusters := map[doc.Cluster]uint64{}
	for _, a := range o.index.Assign(threshold) {
		clusters[a.Cluster] |= o.seen[a.ID]
	}
	m.Union = len(clusters)
	for _, in := range clusters {
		alone := bits.OnesCount64(in) == 1
		for i := range n {
			if in&(1<<uint(i)) == 0 {
				continue
			}
			if alone {
				m.Only[i]++
			}
			for j := range n {
				if in&(1<<uint(j)) != 0 {
					m.Shared[i][j]++
				}
			}
		}
	}
	return m
}

// Clusters is how many distinct documents a source holds once its near
// duplicates are folded together, which is the diagonal of the matrix.
func (m Matrix) Clusters(i int) int {
	if i < 0 || i >= len(m.Shared) {
		return 0
	}
	return m.Shared[i][i]
}

// Containment is the share of source a that is also in source b, as a
// percentage.
//
// It is the number worth acting on, and it is asymmetric on purpose. A source
// that is almost entirely inside another one is a source that buys almost
// nothing, whichever of the two is larger.
func (m Matrix) Containment(a, b int) float64 {
	if m.Clusters(a) == 0 {
		return 0
	}
	return 100 * float64(m.Shared[a][b]) / float64(m.Clusters(a))
}

// Inflation is how many times over the sources' published sizes count the same
// document.
//
// It is the one number this whole measurement exists to produce. A figure of
// 1.0 means the sources are disjoint and adding them up is honest. Anything
// above it is how far the arithmetic everybody does in their head is wrong by.
func (m Matrix) Inflation() float64 {
	if m.Union == 0 {
		return 0
	}
	return float64(m.Sum) / float64(m.Union)
}

// Contribution is the share of a source that nothing else holds, as a
// percentage. It is what ingesting that source bought.
func (m Matrix) Contribution(i int) float64 {
	if m.Clusters(i) == 0 {
		return 0
	}
	return 100 * float64(m.Only[i]) / float64(m.Clusters(i))
}

// String renders the matrix the way it is published.
func (m Matrix) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d distinct documents across %d sources at threshold %.2f, against %d counted one source at a time, which is %.2f times over\n\n",
		m.Union, len(m.Sources), m.Threshold, m.Sum, m.Inflation())

	width := 12
	for _, s := range m.Sources {
		width = max(width, len(s)+1)
	}
	fmt.Fprintf(&b, "%-*s %10s %8s", width, "of this", "documents", "only")
	for _, s := range m.Sources {
		fmt.Fprintf(&b, " %9.9s", s)
	}
	b.WriteString("\n")
	for i, s := range m.Sources {
		fmt.Fprintf(&b, "%-*s %10d %7.1f%%", width, s, m.Clusters(i), m.Contribution(i))
		for j := range m.Sources {
			fmt.Fprintf(&b, " %8.1f%%", m.Containment(i, j))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nA row reads: this share of the source on the left is also in the source above.\n")
	return b.String()
}
