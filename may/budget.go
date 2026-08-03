package may

import (
	"github.com/tamnd/gao/doc"
)

const (
	// TargetTokens is the gao v1 natural token target. It is here rather than
	// only in the spec because the disk arithmetic below is the thing that
	// decides whether the target is reachable on the hardware we have, and that
	// calculation should be one function call rather than a paragraph somebody
	// has to redo.
	TargetTokens int64 = 300_000_000_000

	// ShardBytes is the target compressed size of one shard, which puts the v1
	// release at roughly 750 shards.
	ShardBytes int64 = 512_000_000

	// AssumedCompression is the zstd ratio the disk budget assumes for
	// Vietnamese text. It is an assumption and it is named like one: the
	// measured ratio replaces it in S1, and if it comes in below 2.5 the shard
	// count moves and the budget below moves with it.
	AssumedCompression = 3.0
)

// Budget is what a corpus of a given size costs in disk, checked against the
// fleet as it actually is.
type Budget struct {
	// Tokens is the natural token count the budget was computed for.
	Tokens int64

	// Text is the extracted text size, uncompressed.
	Text int64

	// Compressed is Text under AssumedCompression.
	Compressed int64

	// Shards is how many shards Compressed comes to at ShardBytes each.
	Shards int

	// FleetFree is the sum of free disk across every box, which is the number
	// that looks encouraging and is not the one that matters.
	FleetFree int64

	// Largest is the box with the most free disk, which is the number that does
	// matter, because a working set split across four machines is four working
	// sets and not one large one.
	Largest Box

	// Resident is whether the compressed corpus fits on Largest. When it is
	// false, and it is false, the store of record lives off-box and every stage
	// streams.
	Resident bool

	// ShardsResident is how many shards fit on Largest at once. It is the real
	// working set limit and it is what a stage's concurrency has to respect.
	ShardsResident int
}

// Plan computes the disk budget for a corpus of n natural tokens against the
// current inventory.
func Plan(n int64) Budget {
	text := doc.EstimateBytes(n)
	compressed := int64(float64(text) / AssumedCompression)

	b := Budget{
		Tokens:     n,
		Text:       text,
		Compressed: compressed,
		Shards:     int((compressed + ShardBytes - 1) / ShardBytes),
		Largest:    Largest(),
	}
	for _, box := range Boxes {
		b.FleetFree += box.FreeDisk
	}
	_, b.Resident = Holds(compressed)
	b.ShardsResident = int(b.Largest.FreeDisk / ShardBytes)
	return b
}
