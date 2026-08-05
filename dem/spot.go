package dem

// Level two: reading text, on purpose, and only some of it.
//
// Level one adds the shape columns up and proves the published total is the sum
// of what is stored. It cannot prove the columns describe the text they sit next
// to. A stage that rewrote a document and forgot to recount, a merge that put the
// columns of one row beside the text of another, a batch that went through an
// older pipeline: all of them leave a store that adds up perfectly and a corpus
// that is not the size anybody says it is.
//
// So a sample of parts is read all the way through, every document in them is
// counted again from its own text, and the answer is compared with the column.
// This is expensive per part and that is fine, because the number of parts is
// chosen rather than given.
//
// Sampling makes the result a bound and not a proof, and the bound is stated
// rather than implied. An error in every part is found by the first part read.
// What a sample size buys is the localized case: one bad run of a stage, one
// batch, one shard that went through the pipeline a version behind. The bound is
// over how many parts are wrong and says nothing about how wrong any one of them
// is, which is the right shape for that failure and the wrong shape for a corpus
// that is uniformly a little off. Level one does not catch that one either, since
// it reads the same columns. Nothing here does, and a release note that claims it
// does would be the lie this is meant to prevent.
//
// Reading text is also the only way to get the fourth published unit. There is no
// column for the byte length of the text, so the sample is where a bytes per
// character ratio comes from, and level one carries the ratio the sample
// measured rather than a ratio of its own.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"

	"github.com/parquet-go/parquet-go"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

// MaxMismatches is how many disagreeing documents one part reports by name.
//
// A part where the columns are wrong is usually a part where they are all wrong,
// and printing fifty thousand of them buries the one line that says which part it
// was. The count is exact either way.
const MaxMismatches = 8

// ErrNoText reports a part with no text column, which cannot be spot checked.
//
// A published repo that withholds text is not a mistake, it is the restricted
// publication pattern. It does mean the spot check has to be run against the
// working repo the text is in, and saying that is better than reporting a part
// with no disagreements in it.
var ErrNoText = errors.New("dem: that part carries no text, so its columns cannot be checked against it")

// A Mismatch is one document whose stored shape does not describe its text.
type Mismatch struct {
	Row     int64  `json:"row"`
	DocID   string `json:"doc_id"`
	Column  string `json:"column"`
	Stored  uint32 `json:"stored"`
	Counted uint32 `json:"counted"`
}

// A Spot is one part read all the way through: what its columns say, what its
// text says, and every place the two disagree.
type Spot struct {
	Part      string `json:"part"`
	Documents int64  `json:"documents"`

	// Checked names the columns this run compared. Tokens are only on it when a
	// tokenizer was loaded, and a report that did not say so would read as a
	// token column that was checked and passed.
	Checked []string `json:"checked"`

	// Columns is what level one's cheap read makes of this part, and Stored is
	// the same columns as the whole row carries them. They are the same numbers
	// reached two ways, and a part where they differ has a bug in the fast path
	// rather than anything wrong with the corpus.
	Columns Counts `json:"columns"`
	Stored  Counts `json:"stored"`

	// Counted is what the text comes to. Its Bytes is real, and it is the only
	// place in the protocol a byte count is measured rather than left out.
	Counted Counts `json:"counted"`

	// Wrong is how many documents disagreed on at least one checked column.
	Wrong int64 `json:"wrong"`

	Mismatches []Mismatch `json:"mismatches,omitempty"`
}

// Agrees reports whether every document in the part matched on every checked
// column.
func (s Spot) Agrees() bool { return s.Wrong == 0 && s.Columns == s.Stored }

