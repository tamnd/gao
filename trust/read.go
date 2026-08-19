package trust

// Reading a set of paired scores into an answer about the instrument.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
)

// A Score is what the study says about the proxy.
//
// Nothing in it is a judgment about any recipe. Every number here is about
// whether the cheap benchmark can be used to decide between recipes at all, and
// the recipes are the instrument's test set rather than its subject.
type Score struct {
	Proxy  string `json:"proxy"`
	Anchor string `json:"anchor"`

	// Pairs is every row read, Recipes is how many distinct recipes those came
	// to once the baseline repeats collapsed into one, and Baselines is how many
	// of those repeats there were.
	Pairs     int `json:"pairs"`
	Recipes   int `json:"recipes"`
	Baselines int `json:"baselines"`

	// The floor, per scale, taken as the spread across the baseline repeats.
	NoiseProxy  float64 `json:"noise_proxy"`
	NoiseAnchor float64 `json:"noise_anchor"`

	// Spearman is the rank correlation between the two orderings, tie corrected.
	Spearman float64 `json:"spearman"`

	// The pairwise question, which is the one a decision is actually made with.
	// Compared is how many recipe pairs were far enough apart at the anchor to
	// be a real comparison, Agreed is how many of those the proxy ordered the
	// same way, and TooClose is how many were dropped for sitting inside the
	// noise floor.
	Compared  int     `json:"compared"`
	Agreed    int     `json:"agreed"`
	TooClose  int     `json:"too_close"`
	Agreement float64 `json:"agreement"`

	// Flat is how many recipe pairs the proxy scored identically, which is a
	// proxy declining to answer rather than a proxy answering wrong.
	Flat int `json:"flat"`

	// Missed is every comparison the proxy called backwards, widest anchor gap
	// first, because those are the ones worth reading.
	Missed []Miss `json:"missed"`

	// Believable is the P10-1 bar and Exploratory is the kill criterion. They
	// are separate fields rather than one verdict because the state between them
	// is a real state and collapsing it loses the only honest report of it.
	Believable  bool `json:"believable"`
	Exploratory bool `json:"exploratory"`

	// The disagreements, each one a list of run IDs rather than a count, since a
	// count of problems is a problem nobody can go and look at.
	Repeat    []string `json:"repeat"`
	Nowhere   []string `json:"nowhere"`
	Elsewhere []string `json:"elsewhere"`
}

// a recipe is one row of the ranking, after the baseline repeats have been
// folded into the single recipe they are.
type recipe struct {
	id            string
	proxy, anchor float64
}

