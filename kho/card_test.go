package kho

import (
	"crypto/ed25519"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/luat"
	"github.com/tamnd/gao/may"
)

// released is a manifest with every field a card reads filled in, which is a
// snapshot after a real run rather than the minimum that compiles. A card is
// mostly a rendering of this, so a fixture missing a field is a section nothing
// tests.
func released(t *testing.T) *Manifest {
	t.Helper()
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	m := &Manifest{
		Snapshot:  "2026-09",
		Parent:    "2026-06",
		CreatedAt: at,
		Pipeline:  "0.4.1",
		Box:       "server1",
		Stages: []Stage{
			{Name: "gat@0.4.1", ConfigHash: doc.SumString("ingest config")},
			{Name: "sang@0.4.1", ConfigHash: doc.SumString("clean config"), Inputs: []string{"2026-09-ingest"}},
		},
		Counts: Counts{
			Documents: 412_000_000,
			Bytes:     900 << 30,
			Chars:     1_800_000_000_000,
			Syllables: 380_000_000_000,
			Tokens:    240_000_000_000,
			Tokenizer: "gao-vi-64k",
			Natural:   410_000_000,
			Synthetic: 2_000_000,
			Rejected:  88_000_000,
			BySource: map[string]int64{
				"fineweb2": 120_000_000,
				"glotcc":   200_000_000,
				"culturax": 92_000_000,
			},
			ByRejectReason: map[string]int64{
				"not-vietnamese": 60_000_000,
				"too-short":      28_000_000,
			},
		},
		Shards: []Shard{
			{Name: "part-00000.parquet", Index: 0, Documents: 206_000_000, Bytes: 450 << 30, Hash: doc.SumString("a")},
			{Name: "part-00001.parquet", Index: 1, Documents: 206_000_000, Bytes: 450 << 30, Hash: doc.SumString("b")},
		},
	}
	m.ManifestVersion, m.SchemaVersion = ManifestVersion, doc.SchemaVersion

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Seal(priv, at); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return m
}

func published(t *testing.T) Dataset {
	t.Helper()
	d, ok := Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the published text repo is not in the dataset table")
	}
	return d
}

func TestACardCarriesTheCountsFromTheManifestRatherThanFromAnybodysMemory(t *testing.T) {
	card := Card(published(t), released(t), nil)

	for _, want := range []string{
		"412000000",     // documents
		"410000000",     // natural
		"2000000",       // synthetic
		"240000000000",  // tokens
		"gao-vi-64k",    // and the tokenizer they were counted with
		"1800000000000", // characters
		"380000000000",  // syllables
		"88000000",      // rejected while building
	} {
		if !strings.Contains(card, want) {
			t.Errorf("the card does not carry %s", want)
		}
	}
}

func TestACardBreaksTheCountDownBySourceLargestFirst(t *testing.T) {
	card := Card(published(t), released(t), nil)

	order := []string{"glotcc", "fineweb2", "culturax"}
	at := make([]int, len(order))
	for i, s := range order {
		at[i] = strings.Index(card, "| "+s+" |")
		if at[i] < 0 {
			t.Fatalf("the card does not break the count down by %s", s)
		}
	}
	for i := 1; i < len(at); i++ {
		if at[i] < at[i-1] {
			t.Errorf("%s is listed above %s, so the breakdown is not largest first", order[i], order[i-1])
		}
	}
}

func TestACardNamesTheStagesAndTheVersionsTheyRanAt(t *testing.T) {
	card := Card(published(t), released(t), nil)

	// The version is the point. Two documents cleaned by different versions of
	// the same stage are not comparable, so a card that said "sang" without
	// saying which one would be describing a pipeline nobody can rerun.
	for _, want := range []string{"gat@0.4.1", "sang@0.4.1", "2026-09-ingest"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card does not name %s", want)
		}
	}
}

func TestACardSaysWhoSealedTheSnapshotAndWhen(t *testing.T) {
	m := released(t)
	card := Card(published(t), m, nil)

	if !strings.Contains(card, m.Signature.PublicKey[:16]) {
		t.Error("the card does not say which key sealed the snapshot")
	}
	if !strings.Contains(card, "2026-09-01") {
		t.Error("the card does not say when the snapshot was sealed")
	}
	if !strings.Contains(card, m.Root.String()) {
		t.Error("the card does not carry the merkle root")
	}
}

func TestAnUnsignedSnapshotIsNotDescribedAsARelease(t *testing.T) {
	m := released(t)
	m.Signature = Signature{}

	card := Card(published(t), m, nil)
	if !strings.Contains(card, "not a release") {
		t.Error("an unsigned snapshot is described as though somebody had signed it")
	}
}

