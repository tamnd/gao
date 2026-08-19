package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

// reading is one shard compressed one way, filled in enough to be comparable.
func reading(shard string, order Ordering, raw, compressed int64) Reading {
	return Reading{
		Shard: shard, Ordering: order, Level: 19,
		Raw: raw, Compressed: compressed,
		Documents: 12_000, Hosts: 900, Biggest: 0.04, Box: "server3",
	}
}

// pair is one shard measured both ways, with the sorted run saving the fraction
// given.
func pair(shard string, saves float64) []Reading {
	const raw = 1_500_000_000
	arrival := int64(500_000_000)
	return []Reading{
		reading(shard, Arrival, raw, arrival),
		reading(shard, ByHost, raw, int64(float64(arrival)*(1-saves))),
	}
}

// The whole hypothesis, stated as the number it turns on: a hash shard
// scatters a host across the file and sorting puts it back together.
func TestSortingByHostCollapsesTheRunsAHashShardScatters(t *testing.T) {
	hosts := []string{"vnexpress.net", "tuoitre.vn", "thanhnien.vn", "dantri.com.vn"}
	records := make([]doc.Document, 0, 400)
	for i := range 400 {
		var d doc.Document
		d.Host = hosts[i%len(hosts)]
		d.URL = fmt.Sprintf("https://%s/%04d", d.Host, 400-i)
		records = append(records, d)
	}

	scattered := Runs(records)
	if scattered < 300 {
		t.Fatalf("400 documents from 4 hosts round robin came to %d runs", scattered)
	}
	SortByHost(records)
	if got := Runs(records); got != len(hosts) {
		t.Errorf("sorted by host, 4 hosts came to %d runs", got)
	}

	// Within a host, ordered by URL, because a section of a site sharing a
	// template is the same argument one level down.
	for i := 1; i < len(records); i++ {
		if records[i-1].Host != records[i].Host {
			continue
		}
		if records[i-1].URL > records[i].URL {
			t.Fatalf("%s came before %s", records[i-1].URL, records[i].URL)
		}
	}
}

func TestAnOrderingNobodyMeasuredAgainstTheOtherOneIsAPreference(t *testing.T) {
	only := Compare(512_000_000, []Reading{reading("shard-00004", ByHost, 1_500_000_000, 460_000_000)})
	if only.Settled() {
		t.Fatal("one ordering of one shard settled the question")
	}
	if !strings.Contains(strings.Join(only.Blocking(), "\n"), "only compressed sorted") {
		t.Errorf("a shard measured one way did not say so: %v", only.Blocking())
	}

	empty := Compare(512_000_000, nil)
	if empty.Settled() || !strings.Contains(empty.Verdict(), "is a preference") {
		t.Errorf("nothing measured came back as %q", empty.Verdict())
	}
	if empty.Shards(1e12) != 0 {
		t.Error("a shard count came out of a comparison with no ratio in it")
	}
}

// The saving has to beat what holding a shard resident costs, on the box with
// the least memory, or the ordering is not worth having.
func TestASavingBelowTheFloorDoesNotPayForHoldingAShardResident(t *testing.T) {
	readings := make([]Reading, 0, 10)
	for i := range 5 {
		readings = append(readings, pair(fmt.Sprintf("shard-%05d", i), 0.01)...)
	}
	c := Compare(512_000_000, readings)
	if c.Settled() {
		t.Fatal("a 1% saving settled the ordering")
	}
	if !strings.Contains(c.Verdict(), "is not worth that") {
		t.Errorf("the verdict does not price the saving against the memory: %s", c.Verdict())
	}
	if c.Resident <= c.Target {
		t.Errorf("sorting a %d byte shard was reported as needing %d bytes resident", c.Target, c.Resident)
	}
}

// One shard that is mostly one site saves a great deal and reproduces nowhere,
// which is what the median is for.
func TestTheFigureQuotedIsTheMiddleShardRatherThanTheMean(t *testing.T) {
	readings := pair("shard-00000", 0.05)
	readings = append(readings, pair("shard-00001", 0.06)...)
	readings = append(readings, pair("shard-00002", 0.07)...)
	readings = append(readings, pair("shard-00003", 0.06)...)
	outlier := pair("shard-00004", 0.55)
	for i := range outlier {
		outlier[i].Biggest = 0.62
	}
	readings = append(readings, outlier...)

	c := Compare(512_000_000, readings)
	if c.Median < 0.055 || c.Median > 0.065 {
		t.Errorf("the middle of five shards saving 5, 6, 6, 7 and 55 percent came back as %.3f", c.Median)
	}
	if !strings.Contains(strings.Join(c.Faults, "\n"), "what that site's boilerplate weighs") {
		t.Errorf("a shard that is 62%% one host was folded in without a word: %v", c.Faults)
	}
	if best := c.Gains[0]; best.Shard != "shard-00004" || !best.Worth() {
		t.Errorf("the gains are not ordered by what they saved: %+v", c.Gains[0])
	}
}

