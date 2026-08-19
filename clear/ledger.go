package clear

// What actually happened to each file, as against what the arithmetic said
// would.
//
// The rotation above is a plan and a plan is not evidence. A crawl that ran for
// six weeks either deleted only bytes the store had confirmed or it did not, and
// the difference is invisible from the disk afterwards: a box that reclaimed
// something it never uploaded looks exactly like a box that behaved, because in
// both cases the file is gone. The only place the difference exists is in what
// was written down while it happened.
//
// So every file is a line, and the line says what state it reached and when. The
// checks below are all forms of the same question. Did anything skip a step.

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tamnd/gao/fleet"
)

// State is where a file has got to on its way off the box.
//
// The order is the order they happen in, and the comparison between two states
// is meaningful, which is what lets a skipped step be a subtraction rather than
// a table of cases.
type State uint8

const (
	// Resident is written and closed, and this box holds the only copy.
	Resident State = iota

	// Pushed is uploaded. It is not safe. The upload returned success and the
	// store has not yet been asked whether it holds those bytes, and treating
	// this state as done is the single mistake this package exists to catch.
	Pushed

	// Verified is the store confirming it holds the object and that the object
	// hashes to what was sent. This is the first state in which deleting the
	// local copy loses nothing.
	Verified

	// Reclaimed is deleted here. The store is the only copy, which is the
	// intended end and is why the step before it is not optional.
	Reclaimed
)

// String implements [fmt.Stringer].
func (s State) String() string {
	switch s {
	case Resident:
		return "resident"
	case Pushed:
		return "pushed"
	case Verified:
		return "verified"
	case Reclaimed:
		return "reclaimed"
	}
	return fmt.Sprintf("state(%d)", uint8(s))
}

// ParseState is the inverse of [State.String].
func ParseState(s string) (State, bool) {
	for st := Resident; st <= Reclaimed; st++ {
		if st.String() == s {
			return st, true
		}
	}
	return 0, false
}

// MarshalJSON writes the state as a word.
//
// A rotation log is read by a person at three in the morning, and a line saying
// the state is 2 is a line that sends them to find the table that says what 2
// is. The words also survive somebody inserting a state in the middle of the
// list, which the numbers do not.
func (s State) MarshalJSON() ([]byte, error) {
	if s > Reclaimed {
		return nil, fmt.Errorf("clear: %s is not a state a file can be in", s)
	}
	return []byte(`"` + s.String() + `"`), nil
}

// UnmarshalJSON reads a state written as a word, and refuses anything else.
func (s *State) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return fmt.Errorf("clear: a state is one of resident, pushed, verified or reclaimed: %w", err)
	}
	got, ok := ParseState(name)
	if !ok {
		return fmt.Errorf("clear: %q is not a state, and the states are resident, pushed, verified and reclaimed", name)
	}
	*s = got
	return nil
}

// Event is one thing that happened to one file, as the rotation wrote it down.
type Event struct {
	// Name is the file, local, and Path is where it went in the store. Path is
	// empty until it goes anywhere.
	Name string `json:"name"`
	Path string `json:"path,omitempty"`

	// Bytes is the size of the file. It is on every event rather than only the
	// first because a size that changes between events is a file that was
	// rewritten under the rotation, and that is worth catching.
	Bytes int64 `json:"bytes"`

	// Hash is what the local bytes hashed to, and on a verification it is what
	// the store said it holds. Comparing the two is the entire content of the
	// word verified.
	Hash string `json:"hash,omitempty"`

	State State     `json:"state"`
	At    time.Time `json:"at"`

	// Box is the machine, so that one rotation log can hold the whole fleet and
	// still answer questions about one machine.
	Box string `json:"box,omitempty"`
}

// File is everything that happened to one file, folded together.
type File struct {
	Name  string
	Path  string
	Bytes int64
	Box   string

	// Reached is the furthest state it got to, and Seen records which states it
	// was actually observed in, which are different things when a step was
	// skipped.
	Reached State
	Seen    [Reclaimed + 1]bool

	// First and Last bracket it, so that a file that sat resident for three
	// weeks can be found without reading the log by eye.
	First, Last time.Time

	// Hashes is every distinct hash the file was reported with. More than one
	// means the local copy and the store copy are not the same bytes.
	Hashes []string
}

// OnDisk reports whether this file still occupies space.
func (f File) OnDisk() bool { return f.Reached < Reclaimed }

// Held is how long the file stayed on the box.
func (f File) Held() time.Duration { return f.Last.Sub(f.First) }

// Ledger is a rotation log read back.
type Ledger struct {
	Files []File

	// Faults is what the log says went wrong, one sentence each, naming the
	// file. They are sentences rather than codes because the audience is a
	// person deciding whether a crawl's output can be trusted, and a code sends
	// them to find the table that explains it.
	Faults []string
}

