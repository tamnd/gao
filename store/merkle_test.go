package store

import (
	"fmt"
	"testing"

	"github.com/tamnd/gao/doc"
)

// leaves builds n distinct hashes.
func leaves(n int) []doc.Hash {
	out := make([]doc.Hash, n)
	for i := range out {
		out[i] = doc.SumString(fmt.Sprintf("shard %d", i))
	}
	return out
}

func TestRootOfOneLeafIsNotTheLeaf(t *testing.T) {
	l := leaves(1)
	if got := MerkleRoot(l); got == l[0] {
		t.Fatal("the root of a single leaf is the raw leaf, so a leaf can be presented as a root")
	}
	if got, want := MerkleRoot(l), hashLeaf(l[0]); got != want {
		t.Fatalf("root = %s, want the tagged leaf %s", got, want)
	}
}

func TestEmptyTreeIsTheZeroHash(t *testing.T) {
	if got := MerkleRoot(nil); !got.IsZero() {
		t.Fatalf("root of nothing = %s, want the zero hash", got)
	}
}

func TestRootChangesWithEveryLeaf(t *testing.T) {
	const n = 9
	base := leaves(n)
	root := MerkleRoot(base)

	for i := range n {
		changed := make([]doc.Hash, n)
		copy(changed, base)
		changed[i] = doc.SumString("tampered")
		if got := MerkleRoot(changed); got == root {
			t.Fatalf("changing leaf %d of %d left the root alone", i, n)
		}
	}
}

func TestRootDependsOnOrder(t *testing.T) {
	l := leaves(4)
	forward := MerkleRoot(l)
	l[0], l[3] = l[3], l[0]
	if swapped := MerkleRoot(l); swapped == forward {
		t.Fatal("swapping two shards left the root alone, so the root does not commit to the order")
	}
}

// An odd node is promoted rather than hashed against a copy of itself. The
// duplicate-last shortcut is what lets a tree of three leaves and a tree of four
// leaves whose last two are equal produce the same root, which is a second
// preimage on the corpus: a snapshot with a shard appended verifies against the
// signature of the snapshot without it.
func TestOddNodeIsPromotedRatherThanDuplicated(t *testing.T) {
	three := leaves(3)
	four := []doc.Hash{three[0], three[1], three[2], three[2]}
	if MerkleRoot(three) == MerkleRoot(four) {
		t.Fatal("three leaves and four leaves with the last duplicated share a root")
	}
}

func TestLeavesAndInteriorsAreDomainSeparated(t *testing.T) {
	// Without the tags, a leaf whose value happens to be an interior hash could
	// be presented as that whole subtree.
	l, r := leaves(2)[0], leaves(2)[1]
	interior := hashInterior(l, r)
	if hashLeaf(interior) == interior {
		t.Fatal("hashing a leaf is the identity, so leaves and interiors are not separated")
	}
	if hashLeaf(l) == hashInterior(l, doc.Hash{}) {
		t.Fatal("a leaf and an interior node hash the same way")
	}
}

func TestRootIsStableAcrossCalls(t *testing.T) {
	l := leaves(750)
	first := MerkleRoot(l)
	second := MerkleRoot(l)
	if first != second {
		t.Fatalf("two calls on the same leaves gave %s and %s", first, second)
	}
	// The same leaves in a fresh slice give the same root, so the root is a
	// function of the values and not of the slice they arrived in.
	if third := MerkleRoot(append([]doc.Hash(nil), l...)); third != first {
		t.Fatalf("a copy of the leaves gave %s, want %s", third, first)
	}
}

func TestRootAtEverySizeThroughAFullSnapshot(t *testing.T) {
	seen := make(map[doc.Hash]int, 64)
	for n := 1; n <= 64; n++ {
		root := MerkleRoot(leaves(n))
		if prev, ok := seen[root]; ok {
			t.Fatalf("a tree of %d leaves and a tree of %d leaves share a root", prev, n)
		}
		seen[root] = n
	}
}

func BenchmarkMerkleRoot(b *testing.B) {
	l := leaves(750)
	b.ResetTimer()
	for b.Loop() {
		MerkleRoot(l)
	}
}
