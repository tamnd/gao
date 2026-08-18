package dem

// What the sources have in common, measured rather than assumed.
//
// The number everybody wants from a corpus built out of five web dumps is how
// big it is after the copies are taken out, and the number nobody publishes is
// how much of each source was already in another one. Both come from the same
// pass. The key files are sorted, so walking all of them together in one go
// yields each distinct document once along with the set of sources that hold it,
// and every question is a count over those sets.
//
// One pass rather than one pass per pair matters at this size. Five sources is
// ten pairs, and the pairs are not independent work: a document in three sources
// is in three pairs, and finding that out three times over is three times the
// reading for an answer that was already in hand the first time.
//
// The bit per source is why there is a ceiling of sixty four sources here. gao
// has five and the reason to raise the ceiling would be a corpus of a different
// shape, which would want a different pass anyway.
//
// What this counts is the same text and not the same page, and the difference is
// larger than it sounds. Identity is a blake3 over the extracted text, so
// two projects that pulled the same URL out of the same crawl are the same
// document here only if their extractors agreed on every byte, and extractors
// disagree about nav bars, boilerplate and trailing newlines as a matter of
// course. Run on 2026-08-18 over what three boxes had published, FineWeb2 and
// GlotCC shared 1628 documents out of 4,058,101 and 1,500,000, and FinePDFs
// shared none with either. Both of the first two are Common Crawl derivatives.
//
// So this is not the cheap version of the overlap measurement. It is a different
// measurement that happens to be cheap, and it is the right one for the question
// a store gets deduplicated on. The question of how much of the same page the
// corpus holds twice in two renderings is the near duplicate pass in xay, and
// reading this matrix as that number would describe the corpus as barely
// redundant on the strength of a measurement that cannot see the redundancy.

import (
	"errors"
	"fmt"
	"math/bits"
	"path/filepath"
	"strings"
)

// MaxSources is how many key files one pass can take.
const MaxSources = 64

// Source is one source as an overlap measurement sees it.
type Source struct {
	// Name is what the key file was called, without the extension.
	Name string `json:"name"`

	// Documents is how many documents were read out of the source and Distinct
	// is how many of them were different from each other.
	Documents int64 `json:"documents"`
	Distinct  int64 `json:"distinct"`

	// Only is how many of the distinct documents no other source in the pass
	// holds. It is what the source contributes that nothing else would have,
	// which is the question worth asking of a source that is expensive to
	// ingest.
	Only int64 `json:"only"`
}

// Duplication is the share of the source that is a repeat of something already
// in it, between 0 and 1.
func (s Source) Duplication() float64 {
	return Keys{Documents: s.Documents, Distinct: s.Distinct}.Duplication()
}

// Pair is what two sources have in common.
type Pair struct {
	// A and B are source names, in the order the key files were given.
	A string `json:"a"`
	B string `json:"b"`

	// Both is how many distinct documents are in each of them.
	Both int64 `json:"both"`
}

// Matrix is the answer to every set question a pass can be asked.
type Matrix struct {
	// Sources is the sources in the order their key files were given.
	Sources []Source `json:"sources"`

	// Pairs is every pair of sources once, in the order the sources are in.
	Pairs []Pair `json:"pairs"`

	// Distinct is how many different documents the sources come to together,
	// and Documents is how many were read to get there. Distinct is the corpus
	// size after deduplication and the number a release note quotes.
	Distinct  int64 `json:"distinct"`
	Documents int64 `json:"documents"`
}

// Duplication is the share of everything read that was a copy of something else
// already read, between 0 and 1. It covers both a source repeating itself and
// two sources carrying the same document.
func (m Matrix) Duplication() float64 {
	return Keys{Documents: m.Documents, Distinct: m.Distinct}.Duplication()
}

// Both is how many distinct documents the two named sources have in common.
// A source has everything it has in common with itself.
func (m Matrix) Both(a, b string) int64 {
	if a == b {
		for _, s := range m.Sources {
			if s.Name == a {
				return s.Distinct
			}
		}
		return 0
	}
	for _, p := range m.Pairs {
		if (p.A == a && p.B == b) || (p.A == b && p.B == a) {
			return p.Both
		}
	}
	return 0
}

// Share is how much of a is also in b, between 0 and 1.
//
// It is deliberately not symmetric. Half of a small source being inside a large
// one and half of a large source being inside a small one are different facts,
// and the symmetric measure reports neither.
func (m Matrix) Share(a, b string) float64 {
	for _, s := range m.Sources {
		if s.Name == a {
			if s.Distinct == 0 {
				return 0
			}
			return float64(m.Both(a, b)) / float64(s.Distinct)
		}
	}
	return 0
}

// Measure walks every key file once and reports what they have in common.
//
// The files have to be sorted and free of repeats, which is what [Builder] and
// [MergeKeys] write, and each one is checked against its own header on the way
// through so that a file that was truncated by a full disk is an error rather
// than a smaller overlap.
func Measure(files ...string) (Matrix, error) {
	if len(files) == 0 {
		return Matrix{}, errors.New("dem: no key files to measure")
	}
	if len(files) > MaxSources {
		return Matrix{}, fmt.Errorf("dem: %d key files is more than the %d one pass can hold", len(files), MaxSources)
	}

	m, err := openMerge(files)
	if err != nil {
		return Matrix{}, err
	}
	defer m.close()

	n := len(files)
	out := Matrix{Sources: make([]Source, n)}
	for i, r := range m.readers {
		name := SourceName(r.Name())
		for _, before := range out.Sources[:i] {
			if before.Name == name {
				return Matrix{}, fmt.Errorf("dem: two key files are both called %s, so the answer could not say which is which", name)
			}
		}
		out.Sources[i] = Source{Name: name, Documents: r.Keys().Documents, Distinct: r.Keys().Distinct}
		out.Documents += r.Keys().Documents
	}

	both := make([]int64, n*n)
	seen := make([]int64, n)
	for {
		_, in, ok, err := m.nextGroup()
		if err != nil {
			return Matrix{}, err
		}
		if !ok {
			break
		}
		out.Distinct++

		if bits.OnesCount64(in) == 1 {
			at := bits.TrailingZeros64(in)
			seen[at]++
			out.Sources[at].Only++
			continue
		}
		for rest := in; rest != 0; {
			i := bits.TrailingZeros64(rest)
			seen[i]++
			rest &= rest - 1
			for others := rest; others != 0; {
				j := bits.TrailingZeros64(others)
				others &= others - 1
				both[i*n+j]++
				both[j*n+i]++
			}
		}
	}

	for i, s := range out.Sources {
		if seen[i] != s.Distinct {
			return Matrix{}, fmt.Errorf("dem: %s says it holds %d distinct keys and %d came out of it, so it is truncated or was not written by gao", files[i], s.Distinct, seen[i])
		}
	}
	for i := range n {
		for j := i + 1; j < n; j++ {
			out.Pairs = append(out.Pairs, Pair{A: out.Sources[i].Name, B: out.Sources[j].Name, Both: both[i*n+j]})
		}
	}
	return out, nil
}

// SourceName is what a key file is called in an answer.
//
// The path is the caller's and the name is what gets published, so a run that
// wrote its key files somewhere with a long path does not put that path in a
// table somebody reads.
func SourceName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), KeysExt)
}
