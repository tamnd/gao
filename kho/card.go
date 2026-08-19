package kho

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tamnd/gao/luat"
	"github.com/tamnd/gao/may"
)

// The dataset card, generated rather than written.
//
// A card written by hand describes the release before last. It says 40 billion
// tokens because that was true in March, it lists four sources because a fifth
// was added after somebody last opened the file, and there is no way to tell by
// looking at it which of its numbers have gone stale. Every number here comes
// out of the manifest that sealed the snapshot, so a card that disagrees with
// the data is a bug rather than an oversight, and regenerating it is the same
// command every time rather than an afternoon.
//
// The parts a person actually needs are the ones a generated card is good at:
// what is in the repo, what is not in it and why, how many documents and tokens
// there are, which stages produced them at which versions, and the one line that
// reads the thing without downloading it. The parts a generated card is bad at,
// the argument for why the corpus is built this way, live in the repository and
// are linked rather than restated.

// CardName is the file a dataset card lives in on the Hub.
const CardName = "README.md"

// Repository is where the card sends a reader who wants the argument rather
// than the numbers.
const Repository = "https://github.com/tamnd/gao"

// Language is the corpus language as the Hub's metadata wants it.
const Language = "vi"

// LangValue is the same language as the lang column spells it.
//
// The two are not the same string and that is the trap. The Hub's front matter
// takes ISO 639-1 and the column carries ISO 639-3, so a snippet that filters on
// the front matter's spelling comes back with nothing at all and reads as a repo
// that holds no Vietnamese. It is named here so that the card cannot get it
// wrong twice.
const LangValue = "vie"

// Card renders the dataset card for a repo.
//
// A nil manifest is the repo before its first release: a working repo, which
// never gets one, and a published repo between being created and being filled.
// That card says so rather than printing zeros, because a card full of zeros
// reads as a snapshot that found nothing.
//
// The index is what a working repo has instead of a manifest. It is one row per
// part with the part's document count in it, so a repo that will never be sealed
// can still say what is in it, and a card generated with one is a card with real
// numbers on it rather than a promise that there will be numbers later.
func Card(d Dataset, m *Manifest, x []Indexed) string {
	var b strings.Builder
	cardFrontMatter(&b, d, m, x)
	cardBody(&b, d, m, x)
	return b.String()
}

// cardFrontMatter writes the YAML block the Hub reads. It is written by hand rather
// than marshaled because the Hub cares about the order of nothing in it and a
// person reading the file cares a great deal, and because a YAML dependency for
// twenty lines of output is a dependency to explain at every upgrade.
func cardFrontMatter(b *strings.Builder, d Dataset, m *Manifest, x []Indexed) {
	b.WriteString("---\n")
	fmt.Fprintf(b, "pretty_name: %s\n", cardPretty(d.Name))
	b.WriteString("language:\n  - " + Language + "\n")

	// Not an SPDX identifier, because there is no single license here to name.
	// Every document carries its own class in the license_class column, and a
	// repo tagged cc-by-4.0 because most of it is would be telling a reader
	// something false about the rest.
	b.WriteString("license: other\n")
	fmt.Fprintf(b, "license_name: %s\n", cardLicenseName(d))
	fmt.Fprintf(b, "license_link: %s/blob/main/luat/posture.go\n", Repository)

	if n := cardDocuments(m, x); n > 0 {
		fmt.Fprintf(b, "size_categories:\n  - %s\n", cardSize(n))
	}
	if d.Text {
		b.WriteString("task_categories:\n  - text-generation\n  - fill-mask\n")
	}
	b.WriteString("tags:\n  - vietnamese\n  - pretraining\n  - parquet\n")
	if !d.Text {
		b.WriteString("  - metadata-only\n")
	}
	cardConfigs(b, m, x)
	b.WriteString("---\n\n")
}

// cardConfigs writes the configs block, which is the whole of what makes the
// Hub's dataset viewer work.
//
// Without it the repo's front page is a file listing and the viewer says there
// is no data, which is a repo of a quarter of a terabyte reading as an empty
// one. A published repo has one config, the snapshot. A working repo has one
// per source, because a source is the unit somebody wants: a reader after
// GlotCC does not want to be handed HPLT with it, and a config is the name they
// get to write in a load_dataset call.
func cardConfigs(b *strings.Builder, m *Manifest, x []Indexed) {
	if m != nil {
		b.WriteString("configs:\n  - config_name: default\n    data_files:\n")
		fmt.Fprintf(b, "      - split: train\n        path: %s/*%s\n", SnapshotDir(m.Snapshot), ParquetExt)
		return
	}
	by := BySource(x)
	if len(by) == 0 {
		return
	}

	// The default config is every source at once, and it is first because the
	// viewer opens whichever config is first and a viewer that opens on one
	// source of four is a viewer saying the repo holds a quarter of what it does.
	b.WriteString("configs:\n  - config_name: default\n    data_files:\n")
	fmt.Fprintf(b, "      - split: train\n        path: %s/*/*%s\n", DataDir, ParquetExt)
	for _, s := range by {
		fmt.Fprintf(b, "  - config_name: %s\n    data_files:\n", s.Source)
		fmt.Fprintf(b, "      - split: train\n        path: %s/%s/*%s\n", DataDir, s.Source, ParquetExt)
	}
}

