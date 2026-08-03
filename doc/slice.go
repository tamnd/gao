// Package doc holds the contracts shared across pipeline stages: the document
// record, its provenance columns, and the plan the stages are built against.
package doc

// Slice is one unit of the build plan. Each slice ships something a third party
// could use even if the next one never runs, which is why the Ships field is
// mandatory and why every slice carries both a gate and a kill criterion.
type Slice struct {
	// ID is the stable identifier, S0 through S9.
	ID string
	// Title is the short name.
	Title string
	// Ships is the artifact the slice delivers.
	Ships string
	// Gate is the condition that must hold before the next slice starts.
	Gate string
	// Kill is the condition under which the slice stops rather than continues.
	Kill string
}

// Slices is the build plan, in dependency order. It mirrors section 1 of the
// milestones spec, and the milestone issues on GitHub are generated from it, so
// changing a gate here means changing the gate everywhere.
var Slices = []Slice{
	{
		ID:    "S0",
		Title: "Foundations and law",
		Ships: "Go module, kho store, ingest contract, licensing determination",
		Gate:  "ingest contract frozen, license classes defined, counsel questions filed",
		Kill:  "text and data mining allowance does not cover model training, fall back to recipe-only publication",
	},
	{
		ID:    "S1",
		Title: "Hugging Face ingestion",
		Ships: "205B token public union in the store, exact HPLT v3 count published against the estimate",
		Gate:  "P03-1, HPLT v3 exact count within 15% of the 176B estimate",
		Kill:  "HPLT v3 comes in below 130B, restate the headline with the real number before continuing",
	},
	{
		ID:    "S2",
		Title: "Cleaning pipeline",
		Ships: "normalizer with golden files, Vietnamese language ID, quality classifier, gao-refset",
		Gate:  "E2, E3, P05-1",
		Kill:  "PII recall on national ID or phone stays below 0.90 after two iterations, no public crawl-derived release",
	},
	{
		ID:    "S3",
		Title: "The crawl",
		Ships: "700M fetches, WARC archive, gao-crawl-2026-09",
		Gate:  "P03-4 net yield at or above 0.15, P03-8 blocks under 0.5% of hosts",
		Kill:  "net yield below 0.08 after the first 100M fetches, stop the crawl there",
	},
	{
		ID:    "S4",
		Title: "Multimodal extraction",
		Ships: "OCR evaluation set, gao-pdf, gao-voice",
		Gate:  "E1, diacritic error rate at or below 1.5%",
		Kill:  "no OCR engine clears the gate and a Vietnamese finetune does not fix it in three weeks, ship born-digital only",
	},
	{
		ID:    "S5",
		Title: "Tokenizer and ablation harness",
		Ships: "tokenizer gates T1 through T10, vi-cloze, the 40 run ablation slate",
		Gate:  "E4, E5",
		Kill:  "ablation proxy correlation below 0.5, report the slate as exploratory and flag every threshold as unvalidated",
	},
	{
		ID:    "S6",
		Title: "Corpus release",
		Ships: "gao-v1.0 at 300B tokens, signed, manifested, published",
		Gate:  "E3, publishable subset count stated",
		Kill:  "corpus lands below 250B natural tokens, publish the real number and restate the ratios",
	},
	{
		ID:    "S7",
		Title: "The continued pretraining gate",
		Ships: "com-8B-cpt across three arms, the controlled comparison",
		Gate:  "E6, E7",
		Kill:  "gao does not beat CulturaX by 4 VMLU points, stop before the from-scratch run and publish the negative result",
	},
	{
		ID:    "S8",
		Title: "Synthesis and from-scratch",
		Ships: "gao-synth, com-30B-A3B-base",
		Gate:  "P08-6, model FLOPs utilization at or above 40% in FP8",
		Kill:  "utilization below 25% after a week of tuning, train com-8B-dense instead",
	},
	{
		ID:    "S9",
		Title: "Post-training and release",
		Ships: "com-*-instruct, RLVR specialists, verifiers, full evaluation",
		Gate:  "E8 through E13",
		Kill:  "E12 fails, publish anyway with the analysis, the claim was always about the corpus",
	},
}

// SliceByID returns the slice with the given identifier. The second return value
// reports whether it was found.
func SliceByID(id string) (Slice, bool) {
	for _, s := range Slices {
		if s.ID == id {
			return s, true
		}
	}
	return Slice{}, false
}
