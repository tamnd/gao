package main

import (
	"maps"
	"slices"
	"testing"
)

func TestSourcesReadsAListRatherThanOneName(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"hplt3", []string{"hplt3"}},
		{"glotcc,finepdfs", []string{"finepdfs", "glotcc"}},
		{" glotcc , finepdfs ", []string{"finepdfs", "glotcc"}},
		{"hplt3,", []string{"hplt3"}},
		{",", nil},
	} {
		got := slices.Sorted(maps.Keys(sources(tt.in)))
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("-source %q asked for %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestOfSourceNamesEveryOneOfThem(t *testing.T) {
	// The header line is what tells somebody watching a box which shard of the
	// fleet it is running, so it has to name all of them and in one order.
	if got := ofSource("glotcc,finepdfs"); got != " of finepdfs, glotcc" {
		t.Errorf("the header reads %q", got)
	}
	if got := ofSource(""); got != "" {
		t.Errorf("the whole corpus is named %q, want nothing", got)
	}
}
