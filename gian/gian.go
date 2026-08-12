// Package gian is giãn, to stretch: the context extension ladder, and whether
// the corpus holds enough naturally long Vietnamese to climb it.
//
// A long context is trained in stages rather than from the start, because
// attention is quadratic and the first two thirds of a run does not need the
// last rung's window. The stages are in the curriculum already. What is not in
// the curriculum is the question of whether the data exists to fill them, and
// that question has a bad answer almost everywhere: long context extension is
// usually done on concatenated short documents, which teaches a model to handle
// positions and not to carry a dependency across them. A model trained that way
// passes a synthetic retrieval test at 131072 and cannot answer a question about
// a statute.
//
// So the rung is one half of what this package holds and the corpus is the
// other. A document teaches the window above it only if it is naturally longer
// than the window below it, the pool of such documents is finite, and the number
// of passes over that pool is what decides whether the last stage is training or
// memorization. Those are countable off the parts, and countable without reading
// them: a length distribution is a question about two columns.
package gian

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/tamnd/gao/kho"
	"github.com/tamnd/gao/nau"
)

const (
	// MaxPasses is how many times the run may read the same documents at a rung
	// before the rung is memorization rather than training. It is 4 because
	// P08-4 is the prediction that four epochs on curated Vietnamese beats one
	// at matched compute, and a number above the one the project is prepared to
	// defend is a number nobody defended.
	MaxPasses = 4.0

	// MaxSourceShare is how much of a rung's pool may come from one source. A
	// pool that is nine tenths consolidated legal codes trains the shape of a
	// legal code at length, which reads as a long context result until somebody
	// asks a question about a thesis.
	MaxSourceShare = 0.60

	// MinReach is how far into the window the pool's documents have to reach, as
	// a share of the window. Documents averaging a third of the window leave
	// everything past that position trained by packing, and packing is the thing
	// this ladder exists to avoid.
	MinReach = 0.50
)

// A Rung is one stage of the extension: the window it trains at, how the
// positions get there, and what the data at that stage has to be.
type Rung struct {
	Stage int `json:"stage"`
	// Name is the curriculum phase this rung is, since the ladder and the
	// curriculum are the same three stages seen from two sides.
	Name string `json:"name"`
	// Window is the sequence length trained at.
	Window int `json:"window"`
	// Floor is the natural length a document needs to teach anything at this
	// window, which is the window below it. A document shorter than that was
	// already covered by the previous rung.
	Floor int `json:"floor"`
	// Tokens is the token instances the curriculum spends at this rung.
	Tokens int64 `json:"tokens"`
	// Demand is the part of that spent on the slices whose documents are the
	// long ones, which is what the pool actually has to supply.
	Demand int64 `json:"demand"`

	Method string `json:"method"`
	Data   string `json:"data"`
	Eval   string `json:"eval"`
}

// step is what a curriculum phase becomes when it is read as a rung. It is
// keyed by phase name so that a phase added to the curriculum without one is a
// refusal here rather than a rung quietly missing from the ladder.
type step struct{ method, data, eval string }

var steps = map[string]step{
	"bulk": {
		"native",
		"the full mixture, packed to the window",
		"nothing, since there is no extension yet to have failed",
	},
	"ramp": {
		"RoPE base increase, then continued training",
		"long documents upweighted 6x, not short ones concatenated",
		"vi-needle at 32768",
	},
	"anneal": {
		"YaRN, then a short finetune at the window",
		"naturally long Vietnamese only, and concatenated shorts for nothing",
		"vi-needle and vi-longdoc-qa at 131072",
	},
}

// long is which mixture components hold the naturally long Vietnamese. Doc 08
// §7 names them: theses in gao-pdf run to forty and a hundred pages, and
// statutes and consolidated codes in gao-legal run to hundreds of thousands of
// characters. Nothing else in the mixture is long by nature.
//
// The curriculum gives gao-legal and gao-voice one line between them, and voice
// transcripts are not long, so the demand this produces is a little generous.
// Generous is the safe direction here, since the failure being checked for is a
// pool too small to supply it.
var long = map[string]bool{
	"gao-pdf":             true,
	"gao-legal+gao-voice": true,
}

