package phoi

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// The legacy encodings, checked against bytes rather than against a string
// somebody typed into a test.
//
// Everything else in this package can be tested with literals, because
// everything else takes characters. This cannot. A document in a font encoding
// is bytes, the whole question is what those bytes mean, and a test that starts
// from the characters has already answered it. So the bytes are committed, in
// testdata/legacy, and the test starts where a crawler starts.
//
// Where each file came from is written down in testdata/legacy/README.md, and it
// matters, because two of them are facts and the rest are gao agreeing with
// itself. The TCVN3 page is a real page: it reached the corpus as mojibake, and
// mojibake is reversible, so its bytes are the bytes that were on the wire. The
// TCVN3 and VISCII paragraphs were encoded by iconv, which has its own tables
// and did not get them from here. The other four encodings have no second
// implementation to encode anything, so their files were written from these
// tables and prove only that the detector picks them out from the other five.
var legacyGolden = []struct {
	file    string
	charset string
}{
	{"pho-co-ha-noi.tcvn3", "TCVN3"},
	{"song-hong.tcvn3", "TCVN3"},
	{"song-hong.viscii", "VISCII"},
	{"song-hong.vni", "VNI-WIN"},
	{"song-hong.vps", "VPS"},
	{"song-hong.bkhcm1", "BK HCM1"},
	{"song-hong.bkhcm2", "BK HCM2"},
	{"cong-bao.viscii", "VISCII"},
}

// TestGoldenLegacyBytes runs a document the whole way, from the bytes to the
// text that goes in the corpus.
//
// The assertion at the end is the golden file, and the assertions before it are
// the ones that say why the golden file is what it is: the detector named the
// encoding it was written in, and it named it out of six candidates that all
// decode these bytes to Vietnamese letters.
//
// Run go test ./phoi -update to rewrite the outputs. The bytes are never
// rewritten. They are the input.
func TestGoldenLegacyBytes(t *testing.T) {
	for _, g := range legacyGolden {
		t.Run(g.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "legacy", g.file+".bytes"))
			if err != nil {
				t.Fatal(err)
			}
			if utf8.Valid(raw) && !strings.HasPrefix(string(raw), "\x00") {
				high := false
				for _, b := range raw {
					high = high || b >= 0x80
				}
				if !high {
					t.Fatal("this file has no high bytes in it, so it is not a legacy encoded document")
				}
			}

			text := Text(raw)
			c := Detect(text)
			if c == nil {
				t.Fatalf("the detector would not name an encoding for this document, and it is in %s", g.charset)
			}
			if c.Name() != g.charset {
				t.Fatalf("the detector read this document as %s and it is in %s", c.Name(), g.charset)
			}

			got := Normalize(text)
			if got.Legacy != g.charset {
				t.Errorf("Normalize recorded the encoding as %q and it is %q", got.Legacy, g.charset)
			}
			if got.Transcoded == 0 {
				t.Error("Normalize recorded no transcoded letters")
			}

			out := filepath.Join("testdata", "legacy", g.file+".txt")
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
				t.Errorf("the document does not come out as the golden file says.\n got: %q\nwant: %q", got.Text, want)
			}
		})
	}
}

// TestEveryGoldenDocumentIsRun catches the file that gets added to the directory
// and never named in the table above, which would sit there being read by
// nobody.
func TestEveryGoldenDocumentIsRun(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "legacy", "*.bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no legacy documents in testdata")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".bytes")
		if !slices.ContainsFunc(legacyGolden, func(g struct{ file, charset string }) bool {
			return g.file == name
		}) {
			t.Errorf("testdata/legacy/%s.bytes is not in the table, so nothing reads it", name)
		}
	}
}

// iconvTables is the third source for the two encodings that have one, and how
// much of each encoding it can speak for.
//
// The counts are here rather than derived so that a table changing shows up as a
// number changing. shared is the codes iconv and gao both define, capitals is
// the codes iconv defines above 0x80 that gao leaves empty, and control is the
// letters iconv puts below 0x80, which gao does not read at all.
var iconvTables = []struct {
	file     string
	charset  string
	shared   int
	capitals int
	control  int
}{
	{"viscii.iconv", "VISCII", 128, 0, 6},
	{"tcvn5712-1.iconv", "TCVN3", 74, 48, 12},
}