// cardDocuments is what the repo holds, from whichever of the two things that
// know is present.
func cardDocuments(m *Manifest, x []Indexed) int64 {
	if m != nil {
		return m.Counts.Documents
	}
	n, _ := Total(x)
	return n
}

func cardBody(b *strings.Builder, d Dataset, m *Manifest, x []Indexed) {
	fmt.Fprintf(b, "# %s\n\n", cardPretty(d.Name))
	fmt.Fprintf(b, "> %s\n\n", cardTagline(d, m, x))
	fmt.Fprintf(b, "This dataset is %s.\n\n", d.Holds)
	cardContents(b, d, m, x)

	switch {
	case m != nil:
		cardSnapshot(b, m)
		cardCounts(b, m)
		cardStages(b, m)
	case d.Tier == Working:
		cardParts(b, x)
	default:
		cardUnsealed(b)
	}
	cardLayout(b, d, m, x)
	cardReading(b, d, m, x)
	cardFields(b, d)
	cardShipping(b, d)
	if m == nil && d.Tier == Working {
		cardNotARelease(b)
	}

	b.WriteString("## Where this comes from\n\n")
	fmt.Fprintf(b, "The pipeline that built it, the ingest contract every document had to pass, and the reasoning behind both are at %s.\n\n", Repository)
	fmt.Fprintf(b, "This card is generated by `gao kho card`, from the snapshot manifest where there is one and from `%s` where there is not. Editing it by hand works until the next run overwrites it.\n", IndexName)
}

// cardTagline is the one line under the title, which is the only line a lot of
// readers get to before deciding whether to keep going.
func cardTagline(d Dataset, m *Manifest, x []Indexed) string {
	n := cardDocuments(m, x)
	if n == 0 {
		return "Vietnamese text under one schema, nothing sealed here yet"
	}
	var size int64
	if m != nil {
		size = m.Counts.Bytes
	} else {
		_, size = Total(x)
	}
	what := "Vietnamese documents"
	if !d.Text {
		what = "Vietnamese documents, metadata only"
	}
	if by := BySource(x); len(by) > 0 {
		corpora := fmt.Sprintf("%d public corpora", len(by))
		if len(by) == 1 {
			corpora = "one public corpus"
		}
		return fmt.Sprintf("%s %s from %s, %s of Parquet, one schema",
			cardCommas(n), what, corpora, may.Size(size))
	}
	return fmt.Sprintf("%s %s, %s of Parquet, one schema", cardCommas(n), what, may.Size(size))
}

// cardContents is the table of contents, which a card of this length needs and
// which the Hub renders as links.
func cardContents(b *strings.Builder, d Dataset, m *Manifest, x []Indexed) {
	b.WriteString("## Contents\n\n")
	items := []string{"[What is in it](#what-is-in-it)"}
	if m != nil {
		items = append(items, "[This snapshot](#this-snapshot)")
		if len(m.Stages) > 0 {
			items = append(items, "[How it was produced](#how-it-was-produced)")
		}
	}
	items = append(items,
		"[How it is laid out](#how-it-is-laid-out)",
		"[Reading it](#reading-it)",
		"[The columns](#the-columns)",
		"[What ships and what does not](#what-ships-and-what-does-not)",
	)
	if m == nil && d.Tier == Working {
		items = append(items, "[What this is not](#what-this-is-not)")
	}
	items = append(items, "[Where this comes from](#where-this-comes-from)")
	for _, i := range items {
		fmt.Fprintf(b, "- %s\n", i)
	}
	b.WriteString("\n")
}

