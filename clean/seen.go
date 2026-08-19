package clean

import (
	"encoding/binary"
	"sync"

	"github.com/tamnd/gao/doc"
)

// The set of documents a run has already admitted.
//
// Exact deduplication over a hundred million documents is a membership test and
// nothing else, so what it costs is what the set costs. A map[uint64]struct{}
// at that size is around three gigabytes of Go heap and most of it is the map's
// own bookkeeping, which is three gigabytes the smallest box in this fleet does
// not have. An open addressed table of uint64 is eight bytes a slot and nothing
// else, so the same set is one gigabyte and the size is a number somebody chose
// rather than one the runtime chose.
//
// Sixty four bits of the cluster rather than all one hundred and twenty eight.
// At a hundred million documents the chance of two different documents landing
// on one key is around one in three thousand five hundred, which is a handful
// of documents lost across the corpus and is the trade that halves the table.
// The table is not the corpus: a false positive here drops one document, and
// nothing else in the pipeline reads this key.
//
// # What it does not do
//
// It does not survive the run. A pass that is interrupted and resumed starts
// with an empty set, so a document admitted before the interruption and met
// again after it is admitted twice. That is a real limit and it is reported
// rather than hidden: the cluster is written on every row, so what a resumed
// run leaves behind is a query over the clean repo and not a re-read of the
// corpus. The same is true of two boxes cleaning two sources at once.
//
// It also does not grow. A table that doubled would double at the worst moment,
// which is the middle of a long run on a box with two gigabytes spare, and a
// run that dies of memory at document ninety million has thrown away a day. So
// the size is fixed when the run starts, and a table that fills stops admitting
// new keys and counts what it could not hold. Deduplication degrades into
// keeping documents, which is the safe direction, and the count is in the
// report so nobody has to guess whether it happened.

// SeenLoad is the share of the table that may be occupied.
//
// Open addressing degrades sharply as a table fills: at seven tenths a lookup
// probes about two slots and at nine tenths about five. Seven tenths costs
// forty three percent more memory than the keys need and is where the probe
// count stops mattering.
const SeenLoad = 0.7

// Seen is the set of clusters a run has admitted. It is safe for concurrent
// use, which is the whole reason it is a type rather than a map in the caller.
type Seen struct {
	mu   sync.Mutex
	slot []uint64
	mask uint64
	n    int
	max  int
	over int64
}

// NewSeen returns a set sized to hold keys documents.
//
// The table is rounded up to a power of two with the load factor already in it,
// so a caller asks for the number of documents it expects rather than for a
// table size. Ninety million documents is a table of 2^28 slots, which is 2.1
// GB, and eleven million is 2^25, which is 268 MB.
func NewSeen(keys int) *Seen {
	if keys < 1 {
		keys = 1
	}
	size := uint64(1)
	for float64(size)*SeenLoad < float64(keys) {
		size <<= 1
	}
	return &Seen{
		slot: make([]uint64, size),
		mask: size - 1,
		max:  int(float64(size) * SeenLoad),
	}
}

// Add records a cluster and reports whether it is new.
//
// A cluster the set has already got returns false, which is the caller's signal
// to drop the document. A cluster the table has no room for returns true, which
// keeps the document, and is counted in [Seen.Over].
func (s *Seen) Add(c doc.Cluster) bool {
	key := seenKey(c)
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := key & s.mask; ; i = (i + 1) & s.mask {
		switch s.slot[i] {
		case key:
			return false
		case 0:
			if s.n >= s.max {
				s.over++
				return true
			}
			s.slot[i] = key
			s.n++
			return true
		}
	}
}

// Len is how many distinct clusters the set holds.
func (s *Seen) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// Cap is how many it can hold before it stops.
func (s *Seen) Cap() int { return s.max }

// Over is how many documents were admitted without being checked, because the
// table was full when they arrived. It is zero on a run that was sized right,
// and a run that reports it non-zero has published a corpus with more duplicates
// in it than the run's own numbers say.
func (s *Seen) Over() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.over
}

// Bytes is what the table costs, which is what a run prints before it starts so
// that a box with two gigabytes spare is not asked for three.
func (s *Seen) Bytes() int64 { return int64(len(s.slot)) * 8 }

// seenKey is the sixty four bits of a cluster the table indexes on.
//
// Zero is the empty slot, so a cluster whose first eight bytes are all zero
// takes the next value up instead. One document in eighteen quintillion is
// filed under a key that is not its own, and the alternative is a flag on every
// slot.
func seenKey(c doc.Cluster) uint64 {
	key := binary.LittleEndian.Uint64(c[:8])
	if key == 0 {
		return 1
	}
	return key
}
