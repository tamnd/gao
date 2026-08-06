package kho

// The order records are written in inside a shard, which is worth about as much
// as a compression level and costs a great deal more.
//
// Shards are assigned by hash, and that is the right choice for every other
// reason: a hash shard is a uniform sample of the corpus, so a stage that
// processes one shard sees what a stage processing all of them sees, and two
// copies of a document land together by construction. What it costs is
// compression. Pages from one host share their navigation, their footer, their
// cookie banner, their breadcrumb, and their URL prefix, and a hash shard
// scatters those pages so that no two of them are ever inside the same zstd
// window. The compressor is shown the same boilerplate a hundred times and told
// nothing about it each time.
//
// Sorting by host inside the shard puts them back together without changing
// which shard anything is in. The sample property survives, because it is a
// property of the assignment rather than of the order, and the compressor gets
// to see the repetition.
//
// What it costs is that a stream stops being a stream. A shard cannot be sorted
// until every record for it is in hand, so the writer holds the shard's records
// in memory, orders them, and only then compresses. At the target of 512 MB
// compressed that is somewhere around a gigabyte and a half of raw text
// resident, on a fleet whose smallest box has 6.2 GB total. So the saving is
// worth having only if somebody measured it, on the same shard, both ways, at
// the same compression level, on a box we own.

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/may"
)

// An Ordering is how records are arranged inside a shard before compression.
type Ordering string

const (
	// Arrival is the order the pipeline produced them in, which is the order a
	// streaming writer writes.
	Arrival Ordering = "arrival"

	// ByHost groups every page from one host together, and orders the pages
	// within a host by URL so that a section of a site is contiguous too.
	ByHost Ordering = "host"
)

// Orderings is both of them, in the order they get compared.
var Orderings = []Ordering{Arrival, ByHost}

// MinGain is the smallest saving that justifies holding a shard resident in
// order to sort it. Below this the ordering is costing a gigabyte and a half of
// memory on the box with the least of it, in exchange for a rounding error on
// the download somebody does once.
const MinGain = 0.03

// Dominant is the share of one shard one host may hold before the shard has
// stopped being a sample of the corpus and become a copy of a website. A gain
// measured on a shard like that is a measurement of that site.
const Dominant = 0.25

// SortByHost orders records in place so that pages from one host are contiguous
// and, within a host, ordered by URL.
//
// It is a stable sort on the two fields and nothing else. In particular it does
// not order by size, by score, or by date, because any of those interleaves
// hosts again and undoes the only thing this is for.
func SortByHost(records []doc.Document) {
	slices.SortStableFunc(records, func(a, b doc.Document) int {
		if n := strings.Compare(a.Host, b.Host); n != 0 {
			return n
		}
		return strings.Compare(a.URL, b.URL)
	})
}

// Runs is how many times the host changes reading down a shard, which is the
// thing sorting is trying to reduce. A shard of 100,000 documents from 900
// hosts has 900 runs sorted and close to 100,000 unsorted, and the difference
// between those two numbers is the whole hypothesis.
func Runs(records []doc.Document) int {
	var runs int
	last := ""
	for i, r := range records {
		if i == 0 || r.Host != last {
			runs++
		}
		last = r.Host
	}
	return runs
}

// A Reading is one shard compressed one way, by one box, at one level.
type Reading struct {
	// Shard names the shard, and the two orderings have to be readings of the
	// same one. Compressing shard 4 sorted against shard 9 unsorted compares the
	// shards.
	Shard string `json:"shard"`

	Ordering Ordering `json:"ordering"`

	// Level is the zstd level. A comparison across two levels is a comparison of
	// the levels, which is a different measurement wearing this one's name.
	Level int `json:"level"`

	// Raw and Compressed are the shard's bytes before and after.
	Raw        int64 `json:"raw"`
	Compressed int64 `json:"compressed"`

	// Documents and Hosts say what the shard is made of, and Biggest is the
	// share of it held by the host that holds the most.
	Documents int     `json:"documents"`
	Hosts     int     `json:"hosts"`
	Biggest   float64 `json:"biggest,omitempty"`

	// Box is where it was compressed. Compression is deterministic, so this is
	// not here for reproducibility. It is here because a ratio nobody ran on a
	// real box is an estimate, and the fleet gate on this milestone is the whole
	// reason the distinction is worth keeping.
	Box string `json:"box"`
}