// cardParts is what a working repo has instead of a counts table: the index,
// summed per source.
func cardParts(b *strings.Builder, x []Indexed) {
	b.WriteString("## What is in it\n\n")
	by := BySource(x)
	if len(by) == 0 {
		b.WriteString("Nothing yet. The repo was created ahead of the first ingest, and this section fills in from the parts index once there are parts to index.\n\n")
		return
	}

	documents, bytes := Total(x)
	b.WriteString("| source | documents | parts | input files | size | pinned at |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | --- |\n")
	for _, s := range by {
		fmt.Fprintf(b, "| `%s` | %s | %d | %d | %s | `%s` |\n",
			s.Source, cardCommas(s.Documents), s.Parts, s.Files, may.Size(s.Bytes),
			strings.Join(s.Snapshots, "`, `"))
	}
	fmt.Fprintf(b, "| **total** | **%s** | **%d** | | **%s** | |\n\n",
		cardCommas(documents), len(x), may.Size(bytes))

	b.WriteString("Every count here is the row count in a part's own Parquet footer, added up. None of it is what a run reported writing, because a run that died between pushing a part and writing down that it had is exactly the case a count has to be right about.\n\n")
	fmt.Fprintf(b, "The per part version of this table is `%s` at the root of the repo, which is one row per file with its source, its snapshot, the input file it came from, its document count and its size. It is a CSV so that it can be read without a Parquet reader, and it is small enough to open in anything.\n\n", IndexName)

	if repinned := cardRepinned(by); repinned != "" {
		fmt.Fprintf(b, "Note that %s. Both revisions are in the repo and every document in them is counted twice above. Filter to one revision by its file name prefix until the old parts are swept.\n\n", repinned)
	}
	fmt.Fprintf(b, "The repo grows while ingests run, so these numbers are the ones from the last time `gao kho index` was run against it rather than a sealed total. The counts in `%s` and the counts here always agree, because they are generated together.\n\n", IndexName)
}

// cardRepinned names any source sitting in the repo at two revisions, which is
// the one state where the totals above are wrong in a way a reader cannot see.
func cardRepinned(by []SourceIndex) string {
	var names []string
	for _, s := range by {
		if len(s.Snapshots) > 1 {
			names = append(names, "`"+s.Source+"`")
		}
	}
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, ", ") + " " + cardIs(len(names)) + " here at more than one revision"
}

func cardIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// cardUnsealed is what a published repo says between being created and having
// its first snapshot sealed into it.
//
// It is not the working repo's section. A working repo has an index and can put
// real counts on the card, and it is never going to be sealed. This one is a
// release repo that has not had its release yet, so what it owes a reader is the
// reason there are no counts and the warning not to take any off the files that
// are there.
func cardUnsealed(b *strings.Builder) {
	b.WriteString("## What is in it\n\n")
	b.WriteString("No snapshot has been sealed here yet, so there are no counts on this card and there is no signed manifest behind it.\n\n")
	b.WriteString("That does not mean the repo is empty. A stage pushes each part as it closes it and deletes the local copy, so what is under `data/` is whatever has been written so far. Those parts are real documents under the schema below and they are not a release: a part can be rewritten when a source is pinned again, and the file list is whatever the last run got through rather than a set anybody has fixed. Read them to see what the pipeline produces. Do not cite a count off them.\n\n")
	b.WriteString("The card is regenerated from the manifest each time a snapshot is sealed, so when there is a snapshot this is where its counts will be.\n\n")
}

// cardNotARelease is the caveat a working repo has to carry, kept to its own
// section rather than folded into the counts, because somebody who reads only
// one section of this card should not be able to miss it.
func cardNotARelease(b *strings.Builder) {
	b.WriteString("## What this is not\n\n")
	b.WriteString("This is not a release. There is no signed manifest behind it, no merkle root over the files, and no promise that a part will still be there next week under the same name. An ingest pushes each part as it closes it and deletes the local copy, which is what lets a box with a terabyte of disk work through corpora that do not fit on it, and it means the file list is whatever the last run got through rather than a set anybody has fixed.\n\n")
	b.WriteString("What that changes for a reader:\n\n")
	b.WriteString("- Read it to see what the pipeline produces, and to build on the raw text under one schema without pulling four corpora in four formats.\n")
	b.WriteString("- Do not cite a document count off it in anything that has to still be true later. Cite a release.\n")
	b.WriteString("- Re-pinning a source rewrites its parts under a new revision in the same directory, so a query that has to be stable should name a revision rather than a source.\n")
	b.WriteString("- Nothing here has been deduplicated against anything else here. The same page can be in three of these corpora and it is three documents in this repo.\n\n")
	fmt.Fprintf(b, "The releases carry the signed manifest, the dedup, and the quality filtering. They are the other repos in %s.\n\n", "[open-index](https://huggingface.co/open-index)")
}

func cardSnapshot(b *strings.Builder, m *Manifest) {
	b.WriteString("## This snapshot\n\n")
	b.WriteString("| | |\n| --- | --- |\n")
	fmt.Fprintf(b, "| snapshot | `%s` |\n", m.Snapshot)
	if m.Parent != "" {
		fmt.Fprintf(b, "| parent | `%s` |\n", m.Parent)
	}
	fmt.Fprintf(b, "| sealed | %s |\n", m.CreatedAt.UTC().Format("2006-01-02"))
	fmt.Fprintf(b, "| pipeline | %s |\n", m.Pipeline)
	if m.Box != "" {
		fmt.Fprintf(b, "| built on | %s |\n", m.Box)
	}
	fmt.Fprintf(b, "| files | %d |\n", len(m.Shards))
	fmt.Fprintf(b, "| merkle root | `%s` |\n", m.Root)
	fmt.Fprintf(b, "| signature | %s |\n", cardSigned(m))
	b.WriteString("\n")
	b.WriteString("The root commits to every file in the snapshot, and `gao kho verify` checks the files against it and the root against the signature. A snapshot is immutable: a document removed after this one was sealed is a tombstone in a later snapshot rather than an edit to this one.\n\n")
}

