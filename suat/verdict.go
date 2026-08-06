package suat

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// A Call is what the crawl should do next.
type Call string

// The calls, which are the only three answers this package gives.
const (
	// Continue is the crawl doing what it was planned to do.
	Continue Call = "continue"

	// Slow is the operational response to objections: make the Vietnamese
	// contact page more prominent, write to the largest objecting operators,
	// and resume at half rate. It is a response rather than a scope decision,
	// which is why it is not Stop.
	Slow Call = "slow"

	// Stop is the kill criterion firing. gao-crawl contributes around 9B
	// instead of 60B, the corpus lands near 250B, and the spec's second
	// falsifier has fired.
	Stop Call = "stop"
)

// A Verdict is the call and the sentence that justifies it.
type Verdict struct {
	Call Call   `json:"call"`
	Why  string `json:"why"`

	// Yield and Objection are the two numbers the call was made from, carried
	// so that a verdict pasted into a message still holds its evidence.
	Yield     float64 `json:"yield"`
	Objection float64 `json:"objection"`

	// Settled says whether enough of the crawl is behind it for the kill
	// criterion to mean anything. A yield under the kill line before then is
	// reported and not acted on.
	Settled bool `json:"settled"`
}

// Read is what the run says to do.
//
// The order matters. Objections come first, because an operator asking us to
// stop is a thing to answer today and a yield that is merely disappointing is
// not. The kill criterion comes second and only once the crawl has settled,
// since yield in the first tens of millions of fetches measures the seed list
// rather than the web, and a crawl stopped for being young is a crawl stopped
// for no reason.
func (r *Run) Read() Verdict {
	p, ok := r.Latest()
	if !ok {
		return Verdict{Call: Continue, Why: "nothing has been measured yet, so there is nothing to act on"}
	}
	t := p.Total()
	v := Verdict{Yield: t.Yield(), Objection: t.Objection(), Settled: p.At >= Settled}

	if t.Objection() > Objections {
		v.Call = Slow
		v.Why = fmt.Sprintf(
			"%.2f%% of crawled hosts have objected against a ceiling of %.0f%%, so make the Vietnamese contact page more prominent, write to the largest objecting operators, and resume at half rate",
			100*t.Objection(), 100*Objections)
		return v
	}
	if t.Yield() < Kill {
		if !v.Settled {
			v.Call = Continue
			v.Why = fmt.Sprintf(
				"net yield is %.3f and under the kill line, and %s are behind the crawl against the %s the criterion waits for, so this is the seed list being measured rather than the web",
				t.Yield(), count(p.At), count(Settled))
			return v
		}
		v.Call = Stop
		v.Why = fmt.Sprintf(
			"net yield is %.3f after %s, below the kill line of %.2f, so gao-crawl contributes around 9B rather than 60B and the corpus lands near 250B",
			t.Yield(), count(p.At), Kill)
		return v
	}
	v.Call = Continue
	switch {
	case t.Yield() >= Target:
		v.Why = fmt.Sprintf("net yield is %.3f against a plan of %.2f", t.Yield(), Target)
	default:
		v.Why = fmt.Sprintf(
			"net yield is %.3f, under the plan of %.2f and above the kill line of %.2f, which is the band where the budget moves between classes rather than the crawl stopping",
			t.Yield(), Target, Kill)
	}
	return v
}

// Holding reports whether P03-5 is holding: forums contributing more tokens
// than news archives. It returns the two token counts alongside, because the
// prediction is only interesting with the margin attached.
func (p Point) Holding() (holding bool, forum, news int64) {
	forum, news = p.By[Forum].Tokens, p.By[News].Tokens
	return forum > news, forum, news
}

// Curve is the yield at each checkpoint, which is the thing a single number
// cannot be. A run with one point returns one value and is not a curve, and
// callers are expected to say so rather than draw a line through it.
func (r *Run) Curve() []float64 {
	out := make([]float64, 0, len(r.Points))
	for _, p := range r.Points {
		out = append(out, p.Yield())
	}
	return out
}

// Trend is the change in yield across the last two checkpoints, measured on the
// window rather than on the cumulative number, and false if there is nothing to
// compare against. Cumulative yield over hundreds of millions of fetches is
// almost immovable, so a crawl watched on it alone is a crawl nobody is
// watching.
func (r *Run) Trend() (float64, bool) {
	if len(r.Points) < 3 {
		return 0, false
	}
	last := r.Points[len(r.Points)-1].Total()
	mid := r.Points[len(r.Points)-2].Total()
	first := r.Points[len(r.Points)-3].Total()
	return last.Sub(mid).Yield() - mid.Sub(first).Yield(), true
}

// Classified is the share of fetches the classifier placed into one of the five
// target classes. A large Other is a fact about the classifier and it belongs in
// the report rather than out of it.
func (p Point) Classified() float64 {
	t := p.Total()
	if t.Fetches <= 0 {
		return 0
	}
	return float64(t.Fetches-p.By[Other].Fetches) / float64(t.Fetches)
}

// Ranked is the classes in the order they produced tokens, which is how the
// budget conversation actually goes.
func (p Point) Ranked() []Class {
	out := slices.Clone(Classes)
	slices.SortStableFunc(out, func(a, b Class) int {
		switch {
		case p.By[a].Tokens > p.By[b].Tokens:
			return -1
		case p.By[a].Tokens < p.By[b].Tokens:
			return 1
		default:
			return strings.Compare(string(a), string(b))
		}
	})
	return out
}

// ReadRun loads a run from a file of one JSON point per line, which is what a
// crawl appends to while it runs. A file rather than a database, because the
// thing writing it is a crawler on a box with 111 GB free and the thing reading
// it is somebody at four in the morning.
func ReadRun(crawl, path string) (*Run, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("suat: %w", err)
	}
	r := &Run{Crawl: crawl}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var p Point
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("suat: %s line %d: %w", path, i+1, err)
		}
		r.Points = append(r.Points, p)
	}
	if len(r.Points) == 0 {
		return nil, fmt.Errorf("suat: %s holds no measurements", path)
	}
	return r, nil
}

// count prints a fetch count the way anybody discussing this crawl says it out
// loud, since the numbers here run to hundreds of millions.
func count(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB fetches", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.0fM fetches", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk fetches", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d fetches", n)
	}
}
