package mau_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tamnd/gao/mau"
	"github.com/tamnd/gao/tang"
)

// hplt is the source as it stands: ten buckets, five of them read at 40 MB each
// and five never opened. The sizes are the shape of the real thing, biggest in
// the middle of the ordering.
func hplt() []tang.Layer {
	stored := []int64{
		42_000_000_000, 55_000_000_000, 71_000_000_000, 96_000_000_000, 104_000_000_000,
		98_000_000_000, 88_000_000_000, 66_000_000_000, 49_000_000_000, 31_000_000_000,
	}
	read := map[int]bool{5: true, 7: true, 8: true, 9: true, 10: true}

	out := make([]tang.Layer, 0, len(stored))
	for i, n := range stored {
		l := tang.Layer{Name: fmt.Sprintf("bucket-%d", i+1), Rank: i + 1, Stored: n}
		if read[i+1] {
			l.Read = 40_000_000
			l.Text = 100_000_000
			l.Tokens = 26_000_000
			l.Tokenizer = "gemma-3"
		}
		out = append(out, l)
	}
	return out
}

// listing cuts every layer into shards of the given size, which for HPLT is a
// hundred or so of them at a bit under a gigabyte each, and the shards of a
// layer add up to what the layer says it holds.
func listing(layers []tang.Layer, each int64) []mau.File {
	var out []mau.File
	for _, l := range layers {
		for i, left := 0, l.Stored; left > 0; i++ {
			n := min(left, each)
			out = append(out, mau.File{
				Layer: l.Name,
				Path:  fmt.Sprintf("%s/%04d.jsonl.zst", l.Name, i),
				Bytes: n,
			})
			left -= n
		}
	}
	return out
}

func shards() int64 { return 900_000_000 }

func plan() mau.Plan {
	layers := hplt()
	return mau.ReadPlan("hplt-v3", "hplt-v3-2026-08", mau.Want, layers, listing(layers, shards()))
}

func says(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, want) {
			return
		}
	}
	t.Errorf("nothing says %q:\n  %s", want, strings.Join(lines, "\n  "))
}

func silent(t *testing.T, lines []string) {
	t.Helper()
	if len(lines) > 0 {
		t.Errorf("this should have said nothing:\n  %s", strings.Join(lines, "\n  "))
	}
}

func TestAPlanOverTheUnreadBucketsReadsOnlyThem(t *testing.T) {
	p := plan()

	silent(t, p.Blocking())
	silent(t, p.Faults())
	if !p.Holds() {
		t.Error("a plan that opens every unread layer does not hold")
	}
	if p.Lit != 5 || p.Opens != 5 || p.Shut != 0 {
		t.Errorf("%d layers read, %d opened, %d left shut, want 5, 5 and 0", p.Lit, p.Opens, p.Shut)
	}
	for _, r := range p.Reads {
		if r.Layer == "bucket-5" || r.Layer == "bucket-10" {
			t.Errorf("%s was already read and the plan reads it again", r.Layer)
		}
	}
	if read, of := p.Covers(); read != 10 || of != 10 {
		t.Errorf("the plan covers %d of %d layers, want 10 of 10", read, of)
	}
}

// The number this package exists for. Forty megabytes read off one shard and
// forty megabytes read off sixteen cost the same and answer different questions.
func TestTheReadingIsSpreadAcrossShardsRatherThanTakenOffOne(t *testing.T) {
	p := plan()

	for _, r := range p.Reads {
		if len(r.Takes) < mau.MinFiles {
			t.Errorf("%s is read off %d shards, want at least %d", r.Layer, len(r.Takes), mau.MinFiles)
		}
		if r.ListedShare < mau.MinListed {
			t.Errorf("%s was drawn from a listing covering %.1f%% of it", r.Layer, r.ListedShare*100)
		}
		for _, take := range r.Takes {
			if take.Bytes < mau.MinTake {
				t.Errorf("%s reads %d bytes off %s, under the %d minimum", r.Layer, take.Bytes, take.Path, mau.MinTake)
			}
		}
	}
}

