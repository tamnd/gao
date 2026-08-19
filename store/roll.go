package store

// Writing a stage's output while the stage is still running.
//
// The offload claim is that peak disk is two shards per worker no matter how
// large the corpus is, and this is the half of it that bounds what a worker
// holds. A stage does not finish an input file and then write what it found. It
// writes a part, closes it, hands it off, and starts the next one, so the disk
// under a worker is one part being written and at most one part waiting to go.
//
// A part closes on whichever comes first, the size it has reached or the text
// it has taken in. It was text alone, on the reasoning that a Parquet writer
// compresses a row group at the boundary and does not know its own file size
// until it closes. That is true of the row group being filled and it is not
// true of the ones already written, which are on the disk and counted, and the
// difference matters because the text limit is the shard target divided by a
// compression ratio measured on one source. GlotCC compresses at 2.07 and
// FinePDFs at 1.07, so the first published parts of the two came out at 512 MB
// and at 988 MB against the same 512 MB target.
//
// Size rather than bytes on the disk, because the disk is a floor that moves
// one row group at a time and rolling on it put FinePDFs parts at 0.7 GB. See
// [Part.Size]: the open row group is estimated at the ratio the part has
// already measured on itself. Text is still the second half of the rule,
// because a source that compresses better than the ratio would otherwise write
// a part the size of its input file.

import (
	"errors"
	"fmt"
	"math"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fleet"
)

// TextPerPart is how much text one part holds before the writer rolls over.
//
// It is derived and not chosen. [fleet.ShardBytes] is the compressed size a
// published shard targets and [fleet.Compression] is the ratio the disk budget
// runs on, so a part holding this much text lands near the size the rest of the
// project was sized against. It moved when the ratio did: server3's ingest on
// 2026-08-18 rolled on 1.5 GB of text and wrote 741 MB parts against a 512 MB
// target, because 1.5 GB is what 512 MB costs at the ratio that was assumed and
// not at the one that was measured.
var TextPerPart = int64(math.Round(float64(fleet.ShardBytes) * fleet.Compression))

// ErrRollClosed reports an append to a roll that has already been closed.
var ErrRollClosed = errors.New("store: that roll is closed")

// Roll writes the documents of one input file as a sequence of parts.
//
// The parts are numbered from zero and their paths are a function of the
// snapshot, the input file, and the part number, so a run that dies partway
// through a file and is started again writes the same paths over the ones it
// left behind rather than adding a second copy under different names.
type Roll struct {
	// Dir is the directory the parts are written under. Paths inside it are the
	// paths inside the repo, so a push is a copy of the tree rather than a
	// second naming scheme.
	Dir string

	// Dataset is the repo the parts belong to, which decides the schema and
	// which documents may be written at all.
	Dataset Dataset

	// Stamp is what every part says about itself. The snapshot in it is the
	// partition the parts sit under.
	Stamp Stamp

	// File is the index of the input file within its source, which is the other
	// partition. It is the input file rather than a running count because an
	// ingest does not know how many parts a source will produce until it has
	// produced them, and a path that has to be revised later is not a path.
	File int

	// First is the part number this roll starts numbering at.
	//
	// It is zero for a stage whose input is replayable. An ingest that dies in
	// the middle of a file reads that file again from the beginning, so its
	// parts are written over the ones it left behind and numbering from zero is
	// what makes a restart idempotent. A crawl is not replayable: it stops, it
	// is started again, and it carries on with URLs the first run never saw. A
	// part number it has already used names a file somebody else's documents
	// are in, so the run remembers where it got to and says so here.
	First int

	// TextPerPart overrides [TextPerPart] for this roll. It is here for tests
	// and for a box whose disk says something different from the fleet average.
	TextPerPart int64

	// BytesPerPart overrides [fleet.ShardBytes] as the size a part closes at. It
	// is here for tests, which cannot afford to write half a gigabyte to find
	// out that the rule works.
	BytesPerPart int64

	// Finished, if set, is called with each part as it closes, before the next
	// one is opened. It is where an upload goes: returning an error from it
	// stops the roll, so a part that could not be handed off fails the file it
	// came from rather than being counted as written.
	Finished func(PartFile) error

	part   int
	cur    *Part
	files  []PartFile
	closed bool
}

