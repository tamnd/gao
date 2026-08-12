package tang_test

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/tamnd/gao/tang"
)

// bucket is one HPLT quality bucket, sized the way they actually run: the low
// quality end of the corpus is most of the corpus.
type bucket struct {
	rank   int
	stored int64
	pack   float64 // text bytes per stored byte
	rate   float64 // tokens per byte of text
}

var buckets = []bucket{
	{1, 50_000_000_000, 3.40, 0.228},
	{2, 42_000_000_000, 3.35, 0.230},
	{3, 35_000_000_000, 3.30, 0.232},
	{4, 28_000_000_000, 3.25, 0.234},
	{5, 24_000_000_000, 3.20, 0.236},
	{6, 20_000_000_000, 3.15, 0.238},
	{7, 17_000_000_000, 3.10, 0.240},
	{8, 14_000_000_000, 3.05, 0.242},
	{9, 9_000_000_000, 3.00, 0.244},
	{10, 6_000_000_000, 2.95, 0.246},
}

// layer builds one bucket with read bytes read out of it, or none.
func layer(b bucket, read int64) tang.Layer {
	l := tang.Layer{
		Name:   fmt.Sprintf("bucket %d", b.rank),
		Rank:   b.rank,
		Stored: b.stored,
	}
	if read <= 0 {
		return l
	}
	l.Read = read
	l.Text = int64(float64(read) * b.pack)
	l.Tokens = int64(float64(l.Text) * b.rate)
	l.Tokenizer = "gao-64k"
	return l
}

// hplt is the shape of the reading that produced the 176B figure: ten quality
// buckets, five of them read at 40 MB each, weighted by what each one takes on
// disk. Everything about it is the real design except the numbers, which are
// invented because nothing has been ingested.
func hplt(read ...int) tang.Source {
	want := map[int]bool{}
	for _, r := range read {
		want[r] = true
	}
	s := tang.Source{Source: "hplt-v3 vie_Latn"}
	for _, b := range buckets {
		var n int64
		if want[b.rank] {
			n = 40_000_000
		}
		s.Layers = append(s.Layers, layer(b, n))
	}
	return s
}

// sampled is the five buckets the estimate was actually taken off.
func sampled() tang.Source { return hplt(5, 7, 8, 9, 10) }

// all is the same corpus with every layer read, which is the only shape that
// holds.
func all() tang.Source { return hplt(1, 2, 3, 4, 5, 6, 7, 8, 9, 10) }

func find(t *testing.T, s *tang.Source, name string) *tang.Layer {
	t.Helper()
	for i := range s.Layers {
		if s.Layers[i].Name == name {
			return &s.Layers[i]
		}
	}
	t.Fatalf("%s is not in the source", name)
	return nil
}

func says(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, want) {
			return
		}
	}
	t.Errorf("nothing said %q, and what came back was:\n  %s", want, strings.Join(lines, "\n  "))
}

func silent(t *testing.T, lines []string, unwanted string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, unwanted) {
			t.Errorf("something said %q and should not have:\n  %s", unwanted, l)
		}
	}
}

// The claim the package is built on. An interval from a sample narrows when the
// sample grows, and people read this range as that interval. It is not one:
// reading a layer that was already read, a hundred times harder, moves nothing,
// because the range is over the layers nobody touched.
func TestReadingTheSameLayersHarderDoesNotCloseTheRange(t *testing.T) {
	before := sampled().Spread()

	harder := sampled()
	for i := range harder.Layers {
		if harder.Layers[i].Sampled() {
			harder.Layers[i] = layer(buckets[harder.Layers[i].Rank-1], 4_000_000_000)
		}
	}
	if after := harder.Spread(); math.Abs(after-before) > 0.001 {
		t.Errorf("reading a hundred times as much of the same five layers moved the range from %.1f%% of the estimate to %.1f%%",
			before*100, after*100)
	}

	// And reading one of the layers nobody had read does close it, which is the
	// other half of the same claim.
	if after := hplt(5, 6, 7, 8, 9, 10).Spread(); after >= before {
		t.Errorf("reading a sixth layer left the range at %.1f%% of the estimate against %.1f%%", after*100, before*100)
	}
}

// Half a corpus going unread is not a rounding error and the report says so in
// bytes rather than in layers, since five of ten layers sounds like half and is
// 71% of the text.
func TestTheLayersNobodyReadAreCountedInBytesRatherThanInLayers(t *testing.T) {
	s := sampled()

	if len(s.Dark()) != 5 {
		t.Fatalf("%d layers went unread, want 5", len(s.Dark()))
	}
	if got := s.DarkShare(); got < 0.70 || got > 0.72 {
		t.Errorf("the unread layers are %.1f%% of the source, want about 71%%", got*100)
	}
	says(t, s.Faults(), "5 layers holding 71.4% of the source were never read")

	// It is a fault and not a refusal. The number is the one being quoted
	// either way, and a package that refused to compute it would be leaving the
	// quoted number with nothing next to it.
	if len(s.Blocking()) != 0 {
		t.Errorf("a stratified reading was refused outright:\n  %s", strings.Join(s.Blocking(), "\n  "))
	}
	if s.Estimate() <= 0 {
		t.Error("the estimate was not computed, so there is nothing for the fault to be a fault about")
	}
	if s.Holds() {
		t.Error("a reading that skipped 71% of the corpus holds")
	}
}