// Ratio is uncompressed bytes per compressed byte, which is the number the disk
// budget is written in.
func (r Reading) Ratio() float64 {
	if r.Compressed <= 0 {
		return 0
	}
	return float64(r.Raw) / float64(r.Compressed)
}

// Blocking is every reason this reading cannot be compared with another.
func (r Reading) Blocking() []string {
	var out []string
	if r.Shard == "" {
		out = append(out, "a reading that does not say which shard it is of, and two orderings of two different shards compare the shards")
	}
	if !slices.Contains(Orderings, r.Ordering) {
		out = append(out, fmt.Sprintf("%q is not an ordering this writes shards in", r.Ordering))
	}
	if r.Raw <= 0 || r.Compressed <= 0 {
		out = append(out, fmt.Sprintf("%s came back at %d bytes compressed from %d, which is not a compression ratio", r.Shard, r.Compressed, r.Raw))
	}
	if r.Level == 0 {
		out = append(out, fmt.Sprintf("%s does not say what level it was compressed at, and the level moves the ratio further than the ordering does", r.Shard))
	}
	if r.Box == "" {
		out = append(out, fmt.Sprintf("%s does not say which box compressed it, so the ratio the disk budget gets written against is an estimate", r.Shard))
	}
	return out
}

// A Gain is one shard measured both ways.
type Gain struct {
	Shard    string  `json:"shard"`
	Arrival  Reading `json:"arrival"`
	Sorted   Reading `json:"sorted"`
	Saved    int64   `json:"saved"`
	Fraction float64 `json:"fraction"`
}

// Worth reports whether the saving justifies holding the shard resident.
func (g Gain) Worth() bool { return g.Fraction >= MinGain }

// A Comparison is a set of readings folded into what the ordering bought.
type Comparison struct {
	Gains []Gain `json:"gains"`

	// Median is the middle shard's saving, which is the figure to quote. The
	// mean is not, because one shard that is mostly one host saves forty percent
	// and drags an average nobody can reproduce on any other shard.
	Median float64 `json:"median"`

	// Ratio is the compression ratio under the ordering that gets shipped, which
	// is what replaces the assumed figure in the disk budget.
	Ratio float64 `json:"ratio"`

	// Target is the compressed size each shard is aimed at, and Resident is what
	// one shard needs in memory to be sorted before it is written, which is the
	// target scaled back up by the measured ratio.
	Target   int64 `json:"target"`
	Resident int64 `json:"resident"`

	Boxes  []string `json:"boxes"`
	Faults []string `json:"faults,omitempty"`
}

