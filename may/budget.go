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
	// release at roughly 1200 shards.
	ShardBytes int64 = 512_000_000

	// Compression is the ratio between extracted text and the Parquet that
	// holds it, measured rather than assumed.
	//
	// It was 3.0 and named AssumedCompression, with a note saying the measured
	// ratio would replace it in S1 and that anything under 2.5 moves the shard
	// count. It came in at 2.07. server3 decoded 4,197,188,910 bytes of GlotCC
	// text on 2026-08-18 and wrote 2,026,901,828 bytes of Parquet across three
	// parts, and the same run is why the parts came out at 741 MB against a
	// 512 MB target: they were rolled on 1.5 GB of text, which is what 512 MB
	// costs at 3.0 and not at 2.07.
	//
	// The corpus is therefore 618 GB in the store rather than 396, and the
	// release is about 1200 shards rather than 750. Both follow from the
	// measurement and neither was negotiated afterwards.
	//
	// It is one source and the sources are not alike. GlotCC and fineweb2 both
	// come in near 2.07 and FinePDFs comes in at 1.07, which is text that has
	// already been through a PDF extractor and has had most of the repetition
	// taken out of it on the way. Nothing here averages them, because the mix is
	// not decided yet and an average of three sources weighted by which three
	// happened to run first is a worse number than one that says which source it
	// came from. What it means for the budget is that 618 GB is a floor: a
	// corpus with FinePDFs in it costs more, in proportion to how much of it is
	// FinePDFs. Where the ratio has to be right per source rather than on
	// average is in the part roll, and that reads it off the part being written
	// rather than from here.
	Compression = 2.07
)

// Budget is what a corpus of a given size costs in disk, checked against the
// fleet as it actually is.
type Budget struct {
	// Tokens is the natural token count the budget was computed for.
	Tokens int64

	// Text is the extracted text size, uncompressed.
	Text int64

	// Compressed is Text under Compression.
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
	//
	// It is counted out of scratch and not out of free disk. It was the second
	// until 'gao box' printed 581 shards on a line above a table that said 542
	// on the same box, which is the reserve being spent by one number and left
	// alone by the other.
	ShardsResident int
}

// Plan computes the disk budget for a corpus of n natural tokens against the
// current inventory.
func Plan(n int64) Budget {
	text := doc.EstimateBytes(n)
	compressed := int64(float64(text) / Compression)

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
	b.ShardsResident = int(Scratch(b.Largest) / ShardBytes)
	return b
}