// Append writes one document, opening a part if none is open and closing the
// current one if it has taken its share of text.
func (r *Roll) Append(d *doc.Document) error {
	if r.closed {
		return ErrRollClosed
	}
	if r.cur == nil {
		if err := r.open(); err != nil {
			return err
		}
	}
	if err := r.cur.Append(d); err != nil {
		return err
	}
	if r.cur.Size() >= r.size() || r.cur.Text() >= r.limit() {
		return r.rotate()
	}
	return nil
}

// AppendReject writes one dropped document, opening and rolling parts on the
// same rule [Roll.Append] uses.
//
// The rule is the same and what it measures is not. A rejects part carries no
// text, so the text bound never fires and the size bound is what closes it,
// which is the right way round for a stream of rows that are all metadata.
func (r *Roll) AppendReject(d *doc.Document, stage, reason, detail string) error {
	if r.closed {
		return ErrRollClosed
	}
	if r.cur == nil {
		if err := r.open(); err != nil {
			return err
		}
	}
	if err := r.cur.AppendReject(d, stage, reason, detail); err != nil {
		return err
	}
	if r.cur.Size() >= r.size() || r.cur.Text() >= r.limit() {
		return r.rotate()
	}
	return nil
}

// Close finishes the open part and returns every part the roll wrote.
//
// A roll that was given no documents writes no file. An input file that admits
// nothing should leave nothing behind, since an empty part in a repo reads as a
// shard whose documents went missing rather than as a shard that never had any.
func (r *Roll) Close() ([]PartFile, error) {
	if r.closed {
		return r.files, nil
	}
	r.closed = true
	if r.cur == nil {
		return r.files, nil
	}
	return r.files, r.rotate()
}

// Cut closes the open part now and leaves the roll ready to open the next one.
//
// It is for a writer whose input does not end. An ingest reads a file and
// stops, so its last part closes when the file does, but a crawl runs for days
// and a part that only closes when it is full is a part nobody off the box can
// read until it does. Cutting on a clock publishes what has been written and
// gives the disk back, and what it costs is parts smaller than the target.
//
// A roll with nothing open cuts nothing. An interval in which no document
// arrived should leave no file behind, since an empty part reads as a shard
// whose documents went missing rather than as a shard that never had any.
func (r *Roll) Cut() error {
	if r.closed {
		return ErrRollClosed
	}
	if r.cur == nil {
		return nil
	}
	return r.rotate()
}

// Abandon throws away the part being written and leaves the finished ones
// alone. Those are already somewhere else, or the run failed before they got
// there and the same paths will be written again.
func (r *Roll) Abandon() {
	r.closed = true
	if r.cur != nil {
		r.cur.Abandon()
		r.cur = nil
	}
}

// Files returns the parts written so far.
func (r *Roll) Files() []PartFile {
	out := make([]PartFile, len(r.files))
	copy(out, r.files)
	return out
}

// Documents returns how many documents the roll has written, the open part
// included.
func (r *Roll) Documents() int {
	n := 0
	for _, f := range r.files {
		n += f.Documents
	}
	if r.cur != nil {
		n += r.cur.Documents()
	}
	return n
}

func (r *Roll) limit() int64 {
	if r.TextPerPart > 0 {
		return r.TextPerPart
	}
	return TextPerPart
}

func (r *Roll) size() int64 {
	if r.BytesPerPart > 0 {
		return r.BytesPerPart
	}
	return fleet.ShardBytes
}

func (r *Roll) open() error {
	p, err := CreatePart(r.Dir, StagePath(r.Stamp.Snapshot, r.File, r.First+r.part), r.Dataset, r.Stamp)
	if err != nil {
		return err
	}
	r.cur = p
	return nil
}

// rotate closes the open part, hands it off, and leaves the roll ready to open
// the next one.
func (r *Roll) rotate() error {
	f, err := r.cur.Close()
	r.cur = nil
	if err != nil {
		return err
	}
	r.files = append(r.files, f)
	r.part++
	if r.Finished == nil {
		return nil
	}
	if err := r.Finished(f); err != nil {
		return fmt.Errorf("store: %s: %w", f.Path, err)
	}
	return nil
}
