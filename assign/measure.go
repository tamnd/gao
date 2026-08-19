package assign

// Turning the ledger of a run that happened into the reading a schedule is
// built from.
//
// A reading is four numbers and a person can type all four of them. That is the
// problem. The schedule this package prints is only worth the readings it was
// given, and a reading somebody remembered off a speed test looks exactly like
// one taken across eleven gigabytes of the actual corpus. So the number is
// derived here instead, off the ingest ledger, which records a finished file,
// how big it was, when it finished and which box it finished on.
//
// The window is between finishes and not from the start of the run, because the
// ledger has no start. That costs the first file: its own window is unknown, so
// its bytes are not counted and its finish is the clock the rest is measured
// from. The reading comes out slightly low against a run that was fetching for
// the whole hour, which is the direction to be wrong in when the number decides
// how long a fleet is booked for.

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/tamnd/gao/harvest"
)

// ErrNotAReading is returned when a ledger cannot support a reading. It is not a
// failure of the run, which may have gone perfectly. It is a refusal to publish
// a rate that would not mean anything.
var ErrNotAReading = errors.New("giao: the ledger does not carry a reading")

// Measure derives one box's reading from the entries its ingest wrote.
//
// The entries do not have to be in order and normally are, since the ledger is
// append only and the ingest fetches one file at a time.
func Measure(es []harvest.Entry) (Reading, error) {
	if len(es) == 0 {
		return Reading{}, fmt.Errorf("%w: it is empty, so no file has finished yet", ErrNotAReading)
	}

	var boxes []string
	for _, e := range es {
		if e.Box == "" {
			return Reading{}, fmt.Errorf("%w: %s finished on a box that did not label itself, and a rate without a box belongs to nobody", ErrNotAReading, e.Path)
		}
		if !slices.Contains(boxes, e.Box) {
			boxes = append(boxes, e.Box)
		}
	}
	if len(boxes) > 1 {
		slices.Sort(boxes)
		// Two boxes in one ledger is two runs, and their sum over the union of
		// their clocks is a rate no machine ever achieved.
		return Reading{}, fmt.Errorf("%w: it holds files fetched on %s, which is more than one run", ErrNotAReading, commas(boxes))
	}

	type finished struct {
		at time.Time
		e  harvest.Entry
	}
	ordered := make([]finished, 0, len(es))
	for _, e := range es {
		at, err := time.Parse(time.RFC3339, e.Finished)
		if err != nil {
			return Reading{}, fmt.Errorf("%w: %s finished at %q, which is not a time", ErrNotAReading, e.Path, e.Finished)
		}
		ordered = append(ordered, finished{at, e})
	}
	slices.SortStableFunc(ordered, func(a, b finished) int { return a.at.Compare(b.at) })

	if len(ordered) < 2 {
		return Reading{}, fmt.Errorf("%w: one file finished, and one finish time is not a duration", ErrNotAReading)
	}

	var bytes int64
	var sources []string
	for _, f := range ordered[1:] {
		bytes += f.e.Bytes
		if s := string(f.e.Source); !slices.Contains(sources, s) {
			sources = append(sources, s)
		}
	}
	seconds := ordered[len(ordered)-1].at.Sub(ordered[0].at).Seconds()
	if seconds <= 0 {
		return Reading{}, fmt.Errorf("%w: every file carries the same finish time, so the run took no time", ErrNotAReading)
	}
	if bytes < MinSample {
		return Reading{}, fmt.Errorf("%w: it covers %.1f GB after the first file and a reading is taken across at least %.1f GB, so fetch %s and run this again",
			ErrNotAReading, float64(bytes)/1e9, float64(MinSample)/1e9, another(len(ordered)))
	}

	return Reading{
		Box:     boxes[0],
		Bytes:   bytes,
		Seconds: seconds,
		On:      ordered[len(ordered)-1].at.UTC().Format(time.DateOnly),
		How: fmt.Sprintf("gao harvest hf over %s of %s, timed between finishes in the ingest ledger",
			plural(len(ordered)-1, "file"), commas(sources)),
	}, nil
}

// another is what to tell somebody whose run was too short.
func another(have int) string {
	if have < 3 {
		return "another file"
	}
	return "another file or two"
}

// commas joins names the way a sentence would.
func commas(s []string) string {
	switch len(s) {
	case 0:
		return "nothing"
	case 1:
		return s[0]
	}
	out := s[0]
	for _, x := range s[1 : len(s)-1] {
		out += ", " + x
	}
	return out + " and " + s[len(s)-1]
}