// Ladder is the extension read off the curriculum, in order.
func Ladder() []Rung {
	phases := nau.Curriculum()
	out := make([]Rung, 0, len(phases))
	floor := 0
	for i, p := range phases {
		s := steps[p.Name]
		var demand float64
		for _, slot := range p.Mix {
			if long[slot.Component] {
				demand += float64(p.Tokens()) * slot.Percent / 100
			}
		}
		out = append(out, Rung{
			Stage:  i + 1,
			Name:   p.Name,
			Window: p.Sequence,
			Floor:  floor,
			Tokens: p.Tokens(),
			Demand: int64(demand + 0.5),
			Method: s.method,
			Data:   s.data,
			Eval:   s.eval,
		})
		floor = p.Sequence
	}
	return out
}

// Top is the window the last rung trains at, which is the longest sequence any
// of this is about.
func Top() int {
	l := Ladder()
	if len(l) == 0 {
		return 0
	}
	return l[len(l)-1].Window
}

// CheckLadder is everything about the ladder itself that cannot be what it says
// it is, before any corpus is measured against it.
func CheckLadder() []string {
	var why []string
	l := Ladder()
	if len(l) == 0 {
		return []string{"the curriculum has no phases in it, so there is no ladder to climb"}
	}
	for i, r := range l {
		if _, ok := steps[r.Name]; !ok {
			why = append(why, fmt.Sprintf("the curriculum has a phase called %s and nothing here says how its context gets extended, so the ladder is missing a rung rather than having one nobody wrote down", r.Name))
		}
		if r.Window <= 0 {
			why = append(why, fmt.Sprintf("stage %d trains at a sequence length of %d", r.Stage, r.Window))
		}
		if i > 0 && r.Window <= l[i-1].Window {
			why = append(why, fmt.Sprintf("stage %d trains at %d after stage %d trained at %d, and a ladder that does not go up is three runs rather than an extension", r.Stage, r.Window, l[i-1].Stage, l[i-1].Window))
		}
		if r.Tokens <= 0 {
			why = append(why, fmt.Sprintf("stage %d spends %d tokens, so there is nothing to fill", r.Stage, r.Tokens))
		}
	}
	return why
}

// A Band is the documents that are long enough to teach one rung, and where
// they came from.
type Band struct {
	Rung      Rung   `json:"rung"`
	Documents int64  `json:"documents"`
	Tokens    int64  `json:"tokens"`
	Sources   []Part `json:"sources,omitempty"`
}

// A Part is one source's share of a band.
type Part struct {
	Source    string  `json:"source"`
	Documents int64   `json:"documents"`
	Tokens    int64   `json:"tokens"`
	Share     float64 `json:"share"`
}

// Mean is the average length of a document in the band, which is how far into
// the window the band actually reaches.
func (b Band) Mean() float64 {
	if b.Documents == 0 {
		return 0
	}
	return float64(b.Tokens) / float64(b.Documents)
}

// Reach is the mean length as a share of the window the band is being read
// against.
func (b Band) Reach() float64 {
	if b.Rung.Window == 0 {
		return 0
	}
	return b.Mean() / float64(b.Rung.Window)
}

// Passes is how many times the run would read this band to supply what the rung
// demands of it. It is the number that decides whether the stage is training on
// long documents or memorizing a few thousand of them.
func (b Band) Passes() float64 {
	if b.Tokens == 0 {
		return 0
	}
	return float64(b.Rung.Demand) / float64(b.Tokens)
}

// Covers says whether the band can supply the rung inside the pass ceiling.
func (b Band) Covers() bool { return b.Tokens > 0 && b.Passes() <= MaxPasses }

