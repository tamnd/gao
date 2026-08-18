package kho

import (
	"crypto/ed25519"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/luat"
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
	card := Card(published(t), released(t))

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
	card := Card(published(t), released(t))

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
	card := Card(published(t), released(t))

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
	card := Card(published(t), m)

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

	card := Card(published(t), m)
	if !strings.Contains(card, "not a release") {
		t.Error("an unsigned snapshot is described as though somebody had signed it")
	}
}

func TestACardWithNoSnapshotSaysSoRatherThanPrintingZeros(t *testing.T) {
	card := Card(published(t), nil)

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
	card := Card(published(t), m)

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
		card := Card(d, released(t))
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
	card := Card(Staging(), nil)

	if !strings.Contains(card, "## What ships") {
		t.Error("a working repo is published and does not say what it ships")
	}
	if !strings.Contains(card, "covered by no signed manifest") && !strings.Contains(card, "not covered by a signed manifest") {
		t.Error("a working repo does not say that it is not a release")
	}
}

func TestEveryDatasetRendersACardWithATitleAndAWayToReadIt(t *testing.T) {
	for _, d := range Datasets() {
		card := Card(d, nil)
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
	first := Card(d, m)
	for range 20 {
		if Card(d, m) != first {
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
		card := Card(d, released(t))
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

// The card is what a reader sees before deciding to download 400 GB, so the
// number that belongs on it is how much of the corpus they actually get.
func TestTheCardSaysHowMuchOfTheCorpusShips(t *testing.T) {
	m := licensed(released(t))
	card := Card(published(t), m)
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
