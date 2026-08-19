package tighten

// Reading a configuration and a log back off disk.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReadRecipe reads a configuration written as one JSON object.
//
// Unknown fields are an error rather than something to ignore. A trainer's
// configuration file is where a knob gets renamed between versions, and a
// reader that skips what it does not recognize reports the plan's defaults for
// a run that used something else.
func ReadRecipe(path string) (Recipe, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Recipe{}, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var r Recipe
	if err := dec.Decode(&r); err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// ReadRun reads a training log of one JSON object per step, sorted into the
// order the steps were taken.
func ReadRun(specialist, path string, recipe Recipe) (Run, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Run{}, err
	}
	r := Run{Specialist: specialist, Recipe: recipe}
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var s Step
		if err := dec.Decode(&s); err != nil {
			return Run{}, fmt.Errorf("%s:%d: %w", path, n+1, err)
		}
		r.Steps = append(r.Steps, s)
	}
	r.Sort()
	return r, nil
}
