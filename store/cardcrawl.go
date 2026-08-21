package store

import (
	"fmt"
	"strings"
)

// The parts of a dataset card that belong to the two repos gao's own crawler
// writes.
//
// They are here rather than as conditionals inside the ingest card's prose
// because almost nothing the ingest card says is true of them. There are no
// four upstream corpora to compare, there is no upstream language identifier to
// inherit, and the pages arrived here rather than through somebody else's
// pipeline, which is the whole reason the repo exists.
//
// The kept repo published addresses and no text until the crawl moved to
// [doc.LicenseCrawled], and a good deal of the prose here was written to
// explain that. It is rewritten rather than patched, because a card that says
// what a repo used to withhold is a card that is describing a decision instead
// of describing data.
//
// Every number in this file was measured on a real run rather than composed to
// look plausible. The run behind the current tables read 680 pages from twelve
// Vietnamese news seeds at one request a second per host, kept 148 of them and
// wrote 1,016 rejections, and the boxes below are what DuckDB printed for the
// query above them against the parts it wrote.
//
// They are a small run and they are labeled as one. The point of putting real
// output here is not that 148 pages describe the corpus, it is that a reader
// who runs the query gets output shaped like the output shown, which is the
// thing an invented table cannot promise.

// cardWhatIsItCrawl is the section for a crawl repo.
func cardWhatIsItCrawl(b *strings.Builder, d Dataset) {
	b.WriteString("## What is it\n\n")

	if d.Reject {
		b.WriteString("This is the other half of `open-index/vitweb`: one row for every page gao's crawler fetched or tried to fetch and did not keep, with the stage that turned it away and the reason it gave.\n\n")
		b.WriteString("Most published corpora are the survivors. What was removed, and on what threshold, is a sentence in a paper if it is anywhere, so a filter that turned out to be wrong is not something anybody outside the project can find, and neither is the answer to why a particular site is missing. This repo is that answer as data. A page rejected for repetition carries the measured repetition rate and the threshold it failed, and a page robots.txt declined carries the rule that declined it.\n\n")
		b.WriteString("It doubles as the crawl's redirect map and its robots record. A `301` is a row with the status on it, a page under a `Disallow` is a row that says so, and a host that blocked the crawler is a run of rows rather than an absence.\n\n")
		b.WriteString("There is no text here. A page in this repo was fetched and then turned away, and most of the reasons for turning it away are reasons not to publish it: a page rejected for language is not Vietnamese, a page rejected for boilerplate had no article on it, and a page rejected because its site reserved its text mining rights must not be republished at all. The measurements are the whole point of the row and they are all here.\n\n")
		return
	}

	b.WriteString("Vietnamese web text on the Hub is Common Crawl, three times over. fineweb-2, GlotCC and HPLT are built from overlapping snapshots of the same crawl, so the sites Common Crawl does not reach well are missing from all three at once, and Vietnamese provincial news, forums and government publishing is a good deal of what that misses.\n\n")
	b.WriteString("This is gao crawling those sites itself. One row per page that was fetched and kept, carrying the page three ways: `text` is the article as plain prose, `markdown` is the same article with its headings, lists, tables and links intact, and `body` is the whole page as markdown before the extractor picked the article out of it. Alongside them are the URL, the host, when it was fetched, the robots rule that allowed it, the media type, and every measurement the page was judged on, including the full `heuristics` map the sift produced.\n\n")
	b.WriteString("Three columns rather than one because they fail differently. `text` is what a pretraining run wants and it is the extractor's opinion about which part of the page was the article. `markdown` is the same opinion with the structure kept, which is what a document understanding or a retrieval corpus needs and what plain text throws away. `body` is not an opinion: it is the page, and it is the column to reach for when the extractor got it wrong, which it does. A bug that cost every article on one large news site its text went unnoticed for the life of the crawl, and the only reason a rerun could fix it without asking those sites for their pages a second time is that a column like `body` exists.\n\n")
	b.WriteString("Every page that was not kept is in `open-index/vitweb-rejects` with the stage and the reason, so the shape of this repo can be argued with rather than taken on trust.\n\n")
}