func cardSigned(m *Manifest) string {
	if m.Signature.Value == "" {
		return "unsigned, which means this is not a release"
	}
	return "`" + cardShort(m.Signature.PublicKey) + "` on " + m.Signature.SignedAt.UTC().Format("2006-01-02")
}

func cardCounts(b *strings.Builder, m *Manifest) {
	c := m.Counts
	b.WriteString("## What is in it\n\n")
	b.WriteString("| | |\n| --- | --- |\n")
	fmt.Fprintf(b, "| documents | %d |\n", c.Documents)
	if c.Synthetic > 0 {
		fmt.Fprintf(b, "| natural | %d |\n", c.Natural)
		fmt.Fprintf(b, "| synthetic | %d |\n", c.Synthetic)
	}
	fmt.Fprintf(b, "| characters | %d |\n", c.Chars)
	fmt.Fprintf(b, "| syllables | %d |\n", c.Syllables)
	if c.Tokens > 0 {
		fmt.Fprintf(b, "| tokens | %d, counted with %s |\n", c.Tokens, c.Tokenizer)
	}
	fmt.Fprintf(b, "| size | %s |\n", may.Size(c.Bytes))
	if c.Rejected > 0 {
		fmt.Fprintf(b, "| rejected while building | %d |\n", c.Rejected)
	}
	b.WriteString("\n")

	if c.Synthetic > 0 {
		b.WriteString("Synthetic documents are counted and reported and never added to the headline, which is the natural count.\n\n")
	}
	if len(c.BySource) > 0 {
		b.WriteString("| source | documents |\n| --- | --- |\n")
		for _, k := range cardSorted(c.BySource) {
			fmt.Fprintf(b, "| %s | %d |\n", k, c.BySource[k])
		}
		b.WriteString("\n")
	}
	if len(c.ByRejectReason) > 0 {
		b.WriteString("| dropped for | documents |\n| --- | --- |\n")
		for _, k := range cardSorted(c.ByRejectReason) {
			fmt.Fprintf(b, "| %s | %d |\n", k, c.ByRejectReason[k])
		}
		b.WriteString("\n")
	}
	cardLicenses(b, c)
}

// cardLicenses is the part of the card a reader checks before downloading
// anything, which is how much of what is described above they can actually have.
func cardLicenses(b *strings.Builder, c Counts) {
	if len(c.Licenses) == 0 {
		return
	}

	b.WriteString("## What of it ships\n\n")
	b.WriteString("| license | documents | text | size |\n| --- | --- | --- | --- |\n")
	for _, l := range c.Licenses {
		if l.Documents == 0 {
			continue
		}
		ships := "withheld"
		if l.Class.Publishable() {
			ships = "published"
		}
		fmt.Fprintf(b, "| %s | %d | %s | %s |\n", l.Class, l.Documents, ships, may.Size(l.Bytes))
	}

	pub, held := c.Publishable(), c.Withheld()
	fmt.Fprintf(b, "\nThis file carries %d of the %d documents, which is %s of the %s the snapshot holds.\n",
		pub.Documents, c.Documents, may.Size(pub.Bytes), may.Size(c.Bytes))
	if held.Documents > 0 {
		fmt.Fprintf(b, "The other %d are counted here and not passed on, because a number that quietly disappears reads as a number that was never there.\n", held.Documents)
	}
	b.WriteString("\n")
}

func cardStages(b *strings.Builder, m *Manifest) {
	if len(m.Stages) == 0 {
		return
	}
	b.WriteString("## How it was produced\n\n")
	b.WriteString("Run these stages in this order with these configurations and you get this snapshot back. The version in a stage name is part of the identity: two documents cleaned by different versions of the same stage are not comparable.\n\n")
	b.WriteString("| stage | config | from |\n| --- | --- | --- |\n")
	for _, s := range m.Stages {
		from := strings.Join(s.Inputs, ", ")
		if from == "" {
			from = "the sources in the ingest manifest"
		}
		fmt.Fprintf(b, "| %s | `%s` | %s |\n", s.Name, cardShort(s.ConfigHash.String()), from)
	}
	b.WriteString("\n")
}

