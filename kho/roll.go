package kho

// Writing a stage's output while the stage is still running.
//
// The offload claim is that peak disk is two shards per worker no matter how
// large the corpus is, and this is the half of it that bounds what a worker
// holds. A stage does not finish an input file and then write what it found. It
// writes a part, closes it, hands it off, and starts the next one, so the disk
// under a worker is one part being written and at most one part waiting to go.
//
// Rolling over on text rather than on file size is a choice the format forces.
// A Parquet writer buffers a row group and compresses it at the boundary, so
// the size of the file is not known until it closes, and a writer that waited
// for the file to reach a size would be waiting on a number that only appears
// after the decision was needed. Text bytes are known on every append, they are
// the unit the corpus is measured in everywhere else, and the ratio between the
// two is the compression ratio the disk budget already assumes.

import (
	"errors"
	"fmt"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/may"
)

// TextPerPart is how much text one part holds before the writer rolls over.
//
// It is derived and not chosen. [may.ShardBytes] is the compressed size a
// published shard targets and [may.AssumedCompression] is the ratio the disk
// budget runs on, so a part holding this much text lands near the size the rest
// of the project was sized against. When the measured ratio replaces the assumed
// one, this moves with it instead of sitting behind as a number nobody remembers
// picking.
var TextPerPart = int64(float64(may.ShardBytes) * may.AssumedCompression)

// ErrRollClosed reports an append to a roll that has already been closed.
var ErrRollClosed = errors.New("kho: that roll is closed")

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

	// TextPerPart overrides [TextPerPart] for this roll. It is here for tests
	// and for a box whose disk says something different from the fleet average.
	TextPerPart int64

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
	if r.cur.Text() >= r.limit() {
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

func (r *Roll) open() error {
	p, err := CreatePart(r.Dir, StagePath(r.Stamp.Snapshot, r.File, r.part), r.Dataset, r.Stamp)
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
		return fmt.Errorf("kho: %s: %w", f.Path, err)
	}
	return nil
}
