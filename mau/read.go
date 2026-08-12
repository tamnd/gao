package mau

// Reading the source's own listing of its shards.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReadFiles reads a file of one JSON object per shard, in the order the listing
// gives them. Order does not decide anything here, since the draw is by hash of
// the path, but it is preserved so that a plan printed next to the listing reads
// against it.
//
// Unknown fields are an error. A listing is exactly the sort of thing somebody
// extends with a checksum or a row count, and a reader that skips what it does
// not recognize is a reader that will one day skip the size.
func ReadFiles(path string) ([]File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []File
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var one File
		if err := dec.Decode(&one); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, one)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}
