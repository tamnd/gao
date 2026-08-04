package main

// Writing what an ingest admits, while the ingest is running.
//
// The decoder hands documents to whatever is downstream one at a time, and
// until now that was a counter. This is the sink that also writes them, as
// Parquet, in the layout the store of record uses, so that what a run leaves
// behind is the corpus rather than a number describing it.
//
// One roll per input file. The ingest works a file at a time and stops at the
// first failure, so there is never more than one open, and a file's parts are
// closed before the ledger records the file. A run that dies mid file therefore
// leaves a ledger with no entry for it and a directory with parts that the
// restart writes over, which is the same discipline the ledger already has.
//
// With a pusher, each part goes to the store as it closes and the local copy is
// deleted before the next one opens. That is the offload claim made real: what
// the box holds is one part being written, not the corpus, and a source of any
// size runs on a disk sized for one shard. A part that fails to push fails the
// file it came from, because the alternative is a run that carries on filling
// the disk it was supposed to be emptying.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/gat"
	"github.com/tamnd/gao/kho"
	"github.com/tamnd/gao/may"
)

// parts is a [gat.Sink] that decodes like the ordinary one and writes what it
// admits.
type parts struct {
	docs    *gat.Docs
	dir     string
	dataset kho.Dataset
	box     string
	out     io.Writer

	// push, when set, is where a part goes as it closes. The local copy is
	// deleted once the store has it.
	push *kho.Pusher

	roll    *kho.Roll
	written int
	bytes   int64
	sent    int
	freed   int64
}

// newParts returns the sink and the emit function the decoder feeds. The two
// are separate because the emit function has to be in place before the tally
// wraps it, and the sink has to be in place before the ingest runs.
func newParts(dir string, docs *gat.Docs, box string, out io.Writer) *parts {
	return &parts{docs: docs, dir: dir, dataset: kho.Staging(), box: box, out: out}
}

// write is what the decoder emits into.
func (p *parts) write(d *doc.Document) error {
	if p.roll == nil {
		return fmt.Errorf("gao gat hf: a document arrived with no file open, which is a bug in the sink")
	}
	return p.roll.Append(d)
}

// Consume implements [gat.Sink].
func (p *parts) Consume(ctx context.Context, pin gat.Pinned, f gat.File, r io.Reader) (int64, error) {
	if err := p.open(ctx, pin, f); err != nil {
		return 0, err
	}
	n, err := p.docs.Consume(ctx, pin, f, r)
	return n, p.close(err)
}

// ConsumeAt implements [gat.RandomSink], which the Parquet sources need.
func (p *parts) ConsumeAt(ctx context.Context, pin gat.Pinned, f gat.File, r io.ReaderAt, size int64) (int64, error) {
	if err := p.open(ctx, pin, f); err != nil {
		return 0, err
	}
	n, err := p.docs.ConsumeAt(ctx, pin, f, r, size)
	return n, p.close(err)
}

// open starts the roll for one input file. The file has to be one the source
// pins, because its position in that list is half the path its parts are
// written at, and a file from somewhere else has no position to write under.
func (p *parts) open(ctx context.Context, pin gat.Pinned, f gat.File) error {
	i := pin.IndexOf(f)
	if i < 0 {
		return fmt.Errorf("gao gat hf: %s is not a file %s pins, so there is nowhere to write what is in it", f.Path, pin.Source)
	}
	p.roll = &kho.Roll{
		Dir:     p.dir,
		Dataset: p.dataset,
		Stamp: kho.Stamp{
			Snapshot: pin.Snapshot(),
			Stage:    gat.Stage,
			Box:      p.box,
		},
		File: i,
		// The context comes from the file being read rather than from the sink,
		// so a run that is interrupted stops its upload too instead of holding
		// the process open for the last part.
		Finished: func(f kho.PartFile) error { return p.finished(ctx, f) },
	}
	return nil
}

// close finishes the roll and reports whichever error came first. A file that
// failed to decode is abandoned rather than closed, since a part holding the
// first half of a file that could not be read is worse than no part at all.
func (p *parts) close(err error) error {
	roll := p.roll
	p.roll = nil
	if roll == nil {
		return err
	}
	if err != nil {
		roll.Abandon()
		return err
	}
	_, err = roll.Close()
	return err
}

// finished is called as each part lands: it goes to the store, the local copy
// goes, and the line that says so is what a person watching a multi day run
// has to go on.
func (p *parts) finished(ctx context.Context, f kho.PartFile) error {
	p.written++
	p.bytes += f.Bytes
	verb, note := "wrote", ""
	if p.push != nil {
		sent, err := p.pushOne(ctx, f)
		if err != nil {
			return err
		}
		verb, note = "pushed", " and deleted here"
		switch {
		case sent.Skipped():
			verb, note = "had", " already, so nothing moved"
		case !sent.Transferred:
			note = ", whose bytes the store already held"
		}
	}
	if p.out != nil {
		fmt.Fprintf(p.out, "%-10s %-44s %8s  %d documents%s\n",
			verb, f.Path, may.GB(f.Bytes), f.Documents, note)
	}
	return nil
}

// pushOne sends one part and deletes the local copy.
//
// The delete is not conditional on the push having transferred anything. A part
// the store already had is a part this box no longer needs, and a resumed run
// that kept its local copies would fill the disk doing nothing.
func (p *parts) pushOne(ctx context.Context, f kho.PartFile) (kho.Pushed, error) {
	local := filepath.Join(p.dir, filepath.FromSlash(f.Path))
	sent, err := p.push.Push(ctx, local, f.Path)
	if err != nil {
		return sent, err
	}
	if err := os.Remove(local); err != nil {
		return sent, fmt.Errorf("gao gat hf: %s is in the store and still here: %w", f.Path, err)
	}
	// The directory a file's parts sat in is empty once the last of them has
	// gone, and this fails while it is not, which is the condition to check.
	_ = os.Remove(filepath.Dir(local))
	p.sent++
	p.freed += f.Bytes
	return sent, nil
}

// summary is the line at the end of a run that wrote parts.
func (p *parts) summary(w io.Writer) {
	if p.written == 0 {
		return
	}
	fmt.Fprintf(w, "%d parts written, %s of parquet in %s\n", p.written, may.GB(p.bytes), p.dir)
	if p.sent > 0 {
		fmt.Fprintf(w, "%d of them pushed to %s and deleted here, %s given back to the disk\n",
			p.sent, p.dataset.Repo(), may.GB(p.freed))
	}
}
