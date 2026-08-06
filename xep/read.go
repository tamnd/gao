package xep

// Reading the labels back: how much of the draw got placed, how much of it got
// placed twice, and whether the two people who placed it agreed.

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
)

// A BandCount is one band's share of the labeled set.
type BandCount struct {
	Band      Band    `json:"band"`
	Documents int     `json:"documents"`
	Share     float64 `json:"share"`
}

// A SourceCount is one slice of the draw against what the frame asked for.
type SourceCount struct {
	Source  string  `json:"source"`
	Wanted  int     `json:"wanted"`
	Labeled int     `json:"labeled"`
	Share   float64 `json:"share"`
	Asked   float64 `json:"asked"`
}

// A LabelerCount is one person's contribution, because a reference set that is
// mostly one person's judgment is one person's judgment.
type LabelerCount struct {
	By        string  `json:"by"`
	Labels    int     `json:"labels"`
	Share     float64 `json:"share"`
	Disagreed int     `json:"disagreed"`
}

// A Score is what the labeling produced.
type Score struct {
	Frame   doc.Hash `json:"frame"`
	Version string   `json:"version"`

	// Wanted is what the frame draws and Labeled is how much of it came back.
	Wanted  int `json:"wanted"`
	Labeled int `json:"labeled"`
	Labels  int `json:"labels"`

	// Double is documents two or more people placed, and Pairs is how many
	// two person comparisons that produced.
	Double     int     `json:"double"`
	DoubleRate float64 `json:"double_rate"`
	Pairs      int     `json:"pairs"`

	// Exact is the share of those comparisons where both people chose the same
	// band. Adjacent is the share where they were within one band, reported apart
	// because a rubric that lands people next to each other has a boundary problem
	// and a rubric that lands them two apart has a scale problem.
	Exact    float64 `json:"exact"`
	Adjacent float64 `json:"adjacent"`

	ByBand   []BandCount    `json:"by_band"`
	BySource []SourceCount  `json:"by_source"`
	ByPerson []LabelerCount `json:"by_person"`

	Passed bool `json:"passed"`

	Unknown   []string `json:"unknown,omitempty"`
	OffScale  []string `json:"off_scale,omitempty"`
	Repeat    []string `json:"repeat,omitempty"`
	Elsewhere []string `json:"elsewhere,omitempty"`
}

