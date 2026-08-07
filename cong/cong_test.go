package cong

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// group is a count that adds up, so that a test changing one field is changing
// one thing. The ratios are taken off real Vietnamese: a little over 1.3 bytes
// a character in UTF-8, and a little under two tokens a syllable.
func group(source string, tokens int64) Group {
	return Group{
		Source:    source,
		Snapshot:  "gao-2026-09",
		Origin:    Natural,
		Class:     doc.LicenseOpen,
		Documents: tokens / 400,
		Bytes:     int64(float64(tokens) * 2.4),
		Chars:     int64(float64(tokens) * 1.8),
		Syllables: int64(float64(tokens) / 1.9),
		Tokens:    tokens,
		Tokenizer: "gao-64k",
	}
}

func sheet(groups ...Group) Sheet {
	return Sheet{Release: "gao-v1.0", Groups: groups}
}

// refuses asserts the sheet is blocked and says so in the words given.
func refuses(t *testing.T, s Sheet, want string) {
	t.Helper()
	why := s.Blocking()
	if len(why) == 0 {
		t.Fatalf("the sheet was accepted and it should have been refused for %q", want)
	}
	for _, w := range why {
		if strings.Contains(w, want) {
			return
		}
	}
	t.Errorf("no refusal mentions %q, and what came back was:\n  %s", want, strings.Join(why, "\n  "))
}

func TestTheHeadlineIsTheNaturalNumber(t *testing.T) {
	made := group("gao-synth", 40_000_000_000)
	made.Origin = Made
	s := sheet(group("crawl", 200_000_000_000), group("hplt", 110_000_000_000), made)

	if !s.Settled() {
		t.Fatalf("a sheet that adds up was refused: %v", s.Blocking())
	}
	if got := s.Headline(); got != 310_000_000_000 {
		t.Errorf("the headline is %d and the natural groups hold 310B", got)
	}
	if got := s.Generated().Tokens; got != 40_000_000_000 {
		t.Errorf("the synthetic side is %d and it holds 40B", got)
	}
	// The one thing this package exists to prevent.
	if s.Headline() == s.Corpus().Tokens+s.Generated().Tokens {
		t.Error("the headline includes text a model wrote")
	}
	if !s.Holds() {
		t.Error("310B natural did not meet a 300B claim")
	}
}

func TestThePublishableSubsetIsStatedApartFromTheTotal(t *testing.T) {
	held := group("phap-luat", 30_000_000_000)
	held.Class = doc.LicenseRestricted
	s := sheet(group("crawl", 200_000_000_000), held)

	if got, want := s.Corpus().Tokens, int64(230_000_000_000); got != want {
		t.Errorf("the corpus holds %d and it is %d", got, want)
	}
	if got, want := s.Publishable().Tokens, int64(200_000_000_000); got != want {
		t.Errorf("%d ships and %d should", got, want)
	}
	if len(s.Held()) != 1 || s.Held()[0].Source != "phap-luat" {
		t.Errorf("the withheld groups are %v and phap-luat is the one that does not ship", s.Held())
	}
	// A class that holds nothing is not a row, and a class that holds something
	// is, so a reader counting the rows counts the classes that exist.
	if got := len(s.Classes()); got != 2 {
		t.Errorf("the class table has %d rows over two classes", got)
	}
}

func TestASourceCountedTwiceIsRefused(t *testing.T) {
	// The one mistake a total cannot show, since the sum of a doubled row is a
	// number that adds up perfectly.
	s := sheet(group("crawl", 200_000_000_000), group("crawl", 200_000_000_000))
	refuses(t, s, "appears twice")
}

func TestTwoTokenizersAreTwoUnits(t *testing.T) {
	other := group("hplt", 110_000_000_000)
	other.Tokenizer = "gao-32k"
	s := sheet(group("crawl", 200_000_000_000), other)
	refuses(t, s, "two tokenizers are two units")
}

func TestCountsOffTwoSnapshotsAreRefused(t *testing.T) {
	later := group("hplt", 110_000_000_000)
	later.Snapshot = "gao-2026-10"
	s := sheet(group("crawl", 200_000_000_000), later)
	refuses(t, s, "more than one snapshot")
}

func TestAColumnCountingSomethingElseIsCaught(t *testing.T) {
	// Bytes equal to characters is ASCII, and Vietnamese spends two bytes on
	// every vowel that carries a tone mark.
	ascii := group("crawl", 200_000_000_000)
	ascii.Bytes = ascii.Chars
	refuses(t, sheet(ascii), "bytes a character")

	// Ten tokens a syllable is not a Vietnamese tokenizer, whatever the row says.
	wrong := group("crawl", 200_000_000_000)
	wrong.Syllables = wrong.Tokens / 10
	refuses(t, sheet(wrong), "tokens a syllable")
}