func cardShipping(b *strings.Builder, d Dataset) {
	if d.Tier == Working {
		b.WriteString("## What this repo is\n\n")
		b.WriteString("What a stage wrote on its way to a release, published as it is written so that a box can push a part and delete it rather than holding what it has finished. It is public like everything else here, it is rewritten when a source is pinned again, and it is not covered by a signed manifest. A release is, and that is the difference worth knowing before anybody builds on this.\n\n")
	}

	b.WriteString("## What ships and what does not\n\n")
	if d.Text {
		b.WriteString("This repo carries document text, so it carries only documents whose text may be redistributed.\n\n")
	} else {
		b.WriteString("This repo carries no document text. What it carries is the URL, the provenance columns and the scores, which is what lets somebody rebuild the same corpus from the same sources under their own lawful access.\n\n")
	}
	b.WriteString("| license class | text | metadata |\n| --- | --- | --- |\n")
	for _, c := range d.Classes {
		p := luat.Publishes(c)
		fmt.Fprintf(b, "| %s | %s | %s |\n", c, cardYes(p.Text && d.Text), cardYes(p.Metadata))
	}
	b.WriteString("\nEvery row carries its own class in the `license_class` column, so a reader who needs a narrower set than this repo holds can filter for it rather than trust the repo name.\n\n")
	if d.Text {
		b.WriteString("A page that reserved its text and data mining rights is not here, whatever its license says. The two are separate questions and the reservation is honored at the write, so a page that said no cannot reach a published file through a stage that forgot to ask. The `consent` column records what each page said, and an empty value means nobody was there to ask, which is true of every document that came out of somebody else's corpus.\n\n")
	}
}

// cardLayout says where the files are and why they are named that way, which is
// the thing a reader has to know before any of the queries below make sense.
func cardLayout(b *strings.Builder, d Dataset, m *Manifest, x []Indexed) {
	b.WriteString("## How it is laid out\n\n")

	if m != nil {
		b.WriteString("```\n")
		fmt.Fprintf(b, "%s\n", CardName)
		fmt.Fprintf(b, "%s/\n", SnapshotDir(m.Snapshot))
		fmt.Fprintf(b, "  %s\n", path.Base(DataPath(m.Snapshot, 1, len(m.Shards))))
		fmt.Fprintf(b, "  %s\n", path.Base(DataPath(m.Snapshot, 2, len(m.Shards))))
		b.WriteString("  ...\n```\n\n")
		b.WriteString("One directory per snapshot, and a snapshot is immutable, so a query that names one gets the same rows every time it runs.\n\n")
		return
	}

	// Every source is listed and the files inside one are not. There are four
	// directories and hundreds of parts in each, so the tree that fits is the one
	// that elides the parts, and a tree that elided a directory instead would be
	// hiding a quarter of the repo in the section whose job is to show it.
	by := BySource(x)
	b.WriteString("```\n")
	fmt.Fprintf(b, "%s\n%s\n", CardName, IndexName)
	for _, s := range by {
		fmt.Fprintf(b, "%s/%s/\n", DataDir, s.Source)
		for _, r := range x {
			if r.Source == s.Source {
				fmt.Fprintf(b, "  %s\n", path.Base(r.Path))
				break
			}
		}
		fmt.Fprintf(b, "  ...%s\n", cardRest(s.Parts))
	}
	b.WriteString("```\n\n")

	b.WriteString("One directory per source, and the file name is the snapshot, the input file of the source the part came out of, and the part. The snapshot is the source and the revision it was pinned at, so re-pinning a source puts its new parts beside the old ones in the same directory under a different name rather than moving the directory. That is deliberate: the directory is the config name somebody writes in a `load_dataset` call, and a name that moves every time a source is re-pinned is a name nobody can write down.\n\n")

	b.WriteString("The directories are named plainly rather than Hive style. A Hive path spells the directory `snapshot=" + cardExampleSnapshot(by) + "`, and then every reader who globs the repo gets a `snapshot` column in their result set that is in no file, sitting next to a `source` column that is, so the first thing the layout teaches them is a distinction they did not ask for.\n\n")
	_ = d
}

// cardRest is what the elision in the tree says it is eliding, since "..." on
// its own leaves a reader guessing whether it hides two files or two hundred.
func cardRest(parts int) string {
	if parts <= 1 {
		return ""
	}
	return fmt.Sprintf(" and %d more", parts-1)
}

func cardExampleSnapshot(by []SourceIndex) string {
	if len(by) > 0 && len(by[0].Snapshots) > 0 {
		return by[0].Snapshots[0]
	}
	return "gao-v1.0"
}

