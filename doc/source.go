package doc

import "fmt"

// Source names the acquisition path a document came in through. It is a closed
// set rather than a free string, because the per-source contribution table in
// every release note is generated from it, and a typo in a source name splits
// one row into two without anything failing.
type Source string

// The acquisition paths. The first six are Hugging Face ingestion, the seventh
// is the Common Crawl recovery pass, and the last three are gao's own.
const (
	SourceHPLT3     Source = "hplt3"
	SourceFineWeb2  Source = "fineweb2"
	SourceCulturaX  Source = "culturax"
	SourceFinePDFs  Source = "finepdfs"
	SourceGlotCC    Source = "glotcc"
	SourceMADLAD400 Source = "madlad400"

	// SourceCCRecovery is the pass that recovers Vietnamese that Common Crawl's
	// own language detection missed or misfiled.
	SourceCCRecovery Source = "cc-recovery"

	// SourceCrawl is gao's direct crawl of the Vietnamese web.
	SourceCrawl Source = "gao-crawl"

	// SourceMedia is the non-HTML modalities: PDFs, scans, and speech.
	SourceMedia Source = "gao-media"

	// SourceSynth is model-generated text. It is a source like any other in the
	// store and unlike any other in a headline: synthetic tokens never enter the
	// corpus size, they are a separate artifact with their own generator card.
	SourceSynth Source = "gao-synth"
)

var sources = map[Source]struct {
	natural bool
	desc    string
}{
	SourceHPLT3:      {true, "HPLT v3 vie_Latn"},
	SourceFineWeb2:   {true, "FineWeb2 Vietnamese"},
	SourceCulturaX:   {true, "CulturaX Vietnamese"},
	SourceFinePDFs:   {true, "FinePDFs Vietnamese"},
	SourceGlotCC:     {true, "GlotCC Vietnamese"},
	SourceMADLAD400:  {true, "MADLAD-400 Vietnamese"},
	SourceCCRecovery: {true, "Common Crawl recovery pass"},
	SourceCrawl:      {true, "gao direct crawl"},
	SourceMedia:      {true, "PDF, scan, and speech extraction"},
	SourceSynth:      {false, "model-generated"},
}

// Valid reports whether s is a defined acquisition path.
func (s Source) Valid() bool {
	_, ok := sources[s]
	return ok
}

// Natural reports whether documents from this source count toward the corpus
// size. Model-generated text does not, and this method is the single place that
// rule is encoded so that no report has to remember it.
func (s Source) Natural() bool {
	return sources[s].natural
}

// Describe returns a human readable description of the source.
func (s Source) Describe() string {
	if d, ok := sources[s]; ok {
		return d.desc
	}
	return fmt.Sprintf("unknown source %q", string(s))
}

// Sources returns every defined acquisition path, in a stable order suitable for
// generating a contribution table.
func Sources() []Source {
	return []Source{
		SourceHPLT3, SourceFineWeb2, SourceCulturaX, SourceFinePDFs,
		SourceGlotCC, SourceMADLAD400, SourceCCRecovery, SourceCrawl,
		SourceMedia, SourceSynth,
	}
}
