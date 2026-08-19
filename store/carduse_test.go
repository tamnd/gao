package store

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestTheContentsLinkToHeadingsThatAreOnTheCard(t *testing.T) {
	// A table of contents is the one part of a card that fails silently. The
	// links render, they are the right color, and clicking one does nothing,
	// which is worse than not having them.
	card := Card(Staging(), nil, indexRows())

	headings := map[string]bool{}
	for line := range strings.SplitSeq(card, "\n") {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		headings[cardAnchor(strings.TrimPrefix(line, "## "))] = true
	}

	link := regexp.MustCompile(`^- \[.+\]\(#([a-z0-9-]+)\)$`)
	var found int
	for line := range strings.SplitSeq(card, "\n") {
		m := link.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		found++
		if !headings[m[1]] {
			t.Errorf("the contents link to #%s and there is no heading with that anchor", m[1])
		}
	}
	if found == 0 {
		t.Fatal("the card has no table of contents")
	}
	for h := range headings {
		if h == "contents" {
			continue
		}
		if !strings.Contains(card, "](#"+h+")") {
			t.Errorf("the %s section is not in the contents", h)
		}
	}
}

// cardAnchor is how the Hub's markdown turns a heading into a fragment, which
// is lowercase with the punctuation dropped and the spaces hyphenated.
func cardAnchor(head string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(head) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteRune('-')
		}
	}
	return b.String()
}

func TestTheCardNamesEverySourceAndLinksItUpstream(t *testing.T) {
	card := Card(Staging(), nil, indexRows())
	for _, s := range BySource(indexRows()) {
		u, ok := cardUpstream[s.Source]
		if !ok {
			t.Errorf("%s has no upstream description, so the card cannot say what it is", s.Source)
			continue
		}
		if !strings.Contains(card, u.URL) {
			t.Errorf("the card does not link to %s upstream at %s", s.Source, u.URL)
		}
		if !strings.Contains(card, "### "+u.Name+", as `"+s.Source+"`") {
			t.Errorf("the card has no section for %s", u.Name)
		}
	}
}

func TestTheExampleRowCarriesEveryColumnInTheSchema(t *testing.T) {
	// The row is here to show what the columns hold. A row that is missing one
	// is a reader working out for themselves whether the column is absent from
	// the file or absent from the example.
	card := Card(Staging(), nil, indexRows())
	row, ok := cardSection(card, "## One row")
	if !ok {
		t.Fatal("the card has no example row")
	}
	for _, c := range Schema() {
		if !strings.Contains(row, `"`+c.Name+`":`) {
			t.Errorf("the example row does not carry %s", c.Name)
		}
	}
}

func TestEveryQueryOnTheCardReadsAPathTheRepoHas(t *testing.T) {
	d, x := Staging(), indexRows()
	card := Card(d, nil, x)

	paths := map[string]bool{
		"hf://datasets/" + d.Repo() + "/" + IndexName: true,
	}
	for _, r := range x {
		paths["hf://datasets/"+d.Repo()+"/"+r.Path] = true
	}
	for _, s := range BySource(x) {
		paths["hf://datasets/"+d.Repo()+"/"+DataDir+"/"+s.Source+"/*"+ParquetExt] = true
	}

	read := regexp.MustCompile(`'(hf://[^']+)'`)
	var found int
	for _, m := range read.FindAllStringSubmatch(card, -1) {
		found++
		if !paths[m[1]] {
			t.Errorf("a query reads %s, which is not in this repo", m[1])
		}
	}
	if found == 0 {
		t.Fatal("the card has no queries on it")
	}
}

func TestAMeasuredOutputIsDroppedWhenTheDataItWasMeasuredOnHasMoved(t *testing.T) {
	// The counts on this card are generated and cannot go stale. An example
	// output was measured once, so the only honest thing it can do when the
	// repo has moved on is not appear.
	var b strings.Builder
	r := cardMeasured{Source: "finepdfs", Documents: 1218257,
		Cols: []cardColumn{{Head: "documents", Type: "int64", Cells: []string{"952841"}}}}

	cardMeasuredBox(&b, SourceIndex{Source: "finepdfs", Documents: 1218257}, r)
	if !strings.Contains(b.String(), "952841") {
		t.Error("the output is dropped for the data it was measured on")
	}

	b.Reset()
	cardMeasuredBox(&b, SourceIndex{Source: "finepdfs", Documents: 1218258}, r)
	if b.Len() != 0 {
		t.Errorf("the output survives one more document landing in the source: %q", b.String())
	}

	b.Reset()
	cardMeasuredBox(&b, SourceIndex{Source: "glotcc", Documents: 1218257}, r)
	if b.Len() != 0 {
		t.Errorf("the output survives being run against another source: %q", b.String())
	}
}

