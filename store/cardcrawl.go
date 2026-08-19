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
// four upstream corpora to compare, there is no text to filter, and the
// interesting thing about the repo is not what it holds but what it does not:
// the text stayed on the box that fetched it, and every page that was turned
// away is in the other repo with the reason on it.
//
// Every number in this file was measured on a real run. The first crawl off
// server1 read 412 pages from twenty Vietnamese news sites at one request a
// second per host, kept 151 of them and wrote 299 rejections, and the tables
// below are what came back from querying the parts it wrote.

// cardWhatIsItCrawl is the section for a crawl repo.
func cardWhatIsItCrawl(b *strings.Builder, d Dataset) {
	b.WriteString("## What is it\n\n")

	if d.Reject {
		b.WriteString("This is the other half of `open-index/vitweb`: one row for every page gao's crawler fetched or tried to fetch and did not keep, with the stage that turned it away and the reason it gave.\n\n")
		b.WriteString("Most published corpora are the survivors. What was removed, and on what threshold, is a sentence in a paper if it is anywhere, so a filter that turned out to be wrong is not something anybody outside the project can find, and neither is the answer to why a particular site is missing. This repo is that answer as data. A page rejected for repetition carries the measured repetition rate and the threshold it failed, and a page robots.txt declined carries the rule that declined it.\n\n")
		b.WriteString("It doubles as the crawl's redirect map and its robots record. A `301` is a row with the status on it, a page under a `Disallow` is a row that says so, and a host that blocked the crawler is a run of rows rather than an absence.\n\n")
		b.WriteString("There is no text here, for the same reason there is none in the kept repo. Failing a filter does not change what a page's license lets anybody publish.\n\n")
		return
	}

	b.WriteString("Vietnamese web text on the Hub is Common Crawl, three times over. fineweb-2, GlotCC and HPLT are built from overlapping snapshots of the same crawl, so the sites Common Crawl does not reach well are missing from all three at once, and Vietnamese provincial news, forums and government publishing is a good deal of what that misses.\n\n")
	b.WriteString("This is gao crawling those sites itself. One row per page that was fetched and kept: the URL, the host, when it was fetched, the robots rule that allowed it, the media type, and every measurement the page was judged on, including the full `heuristics` map the sift produced.\n\n")
	b.WriteString("What is not here is the text, and that is the design rather than an omission. A page on the open web carries no grant to redistribute it, so the crawl publishes the address and the measurements and keeps the bytes on the box that fetched them. That is a whole artifact: with the URL and the scores, somebody can fetch the same pages under their own lawful access and rebuild the same corpus, and they can do it knowing which pages are worth the request before making it.\n\n")
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
			{Head: "reject_stage", Type: "varchar", Cells: []string{"crawl.sift", "crawl.sift", "crawl.extract", "crawl.fetch", "crawl.sift", "crawl.fetch", "crawl.reserve", "crawl.sift"}},
			{Head: "reject_reason", Type: "varchar", Cells: []string{"repetition", "short", "boilerplate", "robots", "language", "fetch", "robots", "boilerplate"}},
			{Head: "pages", Type: "int64", Right: true, Cells: []string{"128", "74", "35", "34", "19", "7", "1", "1"}},
		})
		b.WriteString("That is the first run, 299 rejections against 151 kept pages. Two thirds of it is the sift, which is the crawler doing what it is for: a news site's front page and its section indexes are fetched, measured, and turned away as the listings they are.\n\n")

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

	b.WriteString("The repo is addresses and measurements, so what it is good for is deciding which pages are worth fetching, and measuring a crawl while it runs. Both are column scans.\n\n")

	b.WriteString("### A fetch list somebody else can act on\n\n")
	b.WriteString("The rows are pages that were fetched once and passed every gate, so the URLs are a list of Vietnamese prose pages that are known to exist, known to be allowed by robots.txt, and known to be long enough to be worth a request.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT url, host, n_syllables, lang_score\n")
	fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
	b.WriteString("WHERE n_syllables >= 400 AND lang_score >= 0.9 AND diacritics = 'present'\n")
	b.WriteString("ORDER BY n_syllables DESC;\n")
	b.WriteString("```\n\n")
	b.WriteString("Fetching them yourself is what makes the text yours to use. `fetched_at` says how old our reading of each page is, and `robots_decision` says which rule allowed it at that time rather than now.\n\n")

	b.WriteString("### Which Vietnamese sites are worth crawling\n\n")
	b.WriteString("A host's yield is the thing every crawler wants and nobody publishes: how many pages of real prose come back per site, and how long they are.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT host, count(*) AS pages, round(avg(n_syllables)) AS syllables\n")
	fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
	b.WriteString("GROUP BY host ORDER BY pages DESC LIMIT 5;\n")
	b.WriteString("```\n\n")
	cardBox(b, []cardColumn{
		{Head: "host", Type: "varchar", Cells: []string{"baoquangninh.vn", "baolamdong.vn", "tuoitre.vn", "vtv.vn", "radio.nhandan.vn"}},
		{Head: "pages", Type: "int64", Right: true, Cells: []string{"82", "21", "7", "6", "5"}},
		{Head: "syllables", Type: "double", Right: true, Cells: []string{"1153.0", "1364.0", "1147.0", "660.0", "206.0"}},
	})
	b.WriteString("Two provincial papers ahead of the national ones, on a seed list that had both. That is the gap this crawl exists to fill, and it shows up in the first four hundred pages.\n\n")

	b.WriteString("### Measuring a crawl rather than describing one\n\n")
	b.WriteString("Joining this repo to `open-index/vitweb-rejects` on `url` gives the whole outcome of every request, which is what a yield curve is made of.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT date_trunc('day', fetched_at) AS day, count(*) AS kept\n")
	fmt.Fprintf(b, "FROM read_parquet('%s')\n", glob)
	b.WriteString("GROUP BY day ORDER BY day;\n")
	b.WriteString("```\n\n")
	b.WriteString("`url_template` is the URL with its variable parts replaced, which is what the crawl budgets against. Grouping by it rather than by host is how a calendar trap or a faceted catalog shows up as one template with thousands of rows.\n\n")
}

// cardCaveatsCrawl is what a reader has to know before believing any of it.
func cardCaveatsCrawl(b *strings.Builder) {
	b.WriteString("## Things to know before you use it\n\n")

	b.WriteString("**There is no text and there will not be.** `text` is null in every row. The bytes each row was measured from are in a WARC on the machine that fetched them, they are not published, and no reading of `source_locator` will get anybody to them. What the locator is for is us: it is how a page can be extracted again by a later extractor without asking the site a second time.\n\n")

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
