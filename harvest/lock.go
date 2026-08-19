package harvest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamnd/gao/fleet"
)

// One ingest at a time in one directory.
//
// The ledger is append only and keyed by source, revision and path, so two
// ingests sharing a directory do not corrupt it. What they do instead is worse
// to find: both read the same plan at startup, both decide the same 26.6 GB
// shard is outstanding, both fetch it, and both append a line for it. The
// ledger dedupes on read so the file count still looks right, while the bytes
// moved, the request count and the document totals are counted twice, and the
// only trace is a transfer bill that does not match the corpus.
//
// The document store is the part that does not survive it at all. Two writers
// appending to the same segment interleave, and a segment that interleaves is
// not a segment with a few bad records in it, it is a segment nothing can read
// past the first collision.
//
// So the directory takes a lock, and the lock is a file rather than anything
// cleverer. It is created with O_EXCL, which is one atomic operation on every
// filesystem gao runs on, and it holds the box, the process and the time in
// plain JSON so that the refusal can say who is holding it rather than that
// something is.
//
// A process killed with SIGKILL leaves its lock behind, and that case is
// handled by asking whether the process is still there rather than by a
// timeout. A timeout has to be longer than the longest legitimate pause, which
// here is a stalled 26.6 GB download, and a lock that expires while its holder
// is alive is the bug this file exists to prevent. Staleness is only ever
// broken on the box that wrote the lock, because a PID from another machine
// means nothing on this one.

// LockName is the file that holds the claim on an ingest directory.
const LockName = "ingest.lock"

// ErrLocked is returned when another ingest holds the directory.
var ErrLocked = errors.New("harvest: another ingest is running in this directory")

// Holder is what a lock file says about the ingest holding it.
type Holder struct {
	// Box is the machine label, so the message can tell a person on server1
	// that the holder is on server1 and not somewhere they cannot see.
	Box string `json:"box"`
	// PID is the process on that box, which is what makes staleness decidable.
	PID int `json:"pid"`
	// Started is when the lock was taken, in RFC 3339.
	Started string `json:"started"`
	// Command is what took it, for the case where two different gao commands
	// are both plausible.
	Command string `json:"command"`
}

// String is what the refusal prints.
func (h Holder) String() string {
	s := fmt.Sprintf("%s on %s, pid %d", h.Command, h.Box, h.PID)
	if h.Started != "" {
		s += ", started " + h.Started
	}
	return s
}

// Lock is a held claim on an ingest directory.
type Lock struct {
	path string
	me   Holder
}

// LockDir claims dir for one ingest and names the command in the lock file.
//
// The lock is released by [Lock.Release], which the caller should defer. An
// interrupted run releases it because the signal unwinds through that defer. A
// run that is killed outright does not, and the next run breaks the lock itself
// once it has established that the process named in it is gone.
func LockDir(dir, command string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("harvest: creating the ingest directory: %w", err)
	}
	path := filepath.Join(dir, LockName)
	me := Holder{
		Box:     fleet.Label(),
		PID:     os.Getpid(),
		Started: time.Now().UTC().Format(time.RFC3339),
		Command: command,
	}
	b, err := json.Marshal(me)
	if err != nil {
		return nil, fmt.Errorf("harvest: writing %s: %w", path, err)
	}
	b = append(b, '\n')

	// Two attempts and no more. The first can lose to a lock that is already
	// there, the second runs after that lock has been established as stale and
	// removed, and a third would mean racing another process that is breaking
	// the same stale lock, which is a race to lose rather than to retry.
	for attempt := range 2 {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			if _, err := f.Write(b); err != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("harvest: writing %s: %w", path, err)
			}
			if err := f.Close(); err != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("harvest: writing %s: %w", path, err)
			}
			return &Lock{path: path, me: me}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("harvest: locking %s: %w", dir, err)
		}
		if attempt > 0 {
			break
		}

		held, err := ReadHolder(dir)
		if err != nil {
			return nil, err
		}
		if !stale(held, me.Box) {
			return nil, fmt.Errorf("%w: %s. If that process is gone, remove %s",
				ErrLocked, held, path)
		}
		// Removing by name rather than by handle. The holder is gone, this is
		// its box, and the worst case is that another process on this box broke
		// the same lock first and took it, which the next attempt discovers.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("harvest: clearing the lock left by %s: %w", held, err)
		}
	}
	held, err := ReadHolder(dir)
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: %s. If that process is gone, remove %s", ErrLocked, held, path)
}

// stale reports whether a lock can be broken.
//
// Only ever on the box that wrote it. A PID means nothing across machines, and
// an ingest directory that two boxes can both see is a situation where guessing
// wrong costs a corrupted segment.
func stale(h Holder, box string) bool {
	if h.Box == "" || h.Box != box || h.PID <= 0 {
		return false
	}
	return !alive(h.PID)
}

// Release removes the lock. It is safe to call twice.
func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	path := l.path
	l.path = ""

	// Checked before removal, so that a run which lost its lock to something
	// else says so rather than deleting a claim it does not hold.
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil {
		var h Holder
		if json.Unmarshal(b, &h) == nil && (h.PID != l.me.PID || h.Box != l.me.Box) {
			return fmt.Errorf("harvest: %s now belongs to %s, leaving it alone", path, h)
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("harvest: releasing %s: %w", path, err)
	}
	return nil
}

// ReadHolder returns what the lock file in dir says, for a command that wants
// to report on a directory without claiming it.
func ReadHolder(dir string) (Holder, error) {
	path := filepath.Join(dir, LockName)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Holder{}, nil
	}
	if err != nil {
		return Holder{}, fmt.Errorf("harvest: reading %s: %w", path, err)
	}
	var h Holder
	if err := json.Unmarshal(b, &h); err != nil {
		// A lock file that cannot be read still locks the directory. It was
		// written by something, and treating unreadable as absent is how a
		// truncated write turns into two ingests.
		return Holder{Command: "an unreadable " + LockName, Box: "unknown"},
			fmt.Errorf("%w: %s cannot be read: %w. If no ingest is running, remove it",
				ErrLocked, path, err)
	}
	if strings.TrimSpace(h.Box) == "" {
		h.Box = "unknown"
	}
	return h, nil
}