func TestACardWithNoSnapshotSaysSoRatherThanPrintingZeros(t *testing.T) {
	card := Card(published(t), nil, nil)

	if !strings.Contains(card, "No snapshot has been sealed here yet") {
		t.Error("a repo with no snapshot does not say so")
	}
	// A count of 0 documents reads as a snapshot that found nothing, which is a
	// different claim from a repo that has not had one yet.
	if strings.Contains(card, "| documents |") {
		t.Error("a repo with no snapshot prints a count anyway")
	}
	// And it is not an empty repo either. An ingest pushes parts as it writes
	// them, so a reader who lands on this card is usually looking at a tree
	// with data in it, and a card that says nothing has been released reads as
	// a repo with nothing in it.
	if !strings.Contains(card, "does not mean the repo is empty") {
		t.Error("a card with no snapshot reads as a repo with no files")
	}
	if !strings.Contains(card, "Do not cite a count off them") {
		t.Error("the card does not say what the staged parts may not be used for")
	}
}

func TestACardPointsAtTheSnapshotItDescribes(t *testing.T) {
	m := released(t)
	card := Card(published(t), m, nil)

	want := SnapshotDir(m.Snapshot) + "/*" + ParquetExt
	if !strings.Contains(card, want) {
		t.Errorf("the front matter does not point at %s", want)
	}
	if !strings.Contains(card, published(t).Query(m.Snapshot)) {
		t.Error("the card does not carry the query that reads the snapshot")
	}
}

func TestACardOnlyClaimsToShipTextThatShips(t *testing.T) {
	// This is the check that matters legally, and it runs over the table rather
	// than over one example, so a repo added later cannot quietly claim more
	// than its classes allow.
	for _, d := range Datasets() {
		card := Card(d, released(t), nil)
		for _, c := range d.Classes {
			row := "| " + c.String() + " |"
			i := strings.Index(card, row)
			if i < 0 {
				t.Errorf("%s: the card does not say what happens to %s documents", d.Name, c)
				continue
			}
			line := card[i:]
			line = line[:strings.Index(line, "\n")]

			ships := luat.Publishes(c).Text && d.Text
			if got := strings.Contains(line, "| yes | "); got != ships {
				t.Errorf("%s: %q claims text ships = %v, want %v", d.Name, line, got, ships)
			}
		}
	}
}

func TestAWorkingRepoSaysItIsNotARelease(t *testing.T) {
	// It ships the same way a release does, so it carries the same table. What
	// it has to say on top of that is that nothing here is covered by a signed
	// manifest, which is the only thing a reader loses by reading it.
	card := Card(Staging(), nil, nil)

	if !strings.Contains(card, "## What ships") {
		t.Error("a working repo is published and does not say what it ships")
	}
	if !strings.Contains(card, "covered by no signed manifest") && !strings.Contains(card, "not covered by a signed manifest") {
		t.Error("a working repo does not say that it is not a release")
	}
}

func TestEveryDatasetRendersACardWithATitleAndAWayToReadIt(t *testing.T) {
	for _, d := range Datasets() {
		card := Card(d, nil, nil)
		if !strings.HasPrefix(card, "---\n") || !strings.Contains(card, "\n---\n\n# ") {
			t.Errorf("%s: the card has no front matter followed by a title", d.Name)
		}
		if !strings.Contains(card, d.Holds) {
			t.Errorf("%s: the card does not say what the repo holds", d.Name)
		}
		if !strings.Contains(card, "read_parquet") {
			t.Errorf("%s: the card does not say how to read it", d.Name)
		}
		if strings.Contains(card, "\n\n\n") {
			t.Errorf("%s: the card has a blank line doubled up", d.Name)
		}
	}
}

func TestACardIsTheSameCardEveryTimeTheDataIsTheSame(t *testing.T) {
	// The card is pushed on every release and skipped when it has not changed,
	// so a map iterating in a different order would commit an identical card as
	// a new one and make the repo history unreadable.
	d, m := published(t), released(t)
	first := Card(d, m, nil)
	for range 20 {
		if Card(d, m, nil) != first {
			t.Fatal("two renderings of the same snapshot differ")
		}
	}
}

