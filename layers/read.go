package layers

// Reading the layers of a source back off disk.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReadLayers reads a file of one JSON object per layer, in the order they were
// written. The source name and the number the project quotes come from the
// caller, since one is a label and the other is what the reading gets checked
// against.
//
// Unknown fields are an error. A layer is exactly the sort of row somebody
// extends with a second reading or a second weight, and a reader that skips what
// it does not recognize weights the source by a column it decided to ignore.
func ReadLayers(path string) ([]Layer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Layer
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var l Layer
		if err := dec.Decode(&l); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
		}
		out = append(out, l)
	}
	return out, nil
}