// Read folds a rotation log into a ledger.
//
// It refuses nothing and returns everything. A log with a fault in it is a log
// whose other lines are still the only record of what happened, and a reader
// that stopped at the first problem would be a reader nobody could use on the
// day it mattered.
func Read(events []Event) Ledger {
	byName := make(map[string]*File)
	var order []string
	for _, e := range events {
		f, ok := byName[e.Name]
		if !ok {
			f = &File{Name: e.Name, First: e.At}
			byName[e.Name] = f
			order = append(order, e.Name)
		}
		if e.State <= Reclaimed {
			f.Seen[e.State] = true
			f.Reached = max(f.Reached, e.State)
		}
		if e.Path != "" {
			f.Path = e.Path
		}
		if e.Box != "" {
			f.Box = e.Box
		}
		if e.Bytes > f.Bytes {
			f.Bytes = e.Bytes
		}
		if e.Hash != "" && !slices.Contains(f.Hashes, e.Hash) {
			f.Hashes = append(f.Hashes, e.Hash)
		}
		if e.At.Before(f.First) {
			f.First = e.At
		}
		if e.At.After(f.Last) {
			f.Last = e.At
		}
	}

	l := Ledger{Files: make([]File, 0, len(order))}
	for _, name := range order {
		l.Files = append(l.Files, *byName[name])
	}
	l.Faults = l.check()
	return l
}

// ReadLog reads a rotation log, one event per line.
//
// An unknown field is an error rather than something to ignore, because the
// rotation and this reader are the same project and a field one of them does
// not know about means they have drifted apart, which is worth finding out
// before six weeks of crawl are behind it.
func ReadLog(path string) ([]Event, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("clear: %w", err)
	}
	var out []Event
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var e Event
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("clear: %s line %d: %w", path, i+1, err)
		}
		if e.Name == "" {
			return nil, fmt.Errorf("clear: %s line %d: an event with no file on it, and a rotation log is a record of files", path, i+1)
		}
		out = append(out, e)
	}
	return out, nil
}

// check is every way a file can have skipped a step.
func (l Ledger) check() []string {
	var out []string
	for _, f := range l.Files {
		switch {
		case f.Reached == Reclaimed && !f.Seen[Verified]:
			// This is the one. The file is gone from the box and nothing ever
			// confirmed the store has it, so whether the corpus contains it is
			// not a question anybody can now answer.
			out = append(out, fmt.Sprintf("%s was deleted without the store ever confirming it, so %s of crawl either exists off-box or does not and the log cannot say which",
				f.Name, fleet.Size(f.Bytes)))
		case f.Seen[Verified] && !f.Seen[Pushed]:
			out = append(out, fmt.Sprintf("%s was verified without having been uploaded, which means the check passed against something already in the store under that path",
				f.Name))
		}
		if len(f.Hashes) > 1 {
			out = append(out, fmt.Sprintf("%s was reported with %d different hashes, so the copy the store holds is not the copy this box wrote",
				f.Name, len(f.Hashes)))
		}
		if f.Path == "" && f.Reached >= Pushed {
			out = append(out, fmt.Sprintf("%s reached %s with no path in the store, so nothing here says where it went",
				f.Name, f.Reached))
		}
	}
	sort.Strings(out)
	return out
}

// Bytes is what the ledger accounts for in total.
func (l Ledger) Bytes() int64 { return l.bytes(func(File) bool { return true }) }

// OnDisk is what is still occupying the box, which is the number the disk
// itself should agree with and the check that says whether this log is a record
// of the machine or a record of what somebody meant to happen.
func (l Ledger) OnDisk() int64 { return l.bytes(File.OnDisk) }

// Reclaimed is what the rotation has actually freed.
func (l Ledger) Reclaimed() int64 {
	return l.bytes(func(f File) bool { return f.Reached == Reclaimed })
}

// Unsafe is what is on the box in a state that cannot be deleted yet, which is
// the backlog the steady state arithmetic predicts and is the number to compare
// against [Rotation.Held].
func (l Ledger) Unsafe() int64 {
	return l.bytes(func(f File) bool { return f.Reached < Verified })
}

func (l Ledger) bytes(keep func(File) bool) int64 {
	var n int64
	for _, f := range l.Files {
		if keep(f) {
			n += f.Bytes
		}
	}
	return n
}

// Count is how many files reached exactly this state and went no further.
func (l Ledger) Count(s State) int {
	var n int
	for _, f := range l.Files {
		if f.Reached == s {
			n++
		}
	}
	return n
}

// Oldest is the file that has been on the box longest without being reclaimed,
// which is where a rotation that has quietly stopped shows up first.
func (l Ledger) Oldest() (File, bool) {
	var out File
	var found bool
	for _, f := range l.Files {
		if !f.OnDisk() {
			continue
		}
		if !found || f.First.Before(out.First) {
			out, found = f, true
		}
	}
	return out, found
}

// Sound reports whether every file in the log took every step in order.
func (l Ledger) Sound() bool { return len(l.Faults) == 0 }

// Verdict is the log in one sentence.
func (l Ledger) Verdict() string {
	if !l.Sound() {
		return fmt.Sprintf("this rotation cannot be trusted: %d of %d files skipped a step, starting with %s",
			len(l.Faults), len(l.Files), l.Faults[0])
	}
	if len(l.Files) == 0 {
		return "nothing has been rotated, which is what an empty log says and not that anything is wrong"
	}
	return fmt.Sprintf("%d files, %s freed, %s still on the box and %s of that not yet safe to delete",
		len(l.Files), fleet.Size(l.Reclaimed()), fleet.Size(l.OnDisk()), fleet.Size(l.Unsafe()))
}
