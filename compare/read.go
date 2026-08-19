package compare

// Reading a human evaluation back off disk.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReadPairs reads a file of one JSON object per judgement.
//
// Unknown fields are an error. A protocol file is exactly the sort of thing
// somebody extends with a second question for the raters, and a reader that
// skips the column it does not know reports the answer to one question as
// though it were the answer to the protocol.
func ReadPairs(path string) ([]Pair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Pair
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var p Pair
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, p)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}