// Read turns paired scores into an answer about the proxy.
//
// It refuses nothing. Every row that cannot be used comes back named in one of
// the disagreement lists and the numbers are computed from what is left, because
// a reader that stops at the first bad row is a reader somebody runs once and
// then works around.
func Read(pairs []Pair) Score {
	sc := Score{Proxy: Proxy, Anchor: Anchor, Pairs: len(pairs)}

	// The slate of record is whichever digest the most rows carry. Anything else
	// was produced under a different slate and is not part of this study, since
	// a recipe that moved between the two runs is not one recipe.
	under := map[string]int{}
	for _, p := range pairs {
		under[p.Slate.String()]++
	}
	slate := ""
	for d, n := range under {
		if n > under[slate] || (n == under[slate] && d < slate) {
			slate = d
		}
	}

	seen := map[string]bool{}
	var baselines []Pair
	var rows []recipe
	for _, p := range pairs {
		switch {
		case seen[p.Run]:
			sc.Repeat = append(sc.Repeat, p.Run)
			continue
		case len(under) > 1 && p.Slate.String() != slate:
			sc.Elsewhere = append(sc.Elsewhere, p.Run)
			continue
		}
		seen[p.Run] = true
		if p.ProxyBox == "" || p.AnchorBox == "" {
			sc.Nowhere = append(sc.Nowhere, p.Run)
		}
		if p.Baseline {
			baselines = append(baselines, p)
			continue
		}
		rows = append(rows, recipe{id: p.Run, proxy: p.Proxy, anchor: p.Anchor})
	}

	sc.Baselines = len(baselines)
	if len(baselines) > 0 {
		proxies := make([]float64, 0, len(baselines))
		anchors := make([]float64, 0, len(baselines))
		for _, b := range baselines {
			proxies = append(proxies, b.Proxy)
			anchors = append(anchors, b.Anchor)
		}
		sc.NoiseProxy = spread(proxies)
		sc.NoiseAnchor = spread(anchors)
		// The repeats are one recipe. Three copies of it in the ranking would
		// be three nearly identical points pulling the correlation toward
		// whatever they happen to say.
		rows = append(rows, recipe{id: baselines[0].Run, proxy: mean(proxies), anchor: mean(anchors)})
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	sc.Recipes = len(rows)
	sc.Spearman = spearman(rows)
	sc.pairwise(rows)

	sc.Believable = sc.Recipes >= MinRecipes && sc.Baselines >= MinBaselines &&
		sc.Spearman >= Believable && sc.Agreement >= Agree
	sc.Exploratory = sc.Recipes < MinRecipes || sc.Baselines < MinBaselines || sc.Spearman < Kill
	return sc
}

// pairwise walks every pair of recipes once and asks the question a decision is
// actually made with: does the proxy put these two in the same order the anchor
// does.
func (sc *Score) pairwise(rows []recipe) {
	for i := range rows {
		for j := i + 1; j < len(rows); j++ {
			a, b := rows[i], rows[j]
			gap := math.Abs(a.anchor - b.anchor)
			if gap <= sc.NoiseAnchor {
				// Two runs of one recipe differ by this much for no reason, so
				// the anchor has not said which of these is better and a proxy
				// cannot be right or wrong about it.
				sc.TooClose++
				continue
			}
			sc.Compared++
			pgap := a.proxy - b.proxy
			if pgap == 0 {
				sc.Flat++
				continue
			}
			if (a.anchor > b.anchor) == (pgap > 0) {
				sc.Agreed++
				continue
			}
			better, worse := a.id, b.id
			if b.anchor > a.anchor {
				better, worse = b.id, a.id
			}
			sc.Missed = append(sc.Missed, Miss{
				Better: better, Worse: worse,
				AnchorGap: gap, ProxyGap: math.Abs(pgap),
			})
		}
	}
	if sc.Compared > 0 {
		sc.Agreement = float64(sc.Agreed) / float64(sc.Compared)
	}
	slices.SortFunc(sc.Missed, func(a, b Miss) int {
		if a.AnchorGap != b.AnchorGap {
			return cmpDesc(a.AnchorGap, b.AnchorGap)
		}
		return strings.Compare(a.Better, b.Better)
	})
}

// Publishable is every reason this study may not go out as a validation.
//
// Passing the bar and being publishable are different questions. A study can
// clear 0.7 and still be a study of four recipes measured on nobody's machine,
// and the number that comes out of it would be quoted for the life of the
// project.
func (sc Score) Publishable() []string {
	var out []string
	if sc.Pairs == 0 {
		return []string{"nothing was scored at both scales, so there is no study here"}
	}
	if sc.Baselines < MinBaselines {
		out = append(out, fmt.Sprintf("the baseline recipe was run %s and the floor needs %d, so every comparison below was read against a spread taken from too little to be a spread",
			plural(sc.Baselines, "time"), MinBaselines))
	}
	if sc.Recipes < MinRecipes {
		out = append(out, fmt.Sprintf("%s were scored both ways and the floor is %d, and a rank correlation over that few recipes lands where it lands by accident",
			plural(sc.Recipes, "recipe"), MinRecipes))
	}
	if sc.NoiseAnchor == 0 && sc.Baselines > 0 {
		out = append(out, "the baseline repeats scored identically at the anchor, which is either a benchmark with no resolution or a set of runs that were not actually reseeded, and both of them make every comparison below look decisive")
	}
	if sc.Spearman < 0 {
		out = append(out, fmt.Sprintf("the correlation is %.2f, which is the proxy ordering recipes backwards rather than badly, and a proxy pointing the wrong way is worse to publish than none at all",
			sc.Spearman))
	}
	if sc.Compared > 0 && sc.TooClose > sc.Compared {
		out = append(out, fmt.Sprintf("%d of the %d recipe comparisons sat inside the noise floor and only %d were real, so this is mostly a study of how little the anchor separates these recipes",
			sc.TooClose, sc.TooClose+sc.Compared, sc.Compared))
	}
	if sc.Compared > 0 && sc.Flat*4 > sc.Compared {
		out = append(out, fmt.Sprintf("the proxy scored %d of %d comparisons identically, which is the proxy declining to answer rather than answering wrong, and counting those anywhere but here would flatter it",
			sc.Flat, sc.Compared))
	}
	if len(sc.Nowhere) > 0 {
		out = append(out, fmt.Sprintf("%s carry no box at one scale or the other: %s. A result with no machine on it cannot be reproduced and cannot be ruled out as a locale difference",
			plural(len(sc.Nowhere), "run"), join(sc.Nowhere)))
	}
	if len(sc.Repeat) > 0 {
		out = append(out, fmt.Sprintf("%s appear twice: %s. The first was kept, and a run scored twice is a run somebody re-ran after seeing the first number",
			plural(len(sc.Repeat), "run"), join(sc.Repeat)))
	}
	if len(sc.Elsewhere) > 0 {
		out = append(out, fmt.Sprintf("%s were produced under a different slate: %s. A recipe that moved between the two scales is not one recipe and the comparison is not a comparison",
			plural(len(sc.Elsewhere), "run"), join(sc.Elsewhere)))
	}
	return out
}

// Verdict is the answer in one sentence, including the answer nobody wants.
func (sc Score) Verdict() string {
	switch {
	case sc.Believable:
		return fmt.Sprintf("%s can be believed about %s: rank correlation %.2f against a bar of %.2f, and it orders %.0f%% of the real comparisons the same way",
			Proxy, Anchor, sc.Spearman, Believable, 100*sc.Agreement)
	case sc.Exploratory:
		return fmt.Sprintf("%s cannot be believed about %s: rank correlation %.2f against a kill criterion of %.2f. The slate is reported as exploratory, every threshold falls back to a published default, and each one goes into the release notes flagged as unvalidated",
			Proxy, Anchor, sc.Spearman, Kill)
	default:
		return fmt.Sprintf("%s is neither validated nor dead against %s: rank correlation %.2f sits between the kill criterion of %.2f and the bar of %.2f, so the slate's findings go out with the caveat attached rather than without it or not at all",
			Proxy, Anchor, sc.Spearman, Kill, Believable)
	}
}

// spearman is the rank correlation between the two orderings, with tied scores
// sharing the average of the ranks they cover.
//
// Ties are handled rather than broken because a proxy that cannot separate two
// recipes has said something, and breaking the tie by input order invents an
// answer it did not give.
func spearman(rows []recipe) float64 {
	if len(rows) < 2 {
		return 0
	}
	proxies := make([]float64, len(rows))
	anchors := make([]float64, len(rows))
	for i, r := range rows {
		proxies[i] = r.proxy
		anchors[i] = r.anchor
	}
	return pearson(ranks(proxies), ranks(anchors))
}

// ranks returns the average rank of every value, one indexed.
func ranks(vs []float64) []float64 {
	order := make([]int, len(vs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return vs[order[a]] < vs[order[b]] })

	out := make([]float64, len(vs))
	for i := 0; i < len(order); {
		j := i
		for j+1 < len(order) && vs[order[j+1]] == vs[order[i]] {
			j++
		}
		r := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			out[order[k]] = r
		}
		i = j + 1
	}
	return out
}

