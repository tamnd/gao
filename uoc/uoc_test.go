package uoc

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// part is a shard read at about the rate real Vietnamese comes back at, so a
// test changing the rate is changing one thing.
func part(n int, bytes int64, rate float64) Part {
	return Part{
		Path:      fmt.Sprintf("vie_Latn_%04d.parquet", n),
		Bytes:     bytes,
		Manifest:  bytes,
		Tokens:    int64(float64(bytes) * rate),
		Tokenizer: "gao-64k",
	}
}

// sampled builds a source of n parts of which k were read, with the sample sizes
// varying the way real shards do.
func sampled(k int, parts int64, bytes int64, rate float64) Source {
	s := Source{
		Source:   "hplt-v3-vie_Latn",
		Snapshot: "gao-2026-09",
		Seed:     "hplt-v3-2026-08",
		Parts:    parts,
		Bytes:    bytes,
	}
	for i := range k {
		// 200 MB to 2 GB, which is the spread that decides between the two
		// estimators, and a rate that moves part to part, since a corpus that
		// reads at one exact rate everywhere has no sampling error to report.
		size := int64(200_000_000 + (i%10)*200_000_000)
		s.Sample = append(s.Sample, part(i, size, rate*(0.96+0.01*float64(i%9))))
	}
	return s
}

func refuses(t *testing.T, s Source, want string) {
	t.Helper()
	why := s.Blocking()
	if len(why) == 0 {
		t.Fatalf("the sample was accepted and it should have been refused for %q", want)
	}
	for _, w := range why {
		if strings.Contains(w, want) {
			return
		}
	}
	t.Errorf("no refusal mentions %q, and what came back was:\n  %s", want, strings.Join(why, "\n  "))
}

func TestTheEstimateScalesTheKnownTotalRatherThanTheSample(t *testing.T) {
	// 1214 parts holding 700 GB, read at 0.25 tokens a byte, which is 175B.
	s := sampled(40, 1214, 700_000_000_000, 0.25)
	if !s.Settled() {
		t.Fatalf("a sample that reads cleanly was refused: %v", s.Blocking())
	}
	if got, want := s.Estimate(), int64(175_000_000_000); math.Abs(float64(got-want)) > 6e9 {
		t.Errorf("the estimate is %d and the rate against the manifest total is %d", got, want)
	}
	if s.Low() >= s.Estimate() || s.High() <= s.Estimate() {
		t.Errorf("the interval %d to %d does not sit around %d", s.Low(), s.High(), s.Estimate())
	}
}

func TestTheMeanEstimatorIsWrongWhenTheShardsAreNotTheSameSize(t *testing.T) {
	// The sample is drawn across the size range. The source as a whole is not:
	// its parts average 576 MB, which is what the manifest total divided by the
	// part count says. The mean estimator cannot see that and the ratio
	// estimator does not have to.
	s := sampled(40, 1214, 700_000_000_000, 0.25)
	ratio := s.Estimate()
	mean := s.Mean()
	if mean == ratio {
		t.Fatal("the two estimators agree, so this test is not testing anything")
	}
	gap := math.Abs(float64(mean-ratio)) / float64(ratio)
	if gap < 0.05 {
		t.Errorf("the mean estimator is %.1f%% off the ratio estimator, which is too close to make the point", gap*100)
	}
	// And the one that leans on the manifest is the one that is right, since the
	// manifest total is exact.
	if math.Abs(float64(ratio-175_000_000_000)) > math.Abs(float64(mean-175_000_000_000)) {
		t.Error("the mean estimator came closer to the truth than the ratio estimator")
	}
}

func TestReadingMoreNarrowsTheIntervalAndTheToolSaysHowMuchMore(t *testing.T) {
	small := sampled(30, 1214, 700_000_000_000, 0.25)
	if small.Width() <= 0 {
		t.Fatal("a sample whose parts read at different rates has no interval")
	}
	want := small.Width() / 2
	need := small.Needed(want)
	if need <= 0 {
		t.Fatal("halving the interval was reported as free")
	}
	// Halving an interval costs four times the sample, which is the number people
	// guess wrong, so it is asserted rather than assumed.
	if need < int64(2*len(small.Sample)) {
		t.Errorf("halving the interval was priced at %d more parts on a sample of %d", need, len(small.Sample))
	}
}

func TestAnIntervalIsRefusedOnASampleTooSmallToHaveOne(t *testing.T) {
	s := sampled(8, 1214, 700_000_000_000, 0.25)
	refuses(t, s, "under the 30")
	if s.Holds() {
		t.Error("an eight part sample published an estimate")
	}
}

func TestASampleWithNoSeedCannotCarryAnInterval(t *testing.T) {
	s := sampled(40, 1214, 700_000_000_000, 0.25)
	s.Seed = ""
	refuses(t, s, "no seed")
}

func TestAPartReadTwiceIsWeightedTwice(t *testing.T) {
	s := sampled(40, 1214, 700_000_000_000, 0.25)
	s.Sample[9] = s.Sample[3]
	refuses(t, s, "was read twice")
}

func TestASampleOffAnotherCorpusIsCaughtByTheManifest(t *testing.T) {
	s := sampled(40, 1214, 700_000_000_000, 0.25)
	s.Sample[11].Bytes = s.Sample[11].Manifest + 4096
	refuses(t, s, "the manifest pins")
}