// cardReading is the section most readers come for, so every query in it is one
// that was run against this repo before it was written down, and each one says
// what it costs.
func cardReading(b *strings.Builder, d Dataset, m *Manifest, x []Indexed) {
	by := BySource(x)
	b.WriteString("## Reading it\n\n")
	b.WriteString("The files are Parquet and they are readable in place. Nothing below downloads the repo, and none of it needs a token, because the repo is public.\n\n")
	b.WriteString("### DuckDB\n\n")
	b.WriteString("Install DuckDB, then:\n\n")
	b.WriteString("```sql\nINSTALL httpfs;\nLOAD httpfs;\n```\n\n")

	if m == nil && len(by) > 0 {
		fmt.Fprintf(b, "**What is in the repo, without opening a single Parquet file.** `%s` is a CSV of one row per part, so this is a few tens of kilobytes of reading.\n\n", IndexName)
		b.WriteString("```sql\n")
		fmt.Fprintf(b, "SELECT source, count(*) AS parts, sum(documents) AS documents,\n")
		fmt.Fprintf(b, "       round(sum(bytes) / 1e9, 1) AS gb\n")
		fmt.Fprintf(b, "FROM 'hf://datasets/%s/%s'\n", d.Repo(), IndexName)
		b.WriteString("GROUP BY source ORDER BY documents DESC;\n")
		b.WriteString("```\n\n")
		cardIndexOutput(b, by)
	}

	b.WriteString("**Count one source.** A count reads the row counts out of each file's footer rather than the file, so this is a few hundred kilobytes whatever the source weighs.\n\n")
	b.WriteString("```sql\n")
	fmt.Fprintf(b, "SELECT count(*) AS documents\nFROM read_parquet('%s');\n", cardGlob(d, m, by))
	b.WriteString("```\n\n")
	if s, ok := cardSmallest(by); ok {
		cardBox(b, []cardColumn{{
			Head: "documents", Type: "int64", Right: true,
			Cells: []string{strconv.FormatInt(s.Documents, 10)},
		}})
	}

	b.WriteString("**Group by a column.** Parquet is columnar, so a query over two columns reads two columns. This one touches ")
	if s, ok := cardSmallest(by); ok {
		fmt.Fprintf(b, "%s documents across %d files.\n\n", cardCommas(s.Documents), s.Parts)
	} else {
		b.WriteString("only the columns it names.\n\n")
	}
	b.WriteString("```sql\n")
	b.WriteString("SELECT license_class, count(*) AS documents,\n")
	b.WriteString("       round(avg(n_syllables)) AS mean_syllables\n")
	fmt.Fprintf(b, "FROM read_parquet('%s')\n", cardGlob(d, m, by))
	b.WriteString("GROUP BY license_class ORDER BY documents DESC;\n")
	b.WriteString("```\n\n")

	b.WriteString("**Look at some documents.** Reading `text` is the one thing here that is not cheap: the row groups hold 50,000 documents each, so the smallest useful read of the text column is a row group of it, which on a full sized part is a couple of hundred megabytes. That is why this one is pointed at a single part, and at the smallest part in the repo, rather than at a glob.\n\n")
	b.WriteString("```sql\n")
	b.WriteString("SELECT url, lang, n_syllables, substr(text, 1, 60) AS opening\n")
	fmt.Fprintf(b, "FROM read_parquet('%s')\n", cardOnePart(d, m, x))
	fmt.Fprintf(b, "WHERE lang = '%s' AND n_syllables BETWEEN 200 AND 400\nLIMIT 5;\n", LangValue)
	b.WriteString("```\n\n")
	fmt.Fprintf(b, "The `lang` column is ISO 639-3, so the value to filter on is `%s` rather than the `%s` in this card's front matter.\n\n", LangValue, Language)

	b.WriteString("**Every column and its type.**\n\n")
	b.WriteString("```sql\n")
	fmt.Fprintf(b, "DESCRIBE SELECT * FROM read_parquet('%s');\n", cardOnePart(d, m, x))
	b.WriteString("```\n\n")

	b.WriteString("### Python\n\n")
	b.WriteString("The configs in this card's front matter are what `datasets` reads, so a source is a config name.\n\n")
	b.WriteString("```python\nfrom datasets import load_dataset\n\n")
	if m == nil && len(by) > 0 {
		fmt.Fprintf(b, "# One source, streamed rather than downloaded.\nds = load_dataset(%q, %q, split=\"train\", streaming=True)\n", d.Repo(), by[len(by)-1].Source)
		b.WriteString("print(next(iter(ds))[\"url\"])\n```\n\n")
		b.WriteString("```python\n")
		b.WriteString("from huggingface_hub import snapshot_download\n\n")
		fmt.Fprintf(b, "# One source on disk, which for the smallest of these is %s.\nsnapshot_download(\n    %q,\n    repo_type=\"dataset\",\n    allow_patterns=\"%s/%s/*\",\n)\n",
			may.Size(by[len(by)-1].Bytes), d.Repo(), DataDir, by[len(by)-1].Source)
		b.WriteString("```\n\n")
	} else {
		fmt.Fprintf(b, "ds = load_dataset(%q, split=\"train\", streaming=True)\nprint(next(iter(ds))[\"url\"])\n```\n\n", d.Repo())
	}
}

