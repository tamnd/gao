package store

// The published schema, written down.
//
// A corpus is only usable by somebody who did not build it if they can read a
// column list and know what the columns mean. The Go types carry that knowledge
// in doc comments, which reach a reader who has the source and nobody else, and
// a hand written schema page in a README reaches everybody and goes stale the
// first time a column is added.
//
// So this is generated from the row type and pinned by a test. The names and
// the types are read off the struct that defines the file, the meanings are
// written here beside them, and the test fails when the two disagree in either
// direction: a column with no meaning written down, or a meaning left behind by
// a column that no longer exists.

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// A Column is one column of the published format, as somebody reading the
// corpus needs it explained.
type Column struct {
	// Name is the published column name, which is what a query says.
	Name string

	// Type is the type in the readable form, since a reader deciding whether a
	// column answers their question wants map<string, string> rather than the
	// four lines of Parquet that spells it.
	Type string

	// Dict records that the column is dictionary encoded. It is not a property
	// of the data, and it is here because it is the difference between a column
	// that costs nothing to scan across half a billion rows and one that does.
	Dict bool

	// Stage is the gao subcommand that fills the column in. A column nobody
	// fills is a column that ships as a zero value, and knowing which stage owns
	// it is how a reader knows what an empty one means.
	Stage string

	// Meaning is the one sentence version.
	Meaning string
}

// meanings is what each column is, in one sentence, and which stage fills it.
//
// It is a map rather than a slice so that the order comes from the row type,
// which is the order the file is laid out in and the order somebody reading it
// will meet the columns.
var meanings = map[string]struct{ stage, meaning string }{
	"doc_id":         {"normalize", "blake3 of the normalized text, which is the document's identity: two documents with the same normalized text are the same document whichever path found them"},
	"raw_id":         {"harvest", "blake3 of the bytes before extraction, which is what links this row back to the WARC record or the source file it came out of"},
	"text":           {"normalize", "the document text, normalized to NFC with canonical tone mark placement and legacy encodings already transcoded"},
	"markdown":       {"normalize", "the same content as text with the document's shape left in, as CommonMark: headings, lists, tables, links and emphasis, normalized the same way except for the whitespace, which in markdown is the markup"},
	"body":           {"normalize", "the whole page as markdown, with only the elements that are not writing at all taken out, which is what a reader who disagrees with our extraction can run their own over, and what made an extractor bug recoverable without refetching the web"},
	"schema_version": {"store", "the version of this layout, carried per row because a store appended to across a pipeline upgrade holds two versions at once and a reader has to be able to tell"},

	"source":           {"harvest", "which acquisition path produced the document, one of the six gao runs"},
	"source_locator":   {"harvest", "where in that source it came from: shard and offset for an ingested corpus, file, offset and length for a WARC record"},
	"url":              {"harvest", "the page it came from, canonicalized"},
	"host":             {"harvest", "the host of that URL, which is the unit politeness, budgets and takedowns are all measured in"},
	"url_template":     {"harvest", "the URL with its variable path and query parts replaced by placeholders, which is what the crawl budgets against and how a calendar trap reads as one URL rather than ten thousand"},
	"fetched_at":       {"harvest", "when the document was fetched, in UTC milliseconds"},
	"media_type":       {"harvest", "the media type the response declared, before extraction decided what to do with it"},
	"extractor":        {"harvest", "name and semver of the extractor, because two documents extracted by different versions of the same extractor are not comparable"},
	"pipeline_version": {"store", "semver of the cleaning pipeline that produced this row"},

	"http_status":     {"harvest", "the status the fetch got, empty for a document that arrived through somebody else's corpus rather than through our crawl"},
	"robots_decision": {"harvest", "what robots.txt said about this fetch, recorded per fetch rather than assumed from a global setting so that a consent question years later has an answer"},
	"robots_rule":     {"harvest", "the rule that decided it, in the spelling the site wrote"},
	"robots_hash":     {"harvest", "blake3 of the robots.txt the decision was made against, so the decision can be rechecked against the file rather than against the file as it is today"},
	"tdm_signals":     {"harvest", "the machine readable text and data mining reservations the response carried, keyed by mechanism and holding what that mechanism said"},
	"consent":         {"harvest", "what the page said about being kept and trained on, in one word, where empty means nobody asked rather than the page said yes"},

	"lang":       {"sift", "the language identifier's verdict, which is vie for everything in gao and is stored anyway because a column that is constant today needs no migration tomorrow"},
	"lang_score": {"sift", "how sure the identifier was"},
	"diacritics": {"sift", "present, absent or mixed, because Vietnamese written without tone marks is still Vietnamese and is still not the same distribution"},
	"translated": {"sift", "the machine translation detector's verdict, since translated Vietnamese reads as fluent to a metric and as wrong to a native speaker"},

	"gao_qual":    {"sift", "the quality classifier's score for this document"},
	"gao_edu":     {"sift", "the educational value score, which is what the gao-edu slice is selected on"},
	"hplt_bucket": {"harvest", "the source corpus's own quality bucket where it had one, kept so gao's classifier can be compared against an independent one rather than only against itself"},
	"register":    {"harvest", "the source corpus's own register label, kept for the same reason"},
	"heuristics":  {"sift", "the raw heuristic measurements rather than the verdicts, so the corpus can be refiltered at a different threshold without being recomputed from the text"},

	"dup_cluster":       {"mill", "which duplicate cluster the document belongs to, empty when it is in none"},
	"dup_cluster_size":  {"mill", "how large that cluster is"},
	"is_representative": {"mill", "marks the one document per cluster a deduplicated view keeps, with the rest left in the store because deduplication is tuned rather than maximized"},

	"pii_level": {"cover", "how much personal data has been removed: none, the structured identifiers, or those plus addresses and identifying names"},
	"pii_types": {"cover", "which kinds of personal data were found"},
	"pii_spans": {"cover", "where they were found, empty on every row the cleaning line writes, because the offsets index the text before it was covered and because offsets published next to covered text say where the identifiers were"},

	"license_class":    {"law", "the per document redistribution determination, stored by name so a file read without gao says restricted rather than 3"},
	"license_evidence": {"law", "what determined that class, since a class without evidence is a guess"},

	"structure":       {"harvest", "what the document is: article, forum thread, legal, thesis, gazette, transcript, which drives both the extraction handler and the mixture weights"},
	"n_chars":         {"normalize", "how many characters the text holds"},
	"n_syllables":     {"normalize", "how many Vietnamese syllables it holds, which is the unit that survives a change of tokenizer"},
	"n_tokens":        {"count", "how many gao tokens it holds, under the tokenizer named in the manifest"},
	"contam_flags":    {"pick", "which evaluation benchmarks this document overlaps, flagged rather than deleted so one store can serve a training run that excludes them and an analysis that counts them"},
	"upstream_fields": {"harvest", "the source corpus's own metadata, verbatim, which is the difference between answering a provenance question later and having to ingest again"},
}

