package store

// Re-laying a repo without moving its bytes.
//
// A layout is a decision that looks free until there are five hundred parts
// laid out the old way, and then it looks like a quarter of a terabyte of
// re-upload, which on the fleet's uplink is a week. That is enough to keep a
// bad layout, and a bad layout in a published repo is somebody else's broken
// query later.
//
// It is not actually a re-upload. A large file on the Hub is an LFS object
// addressed by the sha256 of its content, and a path is a pointer at that
// object. A commit that points a second path at an object the repo already
// holds moves no content: the commit carries the digest and the storage has the
// bytes. So the whole migration is a listing, a rename of every path, and a
// handful of commits, and what it costs is what listing five hundred files
// costs. The bytes never leave the Hub, which also means the boxes take no part
// in it, and a re-lay can run from a laptop while all four of them are ingesting.
//
// The one thing this cannot do is cross repos. LFS storage on the Hub is
// namespaced per repo, so the batch endpoint on a second repo answers that it
// wants the bytes even for an object the first repo has had for a month, and a
// commit that points at it anyway is answered 404. A repo that wants a new name
// as well as a new layout gets the name from the Hub's own rename, which carries
// the storage with it, and the layout from here afterwards.

import (
	"context"
	"fmt"
	"sort"
)

// A Move is one file's path in the repo it is in and the path it is to have in
// the repo it is going to.
type Move struct {
	From string
	To   string

	// OID is the sha256 of the content, which is the whole reason this is cheap:
	// it is what the destination commit points at, and it is already known from
	// listing the source.
	OID string

	// Bytes is what the file would have cost to move if it were being moved,
	// which is the number worth reporting because it is the one not being spent.
	Bytes int64
}

// A MoveReport is what a migration did.
type MoveReport struct {
	From string `json:"from"`
	To   string `json:"to"`

	// Moved is the paths that were written, and Skipped is the ones already at
	// the destination pointing at the same content. A move is re-runnable, so
	// the second run over a finished migration writes nothing.
	Moved   []Move `json:"moved"`
	Skipped []Move `json:"skipped"`

	// Spared is the bytes that did not have to travel, which is the whole claim
	// this makes and so is a number rather than a sentence.
	Spared int64 `json:"spared"`

	// Commits is how many commits it took. A commit holds many operations, so
	// this is the migration's cost on the repo's history rather than one entry
	// per file.
	Commits int `json:"commits"`
}

// MoveBatch is how many files go in one commit.
//
// One commit for all of them would be tidier and the Hub rejects a body past a
// few thousand operations, so this is a number chosen to be comfortably under
// that and large enough that a five hundred part repo moves in single figures
// of commits. It is also the resume granularity: a migration killed halfway
// leaves whole commits behind it and re-running skips them.
const MoveBatch = 128

// Rename is a function from a path in the source repo to its path in the
// destination, returning false for a file the move does not carry.
type Rename func(path string) (string, bool)

// Moving is called before each commit with what is in it, so a migration says
// where it is rather than going quiet.
type Moving func(batch []Move, done, of int)

// MoveTo points a path at every file the rename accepts, as commits that carry
// a digest rather than any content.
//
// The pusher this is called on reads and dst writes, and for the reason in the
// file comment they have to be the same repo. They are two parameters anyway
// because the read and the write are separate ideas, and a caller that has them
// separate reads better than one that has to remember which of the two a bare
// method is doing.
//
// A path already there pointing at the same content is skipped rather than
// rewritten, which is what makes a killed run resumable and a finished one a
// no-op, and it is also how a part that was written at the right path in the
// first place comes back as skipped rather than as work.
func (p *Pusher) MoveTo(ctx context.Context, dst *Pusher, prefix string, rename Rename, note Moving) (MoveReport, error) {
	report := MoveReport{From: p.Repo, To: dst.Repo}

	from, err := p.List(ctx, prefix)
	if err != nil {
		return report, err
	}
	to, err := dst.List(ctx, prefix)
	if err != nil {
		return report, err
	}
	already := make(map[string]string, len(to))
	for _, s := range to {
		already[s.Path] = s.OID
	}

	var moves []Move
	for _, s := range from {
		path, ok := rename(s.Path)
		if !ok {
			continue
		}
		if !s.LFS {
			return report, fmt.Errorf("kho: %s in %s is a git blob rather than an lfs object, so there is no digest to point %s at and it would have to be uploaded", s.Path, p.Repo, path)
		}
		m := Move{From: s.Path, To: path, OID: s.OID, Bytes: s.Bytes}
		if have, ok := already[path]; ok && have == s.OID {
			report.Skipped = append(report.Skipped, m)
			continue
		}
		moves = append(moves, m)
	}
	sort.Slice(moves, func(i, j int) bool { return moves[i].To < moves[j].To })

	for i := 0; i < len(moves); i += MoveBatch {
		batch := moves[i:min(i+MoveBatch, len(moves))]
		ops := make([]commitOp, 0, len(batch))
		for _, m := range batch {
			ops = append(ops, commitCopy(m.To, m.OID))
		}
		if err := dst.commit(ctx, ops...); err != nil {
			return report, err
		}
		report.Commits++
		report.Moved = append(report.Moved, batch...)
		for _, m := range batch {
			report.Spared += m.Bytes
		}
		if note != nil {
			note(batch, len(report.Moved), len(moves))
		}
	}
	return report, nil
}

// commitCopy points a path at content the storage already holds.
//
// It is an lfsFile operation like an upload's, and it carries no size. The size
// belongs to the object rather than to the pointer, the Hub reads it off the
// object it already has, and sending one here would be this end asserting
// something about content it has not looked at.
func commitCopy(path, oid string) commitOp {
	return commitOp{Key: "lfsFile", Value: map[string]any{
		"path": path, "algo": "sha256", "oid": oid,
	}}
}

// Delete removes paths from the repo, in the same batches a move writes them.
//
// It is here rather than beside the push because the only thing that needs it
// is the far end of a migration: the old paths, once the new ones are up and
// have been read.
func (p *Pusher) Delete(ctx context.Context, paths []string) (int, error) {
	commits := 0
	for i := 0; i < len(paths); i += MoveBatch {
		batch := paths[i:min(i+MoveBatch, len(paths))]
		ops := make([]commitOp, 0, len(batch))
		for _, path := range batch {
			ops = append(ops, commitOp{Key: "deletedFile", Value: map[string]any{"path": path}})
		}
		if err := p.commit(ctx, ops...); err != nil {
			return commits, err
		}
		commits++
	}
	return commits, nil
}
