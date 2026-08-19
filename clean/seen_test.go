package clean

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"

	"github.com/tamnd/gao/doc"
)

func cluster(n uint64) doc.Cluster {
	var c doc.Cluster
	binary.LittleEndian.PutUint64(c[:8], n)
	binary.LittleEndian.PutUint64(c[8:], n*2654435761)
	return c
}

func TestSeenRemembers(t *testing.T) {
	s := NewSeen(1000)
	for i := range uint64(500) {
		if !s.Add(cluster(i + 1)) {
			t.Fatalf("cluster %d was reported as already seen the first time it arrived", i)
		}
	}
	for i := range uint64(500) {
		if s.Add(cluster(i + 1)) {
			t.Fatalf("cluster %d was reported as new the second time it arrived", i)
		}
	}
	if s.Len() != 500 {
		t.Errorf("the set holds %d clusters, want 500", s.Len())
	}
	if s.Over() != 0 {
		t.Errorf("%d documents went unchecked in a set with room for all of them", s.Over())
	}
}

func TestSeenSizesForTheKeysItWasAsked(t *testing.T) {
	// The table is a power of two with the load factor already in it, which is
	// what makes -keys a number of documents rather than a number of slots.
	cases := []struct {
		keys  int
		cap   int
		bytes int64
	}{
		{keys: 1, cap: 1, bytes: 16},
		{keys: 1000, cap: 1433, bytes: 16384},
		{keys: 120_000_000, cap: 187_904_819, bytes: 2_147_483_648},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.keys), func(t *testing.T) {
			s := NewSeen(c.keys)
			if s.Cap() < c.keys {
				t.Errorf("a set asked for %d keys holds %d", c.keys, s.Cap())
			}
			if s.Cap() != c.cap {
				t.Errorf("capacity is %d, want %d", s.Cap(), c.cap)
			}
			if s.Bytes() != c.bytes {
				t.Errorf("the table costs %d bytes, want %d", s.Bytes(), c.bytes)
			}
		})
	}
}

func TestSeenStopsRatherThanGrows(t *testing.T) {
	// A full table keeps documents and counts what it could not check, because
	// the alternatives are a table that doubles in the middle of a long run and
	// a run that quietly stops deduplicating.
	s := NewSeen(10)
	var kept int
	for i := range uint64(200) {
		if s.Add(cluster(i + 1)) {
			kept++
		}
	}
	if kept != 200 {
		t.Errorf("%d of 200 distinct clusters were kept, and a full table must keep everything", kept)
	}
	if s.Len() != s.Cap() {
		t.Errorf("the set holds %d of a capacity of %d, so it stopped early", s.Len(), s.Cap())
	}
	if s.Over() != int64(200-s.Cap()) {
		t.Errorf("%d documents were counted as unchecked, want %d", s.Over(), 200-s.Cap())
	}
}

func TestSeenFilesTheAllZeroPrefixSomewhere(t *testing.T) {
	// Zero is the empty slot, so a cluster whose first eight bytes are zero has
	// to go somewhere else. What matters is that it is remembered at all.
	var zero doc.Cluster
	s := NewSeen(16)
	if !s.Add(zero) {
		t.Fatal("the all zero prefix was reported as already seen on arrival")
	}
	if s.Add(zero) {
		t.Fatal("the all zero prefix was not remembered")
	}
	if seenKey(zero) != 1 {
		t.Errorf("the all zero prefix files under %d, want 1", seenKey(zero))
	}
}

func TestSeenUnderWorkers(t *testing.T) {
	// Every worker on a box shares one set, so exactly one of them may be told
	// that a given cluster is new. A race here publishes duplicates.
	const clusters, workers = 2000, 8
	s := NewSeen(4000)

	var mu sync.Mutex
	first := make(map[uint64]int)

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range uint64(clusters) {
				if s.Add(cluster(i + 1)) {
					mu.Lock()
					first[i+1]++
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()

	if len(first) != clusters {
		t.Fatalf("%d of %d clusters were admitted at all", len(first), clusters)
	}
	for c, n := range first {
		if n != 1 {
			t.Fatalf("cluster %d was admitted %d times by %d workers", c, n, workers)
		}
	}
}
