package doc

import (
	"math"
	"testing"
)

func TestReferenceConversion(t *testing.T) {
	// One gigabyte of extracted Vietnamese text is roughly 758 million
	// characters, 167 million syllables, and 253 million gao tokens. Every size
	// in this project traces back to this row, so it gets a test.
	const gb = int64(BytesPerGB)

	if got := CharsPerGB / 1e6; math.Abs(got-758) > 2 {
		t.Errorf("characters per GB is %.1f million, want about 758", got)
	}
	if got := float64(EstimateTokens(gb)) / 1e6; math.Abs(got-253) > 2 {
		t.Errorf("tokens per GB is %.1f million, want about 253", got)
	}
	if got := float64(EstimateSyllables(gb)) / 1e6; math.Abs(got-167) > 2 {
		t.Errorf("syllables per GB is %.1f million, want about 167", got)
	}
}

func TestEstimateBytesInvertsEstimateTokens(t *testing.T) {
	for _, tokens := range []int64{1_000, 1_000_000, 300_000_000_000} {
		round := EstimateTokens(EstimateBytes(tokens))
		if drift := math.Abs(float64(round-tokens) / float64(tokens)); drift > 0.001 {
			t.Errorf("%d tokens round tripped to %d, drift %.4f", tokens, round, drift)
		}
	}
}

func TestFertilityAgreesWithTheOtherConstants(t *testing.T) {
	// Tokens per syllable is derivable from characters per syllable and
	// characters per token. It is stated separately because it is the number
	// that gets quoted, and this test keeps the two from drifting apart.
	derived := CharsPerSyllable / CharsPerToken
	if math.Abs(derived-TokensPerSyllable) > 0.01 {
		t.Errorf("TokensPerSyllable is %v, but CharsPerSyllable/CharsPerToken is %v", TokensPerSyllable, derived)
	}
}

func TestHPLTEstimateMatchesThePublishedNumber(t *testing.T) {
	// HPLT v3 vie_Latn measures 234.5 GB of extracted text and the spec puts it
	// at 176B tokens. This is the estimate the whole project's headline is
	// measured against in S1, so the arithmetic behind it is pinned here.
	const gbOfHPLT = 234.5
	got := float64(EstimateTokens(int64(gbOfHPLT*BytesPerGB))) / 1e9
	if math.Abs(got-59.2) > 1 {
		t.Errorf("naive conversion of %.1f GB gives %.1fB tokens, want about 59.2B", gbOfHPLT, got)
	}
	// The gap between 59B and 176B is the point: the 234.5 GB figure is
	// compressed archive size, not extracted text. Anyone reaching for these
	// constants to convert a download size into a token count is about to be
	// wrong by a factor of three, and this test is where they find out.
}