// Read scores the labels against the frame. Nothing is refused here; every
// disagreement between the labels and the frame comes back as a field.
func (f Frame) Read(labels []Label) Score {
	digest := f.Digest()
	sc := Score{Frame: digest, Version: f.Version, Wanted: f.Size}

	byDoc := map[doc.Hash][]Label{}
	seenBy := map[string]bool{}
	unknown := map[string]bool{}
	offScale := map[string]bool{}
	for _, l := range labels {
		if _, ok := f.Slice(l.Source); !ok {
			unknown[l.Source] = true
			continue
		}
		if slices.Index(Bands, l.Band) < 0 {
			offScale[string(l.Band)] = true
			continue
		}
		if !l.Frame.IsZero() && l.Frame != digest {
			sc.Elsewhere = append(sc.Elsewhere, l.Doc.String())
			continue
		}
		key := l.Doc.String() + "\x00" + l.By
		if seenBy[key] {
			sc.Repeat = append(sc.Repeat, l.Doc.String())
			continue
		}
		seenBy[key] = true
		byDoc[l.Doc] = append(byDoc[l.Doc], l)
		sc.Labels++
	}
	sc.Unknown, sc.OffScale = sorted(unknown), sorted(offScale)
	slices.Sort(sc.Repeat)
	slices.Sort(sc.Elsewhere)

	bands := map[Band]int{}
	sources := map[string]int{}
	people := map[string]*LabelerCount{}
	var exact, adjacent int
	for _, ls := range byDoc {
		sc.Labeled++
		// The band of record is the first label, which is the one the labeling
		// pass produced. A second opinion measures the rubric rather than
		// overruling the first person, since a set where disagreements are
		// silently resolved has no disagreements left to report.
		bands[ls[0].Band]++
		sources[ls[0].Source]++
		for _, l := range ls {
			p, ok := people[l.By]
			if !ok {
				p = &LabelerCount{By: l.By}
				people[l.By] = p
			}
			p.Labels++
		}
		if len(ls) < 2 {
			continue
		}
		sc.Double++
		for i := range ls {
			for j := i + 1; j < len(ls); j++ {
				sc.Pairs++
				switch Distance(ls[i].Band, ls[j].Band) {
				case 0:
					exact++
					adjacent++
				case 1:
					adjacent++
					people[ls[i].By].Disagreed++
					people[ls[j].By].Disagreed++
				default:
					people[ls[i].By].Disagreed++
					people[ls[j].By].Disagreed++
				}
			}
		}
	}

	if sc.Labeled > 0 {
		sc.DoubleRate = float64(sc.Double) / float64(sc.Labeled)
	}
	if sc.Pairs > 0 {
		sc.Exact = float64(exact) / float64(sc.Pairs)
		sc.Adjacent = float64(adjacent) / float64(sc.Pairs)
	}

	for _, b := range Bands {
		bc := BandCount{Band: b, Documents: bands[b]}
		if sc.Labeled > 0 {
			bc.Share = float64(bands[b]) / float64(sc.Labeled)
		}
		sc.ByBand = append(sc.ByBand, bc)
	}
	for _, sl := range f.Slices {
		s := SourceCount{Source: sl.Source, Wanted: sl.Wanted(f.Size), Labeled: sources[sl.Source], Asked: sl.Share}
		if sc.Labeled > 0 {
			s.Share = float64(sources[sl.Source]) / float64(sc.Labeled)
		}
		sc.BySource = append(sc.BySource, s)
	}
	for _, p := range people {
		if sc.Labels > 0 {
			p.Share = float64(p.Labels) / float64(sc.Labels)
		}
		sc.ByPerson = append(sc.ByPerson, *p)
	}
	slices.SortStableFunc(sc.ByPerson, func(a, b LabelerCount) int {
		if n := cmp.Compare(b.Labels, a.Labels); n != 0 {
			return n
		}
		return strings.Compare(a.By, b.By)
	})

	sc.Passed = sc.Labeled >= f.Size &&
		len(sc.Unknown) == 0 && len(sc.OffScale) == 0 && len(sc.Repeat) == 0 && len(sc.Elsewhere) == 0 &&
		sc.DoubleRate >= Double && sc.Exact >= MinExact && sc.Adjacent >= MinAdjacent
	return sc
}

