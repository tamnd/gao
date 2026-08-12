package mau_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/mau"
)

func write(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "files.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAListingIsReadInTheOrderItWasWritten(t *testing.T) {
	path := write(t,
		`{"layer":"bucket-1","path":"bucket-1/0000.jsonl.zst","bytes":912345678}`,
		``,
		`{"layer":"bucket-1","path":"bucket-1/0001.jsonl.zst","bytes":874321000}`,
	)

	files, err := mau.ReadFiles(path)
	if err != nil {
		t.Fatalf("reading two shards: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("%d shards came back, want 2", len(files))
	}
	if files[0].Path != "bucket-1/0000.jsonl.zst" || files[1].Path != "bucket-1/0001.jsonl.zst" {
		t.Errorf("the shards came back as %s then %s", files[0].Path, files[1].Path)
	}
	if files[0].Bytes != 912345678 {
		t.Errorf("the first shard came back at %d bytes", files[0].Bytes)
	}
}

// A listing is exactly the sort of file somebody extends with a checksum, and a
// reader that skips what it does not recognize is one that will one day skip the
// size.
func TestAColumnTheReaderDoesNotKnowAboutIsAnError(t *testing.T) {
	path := write(t, `{"layer":"bucket-1","path":"bucket-1/0000.jsonl.zst","bytes":9,"rows":41000}`)

	if _, err := mau.ReadFiles(path); err == nil {
		t.Fatal("a shard carrying a column nobody reads was accepted")
	} else if !strings.Contains(err.Error(), "rows") {
		t.Errorf("the error does not name the column: %v", err)
	}
}

func TestABadLineIsNamedByItsLineNumber(t *testing.T) {
	path := write(t,
		`{"layer":"bucket-1","path":"bucket-1/0000.jsonl.zst","bytes":9}`,
		`{"layer":"bucket-1","path":"bucket-1/0001.jsonl.zst","bytes":"big"}`,
	)

	_, err := mau.ReadFiles(path)
	if err == nil {
		t.Fatal("a size that is not a number was accepted")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("the error does not say which line is wrong: %v", err)
	}
}

func TestAListingThatIsNotThereSaysSo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.jsonl")

	_, err := mau.ReadFiles(missing)
	if err == nil {
		t.Fatal("a listing that does not exist was read")
	}
	if !strings.Contains(err.Error(), "nope.jsonl") {
		t.Errorf("the error does not name the file: %v", err)
	}
}