// Compare folds readings into one gain per shard measured both ways.
func Compare(target int64, readings []Reading) Comparison {
	c := Comparison{Target: target}

	byShard := map[string]map[Ordering]Reading{}
	levels := map[int]bool{}
	boxes := map[string]bool{}
	for _, r := range readings {
		if why := r.Blocking(); len(why) > 0 {
			c.Faults = append(c.Faults, why...)
			continue
		}
		if byShard[r.Shard] == nil {
			byShard[r.Shard] = map[Ordering]Reading{}
		}
		if _, twice := byShard[r.Shard][r.Ordering]; twice {
			c.Faults = append(c.Faults, fmt.Sprintf(
				"%s was compressed %s twice and the two runs do not have to agree, so there is no one ratio for it", r.Shard, r.Ordering))
			continue
		}
		byShard[r.Shard][r.Ordering] = r
		levels[r.Level] = true
		boxes[r.Box] = true
	}
	c.Boxes = keys(boxes)

	if len(levels) > 1 {
		c.Faults = append(c.Faults, fmt.Sprintf(
			"the readings come off %d different compression levels, and a saving measured across two levels is a measurement of the levels", len(levels)))
	}

	for _, shard := range keys(byShard) {
		pair := byShard[shard]
		a, gotArrival := pair[Arrival]
		s, gotSorted := pair[ByHost]
		switch {
		case !gotArrival:
			c.Faults = append(c.Faults, fmt.Sprintf("%s was only compressed sorted, so there is nothing to say what the sorting bought", shard))
			continue
		case !gotSorted:
			c.Faults = append(c.Faults, fmt.Sprintf("%s was only compressed in arrival order, so it is not part of this comparison", shard))
			continue
		}
		if a.Raw != s.Raw {
			c.Faults = append(c.Faults, fmt.Sprintf(
				"%s holds %s of text in one ordering and %s in the other, so the two readings are not of the same shard", shard, may.Size(a.Raw), may.Size(s.Raw)))
			continue
		}
		if big := max(a.Biggest, s.Biggest); big > Dominant {
			c.Faults = append(c.Faults, fmt.Sprintf(
				"%.0f%% of %s is one host, so what sorting saves on it is what that site's boilerplate weighs rather than what the ordering is worth", 100*big, shard))
		}
		g := Gain{Shard: shard, Arrival: a, Sorted: s, Saved: a.Compressed - s.Compressed}
		g.Fraction = float64(g.Saved) / float64(a.Compressed)
		c.Gains = append(c.Gains, g)
	}
	slices.SortStableFunc(c.Gains, func(x, y Gain) int { return cmp.Compare(y.Fraction, x.Fraction) })

	if len(c.Gains) > 0 {
		by := slices.Clone(c.Gains)
		slices.SortStableFunc(by, func(x, y Gain) int { return cmp.Compare(x.Fraction, y.Fraction) })
		c.Median = by[len(by)/2].Fraction
		c.Ratio = by[len(by)/2].Sorted.Ratio()
		if c.Ratio > 0 {
			c.Resident = int64(float64(target) * c.Ratio)
		}
	}
	return c
}

// Shards is how many shards a corpus of n bytes of text comes to at the target
// size, under the ratio this comparison measured rather than the one the disk
// budget assumed.
func (c Comparison) Shards(text int64) int {
	if c.Ratio <= 0 || c.Target <= 0 {
		return 0
	}
	compressed := int64(float64(text) / c.Ratio)
	return int((compressed + c.Target - 1) / c.Target)
}

// Blocking is every reason this comparison does not settle the ordering.
func (c Comparison) Blocking() []string {
	out := slices.Clone(c.Faults)
	if len(c.Gains) == 0 {
		return append(out, "no shard was compressed both ways, and an ordering nobody measured against the other one is a preference")
	}
	if c.Median < MinGain {
		out = append(out, fmt.Sprintf(
			"sorting by host saves %.1f%% on the middle shard against a floor of %.0f%%, and holding %s resident to sort a shard is not worth that",
			100*c.Median, 100*MinGain, may.Size(c.Resident)))
	}
	if len(c.Boxes) == 1 && c.Boxes[0] != "" {
		out = append(out, fmt.Sprintf(
			"every reading came off %s, and the ratio the whole disk budget is written against wants a second box before it is a measurement rather than a run", c.Boxes[0]))
	}
	return out
}

// Settled reports whether the ordering question is answered.
func (c Comparison) Settled() bool { return len(c.Blocking()) == 0 }

// Verdict is the comparison in one sentence.
func (c Comparison) Verdict() string {
	if why := c.Blocking(); len(why) > 0 {
		return why[0]
	}
	return fmt.Sprintf(
		"host sorting saves %.1f%% on the middle of %s and compresses Vietnamese %.2f to 1, at the cost of %s resident while a shard is being written",
		100*c.Median, plural(len(c.Gains), "shard"), c.Ratio, may.Size(c.Resident))
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ReadReadings reads one JSON reading per line.
func ReadReadings(path string) ([]Reading, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Reading
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r Reading
		d := json.NewDecoder(strings.NewReader(line))
		d.DisallowUnknownFields()
		if err := d.Decode(&r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: it holds no readings", path)
	}
	return out, nil
}
