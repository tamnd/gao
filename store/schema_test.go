package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/store"
)

// The one worth having. A column added to the row type without a sentence
// saying what it holds ships as an undocumented column in a public dataset,
// and the first time anybody notices is when they ask us what it means.
func TestEveryPublishedColumnIsExplained(t *testing.T) {
	missing, stale := store.Undocumented()
	if len(missing) > 0 {
		t.Errorf("these columns ship with nothing written down about them: %s", strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("these are explained and are not in the file: %s", strings.Join(stale, ", "))
	}
}

// The page describes the file or it is worse than no page.
func TestTheDocumentedColumnsAreTheOnesInTheFile(t *testing.T) {
	cols := store.Schema()
	documented := make([]string, 0, len(cols))
	for _, c := range cols {
		documented = append(documented, c.Name)
	}

	// Columns() returns leaf paths, so a map or a list column arrives as two or
	// three entries under one name. The page is about the columns somebody
	// selects, which is the name.
	seen := map[string]bool{}
	var published []string
	for _, path := range store.Columns(store.SchemaFor(store.Dataset{Text: true})) {
		name, _, _ := strings.Cut(path, ".")
		if !seen[name] {
			seen[name] = true
			published = append(published, name)
		}
	}

	if strings.Join(documented, "\n") != strings.Join(published, "\n") {
		t.Errorf("the page and the file disagree:\ndocumented: %v\npublished:  %v", documented, published)
	}
}

func TestEveryColumnSaysWhichStageFillsIt(t *testing.T) {
	stages := map[string]bool{
		"harvest": true, "normalize": true, "sift": true, "mill": true,
		"count": true, "cover": true, "law": true, "pick": true, "store": true,
	}
	for _, c := range append(store.Schema(), store.Nested()...) {
		if !stages[c.Stage] {
			t.Errorf("%s says it is filled by %q, which is not a gao stage", c.Name, c.Stage)
		}
	}
}

// The root name is printed above the column list by every Parquet tool there
// is, so a file that announces itself as a Go type name is telling a reader
// about our source tree instead of about their data.
func TestThePublishedSchemaIsNamedForWhatItHolds(t *testing.T) {
	for _, d := range []store.Dataset{{Text: true}, {Text: false}} {
		def := store.Definition(d)
		if !strings.HasPrefix(def, "message document {") {
			t.Errorf("text=%v starts %q", d.Text, def[:min(40, len(def))])
		}
	}
}

func TestTheDefinitionCoversTheColumnsAndTheirTypes(t *testing.T) {
	def := store.Definition(store.Dataset{Text: true})
	for _, c := range store.Schema() {
		if !strings.Contains(def, " "+c.Name+" ") && !strings.Contains(def, " "+c.Name+";") {
			t.Errorf("the Parquet definition has no %s", c.Name)
		}
	}
	// The types the page renders in short form have to be the ones Parquet
	// actually writes, since the short form is what a reader will believe.
	for _, want := range []string{
		"required binary text (STRING)",
		"required int64 fetched_at (TIMESTAMP(isAdjustedToUTC=true,unit=MILLIS))",
		"required fixed_len_byte_array(32) doc_id",
		"required fixed_len_byte_array(16) dup_cluster",
	} {
		if !strings.Contains(def, want) {
			t.Errorf("the definition is missing %q:\n%s", want, def)
		}
	}
}

func TestTheTypesAreRenderedForAReaderRatherThanForGo(t *testing.T) {
	want := map[string]string{
		"doc_id":          "bytes(32)",
		"dup_cluster":     "bytes(16)",
		"fetched_at":      "timestamp(millisecond)",
		"tdm_signals":     "map<string, string>",
		"heuristics":      "map<string, float32>",
		"pii_types":       "list<string>",
		"pii_spans":       "list<span>",
		"translated":      "bool",
		"lang_score":      "float32",
		"n_chars":         "uint32",
		"schema_version":  "uint16",
		"pii_level":       "uint8",
		"license_class":   "string",
		"upstream_fields": "map<string, string>",
	}
	for _, c := range store.Schema() {
		if w, ok := want[c.Name]; ok && c.Type != w {
			t.Errorf("%s reads as %q, want %q", c.Name, c.Type, w)
		}
	}
}

// Dictionary encoding is read off the tag rather than written down twice, so
// this is a check that the reading works at all.
func TestTheEncodingIsReadOffTheFileAndNotGuessed(t *testing.T) {
	dict := map[string]bool{}
	for _, c := range store.Schema() {
		dict[c.Name] = c.Dict
	}
	if !dict["host"] {
		t.Error("host is dictionary encoded in the row type and the page says it is not")
	}
	if dict["text"] {
		t.Error("text is not dictionary encoded and the page says it is")
	}
	if dict["doc_id"] {
		t.Error("doc_id is not dictionary encoded and the page says it is")
	}
}

func TestTheSpanFieldsComeFromTheTypeThatWritesThem(t *testing.T) {
	nested := store.Nested()
	names := make([]string, 0, len(nested))
	for _, c := range nested {
		names = append(names, c.Name)
	}
	if got, want := strings.Join(names, " "), "pii_spans.start pii_spans.len pii_spans.type"; got != want {
		t.Errorf("the span fields are %q, want %q", got, want)
	}
}

// SCHEMA.md is the copy a reader meets first, on the web, without the source
// checked out. Generating it and not regenerating it is the failure mode this
// catches.
func TestTheSchemaPageInTheRepositoryIsCurrent(t *testing.T) {
	path := filepath.Join("..", "SCHEMA.md")
	on, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the schema page: %v", err)
	}
	if string(on) != store.Page() {
		t.Errorf("SCHEMA.md is behind the schema, run `make schema`")
	}
}

// Public text is written by people, and these are the tells that it was not.
func TestThePageReadsLikeSomebodyWroteIt(t *testing.T) {
	page := store.Page()
	if strings.Contains(page, "\u2014") {
		t.Error("the page has an em dash in it")
	}
	for _, c := range append(store.Schema(), store.Nested()...) {
		if strings.Contains(c.Meaning, "\n") {
			t.Errorf("the meaning of %s has a line break inside it", c.Name)
		}
		if strings.Contains(c.Meaning, "|") {
			t.Errorf("the meaning of %s has a pipe in it, which breaks the table", c.Name)
		}
		if strings.HasSuffix(c.Meaning, ".") {
			t.Errorf("the meaning of %s ends in a period, and the others do not", c.Name)
		}
	}
}
