package count

// Measuring a corpus that is not on the box measuring it.
//
// Ingestion pushes each part as it closes and deletes the local copy, which is
// what lets a box process more than it can hold. The bill for that arrives here:
// the corpus is in the store and the questions about it are still to be asked.
//
// Downloading it back is not an option at 900 GB and it is also not necessary.
// A part is Parquet, the question is about document identity, and identity is
// one fixed width column. So a pass reads the listing, opens each part over
// HTTP, reads the doc_id chunk of each row group, and never asks for the pages
// the text is in. What moves is around thirty two bytes per document.
//
// A pass is resumable at the part, because it will be interrupted. It writes one
// key file per part and skips a part whose key file is already there, then
// merges them at the end. The unit of lost work is one part rather than a
// source, and nothing has to be remembered anywhere except in the directory the
// files are in.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tamnd/gao/harvest"
	"github.com/tamnd/gao/store"
)

// Store is a repo full of parts, read rather than written.
type Store struct {
	// Repo is the full repo id, org and name.
	Repo string

	// Token is a Hugging Face access token with read access to Repo. A working
	// repo is private, so this is not optional for one.
	Token string

	// API overrides [store.HubAPI]. It is for tests.
	API string

	// window and windows override the read window. They are for tests.
	window, windows int
}

func (s *Store) pusher() *store.Pusher {
	return &store.Pusher{Repo: s.Repo, Token: s.Token, API: s.API}
}

func (s *Store) fetcher() *harvest.Fetcher {
	return &harvest.Fetcher{Token: s.Token}
}

