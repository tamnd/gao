package nhat

// The benchmark list, which is the input this stage has that no other stage has,
// and the roster, which is the part of it that lives in the repository.
//
// They are two files because they have two lifetimes. The roster is the record
// of what gao is judged on: names, revisions, where the items come from, and
// whether a benchmark is native Vietnamese or translated. It is small, it is
// read by people, it goes into a release note, and it only grows. The list is
// the roster with every item's text filled in, which is tens of megabytes of
// other people's test sets, which is a build artifact rather than a thing to
// commit.
//
// Keeping them apart is what makes GA11 checkable. A run loads a list, and the
// list is checked against the roster before the scan starts, so a list that
// quietly lost a benchmark fails at the beginning of the run rather than
// producing a clean report at the end of it.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	_ "embed"
)

// Origins a benchmark can have. The distinction is GA18's and it changes how a
// result reads rather than how it is computed.
const (
	// Native is written in Vietnamese by Vietnamese speakers.
	Native = "native"

	// Translated was translated into Vietnamese from another language. A clean
	// n-gram result on one of these is weaker than it looks, because an item
	// that reached the corpus through a different translation of the same
	// source shares no window with the translation being checked.
	Translated = "translated"

	// Neutral is neither, which in practice is code.
	Neutral = "neutral"
)

// Unpinned is the version of a benchmark whose revision has not been fixed yet.
// A release cannot go out with one, because a release note that says a benchmark
// was checked has to say which revision of it was checked.
const Unpinned = "unpinned"

// Roster is the versioned record of every benchmark gao is judged on.
type Roster struct {
	// Version names this revision of the roster. It goes into every report, so
	// a contamination table can be read years later against the roster it was
	// produced from.
	Version string `json:"version"`

	Note string `json:"note,omitempty"`

	// Benchmarks is every benchmark, in the order they were added. The order is
	// kept rather than sorted, because it is the record of when each one joined.
	Benchmarks []Entry `json:"benchmarks"`
}

// Entry is one benchmark, without its items.
type Entry struct {
	Name string `json:"name"`

	// Version is the benchmark's own revision, not the roster's. Two releases
	// checked against different revisions of the same benchmark have not been
	// checked against the same thing.
	Version string `json:"version"`

	// Origin is Native, Translated or Neutral.
	Origin string `json:"origin"`

	// Source is where the items come from, so that the list can be rebuilt from
	// the roster by somebody who has neither.
	Source string `json:"source,omitempty"`

	// Note is what part of each item goes into the list and why the benchmark is
	// on the roster. It is the thing a reader of a contamination table needs and
	// cannot recover from the numbers.
	Note string `json:"note,omitempty"`

	// HeldOut marks a benchmark built by holding text out of gao's own corpus.
	// Overlap with one of these is not a finding, it is the removal that was
	// supposed to happen, so a single shared window is enough to drop the
	// document rather than report it.
	HeldOut bool `json:"held_out,omitempty"`
}

// dropAt is how many shared windows remove a document on this benchmark's
// account.
func (e Entry) dropAt() int {
	if e.HeldOut {
		return 1
	}
	return DropAt
}

// Benchmark is one roster entry with the text of its items.
//
// Items is the text a corpus must not contain, which for a multiple choice
// benchmark is the question and the options rather than the answer alone, and
// for a reading comprehension benchmark is the passage. What goes in is a
// decision about that benchmark, it is made where the list is built, and the
// entry's note is where it is written down.
type Benchmark struct {
	Entry
	Items []string `json:"items"`
}

// List is a roster with every item filled in, which is what a run scans against.
type List struct {
	Version string `json:"version"`

	// Roster is the version of the roster this list was built from. It is
	// checked, so a list built a year ago cannot be run against today's roster
	// and report on benchmarks that were not in it.
	Roster string `json:"roster,omitempty"`

	Benchmarks []Benchmark `json:"benchmarks"`
}

//go:embed benchmarks.json
var rosterJSON []byte

var rostered = sync.OnceValues(func() (Roster, error) {
	return DecodeRoster(strings.NewReader(string(rosterJSON)))
})

// Rostered is the roster in the repository, which is the one a run uses unless
// it is given another.
func Rostered() (Roster, error) { return rostered() }

// ReadRoster reads a roster from a file.
func ReadRoster(path string) (Roster, error) {
	f, err := os.Open(path)
	if err != nil {
		return Roster{}, err
	}
	defer func() { _ = f.Close() }()
	return DecodeRoster(f)
}

// DecodeRoster reads a roster from JSON.
func DecodeRoster(r io.Reader) (Roster, error) {
	var ros Roster
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ros); err != nil {
		return Roster{}, fmt.Errorf("nhat: reading the roster: %w", err)
	}
	if err := ros.check(); err != nil {
		return Roster{}, err
	}
	return ros, nil
}

// ReadList reads a list from a file.
func ReadList(path string) (List, error) {
	f, err := os.Open(path)
	if err != nil {
		return List{}, err
	}
	defer func() { _ = f.Close() }()
	return DecodeList(f)
}

// DecodeList reads a list from JSON.
func DecodeList(r io.Reader) (List, error) {
	var l List
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return List{}, fmt.Errorf("nhat: reading the benchmark list: %w", err)
	}
	if err := l.check(); err != nil {
		return List{}, err
	}
	return l, nil
}

// Items is how many items the whole list holds.
func (l List) Items() int {
	n := 0
	for _, b := range l.Benchmarks {
		n += len(b.Items)
	}
	return n
}

// Names is every benchmark on the list, sorted, which is what a report header
// prints.
func (l List) Names() []string {
	out := make([]string, 0, len(l.Benchmarks))
	for _, b := range l.Benchmarks {
		out = append(out, b.Name)
	}
	sort.Strings(out)
	return out
}