// A gap in the middle of the ordering widens an estimate. A gap at the bottom
// leans it, because the rate being scaled up was measured on cleaner text than
// the text it is being scaled over.
func TestAGapBelowEveryLayerThatWasReadIsNamedAsOne(t *testing.T) {
	s := sampled()

	under := s.Under()
	if len(under) != 4 {
		t.Fatalf("%d layers sit below every layer that was read, want 4", len(under))
	}
	for i, l := range under {
		if l.Rank != i+1 {
			t.Errorf("the layers below the sample are %v, want buckets 1 through 4", under)
			break
		}
	}
	says(t, s.Faults(), "the rate of the cleaner end of the corpus")

	// The same amount of unread corpus, sitting above the sample instead of
	// below it, is still a gap and is not a lean.
	top := hplt(1, 2, 3, 4, 5)
	if len(top.Under()) != 0 {
		t.Errorf("layers above the sample were reported as sitting below it: %v", top.Under())
	}
	silent(t, top.Faults(), "the cleaner end of the corpus")
	says(t, top.Faults(), "were never read")
}

// If every layer read at the same rate, none of this would matter, and a report
// that never said which layers disagree would be asking the reader to take the
// pooled rate on faith.
func TestLayersThatDisagreeAboutTheRateRefuseOnePooledRate(t *testing.T) {
	s := sampled()
	silent(t, s.Faults(), "do not read at one rate")

	// One layer of boilerplate, which is what a low quality bucket is, reading
	// at two thirds of what the others do.
	thin := find(t, &s, "bucket 5")
	thin.Tokens = int64(float64(thin.Tokens) * 0.66)

	says(t, s.Faults(), "the layers that were read do not read at one rate")
	says(t, s.Faults(), "bucket 5 gives")
	says(t, s.Faults(), "a choice rather than a measurement")
}

// The weights come off the manifest, which knows what a layer costs on disk and
// not what it holds. That is fine while a stored byte holds the same text
// everywhere and is an assumption the moment it does not.
func TestWeightingByWhatALayerCostsOnDiskIsItsOwnAssumption(t *testing.T) {
	s := sampled()
	silent(t, s.Faults(), "bytes of text across the layers")

	// Repetitive text compresses better, so the same 40 MB off disk comes back
	// as half again as much text.
	packed := find(t, &s, "bucket 5")
	packed.Text = int64(float64(packed.Text) * 1.5)
	packed.Tokens = int64(float64(packed.Tokens) * 1.5)

	says(t, s.Faults(), "a stored byte holds between")
	says(t, s.Faults(), "weighted by a number that is off by as much as")
}

// A rate off two megabytes is a rate off whatever pages were in those two
// megabytes.
func TestALayerReadTooThinlyToHaveARateSaysSo(t *testing.T) {
	s := hplt(5, 7, 8, 9, 10)
	thin := find(t, &s, "bucket 9")
	*thin = layer(buckets[8], 2_000_000)

	says(t, s.Faults(), "bucket 9 was read over 2.0 MB, under the 8.0 MB")

	second := find(t, &s, "bucket 10")
	*second = layer(buckets[9], 3_000_000)
	says(t, s.Faults(), "2 layers were read over less than 8.0 MB each, starting with bucket 9")
}

// The number in the README is the one worth checking, and checking it against
// the reading is the only thing that costs anybody anything.
func TestTheNumberTheProjectQuotesIsCheckedAgainstTheReading(t *testing.T) {
	s := sampled()
	s.Quoted = s.Estimate()
	silent(t, s.Faults(), "the number this project publishes")

	s.Quoted = s.High() + 20_000_000_000
	says(t, s.Faults(), "the number this project publishes is")
	says(t, s.Faults(), "so the published number is not what this sample says")
}

// Every layer read, at one rate, off enough text: that is the only shape that
// gets to be called the corpus's number rather than somebody's reading of the
// part of it they had time for.
func TestACorpusReadRightThroughHolds(t *testing.T) {
	s := all()

	if why := s.Blocking(); len(why) > 0 {
		t.Fatalf("a complete reading was refused:\n  %s", strings.Join(why, "\n  "))
	}
	if faults := s.Faults(); len(faults) > 0 {
		t.Fatalf("a complete reading carries faults:\n  %s", strings.Join(faults, "\n  "))
	}
	if !s.Holds() {
		t.Error("a reading of every layer does not hold")
	}
	if s.Low() != s.High() || s.Low() != s.Estimate() {
		t.Errorf("a reading with nothing unread still carries a range, %d to %d around %d", s.Low(), s.High(), s.Estimate())
	}
	if !strings.Contains(s.Verdict(), "Every layer was read") {
		t.Errorf("the verdict does not say the corpus was read right through:\n%s", s.Verdict())
	}
}