// cardUsesCrawl is what somebody builds with a repo of addresses and scores.
//
// Every query here was run against the first crawl's parts. The outputs are
// small enough to print and are labeled with what they were measured on, since
// a crawl in progress moves under a card faster than anything else in this
// project.
func cardUsesCrawl(b *strings.Builder, d Dataset) {
	glob := cardAllGlob(d)

	b.WriteString("## What you can build with it\n\n")
	if d.Reject {
		b.WriteString("A rejection is a measurement plus a verdict, and the verdict is the part somebody else may want to disagree with. These are the queries that make that possible.\n\n")

		b.WriteString("### What the crawl is spending its requests on\n\n")
		b.WriteString("The stage and the reason together say where a fetch was lost, and the split between them is the first thing to look at on a run that is keeping less than it should.\n\n")
		b.WriteString("```sql\n")
		b.WriteString("SELECT reject_stage, reject_reason, count(*) AS pages\n")
		fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
		b.WriteString("GROUP BY ALL ORDER BY pages DESC;\n")
		b.WriteString("```\n\n")
		cardBox(b, []cardColumn{
			{Head: "reject_stage", Type: "varchar", Cells: []string{"crawl.fetch", "crawl.sift", "crawl.fetch", "crawl.sift", "crawl.sift", "crawl.extract", "crawl.reserve", "crawl.sift"}},
			{Head: "reject_reason", Type: "varchar", Cells: []string{"robots", "language", "fetch", "short", "repetition", "boilerplate", "robots", "boilerplate"}},
			{Head: "pages", Type: "int64", Right: true, Cells: []string{"455", "233", "104", "101", "56", "53", "7", "7"}},
		})
		b.WriteString("That is a run of 680 fetches off twelve Vietnamese news seeds: 1,016 rejections against 148 kept pages. Nearly half is `robots`, which is the crawler declining to fetch before it fetches, and the second block is the sift doing what it is for, turning away a news site's front page and its section indexes as the listings they are. The `language` rows are mostly the crawl wandering off Vietnamese sites onto their English editions and onto the wider web through outbound links.\n\n")

		b.WriteString("### What honoring a reservation costs\n\n")
		b.WriteString("The `crawl.reserve` rows are pages that were fetched, measured, and then thrown away because the page asked not to be kept. They are the most useful seven rows in the repo, because they are the only public evidence that a reservation was honored rather than announced.\n\n")
		b.WriteString("```sql\n")
		b.WriteString("SELECT host, consent, reject_detail\n")
		fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
		b.WriteString("WHERE reject_stage = 'crawl.reserve';\n")
		b.WriteString("```\n\n")
		cardBox(b, []cardColumn{
			{Head: "host", Type: "varchar", Cells: []string{"nguyenphutrong.nhandan.vn", "giamngheobenvung.vietnamnet.vn", "wikimediafoundation.org", "docs.google.com", "nhandan.vn", "vi.wikipedia.org"}},
			{Head: "consent", Type: "varchar", Cells: []string{"no-index", "no-index", "no-index", "no-index", "no-index", "no-index"}},
			{Head: "reject_detail", Type: "varchar", Cells: []string{"no-index: noindex, nofollow", "no-index: tdmrep not read, the server answered 301, noindex, nofollow, noarchive", "no-index: noindex, follow", "no-index: tdmrep not read, robots.txt disallows it, noindex, nofollow, nosnippet", "no-index: noindex, nofollow", "no-index: noindex, follow, max-image-preview:standard"}},
		})
		b.WriteString("The detail is the directives in the spelling the site used rather than our conclusion about them, so a site that thinks we read it wrong has the string to point at.\n\n")

		b.WriteString("### Refiltering without recrawling\n\n")
		b.WriteString("Every rejection carries the measurements it was judged on, so a threshold can be moved and the cost of moving it counted before anybody fetches anything again.\n\n")
		b.WriteString("```sql\n")
		b.WriteString("SELECT count(*) AS pages, round(avg(n_syllables)) AS syllables\n")
		fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
		b.WriteString("WHERE reject_reason = 'short' AND n_syllables >= 40;\n")
		b.WriteString("```\n\n")
		b.WriteString("The URLs that come back are the pages a looser bar would have kept, and they are addresses rather than text, so acting on them is a crawl rather than an edit.\n\n")

		b.WriteString("### The robots record\n\n")
		b.WriteString("A site that is absent from the kept repo is either a site nobody offered or a site that said no, and those are very different facts. This tells them apart.\n\n")
		b.WriteString("```sql\n")
		b.WriteString("SELECT host, count(*) AS declined\n")
		fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
		b.WriteString("WHERE reject_reason = 'robots'\n")
		b.WriteString("GROUP BY host ORDER BY declined DESC LIMIT 20;\n")
		b.WriteString("```\n\n")
		b.WriteString("The same query with `reject_reason = 'fetch'` is the pages that answered with something other than a 200, and `reject_detail` carries the status.\n\n")
		return
	}

	b.WriteString("The repo is Vietnamese pages, three ways, with the measurements that decided each one was worth keeping. All of it is column scans, and the three text columns are large, so name the column you want rather than selecting everything.\n\n")

	b.WriteString("### Read this before the first query\n\n")
	b.WriteString("Not every part in this repo has text in it. The crawl published addresses and measurements and no page text for the first stretch of its life, and the parts written since carry `text`, `markdown` and `body`. The older parts do not have those three columns at all rather than having them empty, so a glob across the whole repo is reading two shapes of file, and DuckDB will stop you rather than quietly hand you nulls:\n\n")
	b.WriteString("```\n")
	b.WriteString("Invalid Input Error: schema mismatch in glob: column \"text\" was read from the\n")
	b.WriteString("original file \"...\", but could not be found in file \"...\"\n")
	b.WriteString("```\n\n")
	b.WriteString("That error is the correct behavior and the fix is `union_by_name`, which lines the files up by column name and fills the missing ones with nulls. Start here, because it tells you what you actually have:\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT schema_version, license_class, count(*) AS rows, count(text) AS with_text\n")
	fmt.Fprintf(b, "FROM read_parquet('%s', union_by_name = true)\n", glob)
	b.WriteString("GROUP BY ALL ORDER BY schema_version;\n")
	b.WriteString("```\n\n")
	b.WriteString("There is no sample output under that one, on purpose. It is the query whose answer is the state of the repo on the day you run it, the split moves every time the crawl publishes, and a number printed here would be stale before you read it. Run it and believe the result rather than this page.\n\n")
	b.WriteString("`schema_version` is the filter, and it is a column rather than a filename convention for exactly this reason. A version 1 row is a page recorded before the text columns existed, under the earlier posture that published the address and withheld the page, which is why it still reads `restricted`. A version 2 row carries the page and reads `crawled`. Every other column means the same thing in both, so a query about hosts or robots decisions or measurements should read the whole repo, and a query that wants text should carry `union_by_name = true` and say `WHERE schema_version >= 2`.\n\n")
	b.WriteString("None of this is being backfilled. A version 1 row cannot be upgraded without fetching the page again, since the text was never stored, and rewriting the class on a row whose page we do not have would be a claim about material we are not holding. Those rows stay as they are and the crawl moves forward.\n\n")
	b.WriteString("The text queries below all carry the flag and the filter, so they run against the repo as it stands.\n\n")
	b.WriteString("One thing about the output boxes: they are real output, and they are from a small run rather than from this repo. 680 fetches off twelve Vietnamese news seeds, 148 pages kept. The point is not that 148 pages describe the corpus. It is that a reader who runs the query gets output shaped like the output shown, which is the thing an invented table cannot promise.\n\n")

	b.WriteString("### Pretraining text\n\n")
	b.WriteString("The plain reading. Every row passed the language test, the length test and the repetition test, so the filter here is about what you want rather than about what is usable.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT text\n")
	fmt.Fprintf(b, "FROM read_parquet('%s', union_by_name = true)\n", glob)
	b.WriteString("WHERE schema_version >= 2\n")
	b.WriteString("  AND n_syllables >= 400 AND lang_score >= 0.9 AND diacritics = 'present';\n")
	b.WriteString("```\n\n")
	b.WriteString("`diacritics` is worth a thought rather than a default. Vietnamese written without tone marks is still Vietnamese and there is a good deal of it on forums and in comments, so `present` is the clean slice and dropping the filter is the realistic one.\n\n")

	b.WriteString("### The same pages with their structure\n\n")
	b.WriteString("`markdown` is the article with its headings, lists, tables and links, which is what a retrieval corpus, a document understanding set or an instruction mining run needs and what plain text has already thrown away.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT count(*) AS pages,\n")
	b.WriteString("       count(*) FILTER (markdown LIKE '%](%')  AS with_links,\n")
	b.WriteString("       count(*) FILTER (markdown LIKE '%## %')  AS with_headings,\n")
	b.WriteString("       count(*) FILTER (markdown LIKE '%|%|%')  AS with_tables\n")
	fmt.Fprintf(b, "FROM read_parquet('%s', union_by_name = true)\n", glob)
	b.WriteString("WHERE schema_version >= 2;\n")
	b.WriteString("```\n\n")
	cardBox(b, []cardColumn{
		{Head: "pages", Type: "int64", Right: true, Cells: []string{"148"}},
		{Head: "with_links", Type: "int64", Right: true, Cells: []string{"145"}},
		{Head: "with_headings", Type: "int64", Right: true, Cells: []string{"110"}},
		{Head: "with_tables", Type: "int64", Right: true, Cells: []string{"4"}},
	})
	b.WriteString("Links are on nearly every page and tables are on almost none, which is what Vietnamese news is. Links are rewritten to absolute URLs against the page they were found on, so a link in `markdown` is a link you can follow rather than a fragment that only meant something inside the original document.\n\n")

	b.WriteString("### How much of a page the extractor threw away\n\n")
	b.WriteString("The three columns side by side are the extraction, measured. `body` is the whole page, `text` is what the extractor decided was the article, and the ratio between them is how aggressive it was on that host.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT host, count(*) AS pages,\n")
	b.WriteString("       round(avg(length(text)))     AS text_chars,\n")
	b.WriteString("       round(avg(length(markdown))) AS markdown_chars,\n")
	b.WriteString("       round(avg(length(body)))     AS body_chars\n")
	fmt.Fprintf(b, "FROM read_parquet('%s', union_by_name = true)\n", glob)
	b.WriteString("WHERE schema_version >= 2\n")
	b.WriteString("GROUP BY host ORDER BY pages DESC LIMIT 6;\n")
	b.WriteString("```\n\n")
	cardBox(b, []cardColumn{
		{Head: "host", Type: "varchar", Cells: []string{"vtv.vn", "vietnamnet.vn", "tuoitre.vn", "websosanh.vn", "nhandan.vn", "vnexpress.net"}},
		{Head: "pages", Type: "int64", Right: true, Cells: []string{"18", "16", "16", "13", "12", "8"}},
		{Head: "text_chars", Type: "double", Right: true, Cells: []string{"2744.0", "4686.0", "4221.0", "8899.0", "4713.0", "6442.0"}},
		{Head: "markdown_chars", Type: "double", Right: true, Cells: []string{"5703.0", "7253.0", "8894.0", "16854.0", "9088.0", "7336.0"}},
		{Head: "body_chars", Type: "double", Right: true, Cells: []string{"14250.0", "24240.0", "28953.0", "24976.0", "30278.0", "10012.0"}},
	})
	b.WriteString("A page runs from under twice its article to nearly seven times it, and the difference is the boilerplate the `text` column is for. How much of it there is turns out to be a fact about the site rather than about the crawl: `tuoitre.vn` carries about seven times, `vnexpress.net` about one and a half.\n\n")
	b.WriteString("The number to watch is a host whose `text_chars` is near zero while its `body_chars` is normal, because that is an extraction failure on that host rather than a site that writes short. It is the query we did not run for a long time, and one large news site was returning two byte articles the whole while.\n\n")
	b.WriteString("`markdown` runs longer than `text` on every host here, since the link targets and the table pipes are characters too. It is not a different article, it is the same one with its shape left in.\n\n")

	if rejects, ok := cardRejectGlob(d); ok {
		b.WriteString("### Which Vietnamese sites are worth crawling\n\n")
		b.WriteString("A host's yield is the thing every crawler wants and nobody publishes: of the pages we asked a site for, how many turned out to be worth keeping. This repo has the numerator and the rejects repo has the denominator, and the two together are the whole outcome of every request.\n\n")
		b.WriteString("```sql\n")
		fmt.Fprintf(b, "WITH kept AS (SELECT host, count(*) n FROM read_parquet('%s', union_by_name = true)\n", glob)
		b.WriteString("              WHERE schema_version >= 2 GROUP BY 1),\n")
		fmt.Fprintf(b, "     rej  AS (SELECT host, count(*) n FROM read_parquet('%s') GROUP BY 1)\n", rejects)
		b.WriteString("SELECT coalesce(kept.host, rej.host) AS host,\n")
		b.WriteString("       coalesce(kept.n, 0) AS kept, coalesce(rej.n, 0) AS rejected,\n")
		b.WriteString("       round(coalesce(kept.n, 0) * 100.0 /\n")
		b.WriteString("             (coalesce(kept.n, 0) + coalesce(rej.n, 0)), 1) AS keep_pct\n")
		b.WriteString("FROM kept FULL JOIN rej ON kept.host = rej.host\n")
		b.WriteString("ORDER BY kept DESC LIMIT 8;\n")
		b.WriteString("```\n\n")
		cardBox(b, []cardColumn{
			{Head: "host", Type: "varchar", Cells: []string{"vtv.vn", "tuoitre.vn", "vietnamnet.vn", "websosanh.vn", "nhandan.vn", "vnexpress.net", "tinnhiemmang.vn", "radio.nhandan.vn"}},
			{Head: "kept", Type: "int64", Right: true, Cells: []string{"18", "16", "16", "13", "12", "8", "8", "8"}},
			{Head: "rejected", Type: "int64", Right: true, Cells: []string{"5", "4", "7", "0", "19", "11", "7", "3"}},
			{Head: "keep_pct", Type: "double", Right: true, Cells: []string{"78.3", "80.0", "69.6", "100.0", "38.7", "42.1", "53.3", "72.7"}},
		})
		b.WriteString("The spread is the useful part. `tuoitre.vn` returns four articles for every five requests and `nhandan.vn` returns two for every five, which is a real difference in what a crawler should spend on them, and neither number is guessable from the outside. A host at or near a hundred percent usually means we have barely touched it rather than that it is perfect, so read the `kept` column alongside the rate.\n\n")
		b.WriteString("A `FULL JOIN` rather than an inner one because the interesting hosts are the lopsided ones: a site that is all rejections never appears in this repo at all, and a site that is all keeps has nothing on the other side.\n\n")
	}

	b.WriteString("### Watching the crawl move\n\n")
	b.WriteString("`fetched_at` is on every row in both schemas, so this one needs no flag and no filter and reads the whole repo including the parts that predate the text columns.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT date_trunc('day', fetched_at) AS day, count(*) AS kept\n")
	fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
	b.WriteString("GROUP BY day ORDER BY day;\n")
	b.WriteString("```\n\n")
	b.WriteString("`url_template` is the other axis worth grouping on. It is the URL with its variable parts replaced, which is what the crawl budgets against, and grouping by it rather than by host is how a calendar trap or a faceted catalog shows up as one template with thousands of rows.\n\n")
}

