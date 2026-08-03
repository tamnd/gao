package doc

import (
	"encoding/hex"
	"fmt"

	"github.com/zeebo/blake3"
)

// HashSize is the length of a gao content hash in bytes. Everything in the
// project that identifies content by its bytes uses blake3-256: document
// identity, segment identity, shard files, and the merkle leaves under a
// snapshot signature.
const HashSize = 32

// Hash is a blake3-256 digest. It marshals as lowercase hex so that a record on
// disk stays greppable, and it is a value type so that copying a Document does
// not alias its identity.
type Hash [HashSize]byte

// Sum returns the blake3-256 digest of b.
func Sum(b []byte) Hash {
	return Hash(blake3.Sum256(b))
}

// SumString returns the blake3-256 digest of the UTF-8 bytes of s.
func SumString(s string) Hash {
	return Sum([]byte(s))
}

// IsZero reports whether the hash is the zero value, which is how an unset hash
// is represented. A real digest is never assumed to be non-zero by accident:
// callers that require a hash check IsZero explicitly.
func (h Hash) IsZero() bool {
	return h == Hash{}
}

// String returns the digest as lowercase hex.
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// MarshalText implements [encoding.TextMarshaler].
func (h Hash) MarshalText() ([]byte, error) {
	out := make([]byte, hex.EncodedLen(HashSize))
	hex.Encode(out, h[:])
	return out, nil
}

// UnmarshalText implements [encoding.TextUnmarshaler]. An empty input unmarshals
// to the zero hash, since that is how an absent hash round-trips through JSON.
func (h *Hash) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*h = Hash{}
		return nil
	}
	if len(text) != hex.EncodedLen(HashSize) {
		return fmt.Errorf("doc: hash must be %d hex characters, got %d", hex.EncodedLen(HashSize), len(text))
	}
	if _, err := hex.Decode(h[:], text); err != nil {
		return fmt.Errorf("doc: decoding hash: %w", err)
	}
	return nil
}

// ParseHash decodes a lowercase or uppercase hex digest.
func ParseHash(s string) (Hash, error) {
	var h Hash
	err := h.UnmarshalText([]byte(s))
	return h, err
}

// Shard returns the shard index for a document identifier under a corpus of n
// shards. Assignment is blake3 of the identifier rather than the identifier
// itself, so the distribution does not inherit any structure the identifier
// happens to carry.
//
// Shard assignment is deterministic and independent of processing order: the
// same document lands in the same shard on every run, on every machine, no
// matter which acquisition path ingested it or in what sequence. That is what
// makes a re-run byte identical.
//
// n is fixed per major artifact version. Changing it reshuffles every document
// and invalidates every content hash in every prior manifest, which produces a
// v2 artifact rather than a re-sharded v1.
func Shard(id Hash, n int) int {
	if n <= 0 {
		panic("doc: shard count must be positive")
	}
	d := Sum(id[:])
	// Fold the first eight bytes big-endian into a uint64 and reduce. Eight
	// bytes of a cryptographic digest is far more entropy than any plausible
	// shard count needs.
	var acc uint64
	for _, b := range d[:8] {
		acc = acc<<8 | uint64(b)
	}
	return int(acc % uint64(n))
}