// SpotOf reads one part and checks every document's shape columns against its
// text.
//
// The tokenizer is optional and its absence is recorded rather than worked
// around. Tokens are defined by a pinned model file, so a run without one can
// check two of the three columns honestly and cannot check the third at all.
func SpotOf(name string, r io.ReaderAt, size int64, tok *Tokenizer) (Spot, error) {
	f, err := parquet.OpenFile(r, size)
	if err != nil {
		return Spot{}, fmt.Errorf("dem: opening %s: %w", name, err)
	}
	if _, err := columnIndex(f.Schema(), kho.TextColumn); err != nil {
		return Spot{}, fmt.Errorf("%w: %s", ErrNoText, name)
	}
	columns, err := shapeOf(f)
	if err != nil {
		return Spot{}, err
	}

	s := Spot{Part: name, Columns: columns, Checked: []string{kho.CharsColumn, kho.SyllablesColumn}}
	if tok != nil {
		s.Checked = append(s.Checked, kho.TokensColumn)
	}
	err = kho.ScanRows(name, r, func(row kho.Row) error {
		s.Stored.Documents++
		s.Stored.Chars += int64(row.NChars)
		s.Stored.Syllables += int64(row.NSyllables)
		s.Stored.Tokens += int64(row.NTokens)

		chars, syllables := doc.Chars(row.Text), doc.Syllables(row.Text)
		s.Counted.Documents++
		s.Counted.Bytes += int64(len(row.Text))
		s.Counted.Chars += int64(chars)
		s.Counted.Syllables += int64(syllables)

		bad := s.compare(row, kho.CharsColumn, row.NChars, chars)
		bad = s.compare(row, kho.SyllablesColumn, row.NSyllables, syllables) || bad
		if tok != nil {
			tokens := uint32(tok.Count(row.Text))
			s.Counted.Tokens += int64(tokens)
			bad = s.compare(row, kho.TokensColumn, row.NTokens, tokens) || bad
		}
		if bad {
			s.Wrong++
		}
		return nil
	})
	if err != nil {
		return Spot{}, err
	}
	s.Documents = s.Stored.Documents
	return s, nil
}

// SpotPart reads one part out of the store and checks it.
//
// This is the half of the protocol that moves the corpus, and it moves the parts
// in the sample and no others. A part is read once, in full, and nothing is kept
// afterwards except the answer.
func SpotPart(ctx context.Context, s *Store, part kho.Stored, tok *Tokenizer) (Spot, error) {
	r, err := s.OpenRows(ctx, part)
	if err != nil {
		return Spot{}, err
	}
	return SpotOf(part.Path, r, r.Size(), tok)
}

// compare records one column of one document and reports whether it disagreed.
func (s *Spot) compare(row kho.Row, column string, stored, counted uint32) bool {
	if stored == counted {
		return false
	}
	if len(s.Mismatches) < MaxMismatches {
		s.Mismatches = append(s.Mismatches, Mismatch{
			Row:     s.Stored.Documents - 1,
			DocID:   row.DocID.String(),
			Column:  column,
			Stored:  stored,
			Counted: counted,
		})
	}
	return true
}

// SampleSize is how many parts a spot check has to read to be confident that no
// more than a share of them is wrong.
//
// A sample of k parts drawn from a population where a fraction p is wrong misses
// every wrong one with probability (1-p)^k, so the sample that leaves at most
// 1-c chance of missing one is k >= ln(1-c)/ln(1-p). Drawing without replacement
// does slightly better than that, so this comes out on the conservative side of
// the real number, which is the side to be on.
//
// The shape of the answer is the thing to read off it. Halving the share you are
// willing to miss doubles the sample and tightening the confidence adds to it a
// little, so the cost is driven by how localized a fault you want to catch and
// almost not at all by how sure you want to be. Missing a fifth of the corpus is
// 21 parts at 99%, a twentieth is 90, a hundredth is 459.
func SampleSize(parts int, share, confidence float64) int {
	switch {
	case parts <= 0 || confidence <= 0:
		return 0
	case confidence >= 1 || share <= 0:
		// Certainty, or any confidence at all about a share that small, is only
		// bought by reading everything.
		return parts
	case share >= 1:
		return 1
	}
	k := math.Log(1-confidence) / math.Log(1-share)
	return min(parts, int(math.Ceil(k)))
}