func TestAGroupThatCannotBeAddedSaysWhy(t *testing.T) {
	for name, spoil := range map[string]func(*Group){
		"no source":    func(g *Group) { g.Source = "" },
		"no origin":    func(g *Group) { g.Origin = "" },
		"no class":     func(g *Group) { g.Class = doc.LicenseUnknown },
		"no snapshot":  func(g *Group) { g.Snapshot = "" },
		"no tokenizer": func(g *Group) { g.Tokenizer = "" },
		"no documents": func(g *Group) { g.Documents = 0 },
		"no bytes":     func(g *Group) { g.Bytes = 0 },
		"no chars":     func(g *Group) { g.Chars = 0 },
		"no syllables": func(g *Group) { g.Syllables = 0 },
		"no tokens":    func(g *Group) { g.Tokens = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			g := group("crawl", 200_000_000_000)
			spoil(&g)
			if why := g.Blocking(); len(why) == 0 {
				t.Errorf("a group with %s was accepted as a count", name)
			}
		})
	}
}

func TestTheRatiosAreComputedFromTheNumberThatCameBack(t *testing.T) {
	// The kill criterion says publish the real number and restate the ratios, so
	// the ratios have to move with it rather than sit next to it.
	short := sheet(group("crawl", 180_000_000_000))
	if !short.Killed() {
		t.Fatal("180B natural did not trip a kill criterion written at 250B")
	}
	against := strings.Join(short.Restate(), ", ")
	if strings.Contains(against, "1.7x") {
		t.Errorf("the ratios still read as the 300B claim: %s", against)
	}
	if !strings.HasPrefix(against, "1.0x HPLT") {
		t.Errorf("180B against HPLT's 176B is about even and the restatement says %s", against)
	}
	if v := short.Verdict(); !strings.Contains(v, "180.0B") || !strings.Contains(v, "rather than defended") {
		t.Errorf("a killed release does not read as one:\n  %s", v)
	}
}

func TestMissingTheTargetIsNotTheSameEventAsTheKillCriterion(t *testing.T) {
	s := sheet(group("crawl", 270_000_000_000))
	if s.Holds() {
		t.Error("270B met a 300B claim")
	}
	if s.Killed() {
		t.Error("270B tripped a kill criterion written at 250B")
	}
	if v := s.Verdict(); strings.Contains(v, "kill") {
		t.Errorf("a release that is short reads as a dead one:\n  %s", v)
	}
}

func TestTheSourceTableIsRankedByTokens(t *testing.T) {
	s := sheet(
		group("hplt", 110_000_000_000),
		group("crawl", 200_000_000_000),
		group("fineweb2", 40_000_000_000),
	)
	got := s.Sources()
	if len(got) != 3 {
		t.Fatalf("the table has %d rows over three sources", len(got))
	}
	if got[0].Source != "crawl" || got[2].Source != "fineweb2" {
		t.Errorf("the table is ordered %s, %s, %s", got[0].Source, got[1].Source, got[2].Source)
	}
	if got[0].Share(s.Headline()) <= got[1].Share(s.Headline()) {
		t.Error("the largest contributor does not hold the largest share")
	}
}

func TestASheetWithNothingInItSaysSoRatherThanReportingZero(t *testing.T) {
	var s Sheet
	refuses(t, s, "no counts were read")
	if s.Holds() || s.Killed() {
		t.Error("an empty sheet reached a verdict about the corpus")
	}
	// Killed on an empty sheet would read as a corpus that came in short, which
	// is the worst possible way to report that nothing was counted.
}

func TestReadingASheetOffDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "counts.jsonl")
	line := `{"source":"crawl","snapshot":"gao-2026-09","origin":"natural","class":"open","documents":500000000,"bytes":480000000000,"chars":360000000000,"syllables":105000000000,"tokens":200000000000,"tokenizer":"gao-64k"}`
	if err := os.WriteFile(path, []byte(line+"\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSheet("gao-v1.0", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Groups) != 1 {
		t.Fatalf("%d groups came off a file holding one", len(s.Groups))
	}
	if s.Tokenizer() != "gao-64k" || s.Snapshot() != "gao-2026-09" {
		t.Errorf("the sheet reads as %s off %s", s.Tokenizer(), s.Snapshot())
	}

	// A column nobody declared is a column gao does not know it is adding.
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"source":"crawl","words":12}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSheet("gao-v1.0", bad); err == nil {
		t.Error("a count carrying an undeclared column was read")
	}
	if _, err := ReadSheet("gao-v1.0", filepath.Join(dir, "nothing.jsonl")); err == nil {
		t.Error("a file that is not there read as a sheet")
	}
}