// A plan that reads a little over its target rather than truncating its last
// file to hit it exactly, because the truncated read is the one that buys
// nothing.
func TestAPlanReadsItsTargetAndNotLessThanIt(t *testing.T) {
	p := plan()

	for _, r := range p.Reads {
		if r.Bytes < mau.Want {
			t.Errorf("%s reads %d bytes against a %d target", r.Layer, r.Bytes, mau.Want)
		}
		if r.Bytes > mau.Want+mau.MinTake*2 {
			t.Errorf("%s reads %d bytes against a %d target, which is not a plan sized to the target", r.Layer, r.Bytes, mau.Want)
		}
	}
	if p.Bytes < 5*mau.Want {
		t.Errorf("the plan reads %d bytes across five layers, against %d", p.Bytes, 5*mau.Want)
	}
}

func TestTheSameSeedDrawsTheSameShardsAndADifferentSeedDoesNot(t *testing.T) {
	layers := hplt()
	files := listing(layers, shards())

	first := mau.ReadPlan("hplt-v3", "hplt-v3-2026-08", mau.Want, layers, files)
	again := mau.ReadPlan("hplt-v3", "hplt-v3-2026-08", mau.Want, layers, files)
	other := mau.ReadPlan("hplt-v3", "hplt-v3-2026-09", mau.Want, layers, files)

	if first.Digest != again.Digest {
		t.Error("the same seed over the same listing drew a different plan the second time")
	}
	if first.Digest == other.Digest {
		t.Error("two seeds drew the same shards, so the seed decides nothing")
	}
	if first.Reads[0].Takes[0].Path == other.Reads[0].Takes[0].Path && first.Reads[0].Takes[1].Path == other.Reads[0].Takes[1].Path {
		t.Error("two seeds opened the same shards in the same order")
	}
}

// A listing whose order changed is the same plan, since the draw is by hash of
// the path rather than by position. This is what makes a plan checkable against
// a listing somebody re-exported.
func TestTheOrderOfTheListingDoesNotDecideTheDraw(t *testing.T) {
	layers := hplt()
	files := listing(layers, shards())
	reversed := make([]mau.File, len(files))
	for i, f := range files {
		reversed[len(files)-1-i] = f
	}

	if mau.ReadPlan("hplt-v3", "s", mau.Want, layers, files).Digest != mau.ReadPlan("hplt-v3", "s", mau.Want, layers, reversed).Digest {
		t.Error("the same listing in a different order produced a different plan")
	}
}

func TestALayerWithTooFewShardsIsNamedAsOne(t *testing.T) {
	layers := hplt()
	files := listing(layers, shards())
	// bucket-1 ships as four very large shards, which nothing about the plan
	// can spread a reading across.
	kept := files[:0:0]
	for _, f := range files {
		if f.Layer != "bucket-1" {
			kept = append(kept, f)
		}
	}
	for i := range 4 {
		kept = append(kept, mau.File{Layer: "bucket-1", Path: fmt.Sprintf("bucket-1/big-%d.jsonl.zst", i), Bytes: 10_000_000_000})
	}

	p := mau.ReadPlan("hplt-v3", "s", mau.Want, layers, kept)
	silent(t, p.Blocking())
	says(t, p.Faults(), "bucket-1 is read off 4 shards, under the 16 this plan spreads across")
	if p.Holds() {
		t.Error("a layer read off four shards holds")
	}
}

// An export that stopped early is the failure with no symptom. The plan it
// produces looks exactly like a proper one and every shard in it comes off the
// front of the bucket.
func TestAListingThatStoppedEarlyIsNamedRatherThanDrawnFrom(t *testing.T) {
	layers := hplt()
	files := listing(layers, shards())
	kept := files[:0:0]
	held := 0
	for _, f := range files {
		if f.Layer != "bucket-2" {
			kept = append(kept, f)
			continue
		}
		if held < 20 {
			kept = append(kept, f)
			held++
		}
	}

	p := mau.ReadPlan("hplt-v3", "s", mau.Want, layers, kept)
	silent(t, p.Blocking())
	for _, r := range p.Reads {
		if r.Layer == "bucket-2" && len(r.Takes) < mau.MinFiles {
			t.Errorf("bucket-2 is read off %d shards, so this is the spread fault rather than the listing one", len(r.Takes))
		}
	}
	says(t, p.Faults(), "the listing accounts for 18.0 GB of bucket-2 and the layer holds 55.0 GB, so the shards this plan draws from are 32.7% of it")
	if p.Holds() {
		t.Error("a plan drawn off a third of a bucket holds")
	}
}

