package layers_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/layers"
)

func write(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "layers.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLayersAreReadInTheOrderTheyWereWritten(t *testing.T) {
	path := write(t,
		`{"name":"bucket 10","rank":10,"stored":6000000000,"read":40000000,"text":118000000,"tokens":29028000,"tokenizer":"gao-64k"}`,
		``,
		`{"name":"bucket 9","rank":9,"stored":9000000000}`,
	)

	layers, err := layers.ReadLayers(path)
	if err != nil {
		t.Fatalf("reading two layers: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("%d layers came back, want 2", len(layers))
	}
	if layers[0].Name != "bucket 10" || layers[1].Name != "bucket 9" {
		t.Errorf("the layers came back as %s then %s", layers[0].Name, layers[1].Name)
	}
	if !layers[0].Sampled() || layers[1].Sampled() {
		t.Error("which layers were read did not survive the round trip")
	}
	if got := layers[0].Tokenizer; got != "gao-64k" {
		t.Errorf("the tokenizer came back as %q", got)
	}
}

// A layer file is exactly the sort of thing somebody extends with a second
// weight, and a reader that skips the column it does not know weights the
// corpus by a number it decided to ignore.
func TestAColumnTheReaderDoesNotKnowAboutIsAnError(t *testing.T) {
	path := write(t, `{"name":"bucket 10","rank":10,"stored":6000000000,"compressed":2000000000}`)

	if _, err := layers.ReadLayers(path); err == nil {
		t.Fatal("a layer carrying a column nobody reads was accepted")
	} else if !strings.Contains(err.Error(), "compressed") {
		t.Errorf("the error does not name the column: %v", err)
	}
}

func TestABadLineIsNamedByItsLineNumber(t *testing.T) {
	path := write(t,
		`{"name":"bucket 10","rank":10,"stored":6000000000}`,
		`{"name":"bucket 9","rank":"nine"}`,
	)

	_, err := layers.ReadLayers(path)
	if err == nil {
		t.Fatal("a rank that is not a number was accepted")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("the error does not say which line is wrong: %v", err)
	}
}

func TestAFileThatIsNotThereSaysSo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.jsonl")

	_, err := layers.ReadLayers(missing)
	if err == nil {
		t.Fatal("a file that does not exist was read")
	}
	if !strings.Contains(err.Error(), "nope.jsonl") {
		t.Errorf("the error does not name the file: %v", err)
	}
}
