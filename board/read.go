package board

// Reading a set of benchmark scores back off disk.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReadScores reads a file of one JSON object per benchmark, in the order they
// were written.
//
// Unknown fields are an error. A scores file is the sort of thing somebody
// extends with a second metric or a second baseline, and a reader that skips
// what it does not recognize publishes a board built from part of the run.
func ReadScores(path string) ([]Score, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Score
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var s Score
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
		}
		out = append(out, s)
	}
	return out, nil
}