func TestALayerTheListingDoesNotCoverStaysShutAndIsSaidSo(t *testing.T) {
	layers := hplt()
	files := listing(layers, shards())
	kept := files[:0:0]
	for _, f := range files {
		if f.Layer != "bucket-4" {
			kept = append(kept, f)
		}
	}

	p := mau.ReadPlan("hplt-v3", "s", mau.Want, layers, kept)
	silent(t, p.Blocking())
	if p.Shut != 1 || p.Opens != 4 {
		t.Errorf("%d layers stay shut and %d get opened, want 1 and 4", p.Shut, p.Opens)
	}
	says(t, p.Faults(), "1 layer holding 13.7% of the source stay")
	says(t, p.Faults(), "over the 10.0% tang allows")
	if read, of := p.Covers(); read != 9 || of != 10 {
		t.Errorf("the plan covers %d of %d layers, want 9 of 10", read, of)
	}
	// bucket-4 sits above three layers this plan opens, so leaving it shut
	// widens the bound rather than tilting it.
	for _, f := range p.Faults() {
		if strings.Contains(f, "sits below every layer this plan reads") {
			t.Errorf("a layer above three of the reads was called the leaning kind:\n  %s", f)
		}
	}
}

// The direction that flatters the number, which is the whole reason tang exists
// and the reason a plan says which end it leaves alone.
func TestTheLayerLeftShutUnderneathEverythingIsNamedAsTheLeaningKind(t *testing.T) {
	layers := hplt()
	files := listing(layers, shards())
	kept := files[:0:0]
	for _, f := range files {
		if f.Layer != "bucket-1" {
			kept = append(kept, f)
		}
	}

	faults := mau.ReadPlan("hplt-v3", "s", mau.Want, layers, kept).Faults()
	says(t, faults, "what stays shut sits below every layer this plan reads, 6.0% of it in 1 layer")
	for _, f := range faults {
		if strings.Contains(f, "tang allows") {
			t.Errorf("6%% left shut was reported as too much of the source:\n  %s", f)
		}
	}
}

func TestAReadingTooSmallForTangToUseIsNamedAsOne(t *testing.T) {
	layers := hplt()
	p := mau.ReadPlan("hplt-v3", "s", 4_000_000, layers, listing(layers, shards()))

	says(t, p.Faults(), "the plan reads 4.0 MB off each layer, under the 8.0 MB a layer's rate needs")
}

