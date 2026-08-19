package pick

import (
	"strings"
	"testing"
)

func entry(name, version string) Entry {
	return Entry{Name: name, Version: version, Origin: Native}
}

func roster(entries ...Entry) Roster {
	return Roster{Version: "test", Benchmarks: entries}
}

func TestTheRosterInTheRepositoryLoads(t *testing.T) {
	ros, err := Rostered()
	if err != nil {
		t.Fatalf("the roster in the repository does not load: %v", err)
	}
	if ros.Version == "" {
		t.Error("the roster has no version")
	}
	if len(ros.Benchmarks) < 13 {
		t.Errorf("the roster holds %d benchmarks, and doc 10 names thirteen standard ones before gao's own", len(ros.Benchmarks))
	}
	var gate bool
	for _, e := range ros.Benchmarks {
		if e.Name == "vmlu" {
			gate = true
		}
	}
	if !gate {
		t.Error("VMLU is not on the roster, and it is the primary gate")
	}
}

func TestEveryRosteredBenchmarkSaysWhereItComesFromAndWhatWentIn(t *testing.T) {
	ros, err := Rostered()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ros.Benchmarks {
		if e.Source == "" {
			t.Errorf("%s does not say where its items come from, so the list cannot be rebuilt", e.Name)
		}
		if e.Note == "" {
			t.Errorf("%s does not say what part of an item goes into the list, which a contamination table cannot be read without", e.Name)
		}
	}
}

func TestTheRosterMarksTranslatedBenchmarksAsTranslated(t *testing.T) {
	ros, err := Rostered()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"mmlu-vi": true, "gsm8k-vi": true, "arc-vi": true, "hellaswag-vi": true}
	for _, e := range ros.Benchmarks {
		if want[e.Name] && e.Origin != Translated {
			t.Errorf("%s is marked %s, and a translated benchmark reported as native reads as a stronger result than it is", e.Name, e.Origin)
		}
	}
}

func TestTheBenchmarksBuiltOutOfTheCorpusAreMarkedHeldOut(t *testing.T) {
	ros, err := Rostered()
	if err != nil {
		t.Fatal(err)
	}
	held := 0
	for _, e := range ros.Benchmarks {
		if e.HeldOut {
			held++
			if e.dropAt() != 1 {
				t.Errorf("%s is held out and drops at %d, so its overlap would be reported rather than removed", e.Name, e.dropAt())
			}
		}
	}
	if held == 0 {
		t.Error("nothing on the roster is held out, and vi-cloze and vi-diacritic are built out of gao-web")
	}
}

func TestTheUnpinnedRevisionsAreNamed(t *testing.T) {
	ros, err := Rostered()
	if err != nil {
		t.Fatal(err)
	}
	// Most of the roster is unpinned today and a release cannot go out while it
	// is. What this asserts is that the roster can say which ones, because the
	// alternative to a list of names is finding out at release time.
	unpinned := ros.Unpinned()
	pinned := make(map[string]bool, len(unpinned))
	for _, name := range unpinned {
		if name == "" {
			t.Fatal("an unpinned benchmark came back without a name")
		}
		pinned[name] = true
	}
	for _, e := range ros.Benchmarks {
		if e.Version == Unpinned && !pinned[e.Name] {
			t.Errorf("%s has no revision and was not named as unpinned", e.Name)
		}
	}
	if !sorted(unpinned) {
		t.Errorf("the unpinned benchmarks came back unsorted: %v", unpinned)
	}
}

func sorted(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}

func TestARosterThatLostABenchmarkIsRejected(t *testing.T) {
	older := roster(entry("vmlu", "1"), entry("vinli", "1"))
	newer := roster(entry("vmlu", "1"))
	err := newer.Grew(older)
	if err == nil {
		t.Fatal("a benchmark came off the roster and nothing said so")
	}
	if !strings.Contains(err.Error(), "vinli") {
		t.Errorf("the error does not name the benchmark that went: %v", err)
	}
	if !strings.Contains(err.Error(), "only grows") {
		t.Errorf("the error does not say the rule that was broken: %v", err)
	}
}

func TestARosterThatGrewIsFine(t *testing.T) {
	older := roster(entry("vmlu", "1"))
	newer := roster(entry("vmlu", "1"), entry("vinli", "1"))
	if err := newer.Grew(older); err != nil {
		t.Errorf("adding a benchmark was rejected: %v", err)
	}
}

func TestARevisionThatMovedIsReportedRatherThanIgnored(t *testing.T) {
	older := roster(entry("uit-viquad", "1.0"))
	newer := roster(entry("uit-viquad", "2.0"))
	err := newer.Grew(older)
	if err == nil {
		t.Fatal("a benchmark changed revision and the check passed, so an earlier release looks checked against items it never saw")
	}
	if !strings.Contains(err.Error(), "1.0") || !strings.Contains(err.Error(), "2.0") {
		t.Errorf("the error does not carry both revisions: %v", err)
	}
}