// A comparison across two compression levels is a comparison of the levels.
func TestTwoCompressionLevelsIsNotAComparisonOfOrderings(t *testing.T) {
	readings := pair("shard-00000", 0.08)
	readings[1].Level = 3
	c := Compare(512_000_000, readings)
	if c.Settled() {
		t.Fatal("readings from two levels settled the ordering")
	}
	if !strings.Contains(strings.Join(c.Blocking(), "\n"), "a measurement of the levels") {
		t.Errorf("two levels were folded in without a word: %v", c.Blocking())
	}
}

// A reading missing what it needs to be compared is refused rather than
// compared with the missing part assumed.
func TestAReadingWithoutItsEvidenceIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		of   func(Reading) Reading
		says string
	}{
		{"no shard", func(r Reading) Reading { r.Shard = ""; return r }, "compare the shards"},
		{"no level", func(r Reading) Reading { r.Level = 0; return r }, "moves the ratio further than the ordering does"},
		{"no box", func(r Reading) Reading { r.Box = ""; return r }, "is an estimate"},
		{"nothing compressed", func(r Reading) Reading { r.Compressed = 0; return r }, "not a compression ratio"},
		{"not an ordering", func(r Reading) Reading { r.Ordering = "by size"; return r }, "is not an ordering"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why := tc.of(reading("shard-00000", ByHost, 1_500_000_000, 460_000_000)).Blocking()
			if !strings.Contains(strings.Join(why, "\n"), tc.says) {
				t.Errorf("%v", why)
			}
		})
	}

	// Two readings of the same shard in the same ordering do not have to agree,
	// so neither of them is the ratio for it.
	twice := append(pair("shard-00000", 0.08), reading("shard-00000", ByHost, 1_500_000_000, 400_000_000))
	if c := Compare(512_000_000, twice); !strings.Contains(strings.Join(c.Faults, "\n"), "there is no one ratio for it") {
		t.Errorf("the same shard compressed the same way twice passed: %v", c.Faults)
	}

	// And two readings that do not hold the same bytes are not of one shard.
	mismatched := pair("shard-00000", 0.08)
	mismatched[1].Raw = 900_000_000
	if c := Compare(512_000_000, mismatched); !strings.Contains(strings.Join(c.Faults, "\n"), "not of the same shard") {
		t.Errorf("two different shards were compared under one name: %v", c.Faults)
	}
}

// One box is a run rather than a measurement, which is the fleet gate on this
// milestone stated as arithmetic.
func TestOneBoxIsARunRatherThanAMeasurement(t *testing.T) {
	readings := pair("shard-00000", 0.08)
	readings = append(readings, pair("shard-00001", 0.09)...)
	one := Compare(512_000_000, readings)
	if one.Settled() {
		t.Fatal("readings off one box settled the ratio the disk budget is written against")
	}
	if !strings.Contains(strings.Join(one.Blocking(), "\n"), "wants a second box") {
		t.Errorf("one box passed without a word: %v", one.Blocking())
	}

	second := pair("shard-00002", 0.08)
	for i := range second {
		second[i].Box = "gamingpc"
	}
	two := Compare(512_000_000, append(readings, second...))
	if !two.Settled() {
		t.Fatalf("two boxes and three shards did not settle it: %v", two.Blocking())
	}
	if !strings.Contains(two.Verdict(), "resident while a shard is being written") {
		t.Errorf("the verdict does not say what the ordering costs: %s", two.Verdict())
	}
}

// The point of measuring the ratio is that the shard count is downstream of it,
// and the shard count is what the release is shaped like.
func TestTheShardCountFollowsFromTheMeasuredRatioRatherThanTheAssumedOne(t *testing.T) {
	readings := pair("shard-00000", 0.08)
	readings = append(readings, pair("shard-00001", 0.09)...)
	for i := range readings {
		if i%2 == 1 {
			readings[i].Box = "gamingpc"
		}
	}
	c := Compare(512_000_000, readings)

	// 1.5 GB of text down to 460 MB is a shade over 3.2 to 1.
	if c.Ratio < 3.0 || c.Ratio > 3.5 {
		t.Fatalf("the measured ratio came back as %.2f", c.Ratio)
	}
	const text = 1_200_000_000_000
	got := c.Shards(text)
	if got < 600 || got > 900 {
		t.Errorf("1.2 TB of text at %.2f to 1 in 512 MB shards came to %d shards", c.Ratio, got)
	}

	// A worse ratio is more shards, which is the only relationship here that has
	// to hold whatever the numbers are.
	worse := c
	worse.Ratio = c.Ratio / 2
	if worse.Shards(text) <= got {
		t.Errorf("half the compression came to %d shards against %d", worse.Shards(text), got)
	}
}
