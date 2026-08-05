package dem

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

// spotOf reads one part out of the fake store and checks it, which is what a
// real run does over the sample.
func spotOf(t *testing.T, s *store, snap string, tok *Tokenizer) Spot {
	t.Helper()
	parts, err := s.store().Parts(t.Context(), snap)
	if err != nil {
		t.Fatalf("Parts: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("the fixture holds %d parts, want 1", len(parts))
	}
	got, err := SpotPart(t.Context(), s.store(), parts[0], tok)
	if err != nil {
		t.Fatalf("SpotPart: %v", err)
	}
	return got
}

func TestAPartWhoseColumnsDescribeItsTextPasses(t *testing.T) {
	s := newStore(t)
	written := texts(0, 30)
	s.put(snapshot, 0, 0, written...)

	got := spotOf(t, s, snapshot, nil)
	if !got.Agrees() {
		t.Errorf("an honest part came back with %d documents wrong: %+v", got.Wrong, got.Mismatches)
	}
	if got.Documents != 30 {
		t.Errorf("the spot check read %d documents, want 30", got.Documents)
	}
	if got.Counted.Chars != stored(written).Chars {
		t.Errorf("the text counted to %d characters and the fixture is %d", got.Counted.Chars, stored(written).Chars)
	}
}

// This is the failure level one cannot see. The columns add up perfectly, the
// total is whatever they say, and no document in the part is the size its column
// claims.
func TestAColumnThatDoesNotDescribeItsTextIsCaught(t *testing.T) {
	s := newStore(t)
	docs := make([]*doc.Document, 0, 20)
	for i, text := range texts(0, 20) {
		d := document(text)
		if i == 7 {
			d.NChars += 40
		}
		docs = append(docs, d)
	}
	s.putDocs(snapshot, 0, 0, docs...)

	got := spotOf(t, s, snapshot, nil)
	if got.Agrees() {
		t.Fatal("a document whose n_chars is forty too high passed")
	}
	if got.Wrong != 1 {
		t.Errorf("%d documents came back wrong, want 1", got.Wrong)
	}
	if len(got.Mismatches) != 1 {
		t.Fatalf("the spot check reported %d mismatches, want 1", len(got.Mismatches))
	}
	m := got.Mismatches[0]
	if m.Row != 7 {
		t.Errorf("the mismatch is at row %d, want 7", m.Row)
	}
	if m.Column != kho.CharsColumn {
		t.Errorf("the mismatch names column %s, want %s", m.Column, kho.CharsColumn)
	}
	if m.Stored != m.Counted+40 {
		t.Errorf("the mismatch says stored %d and counted %d, which is not the forty that was added", m.Stored, m.Counted)
	}
	if m.DocID == "" {
		t.Error("the mismatch does not name the document, so nobody can go and look at it")
	}
}

// A part where the columns are wrong is usually a part where they are all wrong,
// and fifty thousand lines of that buries the one line saying which part it was.
// The count stays exact.
func TestEveryDocumentWrongIsCountedInFullAndReportedInPart(t *testing.T) {
	s := newStore(t)
	docs := make([]*doc.Document, 0, 40)
	for _, text := range texts(0, 40) {
		d := document(text)
		d.NSyllables++
		docs = append(docs, d)
	}
	s.putDocs(snapshot, 0, 0, docs...)

	got := spotOf(t, s, snapshot, nil)
	if got.Wrong != 40 {
		t.Errorf("%d documents came back wrong, want 40", got.Wrong)
	}
	if len(got.Mismatches) != MaxMismatches {
		t.Errorf("the spot check named %d of them, want %d", len(got.Mismatches), MaxMismatches)
	}
}

// Level two is the only place a byte count is measured, since there is no column
// for it. A bytes per character ratio has to come from somewhere.
func TestTheSpotCheckIsWhereTheByteCountComesFrom(t *testing.T) {
	s := newStore(t)
	written := texts(0, 25)
	s.put(snapshot, 0, 0, written...)

	got := spotOf(t, s, snapshot, nil)
	if got.Counted.Bytes != stored(written).Bytes {
		t.Errorf("the spot check measured %d bytes of text, want %d", got.Counted.Bytes, stored(written).Bytes)
	}
	if got.Columns.Bytes != 0 {
		t.Errorf("the column read claims %d bytes and there is no column for it", got.Columns.Bytes)
	}
	if got.Counted.BytesPerChar() <= 1 {
		t.Errorf("Vietnamese came out at %.2f bytes per character, and ASCII would be 1.00", got.Counted.BytesPerChar())
	}
}

// The cheap read is the one every number in level one is built from, so a part
// that is being read in full anyway is the place to check that it says the same
// thing the slow read does.
func TestTheCheapReadAgreesWithTheWholeRow(t *testing.T) {
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 30)...)

	got := spotOf(t, s, snapshot, nil)
	if got.Columns != got.Stored {
		t.Errorf("reading the columns gave %+v and reading the rows gave %+v", got.Columns, got.Stored)
	}
}