// Snapshots returns the working snapshots the repo holds, in order.
//
// A working snapshot is named for the source and the revision it was ingested
// at, so this is also the list of what has been ingested and at which pin.
func (s *Store) Snapshots(ctx context.Context) ([]string, error) {
	files, err := s.pusher().List(ctx, store.DataDir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, f := range files {
		if !f.Parquet() {
			continue
		}
		if snapshot, _, _, ok := store.ParseStagePath(f.Path); ok {
			seen[snapshot] = true
		}
	}
	out := make([]string, 0, len(seen))
	for snapshot := range seen {
		out = append(out, snapshot)
	}
	sort.Strings(out)
	return out, nil
}

// Parts returns the parts of one snapshot, in path order.
//
// Path order is the order the parts were written in, which is not required for
// anything here and makes a long run's progress readable.
//
// A source keeps every revision it was ever pinned at in the one directory, so
// the listing is by source and the snapshot is matched off each path. Reading
// the directory as though it were the snapshot would hand a run at one revision
// the parts of another.
func (s *Store) Parts(ctx context.Context, snapshot string) ([]store.Stored, error) {
	files, err := s.pusher().List(ctx, store.SourceDir(snapshot))
	if err != nil {
		return nil, err
	}
	parts := make([]store.Stored, 0, len(files))
	for _, f := range files {
		if got, _, _, ok := store.ParseStagePath(f.Path); ok && got == snapshot {
			parts = append(parts, f)
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Path < parts[j].Path })
	return parts, nil
}

// Open returns a reader over one part that can be read out of order.
//
// One column read forwards is one live read position, plus the footer the reader
// opened the file with, so two windows is what a column pass needs. The twenty
// four a row reader wants are 92 MB held for nothing, which is most of what the
// smallest box in the fleet has spare.
//
// The window is [harvest.ColumnWindow] and not the default, because a fixed width
// column is a couple of hundred kilobytes where the text beside it is tens of
// megabytes. Reading the shape columns of a real part at the default moved 58.1
// MB to sum 1.5 MB of them, and the first level of a published check that says
// it reads twelve bytes a document has to actually read twelve bytes a document
// or it is a check nobody can afford to run.
func (s *Store) Open(ctx context.Context, part store.Stored) (*harvest.RangeAt, error) {
	return s.open(ctx, part, harvest.ColumnWindow, 2)
}

// OpenRows returns a reader over one part for a pass that reads whole rows.
//
// A row reader has every column open at once, so two windows is a reader that
// spends its life refilling, and the text it is there to read comes in chunks a
// small window would fetch a page at a time. It is a second door rather than a
// wider default because the whole point of the other one is that it holds and
// moves almost nothing.
func (s *Store) OpenRows(ctx context.Context, part store.Stored) (*harvest.RangeAt, error) {
	return s.open(ctx, part, harvest.DefaultWindow, 24)
}

func (s *Store) open(ctx context.Context, part store.Stored, window, windows int) (*harvest.RangeAt, error) {
	if s.window > 0 {
		window = s.window
	}
	return s.fetcher().OpenRemote(ctx, harvest.Remote{
		Name:    part.Path,
		From:    s.Repo,
		URL:     s.pusher().ResolveURL(part.Path),
		Bytes:   part.Bytes,
		Auth:    s.Token != "",
		Window:  window,
		Windows: max(s.windows, windows),
	})
}

// SourceOf returns the source a working snapshot was ingested from.
//
// A snapshot is the source and the revision it was pinned at, joined by a
// hyphen, and the source names have no hyphen in them. It is the same answer
// the layout needs, since the source is the directory a part is filed under,
// and having it in one place is what keeps the two from drifting.
func SourceOf(snapshot string) string { return store.Source(snapshot) }

// Progress is called after each part with what the pass has read so far.
type Progress func(part store.Stored, i, of int, keys Keys, moved int64)

// KeysOf reads the doc_id column of every part of a snapshot and writes a sorted
// key file for the whole snapshot at out.
//
// The work directory holds one key file per part and is not cleaned up, because
// it is what makes a second run cheap: a pass that was interrupted after a
// hundred parts reads the last fifty and merges.
func KeysOf(ctx context.Context, s *Store, snapshot, work, out string, note Progress) (Keys, error) {
	parts, err := s.Parts(ctx, snapshot)
	if err != nil {
		return Keys{}, err
	}
	if len(parts) == 0 {
		return Keys{}, fmt.Errorf("dem: %s holds no parts of %s, so there is nothing to measure", s.Repo, snapshot)
	}
	if err := os.MkdirAll(work, 0o750); err != nil {
		return Keys{}, err
	}

	files := make([]string, 0, len(parts))
	var docs, moved int64
	for i, part := range parts {
		path := filepath.Join(work, partKeys(part.Path))
		files = append(files, path)

		done, err := keysDone(path)
		if err != nil {
			return Keys{}, err
		}
		if done.Documents == 0 {
			if done, moved, err = keysOfPart(ctx, s, part, path, moved); err != nil {
				return Keys{}, err
			}
		}
		docs += done.Documents
		if note != nil {
			note(part, i+1, len(parts), done, moved)
		}
	}
	return MergeKeys(out, docs, files...)
}

// keysDone reports what a part's key file holds, or a zero [Keys] when it is not
// there yet. A file that is there and unreadable is an error rather than work to
// redo, because silently rewriting it would hide a disk going bad.
func keysDone(path string) (Keys, error) {
	r, err := OpenKeys(path)
	if os.IsNotExist(err) {
		return Keys{}, nil
	}
	if err != nil {
		return Keys{}, err
	}
	defer func() { _ = r.Close() }()
	return r.Keys(), nil
}

// keysOfPart reads one part and writes its key file, and returns the running
// total of bytes moved.
//
// It writes to a temporary name and renames, so that a pass killed mid part
// leaves either a whole key file or none, and the resume check above can trust
// what it finds.
func keysOfPart(ctx context.Context, s *Store, part store.Stored, path string, moved int64) (Keys, int64, error) {
	r, err := s.Open(ctx, part)
	if err != nil {
		return Keys{}, moved, err
	}
	b := NewBuilder(filepath.Dir(path))
	if err := DocIDs(r, r.Size(), b.Add); err != nil {
		return Keys{}, moved, fmt.Errorf("dem: reading %s: %w", part.Path, err)
	}
	tmp := path + ".partial"
	keys, err := b.Write(tmp)
	if err != nil {
		return Keys{}, moved, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return Keys{}, moved, err
	}
	return keys, moved + r.Bytes(), nil
}

// partKeys is the name of the key file for a part.
//
// It comes from the part's path rather than from a counter, so that a pass over
// a snapshot that has grown since the last one lines its files up with the parts
// they came from instead of shifting everything along by one.
func partKeys(path string) string {
	if _, file, part, ok := store.ParseStagePath(path); ok {
		return fmt.Sprintf("f%05d-p%05d%s", file, part, KeysExt)
	}
	name := strings.TrimSuffix(filepath.Base(path), store.ParquetExt)
	return name + KeysExt
}
