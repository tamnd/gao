package doc

// Vietnamese unit conversions, measured on gao source material. Every size in
// this project is quoted in one of five units and they are not interchangeable
// without these numbers.
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
//
// They are the GlotCC numbers, from file 0 of the vie_Latn snapshot: 500000
// documents, 4197188910 bytes, 3228869043 characters, 679747220 syllables, and
// 983022920 tokens under the pinned tokenizer. Every ratio below is that one
// count divided two ways, which is why they agree with each other. They were
// four assumed figures until 2026-08-18 and the note here said they were
// measured, which they were not.
//
// One source is not the corpus and the sources disagree. Bytes per character
// runs 1.2489 on FinePDFs, 1.2999 on GlotCC and 1.3180 on fineweb2, a spread of
// five percent that is the diacritic density of the text each one collected.
// Characters per syllable runs 4.60 on fineweb2, 4.75 on GlotCC and 5.73 on
// FinePDFs, and that spread is not about Vietnamese at all: FinePDFs vie_Latn
// carries documents that are not Vietnamese, so its syllables are long because
// some of them are English and Japanese. GlotCC is the middle of both ranges and
// the largest thing counted end to end, so it is what the constants say.
const (
	// BytesPerChar is UTF-8 bytes per Unicode character on Vietnamese text.
	// ASCII would be 1.00; the excess is the diacritics.
	BytesPerChar = 1.30

	// CharsPerSyllable is characters per Vietnamese syllable, counting the
	// separating space. Vietnamese syllables are short.
	CharsPerSyllable = 4.75

	// CharsPerToken is characters per token under a Gemma-3 class 256k
	// multilingual vocabulary, which is what a gao token means. A tokenizer fit
	// mostly to English does considerably worse: Llama-3.x measures 2.28.
	//
	// This one was 3.0 and it was the estimate that mattered, because it divides
	// the corpus target into a disk budget. At the measured 3.28 the same 300
	// billion tokens are 1279 GB of text rather than 1188, which is 91 GB the
	// plan had not accounted for and 86 more shards than the release format was
	// sized against.
	CharsPerToken = 3.28

	// TokensPerSyllable is the fertility figure, and it is the one that shows up
	// as a cost multiplier on every training run and as a divisor on every
	// context window.
	TokensPerSyllable = 1.45
)

// BytesPerGB is what a gigabyte means in this project: 10^9 bytes, decimal, the
// way storage and every dataset card quote it. Not 2^30. The two differ by 7.4%,
// which is small enough to look like rounding and large enough to move a corpus
// size claim by 20 billion tokens.
const BytesPerGB = 1e9

// The reference conversion, useful as a sanity check on any number in this
// project: one gigabyte of extracted Vietnamese text is roughly 769 million
// characters, 162 million syllables, and 235 million gao tokens.
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