// Covers reports whether the list holds every benchmark on a roster, at the
// revision the roster names.
//
// This is the check that makes a run's clean result mean something. A list is
// built by a fetch step that can fail one benchmark at a time, and a benchmark
// that failed to fetch produces exactly the same report as a benchmark that came
// back clean.
func (l List) Covers(ros Roster) error {
	if l.Roster != "" && l.Roster != ros.Version {
		return fmt.Errorf("nhat: the list was built from roster %s and this is roster %s, so rebuild the list", l.Roster, ros.Version)
	}
	have := make(map[string]string, len(l.Benchmarks))
	for _, b := range l.Benchmarks {
		have[b.Name] = b.Version
	}
	var missing, wrong []string
	for _, e := range ros.Benchmarks {
		version, ok := have[e.Name]
		switch {
		case !ok:
			missing = append(missing, e.Name)
		case version != e.Version:
			wrong = append(wrong, fmt.Sprintf("%s (roster %s, list %s)", e.Name, e.Version, version))
		}
	}
	switch {
	case len(missing) > 0 && len(wrong) > 0:
		return fmt.Errorf("nhat: the list is missing %s, and holds the wrong revision of %s", strings.Join(missing, ", "), strings.Join(wrong, ", "))
	case len(missing) > 0:
		return fmt.Errorf("nhat: the list is missing %s, and a benchmark that is not in the list comes back clean without being checked", strings.Join(missing, ", "))
	case len(wrong) > 0:
		return fmt.Errorf("nhat: the list holds the wrong revision of %s", strings.Join(wrong, ", "))
	}
	return nil
}

// Grew reports whether this roster is a superset of an older one, and names what
// is missing when it is not.
//
// This is the rule GA11 states, made checkable. A benchmark that comes off the
// roster is a result that stops being checked, and the failure being prevented
// is a contaminated benchmark disappearing from a table of scores instead of
// appearing in it with the contamination written next to it. A benchmark whose
// own revision moved has not been removed and is not silence either: it is
// reported, because a release checked against one revision has not been checked
// against the next.
func (ros Roster) Grew(older Roster) error {
	have := make(map[string]string, len(ros.Benchmarks))
	for _, e := range ros.Benchmarks {
		have[e.Name] = e.Version
	}
	var gone, moved []string
	for _, e := range older.Benchmarks {
		version, ok := have[e.Name]
		switch {
		case !ok:
			gone = append(gone, e.Name)
		case version != e.Version:
			moved = append(moved, fmt.Sprintf("%s (%s to %s)", e.Name, e.Version, version))
		}
	}
	switch {
	case len(gone) > 0 && len(moved) > 0:
		return fmt.Errorf("nhat: %s came off the roster, and %s changed revision", strings.Join(gone, ", "), strings.Join(moved, ", "))
	case len(gone) > 0:
		return fmt.Errorf("nhat: %s came off the roster, and the roster only grows", strings.Join(gone, ", "))
	case len(moved) > 0:
		return fmt.Errorf("nhat: %s changed revision, so an earlier release was checked against different items", strings.Join(moved, ", "))
	}
	return nil
}

// Unpinned is every benchmark whose revision has not been fixed, sorted. A
// release goes out when this is empty and not before.
func (ros Roster) Unpinned() []string {
	var out []string
	for _, e := range ros.Benchmarks {
		if e.Version == Unpinned || e.Version == "" {
			out = append(out, e.Name)
		}
	}
	sort.Strings(out)
	return out
}

func (ros Roster) check() error {
	if ros.Version == "" {
		return fmt.Errorf("nhat: the roster has no version, and a contamination table that cannot name its roster cannot be read later")
	}
	if len(ros.Benchmarks) == 0 {
		return fmt.Errorf("nhat: the roster is empty, and a decontamination run against nothing reports a clean corpus")
	}
	if len(ros.Benchmarks) > MaxBenchmarks {
		return fmt.Errorf("nhat: the roster holds %d benchmarks and an index holds %d", len(ros.Benchmarks), MaxBenchmarks)
	}
	seen := make(map[string]bool, len(ros.Benchmarks))
	for _, e := range ros.Benchmarks {
		if e.Name == "" {
			return fmt.Errorf("nhat: a benchmark on the roster has no name")
		}
		if seen[e.Name] {
			return fmt.Errorf("nhat: %s is on the roster twice", e.Name)
		}
		seen[e.Name] = true
		switch e.Origin {
		case Native, Translated, Neutral:
		default:
			return fmt.Errorf("nhat: %s has origin %q, and a benchmark is %s, %s or %s", e.Name, e.Origin, Native, Translated, Neutral)
		}
	}
	return nil
}

func (l List) check() error {
	if l.Version == "" {
		return fmt.Errorf("nhat: the benchmark list has no version, and a contamination table that cannot name its list cannot be read later")
	}
	if len(l.Benchmarks) == 0 {
		return fmt.Errorf("nhat: the benchmark list is empty, and a decontamination run against nothing reports a clean corpus")
	}
	if len(l.Benchmarks) > MaxBenchmarks {
		return fmt.Errorf("nhat: the list holds %d benchmarks and an index holds %d", len(l.Benchmarks), MaxBenchmarks)
	}
	seen := make(map[string]bool, len(l.Benchmarks))
	for _, b := range l.Benchmarks {
		if b.Name == "" {
			return fmt.Errorf("nhat: a benchmark on the list has no name")
		}
		if seen[b.Name] {
			return fmt.Errorf("nhat: %s is on the list twice", b.Name)
		}
		seen[b.Name] = true
		if len(b.Items) == 0 {
			return fmt.Errorf("nhat: %s has no items, so a run would report it clean without checking it", b.Name)
		}
	}
	return nil
}