// pearson is the correlation of two equally long series.
func pearson(a, b []float64) float64 {
	ma, mb := mean(a), mean(b)
	var num, da, db float64
	for i := range a {
		x, y := a[i]-ma, b[i]-mb
		num += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		// One of the two orderings is flat, so there is no ordering to agree
		// with. Zero is the honest answer and it fails every bar above.
		return 0
	}
	return num / math.Sqrt(da*db)
}

// spread is the range across repeats of one recipe, which is what two runs of
// the same thing differ by. It is the range rather than a standard deviation
// because three points do not have one worth quoting.
func spread(vs []float64) float64 {
	if len(vs) < 2 {
		return 0
	}
	return slices.Max(vs) - slices.Min(vs)
}

func mean(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vs {
		sum += v
	}
	return sum / float64(len(vs))
}

func cmpDesc(a, b float64) int {
	if a > b {
		return -1
	}
	return 1
}

// join names the runs, and stops naming them once the list is longer than
// somebody would read.
func join(ids []string) string {
	out := slices.Clone(ids)
	sort.Strings(out)
	if len(out) <= 5 {
		return strings.Join(out, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(out[:5], ", "), len(out)-5)
}

// ReadPairs reads the paired scores, one JSON object per line.
//
// One per line rather than one array because these arrive a run at a time over
// weeks, and appending a line to a file is something a run can do on its way out
// while rewriting an array is not.
func ReadPairs(path string) ([]Pair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Pair
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		var p Pair
		dec := json.NewDecoder(strings.NewReader(text))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if p.Run == "" {
			return nil, fmt.Errorf("line %d: a score with no run on it cannot be matched to a recipe", line)
		}
		out = append(out, p)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNoPairs
	}
	return out, nil
}
