package spike

// Reading a training log back off disk.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// row is what a line of the log is decoded into. The fields are pointers so that
// a row which never mentioned the loss can be told from one that logged it as
// zero, and those two are not the same thing at all: the first is an exporter
// that changed under us and the second is a run that has stopped learning.
type row struct {
	Step *int     `json:"step"`
	Loss *float64 `json:"loss"`
	LR   *float64 `json:"lr"`
	Grad *float64 `json:"grad_norm"`
}

// ReadSteps reads a training log of one JSON object per line.
//
// Unknown fields are allowed here, which is the opposite of how the shard
// listings in this repo are read, and the difference is deliberate. A listing is
// written once by us and any field in it we do not recognize is a field we are
// about to ignore by mistake. A training log is written every few seconds by
// somebody else's trainer, it carries throughput and memory and whatever the
// framework felt like emitting that release, and a reader that refuses the whole
// run over a new counter is a reader nobody will point at a real log twice.
//
// What is not allowed is a row that does not carry a step and a loss, because
// that is not an extra field, it is the reading missing.
func ReadSteps(path string) ([]Step, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Step
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var one row
		if err := json.Unmarshal([]byte(line), &one); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		switch {
		case one.Step == nil:
			return nil, fmt.Errorf("%s:%d: the row carries no step, and a loss that cannot be placed on the run is not a reading of anything", path, n)
		case one.Loss == nil:
			return nil, fmt.Errorf("%s:%d: step %d carries no loss", path, n, *one.Step)
		}
		s := Step{Step: *one.Step, Loss: *one.Loss}
		if one.LR != nil {
			s.LR = *one.LR
		}
		if one.Grad != nil {
			s.Grad = *one.Grad
		}
		out = append(out, s)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}
