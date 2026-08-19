package store

import (
	"fmt"
	"strings"

	"github.com/tamnd/gao/fleet"
)

// The parts of a dataset card that are argument rather than arithmetic.
//
// What is in the repo is generated from the index and cannot go stale. Why
// somebody would want it, what the four upstream corpora are, what is wrong
// with the text, and how to cite it are none of them derivable from a Parquet
// footer, so they are written here and kept next to the code that renders them
// rather than in a file somebody edits after a push has already overwritten it.
//
// Every query in this file was run against the live repo before it was written
// down, and the ones that print an answer print the answer that came back. The
// answers are pinned to the source and the document count they were measured on,
// so a repo that has moved on drops the output block instead of showing a number
// that is no longer true.

// CorpusYear is the year the corpus was first published, for the citation. It
// is bumped by hand at a release rather than read off a clock, because a card
// regenerated in January should not quietly restate when the work was done.
const CorpusYear = "2026"

// cardUpstream is what each source is, for the section that says where the text
// came from.
//
// The link matters more than the prose. Somebody deciding whether to trust a
// hundred million documents wants the corpus they came from and the people who
// built it, and a card that lists four names without four links is asking them
// to search for it.
var cardUpstream = map[string]struct{ Name, URL, What string }{
	"hplt3": {
		Name: "HPLT v3",
		URL:  "https://hplt-project.org/datasets/v3.0",
		What: "Web text from the High Performance Language Technologies project, built out of Internet Archive and Common Crawl WARCs and cleaned and language identified per document. It is the largest of the four here and the only one that is not on the Hub: the data is sorted zstd JSONL on the Sigma2 NIRD datalake, behind a per language map file, which is most of the reason a Vietnamese corpus assembled by hand usually does not include it.",
	},
	"fineweb2": {
		Name: "fineweb-2",
		URL:  "https://huggingface.co/datasets/HuggingFaceFW/fineweb-2",
		What: "The multilingual half of FineWeb, which is Common Crawl put through the FineWeb recipe with the filters retuned per language. It is the largest single Vietnamese corpus published on the Hub and the one most Vietnamese web text work already starts from.",
	},
	"glotcc": {
		Name: "GlotCC v1",
		URL:  "https://huggingface.co/datasets/cis-lmu/GlotCC-V1",
		What: "A Common Crawl derived corpus from CIS at LMU Munich, built for language coverage rather than for volume, with GlotLID doing the identification. Its documents run shorter than the other web sources and its tail of hosts runs wider, which is what makes it worth having beside them rather than under them.",
	},
	"finepdfs": {
		Name: "FinePDFs",
		URL:  "https://huggingface.co/datasets/HuggingFaceFW/finepdfs",
		What: "Text extracted from PDFs instead of from HTML. It is the smallest source here and the least like the others: government circulars, legal texts, filings and course material, written to be read on a page rather than scrolled, and long. If the interesting part of Vietnamese for a piece of work is the formal register, this is where it is.",
	},
}

// cardWhatIsIt is the section a reader gets to before deciding whether to keep
// reading, so it is the problem rather than the inventory.
func cardWhatIsIt(b *strings.Builder, d Dataset, m *Manifest, x []Indexed) {
	if d.Tier != Working || m != nil {
		return
	}
	b.WriteString("## What is it\n\n")
	b.WriteString("Vietnamese is a language with a lot of public text and nowhere to get it from at once. HPLT v3 is not on the Hub at all, it is sorted zstd JSONL on a datalake behind a per language map file. GlotCC is Parquet with its own column names. fineweb-2 is Parquet with different ones. FinePDFs is text pulled out of PDFs with a third set. The four disagree about what a document is, about what the url field is called, and in places about whether there is one.\n\n")
	b.WriteString("This repo is those corpora after that has been sorted out. Every document from every source is one row of the same schema, carrying its own url, its own host, its own license class, and a locator that says which file of which upstream corpus it came out of. Read one source or read all four and it is the same query either way.\n\n")
	b.WriteString("Nothing has been dropped for quality and nothing has been deduplicated. Those are the two decisions that most change what a model trained on a corpus turns out to be, they are both irreversible, and neither of them is ours to make for somebody else. What is here instead is the columns to make them with: `lang_score`, `n_syllables`, `diacritics`, `host`, `license_class`, and the whole `upstream_fields` map each source arrived carrying.\n\n")

	if n, bytes := Total(x); n > cardBiggestUpstream {
		fmt.Fprintf(b, "For scale, the largest single Vietnamese corpus published on the Hub is the `vie_Latn` config of fineweb-2, at %s documents and 130.2 GB. This repo is at %s documents and %s, and the ingest is not finished.\n\n",
			cardCommas(cardBiggestUpstream), cardCommas(n), fleet.Size(bytes))
	}
}