// A run without the pinned tokenizer can check two of the three columns
// honestly. Reporting that it checked all three would be the lie.
func TestARunWithoutATokenizerSaysWhichColumnsItChecked(t *testing.T) {
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 10)...)

	got := spotOf(t, s, snapshot, nil)
	if contains(got.Checked, kho.TokensColumn) {
		t.Error("a run with no tokenizer says it checked the token column")
	}
	for _, want := range []string{kho.CharsColumn, kho.SyllablesColumn} {
		if !contains(got.Checked, want) {
			t.Errorf("the run does not say it checked %s: %v", want, got.Checked)
		}
	}
}

// The sample size comes from the bound wanted rather than from what seemed like
// enough, and the shape of it is the thing to read off it: halving the share you
// are willing to miss doubles the sample.
func TestTheSampleSizeComesFromTheBound(t *testing.T) {
	for _, tc := range []struct {
		parts      int
		share      float64
		confidence float64
		want       int
	}{
		{1000, 0.20, 0.99, 21},
		{1000, 0.05, 0.99, 90},
		{1000, 0.01, 0.99, 459},
		{1000, 0.05, 0.95, 59},
		{1000, 0.05, 0.90, 45},
	} {
		if got := SampleSize(tc.parts, tc.share, tc.confidence); got != tc.want {
			t.Errorf("SampleSize(%d, %g, %g) = %d, want %d", tc.parts, tc.share, tc.confidence, got, tc.want)
		}
	}
}

// Wanting a bound the corpus is too small to give is not an error, it is a
// corpus that gets read in full.
func TestABoundTighterThanTheCorpusReadsAllOfIt(t *testing.T) {
	if got := SampleSize(20, 0.01, 0.99); got != 20 {
		t.Errorf("SampleSize over 20 parts = %d, want all 20", got)
	}
	if got := SampleSize(50, 0, 0.99); got != 50 {
		t.Errorf("a share of zero is a bound only a full read gives, and SampleSize said %d", got)
	}
	if got := SampleSize(50, 0.05, 1); got != 50 {
		t.Errorf("certainty is only bought by reading everything, and SampleSize said %d", got)
	}
	if got := SampleSize(0, 0.05, 0.99); got != 0 {
		t.Errorf("SampleSize over no parts = %d, want 0", got)
	}
}

// Reproducible by a third party is the whole item. Two people with the same seed
// and the same listing have to check the same parts, or the result is something
// they have to take on trust.
func TestTheSampleIsTheSameOnAnybodysMachine(t *testing.T) {
	parts := listing(200)
	first := Sample(parts, 12, "s1-2026-08")
	second := Sample(parts, 12, "s1-2026-08")
	if paths(first) != paths(second) {
		t.Errorf("two runs of the same seed picked different parts:\n%s\n%s", paths(first), paths(second))
	}
	if len(first) != 12 {
		t.Errorf("the sample holds %d parts, want 12", len(first))
	}
	if Sample(parts, 12, "s1-2026-09") == nil {
		t.Fatal("a different seed sampled nothing")
	}
	if paths(Sample(parts, 12, "s1-2026-09")) == paths(first) {
		t.Error("a different seed picked exactly the same parts, so the seed does nothing")
	}
}

// The order of the listing is the order the parts were written in, so the first
// k and every nth both systematically miss a bad run of a stage that sits
// anywhere else.
func TestTheSampleIsNotTheStartOfTheListing(t *testing.T) {
	parts := listing(200)
	got := Sample(parts, 20, "s1-2026-08")
	var early int
	for _, p := range got {
		if p.Path < parts[20].Path {
			early++
		}
	}
	if early == len(got) {
		t.Errorf("every sampled part came out of the first twenty:\n%s", paths(got))
	}
}

// A corpus that grew by a tenth should not resample the nine tenths that were
// checked last time, because the expensive half of the protocol is the one that
// reads text.
func TestGrowingASnapshotLeavesMostOfTheSampleAlone(t *testing.T) {
	before := Sample(listing(200), 20, "s1-2026-08")
	after := Sample(listing(220), 20, "s1-2026-08")

	kept := 0
	for _, p := range before {
		for _, q := range after {
			if p.Path == q.Path {
				kept++
			}
		}
	}
	if kept < 15 {
		t.Errorf("adding a tenth more parts kept only %d of 20 sampled parts", kept)
	}
}

// The sample is printed next to a listing that is in path order, and it is read
// by people.
func TestTheSampleComesBackInPathOrder(t *testing.T) {
	got := Sample(listing(60), 10, "s1-2026-08")
	for i := 1; i < len(got); i++ {
		if got[i-1].Path >= got[i].Path {
			t.Fatalf("the sample is not in path order:\n%s", paths(got))
		}
	}
}