// Sample picks which parts a spot check reads.
//
// It is deterministic. The same seed over the same parts gives the same sample on
// anybody's machine, which is what turns a spot check into something a third
// party can repeat rather than something they have to take on trust. It is also
// neither the first k nor every nth: the order of the listing is the order the
// parts were written in, and both of those systematically miss a bad run of a
// stage that happens to sit anywhere else.
//
// A part's rank is a hash of the seed and its path, so adding parts to a snapshot
// leaves the ranks of the ones already there alone. A corpus that grew by a tenth
// does not resample the nine tenths that were checked last time.
func Sample(parts []kho.Stored, k int, seed string) []kho.Stored {
	if k <= 0 || len(parts) == 0 {
		return nil
	}
	ranked := make([]kho.Stored, len(parts))
	copy(ranked, parts)
	rank := make(map[string]doc.Hash, len(parts))
	for _, p := range parts {
		rank[p.Path] = doc.SumString(seed + "\x00" + p.Path)
	}
	slices.SortFunc(ranked, func(a, b kho.Stored) int {
		ra, rb := rank[a.Path], rank[b.Path]
		if c := strings.Compare(string(ra[:]), string(rb[:])); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	ranked = ranked[:min(k, len(ranked))]

	// Handed back in path order rather than in rank order, because the list is
	// read by people and printed next to a listing that is in path order.
	slices.SortFunc(ranked, func(a, b kho.Stored) int { return strings.Compare(a.Path, b.Path) })
	return ranked
}

// A Plan is what a verification run is about to do and what it is going to cost,
// worked out from the listing alone.
//
// It exists to be printed before the run rather than discovered during it. A
// protocol whose cost is only knowable by starting it is a protocol that gets
// started on a Friday, and the reason the reproducibility item on S1 sat open is
// that the original claim of under an hour on one machine was never checked
// against a real corpus. This is the check.
type Plan struct {
	Snapshot string `json:"snapshot"`
	Parts    int    `json:"parts"`

	// Bytes is what the whole snapshot weighs in the store, which is what a
	// third party would move if they verified it the obvious way.
	Bytes int64 `json:"bytes"`

	// Documents is the count the report being checked claims for this snapshot's
	// source, or zero when there is no report to read it from. It is only used to
	// size level one, and level one runs either way.
	Documents int64 `json:"documents"`

	// Columns is what level one moves: three fixed width columns per document,
	// before the encoding, which makes it an upper bound and the right kind of
	// number for a budget.
	Columns int64 `json:"columns"`

	// Sample is the parts level two reads, in path order, and SampleBytes is what
	// they weigh.
	Sample      []string `json:"sample"`
	SampleBytes int64    `json:"sample_bytes"`

	Share      float64 `json:"share"`
	Confidence float64 `json:"confidence"`
	Seed       string  `json:"seed"`
}

// ShapeBytes is the width of the three shape columns per document, which is what
// level one's budget is built from.
const ShapeBytes = 12

// Planned works out the protocol for one snapshot.
//
// Documents is the count the report claims for it, or zero when nothing claims
// one. A plan with no document count still names its sample and still prices
// level two, because level two is the half that costs real money.
func Planned(snapshot string, parts []kho.Stored, documents int64, share, confidence float64, seed string) Plan {
	p := Plan{
		Snapshot:   snapshot,
		Parts:      len(parts),
		Documents:  documents,
		Columns:    documents * ShapeBytes,
		Share:      share,
		Confidence: confidence,
		Seed:       seed,
	}
	for _, part := range parts {
		p.Bytes += part.Bytes
	}
	for _, part := range Sample(parts, SampleSize(len(parts), share, confidence), seed) {
		p.Sample = append(p.Sample, part.Path)
		p.SampleBytes += part.Bytes
	}
	return p
}

// Hours is how long a number of bytes takes to move at a link rate in megabits
// per second.
//
// The rate is the caller's and is named in the output, because it is the one
// number here that belongs to whoever is running this rather than to the corpus.
// A budget quoted without it is a budget for one person's broadband.
func Hours(bytes int64, mbit float64) float64 {
	if mbit <= 0 || bytes <= 0 {
		return 0
	}
	return float64(bytes) * 8 / (mbit * 1e6) / 3600
}