// Largest is the source the band leans on hardest.
func (b Band) Largest() Part {
	if len(b.Sources) == 0 {
		return Part{}
	}
	return b.Sources[0]
}

// A Pool is a body of parts measured against the ladder.
type Pool struct {
	// Name is what the measurement is of, which is a snapshot or a slice rather
	// than a list of files.
	Name string `json:"name"`
	// Parts is how many files were read and Read is what reading them cost, so
	// the claim that a length distribution does not cost the corpus is a
	// measurement rather than an assertion.
	Parts int   `json:"parts"`
	Read  int64 `json:"read"`
	Bytes int64 `json:"bytes"`

	Documents int64 `json:"documents"`
	Tokens    int64 `json:"tokens"`
	// Untokenized is documents carrying no token count. They are refused rather
	// than estimated, because a length in characters cannot say which side of a
	// window a document falls on.
	Untokenized int64 `json:"untokenized"`
	// Longest is the longest single document, and Over is how many are longer
	// than the top window.
	Longest uint32 `json:"longest"`
	Over    int64  `json:"over_top"`

	bands []counter
}

// counter accumulates one band while the parts are being read.
type counter struct {
	documents int64
	tokens    int64
	sources   map[string]Part
}

// Measure reads the length columns of the given parts and folds them into the
// ladder's bands.
func Measure(name string, paths []string) (Pool, error) {
	rungs := Ladder()
	p := Pool{Name: name, Parts: len(paths), bands: make([]counter, len(rungs))}
	for i := range p.bands {
		p.bands[i].sources = make(map[string]Part)
	}
	top := Top()

	for _, path := range paths {
		read, err := kho.ScanLengths(path, func(l kho.Length) error {
			p.add(rungs, top, l)
			return nil
		})
		p.Read += read
		if err != nil {
			return Pool{}, err
		}
		p.Bytes += sizeOf(path)
	}
	return p, nil
}

// add folds one document's length into every band it is long enough for.
func (p *Pool) add(rungs []Rung, top int, l kho.Length) {
	p.Documents++
	if l.NTokens == 0 {
		p.Untokenized++
		return
	}
	p.Tokens += int64(l.NTokens)
	if l.NTokens > p.Longest {
		p.Longest = l.NTokens
	}
	if top > 0 && int(l.NTokens) >= top {
		p.Over++
	}
	for i, r := range rungs {
		if int(l.NTokens) < r.Floor {
			continue
		}
		b := &p.bands[i]
		b.documents++
		b.tokens += int64(l.NTokens)
		s := b.sources[l.Source]
		s.Source = l.Source
		s.Documents++
		s.Tokens += int64(l.NTokens)
		b.sources[l.Source] = s
	}
}

// Bands is what the pool holds at each rung, in the ladder's order.
func (p Pool) Bands() []Band {
	rungs := Ladder()
	out := make([]Band, 0, len(rungs))
	for i, r := range rungs {
		b := Band{Rung: r}
		if i < len(p.bands) {
			c := p.bands[i]
			b.Documents, b.Tokens = c.documents, c.tokens
			for _, s := range slices.Collect(maps.Values(c.sources)) {
				if c.tokens > 0 {
					s.Share = float64(s.Tokens) / float64(c.tokens)
				}
				b.Sources = append(b.Sources, s)
			}
			sort.Slice(b.Sources, func(x, y int) bool {
				if b.Sources[x].Tokens != b.Sources[y].Tokens {
					return b.Sources[x].Tokens > b.Sources[y].Tokens
				}
				return b.Sources[x].Source < b.Sources[y].Source
			})
		}
		out = append(out, b)
	}
	return out
}

// Extending is the rungs that actually extend something, which is every rung
// with a floor under it. The first stage trains at the length the model was
// built for, so there is no pool question about it.
func (p Pool) Extending() []Band {
	var out []Band
	for _, b := range p.Bands() {
		if b.Rung.Floor > 0 {
			out = append(out, b)
		}
	}
	return out
}