func TestTheCardDoesNotClaimToBeTheLargestUntilItIs(t *testing.T) {
	small := []Indexed{{Source: "glotcc", Snapshot: "glotcc-9ad140b6be3a",
		Path: "data/glotcc/glotcc-9ad140b6be3a-00000-00000.parquet", Documents: 1000, Bytes: 1000}}
	if strings.Contains(Card(Staging(), nil, small), "For scale") {
		t.Error("a repo of a thousand documents compares itself to fineweb-2")
	}
	big := []Indexed{{Source: "hplt3", Snapshot: "hplt3-5b2785d5b11c",
		Path:      "data/hplt3/hplt3-5b2785d5b11c-00000-00000.parquet",
		Documents: cardBiggestUpstream + 1, Bytes: 1 << 40}}
	if !strings.Contains(Card(Staging(), nil, big), "For scale") {
		t.Error("a repo past fineweb-2 does not say so")
	}
}

// cardSection cuts the card down to one section, so a test about the example
// row does not pass because the column it wanted was named somewhere else.
func cardSection(card, head string) (string, bool) {
	_, rest, ok := strings.Cut(card, head)
	if !ok {
		return "", false
	}
	if body, _, ok := strings.Cut(rest, "\n## "); ok {
		return body, true
	}
	return rest, true
}

// The keep rate is the number a reader of this card is most likely to quote
// somewhere else, so the arithmetic in the table has to be the arithmetic in
// the percentages beside it, and the total line has to be the sum rather than
// an average of the two rates.
func TestTheKeepRateTableAddsUp(t *testing.T) {
	var b strings.Builder
	cardKeepRate(&b)
	card := b.String()

	var parts int
	var raw, kept int64
	for _, k := range cardCleanKeep {
		parts += k.Parts
		raw += k.Raw
		kept += k.Kept

		want := strconv.FormatFloat(100*float64(k.Kept)/float64(k.Raw), 'f', 1, 64) + "%"
		if k.Pct != want {
			t.Errorf("%s is written down as %s kept, and %d of %d is %s", k.Source, k.Pct, k.Kept, k.Raw, want)
		}
		if !strings.Contains(card, "`"+k.Source+"`") {
			t.Errorf("the table does not name %s:\n%s", k.Source, card)
		}
	}

	total := strconv.FormatFloat(100*float64(kept)/float64(raw), 'f', 1, 64) + "%"
	if !strings.Contains(card, total) {
		t.Errorf("the table does not carry the overall rate of %s:\n%s", total, card)
	}
	if !strings.Contains(card, cardCommas(raw)) || !strings.Contains(card, cardCommas(kept)) {
		t.Errorf("the total line is not %s in and %s out:\n%s", cardCommas(raw), cardCommas(kept), card)
	}
	if !strings.Contains(card, "over the "+strconv.Itoa(parts)+" parts named in the table") {
		t.Errorf("the card does not say the reading was taken over %d parts:\n%s", parts, card)
	}
}

// A breakdown that does not account for the whole difference is a breakdown of
// the interesting part, and reading it as the whole story is how a card ends up
// claiming a corpus lost documents for one reason when it lost them for four.
// The normalizer's own residue is the fifth and is not printed, so the four
// reasons come to less than the difference rather than to all of it.
func TestTheReasonsForDroppingDocumentsDoNotOutrunTheDocumentsDropped(t *testing.T) {
	w := cardCleanWhy
	went := w.In - w.Out
	named := w.Repetition + w.Boilerplate + w.Language + w.Short

	if named > went {
		t.Errorf("the reasons account for %d documents and only %d went", named, went)
	}
	if went-named > went/100 {
		t.Errorf("%d of the %d that went are unaccounted for, which is more than the normalizer's share", went-named, went)
	}
}
