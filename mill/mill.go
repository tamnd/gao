// Package mill is the milling stage: it finds the documents a corpus holds more
// than one copy of.
//
// The web republishes. A news agency writes an article and forty sites carry it,
// a forum quotes a post inside a reply to it, a legal notice appears at the foot
// of every page of a ministry site, and a scraper that took a site twice took
// the front page twice. None of that is rare and all of it trains: a model sees
// the duplicated text as many times as the corpus holds it, and what it learns
// from the repetition is to reproduce it.
//
// Exact copies are found by identity. The document id is already blake3 over the
// normalized text, so two documents that are the same bytes are the same id, and
// finding them costs a hash table. That is the easy half and it is not the half
// that matters, because a republisher almost never publishes the same bytes.
//
// The other half is minhash with a banded index. Each document becomes 128
// numbers that stand in for its set of character five-grams, two documents that
// share most of their text agree on most of those numbers, and the bands turn
// "agree on most" into a lookup that does not compare every pair against every
// other pair. The alternative is a hundred million documents squared, which is
// not a slow program, it is a program that does not finish.
//
// # What this package holds and what it does not
//
// A signature is 1 KB, and that number decides the shape of a corpus scale
// run. Four hundred million documents is 400 GB of signatures against a fleet
// whose largest box has 64 GB, so the index here, which keeps signatures in
// memory to be able to answer at more than one threshold, is for a shard and
// for the ablation. The corpus scale pass keeps only the band hashes, 128
// bytes per document, and works one band at a time from a file sorted on disk
// in the way count sorts document keys. That pass is not in this package yet,
// and the arithmetic that says it is needed is written here rather than
// discovered on the box.
//
// # Why the threshold is a measurement
//
// Deduplication is tuned rather than maximized. Removing more duplicates is not
// better past some point, since the corpus loses documents that were merely
// similar, and removing fewer leaves the repetition in. Where that point is, is
// a question about this corpus that is answered by training on both sides of it,
// which is what the ablation harness is for. So this package produces a curve of
// what each threshold would retain, and nothing in it decides the threshold.
package mill

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/tamnd/gao/doc"
)

// Index holds the fingerprints of a set of documents and answers which of them
// are copies of each other.
//
// Adding is streaming and clustering is not. Add can be called as documents
// arrive, and the buckets it fills are independent of any threshold, so one
// pass over a shard answers at every threshold an ablation asks about.
//
// An Index is not safe for concurrent use.
type Index struct {
	banding Banding

	// One entry per distinct document identity, in the order first seen.
	ids    []doc.Hash
	sizes  []int32
	sigs   []Signature
	copies []int32

	byID  map[doc.Hash]int32
	bands []map[uint64][]int32

	docs int
}

// New returns an empty index at the given banding.
func New(b Banding) (*Index, error) {
	if err := b.check(); err != nil {
		return nil, err
	}
	x := &Index{
		banding: b,
		byID:    make(map[doc.Hash]int32),
		bands:   make([]map[uint64][]int32, b.Bands),
	}
	for i := range x.bands {
		x.bands[i] = make(map[uint64][]int32)
	}
	return x, nil
}

// Banding returns the banding the index was built at.
func (x *Index) Banding() Banding { return x.banding }

// Add fingerprints one document. Size is what the representative of a cluster is
// chosen by, and it is the length of the text in runes.
//
// A document whose identity is already in the index is an exact copy. It is
// counted and its signature is not stored again, because it is the same
// signature by construction.
func (x *Index) Add(id doc.Hash, size int, sig Signature) {
	x.docs++
	if i, ok := x.byID[id]; ok {
		x.copies[i]++
		return
	}
	i := int32(len(x.ids))
	x.ids = append(x.ids, id)
	x.sizes = append(x.sizes, int32(size))
	x.sigs = append(x.sigs, sig)
	x.copies = append(x.copies, 1)
	x.byID[id] = i
	for band, h := range x.banding.Hashes(sig) {
		x.bands[band][h] = append(x.bands[band][h], i)
	}
}

// AddText fingerprints a document from its text, which is what a caller that has
// the text and not a signature wants. The identity is blake3 over the text, the
// same hash the ingest contract assigns.
func (x *Index) AddText(text string) doc.Hash {
	id := doc.SumString(text)
	x.Add(id, len([]rune(text)), Sign(text))
	return id
}

// Documents is how many documents were added, copies included.
func (x *Index) Documents() int { return x.docs }

// Distinct is how many document identities were added, which is the count after
// exact duplicates are removed and before near duplicates are considered.
func (x *Index) Distinct() int { return len(x.ids) }

// Report is what a deduplication run at one threshold did.
type Report struct {
	// Threshold is the similarity at or above which two documents were called
	// copies of each other.
	Threshold float64 `json:"threshold"`

	// Documents is every document that went in.
	Documents int `json:"documents"`

	// Exact is documents that were byte for byte copies of another document.
	Exact int `json:"exact"`

	// Near is documents dropped for being near copies, exact ones excluded, so
	// that the two are never quoted as one number.
	Near int `json:"near"`

	// Kept is the documents a deduplicated view holds, one per cluster.
	Kept int `json:"kept"`

	// Clusters is how many clusters held more than one document. It is the count
	// of things that were republished rather than the count of documents that
	// were dropped, and the two are different questions.
	Clusters int `json:"clusters"`

	// Largest is the size of the biggest cluster, in documents. A run whose
	// largest cluster is a hundred thousand documents has found boilerplate
	// rather than duplication, and that is worth seeing next to the totals.
	Largest int `json:"largest"`
}