// Schema returns the published columns in the order the file declares them,
// each with its type and what it means.
func Schema() []Column {
	t := reflect.TypeFor[Row]()
	out := make([]Column, 0, t.NumField())
	for f := range t.Fields() {
		name, dict, ok := tagged(f)
		if !ok {
			continue
		}
		m := meanings[name]
		out = append(out, Column{
			Name:    name,
			Type:    readable(f.Type),
			Dict:    dict,
			Stage:   m.stage,
			Meaning: m.meaning,
		})
	}
	return out
}

// tagged reads the published name and the encoding off a field's parquet tag.
// A field with no tag is not part of the file and is not part of the page.
func tagged(f reflect.StructField) (name string, dict, ok bool) {
	name, opts, _ := strings.Cut(f.Tag.Get("parquet"), ",")
	if name == "" {
		return "", false, false
	}
	return name, slices.Contains(strings.Split(opts, ","), "dict"), true
}

// Undocumented returns the columns with no meaning written down, and the
// meanings written down for columns that do not exist. Both are failures of the
// same kind: a schema page that does not describe the file.
func Undocumented() (missing, stale []string) {
	named := make(map[string]bool, len(meanings))
	for _, c := range append(Schema(), Nested()...) {
		named[c.Name] = true
		if c.Meaning == "" || c.Stage == "" {
			missing = append(missing, c.Name)
		}
	}
	for name := range meanings {
		if !named[name] {
			stale = append(stale, name)
		}
	}
	for name := range spanMeanings {
		if !named["pii_spans."+name] {
			stale = append(stale, "pii_spans."+name)
		}
	}
	slices.Sort(stale)
	return missing, stale
}

// Definition returns the schema as Parquet spells it, which is the authority
// for the physical types and the one a tool outside gao will print.
func Definition(d Dataset) string { return SchemaFor(d).String() }

