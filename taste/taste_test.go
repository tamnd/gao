package taste_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/tamnd/gao/layers"
	"github.com/tamnd/gao/sample"
	"github.com/tamnd/gao/taste"
)

// The reading itself. testdata holds what came back when the plan was run
// against HPLT v3 vie_Latn on 2026-08-19: 240 MB off twelve shards of six
// buckets, tokenized with the pinned gemma-3, 130833 documents and 660.0 MB of
// text. Everything below is checked against that rather than against numbers
// somebody thought a corpus would produce.
func reading(t *testing.T) taste.Sample {
	t.Helper()
	b, err := os.ReadFile("testdata/hplt3-vie_Latn-s1.json")
	if err != nil {
		t.Fatal(err)
	}
	var s taste.Sample
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func says(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, want) {
			return
		}
	}
	t.Errorf("nothing says %q:\n  %s", want, strings.Join(lines, "\n  "))
}

func silent(t *testing.T, lines []string) {
	t.Helper()
	if len(lines) > 0 {
		t.Errorf("this should have said nothing:\n  %s", strings.Join(lines, "\n  "))
	}
}

func TestTheReadingIsOfTheWholeSourceAndSaysWhatItFound(t *testing.T) {
	s := reading(t)

	silent(t, s.Blocking())
	if len(s.Readings) != 6 {
		t.Errorf("the reading covers %d layers, want 6", len(s.Readings))
	}
	if s.Read != 240_000_002 {
		t.Errorf("%d bytes were fetched, want 240000002", s.Read)
	}
	if s.Documents != 130833 {
		t.Errorf("%d documents came out, want 130833", s.Documents)
	}
	if s.Tokens == 0 || s.Tokenizer == "" {
		t.Errorf("a tokenized reading came back with %d tokens under %q", s.Tokens, s.Tokenizer)
	}
	for _, r := range s.Readings {
		if r.Drawn != r.Files {
			t.Errorf("%s has %d shards and %d were read, and every layer of this corpus is under the gate", r.Layer, r.Files, r.Drawn)
		}
		if r.Documents == 0 || r.Bytes == 0 {
			t.Errorf("%s read %d bytes and produced %d documents", r.Layer, r.Read, r.Documents)
		}
	}
}

// Every take records the sha256 of the prefix it read, which is what makes this
// checkable byte for byte instead of file by file. A third party with the seed
// draws the same shards, and this is how they confirm they read the same bytes
// of them.
func TestEveryTakeCarriesTheDigestOfTheBytesItRead(t *testing.T) {
	s := reading(t)

	seen := make(map[string]string)
	n := 0
	for _, r := range s.Readings {
		for _, take := range r.Takes {
			n++
			if !strings.HasPrefix(take.Digest, "sha256:") || len(take.Digest) != len("sha256:")+64 {
				t.Errorf("%s carries %q, which is not a sha256", take.Path, take.Digest)
			}
			if was, ok := seen[take.Digest]; ok {
				t.Errorf("%s and %s read the same bytes", was, take.Path)
			}
			seen[take.Digest] = take.Path
		}
	}
	if n != 12 {
		t.Errorf("%d takes were recorded, want 12", n)
	}
}

// The measurement this whole exercise is for. A stored byte does not hold the
// same amount of text across the corpus, and the reading is what turned that
// from an assumption into a range.
func TestTheLayersDoNotPackTheirTextAtOneRate(t *testing.T) {
	s := reading(t)

	by := make(map[string]float64)
	for _, r := range s.Readings {
		by[r.Layer] = r.Pack()
	}
	if got := by["bucket 5"]; got < 2.35 || got > 2.45 {
		t.Errorf("bucket 5 packs at %.2fx, and the reading measured 2.39x", got)
	}
	if got := by["bucket 10"]; got < 3.28 || got > 3.36 {
		t.Errorf("bucket 10 packs at %.2fx, and the reading measured 3.32x", got)
	}
	if by["bucket 10"] <= by["bucket 5"] {
		t.Error("the top bucket packs no more text per stored byte than the bottom one, and the reading says it packs 39% more")
	}
	says(t, s.Faults(), "the layers pack their text at between 2.39x and 3.32x, a spread of 39%")
}

// What mau said about the spread, carried into the file somebody quotes the rate
// out of. A reading is read without its plan beside it, so the reading has to
// repeat the thing the plan already refused to let pass.
func TestTheNarrowSpreadThePlanReportedIsReportedAgainHere(t *testing.T) {
	s := reading(t)

	says(t, s.Faults(), "6 layers were read off fewer than 16 shards each, starting with bucket 5 at 1")
	if s.Holds() {
		t.Error("a reading taken off one shard a layer holds")
	}
}