func TestARosterThatLostOneBenchmarkAndMovedAnotherSaysBoth(t *testing.T) {
	older := roster(entry("vmlu", "1"), entry("vinli", "1"))
	newer := roster(entry("vmlu", "2"))
	err := newer.Grew(older)
	if err == nil {
		t.Fatal("both a removal and a revision change passed the check")
	}
	if !strings.Contains(err.Error(), "vinli") || !strings.Contains(err.Error(), "vmlu") {
		t.Errorf("the error names only one of the two problems: %v", err)
	}
}

func TestAListMissingARosteredBenchmarkFailsBeforeTheScan(t *testing.T) {
	ros := roster(entry("vmlu", "1"), entry("vinli", "1"))
	l := List{Version: "test", Benchmarks: []Benchmark{bench("vmlu", capital)}}
	err := l.Covers(ros)
	if err == nil {
		t.Fatal("a list missing a rostered benchmark passed, and that benchmark would come back clean without being checked")
	}
	if !strings.Contains(err.Error(), "vinli") {
		t.Errorf("the error does not name what is missing: %v", err)
	}
}

func TestAListAtTheWrongRevisionFails(t *testing.T) {
	ros := roster(entry("uit-viquad", "2.0"))
	l := List{Version: "test", Benchmarks: []Benchmark{bench("uit-viquad", capital)}}
	if err := l.Covers(ros); err == nil {
		t.Fatal("a list built from revision 1 passed against a roster naming revision 2")
	}
}

func TestAListBuiltFromAnOlderRosterFails(t *testing.T) {
	ros := roster(entry("vmlu", "1"))
	l := List{Version: "test", Roster: "2025-01-01", Benchmarks: []Benchmark{bench("vmlu", capital)}}
	err := l.Covers(ros)
	if err == nil {
		t.Fatal("a list built from a different roster passed")
	}
	if !strings.Contains(err.Error(), "rebuild") {
		t.Errorf("the error does not say what to do about it: %v", err)
	}
}

func TestACompleteListCovers(t *testing.T) {
	ros := roster(entry("vmlu", "1"), entry("vinli", "1"))
	l := List{
		Version:    "test",
		Roster:     "test",
		Benchmarks: []Benchmark{bench("vmlu", capital), bench("vinli", delta)},
	}
	if err := l.Covers(ros); err != nil {
		t.Errorf("a complete list was rejected: %v", err)
	}
}

func TestABenchmarkWithNoItemsIsRejected(t *testing.T) {
	l := List{Version: "test", Benchmarks: []Benchmark{{Entry: entry("vmlu", "1")}}}
	err := l.check()
	if err == nil {
		t.Fatal("a benchmark with no items was accepted, and it would be reported clean without being checked")
	}
	if !strings.Contains(err.Error(), "vmlu") {
		t.Errorf("the error does not name the empty benchmark: %v", err)
	}
}

func TestABenchmarkOnTheListTwiceIsRejected(t *testing.T) {
	l := List{Version: "test", Benchmarks: []Benchmark{bench("vmlu", capital), bench("vmlu", delta)}}
	if err := l.check(); err == nil {
		t.Error("the same benchmark twice was accepted, and its report would be split across two rows")
	}
}

func TestAListWithNoVersionIsRejected(t *testing.T) {
	_, err := DecodeList(strings.NewReader(`{"benchmarks":[{"name":"vmlu","version":"1","origin":"native","items":["x"]}]}`))
	if err == nil {
		t.Error("a list with no version was accepted, and a contamination table made from it names no list")
	}
}

func TestARosterWithAnUnknownOriginIsRejected(t *testing.T) {
	_, err := DecodeRoster(strings.NewReader(`{"version":"1","benchmarks":[{"name":"vmlu","version":"1","origin":"vietnamese"}]}`))
	if err == nil {
		t.Fatal("a benchmark with an origin nobody defined was accepted")
	}
	if !strings.Contains(err.Error(), "translated") {
		t.Errorf("the error does not say what the origins are: %v", err)
	}
}

func TestAFieldNobodyDefinedIsRejectedRatherThanIgnored(t *testing.T) {
	// A roster is edited by hand and a misspelled key that is silently dropped
	// is a benchmark that looks marked and is not.
	_, err := DecodeRoster(strings.NewReader(`{"version":"1","benchmarks":[{"name":"vmlu","version":"1","origin":"native","heldout":true}]}`))
	if err == nil {
		t.Error("a misspelled field was ignored, so a roster can say something the code never reads")
	}
}

func TestReadingARosterThatIsNotThereSaysSo(t *testing.T) {
	if _, err := ReadRoster("testdata/no-such-roster.json"); err == nil {
		t.Error("reading a roster that does not exist succeeded")
	}
	if _, err := ReadList("testdata/no-such-list.json"); err == nil {
		t.Error("reading a list that does not exist succeeded")
	}
}
