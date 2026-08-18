package dem_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/tamnd/gao/dem"
	"github.com/tamnd/gao/doc"
)

// document builds a document the way the ingest would have, with its shape
// columns already filled in, because that is what a tally is given.
func document(s doc.Source, text string, syllables uint32) *doc.Document {
	d := &doc.Document{Text: text, SchemaVersion: doc.SchemaVersion}
	d.Source = s
	d.NChars = uint32(utf8.RuneCountInString(text))
	d.NSyllables = syllables
	return d
}

func TestCountsAddUpTheFourUnits(t *testing.T) {
	var c dem.Counts
	c.Add(document(doc.SourceGlotCC, "Việt Nam", 2))
	c.Add(document(doc.SourceGlotCC, "Hà Nội", 2))

	if c.Documents != 2 {
		t.Errorf("documents is %d, want 2", c.Documents)
	}
	if want := int64(len("Việt Nam") + len("Hà Nội")); c.Bytes != want {
		t.Errorf("bytes is %d, want %d", c.Bytes, want)
	}
	if c.Chars != 14 {
		t.Errorf("chars is %d, want 14", c.Chars)
	}
	if c.Syllables != 4 {
		t.Errorf("syllables is %d, want 4", c.Syllables)
	}
}

// The distinction the ledger and this file exist to keep apart. A document's
// bytes are the bytes of its text, not the bytes of the file it arrived in.
func TestBytesAreTextBytesAndNotTheFileTheTextArrivedIn(t *testing.T) {
	var c dem.Counts
	d := document(doc.SourceFineWeb2, "Việt Nam", 2)
	c.Add(d)

	if c.Bytes != int64(len(d.Text)) {
		t.Errorf("bytes is %d, and the text is %d bytes long", c.Bytes, len(d.Text))
	}
	if c.Bytes == c.Chars {
		t.Error("Vietnamese in UTF-8 should be more bytes than characters, and this text came out equal")
	}
}

func TestCountsMerge(t *testing.T) {
	a := dem.Counts{Documents: 1, Bytes: 10, Chars: 8, Syllables: 2, Tokens: 3}
	a.Merge(dem.Counts{Documents: 2, Bytes: 20, Chars: 16, Syllables: 4, Tokens: 6})

	want := dem.Counts{Documents: 3, Bytes: 30, Chars: 24, Syllables: 6, Tokens: 9}
	if a != want {
		t.Errorf("merged to %+v, want %+v", a, want)
	}
}

func TestTheRatiosAreZeroWhenTheTextWasNotTokenized(t *testing.T) {
	c := dem.Counts{Documents: 1, Bytes: 10, Chars: 8, Syllables: 2}

	if got := c.CharsPerToken(); got != 0 {
		t.Errorf("CharsPerToken with no tokens is %v, want 0, because a ratio against an uncounted denominator is worse than none", got)
	}
	if got := c.TokensPerSyllable(); got != 0 {
		t.Errorf("TokensPerSyllable with no tokens is %v, want 0", got)
	}
	if got := c.BytesPerChar(); got != 1.25 {
		t.Errorf("BytesPerChar is %v, want 1.25, and it does not need tokens", got)
	}
}

func TestTheRatiosDivideTheRightWayRound(t *testing.T) {
	c := dem.Counts{Bytes: 132, Chars: 100, Syllables: 20, Tokens: 33}

	if got := c.CharsPerToken(); got < 3.03 || got > 3.031 {
		t.Errorf("CharsPerToken is %v, want 100/33", got)
	}
	if got := c.TokensPerSyllable(); got != 1.65 {
		t.Errorf("TokensPerSyllable is %v, want 33/20", got)
	}
	if got := c.BytesPerChar(); got != 1.32 {
		t.Errorf("BytesPerChar is %v, want 132/100", got)
	}
}

func TestATallyKeepsSourcesApart(t *testing.T) {
	var tally dem.Tally
	tally.Add(document(doc.SourceGlotCC, "một", 1))
	tally.Add(document(doc.SourceGlotCC, "hai", 1))
	tally.Add(document(doc.SourceFineWeb2, "ba", 1))

	if got := tally.Source(doc.SourceGlotCC).Documents; got != 2 {
		t.Errorf("glotcc has %d documents, want 2", got)
	}
	if got := tally.Source(doc.SourceFineWeb2).Documents; got != 1 {
		t.Errorf("fineweb2 has %d documents, want 1", got)
	}
	if got := tally.Source(doc.SourceHPLT3).Documents; got != 0 {
		t.Errorf("a source that was never seen has %d documents, want 0", got)
	}
	if got := tally.Total().Documents; got != 3 {
		t.Errorf("the total is %d documents, want 3", got)
	}
}

func TestATallyReturnsItsSourcesInAStableOrder(t *testing.T) {
	var tally dem.Tally
	for _, s := range []doc.Source{doc.SourceGlotCC, doc.SourceFineWeb2, doc.SourceHPLT3, doc.SourceFinePDFs} {
		tally.Add(document(s, "text", 1))
	}

	want := []doc.Source{doc.SourceFinePDFs, doc.SourceFineWeb2, doc.SourceGlotCC, doc.SourceHPLT3}
	got := tally.Sources()
	if len(got) != len(want) {
		t.Fatalf("Sources returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Sources returned %v, want %v", got, want)
		}
	}
}