func TestTheRefusals(t *testing.T) {
	layers := hplt()
	files := listing(layers, shards())

	for _, c := range []struct {
		name string
		plan mau.Plan
		want string
	}{
		{"no seed", mau.ReadPlan("hplt-v3", "", mau.Want, layers, files),
			"the plan names no seed"},
		{"no source", mau.ReadPlan("", "s", mau.Want, layers, files),
			"does not say what source it reads"},
		{"nothing to read", mau.ReadPlan("hplt-v3", "s", 0, layers, files),
			"reads nothing off each layer"},
		{"no layers", mau.ReadPlan("hplt-v3", "s", mau.Want, nil, files),
			"published in layers and none of them are here"},
		{"no listing", mau.ReadPlan("hplt-v3", "s", mau.Want, layers, nil),
			"the listing holds no files"},
		{"a file with no path", mau.ReadPlan("hplt-v3", "s", mau.Want, layers,
			append(files[:len(files):len(files)], mau.File{Layer: "bucket-1", Bytes: 9})),
			"arrived with no path"},
		{"a file with no size", mau.ReadPlan("hplt-v3", "s", mau.Want, layers,
			append(files[:len(files):len(files)], mau.File{Layer: "bucket-1", Path: "bucket-1/empty.jsonl.zst"})),
			"is listed at no size"},
		{"a file listed twice", mau.ReadPlan("hplt-v3", "s", mau.Want, layers,
			append(files[:len(files):len(files)], files[0])),
			"is listed twice"},
		{"a layer the source does not have", mau.ReadPlan("hplt-v3", "s", mau.Want, layers,
			append(files[:len(files):len(files)], mau.File{Layer: "bucket-11", Path: "bucket-11/0000.jsonl.zst", Bytes: 9})),
			`the listing puts a file in a layer the source does not have: "bucket-11", on bucket-11/0000.jsonl.zst`},
		{"a layer with no rank", mau.ReadPlan("hplt-v3", "s", mau.Want,
			append(layers[:9:9], tang.Layer{Name: "bucket-10", Stored: 31_000_000_000}), files),
			"bucket-10 has no place in the ordering"},
		{"two layers at one rank", mau.ReadPlan("hplt-v3", "s", mau.Want,
			append(layers[:9:9], tang.Layer{Name: "bucket-10", Rank: 9, Stored: 31_000_000_000}), files),
			"both sit at rank 9"},
		{"a layer with no size", mau.ReadPlan("hplt-v3", "s", mau.Want,
			append(layers[:9:9], tang.Layer{Name: "bucket-10", Rank: 10}), files),
			"bucket-10 says it holds nothing on disk"},
	} {
		t.Run(c.name, func(t *testing.T) {
			says(t, c.plan.Blocking(), c.want)
			if c.plan.Holds() {
				t.Error("a plan nobody can run holds")
			}
			silent(t, c.plan.Faults())
		})
	}
}

func TestEveryLayerAlreadyReadIsNotAPlan(t *testing.T) {
	layers := hplt()
	for i := range layers {
		layers[i].Read = 40_000_000
		layers[i].Text = 100_000_000
		layers[i].Tokens = 26_000_000
		layers[i].Tokenizer = "gemma-3"
	}

	says(t, mau.ReadPlan("hplt-v3", "s", mau.Want, layers, listing(layers, shards())).Blocking(),
		"every layer was read already, so there is nothing here to plan")
}

// One bad listing writes the same fault onto every row it produced, and a
// refusal that repeats itself four hundred times is one nobody reads.
func TestABadListingIsOneSentencePerKind(t *testing.T) {
	layers := hplt()
	files := make([]mau.File, 0, 400)
	for i := range 400 {
		files = append(files, mau.File{Layer: "bucket-99", Path: fmt.Sprintf("bucket-99/%04d.jsonl.zst", i), Bytes: 9})
	}

	why := mau.ReadPlan("hplt-v3", "s", mau.Want, layers, files).Blocking()
	if len(why) != 1 {
		t.Errorf("%d sentences for one bad listing:\n  %s", len(why), strings.Join(why, "\n  "))
	}
	says(t, why, "400 files sit in layers the source does not have, the first of them")
}

func TestTheVerdictSaysWhatTheRunWillCostAndWhatItCloses(t *testing.T) {
	v := plan().Verdict()

	for _, want := range []string{
		"This plan reads 200.0 MB off 80 shards across 5 layers of 10 layers",
		"at seed hplt-v3-2026-08",
		"takes hplt-v3 from 5 of 10 layers read to 10 of 10",
		"no single stretch of the crawl carries its rate",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the verdict does not say %q:\n%s", want, v)
		}
	}
}

func TestTheVerdictOfAPlanNobodyCanRunIsTheRefusal(t *testing.T) {
	p := mau.ReadPlan("hplt-v3", "", mau.Want, hplt(), nil)

	if !strings.Contains(p.Verdict(), "names no seed") {
		t.Errorf("the verdict does not lead with the refusal: %s", p.Verdict())
	}
}