// cardBiggestUpstream is the vie_Latn row count of fineweb-2, from the Hub's own
// size API rather than from anybody's summary of it. It is the number this repo
// has to beat before it is allowed to call itself the largest, so it is written
// down where the claim is made and the claim is skipped when it is not met.
const cardBiggestUpstream = 61092524

// cardSources says what the upstream corpora are.
//
// The provenance columns make this recoverable from the data, one host and one
// locator at a time. Nobody is going to do that, and a reader who does not know
// what GlotCC is cannot tell whether the twelve million documents of it in here
// are the part of this corpus they wanted.
func cardSources(b *strings.Builder, x []Indexed) {
	by := BySource(x)
	if len(by) == 0 {
		return
	}
	b.WriteString("## Where the text came from\n\n")
	b.WriteString("Four public corpora, pinned at a revision, read once, and written out under the schema below. Nothing here was crawled by us. Every one of them has its own card, its own paper in most cases, and its own terms, and the links are the place to read them.\n\n")

	for _, s := range by {
		u, ok := cardUpstream[s.Source]
		if !ok {
			fmt.Fprintf(b, "### `%s`\n\n%s documents.\n\n", s.Source, cardCommas(s.Documents))
			continue
		}
		fmt.Fprintf(b, "### %s, as `%s`\n\n", u.Name, s.Source)
		fmt.Fprintf(b, "%s\n\n", u.What)
		fmt.Fprintf(b, "%s documents here, %s of Parquet, pinned at `%s`. Upstream: %s\n\n",
			cardCommas(s.Documents), fleet.Size(s.Bytes), strings.Join(s.Snapshots, "`, `"), u.URL)
	}

	b.WriteString("Two more are pinned in the ingest manifest and are not in the repo yet. CulturaX is gated on the Hub, which is an access control formality rather than a redistribution term, and it waits on the terms being accepted. MADLAD-400 ships as gzipped JSONL rather than Parquet, so it costs a rewrite that the Parquet sources do not, and it is queued behind them.\n\n")
	fmt.Fprintf(b, "The manifest with every pinned revision, every input file and its byte count is at %s/blob/main/gat/manifest.json.\n\n", Repository)
}