// The plan is printed before the run rather than discovered during it, and the
// half that costs real money is the one that reads text.
func TestThePlanPricesTheHalfThatReadsText(t *testing.T) {
	parts := listing(400)
	p := Planned(snapshot, parts, 4_000_000, 0.05, 0.99, "s1-2026-08")

	if p.Parts != 400 {
		t.Errorf("the plan covers %d parts, want 400", p.Parts)
	}
	if len(p.Sample) != SampleSize(400, 0.05, 0.99) {
		t.Errorf("the plan samples %d parts and the bound wants %d", len(p.Sample), SampleSize(400, 0.05, 0.99))
	}
	if p.Columns != 4_000_000*ShapeBytes {
		t.Errorf("level one is priced at %d bytes for four million documents", p.Columns)
	}
	if p.Columns >= p.Bytes {
		t.Errorf("level one moves %d bytes against a snapshot of %d, and the point of it is that it does not", p.Columns, p.Bytes)
	}
	if p.SampleBytes <= 0 || p.SampleBytes >= p.Bytes {
		t.Errorf("the sample weighs %d against a snapshot of %d", p.SampleBytes, p.Bytes)
	}
}

// A budget quoted without the link rate it assumed is a budget for one person's
// broadband.
func TestTheBudgetIsInHoursAtANamedRate(t *testing.T) {
	const gb = 1 << 30
	if got := Hours(100*gb, 100); got < 2 || got > 3 {
		t.Errorf("100 GB at 100 Mbit came out at %.2f hours, and it is about two and a half", got)
	}
	if got := Hours(100*gb, 0); got != 0 {
		t.Errorf("a rate nobody gave came out as %.2f hours rather than as no answer", got)
	}
}

// A repo that withholds text is not a mistake, it is the restricted publication
// pattern, and a spot check against one has to say it cannot run rather than
// report a part with nothing wrong in it.
func TestAPartWithNoTextSaysTheCheckCannotRun(t *testing.T) {
	r := textlessPart(t)
	_, err := SpotOf("parts/f00000-p00000.parquet", r, r.Size(), nil)
	if !errors.Is(err, ErrNoText) {
		t.Fatalf("a part with no text column came back with %v", err)
	}
	if !strings.Contains(err.Error(), "f00000-p00000") {
		t.Errorf("the error does not name the part: %v", err)
	}
}

// With the pinned tokenizer the third column is checked too, and a part that was
// never tokenized is a part whose token column says nothing about its text.
func TestTheTokenColumnIsCheckedWhenThereIsATokenizerToCheckItWith(t *testing.T) {
	tok := tokenizer(t)
	s := newStore(t)
	s.put(snapshot, 0, 0, texts(0, 10)...)

	got := spotOf(t, s, snapshot, tok)
	if !contains(got.Checked, kho.TokensColumn) {
		t.Errorf("a run with a tokenizer does not say it checked the token column: %v", got.Checked)
	}
	if got.Counted.Tokens == 0 {
		t.Fatal("the tokenizer counted no tokens in ten documents")
	}
	if got.Wrong != 10 {
		t.Errorf("%d documents came back wrong, and the fixture was never tokenized so all ten should", got.Wrong)
	}
	if got.Mismatches[0].Column != kho.TokensColumn || got.Mismatches[0].Stored != 0 {
		t.Errorf("the mismatch is %+v, and the fixture's n_tokens is zero", got.Mismatches[0])
	}
}

// tokenizer loads the pinned model, which is not in the repository: it is 4.7 MB
// of somebody else's protobuf and `gao dem model` fetches it. A skip here is a
// real gap in what was tested rather than a formality, so it says how to close it.
func tokenizer(t *testing.T) *Tokenizer {
	t.Helper()
	path := os.Getenv("GAO_TOKENIZER")
	if path == "" {
		t.Skip("no tokenizer: run `gao dem model -o tokenizer.model` and set GAO_TOKENIZER to it")
	}
	tok, err := Open(Gemma3, path)
	if err != nil {
		t.Fatalf("opening the tokenizer at %s: %v", path, err)
	}
	return tok
}

// textlessPart is a part from a repo that withholds text. That is the restricted
// publication pattern rather than a mistake, and it is the case a spot check has
// to refuse rather than pass.
func textlessPart(t *testing.T) *bytes.Reader {
	t.Helper()
	d, ok := kho.Lookup("vietnamese-web-urls")
	if !ok {
		t.Fatal("the restricted URL repo is not in the dataset table")
	}
	var buf bytes.Buffer
	w := kho.NewParquetWriter(&buf, d, kho.Stamp{Snapshot: snapshot, Stage: "gat@0.1.0", Box: "server1"})
	for _, text := range texts(0, 5) {
		row := document(text)
		row.LicenseClass = doc.LicenseRestricted
		if err := w.Append(row); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return bytes.NewReader(buf.Bytes())
}

// listing is a snapshot's worth of parts as the store lists them.
func listing(n int) []kho.Stored {
	out := make([]kho.Stored, n)
	for i := range out {
		out[i] = kho.Stored{Path: kho.StagePath(snapshot, i, 0), Bytes: 700 << 20}
	}
	return out
}

func paths(parts []kho.Stored) string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.Path
	}
	return strings.Join(out, "\n")
}