// Retention is the share of documents a deduplicated view keeps.
func (r Report) Retention() float64 {
	if r.Documents == 0 {
		return 0
	}
	return float64(r.Kept) / float64(r.Documents)
}

// Assignment is where one document ended up.
type Assignment struct {
	ID      doc.Hash    `json:"doc_id"`
	Cluster doc.Cluster `json:"dup_cluster"`

	// Size is how many documents the cluster holds, exact copies included.
	Size int `json:"dup_cluster_size"`

	// Representative marks the one document per cluster a deduplicated view
	// keeps. The rest stay in the store, because a threshold that is tuned has
	// to be retunable without fetching anything again.
	Representative bool `json:"is_representative"`
}

// Dedup returns the assignment in the form a document carries.
func (a Assignment) Dedup() doc.Dedup {
	return doc.Dedup{
		DupCluster:       a.Cluster,
		DupClusterSize:   uint32(a.Size),
		IsRepresentative: a.Representative,
	}
}

// Cluster groups the documents at one threshold and reports what that would
// remove. It does not change the index, so it can be called again at another
// threshold.
func (x *Index) Cluster(threshold float64) Report {
	report, _ := x.cluster(threshold)
	return report
}

// Assign groups the documents at one threshold and returns where each distinct
// identity ended up, in the order the identities were added.
func (x *Index) Assign(threshold float64) []Assignment {
	_, assignments := x.cluster(threshold)
	return assignments
}

// Curve is what each of several thresholds would retain, which is the input to
// choosing one. The thresholds come back in the order given.
//
// It is worth running at a banding wider than the one the pipeline uses, since a
// pair that was never proposed as a candidate cannot be scored at any threshold,
// and a curve built at the operating point would show the low thresholds keeping
// more than they really would.
func (x *Index) Curve(thresholds ...float64) []Report {
	out := make([]Report, 0, len(thresholds))
	for _, t := range thresholds {
		out = append(out, x.Cluster(t))
	}
	return out
}

// cluster does the work behind Cluster and Assign.
//
// Within a bucket every member is compared against the first member rather than
// against every other member, which is linear instead of quadratic in the size
// of the bucket. Boilerplate produces buckets of thousands, so the quadratic
// version is not a constant factor, it is the run not finishing. What the linear
// version can miss is a pair that matched in this band and did not match the
// bucket's first member: those are found through another band, or through a
// third document both of them match, which is what a union of clusters is for.
func (x *Index) cluster(threshold float64) (Report, []Assignment) {
	n := len(x.ids)
	parent := make([]int32, n)
	for i := range parent {
		parent[i] = int32(i)
	}

	for _, bucketsOfBand := range x.bands {
		for _, members := range bucketsOfBand {
			if len(members) < 2 {
				continue
			}
			first := members[0]
			for _, m := range members[1:] {
				if x.sigs[first].Similarity(x.sigs[m]) >= threshold {
					union(parent, first, m)
				}
			}
		}
	}

	// Group by root, and pick the document each cluster is represented by.
	members := make(map[int32][]int32, n)
	for i := range parent {
		r := find(parent, int32(i))
		members[r] = append(members[r], int32(i))
	}

	report := Report{Threshold: threshold, Documents: x.docs}
	report.Exact = x.docs - n
	assignments := make([]Assignment, n)
	for _, group := range members {
		best := group[0]
		size := 0
		for _, i := range group {
			size += int(x.copies[i])
			if x.sizes[i] > x.sizes[best] || (x.sizes[i] == x.sizes[best] && bytesLess(x.ids[i], x.ids[best])) {
				best = i
			}
		}
		id := doc.Cluster(x.ids[best][:len(doc.Cluster{})])
		for _, i := range group {
			assignments[i] = Assignment{
				ID:             x.ids[i],
				Cluster:        id,
				Size:           size,
				Representative: i == best,
			}
		}
		report.Kept++
		report.Near += len(group) - 1
		if size > 1 {
			report.Clusters++
		}
		report.Largest = max(report.Largest, size)
	}
	return report, assignments
}

// String is the one line a report prints as.
func (r Report) String() string {
	return fmt.Sprintf("threshold %.2f: %d documents, %d exact and %d near duplicates, %d kept (%.1f%%) in %d clusters",
		r.Threshold, r.Documents, r.Exact, r.Near, r.Kept, r.Retention()*100, r.Clusters)
}

func union(parent []int32, a, b int32) {
	ra, rb := find(parent, a), find(parent, b)
	if ra == rb {
		return
	}
	// Attach the later root to the earlier one, so that the result does not
	// depend on which band happened to be walked first.
	if rb < ra {
		ra, rb = rb, ra
	}
	parent[rb] = ra
}

func find(parent []int32, i int32) int32 {
	for parent[i] != i {
		parent[i] = parent[parent[i]] // path halving
		i = parent[i]
	}
	return i
}

func bytesLess(a, b doc.Hash) bool { return slices.Compare(a[:], b[:]) < 0 }

// SortReports puts a curve in threshold order, which is how it is read.
func SortReports(r []Report) {
	slices.SortFunc(r, func(a, b Report) int { return cmp.Compare(a.Threshold, b.Threshold) })
}
