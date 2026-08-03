package kho

import (
	"github.com/tamnd/gao/doc"
	"github.com/zeebo/blake3"
)

// Domain separation tags for the merkle tree. A leaf and an interior node are
// hashed with different prefixes so that no leaf value can be mistaken for a
// precomputed subtree, which is the attack that turns a signature over a root
// into a signature over something else.
const (
	leafTag     = 0x00
	interiorTag = 0x01
)

// MerkleRoot returns the root of a binary merkle tree over the shard hashes, in
// the order given. The order is part of the commitment: the same shards listed
// differently produce a different root, which is intended, because a manifest
// that reordered its shards is a manifest that changed.
//
// An odd node at any level is promoted to the next level rather than hashed
// against a copy of itself. Duplicating the last node is the common shortcut and
// it is the one that lets two different leaf sets produce the same root.
//
// The root of an empty tree is the zero hash. A snapshot with no shards is not a
// snapshot, so nothing signs that value, and returning it is simpler than
// inventing an empty-tree constant nobody would ever check.
func MerkleRoot(leaves []doc.Hash) doc.Hash {
	if len(leaves) == 0 {
		return doc.Hash{}
	}

	level := make([]doc.Hash, len(leaves))
	for i, h := range leaves {
		level[i] = hashLeaf(h)
	}

	for len(level) > 1 {
		next := make([]doc.Hash, 0, (len(level)+1)/2)
		for i := 0; i+1 < len(level); i += 2 {
			next = append(next, hashInterior(level[i], level[i+1]))
		}
		if len(level)%2 == 1 {
			next = append(next, level[len(level)-1])
		}
		level = next
	}
	return level[0]
}

func hashLeaf(h doc.Hash) doc.Hash {
	d := blake3.New()
	_, _ = d.Write([]byte{leafTag})
	_, _ = d.Write(h[:])
	return doc.Hash(d.Sum(nil)[:doc.HashSize])
}

func hashInterior(l, r doc.Hash) doc.Hash {
	d := blake3.New()
	_, _ = d.Write([]byte{interiorTag})
	_, _ = d.Write(l[:])
	_, _ = d.Write(r[:])
	return doc.Hash(d.Sum(nil)[:doc.HashSize])
}