func TestTheVerdictSaysWhatWasReadAndWhatCameOut(t *testing.T) {
	v := reading(t).Verdict()

	for _, want := range []string{
		"This read 240.0 MB off 12 shards across 6 layers of hplt3",
		"at seed s1",
		"found 130833 documents holding 660.0 MB of text",
		"tokens under gemma-3",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the verdict does not say %q:\n%s", want, v)
		}
	}
}

// The layer file is the point of the run, and it has to describe the source
// rather than the run: a layer the reading has no reading for comes through with
// its stored size and nothing else, because that is exactly what tang bounds.
func TestTheLayerFileCarriesEveryLayerAndFillsInTheOnesThatWereRead(t *testing.T) {
	s := reading(t)
	all, err := layers.ReadLayers("../sample/testdata/hplt3-vie_Latn-layers.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	out := s.Layers(all)
	if len(out) != len(all) {
		t.Fatalf("%d layers went in and %d came out", len(all), len(out))
	}
	for _, l := range out {
		if !l.Sampled() {
			t.Errorf("%s came back unread and every layer of this reading was read", l.Name)
		}
		if l.Text == 0 || l.Tokens == 0 || l.Tokenizer == "" {
			t.Errorf("%s came back with %d text bytes, %d tokens and tokenizer %q", l.Name, l.Text, l.Tokens, l.Tokenizer)
		}
		if l.Stored == 0 {
			t.Errorf("%s lost its stored size on the way through", l.Name)
		}
	}

	kept := s.Layers(append(all[:len(all):len(all)], layers.Layer{Name: "bucket 4", Rank: 4, Stored: 9}))
	last := kept[len(kept)-1]
	if last.Name != "bucket 4" || last.Sampled() {
		t.Errorf("a layer with no reading came back as %+v, and it should come back untouched", last)
	}
}

// A reading nobody should publish, as opposed to one that is worth less than it
// looks. The difference is exit 1 against exit 2.
func TestTheRefusals(t *testing.T) {
	s := reading(t)

	for _, c := range []struct {
		name string
		of   func(taste.Sample) taste.Sample
		want string
	}{
		{"no source", func(s taste.Sample) taste.Sample { s.Source = ""; return s },
			"does not say what source it read"},
		{"no seed", func(s taste.Sample) taste.Sample { s.Seed = ""; return s },
			"names no seed"},
		{"nothing read", func(s taste.Sample) taste.Sample { s.Readings = nil; return s },
			"no layer was read"},
		{"a layer that gave nothing", func(s taste.Sample) taste.Sample {
			one := s.Readings[0]
			one.Documents = 0
			s.Readings = append([]taste.Reading{one}, s.Readings[1:]...)
			return s
		}, "was read and produced no documents"},
	} {
		t.Run(c.name, func(t *testing.T) {
			bad := c.of(reading(t))
			says(t, bad.Blocking(), c.want)
			if bad.Holds() {
				t.Error("a reading nobody should publish holds")
			}
			silent(t, bad.Faults())
		})
	}
	_ = s
}

// A run with no tokenizer produces a token column of zeroes, and the thing that
// says so is the report rather than the reader's guess.
func TestAReadingNobodyTokenizedSaysSoRatherThanReportingZero(t *testing.T) {
	s := reading(t)
	s.Tokenizer = ""

	says(t, s.Faults(), "nothing tokenized this reading, so the token column is zero throughout")
}

// The digest is the plan's, not one this works out for itself, because a reading
// that computed its own would agree with itself whichever plan was run.
func TestTheSampleCarriesTheDigestOfThePlanItPerformed(t *testing.T) {
	layers, err := layers.ReadLayers("../sample/testdata/hplt3-vie_Latn-layers.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	files, err := sample.ReadFiles("../sample/testdata/hplt3-vie_Latn-listing.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	p := sample.ReadPlan("hplt3", "s1", sample.Want, layers, files)
	if got, want := taste.Of(p, "", nil).Digest, p.Digest.String()[:16]; got != want {
		t.Errorf("the reading carries digest %s and the plan it ran is %s", got, want)
	}
	if got := reading(t).Digest; got != p.Digest.String()[:16] {
		t.Errorf("the reading in testdata carries digest %s and the plan at seed s1 is %s", got, p.Digest.String()[:16])
	}
}
