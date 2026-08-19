package syllable

// Reading the sample off disk.

import (
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// ReadDocs reads plain text documents, one per file, and names each one by the
// file it came out of.
//
// The name is the base name rather than the path, because the name exists to be
// quoted back in a fault and a fault that names half a screen of directory is
// one nobody reads to the end.
func ReadDocs(paths []string) ([]Doc, error) {
	docs := make([]Doc, 0, len(paths))
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(b) {
			return nil, fmt.Errorf("%s: not UTF-8, and text that is not UTF-8 has no syllables in it to count", path)
		}
		docs = append(docs, Doc{Name: filepath.Base(path), Text: string(b)})
	}
	return docs, nil
}