// Blocking is everything that stops this from being a length distribution at
// all. Nothing below it is read when there is anything here.
func (p Pool) Blocking() []string {
	why := CheckLadder()
	switch {
	case p.Parts == 0:
		why = append(why, "no parts were read, and a pool with no files under it is an opinion about the corpus")
	case p.Documents == 0:
		why = append(why, "the parts read hold no documents between them")
	}
	if p.Untokenized > 0 {
		why = append(why, fmt.Sprintf("%d of %d documents carry no token count, and a length in characters cannot say which side of a %d token window a document falls on, so this is a run of dem away from being measurable rather than something to estimate", p.Untokenized, p.Documents, Top()))
	}
	return why
}

// Faults are readings a pool holds that the ladder cannot be climbed with. They
// are about the corpus rather than about the file.
func (p Pool) Faults() []string {
	if len(p.Blocking()) > 0 {
		return nil
	}
	var out []string
	for _, b := range p.Extending() {
		switch {
		case b.Tokens == 0:
			out = append(out, fmt.Sprintf("nothing in the corpus is longer than %d tokens, so the %d window at stage %d would be trained entirely on concatenated shorter documents, which teaches positions and not what is across them",
				b.Rung.Floor, b.Rung.Window, b.Rung.Stage))
		case !b.Covers():
			out = append(out, fmt.Sprintf("the pool for the %d window holds %s tokens against a stage that asks its long slices for %s, so supplying it takes %.1f passes over the same %s documents against a ceiling of %.0f",
				b.Rung.Window, tokens(b.Tokens), tokens(b.Rung.Demand), b.Passes(), thousands(b.Documents), MaxPasses))
		}
	}
	for _, b := range p.Extending() {
		if b.Documents == 0 {
			continue
		}
		if b.Reach() < MinReach {
			out = append(out, fmt.Sprintf("the documents in the %d pool average %s tokens, which is %.0f%% of the window, so every position past that is trained by packing rather than by a document that long",
				b.Rung.Window, thousands(int64(b.Mean())), b.Reach()*100))
		}
		if s := b.Largest(); s.Share > MaxSourceShare {
			out = append(out, fmt.Sprintf("%.0f%% of the %d pool is %s, so what stage %d teaches at length is the shape of one source",
				s.Share*100, b.Rung.Window, s.Source, b.Rung.Stage))
		}
	}
	return out
}

// Holds says whether the corpus as measured can carry the ladder as written.
func (p Pool) Holds() bool { return len(p.Blocking()) == 0 && len(p.Faults()) == 0 }

// Verdict is the sentence that goes in the release notes.
func (p Pool) Verdict() string {
	if why := p.Blocking(); len(why) > 0 {
		return "This is not a length distribution: " + why[0] + "."
	}
	head := fmt.Sprintf("%s holds %s documents and %s tokens, read out of %s of Parquet.",
		p.Name, thousands(p.Documents), tokens(p.Tokens), gigabytes(p.Bytes))
	if faults := p.Faults(); len(faults) > 0 {
		if len(faults) == 1 {
			return fmt.Sprintf("%s One reading says the ladder cannot be climbed as written: %s.", head, faults[0])
		}
		return fmt.Sprintf("%s %d readings say the ladder cannot be climbed as written, the first of which is that %s.", head, len(faults), faults[0])
	}
	top := p.Extending()
	last := top[len(top)-1]
	return fmt.Sprintf("%s The %d pool holds %s tokens in %s documents averaging %s, which supplies the last stage in %.1f passes, so the top rung is trained on documents that are that long rather than on shorts packed to look like them.",
		head, last.Rung.Window, tokens(last.Tokens), thousands(last.Documents), thousands(int64(last.Mean())), last.Passes())
}
