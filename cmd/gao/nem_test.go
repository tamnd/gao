package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The real listing and the real layer file, which is what this command is
// pointed at everywhere else. Nothing here fetches: these are the refusals, and
// every one of them has to happen before a byte moves.
func hpltPlan() (layers, files string) {
	return filepath.Join("..", "..", "mau", "testdata", "hplt3-vie_Latn-layers.jsonl"),
		filepath.Join("..", "..", "mau", "testdata", "hplt3-vie_Latn-listing.jsonl")
}

func TestNemRefusesBeforeItFetchesAnything(t *testing.T) {
	layers, files := hpltPlan()

	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"a source nobody ingests",
			[]string{"-source", "nope", "-seed", "s1", "-layers", layers, files},
			"is not a source gao ingests"},
		{"a source read as whole files",
			[]string{"-source", "fineweb2", "-seed", "s1", "-layers", layers, files},
			"is read as whole files rather than as a stream, and a prefix of one is not a document"},
		{"no seed",
			[]string{"-source", "hplt3", "-layers", layers, files},
			"the plan names no seed"},
		{"a tokenizer that is not there",
			[]string{"-source", "hplt3", "-seed", "s1", "-layers", layers,
				"-tokenizer", filepath.Join(t.TempDir(), "nothing.model"), files},
			"run 'gao dem model -o PATH' to fetch the tokenizer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := exec(t, append([]string{"nem"}, c.args...)...)
			if code == 0 {
				t.Fatalf("a run nobody can perform came back 0\n%s\n%s", out, errOut)
			}
			if !strings.Contains(errOut, c.want) {
				t.Errorf("the refusal does not say %q:\n%s", c.want, errOut)
			}
			// Nothing was fetched, so nothing was printed about a shard.
			if strings.Contains(out, "documents") {
				t.Errorf("a refused run read a shard:\n%s", out)
			}
		})
	}
}

// A tokenizer that is not there is caught before the reading rather than after
// it, because the reading is the expensive half and the typo is the cheap one.
func TestNemLoadsTheTokenizerBeforeTheFirstByteMoves(t *testing.T) {
	layers, files := hpltPlan()
	out := filepath.Join(t.TempDir(), "read.jsonl")

	_, errOut, code := exec(t, "nem", "-source", "hplt3", "-seed", "s1", "-layers", layers,
		"-tokenizer", filepath.Join(t.TempDir(), "nothing.model"), "-out", out, files)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, errOut)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("the run wrote its layer file before finding out it had no tokenizer")
	}
}

func TestNemUsageNamesWhatItDoesAndWhatItCosts(t *testing.T) {
	_, errOut, code := exec(t, "nem")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{
		"gao nem -source name -seed s -layers layers.jsonl",
		"-tokenizer",
		"-out",
		"the digest this prints is the digest that command prints for the same",
		"gao dem model -o PATH",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the usage does not say %q:\n%s", want, errOut)
		}
	}
}
