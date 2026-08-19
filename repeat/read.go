package repeat

// Reading a generated run back off disk, in the order it was generated.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReadDocs reads a file of one JSON object per generated document. A generated
// run is large, so this reads a line at a time rather than the file at once,
// and the buffer is raised because a generated document is a document rather
// than a log line.
//
// Unknown fields are an error. A generation file is the sort of thing somebody
// extends with a second filter's verdict, and a reader that skips the column it
// does not know reports a reject rate for one filter as though it were the
// reject rate.
func ReadDocs(path string) ([]Doc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Doc
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var d Doc
		if err := dec.Decode(&d); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, d)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}