func TestTwoTokenizersAreTwoUnits(t *testing.T) {
	s := sampled(40, 1214, 700_000_000_000, 0.25)
	s.Sample[2].Tokenizer = "gao-32k"
	refuses(t, s, "two tokenizers are two units")
}

func TestAPartThatIsNotVietnameseTextIsCaughtByItsRate(t *testing.T) {
	s := sampled(40, 1214, 700_000_000_000, 0.25)
	s.Sample[5].Tokens = s.Sample[5].Bytes // one token a byte is not this language
	refuses(t, s, "tokens a byte")
}

func TestTheExactCountIsCheckedAgainstTheIntervalThatWasPublished(t *testing.T) {
	s := sampled(40, 1214, 700_000_000_000, 0.25)

	inside := (s.Low() + s.High()) / 2
	if !s.Covers(inside) {
		t.Errorf("a count in the middle of %d to %d was called a miss", s.Low(), s.High())
	}
	if v := s.Against(inside); !strings.Contains(v, "the sampled number stood") {
		t.Errorf("a covered count does not read as one:\n  %s", v)
	}

	outside := s.High() + 20_000_000_000
	if s.Covers(outside) {
		t.Errorf("a count above %d was called covered", s.High())
	}
	v := s.Against(outside)
	if !strings.Contains(v, "the sample missed") {
		t.Errorf("a missed estimate does not say so:\n  %s", v)
	}
	// The consequence is the part worth stating, since the estimate was quoted
	// in three ratios in the README.
	if !strings.Contains(v, "every ratio quoted against the estimate") {
		t.Errorf("a missed estimate does not say what it cost:\n  %s", v)
	}
}

func TestAnIntervalTooWideToPublishSaysWhatWouldCloseIt(t *testing.T) {
	// A corpus read at rates from 0.16 to 0.44 is one nobody has read enough of,
	// which is a different thing from one that is broken.
	s := sampled(32, 1214, 700_000_000_000, 0.25)
	for i := range s.Sample {
		p := &s.Sample[i]
		p.Tokens = int64(float64(p.Bytes) * (0.16 + 0.028*float64(i%11)))
	}
	if !s.Settled() {
		t.Fatalf("a wide sample was refused rather than reported: %v", s.Blocking())
	}
	if s.Holds() {
		t.Fatalf("an interval %.1f%% wide was published against a %.1f%% line", s.Width()*100, MaxWidth*100)
	}
	v := s.Verdict()
	if !strings.Contains(v, "more parts rather than another argument") {
		t.Errorf("the verdict does not price closing the interval:\n  %s", v)
	}
	if s.Needed(MaxWidth) <= 0 {
		t.Error("reaching the line was reported as free")
	}
}

func TestASampleTheManifestCannotAccountForIsRefused(t *testing.T) {
	unnamed := sampled(40, 1214, 700_000_000_000, 0.25)
	unnamed.Source = ""
	refuses(t, unnamed, "what it is a sample of")

	nototals := sampled(40, 0, 0, 0.25)
	refuses(t, nototals, "nothing to scale by is a rate")
	if nototals.Fraction() != 0 || nototals.Estimate() != 0 || nototals.Mean() != 0 {
		t.Error("a source with no manifest totals reported an estimate anyway")
	}

	// Forty parts off a source the manifest says holds ten is a sample of
	// something else, and so is a sample holding more bytes than the source does.
	refuses(t, sampled(40, 10, 700_000_000_000, 0.25), "off a source the manifest says holds")
	refuses(t, sampled(40, 1214, 1_000_000, 0.25), "more bytes than the manifest")
}

func TestAnUnsettledSampleReachesNoVerdictAboutTheCorpus(t *testing.T) {
	var s Source
	refuses(t, s, "no parts were read")
	if s.Holds() || s.Covers(176_000_000_000) {
		t.Error("an empty sample reported on a corpus")
	}
	if v := s.Verdict(); !strings.Contains(v, "no parts were read") {
		t.Errorf("the verdict of an empty sample is %q", v)
	}
	// An empty sample estimating zero and covering nothing would read as a corpus
	// measured at nothing, which is the worst way to report that nothing was read.
	if s.Verdict() != s.Blocking()[0] {
		t.Error("the verdict does not lead with the reason there is no estimate")
	}
}

func TestReadingASampleOffDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.jsonl")
	lines := make([]string, 0, 31)
	for i := range 31 {
		lines = append(lines, fmt.Sprintf(
			`{"path":"vie_Latn_%04d.parquet","bytes":600000000,"manifest":600000000,"tokens":252000000,"tokenizer":"gao-64k"}`, i))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSource("hplt-v3-vie_Latn", "gao-2026-09", "hplt-v3-2026-08", 1214, 700_000_000_000, path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Read() != 31 {
		t.Fatalf("%d parts came off a file holding 31", s.Read())
	}
	if !s.Settled() {
		t.Errorf("a clean sample was refused: %v", s.Blocking())
	}
	if s.Tokenizer() != "gao-64k" {
		t.Errorf("the sample reads in %s", s.Tokenizer())
	}
	if f := s.Fraction(); f < 0.02 || f > 0.03 {
		t.Errorf("31 of 1214 parts is %.3f of the source", f)
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"path":"a.parquet","rows":12}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSource("x", "y", "z", 10, 10, bad); err == nil {
		t.Error("a reading carrying an undeclared column was read")
	}
	if _, err := ReadSource("x", "y", "z", 10, 10, filepath.Join(dir, "nothing.jsonl")); err == nil {
		t.Error("a file that is not there read as a sample")
	}
}