// cardUses is the section the user of a corpus this size actually needs, which
// is not what is in it but what it is for.
//
// A card that stops at the schema has told a reader what the columns are called
// and left them to work out that the host column is a domain corpus, that the
// duplicates being left in is the point rather than an oversight, and that the
// diacritics column is a labeled training set for a task nobody else publishes
// one for. Each of these is a query, and each query here was run.
func cardUses(b *strings.Builder, d Dataset, m *Manifest, x []Indexed) {
	by := BySource(x)
	if len(by) == 0 || m != nil {
		return
	}
	glob := cardGlob(d, m, by)
	small, _ := cardSmallest(by)

	b.WriteString("## What you can build with it\n\n")
	b.WriteString("The reason to keep the provenance columns and skip the filtering is that different people want different corpora out of the same text. These are the ones this repo was shaped for, each with the query that starts it.\n\n")

	b.WriteString("### Pretraining, and continued pretraining\n\n")
	b.WriteString("The whole repo is more Vietnamese than most runs have the budget for, so the first thing a pretraining corpus needs is a filter and a syllable count to spend against. Both are columns, so the count is cheap and the filter does not read the text.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT count(*) AS documents, sum(n_syllables) AS syllables\n")
	fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
	b.WriteString("WHERE lang_score >= 0.9 AND n_syllables >= 200 AND diacritics = 'present';\n")
	b.WriteString("```\n\n")
	cardMeasuredBox(b, small, cardMeasured{
		Source: "finepdfs", Documents: 1218257,
		Cols: []cardColumn{
			{Head: "documents", Type: "int64", Right: true, Cells: []string{"952841"}},
			{Head: "syllables", Type: "int128", Right: true, Cells: []string{"3826960848"}},
		},
	})
	b.WriteString("Vietnamese is written in syllables and counted here in them, because a syllable count is a property of the text and a token count is a property of somebody's tokenizer. Across the tokenizers we have measured on this corpus a syllable costs between 1.25 and 1.32 tokens, so a syllable budget converts to a token budget by multiplying, and it does not go stale when the tokenizer changes.\n\n")
	b.WriteString("For continued pretraining of a model that already speaks some Vietnamese, take one source rather than all of them. The four were built by different projects with different filters, so they fail differently, and a run that only ever sees one of them is a cleaner experiment than a run that sees a blend nobody has characterized.\n\n")

	b.WriteString("### A corpus for one domain\n\n")
	b.WriteString("`host` is on every row and it is dictionary encoded, so grouping by it across a whole source is a column scan rather than a text read. This is how the legal corpus, the finance corpus and the health corpus come out of a general one.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT host, count(*) AS documents, sum(n_syllables) AS syllables\n")
	fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
	b.WriteString("GROUP BY host ORDER BY documents DESC LIMIT 5;\n")
	b.WriteString("```\n\n")
	cardMeasuredBox(b, small, cardMeasured{
		Source: "finepdfs", Documents: 1218257,
		Cols: []cardColumn{
			{Head: "host", Type: "varchar", Cells: []string{"static2.vietstock.vn", "static.luatvietnam.vn", "dmec.moh.gov.vn", "cafef1.mediacdn.vn", "cldup.com"}},
			{Head: "documents", Type: "int64", Right: true, Cells: []string{"29431", "24418", "16702", "10784", "7801"}},
			{Head: "syllables", Type: "int128", Right: true, Cells: []string{"169121447", "93030746", "8333159", "31723952", "1177148"}},
		},
	})
	b.WriteString("Two securities sites, the national drug administration and a legal publisher, in the top five of a source that was assembled with none of that in mind. Swap the `LIMIT` for a `WHERE host IN (...)` and the domain corpus is a subset of a repo somebody else is already hosting.\n\n")

	b.WriteString("### A Vietnamese tokenizer\n\n")
	b.WriteString("A tokenizer wants a few gigabytes of representative text, not a quarter of a terabyte, and it wants the text to be representative rather than the first rows of the first file. Sample across sources and write the sample out once.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("COPY (\n")
	b.WriteString("  SELECT text\n")
	fmt.Fprintf(b, "  FROM read_parquet('%s')\n", glob)
	b.WriteString("  WHERE n_syllables BETWEEN 100 AND 2000\n")
	b.WriteString("  USING SAMPLE 200000 ROWS\n")
	b.WriteString(") TO 'tokenizer-sample.txt' (FORMAT csv, HEADER false, QUOTE '');\n")
	b.WriteString("```\n\n")
	b.WriteString("Run it once per source and concatenate, rather than once over a glob of all four, so that the mix is one you chose. The sources are not the same size and sampling the union hands the tokenizer whatever the largest one happens to be.\n\n")
	b.WriteString("The documents have newlines in them, so that file has many more lines than it has documents. Feed it to a tokenizer trainer as a stream of text rather than as one document per line, or write Parquet out instead of CSV and keep the row boundaries.\n\n")

	b.WriteString("### Deduplication and overlap research\n\n")
	b.WriteString("This repo is one of the few places the same Vietnamese page exists several times with its provenance intact, because nothing here has been deduplicated. That is a defect in a training corpus and it is the whole dataset for anybody working on dedup, near duplicate detection, or the question of how much four public web corpora actually overlap.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT count(*) AS urls, count(DISTINCT url) AS distinct_urls\n")
	fmt.Fprintf(b, "FROM read_parquet('%s');\n", glob)
	b.WriteString("```\n\n")
	cardMeasuredBox(b, small, cardMeasured{
		Source: "finepdfs", Documents: 1218257,
		Cols: []cardColumn{
			{Head: "urls", Type: "int64", Right: true, Cells: []string{"1218257"}},
			{Head: "distinct_urls", Type: "int64", Right: true, Cells: []string{"1208076"}},
		},
	})
	b.WriteString("Ten thousand repeats inside a single source, before anybody has compared it to the other three.\n\n")
	cardOverlap(b, d, by)
	b.WriteString("`dup_cluster` and `is_representative` are in the schema for the stage that will do this properly, and they are zero in every row here.\n\n")

	b.WriteString("### Diacritic restoration, and language identification\n\n")
	b.WriteString("Vietnamese loses its diacritics constantly, in search boxes, in filenames, in chat, and restoring them is a real task with almost no labeled data published for it. The `diacritics` column labels every document as present, absent or mixed at ingest, which makes this corpus a training set for that task rather than only a source of text for it.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT diacritics, count(*) AS documents, round(avg(lang_score), 3) AS mean_score\n")
	fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
	b.WriteString("GROUP BY diacritics ORDER BY documents DESC;\n")
	b.WriteString("```\n\n")
	cardMeasuredBox(b, small, cardMeasured{
		Source: "finepdfs", Documents: 1218257,
		Cols: []cardColumn{
			{Head: "diacritics", Type: "varchar", Cells: []string{"present", "mixed", "absent"}},
			{Head: "documents", Type: "int64", Right: true, Cells: []string{"1078990", "108122", "31145"}},
			{Head: "mean_score", Type: "double", Right: true, Cells: []string{"0.995", "0.951", "0.37"}},
		},
	})
	b.WriteString("A hundred and eight thousand documents with the tone marks partly stripped and thirty one thousand with them gone, in one source, already paired with the identifier's confidence collapsing from 0.995 to 0.37 as they go. Nobody publishes that pairing on purpose.\n\n")
	b.WriteString("`lang_score` is the upstream identifier's own confidence, kept rather than thresholded, so a language identification experiment can see the documents that a threshold would have removed. That is the set that matters: nobody learns anything from the documents every identifier already agrees on.\n\n")

	b.WriteString("### Retrieval, embeddings and evaluation sets\n\n")
	b.WriteString("Every row has a url and a host next to its text, so a retrieval corpus comes out of this without a separate metadata store, and the host doubles as a weak label for the kind of page it is. For an evaluation set, the same columns are what makes a held out slice defensible: hold out by host rather than by row, and the documents in the training set are not the same pages under a different path.\n\n")

	b.WriteString("### Contamination checks\n\n")
	b.WriteString("If a Vietnamese benchmark is public, some of it is in a web corpus. `contam_flags` is in the schema for the stage that will mark this and is empty in every row here, so for now the check is a search, which is a text read and is the expensive kind of query. Point it at one part first.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT url, host\n")
	fmt.Fprintf(b, "FROM read_parquet('%s')\n", cardOnePart(d, m, x))
	b.WriteString("WHERE contains(text, 'a sentence from your benchmark');\n")
	b.WriteString("```\n\n")

	b.WriteString("### Rebuilding it yourself\n\n")
	b.WriteString("`source` and `source_locator` say which file of which upstream corpus each row came out of, down to the row offset, and `url` says what the page was. Between them a reader can go back to the original corpus and check any document here against it, or skip this repo entirely and take only the list of what is in it. That is deliberate. A corpus nobody can audit is a corpus somebody has to take on trust, and this one is assembled out of other people's work.\n\n")
}

// cardMeasured is a query result the card prints under the query, tied to the
// data it was measured against.
//
// The counts on this card are generated and cannot go stale. An example output
// is measured once and cannot be regenerated without running the query again, so
// it carries the source and the document count it was true for, and the card
// drops it rather than print it over data it no longer describes.
type cardMeasured struct {
	Source    string
	Documents int64
	Cols      []cardColumn
}

// cardOverlap is the cross source half of the deduplication section: the query
// that joins two named sources on url, and the answer it gave.
//
// It names two sources rather than using the one the rest of the examples run
// against, because the point of it is the pair. That makes it the one block on
// this card that can name a directory the repo does not have, so it is written
// only when both of them are in the index, and its measured answer is pinned to
// both document counts rather than to one.
func cardOverlap(b *strings.Builder, d Dataset, by []SourceIndex) {
	counts := map[string]int64{}
	for _, s := range by {
		counts[s.Source] = s.Documents
	}
	left, right := "glotcc", "fineweb2"
	if counts[left] == 0 || counts[right] == 0 {
		return
	}

	b.WriteString("Joining two sources on `url` is the cross corpus version, and it is a column scan of both rather than a text read, which on the two web sources is a few minutes.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT count(*) AS shared_urls FROM\n")
	fmt.Fprintf(b, "  (SELECT DISTINCT url FROM read_parquet('%s')) a\n", cardSourceGlob(d, left))
	fmt.Fprintf(b, "  JOIN (SELECT DISTINCT url FROM read_parquet('%s')) b\n", cardSourceGlob(d, right))
	b.WriteString("  USING (url);\n")
	b.WriteString("```\n\n")

	cardBox(b, []cardColumn{{
		Head: "shared_urls", Type: "int64", Right: true,
		Cells: []string{"1399167"},
	}})

	// This one says what it was measured on rather than being dropped when the
	// repo moves past it, which is what the other measured outputs do. Both of
	// its sources are still being ingested, so a count guard would mean the most
	// interesting number on this card never appears. A number that carries the
	// state it was taken in stays true after that state has changed.
	b.WriteString("That was measured while the repo held 12,858,086 `glotcc` documents and 20,941,000 `fineweb2` ones. Two corpora built by different projects from overlapping crawls, sharing 1.4 million urls out of the 33.8 million documents they had here between them. That is the number a training run pays for if nobody looks, and it is the number a dedup paper wants to explain. Both sources are still growing, so run it again rather than quoting it.\n\n")
}

// cardSourceGlob is one named source, for a query that compares two of them and
// so cannot use the one the rest of the examples run against.
func cardSourceGlob(d Dataset, source string) string {
	return fmt.Sprintf("hf://datasets/%s/%s/%s/*%s", d.Repo(), DataDir, source, ParquetExt)
}

func cardMeasuredBox(b *strings.Builder, s SourceIndex, r cardMeasured) {
	if s.Source != r.Source || s.Documents != r.Documents {
		return
	}
	cardBox(b, r.Cols)
}

// cardRow prints one document, because a schema table says what the columns are
// called and a row says what is in them.
//
// The row is real, it is in the repo, and the values are the ones it carries.
// The column list around them comes from the schema rather than from the row, so
// a column added without an example shows up here as null rather than not
// showing up at all.
func cardRow(b *strings.Builder, d Dataset) {
	b.WriteString("## One row\n\n")
	b.WriteString("A document from `glotcc`, as `SELECT * ... LIMIT 1` returns it. The byte columns are printed as hex here and come back as blobs, and the text is cut because the document is seventeen hundred characters and the point of printing a row is the shape.\n\n")
	b.WriteString("```json\n{\n")

	cols := Schema()
	for i, c := range cols {
		v, ok := cardExampleRow[c.Name]
		if !ok {
			v = cardZero(c.Type)
		}
		if c.Name == "text" && !d.Text {
			v = "null"
		}
		comma := ","
		if i == len(cols)-1 {
			comma = ""
		}
		fmt.Fprintf(b, "  %q: %s%s\n", c.Name, v, comma)
	}
	b.WriteString("}\n```\n\n")
	b.WriteString("`gao_qual`, `gao_edu`, `n_tokens`, `register` and `pii_level` are zero because the stages that fill them have not run on this repo. They are in the schema so that a query written here still runs against a release, where they are filled.\n\n")
}

// cardExampleRow is the document above, from
// data/glotcc/glotcc-9ad140b6be3a-00007-00004.parquet. The text is cut, because
// the full document is seventeen hundred characters and the point of printing a
// row is the shape rather than the prose.
var cardExampleRow = map[string]string{
	"doc_id":           `"249eac866336b8c059d9d40b0810bb925b7df9640c5f6f6a3d8b57fce13b43ba"`,
	"raw_id":           `"28ac80fee008e89826f9eb9884e6a8f914653cf2ebc34a420759270629d5aaac"`,
	"text":             `"Tập thể dục giúp nâng cao sức khỏe, cải thiện vóc dáng và giúp hình thành các cơ cho cơ thể dẻo dai hơn. Tuy nhiên không phải ai cũng có nhiều thời gian ..."`,
	"schema_version":   "1",
	"source":           `"glotcc"`,
	"source_locator":   `"v1.0/vie-Latn/vie-Latn_15.parquet:499402"`,
	"url":              `"https://www.sanchoi.cc/t/mach-ban-2-cach-chon-dung-cu-tap-the-duc-cuc-chuan/3028"`,
	"host":             `"www.sanchoi.cc"`,
	"fetched_at":       `"2024-02-24 11:02:49+07"`,
	"robots_hash":      `"0000000000000000000000000000000000000000000000000000000000000000"`,
	"dup_cluster":      `"00000000000000000000000000000000"`,
	"media_type":       `"text/html"`,
	"extractor":        `"gao-gat@1.0.0"`,
	"pipeline_version": `"0.1.0"`,
	"lang":             `"vie"`,
	"lang_score":       "0.98",
	"diacritics":       `"present"`,
	"heuristics":       `{"diacritic_ratio": 0.307, "glotcc_lid_consistency": 1.0, "glotcc_script_share": 1.0}`,
	"license_class":    `"open"`,
	"license_evidence": `"CC0"`,
	"n_chars":          "1737",
	"n_syllables":      "389",
	"upstream_fields":  `{"glotcc_sentences": "10", "glotcc_tlsh": "tlsh:T1EE572C41BC88D8...", "warc_record_id": "<urn:uuid:1216df49-7788-42c0-9aa4-7bbf0b516a1b>"}`,
}

// cardZero is what a column that the example does not carry prints as, which is
// the zero of its type rather than a blank, because a blank reads as a column
// that is missing from the file.
func cardZero(t string) string {
	switch {
	case strings.HasPrefix(t, "list<"):
		return "[]"
	case strings.HasPrefix(t, "map<"):
		return "{}"
	case t == "bool":
		return "false"
	case t == "string" || t == "binary" || strings.HasPrefix(t, "fixed"):
		return `""`
	case strings.HasPrefix(t, "int") || strings.HasPrefix(t, "uint"):
		return "0"
	case strings.HasPrefix(t, "float") || strings.HasPrefix(t, "double"):
		return "0.0"
	case strings.HasPrefix(t, "timestamp"):
		return "null"
	}
	return "null"
}

// cardCaveats is the section every card is supposed to have and most of them
// fill with a paragraph about how bias exists.
//
// The useful version names the specific things wrong with this specific corpus,
// in the order somebody is going to hit them, and says which of them are going
// to be fixed and which are the nature of the thing.
func cardCaveats(b *strings.Builder, x []Indexed) {
	b.WriteString("## Things to know before you use it\n\n")

	b.WriteString("**It is web text, so it is what the web is.** The four sources are Common Crawl and Internet Archive derivatives plus a set of PDFs. Vietnamese on the open web skews towards commerce, SEO, news aggregation and forums, and away from anything behind a login or inside an app. Gambling and affiliate marketing sites are well represented, because they are well represented on the web and no filter here removed them. A model trained on this without filtering will write like that.\n\n")

	b.WriteString("**It is not deduplicated.** Not within a source and not across sources. A page that all four corpora crawled is four documents here, and popular pages are duplicated inside a single source as well. This is on purpose and it is not a state you want to train on. Deduplicate before you do.\n\n")

	b.WriteString("**It is not quality filtered.** `gao_qual` and `gao_edu` are zero in every row, boilerplate has not been stripped beyond what each upstream corpus did, and no classifier has been run. What the upstream projects removed is removed, and they do not agree about what that is.\n\n")

	b.WriteString("**The language identification is inherited.** `lang` and `lang_score` come from whichever identifier the source used, and the sources used different ones. Documents that are mostly Vietnamese with English or Chinese passages in them are in here labeled Vietnamese, and so are some that are not Vietnamese at all.\n\n")

	b.WriteString("**Personal information has not been removed.** `pii_level`, `pii_types` and `pii_spans` are in the schema and are empty, because the stage that fills them has not run on this repo. Names, phone numbers, addresses and email addresses that were on a public page are in the text as they were on the page. If that matters for what you are building, filter before you train rather than after.\n\n")

	b.WriteString("**There is no split and no order.** The parts are the order the ingest happened to read the input files in. Nothing is shuffled, and consecutive rows in a part are frequently from the same site, so a reader taking the first N rows is taking a sample of one crawl of a handful of hosts rather than a sample of the corpus.\n\n")

	if by := BySource(x); len(by) > 0 {
		b.WriteString("**The sources are not balanced and were never meant to be.** ")
		fmt.Fprintf(b, "`%s` is %d%% of the documents here on its own. If a run should see the sources evenly, sample them evenly, because reading the repo does not.\n\n",
			by[0].Source, cardShare(by))
	}
}

// cardShare is the largest source as a percentage of the whole, rounded, which
// is the form the imbalance is worth stating in.
func cardShare(by []SourceIndex) int {
	var total int64
	for _, s := range by {
		total += s.Documents
	}
	if total == 0 {
		return 0
	}
	return int(by[0].Documents * 100 / total)
}

// cardCitation is the block a paper copies.
//
// It cites the upstream corpora as well, and the wording is not a formality: the
// text is theirs, the work of collecting it was theirs, and this repo is the
// schema and the reading. A citation that names only the aggregator is the kind
// of thing that makes projects stop publishing.
func cardCitation(b *strings.Builder, d Dataset, x []Indexed) {
	b.WriteString("## Citation\n\n")
	b.WriteString("If you use this, cite the corpora it is made of. They did the collecting. This repo did the reading.\n\n")
	if by := BySource(x); len(by) > 0 {
		for _, s := range by {
			if u, ok := cardUpstream[s.Source]; ok {
				fmt.Fprintf(b, "- %s, %s\n", u.Name, u.URL)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("For the assembly itself:\n\n")
	b.WriteString("```bibtex\n")
	fmt.Fprintf(b, "@misc{%s,\n", d.Name)
	fmt.Fprintf(b, "  title        = {%s: %s},\n", cardTitle(d), "Vietnamese corpora under one schema")
	b.WriteString("  author       = {The gao project},\n")
	fmt.Fprintf(b, "  year         = {%s},\n", CorpusYear)
	fmt.Fprintf(b, "  howpublished = {\\url{%s}},\n", d.URL())
	fmt.Fprintf(b, "  note         = {Built with gao, %s}\n", Repository)
	b.WriteString("}\n")
	b.WriteString("```\n\n")
}

// cardPython is the download and use section, kept separate from the SQL
// because the two audiences do not overlap as much as a card writer assumes.
func cardPython(b *strings.Builder, d Dataset, m *Manifest, x []Indexed) {
	by := BySource(x)
	b.WriteString("### Python\n\n")
	b.WriteString("The configs in this card's front matter are what `datasets` reads, so a source is a config name and `default` is all of them.\n\n")
	b.WriteString("```python\nfrom datasets import load_dataset\n\n")
	if m == nil && len(by) > 0 {
		small, _ := cardSmallest(by)
		fmt.Fprintf(b, "# One source, streamed rather than downloaded.\nds = load_dataset(%q, %q, split=\"train\", streaming=True)\nprint(next(iter(ds))[\"url\"])\n```\n\n", d.Repo(), small.Source)
		b.WriteString("Streaming is the right default here. The whole repo does not fit on most disks and one source of it does not fit on many, so a run that reads once should read over the network rather than land the corpus first.\n\n")
		b.WriteString("When it does need to be on disk, take a source or a single part rather than the repo.\n\n")
		b.WriteString("```python\n")
		b.WriteString("from huggingface_hub import snapshot_download\n\n")
		fmt.Fprintf(b, "# One source on disk, which for the smallest of these is %s.\nsnapshot_download(\n    %q,\n    repo_type=\"dataset\",\n    allow_patterns=\"%s/%s/*\",\n)\n",
			fleet.Size(small.Bytes), d.Repo(), DataDir, small.Source)
		b.WriteString("```\n\n")
		b.WriteString("```python\n")
		b.WriteString("import pyarrow.parquet as pq\nfrom huggingface_hub import hf_hub_download\n\n")
		b.WriteString("# One part, for looking rather than training. Read the columns you want:\n")
		b.WriteString("# text is most of the bytes and a row group of it is a couple of hundred MB.\n")
		fmt.Fprintf(b, "path = hf_hub_download(%q, %q, repo_type=\"dataset\")\n", d.Repo(), cardSmallestPath(x))
		b.WriteString("table = pq.read_table(path, columns=[\"url\", \"host\", \"lang\", \"n_syllables\"])\n")
		b.WriteString("print(table.num_rows, table.schema.names)\n")
		b.WriteString("```\n\n")
		return
	}
	fmt.Fprintf(b, "ds = load_dataset(%q, split=\"train\", streaming=True)\nprint(next(iter(ds))[\"url\"])\n```\n\n", d.Repo())
}

// cardSmallestPath is the smallest part in the repo, for the snippet that
// downloads exactly one file. It is the same file the SQL above reads, so a
// reader who runs both is looking at the same rows twice rather than at two
// unrelated samples.
func cardSmallestPath(x []Indexed) string {
	if len(x) == 0 {
		return DataDir + "/SOURCE/PART" + ParquetExt
	}
	smallest := x[0]
	for _, row := range x[1:] {
		if row.Bytes < smallest.Bytes {
			smallest = row
		}
	}
	return smallest.Path
}
