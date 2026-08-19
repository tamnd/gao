package normalize

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files from what the code produces now")

// The golden corpus is real Vietnamese with the damage real Vietnamese arrives
// with: a news page off a Windows box, an encyclopedia article that went through
// a bad font conversion, a forum post typed with the input method half off, text
// pulled out of a PDF, a page written throughout in the older tone mark
// convention, and a page from before Unicode that is only Vietnamese under one
// font. Passing on invented strings proves the rules fire. Passing on these
// proves they fire together, on documents nobody wrote for the test.
//
// Run go test ./normalize -update to rewrite the outputs, then read the diff.
// A golden file that changed without somebody reading the change is not a
// test.
func TestGoldenDocuments(t *testing.T) {
	ins, err := filepath.Glob("testdata/*.in")
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) == 0 {
		t.Fatal("no golden documents in testdata")
	}

	for _, in := range ins {
		name := strings.TrimSuffix(filepath.Base(in), ".in")
		t.Run(name, func(t *testing.T) {
			text, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			got := Normalize(string(text))

			out := strings.TrimSuffix(in, ".in") + ".out"
			if *update {
				if err := os.WriteFile(out, []byte(got.Text), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if got.Text != string(want) {
				t.Errorf("Normalize(%s) does not match the golden file.\n got: %q\nwant: %q", in, got.Text, want)
			}
		})
	}
}

// Running the stage twice has to give what running it once gave. Documents pass
// through this code every time the corpus is rebuilt, and a rule that keeps
// finding something to do would rewrite the corpus on every pass and invalidate
// every hash taken over the last one.
func TestNormalizingTwiceChangesNothing(t *testing.T) {
	ins, err := filepath.Glob("testdata/*.in")
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range ins {
		name := strings.TrimSuffix(filepath.Base(in), ".in")
		t.Run(name, func(t *testing.T) {
			text, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			once := Normalize(string(text))
			twice := Normalize(once.Text)
			if twice.Text != once.Text {
				t.Errorf("the second pass changed the document:\n got: %q\nwant: %q", twice.Text, once.Text)
			}
			if twice.Changed {
				t.Error("the second pass reports that it changed the document")
			}
			for _, c := range []struct {
				what string
				n    int
			}{
				{"homoglyphs", twice.Homoglyphs},
				{"invisible characters", twice.Invisible},
				{"control characters", twice.Controls},
				{"compositions", twice.Composed},
				{"tone marks", twice.Tones},
			} {
				if c.n != 0 {
					t.Errorf("the second pass found %d %s", c.n, c.what)
				}
			}
		})
	}
}

// What each document in the corpus is there to exercise. If a rule is deleted
// and every golden file is regenerated, these still fail.
func TestEachGoldenDocumentExercisesWhatItIsThereFor(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want func(Result) (bool, string)
	}{
		{"bao-dien-tu", func(r Result) (bool, string) {
			return r.Invisible > 0, "the byte order mark it starts with"
		}},
		{"bach-khoa", func(r Result) (bool, string) {
			return r.Homoglyphs >= 2 && r.Invisible > 0, "eth, a Cyrillic look-alike and a zero width space"
		}},
		{"dien-dan", func(r Result) (bool, string) {
			return r.Residue >= 3, "the words typed with the input method off"
		}},
		{"ban-in", func(r Result) (bool, string) {
			return r.Homoglyphs >= 4 && r.Invisible > 0, "fullwidth punctuation and a soft hyphen"
		}},
		{"dau-cu", func(r Result) (bool, string) {
			return r.Tones >= 6, "the syllables in the older tone mark convention"
		}},
		{"font-cu", func(r Result) (bool, string) {
			return r.Legacy == "TCVN3" && r.Transcoded > 100, "the page written in a font encoding"
		}},
	} {
		t.Run(tc.doc, func(t *testing.T) {
			text, err := os.ReadFile(filepath.Join("testdata", tc.doc+".in"))
			if err != nil {
				t.Fatal(err)
			}
			r := Normalize(string(text))
			if ok, what := tc.want(r); !ok {
				t.Errorf("%s no longer exercises %s: %+v", tc.doc, what, r)
			}
		})
	}
}

// Nothing that survives normalization may be flagged as residue in a document
// that was typed properly. The detector runs over every syllable of every
// document in the corpus, and a false positive here is a good document thrown
// away later.
func TestTheResidueDetectorDoesNotFireOnCleanDocuments(t *testing.T) {
	for _, doc := range []string{"bao-dien-tu", "bach-khoa", "ban-in", "dau-cu", "font-cu"} {
		text, err := os.ReadFile(filepath.Join("testdata", doc+".in"))
		if err != nil {
			t.Fatal(err)
		}
		if r := Normalize(string(text)); r.Residue != 0 {
			t.Errorf("%s: %d syllables flagged as input method residue, want 0", doc, r.Residue)
		}
	}
}
