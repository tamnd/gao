package gat

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/luat"
)

func TestTheBucketComesOutOfTheFileName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint8
	}{
		{"vie_Latn/10_1.jsonl.zst", 10},
		{"vie_Latn/5_1.jsonl.zst", 5},
		{"vie_Latn/nothing.jsonl.zst", 0},
		{"vie_Latn/999_1.jsonl.zst", 0},
		{"", 0},
	} {
		if got := hpltBucket(tc.in); got != tc.want {
			t.Errorf("hpltBucket(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// The score that goes in the column is the score for the partition being
// ingested, not the top of the ranking, because a document the identifier called
// English with Vietnamese in second place is not a Vietnamese document at 0.9.
func TestTheLanguageScoreIsTheScoreForWhatIsBeingIngested(t *testing.T) {
	for _, tc := range []struct {
		name  string
		langs []string
		probs []float32
		want  string
		score float32
	}{
		{"ranked first", []string{"vie_Latn", "ydd_Hebr"}, []float32{0.97, 0.01}, "vie", 0.97},
		{"ranked second", []string{"eng_Latn", "vie_Latn"}, []float32{0.6, 0.35}, "vie", 0.35},
		// Not in the ranking at all. It comes back as what the producer actually
		// said so the contract can reject it and the reject store can record what
		// it was, which is the only way the rate of this gets measured.
		{"not in the ranking", []string{"eng_Latn"}, []float32{0.99}, "eng", 0.99},
		{"a ranking with no probabilities", []string{"vie_Latn"}, nil, "vie", 0},
		{"no ranking at all", nil, nil, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lang, score := language(tc.langs, tc.probs, "vie_Latn")
			if lang != tc.want || score != tc.score {
				t.Errorf("language = %q, %v, want %q, %v", lang, score, tc.want, tc.score)
			}
		})
	}
}

// vie_Latn and vie-Latn are the same language written in the same script by two
// producers who disagree about punctuation.
func TestTheLanguageTagIsReadTheSameWhicheverProducerWroteIt(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"vie_Latn", "vie"},
		{"vie-Latn", "vie"},
		{"vie", "vie"},
		{"", ""},
	} {
		if got := code(tc.in); got != tc.want {
			t.Errorf("code(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A classifier that is not sure has said something, and writing down its best
// guess anyway would turn that into a fact.
func TestAClassifierBelowItsFloorIsRecordedAsSayingNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    map[string]float32
		floor float32
		want  string
	}{
		{"a clear winner", map[string]float32{"NA": 0.736, "IN": 0.307, "OP": 0.154}, 0.5, "NA"},
		{"nothing clears the floor", map[string]float32{"NA": 0.4, "IN": 0.39}, 0.5, ""},
		{"no output at all", nil, 0.5, ""},
		{"exactly at the floor", map[string]float32{"IN": 0.5}, 0.5, "IN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := topLabel(tc.in, tc.floor); got != tc.want {
				t.Errorf("topLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

// Map iteration order is not order, and two runs over the same shard have to
// produce the same column.
func TestATieBetweenTwoLabelsBreaksTheSameWayEveryTime(t *testing.T) {
	in := map[string]float32{"OP": 0.6, "IN": 0.6, "NA": 0.6}
	for i := range 50 {
		if got := topLabel(in, registerFloor); got != "IN" {
			t.Fatalf("run %d returned %q", i, got)
		}
	}
}

func TestTheStoredScoreIsTheMeanOfTheProducersSegments(t *testing.T) {
	if got := mean([]float32{10, 9.4}); got != 9.7 {
		t.Errorf("mean = %v, want 9.7", got)
	}
	if got := mean([]float32{5}); got != 5 {
		t.Errorf("mean = %v, want 5", got)
	}
}

// A timestamp that does not parse fails the row rather than being dropped,
// because fetched_at is a column the contract requires and a document silently
// missing one would be rejected for a reason that is not the real one.
func TestATimestampThatDoesNotParseFailsTheRow(t *testing.T) {
	p, f := hpltPin(t)
	line := strings.Replace(hpltLine, `"ts":"2017-06-29T15:42:29Z"`, `"ts":"29/06/2017"`, 1)

	dec, _ := DecoderFor(p.Source)
	err := dec.Decode(p, f, zstdOf(t, line+"\n"), func(*doc.Document) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "is not a timestamp") {
		t.Fatalf("Decode returned %v", err)
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Errorf("the error does not say which line: %v", err)
	}
}

// A record filed under vie_Latn whose own identifier ranked it as something else
// is not a Vietnamese document, whatever the file name says.
func TestARecordTheProducerDidNotCallVietnameseKeepsWhatItWasCalled(t *testing.T) {
	p, f := hpltPin(t)
	line := strings.Replace(hpltLine, `"lang":["vie_Latn","ydd_Hebr"]`, `"lang":["eng_Latn"]`, 1)
	line = strings.Replace(line, `"prob":[0.97,0.01]`, `"prob":[0.99]`, 1)

	d := decodeAll(t, p, f, zstdOf(t, line+"\n"))[0]
	if d.Lang != "eng" {
		t.Errorf("lang is %q, want the language the producer actually assigned", d.Lang)
	}
	if err := d.Admit(); err == nil {
		t.Error("a document the producer called English was admitted to a Vietnamese corpus")
	}
}

// The manifest carries a license class and so does the determination table. They
// are two hand written tables about the same six datasets, so a disagreement is a
// bug in one of them, and this is where it gets caught.
func TestTheManifestAndTheDeterminationTableAgreeAboutEverySource(t *testing.T) {
	for _, p := range Sources() {
		dets := luat.For(p.Source)
		if len(dets) == 0 {
			t.Errorf("%s is pinned and has no license determination", p.Source)
			continue
		}
		class, evidence := license(p)
		if class != p.Class {
			t.Errorf("%s is %s in the manifest and %s in luat", p.Source, p.Class, class)
		}
		if evidence == "" {
			t.Errorf("%s has no license evidence, so its class is a guess", p.Source)
		}
	}
}