// TestTheTablesAgreeWithIconv reads the encodings back against somebody else's
// implementation of them.
//
// A table like the ones in legacy_table.go is a transcription, and the failure
// mode of a transcription is a code that is quietly wrong or quietly missing.
// Nothing in this package can catch that on its own, because everything else
// here reads the same table. iconv can, for the two encodings it carries, and
// testdata/legacy/*.iconv is what it says: every letter of Vietnamese and the
// bytes iconv encodes it as. That file is committed so that this runs on a
// machine with no iconv on it, which is every machine the tests run on.
//
// The two disagree in one direction only, and the test pins how far. iconv
// defines codes gao leaves empty and gao defines none that iconv does not, which
// is the shape a subset has and not the shape a mistake has.
func TestTheTablesAgreeWithIconv(t *testing.T) {
	for _, tt := range iconvTables {
		t.Run(tt.charset, func(t *testing.T) {
			iconv := readIconv(t, tt.file)
			if len(iconv) != 134 {
				t.Fatalf("the table names %d letters and Vietnamese has 134", len(iconv))
			}

			c := charset(t, tt.charset)
			shared, capitals, control := 0, 0, 0
			for letter, bs := range iconv {
				switch {
				case len(bs) == 1 && bs[0] < 0x80:
					control++
					if !unicode.IsUpper(letter) {
						t.Errorf("iconv puts %q below 0x80, at %#x, and gao does not read down there", letter, bs[0])
					}
				case len(bs) == 1:
					got, ok := c.single[bs[0]]
					switch {
					case ok && got == letter:
						shared++
					case ok:
						t.Errorf("iconv reads %#x as %q and gao reads it as %q", bs[0], letter, got)
					default:
						capitals++
						if !unicode.IsUpper(letter) {
							t.Errorf("iconv reads %#x as %q and gao leaves it empty, and it is not a capital", bs[0], letter)
						}
					}
				default:
					t.Errorf("iconv spells %q as %d bytes and this encoding is one byte a letter", letter, len(bs))
				}
			}
			if shared != tt.shared || capitals != tt.capitals || control != tt.control {
				t.Errorf("the two tables share %d codes, iconv has %d more above 0x80 and %d below it, and the counts written down are %d, %d and %d",
					shared, capitals, control, tt.shared, tt.capitals, tt.control)
			}

			byLetter := map[rune][]byte{}
			for letter, bs := range iconv {
				byLetter[letter] = bs
			}
			for b, letter := range c.single {
				bs, ok := byLetter[letter]
				if !ok || len(bs) != 1 || bs[0] != b {
					t.Errorf("gao reads %#x as %q and iconv does not", b, letter)
				}
			}
		})
	}
}

// readIconv reads one of the committed iconv tables, a letter and the bytes it
// is encoded as, one to a line.
func readIconv(t *testing.T, name string) map[rune][]byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "legacy", name))
	if err != nil {
		t.Fatal(err)
	}

	table := map[rune][]byte{}
	lines := bufio.NewScanner(strings.NewReader(string(b)))
	for lines.Scan() {
		line := lines.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("%s: %q is not a letter and a code", name, line)
		}
		letter, _ := utf8.DecodeRuneInString(fields[0])
		var bs []byte
		for i := 0; i+1 < len(fields[1]); i += 2 {
			b, err := strconv.ParseUint(fields[1][i:i+2], 16, 8)
			if err != nil {
				t.Fatalf("%s: %q is not a code", name, fields[1])
			}
			bs = append(bs, byte(b))
		}
		table[letter] = bs
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	return table
}

func charset(t *testing.T, name string) *Charset {
	t.Helper()
	for _, c := range charsets {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no encoding called %q", name)
	return nil
}

// TestEveryByteComesBackToTheByteItWasMadeOf is the property the whole stage
// rests on.
//
// [Text] is only useful if it loses nothing, because what it hands on is what
// [Detect] has to work from, and a byte that came out as a character nobody can
// put back is a letter gone before anybody knew it was a letter. All 256 go out
// and all 256 come back.
func TestEveryByteComesBackToTheByteItWasMadeOf(t *testing.T) {
	for i := range 256 {
		b := byte(i)
		s := Text([]byte{b})
		if n := utf8.RuneCountInString(s); n != 1 {
			t.Fatalf("%#x came out as %d characters", b, n)
		}
		r, _ := utf8.DecodeRuneInString(s)
		got, ok := legacyByte(r)
		if !ok {
			t.Errorf("%#x came out as %q and cannot go back", b, r)
			continue
		}
		if got != b {
			t.Errorf("%#x came out as %q and went back as %#x", b, r, got)
		}
	}
}

// TestTheHighBytesAreTheOnesWindows1252Draws checks [Text] against a
// windows-1252 table nobody here wrote, and then checks the one place the two
// part company.
//
// x/text refuses the five codes windows-1252 leaves undefined and returns the
// replacement character for them, which is the right answer for a decoder and
// the wrong one here. Three of these six encodings put a capital in all five,
// so a document that came through that way would lose Ắ and Ế and Ố without
// anybody being able to tell it had.
func TestTheHighBytesAreTheOnesWindows1252Draws(t *testing.T) {
	undefined := []byte{0x81, 0x8d, 0x8f, 0x90, 0x9d}
	d := charmap.Windows1252.NewDecoder()
	for i := range 256 {
		b := byte(i)
		want, err := d.String(string([]byte{b}))
		if err != nil {
			t.Fatalf("%#x: %v", b, err)
		}
		got := Text([]byte{b})
		if slices.Contains(undefined, b) {
			if want != "�" {
				t.Fatalf("%#x is meant to be one of the codes windows-1252 does not define and x/text reads it as %q", b, want)
			}
			if got != string(rune(b)) {
				t.Errorf("%#x has to come through as itself and came through as %q", b, got)
			}
			continue
		}
		if got != want {
			t.Errorf("%#x is %q in windows-1252 and Text made it %q", b, want, got)
		}
	}

	for _, b := range undefined {
		var found []string
		for _, c := range charsets {
			if r, ok := c.single[b]; ok {
				found = append(found, c.name+" "+string(r))
			}
		}
		if len(found) == 0 {
			t.Errorf("%#x is not a letter in any encoding here, so keeping it is not buying anything", b)
		}
	}
}
