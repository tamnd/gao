package soi

// Lining a reading up against the page it was read from.
//
// Every number in this package comes out of one decision: which character in the
// reading corresponds to which character on the page. Nothing else here is
// subtle and this is, because the answer is not unique. A reading that dropped
// three characters and added two can be lined up several ways at the same total
// cost, and which one is chosen decides whether the report says a mark went
// missing or says a letter did.
//
// # The cost table
//
// The weights are the whole design and they are not the usual ones.
//
//	same character           0
//	same letter, other mark  1
//	different letter         3
//	dropped or added         2
//
// Ordinary edit distance uses one for every substitution and one for every
// insertion, and under those weights nothing prefers to line ế up with e rather
// than with whatever else is nearby. Here it does: a mark that changed is the
// cheapest thing that can happen short of nothing happening, so an alignment
// will reach for it, and the report gets to say that the letter came through and
// the tone did not. That is the sentence this package exists to be able to write.
//
// Dropped or added at 2 rather than 1 keeps a substitution cheaper than a
// deletion followed by an insertion, which is the standard reason for that
// ratio. Different letter at 3 keeps it under 4, so two unrelated characters
// still line up rather than passing each other, but above 2, so a reading that
// genuinely lost a character is not made to look like one that misread it.
//
// # Why it is written this way
//
// The obvious implementation fills an n by m table of back pointers, and on a
// page of ten thousand characters against a reading of ten thousand that is a
// hundred million cells. An OCR evaluation set is a few hundred pages, and this
// would be the only part of the pipeline that could not read a book.
//
// Hirschberg's method gets the same alignment in space proportional to the
// shorter side. It computes the cost of the first half forwards and the second
// half backwards, both in one row of memory, finds the column where the two
// halves meet most cheaply, and recurses on each side of that point. It does
// twice the arithmetic of the table version and holds two rows instead of
// n times m, which is the right trade for text.
//
// The naive version is in the tests, and every alignment this produces is
// checked against it for equal cost on inputs small enough to hold both.

const (
	costSame = 0
	costMark = 1
	costGap  = 2
	costSub  = 3
)

// An Op is one step of an alignment.
type Op struct {
	// Ref and Read are the characters that were lined up. Exactly one of them is
	// absent when the step is a gap, and the two flags say which.
	Ref, Read         Letter
	HaveRef, HaveRead bool
}

// substitution reports the cost of reading a as b, and whether that is a
// diacritic difference rather than a different letter.
func substitution(a, b Letter) (int, bool) {
	if a.Base != b.Base {
		return costSub, false
	}
	if a.Mod == b.Mod && a.Tone == b.Tone {
		return costSame, false
	}
	return costMark, true
}

// Align lines a reference up against a reading and returns the steps in order.
//
// The reference is what the page says and the reading is what came out of the
// machine, and the two are not interchangeable: every rate in this package is a
// share of the reference, so passing them the wrong way round produces numbers
// that look plausible and answer a question nobody asked.
func Align(ref, read []Letter) []Op {
	var out []Op
	align(ref, read, &out)
	return out
}

func align(ref, read []Letter, out *[]Op) {
	switch {
	case len(ref) == 0:
		for _, r := range read {
			*out = append(*out, Op{Read: r, HaveRead: true})
		}
		return
	case len(read) == 0:
		for _, r := range ref {
			*out = append(*out, Op{Ref: r, HaveRef: true})
		}
		return
	case len(ref) == 1:
		alignOne(ref[0], read, out)
		return
	}

	// The reference is cut in half and the reading is cut at the column where
	// the two halves meet most cheaply, which is the whole of Hirschberg's
	// method. Everything else here is bookkeeping.
	mid := len(ref) / 2
	head := costs(ref[:mid], read)
	tail := costs(reversed(ref[mid:]), reversed(read))

	at, best := 0, head[0]+tail[len(read)]
	for i := 1; i <= len(read); i++ {
		if c := head[i] + tail[len(read)-i]; c < best {
			at, best = i, c
		}
	}
	align(ref[:mid], read[:at], out)
	align(ref[mid:], read[at:], out)
}

// alignOne is the base case, and it is spelled out rather than left to the
// recursion because one row is where the recursion would otherwise have to
// remember which column it chose.
func alignOne(ref Letter, read []Letter, out *[]Op) {
	// Every character of the reading is added except at most one, which the
	// reference character is either lined up with or not.
	at, best := -1, len(read)*costGap+costGap
	for i, r := range read {
		c, _ := substitution(ref, r)
		if c+(len(read)-1)*costGap < best {
			at, best = i, c+(len(read)-1)*costGap
		}
	}
	for i, r := range read {
		if i == at {
			*out = append(*out, Op{Ref: ref, Read: r, HaveRef: true, HaveRead: true})
			continue
		}
		*out = append(*out, Op{Read: r, HaveRead: true})
	}
	if at < 0 {
		*out = append(*out, Op{Ref: ref, HaveRef: true})
	}
}

// costs is one row of the edit distance table: the cost of turning the whole of
// ref into each prefix of read.
func costs(ref, read []Letter) []int {
	prev := make([]int, len(read)+1)
	cur := make([]int, len(read)+1)
	for i := range prev {
		prev[i] = i * costGap
	}
	for _, a := range ref {
		cur[0] = prev[0] + costGap
		for j, b := range read {
			sub, _ := substitution(a, b)
			c := prev[j] + sub
			if d := prev[j+1] + costGap; d < c {
				c = d
			}
			if d := cur[j] + costGap; d < c {
				c = d
			}
			cur[j+1] = c
		}
		prev, cur = cur, prev
	}
	return prev
}

func reversed(in []Letter) []Letter {
	out := make([]Letter, len(in))
	for i, l := range in {
		out[len(in)-1-i] = l
	}
	return out
}

// Cost is the total cost of an alignment under the table above. It is here so a
// test can check that the linear space version found an alignment as good as the
// one the table version finds, which is the only property of this code that is
// worth asserting and the only one that is easy to get wrong.
func Cost(ops []Op) int {
	total := 0
	for _, op := range ops {
		switch {
		case op.HaveRef && op.HaveRead:
			c, _ := substitution(op.Ref, op.Read)
			total += c
		default:
			total += costGap
		}
	}
	return total
}
