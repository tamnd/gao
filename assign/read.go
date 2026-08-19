package assign

// Reading link measurements back off disk.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReadReadings reads a file of one JSON object per box, in the order they were
// written.
//
// Unknown fields are an error rather than something to ignore. A file of
// throughput readings is exactly the sort of thing somebody extends with a field
// the schedule does not know about, and a reader that skips what it does not
// recognize reports a plan built from part of the measurement.
func ReadReadings(path string) ([]Reading, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Reading
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var l Reading
		if err := dec.Decode(&l); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
		}
		out = append(out, l)
	}
	return out, nil
}