// Page renders the schema as the SCHEMA.md that ships in the repository.
//
// It is generated rather than written because a schema page maintained by hand
// is a schema page that describes last quarter's file, and a reader who cannot
// trust it has to read our source to use our data.
func Page() string {
	cols, nested := Schema(), Nested()

	var b strings.Builder
	p := func(format string, a ...any) {
		fmt.Fprintf(&b, format, a...)
		b.WriteString("\n\n")
	}

	p("# The gao record")
	p("<!-- Generated by `gao store schema -md`. The meanings live in store/schema.go, beside the type they describe, and a test fails when this file falls behind them. -->")
	p("One row is one document. The columns are the same in every gao release, and they are the same for a release that carries text and one that does not: the second is this schema with the text column removed and nothing else changed.")
	p("Nothing is nullable. A field no stage filled in is written as the zero value, which is an empty string, a zero, or false. That is a trade made on purpose, because a definition level on every column of half a billion rows costs more than it explains, and what an empty value means is written down here column by column instead.")
	p("The filled by column names the gao subcommand that puts the value there. It answers the question a reader asks when a column is empty, which is whether the stage ran and found nothing or never ran at all.")

	p("## The columns")
	p("All %d of them, in the order the file holds them.", len(cols))
	table(&b, cols)
	b.WriteString("\n")

	var dict []string
	for _, c := range append(cols, nested...) {
		if c.Dict {
			dict = append(dict, "`"+c.Name+"`")
		}
	}
	p("These are dictionary encoded: %s. That is a property of the file rather than of the data, and it is worth saying out loud because it is the difference between a filter on host reading one dictionary per row group and reading every row.", strings.Join(dict, ", "))

	p("## Inside pii_spans")
	p("`pii_spans` is a list, and each element has three fields.")
	table(&b, nested)
	b.WriteString("\n")
	p("The spans are present at redaction levels 0 and 1 and absent at level 2. Shipping the offsets of what was redacted alongside the redacted text hands back most of what the redaction removed.")

	p("## What Parquet sees")
	p("The physical types, as a Parquet tool prints them.")
	p("```\n%s\n```", Definition(Dataset{Text: true}))

	p("## When this changes")
	b.WriteString("`schema_version` is a column rather than a line in the manifest, because a store appended to across a pipeline upgrade holds two versions of the layout at once and a reader has to be able to tell which row is which. A column is never renamed and never changes meaning, since a column name in a released dataset is somebody else's query. The way this schema grows is a new column and a higher version, which leaves a reader who has never heard of the new column working.\n")
	return b.String()
}

// table writes one markdown table of columns.
func table(b *strings.Builder, cols []Column) {
	b.WriteString("| column | type | filled by | meaning |\n| --- | --- | --- | --- |\n")
	for _, c := range cols {
		fmt.Fprintf(b, "| `%s` | `%s` | `%s` | %s |\n", c.Name, c.Type, c.Stage, c.Meaning)
	}
}

// spanMeanings is the inside of a pii_spans element.
var spanMeanings = map[string]string{
	"start": "byte offset into the text where the identifier begins",
	"len":   "how many bytes long it is, counted in bytes rather than runes because a reader slicing the text has bytes",
	"type":  "which kind of identifier it is, from the same set pii_types draws on",
}

// Nested describes the one column that holds a struct, since a reader looking
// at pii_spans needs to know what is inside it.
func Nested() []Column {
	t := reflect.TypeFor[Span]()
	out := make([]Column, 0, t.NumField())
	for f := range t.Fields() {
		name, dict, ok := tagged(f)
		if !ok {
			continue
		}
		out = append(out, Column{
			Name:    "pii_spans." + name,
			Type:    readable(f.Type),
			Dict:    dict,
			Stage:   "cover",
			Meaning: spanMeanings[name],
		})
	}
	return out
}

// readable renders a Go type the way a reader of the corpus would say it out
// loud, rather than the way Go or Parquet spells it.
func readable(t reflect.Type) string {
	if t == reflect.TypeFor[time.Time]() {
		return "timestamp(millisecond)"
	}
	switch t.Kind() {
	case reflect.Array:
		return fmt.Sprintf("bytes(%d)", t.Len())
	case reflect.Map:
		return fmt.Sprintf("map<%s, %s>", readable(t.Key()), readable(t.Elem()))
	case reflect.Slice:
		return fmt.Sprintf("list<%s>", readable(t.Elem()))
	case reflect.Struct:
		return strings.ToLower(t.Name())
	default:
		return t.Kind().String()
	}
}