// Design rule 2. Synthetic text is in the store and out of the headline, and
// this is the method that keeps it out.
func TestSyntheticTextIsInTheTotalAndOutOfTheCorpusSize(t *testing.T) {
	var tally dem.Tally
	tally.Add(document(doc.SourceGlotCC, "viết bởi người", 3))
	tally.Add(document(doc.SourceSynth, "viết bởi máy", 3))

	if got := tally.Total().Documents; got != 2 {
		t.Errorf("the total is %d documents, want both", got)
	}
	if got := tally.Natural().Documents; got != 1 {
		t.Errorf("the corpus is %d documents, want only the natural one", got)
	}
}

// A tally is written to from the ingest's decoding goroutines.
func TestATallyIsSafeUnderConcurrentAdds(t *testing.T) {
	var tally dem.Tally
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := doc.SourceGlotCC
			if i%2 == 0 {
				s = doc.SourceFineWeb2
			}
			for range 500 {
				tally.Add(document(s, "một", 1))
			}
		}(i)
	}
	wg.Wait()

	if got := tally.Total().Documents; got != 4000 {
		t.Errorf("the total is %d documents, want 4000", got)
	}
	if got := tally.Source(doc.SourceGlotCC).Documents; got != 2000 {
		t.Errorf("glotcc has %d documents, want 2000", got)
	}
}

func TestCountingPassesEveryDocumentOn(t *testing.T) {
	var tally dem.Tally
	var seen int
	count := tally.Counting(nil, func(*doc.Document) error {
		seen++
		return nil
	})

	for range 3 {
		if err := count(document(doc.SourceGlotCC, "một", 1)); err != nil {
			t.Fatal(err)
		}
	}
	if seen != 3 {
		t.Errorf("the next step saw %d documents, want 3", seen)
	}
	if got := tally.Total().Documents; got != 3 {
		t.Errorf("the tally has %d documents, want 3", got)
	}
}

func TestCountingWorksWithNothingAfterIt(t *testing.T) {
	var tally dem.Tally
	count := tally.Counting(nil, nil)

	if err := count(document(doc.SourceGlotCC, "một", 1)); err != nil {
		t.Fatalf("counting with no next step: %v", err)
	}
	if got := tally.Total().Documents; got != 1 {
		t.Errorf("the tally has %d documents, want 1", got)
	}
}

func TestCountingWithoutATokenizerLeavesTokensAtZero(t *testing.T) {
	var tally dem.Tally
	count := tally.Counting(nil, nil)
	d := document(doc.SourceGlotCC, "một hai ba", 3)

	if err := count(d); err != nil {
		t.Fatal(err)
	}
	if d.NTokens != 0 {
		t.Errorf("n_tokens is %d on a run that did not tokenize, want 0", d.NTokens)
	}
	if tally.Tokenizer != "" {
		t.Errorf("the tally names %q as its tokenizer on a run that did not tokenize", tally.Tokenizer)
	}
}

// A file already in the ledger is never fetched again and so is never counted
// again, so a resumed ingest that starts from an empty tally writes a
// counts.json describing its own session. server1 had three FineWeb2 files
// through, 6962000 documents and 29043690013 characters, and the run that took
// the next three zeroed all of it.
func TestATallySeededFromAnEarlierRunCarriesIt(t *testing.T) {
	var first dem.Tally
	count := first.Counting(nil, nil)
	for range 3 {
		if err := count(document(doc.SourceFineWeb2, "Việt Nam", 2)); err != nil {
			t.Fatal(err)
		}
	}
	before := first.Natural()

	var second dem.Tally
	if err := second.Seed(first.Report("server1", at("2026-08-18T12:29:27Z"))); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	next := second.Counting(nil, nil)
	if err := next(document(doc.SourceFineWeb2, "Việt Nam", 2)); err != nil {
		t.Fatal(err)
	}

	got := second.Natural()
	if got.Documents != before.Documents+1 {
		t.Errorf("the resumed run counts %d documents, want the %d it carried plus the one it read", got.Documents, before.Documents)
	}
	if got.Chars <= before.Chars {
		t.Errorf("the resumed run counts %d characters against the %d it carried", got.Chars, before.Chars)
	}
}

// Seeding a tokenized run from an untokenized one would leave a token column
// covering part of the corpus with nothing saying which part, which is the same
// problem as adding two tokenizers together and gets the same answer.
func TestATallyWillNotBeSeededFromADifferentTokenizer(t *testing.T) {
	var first dem.Tally
	first.Tokenizer = "gemma-3"

	var second dem.Tally
	err := second.Seed(first.Report("server1", at("2026-08-18T12:29:27Z")))
	if !errors.Is(err, dem.ErrMixedTokenizers) {
		t.Fatalf("seeding an untokenized run from a tokenized one returned %v, want ErrMixedTokenizers", err)
	}
	if !strings.Contains(err.Error(), "no tokenizer") {
		t.Errorf("the error is %q, and it has to name both sides for somebody to fix it", err)
	}
}