func TestTheSizeCategoryIsTheBucketTheHubExpects(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "n<1K"},
		{999, "n<1K"},
		{1000, "1K<n<10K"},
		{412_000_000, "100M<n<1B"},
		{5_000_000_000, "1B<n<10B"},
		{2 << 40, "n>1T"},
	} {
		if got := cardSize(tc.n); got != tc.want {
			t.Errorf("cardSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestATitleReadsAsATitle(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"vietnamese-web-text", "Vietnamese Web Text"},
		{"vietnamese-web-urls", "Vietnamese Web URLs"},
	} {
		if got := cardPretty(tc.name); got != tc.want {
			t.Errorf("cardPretty(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The Hub validates the front matter and refuses a commit whose license_name is
// not a slug, which no fake found and one 400 from the real thing did. A card
// the Hub will not take is a card nobody sees.
func TestALicenseNameIsSomethingTheHubWillAccept(t *testing.T) {
	for _, d := range Datasets() {
		got := cardLicenseName(d)
		if !LicenseNamePattern.MatchString(got) {
			t.Errorf("%s: license_name %q does not match %s", d.Name, got, LicenseNamePattern)
		}
	}
}

// A front matter value with a colon in it parses as a nested mapping, so a card
// that carried one would load as something other than what it says.
func TestNoFrontMatterValueCarriesAColon(t *testing.T) {
	for _, d := range Datasets() {
		card := Card(d, released(t), nil)
		front, _, ok := strings.Cut(strings.TrimPrefix(card, "---\n"), "\n---\n")
		if !ok {
			t.Fatalf("%s: the card has no front matter", d.Name)
		}
		for _, line := range strings.Split(front, "\n") {
			_, value, isKey := strings.Cut(line, ": ")
			if !isKey {
				continue
			}
			if strings.Contains(value, ": ") {
				t.Errorf("%s: %q carries a colon, so it does not parse as one value", d.Name, line)
			}
		}
	}
}

// The whole reason the index exists is that a working repo can carry real
// numbers on its card instead of a promise that there will be numbers later.
func TestAWorkingRepoCardCarriesTheCountsFromTheIndex(t *testing.T) {
	card := Card(Staging(), nil, indexRows())

	documents, bytes := Total(indexRows())
	for _, want := range []string{
		cardCommas(documents), // the headline
		may.Size(bytes),
		"| `glotcc` |",
		"| `hplt3` |",
		cardCommas(350535), // glotcc summed over its three parts
		IndexName,
	} {
		if !strings.Contains(card, want) {
			t.Errorf("the card does not carry %q", want)
		}
	}
	if strings.Contains(card, "No snapshot has been sealed here yet") {
		t.Error("a repo with an index says it has no numbers anyway")
	}
}

// Without a configs block the Hub's viewer says the repo has no data, which on
// a quarter of a terabyte is the single most expensive line the card can omit.
func TestAWorkingRepoCardGivesTheViewerAConfigPerSourceAndOneForAllOfThem(t *testing.T) {
	front, _, ok := strings.Cut(strings.TrimPrefix(Card(Staging(), nil, indexRows()), "---\n"), "\n---\n")
	if !ok {
		t.Fatal("the card has no front matter")
	}
	if !strings.Contains(front, "configs:\n") {
		t.Fatalf("the front matter carries no configs block:\n%s", front)
	}
	for _, want := range []string{
		"config_name: default",
		"path: " + DataDir + "/*/*" + ParquetExt,
		"config_name: glotcc",
		"path: " + DataDir + "/glotcc/*" + ParquetExt,
		"config_name: hplt3",
	} {
		if !strings.Contains(front, want) {
			t.Errorf("the front matter is missing %q:\n%s", want, front)
		}
	}
	// The viewer opens whichever config is listed first, so a repo whose first
	// config is one source of two opens showing half of what it holds.
	if i, j := strings.Index(front, "config_name: default"), strings.Index(front, "config_name: glotcc"); i > j {
		t.Error("a source config is listed ahead of the default one")
	}
}

// A source pinned again lands in the repo under a second revision, and until the
// old parts are swept every document in it is counted twice. That is the one
// state where the totals above are wrong in a way a reader cannot see.
func TestACardSaysSoWhenASourceIsInTheRepoTwice(t *testing.T) {
	rows := append(indexRows(), Indexed{
		Source: "glotcc", Snapshot: "glotcc-0000deadbeef", File: 3, Part: 0,
		Path: "data/glotcc/glotcc-0000deadbeef-00003-00000.parquet", Documents: 116631, Bytes: 524202221,
	})
	card := Card(Staging(), nil, rows)
	if !strings.Contains(card, "more than one revision") {
		t.Error("the card counts a source twice without saying it does")
	}
	if strings.Contains(Card(Staging(), nil, indexRows()), "more than one revision") {
		t.Error("a repo with one revision per source is warned about anyway")
	}
}

// Every snippet on the card was run against the live repo before it went on,
// and what this checks is the part that rots on its own: the paths in them.
func TestTheSnippetsOnACardPointAtPathsThatExist(t *testing.T) {
	card := Card(Staging(), nil, indexRows())

	for _, want := range []string{
		"INSTALL httpfs",
		"read_parquet",
		"hf://datasets/" + Staging().Repo() + "/" + DataDir,
		IndexName,
		"load_dataset",
		"snapshot_download",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("the card does not carry %q", want)
		}
	}
	// The smallest one, because the snippet it goes in reads the text column
	// and the difference is minutes of waiting for whoever pasted it.
	if !strings.Contains(card, indexRows()[0].Path) {
		t.Error("the sample query does not name the smallest part in the index")
	}
}

// The output blocks on the card claim to be what DuckDB printed, so they have to
// be what DuckDB prints. This is the box v1.5.5 returned for the query the card
// shows, pasted from a terminal and diffed against the generated one.
func TestAnOutputBlockIsPaddedTheWayDuckDBPadsOne(t *testing.T) {
	want := "```\n" +
		"┌──────────┬───────┬───────────┬────────┐\n" +
		"│  source  │ parts │ documents │   gb   │\n" +
		"│ varchar  │ int64 │  int128   │ double │\n" +
		"├──────────┼───────┼───────────┼────────┤\n" +
		"│ hplt3    │   265 │  81290050 │  125.1 │\n" +
		"│ fineweb2 │   117 │  20941000 │   53.1 │\n" +
		"│ glotcc   │   110 │  12858086 │   52.4 │\n" +
		"│ finepdfs │    54 │   1218257 │   26.9 │\n" +
		"└──────────┴───────┴───────────┴────────┘\n" +
		"```\n\n"

	var b strings.Builder
	cardIndexOutput(&b, []SourceIndex{
		{Source: "hplt3", Parts: 265, Documents: 81_290_050, Bytes: 125_140_000_000},
		{Source: "fineweb2", Parts: 117, Documents: 20_941_000, Bytes: 53_060_000_000},
		{Source: "glotcc", Parts: 110, Documents: 12_858_086, Bytes: 52_440_000_000},
		{Source: "finepdfs", Parts: 54, Documents: 1_218_257, Bytes: 26_860_000_000},
	})
	if got := b.String(); got != want {
		t.Errorf("the card renders\n%s\nand DuckDB renders\n%s", got, want)
	}
}

// A column wider than its heading is the case the hand padded version got wrong,
// and a box whose rules do not line up with its cells reads as a broken card.
func TestAnOutputBlockWidensToWhatIsInIt(t *testing.T) {
	var b strings.Builder
	cardBox(&b, []cardColumn{
		{Head: "n", Type: "int64", Right: true, Cells: []string{"1", "1000000000000"}},
		{Head: "source", Type: "varchar", Cells: []string{"a-very-long-source-name", "b"}},
	})
	lines := strings.Split(strings.TrimSpace(strings.Trim(b.String(), "`\n")), "\n")
	width := len([]rune(lines[0]))
	for _, line := range lines {
		if len([]rune(line)) != width {
			t.Errorf("the box is %d wide and this line is %d:\n%s", width, len([]rune(line)), b.String())
		}
	}
}

// A reader deciding whether the corpus is worth 250 GB of their disk is deciding
// on the columns, and a schema they have to download the data to see is a schema
// they will not read.
func TestACardDescribesEveryColumnItShips(t *testing.T) {
	card := Card(Staging(), nil, indexRows())
	for _, c := range Schema() {
		if !strings.Contains(card, "| `"+c.Name+"` |") {
			t.Errorf("the card does not describe the %s column", c.Name)
		}
	}
}

// The card is what a reader sees before deciding to download 400 GB, so the
// number that belongs on it is how much of the corpus they actually get.
func TestTheCardSaysHowMuchOfTheCorpusShips(t *testing.T) {
	m := licensed(released(t))
	card := Card(published(t), m, nil)
	for _, want := range []string{"What of it ships", "restricted", "withheld", "published"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card does not mention %q", want)
		}
	}
	pub := m.Counts.Publishable()
	if !strings.Contains(card, fmt.Sprintf("carries %d of the %d documents", pub.Documents, m.Counts.Documents)) {
		t.Errorf("the card does not state the shippable subset against the total:\n%s", card)
	}
}