// Publishable is every reason this labeling does not go out as it stands.
func (sc Score) Publishable() []string {
	var out []string
	if sc.Labeled < sc.Wanted {
		out = append(out, fmt.Sprintf("%s of the %d the frame draws came back, and a set short of its own draw is a set drawn from whatever got finished",
			plural(sc.Labeled, "document"), sc.Wanted))
	}
	if sc.DoubleRate < Double {
		out = append(out, fmt.Sprintf("%.1f%% of the documents were placed twice and %.0f%% is the floor, so the agreement number below is an estimate off a sample too small to tell a rubric that works from a labeler having a good week",
			100*sc.DoubleRate, 100*Double))
	}
	if sc.Pairs > 0 && sc.Exact < MinExact {
		out = append(out, fmt.Sprintf("two people chose the same band %.1f%% of the time against a floor of %.0f%%, which says the rubric is not deciding the band and the labeler is",
			100*sc.Exact, 100*MinExact))
	}
	if sc.Pairs > 0 && sc.Adjacent < MinAdjacent {
		out = append(out, fmt.Sprintf("two people landed more than one band apart %.1f%% of the time, and a scale people can miss by two steps is four words in a list rather than a scale",
			100*(1-sc.Adjacent)))
	}
	for _, b := range sc.ByBand {
		if b.Documents == 0 {
			out = append(out, fmt.Sprintf("nothing was placed in %s, and a band nobody used is a band that is not in the rubric whatever the rubric says", b.Band))
		}
	}
	for _, s := range sc.BySource {
		if s.Labeled == 0 {
			out = append(out, fmt.Sprintf("nothing came back from %s, which the frame draws %.0f%% of", s.Source, 100*s.Asked))
			continue
		}
		if d := s.Share - s.Asked; d > 0.05 || d < -0.05 {
			out = append(out, fmt.Sprintf("%s is %.1f%% of the labeled set and the frame asked for %.0f%%, so the set labels a different corpus than the one it was drawn against",
				s.Source, 100*s.Share, 100*s.Asked))
		}
	}
	if len(sc.ByPerson) == 1 && sc.Labels > 0 {
		out = append(out, fmt.Sprintf("every label is %s's, so there is no agreement here to report and the rubric has been tested against one person's reading of it", sc.ByPerson[0].By))
	}
	if len(sc.Unknown) > 0 {
		out = append(out, fmt.Sprintf("%s the frame does not draw from: %s, which is a document that got labeled because somebody had it open",
			plural(len(sc.Unknown), "label names a source"), strings.Join(sc.Unknown, ", ")))
	}
	if len(sc.OffScale) > 0 {
		out = append(out, fmt.Sprintf("%s off the scale: %s, and a band invented during labeling is a rubric change with no digest on it",
			plural(len(sc.OffScale), "label uses a band"), strings.Join(sc.OffScale, ", ")))
	}
	if len(sc.Repeat) > 0 {
		out = append(out, fmt.Sprintf("%s placed the same document twice, and a person agreeing with themselves is not agreement: %s",
			plural(len(sc.Repeat), "labeler"), join(sc.Repeat)))
	}
	if len(sc.Elsewhere) > 0 {
		out = append(out, fmt.Sprintf("%s against a different frame: %s, so the rubric moved between the labeling and the reading",
			plural(len(sc.Elsewhere), "document was placed"), join(sc.Elsewhere)))
	}
	return out
}

// Worst is the sources furthest from the share the frame asked for.
func (sc Score) Worst() []SourceCount {
	out := slices.Clone(sc.BySource)
	slices.SortStableFunc(out, func(a, b SourceCount) int {
		return cmp.Compare(off(b), off(a))
	})
	return out
}

func off(s SourceCount) float64 {
	return math.Abs(s.Share - s.Asked)
}

// join writes a list of document identities short enough to read, and says how
// many it did not write.
func join(ids []string) string {
	const most = 5
	if len(ids) <= most {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(ids[:most], ", "), len(ids)-most)
}

func sorted(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ReadFrame reads a frame from JSON, for checking one that is not the fixed one.
func ReadFrame(path string) (Frame, error) {
	f, err := os.Open(path)
	if err != nil {
		return Frame{}, err
	}
	defer func() { _ = f.Close() }()

	var fr Frame
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(&fr); err != nil {
		return Frame{}, fmt.Errorf("%s: %w", path, err)
	}
	return fr, nil
}

// ReadLabels reads one JSON label per line.
func ReadLabels(path string) ([]Label, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Label
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var l Label
		d := json.NewDecoder(strings.NewReader(line))
		d.DisallowUnknownFields()
		if err := d.Decode(&l); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if l.Doc.IsZero() {
			return nil, fmt.Errorf("%s:%d: %w: a label with no document on it", path, n, ErrBadLabels)
		}
		if strings.TrimSpace(l.By) == "" {
			return nil, fmt.Errorf("%s:%d: %w: a label with nobody's name on it, and an unattributed label cannot be checked against a second one", path, n, ErrBadLabels)
		}
		out = append(out, l)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %w: it holds no labels", path, ErrBadLabels)
	}
	return out, nil
}
