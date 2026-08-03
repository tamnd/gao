package doc

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestSumIsStable(t *testing.T) {
	// The digest of this string is baked into the test on purpose. Document
	// identity is content addressed, so a change in the hash function silently
	// renames every document in every published snapshot, and that has to be a
	// deliberate act with a version bump rather than a dependency upgrade.
	const want = "2c204b40c41fd6a53ff8890b54bc3eb774fcd3f9e415f6f8b7ac1dcb6383e09c"
	if got := SumString("Cộng hòa xã hội chủ nghĩa Việt Nam").String(); got != want {
		t.Errorf("blake3 of the reference string is %s, want %s", got, want)
	}
}

func TestHashRoundTripsThroughJSON(t *testing.T) {
	type row struct {
		ID Hash `json:"id"`
	}
	in := row{ID: SumString("phở")}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), in.ID.String()) {
		t.Errorf("hash did not marshal as hex: %s", b)
	}
	var out row
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != in.ID {
		t.Errorf("round trip gave %s, want %s", out.ID, in.ID)
	}
}

func TestParseHashRejectsBadInput(t *testing.T) {
	for _, s := range []string{
		"abc",
		strings.Repeat("a", 63),
		strings.Repeat("a", 65),
		strings.Repeat("z", 64),
	} {
		if _, err := ParseHash(s); err == nil {
			t.Errorf("ParseHash(%q) succeeded", s)
		}
	}
	if h, err := ParseHash(""); err != nil || !h.IsZero() {
		t.Errorf("ParseHash(\"\") = %s, %v, want the zero hash and no error", h, err)
	}
}

func TestShardIsDeterministicAndInRange(t *testing.T) {
	const n = 750
	id := SumString("một tài liệu")
	first := Shard(id, n)
	for range 100 {
		if got := Shard(id, n); got != first {
			t.Fatalf("Shard is not deterministic: got %d then %d", first, got)
		}
	}
	if first < 0 || first >= n {
		t.Fatalf("Shard returned %d, outside [0, %d)", first, n)
	}
}

func TestShardSpreadsEvenly(t *testing.T) {
	// Not a randomness test, a wiring test. If the shard function accidentally
	// returned a constant or keyed off something with structure, the corpus
	// would land in a handful of shards and every downstream size assumption
	// would be wrong. A chi-square style spread check catches that immediately
	// and does not care about the details.
	const (
		n    = 64
		docs = 64000
	)
	counts := make([]int, n)
	for i := range docs {
		counts[Shard(SumString(string(rune('a'+i%26))+strings.Repeat("x", i%97)+itoa(i)), n)]++
	}
	expected := float64(docs) / n
	var chi2 float64
	for _, c := range counts {
		d := float64(c) - expected
		chi2 += d * d / expected
	}
	// 63 degrees of freedom: the 0.999 critical value is about 112. A uniform
	// hash lands well under that and a broken one lands orders of magnitude over.
	if chi2 > 112 || math.IsNaN(chi2) {
		t.Errorf("shard distribution is not uniform: chi-square %.1f over %d shards", chi2, n)
	}
}

func TestShardPanicsOnNonPositiveCount(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Shard(id, 0) did not panic")
		}
	}()
	Shard(Hash{}, 0)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