// cardIndexOutput is the answer the query above came back with, so a reader can
// tell before running it whether it is the query they wanted.
func cardIndexOutput(b *strings.Builder, by []SourceIndex) {
	cols := []cardColumn{
		{Head: "source", Type: "varchar"},
		{Head: "parts", Type: "int64", Right: true},
		{Head: "documents", Type: "int128", Right: true},
		{Head: "gb", Type: "double", Right: true},
	}
	for _, s := range by {
		cols[0].Cells = append(cols[0].Cells, s.Source)
		cols[1].Cells = append(cols[1].Cells, strconv.Itoa(s.Parts))
		cols[2].Cells = append(cols[2].Cells, strconv.FormatInt(s.Documents, 10))
		cols[3].Cells = append(cols[3].Cells, strconv.FormatFloat(float64(s.Bytes)/1e9, 'f', 1, 64))
	}
	cardBox(b, cols)
}

// A cardColumn is one column of a result the card shows the output of.
type cardColumn struct {
	Head, Type string

	// Right is whether the cells are right aligned, which is how DuckDB prints
	// numbers and is not how it prints anything else.
	Right bool

	Cells []string
}

// cardBox prints a result the way DuckDB prints one.
//
// The output blocks on this card are generated rather than pasted, for the same
// reason the counts above them are: a pasted one is right on the afternoon it is
// pasted and describes the repo of that afternoon forever after. Matching the
// real renderer down to the padding is what lets a reader compare what they got
// against what the card says without wondering whether the difference is the
// data or the formatting.
func cardBox(b *strings.Builder, cols []cardColumn) {
	inner := make([]int, len(cols))
	for i, c := range cols {
		w := max(len([]rune(c.Head)), len([]rune(c.Type)))
		for _, cell := range c.Cells {
			w = max(w, len([]rune(cell)))
		}
		inner[i] = w + 2
	}

	rule := func(left, mid, right string) {
		b.WriteString(left)
		for i, w := range inner {
			if i > 0 {
				b.WriteString(mid)
			}
			b.WriteString(strings.Repeat("─", w))
		}
		b.WriteString(right + "\n")
	}
	// The head and the type are centered and everything else is padded to one
	// side, which is DuckDB's own rule rather than a choice made here.
	centered := func(pick func(cardColumn) string) {
		for i, c := range cols {
			pad := inner[i] - len([]rune(pick(c)))
			fmt.Fprintf(b, "│%s%s%s", strings.Repeat(" ", pad/2), pick(c), strings.Repeat(" ", pad-pad/2))
		}
		b.WriteString("│\n")
	}

	b.WriteString("```\n")
	rule("┌", "┬", "┐")
	centered(func(c cardColumn) string { return c.Head })
	centered(func(c cardColumn) string { return c.Type })
	rule("├", "┼", "┤")
	for row := range cols[0].Cells {
		for i, c := range cols {
			cell := c.Cells[row]
			pad := strings.Repeat(" ", inner[i]-2-len([]rune(cell)))
			if c.Right {
				cell = pad + cell
			} else {
				cell += pad
			}
			fmt.Fprintf(b, "│ %s ", cell)
		}
		b.WriteString("│\n")
	}
	rule("└", "┴", "┘")
	b.WriteString("```\n\n")
}

// cardGlob is the path expression the example queries read, which is one source
// on a working repo and the snapshot on a released one.
func cardGlob(d Dataset, m *Manifest, by []SourceIndex) string {
	if m != nil {
		return fmt.Sprintf("hf://datasets/%s/%s/*%s", d.Repo(), SnapshotDir(m.Snapshot), ParquetExt)
	}
	source := "SOURCE"
	if s, ok := cardSmallest(by); ok {
		source = s.Source
	}
	return fmt.Sprintf("hf://datasets/%s/%s/%s/*%s", d.Repo(), DataDir, source, ParquetExt)
}

// cardOnePart names a file that is really in the repo, because a snippet with an
// invented path in it is a snippet that fails for whoever pastes it first.
//
// It is the smallest part rather than the first one. The snippet it goes in
// reads the text column, the smallest useful read of that column is one row
// group of it, and the difference between the smallest part here and the largest
// is minutes of waiting for somebody who has pasted a line to see whether this
// corpus is worth their disk.
func cardOnePart(d Dataset, m *Manifest, x []Indexed) string {
	if len(x) > 0 {
		smallest := x[0]
		for _, row := range x[1:] {
			if row.Bytes < smallest.Bytes {
				smallest = row
			}
		}
		return fmt.Sprintf("hf://datasets/%s/%s", d.Repo(), smallest.Path)
	}
	if m != nil {
		return fmt.Sprintf("hf://datasets/%s/%s", d.Repo(), DataPath(m.Snapshot, 1, len(m.Shards)))
	}
	return fmt.Sprintf("hf://datasets/%s/%s/*/*%s", d.Repo(), DataDir, ParquetExt)
}

