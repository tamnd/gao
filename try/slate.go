package try

// The slate itself. It lives in the source rather than in a file somebody edits,
// because that is the only version of fixing something in advance that means
// anything: changing a run is a diff on a pull request with a reviewer on it,
// not a line edited the afternoon the numbers came back.

import (
	"fmt"
	"strings"
)

// The seeds. Every run that varies something is trained at [Seed], which is also
// the first baseline's seed, so the difference between a run and its reference is
// the knob and nothing else. The other two baselines exist only to say how large
// a difference has to be before it is one.
const (
	Seed  = 20260501
	SeedB = 20260502
	SeedC = 20260503
)

// Fixed is the slate, closed before any of it runs.
func Fixed() Slate {
	s := Slate{
		Version: "1.0",
		Model:   Params,
		Tokens:  Tokens,
		Proxy:   Proxy,
		Compute: Compute{
			Provider: "lambda",
			Instance: "8x H100 SXM",
			GPUHours: 9400,
			USD:      22560,
			Quoted:   "2026-07-28",
		},
		Note: "forty runs, one knob each, against a baseline run three times",
	}

	base := func(id string, seed int64, note string) {
		s.Runs = append(s.Runs, Run{
			ID:   id,
			Asks: "how much two runs of the same recipe differ, which is the floor every other number on this slate is read against",
			Seed: seed,
			Note: note,
		})
	}
	base("B01", Seed, "the reference every other run is a difference from")
	base("B02", SeedB, "")
	base("B03", SeedC, "")

	run := func(id, knob, value, asks, decides string) {
		s.Runs = append(s.Runs, Run{
			ID: id, Knob: knob, Value: value, Asks: asks, Decides: decides,
			Against: "B01", Seed: Seed,
		})
	}

	// Deduplication. The baseline is MinHash at 0.85 Jaccard, which is the number
	// the literature uses and nobody has checked on Vietnamese. The sweep is the
	// only evidence `gao mill -choose` will ever have.
	dedup := "what deduplication throws away that was worth keeping, and what it keeps that was not"
	run("D01", "dedup", "0.70", dedup, "the near duplicate threshold")
	run("D02", "dedup", "0.75", dedup, "the near duplicate threshold")
	run("D03", "dedup", "0.80", dedup, "the near duplicate threshold")
	run("D04", "dedup", "0.90", dedup, "the near duplicate threshold")
	run("D05", "dedup", "exact only", "whether near duplicate removal earns its cost at all, or whether exact matching was the whole of it", "")

	// The quality classifier. Its cut decides how much of the web survives, and
	// every published value for it was set on English.
	quality := "where the quality classifier should cut, given that every point of it costs tokens the corpus does not have spare"
	run("Q01", "quality", "off", "whether the quality classifier earns its place, which is the run that has to be on the slate for the other three to mean anything", "")
	run("Q02", "quality", "0.30", quality, "the quality cut")
	run("Q03", "quality", "0.70", quality, "the quality cut")
	run("Q04", "quality", "0.90", quality, "the quality cut")

	// The tokenizer, which is the one decision on this slate that cannot be
	// revisited after the run it is made for.
	vocab := "what a vocabulary trained on Vietnamese buys, in the only currency that matters, which is what the model knows at the end rather than what the fertility table says"
	run("V01", "vocabulary", "32k expansion", vocab, "P07-2")
	run("V02", "vocabulary", "64k trained", vocab, "the from scratch vocabulary size")
	run("V03", "vocabulary", "128k trained", vocab, "the from scratch vocabulary size")
	run("V04", "vocabulary", "192k trained", vocab, "P07-1")
	run("S01", "pre-tokenization", "syllable atomic", "whether forcing token boundaries onto syllable boundaries helps a language written in syllables, or whether it only costs the model the pieces it would have found itself", "P07-3")

	// The three moves that close the gap between a trillion token run and three
	// hundred billion tokens of Vietnamese. Each one is on the slate because each
	// one fails in its own way.
	synth := "how much generated Vietnamese the mixture carries before the narrowing shows up in what the model can do"
	run("Y01", "synthetic", "0%", "what the synthetic slice is actually worth, which is the run that decides whether 14000 GPU hours of generation happens at all", "")
	run("Y02", "synthetic", "7.5%", synth, "the synthetic share")
	run("Y03", "synthetic", "22%", synth, "the synthetic share")
	run("Y04", "synthetic", "30%", synth, "the synthetic share")

	english := "what anchor language text buys in reasoning against what it costs in Vietnamese, which is the trade the whole mixture turns on"
	run("E01", "english", "0%", "whether the Vietnamese only mixture is worse, and by how much, since every point of English is a point of Vietnamese the run does not read", "")
	run("E02", "english", "5%", english, "the anchor share")
	run("E03", "english", "20%", english, "the anchor share")
	run("E04", "english", "30%", english, "the anchor share")

	epochs := "how many passes over the educational slice keep helping, which is the number that decides whether 309 billion natural tokens can fill a trillion token run"
	run("R01", "epochs", "1", epochs, "the repetition ceiling")
	run("R02", "epochs", "2", epochs, "the repetition ceiling")
	run("R03", "epochs", "6", epochs, "the repetition ceiling")
	run("R04", "epochs", "8", epochs, "the repetition ceiling")

	// The stages that are treated as correctness rather than as tuning. Each one
	// gets exactly one run, and the run exists because a stage nobody ever turned
	// off is a stage nobody has measured.
	run("N01", "normalization", "off", "what leaving two spellings of one word in the corpus costs, which is the question every pipeline that skips normalization has answered by not asking it", "")
	run("P01", "boilerplate", "off", "what the furniture on every page of a site costs a model that reads all of it", "")
	run("C01", "covering", "off", "whether tagging over personal data costs anything the corpus needed, which is worth knowing before it is defended as free", "")
	run("U01", "curriculum", "quality first", "whether the curated slice belongs at the start rather than at the end, which is the one ordering question three phases can actually answer", "")
	run("L01", "legacy pdf", "excluded", "what the transcoded 1995 to 2012 PDFs are worth, since they are the most expensive documents in the corpus per byte", "")

	translated := "whether machine translated Vietnamese helps at all, which has to be settled before it is given a place in a phase"
	run("M01", "translated", "4%", translated, "the translated share")
	run("M02", "translated", "8%", translated, "the translated share")

	ocr := "where the OCR error ceiling belongs, which decides how much of the scanned pile is admitted"
	run("O01", "ocr", "CER 5%", ocr, "the OCR ceiling")
	run("O02", "ocr", "CER 15%", ocr, "the OCR ceiling")

	forum := "what forum text is worth against the news archives it displaces, which is the prediction the whole crawl is arranged around"
	run("F01", "forum", "half", forum, "P03-5 at the model rather than at the crawl")
	run("F02", "forum", "double", forum, "P03-5 at the model rather than at the crawl")

	return s
}

// Describe is the slate in a sentence, which is what goes at the top of a report
// and in the release notes.
func (s Slate) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s of a %s parameter model over %s tokens each, scored by %s, varying %s.",
		plural(len(s.Runs), "run"), scale(s.Model), scale(s.Tokens), s.Proxy, plural(len(s.Knobs()), "thing"))
	fmt.Fprintf(&b, " The baseline is run %s at different seeds, so an effect has a floor to clear before it is one.",
		plural(len(s.Baselines()), "time"))
	fmt.Fprintf(&b, " %d of the runs settle something and the rest are exploratory, which is on the slate rather than decided afterward.", s.Decisive())
	return b.String()
}

// scale prints a count the way a training plan discusses one.
func scale(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprint(n)
	}
}