// The arithmetic, on a source small enough to do by hand. A layer that was read
// is scaled by its own rate and a layer that was not is scaled by the rate of
// the ones that were, which is the estimate a person computes and the reason
// the range around it has to be printed next to it.
func TestEveryLayerThatWasReadIsScaledByItsOwnRate(t *testing.T) {
	s := tang.Source{
		Source: "two layers",
		Layers: []tang.Layer{
			{Name: "low", Rank: 1, Stored: 100_000_000_000},
			{
				Name: "high", Rank: 2, Stored: 100_000_000_000,
				Read: 10_000_000, Text: 30_000_000, Tokens: 7_500_000, Tokenizer: "gao-64k",
			},
		},
	}

	// The read layer gives 0.75 tokens a stored byte, so it is 75B, and the
	// unread layer is scaled by the same 0.75 for another 75B.
	if got, want := s.Estimate(), int64(150_000_000_000); got != want {
		t.Errorf("the estimate is %d, want %d", got, want)
	}
	// One layer read means one rate, so the bound is the estimate. That is the
	// trap: a range this package can compute is not a range that means the
	// number is safe, and the fault about the unread half is what says so.
	if s.Low() != s.Estimate() || s.High() != s.Estimate() {
		t.Errorf("one observed rate produced a range of %d to %d", s.Low(), s.High())
	}
	says(t, s.Faults(), "low holds 50.0% of the source and was never read")
	says(t, s.Faults(), "of the source sits in 1 layer ranked below every layer that was read")
}

func TestAReadingThatIsNotAStratifiedSampleIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(s *tang.Source)
		want  string
	}{
		{"no layers", func(s *tang.Source) { s.Layers = nil }, "published in layers and none of them are here"},
		{"no source", func(s *tang.Source) { s.Source = "" }, "does not say what it is a reading of"},
		{"a layer twice", func(s *tang.Source) { s.Layers[0].Name = "bucket 2" }, "appears twice"},
		{"two layers at one rank", func(s *tang.Source) { s.Layers[0].Rank = 2 }, "both sit at rank 2"},
		{"no rank", func(s *tang.Source) { s.Layers[0].Rank = 0 }, "has no place in the ordering"},
		{"a layer holding nothing", func(s *tang.Source) { s.Layers[0].Stored = 0 }, "says it holds nothing on disk"},
		{"more read than held", func(s *tang.Source) { s.Layers[4].Read = s.Layers[4].Stored + 1 }, "bytes out of the"},
		{"tokens with nothing read", func(s *tang.Source) { s.Layers[0].Tokens = 1_000_000 }, "counted tokens without reading anything"},
		{"no tokenizer", func(s *tang.Source) { s.Layers[4].Tokenizer = "" }, "names no tokenizer"},
		{"two tokenizers", func(s *tang.Source) { s.Layers[6].Tokenizer = "gemma-3" }, "two tokenizers are two units"},
		{"a rate that is not Vietnamese", func(s *tang.Source) { s.Layers[4].Tokens = s.Layers[4].Text }, "outside 0.15 to 0.45"},
		{"nothing read at all", func(s *tang.Source) { *s = hplt() }, "no layer was read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sampled()
			tc.spoil(&s)

			why := s.Blocking()
			if len(why) == 0 {
				t.Fatalf("the reading was accepted and should have been refused for %q", tc.want)
			}
			says(t, why, tc.want)
			if s.Holds() {
				t.Error("a reading that was refused also holds")
			}
			// A refusal is what the verdict says, since the arithmetic under it
			// is arithmetic on a reading that is not one.
			if !strings.Contains(s.Verdict(), tc.want) {
				t.Errorf("the verdict does not lead with the refusal:\n%s", s.Verdict())
			}
			if len(s.Faults()) != 0 {
				t.Errorf("a refused reading also reported faults:\n  %s", strings.Join(s.Faults(), "\n  "))
			}
		})
	}
}

// The verdict is the paragraph somebody pastes into a release note, so it has to
// carry the estimate, the range, and the reason the range is what it is.
func TestTheVerdictSaysWhatWentUnreadAndThatMoreReadingOfTheRestWillNotFixIt(t *testing.T) {
	v := sampled().Verdict()

	for _, want := range []string{
		"hplt-v3 vie_Latn estimates",
		"as thin as the thinnest layer that was read and as rich as the richest",
		"5 of 10 layers holding 71.4% of it were never opened",
		"does not close by reading more of the 5 that were",
		"readings say the estimate carries more than sampling error",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the verdict does not say %q:\n%s", want, v)
		}
	}
}