// cardSmallest picks the source the example queries run against, which is the
// smallest one so that a reader who pastes them gets an answer rather than a
// coffee break.
func cardSmallest(by []SourceIndex) (SourceIndex, bool) {
	if len(by) == 0 {
		return SourceIndex{}, false
	}
	return by[len(by)-1], true
}

// cardFields is the table that says what every column means. It is generated
// from the same struct the writer writes, so a column that is added without a
// meaning fails a test rather than shipping undocumented.
func cardFields(b *strings.Builder, d Dataset) {
	b.WriteString("## The columns\n\n")
	cols, nested := Schema(), Nested()
	fmt.Fprintf(b, "%s, in file order. Every part in this repo has all of them, and a column a stage has not run yet is null rather than absent, so a query written against one source works against the next.\n\n", plural(len(cols), "column"))

	b.WriteString("| column | type | filled in by | meaning |\n| --- | --- | --- | --- |\n")
	for _, c := range cols {
		fmt.Fprintf(b, "| `%s` | `%s` | %s | %s |\n", c.Name, c.Type, cardStage(c.Stage), c.Meaning)
	}
	b.WriteString("\n")

	if len(nested) > 0 {
		fmt.Fprintf(b, "`pii_spans` is a list of structs, and the struct is:\n\n")
		b.WriteString("| field | type | meaning |\n| --- | --- | --- |\n")
		for _, c := range nested {
			fmt.Fprintf(b, "| `%s` | `%s` | %s |\n", strings.TrimPrefix(c.Name, "pii_spans."), c.Type, c.Meaning)
		}
		b.WriteString("\n")
	}

	if !d.Text {
		b.WriteString("The `text` column is in the schema and is null in every row of this repo, which is the point of it: the same schema, with the one thing that may not be redistributed left out, so that a reader can rebuild the text themselves from `url` under their own lawful access.\n\n")
	}
	fmt.Fprintf(b, "The full schema, including the Parquet spelling of each type and what the dictionary encoded columns cost, is at %s/blob/main/SCHEMA.md.\n\n", Repository)
}

func cardStage(s string) string {
	if s == "" {
		return "the ingest"
	}
	return "`" + s + "`"
}

// LicenseNamePattern is what the Hub requires a license_name to match. It is
// written down here because it is not free text, which is what it looks like
// until a commit comes back 400, and because the test that keeps the card
// loadable needs something to check against.
var LicenseNamePattern = regexp.MustCompile(`^[a-z0-9-.]+$`)

// cardLicenseName is the license_name field. It is a slug rather than a
// sentence, so the sentence it wants to be lives in the body: which classes a
// repo carries, and what happens to the text of each, are a table under what
// ships rather than a value the Hub will not accept.
func cardLicenseName(d Dataset) string {
	names := make([]string, 0, len(d.Classes))
	for _, c := range d.Classes {
		names = append(names, c.String())
	}
	return "per-document-" + strings.Join(names, "-")
}

// cardSize is the Hub's bucket for a document count. The buckets are theirs
// and the boundaries are inclusive at the bottom, which is why this walks a
// table rather than computing a power of ten: the top bucket has no upper bound
// and the bottom one has no lower bound, so the arithmetic has two special
// cases either way.
func cardSize(n int64) string {
	buckets := []struct {
		below int64
		name  string
	}{
		{1e3, "n<1K"},
		{1e4, "1K<n<10K"},
		{1e5, "10K<n<100K"},
		{1e6, "100K<n<1M"},
		{1e7, "1M<n<10M"},
		{1e8, "10M<n<100M"},
		{1e9, "100M<n<1B"},
		{1e10, "1B<n<10B"},
		{1e11, "10B<n<100B"},
		{1e12, "100B<n<1T"},
	}
	for _, b := range buckets {
		if n < b.below {
			return b.name
		}
	}
	return "n>1T"
}

// cardPretty turns a repo name into a title. The names are chosen to describe what
// is in them, which means they already read as titles once the hyphens are gone.
func cardPretty(name string) string {
	words := strings.Split(name, "-")
	for i, w := range words {
		if w == "urls" {
			words[i] = "URLs"
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// cardShort cuts a hash to something a person can compare by eye. The full value is
// in the manifest, which is where anything mechanical reads it from.
func cardShort(h string) string {
	if len(h) <= 16 {
		return h
	}
	return h[:16]
}

// cardCommas groups a count in threes. A hundred and sixteen million is the
// headline this repo gets to claim and 116307393 is a number a reader has to
// count the digits of to find out whether it is a hundred million or a billion.
func cardCommas(n int64) string {
	s := strconv.FormatInt(n, 10)
	if n < 0 {
		return "-" + cardCommas(-n)
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func cardYes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// cardSorted orders a breakdown largest first, and by name where two are
// equal so the card does not change when nothing did.
func cardSorted(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}
