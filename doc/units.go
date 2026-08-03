package doc

// Vietnamese unit conversions, measured on gao source material rather than
// assumed. Every size in this project is quoted in one of five units and they
// are not interchangeable without these numbers.
//
// The reason this file exists is that Vietnamese sits differently on each axis
// than English does. Diacritics push it above one byte per character in UTF-8.
// Syllables are short and are written with spaces between them, so a "word"
// count on Vietnamese text is a syllable count wearing a misleading name. And
// subword tokenizers trained mostly on English spend more tokens per character
// on Vietnamese than on the languages they were fit to.
//
// These constants are for estimates only. A count that ships in a manifest is
// produced by actually counting, never by multiplying. The distinction matters
// because an estimate that gets copied into a release note becomes a measurement
// in the reader's mind, and there is no way to take it back.
const (
	// BytesPerChar is UTF-8 bytes per Unicode character on Vietnamese text.
	// ASCII would be 1.00; the excess is the diacritics.
	BytesPerChar = 1.32

	// CharsPerSyllable is characters per Vietnamese syllable, counting the
	// separating space. Vietnamese syllables are short.
	CharsPerSyllable = 4.53

	// CharsPerToken is characters per token under a Gemma-3 class 256k
	// multilingual vocabulary, which is what a gao token means. A tokenizer fit
	// mostly to English does considerably worse: Llama-3.x measures 2.28.
	CharsPerToken = 3.0

	// TokensPerSyllable is the fertility figure, and it is the one that shows up
	// as a cost multiplier on every training run and as a divisor on every
	// context window.
	TokensPerSyllable = 1.51
)

// BytesPerGB is what a gigabyte means in this project: 10^9 bytes, decimal, the
// way storage and every dataset card quote it. Not 2^30. The two differ by 7.4%,
// which is small enough to look like rounding and large enough to move a corpus
// size claim by 20 billion tokens.
const BytesPerGB = 1e9

// The reference conversion, useful as a sanity check on any number in this
// project: one gigabyte of extracted Vietnamese text is roughly 758 million
// characters, 167 million syllables, and 253 million gao tokens.
const (
	// CharsPerGB is characters per gigabyte of UTF-8 extracted text.
	CharsPerGB = BytesPerGB / BytesPerChar

	// TokensPerGB is gao tokens per gigabyte of UTF-8 extracted text.
	TokensPerGB = CharsPerGB / CharsPerToken
)

// EstimateTokens returns the estimated gao token count for a quantity of UTF-8
// extracted text. The result is an estimate and is never a substitute for
// tokenizing.
func EstimateTokens(bytes int64) int64 {
	return int64(float64(bytes) / BytesPerChar / CharsPerToken)
}

// EstimateSyllables returns the estimated syllable count for a quantity of UTF-8
// extracted text.
func EstimateSyllables(bytes int64) int64 {
	return int64(float64(bytes) / BytesPerChar / CharsPerSyllable)
}

// EstimateBytes returns the estimated UTF-8 byte count for a number of gao
// tokens. This is the inverse used when a corpus publishes a token count and we
// need to know what it will cost to store.
func EstimateBytes(tokens int64) int64 {
	return int64(float64(tokens) * CharsPerToken * BytesPerChar)
}
