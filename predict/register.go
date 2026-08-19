package predict

// register is the published table. The identifiers are the ones the
// specification uses and they are numbered by the document a prediction was
// written in, which is why they do not run in slice order: P03-1 is the first
// prediction of the acquisition document and it is measured in S1, while the
// rest of the P03 block is measured by the crawl in S3.
//
// The claims are rewritten here in plain sentences rather than copied with their
// symbols, because the register is published and a claim nobody can read is a
// claim nobody can hold anybody to. The numbers are not touched.
//
// Nothing in this table carries a result. Every prediction is open, the work
// that would measure them has not run, and that is the state the register was
// meant to be published in. Results arrive as a file and go on with Apply.
var register = []Prediction{
	{
		ID: "P03-1", Slice: "S1", State: Open,
		Claim: "the exact HPLT v3 vie_Latn token count lands within 15% of the 176B estimate",
	},
	{
		ID: "P03-2", Slice: "S3", State: Open,
		Claim: "the Common Crawl recovery pass yields 15B tokens give or take 5B, with the first query supplying more than half of it",
	},
	{
		ID: "P03-3", Slice: "S3", State: Open,
		Claim: "the second recovery query, .vn hosts carrying a language flag that is not Vietnamese, yields at least 1B tokens",
	},
	{
		ID: "P03-4", Slice: "S3", State: Open,
		Claim: "the crawl's net yield holds at 0.15 unique documents per fetch or better",
	},
	{
		ID: "P03-5", Slice: "S3", State: Open,
		Claim: "forums contribute more tokens than news archives",
	},
	{
		ID: "P03-6", Slice: "S3", State: Open,
		Claim: "at least 60% of Vietnamese university repositories expose a working OAI-PMH endpoint",
	},
	{
		ID: "P03-7", Slice: "S3", State: Open,
		Claim: "certificate transparency logs find at least 200,000 .vn hosts the seed list does not have",
	},
	{
		ID: "P03-8", Slice: "S3", State: Open,
		Claim: "under 0.5% of crawled hosts issue a removal request or a block",
	},
	{
		ID: "P04-1", Slice: "S4", State: Open,
		Claim: "the forum handler raises net tokens per page by at least 40% against generic extraction",
	},
	{
		ID: "P04-2", Slice: "S4", State: Open,
		Claim: "no more than 45% of Vietnamese institutional PDFs route to OCR",
	},
	{
		ID: "P04-3", Slice: "S4", State: Open,
		Claim: "at least 5% of pre-2012 PDFs are legacy encoded, and at least 90% of those transcode without OCR",
	},
	{
		ID: "P04-4", Slice: "S4", State: Open,
		Claim: "an open OCR engine clears a diacritic error rate of 1.5% with no Vietnamese finetune",
	},
	{
		ID: "P04-5", Slice: "S4", State: Open,
		Claim: "the speech recognition output passes the repetition check on at least 97% of hours",
	},
	{
		ID: "P04-6", Slice: "S4", State: Open,
		Claim: "the whole extraction stage costs under 6,000 GPU hours",
	},
	{
		ID: "P04-7", Slice: "S4", State: Open,
		Claim: "subtitle tracks a person wrote are under 20% of the captions available",
	},
	{
		ID: "P05-1", Slice: "S2", State: Open,
		Claim: "a Vietnamese tuned language identifier admits at least 2B tokens stock fastText rejects, at a precision of 0.95 or better",
	},
	{
		ID: "P05-2", Slice: "S2", State: Open,
		Claim: "the top 15% by the gao quality classifier beats a random 15% by at least 3 VMLU points",
	},
	{
		ID: "P05-3", Slice: "S2", State: Open,
		Claim: "the deduplication threshold that works best sits between 0.7 and 0.8, at least 1 VMLU point ahead of 0.9",
	},
	{
		ID: "P05-4", Slice: "S2", State: Open,
		Claim: "personal data recall reaches 0.95 on national ID numbers, phone numbers and email addresses, and 0.85 on addresses",
	},
	{
		ID: "P05-5", Slice: "S2", State: Open,
		Claim: "normalization changes at least 3% of documents by at least one byte",
	},
	{
		ID: "P05-6", Slice: "S2", State: Open,
		Claim: "global deduplication keeps between 55% and 70% of the tokens that went into it",
	},
	{
		ID: "P05-7", Slice: "S2", State: Open,
		Claim: "decontamination finds at least one standard benchmark materially inside the corpus",
	},
	{
		ID: "P06-1", Slice: "S6", State: Open,
		Claim: "the natural corpus publishes in under 420 GB of Parquet",
	},
	{
		ID: "P06-2", Slice: "S6", State: Open,
		Claim: "sorting documents by host buys at least 5% compression",
	},
	{
		ID: "P06-3", Slice: "S6", State: Open,
		Claim: "gao store reproduce comes back byte identical on every stage at the first attempt",
	},
	{
		ID: "P06-4", Slice: "S6", State: Open,
		Claim: "the metadata columns cost under 12% of the release bytes",
	},
	{
		ID: "P06-5", Slice: "S6", State: Open,
		Claim: "under 100 documents are tombstoned in the first year",
	},
	{
		ID: "P07-1", Slice: "S5", State: Open,
		Claim: "a 192k vocabulary trained on gao reaches 1.35 tokens per syllable or better and stays within 8% of Gemma-3 on English and code",
	},
	{
		ID: "P07-2", Slice: "S5", State: Open,
		Claim: "adding 32k Vietnamese tokens to a base vocabulary improves fertility by at least 15% and still does not beat the baseline at matched compute",
	},
	{
		ID: "P07-3", Slice: "S5", State: Open,
		Claim: "pre-tokenizing to whole syllables costs at least 2 VMLU points",
	},
	{
		ID: "P07-4", Slice: "S5", State: Open,
		Claim: "every serious candidate tokenizer fails at least one of the gates T1 through T8 on its first run",
	},
	{
		ID: "P07-5", Slice: "S5", State: Open,
		Claim: "measured Gemma-3 fertility on gao is 3.0 characters per token give or take 0.15",
	},
	{
		ID: "P08-1", Slice: "S8", State: Open,
		Claim: "150B tokens of gao-synth beats 150B tokens of anchor English by at least 2 VMLU points",
	},
	{
		ID: "P08-2", Slice: "S7", State: Open,
		Claim: "com-8B-cpt continued on gao beats the CulturaX arm by at least 4 VMLU points",
	},
	{
		ID: "P08-3", Slice: "S7", State: Open,
		Claim: "the filters only arm captures at least half of gao's advantage",
	},
	{
		ID: "P08-4", Slice: "S5", State: Open,
		Claim: "four epochs on the gao-edu subset beats one epoch at matched compute",
	},
	{
		ID: "P08-5", Slice: "S5", State: Open,
		Claim: "a 70/30 split of Vietnamese to replay beats both 50/50 and 90/10",
	},
	{
		ID: "P08-6", Slice: "S8", State: Open,
		Claim: "com-30B-A3B sustains model FLOPs utilization of 40% or better in FP8",
	},
	{
		ID: "P08-7", Slice: "S8", State: Open,
		Claim: "the best of the three phase three mixtures beats the worst by at least 3 VMLU points",
	},
	{
		ID: "P08-8", Slice: "S8", State: Open,
		Claim: "com-30B-A3B beats every open Vietnamese model on VMLU",
	},
	{
		ID: "P09-1", Slice: "S9", State: Open,
		Claim: "the diacritic restoration specialist transfers at least 1.5 points to VMLU and to reading comprehension",
	},
	{
		ID: "P09-2", Slice: "S9", State: Open,
		Claim: "multi-teacher on-policy distillation keeps at least 90% of the specialists' gains where weight merging keeps no more than 70%",
	},
	{
		ID: "P09-3", Slice: "S9", State: Open,
		Claim: "native origin finetuning beats translated origin on Vietnamese writing quality by a wide margin",
	},
	{
		ID: "P09-4", Slice: "S9", State: Open,
		Claim: "seven parallel specialists beat one multi-domain run by at least 3 points",
	},
	{
		ID: "P09-5", Slice: "S9", State: Open,
		Claim: "post-training keeps at least 90% of the base model's needle performance at 131k tokens",
	},
	{
		ID: "P09-6", Slice: "S9", State: Open,
		Claim: "over-refusal comes in under 5% while harm refusal stays above 90%",
	},
	{
		ID: "P09-7", Slice: "S9", State: Open,
		Claim: "an optional preference optimization pass moves human rated quality by under 2 points",
	},
	{
		ID: "P10-1", Slice: "S5", State: Open,
		Claim: "the ablation proxy ranks recipes at a correlation of 0.7 or better against the 8B runs",
	},
	{
		ID: "P10-2", Slice: "S7", State: Open,
		Claim: "vi-adherence on the continued pretraining model, before anything is done about it, comes in under 90%",
	},
	{
		ID: "P10-3", Slice: "S2", State: Open,
		Claim: "at least 3 of the 13 standard benchmarks turn up materially contaminated",
	},
	{
		ID: "P10-4", Slice: "S5", State: Open,
		Claim: "vi-cloze and full scale VMLU agree on at least 80% of pairwise recipe comparisons",
	},
	{
		ID: "P10-5", Slice: "S9", State: Open,
		Claim: "human raters tell native finetuning from translated finetuning more than 80% of the time",
	},
	{
		ID: "P10-6", Slice: "S9", State: Open,
		Claim: "com-30B-A3B-instruct clears E12",
	},
	{
		ID: "P11-1", Slice: "S6", State: Open,
		Claim: "the data subtotal lands under $60,000",
	},
	{
		ID: "P11-2", Slice: "S9", State: Open,
		Claim: "the total program cost lands within 20% of $327,000",
	},
	{
		ID: "P11-3", Slice: "S3", State: Open,
		Claim: "Hugging Face ingestion is the cheapest path per token by at least 10x",
	},
	{
		ID: "P11-4", Slice: "S4", State: Open,
		Claim: "speech recognition is the most expensive path per token by at least 3x",
	},
	{
		ID: "P11-5", Slice: "S8", State: Open,
		Claim: "spot instances cover at least 70% of the GPU hours without extending wall clock",
	},
}