// cardCaveatsCrawl is what a reader has to know before believing any of it.
func cardCaveatsCrawl(b *strings.Builder, d Dataset) {
	b.WriteString("## Things to know before you use it\n\n")

	if d.Reject {
		b.WriteString("**There is no text in this repo.** `text`, `markdown` and `body` are null in every row, because a page that was turned away is a page we decided not to publish and writing it into the rejects repo would be publishing it through the back door. What is here is every measurement the page was judged on, which is what makes the judgment arguable.\n\n")
	} else {
		b.WriteString("**The three text columns are not three copies of the same thing.** `text` is the extractor's article as plain prose, `markdown` is that article with its structure, and `body` is the whole page before the extractor chose. `body` includes the navigation, the footer, the related links and the cookie notice, so it is the wrong column to train on and the right one to re-extract from. A run that reads `body` and means `text` will produce a model that writes menus.\n\n")

		b.WriteString("**The extraction is a heuristic and it has been wrong at scale.** It reads the page's containers, discards the ones named for furniture, and measures the ones named for position before deciding, because a container called `sidebar` sometimes holds the article. That rule exists because the previous one did not have it and every article on one large Vietnamese news site came back as two bytes for the life of a crawl. `n_chars` is on every row, so a host whose pages all came back short is a `GROUP BY host` away and worth checking before trusting that host's rows.\n\n")

		b.WriteString("**Nothing here is deduplicated.** Not within a run and not across them. Vietnamese news is syndicated heavily, so the same wire story is here under several hosts with different boilerplate around it, and `doc_id` is a hash of the normalized text, which means those copies are only equal if the extraction agreed byte for byte. It usually does not. Deduplicate before you train, and expect near duplicate detection to find a good deal more than exact matching does.\n\n")

		b.WriteString("**Personal information has not been removed.** `pii_level` is `0` and `pii_types` and `pii_spans` are empty on every row, because the stage that fills them has not run on this repo. That is not a finding that the pages are clean, it is the absence of a measurement. Vietnamese news carries bylines, quoted names, phone numbers in classified sections and email addresses in author blocks, and all of it is in `text`, `markdown` and `body` exactly as it was on the page. Filter before you train rather than after.\n\n")

		b.WriteString("**Not every part has text.** The parts written before the text columns existed do not carry `text`, `markdown` or `body` at all, and they read `schema_version = 1` and `license_class = 'restricted'` because they were published under the earlier posture that gave out the address and withheld the page. A query that names a text column across the whole repo needs `union_by_name = true` or DuckDB will stop it, and `WHERE schema_version >= 2` is the filter that means the pages. None of it is being backfilled, since the text of those pages was never stored and refetching them is a crawl rather than an edit.\n\n")
	}

	b.WriteString("**robots.txt is honored and so is tdmrep.json.** A page under a `Disallow` is not fetched, and a page whose site reserved its text mining rights is fetched, measured and then rejected rather than kept. Both of those are rows in `open-index/vitweb-rejects` with the rule on them. It means the corpus is smaller than the web and that the difference is auditable.\n\n")

	b.WriteString("**The measurements are gao's own and they are not a quality judgment.** `lang_score` is the share of tokens that are Vietnamese syllables, `heuristics` is the full map the sift produced, and `gao_qual` and `gao_edu` are zero because the classifier behind them is trained against a reference set that does not exist yet. A page is here because it is Vietnamese prose of some length, not because it is good.\n\n")

	b.WriteString("**The repetition filter removes party and government prose, and that is a known fault rather than a finding about the writing.** On the first run a third of the repetition rejections were article pages rather than listing pages. Two of them were pulled out and read: the extraction was clean, with no navigation and no related stories in it, and what pushed them over the threshold was the register. An official is named in full every time they are mentioned, so `đồng chí Vũ Quyết Tiến, Phó Bí thư Tỉnh ủy, Chủ tịch Ủy ban MTTQ tỉnh` is three occurrences of the same eight syllable gram in a nine hundred word article, and the body doing the meeting is named in full in every paragraph about it. The threshold those pages fail is Gopher's, scaled from words to syllables, and a Vietnamese title is long in syllables and carries one fact. Every page it removed is in `open-index/vitweb-rejects` with its measured rate, so the threshold can be moved and the corpus recomposed without fetching anything again.\n\n")

	b.WriteString("**It is a crawl in progress and the parts are the order it happened in.** Nothing is shuffled and nothing is deduplicated across runs. Consecutive rows are frequently from one host, because a crawl of a host is a run of requests to that host, so the first N rows are a sample of one site rather than of the corpus.\n\n")

	b.WriteString("**A crawl is not a snapshot of the web.** It is a snapshot of what a seed list plus link following reached, under a budget that closes a template once it stops producing new text. Sites nobody linked to and nobody seeded are absent, and absence here is not evidence of anything.\n\n")
}

// cardFieldsCrawl is the three columns a rejects repo carries that no other repo
// does. They are in the schema of that repo only, so they are described where
// they exist rather than in the shared column table.
func cardFieldsCrawl(b *strings.Builder) {
	b.WriteString("Three columns more than every other repo, because a rejection is a row about a decision:\n\n")
	b.WriteString("| column | type | meaning |\n| --- | --- | --- |\n")
	b.WriteString("| `reject_stage` | `varchar` | which part of the crawl turned the page away: `crawl.fetch`, `crawl.extract`, `crawl.reserve`, `crawl.sift` or `crawl.contract` |\n")
	b.WriteString("| `reject_reason` | `varchar` | the reason in one word, from a fixed list: `robots`, `fetch`, `boilerplate`, `language`, `short`, `repetition`, `reserved`, `contract` |\n")
	b.WriteString("| `reject_detail` | `varchar` | the reason as a sentence with the numbers in it, such as `0.21 of the text is repeated 8 syllable grams, over 0.15` |\n")
	b.WriteString("\nThe first two are dictionary encoded and cost almost nothing to group by. The detail is free text and is the column to read once the grouping has said where to look.\n\n")
}
