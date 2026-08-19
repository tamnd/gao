package dem_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tamnd/gao/dem"
	"github.com/tamnd/gao/doc"
)

// ModelEnv is where these tests look for the tokenizer.
//
// It is not in the repository. It is 4.7 MB of somebody else's protobuf, it is
// pinned by digest rather than by being carried around, and `gao count model`
// fetches it. A skip here is a real gap in what was tested rather than a
// formality, so it says how to close it.
const ModelEnv = "GAO_TOKENIZER"

func tokenizer(t *testing.T) *dem.Tokenizer {
	t.Helper()
	path := os.Getenv(ModelEnv)
	if path == "" {
		t.Skipf("no tokenizer: run `gao count model -o tokenizer.model` and set %s to it", ModelEnv)
	}
	tok, err := dem.Open(dem.Gemma3, path)
	if err != nil {
		t.Fatalf("opening the tokenizer at %s: %v", path, err)
	}
	return tok
}

func TestTheTokenizerLoadsAndIsTheOneWePinned(t *testing.T) {
	tok := tokenizer(t)

	if got := tok.Model().Name; got != "gemma-3" {
		t.Errorf("the tokenizer names itself %q, want gemma-3", got)
	}
	if got := tok.Model().Vocab; got != 262144 {
		t.Errorf("the vocabulary is %d, want 262144", got)
	}
}

func TestLoadRefusesAFileThatIsNotATokenizer(t *testing.T) {
	m, b := fixture("this is not a protobuf")

	_, err := dem.Load(m, b)
	if err == nil {
		t.Fatal("Load accepted bytes that are not a tokenizer")
	}
	if !strings.Contains(err.Error(), "does not parse") {
		t.Errorf("the message is %q, and it should say the file does not parse", err)
	}
}

func TestOpenRefusesAFileThatIsNotThePinnedTokenizer(t *testing.T) {
	path := os.Getenv(ModelEnv)
	if path == "" {
		t.Skipf("no tokenizer: run `gao count model -o tokenizer.model` and set %s to it", ModelEnv)
	}

	// The right file against the wrong pin, which is what a bumped tokenizer
	// looks like before anybody has updated the digest.
	wrong := dem.Gemma3
	wrong.Digest = strings.Repeat("0", 64)

	_, err := dem.Open(wrong, path)
	if !errors.Is(err, dem.ErrWrongModel) {
		t.Fatalf("Open against the wrong pin returned %v, want ErrWrongModel", err)
	}
}

func TestTheTokenizerCountsVietnamese(t *testing.T) {
	tok := tokenizer(t)

	// Counted by hand against the pieces the tokenizer produces, so that a
	// change in the model or the library moves this number and somebody has to
	// look at why.
	for _, c := range []struct {
		text string
		want int
	}{
		{"Cộng hòa xã hội chủ nghĩa Việt Nam", 9},
		{"Việt Nam", 3},
		{"", 0},
	} {
		if got := tok.Count(c.text); got != c.want {
			t.Errorf("Count(%q) is %d, want %d", c.text, got, c.want)
		}
	}
}

// No beginning or end of sequence marker. Two per document across half a billion
// documents is a billion tokens in the corpus size that no document contains.
func TestTheTokenizerAddsNoSequenceMarkers(t *testing.T) {
	tok := tokenizer(t)

	if got := tok.Count(""); got != 0 {
		t.Errorf("the empty string is %d tokens, want 0, which means no markers were added", got)
	}
}

// The fertility figure P07-5 predicts at 3.0 characters per token. This is not
// that measurement, which needs the corpus. It checks that the number this code
// produces is in the range where the prediction is even testable, so that a
// broken tokenizer shows up here rather than in a release note.
func TestFertilityOnVietnameseIsInTheRangeThePredictionIsAbout(t *testing.T) {
	tok := tokenizer(t)
	text := "Việt Nam là một quốc gia nằm ở khu vực Đông Nam Á, có đường bờ biển dài 3260 km và dân số hơn 100 triệu người."

	var c dem.Counts
	d := document(doc.SourceGlotCC, text, 26)
	d.NTokens = uint32(tok.Count(text))
	c.Add(d)

	if got := c.CharsPerToken(); got < 2.0 || got > 5.0 {
		t.Errorf("fertility on a Vietnamese sentence is %.2f characters per token, which is outside anything plausible", got)
	}
}

func TestCountingFillsInTheTokenColumn(t *testing.T) {
	tok := tokenizer(t)
	var tally dem.Tally
	count := tally.Counting(tok, nil)
	d := document(doc.SourceGlotCC, "Cộng hòa xã hội chủ nghĩa Việt Nam", 6)

	if err := count(d); err != nil {
		t.Fatal(err)
	}
	if d.NTokens == 0 {
		t.Error("n_tokens is still zero after a counting run with a tokenizer")
	}
	if got := tally.Source(doc.SourceGlotCC).Tokens; got != int64(d.NTokens) {
		t.Errorf("the tally has %d tokens and the document has %d", got, d.NTokens)
	}
	if tally.Tokenizer != "gemma-3" {
		t.Errorf("the tally names %q as its tokenizer, want gemma-3", tally.Tokenizer)
	}
}
