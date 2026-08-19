package dem

// Building the parts index.
//
// The index is one row per part with the part's document count in it, and the
// document count is the one field that is not already in the tree listing. It
// comes out of the Parquet footer, which is a few kilobytes at the end of a
// file that is half a gigabyte, so the whole index over a quarter terabyte repo
// is a few megabytes of reading.
//
// That is measured rather than claimed. The reader counts what it moved and the
// report carries it, for the same reason gao store weigh does: the cheapness is
// the reason this can be run after every ingest instead of once a quarter, and
// an unmeasured claim about how little was read is the first thing to become
// false when somebody changes a Parquet library version.

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/parquet-go/parquet-go"

	"github.com/tamnd/gao/kho"
)

// An IndexReport is what a pass over the repo came back with.
type IndexReport struct {
	Repo string `json:"repo"`

	// Rows is the index itself, one per part.
	Rows []kho.Indexed `json:"rows"`

	// Moved is how many bytes the pass read to build it, against Held, which is
	// what the parts it indexed take in the repo. The pair is the claim that an
	// index is cheap, stated as two numbers rather than as an adjective.
	Moved int64 `json:"moved"`
	Held  int64 `json:"held"`
}

// Documents is the whole repo's document count, which is the number the card
// puts at the top.
func (r IndexReport) Documents() int64 {
	n, _ := kho.Total(r.Rows)
	return n
}

// Indexing is called after each part, so a pass over five hundred parts says
// where it is rather than going quiet for a few minutes.
type Indexing func(row kho.Indexed, done, of int, moved int64)

// IndexReaders is how many parts are read at once.
//
// One at a time is the wrong shape for this. Reading a footer is three or four
// round trips to a CDN and almost no work at either end, so a serial pass spends
// its whole life waiting, and five hundred parts take over an hour of latency to
// move a few hundred megabytes. The number is small enough to be polite to a
// host we do not own and large enough that the pass is bounded by its slowest
// few parts rather than by the sum of all of them.
const IndexReaders = 16

// IndexOf reads the footer of every part in the repo and returns the index.
//
// Every snapshot is walked, not one, because the index describes the repo and a
// repo with one source missing from its index is worse than no index: it reads
// as a repo that does not have that source.
func IndexOf(ctx context.Context, s *Store, note Indexing) (IndexReport, error) {
	snapshots, err := s.Snapshots(ctx)
	if err != nil {
		return IndexReport{}, err
	}
	if len(snapshots) == 0 {
		return IndexReport{}, fmt.Errorf("dem: %s holds no parts, so there is nothing to index", s.Repo)
	}

	var parts []kho.Stored
	for _, snapshot := range snapshots {
		of, err := s.Parts(ctx, snapshot)
		if err != nil {
			return IndexReport{}, err
		}
		parts = append(parts, of...)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Path < parts[j].Path })

	// Each part is written into its own slot and nothing is appended, so the
	// index comes out in path order however the reads finish. Only the progress
	// count and the first failure are shared, and those are the two things under
	// the lock.
	rows := make([]kho.Indexed, len(parts))
	read := make([]int64, len(parts))

	ctx, stop := context.WithCancel(ctx)
	defer stop()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var done int
	var failed error
	slots := make(chan struct{}, IndexReaders)

	for i, part := range parts {
		slots <- struct{}{}
		mu.Lock()
		gone := failed != nil
		mu.Unlock()
		if gone {
			<-slots
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()

			row, moved, err := indexPart(ctx, s, part)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if failed == nil {
					failed = err
					stop()
				}
				return
			}
			rows[i], read[i] = row, moved
			done++
			if note != nil {
				note(row, done, len(parts), moved)
			}
		}()
	}
	wg.Wait()
	if failed != nil {
		return IndexReport{Repo: s.Repo}, failed
	}

	report := IndexReport{Repo: s.Repo, Rows: rows}
	for i, part := range parts {
		report.Moved += read[i]
		report.Held += part.Bytes
	}
	return report, nil
}

// indexPart reads one part's footer and returns its row and what reading it
// cost.
func indexPart(ctx context.Context, s *Store, part kho.Stored) (kho.Indexed, int64, error) {
	snapshot, file, n, ok := kho.ParseStagePath(part.Path)
	if !ok {
		return kho.Indexed{}, 0, fmt.Errorf("dem: %s is under %s and is not a part path, so the index cannot say which source it belongs to", part.Path, s.Repo)
	}

	r, err := s.Open(ctx, part)
	if err != nil {
		return kho.Indexed{}, 0, err
	}
	// The row count is in the footer and nothing else here is wanted, so the page
	// index and the bloom filters are skipped. They are the two structures that
	// are optional, large, and read eagerly, and on a part of this shape they are
	// most of what an unqualified open pulls down.
	f, err := parquet.OpenFile(r, part.Bytes,
		parquet.SkipPageIndex(true), parquet.SkipBloomFilters(true))
	if err != nil {
		return kho.Indexed{}, r.Bytes(), fmt.Errorf("dem: opening %s: %w", part.Path, err)
	}
	return kho.Indexed{
		Source:    kho.Source(snapshot),
		Snapshot:  snapshot,
		File:      file,
		Part:      n,
		Path:      part.Path,
		Documents: f.NumRows(),
		Bytes:     part.Bytes,
	}, r.Bytes(), nil
}
